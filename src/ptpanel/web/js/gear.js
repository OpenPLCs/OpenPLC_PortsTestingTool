"use strict";

// One station's wiring registration window (PORTTOOL-FLOW.md C.3.10 「接线登记」),
// opened from the big screen's left column and from R&D's connection zone (R&D
// is station 1). Uses T, el, $ and the page's own api(); both pages have
// #lnmodal / #lnbox.

const gearAdd = (p, ...c) => { for (const x of c) { if (x) { p.appendChild(x); } } return p; };

const tail8 = uid => (uid ? "…" + uid.slice(-8) : "");
// What a tile says is on its board: the fixture version, an older one, none, an empty chip.
function fwText(t) {
  if (t.fw === "blank") { return T("line.fw.blank"); }
  if (!t.fw) { return T("line.fw.none"); }
  if (t.want && t.fw !== t.want) { return T("line.fw.old", { v: t.fw }); }
  return t.fw;
}

function popMenu(anchor, items) {
  document.querySelectorAll(".popmenu").forEach(x => x.remove());
  const m = el("div", "popmenu"), r = anchor.getBoundingClientRect();
  m.style.left = (r.left + scrollX) + "px"; m.style.top = (r.bottom + scrollY + 4) + "px";
  for (const [label, f] of items) { const d = el("div", null, label); d.onclick = e => { e.stopPropagation(); m.remove(); f(); }; m.appendChild(d); }
  document.body.appendChild(m);
  // Kept inside the window when the anchor sits near its right edge.
  const over = r.left + m.offsetWidth - (innerWidth - 8);
  if (over > 0) { m.style.left = Math.max(8, r.left - over) + scrollX + "px"; }
  setTimeout(() => document.addEventListener("click", () => m.remove(), { once: true }));
}
function modal(build, fit) {
  const m = $("#lnmodal"), b = $("#lnbox");
  b.textContent = ""; b.classList.toggle("fit", !!fit); build(b); m.classList.add("open");
}
function closeModal() { $("#lnmodal").classList.remove("open"); $("#lnbox").textContent = ""; }

// The test type chip both pages start their top bar with (C.3.6, C.3.10).
async function setType(t) {
  const ui = Object.assign({}, ((await api("/api/memo")) || {}).ui || {}, { type: t });
  await api("/api/memo", { section: "ui", cells: ui });
  location.href = t === "rd" ? "/st/1/" : "/";
}
function typeChip(cur) {
  const tc = $("#typechip"); tc.textContent = "";
  gearAdd(tc, el("span", "lab", T("line.chip.type")), el("b", null, T(cur === "rd" ? "line.type.rd" : "line.type.line")), el("span", "lab", "▾"));
  tc.onclick = () => popMenu(tc, [[T("line.type.rd"), () => cur !== "rd" && setType("rd")], [T("line.type.line"), () => cur === "rd" && setType("line")]]);
}

// The rows of one station's wiring. d: its panel's /api/conn, /api/state and
// /api/ports, its /api/stations record, its big-screen tile, ref_unused.
function gearRows(d) {
  const cn = d.conn || {}, st = d.state || {}, tile = d.tile || {}, rec = d.rec || {};
  const peers = (st.saved || {}).peers || {};
  const usbSerial = (cn.usb || {}).board_serial || "";
  const usbOn = !!usbSerial && ((cn.boardIP || {}).sim || (d.ports || []).some(p => p.serial === usbSerial));
  const ip = cn.boardIP || {};
  const ref = cn.ref || {};
  const probes = ((cn.stlink || {}).probes || []);
  return [
    { blk: "line.gear.blk.dut", name: "line.gear.control", on: !!st.connected || (!!tile.state && tile.state !== "empty"),
      val: rec.control === "sim" ? T("line.set.sim") : rec.control || "", reg: "control" },
    { blk: "line.gear.blk.dut", name: "line.gear.usb", on: usbOn, val: usbSerial, note: "line.gear.usb_note" },
    { blk: "line.gear.blk.dut", name: "line.gear.eth", on: ip.state === "ok", val: ip.addr || "", note: "line.gear.eth_note" },
    { blk: "line.gear.blk.dut", name: "line.gear.rs485", on: !!(peers.rs485 && peers.rs485.com), val: (peers.rs485 || {}).com || "", reg: "rs485" },
    { blk: "line.gear.blk.dut", name: "line.gear.can", on: !!(peers.can && peers.can.com), val: (peers.can || {}).com || "", reg: "can" },
    { blk: "line.gear.blk.ref", name: "line.gear.ref", on: !!ref.connected,
      val: ref.port || (d.refUnused ? T("conn.ref_unused") : ""), reg: "ref" },
    { blk: "line.gear.blk.pc", name: "line.gear.stlink", on: probes.some(p => !rec.stlink || p.sn === rec.stlink), val: rec.stlink || "", reg: "stlink" },
    { blk: "line.gear.blk.pc", name: "line.gear.safety", reserved: true },
    { blk: "line.gear.blk.pc", name: "line.gear.scanner", reserved: true },
  ];
}

