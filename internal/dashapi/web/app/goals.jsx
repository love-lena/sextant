/* goals.jsx — Sextant Goals (v0.5, Track 2). The converged goal model: a goal is
   a north-star sentence + acceptance criteria (claims that can be true), shown in
   two layers. L1 Portfolio (decide where to spend time) → L2 Goal detail (criteria
   + evidence). Read-only for v1: status is kept current on the bus by the agents
   doing the work; there is no write path here yet.

   Wired to the real goal primitive (goal.<id> latest-value artifacts, ADR-0035).
   app.jsx derives the `goals` array off /api/artifacts records and passes it in.
   Goal status is DERIVED from the criteria rollup — there is no stored goal-status
   field. Uses dxg- classes (ported into styles.css). Exports GoalsView to window. */
(function () {
  const { useState } = React;
  const { Avatar } = window;

  // STATUS — keyed on the goal lexicon (ADR-0035): met / in-progress /
  // waiting-on-you / blocked / not-started. Tone classes are the live status
  // chips (--met/--prog/--wait/--blk/--todo). The design's keys
  // (met/progress/waiting/blocked/todo) map onto these.
  const STATUS = {
    "met":            { label: "Met",            glyph: "✓", tone: "t-met" },
    "in-progress":    { label: "In progress",    glyph: "◐", tone: "t-progress" },
    "waiting-on-you": { label: "Waiting on you",  glyph: "●", tone: "t-waiting" },
    "blocked":        { label: "Blocked",        glyph: "⊘", tone: "t-blocked" },
    "not-started":    { label: "Not started",    glyph: "○", tone: "t-todo" },
  };
  function stat(s) { return STATUS[s] || STATUS["not-started"]; }

  // roll(g): the criteria rollup → verdict + tone. Goal status is derived, never
  // stored. No northstar OR no criteria ⇒ undefined; flagged review.state="review"
  // ⇒ awaiting your sign-off; any waiting-on-you ⇒ waiting; all met ⇒ Done; any
  // blocked ⇒ Blocked; else on track.
  // sign-off (TASK-157) and waiting-on-you are checked BEFORE blocked deliberately:
  // this is the operator's front door, so a goal with something for *them* (a
  // pending sign-off, or a waiting criterion) leads with that call to action;
  // blocked (the agents' to clear) surfaces only when nothing waits on the operator.
  // home.jsx's goalRoll() mirrors this order exactly — keep the two in lockstep.
  function roll(g) {
    const crits = (g && g.criteria) || [];
    const met = crits.filter((c) => c.status === "met").length;
    const waiting = crits.filter((c) => c.status === "waiting-on-you").length;
    const blocked = crits.some((c) => c.status === "blocked");
    const total = crits.length;
    const undef = !g || !g.northstar || total === 0;
    const signoff = !!g && g.review === "review";
    let verdict, tone;
    if (undef) { verdict = "Not yet defined"; tone = "t-waiting"; }
    else if (signoff) { verdict = "Awaiting your sign-off"; tone = "t-waiting"; }
    else if (waiting) { verdict = waiting + (waiting > 1 ? " criteria" : " criterion") + " waiting on you"; tone = "t-waiting"; }
    else if (met === total) { verdict = "Done"; tone = "t-met"; }
    else if (blocked) { verdict = "Blocked"; tone = "t-blocked"; }
    else { verdict = "On track — nothing needs you"; tone = "t-met"; }
    return { met, waiting, total, undef, verdict, tone };
  }

  // a goal needs the operator when it's undefined, has any waiting-on-you, has any
  // blocked criterion, OR is flagged review.state="review" awaiting their sign-off
  // (TASK-157) — the same split the Portfolio groups on.
  function needsYou(g) {
    const r = roll(g);
    return r.undef || r.waiting > 0 || g.review === "review" || (g.criteria || []).some((c) => c.status === "blocked");
  }

  /* ---------- L1 · Portfolio ---------- */
  function Card({ g, onOpen, renderWiki }) {
    const r = roll(g);
    const crits = g.criteria || [];
    const rw = renderWiki || ((t) => t);
    return (
      <div className="dxg-card" role="button" tabIndex={0}
        onClick={() => onOpen && onOpen(g.id)}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen && onOpen(g.id); } }}>
        <div className="dxg-card-top">
          <span className="dxg-card-name">{g.name}</span>
          {g.stream && <span className="dxg-card-stream">{g.stream}</span>}
          <span className={"dxg-verdict " + r.tone}>{r.verdict}</span>
        </div>
        <div className={"dxg-northstar" + (r.undef ? " is-undef" : "")}>{g.northstar ? rw(g.northstar) : "No north star yet — what does success look like?"}</div>
        <div className="dxg-rollup">
          <div className="dxg-segs">
            {crits.map((c, i) => <span className="dxg-seg" key={i} style={{ background: "var(--" + segVar(c.status) + ")" }} />)}
            {r.undef && crits.length === 0 && <span className="dxg-seg is-empty" />}
          </div>
          <span className="dxg-rollup-txt">{r.total === 0 ? "0 criteria defined" : r.met + " of " + r.total + " met"}</span>
        </div>
      </div>);
  }

  // status → the CSS custom-property name for the segment fill colour.
  function segVar(s) {
    return s === "met" ? "met"
      : s === "in-progress" ? "prog"
      : s === "waiting-on-you" ? "wait"
      : s === "blocked" ? "blk"
      : "todo";
  }

  function Portfolio({ goals, onOpen, renderWiki }) {
    const needs = goals.filter(needsYou);
    const moving = goals.filter((g) => !needsYou(g));
    return (
      <div className="dxg-scroll"><div className="dxg-col">
        <header className="dxg-phead">
          <h1 className="dxg-h1">Goals</h1>
          <span className="dxg-psub">{needs.length} of {goals.length} need something from you · working backwards from each deliverable</span>
        </header>

        {needs.length > 0 && (
          <React.Fragment>
            <div className="dxg-group-lbl">Needs your attention</div>
            <div className="dxg-cards">{needs.map((g) => <Card g={g} onOpen={onOpen} renderWiki={renderWiki} key={g.id} />)}</div>
          </React.Fragment>
        )}
        {moving.length > 0 && (
          <React.Fragment>
            <div className="dxg-group-lbl">Moving on its own</div>
            <div className="dxg-cards">{moving.map((g) => <Card g={g} onOpen={onOpen} renderWiki={renderWiki} key={g.id} />)}</div>
          </React.Fragment>
        )}
      </div></div>);
  }

  /* ---------- L2 · Goal detail ---------- */
  function Criterion({ c, onOpenArtifact, renderWiki }) {
    const s = stat(c.status);
    const evidence = c.evidence || [];
    const rw = renderWiki || ((t) => t);
    return (
      <div className="dxg-crit">
        <span className={"dxg-crit-icon " + s.tone}>{s.glyph}</span>
        <div className="dxg-crit-main">
          <div className="dxg-crit-text">{c.text ? rw(c.text) : c.text}</div>
          <div className="dxg-crit-evi">
            {evidence.length
              ? evidence.map((e, i) => (
                  <button className="dxg-chip" key={i} type="button"
                    onClick={() => onOpenArtifact && onOpenArtifact(e.name)}
                    title={(e.kind === "proof" ? "proof · " : "related · ") + e.name}>{e.name}</button>))
              : <span className="dxg-noevi">— no work yet</span>}
          </div>
        </div>
        <div className="dxg-crit-right">
          <span className={"dxg-crit-status " + s.tone}>{s.label}</span>
          {c.owner && <span className="dxg-crit-owner"><Avatar name={c.owner} kind="agent" size={20} /></span>}
        </div>
      </div>);
  }

  /* ---------- sign-off (TASK-157) ----------
     A goal flagged review.state="review" is awaiting the operator's sign-off. This
     bar — shown atop the goal detail — carries the verdict, using the SAME review
     primitive as any artifact (onSetReview → POST /api/artifacts/goal.<id>/review):
       review   → "your turn": Approve / Request changes;
       changes  → "the agent's turn": a calm note, no buttons;
       approved → settled: a Reopen affordance.
     Approving clears review.state, which drops the goal from the needs-you / review
     queue (the app re-derives goals off the refreshed record). */
  function SignOff({ g, onSetReview }) {
    const name = "goal." + g.id;
    const st = g.review;
    const [note, setNote] = useState("");
    if (st === "approved") {
      return (
        <div className="dxg-signoff is-approved">
          <span className="dxg-signoff-ic">✓</span>
          <span className="dxg-signoff-txt">You signed off on this goal — it's settled.</span>
          <button className="dxg-signoff-reopen" onClick={() => onSetReview(name, "review")}>Reopen</button>
        </div>);
    }
    if (st === "changes") {
      return (
        <div className="dxg-signoff is-changes">
          <span className="dxg-signoff-ic">↩</span>
          <span className="dxg-signoff-txt">You requested changes — it's back with the agent now.</span>
        </div>);
    }
    // "review" — awaiting the operator's sign-off. The feedback field (TASK-154)
    // carries the WHAT to the agent on the goal's companion topic: REQUIRED to request
    // changes (a bare "changes" with no note is useless — Lena's catch), optional to
    // approve. The full goal discussion thread is the rest of TASK-154.
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

  function Detail({ g, onBack, onOpenArtifact, onSetReview, renderWiki }) {
    const r = roll(g);
    const crits = g.criteria || [];
    const rw = renderWiki || ((t) => t);
    const showSignOff = onSetReview && (g.review === "review" || g.review === "changes" || g.review === "approved");
    return (
      <div className="dxg-detail">
        <div className="dxg-topbar">
          <button className="dxg-back" onClick={onBack}>← Goals</button>
          {g.stream && <span className="dxg-topbar-stream">{g.stream}</span>}
          {!r.undef && <span className="dxg-topbar-roll">{r.met} of {r.total} met</span>}
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
                ? crits.map((c, i) => <Criterion c={c} onOpenArtifact={onOpenArtifact} renderWiki={renderWiki} key={c.id || i} />)
                : <div className="dxg-noevi" style={{ padding: "14px 0" }}>No criteria yet — this goal hasn't been broken down into checkable outcomes.</div>}
            </div>

            <div className="dxg-maintained">Criteria are the contract every small decision below is judged against. Status is kept current on the bus by the agents doing the work.</div>
          </div>
        </div>
      </div>);
  }

  /* ---------- empty state ---------- */
  function Empty() {
    return (
      <div className="dxg-scroll"><div className="dxg-col">
        <header className="dxg-phead">
          <h1 className="dxg-h1">Goals</h1>
        </header>
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
  // GoalsView holds the L1↔L2 selection. openGoal=null shows the Portfolio; an id
  // shows that goal's Detail. initialGoalId (TASK-157) deep-links straight to a
  // goal's detail — set when opened from the needs-you queue (GoalsView remounts on
  // each Goals nav, so the prop seeds the initial selection). onOpenArtifact opens an
  // evidence artifact in the review stage; onSetReview persists a goal sign-off
  // verdict (the same review primitive the rest of the dash uses).
  function GoalsView({ goals, initialGoalId, onOpenArtifact, onSetReview, renderWiki }) {
    const [openGoal, setOpenGoal] = useState(initialGoalId || null);
    const list = goals || [];
    if (list.length === 0) return <Empty />;
    const g = openGoal && list.find((x) => x.id === openGoal);
    if (g) return <Detail g={g} onBack={() => setOpenGoal(null)} onOpenArtifact={onOpenArtifact} onSetReview={onSetReview} renderWiki={renderWiki} />;
    return <Portfolio goals={list} onOpen={(id) => setOpenGoal(id)} renderWiki={renderWiki} />;
  }

  Object.assign(window, { GoalsView });
})();
