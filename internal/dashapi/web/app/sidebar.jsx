/* sidebar.jsx — Sextant Live Flow shell (v0.5 reskin, TASK stage a).
   Charcoal 284px sidebar: brand glyph + Workspace nav (Home / Artifacts / Goals /
   Agents) + an editable Conversations list + a "You · operator" footer. The white
   stage to the right renders the existing views (their internals unchanged).
   Exports: Sidebar, Avatar, StatusPill, MessageList, Composer, SextantGlyph,
   AssistantFab, CmdK, AgentsView (to window). */
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
  function hueOf(name) {let h = 0;for (const c of name) h = h * 31 + c.charCodeAt(0) >>> 0;return PALETTE[h % PALETTE.length];}
  function initials(name) {
    const parts = name.replace(/[-@#]/g, " ").split(/[\s_]+/).filter(Boolean);
    return ((parts[0] ? parts[0][0] : "?") + (parts[1] ? parts[1][0] : "")).toUpperCase();
  }
  function Avatar({ name, kind, size = 26 }) {
    return (
      <span className={"sx-av" + (kind === "agent" ? " is-agent" : "")}
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
     + AgentsList internals, wired to the real /api/artifacts records + /api/clients
     presence. ArtifactsView now lives in its own file (artifacts.jsx, TASK-112),
     the Goals view in goals.jsx (Track 2); AgentsView remains here. ---------- */

  // agent state → flow2 status-chip tone + label + pulse dot colour.
  // IDLE is GREY (a v0.5 design rule), working pulses green, blocked is amber,
  // waiting reads as "waiting on you" red, offline is the calmest grey.
  const AGENT_STATE = {
    working: { tone: "t-met", label: "Working", c: "var(--met)", live: true },
    done: { tone: "t-met", label: "Done", c: "var(--met)" },
    idle: { tone: "t-todo", label: "Idle", c: "var(--todo)" },
    offline: { tone: "t-todo", label: "Offline", c: "var(--todo)" },
    "waiting-for-human": { tone: "t-waiting", label: "Waiting · you", c: "var(--wait)" },
    "waiting-for-agent": { tone: "t-progress", label: "Waiting · agent", c: "var(--prog)" },
    blocked: { tone: "t-blocked", label: "Blocked", c: "var(--blk)" }
  };

  function AgentsView({ agents, onDM }) {
    // offline drops to the bottom; everything else holds its incoming order.
    const sorted = [...agents].sort((a, b) => (a.state === "offline" ? 1 : 0) - (b.state === "offline" ? 1 : 0));
    const working = agents.filter((a) => a.state === "working").length;
    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light">
        <h1 className="fx-h1 fx-in">Agents</h1>
        <p className="fx-psub fx-in" style={{ animationDelay: ".03s" }}>{working} working · {agents.length - working} idle, blocked or offline</p>
        <div className="fx-list" style={{ marginTop: "18px" }}>
          {sorted.map((a, i) => {
            const s = AGENT_STATE[a.state] || AGENT_STATE.offline;
            const task = a.headline || a.meta || "—";
            return (
              <button className="fx-row" key={a.id || i}
              onClick={() => onDM && a.id && onDM(a.id)}
              style={{ cursor: a.id ? "pointer" : "default" }}
              title={a.id ? ("Message " + a.name) : undefined}>
                <Avatar name={a.name} kind="agent" size={30} />
                <span className="fx-row-main">
                  <span className="fx-row-name">{a.name}</span>
                  <span className="fx-row-meta">{task}</span>
                </span>
                <span className="fx-row-right">
                  <span className={"fx-pulse" + (s.live ? " is-live" : "")} style={{ background: s.c }} />
                  <span className="fx-crit-status" style={{ color: s.c }}>{s.label}</span>
                </span>
              </button>);
          })}
          {agents.length === 0 && <p className="fx-psub" style={{ marginTop: "8px" }}>No agents connected to the bus.</p>}
        </div>
      </div></div>);

  }

  /* ---------- floating Assistant FAB · visible stub (NOT wired) ----------
     Controlled when given open/onOpen/onClose (so ⌘K can open it with a
     prefilled prompt); otherwise self-manages its own open state. `prompt` is a
     query carried over from a ⌘K no-match — it's SHOWN, never answered: the
     backend is still a stub, so we never fabricate a reply. */
  function AssistantFab({ open: openProp, prompt, onOpen, onClose } = {}) {
    const [openLocal, setOpenLocal] = useState(false);
    const controlled = openProp !== undefined;
    const open = controlled ? openProp : openLocal;
    const doOpen = () => (controlled ? onOpen && onOpen() : setOpenLocal(true));
    const doClose = () => (controlled ? onClose && onClose() : setOpenLocal(false));
    if (!open) return (
      <button className="fx-asst-btn" title="Assistant (not wired yet)" onClick={doOpen}>
        <span className="fx-asst-spark">✦</span>
      </button>);
    return (
      <div className="fx-asst-panel" role="dialog" aria-label="Assistant">
        <div className="fx-asst-head">
          <span className="fx-asst-mark">✦</span>
          <div><div className="fx-asst-title">Assistant</div><div className="fx-asst-sub">not wired yet</div></div>
          <button className="fx-asst-close" onClick={doClose} aria-label="Close">×</button>
        </div>
        {prompt
          ? <div className="fx-asst-msg you"><span className="fx-asst-bub">{prompt}</span></div>
          : null}
        <div className="fx-asst-stub">
          <p className="fx-asst-stub-lead">This is a placeholder.</p>
          <p className="fx-asst-stub-body">{prompt
            ? "The assistant can't answer yet — it isn't connected to anything. Your question is parked here; the assistant gets wired up in a later track."
            : "The assistant isn't connected to anything yet — it can't answer questions or read your bus. It'll be wired up in a later track."}</p>
        </div>
        <div className="fx-asst-composer">
          <span className="fx-asst-field">{prompt || "Ask a quick question…"} (disabled)</span>
          <button className="fx-asst-send" disabled>↑</button>
        </div>
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
  function CmdK({ index, recents, onClose, onAsk }) {
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
    // selectable result that hands the query to the (stubbed) Assistant FAB.
    const askRow = (onAsk && ql && matches.length === 0)
      ? { key: "ask", type: "Ask", label: "Ask the assistant: “" + q.trim() + "”", sub: "the assistant isn't wired up yet — your question gets parked", go: () => onAsk(q.trim()) }
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
    const TYPEC = { "Go to": "#5b6ef0", Artifact: "#c0573b", Agent: "#3f8f59", Channel: "#8a8e97", Ask: "#5b6ef0" };
    return (
      <div className="fx-cmdk-scrim" onClick={onClose}>
        <div className="fx-cmdk" onClick={(e) => e.stopPropagation()}>
          <input className="fx-cmdk-input" autoFocus placeholder="Search artifacts, agents, conversations…"
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

  /* ---------- sidebar shell: Workspace nav + editable conversations ---------- */
  const WORKSPACE = [["⌂", "Home", "home"], ["❡", "Artifacts", "artifacts"], ["◎", "Goals", "goals"], ["◍", "Agents", "agents"]];

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
    const section = ctx.stageMode === "conversation" ? "convo"
      : ctx.stageMode === "artifact" ? "artifacts"
      : ctx.stageMode; // home | artifacts | agents | goals
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
              {key === "agents" && ctx.workingCount > 0 && <span className="fx-navbadge tone-met">{ctx.workingCount}</span>}
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

  Object.assign(window, { Sidebar, Avatar, StatusPill, MessageList, Composer, SextantGlyph, AssistantFab, CmdK, AgentsView });
})();