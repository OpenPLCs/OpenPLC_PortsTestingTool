package ptpanel

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"PortTool/src/ptboard"
	"PortTool/src/ptrecord"
	"PortTool/src/ptreport"
	"PortTool/src/ptstages"
	"PortTool/src/simboard"
	"PortTool/src/stlink"
)

// The fixture line (decision 190, PORTTOOL-FLOW.md C.3.10 「大屏怎么实现」):
// each station runs a loop that finds the board, prepares it and walks it
// through the five steps, driving the station's own panel through its API.

// Tile states.
const (
	lnEmpty     = "empty"     // no board seen
	lnNeedFlash = "needflash" // board without the current fixture firmware; waits for a click
	lnFlashing  = "flashing"
	lnWire      = "wire"    // connected, waiting for adapters
	lnRun       = "run"     // a step is running
	lnFail      = "fail"    // a step did not pass; LED3 on, waits for continue / restart
	lnBlocked   = "blocked" // the next step cannot run yet (aging mode not built)
	lnDone      = "done"    // all five passed
)

// lineSteps are the five steps; index+1 is the number shown (①..⑤).
var lineSteps = []struct {
	ID    string
	Stage ptstages.Step // 0: not a panel step
}{
	{"functional", ptstages.Functional},
	{"aging", 0},
	{"aging_check", 0},
	{"safety", 0},
	{"user", ptstages.UserLevel},
}

// lineTile is one station as the big screen shows it.
type lineTile struct {
	N       int            `json:"n"`
	State   string         `json:"state"`
	Paused  bool           `json:"paused"`
	Step    int            `json:"step"` // 1..5 for a step, 0 during prep
	UID     string         `json:"uid"`
	FW      string         `json:"fw"`   // fixture firmware on the board; "" none, "blank" empty chip
	Want    string         `json:"want"` // the fixture version this PortTool ships; "" none found
	Passed  map[int]bool   `json:"passed"`
	Results map[int]string `json:"results"` // step -> outcome of its last run
	Missing []string       `json:"missing,omitempty"`
	Why     msg            `json:"why"`
	Since   time.Time      `json:"since"`   // when the board arrived
	StepAt  time.Time      `json:"step_at"` // when the current state began
	Port    string         `json:"port"`    // control port
	Sim     bool           `json:"sim"`
	Records map[int]string `json:"records,omitempty"` // step -> report file
}

type lineStation struct {
	n   int
	srv *Server
	h   http.Handler

	mu      sync.Mutex
	tile    lineTile
	control string // the station's control port; "sim" for the simulated board
	act     chan string
	stop    chan struct{}
	lastSWD time.Time
}

// api calls the station's own panel in-process.
func (x *lineStation) api(method, path string, body any) map[string]any {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader([]byte("{}"))
	}
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func (x *lineStation) set(f func(t *lineTile)) {
	x.mu.Lock()
	f(&x.tile)
	x.mu.Unlock()
}

func (x *lineStation) view() lineTile {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.tile
}

func (x *lineStation) enter(state string, why msg) {
	x.set(func(t *lineTile) { t.State, t.Why, t.StepAt = state, why, time.Now() })
}

// fixtureImage is the fixture image this PortTool ships and its version: the
// delivery's openplc_fixture_<ver>.hex beside the exe, else Output/fixture/
// above a development build.
var fixtureImage = func() (path, version string) {
	dir := deliveryDir()
	if hits, _ := filepath.Glob(filepath.Join(dir, "openplc_fixture_*.hex")); len(hits) == 1 {
		v := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(hits[0]), "openplc_fixture_"), ".hex")
		return hits[0], v
	}
	for d := dir; ; d = filepath.Dir(d) {
		fx := filepath.Join(d, "Output", "fixture")
		if v, err := os.ReadFile(filepath.Join(fx, "VERSION")); err == nil {
			return filepath.Join(fx, "OpenPLC_BootLoader.hex"), strings.TrimSpace(string(v))
		}
		if filepath.Dir(d) == d {
			return "", ""
		}
	}
}

var idRe = regexp.MustCompile(`uid=([0-9A-Fa-f]{24}).*porttool=(\S+)`)

