package ptpanel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	"PortTool/src/ptplan"
	"PortTool/src/ptrecord"
	"PortTool/src/ptreport"
	"PortTool/src/ptstages"
	"PortTool/src/serialx"
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
	lnBlocked   = "blocked" // the panel refuses the step; waits for continue
	lnAgeGo     = "agego"   // task written: unplug, off to the oven; ③ starts once the task ends
	lnShort     = "short"   // back early, no failure: keep aging, age again or cancel
	lnDone      = "done"    // all five passed
	lnBack      = "back"    // back from the oven: ③ reads the record next
	lnCancel    = "cancel"  // aging cancelled: this board cannot ship this time
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
	Tpl     tplRef         `json:"tpl"`               // the template version this board runs on
	Done    int            `json:"done"`              // items finished in the running step
	Total   int            `json:"total"`             // items the running step will take
	Cur     string         `json:"cur,omitempty"`     // the item running now: "port|item"
	EstMS   int64          `json:"est_ms,omitempty"`  // the step's time last run on this PC
	LED     bool           `json:"led"`               // LED3 as the tile shows it
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
	lastAge time.Time
	lastID  time.Time // stillThere's last pt.id
	missed  int       // pt.id asks in a row with no answer
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
	t := x.tile
	t.LED = t.State == lnFail || t.State == lnDone
	return t
}

func (x *lineStation) enter(state string, why msg) {
	x.set(func(t *lineTile) {
		t.State, t.Why, t.StepAt = state, why, time.Now()
		t.Done, t.Total, t.Cur = 0, 0, ""
		t.EstMS = 0
		if state == lnRun || state == lnFlashing {
			t.EstMS = stepTime(stepKey(state, t.Step))
		}
	})
}

// stepKey names a step for its remembered time: "flash" or "1".."5".
func stepKey(state string, step int) string {
	if state == lnFlashing {
		return "flash"
	}
	return strconv.Itoa(step)
}

// The time each step last took on this PC, for the tile's estimate (C.3.10).
var stepTimesMu sync.Mutex

func stepTimesPath() string { return filepath.Join(filepath.Dir(stationsPath()), "line_times.json") }

func stepTime(key string) int64 {
	stepTimesMu.Lock()
	defer stepTimesMu.Unlock()
	t := map[string]int64{}
	if b, err := os.ReadFile(stepTimesPath()); err == nil {
		_ = json.Unmarshal(b, &t)
	}
	return t[key]
}

func setStepTime(key string, d time.Duration) {
	stepTimesMu.Lock()
	defer stepTimesMu.Unlock()
	t := map[string]int64{}
	if b, err := os.ReadFile(stepTimesPath()); err == nil {
		_ = json.Unmarshal(b, &t)
	}
	t[key] = d.Milliseconds()
	_ = writeJSONFile(stepTimesPath(), t)
}

// useTemplate picks the board's template version: the one it started with when
// that is still on disk, else the one in use now.
func (x *lineStation) useTemplate(r tplRef) tplRef {
	tplMu.Lock()
	defer tplMu.Unlock()
	cfg, err := tplLoad(r)
	if r.ID == "" || err != nil {
		r = tplCurrent()
		cfg, err = tplLoad(r)
	}
	x.srv.tplMu.Lock()
	if err == nil {
		x.srv.tplCfg = &cfg
	} else {
		x.srv.tplCfg = nil
	}
	x.srv.tplMu.Unlock()
	return r
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
	rw, err := x.srv.Open(port, serialx.DefaultBaud)
	if err != nil {
		return "", "", false
	}
	b := ptboard.New(rw, 64)
	defer b.Close() // waits for its reader, so the panel can open the port next
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

// rdMode is the page memo's test type: during R&D the line loops idle and
// station 1 only connects its registered control port (C.3.10 「入口」).
var rdMode = func() bool { return loadPageMemo()["ui"]["type"] == "rd" }

// step advances whatever can advance by itself.
func (x *lineStation) step() {
	if rdMode() {
		if x.n == 1 {
			x.rdAttach()
		}
		return
	}
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
	case lnAgeGo:
		if !x.connected() {
			x.lost()
			return
		}
		x.watchAging()
		x.stillThere()
	case lnBack:
		if !x.connected() {
			x.lost()
			return
		}
		x.checkAging(3)
	case lnFail, lnBlocked, lnDone, lnShort, lnCancel:
		if !x.connected() {
			x.lost()
			return
		}
		x.stillThere()
	}
}

