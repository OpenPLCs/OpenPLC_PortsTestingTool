"use strict";

// The fixture line's big screen, as on the agreed demo page (decision 190,
// PORTTOOL-FLOW.md C.3.10): stations' wiring on the left, one tile per
// station, the oven and the records on the right, a board page per station
// and the template window. Every word comes from strings.json.

const NUMS = "①②③④⑤";
const STEP_KEY = { 1: "line.step.1", 2: "line.step.2", 3: "line.step.3", 4: "line.step.4", 5: "line.step.5" };
const STATE_KEY = { empty: "line.st.empty", needflash: "line.st.needflash", flashing: "line.st.flashing", wire: "line.st.wire",
  run: "line.st.run", fail: "line.st.fail", blocked: "line.st.blocked", done: "line.st.done_long", agego: "line.st.agego",
  short: "line.st.short", back: "line.st.back", cancel: "line.st.cancel" };
const STATE_CLS = { needflash: "wait", flashing: "run", wire: "wait", run: "run", fail: "fail", blocked: "wait", done: "done",
  agego: "wait", short: "wait", back: "run", cancel: "fail" };
// Steps a person acts in: ② (unplug, carry to the oven) and the wiring.
const MANUAL = { 2: true };
const STEP_OF_ID = { functional: 1, aging: 2, aging_check: 3, safety: 4, user: 5 };

let tiles = [], now = Date.now();
let hist = { oven: [], subs: [] }, histNow = Date.now();
let tpls = { list: [], using: {} };
let stations = {};           // n -> /api/stations row
let conns = {};              // n -> { conn, state, ports } of the station's panel
let view = { page: "wall", n: 0 }, boardTab = -1, boardFor = 0;
const shippedUIDs = new Set();

function fmt(ms) {
  const s = Math.max(0, Math.floor(ms / 1000)), h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60;
  const p = n => String(n).padStart(2, "0");
  return h ? h + ":" + p(m) + ":" + p(s % 60) : p(m) + ":" + p(s % 60);
}
const since = t => (t && !t.startsWith("0001") ? now - Date.parse(t) : 0);
function tplName(r) { return !r || !r.name ? T("line.tpl.none") : r.name === "default" ? T("line.tpl.default") : r.name; }

function act(n, a) { return api("/api/line/act", { n, act: a }).then(refresh); }

/* ---------------- top bar ---------------- */

function topChips() {
  typeChip("line");                                   // gear.js
  const pc = $("#tplchip"); pc.textContent = "";
  add(pc, el("span", "lab", T("line.chip.tpl")), el("b", null, tplName(tpls.using) + (tpls.using.ver ? " v" + tpls.using.ver : "")), el("span", "lab", "▾"));
  pc.onclick = openTemplates;
  $("#subto").textContent = T("line.subto");
}

/* ---------------- left: every station's wiring ---------------- */

// The rows of one station's wiring (gear.js) from what the big screen keeps.
function stationRows(n) {
  const c = conns[n] || {};
  return gearRows({ conn: c.conn, state: c.state, ports: c.ports, rec: stations[n], tile: tiles.find(t => t.n === n) });
}
function renderSide() {
  const side = $("#lnside"); side.textContent = "";
  add(side, el("h3", null, T("line.side.wiring")), el("div", "hint", T("line.side.wiring_hint")));
  for (const t of tiles) {
    const blk = el("div", "sblk"), hd = el("div", "st-hd");
    blk.onclick = () => gearModal(t.n, refreshSlow);
    add(hd, el("span", null, T("line.station", { n: t.n })), el("span", "mono", tail8(t.uid)), el("span", "sp"));
    blk.appendChild(hd);
    if (t.state === "empty") { add(blk, el("div", "hint", T("line.side.empty"))); }
    else {
      for (const r of stationRows(t.n)) {
        if (r.reserved) { continue; }
        const row = el("div", "srow");
        add(row, el("span", "dot" + (r.on ? " on" : "")), el("span", null, T(r.name)), el("span", "d", r.on ? r.val : T("line.gear.unseen")));
        row.title = r.val;
        blk.appendChild(row);
      }
    }
    side.appendChild(blk);
  }
}

