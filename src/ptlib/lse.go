package ptlib

// The crystal items (decisions 160, 162, 164; PORTTOOL-FLOW.md C.3.6 「晶振页」).
// rtc.lsefreq sends pt.run lse.freq one after another for its minutes: a
// stopped crystal, a missed capture or |ppm| over ppm_max fails it at once, a
// drift over drift_max fails a timed run at its end. rtc.lsemargin restarts
// the crystal on the chosen drive(s) count times and passes when every mlow
// restart started, did not fall back and was within start_ms_max. A cleared
// limit is not judged; with none set the item is only recorded.

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"PortTool/src/ptboard"
	"PortTool/src/ptitems"
	"PortTool/src/ptplan"
	"PortTool/src/ptreport"
)

// The crystal items (ptitems.Catalogue).
const (
	lseFreqItem   = "rtc.lsefreq"
	lseMarginItem = "rtc.lsemargin"
)

// lseFreqEvery is the least time between two measurements' starts.
const lseFreqEvery = time.Second

// lseSeriesMax is how many ppm values the card's line keeps, newest last: an
// hour at one measurement a second.
const lseSeriesMax = 3600

// LseFreqState is what the frequency card draws.
type LseFreqState struct {
	Minutes   int       `json:"minutes"` // 0 once, -1 until stopped
	Count     int       `json:"count"`   // measurements with a ppm
	ElapsedMS int64     `json:"elapsed_ms"`
	Min       float64   `json:"min"`
	Max       float64   `json:"max"`
	Avg       float64   `json:"avg"`
	Drift     float64   `json:"drift"`  // Max - Min: the main crystal's fixed error cancels out
	Series    []float64 `json:"series"` // ppm, oldest first
	sum       float64
}

// LimitOK says a limit parameter's value is usable: a number of 0 or more,
// or empty to only record (decision 164).
func LimitOK(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	x, err := strconv.ParseFloat(v, 64)
	return err == nil && x >= 0
}

// limitOf is a limit parameter's value: the pick's, else the item's default;
// ok false when it was cleared (record only).
func limitOf(it ptitems.Item, params map[string]string, k string) (float64, bool) {
	v, set := params[k]
	if !set {
		v = it.Limits[k]
	}
	x, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return x, err == nil
}

// lseFreqMinutes is the item's minutes; a missing one is once.
func lseFreqMinutes(params map[string]string) int {
	n, err := strconv.Atoi(params[ptitems.ParamMinutes])
	if err != nil {
		return 0
	}
	return n
}

// lseFreqLimits are rtc.lsefreq's limits; a negative one is not judged.
type lseFreqLimits struct{ ppm, drift float64 }

func lseFreqLimitsOf(it ptitems.Item, params map[string]string) lseFreqLimits {
	l := lseFreqLimits{-1, -1}
	if x, ok := limitOf(it, params, ptitems.ParamPpmMax); ok {
		l.ppm = x
	}
	if x, ok := limitOf(it, params, ptitems.ParamDriftMax); ok {
		l.drift = x
	}
	return l
}

func getField(vals [][2]string, k string) string {
	for _, kv := range vals {
		if kv[0] == k {
			return kv[1]
		}
	}
	return ""
}

// add takes one lse.freq reply's fields and says why it fails the item, ""
// when it does not.
func (s *LseFreqState) add(vals [][2]string, l lseFreqLimits) string {
	n := s.Count + 1
	if getField(vals, "ready") != "1" {
		return fmt.Sprintf("the crystal stopped (ready=0) on measurement %d", n)
	}
	if getField(vals, "error") == "capture_missed" {
		return fmt.Sprintf("a capture was missed (error=capture_missed) on measurement %d", n)
	}
	ppm, err := strconv.ParseFloat(getField(vals, "ppm"), 64)
	if err != nil {
		return ""
	}
	if s.Count == 0 {
		s.Min, s.Max = ppm, ppm
	}
	s.Count = n
	s.Min, s.Max = math.Min(s.Min, ppm), math.Max(s.Max, ppm)
	s.sum += ppm
	s.Avg = s.sum / float64(s.Count)
	s.Drift = s.Max - s.Min
	s.Series = append(s.Series, ppm)
	if len(s.Series) > lseSeriesMax {
		s.Series = s.Series[len(s.Series)-lseSeriesMax:]
	}
	if l.ppm >= 0 && math.Abs(ppm) > l.ppm {
		return fmt.Sprintf("deviation %.2f ppm is over ppm_max %g on measurement %d", ppm, l.ppm, n)
	}
	return ""
}

