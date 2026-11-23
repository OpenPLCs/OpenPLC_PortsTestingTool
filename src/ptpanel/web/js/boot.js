"use strict";

// The meter box's buttons, bound once: the box sits outside the redrawn cards.
$("#meterok").onclick    = () => meterDone($("#meterval").value.trim());
$("#meterskip").onclick  = () => meterDone("");
$("#meterstop").onclick  = () => meterDone(null);
$("#meterval").onkeydown = e => {
  if (e.key === "Enter") { meterDone($("#meterval").value.trim()); }
};


/* ---------------- go ---------------- */
// Page memo first: it sets the column widths and the test type; then the ports and the state for the wiring rows.
// Being on this page is R&D: the line loops idle and station 1 connects by itself (C.3.10 「入口」).
memoLoad()
  .then(() => { if (!EMBED && uiMemo.type !== "rd") { uiMemo.type = "rd"; return memoSave(); } })
  .then(() => loadPorts())
  .then(() => refreshState())
  .then(() => pollConn());
