// Command line is T4-08: the line screen on a real board, from no fixture
// firmware to the aging task written. Criteria: HOW-TO-RUN-PORTTOOL-TESTS.md
// 「T4-08」 (decision 192). It erases the board's flash.
//
//	go run ./TestCase/board/line --port COM12 [--stlink SN] [--old old_fixture.hex]
//
// Exit: 0 pass, 1 fail, 2 could not run.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
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
	"PortTool/src/stlink"
	"PortTool/tools/localcfg"
)

var (
	port = flag.String("port", "", "the board's control port (RS232), e.g. COM12")
	sn   = flag.String("stlink", "", "ST-Link serial number; empty takes the only one plugged in")
	old  = flag.String("old", "", "an older fixture image (.hex) to start from; default: an empty chip")
)

// The test's own template: only ports that need no wiring stay on.
const template = `{
  "func":  {"duration": "once", "ports_off": %[1]s},
  "aging": {"duration": "1", "ports_off": %[1]s},
  "user":  {"duration": "1", "ports_off": %[1]s}
}`

var wiredPorts = []string{"ain", "aout", "can", "din", "dout", "eth", "knx", "led", "relay", "reset", "rs232", "rs485", "usb"}

const agingSecs = 3600 // the template's "1" hour

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	if *port == "" {
		fmt.Println("usage: go run ./TestCase/board/line --port COMx [--stlink SN] [--old image.hex]")
		return 2
	}
	repo, err := localcfg.FindRepo()
	if err != nil {
		fmt.Println("cannot find the repo root:", err)
		return 2
	}
	fmt.Println("===== T4-08  the line screen on a real board")
	if sp, err := serialx.Open(*port, 115200); err != nil {
		fmt.Printf("cannot open %s: %v\n", *port, err)
		fmt.Println(">>> ACTION: close PortTool (it holds the control port), then run again.")
		return 2
	} else {
		sp.Close()
	}
	dir, want, err := sandbox(repo)
	if err != nil {
		fmt.Println("cannot set up the test PortTool:", err)
		return 2
	}
	fmt.Printf("test PortTool in %s, fixture image %s\n", dir, want)

	p, err := stlink.OpenSN(*sn)
	if err != nil {
		fmt.Println("no ST-Link:", err)
		fmt.Println(">>> ACTION: plug in the ST-Link and connect it to the board, then run again.")
		return 2
	}
	if *old != "" {
		fmt.Println("flashing the older image", *old)
		err = p.FlashFixture(*old)
	} else {
		fmt.Println("erasing sector 0: the board becomes an empty chip")
		err = p.Erase(0)
	}
	if err != nil {
		fmt.Println("FAIL  could not prepare the board over SWD:", err)
		fmt.Println(">>> ACTION: check the ST-Link cable and that the board is powered, then run again.")
		return 1
	}

	cmd, base, err := startPanel(dir)
	if err != nil {
		fmt.Println("the test PortTool did not start:", err)
		return 2
	}
	defer func() { _ = cmd.Process.Kill() }()
	fmt.Println("panel at", base)
	return check(base, dir, want)
}

