/* home.jsx — Sextant Home: the v0.5 "morning unblock board" (flow2 HomeInbox).
   Wired to REAL primitives (stage b, TASK Track 1):
     - greeting/state line: the curated `home` artifact (ctx.home) when present,
       else a time-of-day default;
     - "Start here" HERO: the single highest-priority review-pending artifact
       (review-state "review" first, then "changes") as a routing-slip card;
     - "Then · N more" queue: the remaining review-pending artifacts;
     - "Goals · N need you" box: a calm summary of ctx.goals (the goal primitive,
       ADR-0035) — a needs-you count + up to 3 needs-attention goals as rows;
     - collapsed "moved overnight" line: a calm count of recently-approved artifacts;
     - the curated home's pinned / quick-links fold in below as secondary panels.
   Exports HomePage(ctx) to window. */
(function () {
  const { useState } = React;

  /* ---------- small helpers (avatar) ---------- */
  const PALETTE = ["#6a55e0", "#e0a23a", "#d2674a", "#3a93d2", "#54ad6e", "#c060a8", "#2bb6a6"];
  function hueOf(name) { let h = 0; for (const c of name || "") h = (h * 31 + c.charCodeAt(0)) >>> 0; return PALETTE[h % PALETTE.length]; }
  function initials(name) {
    const parts = (name || "").replace(/[-@#]/g, " ").split(/[\s_]+/).filter(Boolean);
    return ((parts[0] ? parts[0][0] : "?") + (parts[1] ? parts[1][0] : "")).toUpperCase();
  }
  function Av({ name, size = 22, square }) {
    return <span className={"sx-av" + (square ? " is-agent" : "")} style={{ width: size, height: size, background: hueOf(name), fontSize: size * 0.42 }}>{initials(name)}</span>;
  }

  /* the postmark seal in the hero — reuses the sidebar's brand glyph */
  function Seal() {
    const Glyph = window.SextantGlyph;
    return (
      <svg className="fx-seal" viewBox="0 0 216 152" aria-hidden="true">
        <defs>
          <path id="hmsealTop" d="M78,78 m-58,0 a58,58 0 1,1 116,0" />
          <path id="hmsealBot" d="M78,78 m-52,0 a52,52 0 1,0 104,0" />
        </defs>
        <circle cx="78" cy="78" r="70" fill="none" stroke="currentColor" strokeWidth="1.5" />
        <text className="fx-seal-top"><textPath href="#hmsealTop" startOffset="50%" textAnchor="middle">REVIEW    REQUESTED</textPath></text>
        <text className="fx-seal-bot"><textPath href="#hmsealBot" startOffset="50%" textAnchor="middle">SEXTANT</textPath></text>
        <text className="fx-seal-spark" x="22" y="82" textAnchor="middle">✦</text>
        <text className="fx-seal-spark" x="134" y="82" textAnchor="middle">✦</text>
        {Glyph && <g transform="translate(25 29) scale(0.2)"><Glyph ink="currentColor" /></g>}
        <g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round">
          <path d="M150 60 q11 -6 22 0 t22 0" />
          <path d="M147 72 q12 -6 24 0 t24 0 t20 0" />
          <path d="M147 86 q12 6 24 0 t24 0 t20 0" />
          <path d="M152 98 q11 -6 22 0 t22 0" />
        </g>
      </svg>);
  }

  /* review-state → presentation. "review" is the prominent waiting tone; "changes"
     is a re-review. Both are accented violet/red on the queue's left rail. */
  const STATE_LABEL = { review: "Needs review", changes: "Re-review" };
  const STATE_TONE = { review: "var(--asst)", changes: "var(--wait)" };

  /* the goal verdict — a tiny mirror of goals.jsx's roll() rule, just enough for
     the Home summary (the full Portfolio/Detail lives in goals.jsx). Goal status
     is DERIVED from the criteria rollup; no stored field. */
  function goalRoll(g) {
    const crits = (g && g.criteria) || [];
    const met = crits.filter((c) => c.status === "met").length;
    const waiting = crits.filter((c) => c.status === "waiting-on-you").length;
    const blocked = crits.some((c) => c.status === "blocked");
    const undef = !g || !g.northstar || crits.length === 0;
    let verdict, tone;
    if (undef) { verdict = "Not yet defined"; tone = "t-waiting"; }
    else if (waiting) { verdict = waiting + (waiting > 1 ? " criteria" : " criterion") + " waiting on you"; tone = "t-waiting"; }
    else if (met === crits.length) { verdict = "Done"; tone = "t-met"; }
    else if (blocked) { verdict = "Blocked"; tone = "t-blocked"; }
    else { verdict = "On track"; tone = "t-met"; }
    return { verdict, tone, needsYou: undef || waiting > 0 || blocked };
  }

  /* ---------- the hero — flow2 routing-slip card ---------- */
  function Hero({ a, onOpen }) {
    const authorName = (a.author && a.author.name) || "an agent";
    const tone = STATE_TONE[a.status] || "var(--asst)";
    return (
      <button className="fx-dhero fx-in" style={{ animationDelay: ".06s" }} onClick={() => onOpen(a.name)}>
        <span className="fx-dhero-rail" style={{ background: tone }}><span className="fx-dhero-rail-plus">+</span></span>
        <div className="fx-dhero-body">
          <Seal />
          <div className="fx-dhero-tline">
            <span className="fx-dhero-badge">{STATE_LABEL[a.status] || "Needs review"}</span>
            {a.updated && <span className="fx-dhero-tmeta">updated {a.updated} ago</span>}
            <span className="fx-dhero-tmeta">rev {a.version}</span>
          </div>
          <div className="fx-dhero-title">{a.name}</div>
          <div className="fx-dhero-sum">{a.status === "changes" ? "You asked for changes — the agent revised it. Re-review when you're ready." : "Waiting on your read before it can move."}</div>
          <div className="fx-dhero-unb">→ {authorName} is waiting on <b>your verdict</b></div>
          <div className="fx-dhero-foot">{(a.author && a.author.name) && <Av name={authorName} size={20} square />}<span className="fx-meta">{authorName}{a.updated ? " · " + a.updated + " ago" : ""}</span></div>
        </div>
        <div className="fx-dhero-route">
          <div><div className="fx-dhero-rlbl">Routing</div><div className="fx-dhero-rval is-pri">Highest priority <span className="spark">✦</span></div></div>
          <div><div className="fx-dhero-rlbl">State</div><div className="fx-dhero-rval">{STATE_LABEL[a.status] || "Needs review"}</div></div>
          <div><div className="fx-dhero-rlbl">From</div><div className="fx-dhero-rval">{authorName}</div></div>
          <div><div className="fx-dhero-rlbl">Revision</div><div className="fx-dhero-rval">rev {a.version}</div></div>
          <span className="fx-dhero-open">Open review →</span>
        </div>
      </button>);
  }

  /* ---------- D7: violet's curated agenda ("Needs you") ----------
     Each item is { action, ref, text, tone }. `text` is violet's per-item
     rationale (why you're seeing this) — the prominent line. `ref` is what to
     open: a goal.<id> routes to the Goals view, anything else opens the artifact.
     `tone` splits presentation: "context" is a calm one-liner (an at-a-glance
     status, e.g. "all clear"); a CALL tone (review/call/…) is a real card that
     needs the operator. Wikilinks in `text` render as plain text for v1 (there's
     no trivially-reusable inline wikilink renderer on window — MarkdownArtifact is
     a full-document component). */

  // route an agenda ref to the right open handler. A goal.<id> ref opens the
  // Goals view (goals aren't artifacts in the documents list); everything else
  // opens the named artifact. Empty ref ⇒ no-op (the row stays inert).
  function openAgendaRef(ref, ctx) {
    if (!ref || typeof ref !== "string") return;
    if (ref.indexOf("goal.") === 0) { ctx.onNav && ctx.onNav("goals"); }
    else { ctx.onOpenArtifact && ctx.onOpenArtifact(ref); }
  }

  // a "context" item: a calm single status line (mirrors the "all caught up"
  // zero-state tone). Clickable only when it carries a ref.
  function AgendaContextRow({ item, ctx }) {
    const ref = item.ref;
    const open = () => openAgendaRef(ref, ctx);
    return (
      <div className={"fx-agenda-ctx fx-in" + (ref ? " is-link" : "")} onClick={ref ? open : undefined} role={ref ? "button" : undefined}>
        <span className="fx-agenda-ctx-ic">✓</span>
        <span className="fx-agenda-ctx-txt">{item.text || "All clear — nothing needs you right now."}</span>
        {ref && <span className="fx-agenda-ctx-chev">›</span>}
      </div>);
  }

  // a CALL item: a real-call card. violet's rationale (`text`) leads; the ref's
  // open affordance sits on the right. Adapts the Hero's accented-rail card.
  function AgendaCall({ item, n, ctx }) {
    const ref = item.ref || "";
    const isGoal = ref.indexOf("goal.") === 0;
    const tone = STATE_TONE[item.tone] || "var(--asst)";
    const open = () => openAgendaRef(ref, ctx);
    return (
      <button className="fx-agenda-call fx-in" style={{ "--tn": tone, animationDelay: (0.06 + n * 0.03) + "s" }} onClick={open} disabled={!ref}>
        <span className="fx-agenda-rail" style={{ background: tone }} />
        <span className="fx-agenda-call-body">
          <span className="fx-agenda-call-kicker" style={{ color: tone }}>Needs you</span>
          <span className="fx-agenda-call-text">{item.text || ""}</span>
          {ref && <span className="fx-agenda-call-ref">{isGoal ? "◎ " + ref.replace(/^goal\./, "") : "❡ " + ref}</span>}
        </span>
        {ref && <span className="fx-agenda-call-open">{isGoal ? "Open goal →" : "Open →"}</span>}
      </button>);
  }

  // the agenda block as the "Needs you" list (replaces the auto-derived hero).
  // Calls render first (the work), context lines after (the reassurance).
  function AgendaList({ block, items, ctx }) {
    const title = (block && block.title) || "Needs you";
    const calls = items.filter((it) => it.tone !== "context");
    const ctxs = items.filter((it) => it.tone === "context");
    return (
      <React.Fragment>
        <div className="fx-starthead fx-in" style={{ animationDelay: ".06s" }}>{title}</div>
        <div className="fx-agenda">
          {calls.map((it, i) => <AgendaCall item={it} n={i} ctx={ctx} key={"call-" + i} />)}
          {ctxs.map((it, i) => <AgendaContextRow item={it} ctx={ctx} key={"ctx-" + i} />)}
        </div>
      </React.Fragment>);
  }

  /* ---------- a queue row ---------- */
  function QRow({ a, n, onOpen }) {
    const authorName = (a.author && a.author.name) || "agent";
    const tone = STATE_TONE[a.status] || "var(--todo)";
    return (
      <button className="fx-then-row" style={{ "--tn": tone }} onClick={() => onOpen(a.name)}>
        <span className="fx-then-idx">
          <span className="fx-then-num">{String(n).padStart(2, "0")}</span>
          <span className="fx-then-glyph" style={{ color: tone }}>●</span>
        </span>
        <span className="fx-then-main">
          <span className="fx-then-title">{a.name}</span>
          <span className="fx-then-sub">
            <span className="fx-tbadge" style={{ color: tone, borderColor: tone, background: "rgba(120,124,132,.08)" }}>{STATE_LABEL[a.status] || "Needs review"}</span>
            <span className="fx-then-meta">rev {a.version}</span>
            {a.updated && <span className="fx-then-unb">· updated {a.updated} ago</span>}
          </span>
        </span>
        <span className="fx-then-right">
          <span className="fx-then-age">{(a.author && a.author.name) && <Av name={authorName} size={18} square />}{a.updated || ""}</span>
          <span className="fx-then-chev">›</span>
        </span>
      </button>);
  }

  /* ---------- the Home Goals summary ---------- */
  // A quiet, prose-first box: "N need you" headline, then up to 3 needs-attention
  // goals as compact rows (north-star or name + verdict chip). The full Portfolio
  // lives in goals.jsx; this is the morning glance. Empty ⇒ a single calm line.
  function GoalsBox({ goals, onNav }) {
    if (!goals.length) {
      return (
        <div className="fx-goalsbox fx-in" style={{ animationDelay: ".16s" }}>
          <div className="fx-goalsum-empty">No goals yet.</div>
        </div>);
    }
    const rolled = goals.map((g) => ({ g, r: goalRoll(g) }));
    const needs = rolled.filter((x) => x.r.needsYou);
    const head = needs.length
      ? needs.length + (needs.length === 1 ? " goal needs you" : " goals need you")
      : "All " + goals.length + (goals.length === 1 ? " goal is" : " goals are") + " moving on their own";
    const rows = (needs.length ? needs : rolled).slice(0, 3);
    return (
      <div className="fx-goalsbox fx-in" style={{ animationDelay: ".16s" }}>
        <div className="fx-goalsum-head">
          <span className={"fx-goalsum-dot " + (needs.length ? "is-need" : "is-calm")} />
          {head}
        </div>
        {rows.map(({ g, r }) => (
          <button className="fx-goalsum-row" key={g.id} onClick={() => onNav && onNav("goals")}>
            <span className="fx-goalsum-name">{g.northstar || g.name}</span>
            <span className={"fx-goalsum-verdict " + r.tone}>{r.verdict}</span>
          </button>
        ))}
      </div>);
  }

  /* ---------- the page ---------- */
  function HomePage({ ctx }) {
    const [movedOpen, setMovedOpen] = useState(false);
    const arts = (ctx && ctx.artifacts) || [];

    // the operator's inbox: artifacts explicitly flagged needs-review (review-state),
    // most-recently-touched first. "changes" is the operator's verdict sent back to the
    // agent (their turn) — it re-enters the inbox only when the agent revises + re-flags
    // review, per the needs-review convention (default-neutral; inbox = state==="review").
    const pending = arts.filter((a) => a.status === "review")
      .sort((a, b) => (b.version || 0) - (a.version || 0));
    const hero = pending[0];
    const rest = pending.slice(1);
    const total = pending.length;

    const approved = arts.filter((a) => a.status === "approved").length;

    // greeting: the curated `home` artifact when it carries one, else time-of-day.
    const g = ctx && ctx.home && ctx.home.greeting;
    const hr = new Date().getHours();
    const defGreet = hr < 12 ? "Good morning." : hr < 18 ? "Good afternoon." : "Good evening.";
    const heading = (g && g.heading) || defGreet;
    const stateLine = (g && g.note) ? g.note
      : total === 0 ? "Inbox zero — nothing needs your review right now."
      : total + (total === 1 ? " thing needs you" : " things need you") + " · everything else is moving on its own";

    // secondary curated panels (pinned + quick-links) fold in below when present.
    const home = (ctx && ctx.home) || {};
    const blocks = Array.isArray(home.blocks) ? home.blocks : [];
    const pinnedBlock = blocks.find((b) => b.type === "pinned");
    const linksBlock = blocks.find((b) => b.type === "links");

    // D7: violet's curated agenda (the "Needs you" block of the `home` artifact).
    // When present with items it REPLACES the auto-derived review-state hero — each
    // item carries violet's per-item rationale (why you're seeing this) + a ref to
    // open. When absent we fall back to the auto-derived hero (violet isn't always
    // live, so the fallback must stay intact). An agenda block with no items is
    // treated as absent (nothing curated to show) → fall back.
    const agendaBlock = blocks.find((b) => b.type === "agenda");
    const agendaItems = (agendaBlock && Array.isArray(agendaBlock.items))
      ? agendaBlock.items.filter((it) => it && typeof it === "object" && (typeof it.text === "string" || (typeof it.ref === "string" && it.ref)))
      : [];
    const hasAgenda = agendaItems.length > 0;

    return (
      <article className="fx-scroll"><div className="fx-col fx-col--home sx-conv-light">
        <style>{HOME_CSS}</style>

        <h1 className="fx-h1 fx-in">{heading}</h1>
        <p className="fx-psub fx-in" style={{ animationDelay: ".03s" }}>{stateLine}</p>

        {hasAgenda ? (
          <AgendaList block={agendaBlock} items={agendaItems} ctx={ctx} />
        ) : hero ? (
          <React.Fragment>
            <div className="fx-starthead fx-in" style={{ animationDelay: ".06s" }}>Start here</div>
            <Hero a={hero} onOpen={ctx.onOpenArtifact} />
          </React.Fragment>
        ) : (
          <div className="fx-zero fx-in">
            <span className="fx-zero-ic">✓</span>
            <div>
              <div className="fx-zero-title">You're all caught up.</div>
              <div className="fx-zero-sub">Agents keep working. The next thing that needs your judgement will appear here.</div>
            </div>
          </div>
        )}

        {rest.length > 0 && (
          <React.Fragment>
            <div className="fx-sechead fx-in" style={{ animationDelay: ".1s" }}>
              <span className="fx-grouplbl">Then · {rest.length} more</span>
              <button className="fx-seclink" onClick={() => ctx.onNav && ctx.onNav("artifacts")}>All artifacts →</button>
            </div>
            <div className="fx-then-list">
              {rest.map((a, i) => <QRow a={a} n={i + 1} onOpen={ctx.onOpenArtifact} key={a.name} />)}
            </div>
          </React.Fragment>
        )}

        {/* Goals · a calm summary of the goal primitive (ADR-0035), read from ctx.goals */}
        <div className="fx-sechead fx-in" style={{ animationDelay: ".14s" }}>
          <span className="fx-grouplbl">Goals</span>
          <button className="fx-seclink" onClick={() => ctx.onNav && ctx.onNav("goals")}>All goals →</button>
        </div>
        <GoalsBox goals={(ctx && ctx.goals) || []} onNav={ctx.onNav} />

        {/* secondary: curated pinned + quick links (assistant-owned `home` artifact) */}
        {(pinnedBlock || linksBlock) && (
          <div className="hm-secondary fx-in" style={{ animationDelay: ".18s" }}>
            {pinnedBlock && <PinnedPanel block={pinnedBlock} ctx={ctx} />}
            {linksBlock && <LinksPanel block={linksBlock} />}
          </div>
        )}

        {/* "moved overnight" — calm reassurance */}
        {approved > 0 && (
          <div className="fx-moved fx-in" style={{ animationDelay: ".2s" }}>
            <button className="fx-moved-bar" onClick={() => setMovedOpen((o) => !o)}>
              <span className="fx-moved-dot" />{approved} {approved === 1 ? "artifact" : "artifacts"} approved — settled, nothing needs you
              <span className="fx-moved-go">{movedOpen ? "hide" : "show"}</span>
            </button>
            {movedOpen && (
              <div className="fx-moved-list">
                {arts.filter((a) => a.status === "approved").slice(0, 8).map((a) => (
                  <div className="fx-moved-item" key={a.name}>
                    <span className="fx-moved-icheck">✓</span>
                    <span>{a.name} — <span style={{ color: "var(--fx-ink3)" }}>approved · rev {a.version}</span></span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div></article>);
  }

  /* ---------- secondary curated panels ---------- */
  const ARTICON = { markdown: "❡", sheet: "▦", default: "◆" };
  function PinnedPanel({ block, ctx }) {
    const picked = (block.names || []).map((n) => (ctx.artifacts || []).find((a) => a.name === n)).filter(Boolean);
    if (!picked.length) return null;
    return (
      <div className="hm-panel">
        <div className="hm-panel-h">Pinned</div>
        <div className="fx-list">
          {picked.map((a) => (
            <button className="fx-row" key={a.name} onClick={() => ctx.onOpenArtifact(a.name)}>
              <span className="fx-row-ic">{ARTICON[a.type] || ARTICON.default}</span>
              <span className="fx-row-main">
                <span className="fx-row-name">{a.name}</span>
                <span className="fx-row-meta">{a.type} · rev {a.version}</span>
              </span>
            </button>
          ))}
        </div>
      </div>);
  }
  function LinksPanel({ block }) {
    const items = block.items || [];
    if (!items.length) return null;
    return (
      <div className="hm-panel">
        <div className="hm-panel-h">Quick links</div>
        <div className="hm-links">
          {items.map((l, i) => (
            <a className="hm-link" key={i} href={l.href || "#"} target="_blank" rel="noreferrer">
              <span className="hm-link-label">{l.label}</span>
              <span className="hm-link-meta">{l.meta}</span>
              <span className="hm-link-arrow">↗</span>
            </a>
          ))}
        </div>
      </div>);
  }

  const HOME_CSS = `
  .fx-goalsum-empty{padding:18px 18px;font-size:13.5px;color:var(--fx-ink2);}
  .fx-goalsum-head{display:flex;align-items:center;gap:9px;padding:15px 18px 13px;font-size:14px;font-weight:600;color:var(--ink);border-bottom:1px solid var(--fx-line);}
  .fx-goalsum-dot{width:8px;height:8px;border-radius:50%;flex:0 0 auto;}
  .fx-goalsum-dot.is-need{background:var(--wait);}
  .fx-goalsum-dot.is-calm{background:var(--met);}
  .fx-goalsum-row{display:flex;align-items:center;gap:12px;width:100%;text-align:left;background:none;border:none;border-top:1px solid var(--fx-line);padding:13px 18px;cursor:pointer;color:var(--ink);}
  .fx-goalsum-row:first-of-type{border-top:none;}
  .fx-goalsum-row:hover{background:rgba(20,21,24,.025);}
  .fx-goalsum-name{flex:1;min-width:0;font-family:'Newsreader',Georgia,serif;font-size:15px;line-height:1.35;color:var(--fx-ink);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
  .fx-goalsum-verdict{flex:0 0 auto;font-family:var(--font-mono);font-size:9.5px;font-weight:600;letter-spacing:.03em;text-transform:uppercase;padding:3px 9px;border-radius:20px;white-space:nowrap;}
  #app.dark .fx-goalsum-head,#app.dark .fx-goalsum-row{border-color:#2a2d33;}
  #app.dark .fx-goalsum-row:hover{background:rgba(255,255,255,.03);}
  .hm-secondary{display:grid;grid-template-columns:1fr 1fr;gap:18px;margin-top:30px;}
  @container (max-width:760px){ .hm-secondary{grid-template-columns:1fr;} }
  .hm-panel{border:1px solid var(--fx-line);border-radius:13px;padding:8px 16px 12px;}
  .hm-panel-h{font-family:var(--font-mono);font-size:10.5px;letter-spacing:.12em;text-transform:uppercase;color:var(--fx-ink3);padding:10px 6px 6px;}
  .hm-links{display:flex;flex-direction:column;}
  .hm-link{display:flex;align-items:center;gap:10px;text-decoration:none;padding:11px 6px;border-top:1px solid var(--fx-line);color:var(--ink);}
  .hm-link:first-child{border-top:none;}
  .hm-link:hover{background:rgba(20,21,24,.025);}
  .hm-link-label{font-size:14px;font-weight:500;}
  .hm-link-meta{margin-left:auto;font-family:var(--font-mono);font-size:10px;letter-spacing:.04em;text-transform:uppercase;color:var(--fx-ink3);}
  .hm-link-arrow{color:#c4c4cc;font-size:13px;}
  #app.dark .hm-panel{border-color:#2a2d33;}
  #app.dark .hm-link{border-top-color:#2a2d33;}
  #app.dark .hm-link:hover{background:rgba(255,255,255,.03);}
  /* D7 · violet's curated agenda ("Needs you"): a real-call card (accent rail +
     rationale + open) and a calm context line. Adapts the Hero / zero-state look. */
  .fx-agenda{display:flex;flex-direction:column;gap:11px;margin-top:8px;}
  .fx-agenda-call{position:relative;display:flex;align-items:stretch;width:100%;text-align:left;background:#fff;border:1px solid #e2e2e8;border-radius:14px;overflow:hidden;cursor:pointer;box-shadow:0 10px 28px -20px rgba(20,21,30,.4);transition:box-shadow .14s,transform .14s,border-color .14s;color:var(--ink);}
  .fx-agenda-call:hover{box-shadow:0 16px 38px -20px rgba(20,21,30,.5);border-color:#d4d4dc;transform:translateY(-1px);}
  .fx-agenda-call:disabled{cursor:default;box-shadow:none;transform:none;}
  .fx-agenda-rail{width:5px;flex:0 0 5px;}
  .fx-agenda-call-body{flex:1;min-width:0;padding:16px 18px;display:flex;flex-direction:column;gap:7px;}
  .fx-agenda-call-kicker{font-family:var(--font-mono);font-size:9px;font-weight:600;letter-spacing:.09em;text-transform:uppercase;}
  .fx-agenda-call-text{font-size:15px;line-height:1.45;color:var(--ink);font-weight:500;}
  .fx-agenda-call-ref{font-family:var(--font-mono);font-size:10.5px;letter-spacing:.03em;color:var(--fx-ink3);}
  .fx-agenda-call-open{align-self:center;flex:0 0 auto;padding:0 18px;font-size:13px;font-weight:600;color:var(--asst);white-space:nowrap;}
  .fx-agenda-ctx{display:flex;align-items:center;gap:11px;padding:13px 16px;border:1px solid var(--fx-line);border-radius:12px;background:rgba(63,143,89,.03);color:var(--fx-ink2);}
  .fx-agenda-ctx.is-link{cursor:pointer;}
  .fx-agenda-ctx.is-link:hover{background:rgba(63,143,89,.06);}
  .fx-agenda-ctx-ic{width:24px;height:24px;border-radius:50%;background:rgba(63,143,89,.13);color:var(--met);display:grid;place-items:center;font-size:13px;flex:0 0 auto;}
  .fx-agenda-ctx-txt{flex:1;min-width:0;font-size:13.5px;line-height:1.4;}
  .fx-agenda-ctx-chev{color:#c4c4cc;font-size:15px;flex:0 0 auto;}
  #app.dark .fx-agenda-call{background:#1a1d23;border-color:#2a2d33;}
  #app.dark .fx-agenda-call:hover{border-color:#3a3f47;}
  #app.dark .fx-agenda-ctx{border-color:#2a2d33;background:rgba(63,143,89,.06);}
  `;

  window.HomePage = HomePage;
})();
