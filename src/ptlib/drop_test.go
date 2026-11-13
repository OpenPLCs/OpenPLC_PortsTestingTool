package ptlib

import (
	"testing"

	"PortTool/src/ptcheck"
	"PortTool/src/ptitems"
	"PortTool/src/ptplan"
	"PortTool/src/ptproto"
)

// DO under load's drop_max (decision 164): the default is the plan's
// high-side-drop max, the card's value overrides it, a cleared one judges nothing.
func TestDropLimit(t *testing.T) {
	one := 1.0
	plan := ptplan.Plan{Steps: []ptplan.Step{{ID: dropStep,
		Checks: []ptcheck.Check{{Field: "value", Op: ptcheck.OpRange, Max: &one, Unit: "V"}}}}}

	var it ptitems.Item
	for _, c := range ptitems.Catalogue {
		if c.ID == "dout.load" {
			it = c
		}
	}
	if d := Defaults(it, plan, ptproto.Caps{})[ptitems.ParamDropMax]; d != "1" {
		t.Errorf("card default = %q, want the plan's 1", d)
	}
	if l := dropLimit(plan, nil); l != "1" {
		t.Errorf("no pick value: limit %q, want 1", l)
	}
	if l := dropLimit(plan, map[string]string{ptitems.ParamDropMax: ""}); l != "" {
		t.Errorf("cleared: limit %q, want empty", l)
	}
	for _, c := range []struct {
		limit string
		v     float64
		want  string // "" not judged
	}{{"1", 0.5, "pass"}, {"1", 1.5, "fail"}, {"2", 1.5, "pass"}, {"", 9, ""}} {
		got := ""
		if p := judgeDrop(c.limit, c.v); p != nil {
			got = map[bool]string{true: "pass", false: "fail"}[*p]
		}
		if got != c.want {
			t.Errorf("%g V against %q: %q, want %q", c.v, c.limit, got, c.want)
		}
	}
	if !HostParamOK("dout.load", ptitems.ParamDropMax, "") || HostParamOK("dout.load", ptitems.ParamDropMax, "-1") {
		t.Error("drop_max takes empty or a number of 0 or more")
	}
}
