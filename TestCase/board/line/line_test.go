package main

import (
	"encoding/json"
	"testing"
)

func TestTileDecodes(t *testing.T) {
	var ts []tile
	in := `[{"state":"needflash","fw":"blank","why":{"$t":"line.why.no_fw","args":null},"passed":{"1":true}},
	        {"state":"empty","why":""}]`
	if err := json.Unmarshal([]byte(in), &ts); err != nil {
		t.Fatal(err)
	}
	if ts[0].Why.Key != "line.why.no_fw" || !ts[0].Passed["1"] || ts[1].Why.Key != "" {
		t.Fatalf("decoded %+v", ts)
	}
}

func TestNotPassed(t *testing.T) {
	var r report
	_ = json.Unmarshal([]byte(`{"steps":[{"id":"a","outcome":"PASS"},{"id":"b","outcome":"SKIPPED"}]}`), &r)
	if got := notPassed(r); got != "" {
		t.Errorf("pass + skipped: %q", got)
	}
	_ = json.Unmarshal([]byte(`{"steps":[{"id":"a","outcome":"PASS"},{"id":"b","outcome":"STOPPED","reason":"x"}]}`), &r)
	if got := notPassed(r); got == "" {
		t.Error("a stopped step passed")
	}
	if got := notPassed(report{}); got == "" {
		t.Error("an empty report passed")
	}
}

func TestAgingWrittenLine(t *testing.T) {
	m := writtenRe.FindStringSubmatch("aging task written: 4 items, 3600 s")
	if m == nil || m[1] != "4" || m[2] != "3600" {
		t.Fatalf("%v", m)
	}
}
