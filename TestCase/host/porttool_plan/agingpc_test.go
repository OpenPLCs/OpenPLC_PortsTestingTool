package testcase

// R&D aging with the PC on the simulated board (decision 187, PORTTOOL-FLOW.md
// C.3.8): DI and AI judged every frame against the set values, DO / AO / relays
// only driven and recorded, the first mismatch stopping the run. The board's
// inputs stand in for the outside source (sim.din, sim.ain).

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"PortTool/src/calstore"
	"PortTool/src/ptitems"
	"PortTool/src/ptlib"
	"PortTool/src/ptplan"
	"PortTool/src/ptreport"
	"PortTool/src/ptseq"
)

var agingPCItems = []string{"din.expect", "ain.expect", "dout.emit", "aout.load", "relay.cycle"}

// agingPCEngine runs the five one-way items on b; AI on 0-20 mA, AO on 0-20 mA, k = 1, b = 0.
func agingPCEngine(t *testing.T) (*ptlib.Engine, func(string)) {
	t.Helper()
	b := openSim(t)
	plan, err := ptplan.Load(filepath.Join("..", "..", "..", "src", "plans", "station6-poweron.json"))
	if err != nil {
		t.Fatal(err)
	}
	var items []ptitems.Item
	for _, id := range agingPCItems {
		items = append(items, catItem(t, id))
	}
	src := func(ch string) (ptlib.AnalogCal, error) {
		r := calstore.RangeCurrent
		if strings.HasPrefix(ch, "AO") {
			r = calstore.RangeAO020
		}
		return ptlib.AnalogCal{K: 1, Unit: "mA", FS: 20, Range: r, Nominal: r.AINominal}, nil
	}
	e := &ptlib.Engine{
		Board: b, Plan: plan, Items: items, Hold: &fakeHold{b: b}, Analog: src, NoLive: true,
		Runner: func() *ptseq.Runner { return &ptseq.Runner{Board: b, OpenPeer: refusePeer, FindCDC: noCDC} },
	}
	return e, func(line string) { sim(b, line) }
}

func agingPCPicks(high string) []ptlib.Pick {
	ext := ptitems.PeerExt
	return []ptlib.Pick{
		{ID: "din.expect", On: true, Peer: ext, Params: map[string]string{"ch": "1,2,3", ptitems.ParamHigh: high}},
		{ID: "ain.expect", On: true, Peer: ext, Params: map[string]string{ptitems.ParamIn2: "10", ptitems.ParamTol: "1"}},
		{ID: "dout.emit", On: true, Peer: ext, Params: map[string]string{"ch": "1,2"}},
		{ID: "aout.load", On: true, Peer: ext, Params: map[string]string{ptitems.ParamOut1: "10", ptitems.ParamOut2: "5"}},
		{ID: "relay.cycle", On: true, Peer: ext, Params: map[string]string{"ch": "1", "close": "1000", "open": "1000", "count": "2"}},
	}
}

func sentOf(r ptreport.Report, id string) string {
	var out []string
	for _, s := range r.Steps {
		if s.ID == id {
			for _, a := range s.Attempts {
				out = append(out, a.Sent...)
			}
		}
	}
	return strings.Join(out, " | ")
}

// Inputs as set: the inputs pass, the outputs are recorded, and each output got its command.
func TestAgingPCPassesWhenInputsMatchTheSetValues(t *testing.T) {
	e, simCmd := agingPCEngine(t)
	simCmd("sim.din 0x05")   // DI1, DI3 high
	simCmd("sim.ain 2 1240") // 1240 mV over 124 ohm: 10 mA
	rep, err := e.Run(context.Background(), agingPCPicks("1,3"), 6*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ptreport.Outcome{"din.expect": ptreport.OutcomePass, "ain.expect": ptreport.OutcomePass,
		"dout.emit": ptlib.Recorded, "aout.load": ptlib.Recorded, "relay.cycle": ptlib.Recorded}
	got := outcomes(rep)
	for id, o := range want {
		if got[id] != o {
			t.Errorf("%s is %s, want %s", id, got[id], o)
		}
	}
	for id, cmd := range map[string]string{"dout.emit": "pt.start dout", "aout.load": "pt.start aout", "relay.cycle": "pt.start relay"} {
		if s := sentOf(rep, id); !strings.Contains(s, cmd) {
			t.Errorf("%s never drove its output: %q", id, s)
		}
	}
	if s := sentOf(rep, "dout.emit"); !strings.Contains(s, "ch=1,2") || !strings.Contains(s, "duty=255") {
		t.Errorf("dout.emit drove %q, want ch=1,2 at duty 255", s)
	}
	// The tolerance is judged on the PC, never sent to the board.
	if s := sentOf(rep, "ain.expect"); !strings.Contains(s, "pt.start ain") || strings.Contains(s, "tol=") {
		t.Errorf("ain.expect sent %q, want a pt.start ain without tol=", s)
	}
	if s := sentOf(rep, "aout.load"); !strings.Contains(s, "mv=") {
		t.Errorf("aout.load sent no DAC values: %q", s)
	}
}

// A DI that reads otherwise stops the run at once; so does an AI outside its tolerance.
func TestAgingPCStopsAtTheFirstMismatch(t *testing.T) {
	for _, c := range []struct {
		name, din, ain, high, failed string
	}{
		{"DI3 high where low was set", "0x05", "1240", "1", "din.expect"},
		{"AI2 at 11 mA where 10 was set", "0x05", "1364", "1,3", "ain.expect"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, simCmd := agingPCEngine(t)
			simCmd("sim.din " + c.din)
			simCmd("sim.ain 2 " + c.ain)
			start := time.Now()
			rep, err := e.Run(context.Background(), agingPCPicks(c.high), 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d > 15*time.Second {
				t.Errorf("ran %s: the mismatch did not stop the run", d)
			}
			got := outcomes(rep)
			if got[c.failed] != ptreport.OutcomeFail {
				t.Errorf("%s is %s, want FAIL: %v", c.failed, got[c.failed], got)
			}
			for _, id := range agingPCItems {
				if id != c.failed && (got[id] == ptreport.OutcomePass || got[id] == ptlib.Recorded) {
					t.Errorf("%s is %s after the run stopped", id, got[id])
				}
			}
		})
	}
}

// A single run (the functional test) samples the set-value items for
// ptlib.ExpectOnce and judges every frame; the outputs are recorded (decision 188).
func TestAgingPCItemsRunOnce(t *testing.T) {
	for _, c := range []struct {
		name, din string
		want      ptreport.Outcome
	}{{"inputs as set", "0x05", ptreport.OutcomePass}, {"DI3 high where low was set", "0x07", ptreport.OutcomeFail}} {
		t.Run(c.name, func(t *testing.T) {
			e, simCmd := agingPCEngine(t)
			simCmd("sim.din " + c.din)
			simCmd("sim.ain 2 1240")
			start := time.Now()
			rep, err := e.Run(context.Background(), agingPCPicks("1,3")[:3], 0)
			if err != nil {
				t.Fatal(err)
			}
			got := outcomes(rep)
			if got["din.expect"] != c.want {
				t.Errorf("din.expect once is %s, want %s (all: %v)", got["din.expect"], c.want, got)
			}
			if c.want == ptreport.OutcomePass && (got["ain.expect"] != ptreport.OutcomePass || got["dout.emit"] != ptlib.Recorded) {
				t.Errorf("once: %v", got)
			}
			if d := time.Since(start); d > 20*time.Second {
				t.Errorf("a single run took %s", d)
			}
		})
	}
}
