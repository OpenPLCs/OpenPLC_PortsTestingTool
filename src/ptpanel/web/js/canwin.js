"use strict";

// The CAN analyser window (decisions 111, 172, PORTTOOL-FLOW.md C.3.6 "CAN 分析"):
// the can.bus item's live state drawn in a window of its own, outside the
// cards, because the cards are redrawn every poll and would wipe what is being
// typed (same reason as #libask). Closing the window does not stop the item.

const CANWIN_LEC = ["canwin.lec.0", "canwin.lec.1", "canwin.lec.2", "canwin.lec.3",
  "canwin.lec.4", "canwin.lec.5", "canwin.lec.6", "canwin.lec.7"];
const CANWIN_STATE = { active: "canwin.state.active", warning: "canwin.state.warning",
  passive: "canwin.state.passive", off: "canwin.state.off" };
const CANWIN_MATCH = { wait: "canwin.match.wait", ok: "canwin.match.ok", bad: "canwin.match.bad",
  missing: "canwin.match.missing", unsent: "canwin.match.unsent" };
const CANWIN_WAY = { "board-tx": "canwin.dir.board_to_pc", "pc-tx": "canwin.dir.pc_to_board",
  "board-rx": "canwin.row.board_rx", "pc-rx": "canwin.row.pc_rx" };
// The send result, by the sent frame's match state and who sent it.
const CANWIN_RES = { ok: { board: "canwin.res.ok_pc", pc: "canwin.res.ok_board" },
  missing: { board: "canwin.res.missing_pc", pc: "canwin.res.missing_board" },
  bad: { board: "canwin.res.bad_pc", pc: "canwin.res.bad_board" },
  unsent: { board: "canwin.res.unsent", pc: "canwin.res.unsent" } };
const CANWIN_BAUDS = [125000, 250000, 500000, 1000000];
const CANWIN_FIELDS = ["id", "data", "every", "count"];

// dir: who sends; last: the frame the last 发送 put out ({side, id, data, mark,
// lec: the board's last error seen since, as the board clears it on each read});
// bad: field -> why it was refused; ran: running at the last draw.
let canWin = { id: null, st: null, shown: false, filter: "", wasRunning: false,
  dir: "board", last: null, bad: {}, ran: false };

// A labelled field of the send form: label, input, tip, the red line under it.
function canWinField(name, label, tip, size, value) {
  const f = el("div", "cwfld");
  f.appendChild(el("label", null, T(label)));
  const i = el("input", "mono"); i.dataset.cw = name; i.size = size;
  if (value) { i.value = value; }
  f.appendChild(i);
  if (tip) { f.appendChild(el("span", "cwtip", T(tip))); }
  const e = el("span", "cwfe"); e.dataset.cwerr = name;
  f.appendChild(e);
  return { f, i };
}

function canWinNode(side, name) {
  const n = el("div", "cwnode"); n.dataset.cwnode = side;
  const t = el("div", "cwnt");
  t.appendChild(el("span", "cwdot"));
  t.appendChild(el("span", null, T(name)));
  n.appendChild(t);
  const lines = el("div"); lines.dataset.cwlines = side;
  n.appendChild(lines);
  return n;
}

