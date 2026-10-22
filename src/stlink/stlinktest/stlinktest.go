// Package stlinktest stands in for STM32_Programmer_CLI in host tests: a 2 MiB
// flash held in memory, answering the calls package stlink makes. No ST-Link,
// no board; it proves what was asked, not what CubeProgrammer would do.
package stlinktest

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"PortTool/src/stlink"
)

// Flash is the board's flash as the fake sees it.
type Flash struct {
	mu    sync.Mutex
	Mem   []byte
	Calls []string
	// Downloads are the files given to -d, in order.
	Downloads []string
	// FailOn makes any call whose arguments contain this text fail.
	FailOn string
	// Programmed counts writes per 32-byte flash word, to catch a word written
	// twice without an erase in between.
	programmed map[int]int
	// Regs holds the registers -r32 / -w32 touch. PWR_CR1.DBP is cleared on
	// every connect (mode=UR is a system reset) and gates writes to RCC_BDCR,
	// as on the chip.
	Regs map[int]uint32
	// BackupResets counts BDRST pulses that took effect.
	BackupResets int
}

// PwrCR1Start is what the fake's PWR_CR1 reads before any write; an arbitrary
// non-zero value, so a write that drops other bits shows up.
const PwrCR1Start = 0xF000C000

// Install puts a fresh fake in place of the CLI and returns it; the test's
// cleanup should call the returned restore.
func Install(tempDir string) (*Flash, func()) {
	f := &Flash{Mem: bytes.Repeat([]byte{0xFF}, stlink.Sectors*stlink.SectorSize), programmed: map[int]int{},
		Regs: map[int]uint32{stlink.PwrCR1: PwrCR1Start}}
	wasRun, wasCLI, wasTemp := stlink.Run, stlink.CLI, stlink.TempDir
	stlink.Run = f.run
	stlink.CLI = "fake-STM32_Programmer_CLI"
	stlink.TempDir = func() string { return tempDir }
	return f, func() { stlink.Run, stlink.CLI, stlink.TempDir = wasRun, wasCLI, wasTemp }
}

// At returns n bytes at an absolute address.
func (f *Flash) At(addr, n int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	off := addr - stlink.FlashBase
	return append([]byte(nil), f.Mem[off:off+n]...)
}

// Put writes bytes at an absolute address, as if programmed earlier.
func (f *Flash) Put(addr int, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copy(f.Mem[addr-stlink.FlashBase:], data)
}

func num(s string) (int, error) {
	v, err := strconv.ParseInt(s, 0, 64)
	return int(v), err
}

func (f *Flash) run(cli string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(args, " ")
	f.Calls = append(f.Calls, line)
	if f.FailOn != "" && strings.Contains(line, f.FailOn) {
		return "Error: injected failure", fmt.Errorf("exit status 1")
	}
	if len(args) < 4 || args[0] != "-c" {
		return "Error: no connect", fmt.Errorf("exit status 1")
	}
	op := args[3:]
	f.Regs[stlink.PwrCR1] &^= stlink.PwrDBP // connect under reset
	switch op[0] {
	case "-r32":
		addr, _ := num(op[1])
		v, ok := f.Regs[addr]
		if !ok {
			return "Error: fake has no register " + op[1], fmt.Errorf("exit status 1")
		}
		return fmt.Sprintf("Reading 32-bit memory content\n0x%08X : %08X\n", addr, v), nil
	case "-w32":
		for i := 0; i < len(op); {
			switch {
			case op[i] == "-rst":
				i++
			case op[i] == "-w32" && i+2 < len(op):
				addr, err1 := num(op[i+1])
				v, err2 := num(op[i+2])
				if err1 != nil || err2 != nil {
					return "Error: bad -w32 " + op[i+1] + " " + op[i+2], fmt.Errorf("exit status 1")
				}
				if addr == stlink.RccBDCR {
					if f.Regs[stlink.PwrCR1]&stlink.PwrDBP == 0 {
						i += 3
						continue // write-protected: ignored, as on the chip
					}
					if f.Regs[addr]&stlink.BDRST != 0 && uint32(v)&stlink.BDRST == 0 {
						f.BackupResets++
					}
				}
				f.Regs[addr] = uint32(v)
				i += 3
			default:
				return "Error: unknown " + line, fmt.Errorf("exit status 1")
			}
		}
		return "Writing 32-bit data done", nil
	case "-u":
		addr, _ := num(op[1])
		n, _ := num(op[2])
		off := addr - stlink.FlashBase
		return "Upload done", os.WriteFile(op[3], f.Mem[off:off+n], 0o644)
	case "-e":
		for _, s := range op[1:] {
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 || n >= stlink.Sectors {
				return "Error: bad sector " + s, fmt.Errorf("exit status 1")
			}
			base := n * stlink.SectorSize
			copy(f.Mem[base:base+stlink.SectorSize], bytes.Repeat([]byte{0xFF}, stlink.SectorSize))
			for w := base; w < base+stlink.SectorSize; w += 32 {
				delete(f.programmed, w)
			}
		}
		return "erased", nil
	case "-w":
		data, err := os.ReadFile(op[1])
		if err != nil {
			return "Error: " + err.Error(), err
		}
		addr := stlink.FlashBase // .hex / .elf: the fake puts them at the start
		if len(op) > 2 && strings.HasPrefix(op[2], "0x") {
			addr, _ = num(op[2])
		}
		off := addr - stlink.FlashBase
		for i := 0; i < len(data); i++ {
			if f.Mem[off+i] != 0xFF && f.Mem[off+i] != data[i] {
				return "Error: writing over programmed flash", fmt.Errorf("exit status 1")
			}
		}
		copy(f.Mem[off:], data)
		for w := off &^ 31; w < off+len(data); w += 32 {
			f.programmed[w]++
		}
		return "Download verified successfully", nil
	case "-d":
		// A download erases the sectors it covers first; the fake puts the
		// file's bytes at the start of flash, like -w of a .hex.
		data, err := os.ReadFile(op[1])
		if err != nil {
			return "Error: " + err.Error(), err
		}
		f.Downloads = append(f.Downloads, op[1])
		for s := 0; s*stlink.SectorSize < len(data); s++ {
			base := s * stlink.SectorSize
			copy(f.Mem[base:base+stlink.SectorSize], bytes.Repeat([]byte{0xFF}, stlink.SectorSize))
		}
		copy(f.Mem, data)
		return "Download verified successfully", nil
	case "-rst":
		return "MCU Reset", nil
	}
	return "Error: unknown " + line, fmt.Errorf("exit status 1")
}
