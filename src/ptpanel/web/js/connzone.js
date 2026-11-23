"use strict";

// The wiring column at the left (PORTTOOL-FLOW.md C.3.6 「左 · 接线」, 「连接区」):
// the boards this PC sees, the control port, the far ends of the link ports,
// the reference board, this PC's devices. R&D is station 1: every row shows
// what its wiring registration chose, and a click opens that (gear.js).

// Each one is the far end of a loop=link port, bound through /api/link.
const CONN_LINKS = [
  { k: "usb",   name: "conn.usb",   d: "conn.usb_d" },
  { k: "eth",   name: "conn.eth",   d: "conn.eth_d" },
  { k: "rs485", name: "conn.rs485", d: "conn.rs485_d" },
  { k: "can",   name: "conn.can",   d: "conn.can_d" },
];

let connInfo = { clock: null, ref: {}, stlink: {} };
let connPick = {};       // row -> choice in its dropdown (the KNX interface), kept across redraws
let rdTile = {};         // station 1 on the big screen: the board seen, flashing (line.go rdAttach)
let rdRec = {};          // station 1's registration: control port, ST-Link
let connSig = "";        // what the rows were last drawn from
let connPolling = false;

// Adapter matching (decision 146, PORTTOOL-FLOW.md C.3.6 automatic adapter
// matching): the server's result per terminal, and the ports that appeared
// after it while a row waits for a manual choice (unplug and replug to find one).
let matchSeenAt = 0;     // the result the page last reacted to
let matchBase = null;    // port names present when that result arrived
let freshPorts = [];     // appeared since, newest last
let replugTick = 0;

function matchTerm(k) { return (((connInfo.match || {}).terms) || {})[k] || null; }

function connRow(key, label, title) {
  const r = el("div", "crow");
  r.dataset.conn = key;
  r.title = T(title);
  r.appendChild(el("span", "cl", T(label)));
  const v = el("span", "cv");
  r.appendChild(v);
  const dot = el("span", "dot");
  r.appendChild(dot);
  return { r, v, dot };
}

// A row shows a short value; the full one goes on its hover (decision 190).
function connShort(row, short, full) {
  const s = el("span", "mono", short);
  row.v.appendChild(s);
  if (full && full !== short) { row.r.title = row.r.title + "\n" + full; }
  return s;
}

// A row's state is its dot only (decision 170): green when connected or
// found, gray otherwise, the reason on hover. A gray row offers 重试 when
// retry is given; busy shows it disabled.
function connMark(row, ok, why, retry, busy) {
  row.dot.classList.toggle("ok", !!ok);
  if (why) {
    row.dot.title = why;
    row.r.title = row.r.title + "\n" + why;
  }
  if (ok || !retry) { return; }
  const b = el("button", null, T("conn.retry"));
  b.dataset.retry = row.r.dataset.conn;
  if (why) { b.title = why; }
  b.disabled = !!busy;
  b.onclick = async () => { b.disabled = true; await retry(); };
  row.v.appendChild(b);
}

function connSelect(key, choices, preferred) {
  const sel = el("select");
  for (const o of choices) {
    const op = el("option", null, o.label);
    op.value = o.name;
    sel.appendChild(op);
  }
  const want = connPick[key] || preferred;
  if (want && choices.some(o => o.name === want)) { sel.value = want; }
  sel.onchange = () => { connPick[key] = sel.value; };
  return sel;
}

// Serial ports other than the control port; the simulated board never stands
// in for an adapter.
function adapterChoices() {
  return (allPorts || []).filter(p => p.name !== state.port && p.name !== "sim" && !p.unavailable);
}

