// Command aging is T4-09: production aging on a real board - it starts at
// power-up, keeps its time over a power cut, and step ③'s buttons work when
// the board comes back. Criteria: HOW-TO-RUN-PORTTOOL-TESTS.md 「T4-09」
// (decision 192). About 1.5 h; a person unplugs, plugs and power-cycles when
// told, and the code sees it done over SWD or on the tile.
//
//	go run ./TestCase/board/aging --port COM12 [--stlink SN]
//
// Exit: 0 pass, 1 fail, 2 could not run.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"PortTool/TestCase/board/lineenv"
)

var (
	port = flag.String("port", "", "the board's control port (RS232), e.g. COM12")
	sn   = flag.String("stlink", "", "ST-Link serial number; empty takes the only one plugged in")
	only = flag.String("only", "", `"cancel": only steps 1-2 and cancel aging; "fail": only an aging failure`)
)

// LED3 is PE2 (port_led.h): GPIOE ODR bit 2.
// personWait is how long a step waits for a person to do what it asked.
const personWait = 30 * time.Minute

const (
	gpioeODR = 0x58021014
	ledBit   = 1 << 2
)

var ageNames = []string{"running", "done", "passed", "n_items", "task_secs", "elapsed_ms", "rounds", "fails", "first_code"}

type board struct {
	swd   *lineenv.SWD
	addr  map[string]uint32
	first uint32 // lowest address of the aging variables
	words int
	tick  uint32 // uwTick: it starts over at every boot
}

// ages is the board's aging state as porttool_age.c holds it.
type ages struct {
	Running, Done, Passed, Items, Secs, Elapsed, Rounds, Fails uint32
	Code                                                       uint32 // the first failing item's flash code
	Tick                                                       uint32
}

func (b *board) read() (ages, bool) {
	w, ok := b.swd.Read(b.first, b.words)
	if !ok {
		return ages{}, false
	}
	v := func(n string) uint32 { return w[(b.addr[n]-b.first)/4] }
	t, ok := b.swd.Read(b.tick, 1)
	if !ok {
		return ages{}, false
	}
	return ages{Running: v("running"), Done: v("done"), Passed: v("passed"), Items: v("n_items"),
		Secs: v("task_secs"), Elapsed: v("elapsed_ms") / 1000, Rounds: v("rounds"), Fails: v("fails"),
		Code: v("first_code"), Tick: t[0]}, true
}

func (a ages) String() string {
	return fmt.Sprintf("running=%d done=%d passed=%d items=%d secs=%d elapsed=%d rounds=%d fails=%d code=%d",
		a.Running, a.Done, a.Passed, a.Items, a.Secs, a.Elapsed, a.Rounds, a.Fails, a.Code)
}

// led samples LED3 for d: how many reads, how many lit.
func (b *board) led(d time.Duration) (n, on int) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		if w, ok := b.swd.Read(gpioeODR, 1); ok {
			n++
			if w[0]&ledBit != 0 {
				on++
			}
		}
	}
	return n, on
}

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	if *port == "" {
		fmt.Println("usage: go run ./TestCase/board/aging --port COMx [--stlink SN]")
		return 2
	}
	fmt.Println("===== T4-09  aging on a real board")
	e, err := lineenv.Prepare("t4-09", *port, *sn)
	if err != nil {
		fmt.Println("cannot set up the test PortTool:", err)
		return 2
	}
	swd, err := lineenv.NewSWD(*sn)
	if err != nil {
		fmt.Println("STM32_Programmer_CLI not found:", err)
		return 2
	}
	addr, err := lineenv.Symbols(e.Elf, "porttool_age.c", ageNames...)
	if err != nil {
		fmt.Println(err)
		return 2
	}
	tick, err := lineenv.Symbols(e.Elf, "", "uwTick")
	if err != nil {
		fmt.Println(err)
		return 2
	}
	b := &board{swd: swd, addr: addr, tick: tick["uwTick"]}
	var as []uint32
	for _, a := range addr {
		as = append(as, a)
	}
	sort.Slice(as, func(i, j int) bool { return as[i] < as[j] })
	b.first, b.words = as[0], int(as[len(as)-1]-as[0])/4+1

	fmt.Printf("test PortTool in %s, fixture image %s\n", e.Dir, e.Want)
	if err := e.Start(); err != nil {
		fmt.Println("the test PortTool did not start:", err)
		return 2
	}
	defer e.Stop()
	t := &test{e: e, b: b}
	if !t.all() {
		fmt.Println("FAIL  T4-09")
		return 1
	}
	fmt.Println("PASS  T4-09")
	return 0
}

