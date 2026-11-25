// Package lineenv is what the line-screen board tests (T4-08, T4-09) share: a
// PortTool of their own beside a fixed template, the line screen's interface,
// read-only SWD reads and the prompts for a person. Criteria live in
// HOW-TO-RUN-PORTTOOL-TESTS.md (decision 192).
package lineenv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"PortTool/src/serialx"
	"PortTool/tools/localcfg"
)

// AgingSecs is the template's aging time: "1" hour.
const AgingSecs = 3600

// The tests' template: only ports that need no wiring stay on.
const template = `{
  "func":  {"duration": "once", "ports_off": %[1]s},
  "aging": {"duration": "1", "ports_off": %[1]s},
  "user":  {"duration": "1", "ports_off": %[1]s}
}`

var wiredPorts = []string{"ain", "aout", "can", "din", "dout", "eth", "knx", "led", "relay", "reset", "rs232", "rs485", "usb"}

// Env is one test PortTool in Output/build/<name>.
type Env struct {
	Repo, Dir string
	Base      string // the panel's http://127.0.0.1:port
	Want      string // the fixture version in Output/fixture
	Elf       string
	cmd       *exec.Cmd
	last      string
}

// Prepare checks the control port is free and lays out the test PortTool like
// a delivery: exe, plans, the fixture image under its delivery name, one
// station, the line screen, the template. It does not start it.
func Prepare(name, port, sn string) (*Env, error) {
	repo, err := localcfg.FindRepo()
	if err != nil {
		return nil, fmt.Errorf("cannot find the repo root: %v", err)
	}
	if sp, err := serialx.Open(port, 115200); err != nil {
		Action("Close PortTool (it holds the control port), then run again.")
		return nil, fmt.Errorf("cannot open %s: %v", port, err)
	} else {
		sp.Close()
	}
	plat := runtime.GOOS
	if plat == "darwin" && runtime.GOARCH == "arm64" {
		plat = "darwin-arm64"
	}
	bin := filepath.Join(repo, "Output", "bin", plat)
	if _, err := os.Stat(filepath.Join(bin, exeName())); err != nil {
		return nil, fmt.Errorf("no %s: run start.sh tool", filepath.Join(bin, exeName()))
	}
	fx := filepath.Join(repo, "Output", "fixture")
	v, err := os.ReadFile(filepath.Join(fx, "VERSION"))
	if err != nil {
		return nil, fmt.Errorf("no fixture image: run start.sh fixture")
	}
	e := &Env{Repo: repo, Dir: filepath.Join(repo, "Output", "build", name), Want: strings.TrimSpace(string(v)),
		Elf: filepath.Join(fx, "OpenPLC_BootLoader.elf")}

	if err := os.RemoveAll(e.Dir); err != nil {
		return nil, err
	}
	if err := copyTree(filepath.Join(bin, "plans"), filepath.Join(e.Dir, "plans")); err != nil {
		return nil, err
	}
	if err := copyFile(filepath.Join(bin, exeName()), filepath.Join(e.Dir, exeName())); err != nil {
		return nil, err
	}
	if err := copyFile(filepath.Join(fx, "OpenPLC_BootLoader.hex"), filepath.Join(e.Dir, "openplc_fixture_"+e.Want+".hex")); err != nil {
		return nil, err
	}
	off := map[string]bool{}
	for _, p := range wiredPorts {
		off[p] = true
	}
	offJSON, _ := json.Marshal(off)
	files := map[string]any{
		"stations.json":          []map[string]any{{"n": 1, "stlink": sn, "control": port}},
		"porttool_pagememo.json": map[string]any{"ui": map[string]string{"type": "line"}},
		"templates/t1/meta.json": map[string]string{"name": name},
	}
	for f, v := range files {
		b, _ := json.MarshalIndent(v, "", "  ")
		if err := writeFile(filepath.Join(e.Dir, f), b); err != nil {
			return nil, err
		}
	}
	return e, writeFile(filepath.Join(e.Dir, "templates", "t1", "v1.json"), []byte(fmt.Sprintf(template, offJSON)))
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "PortTool.exe"
	}
	return "PortTool"
}

var addrRe = regexp.MustCompile(`http://(127\.0\.0\.1:\d+)`)