// stillThere asks a waiting real board for pt.id every 2 s: an RS232 cable
// pulled at the board leaves the adapter and the port alive (C.3.10 「认板子」).
func (x *lineStation) stillThere() {
	if x.view().Sim || time.Since(x.lastID) < 2*time.Second {
		return
	}
	x.lastID = time.Now()
	lines, err := x.send("pt.id")
	var left bool
	if left, x.missed = boardLeft(x.view().UID, lines, err, x.missed); left {
		x.missed = 0
		x.lost()
	}
}

// boardLeft judges one pt.id ask: another UID at once, or a second miss in a row.
func boardLeft(uid string, lines []string, err error, missed int) (bool, int) {
	if err == nil {
		for _, l := range lines {
			if mm := idRe.FindStringSubmatch(l); mm != nil {
				return !strings.EqualFold(mm[1], uid), 0
			}
		}
	}
	missed++
	return missed >= 2, missed
}

// lost is the board going away: the tile empties, the progress stays on disk.
func (x *lineStation) lost() {
	x.api("POST", "/api/full/stop", nil)
	x.api("POST", "/api/disconnect", nil)
	x.srv.tplMu.Lock()
	x.srv.tplCfg = nil
	x.srv.tplMu.Unlock()
	x.set(func(t *lineTile) { *t = lineTile{N: t.N, State: lnEmpty, Want: t.Want, StepAt: time.Now()} })
}

// rdAttach connects R&D's panel to the board on the registered control port
// once the fixture firmware answers there, and fills the tile for R&D's
// 「电脑上看到的板子」: a board without the current fixture firmware waits in
// needflash for its 烧写 button (C.3.6 「左 · 接线」). Nothing else runs.
func (x *lineStation) rdAttach() {
	_, want := fixtureImage()
	x.mu.Lock()
	port := x.control
	x.mu.Unlock()
	sim := simboard.IsSim(port)
	x.set(func(t *lineTile) { t.Want, t.Port, t.Sim = want, port, sim })
	// The registration changed under a connected panel: let go of the old port.
	x.srv.mu.Lock()
	held, on := x.srv.board != nil, x.srv.portNam
	x.srv.mu.Unlock()
	if held && on != port {
		x.api("POST", "/api/disconnect", nil)
		x.set(func(t *lineTile) { t.State, t.UID, t.FW = lnEmpty, "", "" })
		return
	}
	if port == "" || x.view().State == lnFlashing {
		return
	}
	if x.connected() {
		x.srv.mu.Lock()
		uid, fw := x.srv.uid, x.srv.caps.Version
		x.srv.mu.Unlock()
		state := lnEmpty
		if !sim && want != "" && fw != want {
			state = lnNeedFlash
		}
		x.set(func(t *lineTile) { t.UID, t.FW, t.State = uid, fw, state })
		return
	}
	if !sim {
		uid, fw, ok := x.askID(port)
		if !ok {
			x.rdLookSWD()
			return
		}
		x.set(func(t *lineTile) { t.UID, t.FW = uid, fw })
	}
	x.connect(port)
}

// connect opens the station's panel on port. Some USB serial drivers report
// the port busy for ~100 ms after askID has closed it, so it tries a few times.
func (x *lineStation) connect(port string) bool {
	for i := 0; i < 5; i++ {
		if st := x.api("POST", "/api/connect", map[string]any{"port": port}); st["connected"] == true {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// rdLookSWD looks behind the ST-Link when nothing answers on the control port,
// at most every 5 s: an empty chip or one without the fixture firmware.
func (x *lineStation) rdLookSWD() {
	if !x.swdDue() {
		return
	}
	clear := func() { x.set(func(t *lineTile) { t.State, t.UID, t.FW = lnEmpty, "", "" }) }
	p, err := stlink.OpenSN(x.srv.STLinkSN)
	if err != nil {
		clear()
		return
	}
	blank, err := p.Blank()
	if err != nil {
		clear()
		return
	}
	fw := ""
	if blank {
		fw = "blank"
	}
	x.set(func(t *lineTile) { t.State, t.UID, t.FW = lnNeedFlash, "", fw })
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
	if x.connected() {
		// Left connected by R&D: the panel already holds the port.
		x.attach("", "")
		return
	}
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
			x.needFlash(m("line.why.old_fw", "have", fw, "want", want))
			return
		}
		x.attach(uid, fw)
		return
	}
	// Nothing answers on the control port: an empty chip shows only on SWD.
	if simboard.IsSim(port) || !x.swdDue() {
		return
	}
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
	x.needFlash(m("line.why.no_fw"))
}

// swdDue paces the ST-Link look to once every 5 s; a flash counts as one, so a
// board still booting is not shown as needing the firmware (C.3.10).
func (x *lineStation) swdDue() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if time.Since(x.lastSWD) < 5*time.Second {
		return false
	}
	x.lastSWD = time.Now()
	return true
}