function canWinBuild() {
  const w = el("div"); w.id = "canwin"; w.hidden = true;

  const head = el("div", "cwhead");
  head.appendChild(el("b", null, T("canwin.title")));
  const live = el("span", "cwlive", T("canwin.live")); live.id = "cwlive";
  head.appendChild(live);
  const close = el("button", "cwx", "×");
  close.id = "cwclose";
  close.title = T("anawin.tip.close");
  close.onclick = () => { canWin.shown = false; w.hidden = true; };
  head.appendChild(close);
  document.addEventListener("keydown", e => { if (e.key === "Escape" && !w.hidden) { close.click(); } });
  w.appendChild(head);

  // Refusals that are not one box's fault (decision 171): the main page's
  // message box is behind this window.
  const err = el("div", "cwerr"); err.id = "cwerr";
  w.appendChild(err);

  // ① both ends, the bit rate between them
  const s1 = el("div", "cwsec");
  s1.appendChild(el("h2", null, T("canwin.sec.nodes")));
  const nodes = el("div", "cwnodes");
  nodes.appendChild(canWinNode("board", "canwin.node.board"));
  const wire = el("div", "cwwire");
  const line = el("div", "cwline"); line.id = "cwline";
  wire.appendChild(line);
  const baud = el("select"); baud.id = "cwbaud";
  baud.title = T("anawin.tip.baud_set");
  for (const b of CANWIN_BAUDS) { baud.appendChild(new Option(b / 1000 + " kbit/s", String(b))); }
  baud.onchange = () => { canWin.last = null; canWinAct({ op: "can_baud", baud: Number(baud.value) }); };
  wire.appendChild(baud);
  wire.appendChild(el("span", "cwtip", T("canwin.baud_hint")));
  nodes.appendChild(wire);
  nodes.appendChild(canWinNode("pc", "canwin.node.pc"));
  s1.appendChild(nodes);
  w.appendChild(s1);

  // ② one send form, the direction first
  const s2 = el("div", "cwsec");
  s2.appendChild(el("h2", null, T("canwin.sec.send")));
  const form = el("div", "cwsend");
  const dirf = el("div", "cwfld");
  dirf.appendChild(el("label", null, T("canwin.f.dir")));
  const seg = el("div", "cwseg");
  for (const [side, key] of [["board", "canwin.dir.board_to_pc"], ["pc", "canwin.dir.pc_to_board"]]) {
    const b = el("button", null, T(key)); b.dataset.cwdir = side;
    b.onclick = () => { canWin.dir = side; canWinDraw(); };
    seg.appendChild(b);
  }
  dirf.appendChild(seg);
  form.appendChild(dirf);
  const id = canWinField("id", "canwin.f.id", "canwin.f.id_tip", 6, "123");
  const data = canWinField("data", "canwin.f.data", "canwin.f.data_tip", 26, "11 22 33");
  form.appendChild(id.f); form.appendChild(data.f);
  const gof = el("div", "cwfld");
  gof.appendChild(el("label", null, " "));
  const go = el("button", "primary", T("canwin.send")); go.id = "cwsend";
  gof.appendChild(go);
  form.appendChild(gof);
  s2.appendChild(form);

  const rep = el("div", "cwrep");
  const repl = el("label");
  const repc = el("input"); repc.type = "checkbox"; repc.id = "cwrep";
  repl.appendChild(repc); repl.appendChild(document.createTextNode(" " + T("canwin.rep")));
  rep.appendChild(repl);
  const repf = el("span", "cwrepf"); repf.id = "cwrepf"; repf.hidden = true;
  const every = canWinField("every", "canwin.rep.every", null, 6, "500");
  const count = canWinField("count", "canwin.f.count", null, 6, "10");
  repf.appendChild(every.f); repf.appendChild(count.f);
  rep.appendChild(repf);
  repc.onchange = () => { repf.hidden = !repc.checked; };
  const now = el("span", "cwtip"); now.id = "cwrepnow";
  rep.appendChild(now);
  s2.appendChild(rep);

  const res = el("div", "cwres"); res.id = "cwresult"; res.hidden = true;
  s2.appendChild(res);
  w.appendChild(s2);

  // While the chosen direction repeats, the send button stops it (decision 172);
  // the session stays up and the other direction can still send.
  go.onclick = () => go.dataset.stop === "1"
    ? canWinAct({ op: "can_stop", side: canWin.dir })
    : canWinSend(id.i.value, data.i.value, repc.checked, every.i.value, count.i.value);

  // ③ the record: a list of fixed height, so the window does not grow
  const s3 = el("div", "cwsec");
  const bar = el("div", "cwbar");
  bar.appendChild(el("h2", null, T("canwin.sec.log")));
  for (const k of ["ok", "miss", "bad"]) {
    const p = el("span", "cwpill " + k); p.id = "cwsum-" + k;
    bar.appendChild(p);
  }
  bar.appendChild(el("span", "cwsp"));
  const filt = el("input"); filt.id = "cwfilter"; filt.size = 12; filt.placeholder = T("canwin.filter");
  filt.oninput = () => { canWin.filter = filt.value.trim(); canWinDraw(); };
  bar.appendChild(filt);
  const clear = el("button", null, T("canwin.clear")); clear.id = "cwclear";
  clear.onclick = () => { canWin.last = null; canWinAct({ op: "can_clear" }); };
  bar.appendChild(clear);
  s3.appendChild(bar);
  const list = el("div", "cwlist"); list.id = "cwtable";
  const tbl = el("table");
  const hr = el("tr");
  for (const h of ["canwin.col.time", "canwin.col.way", "canwin.col.id", "canwin.col.data", "canwin.col.result"]) {
    hr.appendChild(el("th", null, T(h)));
  }
  const thead = el("thead"); thead.appendChild(hr);
  tbl.appendChild(thead);
  const tbody = el("tbody"); tbody.id = "cwrows";
  tbl.appendChild(tbody);
  list.appendChild(tbl);
  s3.appendChild(list);
  const dropped = el("div", "cwtip"); dropped.id = "cwdropped";
  s3.appendChild(dropped);
  w.appendChild(s3);

  const foot = el("div", "cwfoot");
  foot.appendChild(el("span", "cwtip", T("canwin.foot")));
  const end = el("button", "stop", T("canwin.end")); end.id = "cwend";
  end.title = T("anawin.tip.end_can");
  end.onclick = () => canWinAct({ op: "end" });
  foot.appendChild(end);
  w.appendChild(foot);

  document.body.appendChild(w);
  return w;
}

