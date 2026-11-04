package main

// --guide: the screenshots of the panel's user guide (decision 175,
// PORTTOOL-FLOW C.4.0). Walks the panel on the simulated board, lays numbered
// boxes over the places to click and saves each scene per language into
// src/ptpanel/web/guide/shots/<lang>/. The numbers here are the numbers the
// guide's text (web/guide/zh.html, en.html) explains; change both together.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chromedp/cdproto/emulation"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// mark is one numbered box: the first visible match of sel.
type mark struct {
	Sel string `json:"sel"`
	N   int    `json:"n"`
}

// scene is one screenshot: what to do first, what to number, and the region
// to keep (empty = the whole window).
type scene struct {
	name  string
	setup func(p *Page)
	marks []mark
	clip  string
}

var guideLangs = []string{"zh", "en"}

const guideW, guideH = 1440, 900

func guideScenes() []scene {
	card := `#itemcards .item[data-item="sdram.probe"]`
	return []scene{
		{name: "connect", setup: func(p *Page) {
			p.L("#ctlport").Select("sim")
		}, marks: []mark{{"#ctlport", 1}, {"#connect", 2}, {"#showbt", 3}}, clip: "#connzone"},

		{name: "overview", setup: func(p *Page) {
			p.L("#connect").Click()
			p.WaitSel("#afterconn.live", 0)
			p.Wait(4500) // converter matching runs 3 s after connecting and holds Start
			p.L(`.mi[data-menu="rd"]`).Click()
			p.L(lport("sdram")).Click()
			p.Wait(500)
		}, marks: []mark{{"#guidelink", 1}, {"#connbox", 2}, {"#connrows", 3}, {"#menus", 4},
			{"#lports", 5}, {"#itemcards", 6}, {"#logcol", 7}}},

		{name: "rows", marks: []mark{{`#connrows .crow[data-conn="usb"]`, 1}, {`#connrows .crow .dot`, 2},
			{`#connrows button[data-retry]`, 3}}, clip: "#connzone"},

		{name: "card", setup: func(p *Page) {
			pressStart(p, "", "sdram.probe")
			libWaitDone(p, []string{"sdram.probe"}, 60)
		}, marks: []mark{{card + ` button[data-startitem]`, 1}, {card + ` .ib > div:first-child`, 2},
			{card + ` .ib > div:last-child`, 3}, {card + ` .ih .pill`, 4}, {`#itemcards button[data-release]`, 5}}, clip: card},

		{name: "timed", marks: []mark{{`#itemcards .kgroup[data-kind="repeat"] input[data-pick]`, 1},
			{`#itemcards select[data-shell="duration"]`, 2}, {"#shellstart", 3}},
			clip: `#itemcards .kgroup[data-kind="repeat"]`},

		{name: "ask", setup: func(p *Page) {
			p.L(lport("led")).Click()
			p.Wait(300)
			pressStart(p, "", "led.blink")
			p.WaitSel("#libask:not([hidden])", 30000)
		}, marks: []mark{{"#libask", 1}, {`#libask [data-ask="yes"]`, 2}}},

		{name: "can", setup: func(p *Page) {
			p.L(`#libask [data-ask="yes"]`).Click()
			libWaitDone(p, []string{"led.blink"}, 60)
			p.L(lport("can")).Click()
			p.Wait(300)
			p.L(`#itemcards button[data-canwin-open="can.bus"]`).Click()
			p.WaitSel("#canwin:not([hidden])", 20000)
			p.WaitFunc("() => !!(libData.states['can.bus'] || {}).running", nil, 20000)
			p.L("#cwsend").Click()
			p.Wait(1500)
		}, marks: []mark{{"#canwin [data-cwdir]", 1}, {"#cwrep", 2}, {"#cwsend", 3}, {"#cwresult", 4},
			{"#cwtable", 5}, {"#cwend", 6}}, clip: "#canwin"},

		{name: "log", setup: func(p *Page) {
			p.L("#cwend").Click()
			p.Wait(800)
			if p.L("#canwin").Visible() {
				p.L("#cwclose").Click()
				p.Wait(300)
			}
		}, marks: []mark{{"#logwrap .filt", 1}, {"#pause", 2}, {"#save", 3}, {"#raw", 4}, {"#send", 5}}, clip: "#logcol"},

		{name: "full", setup: func(p *Page) {
			p.L(`.mi[data-menu="full"]`).Click()
			p.Wait(800)
		}, marks: []mark{{"#subtiles", 1}, {"#lports", 2}, {"#itemcards", 3}, {"#shellstart", 4}}},
	}
}