// end is the verdict of a run that measured to its end: a timed run's drift
// is judged against drift_max; PASS when any limit was judged, else recorded.
func (s *LseFreqState) end(l lseFreqLimits) (ptreport.Outcome, string) {
	timed := s.Minutes > 0
	if timed && l.drift >= 0 && s.Drift > l.drift {
		return ptreport.OutcomeFail, fmt.Sprintf("drift %.2f ppm is over drift_max %g", s.Drift, l.drift)
	}
	if l.ppm >= 0 || (timed && l.drift >= 0) {
		return ptreport.OutcomePass, ""
	}
	return Recorded, ""
}

func (e *Engine) runLseFreq(ctx context.Context, j *job) (ptreport.Outcome, string, string) {
	st := &LseFreqState{Minutes: lseFreqMinutes(j.params)}
	lim := lseFreqLimitsOf(j.item, j.params)
	start := time.Now()
	end := start.Add(time.Duration(st.Minutes) * time.Minute)
	const cmd = "pt.run lse.freq"
	for {
		att := ptreport.Attempt{StartedAt: time.Now(), Sent: []string{cmd}}
		next := att.StartedAt.Add(lseFreqEvery)
		lines, err := e.Board.Send(cmd, ptboard.ExpectOne, time.Duration(ptplan.DefaultTimeoutMS)*time.Millisecond)
		att.Raw = lines
		att.DurationMS = time.Since(att.StartedAt).Milliseconds()
		if err != nil {
			o, d := e.recordErr(j, att, err)
			return o, "", d
		}
		vals := valuesOf(att)
		why := st.add(vals, lim)
		st.ElapsedMS = time.Since(start).Milliseconds()
		att.Outcome = Recorded
		if why != "" {
			att.Outcome, att.Err = ptreport.OutcomeFail, why
		}
		e.addAttempts(j, att)
		cp := *st
		cp.Series = append([]float64(nil), st.Series...)
		e.change(j.item.ID, func(s *State) { s.Values = vals; s.LseFreq = &cp })
		switch {
		case why != "":
			return ptreport.OutcomeFail, "", why
		case st.Minutes == 0 || (st.Minutes > 0 && !time.Now().Before(end)):
			o, d := st.end(lim)
			return o, "", d
		case ctx.Err() != nil && st.Minutes < 0:
			return Recorded, "", "" // until stopped: stopping is how it ends
		case ctx.Err() != nil:
			return ptreport.OutcomeStopped, "", ""
		}
		// A board answers in about a second; a simulated one at once, which
		// would flood the log, the report and the page.
		select {
		case <-time.After(time.Until(next)):
		case <-ctx.Done():
		}
	}
}

// lseDriveRow is one drive's restarts: how many, how many started (no
// fallback), the start-up time spread and the average deviation of the
// frequency measured after each start (decision 166).
type lseDriveRow struct {
	Drv     string  `json:"drv"`
	Runs    int     `json:"runs"`
	Started int     `json:"started"`
	Failed  int     `json:"failed"`
	MinMS   float64 `json:"min_ms"`
	MaxMS   float64 `json:"max_ms"`
	AvgMS   float64 `json:"avg_ms"`
	MinPPM  float64 `json:"min_ppm"`
	MaxPPM  float64 `json:"max_ppm"`
	AvgPPM  float64 `json:"avg_ppm"`
	PPMN    int     `json:"ppm_n"` // how many frequency measurements the drive has
	sumMS   float64
	sumPPM  float64
}