type test struct {
	e     *lineenv.Env
	b     *board
	uid   string
	items int
}

func pass(f string, a ...any) { fmt.Printf("PASS  "+f+"\n", a...) }

func fail(f string, a ...any) bool {
	fmt.Printf("FAIL  "+f+"\n", a...)
	return false
}

func (t *test) all() bool {
	switch *only {
	case "cancel":
		return t.toOven() && t.cancel()
	case "fail":
		return t.toOven() && t.failCode()
	}
	return t.toOven() && t.reageAndKeep() && t.startsAtPowerUp() && t.keepsTime() &&
		t.pluggedWhileRunning() && t.runsToEnd() && t.stepThree() && t.cancel()
}

// toOven runs ① ② (flashing first when the board needs it) to the hand-off.
func (t *test) toOven() bool {
	e := t.e
	tl, ok := e.Until(3*time.Minute, func(tl lineenv.Tile) bool {
		return tl.State == "needflash" || tl.State == "agego" || tl.State == "fail" || tl.State == "blocked"
	})
	if ok && tl.State == "needflash" {
		if err := e.Act("flash"); err != nil {
			return fail("the flash button: %v", err)
		}
	}
	tl, ok = e.Until(15*time.Minute, func(tl lineenv.Tile) bool {
		return tl.State == "agego" || tl.State == "fail" || tl.State == "blocked"
	})
	if !ok || tl.State != "agego" || tl.FW != e.Want {
		lineenv.Action("Check the control port and ST-Link cables and that the board is powered.")
		return fail("steps 1-2 did not reach the aging hand-off (%s %v)", tl, tl.Why.Args)
	}
	t.uid = tl.UID
	items, secs, err := e.AgingWritten(t.uid)
	if err != nil || secs != lineenv.AgingSecs || items == 0 {
		return fail("step 2 wrote %d items, %d s (%v); want %d s", items, secs, err, lineenv.AgingSecs)
	}
	t.items = items
	pass("steps 1-2: board %s, aging task %d items, %d s", t.uid, items, secs)
	return true
}

// backShort presses remove and waits for the board to be taken back to ③,
// which finds the task not run out.
func (t *test) backShort() (lineenv.Tile, bool) {
	if err := t.e.Act("remove"); err != nil {
		return lineenv.Tile{}, fail("the remove button: %v", err)
	}
	t.e.Until(10*time.Second, func(tl lineenv.Tile) bool { return tl.State == "empty" })
	tl, ok := t.e.Until(30*time.Second, func(tl lineenv.Tile) bool {
		return tl.State == "short" || tl.State == "fail" || tl.State == "blocked"
	})
	if !ok || tl.State != "short" || tl.Why.Key != "line.why.age_short" {
		return tl, fail("after remove the board did not come back to aging-not-done (%s)", tl)
	}
	return tl, true
}

