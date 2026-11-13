package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deliver refuses a plan whose porttool check expects another fixture
// version than the one packed; a plan without that check passes.
func TestPlanVersions(t *testing.T) {
	dir := t.TempDir()
	write := func(name, checks string) string {
		p := filepath.Join(dir, name)
		plan := `{"schema":1,"name":"n","limit_version":"v","steps":[{"id":"identity","type":"PtRaw","command":"pt.id","checks":[` + checks + `]}]}`
		if err := os.WriteFile(p, []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.json", `{"field":"porttool","op":"eq","value":"0.18.9"}`)
	none := write("none.json", `{"field":"uid","op":"contains","value":"00"}`)
	stale := write("stale.json", `{"field":"porttool","op":"eq","value":"0.12.0"}`)

	if err := planVersions([]string{good, none}, "0.18.9"); err != nil {
		t.Errorf("matching plans refused: %v", err)
	}
	err := planVersions([]string{good, stale}, "0.18.9")
	if err == nil {
		t.Fatal("a plan expecting 0.12.0 was packed with fixture 0.18.9")
	}
	for _, want := range []string{"stale.json", "0.12.0", "0.18.9"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %s", err, want)
		}
	}
}
