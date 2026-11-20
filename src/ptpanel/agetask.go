package ptpanel

import (
	"fmt"
	"strconv"
	"strings"

	"PortTool/src/ptcheck"
	"PortTool/src/ptplan"
)

// The aging task the board runs by itself: one line per item,
// "<code> <target> <args>|<field>:<op>:<value>[:<max>];...", the limits taken
// from the criteria plan (decision 190 「老化判据」, PORTTOOL-FLOW.md C.3.9).

// agingSpec is each aging item: its LED3 code (decision 189), the criteria
// plan step its checks come from, and the firmware target when the plan's step
// is a session the board cannot run alone.
var agingSpec = []struct {
	Port   string
	Code   int
	Step   string
	Target string
	Args   string
}{
	{"temp", 1, "temperature", "temp.read", "ch=1,2"},
	{"sd", 2, "sd-integrity", "", ""},
	{"sdram", 3, "sdram-stress", "", ""},
	{"rtc", 4, "rtc", "", ""},
}

// agingTask builds the task lines for the ports not switched off.
func agingTask(plan *ptplan.Plan, off map[string]bool) ([]string, error) {
	steps := map[string]ptplan.Step{}
	for _, st := range plan.Steps {
		steps[st.ID] = st
	}
	var lines []string
	for _, a := range agingSpec {
		if off[a.Port] {
			continue
		}
		st, ok := steps[a.Step]
		if !ok {
			return nil, fmt.Errorf("criteria plan %q has no step %q for aging item %s", plan.Name, a.Step, a.Port)
		}
		target, args := a.Target, a.Args
		if target == "" {
			target, args = st.Target, strings.Join(st.ParamArgs(), " ")
		}
		var checks []string
		for _, c := range st.Checks {
			s, err := agingCheck(c)
			if err != nil {
				return nil, fmt.Errorf("aging item %s: %w", a.Port, err)
			}
			checks = append(checks, s)
		}
		if len(checks) == 0 {
			return nil, fmt.Errorf("aging item %s: step %q has no checks", a.Port, a.Step)
		}
		line := fmt.Sprintf("%d %s %s|%s", a.Code, target, args, strings.Join(checks, ";"))
		if len(line) > 250 {
			return nil, fmt.Errorf("aging item %s: task line longer than the board takes", a.Port)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no aging item is ticked")
	}
	return lines, nil
}

func num(f *float64) string { return strconv.FormatFloat(*f, 'f', -1, 64) }

// agingCheck is one check in the board's form; only what the firmware judges.
func agingCheck(c ptcheck.Check) (string, error) {
	if strings.ContainsAny(c.Field+c.Value, ":;|") {
		return "", fmt.Errorf("check %s: value has a separator in it", c.Field)
	}
	switch c.Op {
	case ptcheck.OpEq, ptcheck.OpContains:
		return fmt.Sprintf("%s:%s:%s", c.Field, c.Op, c.Value), nil
	case ptcheck.OpRange:
		if c.Min == nil || c.Max == nil {
			return "", fmt.Errorf("check %s: the board judges a range only with both ends", c.Field)
		}
		return fmt.Sprintf("%s:range:%s:%s", c.Field, num(c.Min), num(c.Max)), nil
	}
	return "", fmt.Errorf("check %s: the board cannot judge op %q", c.Field, c.Op)
}
