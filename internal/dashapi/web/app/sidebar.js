(function() {
  const { useState, useEffect, useRef } = React;
  const PALETTE = ["#6a55e0", "#e0a23a", "#d2674a", "#3a93d2", "#54ad6e", "#c060a8", "#2bb6a6"];
  function hueOf(name) {
    let h = 0;
    for (const c of name) h = h * 31 + c.charCodeAt(0) >>> 0;
    return PALETTE[h % PALETTE.length];
  }
  function initials(name) {
    const parts = name.replace(/[-@#]/g, " ").split(/[\s_]+/).filter(Boolean);
    return ((parts[0] ? parts[0][0] : "?") + (parts[1] ? parts[1][0] : "")).toUpperCase();
  }
  function Avatar({ name, kind, size = 26 }) {
    return /* @__PURE__ */ React.createElement(
      "span",
      {
        className: "sx-av" + (kind === "agent" ? " is-agent" : ""),
        style: { width: size, height: size, background: hueOf(name), fontSize: size * 0.42 }
      },
      initials(name)
    );
  }
  const ST = {
    review: { t: "Needs review", c: "review" },
    approved: { t: "Approved", c: "approved" },
    changes: { t: "Changes requested", c: "changes" },
    draft: { t: "Draft", c: "draft" },
    rejected: { t: "Rejected", c: "changes" },
    archived: { t: "Archived", c: "draft" }
  };
  function StatusPill({ status, big, dot }) {
    const s = ST[status] || ST.draft;
    if (dot) return /* @__PURE__ */ React.createElement("span", { className: "sx-sd sx-sd-" + s.c, title: s.t });
    return /* @__PURE__ */ React.createElement("span", { className: "sx-status sx-st-" + s.c + (big ? " is-big" : "") }, /* @__PURE__ */ React.createElement("i", null), s.t);
  }
  function MessageList({ messages, onArtifactRef }) {
    return /* @__PURE__ */ React.createElement("div", { className: "sx-activity" }, messages.map(
      (m) => m.kind === "event" ? /* @__PURE__ */ React.createElement("div", { className: "sx-event", key: m.id }, /* @__PURE__ */ React.createElement("span", { className: "sx-event-line" }), /* @__PURE__ */ React.createElement("span", { className: "sx-event-txt" }, m.text), /* @__PURE__ */ React.createElement("span", { className: "sx-event-time" }, m.time)) : /* @__PURE__ */ React.createElement("div", { className: "sx-msg" + (m.self ? " is-self" : ""), key: m.id }, /* @__PURE__ */ React.createElement(Avatar, { name: m.author, kind: m.role === "agent" ? "agent" : "human" }), /* @__PURE__ */ React.createElement("div", { className: "sx-msg-body" }, /* @__PURE__ */ React.createElement("div", { className: "sx-msg-head" }, /* @__PURE__ */ React.createElement("span", { className: "sx-msg-name" }, m.author), m.role === "agent" && /* @__PURE__ */ React.createElement("span", { className: "sx-tag-agent" }, "agent"), /* @__PURE__ */ React.createElement("span", { className: "sx-msg-time" }, m.time)), /* @__PURE__ */ React.createElement("div", { className: "sx-msg-text" }, m.text), m.artifactRef && /* @__PURE__ */ React.createElement("button", { className: "sx-artref", onClick: () => onArtifactRef && onArtifactRef(m.artifactRef) }, /* @__PURE__ */ React.createElement("span", { className: "sx-artref-ic" }, "\u25A3"), m.artifactRef)))
    ));
  }
  function Composer({ draft, setDraft, onSend, placeholder }) {
    return /* @__PURE__ */ React.createElement("div", { className: "sx-composer" }, /* @__PURE__ */ React.createElement(
      "input",
      {
        className: "sx-input",
        placeholder: placeholder || "Message\u2026",
        value: draft,
        onChange: (e) => setDraft(e.target.value),
        onKeyDown: (e) => {
          if (e.key === "Enter" && draft.trim()) onSend();
        }
      }
    ), /* @__PURE__ */ React.createElement("button", { className: "sx-send", disabled: !draft.trim(), onClick: onSend }, "\u21B5"));
  }
  function ConversationsView({ conversations, activeConvo, stageMode, onExpandConvo, hidden, onHide, onUnhide }) {
    const [showHidden, setShowHidden] = useState(false);
    const hid = hidden || /* @__PURE__ */ new Set();
    const visible = conversations.filter((c) => showHidden || !hid.has(c.key));
    const hiddenCount = conversations.reduce((n, c) => n + (hid.has(c.key) ? 1 : 0), 0);
    return /* @__PURE__ */ React.createElement("div", { className: "sx-clist" }, visible.map((c) => {
      const isHidden = hid.has(c.key);
      return /* @__PURE__ */ React.createElement(
        "div",
        {
          key: c.key,
          role: "button",
          className: "sx-citem" + (c.key === activeConvo && stageMode === "conversation" ? " is-on" : ""),
          style: { opacity: isHidden ? 0.5 : 1 },
          onClick: () => onExpandConvo(c.key)
        },
        c.type === "topic" ? /* @__PURE__ */ React.createElement("span", { className: "sx-cglyph" }, "#") : /* @__PURE__ */ React.createElement(Avatar, { name: c.name, kind: "agent", size: 26 }),
        /* @__PURE__ */ React.createElement("div", { className: "sx-cmain" }, /* @__PURE__ */ React.createElement("div", { className: "sx-ctop" }, /* @__PURE__ */ React.createElement("span", { className: "sx-cname" }, c.name), /* @__PURE__ */ React.createElement("span", { className: "sx-ctime" }, c.time)), /* @__PURE__ */ React.createElement("div", { className: "sx-csnip" }, c.snippet)),
        c.unread > 0 && /* @__PURE__ */ React.createElement("span", { className: "sx-unread" }, c.unread),
        /* @__PURE__ */ React.createElement(
          "span",
          {
            className: "sx-chide",
            title: isHidden ? "Unhide" : "Hide",
            onClick: (e) => {
              e.stopPropagation();
              if (isHidden) {
                onUnhide && onUnhide(c.key);
              } else {
                onHide && onHide(c.key);
              }
            }
          },
          isHidden ? "\u21A9" : "\xD7"
        )
      );
    }), hiddenCount > 0 && /* @__PURE__ */ React.createElement("button", { className: "sx-cshowhidden", onClick: () => setShowHidden((v) => !v) }, showHidden ? "\u2014 hide hidden" : hiddenCount + " hidden \u2014 show"));
  }
  function ArtifactsView({ artifacts, activeArtifact, onOpenArtifact }) {
    const groups = [
      ["review", "Needs review"],
      ["changes", "Changes requested"],
      ["draft", "Draft"],
      ["approved", "Approved"],
      ["rejected", "Rejected"],
      ["archived", "Archived"]
    ];
    return /* @__PURE__ */ React.createElement("div", { className: "sx-alist" }, groups.map(([st, label]) => {
      const items = artifacts.filter((a) => a.status === st);
      if (!items.length) return null;
      return /* @__PURE__ */ React.createElement("div", { className: "sx-agroup", key: st }, /* @__PURE__ */ React.createElement("div", { className: "sx-agroup-h" }, /* @__PURE__ */ React.createElement(StatusPill, { status: st, dot: true }), /* @__PURE__ */ React.createElement("span", null, label), /* @__PURE__ */ React.createElement("span", { className: "sx-agroup-n" }, items.length)), items.map(
        (a) => /* @__PURE__ */ React.createElement(
          "button",
          {
            key: a.name,
            className: "sx-aitem" + (a.name === activeArtifact ? " is-on" : ""),
            onClick: () => onOpenArtifact(a.name)
          },
          /* @__PURE__ */ React.createElement("span", { className: "sx-aicon" }, a.type === "markdown" ? "\u2761" : a.type === "sheet" ? "\u25A6" : "\u25C6"),
          /* @__PURE__ */ React.createElement("div", { className: "sx-amain" }, /* @__PURE__ */ React.createElement("div", { className: "sx-aname" }, a.name), /* @__PURE__ */ React.createElement("div", { className: "sx-ameta" }, a.topic && /* @__PURE__ */ React.createElement("span", { className: "sx-achip" }, "# ", a.topic), a.updated && /* @__PURE__ */ React.createElement("span", null, a.updated))),
          a.author && a.author.name && /* @__PURE__ */ React.createElement(Avatar, { name: a.author.name, kind: a.author.kind, size: 20 })
        )
      ));
    }));
  }
  function GoalsView({ goals }) {
    return /* @__PURE__ */ React.createElement("div", { className: "sx-goals" }, goals.map((g, i) => {
      const pct = Math.min(100, Math.round(g.value / g.target * 100));
      return /* @__PURE__ */ React.createElement("div", { className: "sx-goal", key: i }, /* @__PURE__ */ React.createElement("div", { className: "sx-goal-top" }, /* @__PURE__ */ React.createElement("span", { className: "sx-goal-label" }, g.label), /* @__PURE__ */ React.createElement("span", { className: "sx-goal-val" + (g.met ? " met" : g.blocked ? " blk" : "") }, g.display)), /* @__PURE__ */ React.createElement("div", { className: "sx-bar" }, /* @__PURE__ */ React.createElement("span", { className: "sx-bar-fill" + (g.met ? " met" : g.blocked ? " blk" : ""), style: { width: (g.blocked ? 6 : pct) + "%" } })), /* @__PURE__ */ React.createElement("div", { className: "sx-goal-note" }, g.note));
    }));
  }
  function AgentsView({ agents, onDM }) {
    const STATE = {
      working: { c: "approved", label: "working" },
      idle: { c: "draft", label: "idle" },
      blocked: { c: "changes", label: "blocked" },
      offline: { c: "draft", label: "offline" }
    };
    return /* @__PURE__ */ React.createElement("div", { className: "sx-clients" }, agents.map((a, i) => {
      const s = STATE[a.state] || STATE.offline;
      return /* @__PURE__ */ React.createElement(
        "div",
        {
          className: "sx-client",
          key: i,
          onClick: () => onDM && a.id && onDM(a.id),
          style: { cursor: a.id ? "pointer" : "default" },
          title: a.id ? "Message " + a.name : void 0
        },
        /* @__PURE__ */ React.createElement("span", { className: "sx-agent-av" }, /* @__PURE__ */ React.createElement(Avatar, { name: a.name, kind: "agent", size: 28 }), /* @__PURE__ */ React.createElement("span", { className: "sx-agent-dot sx-sd-" + s.c + (a.state === "working" ? " is-live" : "") })),
        /* @__PURE__ */ React.createElement("div", { className: "sx-client-main" }, /* @__PURE__ */ React.createElement("div", { className: "sx-client-name" }, a.name), /* @__PURE__ */ React.createElement("div", { className: "sx-client-meta" }, a.meta)),
        /* @__PURE__ */ React.createElement("span", { className: "sx-agent-state sx-state-" + s.c }, s.label)
      );
    }));
  }
  function sectionsFor(ctx) {
    const unread = ctx.conversations.reduce((s, c) => s + (c.unread || 0), 0);
    const review = ctx.artifacts.filter((a) => a.status === "review").length;
    const working = ctx.agents.filter((a) => a.state === "working").length;
    return [
      { key: "conversations", label: "Conversations", glyph: "\u2317", badge: unread, tone: "brand", render: () => /* @__PURE__ */ React.createElement(ConversationsView, { ...ctx }) },
      { key: "artifacts", label: "Artifacts", glyph: "\u25C6", badge: review, tone: "review", render: () => /* @__PURE__ */ React.createElement(ArtifactsView, { ...ctx }) },
      { key: "goals", label: "Goal progress", glyph: "\u25CE", badge: 0, tone: "draft", render: () => /* @__PURE__ */ React.createElement(GoalsView, { goals: ctx.goals }) },
      { key: "agents", label: "Agent status", glyph: "\u25C9", badge: working, tone: "approved", render: () => /* @__PURE__ */ React.createElement(AgentsView, { agents: ctx.agents, onDM: ctx.onDM }) }
    ];
  }
  function Section({ sec, open, onToggle }) {
    return /* @__PURE__ */ React.createElement("section", { className: "sx-sec" + (open ? " is-open" : "") }, /* @__PURE__ */ React.createElement("button", { className: "sx-sec-head", onClick: onToggle, "aria-expanded": open, style: { padding: "4px 16px" } }, /* @__PURE__ */ React.createElement("span", { className: "sx-sec-chev" }, "\u25B8"), /* @__PURE__ */ React.createElement("span", { className: "sx-sec-label", style: { margin: "0px" } }, sec.label), sec.badge > 0 && /* @__PURE__ */ React.createElement("span", { className: "sx-sec-badge tone-" + sec.tone }, sec.badge)), open && /* @__PURE__ */ React.createElement("div", { className: "sx-sec-body" }, sec.render()));
  }
  function Sidebar({ ctx, busName, navMode }) {
    const secs = sectionsFor(ctx);
    const [open, setOpen] = useState({ conversations: true, artifacts: true, goals: false, agents: false });
    const [tab, setTab] = useState("conversations");
    const toggle = (k) => setOpen((o) => ({ ...o, [k]: !o[k] }));
    const activeTab = secs.find((s) => s.key === tab) || secs[0];
    return /* @__PURE__ */ React.createElement("aside", { className: "sx-side" }, /* @__PURE__ */ React.createElement("div", { className: "sx-brand" }, /* @__PURE__ */ React.createElement("div", { className: "sx-mark" }, /* @__PURE__ */ React.createElement("span", { className: "sx-star" }, "\u2726"), "Sextant"), /* @__PURE__ */ React.createElement("div", { className: "sx-brand-tools" }, /* @__PURE__ */ React.createElement("button", { className: "sx-home-mini" + (ctx.stageMode === "home" ? " is-on" : ""), onClick: ctx.onGoHome, title: "Home" }, /* @__PURE__ */ React.createElement("span", { className: "sx-home-mini-ic" }, "\u2726"), "Home"), /* @__PURE__ */ React.createElement("button", { className: "sx-search-mini", title: "Search the bus  \u2318K" }, /* @__PURE__ */ React.createElement("span", { className: "sx-search-ic" }, "\u2315")))), navMode === "tabs" ? /* @__PURE__ */ React.createElement("div", { className: "sx-tabwrap" }, /* @__PURE__ */ React.createElement("div", { className: "sx-tabs", role: "tablist" }, secs.map(
      (s) => /* @__PURE__ */ React.createElement(
        "button",
        {
          key: s.key,
          role: "tab",
          "aria-selected": s.key === tab,
          className: "sx-tab" + (s.key === tab ? " is-on" : ""),
          onClick: () => setTab(s.key)
        },
        /* @__PURE__ */ React.createElement("span", { className: "sx-tab-label" }, s.label),
        s.badge > 0 && /* @__PURE__ */ React.createElement("span", { className: "sx-tab-badge tone-" + s.tone }, s.badge)
      )
    )), /* @__PURE__ */ React.createElement("div", { className: "sx-tabbody" }, activeTab.render())) : /* @__PURE__ */ React.createElement("div", { className: "sx-nav" }, secs.map(
      (s) => /* @__PURE__ */ React.createElement(Section, { key: s.key, sec: s, open: !!open[s.key], onToggle: () => toggle(s.key) })
    )), /* @__PURE__ */ React.createElement("div", { className: "sx-me" }, /* @__PURE__ */ React.createElement(Avatar, { name: "you", kind: "human", size: 24 }), /* @__PURE__ */ React.createElement("span", { className: "sx-me-name" }, "you"), /* @__PURE__ */ React.createElement("span", { className: "sx-tag-human sm" }, "operator"), /* @__PURE__ */ React.createElement("span", { className: "sx-key" }, "ed25519:7c\u2026e1"), /* @__PURE__ */ React.createElement("span", { className: "sx-verified sm" }, "\u2713")));
  }
  Object.assign(window, { Sidebar, Avatar, StatusPill, MessageList, Composer });
})();
