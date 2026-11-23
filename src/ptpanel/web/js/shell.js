"use strict";

// The shell (decisions 97, 125, 128; PORTTOOL-FLOW.md C.3.6). The port list
// only chooses what is shown; R&D's group Starts start tests. The test type is
// the top bar's chip (gear.js); the fixture test is the big screen.

// The full test's sub-tests and the times each offers (decision 125; same
// lists as stageDurations in stagecfg.go). Step numbers stay internal (decision 122).
const FULL_SUBS = [
  { k: "func",   step: 2, name: "shell.type.func",   durs: ["once"] },   // single run only (decision 139)
  { k: "safety", step: 5, name: "shell.type.safety", durs: null },
  { k: "user",   step: 6, name: "shell.type.user",   durs: ["once", "10m", "30m", "1"] },
];
// R&D aging is an R&D test with a duration, not a page of its own (decisions 190, 191).
const RUN_SUBS = FULL_SUBS;
// The template window's five steps (PORTTOOL-FLOW.md C.3.10 「模板」); ③ has no configuration yet.
const TPL_SUBS = [
  { k: "func", step: 2, name: "line.step.1" }, { k: "aging", name: "line.step.2" }, { k: "check", name: "line.step.3" },
  { k: "safety", step: 5, name: "line.step.4" }, { k: "user", step: 6, name: "line.step.5" },
];
const PEER_NAME = { none: "lib.peer.none", pc: "lib.peer.pc", ref: "lib.peer.ref", human: "lib.peer.human", ext: "lib.peer.ext", self: "wire.self" };

// R&D's wiring (decision 191, PORTTOOL-FLOW.md C.3.8): DI / DO / relays share
// one, AI / AO another; it is the peer of every item on those pages.
const WIRE_PAIR = { din: "dio", dout: "dio", relay: "dio", ain: "aio", aout: "aio" };
const WIRES = ["self", "ref", "ext", "human"];
const WIRE_NAME = { self: "wire.self", ref: "wire.ref", ext: "wire.ext", human: "wire.human" };
const WIRE_SHARED = { dio: "wire.shared.dio", aio: "wire.shared.aio" };
const WIRE_HOW = {
  dio: { self: "wire.how.dio.self", ref: "wire.how.dio.ref", ext: "wire.how.dio.ext", human: "wire.how.dio.human" },
  aio: { self: "wire.how.aio.self", ref: "wire.how.aio.ref", ext: "wire.how.aio.ext", human: "wire.how.aio.human" },
};
let rdWire = { dio: "human", aio: "human" };
try { Object.assign(rdWire, JSON.parse(localStorage.getItem("pt.rdwire") || "{}")); } catch (e) { /* defaults */ }
function setWire(pair, w) {
  rdWire[pair] = w;
  try { localStorage.setItem("pt.rdwire", JSON.stringify(rdWire)); } catch (e) { /* kept for this page only */ }
}
const wireOf = port => WIRE_PAIR[port] ? rdWire[WIRE_PAIR[port]] : "";
function wirePicker(port) {
  const pair = WIRE_PAIR[port], box = el("span", "wirebox");
  box.appendChild(el("span", "hint", T("wire.label")));
  const s = el("select"); s.dataset.wire = pair; s.title = T(WIRE_SHARED[pair]);
  for (const w of WIRES) { const o = el("option", null, T(WIRE_NAME[w])); o.value = w; o.selected = rdWire[pair] === w; s.appendChild(o); }
  s.disabled = libRunning();
  s.onchange = () => { setWire(pair, s.value); renderRight(); };
  box.appendChild(s);
  return box;
}
const DURATIONS = ["once", "1", "2", "3", "4", "forever"];   // R&D's repeatable group

// view is "rd" or one of the full test's sub-tests: the sub-test on screen is
// the configuration its cards edit.
let view = EMBED ? "func" : "rd";
let fullSub = "func";     // the sub-test 工装完整测试 opens on
let lport = null;         // the port picked in the middle
let catalog = { ports: [], items: [] };
let catalogKey = "";      // what the catalogue was fetched for
let itemOff = {};         // `${view}|${item}` -> true unticked, false ticked
let itemPeer = {};        // `${view}|${item}` -> chosen peer