func (x *lineStation) askID(port string) (uid, fw string, ok bool) {
	rw, err := x.srv.Open(port, 0)
	if err != nil {
		return "", "", false
	}
	defer rw.Close()
	b := ptboard.New(rw, 64)
	lines, err := b.Send("pt.id", ptboard.ExpectFor("pt.id"), 1500*time.Millisecond)
	if err != nil {
		return "", "", false
	}
	for _, l := range lines {
		if mm := idRe.FindStringSubmatch(l); mm != nil {
			return strings.ToUpper(mm[1]), mm[2], true
		}
	}
	return "", "", false
}

// run is the station's loop; it returns when stop closes.
func (x *lineStation) run() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-x.stop:
			return
		case a := <-x.act:
			x.do(a)
		case <-tick.C:
			x.step()
		}
	}
}

func (x *lineStation) connected() bool {
	x.srv.mu.Lock()
	defer x.srv.mu.Unlock()
	return x.srv.board != nil && x.srv.linkErr.Key == ""
}

// step advances whatever can advance by itself.
func (x *lineStation) step() {
	t := x.view()
	if t.Paused {
		return
	}
	switch t.State {
	case lnEmpty, lnNeedFlash:
		x.detect()
	case lnWire:
		if !x.connected() {
			x.lost()
			return
		}
		x.checkWiring()
	case lnRun:
		if !x.connected() {
			x.lost()
			return
		}
		x.watchRun()
	case lnFail, lnBlocked, lnDone:
		if !x.connected() {
			x.lost()
		}
	}
}

// lost is the board going away: the tile empties, the progress stays on disk.
func (x *lineStation) lost() {
	x.api("POST", "/api/full/stop", nil)
	x.api("POST", "/api/disconnect", nil)
	x.set(func(t *lineTile) { *t = lineTile{N: t.N, State: lnEmpty, Want: t.Want, StepAt: time.Now()} })
}

// detect looks for a board on the control port, then behind the ST-Link.
func (x *lineStation) detect() {
	_, want := fixtureImage()
	x.mu.Lock()
	port := x.control
	x.mu.Unlock()
	x.set(func(t *lineTile) { t.Want, t.Port, t.Sim = want, port, simboard.IsSim(port) })
	if port == "" {
		x.set(func(t *lineTile) { t.Why = m("line.why.no_control") })
		return
	}
	var uid, fw string
	ok := false
	if simboard.IsSim(port) {
		ok, fw = true, want
	} else {
		uid, fw, ok = x.askID(port)
	}
	if ok {
		if want != "" && fw != want {
			x.set(func(t *lineTile) {
				if t.State != lnNeedFlash {
					t.Since = time.Now()
				}
				t.UID, t.FW = uid, fw
			})
			x.enter(lnNeedFlash, m("line.why.old_fw", "have", fw, "want", want))
			return
		}
		x.attach(uid, fw)
		return
	}
	// Nothing answers on the control port: an empty chip shows only on SWD.
	if time.Since(x.lastSWD) < 5*time.Second || simboard.IsSim(port) {
		return
	}
	x.lastSWD = time.Now()
	p, err := stlink.OpenSN(x.srv.STLinkSN)
	if err != nil {
		return
	}
	blank, err := p.Blank()
	if err != nil {
		if x.view().State == lnNeedFlash {
			x.set(func(t *lineTile) { *t = lineTile{N: t.N, State: lnEmpty, Want: t.Want, Port: port} })
		}
		return
	}
	fwText := ""
	if blank {
		fwText = "blank"
	}
	x.set(func(t *lineTile) {
		if t.State != lnNeedFlash {
			t.Since = time.Now()
		}
		t.UID, t.FW = "", fwText
	})
	x.enter(lnNeedFlash, m("line.why.no_fw"))
}

// attach connects the station's panel to the board and starts the wiring check.
func (x *lineStation) attach(uid, fw string) {
	x.mu.Lock()
	port := x.control
	x.mu.Unlock()
	st := x.api("POST", "/api/connect", map[string]any{"port": port})
	if st["connected"] != true {
		x.set(func(t *lineTile) { t.Why = m("line.why.connect_failed") })
		return
	}
	if fw == "" {
		x.srv.mu.Lock()
		fw = x.srv.caps.Version
		x.srv.mu.Unlock()
	}
	if uid == "" {
		if u := x.api("GET", "/api/uid", nil); u != nil {
			uid, _ = u["uid"].(string)
		}
	}
	passed, recs := map[int]bool{}, map[int]string{}
	if !simboard.IsSim(port) {
		passed, recs = loadLine(uid)
	}
	x.set(func(t *lineTile) {
		t.UID, t.FW, t.Since, t.Passed, t.Records = uid, fw, time.Now(), passed, recs
		t.Results = map[int]string{}
	})
	x.enter(lnWire, msg{})
}

