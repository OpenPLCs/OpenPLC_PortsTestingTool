package ptpanel

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"PortTool/src/calstore"
	"PortTool/src/ptcheck"
	"PortTool/src/ptplan"
)

func TestAgingTaskFromTheShippedPlan(t *testing.T) {
	plan, err := ptplan.Load("../plans/station6-poweron.json")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := agingTask(&plan, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1 temp.read ch=1,2|", "2 sd.integrity bytes=1048576|", "3 sdram.sweep |", "4 lse.freq |",
		"5 loop.dodi |ok:eq:1", "7 loop.rs232 |ok:eq:1", "9 session.can mode=loopback period=300|rx_frames:"}
	if len(lines) != len(want) {
		t.Fatalf("%d lines, want %d: %q", len(lines), len(want), lines)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Errorf("line %d = %q, want it to start %q", i, lines[i], w)
		}
	}
	// The plan's own limits ride along unchanged.
	if !strings.Contains(lines[0], "ch1[1]:range:0:600") || !strings.Contains(lines[2], "mismatches:eq:0") {
		t.Errorf("limits not carried over: %q", lines)
	}
	if !strings.HasSuffix(lines[3], "|ready:eq:1;ppm:range:-1000000:1000000") {
		t.Errorf("rtc without a card limit: %q", lines[3])
	}
	set, _ := agingTask(&plan, nil, "50")
	if !strings.HasSuffix(set[3], "|ready:eq:1;ppm:range:-50:50") {
		t.Errorf("rtc with ppm_max 50: %q", set[3])
	}
	off, _ := agingTask(&plan, map[string]bool{"sd": true, "rtc": true}, "")
	if len(off) != 5 {
		t.Errorf("with sd and rtc off: %q", off)
	}
}

func TestAgingCheckRefusesWhatTheBoardCannotJudge(t *testing.T) {
	one := 1.0
	for _, c := range []ptcheck.Check{
		{Field: "x", Op: ptcheck.OpNe, Value: "1"},
		{Field: "x", Op: ptcheck.OpRange, Min: &one},
		{Field: "x", Op: ptcheck.OpEq, Value: "a;b"},
	} {
		if _, err := agingCheck(c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestAgingAnalogPoints(t *testing.T) {
	one := map[string]kb{"AI1": {1, 0}, "AI2": {1, 0}, "AO1": {1, 0}, "AO2": {1, 0}}
	rs := calstore.Ranges{"AI1": calstore.RangeVoltage, "AO1": calstore.RangeVoltage,
		"AI2": calstore.RangeCurrent, "AO2": calstore.RangeAO020}
	lines, err := agingAnalog(one, rs)
	if err != nil || len(lines) != 2 {
		t.Fatalf("%q %v", lines, err)
	}
	// AO1 voltage: 25 % of 0-10 V is 2500 mV at the DAC; AI1 reads it back
	// through the divider, raw = 2500 / AINominal(1).
	lo, fs := calstore.RangeVoltage.Scale()
	v := lo + 0.25*(fs-lo)
	want := math.Round(v / calstore.RangeVoltage.AINominal(1))
	var mv1, mv2, a, b, c, d int
	if _, err := fmt.Sscanf(lines[0], "6 loop.aoai mv1=%d mv2=%d|ok:eq:1;raw1:range:%d:%d;raw2:range:%d:%d", &mv1, &mv2, &a, &b, &c, &d); err != nil {
		t.Fatalf("%q: %v", lines[0], err)
	}
	if mv1 != int(math.Round(v)) || float64(a) >= want || float64(b) <= want {
		t.Errorf("AO1/AI1 point: mv1=%d raw %d..%d, want mv1=%v around raw %v", mv1, a, b, v, want)
	}
	if _, err := agingAnalog(one, calstore.Ranges{"AI1": calstore.RangeCurrent, "AO1": calstore.RangeVoltage,
		"AI2": calstore.RangeCurrent, "AO2": calstore.RangeAO020}); err == nil {
		t.Error("voltage AO into a current AI accepted")
	}
	if _, err := agingAnalog(map[string]kb{}, rs); err == nil {
		t.Error("no calibration accepted")
	}
}
