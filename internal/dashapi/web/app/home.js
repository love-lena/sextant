(function() {
  const HOME_CONFIG = {
    greeting: {
      eyebrow: "MON \xB7 JUN 12 \xB7 09:14",
      heading: "Two things actually need you. The rest is handled.",
      note: "Build's green, the inbox is quiet, and qa-agent has stopped arguing with itself \u2014 for now. I held the launch brief and the pricing model for your call; everything else moved without you. Coffee first is a defensible strategy.",
      signedBy: "research-agent",
      updated: "4m"
    },
    banner: {
      slotId: "home-banner",
      placeholder: "Drop a banner image",
      caption: "release-q3 \u2014 Lumen Agents GA"
    },
    blocks: [
      {
        type: "agenda",
        title: "Needs you",
        live: false,
        items: [
          { text: "Approve Q3 Launch Brief v4 \u2014 billing risk now has an owner", action: "approve", ref: "Q3 Launch Brief", tone: "review" },
          { text: "Pricing Model v7 is waiting on your read before the soak window", action: "review", ref: "Pricing Model", tone: "review" },
          { text: "designer-agent DM'd you about the logo weight", action: "reply", ref: "dm-designer", tone: "draft" }
        ]
      },
      {
        type: "pinned",
        title: "Pinned",
        live: true,
        names: ["Q3 Launch Brief", "Pricing Model", "API Design Note"]
      },
      {
        type: "links",
        title: "Quick links",
        live: false,
        items: [
          { label: "Launch runbook", meta: "notion", href: "#" },
          { label: "Staging dashboard", meta: "grafana", href: "#" },
          { label: "GA checklist", meta: "linear", href: "#" },
          { label: "Billing meter spec", meta: "doc", href: "#" },
          { label: "On-call rotation", meta: "pagerduty", href: "#" }
        ]
      },
      {
        type: "goals",
        title: "Goal snapshot",
        live: true,
        max: 3
      },
      {
        type: "agents",
        title: "Agents",
        live: true
      },
      {
        type: "note",
        title: "Watching",
        live: false,
        body: "Test coverage is sitting at 81% against an 85% gate. Not blocking yet, but if it hasn't moved by Thursday I'll flag it to qa-agent before it becomes a freeze problem."
      },
      {
        type: "activity",
        title: "Recent on the bus",
        live: false,
        items: [
          { who: "research-agent", text: "published Q3 Launch Brief \xB7 v4", time: "3m" },
          { who: "you", text: "approved API Design Note \xB7 v9", time: "18m" },
          { who: "qa-agent", text: "bumped audit export schema to v2", time: "22m" },
          { who: "research-agent", text: "moved multi-language to Q4", time: "1h" }
        ]
      }
    ]
  };
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
  function Av({ name, size = 22 }) {
    return /* @__PURE__ */ React.createElement("span", { className: "hm-av", style: { width: size, height: size, background: hueOf(name), fontSize: size * 0.42 } }, initials(name));
  }
  const ACTION = {
    approve: { label: "Approve", cls: "is-approve" },
    review: { label: "Review", cls: "is-review" },
    reply: { label: "Reply", cls: "is-reply" }
  };
  const ARTICON = { markdown: "\u2761", sheet: "\u25A6", default: "\u25C6" };
  function CardHead({ title, live, meta }) {
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card-head" }, /* @__PURE__ */ React.createElement("span", { className: "hm-card-title" }, title), live && /* @__PURE__ */ React.createElement("span", { className: "hm-live", title: "Pulls live from the bus" }, /* @__PURE__ */ React.createElement("span", { className: "hm-live-dot" }), "live"), meta && /* @__PURE__ */ React.createElement("span", { className: "hm-card-meta" }, meta));
  }
  function AgendaBlock({ block, ctx }) {
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card hm-agenda" }, /* @__PURE__ */ React.createElement(CardHead, { title: block.title, meta: block.items.length + " open" }), /* @__PURE__ */ React.createElement("ul", { className: "hm-agenda-list" }, block.items.map((it, i) => {
      const a = ACTION[it.action] || ACTION.review;
      const onClick = () => {
        if (it.action === "reply") ctx.onExpandConvo(it.ref);
        else ctx.onOpenArtifact(it.ref);
      };
      return /* @__PURE__ */ React.createElement("li", { className: "hm-agenda-row", key: i }, /* @__PURE__ */ React.createElement("span", { className: "hm-dot sx-sd-" + (it.tone || "review") }), /* @__PURE__ */ React.createElement("span", { className: "hm-agenda-text" }, it.text), /* @__PURE__ */ React.createElement("button", { className: "hm-act " + a.cls, onClick }, a.label));
    })));
  }
  function PinnedBlock({ block, ctx }) {
    const picked = (block.names || []).map((n) => ctx.artifacts.find((a) => a.name === n)).filter(Boolean);
    const items = picked.length ? picked : (ctx.artifacts || []).slice(0, 4);
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card" }, /* @__PURE__ */ React.createElement(CardHead, { title: block.title, live: block.live }), /* @__PURE__ */ React.createElement("div", { className: "hm-pin-list" }, items.map((a) => /* @__PURE__ */ React.createElement("button", { className: "hm-pin", key: a.name, onClick: () => ctx.onOpenArtifact(a.name) }, /* @__PURE__ */ React.createElement("span", { className: "hm-pin-ic" }, ARTICON[a.type] || ARTICON.default), /* @__PURE__ */ React.createElement("span", { className: "hm-pin-main" }, /* @__PURE__ */ React.createElement("span", { className: "hm-pin-name" }, a.name), /* @__PURE__ */ React.createElement("span", { className: "hm-pin-meta" }, a.topic ? "# " + a.topic + " \xB7 " : "", /* @__PURE__ */ React.createElement("span", { className: "mono" }, "v", a.version))), /* @__PURE__ */ React.createElement("span", { className: "sx-sd sx-sd-" + (a.status === "approved" ? "approved" : a.status === "changes" ? "changes" : a.status === "draft" ? "draft" : "review"), title: a.status })))));
  }
  function LinksBlock({ block }) {
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card" }, /* @__PURE__ */ React.createElement(CardHead, { title: block.title }), /* @__PURE__ */ React.createElement("div", { className: "hm-links" }, block.items.map((l, i) => /* @__PURE__ */ React.createElement("a", { className: "hm-link", key: i, href: l.href, target: "_blank", rel: "noreferrer" }, /* @__PURE__ */ React.createElement("span", { className: "hm-link-label" }, l.label), /* @__PURE__ */ React.createElement("span", { className: "hm-link-meta mono" }, l.meta), /* @__PURE__ */ React.createElement("span", { className: "hm-link-arrow" }, "\u2197")))));
  }
  function GoalsBlock({ block, ctx }) {
    const goals = (ctx.goals || []).slice(0, block.max || ctx.goals.length);
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card" }, /* @__PURE__ */ React.createElement(CardHead, { title: block.title, live: block.live }), /* @__PURE__ */ React.createElement("div", { className: "hm-goals" }, goals.map((g, i) => {
      const pct = Math.min(100, Math.round(g.value / g.target * 100));
      return /* @__PURE__ */ React.createElement("div", { className: "hm-goal", key: i }, /* @__PURE__ */ React.createElement("div", { className: "hm-goal-top" }, /* @__PURE__ */ React.createElement("span", { className: "hm-goal-label" }, g.label), /* @__PURE__ */ React.createElement("span", { className: "hm-goal-val" + (g.met ? " met" : g.blocked ? " blk" : "") }, g.display)), /* @__PURE__ */ React.createElement("div", { className: "hm-bar" }, /* @__PURE__ */ React.createElement("span", { className: "hm-bar-fill" + (g.met ? " met" : g.blocked ? " blk" : ""), style: { width: (g.blocked ? 6 : pct) + "%" } })));
    })));
  }
  function AgentsBlock({ block, ctx }) {
    const STATE = { working: "approved", idle: "draft", blocked: "changes", offline: "draft" };
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card" }, /* @__PURE__ */ React.createElement(CardHead, { title: block.title, live: block.live }), /* @__PURE__ */ React.createElement("div", { className: "hm-agents" }, (ctx.agents || []).map((a, i) => /* @__PURE__ */ React.createElement("div", { className: "hm-agent", key: i }, /* @__PURE__ */ React.createElement("span", { className: "hm-agent-av" }, /* @__PURE__ */ React.createElement(Av, { name: a.name, size: 24 }), /* @__PURE__ */ React.createElement("span", { className: "hm-agent-dot sx-sd-" + (STATE[a.state] || "draft") + (a.state === "working" ? " is-live" : "") })), /* @__PURE__ */ React.createElement("span", { className: "hm-agent-name" }, a.name), /* @__PURE__ */ React.createElement("span", { className: "hm-agent-state st-" + (STATE[a.state] || "draft") }, a.state)))));
  }
  function NoteBlock({ block }) {
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card hm-note" }, /* @__PURE__ */ React.createElement(CardHead, { title: block.title }), /* @__PURE__ */ React.createElement("p", { className: "hm-note-body" }, block.body));
  }
  function ActivityBlock({ block, ctx }) {
    const live = !!(ctx && ctx.activity && ctx.activity.length);
    const items = live ? ctx.activity : block.items || [];
    return /* @__PURE__ */ React.createElement("div", { className: "hm-card" }, /* @__PURE__ */ React.createElement(CardHead, { title: block.title, live: live || block.live }), /* @__PURE__ */ React.createElement("ul", { className: "hm-feed" }, items.map((e, i) => /* @__PURE__ */ React.createElement("li", { className: "hm-feed-row", key: i }, /* @__PURE__ */ React.createElement(Av, { name: e.who, size: 20 }), /* @__PURE__ */ React.createElement("span", { className: "hm-feed-text" }, /* @__PURE__ */ React.createElement("b", null, e.who), " ", e.text), /* @__PURE__ */ React.createElement("span", { className: "hm-feed-time mono" }, e.time)))));
  }
  const RENDER = {
    agenda: AgendaBlock,
    pinned: PinnedBlock,
    links: LinksBlock,
    goals: GoalsBlock,
    agents: AgentsBlock,
    note: NoteBlock,
    activity: ActivityBlock
  };
  function HomePage({ ctx }) {
    const cfg = ctx && ctx.home && ctx.home.greeting && ctx.home.banner && Array.isArray(ctx.home.blocks) ? ctx.home : HOME_CONFIG;
    const g = cfg.greeting;
    const agenda = cfg.blocks.find((b) => b.type === "agenda");
    const order = ["pinned", "goals", "agents", "links", "note", "activity"];
    const byType = Object.fromEntries(cfg.blocks.map((b) => [b.type, b]));
    const rest = order.map((t) => byType[t]).filter(Boolean);
    return /* @__PURE__ */ React.createElement("article", { className: "hm" }, /* @__PURE__ */ React.createElement("style", null, HOME_CSS), /* @__PURE__ */ React.createElement("header", { className: "hm-hero" }, /* @__PURE__ */ React.createElement("div", { className: "hm-hero-text" }, /* @__PURE__ */ React.createElement("div", { className: "hm-eyebrow mono" }, g.eyebrow), /* @__PURE__ */ React.createElement("h1", { className: "hm-heading" }, g.heading), /* @__PURE__ */ React.createElement("p", { className: "hm-lede" }, g.note), /* @__PURE__ */ React.createElement("div", { className: "hm-sign" }, /* @__PURE__ */ React.createElement(Av, { name: g.signedBy, size: 22 }), /* @__PURE__ */ React.createElement("span", { className: "hm-sign-by" }, "maintained by ", /* @__PURE__ */ React.createElement("b", null, g.signedBy)), /* @__PURE__ */ React.createElement("span", { className: "hm-sign-dot" }, "\xB7"), /* @__PURE__ */ React.createElement("span", { className: "hm-sign-upd mono" }, "updated ", g.updated, " ago"))), /* @__PURE__ */ React.createElement("div", { className: "hm-hero-banner" }, /* @__PURE__ */ React.createElement(
      "image-slot",
      {
        id: cfg.banner.slotId,
        shape: "rounded",
        radius: "16",
        placeholder: cfg.banner.placeholder,
        style: { width: "100%", height: "100%" }
      }
    ), /* @__PURE__ */ React.createElement("span", { className: "hm-banner-cap mono" }, cfg.banner.caption))), agenda && /* @__PURE__ */ React.createElement(AgendaBlock, { block: agenda, ctx }), /* @__PURE__ */ React.createElement("div", { className: "hm-grid" }, rest.map((b, i) => {
      const C = RENDER[b.type];
      return C ? /* @__PURE__ */ React.createElement(C, { key: i, block: b, ctx }) : null;
    })));
  }
  const HOME_CSS = `
  .hm{
    font-family:var(--font-ui);color:var(--ink);
    height:100%;width:100%;max-width:1480px;margin:0 auto;
    padding:clamp(12px,2.4cqh,24px) clamp(16px,2.2cqw,38px);
    display:grid;grid-template-rows:auto auto minmax(0,1fr);
    gap:clamp(9px,1.7cqh,17px);
    container-type:size;overflow:hidden;
    --gap:clamp(8px,1.5cqh,15px);
    --cpad:clamp(9px,1.6cqh,15px);
    --rowpad:clamp(5px,0.9cqh,9px);
  }
  .hm *{box-sizing:border-box;}
  .hm .mono{font-family:var(--font-mono);}
  .hm-av{display:inline-grid;place-items:center;border-radius:50%;color:#fff;font-weight:600;font-family:var(--font-ui);flex:0 0 auto;letter-spacing:.01em;}

  /* hero (region 1) */
  .hm-hero{min-height:0;display:grid;grid-template-columns:1.7fr 1fr;gap:clamp(16px,2.4cqw,30px);overflow:hidden;}
  .hm-hero-text{display:flex;flex-direction:column;min-width:0;min-height:0;overflow:hidden;}
  .hm-eyebrow{font-size:clamp(10px,1.2cqh,11.5px);letter-spacing:.16em;color:#8b8f96;margin-bottom:clamp(4px,0.9cqh,9px);}
  .hm-heading{font-family:var(--font-ui);font-size:clamp(20px,3.7cqh,34px);line-height:1.1;letter-spacing:-.028em;font-weight:600;color:#16181c;margin:0 0 clamp(5px,1cqh,11px);text-wrap:balance;}
  .hm-lede{font-family:var(--font-ui);font-size:clamp(13px,1.85cqh,16px);line-height:1.5;color:#5b6069;margin:0;max-width:54ch;display:-webkit-box;-webkit-line-clamp:4;-webkit-box-orient:vertical;overflow:hidden;}
  .hm-sign{display:flex;align-items:center;gap:8px;margin-top:auto;padding-top:clamp(6px,1cqh,12px);font-size:clamp(11px,1.4cqh,13px);color:var(--ink-2);}
  .hm-sign-by b{color:var(--ink);font-weight:600;}
  .hm-sign-dot{color:#c4c4c8;}
  .hm-sign-upd{font-size:clamp(10px,1.2cqh,11.5px);color:#8b8f96;}

  .hm-hero-banner{position:relative;min-height:0;min-width:0;border-radius:16px;overflow:hidden;background:#16181d;}
  .hm-hero-banner image-slot{--slot-bg:#16181d;--slot-fg:rgba(255,255,255,.78);--slot-ring:rgba(255,255,255,.28);}
  .hm-banner-cap{position:absolute;left:10px;bottom:9px;max-width:calc(100% - 20px);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;font-size:10.5px;letter-spacing:.04em;color:#fff;background:rgba(20,22,27,.55);backdrop-filter:blur(3px);padding:3px 8px;border-radius:6px;pointer-events:none;}

  /* cards \u2014 shared shell */
  .hm-card{background:#ffffff;border:1px solid #e8e8ea;border-radius:12px;padding:var(--cpad);display:flex;flex-direction:column;min-height:0;overflow:hidden;}
  .hm-card-head{display:flex;align-items:center;gap:8px;margin-bottom:clamp(5px,1cqh,9px);flex:0 0 auto;}
  .hm-card-title{font-size:clamp(10px,1.2cqh,12px);font-weight:700;letter-spacing:.1em;text-transform:uppercase;color:#3a3f47;}
  .hm-card-meta{margin-left:auto;font-family:var(--font-mono);font-size:clamp(9.5px,1.1cqh,11px);color:#9aa0a6;}
  .hm-live{display:inline-flex;align-items:center;gap:5px;font-family:var(--font-mono);font-size:10px;letter-spacing:.08em;text-transform:uppercase;color:var(--brand-strong);}
  .hm-live-dot{width:6px;height:6px;border-radius:50%;background:var(--brand);box-shadow:0 0 0 0 var(--brand-soft);animation:hmpulse 2.4s infinite;}
  @keyframes hmpulse{0%{box-shadow:0 0 0 0 var(--brand-soft);}70%{box-shadow:0 0 0 7px rgba(0,0,0,0);}100%{box-shadow:0 0 0 0 rgba(0,0,0,0);}}
  #app.no-pulse .hm-live-dot,#app.no-pulse .hm-agent-dot.is-live{animation:none;}

  /* agenda \u2014 the prominent "needs you" band (region 2) */
  .hm-agenda{min-height:0;background:#fff;border-color:#e8e8ea;box-shadow:0 1px 2px rgba(20,21,24,.03);}
  .hm-agenda-list{list-style:none;margin:0;padding:0;flex:1;min-height:0;display:flex;flex-direction:column;justify-content:center;}
  .hm-agenda-row{display:flex;align-items:center;gap:clamp(9px,1.4cqw,13px);padding:var(--rowpad) 2px;border-top:1px solid #efeff1;}
  .hm-agenda-row:first-child{border-top:none;}
  .hm-dot{width:8px;height:8px;border-radius:50%;flex:0 0 auto;}
  .hm-agenda-text{flex:1;min-width:0;font-size:clamp(12px,1.65cqh,14.5px);line-height:1.35;color:#23262c;}
  .hm-act{flex:0 0 auto;font-family:var(--font-ui);font-size:clamp(11px,1.35cqh,12.5px);font-weight:600;border-radius:8px;padding:clamp(5px,0.85cqh,7px) clamp(11px,1.5cqw,15px);cursor:pointer;border:1px solid transparent;transition:transform .12s ease;}
  .hm-act:active{transform:translateY(1px);}
  .hm-act.is-approve,.hm-act.is-review,.hm-act.is-reply{background:#1a1c1f;color:#fff;border-color:#1a1c1f;}
  .hm-act.is-approve:hover,.hm-act.is-review:hover,.hm-act.is-reply:hover{background:#000;}

  /* bento grid (region 3) */
  .hm-grid{height:100%;min-height:0;display:grid;grid-template-columns:repeat(3,minmax(0,1fr));grid-template-rows:repeat(2,minmax(0,1fr));gap:var(--gap);overflow:hidden;}

  /* pinned */
  .hm-pin-list{display:flex;flex-direction:column;gap:1px;flex:1;min-height:0;overflow:hidden;}
  .hm-pin{display:flex;align-items:center;gap:10px;width:100%;text-align:left;background:none;border:none;cursor:pointer;padding:var(--rowpad) 7px;border-radius:8px;}
  .hm-pin:hover{background:#f4f4f5;}
  .hm-pin-ic{width:clamp(22px,3cqh,28px);height:clamp(22px,3cqh,28px);border-radius:7px;background:#f0f0f1;display:grid;place-items:center;font-size:13px;color:#8b8f96;flex:0 0 auto;}  .hm-pin-main{flex:1;min-width:0;display:flex;flex-direction:column;gap:1px;}
  .hm-pin-name{font-size:clamp(12px,1.55cqh,14px);font-weight:600;color:#23262c;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
  .hm-pin-meta{font-size:clamp(10px,1.2cqh,11.5px);color:#9aa0a6;}
  .hm-pin .sx-sd{width:9px;height:9px;border-radius:50%;flex:0 0 auto;}

  /* links */
  .hm-links{display:flex;flex-direction:column;gap:0;flex:1;min-height:0;overflow:hidden;}
  .hm-link{display:flex;align-items:center;gap:10px;text-decoration:none;padding:var(--rowpad) 7px;border-radius:8px;color:#23262c;}
  .hm-link:hover{background:#f4f4f5;}
  .hm-link-label{font-size:clamp(12px,1.55cqh,14px);font-weight:500;}
  .hm-link-meta{margin-left:auto;font-size:10.5px;letter-spacing:.04em;text-transform:uppercase;color:#a0a4ab;}
  .hm-link-arrow{color:#c4c4c8;font-size:13px;}
  .hm-link:hover .hm-link-arrow{color:var(--brand-strong);}

  /* goals */
  .hm-goals{display:flex;flex-direction:column;gap:clamp(7px,1.5cqh,14px);flex:1;min-height:0;justify-content:center;overflow:hidden;}
  .hm-goal-top{display:flex;align-items:baseline;justify-content:space-between;gap:10px;margin-bottom:clamp(4px,0.8cqh,7px);}
  .hm-goal-label{font-size:clamp(11px,1.4cqh,13px);color:#3a3f47;}
  .hm-goal-val{font-family:var(--font-mono);font-size:clamp(11px,1.3cqh,12.5px);font-weight:600;color:#3a3f47;}
  .hm-goal-val.met{color:#3f8f59;} .hm-goal-val.blk{color:#c0573b;}
  .hm-bar{height:6px;border-radius:4px;background:#ebebed;overflow:hidden;}
  .hm-bar-fill{display:block;height:100%;border-radius:4px;background:#26282c;}
  .hm-bar-fill.met{background:#26282c;} .hm-bar-fill.blk{background:#d2674a;}

  /* agents */
  .hm-agents{display:flex;flex-direction:column;gap:clamp(2px,0.5cqh,5px);flex:1;min-height:0;overflow:hidden;}
  .hm-agent{display:flex;align-items:center;gap:10px;padding:var(--rowpad) 2px;}
  .hm-agent-av{position:relative;flex:0 0 auto;display:inline-flex;}
  .hm-agent-dot{position:absolute;right:-2px;bottom:-2px;width:9px;height:9px;border-radius:50%;border:2px solid #ffffff;}
  .hm-agent-name{font-size:clamp(11.5px,1.45cqh,13.5px);color:#23262c;}
  .hm-agent-state{margin-left:auto;font-family:var(--font-mono);font-size:clamp(9.5px,1.1cqh,10.5px);letter-spacing:.04em;text-transform:uppercase;padding:2px 8px;border-radius:20px;}
  .hm-agent-state.st-approved{color:#3f8f59;background:rgba(87,176,111,.14);}
  .hm-agent-state.st-review{color:#b9842a;background:rgba(216,162,63,.16);}
  .hm-agent-state.st-changes{color:#c0573b;background:rgba(192,87,59,.13);}
  .hm-agent-state.st-draft{color:#8b8f96;background:rgba(28,31,36,.06);}

  /* note */
  .hm-note{background:#fff;border-color:#e8e8ea;}
  .hm-note-body{font-family:var(--font-ui);font-size:clamp(12px,1.6cqh,14.5px);line-height:1.55;color:#5b6069;margin:0;flex:1;min-height:0;overflow:hidden;display:-webkit-box;-webkit-line-clamp:7;-webkit-box-orient:vertical;}

  /* feed */
  .hm-feed{list-style:none;margin:0;padding:0;flex:1;min-height:0;display:flex;flex-direction:column;overflow:hidden;}
  .hm-feed-row{display:flex;align-items:center;gap:9px;padding:var(--rowpad) 0;border-top:1px solid #efeff1;}
  .hm-feed-row:first-child{border-top:none;}
  .hm-feed-text{flex:1;min-width:0;font-size:clamp(11px,1.4cqh,13px);color:#41454d;line-height:1.3;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
  .hm-feed-text b{color:#23262c;font-weight:600;}
  .hm-feed-time{font-size:10.5px;color:#a0a4ab;flex:0 0 auto;}

  /* status dot colors (shared tokens) */
  .hm .sx-sd-review,.hm .hm-dot.sx-sd-review{background:var(--c-review);}
  .hm .sx-sd-approved,.hm .hm-dot.sx-sd-approved{background:var(--c-approved);}
  .hm .sx-sd-changes,.hm .hm-dot.sx-sd-changes{background:var(--c-changes);}
  .hm .sx-sd-draft,.hm .hm-dot.sx-sd-draft{background:var(--c-draft);}
  /* needs-you agenda dots read as actionable (violet) */
  .hm-agenda .hm-dot.sx-sd-review{background:#7c6df0;}

  /* dynamic fallbacks for short / narrow stages */
  @container (max-height:680px){
    .hm-lede{-webkit-line-clamp:2;}
    .hm-note-body{-webkit-line-clamp:4;}
  }
  @container (max-width:880px){
    .hm-grid{grid-template-columns:repeat(2,1fr);grid-template-rows:repeat(3,1fr);}
  }
  @container (max-width:560px){
    .hm{grid-template-rows:auto auto 1fr;overflow:auto;}
    .hm-hero{height:auto;grid-template-columns:1fr;}
    .hm-hero-banner{display:none;}
    .hm-agenda{height:auto;}
    .hm-grid{height:auto;grid-template-columns:1fr;grid-template-rows:none;overflow:visible;}
    .hm-card{overflow:visible;min-height:120px;}
  }
  `;
  window.HomePage = HomePage;
  window.HOME_CONFIG = HOME_CONFIG;
})();
