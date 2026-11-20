"use strict";

// The fixture line's big screen: one tile per station (decision 190,
// PORTTOOL-FLOW.md C.3.10). Every word comes from strings.json.

function T(key, args) {
  const e = STRINGS[key];
  let s = e ? (e[LANG] || e.zh) : key;
  if (args) { s = s.replace(/\{([a-z0-9_]+)\}/g, (m, k) => (k in args ? args[k] : m)); }
  return s;
}
function localize(v) {
  if (Array.isArray(v)) { return v.map(localize); }
  if (v && typeof v === "object") {
    if ("$t" in v) { return T(v.$t, localize(v.args || {})); }
    const o = {};
    for (const k of Object.keys(v)) { o[k] = localize(v[k]); }
    return o;
  }
  return v;
}
async function api(path, body) {
  const opt = body ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {};
  try { const r = await fetch(path, opt); return localize(await r.json()); } catch (e) { return {}; }
}
const $ = s => document.querySelector(s);
const el = (tag, cls, txt) => { const n = document.createElement(tag); if (cls) { n.className = cls; } if (txt != null) { n.textContent = txt; } return n; };
const add = (p, ...c) => { for (const x of c) { if (x) { p.appendChild(x); } } return p; };

const STEP_KEY = { 1: "line.step.1", 2: "line.step.2", 3: "line.step.3", 4: "line.step.4", 5: "line.step.5" };
const STATE_KEY = { empty: "line.st.empty", needflash: "line.st.needflash", flashing: "line.st.flashing", wire: "line.st.wire",
  run: "line.st.run", fail: "line.st.fail", blocked: "line.st.blocked", done: "line.st.done" };
const STATE_CLS = { empty: "empty", needflash: "wait", flashing: "run", wire: "wait", run: "run", fail: "fail", blocked: "wait", done: "done" };
const NUMS = "①②③④⑤";

let tiles = [], now = Date.now();

function fmt(ms) {
  const s = Math.max(0, Math.floor(ms / 1000)), h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60;
  return (h ? h + ":" + String(m).padStart(2, "0") : m) + ":" + String(s % 60).padStart(2, "0");
}
const since = t => (t && !t.startsWith("0001") ? now - Date.parse(t) : 0);

function stepTitle(t) {
  if (t.state === "done") { return T("line.st.done"); }
  if (t.state === "needflash" || t.state === "flashing") { return T("line.prep.flash"); }
  if (t.state === "wire") { return T("line.prep.wire"); }
  return t.step ? T(STEP_KEY[t.step]) : "";
}

function fwText(t) {
  if (t.fw === "blank") { return T("line.fw.blank"); }
  if (!t.fw) { return T("line.fw.none"); }
  if (t.want && t.fw !== t.want) { return T("line.fw.old", { v: t.fw }); }
  return t.fw;
}

function act(n, a) { return api("/api/line/act", { n, act: a }).then(refresh); }

function popMenu(anchor, items) {
  document.querySelectorAll(".popmenu").forEach(x => x.remove());
  const m = el("div", "popmenu"), r = anchor.getBoundingClientRect();
  m.style.left = (r.left + scrollX) + "px"; m.style.top = (r.bottom + scrollY) + "px";
  for (const [label, f] of items) { const d = el("div", null, label); d.onclick = e => { e.stopPropagation(); m.remove(); f(); }; m.appendChild(d); }
  document.body.appendChild(m);
  setTimeout(() => document.addEventListener("click", () => m.remove(), { once: true }));
}

function tileMenu(btn, t) {
  const items = [];
  if (t.state !== "empty" && t.state !== "done") { items.push([t.paused ? T("line.btn.resume") : T("line.btn.pause"), () => act(t.n, t.paused ? "resume" : "pause")]); }
  if (t.state !== "empty") { items.push([T("line.btn.remove_board"), () => { if (confirm(T("line.confirm.remove_board", { n: t.n }))) { act(t.n, "remove"); } }]); }
  items.push([T("line.btn.settings"), () => settings(t)]);
  items.push([T("line.btn.open_panel"), () => { location.href = "/st/" + t.n + "/"; }]);
  if (t.state === "empty" && t.n !== 1) { items.push([T("line.btn.delete"), () => api("/api/stations/remove", { n: t.n }).then(r => { if (r.error) { alert(r.error); } refresh(); })]); }
  popMenu(btn, items);
}