// wiringNeeded is the adapters the functional test's ticked ports use.
func wiringNeeded() []string {
	c := loadStageCfgs().Func
	need := []string{}
	for _, p := range []string{"rs485", "can"} {
		if !c.PortsOff[p] {
			need = append(need, p)
		}
	}
	return need
}

func (x *lineStation) checkWiring() {
	t := x.view()
	mt, _ := x.api("GET", "/api/match", nil)["match"].(map[string]any)
	if mt == nil && !t.Sim {
		// Not run for this board yet: start it.
		x.api("POST", "/api/match", nil)
		return
	}
	if mt == nil {
		mt = map[string]any{"sim": true}
	}
	if mt["running"] == true {
		return
	}
	terms, _ := mt["terms"].(map[string]any)
	missing := []string{}
	if mt["sim"] != true {
		for _, p := range wiringNeeded() {
			if tm, _ := terms[p].(map[string]any); tm == nil || tm["ok"] != true {
				missing = append(missing, p)
			}
		}
	}
	x.set(func(t *lineTile) { t.Missing = missing })
	if len(missing) > 0 {
		return
	}
	x.next()
}

// next starts the first step not passed yet.
func (x *lineStation) next() {
	t := x.view()
	for i := 1; i <= len(lineSteps); i++ {
		if !t.Passed[i] {
			x.start(i)
			return
		}
	}
	x.set(func(t *lineTile) { t.Step = 0 })
	x.enter(lnDone, msg{})
}

func (x *lineStation) start(i int) {
	x.set(func(t *lineTile) { t.Step = i; delete(t.Results, i) })
	x.api("POST", "/api/command", map[string]any{"cmd": "pt.led fault=0"})
	switch lineSteps[i-1].ID {
	case "aging", "aging_check":
		x.enter(lnBlocked, m("line.why.aging_mode"))
		return
	case "safety":
		// Safety is not defined yet and passes by default (decision 190).
		x.finish(i, ptreport.OutcomePass, x.writeOwn(i, ptreport.OutcomePass))
		return
	}
	// A step the station cannot start (configuration, reference board) waits
	// with the panel's own reason until someone fixes it and presses continue.
	if why := x.srv.stepWhy(lineSteps[i-1].Stage); why != nil {
		x.enter(lnBlocked, *why)
		return
	}
	st := x.api("POST", "/api/full/start", map[string]any{"steps": []int{int(lineSteps[i-1].Stage)}, "resume": "restart"})
	if e, ok := st["error"].(map[string]any); ok {
		x.fail(i, ptreport.OutcomeError, msgFromJSON(e))
		return
	}
	x.enter(lnRun, msg{})
}

// writeOwn writes the record of a step the line judges itself.
func (x *lineStation) writeOwn(i int, o ptreport.Outcome) string {
	t := x.view()
	if t.Sim || t.UID == "" {
		return ""
	}
	now := time.Now()
	f, _ := ptrecord.Write(ptreport.Report{ToolVersion: x.srv.Version, Firmware: t.FW, BoardUID: t.UID,
		Port: t.Port, StartedAt: now, EndedAt: now,
		Steps: []ptreport.StepResult{{ID: lineSteps[i-1].ID, Type: "line" + strconv.Itoa(i), Outcome: o,
			Reason: "safety not defined yet; passes by default", StartedAt: now}}})
	return f
}

func (x *lineStation) watchRun() {
	f := x.api("GET", "/api/full", nil)
	if f["running"] == true {
		return
	}
	t := x.view()
	st, _ := f["status"].(map[string]any)
	o, _ := st[strconv.Itoa(int(lineSteps[t.Step-1].Stage))].(string)
	file := ""
	if last, _ := f["last"].(map[string]any); last != nil {
		file, _ = last["record"].(string)
		file = filepath.Base(file)
	}
	if ptreport.Outcome(o) == ptreport.OutcomePass {
		x.finish(t.Step, ptreport.OutcomePass, file)
		return
	}
	if o == "" {
		o = string(ptreport.OutcomeError)
	}
	x.fail(t.Step, ptreport.Outcome(o), m("line.why.step_failed", "step", t.Step, "outcome", o))
}

