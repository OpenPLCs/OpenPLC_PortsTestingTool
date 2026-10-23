package main

import "strings"

// checkLsePage: the R&D crystal page (decision 160, PORTTOOL-FLOW.md C.3.6
// 「晶振页」), driven by clicks. The sim's stub gives -12.33 ppm and a start
// time of 900-150*drv ms on every drive, so every row starts with no fallback;
// under the default limits (decision 164) both items pass.
func checkLsePage(p *Page) {
	Section("R&D crystal page: frequency and start-up margin")
	p.L(`.mi[data-menu="rd"]`).Click()
	if !check(p.L(lport("rtc")).Count() == 1, "the port list has the clock (crystal) port") {
		return
	}
	p.L(lport("rtc")).Click()
	p.Wait(300)
	check(p.L("#rtop .phl h2").Text() == "时钟（晶振）", "its page is called 时钟（晶振）", p.L("#rtop .phl h2").Text())
	ids := attrs(p.L("#itemcards .item[data-item]"), "data-item")
	check(eqStrings(ids, "rtc.lsefreq", "rtc.lsemargin"), "two cards: frequency deviation and start-up margin", show(ids))
	// Decision 166: no manual drive block; the margin card takes a drive and a count as parameters.
	check(p.L("#itemcards .item").Count() == 2 && p.L("#itemcards [data-lsedrive]").Count() == 0 &&
		p.L(`#itemcards .item[data-item="rtc.lsemargin"] select[data-libparam="rtc.lsemargin.drv"]`).Count() == 1 &&
		p.L(`#itemcards .item[data-item="rtc.lsemargin"] [data-libparam="rtc.lsemargin.count"]`).Count() == 1,
		"no manual drive block: the margin card has a drive and a times parameter")

	card := func(id string) string { return `#itemcards .item[data-item="` + id + `"]` }
	run := func(id string) bool {
		pill := p.L(card(id) + " .ih .pill")
		check(strings.Contains(pill.Text(), "未测"), id+" starts as not run", pill.Text())
		p.L(card(id) + ` button[data-startitem="` + id + `"]`).Click()
		pills := libWaitDone(p, []string{id}, 60)
		return check(strings.Contains(pills[0], "pass"), id+" passes under its default limits", pills[0])
	}
	cells := func(sel string) [][]string {
		var out [][]string
		for _, r := range p.L(sel + " tr").AllTexts() {
			out = append(out, strings.Split(strings.TrimSpace(r), "\t"))
		}
		return out
	}

	if run("rtc.lsefreq") {
		rows := cells(card("rtc.lsefreq") + ` [data-lse="freq"]`)
		check(len(rows) == 4 && rows[1][len(rows[1])-1] == "32767.596" && rows[2][len(rows[2])-1] == "-12.33" &&
			rows[3][len(rows[3])-1] == "1000" && strings.HasPrefix(rows[1][0], "频率") &&
			strings.HasPrefix(rows[2][0], "偏差") && strings.HasPrefix(rows[3][0], "量了多久"),
			"the frequency card shows 32767.596 Hz, -12.33 ppm, measured over 1000 ms", show(rows))
		check(strings.Contains(p.L(card("rtc.lsefreq")+` [data-lse="freq"] .hint`).Text(), "主晶振的固定误差在偏差里"),
			"and says the main crystal's fixed error is in the deviation and cancels out in the drift")
	}
	if run("rtc.lsemargin") {
		rows := cells(card("rtc.lsemargin") + ` [data-lse="margin"]`)
		// Default: all drives, once. The sim starts every drive; mlow is restarted twice
		// (its turn, then the switch back from high).
		want := [][]string{{"驱动档", "重启几次", "起振几次", "没起来或退回中低档", "起振用时 最小 / 最大 / 平均（ms）", "频率偏差 最小 / 最大 / 平均（ppm）"},
			{"低", "1", "1", "0", "900 / 900 / 900", "-12.33 / -12.33 / -12.33"}, {"中低", "2", "2", "0", "750 / 750 / 750", "-12.33 / -12.33 / -12.33"},
			{"中高", "1", "1", "0", "600 / 600 / 600", "-12.33 / -12.33 / -12.33"}, {"高", "1", "1", "0", "450 / 450 / 450", "-12.33 / -12.33 / -12.33"}}
		same := len(rows) == len(want)
		for i := 0; same && i < len(rows); i++ {
			same = strings.Join(rows[i], "|") == strings.Join(want[i], "|")
		}
		check(same, "the margin card has one row per restarted drive, mlow twice (its turn and the switch back)", show(rows))
	}
	// Decision 166: the card says which drive the board is on (mlow after "all"). The buttons follow
	// the dropdown: 全部 -> 开始 and Restore greyed; one level -> 设置 and Restore clickable.
	mc := card("rtc.lsemargin")
	startLabel := func() string { return p.L(mc + " button[data-startitem]").Text() }
	check(p.L(mc+" [data-lsecur]").Count() == 1 && p.L(mc+" [data-lsecur]").Text() == "当前档位：中低",
		"the card shows 当前档位：中低", p.L(mc+" [data-lsecur]").Text())
	check(startLabel() == "开始" && p.L(mc+" [data-lserestore]").Disabled(),
		"with 全部: the button beside the dropdown says 开始; the board is on 中低 so 恢复出厂档位 is greyed", startLabel())
	check(p.L(mc+" .ih button[data-startitem]").Count() == 0 && p.L(mc+` select[data-libparam="rtc.lsemargin.drv"] ~ button[data-startitem]`).Count() == 1,
		"Start sits beside the dropdown, not in the card header")
	p.L(mc + ` select[data-libparam="rtc.lsemargin.drv"]`).Select("mlow")
	p.Wait(200)
	check(startLabel() == "设置" && p.L(mc+" [data-lserestore]").Disabled(),
		"with one level chosen the button says 设置; 恢复出厂档位 follows the board's drive (中低), not the dropdown", startLabel())
	p.L(mc + ` select[data-libparam="rtc.lsemargin.drv"]`).Select("all")
	p.Wait(200)
	check(p.L(card("rtc.lsefreq")+" .ih button[data-startitem]").Count() == 0 &&
		p.L(card("rtc.lsefreq")+` select[data-libparam="rtc.lsefreq.minutes"] ~ button[data-startitem]`).Count() == 1,
		"the frequency card's Start sits beside its minutes dropdown, not in the header")
	// Decision 166: what this run was set to, and what the verdict rests on.
	check(p.L(mc+" [data-lsesettings]").Text() == "本次设置：档位 全部，次数 1，起振用时上限 2000 ms",
		"the card says the settings of this run", p.L(mc+" [data-lsesettings]").Text())
	check(p.L(mc+" [data-lsebasis]").Text() == "判定依据：中低档起振用时 最大 750 ms，上限 2000 ms" &&
		strings.Contains(p.L(mc+" [data-lse=\"margin\"]").Text(), "中低档多的一次是最后切回的"),
		"and the basis: the slowest mlow start against the limit, with the extra switch-back noted", p.L(mc+" [data-lsebasis]").Text())
	// The card's own reads of the current drive (a bare pt.run lse) are not part of the run.
	sent := lseSent(p)
	if len(sent) > 10 {
		sent = sent[len(sent)-10:]
	}
	check(strings.Join(sent, ",") == "pt.run lse.freq,pt.run lse drv=low,pt.run lse.freq,pt.run lse drv=mlow,pt.run lse.freq,pt.run lse drv=mhigh,pt.run lse.freq,pt.run lse drv=high,pt.run lse.freq,pt.run lse drv=mlow",
		"the log shows what was sent to the board, in order", strings.Join(sent, ","))

	// One drive (decision 166): ONE restart, then count frequency measurements; the board stays there.
	before := len(lseSent(p))
	p.L(mc + ` select[data-libparam="rtc.lsemargin.drv"]`).Select("high")
	p.L(mc + ` [data-libparam="rtc.lsemargin.count"]`).Fill("3")
	p.Wait(200)
	p.L(mc + " button[data-startitem]").Click()
	_ = try(func() {
		p.WaitFunc(`() => document.querySelector('#itemcards .item[data-item="rtc.lsemargin"] [data-lsecur]').textContent === "当前档位：高"`, nil, 30000)
	})
	cmds := lseSent(p)
	if len(cmds) >= before+4 {
		cmds = cmds[before:]
	}
	check(strings.Join(cmds, ",") == "pt.run lse drv=high,pt.run lse.freq,pt.run lse.freq,pt.run lse.freq",
		"one drive: a single restart, then 3 frequency measurements, no switch back", strings.Join(cmds, ","))
	rows := cells(mc + ` [data-lse="margin"]`)
	check(len(rows) == 2 && rows[1][0] == "高" && rows[1][1] == "1" && rows[1][2] == "1" && rows[1][5] == "-12.33 / -12.33 / -12.33",
		"its row shows one restart and the three measurements", show(rows))
	check(p.L(mc+" [data-lsecur]").Text() == "当前档位：高" && !p.L(mc+" [data-lserestore]").Disabled(),
		"the board stays on 高 and 恢复出厂档位 can be clicked", p.L(mc+" [data-lsecur]").Text())
	p.L(mc + " [data-lserestore]").Click()
	_ = try(func() {
		p.WaitFunc(`() => document.querySelector('#itemcards .item[data-item="rtc.lsemargin"] [data-lsecur]').textContent === "当前档位：中低"`, nil, 15000)
	})
	check(p.L(mc+" [data-lsecur]").Text() == "当前档位：中低", "Restore puts the board back on 中低", p.L(mc+" [data-lsecur]").Text())
	p.L(mc + ` select[data-libparam="rtc.lsemargin.drv"]`).Select("all")
}

// lseSent is the lse commands the log shows as sent, without the bare drive reads.
func lseSent(p *Page) []string {
	var out []string
	for _, c := range sentLines(p.L("#log").Text(), "pt.run lse") {
		if c != "pt.run lse" {
			out = append(out, c)
		}
	}
	return out
}

// sentLines is every command the log shows as sent that starts with prefix, in order.
func sentLines(log, prefix string) []string {
	var out []string
	for _, l := range splitLines(log) {
		if i := strings.Index(l, "> "+prefix); i >= 0 {
			out = append(out, strings.TrimSpace(l[i+2:]))
		}
	}
	return out
}
