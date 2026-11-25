package main

import (
	"reflect"
	"testing"
)

// The report as fixture 0.18.10 printed it on 2026-11-25, with the line
// before it and the banner after a reset.
var realReport = []string{
	"OK rtc.read init=0 clk=lse date=00-01-02 time=18:49:00",
	"--- UsageFault ---",
	"frame @ 0x2407FF50 (MSP)  EXC_RETURN = 0xFFFFFFE9",
	"R0  = 0x00000001  R1 = 0x24000AF8  R2 = 0x00000000  R3 = 0x00000000",
	"R12 = 0x00000000  LR = 0x08008177  PC = 0x08008176  xPSR = 0x21000000",
	"CFSR = 0x01000000  HFSR = 0x00000000",
	"  UsageFault: UNALIGNED",
	"=== port tool 0.18.10 ===",
}

func TestFaultReportIsKeptAndDecoded(t *testing.T) {
	var kept []string
	for _, l := range realReport {
		if inFault(l, len(kept) > 0) {
			kept = append(kept, l)
		}
	}
	want := realReport[1:7]
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept lines:\n%q\nwant:\n%q", kept, want)
	}
	if got := reportAddrs(kept); !reflect.DeepEqual(got, []string{"0x08008177", "0x08008176"}) {
		t.Fatalf("addresses %v, want LR then PC", got)
	}
}

func TestExceptionNames(t *testing.T) {
	for v, want := range map[uint32]string{0: "none, thread mode", 3: "HardFault", 6: "UsageFault", 16: "IRQ 0"} {
		if got := excName(v); got != want {
			t.Errorf("excName(%d) = %q, want %q", v, got, want)
		}
	}
}