/* ---------------- middle: the wall ---------------- */

function stepTitle(t) {
  if (t.state === "done") { return T("line.st.done"); }
  if (t.state === "needflash" || t.state === "flashing") { return T("line.prep.flash"); }
  if (t.state === "wire") { return T("line.prep.wire"); }
  return t.step ? T(STEP_KEY[t.step]) : "";
}
function tileMenu(btn, t) {
  const items = [];
  if (t.state !== "empty" && t.state !== "done") { items.push([t.paused ? T("line.btn.resume") : T("line.btn.pause"), () => act(t.n, t.paused ? "resume" : "pause")]); }
  if (t.state !== "empty") { items.push([T("line.btn.remove_board"), () => { if (confirm(T("line.confirm.remove_board", { n: t.n }))) { act(t.n, "remove"); } }]); }
  items.push([T("line.btn.settings"), () => gearModal(t.n, refreshSlow)]);
  if (t.state === "empty" && t.n !== 1) { items.push([T("line.btn.delete"), () => removeStation(t.n)]); }
  popMenu(btn, items);
}
function menuBtn(t) {
  const b = el("button", "tmenu", "⋯"); b.title = T("line.btn.menu_tip");
  b.onclick = e => { e.stopPropagation(); tileMenu(b, t); };
  return b;
}
async function removeStation(n) {
  const r = await api("/api/stations/remove", { n });
  if (r.error) { alert(r.error); }
  refresh();
}
function stopClick(f) { return e => { e.stopPropagation(); f(); }; }
function chainOf(t) {
  const ch = el("div", "chain");
  const prepDone = !["needflash", "flashing", "wire"].includes(t.state);
  add(ch, el("span", prepDone ? "ok" : "man", T("line.prep")));
  for (let i = 1; i <= 5; i++) {
    const r = (t.results || {})[i], passed = (t.passed || {})[i];
    const cur = t.step === i && !["done", "empty"].includes(t.state);
    const c = el("span", passed ? "ok" : r && r !== "PASS" ? "bad" : cur ? (MANUAL[i] || t.state === "agego" ? "man" : "cur") : "", NUMS[i - 1]);
    c.title = T(STEP_KEY[i]);
    ch.appendChild(c);
  }
  return ch;
}
// curText is the running item, "port|item", in words.
function curText(c) {
  const [port, item] = c.split("|");
  return portName(port) + " " + itemName(item);
}
function tileOf(t) {
  if (t.state === "empty") {
    const box = el("div", "tile empty"), h = el("div", "th");
    h.style.alignSelf = "stretch";
    add(h, el("span", "sp"), menuBtn(t));
    add(box, h, el("div", null, T("line.tile.empty", { n: t.n })));
    if (t.why) { add(box, el("div", "why", t.why)); }
    return box;
  }
  const cls = t.paused ? "paused" : STATE_CLS[t.state] || "wait";
  const box = el("div", "tile " + cls), h = el("div", "th");
  box.onclick = () => go("board", t.n);
  const warn = t.fw === "blank" || !t.fw || (t.want && t.fw !== t.want);
  const led = el("span", "led" + (t.led ? " on" : "")); led.title = T("line.led_tip");
  add(h, el("b", null, T("line.station", { n: t.n })), el("span", "uid", tail8(t.uid) || T("line.uid_unknown")),
    el("span", "uid" + (warn ? " warn" : ""), fwText(t)), el("span", "sp"), led, menuBtn(t));
  add(box, h, el("div", "stepname", stepTitle(t)), el("div", "st " + cls, t.paused ? T("line.st.paused") : T(STATE_KEY[t.state])));
  if (t.why) { add(box, el("div", "why", t.why)); }
  if (t.missing && t.missing.length) { add(box, el("div", "miss", T("line.missing", { list: t.missing.join(", ").toUpperCase() }))); }
  if (t.state === "run" && !t.paused && t.total) {
    add(box, el("div", "time", T("line.items", { k: Math.min(t.total, t.done + 1), n: t.total }) + (t.cur ? T("line.items_cur", { cur: curText(t.cur) }) : "")));
  }
  const est = t.est_ms ? T("line.time_est", { e: fmt(t.est_ms) }) : "";
  add(box, el("div", "time", T("line.time", { a: fmt(since(t.step_at)) + est, b: fmt(since(t.since)) })));
  if ((t.state === "run" || t.state === "flashing") && t.est_ms) {
    const bar = el("div", "bar"), i = el("i");
    i.style.width = Math.min(100, 100 * since(t.step_at) / t.est_ms) + "%";
    add(box, add(bar, i));
  }
  const acts = el("div", "acts");
  if (t.state === "needflash") {
    const b = el("button", "act", t.fw && t.fw !== "blank" ? T("line.btn.upgrade", { v: t.want }) : T("line.btn.flash", { v: t.want }));
    b.disabled = !t.want; b.onclick = stopClick(() => act(t.n, "flash"));
    acts.appendChild(b);
  }
  if (t.state === "fail" || t.state === "blocked") {
    const c = el("button", "act", T("line.btn.continue")), r = el("button", "act", T("line.btn.restart"));
    c.onclick = stopClick(() => act(t.n, "continue")); r.onclick = stopClick(() => act(t.n, "restart"));
    add(acts, c, r);
  }
  if (t.state === "short") {
    const k = el("button", "act", T("line.btn.keepaging")), r = el("button", "act", T("line.btn.reage")), c = el("button", "act", T("line.btn.cancelage"));
    k.onclick = stopClick(() => act(t.n, "keepaging")); r.onclick = stopClick(() => act(t.n, "reage"));
    c.onclick = stopClick(() => { if (confirm(T("line.confirm.cancelage"))) { act(t.n, "cancelage"); } });
    add(acts, k, r, c);
  }
  if (acts.childNodes.length) { box.appendChild(acts); }
  box.appendChild(chainOf(t));
  return box;
}
function pageWall(pg) {
  add(pg, el("h2", null, T("line.wall.title")), el("div", "hint", T("line.wall.hint")));
  const r = el("div", "row"); r.style.margin = "8px 0";
  add(r, el("span", "kv", T("line.shipped", { n: shippedUIDs.size })));
  pg.appendChild(r);
  const wg = el("div", "wallgrid"), grid = el("div", "grid");
  for (const t of tiles) { grid.appendChild(tileOf(t)); }
  const end = el("div", "wallend"), addb = el("button", null, T("line.add")), delb = el("button", null, T("line.del"));
  addb.onclick = () => api("/api/stations/add", {}).then(refresh);
  delb.onclick = () => {
    const free = tiles.filter(t => t.state === "empty" && t.n !== 1);
    if (!free.length) { alert(T("line.del_none")); return; }
    popMenu(delb, free.map(t => [T("line.del_one", { n: t.n }), () => removeStation(t.n)]));
  };
  add(end, addb, delb);
  const left = el("div"); add(left, grid, end);
  add(wg, left, sideCards());
  pg.appendChild(wg);
}

