/* sidebar.jsx — Sextant Live Flow shell (v0.5 reskin, TASK stage a).
   Charcoal 284px sidebar: brand glyph + Workspace nav (Home / Goals / Work engine /
   Artifacts / Bus) + an editable Conversations list + a "You · operator" footer.
   The white stage to the right renders the existing views (their internals
   unchanged). No-personas (TASK-194): no Agents roster, no named-crew list.
   Exports: Sidebar, Avatar, StatusPill, MessageList, Composer, SextantGlyph,
   AssistantFab, CmdK (to window). The Agents roster was retired (TASK-194). */
(function () {
  const { useState, useEffect, useRef, useCallback } = React;

  /* The sextant brand glyph — eyepiece + A-frame + graduated arc, recreated from
     flow2.jsx. Teal/purple accents in the sidebar; falls back to a single ink. */
  function SextantGlyph({ ink = "currentColor", teal, purple }) {
    teal = teal || ink; purple = purple || ink;
    return (
      <g>
        <g fill="none" stroke={purple} strokeWidth="6" strokeLinecap="round">
          <line x1="29" y1="155" x2="93" y2="155" />
          <line x1="3" y1="194" x2="147" y2="194" />
          <line x1="62" y1="236" x2="117" y2="236" />
        </g>
        <path fill={teal} d="M112 0 L118 23 L141 29 L118 35 L112 59 L106 35 L83 29 L106 23 Z" />
        <path fill={purple} d="M415 27 L419 42 L434 46 L419 50 L415 65 L411 50 L396 46 L411 42 Z" />
        <circle cx="471" cy="132" r="8" fill={teal} />
        <g fill="none" stroke={ink} strokeLinejoin="round">
          <path d="M264 126 A47 47 0 1 1 292.16 117.62" strokeWidth="15" strokeLinecap="butt" />
          <g strokeWidth="15" strokeLinecap="butt">
            <path d="M264 128 L161 308" />
            <path d="M264 125 L362 308" />
          </g>
          <path d="M102 314 Q264 423 426 314" strokeWidth="18" strokeLinecap="butt" />
          <path d="M107 305 L90 336 M421 305 L438 336" strokeWidth="20" strokeLinecap="butt" />
        </g>
        <g fill={ink}>
          <circle cx="161" cy="308" r="7.5" />
          <circle cx="362" cy="308" r="7.5" />
        </g>
        <circle cx="264" cy="127" r="8" fill={ink} />
        <path fill={ink} d="M256 366 H272 V399 H288 V412 H240 V399 H256 Z" />
        <path fill={ink} d="M338 193 L438 147 L438 249 L374 225 L381 209 L422 224 L422 172 L351 204 Z" />
      </g>
    );
  }

  const PALETTE = ["#6a55e0", "#e0a23a", "#d2674a", "#3a93d2", "#54ad6e", "#c060a8", "#2bb6a6"];
  function hueOf(name) {let h = 0;for (const c of name || "") h = h * 31 + c.charCodeAt(0) >>> 0;return PALETTE[h % PALETTE.length];}
  function initials(name) {
    const parts = (name || "").replace(/[-@#]/g, " ").split(/[\s_]+/).filter(Boolean);
    return ((parts[0] ? parts[0][0] : "?") + (parts[1] ? parts[1][0] : "")).toUpperCase();
  }
  // Avatar — no-personas (TASK-194): a non-operator actor never gets a persona
  // avatar (name-hashed colour + initials). An AGENT/run/workflow renders a NEUTRAL
  // function glyph (a square chip with a ⬡ mark, the same neutral ink everywhere) —
  // identity is the ULID + function in the adjacent label, not the avatar. Only a
  // human ("you", kind="human") keeps the initialled, name-coloured chip.
  function Avatar({ name, kind, size = 26 }) {
    if (kind === "agent") {
      return (
        <span className="sx-av is-agent is-run"
        style={{ width: size, height: size, fontSize: size * 0.5 }} aria-hidden="true">⬡</span>);
    }
    return (
      <span className="sx-av"
      style={{ width: size, height: size, background: hueOf(name), fontSize: size * 0.42 }}>
        {initials(name)}
      </span>);

  }
  const ST = {
    review: { t: "Needs review", c: "review" }, approved: { t: "Approved", c: "approved" },
    changes: { t: "Changes requested", c: "changes" }, draft: { t: "Draft", c: "draft" },
    rejected: { t: "Rejected", c: "changes" }, archived: { t: "Archived", c: "draft" }
  };
  function StatusPill({ status, big, dot }) {
    const s = ST[status] || ST.draft;
    if (dot) return <span className={"sx-sd sx-sd-" + s.c} title={s.t} />;
    return <span className={"sx-status sx-st-" + s.c + (big ? " is-big" : "")}><i />{s.t}</span>;
  }

  /* ---------- shared message list + composer ---------- */
  function escapeRe(s) {return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");}
  function escapeAttr(s) {return s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");}
  function escapeHTML(s) {return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");}
  // Render a chat message body: light markdown (newlines→breaks, bold, inline code,
  // lists) + linkify artifact references into clickable <a data-art> wrappers.
  // Two reference forms, both wired to the same open handler (openArtifact fetches
  // by name, so a name that isn't in the known list still resolves on click):
  //   - explicit [[name]] wikilinks (v0.5) — author opts in, any name
  //   - bare known-artifact names (TASK-102) — autolinked from the known list
  // Returns sanitized HTML, or null when the markdown libs are absent (caller falls
  // back to plain text). Sanitize on marked's output, then again after each link
  // injection, since we only ever rewrite text *between* tags (never attributes).
  function renderMessageHTML(text, names) {
    if (!text) return "";
    if (!(window.marked && window.DOMPurify)) return null;
    let html = window.DOMPurify.sanitize(window.marked.parse(text, { breaks: true, gfm: true }));
    const list = (names || []).filter(Boolean);
    const known = new Set(list);
    // [[name]] wikilinks: rewrite only in text between tags. A wikilink is
    // clickable ONLY when `name` is a KNOWN artifact — then data-art carries the
    // (attr-escaped) target. An UNKNOWN [[name]] would navigate to a broken page,
    // so it renders MUTED and inert (no data-art, no pointer) instead.
    if (html.indexOf("[[") >= 0) {
      const wiki = /\[\[([^\[\]]+?)\]\]/g;
      html = html.replace(/>([^<]+)</g, (full, seg) =>
        ">" + seg.replace(wiki, (m, raw) => {
          const n = raw.trim();
          if (!n) return m;
          if (known.has(n)) return '<a class="sx-artlink" data-art="' + escapeAttr(n) + '">' + escapeHTML(n) + "</a>";
          return '<span class="sx-artlink-dead">' + escapeHTML(n) + "</span>";
        }) + "<");
      html = window.DOMPurify.sanitize(html);
    }
    if (list.length) {
      const alt = list.slice().sort((a, b) => b.length - a.length).map(escapeRe).join("|");
      const re = new RegExp("(?<![\\w./-])(" + alt + ")(?![\\w./-])", "g");
      // only rewrite text *between* tags, never a tag's own attributes (so we don't
      // re-link the name already inside a wikilink's <a>…</a> or its data-art attr)
      html = html.replace(/>([^<]+)</g, (full, seg) =>
      ">" + seg.replace(re, (m, n) => '<a class="sx-artlink" data-art="' + n + '">' + n + "</a>") + "<");
      html = window.DOMPurify.sanitize(html);
    }
    return html;
  }
  function MessageList({ messages, onArtifactRef, artifactNames }) {
    function onArtClick(e) {
      const a = e.target.closest && e.target.closest("a.sx-artlink");
      if (a && onArtifactRef) {e.preventDefault();onArtifactRef(a.getAttribute("data-art"));}
    }
    return (
      <div className="sx-activity">
        {messages.map((m) => {
          if (m.kind === "event") return (
            <div className="sx-event" key={m.id}>
              <span className="sx-event-line" /><span className="sx-event-txt">{m.text}</span>
              <span className="sx-event-time">{m.time}</span>
            </div>);
          const html = renderMessageHTML(m.text, artifactNames);
          return (
            <div className={"sx-msg" + (m.self ? " is-self" : "")} key={m.id}>
              <Avatar name={m.author} kind={m.role === "agent" ? "agent" : "human"} />
              <div className="sx-msg-body">
                <div className="sx-msg-head">
                  <span className="sx-msg-name">{m.self ? "you" : m.author}</span>
                  {m.role === "agent" && !m.self && <span className="sx-tag-agent">agent</span>}
                  <span className="sx-msg-time">{m.time}</span>
                </div>
                {html != null ?
                <div className="sx-msg-text" onClick={onArtClick} dangerouslySetInnerHTML={{ __html: html }} /> :
                <div className="sx-msg-text">{m.text}</div>}
                {m.artifactRef &&
                <button className="sx-artref" onClick={() => onArtifactRef && onArtifactRef(m.artifactRef)}>
                    <span className="sx-artref-ic">▣</span>{m.artifactRef}
                  </button>
                }
              </div>
            </div>);
        })}
      </div>);

  }
  function Composer({ draft, setDraft, onSend, placeholder }) {
    const taRef = useRef(null);
    // grow vertically with content (wrap, don't push right); cap so it never eats
    // the whole pane — past the cap it scrolls.
    useEffect(() => {
      const ta = taRef.current;if (!ta) return;
      ta.style.height = "auto";
      ta.style.height = Math.min(ta.scrollHeight, 160) + "px";
    }, [draft]);
    return (
      <div className="sx-composer">
        <textarea ref={taRef} className="sx-input" rows={1} placeholder={placeholder || "Message…"} value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {if (e.key === "Enter" && !e.shiftKey) {e.preventDefault();if (draft.trim()) onSend();}}} />
        <button className="sx-send" disabled={!draft.trim()} onClick={onSend}>↵</button>
      </div>);

  }

  /* ---------- views (rendered in the white stage). Stage (b): flow2 ArtifactsList
     internals, wired to the real /api/artifacts records. ArtifactsView lives in
     its own file (artifacts.jsx, TASK-112), the Goals view in goals.jsx (Track 2).
     The Agents roster was RETIRED in the no-personas sweep (TASK-194): work is
     surfaced by ULID + function via runs/goals/conversations, not a named crew
     list. Steering is the goal/run topic threads + the single Assistant. ---------- */

  /* ---------- floating Assistant FAB · the de-named operator helper ----------
     "Assistant · always here": a UNIVERSAL helper, never a persona (TASK-194).
     It is ALWAYS labelled "Assistant" — never a person's name. Controlled when
     given open/onOpen/onClose (so ⌘K can open it with a prefilled prompt);
     otherwise self-manages its own open state.

     `assistant` is the optional live bus-backed helper ({ id, accent }) read from
     the `assistant` artifact, or null when it doesn't exist yet. It drives two
     modes — but BOTH wear the generic "Assistant" label:
       - PRESENT → the FAB panel IS the live DM thread: header,
         window.MessageList (the backfilled DM history), window.Composer wired to
         onSend (publishes to the helper's DM subject). An optional accent colour
         tints the spark inline (never touching the global --brand), but the name
         is never shown.
       - ABSENT  → the local helper that answers from the dash's own loaded data.
     `prompt` is a query carried over from a ⌘K no-match (prefilled into the
     composer by app.jsx). `online` is the helper's live bus presence → the dot. */
  function AssistantFab({ open: openProp, prompt, assistant, online, messages, self, draft, setDraft, onSend, onArtifactRef, artifactNames, onOpen, onClose } = {}) {
    const [openLocal, setOpenLocal] = useState(false);
    const controlled = openProp !== undefined;
    const open = controlled ? openProp : openLocal;
    const doOpen = () => (controlled ? onOpen && onOpen() : setOpenLocal(true));
    const doClose = () => (controlled ? onClose && onClose() : setOpenLocal(false));
    const live = !!(assistant && assistant.id);
    const accent = (assistant && assistant.accent) || "";
    // keep the freshest message in view as the thread grows / on open.
    const bodyRef = useRef(null);
    useEffect(() => {
      const el = bodyRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    }, [open, live, messages && messages.length]);

    if (!open) return (
      <button className="fx-asst-btn" title="Assistant · always here" onClick={doOpen}>
        <span className="fx-asst-spark" style={accent ? { color: accent } : undefined}>✦</span>
      </button>);

    // LIVE → the bus-backed DM thread (window.MessageList + window.Composer, the
    // same primitives ConversationView uses). The Composer reads its own `draft`,
    // so its zero-arg onSend forwards the current draft to onSend(text). Labelled
    // "Assistant" — the bus helper is a function, not a persona.
    if (live) {
      const msgs = messages || [];
      return (
        <div className="fx-asst-panel is-live sx-conv-light" role="dialog" aria-label="Assistant">
          <div className="fx-asst-head">
            <span className="fx-asst-mark" style={accent ? { background: accent } : undefined}>✦</span>
            <div>
              <div className="fx-asst-title" style={accent ? { color: accent } : undefined}>Assistant</div>
              <div className="fx-asst-live">
                <span className={"fx-asst-dot" + (online ? " is-on" : "")} style={online && accent ? { background: accent } : undefined} />
                {online ? "online" : "always here"}
              </div>
            </div>
            <button className="fx-asst-close" onClick={doClose} aria-label="Close">×</button>
          </div>
          <div className="fx-asst-thread" ref={bodyRef}>
            {msgs.length === 0 && (
              <p className="fx-asst-empty">Ask the assistant anything about your workspace — it can see your goals, artifacts, and the bus.</p>
            )}
            <window.MessageList messages={msgs} onArtifactRef={onArtifactRef} artifactNames={artifactNames} />
          </div>
          <window.Composer draft={draft || ""} setDraft={setDraft} onSend={() => onSend && onSend(draft)} placeholder="Message the assistant…" />
        </div>);
    }

    // ABSENT (no live bus assistant) → the DE-NAMED local helper (TASK-203).
    // "Assistant · always here": a chat panel that answers from the dash's own
    // loaded data (goals/artifacts/runs) — never a person, never an agent on the
    // bus. Each answer may embed [[wikilinks]] that navigate on click (resolved via
    // onArtifactRef + artifactNames). The composer is LIVE (zero-arg onSend forwards
    // the current draft) — it computes a local answer, it doesn't publish.
    const localMsgs = messages || [];
    return (
      <div className="fx-asst-panel is-live sx-conv-light" role="dialog" aria-label="Assistant">
        <div className="fx-asst-head">
          <span className="fx-asst-mark">✦</span>
          <div>
            <div className="fx-asst-title">Assistant</div>
            <div className="fx-asst-sub">always here</div>
          </div>
          <button className="fx-asst-close" onClick={doClose} aria-label="Close">×</button>
        </div>
        <div className="fx-asst-thread" ref={bodyRef}>
          {localMsgs.length === 0 && (
            <p className="fx-asst-empty">Ask me about your goals, what's waiting on you, or where a workstream stands — I read your workspace. Try “what's waiting on me?”</p>
          )}
          <window.MessageList messages={localMsgs} onArtifactRef={onArtifactRef} artifactNames={artifactNames} />
        </div>
        <window.Composer draft={draft || ""} setDraft={setDraft} onSend={() => onSend && onSend(draft)} placeholder="Ask a quick question…" />
      </div>);
  }

  /* ---------- ⌘K command palette · scoring + recency ----------
     scoreEntry returns a numeric score (higher = better) for a single index
     entry. It is a WEIGHTED SUM of named signals — each signal is a pure
     function (entry, ctx) → 0..1. Adding a new signal (frecency, type
     weight, pinned) is one array entry + one weight constant; nothing else
     needs to change.

     ctx = { ql: string, recents: { [key]: timestamp(ms) }, now: number }
       ql       — lower-cased trimmed query (empty string when palette is blank)
       recents  — the recency store from localStorage (keyed by entry.key)
       now      — Date.now() snapshot for the render
  */
  const RECENCY_HALF_LIFE_MS = 7 * 24 * 60 * 60 * 1000; // 7 days → score decays to 0.5

  // matchSignal: 1.0 for exact label match, 0.8 for prefix, 0.5 for substring,
  // 0 for no match.  When query is empty every entry scores 0 so the signal
  // contributes nothing and recency alone orders results.
  function matchSignal(entry, { ql }) {
    if (!ql) return 0;
    const label = entry.label.toLowerCase();
    if (label === ql) return 1.0;
    if (label.startsWith(ql)) return 0.8;
    if (entry.kw.indexOf(ql) >= 0) return 0.5;
    return 0; // no match — entry will be filtered out by the caller
  }

  // recencySignal: exponential decay from the last open time.  An entry never
  // opened scores 0.  One opened just now scores ~1.  One opened RECENCY_HALF_LIFE_MS
  // ago scores 0.5.  The decay is fast enough to be useful without feeling stale.
  function recencySignal(entry, { recents, now }) {
    // Coerce + validate: a malformed localStorage value must not poison the
    // score with NaN (which would corrupt the sort).
    const ts = Number(recents[entry.key]);
    if (!(Number.isFinite(ts) && ts > 0)) return 0;
    const age = Math.max(0, now - ts);
    return Math.pow(0.5, age / RECENCY_HALF_LIFE_MS);
  }

  // SIGNALS: array of { fn, weight } — add new signals here.
  // Weights are relative; they don't have to sum to 1 (scores are only used
  // for ordering, not displayed to the user).
  const SIGNALS = [
    { fn: matchSignal,   weight: 10 }, // match quality dominates when typing
    { fn: recencySignal, weight: 3  }, // recency breaks ties + orders the empty state
  ];

  function scoreEntry(entry, ctx) {
    let score = 0;
    for (const { fn, weight } of SIGNALS) score += fn(entry, ctx) * weight;
    return score;
  }

  /* ---------- ⌘K command palette · real, client-side find & jump ---------- */
  // Searches the already-loaded artifacts + clients (agents) + conversation
  // subjects; selecting a result opens it via the existing open handlers.
  // `recents` is the recency store ({ [entry.key]: timestamp }) passed down
  // from App so the palette can rank recently-opened destinations to the top.
  function CmdK({ index, recents, assistantLive, onClose, onAsk }) {
    const [q, setQ] = useState("");
    const [sel, setSel] = useState(0);
    const ql = q.trim().toLowerCase();
    const now = Date.now();
    const ctx = { ql, recents: recents || {}, now };

    // Score every entry.  When there's a query, discard entries with zero
    // match quality (matchSignal returns 0 → combined score may still be >0
    // via recency; we want a strict "no match → hidden" rule when typing).
    // When empty, include everything (match score is 0 for all, recency orders).
    const scored = index
      .filter((it) => !ql || it.kw.indexOf(ql) >= 0)
      .map((it) => ({ it, score: scoreEntry(it, ctx) }))
      .sort((a, b) => b.score - a.score);

    const matches = scored.slice(0, 9).map((s) => s.it);
    // No good match for a typed query → offer "Ask the assistant" as a real,
    // selectable result that hands the query to the Assistant FAB. No-personas
    // (TASK-194): the assistant is always the generic "the assistant", never a
    // person's name. When a live bus helper is present (assistantLive) it opens a
    // DM; otherwise the question is answered from the workspace locally.
    const askRow = (onAsk && ql && matches.length === 0)
      ? { key: "ask", type: "Ask", label: "Ask the assistant: “" + q.trim() + "”", sub: assistantLive ? "opens a DM" : "answers from your workspace", go: () => onAsk(q.trim()) }
      : null;
    const results = askRow ? [askRow] : matches;
    useEffect(() => { setSel(0); }, [q]);
    const pick = (it) => { if (it) { it.go(); onClose(); } };
    const onKey = (e) => {
      if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => Math.min(s + 1, results.length - 1)); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => Math.max(s - 1, 0)); }
      else if (e.key === "Enter") { e.preventDefault(); pick(results[sel]); }
      else if (e.key === "Escape") { e.preventDefault(); onClose(); }
    };
    const TYPEC = { "Go to": "#5b6ef0", Surface: "#5b6ef0", Action: "#7c6df0", Goal: "#3a82c4", Workflow: "#b9842a", Run: "#b9842a", Artifact: "#c0573b", Agent: "#3f8f59", Channel: "#8a8e97", Ask: "#5b6ef0" };
    return (
      <div className="fx-cmdk-scrim" onClick={onClose}>
        <div className="fx-cmdk" onClick={(e) => e.stopPropagation()}>
          <input className="fx-cmdk-input" autoFocus placeholder="Search artifacts, runs, conversations…"
            value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={onKey} />
          <div className="fx-cmdk-results">
            {results.length === 0 && <div className="fx-cmdk-empty">{ql ? "No matches for “" + q + "”" : "Type to search."}</div>}
            {results.map((it, i) => (
              <button className={"fx-cmdk-row" + (i === sel ? " is-sel" : "")} key={it.key}
                onMouseEnter={() => setSel(i)} onClick={() => pick(it)}>
                <span className="fx-cmdk-type" style={{ color: TYPEC[it.type], borderColor: TYPEC[it.type] + "44", background: TYPEC[it.type] + "10" }}>{it.type === "Ask" ? "✦ Ask" : it.type}</span>
                <span className="fx-cmdk-main"><span className="fx-cmdk-label">{it.label}</span>{it.sub && <span className="fx-cmdk-sub">{it.sub}</span>}</span>
                <span className="fx-cmdk-enter">↵</span>
              </button>
            ))}
          </div>
          <div className="fx-cmdk-foot"><span>↑↓ navigate</span><span>↵ open</span><span>esc close</span></div>
        </div>
      </div>);
  }

  /* ---------- sidebar shell: Workspace nav + editable conversations ----------
     The five design nav rows in their exact order (TASK-220 S1.2): Home, Goals,
     Work engine, Artifacts, Bus. The Agents + Workflow surfaces still exist as
     code (reachable via ⌘K / deep-links) but are not primary nav rows. */
  const WORKSPACE = [["⌂", "Home", "home"], ["◎", "Goals", "goals"], ["⬡", "Work engine", "workengine"], ["❡", "Artifacts", "artifacts"], ["⇆", "Bus", "bus"]];

  function ConvNav({ ctx }) {
    const [showHidden, setShowHidden] = useState(false);
    const hid = ctx.hidden || new Set();
    const visible = ctx.conversations.filter((c) => showHidden || !hid.has(c.key));
    const hiddenCount = ctx.conversations.reduce((n, c) => n + (hid.has(c.key) ? 1 : 0), 0);
    const active = ctx.activeConvo;
    return (
      <>
        {visible.map((c) => {
          const isHidden = hid.has(c.key);
          const on = c.key === active && ctx.stageMode === "conversation";
          return (
            <div className="fx-conv" key={c.key} style={{ opacity: isHidden ? 0.5 : 1 }}>
              <button className={"fx-navrow" + (on ? " is-on" : " dim")} onClick={() => ctx.onExpandConvo(c.key)}>
                <span className="fx-navhash">{c.type === "topic" ? "#" : "@"}</span>
                <span>{c.name}</span>
              </button>
              <span className="fx-conv-actions">
                <button className="fx-conv-act" title={isHidden ? "Unhide" : "Remove from list"}
                  onClick={(e) => { e.stopPropagation(); if (isHidden) { ctx.onUnhide && ctx.onUnhide(c.key); } else { ctx.onHide && ctx.onHide(c.key); } }}>
                  {isHidden ? "↩" : "×"}
                </button>
              </span>
            </div>);
        })}
        {hiddenCount > 0 &&
        <button className="fx-navadd" onClick={() => setShowHidden((v) => !v)}>
          <span className="fx-navhash">{showHidden ? "—" : "+"}</span>
          <span>{showHidden ? "hide hidden" : hiddenCount + " hidden — show"}</span>
        </button>}
      </>);
  }

  // Sidebar — the shell left nav. Accepts resize + collapse props (TASK-141)
  // matching the right-rail pattern in review.jsx:
  //   sideWidth / sideCollapsed  persisted state from app.jsx
  //   onSideWidth(w)             parent clamps + persists the new width
  //   onToggleSide()             parent flips the collapsed flag
  // The drag handle sits on the sidebar's RIGHT edge; dragging right widens
  // (startW + delta, opposite sign from the rail on the right). The
  // endDragRef + unmount-cleanup mirrors the codex fix in review.jsx so a
  // drag that's still in progress when the component unmounts can't leak
  // document-level listeners.
  function Sidebar({ ctx, busName, navMode, sideWidth, sideCollapsed, onSideWidth, onToggleSide }) {
    // Map the open stage to the primary nav row it lives under, so the active row
    // is marked (S1.2). An artifact opened from Goals still marks Artifacts (it's
    // the artifact stage); a goal/conversation/agents/workflow stage maps to no
    // primary row when it isn't one of the five.
    const section = ctx.stageMode === "conversation" ? "convo"
      : ctx.stageMode === "artifact" ? "artifacts"
      : (ctx.stageMode === "compose" || ctx.stageMode === "criteria" || ctx.stageMode === "brief" || ctx.stageMode === "consequence") ? "artifacts"
      : ctx.stageMode; // home | goals | workengine | artifacts | bus | agents | workflow
    const meName = (ctx.self && ctx.self.display_name) || "you";

    // right-edge drag to resize — same shape as review.jsx's onHandleDown.
    const draggingRef = useRef(false);
    // holds the active drag's teardown so unmount mid-drag can't leak listeners.
    const endDragRef = useRef(null);
    const onHandleDown = useCallback((e) => {
      e.preventDefault();
      draggingRef.current = true;
      const startX = e.clientX;
      const startW = sideWidth;
      const move = (ev) => {
        if (!draggingRef.current) return;
        // sidebar is on the LEFT, so dragging the handle RIGHT (larger clientX) widens it.
        const next = startW + (ev.clientX - startX);
        onSideWidth && onSideWidth(next);
      };
      const up = () => {
        draggingRef.current = false;
        document.removeEventListener("mousemove", move);
        document.removeEventListener("mouseup", up);
        document.body.style.userSelect = "";
        endDragRef.current = null;
      };
      document.body.style.userSelect = "none";
      document.addEventListener("mousemove", move);
      document.addEventListener("mouseup", up);
      endDragRef.current = up;
    }, [sideWidth, onSideWidth]);
    // tear down a drag still in progress if the sidebar unmounts (codex pattern).
    useEffect(() => () => { if (endDragRef.current) endDragRef.current(); }, []);

    // Collapsed: render a thin reveal strip instead of hiding completely, so
    // the operator can always get the nav back without a mystery gesture.
    if (sideCollapsed) {
      return (
        <aside className="fx-side fx-side--collapsed" aria-label="Navigation (collapsed)">
          <button className="fx-side-reveal" title="Show navigation" onClick={onToggleSide}>
            <span className="fx-side-reveal-ic">›</span>
          </button>
        </aside>);
    }

    return (
      <aside className="fx-side" style={{ width: sideWidth, flexBasis: sideWidth }}>
        {/* right-edge drag handle — col-resize affordance, mirrors .fx-rail-resize */}
        <div className="fx-side-resize" onMouseDown={onHandleDown} title="Drag to resize" />
        <div className="fx-brand">
          <span className="fx-mark">
            <svg className="fx-logomark" viewBox="0 0 479 412" width="43" height="37" aria-hidden="true">
              <SextantGlyph ink="#f3f2ee" teal="#4AA2AA" purple="#8280E5" />
            </svg>
            <span className="fx-word">Sextant</span>
          </span>
          <div className="fx-brand-end">
            <button className="fx-side-search" title="Search the bus  ⌘K" onClick={ctx.onSearch}>
              <span className="fx-search-ic">⌕</span>
              <span className="fx-kbd">⌘K</span>
            </button>
            <button className="fx-side-collapse" title="Collapse navigation" onClick={onToggleSide} aria-label="Collapse navigation">‹</button>
          </div>
        </div>

        <nav className="fx-nav">
          <div className="fx-navsec">Workspace</div>
          {WORKSPACE.map(([ic, label, key]) => (
            <button className={"fx-navrow" + (section === key ? " is-on" : "")} key={key} onClick={() => ctx.onNav(key)}>
              <span className="fx-navic">{ic}</span><span>{label}</span>
              {key === "artifacts" && ctx.reviewCount > 0 && <span className="fx-navbadge">{ctx.reviewCount}</span>}
              {key === "goals" && ctx.goalReviewCount > 0 && <span className="fx-navbadge" title="goals awaiting your sign-off">{ctx.goalReviewCount}</span>}
            </button>
          ))}
          <div className="fx-navsec">Conversations</div>
          <ConvNav ctx={ctx} />
        </nav>

        <div className="fx-me">
          <Avatar name={meName} kind="human" size={26} />
          <span className="fx-me-name">You</span>
          <span className="fx-me-key">operator</span>
        </div>
      </aside>);

  }

  Object.assign(window, { Sidebar, Avatar, StatusPill, MessageList, Composer, SextantGlyph, AssistantFab, CmdK });
})();