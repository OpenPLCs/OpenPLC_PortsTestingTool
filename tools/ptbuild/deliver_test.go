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

func TestBoardFQBNReadsBothForms(t *testing.T) {
	const want = "OpenPLC_Alpha:stm32:OPEN-PLC:pnum=PLC_H743,usb=CDCgen"
	for name, src := range map[string]string{
		"one string": "x = 1\nFQBN = \"" + want + "\"\n",
		"split":      "x = 1\nFQBN = (\"OpenPLC_Alpha:stm32:OPEN-PLC:\"\n        \"pnum=PLC_H743,usb=CDCgen\")\n",
	} {
		core := t.TempDir()
		if err := os.MkdirAll(filepath.Join(core, "tests"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(core, "tests", "_common.py"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := boardFQBN(core)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", name, got, err, want)
		}
	}
}
