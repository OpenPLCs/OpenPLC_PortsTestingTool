package main

import "strings"

// checkLsePage: the R&D crystal page (decision 160, PORTTOOL-FLOW.md C.3.6
// 「晶振页」), driven by clicks. The sim's stub gives -12.33 ppm and a start
// time of 900-150*drv ms on every drive, so every row starts with no fallback.
func checkLsePage(p *Page) {
	Section("R&D crystal page: frequency and start-up margin")
	p.L(`.mi[data-menu="rd"]`).Click()
	if !check(p.L(lport("rtc")).Count() == 1, "the port list has the clock (crystal) port") {
		return
	}
	p.L(lport("rtc")).Click()
	p.Wait(300)
	check(p.L("#rtop .phl h2").Text() == "时钟（晶振）", "its page is called 时钟（晶振）", p.L("#rtop .phl h2").Text())
	ids := attrs(p.L("#itemcards .item"), "data-item")
	check(eqStrings(ids, "rtc.lsefreq", "rtc.lsemargin"), "two cards: frequency deviation and start-up margin", show(ids))

	card := func(id string) string { return `#itemcards .item[data-item="` + id + `"]` }
	run := func(id string) bool {
		pill := p.L(card(id) + " .ih .pill")
		check(strings.Contains(pill.Text(), "未测"), id+" starts as not run", pill.Text())
		p.L(card(id) + ` button[data-startitem="` + id + `"]`).Click()
		pills := libWaitDone(p, []string{id}, 60)
		return check(strings.Contains(pills[0], "rec"), id+" ends recorded, not judged", pills[0])
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
		check(strings.Contains(p.L(card("rtc.lsefreq")+` [data-lse="freq"] .hint`).Text(), "主晶振的误差也算在里面"),
			"and says the main crystal's error is included, so it is only recorded")
	}
	if run("rtc.lsemargin") {
		rows := cells(card("rtc.lsemargin") + ` [data-lse="margin"]`)
		want := [][]string{{"驱动档", "起没起来", "起振用时（ms）", "退回中低档"},
			{"低", "起来了", "900", "没有"}, {"中低", "起来了", "750", "没有"}, {"中高", "起来了", "600", "没有"},
			{"高", "起来了", "450", "没有"}, {"中低（切回）", "起来了", "750", "没有"}}
		same := len(rows) == len(want)
		for i := 0; same && i < len(rows); i++ {
			same = strings.Join(rows[i], "|") == strings.Join(want[i], "|")
		}
		check(same, "the margin card lists the five restarts, one row each, ending on the switch back to 中低", show(rows))
	}
	sent := sentLines(p.L("#log").Text(), "pt.run lse")
	if len(sent) > 6 {
		sent = sent[len(sent)-6:]
	}
	check(strings.Join(sent, ",") == "pt.run lse.freq,pt.run lse drv=low,pt.run lse drv=mlow,pt.run lse drv=mhigh,pt.run lse drv=high,pt.run lse drv=mlow",
		"the log shows what was sent to the board, in order", strings.Join(sent, ","))
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