// R&D's wired ports take the page's wiring, other R&D items their R&D peers
// (decision 191, ptitems.RDPeers); a configuration offers every peer of the item.
const peersIn = (v, it) => isCfgView(v) ? it.peers
  : WIRE_PAIR[it.port] ? [wireOf(it.port)] : (it.rd_peers || it.peers);
// An R&D item's default peer: its first R&D peer (Engine.withDefaultPeers).
function defaultPeer(it) { return peersIn("rd", it)[0]; }
let duration = { rd: "once", func: "once", aging: "4", user: "once" };

// Same rule as ptitems.Excludes: one exclusive group and both once-only or both
// repeatable (decision 128); in R&D by card group, which runs on its own Start.
function excludes(v, a, b) {
  return a.id !== b.id && !!a.exclusive && a.exclusive === b.exclusive
    && (v === "rd" ? itemKind(a, v) === itemKind(b, v) : !!a.once === !!b.once);
}

// Ticked unless changed; of items excluding each other only the first starts ticked.
function itemOnIn(v, it) {
  if (isCfgView(v)) { return cfgPortOn(v, it.port) && cfgOn(v, it); } // remembered: stagecfg.js
  const k = v + "|" + it.id;
  if (k in itemOff) { return !itemOff[k]; }
  if (groupDefaultOff(v, it)) { return false; } // group.js
  // In R&D the 「只测一次」 cards carry no tick (decision 116), so only the
  // repeatable ones compete for the first place.
  return !it.exclusive || itemsOf(v, it.port).find(o => o === it || excludes(v, o, it)) === it;
}

// A sub-test of the full test's page.
const isFullSub = v => (EMBED ? TPL_SUBS : RUN_SUBS).some(s => s.k === v);
const hasPorts = v => v === "rd" || (isCfgView(v) && !(EMBED && v === "aging"));
// R&D's wired ports list only the items the page's wiring can run (decision 191).
const itemsOf = (v, port) => catalog.items.filter(it => it.port === port && it.stages.includes(v)
  && (v !== "rd" || !WIRE_PAIR[port] || (it.rd_peers || []).includes(wireOf(port))));

// Called from applyState: the catalogue depends on the board's caps and on
// which plan supplies the criteria.
function shellOnState(st) {
  // The template window configures every port, board or not (?all=1).
  const key = EMBED ? "embed" : st.connected ? [st.port, st.firmware, st.criteriaPlan].join("|") : "";
  if (key !== catalogKey) {
    catalogKey = key;
    if (!key) { catalog = { ports: [], items: [] }; renderShell(); return; }
    api(EMBED ? "/api/items?all=1" : "/api/items").then(j => {
      catalog = { ports: j.ports || [], items: j.items || [] };
      if (!catalog.ports.some(p => p.port === lport)) { lport = catalog.ports.length ? catalog.ports[0].port : null; }
      renderShell();
      libRefresh(); // results and parameter defaults (library.js)
    });
    return;
  }
  renderShell();
}

function renderShell() {
  if (!stageCfgs) { loadStageCfgs(); }
  renderLports();
  renderRight();
}

// Leaving an R&D port page ends what runs on it and keeps the round
// (decision 137, rdvisit.js): nobody is looking at that page any more.
function outputsOff() { rdLeave(); }

window.addEventListener("beforeunload", rdUnload);

// Menus only: picking one sends nothing to the board (decision 97), apart
// from turning off what a card left on.
function pickView(v) {
  if (v === "full") { v = fullSub; }
  // A running test keeps the page on its own page (decision 128).
  if (typeof fullRunning === "function" && fullRunning() && !isFullSub(v)) { return; }
  if (isFullSub(v)) { fullSub = v; }
  const was = view;
  if (v !== view) { outputsOff(); }
  view = v;
  if (hasPorts(v) && lport && !itemsOf(v, lport).length) {
    const first = catalog.ports.find(p => itemsOf(v, p.port).length);
    lport = first ? first.port : lport;
  }
  renderShell();
  if (isFullSub(v) && !isFullSub(was)) { flowLoad("full"); }     // flows.js
}