function linkConnRow(c) {
  const row = connRow(c.k, c.name, c.d);
  const { r, v } = row;
  if (!state.connected) {
    connMark(row, false, T("conn.need_control"));
    return r;
  }
  const p = (state.ports || []).find(x => x.name === c.k);
  if (!p || p.loop !== "link") {
    connMark(row, false, T("conn.no_port"));
    return r;
  }
  // Chosen in the registration, opened only by a test (decision 168): the
  // counts show while one has it open, and a failure turns the row gray with
  // the error on hover.
  const open = linkNow(c.k);
  const peerWhy = open && open.error ? T("conn.peer_err", { error: open.error }) : "";
  const mark = (ok, why, retry, busy) => {
    connMark(row, ok && !peerWhy, peerWhy || why, retry, busy);
    if (!open) { return; }
    const n = el("div", "ccount");
    n.dataset.linkcounts = c.k;
    n.textContent = linkCounts(open);
    r.appendChild(n);
  };
  if (c.k === "eth") {
    // Found by the server right after connecting (boardip.go), not from a test.
    const ip = connInfo.boardIP || state.boardIP || {};
    const again = async () => { connInfo = await api("/api/conn", { ip: true }); refreshConnRows(true); };
    if (!ip.addr) {
      const probing = ip.state === "probing";
      v.dataset.ethip = "";
      mark(false, probing ? T("conn.eth_probing") : (ip.why || T("conn.eth_probing")), again, probing);
      return r;
    }
    const fr = reads.eth;
    const addr = ip.addr + ":" + ((fr && fr.fields && fr.fields.port) || "5000");
    const shown = connShort(row, ip.addr, addr + (ip.sim ? " " + T("conn.eth_sim") : ""));
    shown.dataset.ethip = ip.addr;
    mark(true, "", again);
    return r;
  }
  const mt = matchTerm(c.k);
  const m = connInfo.match || {};
  const rematch = async () => {
    const j = await api("/api/match", {});
    if (j.error) { showMessage(j.error, true); } else { connInfo.match = j.match; }
    refreshConnRows(true);
  };
  // The simulated board plays its own peers: what matching found is all there is.
  if (mt && mt.ok && m.sim) {
    connShort(row, mt.com).dataset.matched = c.k;
    mark(true, mt.why, rematch);
    return r;
  }
  if (c.k === "usb") {
    const serial = (connInfo.usb || {}).board_serial || "";
    const found = usbFound();
    if (found && serial) { connShort(row, serial); }
    mark(found, found ? "" : T("conn.usb_unseen"), async () => { await loadPorts(); refreshConnRows(true); });
    return r;
  }
  const was = (((state.saved || {}).peers || {})[c.k] || {}).com || "";
  if (was) { connShort(row, was).dataset.bound = c.k; }
  // A port plugged in while the row waits is bound to it (「手选时拔插」):
  // CANables only to CAN, other serial ports only to RS485.
  const fits = o => c.k === "rs485" ? !o.canable : !!o.canable;
  const fresh = mt && !mt.ok && !was ? freshPorts.filter(n => adapterChoices().some(o => o.name === n && fits(o))).pop() : null;
  if (fresh) { api("/api/link", { port: c.k, com: fresh }).then(() => refreshState()); }
  const why = (mt && mt.why) || m.error || (!adapterChoices().length ? T("conn.no_adapter") : "");
  mark(!!was || !!(mt && mt.ok), why, rematch, !!m.running);
  return r;
}

// The board under test's own USB port is on this PC (by its USB serial,
// decision 163); the simulated board plays it itself.
function usbFound() {
  if (state.port === "sim") { return true; }
  const s = ((connInfo.usb || {}).board_serial || "").toUpperCase();
  return !!s && (allPorts || []).some(p => !p.unavailable && (p.serial || "").toUpperCase() === s);
}

// A bound peer: bound per the page's state (changed by bind / unbind at once),
// counts and error from the once-a-second /api/conn when it agrees.
function linkNow(k) {
  const b = (state.links || {})[k];
  if (!b) { return null; }
  const p = (connInfo.links || {})[k];
  return p && p.com === b.com ? p : b;
}
// While a row waits for a manual choice, the port list is looked at every two
// seconds: a port that appears (or comes back) is marked and chosen.
async function watchReplug() {
  const mt = connInfo.match || {};
  const waiting = state.connected && mt.at && !mt.running && !mt.sim &&
    ["rs485", "can"].some(k => { const t = matchTerm(k); return t && !t.ok && !linkNow(k); });
  if (!waiting) { matchBase = null; freshPorts = []; return; }
  if (++replugTick % 2 && matchBase) { return; }
  const j = await api("/api/ports");
  if (!j.ports) { return; }
  const now = j.ports.map(p => p.name);
  if (!matchBase) { matchBase = now.slice(); return; }
  const before = freshPorts.join(",");
  matchBase = matchBase.filter(n => now.includes(n));
  freshPorts = freshPorts.filter(n => now.includes(n));
  for (const n of now) {
    if (!matchBase.includes(n) && !freshPorts.includes(n)) { freshPorts.push(n); }
  }
  allPorts = j.ports;
  if (freshPorts.join(",") !== before) {
    for (const k of ["rs485", "can"]) { delete connPick[k]; }
    refreshConnRows(true);
  }
}

function linkCounts(b) { return T("conn.peer_counts", { rx_lines: b.rxLines || 0, echoed: b.echoed || 0 }); }