func (x *lineStation) finish(i int, o ptreport.Outcome, file string) {
	x.set(func(t *lineTile) {
		t.Results[i] = string(o)
		t.Passed[i] = true
		if file != "" {
			t.Records[i] = file
		}
	})
	t := x.view()
	if !t.Sim {
		saveLine(t.UID, t.Passed, t.Records)
	}
	x.next()
}

func (x *lineStation) fail(i int, o ptreport.Outcome, why any) {
	w, ok := why.(msg)
	if !ok {
		w = m("line.why.step_failed", "step", i, "outcome", string(o))
	}
	x.set(func(t *lineTile) { t.Results[i] = string(o) })
	x.api("POST", "/api/command", map[string]any{"cmd": "pt.led fault=1"})
	x.enter(lnFail, w)
}

// do is a button the person pressed.
func (x *lineStation) do(a string) {
	t := x.view()
	switch a {
	case "flash":
		if t.State != lnNeedFlash {
			return
		}
		img, _ := fixtureImage()
		if img == "" {
			x.set(func(t *lineTile) { t.Why = m("line.why.no_image") })
			return
		}
		x.enter(lnFlashing, msg{})
		go func() {
			p, err := stlink.OpenSN(x.srv.STLinkSN)
			if err == nil {
				err = p.FlashFixture(img)
			}
			if err != nil {
				x.enter(lnNeedFlash, m("line.why.flash_failed", "detail", err.Error()))
				return
			}
			x.set(func(t *lineTile) { *t = lineTile{N: t.N, State: lnEmpty, Want: t.Want, Port: t.Port} })
		}()
	case "continue":
		if t.State == lnFail || t.State == lnBlocked {
			x.start(t.Step)
		}
	case "restart":
		if t.State == lnFail || t.State == lnBlocked || t.State == lnDone {
			x.set(func(t *lineTile) { t.Passed, t.Records, t.Results = map[int]bool{}, map[int]string{}, map[int]string{} })
			if !t.Sim {
				clearLine(t.UID)
			}
			x.start(1)
		}
	case "pause":
		if t.State == lnRun {
			x.api("POST", "/api/full/stop", nil)
		}
		x.set(func(t *lineTile) { t.Paused = true })
	case "resume":
		x.set(func(t *lineTile) { t.Paused = false })
		if t.State == lnRun || (t.State == lnFail && t.Paused) {
			x.start(t.Step)
		}
	case "remove":
		x.lost()
		x.set(func(t *lineTile) { t.Why = m("line.why.removed") })
	}
}

// line.json beside the board's records: which of the five passed.
type lineFile struct {
	UID     string         `json:"uid"`
	Passed  []int          `json:"passed"`
	Records map[int]string `json:"records,omitempty"`
}

func linePath(uid string) string { return filepath.Join(ptrecord.BoardDir(uid), "line.json") }

func loadLine(uid string) (map[int]bool, map[int]string) {
	passed, recs := map[int]bool{}, map[int]string{}
	if uid == "" {
		return passed, recs
	}
	var f lineFile
	if b, err := os.ReadFile(linePath(uid)); err == nil && json.Unmarshal(b, &f) == nil && f.UID == uid {
		for _, i := range f.Passed {
			passed[i] = true
		}
		for k, v := range f.Records {
			recs[k] = v
		}
	}
	return passed, recs
}

func saveLine(uid string, passed map[int]bool, recs map[int]string) {
	if uid == "" {
		return
	}
	f := lineFile{UID: uid, Records: recs}
	for i := range passed {
		f.Passed = append(f.Passed, i)
	}
	sort.Ints(f.Passed)
	b, _ := json.MarshalIndent(f, "", "  ")
	_ = os.MkdirAll(ptrecord.BoardDir(uid), 0o755)
	_ = os.WriteFile(linePath(uid), append(b, '\n'), 0o644)
}

func clearLine(uid string) {
	if uid != "" {
		if err := os.Remove(linePath(uid)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
	}
}

// msgFromJSON turns a panel reply's {"$t", "args"} back into a msg.
func msgFromJSON(v map[string]any) msg {
	key, _ := v["$t"].(string)
	args, _ := v["args"].(map[string]any)
	return msg{Key: key, Args: args}
}
