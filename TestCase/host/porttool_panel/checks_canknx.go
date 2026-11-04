package main

import (
	"strings"
)

// checkCanWindow: the CAN analyser window (decisions 111, 172, PORTTOOL-FLOW.md
// C.3.6 "CAN 分析"): the card's 打开高级窗口 starts can.bus and opens the
// window without Start (decision 171); a box the page refuses says why under
// that box; one send form with a direction, each direction's frame is matched
// on the other side and the line under the form says so; 停止连续发 stops both
// directions' repeats; the record is a list of fixed height; ending it records.
// On the simulated board the analyser end is the panel-side wire (simboard),
// not a CANable - no bit rate, no transceiver.
func checkCanWindow(p *Page, com string) {
	Section("CAN: the analyser window")
	if !isSim(com) {
		check(true, "a real bench: needs the CANable - not run here")
		return
	}
	p.L(`.mi[data-menu="rd"]`).Click()
	p.L(lport("can")).Click()
	p.Wait(300)
	if !check(p.L(`#itemcards .item[data-item="can.bus"]`).Count() == 1, "the CAN port has the analyser card") {
		return
	}
	check(p.L(`#itemcards button[data-startitem="can.bus"]`).Count() == 0, "the analyser card has no Start (decision 173)")
	p.L(`#itemcards button[data-canwin-open="can.bus"]`).Click()
	if try(func() {
		p.WaitSel("#canwin:not([hidden])", 20000)
		p.WaitFunc("() => !!(libData.states['can.bus'] || {}).running", nil, 20000)
	}) != nil {
		check(false, "打开高级窗口 starts the item and opens the window without Start", cut(p.Eval("libData.states['can.bus']").String(), 300))
		return
	}
	check(true, "打开高级窗口 starts the item and opens the window without Start")
	check(p.L("#cwrepf").Hidden(), "the interval and count are hidden until 连续发 is ticked")
	dir := func(side string) { p.L(`#canwin button[data-cwdir="` + side + `"]`).Click() }
	fill := func(id, data string) {
		p.L(`#canwin input[data-cw="id"]`).Fill(id)
		p.L(`#canwin input[data-cw="data"]`).Fill(data)
	}
	// Decision 171: more than one send needs an interval of at least 50 ms;
	// the refusal shows under the interval box, nothing is sent.
	dir("board")
	fill("123", "112233")
	p.L("#cwrep").SetChecked(true)
	p.L(`#canwin input[data-cw="every"]`).Fill("1")
	p.L(`#canwin input[data-cw="count"]`).Fill("5")
	p.L("#cwsend").Click()
	everyErr := p.L(`#canwin [data-cwerr="every"]`)
	check(strings.Contains(everyErr.Text(), "50"), "an interval under 50 ms with a count of 5 is refused under the interval box", everyErr.Text())
	check(p.L("#cwerr").Hidden(), "and not on the window's error line", p.L("#cwerr").Text())
	check(p.Eval(`document.querySelector("#cwsend").dataset.stop !== "1"`).B(), "and nothing repeats")
	p.L("#cwrep").SetChecked(false)
	fill("123", "11 22 33")
	p.L("#cwsend").Click()
	if try(func() {
		p.WaitSel(`#canwin tr[data-cwrow="pc-rx"][data-match="ok"]`, 10000)
		p.WaitSel("#cwresult.ok", 5000)
	}) == nil {
		check(true, "被测板 → 电脑: matched on the PC side, and the line under the form says it arrived")
	} else {
		check(false, "被测板 → 电脑: matched on the PC side, and the line under the form says it arrived",
			cut(p.L("#cwresult").Text()+" | "+p.L("#cwtable").Text(), 300))
	}
	check(p.L("#cwerr").Hidden() && everyErr.Text() == "", "a good send clears the refusals", p.L("#cwerr").Text()+" | "+everyErr.Text())
	dir("pc")
	fill("7FF", "AA")
	p.L("#cwsend").Click()
	if try(func() {
		p.WaitSel(`#canwin tr[data-cwrow="board-rx"][data-match="ok"]`, 10000)
		p.WaitFunc("() => /7FF/.test(document.querySelector('#cwresult').textContent) &&"+
			" document.querySelector('#cwresult').classList.contains('ok')", nil, 5000)
	}) == nil {
		check(true, "电脑 → 被测板: matched on the board side, and the line under the form follows the new send")
	} else {
		check(false, "电脑 → 被测板: matched on the board side, and the line under the form follows the new send",
			cut(p.L("#cwresult").Text()+" | "+p.L("#cwtable").Text(), 300))
	}
	check(strings.Contains(p.L("#cwsum-ok").Text(), "2"), "the matched count says two", p.L("#cwsum-ok").Text())
	// Decision 151 #7: 停止连续发 stops both directions' repeats, the session stays up.
	p.L("#cwrep").SetChecked(true)
	p.L(`#canwin input[data-cw="every"]`).Fill("100")
	p.L(`#canwin input[data-cw="count"]`).Fill("1000")
	for _, side := range []string{"board", "pc"} {
		dir(side)
		p.L("#cwsend").Click()
	}
	both := "() => { const c = (libData.states['can.bus'].live || {}).can || {};" +
		" return !!((c.board || {}).every && (c.pc || {}).every); }"
	if try(func() {
		p.WaitFunc(both, nil, 5000)
		p.WaitSel(`#cwsend[data-stop="1"]`, 5000)
	}) == nil {
		check(true, "both directions repeat and 发送 turns into 停止")
	} else {
		check(false, "both directions repeat and 发送 turns into 停止", cut(p.Eval("libData.states['can.bus'].live").String(), 300))
	}
	p.Wait(1500)
	check(p.Eval("(() => { const l = document.querySelector('#cwtable'); return l.scrollHeight > l.clientHeight && l.clientHeight <= 260; })()").B(),
		"the record scrolls inside a list of fixed height")
	// 停止 stops the chosen direction only; stop both, one after the other.
	for _, side := range []string{"pc", "board"} {
		dir(side)
		p.WaitSel(`#cwsend[data-stop="1"]`, 5000)
		p.L("#cwsend").Click()
	}
	if try(func() {
		p.WaitFunc("() => { const c = libData.states['can.bus'].live.can; return !c.board.every && !c.pc.every; }", nil, 5000)
		p.WaitSel(`#cwsend[data-stop="0"]`, 5000)
	}) == nil {
		check(true, "停止 stops each direction and the button turns back into 发送")
	} else {
		check(false, "停止 stops each direction and the button turns back into 发送", cut(p.Eval("libData.states['can.bus'].live").String(), 300))
	}
	check(p.Eval("!!libData.states['can.bus'].running").B(), "and the session keeps running")
	// × / Esc only hide it; the card reopens it.
	p.Press("Escape", 27)
	check(p.L("#canwin").Hidden(), "Esc closes the window")
	check(p.Eval("!!libData.states['can.bus'].running").B(), "closing the window does not end the session")
	p.L(`#itemcards button[data-canwin-open="can.bus"]`).Click()
	check(p.L("#canwin").Visible(), "the card's button reopens it")
	p.L("#cwend").Click()
	_ = try(func() { p.WaitFunc("() => !libData.running", nil, 10000) })
	check(p.Eval("libData.states['can.bus'].outcome").S() == "RECORDED", "结束 CAN 分析 records, nothing judged",
		cut(p.Eval("libData.states['can.bus']").String(), 200))
	p.L("#cwclose").Click()
	check(p.L("#canwin").Hidden(), "× hides the window")
}