// guideOverlay brings the scene's region into view, then draws the boxes, and
// returns the numbers it could not place.
const guideOverlay = `([marks, clip]) => {
  document.getElementById("__guide")?.remove();
  if (clip) { document.querySelector(clip)?.scrollIntoView({block: "nearest"}); }
  const layer = document.createElement("div");
  layer.id = "__guide";
  layer.style.cssText = "position:fixed;inset:0;pointer-events:none;z-index:2147483647";
  const missing = [];
  for (const m of marks) {
    const e = [...document.querySelectorAll(m.sel)].find(x => x.getClientRects().length);
    if (!e) { missing.push(m.n); continue; }
    const r = e.getBoundingClientRect();
    const box = document.createElement("div");
    box.style.cssText = "position:fixed;border:3px solid #e8590c;border-radius:6px;box-sizing:border-box;" +
      "left:" + (r.left - 4) + "px;top:" + (r.top - 4) + "px;width:" + (r.width + 8) + "px;height:" + (r.height + 8) + "px";
    const b = document.createElement("div");
    b.textContent = m.n;
    b.style.cssText = "position:fixed;width:26px;height:26px;border-radius:13px;background:#e8590c;color:#fff;" +
      "font:700 15px/26px sans-serif;text-align:center;box-shadow:0 1px 3px rgba(0,0,0,.4);" +
      "left:" + Math.max(0, r.left - 16) + "px;top:" + Math.max(0, r.top - 16) + "px";
    layer.append(box, b);
  }
  document.body.appendChild(layer);
  return missing;
}`

// guideClip is the region to keep: sel and every numbered place, with room
// for the badges.
const guideClip = `([sel, marks]) => {
  const rs = [sel, ...marks.map(m => m.sel)]
    .map(s => [...document.querySelectorAll(s)].find(x => x.getClientRects().length))
    .filter(Boolean).map(e => e.getBoundingClientRect());
  if (!rs.length) { return null; }
  const pad = 24;
  const x = Math.max(0, Math.min(...rs.map(r => r.left)) - pad), y = Math.max(0, Math.min(...rs.map(r => r.top)) - pad);
  const w = Math.min(innerWidth, Math.max(...rs.map(r => r.right)) + pad) - x;
  const h = Math.min(innerHeight, Math.max(...rs.map(r => r.bottom)) + pad) - y;
  return {x, y, w, h};
}`

// runGuide shoots every scene in every guide language.
func runGuide(p *Page, base string) {
	if err := chromedp.Run(p.ctx, emulation.SetDeviceMetricsOverride(guideW, guideH, 1, false)); err != nil {
		throw("viewport: %v", err)
	}
	for _, lang := range guideLangs {
		Section("guide screenshots: " + lang)
		dir := filepath.Join(repo, "src", "ptpanel", "web", "guide", "shots", lang)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			throw("%v", err)
		}
		p.Goto(base)
		p.Eval(`l => fetch("/api/settings", {method: "POST", body: JSON.stringify({lang: l})})`, lang)
		p.Wait(300)
		if p.L("#disconnect").Visible() {
			p.L("#disconnect").Click()
			p.Wait(500)
		}
		p.Goto(base)
		p.Wait(800)
		for _, s := range guideScenes() {
			if s.setup != nil {
				s.setup(p)
			}
			p.Wait(300)
			missing := p.Eval(guideOverlay, []any{s.marks, s.clip})
			check(missing.Len() == 0, "scene "+s.name+": every numbered place is on screen", missing.String())
			png := shoot(p, s)
			p.Eval(`() => document.getElementById("__guide")?.remove()`)
			out := filepath.Join(dir, s.name+".png")
			check(os.WriteFile(out, png, 0o644) == nil, "wrote "+out)
		}
	}
	p.Eval(`() => fetch("/api/settings", {method: "POST", body: JSON.stringify({lang: "zh"})})`)
}

func shoot(p *Page, s scene) []byte {
	shot := cdppage.CaptureScreenshot().WithFormat(cdppage.CaptureScreenshotFormatPng)
	if s.clip != "" {
		if r := p.Eval(guideClip, []any{s.clip, s.marks}); !r.Nil() && r.K("w").N() > 0 && r.K("h").N() > 0 {
			shot = shot.WithClip(&cdppage.Viewport{X: r.K("x").N(), Y: r.K("y").N(), Width: r.K("w").N(), Height: r.K("h").N(), Scale: 1})
		}
	}
	var png []byte
	if err := chromedp.Run(p.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		png, err = shot.Do(ctx)
		return err
	})); err != nil {
		throw("screenshot: %v", err)
	}
	fmt.Printf("  %d bytes\n", len(png))
	return png
}
