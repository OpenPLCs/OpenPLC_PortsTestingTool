"use strict";

// What the big screen needs from the panel's core.js, without its log and
// connection state (PORTTOOL-FLOW.md C.3.10). words.js loads after this.

function T(key, args) {
  const e = STRINGS[key];
  let s = e ? (e[LANG] || e.zh) : key;
  if (args) { s = s.replace(/\{([a-z0-9_]+)\}/g, (m, k) => (k in args ? String(args[k]) : m)); }
  return s;
}
function tr(o) {
  return new Proxy(o, { get(t, k) {
    const v = t[k];
    if (typeof v === "string") { return T(v); }
    return (v && typeof v === "object") ? tr(v) : v;
  }});
}
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
async function api(path, body) {
  const opt = body ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {};
  try { const r = await fetch(path, opt); return localize(await r.json()); } catch (e) { return {}; }
}
const $ = s => document.querySelector(s);
const el = (tag, cls, txt) => { const n = document.createElement(tag); if (cls) { n.className = cls; } if (txt != null) { n.textContent = txt; } return n; };
const add = (p, ...c) => { for (const x of c) { if (x) { p.appendChild(x); } } return p; };
// words.js names channels from the board's ports; the big screen has none.
const state = { ports: [] };
