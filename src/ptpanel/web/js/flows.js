"use strict";

/* ================= 工装完整测试 layers 1-2 =================
 *
 * The full test (decisions 125, 128, PORTTOOL-FLOW.md C.3.6 「工装完整测试页」):
 * layer 1 is the title, what will run and 「配置完成，开始测试」, the only
 * control that starts it; layer 2 is the four sub-tests with their ticks,
 * times and run state. Notes, the safety card and the last outcome are
 * full.js; the right column is runview.js.
 */

// Called from pickView when the full test is opened.
function flowLoad() {
  $("#flowwhat").textContent = T("ui.full.what");
  fullSeen = "";
  loadFull();
}

/* ---------------- 工装完整测试 ---------------- */

const fullRunning = () => !!(fullData && fullData.running);
const fullStepInfo = step => ((fullData && fullData.steps) || []).find(s => s.step === step);
const fullAvailable = step => { const s = fullStepInfo(step); return !s || s.available; };
const fullTicked = () => FULL_SUBS.filter(s => fullPick[s.step] && fullAvailable(s.step));

// How many items a sub-test's configuration runs on this board.
function subPicked(k) {
  return catalog.items.filter(it => it.stages.includes(k) && cfgPortOn(k, it.port) && cfgOn(k, it)).length;
}

function subPlanText() {
  const t = fullTicked();
  if (!t.length) { return T("full.plan_none"); }
  return T("full.plan", { list: t.map(s => s.durs ? T("full.plan_item", { name: T(s.name), dur: durLabel(cfgDuration(s.k)) }) : T(s.name)).join(" → ") });
}

// Called from renderSubTest (shell.js) and when /api/full changes.
function fullTop(top, host) {
  top.textContent = ""; host.textContent = "";
  const running = fullRunning();
  top.appendChild(el("h2", null, T("shell.entry.full")));
  const plan = el("span", "sub", subPlanText()); plan.dataset.fullplan = "1";
  top.appendChild(plan);
  top.appendChild(el("span", "sp"));
  const btn = el("button", running ? "stop" : "primary", T(running ? "ui.full.stop" : "full.go"));
  btn.id = "shellstart";
  btn.disabled = !running && !fullTicked().length;
  btn.onclick = running ? stopFullRun : () => startFull("");
  top.appendChild(btn);
  host.appendChild(subTiles(running));
}

function subTiles(running) {
  const w = el("div", "subtiles");
  const st = fullData || {};
  const shown = running || !!st.last;   // run states stay up after a run
  for (const s of FULL_SUBS) {
    const info = fullStepInfo(s.step);
    const avail = !info || info.available;
    const tile = el("div", "subtile" + (s.k === view ? " cur" : ""));
    tile.dataset.sub = s.k;

    const head = el("div", "row");
    const cb = el("input"); cb.type = "checkbox"; cb.dataset.fullstep = s.step;
    cb.checked = !!fullPick[s.step] && avail;
    cb.disabled = !avail || running;
    if (!avail && info.why) { cb.title = info.why; }
    cb.onclick = e => e.stopPropagation();
    cb.onchange = () => {
      fullPick[s.step] = cb.checked;
      renderFullTop();
    };
    head.appendChild(cb);
    head.appendChild(el("span", "nm", T(s.name)));
    tile.appendChild(head);

    const time = el("div", "row");
    if (s.durs && s.durs.length === 1) {
      // Nothing to choose: the functional test runs once (decision 139).
      time.appendChild(el("span", "hint", T("full.time")));
      const one = el("span", null, durLabel(s.durs[0])); one.dataset.subonly = s.k;
      time.appendChild(one);
    } else if (s.durs) {
      time.appendChild(el("span", "hint", T("full.time")));
      const sel = el("select"); sel.dataset.subdur = s.k; sel.disabled = running;
      for (const d of s.durs) { const o = el("option", null, durLabel(d)); o.value = d; o.selected = cfgDuration(s.k) === d; sel.appendChild(o); }
      sel.onclick = e => e.stopPropagation();
      sel.onchange = () => cfgSetDuration(s.k, sel.value);   // saved on the spot (stagecfg.js)
      time.appendChild(sel);
    } else {
      time.appendChild(el("span", "hint", T("full.safety_d")));
    }
    tile.appendChild(time);
    // Why it cannot be ticked, where the eye lands after the grey box (PORTTOOL-FLOW.md C.3.6 layer 2).
    if (!avail && info.why) { const w = el("div", "subwhy", info.why); w.dataset.subwhy = s.step; tile.appendChild(w); }

    if (shown) { tile.appendChild(subState(s.step, st)); }
    if (s.durs && !running) {
      const n = el("div", "hint", T("full.picked", { n: subPicked(s.k) })); n.dataset.subn = s.k;
      tile.appendChild(n);
    }
    tile.onclick = () => pickView(s.k); // while running too: its cards are shown locked
    w.appendChild(tile);
  }
  return w;
}

