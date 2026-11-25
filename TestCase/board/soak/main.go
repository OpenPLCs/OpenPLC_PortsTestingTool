// Command soak is T4-07: the fixture firmware on a real board stays up, and
// when it does not, the evidence is collected and decoded here, not by hand.
// Criteria: HOW-TO-RUN-PORTTOOL-TESTS.md 「T4-07」 (decision 192).
//
//	go run ./TestCase/board/soak --port COM12 [--stlink SN] [--minutes 30] [--every 60]
//
// Exit: 0 pass, 1 fail, 2 could not run.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"PortTool/src/ptboard"
	"PortTool/src/serialx"
	"PortTool/src/stlink"
	"PortTool/tools/localcfg"
)

var (
	port    = flag.String("port", "", "the board's control port (RS232), e.g. COM12")
	sn      = flag.String("stlink", "", "ST-Link serial number; empty takes the only one plugged in")
	minutes = flag.Float64("minutes", 30, "how long the board must stay up")
	every   = flag.Int("every", 60, "seconds between pt.id checks")
	elf     = flag.String("elf", "", "fixture image the board runs; default Output/fixture/OpenPLC_BootLoader.elf")
)

// Cortex-M7 system control block and the STM32H7 debug ID register.
const (
	regIDCODE = 0x5C001000
	regICSR   = 0xE000ED04
	regCFSR   = 0xE000ED28
	regHFSR   = 0xE000ED2C
	regMMFAR  = 0xE000ED34
	regBFAR   = 0xE000ED38
)

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	if *port == "" {
		fmt.Println("usage: go run ./TestCase/board/soak --port COMx")
		return 2
	}
	repo, err := localcfg.FindRepo()
	if err != nil {
		fmt.Println("cannot find the repo root:", err)
		return 2
	}
	cfg := localcfg.Load(repo)
	cli := programmerCLI(cfg)
	if cli == "" {
		fmt.Println("STM32_Programmer_CLI not found: run start.sh find-tools")
		return 2
	}
	if *elf == "" {
		*elf = filepath.Join(repo, "Output", "fixture", "OpenPLC_BootLoader.elf")
	}

	fmt.Println("===== T4-07  fixture firmware stays up on a real board")
	if bad := swdCheck(cli); bad > 0 {
		fmt.Printf("FAIL  SWD: %d of 10 reads at 8 MHz failed\n", bad)
		fmt.Println(">>> ACTION: re-seat the ST-Link cable at both ends and check the board is powered, then run again.")
		return 1
	}
	fmt.Println("PASS  SWD: 10 of 10 reads at 8 MHz")

	sp, err := serialx.Open(*port, 115200)
	if err != nil {
		fmt.Printf("cannot open %s: %v (close the panel; it holds the control port)\n", *port, err)
		return 2
	}
	b := ptboard.New(sp, 0)
	defer b.Close()

	var mu sync.Mutex
	var faultLines []string
	events, stop := b.Subscribe(256)
	defer stop()
	go func() {
		for ev := range events {
			if inFault(ev.Line, len(faultLines) > 0) {
				mu.Lock()
				faultLines = append(faultLines, ev.Line)
				mu.Unlock()
			}
		}
	}()

	version, ok := ask(b)
	if !ok {
		fmt.Println("FAIL  the board does not answer pt.id at the start")
		return evidence(cli, nil, "")
	}
	fmt.Printf("board answers: porttool=%s\n", version)

	end := time.Now().Add(time.Duration(*minutes * float64(time.Minute)))
	checks := 0
	for time.Now().Before(end) {
		wait := time.Duration(*every) * time.Second
		if left := time.Until(end); left < wait {
			wait = left
		}
		time.Sleep(wait)
		mu.Lock()
		seen := append([]string(nil), faultLines...)
		mu.Unlock()
		if len(seen) > 0 {
			fmt.Printf("FAIL  fault report on the control port after %d checks\n", checks)
			return evidence(cli, seen, version)
		}
		if _, ok := ask(b); !ok {
			fmt.Printf("FAIL  no answer to pt.id after %d good checks (%s)\n", checks, time.Now().Format("15:04:05"))
			time.Sleep(time.Second) // a report still being printed
			mu.Lock()
			seen = append([]string(nil), faultLines...)
			mu.Unlock()
			return evidence(cli, seen, version)
		}
		checks++
		fmt.Printf("%s  up (%d)\n", time.Now().Format("15:04:05"), checks)
	}
	fmt.Printf("PASS  up for %.0f min, %d checks, no fault report\n", *minutes, checks)
	return 0
}

// inFault keeps the lines of the firmware's fault report (stm32h7xx_it.c
// IT_Fault_Report): its "--- X ---" header and the register lines after it.
func inFault(line string, open bool) bool {
	if strings.Contains(line, "Fault ---") || strings.Contains(line, "[FAULT]") {
		return true
	}
	return open && (strings.Contains(line, " = 0x") || strings.Contains(line, "Fault:") || strings.HasPrefix(strings.TrimSpace(line), "("))
}

var idRe = regexp.MustCompile(`porttool=(\S+)`)

func ask(b *ptboard.Board) (string, bool) {
	for try := 0; try < 2; try++ {
		lines, err := b.Send("pt.id", ptboard.ExpectOne, 3*time.Second)
		if err == nil && len(lines) > 0 && strings.HasPrefix(lines[0], "OK uid=") {
			if m := idRe.FindStringSubmatch(lines[0]); m != nil {
				return m[1], true
			}
			return "", true
		}
	}
	return "", false
}