// needFlash asks for the fixture firmware; a failed flash keeps its reason up
// until the next try or the board leaves (PORTTOOL-FLOW.md C.3.10).
func (x *lineStation) needFlash(why msg) {
	if t := x.view(); t.State == lnNeedFlash && t.Why.Key == "line.why.flash_failed" {
		return
	}
	x.enter(lnNeedFlash, why)
}

// attach connects the station's panel to the board and starts the wiring check.
func (x *lineStation) attach(uid, fw string) {
	x.mu.Lock()
	port := x.control
	x.mu.Unlock()
	if !x.connected() {
		if !x.connect(port) {
			x.set(func(t *lineTile) { t.Why = m("line.why.connect_failed") })
			return
		}
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
	passed, recs, tpl := map[int]bool{}, map[int]string{}, tplRef{}
	if !simboard.IsSim(port) {
		passed, recs, tpl = loadLine(uid)
	}
	tpl = x.useTemplate(tpl)
	x.set(func(t *lineTile) {
		t.UID, t.FW, t.Since, t.Passed, t.Records, t.Tpl = uid, fw, time.Now(), passed, recs, tpl
		t.Results = map[int]string{}
	})
	x.enter(lnWire, msg{})
}

// wiringNeeded is the adapters the functional test's ticked ports use.
func (x *lineStation) wiringNeeded() []string {
	c := x.srv.cfgs().Func
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
		for _, p := range x.wiringNeeded() {
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
	case "aging":
		x.writeAging(i)
		return
	case "aging_check":
		x.enter(lnBack, msg{})
		return
	case "safety":
		// Safety is not defined yet and passes by default (decision 190).
		x.finish(i, ptreport.OutcomePass, x.writeOwn(i, ptreport.OutcomePass, "safety not defined yet; passes by default"))
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
func (x *lineStation) writeOwn(i int, o ptreport.Outcome, reason string) string {
	t := x.view()
	if t.Sim || t.UID == "" {
		return ""
	}
	now := time.Now()
	f, _ := ptrecord.Write(ptreport.Report{ToolVersion: x.srv.Version, Firmware: t.FW, BoardUID: t.UID,
		Port: t.Port, Station: x.n, StartedAt: now, EndedAt: now,
		Steps: []ptreport.StepResult{{ID: lineSteps[i-1].ID, Type: "line" + strconv.Itoa(i), Outcome: o,
			Reason: reason, StartedAt: now}}})
	if f == "" {
		return ""
	}
	return filepath.Base(f) // the board page asks for the report by name (C.3.10 「记录」)
}

func (x *lineStation) watchRun() {
	f := x.api("GET", "/api/full", nil)
	if f["running"] == true {
		x.progress(f)
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

// progress copies the running step's item count and current item to the tile.
func (x *lineStation) progress(f map[string]any) {
	run, _ := f["run"].(map[string]any)
	if run == nil {
		return
	}
	total, _ := run["total"].(float64)
	done, cur := 0, ""
	steps, _ := run["steps"].([]any)
	for _, v := range steps {
		st, _ := v.(map[string]any)
		if st == nil {
			continue
		}
		if st["state"] == jRunning {
			port, _ := st["port"].(string)
			item, _ := st["item"].(string)
			cur = port + "|" + item
		} else {
			done++
		}
	}
	x.set(func(t *lineTile) { t.Done, t.Total, t.Cur = done, int(total), cur })
}

func (x *lineStation) finish(i int, o ptreport.Outcome, file string) {
	if t := x.view(); t.State == lnRun {
		setStepTime(strconv.Itoa(i), time.Since(t.StepAt))
	}
	x.set(func(t *lineTile) {
		t.Results[i] = string(o)
		t.Passed[i] = true
		if file != "" {
			t.Records[i] = file
		}
	})
	t := x.view()
	if !t.Sim {
		saveLine(t.UID, t.Passed, t.Records, t.Tpl)
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
		// R&D may hold the port with an older fixture firmware.
		x.api("POST", "/api/disconnect", nil)
		x.enter(lnFlashing, msg{})
		began := time.Now()
		go func() {
			p, err := stlink.OpenSN(x.srv.STLinkSN)
			if err == nil {
				err = p.FlashFixture(img)
			}
			if err != nil {
				x.enter(lnNeedFlash, m("line.why.flash_failed", "detail", err.Error()))
				return
			}
			setStepTime("flash", time.Since(began))
			x.mu.Lock()
			x.lastSWD = time.Now()
			x.mu.Unlock()
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
	case "keepaging":
		if t.State == lnShort {
			x.enter(lnAgeGo, msg{})
		}
	case "reage":
		if t.State == lnShort {
			x.start(2)
		}
	case "cancelage":
		if t.State == lnShort {
			_, _ = x.send("pt.age clear")
			x.writeOwn(3, ptreport.OutcomeFail, "aging cancelled")
			x.set(func(t *lineTile) { t.Results[3] = string(ptreport.OutcomeFail) })
			x.enter(lnCancel, m("line.why.age_cancelled"))
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
	Tpl     tplRef         `json:"tpl"`
}

func linePath(uid string) string { return filepath.Join(ptrecord.BoardDir(uid), "line.json") }

func loadLine(uid string) (map[int]bool, map[int]string, tplRef) {
	passed, recs := map[int]bool{}, map[int]string{}
	if uid == "" {
		return passed, recs, tplRef{}
	}
	var f lineFile
	if b, err := os.ReadFile(linePath(uid)); err == nil && json.Unmarshal(b, &f) == nil && f.UID == uid {
		for _, i := range f.Passed {
			passed[i] = true
		}
		for k, v := range f.Records {
			recs[k] = filepath.Base(v) // older files kept the whole path
		}
	}
	return passed, recs, f.Tpl
}

func saveLine(uid string, passed map[int]bool, recs map[int]string, tpl tplRef) {
	if uid == "" {
		return
	}
	f := lineFile{UID: uid, Records: recs, Tpl: tpl}
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

// send is one command to the station's board.
func (x *lineStation) send(cmd string) ([]string, error) {
	x.srv.mu.Lock()
	b := x.srv.board
	x.srv.mu.Unlock()
	if b == nil {
		return nil, errors.New("no board connected")
	}
	return b.Send(cmd, ptboard.ExpectFor(cmd), ptboard.TimeoutFor(cmd))
}

// agingSecs is how long the template's aging runs; tests shorten it.
var agingSecs = func(cfg stageCfgs) int {
	d := cfg.Aging.Duration
	if h, err := strconv.Atoi(d); err == nil && h > 0 {
		return h * 3600
	}
	return 4 * 3600
}

// writeAging is step ②: the task goes into the board, which starts aging.
func (x *lineStation) writeAging(i int) {
	name, _, problem := criteriaSource()
	var lines []string
	err := errors.New(problem)
	if problem == "" {
		var path string
		if path, err = safePlanPath(name); err == nil {
			var plan ptplan.Plan
			if plan, err = ptplan.Load(path); err == nil {
				off := x.srv.cfgs().Aging.PortsOff
				lines, err = agingTask(&plan, off, cardPpmMax())
				// The simulated board has no harness to loop AO into AI.
				if err == nil && !off["aout"] && !x.view().Sim {
					var an []string
					cal := x.srv.readBoardCal(x.view().UID)
					if cal.State != calOK {
						err = fmt.Errorf("the board's calibration area is %s", cal.State)
					} else if an, err = agingAnalog(cal.Ch, x.srv.cfgs().Ranges); err == nil {
						lines = append(lines, an...)
					}
				}
			}
		}
	}
	secs := agingSecs(x.srv.cfgs())
	cmds := append([]string{fmt.Sprintf("pt.age begin secs=%d", secs)}, prefixed("pt.age add ", lines)...)
	cmds = append(cmds, "pt.age commit")
	if x.view().Sim {
		// The simulated board never goes to the oven: start it here.
		cmds = append(cmds, "pt.age run")
	}
	for _, c := range cmds {
		if err != nil {
			break
		}
		_, err = x.send(c)
	}
	if err != nil {
		x.writeOwn(i, ptreport.OutcomeError, err.Error())
		x.fail(i, ptreport.OutcomeError, m("line.why.age_task", "detail", err.Error()))
		return
	}
	file := x.writeOwn(i, ptreport.OutcomePass, fmt.Sprintf("aging task written: %d items, %d s", len(lines), secs))
	x.set(func(t *lineTile) {
		t.Results[i], t.Passed[i] = string(ptreport.OutcomePass), true
		if file != "" {
			t.Records[i] = file
		}
	})
	if t := x.view(); !t.Sim {
		saveLine(t.UID, t.Passed, t.Records, t.Tpl)
	}
	x.set(func(t *lineTile) { t.Step = i + 1 })
	x.enter(lnAgeGo, m("line.why.age_written", "items", len(lines), "hours", float64(secs)/3600))
}

func prefixed(p string, xs []string) []string {
	out := make([]string, len(xs))
	for i, s := range xs {
		out[i] = p + s
	}
	return out
}

var ageReadRe = regexp.MustCompile(`state=(\w+) secs=(\d+) elapsed=(\d+) rounds=(\d+) fails=(\d+)`)

type ageState struct {
	State                        string
	Secs, Elapsed, Rounds, Fails int
}

func (x *lineStation) readAging() (ageState, error) {
	lines, err := x.send("pt.age read")
	if err != nil {
		return ageState{}, err
	}
	for _, l := range lines {
		if mm := ageReadRe.FindStringSubmatch(l); mm != nil {
			a := ageState{State: mm[1]}
			a.Secs, _ = strconv.Atoi(mm[2])
			a.Elapsed, _ = strconv.Atoi(mm[3])
			a.Rounds, _ = strconv.Atoi(mm[4])
			a.Fails, _ = strconv.Atoi(mm[5])
			return a, nil
		}
	}
	return ageState{}, errors.New("no pt.age read reply")
}

// watchAging waits, on the bench, for the board's task to end; then ③.
func (x *lineStation) watchAging() {
	// A real board starts aging at its next power-up, in the oven; ③ waits for
	// it to come back. Only the simulated one ages on the bench.
	if !x.view().Sim || time.Since(x.lastAge) < 2*time.Second {
		return
	}
	x.lastAge = time.Now()
	a, err := x.readAging()
	if err == nil && a.State != "run" && a.State != "paused" {
		x.checkAging(3)
	}
}

// checkAging is step ③: the board's own record decides (decision 190).
func (x *lineStation) checkAging(i int) {
	x.set(func(t *lineTile) { t.Step = i })
	a, err := x.readAging()
	switch {
	case err != nil:
		x.fail(i, ptreport.OutcomeError, m("line.why.age_task", "detail", err.Error()))
	case a.State == "none":
		x.writeOwn(i, ptreport.OutcomeFail, "no aging task on the board")
		x.fail(i, ptreport.OutcomeFail, m("line.why.age_none"))
	case a.Fails > 0:
		got := ""
		if lines, err := x.send("pt.age fail i=1"); err == nil && len(lines) > 0 {
			got = strings.TrimPrefix(lines[0], "OK age ")
		}
		x.writeOwn(i, ptreport.OutcomeFail, fmt.Sprintf("aging failed %d times in %d rounds; first: %s", a.Fails, a.Rounds, got))
		x.fail(i, ptreport.OutcomeFail, m("line.why.age_failed", "fails", a.Fails, "first", got))
	case a.State == "run" || a.State == "paused":
		x.enter(lnShort, m("line.why.age_short", "elapsed", fmtSecs(a.Elapsed), "secs", fmtSecs(a.Secs)))
	default:
		_, _ = x.send("pt.age clear")
		x.finish(i, ptreport.OutcomePass, x.writeOwn(i, ptreport.OutcomePass,
			fmt.Sprintf("aging passed: %d s, %d rounds", a.Elapsed, a.Rounds)))
	}
}

func fmtSecs(s int) string { return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60) }
