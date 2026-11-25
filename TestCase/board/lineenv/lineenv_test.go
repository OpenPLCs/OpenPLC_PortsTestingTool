package lineenv

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTileDecodes(t *testing.T) {
	var ts []Tile
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
	var r Report
	_ = json.Unmarshal([]byte(`{"steps":[{"id":"a","outcome":"PASS"},{"id":"b","outcome":"SKIPPED"}]}`), &r)
	if got := NotPassed(r); got != "" {
		t.Errorf("pass + skipped: %q", got)
	}
	_ = json.Unmarshal([]byte(`{"steps":[{"id":"a","outcome":"PASS"},{"id":"b","outcome":"STOPPED","reason":"x"}]}`), &r)
	if NotPassed(r) == "" {
		t.Error("a stopped step passed")
	}
	if NotPassed(Report{}) == "" {
		t.Error("an empty report passed")
	}
}

func TestAgeRead(t *testing.T) {
	a, err := ParseAgeRead([]string{"OK age state=paused secs=3600 elapsed=612 rounds=3 fails=0 items=4 code=0"})
	if err != nil || a != (AgeRead{State: "paused", Secs: 3600, Elapsed: 612, Items: 4}) {
		t.Fatalf("%+v %v", a, err)
	}
	if m := writtenRe.FindStringSubmatch("aging task written: 4 items, 3600 s"); m == nil || m[1] != "4" || m[2] != "3600" {
		t.Fatalf("%v", m)
	}
}

// As CubeProgrammer prints a -r32 read.
func TestParseWords(t *testing.T) {
	out := "Reading 32-bit memory content\n  Size          : 40 Bytes\n  Address:      : 0x24010CE8\n\n" +
		"0x24010CE8 : 00000003 00000000 0000000C 0009C40A\r\n" +
		"0x24010CF8 : 00000000 00000001 00000E10 00000004\r\n" +
		"0x24010D08 : 12345678 9ABCDEF0\r\n"
	got := parseWords(out, 0x24010CE8)
	want := []uint32{3, 0, 12, 0x9C40A, 0, 1, 0xE10, 4, 0x12345678, 0x9ABCDEF0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %x", got)
	}
}

// As arm-none-eabi-nm -l prints the fixture image, with a same-named static elsewhere.
func TestParseSymbols(t *testing.T) {
	out := "24010cfc b running\tE:/x/Debug/../TestCase/porttool/porttool_age.c:65\n" +
		"24000100 b running\tE:/x/Debug/../TestCase/porttool/porttool_knx.c:40\n" +
		"240009a0 B uwTick\tE:/x/Debug/../Drivers/STM32H7xx_HAL_Driver/Src/stm32h7xx_hal.c:59\n"
	got, err := parseSymbols(out, "porttool_age.c", []string{"running"})
	if err != nil || got["running"] != 0x24010cfc {
		t.Fatalf("%v %v", got, err)
	}
	got, err = parseSymbols(out, "", []string{"uwTick"})
	if err != nil || got["uwTick"] != 0x240009a0 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := parseSymbols(out, "porttool_age.c", []string{"fails"}); err == nil {
		t.Error("a missing symbol was not reported")
	}
}