/* ---------------- right: oven and records ---------------- */

function pill(o) { return el("span", "pill " + (o === "PASS" ? "ok" : "bad"), o === "PASS" ? T("line.res.pass") : T("line.res.fail")); }
function stepName(id) { return STEP_OF_ID[id] ? T(STEP_KEY[STEP_OF_ID[id]]) : id; }
function sideCards() {
  const side = el("div");
  const ov = el("div", "card"); add(ov, el("b", null, T("line.side.oven")));
  if (!hist.oven.length) { add(ov, el("div", "hint", T("line.side.oven_none"))); }
  else { add(ov, el("div", "hint", T("line.side.oven_hint"))); }
  for (const o of hist.oven) {
    add(ov, add(el("div", "th"), el("span", "mono", tail8(o.uid)), el("span", "time", T("line.side.oven_away", { t: fmt(histNow - Date.parse(o.since)) }))));
  }
  const sc = el("div", "card"); sc.style.marginTop = "12px"; add(sc, el("b", null, T("line.side.subs")));
  if (!hist.subs.length) { add(sc, el("div", "hint", T("line.side.subs_none"))); }
  else {
    const tb = el("table");
    add(tb, add(el("tr"), el("th", null, T("line.col.time")), el("th", null, T("line.col.station")), el("th", null, T("line.col.board")),
      el("th", null, T("line.col.step")), el("th", null, T("line.detail.outcome"))));
    for (const x of hist.subs) {
      const tr0 = el("tr"), at = new Date(x.at);
      add(tr0, el("td", "mono", [at.getHours(), at.getMinutes(), at.getSeconds()].map(v => String(v).padStart(2, "0")).join(":")),
        el("td", null, x.station ? String(x.station) : "—"), el("td", "mono", tail8(x.uid)), el("td", null, stepName(x.step)), add(el("td"), pill(x.outcome)));
      tr0.style.cursor = "pointer"; tr0.onclick = () => showRecord(x.uid, x.file);
      tb.appendChild(tr0);
    }
    add(sc, add(el("div", "sub"), tb));
  }
  add(side, ov, sc);
  return side;
}
async function showRecord(uid, file) {
  const j = await api("/api/line/record?uid=" + encodeURIComponent(uid) + "&file=" + encodeURIComponent(file));
  modal(b => {
    const h = el("div", "mhd"), x = el("button", "x", "✕"); x.onclick = closeModal;
    add(h, el("h2", null, tail8(uid) + " · " + stepName(((j.record || {}).steps || [{}])[0].id)), x);
    add(b, h);
    for (const st of (j.record || {}).steps || []) {
      if (st.reason) { add(b, el("div", "hint", T("line.detail.reason", { r: st.reason }))); }
      if ((st.items || []).length) { b.appendChild(itemTable(st.items, (j.record || {}).station)); }
    }
  });
}