// LseMarginState is what the margin card draws.
type LseMarginState struct {
	Round  int            `json:"round"` // the round in progress, 1-based
	Rounds int            `json:"rounds"`
	Drives []*lseDriveRow `json:"drives"` // in the order first restarted
	Limit  *float64       `json:"limit,omitempty"`
	// Single: one drive chosen, so Round counts frequency measurements of Rounds
	// (decision 166). Skipped: its one restart did not start, nothing was measured.
	Single  bool `json:"single,omitempty"`
	Skipped bool `json:"skipped,omitempty"`
	// mlowBad is why an mlow restart fails the item, "" while none did.
	mlowBad string
}

func (s *LseMarginState) row(drv string) *lseDriveRow {
	for _, r := range s.Drives {
		if r.Drv == drv {
			return r
		}
	}
	r := &lseDriveRow{Drv: drv}
	s.Drives = append(s.Drives, r)
	return r
}

// lseRestart is one pt.run lse drv=<x> reply: started means the crystal came
// up on that drive (ready, no fallback, no error).
func lseRestart(vals [][2]string) (started bool, ms float64, ok bool) {
	ms, err := strconv.ParseFloat(getField(vals, "start_ms"), 64)
	started = getField(vals, "ready") == "1" && getField(vals, "fallback") == "" && getField(vals, "error") == ""
	return started, ms, err == nil
}

// add takes one restart of drv: ppm is its follow-up frequency deviation when
// there was one. An mlow restart that did not start, fell back or took longer
// than limit (limit < 0: not judged) marks the item failed.
func (s *LseMarginState) add(drv string, started bool, ms float64, ppm *float64, limit float64) {
	r := s.row(drv)
	r.Runs++
	if started {
		r.Started++
		if r.Started == 1 {
			r.MinMS, r.MaxMS = ms, ms
		}
		r.MinMS, r.MaxMS = math.Min(r.MinMS, ms), math.Max(r.MaxMS, ms)
		r.sumMS += ms
		r.AvgMS = r.sumMS / float64(r.Started)
		if ppm != nil {
			s.addPPM(drv, *ppm)
		}
	} else {
		r.Failed++
	}
	if drv != "mlow" || s.mlowBad != "" {
		return
	}
	switch {
	case !started:
		s.mlowBad = fmt.Sprintf("an mlow restart did not start (restart %d)", r.Runs)
	case limit >= 0 && ms > limit:
		s.mlowBad = fmt.Sprintf("an mlow restart took %g ms, over start_ms_max %g (restart %d)", ms, limit, r.Runs)
	}
}

// addPPM adds one frequency deviation measured on drv.
func (s *LseMarginState) addPPM(drv string, ppm float64) {
	r := s.row(drv)
	if r.PPMN == 0 {
		r.MinPPM, r.MaxPPM = ppm, ppm
	}
	r.MinPPM, r.MaxPPM = math.Min(r.MinPPM, ppm), math.Max(r.MaxPPM, ppm)
	r.PPMN++
	r.sumPPM += ppm
	r.AvgPPM = r.sumPPM / float64(r.PPMN)
}

// verdict is PASS when every mlow restart was good and a limit was set;
// without a limit, or with no mlow restart at all (one other drive chosen),
// the item is only recorded.
func (s *LseMarginState) verdict(limit float64) (ptreport.Outcome, string) {
	if s.mlowBad != "" {
		return ptreport.OutcomeFail, s.mlowBad
	}
	hasMlow := false
	for _, r := range s.Drives {
		hasMlow = hasMlow || r.Drv == "mlow"
	}
	if limit < 0 || !hasMlow {
		return Recorded, ""
	}
	return ptreport.OutcomePass, ""
}

// lseSequence is the drives one round restarts.
func lseSequence(drv string) []string {
	if drv == "" || drv == "all" {
		return []string{"low", "mlow", "mhigh", "high"}
	}
	return []string{drv}
}

// lseMarginGap keeps the commands from flooding a board that answers at once.
const lseMarginGap = 200 * time.Millisecond

