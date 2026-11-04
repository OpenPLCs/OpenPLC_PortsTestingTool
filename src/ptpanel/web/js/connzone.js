"use strict";

// The connection block at the top left (decision 103): board time under
// the control port, the far ends of the link ports, the reference board, the
// ST-Link and the reserved instrument row. PORTTOOL-FLOW.md C.3.6, "连接区".

// Each one is the far end of a loop=link port, bound through /api/link.
const CONN_LINKS = [
  { k: "usb",   name: "conn.usb",   d: "conn.usb_d" },
  { k: "eth",   name: "conn.eth",   d: "conn.eth_d" },
  { k: "rs232", name: "conn.rs232", d: "conn.rs232_d", fixed: true },
  { k: "rs485", name: "conn.rs485", d: "conn.rs485_d" },
  { k: "can",   name: "conn.can",   d: "conn.can_d" },
];

let connInfo = { clock: null, ref: {}, stlink: {} };
let connPick = {};       // row -> COM chosen in its dropdown, kept across redraws
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

async function connAct(promise) {
  const j = await promise;
  if (j && j.error) { showMessage(j.error, true); return; }
  if (j && "links" in j) { applyState(j); }
  else if (j && "firmware" in j) { connInfo.ref = j; }   // an /api/ref answer
  refreshConnRows(true);
}

function linkConnRow(c) {
  const row = connRow(c.k, c.name, c.d);
  const { r, v } = row;
  if (c.fixed) {
    connMark(row, state.connected, T(state.connected ? "conn.rs232_ctl" : "conn.need_control"));
    return r;
  }
  if (!state.connected) {
    connMark(row, false, T("conn.need_control"));
    return r;
  }
  const p = (state.ports || []).find(x => x.name === c.k);
  if (!p || p.loop !== "link") {
    connMark(row, false, T("conn.no_port"));
    return r;
  }
  // Choose only (decision 168): a test opens the chosen port when it starts
  // and closes it when it ends; the counts show while one has it open, and a
  // failure turns the row gray with the error on hover.
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
    const shown = el("span", "mono", addr);
    shown.dataset.ethip = ip.addr;
    v.appendChild(shown);
    if (ip.sim) { v.appendChild(el("span", "sub", T("conn.eth_sim"))); }
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
    const shown = el("span", "mono", mt.com);
    shown.dataset.matched = c.k;
    v.appendChild(shown);
    mark(true, mt.why, rematch);
    return r;
  }
  const was = (((state.saved || {}).peers || {})[c.k] || {}).com || "";
  // RS485 never takes a CANable; a fresh port is picked only in its own kind's row.
  const fits = o => c.k === "rs485" ? !o.canable : c.k !== "can" || !!o.canable;
  const choices = btShown(adapterChoices(), was).filter(o => c.k !== "rs485" || !o.canable).map(o => freshPorts.includes(o.name)
    ? Object.assign({}, o, { label: o.label + T("conn.fresh") }) : o);
  // usb and CAN fall back to finding the port by USB id; RS485 has no such id.
  const none = { name: "", label: T(c.k === "rs485" ? "conn.pick_none" : "conn.pick_auto") };
  const fresh = mt && !mt.ok ? freshPorts.filter(n => choices.some(o => o.name === n && fits(o))).pop() : null;
  const sel = connSelect(c.k, [none].concat(choices), was);
  sel.value = choices.some(o => o.name === was) ? was : "";
  sel.dataset.pick = c.k;
  const choose = com => connAct(com ? api("/api/link", { port: c.k, com }) : api("/api/unlink", { port: c.k }));
  sel.onchange = () => { connPick[c.k] = sel.value; choose(sel.value); };
  v.appendChild(sel);
  if (fresh && fresh !== was) { sel.value = fresh; sel.dataset.fresh = fresh; choose(fresh); }
  const why = (mt && mt.why) || (c.k !== "usb" && m.error) || (!choices.length ? T("conn.no_adapter") : "");
  if (c.k === "usb") {
    mark(!!was || usbFound(), why, async () => { await loadPorts(); refreshConnRows(true); });
  } else {
    mark(!!was || !!(mt && mt.ok), why, rematch, !!m.running);
  }
  return r;
}

// The board under test's own USB port is on this PC (by its USB serial,
// decision 163); the simulated board plays it itself.
function usbFound() {
  if (connBoardShown === "sim") { return true; }
  const s = (connBoardShown || "").toUpperCase();
  return !!s && (allPorts || []).some(p => !p.unavailable && (p.serial || "").toUpperCase() === s);
}

// The boards visible on this PC for the 被测板 dropdown (decision 163): the
// connected one by its UID tail, the others by USB serial (the board's own
// USB id), the simulated board as one entry. The reference board is not one.
function connBoards() {
  const u = connInfo.usb || {}, out = [];
  const mine = (u.board_serial || "").toUpperCase(), refS = (u.ref_serial || "").toUpperCase();
  if (state.connected && u.board_uid) { out.push({ v: mine || "uid", label: T("conn.board.uid", { uid: u.board_uid.slice(-6) }) }); }
  for (const p of allPorts || []) {
    const s = (p.serial || "").toUpperCase();
    if (!p.unavailable && p.name === "sim") { out.push({ v: "sim", label: T("conn.board.sim") }); continue; }
    if (!s || !/0483:5740/i.test(p.label || "") || s === mine || s === refS) { continue; }
    if (!out.some(o => o.v === s)) { out.push({ v: s, label: T("conn.board.serial", { serial: s }) }); }
  }
  return out;
}
let connBoardPick = "";   // the 被测板 dropdown's choice: a USB serial, or "sim"
let connBoardShown = "";  // what that dropdown shows, the choice or its first entry

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