/* ---------------- one board ---------------- */

const PILL = { PASS: ["line.res.pass", "ok"], RUN: ["line.res.run", "run"], SKIPPED: ["line.res.skip", "off"], "": ["line.res.wait", "off"] };
function resPill(o) {
  const [k, c] = PILL[o] || ["line.res.fail", "bad"];
  return el("span", "pill " + c, T(k));
}
function itemName(id) {
  const k = "lib.item." + id + ".name";
  return STRINGS[k] ? T(k) : id || "—";
}
function portName(p) { return PORT_NAME[p] || (p || "").toUpperCase(); }
function paramsText(port, ps) {
  return Object.entries(ps || {}).map(([k, v]) => paramCN(k) + " " + valueCN(v)).join(T("shell.list_sep")) || "—";
}
function readingText(r) {
  const out = [];
  for (const c of (r.checks || []).slice().sort((a, b) => (a.pass ? 1 : 0) - (b.pass ? 1 : 0)).slice(0, 3)) {
    out.push(checkField(r.port, c.check || {}) + " " + (c.missing ? T("ui.failCN.3") : checkGot(c.check, c.got)));
  }
  if (!out.length && r.value) { out.push(r.value); }
  if (!out.length && r.values) { for (const [k, v] of r.values.slice(0, 3)) { out.push(fieldLabel(r.port, k) + " " + valueCN(v)); } }
  return out.join(T("shell.list_sep")) || "—";
}
// itemTable lists a step's numbered items: port, item, parameters, criterion, reading, result.
function itemTable(rows) {
  const tb = el("table");
  add(tb, add(el("tr"), ...["line.col.port", "line.col.item", "line.col.params", "line.col.crit", "line.col.reading", "line.detail.outcome"].map(k => el("th", null, T(k)))));
  for (const r of rows) {
    const o = r.outcome || r.state || "";
    const tr0 = el("tr", o === "RUN" ? "cur" : o && o !== "PASS" && o !== "SKIPPED" ? "bad" : "");
    const crit = (r.checks || []).map(c => checkCN(r.port, c.check || {})).join(T("shell.list_sep")) || "—";
    add(tr0, el("td", null, portName(r.port) + (r.ch || r.channel ? " " + (r.ch || r.channel) : "")), el("td", null, itemName(r.item)),
      el("td", null, paramsText(r.port, r.params)), el("td", null, crit), el("td", "mono", readingText(r)), add(el("td"), resPill(o)));
    tb.appendChild(tr0);
  }
  return tb;
}

