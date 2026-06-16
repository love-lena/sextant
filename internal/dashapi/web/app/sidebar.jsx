/* sidebar.jsx — Sextant Live Flow shell (v0.5 reskin, TASK stage a).
   Charcoal 284px sidebar: brand glyph + Workspace nav (Home / Artifacts / Goals /
   Agents) + an editable Conversations list + a "You · operator" footer. The white
   stage to the right renders the existing views (their internals unchanged).
   Exports: Sidebar, Avatar, StatusPill, MessageList, Composer, SextantGlyph,
   GoalsStub, AssistantFab, CmdK (to window). */
(function () {
  const { useState, useEffect, useRef } = React;

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
    // [[name]] wikilinks: rewrite only in text between tags. The captured name is
    // the visible label; data-art carries the (attr-escaped) target for the click.
    if (html.indexOf("[[") >= 0) {
      const wiki = /\[\[([^\[\]]+?)\]\]/g;
      html = html.replace(/>([^<]+)</g, (full, seg) =>
        ">" + seg.replace(wiki, (m, raw) => {
          const n = raw.trim();
          if (!n) return m;
          return '<a class="sx-artlink" data-art="' + escapeAttr(n) + '">' + escapeHTML(n) + "</a>";
        }) + "<");
      html = window.DOMPurify.sanitize(html);
    }
    const list = (names || []).filter(Boolean);
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

  /* ---------- views (rendered in the white stage). Stage (a) wraps the existing
     list internals in a flow2 stage frame; the internals are a later stage. ---------- */
  function ArtifactsView({ artifacts, activeArtifact, onOpenArtifact }) {
    const groups = [
    ["review", "Needs review"], ["changes", "Changes requested"],
    ["draft", "Draft"], ["approved", "Approved"],
    ["rejected", "Rejected"], ["archived", "Archived"]];
    const review = artifacts.filter((a) => a.status === "review").length;
    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light">
        <h1 className="fx-h1">Artifacts</h1>
        <p className="fx-psub">{artifacts.length} documents · {review} awaiting you</p>
        <div className="sx-alist" style={{ marginTop: "18px" }}>
          {groups.map(([st, label]) => {
            const items = artifacts.filter((a) => a.status === st);
            if (!items.length) return null;
            return (
              <div className="sx-agroup" key={st}>
                <div className="sx-agroup-h"><StatusPill status={st} dot /><span>{label}</span><span className="sx-agroup-n">{items.length}</span></div>
                {items.map((a) =>
                <button key={a.name} className={"sx-aitem" + (a.name === activeArtifact ? " is-on" : "")}
                onClick={() => onOpenArtifact(a.name)}>
                    <span className="sx-aicon">{a.type === "markdown" ? "❡" : a.type === "sheet" ? "▦" : "◆"}</span>
                    <div className="sx-amain">
                      <div className="sx-aname">{a.name}</div>
                      <div className="sx-ameta">{a.topic && <span className="sx-achip"># {a.topic}</span>}{a.updated && <span>{a.updated}</span>}</div>
                    </div>
                    {a.author && a.author.name && <Avatar name={a.author.name} kind={a.author.kind} size={20} />}
                  </button>
                )}
              </div>);
          })}
        </div>
      </div></div>);

  }

  function AgentsView({ agents, onDM }) {
    // status → tone. IDLE is GREY (draft), not amber — a design rule for v0.5.
    const STATE = {
      working: { c: "approved", label: "working" }, done: { c: "approved", label: "done" },
      idle: { c: "draft", label: "idle" }, offline: { c: "draft", label: "offline" },
      "waiting-for-human": { c: "review", label: "waiting · human" },
      "waiting-for-agent": { c: "review", label: "waiting · agent" },
      blocked: { c: "changes", label: "blocked" }
    };
    const sorted = [...agents].sort((a, b) => (a.state === "offline" ? 1 : b.state === "offline" ? -1 : 0));
    const working = agents.filter((a) => a.state === "working").length;
    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light">
        <h1 className="fx-h1">Agents</h1>
        <p className="fx-psub">{working} working · {agents.length - working} idle or offline</p>
        <div className="sx-clients" style={{ marginTop: "12px" }}>
          {sorted.map((a, i) => {
            const s = STATE[a.state] || STATE.offline;
            return (
              <div className="sx-client" key={i}
              onClick={() => onDM && a.id && onDM(a.id)}
              style={{ cursor: a.id ? "pointer" : "default" }}
              title={a.id ? ("Message " + a.name) : undefined}>
                <span className="sx-agent-av"><Avatar name={a.name} kind="agent" size={28} /><span className={"sx-agent-dot sx-sd-" + s.c + (a.state === "working" ? " is-live" : "")} /></span>
                <div className="sx-client-main">
                  <div className="sx-client-name">{a.name}</div>
                  <div className="sx-client-meta">{a.meta}</div>
                </div>
                <span className={"sx-agent-state sx-state-" + s.c}>{s.label}</span>
              </div>);
          })}
        </div>
      </div></div>);

  }

  /* ---------- Goals · inert placeholder (Track 2 owns the real view) ---------- */
  function GoalsStub() {
    return (
      <div className="fx-scroll"><div className="fx-col">
        <h1 className="fx-h1">Goals</h1>
        <p className="fx-psub">Coming in Track 2 — goal north stars, criteria, and rollup.</p>
        <div className="fx-stub">
          <span className="fx-stub-ic">◎</span>
          <div>
            <div className="fx-stub-title">No goal data yet.</div>
            <div className="fx-stub-sub">Goals get a bus primitive in a later track. This page is a placeholder.</div>
          </div>
        </div>
      </div></div>);
  }

  /* ---------- floating Assistant FAB · visible stub (NOT wired) ---------- */
  function AssistantFab() {
    const [open, setOpen] = useState(false);
    if (!open) return (
      <button className="fx-asst-btn" title="Assistant (not wired yet)" onClick={() => setOpen(true)}>
        <span className="fx-asst-spark">✦</span>
      </button>);
    return (
      <div className="fx-asst-panel" role="dialog" aria-label="Assistant">
        <div className="fx-asst-head">
          <span className="fx-asst-mark">✦</span>
          <div><div className="fx-asst-title">Assistant</div><div className="fx-asst-sub">not wired yet</div></div>
          <button className="fx-asst-close" onClick={() => setOpen(false)} aria-label="Close">×</button>
        </div>
        <div className="fx-asst-stub">
          <p className="fx-asst-stub-lead">This is a placeholder.</p>
          <p className="fx-asst-stub-body">The assistant isn't connected to anything yet — it can't answer questions or read your bus. It'll be wired up in a later track.</p>
        </div>
        <div className="fx-asst-composer">
          <span className="fx-asst-field">Ask a quick question… (disabled)</span>
          <button className="fx-asst-send" disabled>↑</button>
        </div>
      </div>);
  }

  /* ---------- ⌘K command palette · real, client-side find & jump ---------- */
  // Searches the already-loaded artifacts + clients (agents) + conversation
  // subjects; selecting a result opens it via the existing open handlers.
  function CmdK({ index, onClose }) {
    const [q, setQ] = useState("");
    const [sel, setSel] = useState(0);
    const ql = q.trim().toLowerCase();
    const results = (ql ? index.filter((it) => it.kw.indexOf(ql) >= 0) : index).slice(0, 9);
    useEffect(() => { setSel(0); }, [q]);
    const pick = (it) => { if (it) { it.go(); onClose(); } };
    const onKey = (e) => {
      if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => Math.min(s + 1, results.length - 1)); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => Math.max(s - 1, 0)); }
      else if (e.key === "Enter") { e.preventDefault(); pick(results[sel]); }
      else if (e.key === "Escape") { e.preventDefault(); onClose(); }
    };
    const TYPEC = { Artifact: "#c0573b", Agent: "#3f8f59", Channel: "#8a8e97" };
    return (
      <div className="fx-cmdk-scrim" onClick={onClose}>
        <div className="fx-cmdk" onClick={(e) => e.stopPropagation()}>
          <input className="fx-cmdk-input" autoFocus placeholder="Search artifacts, agents, conversations…"
            value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={onKey} />
          <div className="fx-cmdk-results">
            {results.length === 0 && <div className="fx-cmdk-empty">No matches for “{q}”</div>}
            {results.map((it, i) => (
              <button className={"fx-cmdk-row" + (i === sel ? " is-sel" : "")} key={it.key}
                onMouseEnter={() => setSel(i)} onClick={() => pick(it)}>
                <span className="fx-cmdk-type" style={{ color: TYPEC[it.type], borderColor: TYPEC[it.type] + "44", background: TYPEC[it.type] + "10" }}>{it.type}</span>
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

  function Sidebar({ ctx, busName, navMode }) {
    const section = ctx.stageMode === "conversation" ? "convo"
      : ctx.stageMode === "artifact" ? "artifacts"
      : ctx.stageMode; // home | artifacts | agents | goals
    const meName = (ctx.self && ctx.self.display_name) || "you";
    return (
      <aside className="fx-side">
        <div className="fx-brand">
          <span className="fx-mark">
            <svg className="fx-logomark" viewBox="0 0 479 412" width="43" height="37" aria-hidden="true">
              <SextantGlyph ink="#f3f2ee" teal="#4AA2AA" purple="#8280E5" />
            </svg>
            <span className="fx-word">Sextant</span>
          </span>
          <button className="fx-side-search" title="Search the bus  ⌘K" onClick={ctx.onSearch}>
            <span className="fx-search-ic">⌕</span>
            <span className="fx-kbd">⌘K</span>
          </button>
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

  Object.assign(window, { Sidebar, Avatar, StatusPill, MessageList, Composer, SextantGlyph, GoalsStub, AssistantFab, CmdK, ArtifactsView, AgentsView });
})();