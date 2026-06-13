/* sidebar.jsx — splittable multi-view navigator.
   Panes stack vertically; each pane shows one of: Conversations, Chat, Artifacts, Goals, Clients.
   Exports: Sidebar, Avatar, StatusPill, MessageList, Composer (to window). */
(function () {
  const { useState, useEffect, useRef } = React;

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
    changes: { t: "Changes requested", c: "changes" }, draft: { t: "Draft", c: "draft" }
  };
  function StatusPill({ status, big, dot }) {
    const s = ST[status] || ST.draft;
    if (dot) return <span className={"sx-sd sx-sd-" + s.c} title={s.t} />;
    return <span className={"sx-status sx-st-" + s.c + (big ? " is-big" : "")}><i />{s.t}</span>;
  }

  /* ---------- shared message list + composer ---------- */
  function MessageList({ messages, onArtifactRef }) {
    return (
      <div className="sx-activity">
        {messages.map((m) => m.kind === "event" ?
        <div className="sx-event" key={m.id}>
            <span className="sx-event-line" /><span className="sx-event-txt">{m.text}</span>
            <span className="sx-event-time">{m.time}</span>
          </div> :

        <div className={"sx-msg" + (m.self ? " is-self" : "")} key={m.id}>
            <Avatar name={m.author} kind={m.role === "agent" ? "agent" : "human"} />
            <div className="sx-msg-body">
              <div className="sx-msg-head">
                <span className="sx-msg-name">{m.author}</span>
                {m.role === "agent" && <span className="sx-tag-agent">agent</span>}
                <span className="sx-msg-time">{m.time}</span>
              </div>
              <div className="sx-msg-text">{m.text}</div>
              {m.artifactRef &&
            <button className="sx-artref" onClick={() => onArtifactRef && onArtifactRef(m.artifactRef)}>
                  <span className="sx-artref-ic">▣</span>{m.artifactRef}
                </button>
            }
            </div>
          </div>
        )}
      </div>);

  }
  function Composer({ draft, setDraft, onSend, placeholder }) {
    return (
      <div className="sx-composer">
        <input className="sx-input" placeholder={placeholder || "Message…"} value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {if (e.key === "Enter" && draft.trim()) onSend();}} />
        <button className="sx-send" disabled={!draft.trim()} onClick={onSend}>↵</button>
      </div>);

  }

  /* ---------- views ---------- */
  function ConversationsView({ conversations, activeConvo, stageMode, onExpandConvo }) {
    return (
      <div className="sx-clist">
        {conversations.map((c) =>
        <button key={c.key} className={"sx-citem" + (c.key === activeConvo && stageMode === "conversation" ? " is-on" : "")}
        onClick={() => onExpandConvo(c.key)}>
            {c.type === "topic" ?
          <span className="sx-cglyph">#</span> :
          <Avatar name={c.name} kind="agent" size={26} />}
            <div className="sx-cmain">
              <div className="sx-ctop">
                <span className="sx-cname">{c.type === "topic" ? c.name : c.name}</span>
                <span className="sx-ctime">{c.time}</span>
              </div>
              <div className="sx-csnip">{c.snippet}</div>
            </div>
            {c.unread > 0 && <span className="sx-unread">{c.unread}</span>}
          </button>
        )}
      </div>);

  }

  function ArtifactsView({ artifacts, activeArtifact, onOpenArtifact }) {
    const groups = [
    ["review", "Needs review"], ["changes", "Changes requested"],
    ["draft", "Draft"], ["approved", "Approved"]];

    return (
      <div className="sx-alist">
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
                    <div className="sx-ameta"><span className="sx-achip"># {a.topic}</span><span className="mono">v{a.version}</span><span>· {a.updated}</span></div>
                  </div>
                  <Avatar name={a.author.name} kind={a.author.kind} size={20} />
                </button>
              )}
            </div>);

        })}
      </div>);

  }

  function GoalsView({ goals }) {
    return (
      <div className="sx-goals">
        {goals.map((g, i) => {
          const pct = Math.min(100, Math.round(g.value / g.target * 100));
          return (
            <div className="sx-goal" key={i}>
              <div className="sx-goal-top">
                <span className="sx-goal-label">{g.label}</span>
                <span className={"sx-goal-val" + (g.met ? " met" : g.blocked ? " blk" : "")}>{g.display}</span>
              </div>
              <div className="sx-bar"><span className={"sx-bar-fill" + (g.met ? " met" : g.blocked ? " blk" : "")} style={{ width: (g.blocked ? 6 : pct) + "%" }} /></div>
              <div className="sx-goal-note">{g.note}</div>
            </div>);

        })}
      </div>);

  }

  function AgentsView({ agents }) {
    const STATE = {
      working: { c: "approved", label: "working" }, idle: { c: "draft", label: "idle" },
      blocked: { c: "changes", label: "blocked" }, offline: { c: "draft", label: "offline" }
    };
    return (
      <div className="sx-clients">
        {agents.map((a, i) => {
          const s = STATE[a.state] || STATE.offline;
          return (
            <div className="sx-client" key={i}>
              <span className="sx-agent-av"><Avatar name={a.name} kind="agent" size={28} /><span className={"sx-agent-dot sx-sd-" + s.c + (a.state === "working" ? " is-live" : "")} /></span>
              <div className="sx-client-main">
                <div className="sx-client-name">{a.name}</div>
                <div className="sx-client-meta">{a.meta}</div>
              </div>
              <span className={"sx-agent-state sx-state-" + s.c}>{s.label}</span>
            </div>);

        })}
      </div>);

  }

  /* ---------- section registry ---------- */
  function sectionsFor(ctx) {
    const unread = ctx.conversations.reduce((s, c) => s + (c.unread || 0), 0);
    const review = ctx.artifacts.filter((a) => a.status === "review").length;
    const working = ctx.agents.filter((a) => a.state === "working").length;
    return [
    { key: "conversations", label: "Conversations", glyph: "⌗", badge: unread, tone: "brand", render: () => <ConversationsView {...ctx} /> },
    { key: "artifacts", label: "Artifacts", glyph: "◆", badge: review, tone: "review", render: () => <ArtifactsView {...ctx} /> },
    { key: "goals", label: "Goal progress", glyph: "◎", badge: 0, tone: "draft", render: () => <GoalsView goals={ctx.goals} /> },
    { key: "agents", label: "Agent status", glyph: "◉", badge: working, tone: "approved", render: () => <AgentsView agents={ctx.agents} /> }];

  }

  /* ---------- accordion section ---------- */
  function Section({ sec, open, onToggle }) {
    return (
      <section className={"sx-sec" + (open ? " is-open" : "")}>
        <button className="sx-sec-head" onClick={onToggle} aria-expanded={open} style={{ padding: "4px 16px" }}>
          <span className="sx-sec-chev">▸</span>
          <span className="sx-sec-label" style={{ margin: "0px" }}>{sec.label}</span>
          {sec.badge > 0 && <span className={"sx-sec-badge tone-" + sec.tone}>{sec.badge}</span>}
        </button>
        {open && <div className="sx-sec-body">{sec.render()}</div>}
      </section>);

  }

  /* ---------- sidebar shell ---------- */
  function Sidebar({ ctx, busName, navMode }) {
    const secs = sectionsFor(ctx);
    const [open, setOpen] = useState({ conversations: true, artifacts: true, goals: false, agents: false });
    const [tab, setTab] = useState("conversations");
    const toggle = (k) => setOpen((o) => ({ ...o, [k]: !o[k] }));
    const activeTab = secs.find((s) => s.key === tab) || secs[0];

    return (
      <aside className="sx-side">
        <div className="sx-brand">
          <div className="sx-mark"><span className="sx-star">✦</span>Sextant</div>
          <div className="sx-brand-tools">
            <button className={"sx-home-mini" + (ctx.stageMode === "home" ? " is-on" : "")} onClick={ctx.onGoHome} title="Home">
              <span className="sx-home-mini-ic">✦</span>Home
            </button>
            <button className="sx-search-mini" title="Search the bus  ⌘K">
              <span className="sx-search-ic">⌕</span>
            </button>
          </div>
        </div>

        {navMode === "tabs" ?
        <div className="sx-tabwrap">
            <div className="sx-tabs" role="tablist">
              {secs.map((s) =>
            <button key={s.key} role="tab" aria-selected={s.key === tab}
            className={"sx-tab" + (s.key === tab ? " is-on" : "")} onClick={() => setTab(s.key)}>
                  <span className="sx-tab-label">{s.label}</span>
                  {s.badge > 0 && <span className={"sx-tab-badge tone-" + s.tone}>{s.badge}</span>}
                </button>
            )}
            </div>
            <div className="sx-tabbody">{activeTab.render()}</div>
          </div> :

        <div className="sx-nav">
            {secs.map((s) =>
          <Section key={s.key} sec={s} open={!!open[s.key]} onToggle={() => toggle(s.key)} />
          )}
          </div>
        }

        <div className="sx-me">
          <Avatar name="you" kind="human" size={24} />
          <span className="sx-me-name">you</span>
          <span className="sx-tag-human sm">operator</span>
          <span className="sx-key">ed25519:7c…e1</span>
          <span className="sx-verified sm">✓</span>
        </div>
      </aside>);

  }

  Object.assign(window, { Sidebar, Avatar, StatusPill, MessageList, Composer });
})();