// Its 连接 button is its retry (decision 170).
function refConnRow() {
  const row = connRow("ref", "conn.ref", "conn.ref_d");
  const { r, v } = row;
  const ref = connInfo.ref || {};
  if (ref.connected) {
    v.appendChild(el("span", "mono", ref.port + (ref.firmware ? " · " + ref.firmware : "")));
    const off = el("button", null, T("conn.disconnect"));
    off.onclick = () => connAct(api("/api/ref", { port: "" }));
    v.appendChild(off);
    const set = el("button", null, T("refpg.open")); set.dataset.refopen = "1";
    set.onclick = () => openRefPage();
    v.appendChild(set);
  } else {
    closeRefPage();
    // The simulated board is allowed here even when it is the control port too:
    // each "sim" is its own process.
    const choices = btShown((allPorts || []).filter(p => !p.unavailable && (p.name === "sim" || p.name !== state.port)), connPick.ref);
    // "Not used" is saved with the stage configs (ref_unused): CAN exchange turns
    // to the PC, the other reference-board items cannot run (decision 135).
    const unused = !!(stageCfgs && stageCfgs.ref_unused);
    const sel = connSelect("ref", choices.concat([{ name: REF_UNUSED, label: T("conn.ref_unused") }]),
                           unused ? REF_UNUSED : null);
    if (unused) { sel.value = REF_UNUSED; }
    sel.onchange = () => {
      connPick.ref = sel.value;
      if ((sel.value === REF_UNUSED) !== unused) { setRefUnused(sel.value === REF_UNUSED); }
    };
    v.appendChild(sel);
    const on = el("button", null, T("conn.connect"));
    on.disabled = unused || !choices.length;
    on.onclick = async () => {
      await connAct(api("/api/ref", { port: sel.value }));
      if ((connInfo.ref || {}).connected && unused) { setRefUnused(false); }
    };
    v.appendChild(on);
  }
  connMark(row, ref.connected && !ref.error, ref.error || "");
  return r;
}

const REF_UNUSED = "-";


async function setRefUnused(on) {
  const j = await api("/api/stagecfg", { ref_unused: on });
  stageCfgs = j;
  if (j.error) { showMessage(j.error, true); }
  renderShell();
  refreshConnRows(true);
}

function stlinkConnRow() {
  const row = connRow("stlink", "conn.stlink", "conn.stlink_d");
  const st = connInfo.stlink || {};
  const checking = !st.checked || st.busy;
  const found = !checking && (st.probes || []).length > 0;
  if (found) {
    const p = st.probes[0];
    row.v.appendChild(el("span", "mono", (p.board || "ST-Link") + " · " + p.sn));
  }
  const why = checking ? T("conn.stlink_checking") : found ? "" : (st.error || T("conn.stlink_none"));
  connMark(row, found, why, async () => {
    connInfo = await api("/api/conn", { stlink: true });
    refreshConnRows(true);
  }, checking);
  return row.r;
}

function reservedConnRow() {
  const { r, v } = connRow("instrument", "conn.instrument", "conn.instrument_d");
  r.classList.add("reserved");
  const sel = el("select");
  sel.disabled = true;
  sel.appendChild(el("option", null, T("conn.reserved")));
  v.appendChild(sel);
  return r;
}

// Redrawn only when what it shows changed, so an open dropdown is not pulled
// away from under the mouse once a second.
let refWasConnected = false;

function refreshConnRows(force) {
  const eth = JSON.stringify(connInfo.boardIP || state.boardIP || {});
  const sig = JSON.stringify([state.connected, state.port, Object.keys(state.links || {}),
    Object.keys(state.links || {}).map(k => (linkNow(k) || {}).error || ""),
    (allPorts || []).map(p => p.name), connInfo.ref, connInfo.stlink, eth, LANG,
    connInfo.match ? [connInfo.match.running, connInfo.match.at, connInfo.match.error] : null, freshPorts,
    !!(typeof stageCfgs !== "undefined" && stageCfgs && stageCfgs.ref_unused), connInfo.usb || null]);
  if (!force && sig === connSig) { return; }
  connSig = sig;
  // The AI / AO / DI / DO cards' default peer follows the reference board
  // (defaultPeer in shell.js, decision 112).
  const refNow = !!(connInfo.ref || {}).connected;
  if (refNow !== refWasConnected) { refWasConnected = refNow; if (state.connected) { renderShell(); } }
  const host = $("#connrows");
  host.textContent = "";
  // Grouped by machine (decision 163): the board under test, the reference
  // board, this PC's devices.
  const u = connInfo.usb || {};
  // Only the board under test has a header: a dropdown of the boards visible here.
  const group = (k, rows, head) => {
    const g = el("div", "cgroup"); g.dataset.cgroup = k;
    if (head) { g.appendChild(head); }
    for (const r of rows) { g.appendChild(r); }
    host.appendChild(g);
  };
  const head = el("div", "cghead");
  head.appendChild(el("span", null, T("conn.grp.dut")));
  const boards = connBoards(), sel = el("select"); sel.dataset.connboard = "1";
  for (const b of boards) { const o = el("option", null, b.label); o.value = b.v; sel.appendChild(o); }
  if (!boards.length) { sel.appendChild(el("option", null, T("conn.board.none"))); sel.disabled = true; }
  sel.value = boards.some(b => b.v === connBoardPick) ? connBoardPick : (boards[0] || {}).v;
  connBoardShown = boards.length ? sel.value : "";
  sel.onchange = () => { connBoardPick = sel.value; delete connPick.usb; refreshConnRows(true); };
  head.appendChild(sel);
  group("dut", CONN_LINKS.map(linkConnRow), head);
  group("ref", [refConnRow()]);
  group("pc", [knxConnRow(), stlinkConnRow(), reservedConnRow()]);   // knxConnRow: knxwin.js
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
