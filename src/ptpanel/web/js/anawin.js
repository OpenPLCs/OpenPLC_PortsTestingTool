"use strict";

// The KNX analyser window's shell (knxwin.js): head with stop-all / end /
// close, the error line, status, the two send forms, the pause / clear /
// filter bar and the table host. Built once, outside the cards, so typing
// survives a poll. The CAN window builds its own (canwin.js, decision 172);
// the error line, opening, the repeat check and the card are shared.
//
// cfg: win (the window's state object), id (element id), p (id prefix),
// title, hint, filter, endTip (string keys), stop / clear (ops), form
// (side -> element), draw.
function anaWinBuild(cfg) {
  const win = cfg.win, act = body => anaWinAct(win, cfg.draw, body);
  const w = el("div"); w.id = cfg.id; w.hidden = true;
  const head = el("div", "cwhead");
  head.appendChild(el("b", null, T(cfg.title)));
  // Stops both sides' repeats; the session stays up (decision 151 #7).
  const stopAll = el("button", null, T("canwin.stop_all"));
  stopAll.id = cfg.p + "stopall";
  stopAll.title = T("anawin.tip.stop_all");
  stopAll.onclick = async () => { if (await act({ op: cfg.stop, side: "board" })) { await act({ op: cfg.stop, side: "pc" }); } };
  head.appendChild(stopAll);
  const end = el("button", "stop", T("lib.live.end"));
  end.id = cfg.p + "end";
  end.title = T(cfg.endTip);
  end.onclick = () => act({ op: "end" });
  const close = el("button", null, T("canwin.close"));
  close.id = cfg.p + "close";
  close.title = T("anawin.tip.close");
  close.onclick = () => { win.shown = false; w.hidden = true; };
  head.appendChild(end); head.appendChild(close);
  document.addEventListener("keydown", e => { if (e.key === "Escape" && !w.hidden) { close.click(); } });
  w.appendChild(head);

  // Why the last action failed or why nothing can be sent (decision 171):
  // the main page's message box is behind this window.
  const err = el("div", "cwerr"); err.id = cfg.p + "err";
  w.appendChild(err);

  w.appendChild(el("div", "hint", T(cfg.hint)));
  const st = el("div", "cwstatus"); st.id = cfg.p + "status";
  w.appendChild(st);

  const forms = el("div", "cwforms");
  for (const side of ["board", "pc"]) { forms.appendChild(cfg.form(side, act)); }
  w.appendChild(forms);

  const bar = el("div", "cwbar");
  const sum = el("span", "cwsum"); sum.id = cfg.p + "sum";
  bar.appendChild(sum);
  const pause = el("button", null, T("canwin.pause"));
  pause.id = cfg.p + "pause";
  pause.onclick = () => { win.paused = !win.paused; pause.textContent = T(win.paused ? "canwin.resume" : "canwin.pause"); cfg.draw(); };
  const clear = el("button", null, T("canwin.clear"));
  clear.id = cfg.p + "clear";
  clear.onclick = () => act({ op: cfg.clear });
  const filt = el("input"); filt.id = cfg.p + "filter"; filt.placeholder = T(cfg.filter);
  filt.oninput = () => { win.filter = filt.value.trim(); cfg.draw(true); };
  bar.appendChild(pause); bar.appendChild(clear); bar.appendChild(filt);
  w.appendChild(bar);

  const tbl = el("div", "cwtable"); tbl.id = cfg.p + "table";
  w.appendChild(tbl);
  document.body.appendChild(w);
  return w;
}

// One window action: its error goes on the window's error line. True when taken.
async function anaWinAct(win, draw, body) {
  const j = await api("/api/lib/live", Object.assign({ id: win.id }, body));
  win.err = j.error || "";
  draw(true);
  libRefresh();
  return !j.error;
}

// The window's error line: the last refusal, else why nothing can be sent now.
function anaWinErr(w, win, p, run) {
  const line = w.querySelector("#" + p + "err");
  if (run) { win.starting = false; }
  const why = win.err || (win.starting ? T("anawin.starting")
    : run ? "" : T(libRunning() || libPageBusy ? "anawin.busy" : "anawin.not_running"));
  line.textContent = why;
  line.hidden = !why;
}

// 打开高级窗口 (decision 171): opens the window and, when nothing else runs,
// starts just this item first, so the window is ready to send.
async function anaWinOpen(win, id, draw) {
  win.id = id; win.shown = true; win.err = "";
  const st = libState(id);
  if ((st && st.running) || libRunning() || libPageBusy) { draw(true); return; }
  win.starting = true;
  draw(true);
  const j = await libStartOnly([id]);
  win.starting = false;
  if (j && j.error) { win.err = j.error; }
  draw(true);
}

// The send form's own check before anything is sent (decision 171): a count
// of 1 sends once and ignores the interval; more needs at least min ms.
// Returns the refused field and why, null when fine.
function anaWinRepeatField(every, count, min) {
  const c = count.trim() === "" ? 1 : Number(count);
  if (!Number.isInteger(c) || c < 1 || c > 10000) { return { field: "count", msg: T("anawin.err.count", { max: 10000 }) }; }
  const e = every.trim() === "" ? 0 : Number(every);
  if (c > 1 && (!Number.isFinite(e) || e < min)) { return { field: "every", msg: T("anawin.err.every", { min }) }; }
  return null;
}

// The same check, as the refusal alone: "" when fine.
function anaWinCheckRepeat(every, count, min) {
  const r = anaWinRepeatField(every, count, min);
  return r ? r.msg : "";
}

// A send press: the page's check first, then the server.
function anaWinSend(win, draw, act, bad, body) {
  if (bad) { win.err = bad; draw(true); return; }
  act(body);
}

// The card libLive draws for an analyser item on every redraw, and libResult
// before the item has ever run (st null): the summary and the button that
// opens the window. kind is "can" or "knx"; the open button's data attribute
// is data-<kind>win-open.
function anaWinCard(win, kind, summary, draw, it, st) {
  const box = el("div", "live");
  box.dataset.live = it.id;
  const v = (st && st.live && st.live[kind]) || {}, run = !!(st && st.running);
  box.appendChild(el("div", "hint", T("canwin.card_hint")));
  if (st) { box.appendChild(el("div", null, T(summary, { ok: v.matched || 0, miss: v.missing || 0, bad: v.bad || 0 }))); }
  const open = el("button", "primary", T("canwin.open"));
  open.dataset[kind + "winOpen"] = it.id;
  open.onclick = () => anaWinOpen(win, it.id, draw);
  box.appendChild(open);
  if (run) {
    const end = el("button", null, T("lib.live.end"));
    end.dataset.liveend = it.id;
    end.onclick = () => liveAct(it.id, { op: "end" });
    box.appendChild(end);
  }
  win.id = it.id;
  win.st = st;
  // Opens by itself when the session comes up.
  if (run && !win.wasRunning) { win.shown = true; }
  win.wasRunning = run;
  draw();
  return box;
}

// The analyser items whose card has the open button even before a first run.
const ANAWIN_CARDS = { "can.bus": (it, st) => canWinCard(it, st), "knx.live": (it, st) => knxWinCard(it, st) };

// The send form's labelled inputs; attr is the data attribute each input carries.
function anaWinField(f, attr, name, label, size, value) {
  const lab = el("label", null, T(label) + " ");
  const i = el("input"); i.dataset[attr] = name; i.size = size;
  if (value) { i.value = value; }
  lab.appendChild(i); f.appendChild(lab);
  return i;
}
