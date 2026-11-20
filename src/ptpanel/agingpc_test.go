package ptpanel

// R&D aging with the PC (decision 187, PORTTOOL-FLOW.md C.3.8) through the
// panel: the saved configuration, its checks and the start gate, then a run
// from 「开始」 on the simulated board with the clock cut to seconds.

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"PortTool/src/ptboard"
	"PortTool/src/ptitems"
	"PortTool/src/ptreport"
	"PortTool/src/ptstages"
)

func onCfg(params map[string]string) itemCfg {
	on := true
	return itemCfg{On: &on, Params: params}
}

// Saving refuses values the run could not use; a ticked item with nothing set
// greys step 7 with the reason (decision 187).
func TestAgingPCConfigIsChecked(t *testing.T) {
	useTempStageCfg(t)
	s := New()
	h := s.Handler()
	for name, ic := range map[string]map[string]itemCfg{
		"DI 9":      {"din.expect": onCfg(map[string]string{ptitems.ParamHigh: "1,9"})},
		"AI letter": {"ain.expect": onCfg(map[string]string{ptitems.ParamIn1: "abc"})},
		"tol below": {"ain.expect": onCfg(map[string]string{ptitems.ParamTol: "-1"})},
	} {
		if out := postCfg(t, h, map[string]any{"stage": "agingpc", "cfg": stageCfg{Duration: "1", Items: ic}}); out["error"] == nil {
			t.Errorf("%s: saved", name)
		}
	}
	if out := postCfg(t, h, map[string]any{"stage": "agingpc", "cfg": stageCfg{Duration: "once"}}); out["error"] == nil {
		t.Error("R&D aging saved a single run")
	}

	s.caps, s.board = cfgGoldenCaps(t), new(ptboard.Board)
	// Default: the five one-way items unticked, nothing on the reference board.
	f := loadStageCfgs()
	for _, p := range f.AgingPC.picks(ptitems.AgingPC, s.caps, false, true) {
		if p.Peer == ptitems.PeerRef || p.Item.Offers(ptitems.PeerExt) {
			t.Errorf("default pick %s with %s", p.Item.ID, p.Peer)
		}
	}
	if why := s.stepWhy(ptstages.AgingPC); why != nil {
		t.Errorf("default step 7 refused: %v", *why)
	}
	cfg := stageCfg{Duration: "1", Items: map[string]itemCfg{"din.expect": onCfg(nil),
		"relay.cycle": onCfg(map[string]string{"ch": "1", "count": "2"})}}
	if out := postCfg(t, h, map[string]any{"stage": "agingpc", "cfg": cfg}); out["error"] != nil {
		t.Fatal(out["error"])
	}
	why := s.stepWhy(ptstages.AgingPC)
	if why == nil || why.Key != "go.full.no_target" {
		t.Fatalf("din.expect without its high list: %v", why)
	}
	for _, p := range loadStageCfgs().AgingPC.picks(ptitems.AgingPC, s.caps, false, true) {
		if p.Item.ID == "relay.cycle" && p.Peer != ptitems.PeerExt {
			t.Errorf("relays run with %s, want the outside instrument", p.Peer)
		}
	}
}