// One window action. A refused box goes under that box, anything else on the
// error line. True when taken.
async function canWinAct(body) {
  const j = await api("/api/lib/live", Object.assign({ id: canWin.id }, body));
  canWin.bad = {};
  canWin.err = "";
  if (j.error && j.field) { canWin.bad[j.field] = j.error; } else { canWin.err = j.error || ""; }
  canWinDraw();
  libRefresh();
  return !j.error;
}

// The page's check of one send (decision 171): field -> why, empty when fine.
function canWinCheck(id, data, rep, every, count) {
  const bad = {};
  const i = id.trim().replace(/^0x/i, "");
  if (!/^[0-9a-f]{1,3}$/i.test(i) || parseInt(i, 16) > 0x7FF) { bad.id = T("anawin.err.id"); }
  if (!/^([0-9a-f]{2}){0,8}$/i.test(data.replace(/[\s,:]/g, ""))) { bad.data = T("anawin.err.data"); }
  const r = rep ? anaWinRepeatField(every, count, 50) : null;
  if (r) { bad[r.field] = r.msg; }
  return bad;
}

function canWinSend(id, data, rep, every, count) {
  const bad = canWinCheck(id, data, rep, every, count);
  if (Object.keys(bad).length) { canWin.bad = bad; canWin.err = ""; canWinDraw(); return; }
  // The rows this send makes come after every row there now (ms only grows).
  const rows = ((canWin.st && canWin.st.live && canWin.st.live.can) || {}).rows || [];
  const hex = data.replace(/[\s,:]/g, "").toUpperCase();
  canWin.last = { side: canWin.dir, mark: rows.length ? rows[rows.length - 1].ms : -1,
    id: "0x" + parseInt(id.trim().replace(/^0x/i, ""), 16).toString(16).toUpperCase().padStart(3, "0"),
    data: (hex.match(/../g) || []).join(" "), lec: 0 };
  canWinAct({ op: "can_send", side: canWin.dir, can_id: id, data,
    every: rep ? Number(every) || 0 : 0, count: rep ? Number(count) || 1 : 1 }).then(ok => {
    if (!ok) { canWin.last = null; canWinDraw(); }
  });
}

// Called by libLive for the can.bus card on every redraw.
function canWinCard(it, st) { return anaWinCard(canWin, "can", "canwin.summary", canWinDraw, it, st); }

// The board's last error in words, "" when there is none worth saying.
function canWinLec(b) { return b.last_lec >= 1 && b.last_lec <= 6 ? T(CANWIN_LEC[b.last_lec]) : ""; }

function canWinDrawNodes(b, p) {
  const bad = b.state === "off" || b.state === "passive" || !!canWinLec(b);
  const bn = $('#canwin [data-cwnode="board"]');
  bn.classList.toggle("bad", bad);
  $("#cwline").classList.toggle("cut", bad);
  const bl = $('#canwin [data-cwlines="board"]');
  bl.textContent = "";
  bl.appendChild(el("div", null, T("canwin.node.counts", { tx: b.tx || 0, rx: b.rx || 0 })));
  bl.appendChild(el("div", null, T("canwin.node.state", { state: T(CANWIN_STATE[b.state] || "canwin.state.unknown") })));
  if (canWinLec(b)) { bl.appendChild(el("div", "cwbadtxt", T("canwin.node.lec", { lec: canWinLec(b) }))); }
  if (b.tec || b.rec || b.lost) {
    bl.appendChild(el("div", "cwtip", T("canwin.node.errs", { tec: b.tec || 0, rec: b.rec || 0, lost: b.lost || 0 })));
  }
  const pl = $('#canwin [data-cwlines="pc"]');
  pl.textContent = "";
  pl.appendChild(el("div", null, T("canwin.node.counts", { tx: p.tx || 0, rx: p.rx || 0 })));
  pl.appendChild(el("div", null, T("canwin.node.state", {
    state: p.bells ? T("canwin.node.bells", { n: p.bells }) : T("canwin.state.active") })));
}

