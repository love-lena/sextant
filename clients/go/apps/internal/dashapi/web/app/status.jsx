/* status.jsx — the ONE canonical status vocabulary (TASK-204, AC §0 / S22.3).

   Five statuses, each with exactly ONE colour + ONE glyph + ONE label. Every
   surface — goal criteria, runs, drafts, timelines, chips — reads its colour and
   glyph from HERE, so the dash can never disagree with itself about what "blocked"
   looks like. The colours are also mirrored as CSS custom properties in styles.css
   (--met / --prog / --wait / --blk / --todo) for pure-CSS render sites; this module
   is the source of truth for JS render sites and the canonical hexes live in both.

   Canonical keys (the conv/goals criterion vocabulary, ADR-0035):
     met            ✓  green       #3f8f59
     in-progress    ◐  blue        #3a82c4
     waiting-on-you ●  terracotta  #c0573b
     blocked        ⊘  amber       #b9842a
     not-started    ○  grey        #9a9ea7

   ALIASES map the other status vocabularies the dash already speaks (agent.status
   states, the review convention's states) onto the five, so a single helper covers
   every surface without each caller re-deriving the mapping.

   Exports (window): SxStatus { meta, KEYS, CANON }, StatusGlyph, StatusChip, StatusDot. */
(function () {
  const CANON = {
    "met":            { color: "#3f8f59", glyph: "✓", label: "Met",            cssVar: "--met"  },
    "in-progress":    { color: "#3a82c4", glyph: "◐", label: "In progress",    cssVar: "--prog" },
    "waiting-on-you": { color: "#c0573b", glyph: "●", label: "Waiting on you",  cssVar: "--wait" },
    "blocked":        { color: "#b9842a", glyph: "⊘", label: "Blocked",        cssVar: "--blk"  },
    "not-started":    { color: "#9a9ea7", glyph: "○", label: "Not started",    cssVar: "--todo" },
  };

  // Every other status word the dash speaks, folded onto a canonical key.
  // - agent.status states (TASK-84): working/done/idle/offline/waiting-*/blocked
  // - the review convention states (TASK-66): review/approved/changes/draft/…
  const ALIASES = {
    // agent.status
    "working": "in-progress",
    "done": "met",
    "idle": "not-started",
    "offline": "not-started",
    "waiting-for-human": "waiting-on-you",
    "waiting": "waiting-on-you",
    "waiting-for-agent": "in-progress",
    // review convention
    "review": "waiting-on-you",
    "approved": "met",
    "changes": "in-progress",
    "rejected": "blocked",
    "draft": "not-started",
    "archived": "not-started",
    // run / workflow words
    "running": "in-progress",
    "ok": "met",
    "complete": "met",
    "completed": "met",
    "error": "blocked",
    "failed": "blocked",
    "todo": "not-started",
    "pending": "not-started",
    "to do": "not-started",
  };

  // meta(key) → the canonical descriptor for any status word, canonical or alias.
  // Unknown words fall back to not-started (the calmest, least-claiming status) so
  // a surface never renders a blank/undefined status glyph.
  function meta(key) {
    const k = String(key || "").trim().toLowerCase();
    if (CANON[k]) return CANON[k];
    const a = ALIASES[k];
    if (a && CANON[a]) return CANON[a];
    return CANON["not-started"];
  }

  // StatusGlyph — the bare glyph in its canonical colour. `size` sets font-size.
  function StatusGlyph({ status, size, title }) {
    const m = meta(status);
    return React.createElement("span", {
      className: "sx-stg",
      style: { color: m.color, fontSize: size || undefined },
      title: title || m.label,
      "aria-label": m.label,
      role: "img",
    }, m.glyph);
  }

  // StatusDot — a small filled dot in the canonical colour (for dense rows/timelines).
  function StatusDot({ status, size, title }) {
    const m = meta(status);
    const d = size || 8;
    return React.createElement("span", {
      className: "sx-std",
      style: { width: d, height: d, background: m.color },
      title: title || m.label,
      "aria-label": m.label,
      role: "img",
    });
  }

  // StatusChip — glyph + label pill, tinted to the canonical colour. The default
  // label is the canonical one; pass `label` to override the text while keeping
  // the colour+glyph (e.g. "2 criteria waiting on you").
  function StatusChip({ status, label, big }) {
    const m = meta(status);
    return React.createElement("span", {
      className: "sx-stchip" + (big ? " is-big" : ""),
      style: { color: m.color, background: m.color + "1c", borderColor: m.color + "33" },
      title: m.label,
    },
      React.createElement("span", { className: "sx-stchip-g", "aria-hidden": "true" }, m.glyph),
      React.createElement("span", { className: "sx-stchip-l" }, label || m.label),
    );
  }

  window.SxStatus = { meta, CANON, KEYS: Object.keys(CANON) };
  Object.assign(window, { StatusGlyph, StatusDot, StatusChip });
})();
