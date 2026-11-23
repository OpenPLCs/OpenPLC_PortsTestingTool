package ptlib

// With an outside instrument (decisions 188, 191, PORTTOOL-FLOW.md C.3.8): DI and AI are
// judged frame by frame against values set on the card, the signal
// coming from an outside source; the outputs (DO, AO, relays) only run.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"PortTool/src/ptcheck"
	"PortTool/src/ptitems"
	"PortTool/src/ptplan"
	"PortTool/src/ptproto"
	"PortTool/src/ptreport"
)

const (
	dinExpectItem = "din.expect"
	ainExpectItem = "ain.expect"
)

// ExpectOnce is how long a single run samples a set-value item, every frame
// judged (PORTTOOL-FLOW.md C.3.6 「测试方式」).
const ExpectOnce = 3 * time.Second

// frameJudge judges one frame of a set-value item: the checks, whether all
// passed, and what the card shows.
type frameJudge func(f ptproto.Frame) ([]ptcheck.Result, bool, [][2]string)

// IsExpect says an item is judged against set values (decision 187).
func IsExpect(it ptitems.Item) bool { return it.ID == dinExpectItem || it.ID == ainExpectItem }

// expectMissing says a set-value item has nothing to judge against: DI
// without its high list, AI without a set value on either channel.
func expectMissing(it ptitems.Item, params map[string]string) bool {
	blank := func(k string) bool { return strings.TrimSpace(params[k]) == "" }
	switch it.ID {
	case dinExpectItem:
		return blank(ptitems.ParamHigh)
	case ainExpectItem:
		return blank(ptitems.ParamIn1) && blank(ptitems.ParamIn2)
	}
	return false
}

// expectParamOK checks din.expect's high list and ain.expect's set values;
// known false for any other parameter.
func expectParamOK(k, v string) (ok, known bool) {
	switch k {
	case ptitems.ParamHigh:
		_, err := dinChans(v)
		return err == nil, true
	case ptitems.ParamIn1, ptitems.ParamIn2:
		return LoadTargetOK(v), true
	}
	return false, false
}