func check(base, dir, want string) int {
	w := &watcher{base: base}

	// Needs flashing, and says why.
	t, ok := w.until(60*time.Second, func(t tile) bool { return t.State == "needflash" })
	if !ok {
		fmt.Printf("FAIL  the tile never asked for the fixture firmware (state %s, why %s)\n", t.State, t.Why.Key)
		fmt.Println(">>> ACTION: check the control port and ST-Link cables and that the board is powered.")
		return 1
	}
	if *old == "" && (t.FW != "blank" || t.Why.Key != "line.why.no_fw") {
		fmt.Printf("FAIL  empty chip shown as firmware %q, reason %s\n", t.FW, t.Why.Key)
		return 1
	}
	if *old != "" && (t.FW == "" || t.FW == "blank" || t.FW == want || t.Why.Key != "line.why.old_fw") {
		fmt.Printf("FAIL  older firmware shown as %q, reason %s\n", t.FW, t.Why.Key)
		return 1
	}
	fmt.Printf("PASS  needs flashing: firmware %q, reason %s\n", t.FW, t.Why.Key)

	// Flash, then the board is recognised with the current version.
	if err := post(base+"/api/line/act", map[string]any{"n": 1, "act": "flash"}, nil); err != nil {
		fmt.Println("FAIL  the flash button:", err)
		return 1
	}
	flashing := false
	t, ok = w.until(3*time.Minute, func(t tile) bool {
		flashing = flashing || t.State == "flashing"
		if flashing && t.State == "needflash" {
			return true // back to needing the firmware: judged below
		}
		return t.FW == want && t.UID != "" && t.State != "needflash" && t.State != "flashing" && t.State != "empty"
	})
	if ok && t.State == "needflash" {
		fmt.Printf("FAIL  back to needs-flashing after the flash (firmware %q, why %s %v)\n", t.FW, t.Why.Key, t.Why.Args)
		return 1
	}
	if !ok {
		fmt.Printf("FAIL  not recognised after flashing (state %s, firmware %q, why %s %v)\n", t.State, t.FW, t.Why.Key, t.Why.Args)
		return 1
	}
	if t.Want != want {
		fmt.Printf("FAIL  the tile wants %q, the image is %q\n", t.Want, want)
		return 1
	}
	uid := t.UID
	fmt.Printf("PASS  flashed and recognised: board %s, firmware %s\n", uid, t.FW)

	// ① and ② run by themselves and stop at "unplug, off to the oven".
	t, ok = w.until(15*time.Minute, func(t tile) bool {
		return t.State == "agego" || t.State == "fail" || t.State == "blocked"
	})
	if !ok || t.State != "agego" {
		fmt.Printf("FAIL  steps 1-2 did not reach the aging hand-off (state %s, step %d, why %s %v, results %v)\n",
			t.State, t.Step, t.Why.Key, t.Why.Args, t.Results)
		return 1
	}
	if !t.Passed["1"] || !t.Passed["2"] {
		fmt.Printf("FAIL  at the aging hand-off but passed=%v\n", t.Passed)
		return 1
	}
	fmt.Println("PASS  steps 1 and 2 passed; the tile says unplug for the oven")

	bad := records(base, dir, uid)
	items, secs, err := agingWritten(dir, uid)
	if err != nil {
		fmt.Println("FAIL ", err)
		bad++
	} else if secs != agingSecs {
		fmt.Printf("FAIL  step 2 wrote %d s, the template says %d s\n", secs, agingSecs)
		bad++
	}
	bad += agingOnBoard(base, items)

	// Leave the board without a task, so it does not start aging at power-up.
	if lines, err := command(base, "pt.age clear"); err != nil || len(lines) == 0 || !strings.HasPrefix(lines[0], "OK age cleared") {
		fmt.Printf("FAIL  pt.age clear: %v %v\n", lines, err)
		bad++
	} else {
		fmt.Println("cleaned up: the aging task is cleared")
	}
	if bad > 0 {
		fmt.Printf("FAIL  %d check(s) above\n", bad)
		return 1
	}
	fmt.Println("PASS  T4-08")
	return 0
}

// records checks line.json and the two reports it names.
func records(base, dir, uid string) int {
	bad := 0
	fail := func(f string, a ...any) { fmt.Printf("FAIL  "+f+"\n", a...); bad++ }
	var lf struct {
		UID     string            `json:"uid"`
		Passed  []int             `json:"passed"`
		Records map[string]string `json:"records"`
	}
	bd := filepath.Join(dir, "records", uid)
	b, err := os.ReadFile(filepath.Join(bd, "line.json"))
	if err != nil || json.Unmarshal(b, &lf) != nil || lf.UID != uid {
		fail("records/%s/line.json missing or not this board's", uid)
		return bad
	}
	if fmt.Sprint(lf.Passed) != "[1 2]" {
		fail("line.json passed=%v, want [1 2]", lf.Passed)
	}
	for _, step := range []string{"1", "2"} {
		f := lf.Records[step]
		if f == "" || f != filepath.Base(f) {
			fail("line.json step %s names %q, want a file name", step, f)
			continue
		}
		rep, err := readReport(filepath.Join(bd, f))
		if err != nil {
			fail("step %s report %s: %v", step, f, err)
			continue
		}
		if rep.BoardUID != uid {
			fail("step %s report is for board %q", step, rep.BoardUID)
		}
		if why := notPassed(rep); why != "" {
			fail("step %s report: %s", step, why)
		}
		var got struct {
			Record *report `json:"record"`
		}
		q := url.Values{"uid": {uid}, "file": {f}}
		if err := get(base+"/api/line/record?"+q.Encode(), &got); err != nil || got.Record == nil || got.Record.BoardUID != uid {
			fail("the board page cannot fetch step %s's report %s: %v", step, f, err)
		}
	}
	if bad == 0 {
		fmt.Println("PASS  records: line.json passes 1 and 2, both reports pass and the board page reads them")
	}
	return bad
}

