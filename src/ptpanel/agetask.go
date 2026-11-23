package ptpanel

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"PortTool/src/calstore"
	"PortTool/src/ptcheck"
	"PortTool/src/ptitems"
	"PortTool/src/ptplan"
)

// The aging task the board runs by itself: one line per item,
// "<code> <target> <args>|<field>:<op>:<value>[:<max>];...", the limits taken
// from the criteria plan (decision 190 「老化判据」, PORTTOOL-FLOW.md C.3.9).

// agingSpec is each aging item: its LED3 code (decision 189), the criteria
// plan step its checks come from, and the firmware target when the plan's step
// is a session the board cannot run alone.
var agingSpec = []struct {
	Port   string
	Code   int
	Step   string
	Target string
	Args   string
	Checks string // for an item with no plan step: judged by its own counters
}{
	{"temp", 1, "temperature", "temp.read", "ch=1,2", ""},
	{"sd", 2, "sd-integrity", "", "", ""},
	{"sdram", 3, "sdram-stress", "", "", ""},
	{"rtc", 4, "", "lse.freq", "", ""}, // the card's limits: rtcChecks
	// The harness loops (decision 188) judge themselves.
	{"dout", 5, "", "loop.dodi", "", "ok:eq:1"},
	{"rs232", 7, "", "loop.rs232", "", "ok:eq:1"},
	// The CAN controller's internal loopback, 1.5 s of frames (PORTTOOL-FLOW.md C.3.9).
	{"can", 9, "", "session.can", "mode=loopback period=300", "rx_frames:range:3:1000000000;miss:eq:0;junk:eq:0;lec:eq:0"},
}

// rtcChecks are the crystal's: running, and |ppm| within the R&D card's
// ppm_max when one is set (decision 190 「老化里的 RTC」, decision 164).
func rtcChecks(ppmMax string) []string {
	if x, err := strconv.ParseFloat(strings.TrimSpace(ppmMax), 64); err == nil && x >= 0 {
		f := strconv.FormatFloat(x, 'f', -1, 64)
		return []string{"ready:eq:1", "ppm:range:-" + f + ":" + f}
	}
	return []string{"ready:eq:1", "ppm:range:-1000000:1000000"} // only that a deviation was measured
}

