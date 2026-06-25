/* goals.jsx — Sextant Goals (dash redesign · EPIC D, TASK-217/218). A goal is a
   north-star sentence + acceptance criteria (claims that can be true), shown in
   two layers. L1 Portfolio (decide where to spend time) → L2 Goal detail (the
   working-backwards document: criteria + the work toward each).

   Wired to the real goal primitive (goal.<id> latest-value artifacts, ADR-0035).
   app.jsx derives the `goals` array off the GET /api/goals projection (conv/goals,
   proof-filter already applied) and passes it in. Goal status is DERIVED from the
   criteria rollup — there is no stored goal-status field. Uses dxg- classes
   (ported into styles.css) plus the GOAL_CSS block below for the redesign bits.

   No first-class run/brief primitive exists yet, so a "run" is derived honestly:
   an in-progress criterion with an owner is a run working toward that criterion
   (the owner is its ULID + function). "Watch" opens the run's goal (no run view
   yet — never a DM); every spawn affordance opens the shared top-level Spawn-work
   flow (app.jsx's openSpawn → the MobilizeButton popover). Where nothing backs a
   piece, we degrade to the design's empty/dashed states. Exports GoalsView. */
(function () {
  const { useState, useEffect, useRef } = React;
  const { Avatar } = window;

  // stat(s) — the goal lexicon (ADR-0035): met / in-progress / waiting-on-you /
  // blocked / not-started. The glyph + label come from the ONE canonical status
  // helper (window.SxStatus, TASK-204) so this view can't drift from the rest of
  // the dash; the `tone` class is the local CSS chip tint (--met/--prog/…).
  const TONE = { "met": "t-met", "in-progress": "t-progress", "waiting-on-you": "t-waiting", "blocked": "t-blocked", "not-started": "t-todo" };
  function stat(s) {
    const m = (window.SxStatus ? window.SxStatus.meta(s) : { glyph: "○", label: "Not started" });
    // canonicalise to the lexicon key for the tone lookup
    const key = (window.SxStatus && TONE[s]) ? s
      : (m.label === "Met" ? "met" : m.label === "In progress" ? "in-progress"
        : m.label === "Waiting on you" ? "waiting-on-you" : m.label === "Blocked" ? "blocked" : "not-started");
    return { label: m.label, glyph: m.glyph, tone: TONE[key] || "t-todo" };
  }

  // roll(g): the criteria rollup → verdict + tone. Goal status is derived, never
  // stored. Order (sign-off, waiting BEFORE blocked) mirrors home.jsx's goalRoll()
  // exactly — keep the two in lockstep: this is the operator's front door, so a
  // call FOR them leads; blocked (the agents' to clear) surfaces only when nothing
  // waits on the operator.
  function roll(g) {
    const crits = (g && g.criteria) || [];
    const met = crits.filter((c) => c.status === "met").length;
    const waiting = crits.filter((c) => c.status === "waiting-on-you").length;
    const blocked = crits.some((c) => c.status === "blocked");
    const inprog = crits.filter((c) => c.status === "in-progress").length;
    const total = crits.length;
    const undef = !g || !g.northstar || total === 0;
    const signoff = !!g && g.review === "review";
    let verdict, tone;
    if (undef) { verdict = "Not yet defined"; tone = "t-waiting"; }
    else if (signoff) { verdict = "Awaiting your sign-off"; tone = "t-waiting"; }
    else if (waiting) { verdict = waiting + (waiting > 1 ? " criteria" : " criterion") + " waiting on you"; tone = "t-waiting"; }
    else if (met === total) { verdict = "Done"; tone = "t-met"; }
    else if (blocked) { verdict = "Blocked"; tone = "t-blocked"; }
    else { verdict = "On track"; tone = "t-met"; }
    return { met, waiting, blocked, inprog, total, undef, signoff, verdict, tone };
  }

  // a goal needs the operator when undefined, has any waiting-on-you, any blocked
  // criterion, OR is flagged review.state="review" awaiting their sign-off (TASK-157).
  function needsYou(g) {
    const r = roll(g);
    return r.undef || r.waiting > 0 || g.review === "review" || r.blocked;
  }

  // S4.2 bucketing — three attention buckets:
  //   needs   = a waiting/blocked criterion, undefined, or awaiting sign-off;
  //   started = no work running AND nothing met (not yet underway);
  //   moving  = settled (all met) or genuinely in flight (an in-progress criterion).
  // A goal lands in exactly one bucket; needsYou wins, then "not started", else moving.
  function bucketOf(g) {
    if (needsYou(g)) return "needs";
    const r = roll(g);
    if (r.inprog === 0 && r.met === 0) return "started"; // nothing running, nothing met
    return "moving";
  }

  // status → the CSS custom-property name for the segment fill colour.
  function segVar(s) {
    return s === "met" ? "met"
      : s === "in-progress" ? "prog"
      : s === "waiting-on-you" ? "wait"
      : s === "blocked" ? "blk"
      : "todo";
  }

  /* ---------- escape-hatch tag (S4.4) ----------
     A goal whose stream reads like an escape hatch (off-track / manual / paused)
     gets a small caution tag. The stream label is opaque to the bus, so this is a
     light convention, not a stored flag. */
  function escapeHatch(g) {
    const s = (g.stream || "").toLowerCase();
    return /escape|manual|paused|off-track|stuck/.test(s);
  }

  /* ---------- run chip (S4.5 / S5.4) ---------- */
  function RunChip({ run, onWatch }) {
    // no-personas (TASK-194): a run is identified by function + ULID, never its
    // owner's name. Show a neutral ⬡ run marker (+ short ULID when one exists),
    // matching the Home "Moving on its own" rows — never run.owner.
    const id = run.ulid || run.id || run.runId || null;
    return (
      <button type="button" className="dxg-runchip" title="Watch the run"
        onClick={(e) => { e.stopPropagation(); onWatch && onWatch(); }}>
        <span className="dxg-runpulse" />
        <span className="dxg-runlabel">⬡ {id ? shortId(id) : "run"}</span>
        <span className="dxg-runwatch">watch</span>
      </button>);
  }
  // shortId trims a long ULID-ish id to head…tail for a chip.
  function shortId(id) {
    if (!id) return "run";
    if (id.length <= 14) return id;
    return id.slice(0, 6) + "…" + id.slice(-4);
  }

  /* ---------- L1 · Portfolio card (S4.4–S4.7) ----------
     No-personas (operator hard line): a goal CARD carries NO owner/run chips — only
     the criteria activity-map + status verdict (and, on a not-started card, the
     dashed +spawn-work cue). The ULID+run-label chips that work toward a criterion
     live in the goal DETAIL, never on the card. */
  function Card({ g, onOpen, onSpawn, renderWiki }) {
    const r = roll(g);
    const crits = g.criteria || [];
    const rw = renderWiki || ((t) => t);
    const bucket = bucketOf(g);
    const open = () => onOpen && onOpen(g.id);
    return (
      <div className="dxg-card" role="button" tabIndex={0}
        onClick={open}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(); } }}>
        <div className="dxg-card-top">
          <span className="dxg-card-name">{g.name}</span>
          {g.stream && <span className="dxg-card-stream">{g.stream}</span>}
          {escapeHatch(g) && <span className="dxg-card-escape" title="Escape hatch — off the automatic path">⤴ escape</span>}
          <span className={"dxg-verdict " + r.tone}>{r.verdict}</span>
        </div>
        <div className={"dxg-northstar" + (r.undef ? " is-undef" : "")}>{g.northstar ? rw(g.northstar) : "No north star yet — what does success look like?"}</div>
        <div className="dxg-rollup">
          <div className="dxg-segs">
            {crits.map((c, i) => <span className="dxg-seg" key={i} title={(c.text || c.id) + " — " + stat(c.status).label} style={{ background: "var(--" + segVar(c.status) + ")" }} />)}
            {r.undef && crits.length === 0 && <span className="dxg-seg is-empty" />}
          </div>
          <span className="dxg-rollup-txt">{r.total === 0 ? "0 criteria defined" : r.met + " of " + r.total + " met"}</span>
        </div>

        {/* S4.6 — Not-started cards show a dashed +spawn-work chip */}
        {bucket === "started" && (
          <div className="dxg-card-runs">
            <button type="button" className="dxg-spawnchip"
              onClick={(e) => { e.stopPropagation(); onSpawn && onSpawn(g); }}>
              <span className="dxg-spawnchip-plus">+</span> No work running yet — spawn work
            </button>
          </div>
        )}
      </div>);
  }

  /* ---------- inline New-goal creator (S4.3) ----------
     A one-line north-star input. "Write the charter" hands the north star to the
     Composer (onCompose) — which, where wired, opens the §16 composer pre-seeded.
     Where no composer is wired we fall back to the shared Spawn-work flow so the
     affordance still does something live. */
  function NewGoal({ onCompose }) {
    const [open, setOpen] = useState(false);
    const [ns, setNs] = useState("");
    const inputRef = useRef(null);
    useEffect(() => { if (open && inputRef.current) inputRef.current.focus(); }, [open]);
    if (!open) {
      return (
        <button className="dxg-newgoal-cue" onClick={() => setOpen(true)}>
          <span className="dxg-newgoal-plus">+</span> New goal — name a north star
        </button>);
    }
    const go = () => { if (ns.trim()) { onCompose && onCompose(ns.trim()); setNs(""); setOpen(false); } };
    return (
      <div className="dxg-newgoal">
        <span className="dxg-newgoal-ic">◎</span>
        <input ref={inputRef} className="dxg-newgoal-in" value={ns}
          placeholder="What does success look like, in one line?"
          onChange={(e) => setNs(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); go(); } if (e.key === "Escape") { setNs(""); setOpen(false); } }} />
        <button className="dxg-newgoal-go" disabled={!ns.trim()} onClick={go}>Write the charter →</button>
      </div>);
  }

  /* ---------- L1 · Portfolio (S4.1–S4.2) ---------- */
  const BUCKETS = [
    { key: "needs",   label: "Needs you" },
    { key: "started", label: "Not started" },
    { key: "moving",  label: "Moving on its own" },
  ];
  function Portfolio({ goals, onOpen, onSpawn, onCompose, renderWiki, reduced }) {
    const by = { needs: [], started: [], moving: [] };
    for (const g of goals) by[bucketOf(g)].push(g);
    const needCount = by.needs.length;
    return (
      <div className="dxg-scroll"><div className="dxg-col">
        <header className="dxg-phead">
          <h1 className="dxg-h1">Goals</h1>
          <span className="dxg-psub">{needCount} of {goals.length} {needCount === 1 ? "needs" : "need"} something from you · working backwards from each deliverable</span>
        </header>

        <NewGoal onCompose={onCompose} />

        {BUCKETS.map((b) => {
          const list = by[b.key];
          if (list.length === 0) return null; // S4.1 — omit empty buckets
          return (
            <React.Fragment key={b.key}>
              <div className="dxg-group-lbl">{b.label} <span className="dxg-group-n">{list.length}</span></div>
              <div className="dxg-cards">
                {list.map((g, i) => (
                  <div className={reduced ? "" : "fx-in"} style={reduced ? undefined : { animationDelay: (0.04 + i * 0.03) + "s" }} key={g.id}>
                    <Card g={g} onOpen={onOpen} onSpawn={onSpawn} renderWiki={renderWiki} />
                  </div>))}
              </div>
            </React.Fragment>);
        })}
      </div></div>);
  }

  /* ---------- L2 · Goal detail criterion row (S5.2–S5.4) ----------
     status → routing line + the inline actions a criterion offers:
       waiting     → "→ its brief"        + +spawn work
       in-progress → "→ watch the run"    + watch (the run chip)
       blocked     → "→ see the blocker"  + +link a workstream
       not-started → "no work yet"        + +spawn work
       met         → settled */
  function routeFor(status) {
    switch (status) {
      case "waiting-on-you": return "→ open its brief";
      case "in-progress":    return "→ watch the run";
      case "blocked":        return "→ see the blocker";
      case "not-started":    return "no work yet";
      case "met":            return "settled";
      default:               return "";
    }
  }
  function Criterion({ g, c, goalName, onOpenArtifact, onOpenRun, onSpawn, onLink, onLinkCriterion, renderWiki }) {
    const s = stat(c.status);
    const evidence = c.evidence || [];
    const rw = renderWiki || ((t) => t);
    const runOwner = c.status === "in-progress" && c.owner ? c.owner : null;
    return (
      <div className="dxg-crit">
        <span className={"dxg-crit-icon " + s.tone}>{s.glyph}</span>
        <div className="dxg-crit-main">
          <div className="dxg-crit-text">{c.text ? rw(c.text) : c.text}</div>
          <div className="dxg-crit-evi">
            {/* the run(s) working toward this criterion — ULID chips (S5.2).
                "watch" opens the run's goal (no run view yet); never a DM. */}
            {runOwner && <RunChip run={{ owner: runOwner, crit: c }} onWatch={() => onOpenRun && onOpenRun(g)} />}
            {evidence.length
              ? evidence.map((e, i) => (
                  <button className="dxg-chip" key={i} type="button"
                    onClick={() => onOpenArtifact && onOpenArtifact(e.name)}
                    title={(e.kind === "proof" ? "proof · " : "related · ") + e.name}>{e.name}</button>))
              : !runOwner && <span className="dxg-noevi">— no work yet</span>}

            {/* per-criterion inline actions (S5.4). +link prefers the dedicated
                LinkWorkstream overlay (review lane, onLinkCriterion) when wired,
                else falls back to the local DM/spawn handler (onLink). */}
            <span className="dxg-crit-acts">
              {c.status === "blocked" && (
                onLinkCriterion
                  ? <button className="dxg-critact" type="button"
                      onClick={() => onLinkCriterion({ id: c.id, text: c.text, goal: goalName, linked: evidence.map((e) => e.name) })}>+ link a workstream</button>
                  : <button className="dxg-critact" type="button" onClick={() => onLink && onLink(g, c)}>+ link a workstream</button>)}
              {(c.status === "waiting-on-you" || c.status === "not-started") && (
                <button className="dxg-critact" type="button" onClick={() => onSpawn && onSpawn(g, c)}>+ spawn work</button>)}
            </span>
          </div>
        </div>
        <div className="dxg-crit-right">
          <span className={"dxg-crit-route " + s.tone}>{routeFor(c.status)}</span>
          <span className={"dxg-crit-status " + s.tone}>{s.label}</span>
        </div>
      </div>);
  }

  /* ---------- Add criterion inline (S5.5) ---------- */
  function AddCriterion({ onAdd }) {
    const [open, setOpen] = useState(false);
    const [text, setText] = useState("");
    const [busy, setBusy] = useState(false);
    const inputRef = useRef(null);
    useEffect(() => { if (open && inputRef.current) inputRef.current.focus(); }, [open]);
    if (!open) {
      return <button className="dxg-addcrit-cue" onClick={() => setOpen(true)}><span className="dxg-addcrit-plus">+</span> Add criterion</button>;
    }
    const submit = async () => {
      const t = text.trim();
      if (!t || busy) return;
      setBusy(true);
      try { await onAdd(t); setText(""); setOpen(false); }
      finally { setBusy(false); }
    };
    return (
      <div className="dxg-addcrit">
        <span className="dxg-crit-icon t-todo">○</span>
        <input ref={inputRef} className="dxg-addcrit-in" value={text} disabled={busy}
          placeholder="A checkable outcome — Enter to add, Esc to cancel"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); submit(); } if (e.key === "Escape") { setText(""); setOpen(false); } }} />
        <button className="dxg-addcrit-go" disabled={!text.trim() || busy} onClick={submit}>{busy ? "Adding…" : "Add"}</button>
      </div>);
  }

  /* ---------- Goal topic composer + thread (S5.6) ----------
     A posting composer for the goal's companion topic (msg.topic.goals.<id>). Posts
     are durable bus messages; the thread is re-read from the bus (so a reload
     re-derives it). Each post is attributed to you — just now / its relative age. */
  function GoalTopic({ goalId, self, onPost, loadThread }) {
    const [text, setText] = useState("");
    const [thread, setThread] = useState([]);
    const [busy, setBusy] = useState(false);
    const reload = () => { if (loadThread) loadThread(goalId).then((m) => setThread(Array.isArray(m) ? m : [])).catch(() => {}); };
    useEffect(() => { reload(); /* eslint-disable-next-line */ }, [goalId]);
    const post = async () => {
      const t = text.trim();
      if (!t || busy) return;
      setBusy(true);
      // optimistic append — attributed to you, just now (re-derived from the bus on reload)
      setThread((prev) => prev.concat([{ id: "local-" + Date.now(), self: true, text: t, time: "just now" }]));
      setText("");
      try { await onPost(goalId, t); } finally { setBusy(false); setTimeout(reload, 400); }
    };
    return (
      <div className="dxg-topic">
        <div className="dxg-topic-h">Goal topic</div>
        {thread.length > 0 && (
          <div className="dxg-topic-thread">
            {thread.map((m) => (
              <div className={"dxg-topic-msg" + (m.self ? " is-self" : "")} key={m.id}>
                <span className="dxg-topic-author">{m.self ? "you" : (shortId(m.author) || "run")}</span>
                <span className="dxg-topic-text">{m.text}</span>
                <span className="dxg-topic-time">{m.time || ""}</span>
              </div>))}
          </div>)}
        <div className="dxg-topic-compose">
          <textarea className="dxg-topic-in" rows={2} value={text} disabled={busy}
            placeholder="Post to this goal's topic — agents working it are listening…"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); post(); } }} />
          <button className="dxg-topic-post" disabled={!text.trim() || busy} onClick={post}>{busy ? "Posting…" : "Post"}</button>
        </div>
      </div>);
  }

  /* ---------- sign-off (TASK-157) — unchanged ---------- */
  function SignOff({ g, onSetReview }) {
    const name = "goal." + g.id;
    const st = g.review;
    const [note, setNote] = useState("");
    // No "signed off / settled" or "requested changes" confirmation banner — the
    // design's goal detail has no such banner (the verdict reads off the criteria
    // rollup). Only the INTERACTIVE sign-off prompt (awaiting your sign-off) renders;
    // once approved/changes, the prompt simply isn't shown.
    if (st === "approved" || st === "changes") return null;
    const canChanges = !!note.trim();
    return (
      <div className="dxg-signoff is-review">
        <div className="dxg-signoff-lead"><span className="dxg-signoff-ic">✦</span><span className="dxg-signoff-txt">This goal is waiting on <b>your sign-off</b>. Review the criteria below, then approve or ask for changes.</span></div>
        <textarea className="dxg-signoff-note" rows={2}
          placeholder="Add feedback for the agent — required to request changes, optional to approve…"
          value={note} onChange={(e) => setNote(e.target.value)} />
        <div className="dxg-signoff-acts">
          <button className="dxg-signoff-btn is-approve" onClick={() => onSetReview(name, "approved", note)}>Approve goal</button>
          <button className="dxg-signoff-btn" disabled={!canChanges} title={canChanges ? "" : "Add feedback to request changes"} onClick={() => canChanges && onSetReview(name, "changes", note)}>Request changes</button>
        </div>
      </div>);
  }

  /* ---------- L2 · Goal detail (S5.1) ---------- */
  function Detail({ g, self, onBack, onOpenArtifact, onSetReview, onOpenRun, onSpawnGoal, onSpawnCrit, onLinkCrit, onLinkCriterion, onAddCriterion, onPostTopic, loadThread, renderWiki }) {
    const r = roll(g);
    const crits = g.criteria || [];
    const rw = renderWiki || ((t) => t);
    const showSignOff = onSetReview && (g.review === "review" || g.review === "changes" || g.review === "approved");
    return (
      <div className="dxg-detail">
        <div className="dxg-topbar">
          <button className="dxg-back" onClick={onBack}>← Goals</button>
          {g.stream && <span className="dxg-topbar-stream">{g.stream}</span>}
          {escapeHatch(g) && <span className="dxg-card-escape" title="Escape hatch">⤴ escape</span>}
          {!r.undef && <span className="dxg-topbar-roll">{r.met} of {r.total} met</span>}
          <button className="dxg-topbar-spawn" onClick={() => onSpawnGoal && onSpawnGoal(g)}>+ Spawn work</button>
        </div>
        <div className="dxg-scroll">
          <div className="dxg-doc">
            {showSignOff && <SignOff g={g} onSetReview={onSetReview} />}
            <div className="dxg-ns-block">
              <div className="dxg-ns-lbl">North star</div>
              {g.northstar
                ? <div className="dxg-ns-text">{rw(g.northstar)}</div>
                : <div className="dxg-ns-text is-undef">This goal isn't defined yet.</div>}
            </div>

            <div className="dxg-donewhen">
              <span className="dxg-donewhen-lbl">Done when</span>
              <span className="dxg-donewhen-sub">{r.undef ? "first, what does success look like?" : "every criterion below is true."}</span>
            </div>

            <div className="dxg-crits">
              {crits.length
                ? crits.map((c, i) => <Criterion g={g} c={c} goalName={g.name || g.id} onOpenArtifact={onOpenArtifact} onOpenRun={onOpenRun} onSpawn={onSpawnCrit} onLink={onLinkCrit} onLinkCriterion={onLinkCriterion} renderWiki={renderWiki} key={c.id || i} />)
                : <div className="dxg-noevi" style={{ padding: "14px 0" }}>No criteria yet — this goal hasn't been broken down into checkable outcomes.</div>}
              <AddCriterion onAdd={(t) => onAddCriterion(g.id, t)} />
            </div>

            <GoalTopic goalId={g.id} self={self} onPost={onPostTopic} loadThread={loadThread} />

            <div className="dxg-maintained">Criteria are the contract every small decision is judged against. Status is kept current on the bus by the agents doing the work.</div>
          </div>
        </div>
      </div>);
  }

  /* ---------- empty state ---------- */
  function Empty({ onCompose }) {
    return (
      <div className="dxg-scroll"><div className="dxg-col">
        <header className="dxg-phead">
          <h1 className="dxg-h1">Goals</h1>
        </header>
        <NewGoal onCompose={onCompose} />
        <div className="fx-stub">
          <span className="fx-stub-ic">◎</span>
          <div>
            <div className="fx-stub-title">No goals on the bus yet.</div>
            <div className="fx-stub-sub">A goal appears here when an agent or operator publishes a <span className="mono">goal.&lt;id&gt;</span> artifact — a north star plus the criteria that make it true.</div>
          </div>
        </div>
      </div></div>);
  }

  /* ---------- the view ---------- */
  // GoalsView holds the L1↔L2 selection plus the live mobilize popover (spawn).
  // initialGoalId deep-links straight to a goal's detail (set when opened from the
  // needs-you queue). onOpenArtifact opens an evidence artifact in the review stage;
  // onSetReview persists a goal sign-off verdict (the same review primitive the rest
  // of the dash uses); onLinkCriterion opens the LinkWorkstream overlay (review lane).
  function GoalsView({ goals, initialGoalId, self, onOpenArtifact, onSetReview, onLinkCriterion, onSpawn, renderWiki }) {
    const [openGoal, setOpenGoal] = useState(initialGoalId || null);
    const reduced = typeof window !== "undefined" && window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const list = goals || [];

    // Every spawn affordance opens the shared top-level Spawn-work flow (app.jsx's
    // openSpawn → the MobilizeButton popover, the live spawn.request path). No
    // hidden auto-clicked button, and no DM/Conversations fallback.
    const spawn = (ctx) => onSpawn && onSpawn(ctx);
    // S4.3 / §16 Composer: where a real Composer route exists we'd open it; here we
    // seed a spawn from the north star (the live affordance), so "Write the charter"
    // does something real against the bus rather than dead-ending.
    const compose = (northstar) => spawn({ type: "goal", northstar, id: "" });
    const spawnGoal = (g) => spawn({ type: "goal", northstar: g.northstar || g.name, id: g.id });
    const spawnCrit = (g, c) => spawn({ type: "goal", northstar: (c && c.text) || g.northstar || g.name, id: g.id });
    // +link a workstream (S5.4 / §14): no link-picker primitive yet — seed a spawn
    // to wire a workstream toward the blocked criterion. Degrades honestly.
    const linkCrit = (g, c) => spawn({ type: "goal", northstar: "Unblock: " + ((c && c.text) || (g && g.northstar) || ""), id: g.id });
    // a run has no first-class view yet → "watch" deep-links the goal it works
    // toward (the run view's stand-in). No DM, no owner rendered.
    const openRun = (g) => setOpenGoal(g.id);

    const addCriterion = (goalId, text) => window.SX.addCriterion(goalId, text);
    const postTopic = (goalId, text) => window.SX.postToGoalTopic(goalId, text);
    const loadThread = async (goalId) => {
      const res = await window.SX.get("/api/messages?subject=" + encodeURIComponent("msg.topic.goals." + goalId) + "&limit=50");
      const msgs = (res && res.messages) || [];
      return msgs.map((m) => ({
        id: m.id, self: m.author === (self && self.id),
        author: m.author, text: (m.record && (m.record.text || m.record.title)) || "",
        time: m.createdAt ? relAge(m.createdAt) : "",
      })).filter((m) => m.text);
    };

    if (list.length === 0) return (
      <React.Fragment>
        <Empty onCompose={compose} />
      </React.Fragment>);

    const g = openGoal && list.find((x) => x.id === openGoal);
    return (
      <React.Fragment>
        {g ? (
          <Detail g={g} self={self} onBack={() => setOpenGoal(null)}
            onOpenArtifact={onOpenArtifact} onSetReview={onSetReview} onOpenRun={openRun}
            onSpawnGoal={spawnGoal} onSpawnCrit={spawnCrit} onLinkCrit={linkCrit}
            onLinkCriterion={onLinkCriterion}
            onAddCriterion={addCriterion} onPostTopic={postTopic} loadThread={loadThread}
            renderWiki={renderWiki} />
        ) : (
          <Portfolio goals={list} onOpen={(id) => setOpenGoal(id)}
            onSpawn={spawnGoal} onCompose={compose} renderWiki={renderWiki} reduced={reduced} />
        )}
        <style>{GOAL_CSS}</style>
      </React.Fragment>);
  }

  // relAge: a coarse "Xm/Xh/Xd ago" from an ISO timestamp for thread posts.
  function relAge(iso) {
    const t = Date.parse(iso || ""); if (isNaN(t)) return "";
    const s = Math.max(0, (Date.now() - t) / 1000);
    if (s < 45) return "just now";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
  }

  const GOAL_CSS = `
  .dxg-group-n{font-family:var(--font-mono);font-size:11px;font-weight:600;color:var(--fx-ink3);margin-left:6px;}
  .dxg-card-escape{font-family:var(--font-mono);font-size:9.5px;font-weight:600;letter-spacing:.03em;text-transform:uppercase;color:var(--wait);border:1px solid var(--wait);border-radius:5px;padding:2px 6px;}
  .dxg-card-runs{display:flex;flex-wrap:wrap;gap:8px;margin-top:11px;padding-top:11px;border-top:1px solid var(--fx-line);}
  .dxg-runchip{display:inline-flex;align-items:center;gap:7px;background:rgba(58,147,210,.08);border:1px solid rgba(58,147,210,.3);border-radius:20px;padding:4px 11px 4px 9px;font-size:11.5px;color:var(--ink);cursor:pointer;font-family:var(--font-mono);}
  .dxg-runchip:hover{background:rgba(58,147,210,.15);}
  .dxg-runpulse{width:7px;height:7px;border-radius:50%;background:var(--prog,#3a93d2);box-shadow:0 0 0 0 rgba(58,147,210,.6);animation:dxgpulse 1.8s infinite;}
  @media (prefers-reduced-motion: reduce){ .dxg-runpulse{animation:none;} }
  @keyframes dxgpulse{0%{box-shadow:0 0 0 0 rgba(58,147,210,.5);}70%{box-shadow:0 0 0 6px rgba(58,147,210,0);}100%{box-shadow:0 0 0 0 rgba(58,147,210,0);}}
  .dxg-runlabel{letter-spacing:.02em;}
  .dxg-runwatch{font-size:10px;text-transform:uppercase;letter-spacing:.05em;color:var(--prog,#3a93d2);font-weight:600;}
  .dxg-spawnchip{display:inline-flex;align-items:center;gap:7px;background:none;border:1px dashed var(--fx-line);border-radius:20px;padding:5px 13px;font-size:12px;color:var(--fx-ink2);cursor:pointer;}
  .dxg-spawnchip:hover{border-color:var(--asst);color:var(--asst);}
  .dxg-spawnchip-plus{font-size:14px;line-height:1;}
  .dxg-newgoal-cue{display:flex;align-items:center;gap:8px;width:100%;text-align:left;background:none;border:1px dashed var(--fx-line);border-radius:12px;padding:13px 16px;font-size:13.5px;color:var(--fx-ink2);cursor:pointer;margin-bottom:18px;}
  .dxg-newgoal-cue:hover{border-color:var(--asst);color:var(--asst);}
  .dxg-newgoal-plus{font-size:16px;line-height:1;}
  .dxg-newgoal{display:flex;align-items:center;gap:10px;border:1px solid var(--asst);border-radius:12px;padding:10px 12px;margin-bottom:18px;background:rgba(106,85,224,.03);}
  .dxg-newgoal-ic{color:var(--asst);font-size:16px;}
  .dxg-newgoal-in{flex:1;min-width:0;border:none;background:none;outline:none;font-family:'Newsreader',Georgia,serif;font-size:16px;color:var(--ink);}
  .dxg-newgoal-go{flex:0 0 auto;background:var(--asst);color:#fff;border:none;border-radius:8px;padding:8px 14px;font-size:12.5px;font-weight:600;cursor:pointer;}
  .dxg-newgoal-go:disabled{opacity:.45;cursor:default;}
  .dxg-crit-route{font-family:var(--font-mono);font-size:10px;letter-spacing:.02em;}
  .dxg-crit-acts{display:inline-flex;gap:8px;}
  .dxg-critact{background:none;border:none;color:var(--asst);font-size:11.5px;font-weight:600;cursor:pointer;padding:2px 0;}
  .dxg-critact:hover{text-decoration:underline;}
  .dxg-topbar-spawn{margin-left:auto;background:var(--asst);color:#fff;border:none;border-radius:8px;padding:6px 13px;font-size:12px;font-weight:600;cursor:pointer;}
  .dxg-addcrit-cue{display:inline-flex;align-items:center;gap:7px;background:none;border:none;color:var(--asst);font-size:12.5px;font-weight:600;cursor:pointer;padding:12px 2px 4px;}
  .dxg-addcrit-plus{font-size:15px;line-height:1;}
  .dxg-addcrit{display:flex;align-items:center;gap:10px;padding:11px 2px;border-top:1px solid var(--fx-line);}
  .dxg-addcrit-in{flex:1;min-width:0;border:none;background:none;outline:none;font-size:14.5px;color:var(--ink);}
  .dxg-addcrit-go{flex:0 0 auto;background:var(--asst);color:#fff;border:none;border-radius:7px;padding:6px 12px;font-size:12px;font-weight:600;cursor:pointer;}
  .dxg-addcrit-go:disabled{opacity:.45;cursor:default;}
  .dxg-topic{margin-top:26px;border-top:1px solid var(--fx-line);padding-top:18px;}
  .dxg-topic-h{font-family:var(--font-mono);font-size:10.5px;letter-spacing:.12em;text-transform:uppercase;color:var(--fx-ink3);margin-bottom:12px;}
  .dxg-topic-thread{display:flex;flex-direction:column;gap:10px;margin-bottom:14px;}
  .dxg-topic-msg{display:flex;flex-direction:column;gap:3px;border-left:2px solid var(--fx-line);padding:2px 0 2px 12px;}
  .dxg-topic-msg.is-self{border-left-color:var(--asst);}
  .dxg-topic-author{font-family:var(--font-mono);font-size:10px;letter-spacing:.04em;text-transform:uppercase;color:var(--fx-ink3);}
  .dxg-topic-text{font-size:14px;line-height:1.45;color:var(--ink);}
  .dxg-topic-time{font-family:var(--font-mono);font-size:10px;color:var(--fx-ink3);}
  .dxg-topic-compose{display:flex;gap:10px;align-items:flex-end;}
  .dxg-topic-in{flex:1;min-width:0;border:1px solid var(--fx-line);border-radius:10px;padding:9px 11px;font-size:13.5px;font-family:inherit;color:var(--ink);background:var(--fx-bg,transparent);resize:vertical;outline:none;}
  .dxg-topic-in:focus{border-color:var(--asst);}
  .dxg-topic-post{flex:0 0 auto;background:var(--asst);color:#fff;border:none;border-radius:8px;padding:9px 16px;font-size:12.5px;font-weight:600;cursor:pointer;}
  .dxg-topic-post:disabled{opacity:.45;cursor:default;}
  .dxg-spawnportal{position:fixed;inset:0;z-index:50;background:rgba(20,21,30,.35);display:grid;place-items:start center;padding-top:14vh;}
  #app.dark .dxg-card-runs,#app.dark .dxg-addcrit,#app.dark .dxg-topic,#app.dark .dxg-topic-msg{border-color:#2a2d33;}
  #app.dark .dxg-newgoal-cue,#app.dark .dxg-spawnchip{border-color:#2a2d33;}
  #app.dark .dxg-topic-in{border-color:#2a2d33;}
  `;

  Object.assign(window, { GoalsView });
})();
