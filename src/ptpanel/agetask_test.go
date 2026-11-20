package ptpanel

import (
	"strings"
	"testing"

	"PortTool/src/ptcheck"
	"PortTool/src/ptplan"
)

func TestAgingTaskFromTheShippedPlan(t *testing.T) {
	plan, err := ptplan.Load("../plans/station6-poweron.json")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := agingTask(&plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1 temp.read ch=1,2|", "2 sd.integrity bytes=1048576|", "3 sdram.sweep |", "4 rtc.read |"}
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
	off, _ := agingTask(&plan, map[string]bool{"sd": true, "rtc": true})
	if len(off) != 2 {
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