// Connected from the registration; 设置标准机 once it is (C.3.6 「标准机设置页」).
function refConnRow() {
  const row = connRow("ref", "conn.ref", "conn.ref_d");
  const ref = connInfo.ref || {};
  if (ref.connected) {
    connShort(row, ref.port, ref.port + (ref.firmware ? " · " + ref.firmware : ""));
    const set = el("button", null, T("refpg.open")); set.dataset.refopen = "1";
    set.onclick = () => openRefPage();
    row.v.appendChild(set);
  } else {
    closeRefPage();
    if (typeof stageCfgs !== "undefined" && stageCfgs && stageCfgs.ref_unused) { connShort(row, T("conn.ref_unused")); }
  }
  connMark(row, ref.connected && !ref.error, ref.error || "");
  return row.r;
}

function stlinkConnRow() {
  const row = connRow("stlink", "conn.stlink", "conn.stlink_d");
  const st = connInfo.stlink || {};
  const checking = !st.checked || st.busy;
  const found = !checking && (st.probes || []).length > 0;
  if (found) {
    const p = st.probes[0];
    connShort(row, T("conn.stlink_in"), (p.board || "ST-Link") + " · " + p.sn);
  }
  const why = checking ? T("conn.stlink_checking") : found ? "" : (st.error || T("conn.stlink_none"));
  connMark(row, found, why, async () => {
    connInfo = await api("/api/conn", { stlink: true });
    refreshConnRows(true);
  }, checking);
  return row.r;
}

function reservedConnRow() {
  const row = connRow("instrument", "conn.instrument", "conn.instrument_d");
  row.r.classList.add("reserved");
  connShort(row, T("conn.reserved"));
  return row.r;
}

// Station 1's control port as registered, and the board on it (C.3.6 「左 · 接线」).
function renderCtlRow() {
  const host = $("#ctlrow"); host.textContent = "";
  const row = connRow("control", "line.gear.control", "conn.rs232_d");
  const reg = rdRec.control || "";
  if (!reg) {
    connShort(row, T("conn.ctl_unreg"));
    connMark(row, false, "");
  } else {
    let txt = reg === "sim" ? T("line.set.sim") : reg;
    if (state.connected) {
      txt += "\u3000" + T("conn.ctl_board", { uid: tail8((connInfo.usb || {}).board_uid || rdTile.uid), fw: state.firmware || "?" });
    }
    connShort(row, txt, txt).dataset.ctl = reg;
    connMark(row, state.connected && !state.linkError, state.connected ? (state.linkError || "") : T("conn.ctl_wait"));
  }
  host.appendChild(row.r);
}

// 「电脑上看到的板子」: the board on the control port or behind the ST-Link
// (line.go rdAttach), and other OpenPLC boards on USB, by their USB serial.
function renderSeen() {
  const box = $("#seenbox"); box.textContent = "";
  box.appendChild(el("div", "cghead", T("conn.seen")));
  const t = rdTile || {}, u = connInfo.usb || {}, rows = [];
  const needs = t.state === "needflash" || t.state === "flashing";
  if (state.connected) {
    rows.push({ uid: tail8(u.board_uid || t.uid), via: state.port === "sim" ? T("line.set.sim") : state.port,
      fw: fwText({ fw: state.firmware, want: state.port === "sim" ? "" : t.want }), ok: !needs, flash: needs });
  } else if (needs) {
    rows.push({ uid: tail8(t.uid), via: t.uid ? t.port : "ST-Link", fw: fwText(t), ok: false, flash: true });
  }
  const mine = (u.board_serial || "").toUpperCase(), refS = (u.ref_serial || "").toUpperCase();
  for (const p of allPorts || []) {
    const s = (p.serial || "").toUpperCase();
    if (p.unavailable || !s || !/0483:5740/i.test(p.label || "") || s === mine || s === refS) { continue; }
    rows.push({ uid: s, via: "USB", fw: T("conn.seen.usb_fw"), ok: false });
  }
  if (!rows.length) { box.appendChild(el("div", "hint", T("conn.board.none"))); return; }
  for (const x of rows) {
    const r = el("div", "seenrow");
    r.appendChild(el("span", "dot" + (x.ok ? " ok" : "")));
    r.appendChild(el("span", "mono", x.uid));
    r.appendChild(el("span", "hint", x.via));
    r.appendChild(el("span", x.ok ? "" : "bad", x.fw));
    if (x.flash) {
      const b = el("button", null, T(t.state === "flashing" ? "conn.seen.flashing" : "conn.seen.flash"));
      b.dataset.flash = "1";
      b.disabled = t.state === "flashing" || !t.want;
      b.title = t.want ? T("conn.seen.flash_hint", { v: t.want }) : T("line.why.no_image");
      b.onclick = async () => {
        b.disabled = true;
        const j = await api("/api/line/act", { n: 1, act: "flash" });
        if (j.error) { showMessage(j.error, true); }
      };
      r.appendChild(b);
    } else { r.appendChild(el("span")); }
    box.appendChild(r);
  }
}