// agingTask builds the task lines for the ports not switched off; ppmMax is
// the R&D card's rtc.lsefreq limit.
func agingTask(plan *ptplan.Plan, off map[string]bool, ppmMax string) ([]string, error) {
	steps := map[string]ptplan.Step{}
	for _, st := range plan.Steps {
		steps[st.ID] = st
	}
	var lines []string
	for _, a := range agingSpec {
		if off[a.Port] {
			continue
		}
		if a.Step == "" {
			checks := a.Checks
			if a.Port == "rtc" {
				checks = strings.Join(rtcChecks(ppmMax), ";")
			}
			lines = append(lines, fmt.Sprintf("%d %s %s|%s", a.Code, a.Target, a.Args, checks))
			continue
		}
		st, ok := steps[a.Step]
		if !ok {
			return nil, fmt.Errorf("criteria plan %q has no step %q for aging item %s", plan.Name, a.Step, a.Port)
		}
		target, args := a.Target, a.Args
		if target == "" {
			target, args = st.Target, strings.Join(st.ParamArgs(), " ")
		}
		var checks []string
		for _, c := range st.Checks {
			s, err := agingCheck(c)
			if err != nil {
				return nil, fmt.Errorf("aging item %s: %w", a.Port, err)
			}
			checks = append(checks, s)
		}
		if len(checks) == 0 {
			return nil, fmt.Errorf("aging item %s: step %q has no checks", a.Port, a.Step)
		}
		line := fmt.Sprintf("%d %s %s|%s", a.Code, target, args, strings.Join(checks, ";"))
		if len(line) > 250 {
			return nil, fmt.Errorf("aging item %s: task line longer than the board takes", a.Port)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no aging item is ticked")
	}
	return lines, nil
}

func num(f *float64) string { return strconv.FormatFloat(*f, 'f', -1, 64) }

// agingCheck is one check in the board's form; only what the firmware judges.
func agingCheck(c ptcheck.Check) (string, error) {
	if strings.ContainsAny(c.Field+c.Value, ":;|") {
		return "", fmt.Errorf("check %s: value has a separator in it", c.Field)
	}
	switch c.Op {
	case ptcheck.OpEq, ptcheck.OpContains:
		return fmt.Sprintf("%s:%s:%s", c.Field, c.Op, c.Value), nil
	case ptcheck.OpRange:
		if c.Min == nil || c.Max == nil {
			return "", fmt.Errorf("check %s: the board judges a range only with both ends", c.Field)
		}
		return fmt.Sprintf("%s:range:%s:%s", c.Field, num(c.Min), num(c.Max)), nil
	}
	return "", fmt.Errorf("check %s: the board cannot judge op %q", c.Field, c.Op)
}

// cardPpmMax is the R&D card's ppm_max for rtc.lsefreq: the page memo's value,
// else the catalogue default.
func cardPpmMax() string {
	if v, ok := loadPageMemo()["rd"]["par|rtc.lsefreq|"+ptitems.ParamPpmMax]; ok {
		return v
	}
	for _, it := range ptitems.Catalogue {
		if it.ID == "rtc.lsefreq" {
			return it.Limits[ptitems.ParamPpmMax]
		}
	}
	return ""
}

// agingTolPct is the AO -> AI tolerance, % of full scale: a default, the two
// channels' calibration errors added, not measured (PORTTOOL-FLOW.md C.3.9).
const agingTolPct = 2.0

// agingAnalog is the AO -> AI loop's task lines: two points, 25 % and 75 % of
// the range both pairs share, with the DAC millivolts to set and the raw AI
// counts to expect, from the board's k, b and the range formulas.
func agingAnalog(cal map[string]kb, ranges calstore.Ranges) ([]string, error) {
	type pair struct {
		mv     int
		lo, hi int
	}
	var pts [2][2]pair // [point][channel]
	for ch := 1; ch <= 2; ch++ {
		ao, ai := fmt.Sprintf("AO%d", ch), fmt.Sprintf("AI%d", ch)
		ra, ri := ranges[ao], ranges[ai]
		kbo, oko := cal[ao]
		kbi, oki := cal[ai]
		if !oko || !oki || kbo.K == 0 || kbi.K == 0 {
			return nil, fmt.Errorf("no calibration on the board for %s or %s", ao, ai)
		}
		if ra.Current() != ri.Current() {
			return nil, fmt.Errorf("%s and %s are on different kinds of range; the harness needs both voltage or both current", ao, ai)
		}
		alo, afs := ra.Scale()
		ilo, ifs := ri.Scale()
		lo, fs := math.Max(math.Max(alo, ilo), ra.Min()), math.Min(afs, ifs)
		perRaw := ri.AINominal(1) // AI nominal per raw count
		tol := agingTolPct / 100 * (fs - lo)
		for i, f := range []float64{0.25, 0.75} {
			v := lo + f*(fs-lo)
			mv := ra.AODACmV((v - kbo.B) / kbo.K)
			raw := func(x float64) int { return int(math.Round((x - kbi.B) / kbi.K / perRaw)) }
			pts[i][ch-1] = pair{mv: int(math.Max(0, math.Round(mv))), lo: raw(v - tol), hi: raw(v + tol)}
		}
	}
	var lines []string
	for _, p := range pts {
		lines = append(lines, fmt.Sprintf("6 loop.aoai mv1=%d mv2=%d|ok:eq:1;raw1:range:%d:%d;raw2:range:%d:%d",
			p[0].mv, p[1].mv, p[0].lo, p[0].hi, p[1].lo, p[1].hi))
	}
	return lines, nil
}