func (t *test) reageAndKeep() bool {
	e := t.e
	if _, ok := t.backShort(); !ok {
		return false
	}
	if e.Act("reage") != nil {
		return fail("the age-again button")
	}
	if tl, ok := e.Until(30*time.Second, func(tl lineenv.Tile) bool { return tl.State == "agego" || tl.State == "fail" }); !ok || tl.State != "agego" {
		return fail("age again did not reach the hand-off (%s)", tl)
	}
	if a, err := e.AgeRead(); err != nil || a.State != "paused" || a.Elapsed != 0 || a.Items != t.items || a.Secs != lineenv.AgingSecs {
		return fail("after age again the board holds %+v (%v)", a, err)
	}
	pass("age again: a fresh task, %d items, 0 s run", t.items)
	if _, ok := t.backShort(); !ok {
		return false
	}
	if e.Act("keepaging") != nil {
		return fail("the keep-aging button")
	}
	if tl, ok := e.Until(30*time.Second, func(tl lineenv.Tile) bool { return tl.State == "agego" }); !ok {
		return fail("keep aging did not go back to the hand-off (%s)", tl)
	}
	if a, err := e.AgeRead(); err != nil || a.State != "paused" || a.Items != t.items {
		return fail("after keep aging the board holds %+v (%v)", a, err)
	}
	pass("keep aging: back to the hand-off, the task kept")
	return true
}

// waitRestart waits for a power cycle - uwTick starts over - and aging
// running. A failed read is not a power cut: the panel looks over the same
// ST-Link while the board does not answer on RS232.
func (t *test) waitRestart(limit time.Duration, needGone bool) (ages, bool) {
	var last ages
	restarted, gone := false, !needGone
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(time.Second) {
		if !gone {
			if tl, err := t.e.Tile(); err == nil {
				t.e.Show(tl)
				// On this bench the ST-Link stays on, so a board gone from RS232
				// can show as needing the firmware.
				gone = tl.State == "empty" || tl.State == "needflash"
			}
		}
		a, ok := t.b.read()
		if !ok {
			continue
		}
		if last.Tick != 0 && a.Tick < last.Tick {
			restarted = true
		}
		last = a
		if restarted && a.Running == 1 && gone {
			return a, true
		}
	}
	switch {
	case !gone:
		fail("the tile still shows the board: was the RS232 cable unplugged at the board?")
	case !restarted:
		fail("no power cycle seen over SWD")
	default:
		fail("the board restarted but does not age (%s): RS232 still connected?", last)
	}
	return last, false
}

func (t *test) startsAtPowerUp() bool {
	lineenv.Action("Unplug the RS232 cable at the board, leave the adapter in the PC. Then switch the board off and on.")
	a, ok := t.waitRestart(personWait, true)
	if !ok {
		return false
	}
	if int(a.Items) != t.items || a.Secs != lineenv.AgingSecs {
		return fail("aging runs %d items, %d s; step 2 wrote %d items, %d s", a.Items, a.Secs, t.items, lineenv.AgingSecs)
	}
	pass("the board ages after power-up: %s", a)
	n, on := t.b.led(8 * time.Second)
	if n < 10 || on == 0 || on == n || on*10 < n*3 || on*10 > n*7 {
		return fail("LED3 while aging: %d of %d reads lit; want 1 s on, 1 s off", on, n)
	}
	pass("LED3 blinks while aging: %d of %d reads lit", on, n)
	return true
}

// waitElapsed waits until the board has aged at least secs, printing progress.
func (t *test) waitElapsed(secs uint32, limit time.Duration) (ages, bool) {
	var a ages
	shown := time.Time{}
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(10 * time.Second) {
		got, ok := t.b.read()
		if !ok {
			continue
		}
		a = got
		if time.Since(shown) >= 5*time.Minute {
			shown = time.Now()
			fmt.Printf("%s  aging: %s\n", time.Now().Format("15:04:05"), a)
		}
		if a.Done == 1 || a.Elapsed >= secs {
			return a, true
		}
		if a.Running == 0 {
			return a, fail("aging stopped before %d s: %s", secs, a)
		}
	}
	return a, fail("aging did not reach %d s in time: %s", secs, a)
}

func (t *test) keepsTime() bool {
	before, ok := t.waitElapsed(630, 15*time.Minute)
	if !ok {
		return false
	}
	lineenv.Action("Switch the board off and on. Leave the RS232 cable unplugged.")
	after, ok := t.waitRestart(personWait, false)
	if !ok {
		return false
	}
	if after.Elapsed < 600 || before.Elapsed > after.Elapsed+600 {
		return fail("aged %d s before the power cut, %d s after it; want at least 600 and no more than 600 lost", before.Elapsed, after.Elapsed)
	}
	pass("the aging time survives a power cut: %d s before, %d s after", before.Elapsed, after.Elapsed)
	return true
}