// From 「开始」 on the simulated board: inputs as set pass and the outputs are
// recorded; a DI that reads otherwise stops the run long before its time.
func TestAgingPCRunsFromTheStartButton(t *testing.T) {
	needSim(t)
	useTempStageCfg(t)
	DeliveryDir = t.TempDir()
	plans, _ := filepath.Abs(filepath.Join("..", "plans"))
	was := PlanDir
	PlanDir = plans
	t.Cleanup(func() { DeliveryDir, PlanDir = "", was })
	s := New()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close(); s.Close() })
	if st := call(t, srv, "/api/connect", map[string]any{"port": "sim"}); st["connected"] != true {
		t.Fatalf("connect: %v", st)
	}
	sim := func(line string) { _, _ = s.board.Send(line, ptboard.ExpectMany, 300*time.Millisecond) }

	off := map[string]bool{}
	for _, p := range ptitems.Ports {
		off[p.Port] = !map[string]bool{"din": true, "ain": true, "dout": true, "aout": true}[p.Port]
	}
	// AI2 on the shared 0-20 mA range; the simulated board's own k, b convert it.
	cfg := stageCfg{Duration: "1", PortsOff: off, Items: map[string]itemCfg{
		"din.expect": onCfg(map[string]string{"ch": "1,2,3", ptitems.ParamHigh: "1,3"}),
		"ain.expect": onCfg(map[string]string{ptitems.ParamIn2: "10", ptitems.ParamTol: "5"}),
		"dout.emit":  onCfg(map[string]string{"ch": "1,2"}),
		"aout.load":  onCfg(map[string]string{ptitems.ParamOut1: "10", ptitems.ParamOut2: "5"}),
	}}
	if out := postCfg(t, s.Handler(), map[string]any{"stage": "agingpc", "cfg": cfg}); out["error"] != nil {
		t.Fatal(out["error"])
	}

	run := func(clock time.Duration) (map[string]ptreport.Outcome, bool, time.Duration) {
		t.Helper()
		stageAfter = func(time.Duration) <-chan time.Time { return time.After(clock) }
		t.Cleanup(func() { stageAfter = time.After })
		start := time.Now()
		if r := call(t, srv, "/api/full/start", map[string]any{"steps": []int{7}}); r["started"] != true {
			t.Fatalf("start: %v", r)
		}
		for s.fullRunningNow() {
			if time.Since(start) > 2*time.Minute {
				s.stopFull("test timeout")
				t.Fatal("the run did not end")
			}
			time.Sleep(200 * time.Millisecond)
		}
		s.full.mu.Lock()
		last := s.full.last
		s.full.mu.Unlock()
		got := map[string]ptreport.Outcome{}
		items, _ := last["items"].([]ptreport.ItemRun)
		for _, it := range items {
			if prev, ok := got[it.Item]; !ok || prev == ptreport.OutcomePass || prev == "RECORDED" {
				got[it.Item] = it.Outcome
			}
		}
		return got, last["passed"] == true, time.Since(start)
	}

	sim("sim.din 0x05")
	sim("sim.ain 2 1240") // 10 mA nominal
	got, passed, _ := run(4 * time.Second)
	want := map[string]ptreport.Outcome{"din.expect": ptreport.OutcomePass, "ain.expect": ptreport.OutcomePass,
		"dout.emit": "RECORDED", "aout.load": "RECORDED"}
	for id, o := range want {
		if got[id] != o {
			t.Errorf("%s is %q, want %s (all: %v)", id, got[id], o, got)
		}
	}
	if !passed {
		t.Errorf("the run did not pass: %v", got)
	}

	sim("sim.din 0x07") // DI2 high where low was set
	got, passed, took := run(time.Minute)
	if passed || got["din.expect"] != ptreport.OutcomeFail || took > 30*time.Second {
		t.Errorf("mismatch: passed %v, din.expect %q, took %s", passed, got["din.expect"], took)
	}
}

func (s *Server) fullRunningNow() bool {
	s.full.mu.Lock()
	defer s.full.mu.Unlock()
	return s.full.running
}

// The reference board is one test method (decision 188): set to unused, the
// functional and user-level steps can still start; aging with the reference
// board cannot.
func TestRefUnusedLeavesTheStepsStartable(t *testing.T) {
	useTempStageCfg(t)
	s := New()
	s.caps, s.board = cfgGoldenCaps(t), new(ptboard.Board)
	if out := postCfg(t, s.Handler(), map[string]any{"ref_unused": true}); out["error"] != nil {
		t.Fatal(out["error"])
	}
	if why := s.stepWhy(ptstages.Functional); why != nil {
		t.Errorf("functional test refused with the reference board unused: %v", *why)
	}
	// Step 6 also needs its files to flash; its picks are what this is about.
	for _, p := range loadStageCfgs().User.picks(ptitems.User, s.caps, true, false) {
		if p.Peer == ptitems.PeerRef || p.Peer == ptitems.PeerHuman {
			t.Errorf("user-level picks %s with %s", p.Item.ID, p.Peer)
		}
	}
	if s.stepWhy(ptstages.Aging) == nil {
		t.Error("aging with the reference board started with the board unused")
	}
}