// checkKnxPortVerdict: the KNX port verdict comes from the judged items alone,
// whatever the diagnostics ended as (decision 103). Runs right after
// checkKnxFrames, which leaves knx.frames STOPPED.
func checkKnxPortVerdict(p *Page) {
	Section("KNX: the port verdict ignores the diagnostics")
	if p.L(lport("knx")).Count() == 0 {
		check(true, "no KNX on this board - nothing to check")
		return
	}
	check(p.Eval("(libData.ports || {}).knx").Nil(), "a frames session stopped by hand gives the port no verdict", p.Eval("libData.ports").String())
	p.L(lport("knx")).Click()
	p.Wait(200)
	run := []string{"knx.bus", "knx.loop"} // every item the card allows at once: frames shares loop's group
	for _, it := range p.Eval("itemsOf('rd', 'knx').map(it => it.id)").Strings() {
		pick(p, it, it == run[0] || it == run[1])
	}
	runBothGroups(p, []string{"knx.bus"})
	if try(func() {
		p.WaitFunc("ids => !libData.running && ids.every(i => {"+
			" const o = (libData.states[i] || {}).outcome; return o && o !== 'SKIPPED'; })", run, 60000)
	}) != nil {
		check(false, "the KNX items finish", p.Eval("libData.states").String())
		return
	}
	st := p.Eval("ids => ids.map(i => libData.states[i].outcome)", run).Strings()
	check(eqStrings(st, "RECORDED", "PASS"), "the diagnostic only records, the loopback is judged", show(st))
	check(p.Eval("(libData.ports || {}).knx").S() == "pass", "the KNX port passes on the judged loopback alone", p.Eval("libData.ports").String())
}

