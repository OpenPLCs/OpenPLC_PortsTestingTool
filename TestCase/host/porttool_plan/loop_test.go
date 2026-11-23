package testcase

// R&D's loop-back wiring (decision 191, PORTTOOL-FLOW.md C.3.8): the board
// under test is its own reference board on the harness of decision 188.
//
// Bench: one simulated board that simboard.SelfLink wires onto itself (DOn and
// RYn on -> DIn for n = 1..6, DO7 / DO8 straight; AOn current -> AIn), or the
// same board left unwired. Neither sees a real harness, contact or load.

import (
	"context"
	"errors"
	"testing"

	"PortTool/src/ptboard"
	"PortTool/src/ptitems"
	"PortTool/src/ptlib"
	"PortTool/src/ptreport"
	"PortTool/src/ptseq"
	"PortTool/src/simboard"
)

func loopEngine(t *testing.T, b *ptboard.Board, wire func() func(), id string) *ptlib.Engine {
	t.Helper()
	e := doutEngine(t, b, nil, id, func(q ptlib.Question) ptlib.Answer {
		t.Errorf("a person was asked on the harness: %+v", q)
		return ptlib.Answer{}
	})
	e.Runner = func() *ptseq.Runner { return &ptseq.Runner{Board: b, OpenPeer: refusePeer, FindCDC: noCDC} }
	e.RD, e.SelfWire = true, wire
	return e
}

func TestLoopBackPassesDIDOAndRelaysOnOneWiredSimulatedBoard(t *testing.T) {
	b, rw := openSimRaw(t)
	for _, p := range []ptlib.Pick{
		{ID: "din.level", On: true, Peer: ptitems.PeerSelf, Params: map[string]string{"ch": "1,2,7"}},
		{ID: "dout.load", On: true, Peer: ptitems.PeerSelf, Params: map[string]string{"ch": "1,2,3,4,5,6,7,8"}},
		{ID: "relay.cycle", On: true, Peer: ptitems.PeerSelf,
			Params: map[string]string{"ch": "1,2", "close": "1000", "open": "1000", "count": "2", "by": "gap"}},
	} {
		t.Run(p.ID, func(t *testing.T) {
			e := loopEngine(t, b, func() func() { return simboard.SelfLink(rw) }, p.ID)
			rep, err := e.Run(context.Background(), []ptlib.Pick{p}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if o, st := outcomes(rep)[p.ID], chainState(e, p.ID); o != ptreport.OutcomePass {
				t.Fatalf("%s on the harness: %s / %q %s", p.ID, o, st.Reason, st.Detail)
			}
		})
	}
}

func TestLoopBackFailsWhenNoHarnessIsWired(t *testing.T) {
	b, _ := openSimRaw(t)
	e := loopEngine(t, b, nil, "din.level")
	rep, err := e.Run(context.Background(), []ptlib.Pick{{ID: "din.level", On: true, Peer: ptitems.PeerSelf, Params: map[string]string{"ch": "1"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if o, st := outcomes(rep)["din.level"], chainState(e, "din.level"); o != ptreport.OutcomeFail || st.Reason != ptlib.ReasonCheckChain {
		t.Fatalf("got %s / %q, want FAIL / %s", o, st.Reason, ptlib.ReasonCheckChain)
	}
}

func TestLoopBackAO1ReadsBackOnAI1OfTheSameBoard(t *testing.T) {
	b, rw := openSimRaw(t)
	asked := 0
	e := refAnalogEngine(t, b, nil, nil, &asked)
	e.Runner = func() *ptseq.Runner { return &ptseq.Runner{Board: b} }
	e.RD, e.SelfWire = true, func() func() { return simboard.SelfLink(rw) }
	_, st := runOne(t, e, ptlib.Pick{ID: "aout.out1", On: true, Peer: ptitems.PeerSelf})
	if st.Outcome != ptreport.OutcomePass || asked != 0 {
		t.Fatalf("aout.out1 on the harness: %s (%s %s), asked %d; want PASS, nobody asked", st.Outcome, st.Reason, st.Detail, asked)
	}
}

// The harness is R&D's alone, and one run has one reference side.
func TestLoopBackIsRefusedOutsideRDAndBesideTheReferenceBoard(t *testing.T) {
	b, fake := doutBoard(t)
	e := doutEngine(t, b, nil, "dout.load", func(ptlib.Question) ptlib.Answer { return ptlib.Answer{} })
	e.Items = append(e.Items, catalogueItem(t, "relay.cycle"))
	self := ptlib.Pick{ID: "dout.load", On: true, Peer: ptitems.PeerSelf}
	var pe *ptlib.PickError
	if _, err := e.Run(context.Background(), []ptlib.Pick{self}, 0); !errors.As(err, &pe) || pe.Code != ptlib.ReasonBadPeer {
		t.Errorf("the harness outside R&D: %v, want a %s refusal", err, ptlib.ReasonBadPeer)
	}
	e.RD = true
	mixed := []ptlib.Pick{self, {ID: "relay.cycle", On: true, Peer: ptitems.PeerRef}}
	if _, err := e.Run(context.Background(), mixed, 0); !errors.As(err, &pe) || pe.Code != ptlib.ReasonBadPeer {
		t.Errorf("harness and reference board in one run: %v, want a %s refusal", err, ptlib.ReasonBadPeer)
	}
	if len(fake.commands()) != 0 {
		t.Errorf("the board was touched: %v", fake.commands())
	}
}