// 等着 / 在跑 / outcome / 没勾跳过, and the step's progress bar (C.3.3 「进度」).
function subState(step, st) {
  const row = el("div", "row");
  const picked = (st.picked || []).includes(step);
  const o = (st.status || {})[step];
  let pill;
  if (!picked) { pill = el("span", "tgtres", T("full.st.skip")); }
  else if (o) { pill = el("span", "tgtres " + (o === "PASS" ? "ok" : "bad"), T(FULL_OUTCOME[o])); pill.dataset.fullres = step; }
  else if (st.running && st.current === step) { pill = el("span", "tgtres", T("ui.full.running")); }
  else { pill = el("span", "tgtres", T("full.st.wait")); }
  pill.dataset.substate = step;
  row.appendChild(pill);
  if (picked) {
    const bar = el("span", "stbar"), fill = el("i");
    fill.style.width = Math.max(0, Math.min(100, (st.pct || {})[step] || 0)) + "%";
    bar.appendChild(fill); row.appendChild(bar);
  }
  return row;
}

// Layers 1 and 2 again, leaving the cards alone.
function renderFullTop() { if (isFullSub(view)) { fullTop($("#rtop"), $("#subtiles")); } }

// 去研发测试: the R&D view on the port to calibrate on (decision 97).
function calButton(port) {
  const b = el("button", null, T("flow.goto_rd"));
  b.dataset.flow = "goto_rd";
  b.onclick = () => { lport = port; pickView("rd"); };
  return b;
}

/* ================= the big screen's template window =================
 *
 * Layers 2 and 3 of a template (PORTTOOL-FLOW.md C.3.10 「模板」): five
 * blocks, one per step, and the picked step's settings. What has nothing
 * behind it yet is drawn greyed with its reason (user 2026-11-23).
 */

// The aging items in the demo page's three groups: [ports_off key or "", label key, why greyed].
const AGE_GROUPS = [
  ["tpl.age.grp.nowire", [["temp", "line.age.item.temp"], ["sd", "line.age.item.sd"], ["sdram", "line.age.item.sdram"],
    ["rtc", "line.age.item.rtc"], ["can", "line.age.item.can"], ["", "tpl.age.eth_loop"]]],
  ["tpl.age.grp.harness", [["dout", "line.age.item.dout"], ["aout", "line.age.item.aout"], ["rs232", "line.age.item.rs232"], ["", "tpl.age.knx"]]],
  ["tpl.age.grp.peer", [["", "tpl.age.rs485_pair"], ["", "tpl.age.can_pair"], ["", "tpl.age.eth_rj45"], ["", "tpl.age.usb"]]],
];
const AGE_HOURS = ["1", "2", "4", "8"];

