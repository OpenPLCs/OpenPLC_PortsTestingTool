// Command line is T4-08: the line screen on a real board, from no fixture
// firmware to the aging task written. Criteria: HOW-TO-RUN-PORTTOOL-TESTS.md
// 「T4-08」 (decision 192). It erases the board's flash.
//
//	go run ./TestCase/board/line --port COM12 [--stlink SN] [--old old_fixture.hex]
//
// Exit: 0 pass, 1 fail, 2 could not run.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"PortTool/TestCase/board/lineenv"
	"PortTool/src/stlink"
)

var (
	port = flag.String("port", "", "the board's control port (RS232), e.g. COM12")
	sn   = flag.String("stlink", "", "ST-Link serial number; empty takes the only one plugged in")
	old  = flag.String("old", "", "an older fixture image (.hex) to start from; default: an empty chip")
)

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	if *port == "" {
		fmt.Println("usage: go run ./TestCase/board/line --port COMx [--stlink SN] [--old image.hex]")
		return 2
	}
	fmt.Println("===== T4-08  the line screen on a real board")
	e, err := lineenv.Prepare("t4-08", *port, *sn)
	if err != nil {
		fmt.Println("cannot set up the test PortTool:", err)
		return 2
	}
	fmt.Printf("test PortTool in %s, fixture image %s\n", e.Dir, e.Want)

	p, err := stlink.OpenSN(*sn)
	if err != nil {
		fmt.Println("no ST-Link:", err)
		lineenv.Action("Plug in the ST-Link and connect it to the board, then run again.")
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
		lineenv.Action("Check the ST-Link cable and that the board is powered, then run again.")
		return 1
	}

	if err := e.Start(); err != nil {
		fmt.Println("the test PortTool did not start:", err)
		return 2
	}
	defer e.Stop()
	return check(e)
}

func check(e *lineenv.Env) int {
	want := e.Want

	// Needs flashing, and says why.
	t, ok := e.Until(60*time.Second, func(t lineenv.Tile) bool { return t.State == "needflash" })
	if !ok {
		fmt.Printf("FAIL  the tile never asked for the fixture firmware (%s)\n", t)
		lineenv.Action("Check the control port and ST-Link cables and that the board is powered.")
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
	if err := e.Act("flash"); err != nil {
		fmt.Println("FAIL  the flash button:", err)
		return 1
	}
	flashing := false
	t, ok = e.Until(3*time.Minute, func(t lineenv.Tile) bool {
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
		fmt.Printf("FAIL  not recognised after flashing (%s %v)\n", t, t.Why.Args)
		return 1
	}
	if t.Want != want {
		fmt.Printf("FAIL  the tile wants %q, the image is %q\n", t.Want, want)
		return 1
	}
	uid := t.UID
	fmt.Printf("PASS  flashed and recognised: board %s, firmware %s\n", uid, t.FW)

	// ① and ② run by themselves and stop at "unplug, off to the oven".
	t, ok = e.Until(15*time.Minute, func(t lineenv.Tile) bool {
		return t.State == "agego" || t.State == "fail" || t.State == "blocked"
	})
	if !ok || t.State != "agego" {
		fmt.Printf("FAIL  steps 1-2 did not reach the aging hand-off (%s %v, results %v)\n", t, t.Why.Args, t.Results)
		return 1
	}
	if !t.Passed["1"] || !t.Passed["2"] {
		fmt.Printf("FAIL  at the aging hand-off but passed=%v\n", t.Passed)
		return 1
	}
	fmt.Println("PASS  steps 1 and 2 passed; the tile says unplug for the oven")

	bad := records(e, uid)
	items, secs, err := e.AgingWritten(uid)
	if err != nil {
		fmt.Println("FAIL ", err)
		bad++
	} else if secs != lineenv.AgingSecs {
		fmt.Printf("FAIL  step 2 wrote %d s, the template says %d s\n", secs, lineenv.AgingSecs)
		bad++
	}
	if a, err := e.AgeRead(); err != nil {
		fmt.Println("FAIL  pt.age read:", err)
		bad++
	} else if a.State != "paused" || a.Secs != lineenv.AgingSecs || a.Fails != 0 || a.Items != items || a.Items == 0 {
		fmt.Printf("FAIL  the board holds %+v; want paused, %d s, 0 fails, %d items\n", a, lineenv.AgingSecs, items)
		bad++
	} else {
		fmt.Printf("PASS  the board holds the task: %d items, %d s, waiting for power-up\n", a.Items, a.Secs)
	}

	// Leave the board without a task, so it does not start aging at power-up.
	if lines, err := e.Command("pt.age clear"); err != nil || len(lines) == 0 || !strings.HasPrefix(lines[0], "OK age cleared") {
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
func records(e *lineenv.Env, uid string) int {
	bad := 0
	fail := func(f string, a ...any) { fmt.Printf("FAIL  "+f+"\n", a...); bad++ }
	lf, err := e.LineFile(uid)
	if err != nil {
		fail("records/%s/line.json: %v", uid, err)
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
		rep, err := e.Report(uid, f)
		if err != nil {
			fail("step %s report %s: %v", step, f, err)
			continue
		}
		if rep.BoardUID != uid {
			fail("step %s report is for board %q", step, rep.BoardUID)
		}
		if why := lineenv.NotPassed(rep); why != "" {
			fail("step %s report: %s", step, why)
		}
		var got struct {
			Record *lineenv.Report `json:"record"`
		}
		if err := lineenv.Get(e.RecordURL(uid, f), &got); err != nil || got.Record == nil || got.Record.BoardUID != uid {
			fail("the board page cannot fetch step %s's report %s: %v", step, f, err)
		}
	}
	if bad == 0 {
		fmt.Println("PASS  records: line.json passes 1 and 2, both reports pass and the board page reads them")
	}
	return bad
}