// Station 1's tile and registration, once a second with the connection rows.
async function pollStation1() {
  const [line, list] = await Promise.all([api("/api/line"), api("/api/stations")]);
  rdTile = (line.tiles || []).find(t => t.n === 1) || {};
  rdRec = (list.stations || []).find(s => s.n === 1) || {};
}

// After a change in the registration window: the state, the ports, the rows.
async function afterGear() {
  await pollStation1();
  await refreshState();
  await loadPorts();
  connInfo = await api("/api/conn");
  refreshConnRows(true);
}
$("#connzone").title = T("conn.zone_hint");
$("#connzone").onclick = e => {
  if (EMBED || e.target.closest("button, select, input, a, option, #leftgrip")) { return; }
  gearModal(1, afterGear);
};

// Redrawn only when what it shows changed, so an open dropdown is not pulled
// away from under the mouse once a second.
let refWasConnected = false;

function refreshConnRows(force) {
  const eth = JSON.stringify(connInfo.boardIP || state.boardIP || {});
  const sig = JSON.stringify([state.connected, state.port, Object.keys(state.links || {}),
    Object.keys(state.links || {}).map(k => (linkNow(k) || {}).error || ""),
    (allPorts || []).map(p => p.name), connInfo.ref, connInfo.stlink, eth, LANG, rdTile, rdRec, state.firmware, state.linkError,
    connInfo.match ? [connInfo.match.running, connInfo.match.at, connInfo.match.error] : null, freshPorts,
    !!(typeof stageCfgs !== "undefined" && stageCfgs && stageCfgs.ref_unused), connInfo.usb || null]);
  if (!force && sig === connSig) { return; }
  connSig = sig;
  // The AI / AO / DI / DO cards' default peer follows the reference board
  // (defaultPeer in shell.js, decision 112).
  const refNow = !!(connInfo.ref || {}).connected;
  if (refNow !== refWasConnected) { refWasConnected = refNow; if (state.connected) { renderShell(); } }
  renderSeen();
  renderCtlRow();
  const host = $("#connrows");
  host.textContent = "";
  // Grouped by machine (decision 163): the board under test, the reference
  // board, this PC's devices; the group's name heads it.
  const group = (k, title, rows) => {
    const g = el("div", "cgroup"); g.dataset.cgroup = k;
    g.appendChild(el("div", "cghead", T(title)));
    for (const r of rows) { g.appendChild(r); }
    host.appendChild(g);
  };
  group("dut", "line.gear.blk.dut", CONN_LINKS.map(linkConnRow));
  group("ref", "line.gear.blk.ref", [refConnRow()]);
  group("pc", "line.gear.blk.pc", [knxConnRow(), stlinkConnRow(), reservedConnRow()]);   // knxConnRow: knxwin.js
}

function renderClock() {
  const box = $("#boardclock");
  const c = connInfo.clock;
  box.classList.remove("bad");
  if (!state.connected) { box.textContent = ""; return; }
  if (!c) { box.textContent = T("conn.clock_wait"); return; }
  if (c.error) { box.classList.add("bad"); box.textContent = T("conn.clock_err", { error: c.error }); return; }
  box.textContent = T("conn.clock", { date: c.date, time: c.time }) + (c.init ? "" : " " + T("conn.clock_unset"));
}

// Once a second: the server reads the board's time at most that often and
// skips a turn while a command is on the control port (conn.go).
async function pollConn() {
  if (connPolling) { return; }
  connPolling = true;
  try {
    connInfo = await api("/api/conn");
    await pollStation1();
    // Station 1 connects and drops by itself (line.go rdAttach).
    if ("connected" in connInfo && connInfo.connected !== !!state.connected) { await refreshState(); }
    renderClock();
    // A finished match may have bound RS485 on the server: take the state with it.
    const at = (connInfo.match || {}).at || 0;
    if (at && at !== matchSeenAt) { matchSeenAt = at; matchBase = null; freshPorts = []; await refreshState(); }
    refreshConnRows(false);
    watchReplug();
    // Counts change every second: rewritten in place, not by a redraw that
    // would close an open dropdown on another row.
    for (const n of document.querySelectorAll("#connrows [data-linkcounts]")) {
      const b = linkNow(n.dataset.linkcounts);
      if (b) { n.textContent = linkCounts(b); }
    }
  } catch (e) { /* the panel process went away; the next poll says so */ }
  connPolling = false;
}

refreshConnRows(true);
setInterval(pollConn, 1000);
