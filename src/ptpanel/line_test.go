package ptpanel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"PortTool/src/ptreport"
	"PortTool/src/ptstages"
	"PortTool/src/simboard"
)

// lineOnSim runs one station on the simulated board with step ① faked: it
// passes unless fail is set.
func lineOnSim(t *testing.T, fail *atomic.Bool) (*Stations, *lineStation) {
	t.Helper()
	if _, err := simboard.Find(); err != nil {
		t.Skip("simulated board not built: ", err)
	}
	StationsFile = filepath.Join(t.TempDir(), "stations.json")
	t.Cleanup(func() { StationsFile = "" })
	// No item needs the reference board, which the simulated bench lacks.
	StageCfgFile = filepath.Join(t.TempDir(), "stagecfg.json")
	if err := os.WriteFile(StageCfgFile, []byte(`{"ref_unused": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { StageCfgFile = "" })
	plans, _ := filepath.Abs("../plans")
	PlanDir = plans
	t.Cleanup(func() { PlanDir = "" })
	oldSecs := agingSecs
	agingSecs = func() int { return 2 }
	t.Cleanup(func() { agingSecs = oldSecs })
	old := fixtureImage
	fixtureImage = func() (string, string) { return "", "" }
	t.Cleanup(func() { fixtureImage = old })
	st := &Stations{New: func(int) *Server {
		s := New()
		s.Stages[ptstages.Functional] = func(ctx context.Context) ptreport.StepResult {
			if fail.Load() {
				return ptreport.StepResult{Outcome: ptreport.OutcomeFail, Reason: "faked"}
			}
			return ptreport.StepResult{Outcome: ptreport.OutcomePass}
		}
		return s
	}}
	st.Load()
	t.Cleanup(st.Close)
	x := st.list[1].line
	x.mu.Lock()
	x.control = simboard.PortName
	x.mu.Unlock()
	return st, x
}

func waitState(t *testing.T, x *lineStation, want string, within time.Duration) lineTile {
	t.Helper()
	end := time.Now().Add(within)
	for time.Now().Before(end) {
		if v := x.view(); v.State == want {
			return v
		}
		time.Sleep(100 * time.Millisecond)
	}
	v := x.view()
	t.Fatalf("station stayed %q (step %d, why %v), want %q", v.State, v.Step, v.Why, want)
	return v
}

// ① passes, ② writes a two-second aging task, the board runs it, ③ reads it
// back and passes, ④ passes by default, and ⑤ stops: the simulated board has
// no ST-Link or bootloader for the user step.
func TestLineRunsThroughAging(t *testing.T) {
	var fail atomic.Bool
	_, x := lineOnSim(t, &fail)
	v := waitState(t, x, lnBlocked, 90*time.Second)
	if v.Step != 5 {
		t.Fatalf("blocked at step %d (why %v), want 5", v.Step, v.Why)
	}
	for i := 1; i <= 4; i++ {
		if !v.Passed[i] || v.Results[i] != "PASS" {
			t.Fatalf("step %d: passed %v result %q; all of ①-④ should have passed (%v)", i, v.Passed[i], v.Results[i], v.Results)
		}
	}
	if v.Why.Key != "go.full.user_sim" {
		t.Fatalf("why = %v, want the simulated board's user-step reason", v.Why)
	}
}

// A task that cannot pass: ③ fails with the board's first failure record.
func TestLineAgingFailureFailsStepThree(t *testing.T) {
	var fail atomic.Bool
	_, x := lineOnSim(t, &fail)
	old := agingSpec[3].Step
	t.Cleanup(func() { agingSpec[3].Step = old })
	agingSpec[3].Step = "sdram-stress" // rtc.read judged by the SDRAM sweep's fields: never holds
	agingSpec[3].Target, agingSpec[3].Args = "rtc.read", ""
	t.Cleanup(func() { agingSpec[3].Target = "" })
	v := waitState(t, x, lnFail, 90*time.Second)
	if v.Step != 3 || v.Results[3] != "FAIL" || v.Why.Key != "line.why.age_failed" {
		t.Fatalf("failed at step %d with %v, why %v; want ③ FAIL from the board's record", v.Step, v.Results, v.Why)
	}
	if first, _ := v.Why.Args["first"].(string); !strings.Contains(first, "code=4") {
		t.Fatalf("first failure %q should name LED code 4", first)
	}
}

func TestLineFailContinueRestart(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	_, x := lineOnSim(t, &fail)
	v := waitState(t, x, lnFail, 60*time.Second)
	if v.Step != 1 || v.Results[1] != "FAIL" {
		t.Fatalf("failed at step %d with %v, want step 1 FAIL", v.Step, v.Results)
	}
	// Continue redoes the failed step; still failing, it stops there again.
	x.act <- "continue"
	time.Sleep(300 * time.Millisecond)
	waitState(t, x, lnFail, 30*time.Second)
	// Fixed, restart goes from ① through aging to the user step.
	fail.Store(false)
	x.act <- "restart"
	v = waitState(t, x, lnBlocked, 60*time.Second)
	if v.Step != 5 {
		t.Fatalf("after restart blocked at step %d, want 5", v.Step)
	}
}

func TestLinePauseAndRemove(t *testing.T) {
	var fail atomic.Bool
	_, x := lineOnSim(t, &fail)
	waitState(t, x, lnBlocked, 90*time.Second)
	x.act <- "pause"
	time.Sleep(300 * time.Millisecond)
	if !x.view().Paused {
		t.Fatal("pause did not stick")
	}
	x.act <- "remove"
	time.Sleep(300 * time.Millisecond)
	if v := x.view(); v.State != lnEmpty || v.Paused {
		t.Fatalf("after remove: state %q paused %v, want empty and not paused", v.State, v.Paused)
	}
}