type report struct {
	BoardUID string `json:"board_uid"`
	Steps    []struct {
		ID      string `json:"id"`
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	} `json:"steps"`
}

func readReport(path string) (report, error) {
	var r report
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// notPassed says what in a report is not a pass; "" when it passed.
func notPassed(r report) string {
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

// agingWritten is what step 2's report says went into the board.
func agingWritten(dir, uid string) (items, secs int, err error) {
	b, _ := os.ReadFile(filepath.Join(dir, "records", uid, "line.json"))
	var lf struct {
		Records map[string]string `json:"records"`
	}
	_ = json.Unmarshal(b, &lf)
	rep, err := readReport(filepath.Join(dir, "records", uid, lf.Records["2"]))
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

var ageRe = regexp.MustCompile(`state=(\w+) secs=(\d+) elapsed=\d+ rounds=\d+ fails=(\d+) items=(\d+)`)

// agingOnBoard reads the task back from the board.
func agingOnBoard(base string, items int) int {
	lines, err := command(base, "pt.age read")
	var m []string
	for _, l := range lines {
		if m = ageRe.FindStringSubmatch(l); m != nil {
			break
		}
	}
	if err != nil || m == nil {
		fmt.Printf("FAIL  pt.age read: %v %v\n", lines, err)
		return 1
	}
	secs, _ := strconv.Atoi(m[2])
	fails, _ := strconv.Atoi(m[3])
	n, _ := strconv.Atoi(m[4])
	if m[1] != "paused" || secs != agingSecs || fails != 0 || n != items || n == 0 {
		fmt.Printf("FAIL  the board holds state=%s secs=%d fails=%d items=%d; want paused, %d s, 0 fails, %d items\n",
			m[1], secs, fails, n, agingSecs, items)
		return 1
	}
	fmt.Printf("PASS  the board holds the task: %d items, %d s, waiting for power-up\n", n, secs)
	return 0
}

// --- the panel's interface

type tile struct {
	State   string          `json:"state"`
	Step    int             `json:"step"`
	UID     string          `json:"uid"`
	FW      string          `json:"fw"`
	Want    string          `json:"want"`
	Passed  map[string]bool `json:"passed"`
	Results map[string]any  `json:"results"`
	Why     why             `json:"why"`
}

type why struct {
	Key  string
	Args map[string]any
}

func (w *why) UnmarshalJSON(b []byte) error {
	var v struct {
		Key  string         `json:"$t"`
		Args map[string]any `json:"args"`
	}
	if bytes.Equal(b, []byte(`""`)) {
		return nil
	}
	err := json.Unmarshal(b, &v)
	w.Key, w.Args = v.Key, v.Args
	return err
}

type watcher struct {
	base string
	last string
}

// until polls station 1 and prints each change of state until done is true.
func (w *watcher) until(limit time.Duration, done func(tile) bool) (tile, bool) {
	var t tile
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		var l struct {
			Tiles []tile `json:"tiles"`
		}
		if err := get(w.base+"/api/line", &l); err == nil && len(l.Tiles) > 0 {
			t = l.Tiles[0]
			if s := fmt.Sprintf("%s step=%d fw=%q %s", t.State, t.Step, t.FW, t.Why.Key); s != w.last {
				w.last = s
				fmt.Printf("%s  tile: %s\n", time.Now().Format("15:04:05"), s)
			}
			if done(t) {
				return t, true
			}
		}
		time.Sleep(time.Second)
	}
	return t, false
}

func command(base, cmd string) ([]string, error) {
	var r struct {
		Lines   []string `json:"lines"`
		Refused string   `json:"refused"`
	}
	err := post(base+"/st/1/api/command", map[string]any{"cmd": cmd}, &r)
	if err == nil && r.Refused != "" {
		err = fmt.Errorf("refused: %s", r.Refused)
	}
	return r.Lines, err
}

var client = &http.Client{Timeout: 30 * time.Second}

func get(u string, out any) error {
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func post(u string, body, out any) error {
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

// --- the test's own PortTool

// sandbox lays out Output/build/t4-08 like a delivery: exe, plans, the fixture
// image under its delivery name, one station, the line screen, the template.
func sandbox(repo string) (dir, want string, err error) {
	plat := runtime.GOOS
	if plat == "darwin" && runtime.GOARCH == "arm64" {
		plat = "darwin-arm64"
	}
	exe := "PortTool"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	bin := filepath.Join(repo, "Output", "bin", plat)
	if _, err := os.Stat(filepath.Join(bin, exe)); err != nil {
		return "", "", fmt.Errorf("no %s: run start.sh tool", filepath.Join(bin, exe))
	}
	fx := filepath.Join(repo, "Output", "fixture")
	v, err := os.ReadFile(filepath.Join(fx, "VERSION"))
	if err != nil {
		return "", "", fmt.Errorf("no fixture image: run start.sh fixture")
	}
	want = strings.TrimSpace(string(v))

	dir = filepath.Join(repo, "Output", "build", "t4-08")
	if err := os.RemoveAll(dir); err != nil {
		return "", "", err
	}
	if err := copyTree(filepath.Join(bin, "plans"), filepath.Join(dir, "plans")); err != nil {
		return "", "", err
	}
	if err := copyFile(filepath.Join(bin, exe), filepath.Join(dir, exe)); err != nil {
		return "", "", err
	}
	if err := copyFile(filepath.Join(fx, "OpenPLC_BootLoader.hex"), filepath.Join(dir, "openplc_fixture_"+want+".hex")); err != nil {
		return "", "", err
	}
	off := map[string]bool{}
	for _, p := range wiredPorts {
		off[p] = true
	}
	offJSON, _ := json.Marshal(off)
	files := map[string]any{
		"stations.json":          []map[string]any{{"n": 1, "stlink": *sn, "control": *port}},
		"porttool_pagememo.json": map[string]any{"ui": map[string]string{"type": "line"}},
		"templates/t1/meta.json": map[string]string{"name": "T4-08"},
	}
	for name, v := range files {
		b, _ := json.MarshalIndent(v, "", "  ")
		if err := writeFile(filepath.Join(dir, name), b); err != nil {
			return "", "", err
		}
	}
	err = writeFile(filepath.Join(dir, "templates", "t1", "v1.json"), []byte(fmt.Sprintf(template, offJSON)))
	return dir, want, err
}

var addrRe = regexp.MustCompile(`http://(127\.0\.0\.1:\d+)`)

// startPanel starts the test PortTool and reads its address from its output.
func startPanel(dir string) (*exec.Cmd, string, error) {
	exe := filepath.Join(dir, "PortTool")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	logPath := filepath.Join(dir, "panel.log")
	fh, err := os.Create(logPath)
	if err != nil {
		return nil, "", err
	}
	cmd := exec.Command(exe, "--no-browser")
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, fh, fh
	if err := cmd.Start(); err != nil {
		fh.Close()
		return nil, "", err
	}
	go func() { _ = cmd.Wait(); fh.Close() }()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		b, _ := os.ReadFile(logPath)
		if m := addrRe.FindSubmatch(b); m != nil {
			return cmd, "http://" + string(m[1]), nil
		}
	}
	_ = cmd.Process.Kill()
	return nil, "", fmt.Errorf("no address printed; see %s", logPath)
}

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