function tileOf(t) {
  const cls = t.paused ? "paused" : STATE_CLS[t.state] || "wait";
  const box = el("div", "tile " + cls), h = el("div", "th");
  add(h, el("b", null, T("line.station", { n: t.n })));
  if (t.state !== "empty") {
    add(h, el("span", "uid", t.uid ? "…" + t.uid.slice(-8) : T("line.uid_unknown")),
      el("span", "uid" + (t.want && t.fw !== t.want ? " warn" : ""), fwText(t)));
  }
  add(h, el("span", "sp"));
  if (t.state === "fail") { add(h, el("span", "led on")); }
  const menu = el("button", "tmenu", "⋯");
  menu.onclick = e => { e.stopPropagation(); tileMenu(menu, t); };
  add(h, menu, null);
  box.appendChild(h);
  if (t.state === "empty") {
    add(box, el("div", "st", T("line.st.empty")));
    if (t.why) { add(box, el("div", "why", t.why)); }
    return box;
  }
  box.onclick = () => detail(t);
  add(box, el("div", "stepname", stepTitle(t)), el("div", "st", t.paused ? T("line.st.paused") : T(STATE_KEY[t.state])));
  if (t.why) { add(box, el("div", "why", t.why)); }
  if (t.missing && t.missing.length) { add(box, el("div", "miss", T("line.missing", { list: t.missing.join(", ").toUpperCase() }))); }
  add(box, el("div", "time", T("line.time", { a: fmt(since(t.step_at)), b: fmt(since(t.since)) })));
  const acts = el("div", "acts");
  if (t.state === "needflash") {
    const b = el("button", "act", t.fw && t.fw !== "blank" ? T("line.btn.upgrade", { v: t.want }) : T("line.btn.flash", { v: t.want }));
    b.disabled = !t.want;
    b.onclick = e => { e.stopPropagation(); act(t.n, "flash"); };
    acts.appendChild(b);
  }
  if (t.state === "fail" || t.state === "blocked") {
    { const c = el("button", "act", T("line.btn.continue")); c.onclick = e => { e.stopPropagation(); act(t.n, "continue"); }; acts.appendChild(c); }
    const r = el("button", "act", T("line.btn.restart")); r.onclick = e => { e.stopPropagation(); act(t.n, "restart"); }; acts.appendChild(r);
  }
  if (acts.childNodes.length) { box.appendChild(acts); }
  const ch = el("div", "chain");
  const prepDone = t.state !== "needflash" && t.state !== "flashing" && t.state !== "wire";
  add(ch, el("span", prepDone ? "ok" : "cur", T("line.prep")));
  for (let i = 1; i <= 5; i++) {
    const r = (t.results || {})[i], passed = (t.passed || {})[i];
    const c = el("span", passed ? "ok" : r && r !== "PASS" ? "bad" : t.step === i && t.state === "run" ? "cur" : "", NUMS[i - 1]);
    c.title = T(STEP_KEY[i]);
    ch.appendChild(c);
  }
  box.appendChild(ch);
  return box;
}

function render() {
  const w = $("#lnwall"); w.textContent = "";
  for (const t of tiles) { w.appendChild(tileOf(t)); }
  const a = el("div", "addtile"), b = el("button", null, T("line.add"));
  b.onclick = () => api("/api/stations/add", {}).then(refresh);
  add(w, add(a, b));
}

async function refresh() {
  const j = await api("/api/line");
  if (!j.tiles) { return; }
  tiles = j.tiles; now = Date.parse(j.now) || Date.now();
  const want = (tiles.find(t => t.want) || {}).want;
  $("#lnfw").textContent = want ? T("line.fw_shipped", { v: want }) : T("line.fw_none_shipped");
  render();
}

function modal(build) {
  const m = $("#lnmodal"), b = $("#lnbox");
  b.textContent = ""; build(b); m.classList.add("open");
}
function closeModal() { $("#lnmodal").classList.remove("open"); }