// dinChans reads "1,3,8" (DI numbers) or "none".
func dinChans(v string) ([]int, error) {
	v = strings.TrimSpace(v)
	if v == "none" {
		return nil, nil
	}
	var out []int
	seen := map[int]bool{}
	for _, f := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 || n > 8 || seen[n] {
			return nil, fmt.Errorf("%q is not DI numbers 1..8 or none", v)
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

// setExpect readies a set-value job for the timed phase: its one session and
// how each frame is judged. A non-empty outcome ends the item.
func (e *Engine) setExpect(j *job) (ptreport.Outcome, string, string) {
	t := j.item.Targets[0]
	st := ptplan.Step{ID: j.item.ID, Type: ptplan.TypePtSession, Port: t.Session, Params: ptplan.Params{}, Frames: 1}
	for k, v := range t.Set {
		st.Params[k] = v
	}
	for k, v := range boardParams(j.params) {
		st.Params[k] = v
	}
	switch j.item.ID {
	case dinExpectItem:
		high, err := dinChans(j.params[ptitems.ParamHigh])
		if err != nil {
			return ptreport.OutcomeError, ReasonNoTarget, err.Error()
		}
		sel := []int{1, 2, 3, 4, 5, 6, 7, 8}
		if c := strings.TrimSpace(j.params["ch"]); c != "" {
			if sel, err = dinChans(c); err != nil {
				return ptreport.OutcomeError, "", "ch: " + err.Error()
			}
		}
		j.judge = dinExpectJudge(sel, high)
	case ainExpectItem:
		judge, chs, o, r, d := e.ainJudge(j)
		if o != "" {
			return o, r, d
		}
		st.Params["ch"] = chs
		j.judge = judge
	}
	j.steps = []ptplan.Step{st}
	return "", "", ""
}

// dinExpectJudge: every selected DI reads high exactly when it is in high, from the
// frame's eight-bit v (PORTTOOL-FLOW.md A.5 「!din」).
func dinExpectJudge(sel, high []int) frameJudge {
	want := map[int]bool{}
	for _, n := range high {
		want[n] = true
	}
	return func(f ptproto.Frame) ([]ptcheck.Result, bool, [][2]string) {
		raw, _ := ptproto.Get(f.Fields, "v")
		v, err := strconv.ParseUint(raw, 0, 8)
		var res []ptcheck.Result
		var vals [][2]string
		all := true
		for _, n := range sel {
			name := "DI" + strconv.Itoa(n)
			exp := map[bool]string{true: "1", false: "0"}[want[n]]
			r := ptcheck.Result{Check: ptcheck.Check{Field: name, Op: ptcheck.OpEq, Value: exp}}
			if err != nil {
				r.Missing, r.Why = true, "no v in the frame"
			} else {
				r.Got = strconv.FormatUint(v>>(n-1)&1, 10)
				r.Pass = r.Got == exp
				vals = append(vals, [2]string{name, r.Got})
			}
			all = all && r.Pass
			res = append(res, r)
		}
		return res, all, vals
	}
}

// ainJudge converts each AI with a set value by the board's own k, b on the
// shared range (C.3.2) and accepts tol % of full scale around it; ok=0 fails
// the frame (A.5 「!ain」). chs is the session's ch=.
func (e *Engine) ainJudge(j *job) (judge frameJudge, chs string, o ptreport.Outcome, reason, detail string) {
	tol, err := strconv.ParseFloat(strings.TrimSpace(j.params[ptitems.ParamTol]), 64)
	if err != nil || tol < 0 {
		tol = 1 // the catalogue's default
	}
	if e.Analog == nil {
		return nil, "", ptreport.OutcomeError, ReasonNotCalibrated, "no calibration source"
	}
	type chanSet struct {
		n           string
		cal         AnalogCal
		set, lo, hi float64
	}
	var set []chanSet
	var used []ptreport.AnalogUsed
	for _, n := range []string{"1", "2"} {
		v := strings.TrimSpace(j.params["in"+n])
		if v == "" {
			continue
		}
		x, _ := strconv.ParseFloat(v, 64)
		cal, err := e.Analog("AI" + n)
		if err != nil {
			o, r, d := analogErr(err)
			return nil, "", o, r, d
		}
		if x < cal.Lo || x > cal.FS {
			return nil, "", ptreport.OutcomeError, ReasonNoTarget, fmt.Sprintf("AI%s takes %g-%g %s", n, cal.Lo, cal.FS, cal.Unit)
		}
		w := tol / 100 * cal.FS
		set = append(set, chanSet{n: n, cal: cal, set: x, lo: x - w, hi: x + w})
		used = append(used, cal.used("dut", "AI"+n))
	}
	e.noteUsed(j, used...)
	var names []string
	for _, c := range set {
		names = append(names, c.n)
	}
	judge = func(f ptproto.Frame) ([]ptcheck.Result, bool, [][2]string) {
		var res []ptcheck.Result
		var vals [][2]string
		ok, _ := ptproto.Get(f.Fields, "ok")
		all := ok == "1"
		res = append(res, ptcheck.Result{Check: ptcheck.Check{Field: "ok", Op: ptcheck.OpEq, Value: "1"}, Got: ok, Pass: ok == "1"})
		for _, c := range set {
			lo, hi := c.lo, c.hi
			r := ptcheck.Result{Check: ptcheck.Check{Field: "AI" + c.n, Op: ptcheck.OpRange, Min: &lo, Max: &hi, Unit: c.cal.Unit}}
			field, have := ptproto.Get(f.Fields, "ch"+c.n)
			raw, perr := strconv.ParseFloat(strings.SplitN(field, "/", 2)[0], 64)
			if !have || perr != nil {
				r.Missing, r.Why = true, "no ch"+c.n+" in the frame"
			} else {
				got := c.cal.K*c.cal.Nominal(raw) + c.cal.B
				got = math.Round(got*1000) / 1000
				r.Got = fmtNum(got)
				r.Pass = got >= lo && got <= hi
				vals = append(vals, [2]string{"AI" + c.n, r.Got + " " + c.cal.Unit})
			}
			all = all && r.Pass
			res = append(res, r)
		}
		return res, all, vals
	}
	return judge, strings.Join(names, ","), "", "", ""
}