func (t *test) pluggedWhileRunning() bool {
	lineenv.Action("Plug the RS232 cable back into the board. Leave the power on.")
	connectFailed := false
	tl, ok := t.e.Until(personWait, func(tl lineenv.Tile) bool {
		connectFailed = connectFailed || tl.Why.Key == "line.why.connect_failed"
		return tl.State == "short" || tl.State == "fail" || tl.State == "blocked"
	})
	if !ok || tl.State != "short" {
		return fail("plugged back while aging, the tile did not reach aging-not-done (%s)", tl)
	}
	if connectFailed {
		return fail("the panel could not connect while the board was aging")
	}
	if a, ok := t.b.read(); !ok || a.Running != 0 {
		return fail("aging did not pause on the control port: %s", a)
	}
	pass("plugged back while aging: the board paused and the tile offers keep / again / cancel")
	if t.e.Act("keepaging") != nil {
		return fail("the keep-aging button")
	}
	if tl, ok := t.e.Until(30*time.Second, func(tl lineenv.Tile) bool { return tl.State == "agego" }); !ok {
		return fail("keep aging did not go back to the hand-off (%s)", tl)
	}
	return true
}

func (t *test) runsToEnd() bool {
	lineenv.Action("Unplug the RS232 cable at the board. Then switch the board off and on.")
	a, ok := t.waitRestart(personWait, true)
	if !ok {
		return false
	}
	if a.Elapsed < 600 {
		return fail("aging restarted at %d s; it had passed 600 s before", a.Elapsed)
	}
	pass("aging carries on from %d s", a.Elapsed)
	a, ok = t.waitElapsed(lineenv.AgingSecs+1, time.Duration(lineenv.AgingSecs-a.Elapsed+600)*time.Second)
	if !ok {
		return false
	}
	time.Sleep(3 * time.Second)
	if a, ok = t.b.read(); !ok || a.Done != 1 || a.Passed != 1 || a.Fails != 0 || a.Elapsed < lineenv.AgingSecs || a.Rounds == 0 {
		return fail("at the end of aging: %s", a)
	}
	pass("aging ran out and passed: %s", a)
	if n, on := t.b.led(8 * time.Second); n < 10 || on != n {
		return fail("LED3 after a pass: %d of %d reads lit; want always lit", on, n)
	}
	pass("LED3 stays lit after a pass")
	return true
}

func (t *test) stepThree() bool {
	lineenv.Action("Aging is done. Plug the RS232 cable back into the board.")
	tl, ok := t.e.Until(personWait, func(tl lineenv.Tile) bool {
		return tl.State == "blocked" || tl.State == "fail" || tl.State == "short"
	})
	if !ok || tl.State != "blocked" || !tl.Passed["3"] || !tl.Passed["4"] || tl.Step != 5 {
		return fail("back from aging, steps 3-4 did not pass to the step-5 wait (%s, passed %v)", tl, tl.Passed)
	}
	lf, err := t.e.LineFile(t.uid)
	if err != nil {
		return fail("line.json: %v", err)
	}
	rep, err := t.e.Report(t.uid, lf.Records["3"])
	if err != nil || lineenv.NotPassed(rep) != "" {
		return fail("step 3 report %q: %v %s", lf.Records["3"], err, lineenv.NotPassed(rep))
	}
	if a, err := t.e.AgeRead(); err != nil || a.State != "none" {
		return fail("after step 3 passed the board still holds %+v (%v)", a, err)
	}
	pass("step 3 passed: report %s, the task cleared; step 5 waits (no step-5 files here)", lf.Records["3"])
	return true
}