// Start runs the test PortTool and reads its address from its output.
func (e *Env) Start() error {
	logPath := filepath.Join(e.Dir, "panel.log")
	fh, err := os.Create(logPath)
	if err != nil {
		return err
	}
	e.cmd = exec.Command(filepath.Join(e.Dir, exeName()), "--no-browser")
	e.cmd.Dir, e.cmd.Stdout, e.cmd.Stderr = e.Dir, fh, fh
	if err := e.cmd.Start(); err != nil {
		fh.Close()
		return err
	}
	go func() { _ = e.cmd.Wait(); fh.Close() }()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		b, _ := os.ReadFile(logPath)
		if m := addrRe.FindSubmatch(b); m != nil {
			e.Base = "http://" + string(m[1])
			fmt.Println("panel at", e.Base)
			return nil
		}
	}
	_ = e.cmd.Process.Kill()
	return fmt.Errorf("no address printed; see %s", logPath)
}

// Stop ends the test PortTool.
func (e *Env) Stop() {
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		time.Sleep(time.Second) // the control port is free once the process is gone
	}
}

// --- the line screen

// Tile is station 1's tile as /api/line gives it.
type Tile struct {
	State   string          `json:"state"`
	Step    int             `json:"step"`
	UID     string          `json:"uid"`
	FW      string          `json:"fw"`
	Want    string          `json:"want"`
	Passed  map[string]bool `json:"passed"`
	Results map[string]any  `json:"results"`
	Why     Why             `json:"why"`
	LED     bool            `json:"led"` // LED3 as the tile shows it
}

// Why is a tile's reason: a string key and its arguments.
type Why struct {
	Key  string
	Args map[string]any
}

func (w *Why) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte(`""`)) {
		*w = Why{}
		return nil
	}
	var v struct {
		Key  string         `json:"$t"`
		Args map[string]any `json:"args"`
	}
	err := json.Unmarshal(b, &v)
	w.Key, w.Args = v.Key, v.Args
	return err
}

func (t Tile) String() string {
	return fmt.Sprintf("%s step=%d fw=%q %s", t.State, t.Step, t.FW, t.Why.Key)
}

// Tile reads station 1 once.
func (e *Env) Tile() (Tile, error) {
	var l struct {
		Tiles []Tile `json:"tiles"`
	}
	if err := Get(e.Base+"/api/line", &l); err != nil {
		return Tile{}, err
	}
	if len(l.Tiles) == 0 {
		return Tile{}, fmt.Errorf("no station")
	}
	return l.Tiles[0], nil
}

// Until polls station 1 each second, printing each change, until done is true.
func (e *Env) Until(limit time.Duration, done func(Tile) bool) (Tile, bool) {
	var t Tile
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(time.Second) {
		got, err := e.Tile()
		if err != nil {
			continue
		}
		t = got
		e.Show(t)
		if done(t) {
			return t, true
		}
	}
	return t, false
}

// Show prints the tile when it changed since the last print.
func (e *Env) Show(t Tile) {
	if s := t.String(); s != e.last {
		e.last = s
		fmt.Printf("%s  tile: %s\n", time.Now().Format("15:04:05"), s)
	}
}

// Act presses a tile button.
func (e *Env) Act(act string) error {
	fmt.Printf("%s  press %s\n", time.Now().Format("15:04:05"), act)
	return Post(e.Base+"/api/line/act", map[string]any{"n": 1, "act": act}, nil)
}

// Command sends one line to station 1's board through the panel.
func (e *Env) Command(cmd string) ([]string, error) {
	var r struct {
		Lines   []string `json:"lines"`
		Refused string   `json:"refused"`
	}
	err := Post(e.Base+"/st/1/api/command", map[string]any{"cmd": cmd}, &r)
	if err == nil && r.Refused != "" {
		err = fmt.Errorf("refused: %s", r.Refused)
	}
	return r.Lines, err
}

var ageRe = regexp.MustCompile(`state=(\w+) secs=(\d+) elapsed=(\d+) rounds=\d+ fails=(\d+) items=(\d+)`)

// AgeRead is the board's pt.age read reply.
type AgeRead struct {
	State                       string
	Secs, Elapsed, Fails, Items int
}