function renderLports() {
  const host = $("#lports"); host.textContent = "";
  const sub = RUN_SUBS.find(s => s.k === view);
  $("#midtitle").textContent = sub ? T("full.ports_of", { sub: T(sub.name) }) : T("shell.ports");
  if (view === "safety" || (EMBED && !hasPorts(view))) {
    if (!EMBED) { host.appendChild(el("div", "hint", T("shell.safety.noports"))); }
    return;
  }
  const order = ["bridge", "upper", "lower", "junction", "whole"];
  const boards = [...new Set(catalog.ports.map(p => p.board || "other"))]
    .sort((a, b) => (order.indexOf(a) + 1 || 99) - (order.indexOf(b) + 1 || 99));
  for (const b of boards) {
    host.appendChild(el("div", "boardhd", BOARD_LABEL[b] || b));
    for (const p of catalog.ports.filter(x => (x.board || "other") === b)) {
      const n = cardsOf(view, p.port).length;
      const row = el("div", "lport" + (n ? "" : " dim") + (p.port === lport ? " cur" : ""));
      row.dataset.lport = p.port;
      if (n && isCfgView(view)) {
        // Which ports a configuration tests; part of the remembered config.
        const pc = el("input"); pc.type = "checkbox"; pc.dataset.cfgport = p.port;
        pc.checked = cfgPortOn(view, p.port);
        pc.onclick = e => e.stopPropagation();
        pc.onchange = () => cfgSetPort(view, p.port, pc.checked);
        row.appendChild(pc);
      }
      if (sub) { row.appendChild(runPortDot(sub.step, p.port)); } // runview.js
      row.appendChild(el("span", "nm", p.label));
      if (sub && n) {
        // Ticked of all this sub-test's items on the port (decision 128).
        const all = itemsOf(view, p.port);
        const on = cfgPortOn(view, p.port) ? all.filter(it => cfgOn(view, it)).length : 0;
        const c = el("span", "cnt", T("full.port_ticked", { n: on, of: all.length }));
        c.dataset.ticked = p.port;
        row.appendChild(c);
      } else {
        row.appendChild(el("span", "cnt", n ? T("shell.port.count", { n }) : "—"));
      }
      if (!sub) { row.appendChild(libPortBadge(p.port)); }
      row.title = n ? T("shell.port.term", { term: p.term }) : T("shell.port.none");
      if (n) {
        row.onclick = () => {
          if (p.port !== lport) { outputsOff(); }
          lport = p.port;
          renderShell();
        };
      }
      host.appendChild(row);
    }
  }
}

// Not redrawn while a button is held on the right: a press and release on two
// copies of a button is no click (PORTTOOL-FLOW.md C.3.6 「按住时不重画」).
let rightHeld = false, rightPending = false;
document.addEventListener("pointerdown", e => {
  if (e.target.closest && e.target.closest("#rtop, #itemcards")) { rightHeld = true; }
}, true);
function rightRelease() {
  if (!rightHeld) { return; }
  rightHeld = false;
  // After the click has fired.
  if (rightPending) { rightPending = false; setTimeout(renderRight, 0); }
}
document.addEventListener("pointerup", rightRelease, true);
document.addEventListener("pointercancel", rightRelease, true);
window.addEventListener("blur", rightRelease);

function renderRight() {
  if (rightHeld) { rightPending = true; return; }
  const top = $("#rtop"), cards = $("#itemcards"), note = $("#viewnote");
  top.textContent = ""; cards.textContent = ""; note.textContent = ""; $("#subtiles").textContent = "";
  $("#fullview").hidden = !isFullSub(view);
  $("#shell").className = "shell " + (isFullSub(view) ? "full" : "rd");
  cards.classList.toggle("locked", isFullSub(view) && fullRunning());

  if (EMBED) { tplStep(top, $("#subtiles"), cards); return; }                     // flows.js
  if (isFullSub(view)) { renderSubTest(top, cards, note); runHighlight(); return; } // runview.js

  const port = catalog.ports.find(p => p.port === lport);
  if (!port) {
    top.appendChild(el("h2", null, T("shell.pick_port")));
    top.appendChild(el("span", "sp"));
    top.appendChild(libRunNote());
    return;
  }
  rdEnter(port.port);
  // One header block (decision 139 item 1): name and terminals on the left,
  // stats, last test and its log on the right (PORTTOOL-FLOW.md C.3.6 「页头和日志」).
  const left = el("div", "phl"), right = el("div", "phr");
  left.appendChild(el("h2", null, port.label));
  left.appendChild(el("span", "sub", T("shell.port.term", { term: port.term })));
  if (WIRE_PAIR[port.port]) { left.appendChild(wirePicker(port.port)); }
  right.appendChild(rdLastBox(port.port));
  top.append(left, right);
  if (WIRE_PAIR[port.port]) {
    const pair = WIRE_PAIR[port.port];
    const how = el("div", "hint wirehow", T(WIRE_HOW[pair][rdWire[pair]]) + T("wire.shared_note", { who: T(WIRE_SHARED[pair]) }));
    how.dataset.wirehow = pair;
    cards.appendChild(how);
  }
  renderPortCards(cards, port);
}

