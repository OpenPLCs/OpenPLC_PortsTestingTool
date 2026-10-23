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
  const { r, v, dot } = connRow(c.k, c.name, c.d);
  if (c.fixed) {
    v.appendChild(el("span", "sub", T("conn.rs232_ctl")));
    return r;
  }
  if (!state.connected) {
    v.appendChild(el("span", "sub", T("conn.need_control")));
    return r;
  }
  const p = (state.ports || []).find(x => x.name === c.k);
  if (!p || p.loop !== "link") {
    v.appendChild(el("span", "sub", T("conn.no_port")));
    return r;
  }
  const bound = linkNow(c.k);
  const mt = matchTerm(c.k);
  if (bound) {
    dot.classList.add(bound.error ? "bad" : "ok");
    v.appendChild(el("span", "mono", bound.com));
    const off = el("button", null, T("conn.unbind"));
    off.onclick = () => connAct(api("/api/unlink", { port: c.k }));
    v.appendChild(off);
    const n = el("div", "ccount");
    n.dataset.linkcounts = c.k;
    n.textContent = linkCounts(bound);
    r.appendChild(n);
    if (bound.error) { r.appendChild(el("div", "cerr", T("conn.peer_err", { error: bound.error }))); }
    if (mt && mt.ok && mt.com === bound.com) { r.appendChild(matchWhy(c.k, mt)); }
    return r;
  }
  if (c.k === "eth") {
    // Found by the server right after connecting (boardip.go), not from a test.
    const ip = connInfo.boardIP || state.boardIP || {};
    if (!ip.addr) {
      const why = ip.state === "probing" ? T("conn.eth_probing") : (ip.why || T("conn.eth_probing"));
      v.appendChild(el("span", "sub", why));
      v.dataset.ethip = "";
      return r;
    }
    const fr = reads.eth;
    const addr = ip.addr + ":" + ((fr && fr.fields && fr.fields.port) || "5000");
    const shown = el("span", "mono", addr);
    shown.dataset.ethip = ip.addr;
    v.appendChild(shown);
    if (ip.sim) { v.appendChild(el("span", "sub", T("conn.eth_sim"))); }
    const on = el("button", null, T("conn.bind"));
    on.onclick = () => connAct(api("/api/link", { port: "eth", tcp: addr }));
    v.appendChild(on);
    return r;
  }
  // Matched and not bound: the CAN adapter (found by USB id when a test needs
  // it), or anything on the simulated board, which plays its own peers.
  if (mt && mt.ok) {
    dot.classList.add("ok");
    const shown = el("span", "mono", mt.com);
    shown.dataset.matched = c.k;
    v.appendChild(shown);
    r.appendChild(matchWhy(c.k, mt));
    return r;
  }
  if (mt) { r.appendChild(matchWhy(c.k, mt)); }
  const was = ((state.saved || {}).peers || {})[c.k];
  // The board's own USB port: the one whose serial the server worked out from
  // this board's UID (decision 163); else the first ST virtual COM port.
  const own = c.k === "usb" ? connUsbOwn() : "";
  const choices = btShown(adapterChoices(), connPick[c.k] || own || (was && was.com)).map(o => freshPorts.includes(o.name)
    ? Object.assign({}, o, { label: o.label + T("conn.fresh") }) : o);
  if (!choices.length) {
    v.appendChild(el("span", "sub", T("conn.no_adapter")));
    return r;
  }
  const cdc = c.k === "usb" ? (choices.find(o => o.name === own) || choices.find(o => /0483:5740/i.test(o.label || ""))) : null;
  const fresh = mt ? freshPorts.filter(n => choices.some(o => o.name === n)).pop() : null;
  const sel = connSelect(c.k, choices, fresh || (own && cdc && cdc.name) || (was && was.com) || (cdc && cdc.name));
  if (fresh) { sel.value = fresh; sel.dataset.fresh = fresh; }
  v.appendChild(sel);
  const on = el("button", null, T("conn.bind"));
  on.onclick = () => connAct(api("/api/link", { port: c.k, com: sel.value }));
  v.appendChild(on);
  return r;
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