// cancel ages again from step 1 and cancels; from the hand-off it skips the restart.
func (t *test) cancel() bool {
	e := t.e
	if tl, _ := e.Tile(); tl.State != "agego" {
		if e.Act("restart") != nil {
			return fail("the restart button")
		}
		// The press is queued: wait for step 1 to start before judging.
		if tl, ok := e.Until(time.Minute, func(tl lineenv.Tile) bool { return tl.Step == 1 }); !ok {
			return fail("restart did not go back to step 1 (%s)", tl)
		}
	}
	if tl, ok := e.Until(15*time.Minute, func(tl lineenv.Tile) bool {
		return tl.State == "agego" || tl.State == "fail" || tl.State == "blocked"
	}); !ok || tl.State != "agego" {
		return fail("restart did not reach the aging hand-off (%s)", tl)
	}
	if _, ok := t.backShort(); !ok {
		return false
	}
	if e.Act("cancelage") != nil {
		return fail("the cancel-aging button")
	}
	if tl, ok := e.Until(30*time.Second, func(tl lineenv.Tile) bool { return tl.State == "cancel" }); !ok {
		return fail("cancel did not reach aging-cancelled (%s)", tl)
	}
	if a, err := e.AgeRead(); err != nil || a.State != "none" {
		return fail("after cancel the board holds %+v (%v)", a, err)
	}
	pass("cancel aging: the tile says cancelled, the board holds no task")
	return true
}

// failTask is 60 s of one item that cannot pass: temperature (flash code 1)
// between 100 and 200 C.
var failTask = []string{"pt.age begin secs=60", "pt.age add 1 temp.read ch=1,2|ch1[1]:range:1000:2000", "pt.age commit"}

// failCode ages a task that fails: the board records it, LED3 flashes code 1,
// and step 3 fails with LED3 on.
func (t *test) failCode() bool {
	e := t.e
	for _, c := range failTask {
		if lines, err := e.Command(c); err != nil || len(lines) == 0 || !strings.HasPrefix(lines[0], "OK") {
			return fail("%s: %v %v", c, lines, err)
		}
	}
	if a, err := e.AgeRead(); err != nil || a.Items != 1 || a.Secs != 60 {
		return fail("the failing task did not go in: %+v (%v)", a, err)
	}
	pass("a 60 s task that cannot pass is on the board")
	// With PortTool closed nothing can reach the board, so RS232 can stay in
	// (unplugging is the full run's); an open page's clock poll would pause it.
	e.Stop()
	lineenv.Action("Switch the board off and on. Leave the RS232 cable plugged in.")
	if _, ok := t.waitRestart(personWait, false); !ok {
		return false
	}
	if _, ok := t.waitElapsed(61, 3*time.Minute); !ok {
		return false
	}
	time.Sleep(time.Second)
	a, ok := t.b.read()
	if !ok || a.Done != 1 || a.Passed != 0 || a.Fails == 0 || a.Code != 1 {
		return fail("after the failing task: %s; want done, not passed, failures, code 1", a)
	}
	pass("the board recorded the failure: %s", a)
	n, on := t.b.led(10 * time.Second)
	if n < 10 || on == 0 || on*100 > n*25 {
		return fail("LED3 after a failure: %d of %d reads lit; want code 1, a short flash every 2.4 s", on, n)
	}
	pass("LED3 flashes the failure code: %d of %d reads lit", on, n)
	if err := e.Start(); err != nil {
		return fail("PortTool did not start again: %v", err)
	}
	tl, ok := e.Until(time.Minute, func(tl lineenv.Tile) bool {
		return tl.State == "fail" || tl.State == "short" || tl.State == "blocked" || tl.State == "done"
	})
	if !ok || tl.State != "fail" || tl.Step != 3 || tl.Why.Key != "line.why.age_failed" || !tl.LED {
		return fail("back from a failed aging, step 3 did not fail with LED3 on (%s, led=%v)", tl, tl.LED)
	}
	pass("step 3 fails: %v; the tile lights LED3", tl.Why.Args)
	for _, c := range []string{"pt.age clear", "pt.led fault=0"} {
		if _, err := e.Command(c); err != nil {
			return fail("%s: %v", c, err)
		}
	}
	fmt.Println("cleaned up: the task cleared, LED3 off")
	return true
}
