"use strict";

/* ---------------- api ---------------- */
async function api(path, body) {
  const opt = body ? {method:"POST", headers:{"Content-Type":"application/json"},
                      body:JSON.stringify(body)} : {};
  // A request cut off (the page reloading, the panel gone) is an error reply, not a throw.
  let r;
  try { r = await fetch(apiURL(path), opt); } catch (e) { return {error:T("ui.api.unreachable")}; }
  const j = await r.json().catch(() => ({error:T("ui.api.1")}));
  return localize(j);
}

// Go's sentences arrive as {"$t": key, "args": {...}} and are worded here, in
// the page's language (decision 82).
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

let allPorts = [];

// The serial ports this PC has, for the connection rows (connzone.js). The
// control port itself comes from station 1's wiring registration (C.3.6).
async function loadPorts() {
  const j = await api("/api/ports");
  allPorts = j.ports || [];
}

// Closed when the page is left: a browser keeps six connections per host,
// and log streams of pages already left would stall every request (C.3.6 「连接区」).
window.addEventListener("pagehide", () => { if (evtSource) { evtSource.close(); evtSource = null; } });

function startEvents() {
  if (evtSource) evtSource.close();
  evtSource = new EventSource(apiURL("/api/events"));
  evtSource.onmessage = e => {
    const d = JSON.parse(e.data);
    addLog(d.kind, d.msg ? () => localize(d.msg) : d.line, d.at);
    // Faults, resets and disconnects go on top, not only into the log, and refresh the state (decision 77).
    if (d.kind === "panel" && d.alert) {
      refreshState().then(() => showMessage(localize(d.msg), true));
      return;
    }
    if (d.kind === "frame" && d.port) {
      reads[d.port] = d;
      if (d.port === "ain" && typeof anaNoiseFrame === "function") { anaNoiseFrame(d); }
    }
  };
  evtSource.onerror = () => { /* EventSource retries by itself */ };
}

/* ---------------- state ---------------- */
function applyState(j) {
  const st = j.state || j;
  if (!st.connected && typeof rdForgetUid === "function") { rdForgetUid(); }
  const was = state && state.connected;
  state = st;
  // Station 1 connects by itself (line.go rdAttach): the log stream follows it.
  // The template window shows no log (embed=template).
  if (st.connected && !was && !EMBED) { startEvents(); }
  if (!st.connected && evtSource) { evtSource.close(); evtSource = null; }

  $("#toolver").textContent = st.tool ? T("html.toolver", {version: st.tool}) : "";
  // Readings belong to one board: a switch straight to another control port
  // must not leave the last board's frames on screen (C.3.6 「网口」).
  if (st.port !== readsPort) { reads = {}; readsPort = st.port; }
  // One gate for the rest of the page: all of it comes over the control port.
  $("#afterconn").classList.toggle("live", !!st.connected);

  const s = $("#status");
  if (st.connected && st.linkError) {
    s.classList.add("bad"); s.textContent = T("ui.applyState.3");
  } else if (st.connected) {
    s.classList.remove("bad");
    s.innerHTML = T("ui.applyState.connected", {port: st.port, firmware: st.firmware || "?"});
    // The first two numbers name one set of panel and firmware (decision 120).
    const mm = v => String(v || "").split(".").slice(0, 2).join(".");
    if (st.tool && st.firmware && mm(st.tool) !== mm(st.firmware)) {
      s.appendChild(el("b", "verbad", " " + T("html.ver_mismatch", {tool: st.tool, firmware: st.firmware})));
    }
  } else {
    s.classList.add("bad"); s.textContent = T("ui.applyState.6"); reads = {};
  }
  renderCriteria(st);

  showMessage(st.linkError || st.capsError || (st.echoNote ? T("ui.applyState.7", {echo_note: st.echoNote}) : ""), true);
  shellOnState(st);
}

// Above the right-hand area, so a fault is seen whatever the menus show.
let readsPort = "";  // the control port the frames in reads came from

function showMessage(text, bad) {
  const host = $("#msgbox");
  const old = host.querySelector(".msg"); if (old) old.remove();
  if (!text) return;
  const m = el("div", "msg" + (bad ? " bad" : "")); m.textContent = text;
  host.prepend(m);
}

async function refreshState() { applyState(await api("/api/state")); }

/* ---------------- criteria plan (header) ---------------- */
// The plan list is fetched once and cached; it only changes when somebody adds
// a file next to the exe.
let critPlans = null;

async function renderCriteria(st) {
  const box = $("#critbox");
  box.hidden = !st.connected;
  if (!st.connected) { return; }

  const sel = $("#critplan");
  if (critPlans === null) {
    critPlans = [];
    try {
      const r = await api("/api/plans");
      critPlans = (r.plans || []).map(p => p.name || p);
    } catch (e) { critPlans = []; }
  }
  const want = st.criteriaPlan || "";
  // The plan in use stays selectable even when the list came back empty.
  const names = critPlans.slice();
  if (want && names.indexOf(want) < 0) { names.unshift(want); }

  const same = sel.options.length === names.length &&
               names.every((n, i) => sel.options[i].value === n);
  if (!same) {
    sel.textContent = "";
    for (const n of names) {
      const o = el("option", null, n);
      o.value = n;
      sel.appendChild(o);
    }
  }
  sel.value = want;

  const ver = $("#critver");
  if (st.criteriaProblem) {
    ver.textContent = T("ui.renderCriteria.1", {criteria_problem: st.criteriaProblem});
    ver.style.color = "var(--err)";
  } else {
    ver.textContent = st.criteriaLimits ? T("ui.renderCriteria.2", {criteria_limits: st.criteriaLimits}) : "";
    // A limit set that is not the default is worth noticing, since a board can
    // pass under it and fail under the strict one.
    ver.style.color = /relax|宽/i.test(st.criteriaPlan || "") ? "var(--warn)" : "";
  }
}

$("#critplan").onchange = async () => {
  const j = await api("/api/criteria", {plan: $("#critplan").value});
  if (j.error) { showMessage(j.error, true); refreshState(); return; }
  // The server has cleared every result judged by the old limits (decision 30).
  addLog("panel", () => T("ui.top.crit_changed", {plan: j.plan, limits: j.limitVersion || "?"}));
  const st = await api("/api/state");
  applyState(st);
  libRefresh();
};
