"use strict";

/* ================= the big screen's template window =================
 *
 * The configuration of the five steps as the template window shows it
 * (PORTTOOL-FLOW.md C.3.10 「模板」): layer 2 is a block per step, layer 3
 * the chosen step's settings; the page is index.html?embed=template.
 */

// How many items a step's configuration runs on this board.
function subPicked(k) {
  return catalog.items.filter(it => it.stages.includes(k) && cfgPortOn(k, it.port) && cfgOn(k, it)).length;
}

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
    // Step ⑤ runs the demo on a real board (PORTTOOL-FLOW.md C.3.6 「功能 / 老化 / 用户级有什么不同」).
    cards.appendChild(el("div", "hint warn", T("cfg.user_demo"))).dataset.userdemo = "1";
    cards.appendChild(tplCard("tpl.user.title", [["tpl.user.boot", "—", "tpl.why.delivery"], ["tpl.user.demo", "FixtureDemo", "tpl.why.delivery"]]));
  }
  const port = catalog.ports.find(p => p.port === lport);
  if (!port) { return; }
  const sub = TPL_SUBS.find(s => s.k === view);
  const hd = el("div", "sechd", T("full.sechd", { sub: T(sub.name), port: port.label }));
  hd.dataset.sechd = view + "|" + port.port;
  cards.appendChild(hd);
  if (!cfgPortOn(view, port.port)) {
    cards.appendChild(el("div", "hint", T("full.port_off", { sub: T(sub.name) }))).dataset.portoff = port.port;
    return;
  }
  cards.appendChild(el("div", "hint", T("shell.cfg_note")));
  renderPortCards(cards, port);
}
