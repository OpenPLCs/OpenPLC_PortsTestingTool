package lineenv

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"PortTool/src/stlink"
	"PortTool/tools/localcfg"
)

// SWD reads words from a running board without halting or resetting it.
type SWD struct {
	cli, sn string
}

func NewSWD(sn string) (*SWD, error) {
	cli, err := stlink.Find()
	if err != nil {
		return nil, err
	}
	return &SWD{cli: cli, sn: sn}, nil
}

var wordLineRe = regexp.MustCompile(`(?m)^\s*0x([0-9A-Fa-f]{8})\s*:\s*((?:[0-9A-Fa-f]{8}\s*)+)$`)

// Read returns n words from addr; false when the board did not answer.
func (s *SWD) Read(addr uint32, n int) ([]uint32, bool) {
	args := []string{"-c", "port=SWD", "freq=4000", "mode=HOTPLUG"}
	if s.sn != "" {
		args = append(args, "sn="+s.sn)
	}
	args = append(args, "-r32", fmt.Sprintf("0x%08X", addr), strconv.Itoa(4*n))
	out, err := stlink.Run(s.cli, args...)
	if err != nil || strings.Contains(out, "Error") {
		return nil, false
	}
	words := parseWords(out, addr)
	if len(words) < n {
		return nil, false
	}
	return words[:n], true
}

// parseWords reads CubeProgrammer's "0xADDR : WORD WORD ..." lines from addr on.
func parseWords(out string, addr uint32) []uint32 {
	var words []uint32
	for _, m := range wordLineRe.FindAllStringSubmatch(out, -1) {
		at, _ := strconv.ParseUint(m[1], 16, 32)
		if uint32(at) != addr+uint32(4*len(words)) {
			continue
		}
		for _, f := range strings.Fields(m[2]) {
			v, _ := strconv.ParseUint(f, 16, 32)
			words = append(words, uint32(v))
		}
	}
	return words
}

// Symbols finds the addresses of names in the elf; with file set, only the
// file-local ones from that source file (statics share names across files).
func Symbols(elf, file string, names ...string) (map[string]uint32, error) {
	nm := cubeTool("arm-none-eabi-nm")
	if nm == "" {
		return nil, fmt.Errorf("arm-none-eabi-nm not found in the CubeIDE that tools/local.json names")
	}
	out, err := exec.Command(nm, "-l", elf).Output()
	if err != nil {
		return nil, fmt.Errorf("nm %s: %v", elf, err)
	}
	return parseSymbols(string(out), file, names)
}

func parseSymbols(out, file string, names []string) (map[string]uint32, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	got := map[string]uint32{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(l))
		if len(f) < 3 || !want[f[2]] {
			continue
		}
		if file != "" && (len(f) < 4 || !strings.Contains(filepath.ToSlash(f[3]), "/"+file+":")) {
			continue
		}
		v, err := strconv.ParseUint(f[0], 16, 32)
		if err == nil {
			got[f[2]] = uint32(v)
		}
	}
	for _, n := range names {
		if _, ok := got[n]; !ok {
			return nil, fmt.Errorf("symbol %s (%s) not in the elf", n, file)
		}
	}
	return got, nil
}

func cubeTool(tool string) string {
	repo, err := localcfg.FindRepo()
	if err != nil {
		return ""
	}
	cfg := localcfg.Load(repo)
	if cfg["CUBEIDE"] == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	hits, _ := filepath.Glob(filepath.Join(cfg["CUBEIDE"], "STM32CubeIDE", "plugins",
		"com.st.stm32cube.ide.mcu.externaltools.gnu-tools-for-stm32.*", "tools", "bin", tool))
	if len(hits) == 0 {
		return ""
	}
	return hits[len(hits)-1]
}
