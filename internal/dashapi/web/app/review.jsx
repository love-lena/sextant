/* review.jsx — the Review / DocumentView: an artifact under review, rendered as
   flow2's DocumentView (TASK Track 1, stage c). Extracted from app.jsx into its
   own file (Lena's per-view split), like artifacts.jsx / home.jsx.

   Wired to the REAL review primitives (no fake state):
     - the doc       → MarkdownArtifact (artifact.jsx, looked up as a global) renders
                        the live artifact body + the TASK-79 staleness badge;
     - the verdict   → setReview(name, state) persists the review-state primitive
                        (POST /api/artifacts/{name}/review) + posts a companion-topic
                        event. Approve→approved, Request changes→changes;
     - the comments  → the companion-topic discussion (msg.topic.artifact.<name>), a
                        FLAT thread (the `discussion` list), with sendDiscussion()
                        posting the composer's text. (Flat for Track 1 — not per-block
                        inline-anchored marks; those need a comment primitive later.)

   The right rail is resizable (drag handle on its left edge) + collapsible (a
   toggle), its width persisted in localStorage (TASK-141). After a verdict the
   ReviewDone consequence note shows the artifact's own resulting state (Goals are
   Track 2, so no criteria are referenced). Exports ReviewView + ReviewDone to
   window. */