// The COM port whose USB serial is the board under test's, "" when none is listed.
function connUsbOwn() {
  if (connBoardPick === "sim") { return ""; }
  const want = (connBoardPick || (connInfo.usb || {}).board_serial || "").toUpperCase();
  const p = want ? (allPorts || []).find(x => (x.serial || "").toUpperCase() === want) : null;
  return p ? p.name : "";
}

// A bound peer: bound per the page's state (changed by bind / unbind at once),
// counts and error from the once-a-second /api/conn when it agrees.
function linkNow(k) {
  const b = (state.links || {})[k];
  if (!b) { return null; }
  const p = (connInfo.links || {})[k];
  return p && p.com === b.com ? p : b;
}
// One terminal's matching result, in a sentence under the row.
function matchWhy(k, mt) {
  const d = el("div", mt.ok ? "cok" : "cwhy", mt.why);
  d.dataset.matchwhy = k;
  return d;
}

// The matching row: when it last ran, and the button to run it again.
function matchConnRow() {
  const { r, v, dot } = connRow("match", "conn.match", "conn.match_d");
  if (!state.connected) {
    v.appendChild(el("span", "sub", T("conn.need_control")));
    return r;
  }
  const mt = connInfo.match || null;
  const st = el("span", "sub");
  if (mt && mt.running) {
    st.dataset.matchstate = "running";
    st.textContent = T("conn.match_running");
  } else if (mt && mt.at) {
    st.dataset.matchstate = "done";
    st.textContent = T("conn.match_at", { time: new Date(mt.at).toLocaleTimeString() });
  } else {
    st.dataset.matchstate = "never";
    st.textContent = T("conn.match_never");
  }
  v.appendChild(st);
  const again = el("button", null, T("conn.match_again"));
  again.dataset.matchagain = "1";
  again.disabled = !!(mt && mt.running);
  again.onclick = async () => {
    again.disabled = true;
    const j = await api("/api/match", {});
    if (j.error) { showMessage(j.error, true); }
    else { connInfo.match = j.match; }
    refreshConnRows(true);
  };
  v.appendChild(again);
  if (mt && mt.error) {
    dot.classList.add("bad");
    r.appendChild(el("div", "cerr", mt.error));
  }
  for (const u of (mt && mt.unseen) || []) { r.appendChild(el("div", "ccount", u)); }
  return r;
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

function refConnRow() {
  const { r, v, dot } = connRow("ref", "conn.ref", "conn.ref_d");
  const ref = connInfo.ref || {};
  if (ref.connected) {
    dot.classList.add("ok");
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
  if (ref.error) {
    dot.classList.add("bad");
    r.appendChild(el("div", "cerr", ref.error));
  }
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
  const { r, v, dot } = connRow("stlink", "conn.stlink", "conn.stlink_d");
  const st = connInfo.stlink || {};
  if (!st.checked || st.busy) {
    v.appendChild(el("span", "sub", T("conn.stlink_checking")));
  } else if ((st.probes || []).length) {
    dot.classList.add("ok");
    const p = st.probes[0];
    v.appendChild(el("span", "mono", (p.board || "ST-Link") + " · " + p.sn));
  } else if (st.error) {
    dot.classList.add("bad");
    v.appendChild(el("span", "sub", st.error));
  } else {
    v.appendChild(el("span", "sub", T("conn.stlink_none")));
  }
  const again = el("button", null, T("conn.recheck"));
  again.disabled = !!st.busy;
  again.onclick = async () => {
    again.disabled = true;
    connInfo = await api("/api/conn", { stlink: true });
    refreshConnRows(true);
  };
  v.appendChild(again);
  return r;
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
  sel.onchange = () => { connBoardPick = sel.value; delete connPick.usb; refreshConnRows(true); };
  head.appendChild(sel);
  group("dut", CONN_LINKS.map(linkConnRow).concat([matchConnRow()]), head);
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
