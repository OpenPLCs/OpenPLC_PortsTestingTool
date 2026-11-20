package ptlib

import (
	"testing"

	"PortTool/src/ptitems"
	"PortTool/src/ptproto"
)

func frame(fields string) ptproto.Frame { return ptproto.Frame{Fields: ptproto.Fields(fields)} }

// Every selected DI must read its set level; unselected ones are not judged (decision 187).
func TestDinExpectJudgesEverySelectedInput(t *testing.T) {
	judge := dinExpectJudge([]int{1, 2, 3}, []int{1, 3})
	if _, ok, _ := judge(frame("v=0x05")); !ok {
		t.Fatal("DI1, DI3 high and DI2 low is what was set")
	}
	if _, ok, _ := judge(frame("v=0x85")); !ok {
		t.Fatal("DI8 is not selected and must not be judged")
	}
	res, ok, _ := judge(frame("v=0x07"))
	if ok || res[1].Pass || res[1].Check.Field != "DI2" {
		t.Fatalf("DI2 high where low was set: %+v", res)
	}
	if _, ok, _ := judge(frame("seq=1")); ok {
		t.Fatal("a frame without v passed")
	}
}

// Each AI with a set value is converted with the board's k, b and judged
// within tol % of full scale; ok=0 fails the frame (decision 187).
func TestAinExpectJudgesWithinTolerance(t *testing.T) {
	e := &Engine{Analog: func(ch string) (AnalogCal, error) {
		return AnalogCal{K: 1, B: 0, Unit: "mA", FS: 20, Nominal: func(raw float64) float64 { return raw / 1000 }}, nil
	}}
	e.states = map[string]*State{"ain.expect": {}}
	e.results = nil
	it := ptitems.Item{ID: "ain.expect"}
	j := &job{item: it, params: map[string]string{ptitems.ParamIn2: "10", ptitems.ParamTol: "1"}}
	judge, chs, o, _, d := e.ainJudge(j)
	if o != "" || chs != "2" {
		t.Fatalf("outcome %q %s, ch=%q", o, d, chs)
	}
	if _, ok, vals := judge(frame("ok=1 ch2=10150/100")); !ok || vals[0][1] != "10.15 mA" {
		t.Fatalf("10.15 mA is within 0.2 mA of 10: %v", vals)
	}
	if _, ok, _ := judge(frame("ok=1 ch2=10300/100")); ok {
		t.Fatal("10.3 mA passed a 0.2 mA tolerance")
	}
	if _, ok, _ := judge(frame("ok=0 ch2=10000/100")); ok {
		t.Fatal("a frame with ok=0 passed")
	}
	j.params = map[string]string{ptitems.ParamIn1: "25"}
	if _, _, o, _, _ := e.ainJudge(j); o == "" {
		t.Fatal("a set value above full scale was accepted")
	}
}

// A set-value item with nothing set refuses to start (decision 187).
func TestExpectItemsNeedTheirSetValues(t *testing.T) {
	din, ain := ptitems.Item{ID: "din.expect"}, ptitems.Item{ID: "ain.expect"}
	if !TargetMissing(din, nil) || TargetMissing(din, map[string]string{ptitems.ParamHigh: "none"}) {
		t.Fatal("din.expect: high is required, none is a value")
	}
	if !TargetMissing(ain, map[string]string{ptitems.ParamTol: "1"}) || TargetMissing(ain, map[string]string{ptitems.ParamIn1: "5"}) {
		t.Fatal("ain.expect: one set value is enough, none is not")
	}
	if HostParamOK("din.expect", ptitems.ParamHigh, "1,9") || !HostParamOK("din.expect", ptitems.ParamHigh, "1,8") {
		t.Fatal("high takes DI numbers 1..8")
	}
}
