"use strict";

// The KNX analyser window (decision 151 #8, PORTTOOL-FLOW.md C.3.6 "KNX 分析"):
// the same shell as the CAN window (canwin.js), with KNX content. Also the
// connection block's KNX USB interface row (C.3.6 "连接区").

const KNXWIN_MATCH = { wait: "canwin.match.wait", ok: "canwin.match.ok", bad: "canwin.match.bad",
  missing: "knxwin.match.missing", unsent: "knxwin.match.unsent", self: "knxwin.match.self" };
const KNXWIN_BUS = { ok: "knxwin.bus.ok", dead: "knxwin.bus.dead", odd: "knxwin.bus.odd" };
const KNXWIN_WHY = { sim: "knxwin.why.sim", unsupported: "knxwin.why.unsupported", none: "knxwin.why.none",
  pick: "knxwin.why.pick", open: "knxwin.why.open", lost: "knxwin.why.lost" };
const KNXWIN_APCI = { write: "knxwin.apci.write", read: "knxwin.apci.read", response: "knxwin.apci.response" };
const KNXWIN_CRC = { raw: "knxwin.crc.raw", inv: "knxwin.crc.inv", bad: "knxwin.crc.bad" };

let knxWin = { id: null, st: null, shown: false, paused: false, filter: "", wasRunning: false };

function knxWinBuild() {
  return anaWinBuild({ win: knxWin, id: "knxwin", p: "kw", title: "knxwin.title", hint: "knxwin.hint",
    filter: "knxwin.filter", endTip: "anawin.tip.end_knx", stop: "knx_stop", clear: "knx_clear",
    form: knxWinForm, draw: knxWinDraw });
}

// The page's check of one KNX send (decision 171): "" when fine. The
// addresses are checked by the server.
function knxWinCheck(value, every, count) {
  const n = Number(value.trim());
  if (value.trim() === "" || !Number.isInteger(n) || n < 0 || n > 63) { return T("anawin.err.value"); }
  return anaWinCheckRepeat(every, count, 100);
}

function knxWinForm(side, act) {
  const f = el("div", "cwform");
  f.dataset.kwside = side;
  f.appendChild(el("b", null, T(side === "board" ? "knxwin.send.board" : "knxwin.send.pc")));
  const field = (name, label, size, value) => anaWinField(f, "kw", name, label, size, value);
  // 31/7/255 is the quiet corner of the address space, as the board's own default.
  const ga = field("ga", "knxwin.f.ga", 9, "31/7/255"), val = field("value", "knxwin.f.value", 3, "1");
  const src = side === "board" ? field("src", "knxwin.f.src", 10, "") : null;
  if (src) { src.placeholder = "15.15.250"; }
  const every = field("every", "knxwin.f.every", 6), count = field("count", "canwin.f.count", 5);
  const go = el("button", "primary", T("canwin.send"));
  go.dataset.kwsend = side;
  go.onclick = () => anaWinSend(knxWin, knxWinDraw, act, knxWinCheck(val.value, every.value, count.value),
    { op: "knx_send", side, ga: ga.value, value: val.value, src: src ? src.value : "",
      every: Number(every.value) || 0, count: Number(count.value) || 1 });
  // No stop of its own: 停止发包 at the top stops both sides.
  f.appendChild(go);
  const rep = el("div", "hint"); rep.dataset.kwrepeat = side;
  f.appendChild(rep);
  return f;
}

// Called by libLive for the knx.live card on every redraw.
function knxWinCard(it, st) { return anaWinCard(knxWin, "knx", "knxwin.summary", knxWinDraw, it, st); }