// checkKnxWindow: the KNX analyser window (decision 151 #8, PORTTOOL-FLOW.md
// C.3.6 "KNX 分析"). On the simulated board this PC's side stays off and says
// why, the board's half works: a telegram the board sends shows as sent and as
// its own echo. Runs after checkKnxPortVerdict, so the record-only outcome
// cannot touch that check.
func checkKnxWindow(p *Page, com string) {
	Section("KNX: the analyser window")
	check(p.L(`#connrows [data-conn="knxusb"]`).Count() == 1, "the connection block has a KNX USB interface row")
	if !isSim(com) {
		check(true, "a real bench: needs the KNX USB interface - not run here")
		return
	}
	p.L(`.mi[data-menu="rd"]`).Click()
	if p.L(lport("knx")).Count() == 0 {
		check(true, "no KNX on this board - nothing to check")
		return
	}
	p.L(lport("knx")).Click()
	p.Wait(300)
	if !check(p.L(`#itemcards .item[data-item="knx.live"]`).Count() == 1, "the KNX port has the analyser card") {
		return
	}
	for _, it := range p.Eval("itemsOf('rd', 'knx').map(it => it.id)").Strings() {
		pick(p, it, it == "knx.live")
	}
	pressStart(p, "", "knx.live")
	if try(func() { p.WaitSel("#knxwin:not([hidden])", 20000) }) != nil {
		check(false, "Start opens the analyser window", cut(p.Eval("libData.states['knx.live']").String(), 300))
		return
	}
	check(true, "Start opens the analyser window")
	check(p.L(`#kwstatus [data-kwpc-why="sim"]`).Count() == 1, "this PC's side says the simulated board has no KNX bus", cut(p.L("#kwstatus").Text(), 300))
	check(p.L(`#knxwin [data-kwside="pc"] button[data-kwsend="pc"]`).Disabled(), "and its send button is off")
	board := p.L(`#knxwin [data-kwside="board"]`)
	board.L(`input[data-kw="ga"]`).Fill("1/2/3")
	board.L(`input[data-kw="value"]`).Fill("5")
	board.L(`input[data-kw="src"]`).Fill("1.1.250")
	board.L(`button[data-kwsend="board"]`).Click()
	if try(func() {
		p.WaitSel(`#knxwin tr[data-kwrow="board-tx"]`, 10000)
		p.WaitSel(`#knxwin tr[data-kwrow="board-rx"][data-match="self"]`, 10000)
	}) == nil {
		check(true, "the board's telegram shows as sent and as its own echo")
	} else {
		check(false, "the board's telegram shows as sent and as its own echo", cut(p.L("#kwtable").Text(), 300))
	}
	row := p.L(`#knxwin tr[data-kwrow="board-tx"]`).First().Text()
	check(strings.Contains(row, "1.1.250") && strings.Contains(row, "1/2/3") && strings.Contains(row, "5"),
		"the row has the source address, group address and value", row)
	board.L(`input[data-kw="every"]`).Fill("200")
	board.L(`input[data-kw="count"]`).Fill("1000")
	board.L(`button[data-kwsend="board"]`).Click()
	rep := `#knxwin [data-kwrepeat="board"]`
	if try(func() { p.WaitFunc("s => document.querySelector(s).textContent", rep, 5000) }) == nil {
		check(true, "the board shows it is repeating")
	} else {
		check(false, "the board shows it is repeating", p.L(rep).Text())
	}
	check(p.L("#knxwin [data-kwstop]").Count() == 0, "neither side has its own 停止连发: only 停止发包 at the top")
	p.L("#kwstopall").Click()
	if try(func() { p.WaitFunc("s => !document.querySelector(s).textContent", rep, 5000) }) == nil {
		check(true, "停止发包 stops the repeat")
	} else {
		check(false, "停止发包 stops the repeat", p.L(rep).Text())
	}
	check(p.Eval("!!libData.states['knx.live'].running").B(), "and the session keeps running")
	p.Press("Escape", 27)
	check(p.L("#knxwin").Hidden(), "Esc closes the window")
	check(p.Eval("!!libData.states['knx.live'].running").B(), "closing the window does not end the session")
	p.L(`#itemcards button[data-knxwin-open="knx.live"]`).Click()
	check(p.L("#knxwin").Visible(), "the card's button reopens it")
	p.L("#kwend").Click()
	_ = try(func() { p.WaitFunc("() => !libData.running", nil, 10000) })
	check(p.Eval("libData.states['knx.live'].outcome").S() == "RECORDED", "ending it records, nothing judged",
		cut(p.Eval("libData.states['knx.live']").String(), 200))
	p.L("#kwclose").Click()
	check(p.L("#knxwin").Hidden(), "关闭窗口 hides the window")
}