const TABS = ["line.tab.flash", "line.tab.wire", "line.step.1", "line.step.2", "line.step.3", "line.step.4", "line.step.5", "line.tab.ship"];
// tabOf is the tab a tile is on: 0 flash, 1 wiring, 2..6 steps ①..⑤, 7 shipping sheet.
function tabOf(t) {
  if (t.state === "done") { return 7; }
  if (t.state === "needflash" || t.state === "flashing") { return 0; }
  if (t.state === "wire") { return 1; }
  return t.step ? t.step + 1 : 0;
}
function tabMark(t, i) {
  if (i === 0) { return ["needflash", "flashing"].includes(t.state) ? "cur" : "ok"; }
  if (i === 1) { return t.state === "wire" ? "cur" : ["needflash", "flashing"].includes(t.state) ? "" : "ok"; }
  if (i === 7) { return t.state === "done" ? "ok" : ""; }
  const s = i - 1, r = (t.results || {})[s];
  if ((t.passed || {})[s]) { return "ok"; }
  if (r && r !== "PASS") { return "bad"; }
  return t.step === s ? "cur" : "";
}
let boardFull = null, boardRec = {};
async function loadBoard(t) {
  boardFull = await api("/st/" + t.n + "/api/full");
  for (const [i, f] of Object.entries(t.records || {})) {
    if (!boardRec[i + f] && t.uid) {
      const j = await api("/api/line/record?uid=" + encodeURIComponent(t.uid) + "&file=" + encodeURIComponent(f));
      boardRec[i + f] = j.record || {};
    }
  }
}
function statusLine(t, i) {
  const m = tabMark(t, i), d = el("div", "row");
  const waiting = ["needflash", "wire", "agego", "short", "blocked"].includes(t.state);
  const [k, c] = m === "ok" ? ["line.bst.ok", "ok"] : m === "bad" ? ["line.bst.bad", "bad"] : m === "cur" ? (waiting ? ["line.bst.wait", "blue"] : ["line.bst.run", "run"]) : ["line.bst.later", "off"];
  add(d, el("span", "pill " + c, T(k)));
  if (m === "cur") { add(d, el("span", "time", T("line.bst.took", { t: fmt(since(t.step_at)) }))); }
  if (m === "bad" && t.why) { add(d, el("span", "why", t.why)); }
  return d;
}
function stepPage(p, t, i) {
  add(p, el("h3", null, T(TABS[i])), statusLine(t, i));
  const notes = ["line.note.flash", "line.note.wire", "line.note.1", "line.note.2", "line.note.3", "line.note.4", "line.note.5"];
  add(p, el("div", "note", T(notes[i])));
  if (i === 1) { p.appendChild(connZone(t.n)); return; }
  if (i === 3) {
    const o = hist.oven.find(x => x.uid === t.uid);
    add(p, el("div", "big", t.state === "agego" ? T("line.age.big_go") : o ? T("line.age.big_away", { t: fmt(histNow - Date.parse(o.since)) }) : (t.passed || {})[2] ? T("line.age.big_back") : T("line.bst.later")));
    add(p, el("h3", null, T("line.age.leds_title")), el("div", "hint", T("line.age.leds")));
  }
  const s = i - 1;
  if (i >= 2) {
    const run = boardFull && boardFull.run;
    if (t.step === s && t.state === "run" && run && run.steps) { p.appendChild(itemTable(run.steps)); return; }
    const f = (t.records || {})[s], rec = f && boardRec[s + f];
    if (rec) {
      for (const st of rec.steps || []) {
        if (st.reason) { add(p, el("div", "hint", T("line.detail.reason", { r: st.reason }))); }
        if ((st.items || []).length) { p.appendChild(itemTable(st.items)); }
      }
    } else if (i !== 3) { add(p, el("div", "hint", T("line.detail.none"))); }
  }
}
function shipSheet(p, t) {
  add(p, el("h3", null, T("line.tab.ship")));
  if (t.state !== "done") { add(p, el("div", "warnbox", T("line.ship.not_yet", { at: stepTitle(t) }))); }
  else { add(p, el("div", "big", T("line.detail.ship_ok"))); }
  const tb = el("table");
  add(tb, add(el("tr"), el("th", null, T("line.col.step")), el("th", null, T("line.detail.outcome")), el("th", null, T("line.col.record"))));
  for (let s = 1; s <= 5; s++) {
    const r = (t.results || {})[s], ok = (t.passed || {})[s];
    add(tb, add(el("tr"), el("td", null, T(STEP_KEY[s])),
      add(el("td"), el("span", "pill " + (ok ? "ok" : r ? "bad" : "off"), T(ok ? "line.res.pass" : r ? "line.res.fail" : "line.res.notyet"))),
      el("td", "mono", (t.records || {})[s] || "")));
  }
  p.appendChild(tb);
  add(p, el("div", "hint", T("line.ship.foot", { uid: t.uid || "—", tpl: tplName(t.tpl) + " v" + (t.tpl || {}).ver, n: t.n })));
}
// connZone is the station's wiring as rows, for the board page's wiring tab.
function connZone(n) {
  const z = el("div", "conn");
  let blk = "";
  for (const r of stationRows(n)) {
    if (r.reserved) { continue; }
    if (r.blk !== blk) { blk = r.blk; z.appendChild(el("div", "blkhd", T(blk))); }
    const row = el("div", "crow");
    add(row, el("span", "dot" + (r.on ? " on" : "")), el("span", "nm", T(r.name)), el("span", "mono", r.on ? r.val : T("line.gear.unseen")));
    z.appendChild(row);
  }
  return z;
}
function logLines(run) {
  return ((run || {}).log || []).map(l => {
    const at = new Date(l.at);
    const ts = [at.getHours(), at.getMinutes(), at.getSeconds()].map(v => String(v).padStart(2, "0")).join(":");
    return ts + "  " + (l.msg ? (typeof l.msg === "string" ? l.msg : "") : l.text || "");
  }).join("\n");
}
function pageBoard(pg, n) {
  const t = tiles.find(x => x.n === n);
  if (!t || t.state === "empty") { add(pg, el("div", "warnbox", T("line.board.empty"))); const b = el("button", null, T("line.board.back")); b.onclick = () => go("wall"); pg.appendChild(b); return; }
  if (boardFor !== n) { boardFor = n; boardTab = tabOf(t); }
  const back = el("button", null, T("line.board.back")); back.onclick = () => go("wall");
  const h = el("div", "row");
  const led = el("span", "led" + (t.led ? " on" : "")); led.title = T("line.led_tip");
  add(h, back, el("h2", null, T("line.board.title", { n, uid: t.uid || T("line.uid_unknown") })), el("span", "kv", fwText(t)), el("span", "sp"),
    el("span", "kv", T("line.board.tpl", { tpl: tplName(t.tpl), v: (t.tpl || {}).ver || "" })), led);
  if (t.state !== "done") { const pz = el("button", t.paused ? "primary" : null, t.paused ? T("line.btn.resume") : T("line.btn.pause")); pz.onclick = () => act(n, t.paused ? "resume" : "pause"); h.appendChild(pz); }
  const off = el("button", "stop", T("line.btn.remove_board"));
  off.onclick = () => { if (confirm(T("line.confirm.remove_board", { n }))) { act(n, "remove"); go("wall"); } };
  h.appendChild(off);
  pg.appendChild(h);
  if (t.paused) { add(pg, el("div", "warnbox", T("line.board.paused", { at: stepTitle(t) }))); }
  const tabs = el("div", "tabs");
  TABS.forEach((k, i) => {
    const m = tabMark(t, i);
    const b = el("button", (boardTab === i ? "on " : "") + (m === "ok" ? "ok" : m === "bad" ? "bad" : ""), T(k) + (m === "ok" ? " ✓" : m === "bad" ? " ✗" : m === "cur" ? " …" : ""));
    b.onclick = () => { boardTab = i; route(); };
    tabs.appendChild(b);
  });
  pg.appendChild(tabs);
  const cols = el("div", "bcols"), left = el("div"), right = el("div");
  if (boardTab === 7) { shipSheet(left, t); } else { stepPage(left, t, boardTab); }
  add(right, el("b", null, T("line.board.log")));
  const lg = el("div", "log", logLines(boardFull && boardFull.run) || T("line.board.log_none"));
  right.appendChild(lg);
  const s = boardTab - 1, f = (t.records || {})[s];
  if (boardTab >= 2 && boardTab <= 6 && f) {
    add(right, el("b", null, T("line.board.form")), el("div", "form", JSON.stringify(boardRec[s + f] || {}, null, 1)));
  }
  add(cols, left, right); pg.appendChild(cols);
  setTimeout(() => { lg.scrollTop = lg.scrollHeight; });
}

