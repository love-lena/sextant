/* composer.jsx — the authoring lane (EPIC B): the Artifacts surface (TASK-205),
   the Composer writing surface (TASK-206) and the Criteria proposal (TASK-207).

   These are the operator's WRITE surfaces. A draft is the operator's alone until
   they mark it ready (it lives in localStorage under sextant.synth.drafts.v1, the
   design's stable key); mark-ready then files a durable artifact on the bus (or,
   for a charter, hands off to the criteria proposal which creates a goal). No
   personas anywhere — a byline is "You" or a ULID, never a name/avatar.

   Data layer: window.SX (app.jsx) gives create/update/getArtifact/publish over the
   one browser bus client (ADR-0044). Everything degrades gracefully when the bus
   is unreachable — the draft store is local and survives regardless.

   Exports: ArtifactsSurface, ComposerView, CriteriaProposal to window. */
(function () {
  const { useState, useEffect, useRef, useCallback, useMemo } = React;

  // ---- the local draft store (sextant.synth.drafts.v1) -------------------
  // A draft: { id, kind, title, sections:{north,vision,done}|{body}, seed,
  //   importMeta?, updated, ready }. The store is a flat { [id]: draft } map.
  const DRAFTS_KEY = "sextant.synth.drafts.v1";
  function loadDrafts() {
    try { return JSON.parse(localStorage.getItem(DRAFTS_KEY) || "{}") || {}; }
    catch (_) { return {}; }
  }
  function saveDrafts(map) {
    try { localStorage.setItem(DRAFTS_KEY, JSON.stringify(map)); } catch (_) {}
  }
  // a draft id is a timestamp + random tail — enough to be unique per operator
  // (not a bus ULID; these are local until filed).
  function newDraftId() { return "d-" + Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 6); }

  // the three seed shapes a Composer opens in (S16.1).
  const KIND_LABEL = { note: "Note", charter: "Charter", import: "Imported file" };
  function blankDraft(kind, extra) {
    const base = { id: newDraftId(), kind, title: "", updated: Date.now(), ready: false };
    if (kind === "charter") base.sections = { north: "", vision: "", done: "" };
    else base.sections = { body: "" };
    return Object.assign(base, extra || {});
  }

  function relAgo(ms) {
    if (!ms) return "";
    const s = Math.max(0, (Date.now() - ms) / 1000);
    if (s < 5) return "just now";
    if (s < 60) return Math.floor(s) + "s ago";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
  }
  function kindIcon(kind) { return kind === "charter" ? "◎" : kind === "import" ? "⇪" : "❡"; }

  /* ====================================================================== *
   *  TASK-205 — Artifacts surface                                          *
   *  Your drafts (local, operator-only) + Filed (artifacts a run brought   *
   *  back), with Import + New doc actions.                                  *
   * ====================================================================== */
  function ArtifactsSurface({ filed, drafts, onNewDoc, onNewCharter, onImport, onOpenDraft, onOpenFiled, onSpawnWork }) {
    const fileRef = useRef(null);
    // Your drafts, most-recent first (S18.2).
    const myDrafts = useMemo(() =>
      Object.values(drafts || {}).sort((a, b) => (b.updated || 0) - (a.updated || 0)), [drafts]);

    function pick() { if (fileRef.current) fileRef.current.click(); }
    function onFile(e) {
      const f = e.target.files && e.target.files[0];
      if (f && onImport) onImport(f);
      // S18.4: reset the picker after use so the same file can be re-imported.
      e.target.value = "";
    }

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light">
        <div className="sa-head fx-in">
          <div>
            <h1 className="fx-h1">Artifacts</h1>
            <p className="fx-psub">Versioned documents — what you draft, and what your runs bring back. Authored-by is a bus identity, never a person.</p>
          </div>
          <div className="sa-head-actions">
            <button className="fx-submit" onClick={onNewDoc}>＋ New doc</button>
            <button className="sa-ghost" onClick={onNewCharter}>◎ New charter</button>
            <button className="sa-ghost" onClick={pick}>⇪ Import a file</button>
            <input ref={fileRef} type="file" style={{ display: "none" }} onChange={onFile} />
          </div>
        </div>

        {/* ---- Your drafts (S18.2) ---- */}
        <div className="fx-group" style={{ marginTop: 22 }}>
          <div className="fx-group-h"><span className="fx-dot" style={{ background: "var(--prog)" }} /><span>Your drafts</span><span className="fx-group-n">{myDrafts.length}</span></div>
          {myDrafts.length === 0 ? (
            <p className="fx-psub" style={{ marginTop: 6 }}>Nothing in progress. <b>New doc</b> opens a blank writing surface; what you draft stays yours until you mark it ready.</p>
          ) : (
            <div className="fx-list">
              {myDrafts.map((d) => (
                <button className="fx-row fx-row--inline" key={d.id} onClick={() => onOpenDraft(d.id)}>
                  <span className="fx-row-ic">{kindIcon(d.kind)}</span>
                  <span className="fx-row-main">
                    <span className="fx-row-name">{d.title || "Untitled " + (KIND_LABEL[d.kind] || "note").toLowerCase()}</span>
                    <span className="fx-row-meta">{KIND_LABEL[d.kind] || d.kind} · edited {relAgo(d.updated)}</span>
                  </span>
                  <span className={"fx-chip-status " + (d.ready ? "t-met" : "t-todo")}>{d.ready ? "Ready" : "Draft"}</span>
                </button>
              ))}
            </div>
          )}
        </div>

        {/* ---- Filed (S18.3): artifacts a run brought back ---- */}
        <div className="fx-group" style={{ marginTop: 26 }}>
          <div className="fx-group-h"><span className="fx-dot" style={{ background: "var(--met)" }} /><span>Filed</span><span className="fx-group-n">{(filed || []).length}</span></div>
          {(filed || []).length === 0 ? (
            <div className="fx-stub" style={{ marginTop: 10 }}>
              <span className="fx-stub-ic">▣</span>
              <div>
                <div className="fx-stub-title">No filed artifacts yet.</div>
                <div className="fx-stub-sub">When a run finishes work it files an artifact here — a brief, a report, a doc — tagged with the run that produced it. Spawn work from a goal criterion to get one.</div>
              </div>
            </div>
          ) : (
            <div className="fx-list">
              {filed.map((a) => (
                <div className="fx-row-wrap" key={a.name}>
                  <button className="fx-row fx-row--inline" onClick={() => onOpenFiled(a.name)}>
                    <span className="fx-row-ic">{kindIcon("note")}</span>
                    <span className="fx-row-main">
                      <span className="fx-row-name">{a.name}</span>
                      <span className="fx-row-meta">
                        <span className="sa-vchip">v{a.version || 1}</span>
                        {a.runId ? <> · run <span className="mono">{a.runId}</span></> : null}
                        {a.goal ? <> · {a.goal}</> : null}
                        {a.updated ? <> · {a.updated} ago</> : null}
                      </span>
                    </span>
                    <span className={"fx-chip-status t-" + ({ review: "waiting", changes: "progress", approved: "met", rejected: "blocked" }[a.status] || "todo")}>{a.status || "filed"}</span>
                  </button>
                  <div className="fx-row-mobilize">
                    <button className="sa-ghost sa-ghost--sm" title="Spawn work on this artifact" onClick={() => onSpawnWork && onSpawnWork(a.name)}>spawn work</button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div></div>
    );
  }

  /* ====================================================================== *
   *  TASK-206 — Composer (one writing surface)                             *
   * ====================================================================== */
  // ComposerView edits ONE draft (by id) out of the draft store. It autosaves
  // continuously (every keystroke, debounced to the store) and shows the
  // autosaved-ago in its top bar. Mark-ready requires a title + body; a charter
  // routes to the criteria step (onDefineCriteria), anything else files an
  // artifact on the bus (onFileArtifact) and shows the compose-done screen.
  function ComposerView({ draftId, drafts, onPatch, onFileArtifact, onDefineCriteria, runPrompt, onAskBack, onBack }) {
    const draft = (drafts || {})[draftId];
    const [savedAt, setSavedAt] = useState(draft ? draft.updated : 0);
    const [tick, setTick] = useState(0); // re-render the "autosaved Xs ago" line
    const [done, setDone] = useState(null); // null | {kind, consequence}
    const [ask, setAsk] = useState("");

    // keep the autosaved-ago line fresh.
    useEffect(() => { const id = setInterval(() => setTick((t) => t + 1), 4000); return () => clearInterval(id); }, []);

    if (!draft) {
      return (<div className="fx-scroll"><div className="fx-col sx-conv-light">
        <h1 className="fx-h1">No draft open</h1>
        <p className="fx-psub">This draft was filed or removed. <button className="sx-artlink" onClick={onBack}>Back to Artifacts</button>.</p>
      </div></div>);
    }

    // edit a field → patch the store (autosave) + bump the saved-at.
    function patch(next) { onPatch(draftId, next); setSavedAt(Date.now()); }
    function setTitle(v) { patch({ title: v }); }
    function setSection(key, v) { patch({ sections: Object.assign({}, draft.sections, { [key]: v }) }); }

    const isCharter = draft.kind === "charter";
    const body = isCharter
      ? [draft.sections.north, draft.sections.vision, draft.sections.done].join("").trim()
      : (draft.sections.body || "").trim();
    const canReady = !!draft.title.trim() && !!body;

    function markReady() {
      if (!canReady) return;
      if (isCharter) {
        // hand the charter to the criteria proposal (S16.4) — the goal is only
        // created once criteria are accepted there.
        onDefineCriteria(draftId);
        return;
      }
      // file a durable artifact on the bus, then show the consequence (S16.6).
      onFileArtifact(draft).then((res) => {
        setDone({ kind: draft.kind, name: (res && res.name) || draft.title });
      }).catch(() => {
        // bus unreachable — still mark the draft ready locally + show the
        // honest "filed locally, will sync" line rather than a hard error.
        onPatch(draftId, { ready: true });
        setDone({ kind: draft.kind, name: draft.title, offline: true });
      });
    }

    if (done) {
      return <ComposeDone done={done} onBack={onBack} />;
    }

    return (
      <div className="cz-wrap">
        <div className="cz-doc">
          <div className="cz-topbar">
            <button className="sx-back" onClick={onBack}><span className="sx-back-ic">←</span><span className="sx-back-lbl">Artifacts</span></button>
            <span className="cz-saved">{tick >= 0 ? "autosaved " + relAgo(savedAt || draft.updated) : ""}</span>
          </div>
          <div className="cz-paper">
            <div className="cz-kindrow">
              <span className={"cz-kindtag cz-kind-" + draft.kind}>{kindIcon(draft.kind)} {KIND_LABEL[draft.kind] || draft.kind}</span>
              <span className="cz-byline">by You</span>
              {draft.ready && <span className="fx-chip-status t-met">Ready</span>}
            </div>
            {draft.importMeta && (
              <div className="cz-importbanner">
                <span className="cz-import-ic">⇪</span>
                <div>
                  <div className="cz-import-name">{draft.importMeta.name}</div>
                  <div className="cz-import-meta">{draft.importMeta.type || "file"} · {draft.importMeta.size}{draft.importMeta.binary ? " · binary (metadata only — contents not read into the editor)" : ""}</div>
                </div>
              </div>
            )}
            <input className="cz-title" placeholder={isCharter ? "Name this goal…" : "Title…"} value={draft.title} onChange={(e) => setTitle(e.target.value)} />
            {isCharter ? (
              <div className="cz-sections">
                <CzSection label="North star" hint="One sentence: what does success look like?" value={draft.sections.north} onChange={(v) => setSection("north", v)} />
                <CzSection label="The vision" hint="Why this matters; the shape of the outcome." value={draft.sections.vision} onChange={(v) => setSection("vision", v)} />
                <CzSection label="How we'll know it's done" hint="The checkable outcomes — these seed the acceptance criteria." value={draft.sections.done} onChange={(v) => setSection("done", v)} />
              </div>
            ) : (
              <div className="cz-sections">
                <CzSection label="Body" hint={draft.importMeta ? "Imported contents — edit freely." : "Write…"} value={draft.sections.body} onChange={(v) => setSection("body", v)} big />
              </div>
            )}
            <div className="cz-actions">
              <button className="fx-submit" disabled={!canReady} onClick={markReady} title={canReady ? "" : "A title and body are required to mark ready"}>
                {isCharter ? "Mark ready & define criteria →" : "Mark ready"}
              </button>
              <span className="cz-private">✦ Only you can see this until you mark it ready.</span>
            </div>
          </div>
        </div>

        {/* run-prompt rail (S16.5): present only when a run prompted this writing */}
        {runPrompt && (
          <aside className="cz-rail">
            <div className="cz-rail-h">Why you're writing this</div>
            <div className="cz-rail-prompt">{runPrompt.prompt}</div>
            {Array.isArray(runPrompt.questions) && runPrompt.questions.length > 0 && (
              <div className="cz-rail-qs">
                <div className="cz-rail-sub">It's asking</div>
                {runPrompt.questions.map((q, i) => <div className="cz-rail-q" key={i}>• {q}</div>)}
              </div>
            )}
            <div className="cz-rail-ask">
              <div className="cz-rail-sub">Ask a question back</div>
              <textarea className="sx-input" rows={2} value={ask} onChange={(e) => setAsk(e.target.value)} placeholder={"Reply to the run on " + (runPrompt.topic || "its topic") + "…"} />
              <button className="sa-ghost sa-ghost--sm" disabled={!ask.trim()} onClick={() => { onAskBack && onAskBack(runPrompt.topic, ask.trim()); setAsk(""); }}>Send to the run</button>
            </div>
          </aside>
        )}
      </div>
    );
  }

  function CzSection({ label, hint, value, onChange, big }) {
    const ref = useRef(null);
    useEffect(() => { const ta = ref.current; if (!ta) return; ta.style.height = "auto"; ta.style.height = Math.min(ta.scrollHeight, big ? 520 : 220) + "px"; }, [value, big]);
    return (
      <div className="cz-sec">
        <label className="cz-sec-label">{label}</label>
        <textarea ref={ref} className="cz-sec-input" rows={big ? 6 : 2} value={value} onChange={(e) => onChange(e.target.value)} placeholder={hint} />
      </div>
    );
  }

  // ComposeDone (S16.6): the consequence screen after a non-charter mark-ready.
  function ComposeDone({ done, onBack }) {
    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light" style={{ maxWidth: 620 }}>
        <span className="fx-done-icon" style={{ color: "var(--met)", background: "color-mix(in srgb,var(--met) 12%,transparent)" }}>✓</span>
        <h1 className="fx-h1" style={{ marginTop: 14 }}>Filed.</h1>
        <p className="fx-psub" style={{ fontSize: 15 }}>
          <b>{done.name}</b> is now a {KIND_LABEL[done.kind] ? KIND_LABEL[done.kind].toLowerCase() : "document"} artifact on the bus{done.offline ? " (queued locally — it'll sync when the bus reconnects)" : ""}. It's visible to runs and to anyone with the goal.
        </p>
        <div className="cz-done-note">Nothing was visible until you marked it ready — your drafts stay yours.</div>
        <div style={{ marginTop: 20 }}><button className="fx-submit" onClick={onBack}>Back to Artifacts</button></div>
      </div></div>
    );
  }

  /* ====================================================================== *
   *  TASK-207 — Criteria proposal                                          *
   *  After a charter, propose criteria; accept/edit/drop/add; Accept all   *
   *  → goal live (creates a goal.<id> artifact on the bus).                *
   * ====================================================================== */
  // derive proposed criteria from the charter's "how we'll know it's done"
  // section — one per non-empty line / sentence. A pragmatic split: lines first,
  // else sentences. This is a PROPOSAL the operator curates, not a commitment.
  function deriveCriteria(doneText) {
    const t = (doneText || "").trim();
    if (!t) return [];
    let parts = t.split(/\n+/).map((s) => s.replace(/^[-*•\d.)\s]+/, "").trim()).filter(Boolean);
    if (parts.length <= 1) parts = t.split(/(?<=[.!?])\s+/).map((s) => s.trim()).filter(Boolean);
    return parts.slice(0, 12).map((text, i) => ({ key: "c" + i, text, accepted: true }));
  }

  function CriteriaProposal({ draft, onCreateGoal, onBack }) {
    const [crits, setCrits] = useState(() => deriveCriteria(draft && draft.sections && draft.sections.done));
    const [adding, setAdding] = useState("");
    const [creating, setCreating] = useState(false);
    const [err, setErr] = useState("");

    if (!draft) return (<div className="fx-scroll"><div className="fx-col sx-conv-light"><h1 className="fx-h1">No charter</h1><p className="fx-psub">Start a charter in the Composer first.</p></div></div>);

    const northstar = (draft.sections.north || draft.title || "").trim();
    const accepted = crits.filter((c) => c.accepted);

    function toggle(key) { setCrits((cs) => cs.map((c) => c.key === key ? Object.assign({}, c, { accepted: !c.accepted }) : c)); }
    function edit(key, text) { setCrits((cs) => cs.map((c) => c.key === key ? Object.assign({}, c, { text }) : c)); }
    function drop(key) { setCrits((cs) => cs.filter((c) => c.key !== key)); }
    function add() { const t = adding.trim(); if (!t) return; setCrits((cs) => cs.concat([{ key: "c" + Date.now(), text: t, accepted: true }])); setAdding(""); }

    function acceptAll() {
      if (!accepted.length) return;
      setCreating(true); setErr("");
      onCreateGoal({ draftId: draft.id, northstar, title: draft.title, criteria: accepted.map((c) => c.text) })
        .then(() => { /* parent navigates to the goal */ })
        .catch((e) => { setErr((e && e.message) || "Could not create the goal — the bus may be unreachable."); setCreating(false); });
    }

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light" style={{ maxWidth: 760 }}>
        <button className="sx-back" onClick={onBack}><span className="sx-back-ic">←</span><span className="sx-back-lbl">Composer</span></button>
        <h1 className="fx-h1" style={{ marginTop: 8 }}>Define the criteria</h1>
        <p className="fx-psub">From your charter, here are the acceptance criteria that make this goal true. Accept, edit, drop, or add your own — then make the goal live.</p>

        {/* the north star (S17.1) */}
        <div className="cp-northstar"><span className="cp-ns-ic">◎</span><span>{northstar || "No north star — add one in the charter."}</span></div>

        {/* proposed criteria (S17.2) */}
        <div className="cp-list">
          {crits.length === 0 && <p className="fx-psub">No criteria derived from "How we'll know it's done." Add one below.</p>}
          {crits.map((c) => (
            <div className={"cp-crit" + (c.accepted ? " is-on" : " is-off")} key={c.key}>
              <button className="cp-check" title={c.accepted ? "Drop from the goal" : "Accept"} onClick={() => toggle(c.key)}>{c.accepted ? "✓" : "○"}</button>
              <input className="cp-crit-text" value={c.text} onChange={(e) => edit(c.key, e.target.value)} />
              <button className="cp-drop" title="Remove" onClick={() => drop(c.key)}>×</button>
            </div>
          ))}
        </div>

        {/* add your own (S17.2) */}
        <div className="cp-add">
          <input className="sx-input" value={adding} placeholder="Add a criterion of your own…" onChange={(e) => setAdding(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }} />
          <button className="sa-ghost sa-ghost--sm" disabled={!adding.trim()} onClick={add}>＋ Add</button>
        </div>

        {/* footer (S17.3) */}
        <div className="cp-foot">
          <span className="cp-count">{accepted.length} of {crits.length} accepted</span>
          <button className="fx-submit" disabled={!accepted.length || creating} onClick={acceptAll}>{creating ? "Creating…" : "Accept all → goal live"}</button>
        </div>
        {err && <p className="fx-psub" style={{ color: "var(--wait)", marginTop: 8 }}>{err}</p>}
        <p className="cp-hint">Only once the goal is live can workflows attach to its criteria.</p>
      </div></div>
    );
  }

  Object.assign(window, { ArtifactsSurface, ComposerView, CriteriaProposal, _synthDrafts: { loadDrafts, saveDrafts, blankDraft, newDraftId, DRAFTS_KEY } });
})();