function knxWinDraw(force) {
  const w = $("#knxwin") || knxWinBuild();
  // st is null before the item's first run: an empty window that says why it cannot send.
  const st = knxWin.st || { live: {}, running: false };
  w.hidden = !knxWin.shown;
  if (w.hidden) { return; }
  const v = st.live.knx || {}, run = !!st.running;
  const b = v.board || {}, p = v.pc || {};
  w.dataset.running = run ? "1" : "0";
  anaWinErr(w, knxWin, "kw", run);
  for (const n of w.querySelectorAll("button, input, select")) {
    if (n.id === "kwclose" || n.id === "kwpause" || n.id === "kwfilter") { continue; }
    const pcSide = n.closest('[data-kwside="pc"]');
    n.disabled = !run || (!!pcSide && !p.open);
  }

  const status = $("#kwstatus");
  status.textContent = "";
  status.appendChild(el("div", b.bus && b.bus !== "ok" ? "bad" : null, T("knxwin.board_status", {
    bus: T(KNXWIN_BUS[b.bus] || "knxwin.bus.unknown"), tx: b.tx || 0, rx: b.rx || 0,
    raw: b.crc_raw || 0, inv: b.crc_inv || 0, crcbad: b.crc_bad || 0, acks: b.acks || 0, other: b.ack_other || 0,
    partial: b.partial || 0, chars: b.bad_chars || 0, pulses: b.pulses || 0, dropped: b.dropped || 0,
    w: b.w_avg || 0, d: b.d_avg || 0 })));
  const pc = p.open
    ? el("div", null, T("knxwin.pc_status", { name: p.name || "-", emi: p.emi || "-", tx: p.tx || 0, rx: p.rx || 0,
        ok: p.con_ok || 0, fail: p.con_fail || 0 }))
    : el("div", "bad", T("knxwin.pc_closed", { why: T(KNXWIN_WHY[p.why] || "knxwin.why.none") }) + (p.detail ? " (" + p.detail + ")" : ""));
  pc.dataset.kwpcWhy = p.open ? "" : (p.why || "none");
  status.appendChild(pc);
  for (const [side, s] of [["board", b], ["pc", p]]) {
    const r = w.querySelector('[data-kwrepeat="' + side + '"]');
    r.textContent = s.every ? T("knxwin.repeating", { every: s.every, left: s.left }) : "";
  }
  $("#kwsum").textContent = T("knxwin.summary", { ok: v.matched || 0, miss: v.missing || 0, bad: v.bad || 0 });
  if (knxWin.paused && !force) { return; }

  const host = $("#kwtable");
  host.textContent = "";
  const tbl = el("table", "quad live cw");
  const tr = el("tr");
  for (const h of ["canwin.col.time", "canwin.col.who", "canwin.col.dir", "knxwin.col.src", "knxwin.col.dst",
    "knxwin.col.type", "knxwin.col.value", "knxwin.col.crc", "canwin.col.match"]) { tr.appendChild(el("th", null, T(h))); }
  tbl.appendChild(tr);
  const want = knxWin.filter;
  const rows = (v.rows || []).filter(r => !want || (r.dst || "").startsWith(want));
  for (const r of rows.slice().reverse()) {
    const row = el("tr");
    row.dataset.kwrow = r.side + "-" + r.dir;
    row.dataset.match = r.match || "";
    row.appendChild(el("td", "mono", (r.ms / 1000).toFixed(3)));
    row.appendChild(el("td", null, T(r.side === "board" ? "canwin.who.board" : "knxwin.who.pc")));
    row.appendChild(el("td", null, T(r.dir === "tx" ? "canwin.dir.tx" : "canwin.dir.rx")));
    row.appendChild(el("td", "mono", r.src || "-"));
    row.appendChild(el("td", "mono", r.dst || "-"));
    row.appendChild(el("td", null, r.dst ? T(KNXWIN_APCI[r.apci] || "knxwin.apci.other") : "-"));
    row.appendChild(el("td", "mono", r.value || "-"));
    const crc = el("td", r.crc === "bad" ? "bad" : null, r.crc ? T(KNXWIN_CRC[r.crc] || "knxwin.crc.bad") : "");
    if (r.raw) { crc.title = r.raw; }
    row.appendChild(crc);
    let m = r.match ? T(KNXWIN_MATCH[r.match] || "canwin.match.wait", { got: r.got || "-" }) : "";
    if (r.con) { m += (m ? "; " : "") + T(r.con === "ok" ? "knxwin.con.ok" : "knxwin.con.fail"); }
    const bad = (r.match && r.match !== "wait" && r.match !== "ok" && r.match !== "self") || r.con === "fail";
    row.appendChild(el("td", r.match === "ok" && !bad ? "ok" : bad ? "bad" : null, m));
    tbl.appendChild(row);
  }
  if (!rows.length) { host.appendChild(el("div", "hint", T("knxwin.none"))); }
  host.appendChild(tbl);
  if (v.dropped) { host.appendChild(el("div", "hint", T("knxwin.dropped", { n: v.dropped }))); }
}

// ---- the connection block's KNX USB interface row ----

let knxUsbInfo = {};

async function knxUsbLoad(body) {
  try { knxUsbInfo = await api("/api/knxusb", body); } catch (e) { /* next refresh says so */ }
  refreshConnRows(true);
}

function knxConnRow() {
  const row = connRow("knxusb", "conn.knxusb", "conn.knxusb_d");
  const { r, v } = row;
  const k = knxUsbInfo || {};
  const devs = k.devices || [];
  if (k.unsupported) {
    connMark(row, false, T("knxwin.why.unsupported"));   // connzone.js
    return r;
  }
  if (devs.length && k.pick) {
    const i = devs.findIndex(d => d.path === k.pick);
    const shown = el("span", "mono", (k.labels || [])[i] || k.pick);
    shown.dataset.knxusbPick = k.pick;
    v.appendChild(shown);
  }
  if (devs.length > 1 || (devs.length && !k.pick)) {
    const sel = connSelect("knxusb", devs.map((d, i) => ({ name: d.path, label: (k.labels || [])[i] || d.path })), k.pick);
    v.appendChild(sel);
    const use = el("button", null, T("conn.knxusb_use"));
    use.onclick = () => knxUsbLoad({ pick: sel.value });
    v.appendChild(use);
  }
  // Green once an interface is chosen; 重试 lists them again (decision 170).
  const why = k.error || (devs.length ? "" : T("conn.knxusb_none"));
  connMark(row, !!k.pick && !k.error, why, () => knxUsbLoad({ rescan: true }));
  return r;
}

// After every script has run: the reply redraws through connzone.js, loaded later.
document.addEventListener("DOMContentLoaded", () => knxUsbLoad());