/* ---------------- templates ---------------- */

let tplEdit = "";
async function openTemplates() {
  tpls = await api("/api/templates");
  tplEdit = tplEdit || (tpls.using || {}).id || ((tpls.list || [])[0] || {}).id;
  await api("/api/templates", { act: "edit", id: tplEdit });
  renderTemplates();
}
function renderTemplates() {
  modal(box => {
    const h = el("div", "row");
    const close = el("button", null, T("line.detail.close")); close.onclick = () => { closeModal(); refresh(); };
    add(h, el("h2", null, T("line.tpl.title")), el("span", "sp"), close);
    add(box, h, el("div", "hint", T("line.tpl.hint")));
    const wrap = el("div", "tplwrap"), list = el("div", "tpllist"), right = el("div");
    for (const r of tpls.list || []) {
      const d = el("div", r.id === tplEdit ? "on" : "");
      add(d, el("span", null, tplName(r)), el("small", null, "v" + r.ver + ((tpls.using || {}).id === r.id ? T("line.tpl.using") : "")));
      d.onclick = async () => { tplEdit = r.id; await api("/api/templates", { act: "edit", id: r.id }); renderTemplates(); };
      list.appendChild(d);
    }
    const nw = el("div", "new", T("line.tpl.new"));
    nw.onclick = async () => {
      const j = await api("/api/templates", { act: "new", name: T("line.tpl.new_name", { n: (tpls.list || []).length + 1 }) });
      if (j.error) { alert(j.error); return; }
      tpls = j; tplEdit = j.id; renderTemplates();
    };
    list.appendChild(nw);
    const cur = (tpls.list || []).find(r => r.id === tplEdit) || {};
    const l1 = el("div", "layer1"), nm = el("input");
    nm.value = tplName(cur); nm.style.width = "220px";
    const sv = el("button", null, T("line.tpl.save")), use = el("button", "primary", T("line.tpl.use"));
    sv.onclick = async () => {
      const j = await api("/api/templates", { act: "save", id: tplEdit, name: nm.value === T("line.tpl.default") ? "default" : nm.value });
      if (j.error) { alert(j.error); }
      tpls = j.list ? j : tpls; renderTemplates();
    };
    use.onclick = async () => {
      const j = await api("/api/templates", { act: "save", id: tplEdit, name: nm.value === T("line.tpl.default") ? "default" : nm.value });
      if (j.error) { alert(j.error); return; }
      const u = await api("/api/templates", { act: "use", id: tplEdit });
      tpls = u.list ? u : tpls; closeModal(); refresh();
    };
    add(l1, el("b", null, T("line.tpl.name")), nm, el("span", "hint", "v" + (cur.ver || "")), el("span", "sp"), sv, use);
    const fr = el("iframe", "tplframe"); fr.src = "/st/1/?embed=template";
    add(right, l1, fr);
    add(wrap, list, right); box.appendChild(wrap);
  });
}

