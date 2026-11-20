package ptpanel

import (
	"path/filepath"
	"testing"
	"time"

	"PortTool/src/ptrecord"
	"PortTool/src/ptreport"
)

func TestLineHistoryFindsTheOvenAndTheLatestRecords(t *testing.T) {
	ptrecord.Dir = t.TempDir()
	t.Cleanup(func() { ptrecord.Dir = "" })
	const away, done = "003900393133510F39393938", "003400413135511439303538"
	write := func(uid, step string, o ptreport.Outcome, at time.Time) string {
		f, err := ptrecord.Write(ptreport.Report{BoardUID: uid, StartedAt: at,
			Steps: []ptreport.StepResult{{ID: step, Outcome: o}}})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	t0 := time.Now().Add(-time.Hour)
	saveLine(away, map[int]bool{1: true, 2: true}, map[int]string{2: base(write(away, "aging", ptreport.OutcomePass, t0))})
	saveLine(done, map[int]bool{1: true, 2: true, 3: true}, map[int]string{3: base(write(done, "aging_check", ptreport.OutcomePass, t0.Add(time.Minute)))})

	oven, subs := lineHistory(map[string]bool{}, 10)
	if len(oven) != 1 || oven[0].UID != away || oven[0].Since.IsZero() {
		t.Fatalf("oven = %+v, want only %s with a time", oven, away)
	}
	if len(subs) != 2 || subs[0].Step == "" || subs[0].Outcome != "PASS" {
		t.Fatalf("subs = %+v", subs)
	}
	// A board back on a tile is not in the oven any more.
	if oven, _ := lineHistory(map[string]bool{away: true}, 10); len(oven) != 0 {
		t.Fatalf("oven with the board on a tile = %+v", oven)
	}
	if _, err := readRecord(away, "../x.json"); err == nil {
		t.Fatal("a path outside the board's folder was read")
	}
}

func base(p string) string { return filepath.Base(p) }
