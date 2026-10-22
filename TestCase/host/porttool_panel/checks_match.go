package main

import (
	"strings"
)

// checkAdapterMatch: the RS485 and CAN rows are filled by matching (decision
// 146, PORTTOOL-FLOW.md C.3.6 automatic adapter matching). On the simulated
// board the adapters are SIM-A, SIM-B and SIM-CAN, wired by sim.wiring; no
// serial port on this machine is written to. Clicks and typing only; the page
// is read, never driven through script.
func checkAdapterMatch(p *Page, com string) {
	Section("adapter matching (decision 146)")
	row := func(k string) *Loc { return p.L(`#connrows .crow[data-conn="` + k + `"]`) }
	why := func(k string) string {
		l := p.L(`#connrows [data-matchwhy="` + k + `"]`)
		if l.Count() == 0 {
			return ""
		}
		return strings.TrimSpace(l.First().Text())
	}
	done := try(func() { p.WaitSel(`#connrows [data-conn="match"] [data-matchstate="done"]`, 20000) }) == nil
	check(done, "matching ran by itself after connecting", row("match").Text())
	if !isSim(com) {
		// A bench: what it finds depends on the adapters plugged in; each row says something.
		check(why("rs485") != "" && why("can") != "", "each of RS485 and CAN says what matching found", why("rs485")+" / "+why("can"))
		return
	}

	matched := func(k string) string {
		l := p.L(`#connrows [data-matched="` + k + `"]`)
		if l.Count() == 0 {
			return ""
		}
		return strings.TrimSpace(l.First().Text())
	}
	check(matched("rs485") == "SIM-A" && strings.Contains(why("rs485"), "认出来了"),
		"RS485 is found on SIM-A, said in words", matched("rs485")+" / "+why("rs485"))
	check(matched("can") == "SIM-CAN" && strings.Contains(why("can"), "认出来了"),
		"CAN is found on SIM-CAN", matched("can")+" / "+why("can"))
	check(strings.Contains(row("match").Text(), "SIM-B"), "the adapter wired to nothing is named", row("match").Text())
	check(row("rs485").L("select").Count() == 0, "a found row offers no manual choice")

	// Typed into the command line, as a person would.
	typeSim := func(cmd string) {
		if !p.L("#raw").Visible() {
			p.L(`.mi[data-menu="rd"]`).Click()
		}
		p.L("#raw").Fill(cmd)
		p.L("#send").Click()
		p.Wait(500)
	}
	again := func(wantRS485 string) bool {
		before := why("rs485")
		p.L(`#connrows [data-matchagain]`).Click()
		return try(func() {
			p.WaitFunc(`([want, before]) => {
				const r = document.querySelector('#connrows [data-matchwhy="rs485"]');
				const s = document.querySelector('#connrows [data-conn="match"] [data-matchstate]');
				return r && s && s.dataset.matchstate === 'done' && r.textContent.includes(want) && r.textContent !== before;
			}`, []string{wantRS485, before}, 20000)
		}) == nil
	}

	// Swapped: the CANable on the RS485 terminals, SIM-A on CAN.
	typeSim("sim.wiring SIM-A=can SIM-CAN=rs485")
	ok := again("SIM-CAN")
	check(ok && strings.Contains(why("rs485"), "SIM-CAN 的暗号出现在 RS485 端子上"),
		"crossed wiring: RS485 says whose code word it heard", why("rs485"))
	check(strings.Contains(why("can"), "SIM-A 的暗号出现在 CAN 端子上"), "and CAN says the same about SIM-A", why("can"))
	check(row("rs485").L(".cwhy").Count() == 1 && matched("rs485") == "", "a row that was not found falls back to a manual choice")

	// SIM-A's cable out: RS485 heard nothing.
	typeSim("sim.wiring SIM-A=none SIM-CAN=can")
	ok = again("什么都没收到")
	check(ok && strings.Contains(why("rs485"), "C10,C11"), "nothing heard: RS485 says so and names its terminals", why("rs485"))
	check(matched("can") == "SIM-CAN", "CAN is found again once the CANable is back", matched("can"))

	// Back to the default wiring for the checks after this one.
	typeSim("sim.wiring SIM-A=rs485 SIM-B=none SIM-CAN=can")
	ok = again("认出来了")
	check(ok && matched("rs485") == "SIM-A", "detecting again after a fix finds RS485 on SIM-A", why("rs485"))
}