// evidence prints what the core and the report say, decoded; always a fail.
func evidence(cli string, report []string, version string) int {
	fmt.Println("----- fault registers (SWD 1 MHz, read only)")
	icsr, okI := readReg(cli, regICSR, 1000)
	if okI {
		v := icsr & 0x1FF
		fmt.Printf("ICSR  = 0x%08X  active exception %d (%s)\n", icsr, v, excName(v))
	} else {
		fmt.Println("ICSR  = unreadable over SWD")
	}
	for _, r := range []struct {
		name string
		addr uint32
	}{{"CFSR ", regCFSR}, {"HFSR ", regHFSR}, {"MMFAR", regMMFAR}, {"BFAR ", regBFAR}} {
		if v, ok := readReg(cli, r.addr, 1000); ok {
			fmt.Printf("%s = 0x%08X\n", r.name, v)
		} else {
			fmt.Printf("%s = unreadable over SWD\n", r.name)
		}
	}
	if len(report) == 0 {
		fmt.Println("----- no fault report reached the control port")
		return 1
	}
	fmt.Println("----- fault report from the control port")
	for _, l := range report {
		fmt.Println("  " + l)
	}
	fmt.Println("----- addresses in the report, against", *elf)
	if v := elfVersion(); version != "" && v != "" && v != version {
		fmt.Printf("  WARNING: the board runs %s, the image is %s: locations below are not reliable\n", version, v)
	}
	a2l := addr2line()
	for _, a := range reportAddrs(report) {
		fmt.Printf("  %s  %s\n", a, where(a2l, a))
	}
	return 1
}

var addrRe = regexp.MustCompile(`\b(?:PC|LR|pc|lr)\s*=\s*(0x[0-9A-Fa-f]{8})`)

func reportAddrs(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lines {
		for _, m := range addrRe.FindAllStringSubmatch(l, -1) {
			a := strings.ToLower(m[1])
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}

func where(a2l, addr string) string {
	if a2l == "" {
		return "(arm-none-eabi-addr2line not found)"
	}
	n, _ := strconv.ParseUint(addr[2:], 16, 32)
	n &^= 1 // a Thumb return address has bit 0 set
	out, err := exec.Command(a2l, "-f", "-e", *elf, fmt.Sprintf("0x%x", n)).CombinedOutput()
	if err != nil {
		return "(addr2line: " + err.Error() + ")"
	}
	f := strings.Fields(strings.ReplaceAll(string(out), "\r", ""))
	if len(f) >= 2 {
		return f[0] + "  " + filepath.Base(f[1])
	}
	return strings.TrimSpace(string(out))
}

func excName(v uint32) string {
	switch v {
	case 0:
		return "none, thread mode"
	case 2:
		return "NMI"
	case 3:
		return "HardFault"
	case 4:
		return "MemManage"
	case 5:
		return "BusFault"
	case 6:
		return "UsageFault"
	}
	if v >= 16 {
		return fmt.Sprintf("IRQ %d", v-16)
	}
	return "other"
}

func elfVersion() string {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(*elf), "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// swdCheck reads the debug ID register ten times at 8 MHz; it returns the misses.
func swdCheck(cli string) int {
	bad := 0
	for i := 0; i < 10; i++ {
		if _, ok := readReg(cli, regIDCODE, 8000); !ok {
			bad++
		}
	}
	return bad
}

var regRe = regexp.MustCompile(`0x[0-9A-Fa-f]{8}\s*:\s*([0-9A-Fa-f]{8})`)

// readReg reads one word without halting or resetting the board.
func readReg(cli string, addr uint32, khz int) (uint32, bool) {
	args := []string{"-c", "port=SWD", "freq=" + strconv.Itoa(khz), "mode=HOTPLUG"}
	if *sn != "" {
		args = append(args, "sn="+*sn)
	}
	args = append(args, "-r32", fmt.Sprintf("0x%08X", addr), "4")
	out, err := stlink.Run(cli, args...)
	if err != nil || strings.Contains(out, "Error") {
		return 0, false
	}
	m := regRe.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseUint(m[1], 16, 32)
	return uint32(v), err == nil
}

// programmerCLI and addr2line look inside the CubeIDE install that
// tools/local.json names; the newest plugin wins.
func programmerCLI(cfg map[string]string) string {
	if hit := inCubeIDE(cfg, "com.st.stm32cube.ide.mcu.externaltools.cubeprogrammer.*", "STM32_Programmer_CLI"); hit != "" {
		return hit
	}
	p, _ := stlink.Find()
	return p
}

func addr2line() string {
	return inCubeIDE(localcfg.Load(mustRepo()), "com.st.stm32cube.ide.mcu.externaltools.gnu-tools-for-stm32.*", "arm-none-eabi-addr2line")
}

func inCubeIDE(cfg map[string]string, plugin, tool string) string {
	if cfg["CUBEIDE"] == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	hits, _ := filepath.Glob(filepath.Join(cfg["CUBEIDE"], "STM32CubeIDE", "plugins", plugin, "tools", "bin", tool))
	if len(hits) == 0 {
		return ""
	}
	return hits[len(hits)-1]
}

func mustRepo() string {
	r, _ := localcfg.FindRepo()
	return r
}