// The big line under the form: what became of the last send.
function canWinDrawResult(v, b, p) {
  const res = $("#cwresult");
  const l = canWin.last;
  res.hidden = !l;
  if (!l) { return; }
  if (b.last_lec >= 1 && b.last_lec <= 6) { l.lec = b.last_lec; }
  const mine = (v.rows || []).filter(r => r.side === l.side && r.dir === "tx" && r.ms > l.mark &&
    r.id === l.id && (r.data || "") === l.data);
  // A repeat follows its newest settled frame.
  const r = mine.slice().reverse().find(x => x.match && x.match !== "wait") || mine[mine.length - 1];
  const m = r ? r.match : "wait";
  res.className = "cwres " + (m === "ok" ? "ok" : CANWIN_RES[m] ? "bad" : "wait");
  res.textContent = "";
  if (!CANWIN_RES[m]) { res.appendChild(el("div", null, T("canwin.match.wait"))); return; }
  res.appendChild(el("div", null, T(CANWIN_RES[m][l.side], { id: l.id, data: l.data || "-", got: r.got || "-" })));
  if (m !== "missing" && m !== "unsent") { return; }
  const why = [];
  if (m === "unsent" && l.side === "board") { why.push(T("canwin.why.unsent")); }
  if (l.lec) { why.push(T("canwin.why.lec", { lec: T(CANWIN_LEC[l.lec]) })); }
  if (p.bells) { why.push(T("canwin.why.bells", { n: p.bells })); }
  why.push(T("canwin.why.wiring"));
  res.appendChild(el("div", "cwwhy", why.join(" ")));
}

function canWinDrawRows(v) {
  const body = $("#cwrows");
  body.textContent = "";
  const want = canWin.filter.toLowerCase().replace(/^0x/, "");
  const rows = (v.rows || []).filter(r => !want || r.id.toLowerCase().replace(/^0x0*/, "").includes(want.replace(/^0*/, "")));
  for (const r of rows.slice().reverse()) {
    const row = el("tr");
    row.dataset.cwrow = r.side + "-" + r.dir;
    row.dataset.match = r.match || "";
    row.appendChild(el("td", "mono", (r.ms / 1000).toFixed(3)));
    row.appendChild(el("td", null, T(CANWIN_WAY[r.side + "-" + r.dir] || "canwin.row.pc_rx")));
    row.appendChild(el("td", "mono", r.id));
    row.appendChild(el("td", "mono", r.data || "-"));
    const m = r.match ? T(CANWIN_MATCH[r.match] || "canwin.match.wait", { got: r.got || "-" }) : "";
    row.appendChild(el("td", r.match === "ok" ? "ok" : (r.match && r.match !== "wait") ? "bad" : null, m));
    body.appendChild(row);
  }
  if (!rows.length) {
    const row = el("tr");
    const td = el("td", "cwempty", T("canwin.none")); td.colSpan = 5;
    row.appendChild(td);
    body.appendChild(row);
  }
  $("#cwdropped").textContent = v.dropped ? T("canwin.dropped", { n: v.dropped }) : "";
}

function canWinDraw() {
  const w = $("#canwin") || canWinBuild();
  // st is null before the item's first run: an empty window that says why it cannot send.
  const st = canWin.st || { live: {}, running: false };
  const run = !!st.running;
  // A new session starts its clock from zero: the last send and refusals belong to the old one.
  if (run && !canWin.ran) { canWin.last = null; canWin.bad = {}; }
  canWin.ran = run;
  w.hidden = !canWin.shown;
  if (w.hidden) { return; }
  const v = st.live.can || {};
  const b = v.board || {}, p = v.pc || {};
  w.dataset.running = run ? "1" : "0";
  anaWinErr(w, canWin, "cw", run);
  $("#cwlive").hidden = !run;
  for (const n of w.querySelectorAll("button, input, select")) {
    if (n.id !== "cwclose" && n.id !== "cwfilter") { n.disabled = !run; }
  }
  for (const n of w.querySelectorAll("[data-cwdir]")) { n.classList.toggle("on", n.dataset.cwdir === canWin.dir); }
  for (const f of CANWIN_FIELDS) {
    const why = canWin.bad[f] || "";
    w.querySelector('[data-cwerr="' + f + '"]').textContent = why;
    w.querySelector('input[data-cw="' + f + '"]').classList.toggle("bad", !!why);
  }
  $("#cwrepf").hidden = !$("#cwrep").checked;
  const baud = $("#cwbaud");
  if (document.activeElement !== baud && v.baud) { baud.value = String(v.baud); }

  canWinDrawNodes(b, p);
  const repeating = !!(b.every || p.every);
  const r = b.every ? b : p;
  $("#cwrepnow").textContent = repeating ? T("canwin.repeating", { every: r.every, left: r.left }) : "";
  const go = $("#cwsend"), mine = canWin.dir === "board" ? b : p;
  go.dataset.stop = mine.every ? "1" : "0";
  go.textContent = T(mine.every ? "canwin.stop_rep" : "canwin.send");
  canWinDrawResult(v, b, p);

  $("#cwsum-ok").textContent = T("canwin.pill.ok", { n: v.matched || 0 });
  $("#cwsum-miss").textContent = T("canwin.pill.miss", { n: v.missing || 0 });
  $("#cwsum-bad").textContent = T("canwin.pill.bad", { n: v.bad || 0 });
  canWinDrawRows(v);
}