// gearData fetches what gearRows needs for station n.
async function gearData(n, stlink) {
  const b = "/st/" + n;
  const [conn, st, ports, list, line, cfg] = await Promise.all([api(b + "/api/conn", stlink ? { stlink: true } : undefined),
    api(b + "/api/state"), api(b + "/api/ports"), api("/api/stations"), api("/api/line"), api(b + "/api/stagecfg")]);
  return { conn, state: st, ports: ports.ports || [], rec: (list.stations || []).find(s => s.n === n) || {},
    tile: (line.tiles || []).find(t => t.n === n) || {}, refUnused: !!cfg.ref_unused };
}

// gearModal opens station n's registration window; after runs once something was changed.
async function gearModal(n, after) {
  const d = await gearData(n, true);
  modal(b => {
    const h = el("div", "mhd"), x = el("button", "x", "✕");
    x.title = T("line.detail.close"); x.onclick = closeModal;
    gearAdd(h, el("h2", null, T("line.gear.title", { n })), x);
    const tb = el("table", "reg");
    let blk = "";
    for (const r of gearRows(d)) {
      const tr0 = el("tr", r.blk !== blk ? "first" : null);
      const val = r.reserved ? T("line.gear.reserved") : r.note ? T(r.note) + (r.val ? " · " + r.val : "") : (r.val || T("line.gear.none"));
      const cell = el("td");
      if (r.reg) {
        const chg = el("button", null, T("line.gear.change"));
        chg.dataset.gear = r.reg;
        chg.onclick = () => gearPick(n, r.reg, d, chg, after);
        cell.appendChild(chg);
      }
      gearAdd(tr0, el("td", "g", r.blk !== blk ? T(r.blk) : ""), el("td", null, T(r.name)), el("td", "d", val), cell);
      blk = r.blk; tb.appendChild(tr0);
    }
    gearAdd(b, h, tb, el("div", "hint", T("line.gear.serial_note")));
  }, true);
}

// gearPick lists this kind's candidates and stores the one picked.
function gearPick(n, reg, d, anchor, after) {
  const base = "/st/" + n, items = [];
  async function store(v) {
    let r = {};
    if (reg === "control") { r = await api("/api/stations/set", { n, control: v }); }
    else if (reg === "stlink") { r = await api("/api/stations/set", { n, sn: v }); }
    else if (reg === "ref") {
      // "Not used" is kept with the configurations (decision 135); connecting one clears it.
      r = await api(base + "/api/ref", { port: v === REF_NOT_USED ? "" : v });
      if (!r.error && (v === REF_NOT_USED) !== d.refUnused && (v === REF_NOT_USED || r.connected)) {
        await api(base + "/api/stagecfg", { ref_unused: v === REF_NOT_USED });
      }
    }
    else if (v) { r = await api(base + "/api/link", { port: reg, com: v }); }
    else { r = await api(base + "/api/unlink", { port: reg }); }
    if (r.error) { alert(r.error); }
    if (after) { await after(); }
    gearModal(n, after);
  }
  if (reg === "stlink") {
    for (const p of ((d.conn.stlink || {}).probes || [])) { items.push([p.sn + (p.board ? " · " + p.board : ""), () => store(p.sn)]); }
  } else {
    const want = reg === "can" ? (p => p.canable) : (p => !p.canable && !p.bt);
    if (reg === "control") {
      // Asks every candidate for pt.caps and takes the one fixture that answers (decision 146).
      items.push([T("html.ctl_auto"), async () => {
        const j = await api(base + "/api/match/control", {});
        if (j.error) {
          const tried = (j.tried || []).map(t => t.com + ": " + t.why).join("; ");
          alert(tried ? T("html.ctl_auto_tried", { error: j.error, tried }) : j.error);
          return;
        }
        store(j.found);
      }]);
    }
    for (const p of d.ports.filter(p => p.name !== "sim" && want(p))) {
      const rs232 = reg === "control" && looksLikeRs232(p.label || "");
      items.push([(p.label || p.name) + (rs232 ? T("ui.loadPorts.2") : ""), () => store(p.name)]);
    }
    if (reg === "control" || reg === "ref") { items.push([T("line.set.sim"), () => store("sim")]); }
    if (reg === "ref") { items.push([T("conn.ref_unused"), () => store(REF_NOT_USED)]); }
  }
  items.push([T("line.gear.none"), () => store("")]);
  popMenu(anchor, items);
}
const REF_NOT_USED = "-";

// A hint, never a choice: the label is all there is, and adapters can share a VID/PID.
function looksLikeRs232(label) {
  return /pl2303|prolific|ft232|ftdi|cp210|ch343|usb-serial ch34/i.test(label);
}
