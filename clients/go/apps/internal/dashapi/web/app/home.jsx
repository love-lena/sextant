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

  /* ---------- small helpers (avatar) ----------
     No-personas (TASK-194): an artifact's author is a run/agent, surfaced by ULID +
     function in the byline text, not a persona avatar. Av here is only ever called
     with square (the author chip), so it renders the NEUTRAL function glyph (the
     same ⬡ chip the shared sidebar Avatar uses for kind="agent") — no name-hashed
     colour, no initials. */
  function Av({ name, size = 22, square }) {
    if (square) return <span className="sx-av is-agent is-run" style={{ width: size, height: size, fontSize: size * 0.5 }} aria-hidden="true">⬡</span>;
    return <span className="sx-av" style={{ width: size, height: size, fontSize: size * 0.42 }}>{(name || "?")[0]}</span>;
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
    const inprog = crits.filter((c) => c.status === "in-progress").length;
    const undef = !g || !g.northstar || crits.length === 0;
    const signoff = !!g && g.review === "review"; // awaiting the operator's sign-off (TASK-157)
    let verdict, tone;
    if (undef) { verdict = "Not yet defined"; tone = "t-waiting"; }
    else if (signoff) { verdict = "Awaiting your sign-off"; tone = "t-waiting"; }
    else if (waiting) { verdict = waiting + (waiting > 1 ? " criteria" : " criterion") + " waiting on you"; tone = "t-waiting"; }
    else if (met === crits.length) { verdict = "Done"; tone = "t-met"; }
    else if (blocked) { verdict = "Blocked"; tone = "t-blocked"; }
    else { verdict = "On track"; tone = "t-met"; }
    return { verdict, tone, met, total: crits.length, inprog, needsYou: undef || signoff || waiting > 0 || blocked };
  }

  // status → the segment fill colour (mirrors goals.jsx segVar).
  function segVar(s) {
    return s === "met" ? "met" : s === "in-progress" ? "prog"
      : s === "waiting-on-you" ? "wait" : s === "blocked" ? "blk" : "todo";
  }

  // briefRank: S3.3 urgency order over review-pending items. A "changes" re-review
  // is the most urgent (you already engaged it once); then a fresh review (a
  // decision). No richer brief-type primitive exists, so type/read-effort are
  // derived heuristics (see briefMeta) — honest until a brief lexicon lands.
  function briefRank(a) {
    if (a.status === "changes") return 0; // change / re-review
    return 1; // decision (a fresh needs-review)
  }
  // briefMeta: a type badge + a coarse read-effort, derived from what we have (the
  // review state + name). Degrades to neutral labels; replace when a brief
  // primitive carries real type/effort.
  function briefMeta(a) {
    const type = a.isGoal ? "Goal" : a.status === "changes" ? "Re-review" : "Decision";
    const glyph = a.isGoal ? "◎" : a.status === "changes" ? "↻" : "✦";
    return { type, glyph, effort: "quick read" };
  }

  /* ---------- the hero — flow2 routing-slip card ----------
     Renders either a review-pending artifact or (TASK-157) a review-flagged goal
     awaiting the operator's sign-off. The goal branch swaps the artifact-specific
     copy (rev / "your verdict" / "Open review") for goal copy and routes to the
     Goals view; everything else is shared. */
  function Hero({ a, onOpen }) {
    const isGoal = !!a.isGoal;
    const authorName = (a.author && a.author.name) || "an agent";
    const tone = STATE_TONE[a.status] || "var(--asst)";
    const badge = isGoal ? "Needs your sign-off" : (STATE_LABEL[a.status] || "Needs review");
    return (
      <button className="fx-dhero fx-in" style={{ animationDelay: ".06s" }} onClick={() => onOpen(a)}>
        <span className="fx-dhero-rail" style={{ background: tone }}><span className="fx-dhero-rail-plus">+</span></span>
        <div className="fx-dhero-body">
          <Seal />
          <div className="fx-dhero-tline">
            <span className="fx-dhero-badge">{badge}</span>
            {a.updated && <span className="fx-dhero-tmeta">updated {a.updated} ago</span>}
            {isGoal ? <span className="fx-dhero-tmeta">goal</span> : <span className="fx-dhero-tmeta">rev {a.version}</span>}
          </div>
          <div className="fx-dhero-title">{a.name}</div>
          <div className="fx-dhero-sum">{isGoal ? "A goal is waiting on your sign-off — review its criteria, then approve or ask for changes." : a.status === "changes" ? "You asked for changes — the agent revised it. Re-review when you're ready." : "Waiting on your read before it can move."}</div>
          <div className="fx-dhero-unb">{isGoal ? <>→ this goal needs <b>your sign-off</b></> : <>→ {authorName} is waiting on <b>your verdict</b></>}</div>
          <div className="fx-dhero-foot">{(a.author && a.author.name) && <Av name={authorName} size={20} square />}<span className="fx-meta">{authorName}{a.updated ? " · " + a.updated + " ago" : ""}</span></div>
        </div>
        <div className="fx-dhero-route">
          <div><div className="fx-dhero-rlbl">Routing</div><div className="fx-dhero-rval is-pri">Highest priority <span className="spark">✦</span></div></div>
          <div><div className="fx-dhero-rlbl">State</div><div className="fx-dhero-rval">{badge}</div></div>
          <div><div className="fx-dhero-rlbl">From</div><div className="fx-dhero-rval">{authorName}</div></div>
          {isGoal
            ? <div><div className="fx-dhero-rlbl">Type</div><div className="fx-dhero-rval">Goal</div></div>
            : <div><div className="fx-dhero-rlbl">Revision</div><div className="fx-dhero-rval">rev {a.version}</div></div>}
          <span className="fx-dhero-open">{isGoal ? "Open goal →" : "Open review →"}</span>
        </div>
      </button>);
  }

  /* ---------- D7: violet's curated agenda ("Needs you") ----------
     Each item is { action, ref, text, tone }. `text` is violet's per-item
     rationale (why you're seeing this) — the prominent line. `ref` is what to
     open: a goal.<id> routes to the Goals view, anything else opens the artifact.
     `tone` splits presentation: "context" is a calm one-liner (an at-a-glance
     status, e.g. "all clear"); a CALL tone (review/call/…) is a real card that
     needs the operator. Wikilinks in `text` render as in-dash links: a known
     target becomes a clickable span-link; an unknown target renders muted+inert
     (same classes as the document-body wikilinks: sx-artlink / sx-artlink-dead). */

  // renderAgendaText: split a plain text string on [[name]] / [[name|alias]]
  // wikilinks and return an array of React nodes. Known targets get a clickable
  // span[role=link]; unknown targets get a muted, inert span. Clicking a valid
  // wikilink calls e.stopPropagation() so it doesn't bubble to the card button.
  // "Known" = present in ctx.artifacts (by .name) or ctx.goals (by .name or .id).
  function renderAgendaText(text, ctx) {
    if (!text) return text;
    const arts = (ctx && ctx.artifacts) || [];
    const goals = (ctx && ctx.goals) || [];
    // build a set of all known target strings
    const known = new Set();
    for (const a of arts) { if (a && a.name) known.add(a.name); }
    for (const g of goals) {
      if (g && g.name) known.add(g.name);
      if (g && g.id) known.add("goal." + g.id);
    }
    // split on [[name]] or [[name|alias]] — keep delimiters via capturing group
    const parts = text.split(/(\[\[[^\]|]+(?:\|[^\]]+)?\]\])/g);
    if (parts.length === 1) return text; // no wikilinks — fast path
    return parts.map((part, i) => {
      const m = part.match(/^\[\[([^\]|]+)(?:\|([^\]]+))?\]\]$/);
      if (!m) return part; // plain text segment
      const target = m[1].trim();
      const display = m[2] != null ? m[2].trim() : target;
      if (known.has(target)) {
        const onClick = (e) => {
          e.stopPropagation();
          if (target.indexOf("goal.") === 0) { ctx.onNav && ctx.onNav("goals"); }
          else { ctx.onOpenArtifact && ctx.onOpenArtifact(target); }
        };
        return <span key={i} className="sx-artlink" role="link" tabIndex={0} onClick={onClick}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onClick(e); } }}>{display}</span>;
      }
      return <span key={i} className="sx-artlink-dead">{display}</span>;
    });
  }

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
        <span className="fx-agenda-ctx-txt">{item.text ? renderAgendaText(item.text, ctx) : "All clear — nothing needs you right now."}</span>
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
      <div className={"fx-agenda-call fx-in" + (ref ? "" : " is-inert")} style={{ "--tn": tone, animationDelay: (0.06 + n * 0.03) + "s" }}
        role={ref ? "button" : undefined} tabIndex={ref ? 0 : undefined} onClick={ref ? open : undefined}
        onKeyDown={ref ? (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(); } } : undefined}>
        <span className="fx-agenda-rail" style={{ background: tone }} />
        <span className="fx-agenda-call-body">
          <span className="fx-agenda-call-kicker" style={{ color: tone }}>Needs you</span>
          <span className="fx-agenda-call-text">{item.text ? renderAgendaText(item.text, ctx) : ""}</span>
          {ref && <span className="fx-agenda-call-ref">{isGoal ? "◎ " + ref.replace(/^goal\./, "") : "❡ " + ref}</span>}
        </span>
        {ref && <span className="fx-agenda-call-open">{isGoal ? "Open goal →" : "Open →"}</span>}
      </div>);
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

  /* ---------- a queue row (S3.4: index · type glyph · title · type badge ·
     read-effort · originating goal · age) ---------- */
  function QRow({ a, n, onOpen }) {
    const isGoal = !!a.isGoal;
    const authorName = (a.author && a.author.name) || "agent";
    const tone = STATE_TONE[a.status] || "var(--todo)";
    const m = briefMeta(a);
    return (
      <button className="fx-then-row" style={{ "--tn": tone }} onClick={() => onOpen(a)}>
        <span className="fx-then-idx">
          <span className="fx-then-num">{String(n).padStart(2, "0")}</span>
          <span className="fx-then-glyph" style={{ color: tone }}>{m.glyph}</span>
        </span>
        <span className="fx-then-main">
          <span className="fx-then-title">{a.name}</span>
          <span className="fx-then-sub">
            <span className="fx-tbadge" style={{ color: tone, borderColor: tone, background: "rgba(120,124,132,.08)" }}>{m.type}</span>
            <span className="fx-then-effort">{m.effort}</span>
            {a.goal && <span className="fx-then-meta">◎ {a.goal}</span>}
            {!a.goal && <span className="fx-then-meta">{isGoal ? "goal" : "rev " + a.version}</span>}
          </span>
        </span>
        <span className="fx-then-right">
          <span className="fx-then-age">{(a.author && a.author.name) && <Av name={authorName} size={18} square />}{a.updated || ""}</span>
          <span className="fx-then-chev">›</span>
        </span>
      </button>);
  }

  /* ---------- Moving on its own (S3.6) ----------
     in-progress runs tied to a goal. No first-class run primitive yet: a run is an
     in-progress criterion with an owner (the owner = its ULID + function), tied to
     its goal. Each row has a live pulse, the run label/code, the goal, and its
     latest activity (the criterion text). Rows peek into the run (open a DM with
     the owner). Headlined "nothing needs you — this is just moving." */
  function movingRuns(goals) {
    const out = [];
    for (const g of goals || []) {
      for (const c of (g.criteria || [])) {
        if (c.status === "in-progress" && c.owner) out.push({ owner: c.owner, goal: g, crit: c });
      }
    }
    return out;
  }
  function shortId(id) {
    if (!id) return "run"; if (id.length <= 14) return id;
    return id.slice(0, 6) + "…" + id.slice(-4);
  }
  function MovingRow({ run, onDM }) {
    return (
      <button className="fx-moving-row" onClick={() => onDM && onDM(run.owner)}>
        <span className="fx-moving-pulse" />
        <span className="fx-moving-main">
          <span className="fx-moving-label"><span className="fx-moving-code">{shortId(run.owner)}</span> · {run.goal.name}</span>
          <span className="fx-moving-act">{run.crit.text || "working…"}</span>
        </span>
        <span className="fx-then-chev">›</span>
      </button>);
  }

  /* ---------- Goals · N need you (S3.5) ----------
     Only goals with a waiting/blocked criterion (or undefined / awaiting sign-off);
     each row carries a criteria status bar (one segment per criterion in its status
     colour), M-of-N met, and the rollup verdict chip. Clicking a row deep-links to
     that goal; "All goals" opens the portfolio. Returns null when none need you so
     the section header doesn't render (S3.9). */
  function CritBar({ g }) {
    const crits = g.criteria || [];
    if (!crits.length) return <span className="fx-goalsum-bar"><span className="fx-goalsum-seg is-empty" /></span>;
    return (
      <span className="fx-goalsum-bar">
        {crits.map((c, i) => <span className="fx-goalsum-seg" key={i} style={{ background: "var(--" + segVar(c.status) + ")" }} />)}
      </span>);
  }
  function GoalsBox({ goals, onNav }) {
    const rolled = goals.map((g) => ({ g, r: goalRoll(g) }));
    const needs = rolled.filter((x) => x.r.needsYou);
    if (!needs.length) return null;
    return (
      <div className="fx-goalsbox fx-in" style={{ animationDelay: ".16s" }}>
        {needs.slice(0, 4).map(({ g, r }) => (
          <button className="fx-goalsum-row" key={g.id} onClick={() => onNav && onNav("goals", g.id)}>
            <span className="fx-goalsum-body">
              <span className="fx-goalsum-name">{g.northstar || g.name}</span>
              <span className="fx-goalsum-meta">
                <CritBar g={g} />
                <span className="fx-goalsum-mn">{r.total ? r.met + " of " + r.total + " met" : "no criteria yet"}</span>
              </span>
            </span>
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
    // the review queue: review-flagged artifacts AND review-flagged goals (TASK-157),
    // interleaved most-recent-first on the same revision key artItems sorts on — so a
    // freshly-flagged goal can lead the queue (it isn't pinned behind every artifact).
    // A goal awaiting the operator's sign-off surfaces in the SAME needs-you queue as
    // any review-pending artifact, but routes to the Goals view (deep-linked to that
    // goal) rather than the artifact stage. Clearing the goal's review (approve/changes
    // in the Goals detail) drops it from goals.filter(review==="review") — so the queue
    // clears on sign-off.
    const goalArr = (ctx && ctx.goals) || [];
    const artPending = arts.filter((a) => a.status === "review");
    const goalPending = goalArr.filter((g) => g.review === "review").map((g) => ({
      name: g.northstar || g.name, isGoal: true, goalId: g.id, status: "review",
      // No-personas (TASK-194): a goal's author is not a persona byline — g.by may
      // be a person's display name, so it is deliberately dropped (empty author →
      // the hero shows generic copy + no avatar, never a name).
      version: g.version || 0, author: { name: "", kind: "agent" }, updated: g.updated || "",
    }));
    // S3.3 — rank by urgency (change/re-review first, then decision), then by
    // recency within a rank, so the hero is genuinely rank 1.
    const pending = artPending.concat(goalPending).sort((a, b) => {
      const r = briefRank(a) - briefRank(b);
      return r !== 0 ? r : (b.version || 0) - (a.version || 0);
    });
    const hero = pending[0];
    const rest = pending.slice(1);
    const total = pending.length;

    // goals breakdown for the summary line + the calm sections.
    const goalRolled = goalArr.map((gg) => ({ g: gg, r: goalRoll(gg) }));
    const goalsNeed = goalRolled.filter((x) => x.r.needsYou).length;
    const runs = movingRuns(goalArr); // S3.6 — in-progress runs tied to a goal

    // S3.9 — respect prefers-reduced-motion: when set, drop the staggered fade-in.
    const reduced = typeof window !== "undefined" && window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const inCls = reduced ? "" : "fx-in";

    // open a queue item: a goal routes to the Goals view (deep-linked to that goal);
    // anything else opens the artifact review stage.
    const openItem = (a) => { if (a && a.isGoal) { ctx.onNav && ctx.onNav("goals", a.goalId); } else { ctx.onOpenArtifact(a.name); } };

    const approved = arts.filter((a) => a.status === "approved").length;

    // greeting: the curated `home` artifact when it carries one, else time-of-day.
    const g = ctx && ctx.home && ctx.home.greeting;
    const hr = new Date().getHours();
    const defGreet = hr < 12 ? "Good morning." : hr < 18 ? "Good afternoon." : "Good evening.";
    const heading = (g && g.heading) || defGreet;
    // S3.1 — count needing a decision · goals waiting of total · everything else
    // is moving on its own.
    const goalClause = goalArr.length
      ? " · " + goalsNeed + " of " + goalArr.length + (goalArr.length === 1 ? " goal" : " goals") + " waiting"
      : "";
    const stateLine = (g && g.note) ? g.note
      : total === 0 && goalsNeed === 0 ? "Nothing needs a decision" + goalClause + " · everything else is moving on its own."
      : (total ? total + (total === 1 ? " thing needs" : " things need") + " a decision" : "Nothing needs a decision")
        + goalClause + " · everything else is moving on its own.";

    // S3.8 — the true empty state: nothing needs you AND nothing is running.
    const isEmpty = total === 0 && goalsNeed === 0 && runs.length === 0 && goalArr.length === 0 && approved === 0;

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

    // S3.8 — empty state: greeting + "Nothing needs you, because nothing's running
    // yet" + two start options + a the-bus-is-live note. Sections only render with
    // content, so when there's nothing at all this is the whole page.
    if (isEmpty && !hasAgenda) {
      return (
        <article className="fx-scroll"><div className="fx-col fx-col--home sx-conv-light">
          <style>{HOME_CSS}</style>
          <h1 className={"fx-h1 " + inCls}>{heading}</h1>
          <div className={"fx-emptyhome " + inCls} style={reduced ? undefined : { animationDelay: ".05s" }}>
            <div className="fx-emptyhome-lead">Nothing needs you — because nothing's running yet.</div>
            <div className="fx-emptyhome-opts">
              <button className="fx-emptyhome-opt" onClick={() => ctx.onNav && ctx.onNav("goals")}>
                <span className="fx-emptyhome-ic">◎</span>
                <span className="fx-emptyhome-otitle">Define your first goal</span>
                <span className="fx-emptyhome-osub">A north star + the criteria that make it true.</span>
              </button>
              <button className="fx-emptyhome-opt" onClick={() => ctx.onNav && ctx.onNav("workflow")}>
                <span className="fx-emptyhome-ic">⚡</span>
                <span className="fx-emptyhome-otitle">Build your first workflow</span>
                <span className="fx-emptyhome-osub">Spawn an agent to start moving on something.</span>
              </button>
            </div>
            <div className="fx-emptyhome-live"><span className="fx-emptyhome-livedot" /> The bus is live — the moment work starts, it surfaces here.</div>
          </div>
        </div></article>);
    }

    return (
      <article className="fx-scroll"><div className="fx-col fx-col--home sx-conv-light">
        <style>{HOME_CSS}</style>

        <h1 className={"fx-h1 " + inCls}>{heading}</h1>
        <p className={"fx-psub " + inCls} style={reduced ? undefined : { animationDelay: ".03s" }}>{stateLine}</p>

        {hasAgenda ? (
          <AgendaList block={agendaBlock} items={agendaItems} ctx={ctx} />
        ) : hero ? (
          <React.Fragment>
            <div className={"fx-starthead " + inCls} style={reduced ? undefined : { animationDelay: ".06s" }}>Start here</div>
            <Hero a={hero} onOpen={openItem} />
          </React.Fragment>
        ) : (
          <div className={"fx-zero " + inCls}>
            <span className="fx-zero-ic">✓</span>
            <div>
              <div className="fx-zero-title">You're all caught up.</div>
              <div className="fx-zero-sub">Agents keep working. The next thing that needs your judgement will appear here.</div>
            </div>
          </div>
        )}

        {rest.length > 0 && (
          <React.Fragment>
            <div className={"fx-sechead " + inCls} style={reduced ? undefined : { animationDelay: ".1s" }}>
              <span className="fx-grouplbl">Then · {rest.length} more</span>
              <button className="fx-seclink" onClick={() => ctx.onNav && ctx.onNav("artifacts")}>All artifacts →</button>
            </div>
            <div className="fx-then-list">
              {rest.map((a, i) => <QRow a={a} n={i + 1} onOpen={openItem} key={a.isGoal ? "goal:" + a.goalId : a.name} />)}
            </div>
          </React.Fragment>
        )}

        {/* S3.5 — Goals · N need you (header + rows only when goals need you) */}
        {goalsNeed > 0 && (
          <React.Fragment>
            <div className={"fx-sechead " + inCls} style={reduced ? undefined : { animationDelay: ".14s" }}>
              <span className="fx-grouplbl">Goals · {goalsNeed} need you</span>
              <button className="fx-seclink" onClick={() => ctx.onNav && ctx.onNav("goals")}>All goals →</button>
            </div>
            <GoalsBox goals={goalArr} onNav={ctx.onNav} />
          </React.Fragment>
        )}

        {/* S3.6 — Moving on its own: in-progress runs tied to a goal */}
        {runs.length > 0 && (
          <React.Fragment>
            <div className={"fx-sechead " + inCls} style={reduced ? undefined : { animationDelay: ".16s" }}>
              <span className="fx-grouplbl">Moving on its own</span>
              <span className="fx-sechead-note">nothing needs you</span>
            </div>
            <div className="fx-moving-list">
              {runs.slice(0, 6).map((run, i) => <MovingRow run={run} onDM={ctx.onDM} key={run.owner + ":" + run.crit.id + ":" + i} />)}
            </div>
          </React.Fragment>
        )}

        {/* secondary: curated pinned + quick links (assistant-owned `home` artifact) */}
        {(pinnedBlock || linksBlock) && (
          <div className={"hm-secondary " + inCls} style={reduced ? undefined : { animationDelay: ".18s" }}>
            {pinnedBlock && <PinnedPanel block={pinnedBlock} ctx={ctx} />}
            {linksBlock && <LinksPanel block={linksBlock} />}
          </div>
        )}

        {/* S3.7 — Finished while you were away: one collapsed reassurance bar */}
        {approved > 0 && (
          <div className={"fx-moved " + inCls} style={reduced ? undefined : { animationDelay: ".2s" }}>
            <button className="fx-moved-bar" onClick={() => setMovedOpen((o) => !o)}>
              <span className="fx-moved-dot" />Finished while you were away · {approved} {approved === 1 ? "thing" : "things"} — no input needed
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
  .fx-goalsbox{border:1px solid var(--fx-line);border-radius:13px;overflow:hidden;}
  .fx-goalsum-empty{padding:18px 18px;font-size:13.5px;color:var(--fx-ink2);}
  .fx-goalsum-row{display:flex;align-items:center;gap:14px;width:100%;text-align:left;background:none;border:none;border-top:1px solid var(--fx-line);padding:14px 18px;cursor:pointer;color:var(--ink);}
  .fx-goalsum-row:first-of-type{border-top:none;}
  .fx-goalsum-row:hover{background:rgba(20,21,24,.025);}
  .fx-goalsum-body{flex:1;min-width:0;display:flex;flex-direction:column;gap:7px;}
  .fx-goalsum-name{min-width:0;font-family:'Newsreader',Georgia,serif;font-size:15.5px;line-height:1.3;color:var(--fx-ink);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
  .fx-goalsum-meta{display:flex;align-items:center;gap:10px;}
  .fx-goalsum-bar{display:inline-flex;gap:3px;flex:0 0 auto;}
  .fx-goalsum-seg{width:18px;height:5px;border-radius:3px;background:var(--todo);}
  .fx-goalsum-seg.is-empty{width:36px;background:repeating-linear-gradient(90deg,var(--fx-line),var(--fx-line) 4px,transparent 4px,transparent 8px);}
  .fx-goalsum-mn{font-family:var(--font-mono);font-size:10.5px;letter-spacing:.02em;color:var(--fx-ink3);}
  .fx-goalsum-verdict{flex:0 0 auto;font-family:var(--font-mono);font-size:9.5px;font-weight:600;letter-spacing:.03em;text-transform:uppercase;padding:3px 9px;border-radius:20px;white-space:nowrap;align-self:flex-start;}
  #app.dark .fx-goalsbox,#app.dark .fx-goalsum-row{border-color:#2a2d33;}
  #app.dark .fx-goalsum-row:hover{background:rgba(255,255,255,.03);}
  /* S3.4 Then-row read-effort + S3.6 Moving-on-its-own + S3.7 sechead note */
  .fx-then-effort{font-family:var(--font-mono);font-size:10px;letter-spacing:.02em;color:var(--fx-ink3);}
  .fx-sechead-note{margin-left:auto;font-size:11.5px;color:var(--met);font-style:italic;}
  .fx-moving-list{display:flex;flex-direction:column;border:1px solid var(--fx-line);border-radius:13px;overflow:hidden;}
  .fx-moving-row{display:flex;align-items:center;gap:12px;width:100%;text-align:left;background:none;border:none;border-top:1px solid var(--fx-line);padding:13px 18px;cursor:pointer;color:var(--ink);}
  .fx-moving-row:first-child{border-top:none;}
  .fx-moving-row:hover{background:rgba(20,21,24,.025);}
  .fx-moving-pulse{width:8px;height:8px;border-radius:50%;flex:0 0 auto;background:var(--prog,#3a93d2);box-shadow:0 0 0 0 rgba(58,147,210,.6);animation:fxmpulse 1.8s infinite;}
  @keyframes fxmpulse{0%{box-shadow:0 0 0 0 rgba(58,147,210,.5);}70%{box-shadow:0 0 0 6px rgba(58,147,210,0);}100%{box-shadow:0 0 0 0 rgba(58,147,210,0);}}
  .fx-moving-main{flex:1;min-width:0;display:flex;flex-direction:column;gap:3px;}
  .fx-moving-label{font-size:13px;color:var(--fx-ink2);}
  .fx-moving-code{font-family:var(--font-mono);font-size:11.5px;color:var(--prog,#3a93d2);}
  .fx-moving-act{font-size:13.5px;color:var(--ink);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
  #app.dark .fx-moving-list,#app.dark .fx-moving-row{border-color:#2a2d33;}
  #app.dark .fx-moving-row:hover{background:rgba(255,255,255,.03);}
  /* S3.8 empty state */
  .fx-emptyhome{margin-top:18px;}
  .fx-emptyhome-lead{font-family:'Newsreader',Georgia,serif;font-size:21px;line-height:1.35;color:var(--fx-ink);margin-bottom:22px;}
  .fx-emptyhome-opts{display:grid;grid-template-columns:1fr 1fr;gap:14px;}
  @container (max-width:640px){ .fx-emptyhome-opts{grid-template-columns:1fr;} }
  .fx-emptyhome-opt{display:flex;flex-direction:column;gap:7px;text-align:left;background:none;border:1px solid var(--fx-line);border-radius:14px;padding:20px;cursor:pointer;color:var(--ink);transition:border-color .14s,transform .14s;}
  .fx-emptyhome-opt:hover{border-color:var(--asst);transform:translateY(-1px);}
  .fx-emptyhome-ic{font-size:22px;color:var(--asst);}
  .fx-emptyhome-otitle{font-size:15.5px;font-weight:600;}
  .fx-emptyhome-osub{font-size:13px;color:var(--fx-ink2);line-height:1.4;}
  .fx-emptyhome-live{display:flex;align-items:center;gap:9px;margin-top:22px;font-size:13px;color:var(--fx-ink2);}
  .fx-emptyhome-livedot{width:8px;height:8px;border-radius:50%;background:var(--met);box-shadow:0 0 0 0 rgba(84,173,110,.6);animation:fxmpulse 2.4s infinite;}
  @media (prefers-reduced-motion: reduce){ .fx-moving-pulse,.fx-emptyhome-livedot{animation:none;} .fx-in{animation:none!important;opacity:1!important;} }
  #app.dark .fx-emptyhome-opt{border-color:#2a2d33;}
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
  .fx-agenda-call.is-inert{cursor:default;box-shadow:none;transform:none;}
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