/* ---------------- data and routing ---------------- */

function go(page, n) { view = { page, n: n || 0 }; boardFor = 0; boardRec = {}; route(); if (page === "board") { refreshBoard(); } }
function route() {
  topChips();
  renderSide();
  const pg = $("#lnpage"), keep = pg.scrollTop;
  pg.textContent = "";
  if (view.page === "board") { pageBoard(pg, view.n); } else { pageWall(pg); }
  pg.scrollTop = keep;
}
async function refreshStations() {
  const j = await api("/api/stations");
  stations = {};
  for (const s of j.stations || []) { stations[s.n] = s; }
}
async function refreshConn(n) {
  const [conn, st, ports] = await Promise.all([api("/st/" + n + "/api/conn"), api("/st/" + n + "/api/state"), api("/st/" + n + "/api/ports")]);
  conns[n] = { conn, state: st, ports: ports.ports || [] };
}
async function refresh() {
  const j = await api("/api/line");
  if (!j.tiles) { return; }
  tiles = j.tiles; now = Date.parse(j.now) || Date.now();
  for (const t of tiles) { if (t.state === "done" && t.uid) { shippedUIDs.add(t.uid); } }
  if (!$("#lnmodal").classList.contains("open")) { route(); }
}
async function refreshSlow() {
  const [h, tp] = await Promise.all([api("/api/line/history"), api("/api/templates")]);
  if (h.oven) { hist = h; histNow = Date.parse(h.now) || Date.now(); }
  if (tp.list) { tpls = tp; }
  await refreshStations();
  await Promise.all(tiles.map(t => refreshConn(t.n)));
}
async function refreshBoard() {
  const t = tiles.find(x => x.n === view.n);
  if (view.page === "board" && t) { await loadBoard(t); if (!$("#lnmodal").classList.contains("open")) { route(); } }
}