// 工装完整测试: layers 1 and 2 on top (flows.js), then layer 3 - the sub-test's
// cards for one port, settings and criteria only; shown but locked while a run
// goes (decisions 125, 128; PORTTOOL-FLOW.md C.3.6).
function renderSubTest(top, cards, note) {
  fullTop(top, $("#subtiles"));
  if (view === "safety") { note.textContent = T("shell.safety.what"); return; }
  const port = catalog.ports.find(p => p.port === lport);
  if (!port) { return; }
  const sub = RUN_SUBS.find(s => s.k === view);
  const hd = el("div", "sechd", T("full.sechd", { sub: T(sub.name), port: port.label }));
  hd.dataset.sechd = view + "|" + port.port;
  cards.appendChild(hd);
  if (!cfgPortOn(view, port.port)) {
    cards.appendChild(el("div", "hint", T("full.port_off", { sub: T(sub.name) }))).dataset.portoff = port.port;
    return;
  }
  cards.appendChild(el("div", "hint", T("shell.cfg_note")));
  // Step 6 runs the demo on a real board (PORTTOOL-FLOW.md C.3.6 「功能 / 老化 / 用户级有什么不同」).
  if (view === "user") { cards.appendChild(el("div", "hint warn", T("cfg.user_demo"))).dataset.userdemo = "1"; }
  renderPortCards(cards, port);
}

// One port's cards, in R&D or in a sub-test's configuration.
function renderPortCards(cards, port) {
  // Always there in R&D, whatever runs: tests release their ports when they
  // end, this is for when one did not (decision 168).
  if (view === "rd") { cards.appendChild(releaseBar()); }
  // A configuration's AI / AO / DI / DO card: the same shape, settings only (decision 121, cfgcard.js).
  if (isCfgView(view) && ["ain", "aout", "din", "dout"].includes(port.port)) { cards.appendChild(cfgPortCard(port)); return; }
  // R&D's AI / AO is one card of its own (decision 117, anacard.js).
  const byHand = wireOf(port.port) === "human"; // other wirings run as plain item cards (decision 191)
  if (view === "rd" && byHand && (port.port === "ain" || port.port === "aout")) { cards.appendChild(anaPortCard(port)); return; }
  // DI / DO the same way (decision 119, dio.js).
  if (view === "rd" && byHand && (port.port === "din" || port.port === "dout")) { cards.appendChild(dioPortCard(port)); return; }
  // Relays: one card in every test type, per-relay rows (decision 123, relay.js).
  if (port.port === "relay") { cards.appendChild(relayCard(port)); return; }
  // Two groups (decision 115): checked once on top, repeatable for a duration
  // below. In R&D each has its own Start (library.js); in a sub-test they are
  // headings only, its time sits on layer 2 (decision 125).
  const all = cardsOf(view, port.port);
  for (const kind of ["once", "repeat"]) {
    const cs = all.filter(c => cardKind(c, view) === kind);
    const runs = itemsOf(view, port.port).filter(it => itemKind(it, view) === kind && itemOnIn(view, it));
    // R&D's SD file transfer is a one-off, so it heads the 「只测一次」 group (decision 120, sdfile.js).
    const sdf = kind === "once" && view === "rd" && port.port === "sd";
    if (!cs.length && !runs.length && !sdf) { continue; }
    const sec = el("div", "kgroup"); sec.dataset.kind = kind;
    const hd = el("div", "khead");
    hd.appendChild(el("b", null, T(isCfgView(view) ? "cfg.kind.repeat" : "shell.kind.repeat")));
    hd.appendChild(el("span", "sp"));
    if (kind === "repeat" && view === "rd") { hd.appendChild(durationPicker()); }
    // R&D runs a 「只测一次」 card from its own Start, no ticks (decision 116);
    // in a configuration the ticks choose what the full test runs.
    const solo = kind === "once" && view === "rd";
    if (view === "rd" && !solo) {
      const ticked = kind === "once" ? cs.filter(c => cardOn(view, c)).length : runs.length;
      const start = el("button", "primary", T("shell.start_n", { n: ticked }));
      start.id = kind === "once" ? "shellstart1" : "shellstart";
      start.dataset.start = kind;
      libStartButton(start, ticked, kind);
      hd.appendChild(start);
    }
    // The top group has no heading: nothing on the panel says 「只测一次」 (decision 151).
    if (kind !== "once") { sec.appendChild(hd); }
    if (sdf) { sec.appendChild(sdfPanel()); }
    for (const c of cs) { sec.appendChild(c.group ? groupCard(c, solo) : itemCard(c.items[0], solo)); }
    cards.appendChild(sec);
  }
}

