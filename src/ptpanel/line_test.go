package ptpanel

import (
	"context"
	"os"
	"path/filepath"
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

func TestLinePassesStepOneThenWaitsForAgingMode(t *testing.T) {
	var fail atomic.Bool
	_, x := lineOnSim(t, &fail)
	v := waitState(t, x, lnBlocked, 60*time.Second)
	if v.Step != 2 || !v.Passed[1] || v.Results[1] != "PASS" {
		t.Fatalf("blocked at step %d, passed %v, results %v; want step 2 after ① passed", v.Step, v.Passed, v.Results)
	}
	if v.Why.Key != "line.why.aging_mode" {
		t.Fatalf("why = %v, want the aging-mode reason", v.Why)
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
	// Fixed, restart goes from ① and reaches the aging step.
	fail.Store(false)
	x.act <- "restart"
	v = waitState(t, x, lnBlocked, 30*time.Second)
	if v.Step != 2 {
		t.Fatalf("after restart blocked at step %d, want 2", v.Step)
	}
}

func TestLinePauseAndRemove(t *testing.T) {
	var fail atomic.Bool
	_, x := lineOnSim(t, &fail)
	waitState(t, x, lnBlocked, 60*time.Second)
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