function tplSub(k) {
  if (k === "func" || k === "user") { return T("full.picked", { n: subPicked(k) }); }
  if (k === "aging") { return T("line.age.hours_n", { n: cfgDuration("aging") }); }
  return T(k === "check" ? "tpl.why.notyet" : "tpl.why.undef");
}
// A greyed row: label, a disabled field showing what happens now, the reason.
function greyRow(g, label, value, why) {
  const inp = el("input"); inp.value = value; inp.disabled = true; inp.style.width = "120px";
  const v = el("span"); v.append(inp, el("span", "why", " " + T(why)));
  g.append(el("span", null, T(label)), v);
}
function tplCard(title, rows) {
  const c = el("div", "tplcard"), g = el("div", "gr");
  c.appendChild(el("b", null, T(title)));
  for (const r of rows) { greyRow(g, ...r); }
  c.appendChild(g);
  return c;
}
function tplAging(cards) {
  cards.appendChild(el("div", "note", T("tpl.note.aging")));
  const c = el("div", "tplcard"), g = el("div", "gr");
  c.appendChild(el("b", null, T("line.step.2")));
  const sel = el("select");
  for (const h of AGE_HOURS) { const o = el("option", null, T("line.age.hours_n", { n: h })); o.value = h; o.selected = cfgDuration("aging") === h; sel.appendChild(o); }
  sel.onchange = () => cfgSetDuration("aging", sel.value);
  g.append(el("span", null, T("tpl.age.dur")), sel);
  greyRow(g, "tpl.age.temp_max", "—", "tpl.why.plan");
  c.appendChild(g);
  cards.appendChild(c);
  for (const [title, items] of AGE_GROUPS) {
    const card = el("div", "tplcard");
    card.appendChild(el("b", null, T(title)));
    for (const [key, label] of items) {
      const lab = el("label", key ? null : "off"), cb = el("input");
      cb.type = "checkbox";
      if (key) { cb.checked = cfgPortOn("aging", key); cb.onchange = () => cfgSetPort("aging", key, cb.checked); }
      else { cb.disabled = true; }
      lab.append(cb, " " + T(label), key ? "" : el("span", "why", " " + T("tpl.why.fw")));
      lab.style.display = "block";
      card.appendChild(lab);
    }
    cards.appendChild(card);
  }
}
function tplStep(top, host, cards) {
  top.textContent = ""; host.textContent = "";
  const bl = el("div", "tplblocks");
  for (const s of TPL_SUBS) {
    const b = el("div", "tplblk" + (s.k === view ? " cur" : ""));
    b.dataset.tplstep = s.k;
    b.append(el("b", null, T(s.name)), el("div", "hint", tplSub(s.k)));
    b.onclick = () => pickView(s.k);
    bl.appendChild(b);
  }
  host.appendChild(bl);
  if (view === "aging") { tplAging(cards); return; }
  if (view === "check") {
    cards.appendChild(tplCard("line.step.3", [["tpl.check.fails", "0", "tpl.why.check"], ["tpl.check.temp", "—", "tpl.why.check"], ["tpl.check.rounds", T("tpl.check.full"), "tpl.why.check"]]));
    return;
  }
  if (view === "safety") {
    cards.appendChild(tplCard("tpl.safety.scan", [["tpl.safety.code", "—", "tpl.why.undef"]]));
    cards.appendChild(tplCard("tpl.safety.ins", [["tpl.safety.volt", "—", "tpl.why.undef"], ["tpl.safety.min", "—", "tpl.why.undef"]]));
    cards.appendChild(tplCard("tpl.safety.hv", [["tpl.safety.volt", "—", "tpl.why.undef"], ["tpl.safety.secs", "—", "tpl.why.undef"], ["tpl.safety.leak", "—", "tpl.why.undef"]]));
    return;
  }
  if (view === "user") {
    cards.appendChild(el("div", "note", T("cfg.user_demo")));
    cards.appendChild(tplCard("tpl.user.title", [["tpl.user.boot", "—", "tpl.why.delivery"], ["tpl.user.demo", "FixtureDemo", "tpl.why.delivery"]]));
  }
  const port = catalog.ports.find(p => p.port === lport);
  if (!port) { return; }
  const sub = TPL_SUBS.find(s => s.k === view);
  cards.appendChild(el("div", "sechd", T("full.sechd", { sub: T(sub.name), port: port.label })));
  if (!cfgPortOn(view, port.port)) { cards.appendChild(el("div", "hint", T("full.port_off", { sub: T(sub.name) }))); return; }
  renderPortCards(cards, port);
}