func (e *Env) AgeRead() (AgeRead, error) {
	lines, err := e.Command("pt.age read")
	if err != nil {
		return AgeRead{}, err
	}
	return ParseAgeRead(lines)
}

func ParseAgeRead(lines []string) (AgeRead, error) {
	for _, l := range lines {
		if m := ageRe.FindStringSubmatch(l); m != nil {
			a := AgeRead{State: m[1]}
			a.Secs, _ = strconv.Atoi(m[2])
			a.Elapsed, _ = strconv.Atoi(m[3])
			a.Fails, _ = strconv.Atoi(m[4])
			a.Items, _ = strconv.Atoi(m[5])
			return a, nil
		}
	}
	return AgeRead{}, fmt.Errorf("no pt.age read reply in %q", lines)
}

// RecordURL is the board page's fetch of one report.
func (e *Env) RecordURL(uid, file string) string {
	return e.Base + "/api/line/record?" + url.Values{"uid": {uid}, "file": {file}}.Encode()
}

var client = &http.Client{Timeout: 30 * time.Second}

func Get(u string, out any) error {
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func Post(u string, body, out any) error {
	b, _ := json.Marshal(body)
	resp, err := client.Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// --- records

// LineFile is records/<UID>/line.json.
type LineFile struct {
	UID     string            `json:"uid"`
	Passed  []int             `json:"passed"`
	Records map[string]string `json:"records"`
}

func (e *Env) LineFile(uid string) (LineFile, error) {
	var lf LineFile
	b, err := os.ReadFile(filepath.Join(e.Dir, "records", uid, "line.json"))
	if err != nil {
		return lf, err
	}
	if err := json.Unmarshal(b, &lf); err != nil {
		return lf, err
	}
	if lf.UID != uid {
		return lf, fmt.Errorf("line.json is for board %q", lf.UID)
	}
	return lf, nil
}

// Report is the part of a records/ report the tests judge.
type Report struct {
	BoardUID string `json:"board_uid"`
	Steps    []struct {
		ID      string `json:"id"`
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	} `json:"steps"`
}

func (e *Env) Report(uid, file string) (Report, error) {
	var r Report
	b, err := os.ReadFile(filepath.Join(e.Dir, "records", uid, file))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// NotPassed says what in a report is not a pass; "" when it passed.
func NotPassed(r Report) string {
	pass := 0
	for _, s := range r.Steps {
		switch s.Outcome {
		case "PASS":
			pass++
		case "SKIPPED":
		default:
			return fmt.Sprintf("%s is %s (%s)", s.ID, s.Outcome, s.Reason)
		}
	}
	if pass == 0 {
		return "no step passed"
	}
	return ""
}

var writtenRe = regexp.MustCompile(`aging task written: (\d+) items, (\d+) s`)

// AgingWritten is what step 2's report says went into the board.
func (e *Env) AgingWritten(uid string) (items, secs int, err error) {
	lf, err := e.LineFile(uid)
	if err != nil {
		return 0, 0, err
	}
	rep, err := e.Report(uid, lf.Records["2"])
	if err != nil {
		return 0, 0, fmt.Errorf("step 2 report: %v", err)
	}
	for _, s := range rep.Steps {
		if m := writtenRe.FindStringSubmatch(s.Reason); m != nil {
			items, _ = strconv.Atoi(m[1])
			secs, _ = strconv.Atoi(m[2])
			return items, secs, nil
		}
	}
	return 0, 0, fmt.Errorf("step 2 report does not say what it wrote")
}

// --- a person

// Action is a step only a person can do: printed, and on Windows also spoken,
// since a long test is not watched all the time.
func Action(s string) {
	fmt.Println(">>> ACTION: " + s)
	if runtime.GOOS == "windows" {
		say := "Add-Type -AssemblyName System.Speech; (New-Object System.Speech.Synthesis.SpeechSynthesizer).Speak('" +
			strings.ReplaceAll(s, "'", "") + "')"
		_ = exec.Command("powershell", "-NoProfile", "-Command", say).Start()
	}
}

// --- files

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return writeFile(to, b)
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		return copyFile(p, filepath.Join(to, rel))
	})
}
