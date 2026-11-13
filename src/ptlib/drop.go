package ptlib

// DO under load with a person as the peer (PORTTOOL-FLOW.md C.3.6
// "DO 带负载（研发）"): one channel on at a time, deadman held, the person
// measures the drop between 24 V and the terminal while it is on, judged
// against the card's drop_max; then off and the next channel.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"PortTool/src/ptboard"
	"PortTool/src/ptcheck"
	"PortTool/src/pthold"
	"PortTool/src/ptitems"
	"PortTool/src/ptplan"
	"PortTool/src/ptreport"
)

// dropWalk are items a person measures channel by channel while the output is on.
var dropWalk = map[string]bool{"dout.load": true}

// dropStep is the plan step that asks for a typed drop and holds drop_max's default.
const dropStep = "high-side-drop"

// DropDefault is drop_max's default: the plan's high-side-drop value max, so
// the limit has one source (decision 164); "" when the plan has none.
func DropDefault(plan ptplan.Plan) string {
	for _, st := range plan.Steps {
		if st.ID != dropStep {
			continue
		}
		for _, c := range st.Checks {
			if c.Field == "value" && c.Max != nil {
				return strconv.FormatFloat(*c.Max, 'f', -1, 64)
			}
		}
	}
	return ""
}

// dropLimit is the drop limit in force: the pick's drop_max when it has one,
// even cleared, else the plan's default.
func dropLimit(plan ptplan.Plan, params map[string]string) string {
	if v, ok := params[ptitems.ParamDropMax]; ok {
		return strings.TrimSpace(v)
	}
	return DropDefault(plan)
}

// dropChecks judges a typed drop against limit; nil when it is cleared (record only).
func dropChecks(limit string) []ptcheck.Check {
	max, err := strconv.ParseFloat(limit, 64)
	if err != nil {
		return nil
	}
	return []ptcheck.Check{{Field: "value", Op: ptcheck.OpRange, Max: &max, Unit: "V"}}
}

// judgeDrop is a typed drop against limit; nil when the limit is cleared.
func judgeDrop(limit string, v float64) *bool {
	checks := dropChecks(limit)
	if checks == nil {
		return nil
	}
	_, pass := ptcheck.EvalAll(checks, func(f string) (string, bool) {
		return strconv.FormatFloat(v, 'f', 4, 64), f == "value"
	})
	return &pass
}

// DropRow is one channel of the walk, for the card.
type DropRow struct {
	Ch   int      `json:"ch"`
	Duty int      `json:"duty"`           // raw duty count sent while it was on
	Drop *float64 `json:"drop,omitempty"` // the person's reading, V; nil when skipped
	Pass *bool    `json:"pass,omitempty"`
}

// DropResult is what the card draws.
type DropResult struct {
	Max  string    `json:"max"` // drop_max in force, V; "" records only
	On   int       `json:"on"`  // the channel switched on right now, 0 between
	Rows []DropRow `json:"rows"`
}

func dropChannels(params map[string]string) []int {
	var out []int
	for _, f := range strings.Split(params["ch"], ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n >= 1 && n <= 8 {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		out = []int{1, 2, 3, 4, 5, 6, 7, 8}
	}
	return out
}

// fillDrop keeps only the drop step for a person's per-channel walk.
func fillDrop(j *job) {
	for _, st := range j.steps {
		if st.ID == dropStep {
			j.steps, j.drop = []ptplan.Step{st}, true
			return
		}
	}
}

func (e *Engine) runDrop(ctx context.Context, j *job) (ptreport.Outcome, string, string) {
	step := j.steps[0]
	res := DropResult{Max: dropLimit(e.Plan, j.params)}
	checks := dropChecks(res.Max)
	show := func() {
		c := res
		c.Rows = append([]DropRow(nil), res.Rows...)
		e.change(j.item.ID, func(s *State) { s.Drop = &c })
	}
	show()
	att := ptreport.Attempt{N: 1, StartedAt: time.Now()}
	finish := func(o ptreport.Outcome, why string) (ptreport.Outcome, string, string) {
		att.Outcome, att.Err = o, why
		att.DurationMS = time.Since(att.StartedAt).Milliseconds()
		e.addAttempts(j, att)
		return o, "", why
	}
	send := func(cmd string) error {
		att.Sent = append(att.Sent, cmd)
		lines, err := e.Board.Send(cmd, ptboard.ExpectOne, ptboard.DefaultTimeout)
		att.Raw = append(att.Raw, lines...)
		return err
	}

	// 24 V stays on while a person measures: the deadman drops it if the PC goes away.
	att.Sent = append(att.Sent, pthold.Command())
	keeper, err := pthold.Arm(e.Board)
	if err != nil {
		return finish(ptreport.OutcomeError, "could not arm the deadman: "+err.Error())
	}
	defer keeper.Release()
	events, unsub := e.Board.Subscribe(256)
	defer unsub()

	failed, judged, blank := false, 0, 0
	for _, ch := range dropChannels(j.params) {
		n := strconv.Itoa(ch)
		if err := send("pt.start dout mode=hold ch=" + n + " duty=" + n + ":255"); err != nil {
			_ = send("pt.stop dout")
			return finish(ptreport.OutcomeError, err.Error())
		}
		if _, err := waitFrames(ctx, events, "dout", 1, 5*time.Second); err != nil {
			_ = send("pt.stop dout")
			return finish(frameOutcome(ctx, err), err.Error())
		}
		res.On = ch
		show()
		a, err := e.Ask(ctx, Question{Item: j.item.ID, Kind: ptitems.AnswerNumber, Prompt: step.Prompt,
			Unit: "V", Channel: "DO" + n, Walk: "drop"})
		_ = send("pt.stop dout")
		res.On = 0
		if err != nil {
			show()
			return finish(ptreport.OutcomeStopped, "")
		}
		row := DropRow{Ch: ch, Duty: 255}
		typed := strings.TrimSpace(a.Value)
		att.SetExtra("drop"+n, typed)
		if v, perr := strconv.ParseFloat(typed, 64); perr == nil && typed != "" && checks == nil {
			row.Drop = &v // drop_max cleared: recorded, not judged (decision 164)
		} else if perr == nil && typed != "" {
			got, pass := ptcheck.EvalAll(checks, func(f string) (string, bool) {
				if f == "value" {
					return typed, true
				}
				return "", false
			})
			att.Checks = append(att.Checks, got...)
			row.Drop, row.Pass = &v, &pass
			failed = failed || !pass
			judged++
		} else {
			blank++
		}
		res.Rows = append(res.Rows, row)
		switch {
		case row.Pass != nil && *row.Pass:
			e.setChan(j, ch, ptreport.OutcomePass, "")
		case row.Pass != nil:
			e.setChan(j, ch, ptreport.OutcomeFail, "")
		case row.Drop != nil:
			e.setChan(j, ch, Recorded, "")
		case e.RD:
			e.setChan(j, ch, ptreport.OutcomeStopped, ReasonStopped) // not measured to the end (decision 129)
		}
		show()
	}
	switch {
	case failed:
		return finish(ptreport.OutcomeFail, "")
	case blank > 0 && e.RD:
		return finish(ptreport.OutcomeStopped, "") // not measured to the end (decision 129)
	case judged == 0:
		return finish(Recorded, "")
	}
	return finish(ptreport.OutcomePass, "")
}
