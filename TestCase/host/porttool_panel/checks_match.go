package main

import (
	"strings"
)

// checkAdapterMatch: the RS485 and CAN rows are filled by matching (decision
// 146, PORTTOOL-FLOW.md C.3.6 automatic adapter matching). A row shows its
// state by its dot only, the reason on hover, and a gray row offers 重试
// (decision 170). On the simulated board the adapters are SIM-A, SIM-B and
// SIM-CAN, wired by sim.wiring; no serial port on this machine is written to.
func checkAdapterMatch(p *Page, com string) {
	Section("adapter matching (decision 146)")
	row := func(k string) *Loc { return p.L(`#connrows .crow[data-conn="` + k + `"]`) }
	why := func(k string) string {
		l := row(k).L(".dot")
		if l.Count() == 0 {
			return ""
		}
		return strings.TrimSpace(l.First().Attr("title"))
	}
	green := func(k string) bool { return row(k).L(".dot.ok").Count() == 1 }
	done := try(func() {
		p.WaitFunc(`() => { const m = connInfo.match; return !!m && !!m.at && !m.running; }`, nil, 20000)
	}) == nil
	check(done, "matching ran by itself after connecting")
	check(p.L(`#connrows [data-conn="match"]`).Count() == 0, "there is no separate 自动识别 row")
	if !isSim(com) {
		// A bench: what it finds depends on the adapters plugged in; each row is
		// green or has its reason on hover.
		for _, k := range []string{"rs485", "can"} {
			check(green(k) || why(k) != "", k+" is green or says why on hover", why(k))
		}
		return
	}

	matched := func(k string) string {
		l := p.L(`#connrows [data-matched="` + k + `"]`)
		if l.Count() == 0 {
			return ""
		}
		return strings.TrimSpace(l.First().Text())
	}
	check(matched("rs485") == "SIM-A" && green("rs485") && strings.Contains(why("rs485"), "认出来了"),
		"RS485 is found on SIM-A, green, said in words on hover", matched("rs485")+" / "+why("rs485"))
	check(matched("can") == "SIM-CAN" && green("can"), "CAN is found on SIM-CAN and green", matched("can")+" / "+why("can"))
	check(row("rs485").L("select").Count() == 0 && row("rs485").L("[data-retry]").Count() == 0,
		"a found row offers no manual choice and no 重试")
	check(!strings.Contains(row("rs485").Text(), "认出来了"), "the reason is not written on the row", row("rs485").Text())

	// Typed into the command line, as a person would.
	typeSim := func(cmd string) {
		if !p.L("#raw").Visible() {
			p.L(`.mi[data-menu="rd"]`).Click()
		}
		p.L("#raw").Fill(cmd)
		p.L("#send").Click()
		p.Wait(500)
	}
	// Waits until RS485's hover reason has changed and says want.
	settled := func(want, before string) bool {
		return try(func() {
			p.WaitFunc(`([want, before]) => {
				const d = document.querySelector('#connrows .crow[data-conn="rs485"] .dot');
				const m = connInfo.match;
				return d && m && !m.running && d.title.includes(want) && d.title !== before;
			}`, []string{want, before}, 20000)
		}) == nil
	}
	// A green row has no 重试, so the first re-run after rewiring goes through
	// the same endpoint the button calls.
	rematchByAPI := func(want string) bool {
		before := why("rs485")
		p.Eval(`() => { api("/api/match", {}).then(j => { connInfo.match = j.match; refreshConnRows(true); }); }`)
		return settled(want, before)
	}
	// The gray row's own 重试.
	retry := func(want string) bool {
		before := why("rs485")
		b := row("rs485").L("[data-retry]")
		if !check(b.Count() == 1, "the gray RS485 row offers 重试") {
			return false
		}
		b.First().Click()
		return settled(want, before)
	}

	// Swapped: the CANable on the RS485 terminals, SIM-A on CAN.
	typeSim("sim.wiring SIM-A=can SIM-CAN=rs485")
	ok := rematchByAPI("SIM-CAN")
	check(ok && strings.Contains(why("rs485"), "SIM-CAN 的暗号出现在 RS485 端子上"),
		"crossed wiring: RS485's hover says whose code word it heard", why("rs485"))
	check(strings.Contains(why("can"), "SIM-A 的暗号出现在 CAN 端子上"), "and CAN's says the same about SIM-A", why("can"))
	check(!green("rs485") && matched("rs485") == "" && row("rs485").L("select").Count() == 1,
		"a row that was not found is gray and falls back to a manual choice")

	// SIM-A's cable out: RS485 heard nothing.
	typeSim("sim.wiring SIM-A=none SIM-CAN=can")
	ok = retry("什么都没收到")
	check(ok && strings.Contains(why("rs485"), "C10,C11"), "nothing heard: RS485's hover says so and names its terminals", why("rs485"))
	check(matched("can") == "SIM-CAN" && green("can"), "CAN is found again once the CANable is back", matched("can"))

	// Back to the default wiring for the checks after this one.
	typeSim("sim.wiring SIM-A=rs485 SIM-B=none SIM-CAN=can")
	ok = retry("认出来了")
	check(ok && matched("rs485") == "SIM-A" && green("rs485"), "重试 after a fix finds RS485 on SIM-A", why("rs485"))
}