(function () {
  const { useState, useRef, useEffect, useCallback } = React;

  // review-state → the doc's "why you're seeing this" line + the byline verb. These
  // describe the artifact's OWN state (no goals/criteria — Track 2). `review` and
  // `changes` are the "waiting on you" states the dash routes to your inbox.
  const WHY = {
    review:  "Flagged for your review — an agent marked it ready and it's waiting on you.",
    changes: "You requested changes — it's with the author now, and returns to your inbox once they revise and re-flag it.",
    approved: "You approved this. It's settled; reopen it if something changed.",
    rejected: "You rejected this. It's settled; reopen it to put it back in review.",
    archived: "Archived — kept for the record, off your active list. Reopen to revisit.",
    draft:   "A working draft. No one has asked for your review yet.",
  };
  const VERB = {
    review:  "marked this ready for you",
    changes: "is revising this after your notes",
    approved: "wrote this",
    rejected: "wrote this",
    archived: "wrote this",
    draft:   "is still drafting this",
  };

  // ReviewDone — the consequence panel shown right after a verdict. It speaks only
  // to the artifact's own resulting state (approved / changes / comment); Goals are
  // Track 2 so there's no criterion-met language here (mirrors flow-ext's ReviewDone
  // minus the criteria branch).
  function ReviewDone({ verdict, name, author, onClose }) {
    let tone, glyph, title, line;
    if (verdict === "approved") {
      tone = "met"; glyph = "✓";
      title = "Approved.";
      line = <><b>{name}</b> is marked approved. It's settled and off your plate.</>;
    } else if (verdict === "changes") {
      tone = "waiting"; glyph = "↩";
      title = "Changes requested — sent back.";
      line = <>{author ? <b>{author}</b> : "The agent"} will revise <b>{name}</b>; it returns to your inbox as a new version. Off your plate for now.</>;
    } else {
      tone = "progress"; glyph = "✑";
      title = "Comment posted.";
      line = <>Your note is on <b>{name}</b>'s thread. It stays in your inbox — you haven't decided yet.</>;
    }
    const C = { met: "var(--met)", waiting: "var(--wait)", progress: "var(--prog)" }[tone];
    return (
      <div className="fx-done">
        <div className="fx-done-card fx-in">
          <span className="fx-done-icon" style={{ color: C, background: "color-mix(in srgb," + C + " 12%,transparent)" }}>{glyph}</span>
          <h2 className="fx-done-title">{title}</h2>
          <p className="fx-done-line">{line}</p>
          <div className="fx-done-actions">
            <button className="fx-submit" onClick={onClose}>Close</button>
          </div>
        </div>
      </div>);
  }

  // ReviewView — the artifact-under-review stage. Props are wired from app.jsx to the
  // existing primitives (no new data layer):
  //   artifact   {name, version, status, ...}  (the component-shape artifact)
  //   record     the live artifact Record (body/title/review) for MarkdownArtifact
  //   discussion the flat companion-topic thread (newest last)
  //   draft/setDraft  the shared composer buffer
  //   onSetReview(name, state)  → setReview (the verdict primitive)
  //   onSendComment()           → sendDiscussion (post the composer to the companion topic)
  //   onExpandDiscussion  navigation handler (pop the thread out as a conversation)
  //   onClose    dismiss the review (the modal host's close) — the review's own
  //              exit affordance, since the review now lives in a dismissible modal
  //              (× · scrim · Esc) rather than a full-stage takeover, so the in-view
  //              "← Artifacts" became a plain "Close" (no "back to X" navigation).
  //   railWidth / railCollapsed / onRailWidth / onToggleRail  the resizable rail (TASK-141)
  //   onOpenArtifact / artifactNames  passed through to MarkdownArtifact so body
  //                                   [[wikilinks]] resolve + open in-dash
  function ReviewView(props) {
    const {
      artifact, record, discussion, draft, setDraft,
      onSetReview, onSendComment, onExpandDiscussion, onClose,
      railWidth, railCollapsed, onRailWidth, onToggleRail,
      onOpenArtifact, artifactNames,
    } = props;

    const name = artifact.name;
    const status = artifact.status;
    const reviewRev = (record && record.review && record.review.rev) || 0;
    const author = (artifact.author && artifact.author.name) || "";

    // a verdict closes the loop into the ReviewDone consequence note; cleared when
    // the open artifact changes (a fresh open should show the doc, not the last note).
    const [done, setDone] = useState(null); // null | "approved" | "changes" | "comment"
    useEffect(() => { setDone(null); }, [name]);

    // the verdict the composer's Submit will apply; the Comment option just posts
    // the thread comment without changing the review-state.
    const [verdict, setVerdict] = useState("approved");
    const VOPTS = [["comment", "Comment"], ["approved", "Approve"], ["changes", "Request changes"]];

    // keep the comments list pinned to the newest comment (flat thread, newest last).
    const commentsRef = useRef(null);
    useEffect(() => { const el = commentsRef.current; if (el) el.scrollTop = el.scrollHeight; }, [discussion.length]);

    // rail resize: drag the handle on the rail's left edge; width is clamped and
    // persisted by the parent (mirrors the dark-mode / sx-disc-split persistence).
    const draggingRef = useRef(false);
    // holds the active drag's teardown so an unmount mid-drag can't leak the
    // document-level listeners (codex flag).
    const endDragRef = useRef(null);
    const onHandleDown = useCallback((e) => {
      e.preventDefault();
      draggingRef.current = true;
      const startX = e.clientX;
      const startW = railWidth;
      const move = (ev) => {
        if (!draggingRef.current) return;
        // the rail is on the RIGHT, so dragging the handle LEFT (smaller clientX) widens it.
        const next = startW + (startX - ev.clientX);
        onRailWidth(next);
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
    }, [railWidth, onRailWidth]);
    // tear down a drag still in progress if the view unmounts.
    useEffect(() => () => { if (endDragRef.current) endDragRef.current(); }, []);

    const submit = useCallback(() => {
      if (verdict === "comment") {
        // a blank comment is a no-op — don't flash the "comment posted" note.
        if (draft.trim()) { onSendComment(); setDone("comment"); }
        return;
      }
      // an attached comment rides along with the verdict (posted to the same thread).
      if (draft.trim()) onSendComment();
      onSetReview(name, verdict);
      setDone(verdict);
    }, [verdict, draft, name, onSendComment, onSetReview]);

    if (done) return <ReviewDone verdict={done} name={name} author={author} onClose={onClose} />;

    const settled = status === "approved" || status === "rejected" || status === "archived";
    const why = WHY[status] || WHY.draft;
    const verb = VERB[status] || VERB.draft;
    const updated = artifact.updated;

    return (
      <div className={"fx-docwrap" + (railCollapsed ? " rail-hidden" : "")}>
        <div className="fx-doccol">
          <div className="fx-topbar">
            <button className="fx-back" onClick={onClose}>‹ Close</button>
            <span className="fx-top-tag">{STATUS_LABEL(status).toLowerCase()}</span>
            <button className="fx-top-right" title={railCollapsed ? "Show the comments rail" : "Hide the comments rail"} onClick={onToggleRail}>
              {railCollapsed ? "Comments ↤" : "Hide rail ↦"}
            </button>
          </div>
          <div className="fx-docinner">
            <div className="fx-crumbs">
              <span className="fx-type">Artifact</span>
              <span className="fx-stream">{name}</span>
              <span className="fx-mono" style={{ color: "var(--fx-ink3)" }}>rev {artifact.version}</span>
            </div>
            <h1 className="fx-title">{(record && record.title) || name}</h1>
            <div className="fx-byline">
              {author && <window.Avatar name={author} kind={(artifact.author && artifact.author.kind) || "agent"} size={22} />}
              <span>{author ? <><b>{author}</b> {verb}</> : <>An agent {verb}</>}</span>
              {updated && <><span className="fx-sep">·</span><span className="fx-mono">updated {updated} ago</span></>}
              {status === "approved" && reviewRev > 0 && <><span className="fx-sep">·</span><span className="fx-mono">approved at rev {reviewRev}</span></>}
            </div>
            <div className="fx-why">
              <span className="fx-why-lbl">Why you're seeing this</span>
              <span className="fx-why-text">{why}</span>
            </div>
            <div className="fx-docmd">
              <window.MarkdownArtifact record={record} name={name} revision={artifact.version} onOpenArtifact={onOpenArtifact} artifactNames={artifactNames} />
            </div>
          </div>
        </div>

        {!railCollapsed && (
          <aside className="fx-rail" style={{ width: railWidth, flexBasis: railWidth }}>
            <div className="fx-rail-resize" onMouseDown={onHandleDown} title="Drag to resize" />
            <div className="fx-rail-head">
              Discussion <span className="fx-rail-count">· {discussion.length}</span>
              <button className="fx-rail-pop" title="Open the full discussion as a conversation" onClick={() => onExpandDiscussion(name)}>↗</button>
            </div>
            <div className="fx-rail-body" ref={commentsRef}>
              {/* current-status header — one-glance context before scrolling the history */}
              <div className="fx-rail-status-bar">
                <span className={"fx-chip-status " + STATUS_TONE(status)}>{STATUS_LABEL(status)}</span>
                <span className="fx-rail-status-note">
                  {settled
                    ? "Settled — reopening puts it back in review."
                    : "Live — your verdict updates the state on the bus."}
                </span>
              </div>

              {discussion.length === 0 && <div className="fx-noevi" style={{ padding: "4px 2px 14px" }}>No activity yet — comments and status changes will appear here.</div>}

              {/* unified timeline: status-change events inline with plain comments (oldest first) */}
              {discussion.map((c) => {
                if (c.review && c.review.state) {
                  // status-change event: render as an inset timeline entry with a chip
                  const evState = c.review.state;
                  const evRev   = c.review.rev;
                  return (
                    <div className="fx-timeline-event" key={c.id}>
                      <div className="fx-timeline-event-line" />
                      <div className="fx-timeline-event-body">
                        <span className={"fx-chip-status " + STATUS_TONE(evState)}>{STATUS_LABEL(evState)}</span>
                        <span className="fx-timeline-event-text">
                          <b>{c.self ? "you" : c.author}</b>
                          {" "}{c.text}
                          {evRev != null ? <span className="fx-mono"> · rev {evRev}</span> : null}
                        </span>
                        {c.time && <span className="fx-event-time">{c.time}</span>}
                      </div>
                    </div>
                  );
                }
                // plain comment
                return (
                  <div className="fx-comment" key={c.id}>
                    <div className="fx-comment-head">
                      <window.Avatar name={c.author} kind={c.role === "agent" ? "agent" : "human"} size={18} />
                      <b>{c.self ? "you" : c.author}</b>
                      {c.role === "agent" && !c.self && <span className="fx-comment-tag">agent</span>}
                      <span className="fx-comment-time">{c.time}</span>
                    </div>
                    <p className="fx-comment-text">{c.text}</p>
                  </div>
                );
              })}
            </div>

            <div className="fx-review">
              <div className="fx-review-head">{settled ? "This artifact is " + STATUS_LABEL(status).toLowerCase() + "." : "Your call on this artifact."}</div>
              <textarea
                className="fx-rinput"
                rows={2}
                placeholder={verdict === "comment" ? "Leave a comment…" : "Add a note with your verdict (optional)…"}
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); submit(); } }}
              />
              {settled ? (
                <button className="fx-submit" onClick={() => onSetReview(name, "review")}>Reopen for review</button>
              ) : (
                <>
                  <div className="fx-verdict">
                    {VOPTS.map(([k, lbl]) => (
                      <span key={k} className={"fx-vopt" + (verdict === k ? " is-on" : "")} onClick={() => setVerdict(k)}>{lbl}</span>
                    ))}
                  </div>
                  <button className="fx-submit" onClick={submit}>
                    {verdict === "comment" ? "Post comment" : verdict === "approved" ? "Approve" : "Request changes"}
                  </button>
                </>
              )}
            </div>
          </aside>
        )}
      </div>);
  }

  // status → flow2 status-chip tone + label (the v0.5 token scale), used by the rail's
  // activity chip. Kept local so review.jsx doesn't depend on a sidebar export beyond
  // Avatar + MarkdownArtifact.
  function STATUS_TONE(st) {
    // review (your turn) = the attention tone; changes (the author's turn) = the calmer
    // progress tone, so "waiting on you" reads distinctly from "waiting on the agent".
    return { review: "t-waiting", changes: "t-progress", approved: "t-met", rejected: "t-blocked", archived: "t-todo", draft: "t-todo" }[st] || "t-todo";
  }
  function STATUS_LABEL(st) {
    return { review: "Needs review", changes: "Waiting for author", approved: "Approved", rejected: "Rejected", archived: "Archived", draft: "Draft" }[st] || "Draft";
  }

  Object.assign(window, { ReviewView, ReviewDone });
})();