// checkKnxFrames: knx.frames stays open and lists what the board sent and heard
// on its card (decision 105). Stopped once a sent telegram shows.
// ⚠️ On a real board this transmits to group address 31/7/255, the corner a
// real installation is least likely to have assigned.
func checkKnxFrames(p *Page) {
	Section("KNX frames: the watched card")
	p.L(`.mi[data-menu="rd"]`).Click()
	if p.L(lport("knx")).Count() == 0 {
		check(true, "no KNX on this board - nothing to check")
		return
	}
	p.L(lport("knx")).Click()
	p.Wait(200)
	for _, it := range p.Eval("itemsOf('rd', 'knx').map(it => it.id)").Strings() {
		pick(p, it, it == "knx.frames")
	}
	check(p.L(`#itemcards input[data-libparam="knx.frames.ga"]`).Count() == 1, "the group address is set on the frames card")
	check(p.L(`#itemcards input[data-libparam="knx.loop.ga"]`).Count() == 0, "and not on the loopback card, where the firmware ignores it")
	pressStart(p, "", "knx.frames")
	if try(func() {
		p.WaitFunc(`() => { const p = document.querySelector('pre[data-events="knx.frames"]');
            return p && p.textContent.indexOf('knx.tx') >= 0; }`, nil, 20000)
	}) == nil {
		check(true, "the card lists the telegrams the board sent while the session is open")
	} else {
		check(false, "the card lists the telegrams the board sent while the session is open",
			p.Eval("JSON.stringify({running: libData.running, st: (libData.states || {})['knx.frames'],"+
				" msg: document.querySelector('#msgbox').innerText})").S())
	}
	r := p.Eval("libData.running")
	check(r.v == true, "and the session is still running")
	pressStart(p, "", "")
	p.WaitFunc("() => !libData.running", nil, 10000)
	check(p.Eval("libData.states['knx.frames'].outcome").S() == "STOPPED", "Stop ends it early and says so")
}

// checkRs232Baud: RS232 at another rate, picked from the board's own list, and
// the whole control port follows for the test and back (decision 104). The
// simulated board talks over a pipe, so this proves the switch handshake and
// the keepalive, not that a USB-232 adapter runs at 19200.
func checkRs232Baud(p *Page) {
	Section("RS232: picking a baud rate")
	p.L(`.mi[data-menu="rd"]`).Click()
	p.L(lport("rs232")).Click()
	p.Wait(200)
	sel := p.L(`#itemcards select[data-libparam="rs232.peer.baud"]`)
	if !check(sel.Count() == 1, "the rate is a dropdown on the RS232 card") {
		return
	}
	opts := sel.L("option").AllTexts()
	check(len(opts) == 6 && strings.Contains(opts[len(opts)-1], "转换器"), "six rates, 230400 marked as up to the adapter", show(opts))
	sel.Select("19200")
	p.L(`#itemcards input[data-pick="rs232.peer"]`).SetChecked(true)
	pressStart(p, "", "")
	if try(func() {
		p.WaitFunc("() => { const s = (libData.states || {})['rs232.peer'];"+
			" return !libData.running && s && !s.running && s.outcome; }", nil, 30000)
	}) != nil {
		check(false, "the RS232 item finishes at 19200", p.Eval("libData.states['rs232.peer']").String())
		return
	}
	st := p.Eval("libData.states['rs232.peer']")
	baud := ""
	for _, kv := range st.K("values").Arr() {
		if kv.I(0).S() == "baud" {
			baud = kv.I(1).S()
		}
	}
	check(st.K("outcome").S() == "PASS" && baud == "19200", "it passes, with frames reporting 19200", cut(st.String(), 300))
	j := p.Eval("api('/api/command', {cmd: 'pt.list'})")
	check(!j.K("error").Truthy() && !j.K("refused").Truthy(),
		"and the panel still talks to the board afterwards, at the default rate", cut(j.String(), 200))
}