// Drag the left column's edge; the width is kept in the page memo (decision 190).
function leftGrip() {
  const g = $("#lngrip");
  g.title = T("html.leftgrip");
  g.onpointerdown = e => {
    e.preventDefault(); g.setPointerCapture(e.pointerId); g.classList.add("drag");
    g.onpointermove = m => { document.documentElement.style.setProperty("--leftw", Math.max(200, Math.min(640, m.clientX)) + "px"); };
    g.onpointerup = async () => {
      g.onpointermove = null; g.classList.remove("drag");
      const w = parseInt(getComputedStyle(document.documentElement).getPropertyValue("--leftw"), 10);
      const ui = Object.assign({}, ((await api("/api/memo")) || {}).ui || {}, { leftw: String(w) });
      api("/api/memo", { section: "ui", cells: ui });
    };
  };
}

async function boot() {
  document.title = T("line.title");
  const memo = await api("/api/memo"), ui = memo.ui || {};
  if (ui.type === "rd") { location.href = "/st/1/"; return; }
  if (ui.leftw) { document.documentElement.style.setProperty("--leftw", ui.leftw + "px"); }
  leftGrip();
  $("#lnmodal").onclick = e => { if (e.target.id === "lnmodal") { closeModal(); refresh(); } };
  await refresh();
  await refreshSlow();
  route();
  setInterval(refresh, 1000);
  setInterval(refreshSlow, 5000);
  setInterval(refreshBoard, 2000);
}
boot();