// 「强制释放端口」: stop every test and close every far end the panel holds.
function releaseBar() {
  const bar = el("div", "khead");
  bar.appendChild(el("span", "sp"));
  const b = el("button", null, T("lib.release"));
  b.title = T("lib.release_d");
  b.dataset.release = "1";
  b.onclick = async () => {
    b.disabled = true;
    const j = await api("/api/release", {});
    b.disabled = false;
    if (j && j.error) { showMessage(j.error, true); }
    refreshConnRows(true);
  };
  bar.appendChild(b);
  return bar;
}

// R&D's repeatable group: how long it runs.
function durationPicker() {
  const lb = el("label", null, T("shell.duration") + " ");
  const sel = el("select"); sel.dataset.shell = "duration";
  for (const d of DURATIONS) {
    const o = el("option", null, durLabel(d));
    o.value = d; o.selected = duration[view] === d; sel.appendChild(o);
  }
  sel.onchange = () => { duration[view] = sel.value; };
  lb.appendChild(sel);
  return lb;
}

// A duration key in words: once, 10m / 30m, forever, else hours.
function durLabel(d) {
  if (d === "once") { return T("shell.dur.once"); }
  if (d === "forever") { return T("shell.dur.forever"); }
  if (/^[0-9]+m$/.test(d)) { return T("shell.dur.min", { n: d.slice(0, -1) }); }
  return T("shell.dur.hours", { n: d });
}

// An item's 目标 and 测试方法 (decision 174): how is one step per line; a
// line starting with "• " is a note under the steps. Headings only when asked.
function goalHow(box, goal, how, headings) {
  if (headings) { box.appendChild(el("h3", null, T("shell.item.goal"))); }
  box.appendChild(el("div", null, goal));
  if (headings) { box.appendChild(el("h3", null, T("shell.item.how"))); }
  const lines = String(how || "").split(String.fromCharCode(10)).filter(l => l.trim());
  const steps = lines.filter(l => !l.startsWith("• ")), notes = lines.filter(l => l.startsWith("• "));
  if (steps.length > 1) {
    const ol = el("ol", "steps");
    for (const s of steps) { ol.appendChild(el("li", null, s)); }
    box.appendChild(ol);
  } else if (steps.length) {
    box.appendChild(el("div", null, steps[0]));
  }
  for (const n of notes) { box.appendChild(el("div", "hint", n.slice(2))); }
}

