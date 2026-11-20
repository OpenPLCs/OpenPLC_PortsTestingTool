package main

import "strings"

// checkRDAging: R&D aging's two, one at a time; with the PC the AI / AO / DI /
// DO cards say outside instruments; a tick that cannot be set says why under
// it (decision 187, PORTTOOL-FLOW.md C.3.8).
func checkRDAging(p *Page) {
	Section("R&D aging (decision 187)")
	openSub(p, "agingpc")
	p.WaitFunc("() => !!(fullData && fullData.steps)", nil, 0)

	// One of the two: ticking 7 drops 4.
	p.Eval("() => { fullPick[4] = true; renderFullTop(); }")
	box7 := p.L(`#subtiles input[data-fullstep="7"]`)
	if check(box7.Enabled(), "with the PC can be ticked by default (the one-way items start unticked)",
		cut(p.L("#subtiles").Text(), 300)) {
		box7.SetChecked(true)
		p.Wait(200)
		check(p.Eval("() => !fullPick[4] && !!fullPick[7]").Truthy(), "ticking with the PC unticks with the reference board")
	}

	// The cards on this page name the outside instrument as their source.
	p.L(lport("din")).Click()
	p.Wait(300)
	src := p.L(`#itemcards [data-cfgsrc="din"]`)
	check(src.Count() == 1 && strings.Contains(src.Text(), "外部仪器"), "the DI card's source is the outside instrument", cut(p.L("#itemcards").Text(), 200))

	// A ticked DI check with no levels set: step 7 is greyed, the reason under the tick and on hover.
	pick := p.L(`#itemcards input[data-pick="din.expect"]`)
	if check(pick.Count() == 1 && !pick.Checked(), "DI against set levels starts unticked") {
		pick.SetChecked(true)
		_ = try(func() {
			p.WaitFunc("() => !!document.querySelector('#subtiles [data-subwhy=\"7\"]')", nil, 8000)
		})
		why := p.L(`#subtiles [data-subwhy="7"]`)
		check(why.Count() == 1 && strings.Contains(why.Text(), "目标值没填") && box7.Disabled(),
			"the greyed tick says why right under it", cut(p.L("#subtiles").Text(), 300))
		check(why.Count() == 1 && box7.Attr("title") == why.Text(), "and the same words on hover")
		p.L(`#itemcards input[data-pick="din.expect"]`).SetChecked(false)
		_ = try(func() {
			p.WaitFunc("() => !document.querySelector('#subtiles input[data-fullstep=\"7\"]').disabled", nil, 8000)
		})
	}

	p.L(`#subtiles input[data-fullstep="7"]`).SetChecked(false)
}

// checkRDAgingRunLock: while R&D aging runs, the other menus do not take the page away (decision 187).
func checkRDAgingRunLock(p *Page) {
	Section("R&D aging while it runs")
	openSub(p, "agingpc")
	p.WaitFunc("() => !!(fullData && fullData.steps)", nil, 0)
	p.L(`#subtiles input[data-fullstep="7"]`).SetChecked(true)
	p.L("#shellstart").Click()
	if try(func() { p.WaitFunc("() => fullRunning()", nil, 10000) }) != nil {
		check(false, "R&D aging with the PC starts", cut(p.L("#fullbody").Text(), 300))
		return
	}
	for _, k := range []string{"rd", "full"} {
		p.L(`.mi[data-menu="` + k + `"]`).Click()
		p.Wait(200)
		check(p.Eval("() => view").S() == "agingpc", "while it runs, clicking "+k+" keeps the page on R&D aging")
	}
	p.L("#shellstart").Click() // 停止
	_ = try(func() { p.WaitFunc("() => !fullRunning()", nil, 30000) })
	check(!p.Eval("() => fullRunning()").Truthy(), "stop ends it")
	p.L(`#subtiles input[data-fullstep="7"]`).SetChecked(false)
}
