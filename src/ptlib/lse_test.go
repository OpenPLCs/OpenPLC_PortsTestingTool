package ptlib

import (
	"testing"

	"PortTool/src/ptitems"
	"PortTool/src/ptreport"
)

func lseOK(ppm string) [][2]string {
	return [][2]string{{"ready", "1"}, {"hz", "32767.596"}, {"ppm", ppm}, {"ms", "1000"}}
}

var noLimits = lseFreqLimits{-1, -1}

// A stopped crystal or a missed capture fails the frequency item whatever the
// limits; the ppm adds to the run's spread (decision 162).
func TestLseFreqVerdict(t *testing.T) {
	var s LseFreqState
	for _, p := range []string{"-12.33", "-12.10", "-12.50"} {
		if why := s.add(lseOK(p), noLimits); why != "" {
			t.Fatalf("a good measurement failed: %s", why)
		}
	}
	if s.Count != 3 || s.Min != -12.5 || s.Max != -12.1 || len(s.Series) != 3 {
		t.Errorf("stats = %+v", s)
	}
	if d := s.Drift - 0.4; d > 1e-9 || d < -1e-9 {
		t.Errorf("drift = %v, want 0.4", s.Drift)
	}
	if why := s.add([][2]string{{"ready", "0"}}, noLimits); why == "" {
		t.Error("ready=0 did not fail")
	}
	if why := s.add([][2]string{{"ready", "1"}, {"error", "capture_missed"}}, noLimits); why == "" {
		t.Error("capture_missed did not fail")
	}
	if s.Count != 3 {
		t.Errorf("a failed measurement was counted: %d", s.Count)
	}
	if o, _ := s.end(noLimits); o != Recorded {
		t.Errorf("no limit set: %s, want only recorded", o)
	}
}

// |ppm| over ppm_max fails that measurement; drift over drift_max fails a
// timed run at its end, not a single measurement (decision 164).
func TestLseFreqLimits(t *testing.T) {
	l := lseFreqLimits{ppm: 40, drift: 5}
	var once LseFreqState
	if why := once.add(lseOK("-39.99"), l); why != "" {
		t.Errorf("-39.99 ppm under 40 failed: %s", why)
	}
	if o, _ := once.end(l); o != ptreport.OutcomePass {
		t.Errorf("once within ppm_max: %s, want PASS", o)
	}
	if why := once.add(lseOK("40.5"), l); why == "" {
		t.Error("40.5 ppm over 40 did not fail")
	}

	timed := LseFreqState{Minutes: 10}
	for _, p := range []string{"-10", "-12", "-16"} {
		if why := timed.add(lseOK(p), l); why != "" {
			t.Fatalf("drift is judged at the end only: %s", why)
		}
	}
	if o, _ := timed.end(l); o != ptreport.OutcomeFail {
		t.Errorf("drift 6 over 5: %s, want FAIL", o)
	}
	if o, _ := timed.end(lseFreqLimits{ppm: -1, drift: 7}); o != ptreport.OutcomePass {
		t.Errorf("drift 6 under 7: %s, want PASS", o)
	}
	// A single measurement has no drift to judge.
	single := LseFreqState{}
	single.add(lseOK("-10"), lseFreqLimits{ppm: -1, drift: 5})
	if o, _ := single.end(lseFreqLimits{ppm: -1, drift: 5}); o != Recorded {
		t.Errorf("once with only drift_max: %s, want only recorded", o)
	}
}

// A cleared limit is not judged; a missing one takes the catalogue default.
func TestLseLimitParams(t *testing.T) {
	var it ptitems.Item
	for _, x := range ptitems.Catalogue {
		if x.ID == lseFreqItem {
			it = x
		}
	}
	if l := lseFreqLimitsOf(it, map[string]string{}); l.ppm != 40 || l.drift != 5 {
		t.Errorf("defaults = %+v, want 40 and 5", l)
	}
	if l := lseFreqLimitsOf(it, map[string]string{"ppm_max": "", "drift_max": "2"}); l.ppm >= 0 || l.drift != 2 {
		t.Errorf("cleared ppm_max still judged: %+v", l)
	}
	for v, want := range map[string]bool{"": true, "0": true, "12.5": true, "-1": false, "abc": false} {
		if got := HostParamOK(lseFreqItem, "ppm_max", v); got != want {
			t.Errorf("ppm_max %q accepted = %v, want %v", v, got, want)
		}
	}
}

// Every mlow restart must have started and been within start_ms_max; the other
// drives are only recorded (decision 166).
func TestLseMarginVerdict(t *testing.T) {
	ppm := func(x float64) *float64 { return &x }
	var good LseMarginState
	good.add("low", false, 5000, nil, 2000) // a drive that does not start: recorded only
	good.add("mlow", true, 750, ppm(-12), 2000)
	good.add("mlow", true, 2000, ppm(-14), 2000)
	good.add("high", true, 4800, nil, 2000) // slow, but not mlow
	if o, why := good.verdict(2000); o != ptreport.OutcomePass {
		t.Errorf("good mlow restarts: %s (%s), want PASS", o, why)
	}
	if o, _ := good.verdict(-1); o != Recorded {
		t.Errorf("no limit: %s, want only recorded", o)
	}
	for name, add := range map[string]func(*LseMarginState){
		"too slow":      func(s *LseMarginState) { s.add("mlow", true, 2100, nil, 2000) },
		"did not start": func(s *LseMarginState) { s.add("mlow", false, 5000, nil, 2000) },
	} {
		var s LseMarginState
		s.add("mlow", true, 750, nil, 2000)
		add(&s)
		if o, _ := s.verdict(2000); o != ptreport.OutcomeFail {
			t.Errorf("%s: %s, want FAIL", name, o)
		}
	}
	// No mlow restart at all (one other drive chosen): only recorded.
	var high LseMarginState
	high.add("high", true, 4800, nil, 2000)
	if o, _ := high.verdict(2000); o != Recorded {
		t.Errorf("only high restarted: %s, want only recorded", o)
	}
	// A cleared limit does not judge the time, but a restart that never started still fails.
	var open LseMarginState
	open.add("mlow", true, 9000, nil, -1)
	if o, _ := open.verdict(-1); o != Recorded {
		t.Errorf("slow with no limit: %s, want only recorded", o)
	}
	open.add("mlow", false, 5000, nil, -1)
	if o, _ := open.verdict(-1); o != ptreport.OutcomeFail {
		t.Errorf("a failed mlow start with no limit: %s, want FAIL", o)
	}
}