function itemCard(it, solo) {
  const key = view + "|" + it.id;
  const on = solo || itemOnIn(view, it);
  const card = el("div", "item" + (on ? "" : " off"));
  card.dataset.item = it.id;

  const hd = el("div", "ih");
  const c = el("input"); c.type = "checkbox"; c.checked = on; c.dataset.pick = it.id;
  c.disabled = libRunning();
  c.onchange = () => {
    if (isCfgView(view)) { cfgTick(view, it, c.checked); return; }
    itemOff[key] = !c.checked;
    // One session cannot run in two modes at once (decision 102).
    if (c.checked && it.exclusive) {
      for (const o of itemsOf(view, it.port)) {
        if (excludes(view, o, it)) { itemOff[view + "|" + o.id] = true; }
      }
    }
    renderRight();
  };
  if (!solo) { hd.appendChild(c); }
  hd.appendChild(el("b", null, it.name));
  // library.js: the crystal cards start beside their parameter; CAN analysis starts from 打开高级窗口 (decision 173).
  if (solo && !INLINE_START[it.id] && it.id !== "can.bus") { hd.appendChild(libItemStart([it.id])); }
  if (it.pending) { hd.appendChild(el("span", "todo", it.pending)); }
  hd.appendChild(el("span", "sp"));
  if (!isCfgView(view)) { hd.appendChild(libPill(it)); } // a sub-test's card shows no result (decision 125)
  card.appendChild(hd);

  const body = el("div", "ib");
  const left = el("div");
  goalHow(left, it.goal, it.how, true);

  left.appendChild(el("h3", null, T("shell.item.peer")));
  const peers = peersIn(view, it);
  if (peers.length > 1) {
    const ps = el("select"); ps.dataset.peer = it.id;
    for (const p of peers) {
      const o = el("option", null, T(PEER_NAME[p])); o.value = p;
      o.selected = (isCfgView(view) ? cfgPeer(view, it) : (itemPeer[key] || defaultPeer(it))) === p; ps.appendChild(o);
    }
    ps.onchange = () => { if (isCfgView(view)) { cfgSetPeer(view, it, ps.value); } else { itemPeer[key] = ps.value; renderRight(); } };
    left.appendChild(ps);
  } else {
    left.appendChild(el("div", null, T(PEER_NAME[peers[0]])));
  }
  if (peers.includes("ref")) { left.appendChild(el("div", "hint", T("shell.item.ref_note"))); }
  const subst = isCfgView(view) && cfgSubstNote(view, it);
  if (subst) { left.appendChild(subst); }
  if (it.human && !isCfgView(view)) {
    left.appendChild(el("div", "hint", T(it.human === "number" ? "shell.item.human_number" : "shell.item.human_yesno")));
  }

  // No adjustable parameter: no heading and no "none" line (decision 117).
  if ((it.params || []).length || ITEM_BODIES[it.id]) {
    left.appendChild(el("h3", null, T("shell.item.params")));
    if ((it.params || []).length) { left.appendChild(isCfgView(view) ? cfgParamEditor(view, it) : libParamEditor(it)); }
    if (ITEM_BODIES[it.id]) { left.appendChild(ITEM_BODIES[it.id].params(it, view)); }
  }

  if (it.exclusive && !solo) {
    const others = itemsOf(view, it.port).filter(o => excludes(view, o, it));
    if (others.length) {
      left.appendChild(el("div", "hint", T("shell.item.excl", { list: others.map(o => o.name).join(T("shell.list_sep")) })));
    }
  }

  // Criteria and the board's readings sit in the result table (decisions 113, 116).
  const ib = ITEM_BODIES[it.id];
  body.appendChild(left);

  const right = el("div");
  if (isCfgView(view)) {
    // What a person gives, how it runs in this sub-test (C.3.6 layer 3).
    for (const n of cfgItemNotes(view, it)) { left.appendChild(n); }
    // Criteria only, 项目 / 预期, of the picked peer (decision 125).
    right.appendChild(el("h3", null, T("full.crit")));
    right.appendChild(ib ? el("div", "hint", ib.crit(it, view)) : libCritTable(it, null, true, cfgEffPeer(view, it)));
    body.appendChild(right);
    card.appendChild(body);
    return card;
  }
  right.appendChild(el("h3", null, T("shell.item.result")));
  // Port-specific bodies (AI / AO in analog.js) word their own criterion: a per-point limit in % FS.
  if (ib) { right.appendChild(el("div", "hint", ib.crit(it, view))); }
  right.appendChild(ib ? ib.result(it, view) : libResult(it));
  body.appendChild(right);
  card.appendChild(body);
  return card;
}