func (e *Engine) runLseMargin(ctx context.Context, j *job) (ptreport.Outcome, string, string) {
	count := 1
	if n, err := strconv.Atoi(j.params[ptitems.ParamCount]); err == nil && n >= 1 {
		count = n
	}
	seq := lseSequence(j.params[ptitems.ParamDrv])
	all := len(seq) > 1 // one chosen drive is left as it ends; "all" goes back to mlow
	limit := -1.0
	if x, ok := limitOf(j.item, j.params, ptitems.ParamStartMSMax); ok {
		limit = x
	}
	st := &LseMarginState{Rounds: count, Single: !all}
	if limit >= 0 {
		st.Limit = &limit
	}
	timeout := time.Duration(ptplan.DefaultTimeoutMS) * time.Millisecond
	last := ""
	publish := func(vals [][2]string) {
		cp := *st
		cp.Drives = make([]*lseDriveRow, len(st.Drives))
		for i, r := range st.Drives {
			c := *r
			cp.Drives[i] = &c
		}
		e.change(j.item.ID, func(s *State) {
			if vals != nil {
				s.Values = vals
			}
			s.LseMargin = &cp
		})
	}
	send := func(cmd string) ([][2]string, error) {
		att := ptreport.Attempt{StartedAt: time.Now(), Sent: []string{cmd}}
		lines, err := e.Board.Send(cmd, ptboard.ExpectOne, timeout)
		att.Raw = lines
		att.DurationMS = time.Since(att.StartedAt).Milliseconds()
		if err != nil {
			e.recordErr(j, att, err)
			return nil, err
		}
		att.Outcome = Recorded
		e.addAttempts(j, att)
		return valuesOf(att), nil
	}
	pause := func(c context.Context) bool {
		select {
		case <-c.Done():
			return false
		case <-time.After(lseMarginGap):
			return true
		}
	}
	// After "all", back on mlow whatever ended the run, a stop included: the
	// drive stays on the board across a reset. One chosen drive is left on the
	// board; the card's Restore button goes back (decision 166).
	back := func() {
		if !all || last == "" || last == "mlow" {
			return
		}
		vals, err := send("pt.run lse drv=mlow")
		if err != nil {
			return
		}
		started, ms, _ := lseRestart(vals)
		st.add("mlow", started, ms, nil, limit)
		publish(vals)
	}

	var failure error
	// freq measures the frequency once; ppm nil when the board gave none.
	freq := func() *float64 {
		fv, err := send("pt.run lse.freq")
		if err != nil || getField(fv, "ready") != "1" || getField(fv, "error") != "" {
			return nil
		}
		x, perr := strconv.ParseFloat(getField(fv, "ppm"), 64)
		if perr != nil {
			return nil
		}
		return &x
	}
	if !all {
		// One drive: ONE restart on it, then count frequency measurements; the
		// board stays on that drive (decision 166).
		drv := seq[0]
		vals, err := send("pt.run lse drv=" + drv)
		if err != nil {
			return ptreport.OutcomeError, "", err.Error()
		}
		last = drv
		started, ms, _ := lseRestart(vals)
		st.add(drv, started, ms, nil, limit)
		st.Skipped = !started
		publish(vals)
		for i := 1; started && i <= count && ctx.Err() == nil; i++ {
			st.Round = i
			if x := freq(); x != nil {
				st.addPPM(drv, *x)
			}
			publish(nil)
			if i < count && !pause(ctx) {
				break
			}
		}
	}
loop:
	for round := 1; all && round <= count; round++ {
		st.Round = round
		for _, drv := range seq {
			if ctx.Err() != nil {
				break loop
			}
			vals, err := send("pt.run lse drv=" + drv)
			if err != nil {
				failure = err
				break loop
			}
			last = drv
			started, ms, _ := lseRestart(vals)
			var ppm *float64
			if started {
				pause(ctx)
				ppm = freq()
			}
			st.add(drv, started, ms, ppm, limit)
			publish(vals)
			pause(ctx)
		}
	}
	back()
	publish(nil)
	switch {
	case failure != nil:
		return ptreport.OutcomeError, "", failure.Error()
	case ctx.Err() != nil:
		return ptreport.OutcomeStopped, "", ""
	}
	o, d := st.verdict(limit)
	return o, "", d
}