// One row per drive: runs, started, failed, time spread and the average ppm.
func TestLseMarginRows(t *testing.T) {
	ppm := func(x float64) *float64 { return &x }
	var s LseMarginState
	s.add("high", true, 400, ppm(-10), -1)
	s.add("high", true, 600, ppm(-20), -1)
	s.add("high", false, 5000, nil, -1)
	s.add("low", true, 900, nil, -1)
	if len(s.Drives) != 2 || s.Drives[0].Drv != "high" || s.Drives[1].Drv != "low" {
		t.Fatalf("rows = %+v", s.Drives)
	}
	r := s.Drives[0]
	if r.Runs != 3 || r.Started != 2 || r.Failed != 1 || r.MinMS != 400 || r.MaxMS != 600 || r.AvgMS != 500 || r.AvgPPM != -15 || r.MinPPM != -20 || r.MaxPPM != -10 {
		t.Errorf("high row = %+v", r)
	}
	if l := s.Drives[1]; l.PPMN != 0 || l.AvgPPM != 0 || l.Started != 1 {
		t.Errorf("low row = %+v", l)
	}
}

// One drive: a single restart row, then every frequency measurement adds to
// its min / max / average; mlow judges that one restart, another drive is
// only recorded (decision 166).
func TestLseMarginSingleDrive(t *testing.T) {
	var s LseMarginState
	s.add("high", true, 450, nil, 2000)
	for _, x := range []float64{-12, -14, -13} {
		s.addPPM("high", x)
	}
	r := s.Drives[0]
	if len(s.Drives) != 1 || r.Runs != 1 || r.Started != 1 || r.PPMN != 3 || r.MinPPM != -14 || r.MaxPPM != -12 || r.AvgPPM != -13 {
		t.Errorf("row = %+v", r)
	}
	if o, _ := s.verdict(2000); o != Recorded {
		t.Errorf("one other drive: %s, want only recorded", o)
	}
	var m LseMarginState
	m.add("mlow", true, 800, nil, 2000)
	m.addPPM("mlow", -12)
	if o, _ := m.verdict(2000); o != ptreport.OutcomePass {
		t.Errorf("single mlow within the limit: %s, want PASS", o)
	}
	var slow LseMarginState
	slow.add("mlow", true, 2500, nil, 2000)
	if o, _ := slow.verdict(2000); o != ptreport.OutcomeFail {
		t.Errorf("single mlow over the limit: %s, want FAIL", o)
	}
}

// A reply counts as started only when ready, with no fallback and no error.
func TestLseRestartReply(t *testing.T) {
	for _, c := range []struct {
		vals [][2]string
		want bool
	}{
		{[][2]string{{"ready", "1"}, {"start_ms", "700"}}, true},
		{[][2]string{{"ready", "1"}, {"start_ms", "5000"}, {"fallback", "mlow"}}, false},
		{[][2]string{{"ready", "0"}, {"start_ms", "5000"}}, false},
		{[][2]string{{"ready", "1"}, {"error", "drv_must_be_low_mlow_mhigh_high"}}, false},
	} {
		if got, _, _ := lseRestart(c.vals); got != c.want {
			t.Errorf("%v started = %v, want %v", c.vals, got, c.want)
		}
	}
	if got := lseSequence("all"); len(got) != 4 || got[1] != "mlow" {
		t.Errorf("all = %v", got)
	}
	if got := lseSequence("high"); len(got) != 1 || got[0] != "high" {
		t.Errorf("high = %v", got)
	}
}

// count is a whole number from 1 to 100; drv one of the listed drives; the
// relay's own "count" is not touched.
func TestLseMarginParams(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "100": true, "0": false, "101": false, "": false, "2.5": false, "x": false} {
		if got := HostParamOK(lseMarginItem, "count", v); got != want {
			t.Errorf("count %q accepted = %v, want %v", v, got, want)
		}
	}
	for v, want := range map[string]bool{"all": true, "mlow": true, "fast": false} {
		if got := HostParamOK(lseMarginItem, "drv", v); got != want {
			t.Errorf("drv %q accepted = %v, want %v", v, got, want)
		}
	}
	if !HostParamOK("relay.cycle", "count", "0") {
		t.Error("the relay's count rule changed")
	}
}

// The duration comes from the item's minutes; a missing one runs once.
func TestLseFreqMinutes(t *testing.T) {
	for v, want := range map[string]int{"": 0, "0": 0, "10": 10, "-1": -1} {
		if got := lseFreqMinutes(map[string]string{"minutes": v}); got != want {
			t.Errorf("minutes %q = %d, want %d", v, got, want)
		}
	}
	if !HostParamOK(lseFreqItem, "minutes", "60") || HostParamOK(lseFreqItem, "minutes", "5") || !HostParamOK("relay.cycle", "minutes", "5") {
		t.Error("rtc.lsefreq takes only its listed minutes; the relay keeps its own rule")
	}
}