// Station settings: its control port and its ST-Link.
async function settings(t) {
  const [ports, conn, list] = await Promise.all([api("/api/ports"), api("/api/conn", { stlink: true }), api("/api/stations")]);
  const me = (list.stations || []).find(x => x.n === t.n) || {};
  modal(b => {
    add(b, el("h2", null, T("line.set.title", { n: t.n })));
    const f = el("div", "lnform"), ctl = el("select"), sw = el("select");
    for (const p of ports.ports || []) {
      const o = el("option", null, p.name === "sim" ? T("line.set.sim") : p.label || p.name); o.value = p.name;
      if (p.unavailable) { o.disabled = true; }
      ctl.appendChild(o);
    }
    ctl.value = t.port || "";
    const none = el("option", null, T("line.set.none")); none.value = ""; sw.appendChild(none);
    for (const p of ((conn.stlink || {}).probes || [])) { const o = el("option", "mono", p.sn + (p.board ? " · " + p.board : "")); o.value = p.sn; sw.appendChild(o); }
    sw.value = me.stlink || "";
    add(f, el("span", null, T("line.set.control")), ctl, el("span", null, T("line.set.stlink")), sw);
    const save = el("button", "primary", T("line.set.save")), cancel = el("button", null, T("line.set.cancel"));
    save.onclick = async () => { const r = await api("/api/stations/set", { n: t.n, control: ctl.value, sn: sw.value }); if (r.error) { alert(r.error); return; } closeModal(); refresh(); };
    cancel.onclick = closeModal;
    add(b, f, add(el("div", "acts"), save, cancel));
  });
}

// One board: what each step left, from the station's own panel.
async function detail(t) {
  const f = await api("/st/" + t.n + "/api/full");
  modal(b => {
    add(b, el("h2", null, T("line.detail.title", { n: t.n, uid: t.uid || T("line.uid_unknown") })));
    for (let i = 1; i <= 5; i++) {
      add(b, el("b", null, T(STEP_KEY[i])));
      const rec = (t.records || {})[i];
      if (rec) { add(b, el("div", "hint mono", T("line.detail.record", { file: rec }))); }
      if (t.step === i && f.run && f.run.steps && f.run.steps.length) {
        const tb = el("table"), hr = el("tr");
        add(hr, el("th", null, T("line.detail.where")), el("th", null, T("line.detail.outcome")), el("th", null, T("line.detail.value")));
        tb.appendChild(hr);
        for (const s of f.run.steps) {
          const tr = el("tr");
          add(tr, el("td", null, s.where || ""), el("td", null, s.outcome === "PASS" ? T("line.res.pass") : s.outcome ? T("line.res.fail") + " · " + s.outcome : ""), el("td", "mono", s.value || ""));
          tb.appendChild(tr);
        }
        b.appendChild(tb);
      } else if (!rec) {
        add(b, el("div", "hint", T("line.detail.none")));
      }
    }
    const close = el("button", null, T("line.detail.close")), open = el("button", "act", T("line.btn.open_panel"));
    close.onclick = closeModal; open.onclick = () => { location.href = "/st/" + t.n + "/"; };
    add(b, add(el("div", "acts"), open, close));
  });
}

// Fixture test or R&D test, remembered in the page memo (decision 190).
async function initType() {
  const sel = $("#lntype");
  for (const [v, k] of [["line", "line.type.line"], ["rd", "line.type.rd"]]) { const o = el("option", null, T(k)); o.value = v; sel.appendChild(o); }
  sel.value = "line";
  sel.onchange = async () => {
    const ui = Object.assign({}, ((await api("/api/memo")) || {}).ui || {}, { type: sel.value });
    await api("/api/memo", { section: "ui", cells: ui });
    if (sel.value === "rd") { location.href = "/st/1/"; }
  };
  const memo = await api("/api/memo");
  if ((memo.ui || {}).type === "rd") { location.href = "/st/1/"; }
}

document.title = T("line.title");
$("#lntitle").textContent = T("line.title");
$("#lnmodal").onclick = e => { if (e.target.id === "lnmodal") { closeModal(); } };
initType();
refresh();
setInterval(refresh, 1000);
