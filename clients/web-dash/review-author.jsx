/* review-author.jsx — the review lane (EPIC B): the PR-style Brief reader
   (TASK-208), the Review consequence screen (TASK-209) and the Link-a-workstream
   flow (TASK-210).

   A brief is reviewed like a pull request: the decision is earned at the END of
   reading, never from a preview (§13 quick-decision is intentionally NOT built).
   Two columns — document left, comments rail right — with inline comment marks
   that toggle the rail, a five-verb review action, an activity log, and a
   collapsible rail (sextant.rail.collapsed.v1).

   The five verbs: Approve · Request revisions · Request answers · Reject ·
   Ignore. Submitting any verb routes to the Review consequence (TASK-209), a
   DISPLAY-ONLY screen: it renders the honest consequence + the monospace
   transition line. The actual mutation (criterion→met, goal rollup, run resume)
   is owned by the live-state model (TASK-216) — this lane reflects it, never
   writes it. The verdict itself is emitted ONCE here as a durable message on the
   brief's topic.

   No personas — a byline / author is a run ULID or a bus identity, never a
   person or avatar.

   Exports: BriefReader, ReviewConsequence, LinkWorkstream to window. */
(function () {
  const { useState, useEffect, useRef, useMemo, useCallback } = React;

  const RAIL_COLLAPSED_KEY = "sextant.rail.collapsed.v1";

  // the five verbs (S12.5) — value, label, tone, and the consequence tone the
  // §15 screen renders.
  const VERBS = [
    { id: "approve",  label: "Approve",            tone: "met" },
    { id: "revisions", label: "Request revisions", tone: "waiting" },
    { id: "answers",  label: "Request answers",    tone: "progress" },
    { id: "reject",   label: "Reject",             tone: "blocked" },
    { id: "ignore",   label: "Ignore",             tone: "todo" },
  ];
  const TONE_VAR = { met: "var(--met)", waiting: "var(--wait)", progress: "var(--prog)", blocked: "var(--blk)", todo: "var(--todo)" };

  // a type-specific framing prompt for the decision (S12.5). Keyed by the brief's
  // type label; falls back to a generic line.
  function decisionPrompt(type) {
    const t = (type || "").toLowerCase();
    if (t.indexOf("plan") >= 0 || t.indexOf("design") >= 0) return "This is a checkpoint — the run pauses here. Approving lets it proceed; requesting revisions sends it back to replan.";
    if (t.indexOf("proof") >= 0 || t.indexOf("result") >= 0) return "This brief claims a criterion is met. Approving advances the criterion and resumes the run.";
    if (t.indexOf("blocked") >= 0) return "The run says it's blocked. Answer its questions to unblock it, or reject the direction.";
    return "Read the brief, then decide. The verdict is earned at the end — there's no decision from a preview.";
  }

  function relAgo(ms) {
    if (!ms) return "";
    const s = Math.max(0, (Date.now() - ms) / 1000);
    if (s < 60) return Math.floor(s) + "s ago";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
  }
  function shortId(id) { return (id || "").length > 12 ? (id.slice(0, 6) + "…" + id.slice(-4)) : (id || ""); }

  /* ====================================================================== *
   *  TASK-208 — Brief reader (PR-style)                                    *
   * ====================================================================== */
  // brief is the shaped record the reader renders (built by app.jsx from the
  // artifact + its companion thread). Shape (all optional, degrades gracefully):
  //   { name, runId, goal, type, stream, title, authorRun, revisedAfterNotes,
  //     readEffort, why, plan:[...], blocks:[{id,text,note?}], comments:[...],
  //     activity:[...], resolved? }
  function BriefReader(props) {
    const { brief, onSubmitVerdict, onReply, collapsed, onToggleRail, onBack } = props;
    const [activeComment, setActiveComment] = useState(null); // anchor id with an open rail comment
    const [replyDraft, setReplyDraft] = useState({}); // commentId -> text
    const [verb, setVerb] = useState(null);
    const [note, setNote] = useState("");
    const railRef = useRef(null);

    const b = brief || {};
    const blocks = b.blocks || [];
    const comments = b.comments || [];
    const activity = b.activity || [];

    // map a block's note to its rail comment (S12.3): clicking a mark toggles the
    // associated comment active.
    function markFor(block) { return (comments.find((c) => c.anchor === block.id) || {}).id || null; }
    function onMark(block) {
      const cid = markFor(block);
      if (!cid) return;
      setActiveComment((cur) => (cur === cid ? null : cid));
      // scroll the rail to the comment.
      requestAnimationFrame(() => { const el = railRef.current && railRef.current.querySelector('[data-cid="' + cid + '"]'); if (el) el.scrollIntoView({ block: "center", behavior: "smooth" }); });
    }

    function submit() {
      if (!verb) return;
      onSubmitVerdict({ verb, note: note.trim(), brief: b });
    }

    return (
      <div className={"br-wrap" + (collapsed ? " is-collapsed" : "")}>
        {/* ---- document column ---- */}
        <div className="br-doc">
          <div className="br-topbar">
            {onBack && <button className="sx-back" onClick={onBack}><span className="sx-back-ic">←</span><span className="sx-back-lbl">Inbox</span></button>}
            <span className="br-top-run">run <span className="mono">{shortId(b.runId) || "—"}</span></span>
            {b.goal && <><span className="br-top-sep">·</span><span className="br-top-goal">◎ {b.goal}</span></>}
          </div>

          <div className="br-paper">
            <span className={"br-typelabel"}>{(b.type || "brief").toUpperCase()}</span>
            {b.stream && <span className="br-stream">{b.stream}</span>}
            <h1 className="br-title">{b.title || b.name || "Untitled brief"}</h1>
            <div className="br-byline">
              authored by run <span className="mono">{shortId(b.authorRun || b.runId) || "—"}</span>
              {b.revisedAfterNotes ? <span className="br-revised"> · revised after your notes</span> : null}
              {b.readEffort ? <span className="br-effort"> · {b.readEffort} read</span> : null}
            </div>

            {/* why-you're-seeing-this callout (S12.2) */}
            <div className="br-why">
              <span className="br-why-ic">✦</span>
              <span>{b.why || "This brief is waiting on you — a run posted it and stopped at this checkpoint."}</span>
            </div>

            {/* optional numbered plan (S12.2) */}
            {Array.isArray(b.plan) && b.plan.length > 0 && (
              <div className="br-plan">
                <div className="br-plan-h">Plan</div>
                <ol className="br-plan-ol">{b.plan.map((p, i) => <li key={i}>{p}</li>)}</ol>
              </div>
            )}

            {/* body: an HTML brief renders as one sanitized region (TASK-222);
                markdown/plaintext keep the per-block path with inline marks. */}
            {b.format === "html" ? (
              <div className="br-body">
                <div className="br-html-body"
                  dangerouslySetInnerHTML={{ __html: (window.renderArtifactBody ? window.renderArtifactBody(b.body || "", "html") : "") }} />
              </div>
            ) : (
              <div className="br-body">
                {blocks.length === 0 && <p className="br-block">{b.body || "No body — this brief has a headline only."}</p>}
                {blocks.map((blk) => {
                  const cid = markFor(blk);
                  return (
                    <div className={"br-block" + (cid && activeComment === cid ? " is-active" : "")} key={blk.id}>
                      <span className="br-block-text">{blk.text}</span>
                      {cid && (
                        <button className="br-mark" title="Show comment" onClick={() => onMark(blk)}>💬</button>
                      )}
                    </div>
                  );
                })}
              </div>
            )}

            {/* Activity log (S12.7) */}
            <div className="br-activity">
              <div className="br-activity-h">Activity</div>
              {activity.length === 0 ? <div className="br-activity-empty">No events yet.</div> : (
                <ul className="br-activity-list">
                  {activity.map((e, i) => (
                    <li className="br-activity-item" key={i}>
                      <span className={"br-act-kind br-act-" + (e.kind || "event")}>{e.kind || "event"}</span>
                      <span className="br-act-text">{e.text}</span>
                      <span className="br-act-meta">{e.source ? <span className="mono">{shortId(e.source)}</span> : null}{e.ts ? " · " + relAgo(e.ts) : ""}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>

        {/* ---- comments rail (collapsible to a spine, S12.6) ---- */}
        {collapsed ? (
          <button className="br-rail-spine" title="Show comments" onClick={onToggleRail}>
            <span className="br-spine-ic">‹</span>
            <span className="br-spine-lbl">Comments{comments.length ? " · " + comments.length : ""}</span>
          </button>
        ) : (
          <aside className="br-rail" ref={railRef}>
            <div className="br-rail-h">
              <span>Comments</span>
              <button className="br-rail-collapse" title="Collapse" onClick={onToggleRail}>›</button>
            </div>
            <div className="br-rail-body">
              {comments.length === 0 ? (
                <div className="br-rail-empty">No comments yet.</div>
              ) : comments.map((c) => (
                <div className={"br-comment" + (activeComment === c.id ? " is-active" : "")} key={c.id} data-cid={c.id}
                  onClick={() => setActiveComment(c.id)}>
                  {c.anchor && c.quote && <div className="br-comment-quote">“{c.quote}”</div>}
                  <div className="br-comment-head">
                    <span className="br-comment-author mono">{shortId(c.author)}</span>
                    <span className="br-comment-time">{relAgo(c.ts)}</span>
                  </div>
                  <div className="br-comment-text">{c.text}</div>
                  {(c.replies || []).map((r, i) => (
                    <div className="br-reply" key={i}><span className="br-reply-who">You</span> {r.text}</div>
                  ))}
                  <div className="br-reply-box">
                    <input className="sx-input br-reply-input" placeholder="Reply…" value={replyDraft[c.id] || ""}
                      onChange={(e) => setReplyDraft((d) => Object.assign({}, d, { [c.id]: e.target.value }))}
                      onKeyDown={(e) => { if (e.key === "Enter" && (replyDraft[c.id] || "").trim()) { e.preventDefault(); onReply && onReply(c.id, replyDraft[c.id].trim()); setReplyDraft((d) => Object.assign({}, d, { [c.id]: "" })); } }} />
                  </div>
                </div>
              ))}
            </div>

            {/* rail footer: the review action (S12.5) */}
            <div className="br-review">
              {b.resolved ? (
                <div className="br-resolved">
                  <span className="br-resolved-ic">✓</span>
                  <span>Closed — you {b.resolved.verb} this. <span className="br-resolved-time">{relAgo(b.resolved.ts)}</span></span>
                </div>
              ) : (
                <>
                  <div className="br-review-prompt">{decisionPrompt(b.type)}</div>
                  <textarea className="sx-input br-review-note" rows={2} placeholder="Add a note for the run (optional)…" value={note} onChange={(e) => setNote(e.target.value)} />
                  <div className="br-verbs">
                    {VERBS.map((v) => (
                      <button key={v.id} className={"br-verb" + (verb === v.id ? " is-sel" : "")}
                        style={verb === v.id ? { borderColor: TONE_VAR[v.tone], color: TONE_VAR[v.tone] } : undefined}
                        onClick={() => setVerb(v.id)}>{v.label}</button>
                    ))}
                  </div>
                  <button className="fx-submit br-submit" disabled={!verb} onClick={submit}>Submit verdict</button>
                </>
              )}
            </div>
          </aside>
        )}
      </div>
    );
  }

  /* ====================================================================== *
   *  TASK-209 — Review consequence (DISPLAY ONLY)                          *
   *  Renders the honest consequence of a verdict + the monospace           *
   *  transition line. Never performs the mutation (owned by TASK-216).     *
   * ====================================================================== */
  // verdict: { verb, note, brief } as emitted by the reader. transition is the
  // live-state read-back (from app.jsx, reflecting TASK-216): { line, criterion,
  // from, to, goalMoved, runResumes } — null when no criterion was linked.
  function ReviewConsequence({ verdict, transition, onBack, onSeeGoal }) {
    const verb = verdict && verdict.verb;
    const b = (verdict && verdict.brief) || {};
    const linked = !!(transition && transition.criterion);

    // honest consequence copy per verb (S15.1–15.4). runResumes/criterion advance
    // come from `transition` (TASK-216's read-back), never asserted by this screen.
    let tone, glyph, title, lines, resumes = false, advanced = false;
    if (verb === "approve" || verb === "answers") {
      tone = "met"; glyph = "✓";
      if (linked) {
        advanced = true; resumes = transition.runResumes !== false;
        title = verb === "answers" ? "Answered — criterion advanced." : "Approved — criterion advanced.";
        lines = [
          <>The goal rollup moved and the run resumed{transition.goalMoved === false ? " (rollup unchanged)" : ""}. It runs the remaining steps and returns only at another checkpoint.</>,
        ];
      } else {
        resumes = true;
        title = verb === "answers" ? "Answered — run continues." : "Approved — run continues.";
        lines = [<>The run picks up from this checkpoint, runs its remaining steps, ends at its stopping brief, and returns only at another checkpoint.</>];
      }
    } else if (verb === "revisions") {
      tone = "waiting"; glyph = "↩";
      title = "Revisions requested — sent back.";
      lines = [<>The run revises against your notes and returns to your inbox as a <b>new version</b>. Nothing is marked met.</>];
    } else if (verb === "reject") {
      tone = "blocked"; glyph = "✕";
      title = "Rejected.";
      lines = [<>The run drops this direction. The criterion is unchanged.</>];
    } else { // ignore
      tone = "todo"; glyph = "—";
      title = "Set aside.";
      lines = [<>This brief is set aside. The criterion is unchanged; nothing resumes.</>];
    }

    const C = TONE_VAR[tone];
    // the monospace transition line (S15.2) — shown only when a criterion
    // actually advanced and TASK-216 reported the transition.
    const transLine = (advanced && transition && transition.line)
      ? transition.line
      : (advanced ? ("criterion · waiting-on-you → met") : null);

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light rc-wrap">
        <span className="fx-done-icon" style={{ color: C, background: "color-mix(in srgb," + C + " 12%,transparent)" }}>{glyph}</span>
        <h1 className="fx-h1" style={{ marginTop: 14 }}>{title}</h1>
        {b.title && <p className="rc-subject">on <b>{b.title}</b>{b.runId ? <> · run <span className="mono">{shortId(b.runId)}</span></> : null}</p>}
        {lines.map((l, i) => <p className="fx-psub rc-line" key={i} style={{ fontSize: 15 }}>{l}</p>)}

        {transLine && (
          <div className="rc-transition">
            <span className="rc-trans-label">transition</span>
            <code className="rc-trans-code">{transLine}</code>
          </div>
        )}

        {verdict && verdict.note && (
          <div className="rc-note"><span className="rc-note-label">your note to the run</span><div className="rc-note-text">{verdict.note}</div></div>
        )}

        <div className="rc-actions">
          {advanced && onSeeGoal && <button className="fx-submit" onClick={onSeeGoal}>See the goal →</button>}
          <button className={advanced ? "sa-ghost" : "fx-submit"} onClick={onBack}>Back to origin</button>
        </div>
        <p className="rc-honest">The verdict was emitted once; this screen reads the resulting state back from the bus — it holds no copy of run or criterion state.</p>
      </div></div>
    );
  }

  /* ====================================================================== *
   *  TASK-210 — Link a workstream to a criterion                           *
   *  Two-column many-to-many: target criterion left, candidate runs/       *
   *  workflows right; rows toggle +link ↔ ✓linked.                          *
   * ====================================================================== */
  // criterion: { id, text, goal, linked:[ids] }. candidates: [{id, kind, label,
  // meta}]. onToggleLink(candidateId, linked) writes the relation (relates) on
  // the artifact side; onBuildWorkflow opens the builder.
  function LinkWorkstream({ criterion, candidates, onToggleLink, onBuildWorkflow, onBack }) {
    const c = criterion || {};
    const [linked, setLinked] = useState(() => new Set(c.linked || []));
    const cands = candidates || [];

    function toggle(id) {
      setLinked((prev) => {
        const next = new Set(prev);
        const willLink = !next.has(id);
        willLink ? next.add(id) : next.delete(id);
        if (onToggleLink) onToggleLink(id, willLink);
        return next;
      });
    }

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light" style={{ maxWidth: 980 }}>
        <button className="sx-back" onClick={onBack}><span className="sx-back-ic">←</span><span className="sx-back-lbl">Goal</span></button>
        <h1 className="fx-h1" style={{ marginTop: 8 }}>Link work to this criterion</h1>
        <p className="fx-psub">Attach existing runs or workflows to the criterion they advance. A workstream can feed several criteria — linking is many-to-many.</p>

        <div className="lw-cols">
          {/* target criterion (left) */}
          <div className="lw-target">
            <div className="lw-col-h">Criterion</div>
            <div className="lw-crit-card">
              {c.goal && <div className="lw-crit-goal">◎ {c.goal}</div>}
              <div className="lw-crit-text">{c.text || "No criterion selected."}</div>
              <div className="lw-crit-linkn">{linked.size} workstream{linked.size === 1 ? "" : "s"} linked</div>
            </div>
          </div>

          {/* candidates (right) */}
          <div className="lw-cands">
            <div className="lw-col-h">Existing work</div>
            {cands.length === 0 ? (
              <div className="fx-stub" style={{ marginTop: 8 }}>
                <span className="fx-stub-ic">⬡</span>
                <div>
                  <div className="fx-stub-title">No runs or workflows yet.</div>
                  <div className="fx-stub-sub">Nothing on the bus to link. Build a workflow for this criterion to get started.</div>
                </div>
              </div>
            ) : (
              <div className="fx-list">
                {cands.map((w) => {
                  const on = linked.has(w.id);
                  return (
                    <div className={"lw-cand" + (on ? " is-linked" : "")} key={w.id}>
                      <span className="lw-cand-ic">{w.kind === "workflow" ? "⬡" : "▸"}</span>
                      <span className="fx-row-main">
                        <span className="fx-row-name">{w.label}</span>
                        <span className="fx-row-meta">{w.meta || w.kind}</span>
                      </span>
                      <button className={"lw-linkbtn" + (on ? " is-linked" : "")} onClick={() => toggle(w.id)}>{on ? "✓ linked" : "+ link"}</button>
                    </div>
                  );
                })}
              </div>
            )}
            <button className="sa-ghost" style={{ marginTop: 14 }} onClick={onBuildWorkflow}>⬡ Build a workflow for this →</button>
          </div>
        </div>
      </div></div>
    );
  }

  Object.assign(window, { BriefReader, ReviewConsequence, LinkWorkstream, _reviewAuthor: { RAIL_COLLAPSED_KEY, VERBS } });
})();
