/* workengine.jsx — the Work engine lane of the operator dash (TASK-211/212/213/214/215).

   One surface, five sub-views, driven by the existing bus data layer (window.SX,
   ADR-0044) and the run-record contract (ADR-0048). The work-engine never tracks a
   person: a run/workflow is a ULID + function, never a persona.

   Records (ADR-0048, conventions over Artifacts + Messages — nothing in the core):
     - a workflow TEMPLATE is `sextant.workflow.template/v1`, generic/reusable, no
       goal or criterion of its own. Persisted as artifact `workflow.template.<slug>`.
     - a RUN is `sextant.workflow.run/v1`, one live instance, ULID-identified.
       Persisted as artifact `workflow.run.<ULID>`. An ad-hoc run carries
       template:null. The goal binding rides ADR-0035 `relates:[{goal,crit,
       kind:"toward"}]`, written at spawn, never on the template.
     - the run TOPIC is `msg.topic.run.<ULID>` — steering posts (no takeover, §11 CUT).

   Run records (TASK-193) aren't produced by a coordinator yet, so the active-run /
   run-history surfaces are driven by these dash-written artifacts plus a small set of
   SEED records, and degrade gracefully when none exist. The dash IS a co-equal client
   (ADR-0044), so a run it spawns is a real artifact on the bus: spawn → reload → the
   run re-derives from the bus, which is the persistence proof (TASK-212 #7).

   Exports WorkEngineView to window. */
(function () {
  const { useState, useRef, useEffect, useMemo, useCallback } = React;

  const TEMPLATE_TYPE = "sextant.workflow.template/v1";
  const RUN_TYPE = "sextant.workflow.run/v1";
  const TEMPLATE_PREFIX = "workflow.template.";
  const RUN_PREFIX = "workflow.run.";
  const RUN_TOPIC = (id) => "msg.topic.run." + id;
  // The wake signal the coordinator (run executor, TASK-236) watches: the dash
  // writes the run artifact, then publishes run.start{id}; the coordinator adopts it.
  const RUN_START_SUBJECT = "msg.topic.run.start";
  const RUN_CONTROL = (id) => "msg.workflow.run." + id + ".control";
  const BASE_STOP = ["done — brief w/ proof of success", "blocked — brief documenting why"];

  // The always-available base template (TASK-212 S7.2): Investigate → review → brief.
  // It is not a stored artifact — it is the floor every workflow list / spawn picker
  // offers so an operator can always spawn something without authoring a workflow.
  const BASE_TEMPLATE = {
    name: "Investigate → review → brief",
    base: true,
    description: "Investigate the objective, pause for an operator review, then write the stopping brief.",
    steps: [
      { id: "s1", label: "Investigate the objective", kind: "work" },
      { id: "s2", label: "Pause for operator review", kind: "checkpoint" },
      { id: "s3", label: "Write the stopping brief", kind: "brief" },
    ],
    triggers: [{ label: "Manual", manual: true }],
    stop_conditions: [],
  };

  // The terminal step every workflow carries (ADR-0048): write the stopping brief.
  // Always present, always required, never removable (TASK-213 S8.3).
  const BRIEF_STEP = () => ({ id: "brief", label: "Write the stopping brief", kind: "brief" });

  // ---- ULID (Crockford base32, 48-bit ms time + 80-bit randomness) ----
  const ULID_ALPHA = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
  function ulid() {
    let t = Date.now(), time = "";
    for (let i = 9; i >= 0; i--) { time = ULID_ALPHA[t % 32] + time; t = Math.floor(t / 32); }
    let rand = "";
    for (let i = 0; i < 16; i++) rand += ULID_ALPHA[Math.floor(Math.random() * 32)];
    return time + rand;
  }
  function ulidTime(id) {
    if (!id || id.length < 10) return 0;
    let t = 0;
    for (let i = 0; i < 10; i++) { const v = ULID_ALPHA.indexOf((id[i] || "").toUpperCase()); if (v < 0) return 0; t = t * 32 + v; }
    return t;
  }
  function relMs(ms) {
    if (!ms) return "";
    const s = Math.max(0, (Date.now() - ms) / 1000);
    if (s < 60) return Math.floor(s) + "s";
    if (s < 3600) return Math.floor(s / 60) + "m";
    if (s < 86400) return Math.floor(s / 3600) + "h";
    return Math.floor(s / 86400) + "d";
  }
  function slugify(s) {
    return (s || "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 48) || "workflow";
  }
  // shortUlid trims a full ULID to head…tail for the active-run code line.
  function shortUlid(id) {
    id = id || "";
    return id.length > 14 ? id.slice(0, 6) + "…" + id.slice(-4) : id;
  }

  // Run status → pulse tone + label. A live run pulses; terminal ones are calm.
  const RUN_STATUS = {
    running: { label: "Running", c: "var(--met)", live: true, active: true },
    waiting: { label: "Needs you", c: "var(--wait)", live: true, active: true },
    blocked: { label: "Blocked", c: "var(--blk)", live: true }, // terminal — not active (no Stop button)
    done: { label: "Done", c: "var(--met)" },
    cancelled: { label: "Cancelled", c: "var(--todo)" },
  };
  const isActive = (st) => { const s = RUN_STATUS[st]; return !!(s && s.active); };

  /* ============================ data layer ============================ */
  // The work-engine reads every workflow.template.* / workflow.run.* artifact and
  // adapts the record into the view shape. window.SX is the one bus Client (ADR-0044).

  // SEED data — used ONLY when the bus has no run/template artifacts yet, so the
  // surfaces are populated for a demo while the coordinator that writes real runs
  // (TASK-193) doesn't exist. A single real spawned run replaces the seed (the
  // surfaces prefer bus records; seed fills the empty floor). Marked `seed:true` so
  // a row can flag itself as illustrative.
  const SEED_TEMPLATES = [
    {
      name: "Plan → build → review → PR", seed: true,
      description: "Draft a plan, pause for the operator to approve it, build the change, run a self-review, open a PR (trusted host-side step — the sandboxed worker cannot push), then write the stopping brief.",
      steps: [
        { id: "s1", label: "Draft an implementation plan", kind: "work" },
        { id: "s2", label: "Operator approves the plan", kind: "checkpoint" },
        { id: "s3", label: "Build the change", kind: "work" },
        { id: "s4", label: "Self-review against acceptance criteria", kind: "work" },
        { id: "s5", label: "Open a pull request", kind: "pr-open" },
        { id: "s6", label: "Write the stopping brief", kind: "brief" },
      ],
      triggers: [{ label: "Manual", manual: true }, { label: "On ticket labelled ready-for-agent", on: "ticket.ready" }],
      stop_conditions: ["plan-review: pause after the plan and post it for operator feedback"],
    },
  ];
  const SEED_RUNS = [
    {
      id: "01J9SEEDRUN0000000000ACTV", template: "Plan → build → review → PR", seed: true,
      label: "Wire run-topic composer", objective: "Add a posting composer to the run view so the operator can steer without taking over.",
      status: "waiting",
      steps: [
        { id: "s1", label: "Draft an implementation plan", kind: "work", status: "done" },
        { id: "s2", label: "Operator approves the plan", kind: "checkpoint", status: "waiting" },
        { id: "s3", label: "Build the change", kind: "work", status: "upcoming" },
        { id: "s4", label: "Self-review against acceptance criteria", kind: "work", status: "upcoming" },
        { id: "s5", label: "Open a pull request", kind: "pr-open", status: "upcoming" },
        { id: "s6", label: "Write the stopping brief", kind: "brief", status: "upcoming" },
      ],
      relates: [],
      activity: [
        { id: "a1", glyph: "•", text: "Run spawned from Plan → build → review → PR", src: "01J9SEEDRUN0000000000ACTV", at: Date.now() - 1000 * 60 * 14 },
        { id: "a2", glyph: "✓", text: "Drafted the implementation plan (3 steps)", src: "01J9SEEDRUN0000000000ACTV", at: Date.now() - 1000 * 60 * 9 },
        { id: "a3", glyph: "❡", text: "Posted plan brief for operator review", src: "01J9SEEDRUN0000000000ACTV", at: Date.now() - 1000 * 60 * 8 },
      ],
      artifacts: [{ name: "plan-run-topic-composer", kind: "plan", version: 1, status: "review" }],
      created: Date.now() - 1000 * 60 * 14,
    },
  ];

  function adaptTemplate(name, rec) {
    return {
      key: name, name: (rec && rec.name) || name.replace(TEMPLATE_PREFIX, ""),
      description: (rec && rec.description) || "",
      steps: (rec && Array.isArray(rec.steps)) ? rec.steps : [],
      triggers: (rec && Array.isArray(rec.triggers)) ? rec.triggers : [{ label: "Manual", manual: true }],
      stop_conditions: (rec && Array.isArray(rec.stop_conditions)) ? rec.stop_conditions : [],
    };
  }
  function adaptRun(name, rec) {
    const id = (rec && rec.id) || name.replace(RUN_PREFIX, "");
    return {
      key: name, id, template: (rec && rec.template) || null,
      label: (rec && rec.label) || "(unnamed run)",
      objective: (rec && rec.objective) || "",
      status: (rec && rec.status) || "running",
      steps: (rec && Array.isArray(rec.steps)) ? rec.steps : [],
      relates: (rec && Array.isArray(rec.relates)) ? rec.relates : [],
      activity: (rec && Array.isArray(rec.activity)) ? rec.activity : [],
      artifacts: (rec && Array.isArray(rec.artifacts)) ? rec.artifacts : [],
      created: (rec && rec.created) || ulidTime(id),
    };
  }

  // useWorkEngineData — list + poll every workflow.template.* / workflow.run.*
  // artifact off the bus, adapt them, fold in the always-available base template,
  // and fall back to seed records only when the bus has none. Returns
  // { templates, runs, reload }.
  function useWorkEngineData() {
    const [busTemplates, setBusTemplates] = useState([]);
    const [busRuns, setBusRuns] = useState([]);

    const load = useCallback(() => {
      const SX = window.SX; if (!SX) return;
      SX.get("/api/artifacts").then((list) => {
        const names = (Array.isArray(list) ? list : []).map((a) => a.Name || a.name).filter(Boolean);
        const tNames = names.filter((n) => n.startsWith(TEMPLATE_PREFIX));
        const rNames = names.filter((n) => n.startsWith(RUN_PREFIX));
        return Promise.all([
          Promise.all(tNames.map((n) => SX.get("/api/artifacts/" + encodeURIComponent(n)).then((a) => adaptTemplate(n, a && a.Record)).catch(() => null))),
          Promise.all(rNames.map((n) => SX.get("/api/artifacts/" + encodeURIComponent(n)).then((a) => adaptRun(n, a && a.Record)).catch(() => null))),
        ]);
      }).then((pair) => {
        if (!pair) return;
        setBusTemplates((pair[0] || []).filter(Boolean));
        setBusRuns((pair[1] || []).filter(Boolean));
      }).catch(() => {});
    }, []);

    useEffect(() => {
      load();
      const id = setInterval(load, 4000);
      return () => clearInterval(id);
    }, [load]);

    // Compose: the base template is always first; bus templates next; seed templates
    // fill in only if the bus has none. Same for runs (bus first, seed as a floor).
    const templates = useMemo(() => {
      const out = [BASE_TEMPLATE, ...busTemplates];
      if (busTemplates.length === 0) out.push(...SEED_TEMPLATES);
      return out;
    }, [busTemplates]);
    const runs = useMemo(() => {
      if (busRuns.length > 0) return busRuns.slice().sort((a, b) => (b.created || 0) - (a.created || 0));
      return SEED_RUNS.slice();
    }, [busRuns]);

    return { templates, runs, reload: load };
  }

  /* ============================ small shared bits ============================ */
  function Pulse({ status }) {
    const s = RUN_STATUS[status] || RUN_STATUS.running;
    return <span className={"we-pulse" + (s.live ? " is-live" : "")} style={{ background: s.c }} />;
  }
  function StepKindTag({ kind }) {
    if (kind === "checkpoint") return <span className="we-tag we-tag-ask">ask operator</span>;
    if (kind === "brief") return <span className="we-tag we-tag-brief">stopping brief</span>;
    if (kind === "pr-open") return <span className="we-tag we-tag-pr">open PR</span>;
    return null;
  }
  function stepLine(steps) {
    return (steps || []).map((s) => s.label).join("  →  ");
  }

  /* ============================ C.1 — the list ============================ */
  function WorkEngineList({ templates, runs, onSpawn, onNewWorkflow, onOpenTemplate, onEditTemplate, onOpenRun }) {
    const active = runs.filter((r) => isActive(r.status));
    // Terminal runs (done/blocked/cancelled) stay REACHABLE — without this they
    // drop out of the list entirely and the operator can't reopen a finished run
    // to read its brief or see why it blocked. Most-recent first, capped.
    const recent = runs.filter((r) => !isActive(r.status))
      .sort((a, b) => (b.created || 0) - (a.created || 0))
      .slice(0, 12);
    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light">
        <div className="we-head fx-in">
          <div>
            <h1 className="fx-h1">Work engine</h1>
            <p className="fx-psub">Define the work as a spec, then watch its runs. A workflow is the de-named actor; a run is one live instance of it.</p>
          </div>
          <button className="we-spawn-btn" onClick={onSpawn}>＋ Spawn work</button>
        </div>

        {/* ---- Workflows ---- */}
        <div className="we-section fx-in" style={{ animationDelay: ".03s" }}>
          <div className="we-sec-head">
            <h2 className="we-sec-title">Workflows</h2>
            <span className="we-sec-sub">The templates. Reusable specs that define what gets done — trigger, steps, outputs. Nothing runs here.</span>
          </div>
          <div className="we-rows">
            {templates.map((t) => {
              const live = runs.filter((r) => r.template === t.name && isActive(r.status)).length;
              // Trigger as an "on <trigger>" tag (design): prefer a real (non-manual)
              // trigger, else the first; "Manual" reads "on manual".
              const trig = (t.triggers || []).find((x) => !x.manual) || (t.triggers || [])[0] || { label: "Manual" };
              return (
                <div className="we-wf-row" key={t.name} onClick={() => onOpenTemplate(t)} role="button" tabIndex={0}
                  onKeyDown={(e) => { if (e.key === "Enter") onOpenTemplate(t); }}>
                  <div className="we-wf-main">
                    <div className="we-wf-top">
                      <span className="we-wf-name">{t.name}</span>
                      {t.seed && <span className="we-chip we-chip-seed">sample</span>}
                      {live > 0 && <span className="we-livebadge"><Pulse status="running" />{live} live</span>}
                    </div>
                    <div className="we-wf-recipe">{stepLine(t.steps) || "no steps yet"}</div>
                    <div className="we-wf-meta"><span className="we-wf-tag">on {(trig.label || "manual").toLowerCase().replace(/^on\s+/, "")}</span></div>
                  </div>
                  <div className="we-wf-right">
                    {!t.base && (
                      <button className="we-mini" title="Edit spec" onClick={(e) => { e.stopPropagation(); onEditTemplate(t); }}>Edit spec</button>
                    )}
                  </div>
                </div>
              );
            })}
            {templates.length === 0 && (
              <div className="we-empty">
                <div className="we-empty-title">No workflows yet.</div>
                <div className="we-empty-sub">Describe a reusable spec — a trigger plus the steps a run walks.</div>
              </div>
            )}
          </div>
          {/* "describe a workflow" lives at the BOTTOM as a field, not the header (design). */}
          <button className="we-newwf" onClick={onNewWorkflow}>
            <span>＋ describe a new workflow…</span><span className="we-newwf-go">→</span>
          </button>
        </div>

        {/* ---- Active runs ---- */}
        <div className="we-section fx-in" style={{ animationDelay: ".06s" }}>
          <div className="we-sec-head">
            <h2 className="we-sec-title">Active runs</h2>
            <span className="we-sec-sub">Live work happening now — each is one instance of a workflow above (or ad-hoc). Open a run to steer it.</span>
          </div>
          <div className="we-rows">
            {active.map((r) => {
              const toward = (r.relates || []).find((x) => x.kind === "toward");
              // design's Active-run row: pulse + label + a single code line
              // ({ULID-short} · via {workflow} · {goal}) + watch ▸. No SAMPLE or
              // "Needs you" badge — the pulse already carries the live status.
              return (
                <div className="we-run-row" key={r.id} onClick={() => onOpenRun(r)} role="button" tabIndex={0}
                  onKeyDown={(e) => { if (e.key === "Enter") onOpenRun(r); }}>
                  <Pulse status={r.status} />
                  <div className="we-run-main">
                    <div className="we-run-top">
                      <span className="we-run-label">{r.label}</span>
                    </div>
                    <div className="we-run-meta">
                      <span className="we-ulid mono">{shortUlid(r.id)}</span>
                      <span className="we-run-via">· {r.template ? "via " + r.template : "ad-hoc"}</span>
                      <span className="we-run-goal">· {toward ? (toward.goal || "goal") + " · " + (toward.crit || "criterion") : "no goal yet"}</span>
                    </div>
                  </div>
                  <span className="we-run-watch">watch ▸</span>
                </div>
              );
            })}
            {active.length === 0 && (
              <div className="we-empty">
                <div className="we-empty-title">No runs in flight.</div>
                <div className="we-empty-sub">Spawn work to point a workflow at a goal and watch it walk its steps.</div>
                <button className="we-spawn-btn" onClick={onSpawn}>＋ Spawn work</button>
              </div>
            )}
          </div>
        </div>

        {/* ---- Recent runs (terminal: done/blocked/cancelled) ---- */}
        {recent.length > 0 && (
          <div className="we-section fx-in" style={{ animationDelay: ".09s" }}>
            <div className="we-sec-head">
              <h2 className="we-sec-title">Recent runs</h2>
              <span className="we-sec-sub">Finished runs — open one to read its stopping brief or see why it blocked.</span>
            </div>
            <div className="we-rows">
              {recent.map((r) => {
                const st = RUN_STATUS[r.status] || RUN_STATUS.running;
                return (
                  <div className="we-run-row" key={r.id} onClick={() => onOpenRun(r)} role="button" tabIndex={0}
                    onKeyDown={(e) => { if (e.key === "Enter") onOpenRun(r); }}>
                    <Pulse status={r.status} />
                    <div className="we-run-main">
                      <div className="we-run-top">
                        <span className="we-run-label">{r.label}</span>
                      </div>
                      <div className="we-run-meta">
                        <span className="we-ulid mono">{shortUlid(r.id)}</span>
                        <span className="we-run-via">· {r.template ? "via " + r.template : "ad-hoc"}</span>
                        <span className="we-run-goal">· {st.label}</span>
                      </div>
                    </div>
                    <span className="we-run-watch">open ▸</span>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div></div>
    );
  }

  /* ============================ C.2 — Spawn work ============================ */
  function SpawnWork({ templates, goals, initial, onCancel, onSpawned, onNewWorkflow }) {
    // Hold the picked template as an OBJECT, frozen at click — NOT a name re-resolved
    // against `templates` at spawn time. The 4s poll rebuilds the templates array from
    // scratch every tick (each template re-fetched), so a name lookup can transiently
    // miss and the old `|| templates[0]` fallback would then SILENTLY spawn the base
    // workflow instead of the one the operator chose (TASK-248). `initial` pre-selects
    // a template handed in from its detail view.
    const [picked, setPicked] = useState(initial || templates[0] || null);
    const [objective, setObjective] = useState("");
    const [goalId, setGoalId] = useState(""); // "" = no goal yet
    const [critId, setCritId] = useState("");
    const [phase, setPhase] = useState("idle"); // idle | spawning | error
    const [err, setErr] = useState("");

    // Prefer the fresh-by-name template (newest steps) but fall back to the FROZEN
    // pick — never silently to base — when the poll has momentarily dropped it.
    const tpl = (picked && templates.find((t) => t.name === picked.name)) || picked || null;
    const goal = goals.find((g) => g.id === goalId) || null;
    const crit = goal ? (goal.criteria || []).find((c) => c.id === critId) : null;
    const pauses = tpl ? (tpl.steps || []).some((s) => s.kind === "checkpoint") : false;
    const canSpawn = objective.trim().length > 0 && phase !== "spawning";

    function handleSpawn() {
      if (!canSpawn) return;
      setPhase("spawning"); setErr("");
      const id = ulid();
      const name = RUN_PREFIX + id;
      const relates = (goal && crit) ? [{ goal: goal.name || goal.id, crit: crit.id, kind: "toward" }] : [];
      const steps = (tpl ? tpl.steps : BASE_TEMPLATE.steps).map((s, i) => ({ ...s, status: i === 0 ? "running" : "upcoming" }));
      // stop conditions: the baseline two prompts plus the template's additions.
      const stop = BASE_STOP.concat((tpl && Array.isArray(tpl.stop_conditions)) ? tpl.stop_conditions : []);
      const record = {
        "$type": RUN_TYPE,
        id,
        template: (tpl && !tpl.base) ? tpl.name : null,
        label: objective.trim().slice(0, 72),
        objective: objective.trim(),
        status: "running",
        steps,
        relates,
        activity: [{ id: "a" + Date.now(), glyph: "•", text: "Run spawned" + (tpl ? " from " + tpl.name : " (ad-hoc)"), src: id, at: Date.now() }],
        artifacts: [],
        stop,
        created: Date.now(),
      };
      const SX = window.SX;
      SX.createArtifact(name, record).then(() => {
        // announce the spawn on the run topic so the thread has a first entry the
        // bus durably holds (TASK-215 #6 persistence floor for the run thread).
        SX.publish(RUN_TOPIC(id), { "$type": "chat.message", text: "Run spawned: " + record.label }).catch(() => {});
        // wake the coordinator: it adopts the run we just wrote and drives it (TASK-236).
        // Fire-and-forget — the dash polls the artifact, which the coordinator now owns.
        SX.publish(RUN_START_SUBJECT, { "$type": "run.start", id }).catch(() => {});
        onSpawned(adaptRun(name, record));
      }).catch((e) => { setErr((e && e.message) || String(e)); setPhase("error"); });
    }

    const summary = tpl
      ? "Spawns " + tpl.name + " on “" + (objective.trim() || "…") + "” " + (goal ? "→ " + (goal.name || goal.id) + (crit ? " — toward " + (crit.text || crit.id) : "") : "→ no goal yet")
      : "Pick a workflow to begin.";

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light we-spawn">
        <div className="we-head fx-in">
          <div><h1 className="fx-h1">Spawn work</h1>
            <p className="fx-psub">The workflow is the <b>how</b>; spawning gives it a concrete <b>what</b> pointed at a goal.</p></div>
          <button className="we-link" onClick={onCancel}>Cancel</button>
        </div>

        {/* Step 1 — How */}
        <section className="we-step-card fx-in">
          <div className="we-step-h"><span className="we-step-n">1</span> How — pick a workflow</div>
          <div className="we-tpl-grid">
            {templates.map((t) => (
              <button key={t.name} className={"we-tpl-opt" + (picked && t.name === picked.name ? " is-on" : "")} onClick={() => setPicked(t)}>
                <div className="we-tpl-opt-name">{t.name}{t.base && <span className="we-chip we-chip-base">base</span>}</div>
                <div className="we-tpl-opt-steps">{stepLine(t.steps)}</div>
              </button>
            ))}
            <button className="we-tpl-opt we-tpl-new" onClick={onNewWorkflow}>＋ New template…</button>
          </div>
        </section>

        {/* Step 2 — On */}
        <section className="we-step-card fx-in" style={{ animationDelay: ".03s" }}>
          <div className="we-step-h"><span className="we-step-n">2</span> On — the task objective</div>
          <textarea className="we-textarea" rows={3} placeholder="What should this run actually do? e.g. 'Investigate the flaky workflow.start replay and propose a fix.'"
            value={objective} onChange={(e) => setObjective(e.target.value)} />
        </section>

        {/* Step 3 — Toward */}
        <section className="we-step-card fx-in" style={{ animationDelay: ".06s" }}>
          <div className="we-step-h"><span className="we-step-n">3</span> Toward — a goal &amp; criterion</div>
          <div className="we-goal-row">
            <button className={"we-goal-opt" + (goalId === "" ? " is-on" : "")} onClick={() => { setGoalId(""); setCritId(""); }}>No goal yet</button>
            {goals.map((g) => (
              <button key={g.id} className={"we-goal-opt" + (goalId === g.id ? " is-on" : "")} onClick={() => { setGoalId(g.id); setCritId(""); }}>{g.name || g.id}</button>
            ))}
          </div>
          {goal && (
            <div className="we-crit-chips">
              {(goal.criteria || []).length === 0 && <span className="we-muted">This goal has no criteria yet.</span>}
              {(goal.criteria || []).map((c) => (
                <button key={c.id} className={"we-crit-chip" + (critId === c.id ? " is-on" : "")} onClick={() => setCritId(c.id)}>{c.text || c.id}</button>
              ))}
            </div>
          )}
        </section>

        {/* Live summary */}
        <div className="we-summary fx-in" style={{ animationDelay: ".09s" }}>
          <div className="we-summary-line">{summary}</div>
          <div className="we-summary-meta">
            <span>New run ULID minted on spawn</span>
            <span className="we-dot">·</span>
            <span>{pauses ? "Pauses at an operator checkpoint" : "Runs to completion (stops at the brief)"}</span>
          </div>
        </div>

        {phase === "error" && <div className="we-err">⊘ {err}</div>}

        <div className="we-actions">
          <button className="we-spawn-btn" disabled={!canSpawn} onClick={handleSpawn}>
            {phase === "spawning" ? "Spawning…" : "Spawn & watch →"}
          </button>
          {!objective.trim() && <span className="we-hint">Enter a task objective to spawn.</span>}
        </div>
      </div></div>
    );
  }

  /* ============================ C.3 — Workflow builder ============================ */
  function WorkflowBuilder({ initial, onCancel, onSaved }) {
    const editing = !!(initial && initial.key);
    const [name, setName] = useState((initial && initial.name) || "");
    const [description, setDescription] = useState((initial && initial.description) || "");
    // steps always end with the immovable brief step.
    const [steps, setSteps] = useState(() => {
      const base = (initial && initial.steps && initial.steps.filter((s) => s.kind !== "brief")) || [];
      return [...base, BRIEF_STEP()];
    });
    const [triggers, setTriggers] = useState(() => {
      const t = (initial && initial.triggers) || [];
      const manual = { label: "Manual", manual: true, on: true };
      const customs = t.filter((x) => !x.manual).map((x) => ({ label: x.label, on: x.on !== false }));
      return [manual, ...customs];
    });
    const [stopConds] = useState((initial && initial.stop_conditions) || []);
    const [newTrig, setNewTrig] = useState("");
    const [phase, setPhase] = useState("idle");
    const dragIdx = useRef(null);

    // Generate steps from prose (S8.1): split on connectives; mark ask/review
    // language as operator-checkpoints. A heuristic draft, fully editable after.
    function generate() {
      const txt = description.trim();
      if (!txt) return;
      const parts = txt.split(/(?:,|;|\.|\bthen\b|\band then\b|\bnext\b|\bafter that\b|\bfinally\b)/i)
        .map((s) => s.trim()).filter((s) => s.length > 2);
      const drafted = parts.map((p, i) => {
        const ask = /\b(ask|review|approv|confirm|check with|operator|sign[- ]?off|feedback|pause)\b/i.test(p);
        return { id: "g" + Date.now() + i, label: p.charAt(0).toUpperCase() + p.slice(1), kind: ask ? "checkpoint" : "work" };
      });
      setSteps([...drafted, BRIEF_STEP()]);
    }

    function addStep(kind) {
      setSteps((prev) => {
        const tail = prev[prev.length - 1]; // the brief
        const body = prev.slice(0, -1);
        return [...body, { id: "n" + Date.now(), label: kind === "checkpoint" ? "Operator checkpoint" : "New step", kind }, tail];
      });
    }
    function setLabel(id, label) { setSteps((prev) => prev.map((s) => s.id === id ? { ...s, label } : s)); }
    function removeStep(id) { setSteps((prev) => prev.filter((s) => s.kind === "brief" || s.id !== id)); }
    function onDragStart(i) { dragIdx.current = i; }
    function onDragOver(e, i) {
      e.preventDefault();
      const from = dragIdx.current;
      if (from == null || from === i) return;
      setSteps((prev) => {
        // never move the brief (last) and never drop onto it
        if (prev[from] && prev[from].kind === "brief") return prev;
        if (i >= prev.length - 1) return prev;
        const next = prev.slice();
        const [m] = next.splice(from, 1);
        next.splice(i, 0, m);
        dragIdx.current = i;
        return next;
      });
    }

    function toggleTrigger(i) { setTriggers((prev) => prev.map((t, j) => j === i ? { ...t, on: !t.on } : t)); }
    function removeTrigger(i) { setTriggers((prev) => prev[i] && prev[i].manual ? prev : prev.filter((_, j) => j !== i)); }
    function addTrigger() {
      const v = newTrig.trim(); if (!v) return;
      setTriggers((prev) => [...prev, { label: v, on: true }]); setNewTrig("");
    }

    // Live WORKFLOW.md preview (S8.5).
    const md = useMemo(() => {
      const trigOn = triggers.filter((t) => t.on).map((t) => t.label);
      let out = "---\n";
      out += "name: " + (name || "(unnamed workflow)") + "\n";
      out += "triggers: [" + trigOn.join(", ") + "]\n";
      out += "---\n\n";
      out += "# " + (name || "(unnamed workflow)") + "\n\n";
      if (description.trim()) out += description.trim() + "\n\n";
      out += "## Steps\n\n";
      steps.forEach((s, i) => {
        let line = (i + 1) + ". " + s.label;
        if (s.kind === "checkpoint") line += "  _(ask operator)_";
        if (s.kind === "brief") line += "  _(always required — stopping brief)_";
        out += line + "\n";
      });
      out += "\n## Stop conditions\n\n";
      out += "- done — work complete, brief with proof\n";
      out += "- blocked — cannot proceed, brief documents why\n";
      stopConds.forEach((c) => { out += "- " + c + "\n"; });
      return out;
    }, [name, description, steps, triggers, stopConds]);

    function buildRecord() {
      return {
        "$type": TEMPLATE_TYPE,
        name: name.trim() || "(unnamed workflow)",
        description: description.trim(),
        steps: steps.map((s) => ({ id: s.id, label: s.label, kind: s.kind })),
        triggers: triggers.filter((t) => t.on || t.manual).map((t) => t.manual ? { label: "Manual", manual: true } : { label: t.label, on: t.label.toLowerCase().replace(/\s+/g, ".") }),
        stop_conditions: stopConds,
      };
    }
    function save(open) {
      setPhase("saving");
      const rec = buildRecord();
      // editing preserves identity (the artifact key); a new workflow keys off the slug.
      const key = editing ? initial.key : TEMPLATE_PREFIX + slugify(name);
      window.SX.saveArtifact(key, rec).then(() => {
        onSaved(adaptTemplate(key, rec), open);
      }).catch((e) => { setPhase("error"); console.error("save workflow:", e); });
    }

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light we-builder">
        <div className="we-head fx-in">
          <div><h1 className="fx-h1">{editing ? "Edit workflow" : "Describe a workflow"}</h1>
            <p className="fx-psub">A reusable spec — generic, no goal of its own. Nothing runs until you save it.</p></div>
          <button className="we-link" onClick={onCancel}>Cancel</button>
        </div>

        <div className="we-builder-grid">
          <div className="we-builder-form">
            {/* 1 Describe */}
            <section className="we-step-card">
              <div className="we-step-h"><span className="we-step-n">1</span> Describe</div>
              <input className="we-input" placeholder="Workflow name — e.g. Plan → build → review → PR" value={name} onChange={(e) => setName(e.target.value)} />
              <textarea className="we-textarea" rows={3} style={{ marginTop: 8 }} placeholder="Describe the workflow in prose — connectives like 'then' and 'after that' become steps; 'review' / 'ask' language becomes operator checkpoints."
                value={description} onChange={(e) => setDescription(e.target.value)} />
              <button className="we-mini" style={{ marginTop: 8 }} onClick={generate}>✦ Generate steps</button>
            </section>

            {/* 2 Steps */}
            <section className="we-step-card">
              <div className="we-step-h"><span className="we-step-n">2</span> Steps <span className="we-muted">· drag to reorder</span></div>
              <div className="we-steps">
                {steps.map((s, i) => (
                  <div key={s.id} className={"we-step-edit we-step-" + s.kind}
                    draggable={s.kind !== "brief"}
                    onDragStart={() => onDragStart(i)} onDragOver={(e) => onDragOver(e, i)}>
                    <span className="we-step-grip">{s.kind === "brief" ? "⊡" : "⠿"}</span>
                    <input className="we-step-input" value={s.label} disabled={s.kind === "brief"} onChange={(e) => setLabel(s.id, e.target.value)} />
                    <StepKindTag kind={s.kind} />
                    {s.kind === "brief"
                      ? <span className="we-required">required</span>
                      : <button className="we-step-x" title="Remove" onClick={() => removeStep(s.id)}>×</button>}
                  </div>
                ))}
              </div>
              <div className="we-add-row">
                <button className="we-mini" onClick={() => addStep("work")}>＋ step</button>
                <button className="we-mini" onClick={() => addStep("checkpoint")}>＋ operator checkpoint</button>
              </div>
            </section>

            {/* 3 Triggers */}
            <section className="we-step-card">
              <div className="we-step-h"><span className="we-step-n">3</span> Triggers</div>
              <div className="we-trigs">
                {triggers.map((t, i) => (
                  <span key={i} className={"we-trig" + (t.on ? " is-on" : "") + (t.manual ? " is-fixed" : "")}
                    onClick={() => !t.manual && toggleTrigger(i)}
                    onDoubleClick={() => removeTrigger(i)}
                    title={t.manual ? "Manual is always available" : "Click to toggle · double-click to remove"}>
                    <span className="we-trig-dot" />{t.label}{t.manual && <span className="we-fixed-lbl">fixed</span>}
                  </span>
                ))}
              </div>
              <div className="we-add-row">
                <input className="we-input we-trig-input" placeholder="Add a custom trigger — e.g. On nightly schedule" value={newTrig}
                  onChange={(e) => setNewTrig(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") addTrigger(); }} />
                <button className="we-mini" onClick={addTrigger}>＋ add</button>
              </div>
            </section>
          </div>

          {/* 4 WORKFLOW.md live preview */}
          <div className="we-builder-preview">
            <div className="we-preview-h">WORKFLOW.md <span className="we-muted">· live</span></div>
            <pre className="we-md">{md}</pre>
          </div>
        </div>

        <div className="we-actions">
          <button className="we-mini" disabled={phase === "saving"} onClick={() => save(false)}>Save as draft</button>
          <button className="we-spawn-btn" disabled={phase === "saving"} onClick={() => save(true)}>{phase === "saving" ? "Saving…" : "Save workflow →"}</button>
          {phase === "error" && <span className="we-err">⊘ save failed</span>}
        </div>
      </div></div>
    );
  }

  /* ============================ C.4 — Template detail ============================ */
  function TemplateDetail({ tpl, runs, onBack, onSpawn, onEdit, onOpenRun }) {
    const [paused, setPaused] = useState(false);
    const history = runs.filter((r) => r.template === tpl.name).sort((a, b) => (b.created || 0) - (a.created || 0));
    // "feeds criteria" — DERIVED from where this template's runs were pointed
    // (relates:toward), never declared on the template (ADR-0048 S9.3).
    const feeds = useMemo(() => {
      const seen = new Set(), out = [];
      history.forEach((r) => (r.relates || []).forEach((rel) => {
        if (rel.kind !== "toward") return;
        const k = (rel.goal || "") + "|" + (rel.crit || "");
        if (seen.has(k)) return; seen.add(k); out.push(rel);
      }));
      return out;
    }, [history]);
    const trigLine = (tpl.triggers || []).map((t) => t.label).join(", ") || "Manual";

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light we-detail">
        <div className="we-head fx-in">
          <div>
            <button className="we-link" onClick={onBack}>← Work engine</button>
            <h1 className="fx-h1" style={{ marginTop: 6 }}>{tpl.name}</h1>
            <p className="fx-psub mono">triggers: [{trigLine}]</p>
          </div>
        </div>

        <div className="we-detail-grid">
          <div className="we-detail-main">
            <section className="we-step-card fx-in">
              <div className="we-step-h">Spec</div>
              {tpl.description && <p className="we-desc">{tpl.description}</p>}
              <ol className="we-spec-steps">
                {(tpl.steps || []).map((s) => (
                  <li key={s.id} className={"we-spec-step we-step-" + s.kind}>
                    <span>{s.label}</span><StepKindTag kind={s.kind} />
                  </li>
                ))}
              </ol>
              {(tpl.stop_conditions || []).length > 0 && (
                <div className="we-stopconds">
                  <div className="we-muted">Stop conditions (additions to done/blocked)</div>
                  {tpl.stop_conditions.map((c, i) => <div key={i} className="we-stopcond">+ {c}</div>)}
                </div>
              )}
            </section>

            <section className="we-step-card fx-in" style={{ animationDelay: ".03s" }}>
              <div className="we-step-h">Run history</div>
              <div className="we-rows">
                {history.map((r) => (
                  <div className="we-run-row" key={r.id} onClick={() => isActive(r.status) && onOpenRun(r)} role={isActive(r.status) ? "button" : undefined}
                    style={{ cursor: isActive(r.status) ? "pointer" : "default" }}>
                    <Pulse status={r.status} />
                    <div className="we-run-main">
                      <div className="we-run-top"><span className="we-run-label">{r.label}</span></div>
                      <div className="we-run-meta"><span className="we-ulid mono">{r.id}</span><span>{relMs(r.created)} ago</span></div>
                    </div>
                    <span className="we-run-status" style={{ color: (RUN_STATUS[r.status] || {}).c }}>{(RUN_STATUS[r.status] || {}).label}</span>
                  </div>
                ))}
                {history.length === 0 && <div className="we-empty"><div className="we-empty-sub">No runs from this workflow yet.</div></div>}
              </div>
            </section>

            <section className="we-step-card fx-in" style={{ animationDelay: ".06s" }}>
              <div className="we-step-h">Feeds criteria <span className="we-muted">· derived from where its runs were pointed</span></div>
              {feeds.length === 0
                ? <div className="we-muted">Not linked to a criterion yet — point a run at a goal to derive this.</div>
                : feeds.map((f, i) => <div key={i} className="we-feed">→ {f.goal} · {f.crit}</div>)}
            </section>
          </div>

          <div className="we-detail-rail">
            <div className="we-rail-h">Actions</div>
            <button className="we-spawn-btn we-rail-btn" onClick={() => onSpawn(tpl)}>＋ Spawn work</button>
            {!tpl.base && <button className="we-mini we-rail-btn" onClick={() => onEdit(tpl)}>Edit spec</button>}
            <button className="we-mini we-rail-btn" onClick={() => setPaused((p) => !p)}>{paused ? "Resume triggers" : "Pause triggers"}</button>
            {paused && <div className="we-paused-note">Triggers paused — only manual spawns run.</div>}
          </div>
        </div>
      </div></div>
    );
  }

  /* ============================ C.5 — Run view (no takeover) ============================ */
  function RunView({ run, onBack, onOpenArtifact }) {
    const id = run.id;
    const topic = RUN_TOPIC(id);
    const [thread, setThread] = useState([]); // run-topic posts re-read from the bus
    const [draft, setDraft] = useState("");
    const selfRef = useRef(window.SX && window.SX.self ? window.SX.self() : { id: "", name: "" });

    // Re-read the run thread from the bus (TASK-215 #6: after reload the post
    // appears, re-read from the bus, not local state). Page forward from 0.
    const loadThread = useCallback(() => {
      const SX = window.SX; if (!SX) return;
      SX.get("/api/messages?subject=" + encodeURIComponent(topic) + "&since=0&limit=200").then((res) => {
        const frames = (res && res.messages) || [];
        setThread(frames.map((f) => ({ id: f.id, author: f.author, text: (f.record && f.record.text) || "", at: Date.parse(f.createdAt || "") || ulidTime(f.id) })));
      }).catch(() => {});
    }, [topic]);
    useEffect(() => {
      loadThread();
      let stop = null, dead = false;
      if (window.SX && window.SX.subscribe) {
        window.SX.subscribe(topic, () => loadThread(), { deliverAll: false }).then((s) => { if (dead) s.stop(); else stop = s; }).catch(() => {});
      }
      const poll = setInterval(loadThread, 5000);
      return () => { dead = true; if (stop) stop.stop().catch(() => {}); clearInterval(poll); };
    }, [topic, loadThread]);

    function post() {
      const text = draft.trim(); if (!text) return;
      window.SX.publish(topic, { "$type": "chat.message", text }).then(() => { setDraft(""); loadThread(); }).catch(() => {});
    }

    // control publishes a cooperative run.control verb (approve/cancel) the
    // coordinator honours (TASK-225/226). The dash never writes the run envelope —
    // single-writer is the coordinator (ADR-0048); the 4s poll reflects the change.
    function control(verb) {
      window.SX.publish(RUN_CONTROL(id), { "$type": "run.control", verb }).catch(() => {});
    }

    const toward = (run.relates || []).find((x) => x.kind === "toward");
    const STEP_STATUS = {
      done: { glyph: "✓", cls: "is-done", label: "met" },
      running: { glyph: "◌", cls: "is-running", label: "in progress" },
      waiting: { glyph: "✦", cls: "is-waiting", label: "needs you" },
      upcoming: { glyph: "○", cls: "is-upcoming", label: "" },
    };

    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light we-run">
        {/* top bar: ULID + goal (no take-the-keyboard — §11 CUT) */}
        <div className="we-run-bar fx-in">
          <button className="we-link" onClick={onBack}>← Work engine</button>
          <div className="we-run-bar-main">
            <span className="we-ulid mono">{id}</span>
            <span className="we-run-goal">{toward ? "→ " + toward.goal + " · " + toward.crit : "no goal yet"}</span>
          </div>
          {isActive(run.status) && (
            <button className="we-mini we-run-stop" onClick={() => control("cancel")}>Stop run</button>
          )}
          <Pulse status={run.status} />
        </div>
        <h1 className="fx-h1 fx-in" style={{ animationDelay: ".02s" }}>{run.label}</h1>
        <p className="fx-psub fx-in" style={{ animationDelay: ".03s" }}>{run.objective || "—"}{run.template ? "  ·  via " + run.template : "  ·  ad-hoc"}</p>

        <div className="we-run-grid">
          <div className="we-run-col">
            {/* Workflow steps timeline */}
            <section className="we-step-card fx-in">
              <div className="we-step-h">Workflow steps</div>
              <div className="we-timeline">
                {(run.steps || []).map((s) => {
                  const st = STEP_STATUS[s.status] || STEP_STATUS.upcoming;
                  return (
                    <div key={s.id} className={"we-tl-step " + st.cls}>
                      <span className="we-tl-glyph">{st.glyph}</span>
                      <span className="we-tl-label">{s.label}</span>
                      {s.kind === "checkpoint" && s.status === "waiting" && <StepKindTag kind="checkpoint" />}
                      {s.kind === "brief" && <StepKindTag kind="brief" />}
                      {st.label && <span className="we-tl-status">{st.label}</span>}
                      {s.kind === "checkpoint" && s.status === "waiting" && (
                        <button className="we-mini we-tl-approve" onClick={() => control("approve")}>Approve</button>
                      )}
                    </div>
                  );
                })}
                {(run.steps || []).length === 0 && <div className="we-muted">No steps recorded.</div>}
              </div>
            </section>

            {/* Run timeline / activity log */}
            <section className="we-step-card fx-in" style={{ animationDelay: ".03s" }}>
              <div className="we-step-h">Run timeline</div>
              <div className="we-activity">
                {(run.activity || []).map((a) => (
                  <div key={a.id} className="we-act">
                    <span className="we-act-glyph">{a.glyph || "•"}</span>
                    <span className="we-act-text">{a.text}</span>
                    <span className="we-act-src mono">{(a.src || "").slice(0, 8)}</span>
                    <span className="we-act-time">{relMs(a.at)}</span>
                  </div>
                ))}
                {isActive(run.status) && (
                  <div className="we-act we-act-pending"><span className="we-act-glyph">◌</span><span className="we-act-text">…working…</span></div>
                )}
                {(run.activity || []).length === 0 && !isActive(run.status) && <div className="we-muted">No activity recorded.</div>}
              </div>
            </section>

            {/* Draft artifacts */}
            <section className="we-step-card fx-in" style={{ animationDelay: ".06s" }}>
              <div className="we-step-h">Draft artifacts</div>
              {(run.artifacts || []).length === 0
                ? <div className="we-muted">No artifacts produced yet.</div>
                : (run.artifacts || []).map((a) => (
                  <div key={a.name} className="we-artrow" onClick={() => onOpenArtifact && onOpenArtifact(a.name)} role="button" tabIndex={0}>
                    <span className="we-art-name">{a.name}</span>
                    <span className="we-art-kind">{a.kind}</span>
                    <span className="we-art-ver mono">v{a.version}</span>
                    <span className={"we-art-status we-st-" + (a.status || "draft")}>{a.status || "draft"}</span>
                  </div>
                ))}
            </section>
          </div>

          {/* Run topic composer (no takeover — steer by posting). A post is a live STEER:
              the coordinator routes it to the active step's worker (or applies it at the
              next step), and records it on the run timeline. Not a passive chat log. After
              the run ends, the coordinator answers a post with a "not applied" notice in
              the thread — never a silent drop (TASK-246). */}
          <div className="we-run-topic">
            <div className="we-rail-h">Steer the run</div>
            <div className="we-topic-note">{isActive(run.status)
              ? "Post to steer this run — the coordinator routes it to the working agent and records it on the timeline. No takeover needed."
              : "This run has ended. A post here is reported back as not-applied (it can no longer change the run)."}</div>
            <div className="we-thread">
              {thread.map((m) => (
                <div key={m.id} className={"we-post" + (m.author === selfRef.current.id ? " is-self" : "")}>
                  <div className="we-post-head"><span className="we-post-who">{m.author === selfRef.current.id ? "you" : (m.author || "").slice(0, 8)}</span><span className="we-post-time">{relMs(m.at)}</span></div>
                  <div className="we-post-text">{m.text}</div>
                </div>
              ))}
              {thread.length === 0 && <div className="we-muted" style={{ padding: "8px 0" }}>No posts yet — say something to the run.</div>}
            </div>
            <div className="we-topic-composer">
              <textarea className="we-input" rows={2} placeholder={isActive(run.status) ? "Steer the run… e.g. 'write it to its own artifact'" : "Post to the run…"} value={draft}
                onChange={(e) => setDraft(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); post(); } }} />
              <button className="we-mini" disabled={!draft.trim()} onClick={post}>Post ↵</button>
            </div>
          </div>
        </div>
      </div></div>
    );
  }

  /* ============================ the lane root ============================ */
  // WorkEngineView owns the sub-surface routing internally so app.jsx stays
  // untouched beyond the one route. view = { name, payload }.
  function WorkEngineView({ goals, artifacts, onOpenArtifact, renderWiki }) {
    const { templates, runs, reload } = useWorkEngineData();
    const [view, setView] = useState({ name: "list" });
    // a run spawned/opened this session, held locally so the run view shows it
    // immediately even before the 4s poll re-reads it from the bus.
    const [localRun, setLocalRun] = useState(null);

    // re-resolve the open run/template against fresh bus data so a poll update flows in.
    const openRun = useMemo(() => {
      if (view.name !== "run") return null;
      const fromBus = runs.find((r) => r.id === (view.payload && view.payload.id));
      return fromBus || localRun || view.payload;
    }, [view, runs, localRun]);
    const openTpl = useMemo(() => {
      if (view.name !== "template") return null;
      return templates.find((t) => t.name === (view.payload && view.payload.name)) || view.payload;
    }, [view, templates]);

    const goList = () => { reload(); setView({ name: "list" }); };

    if (view.name === "spawn") {
      return <SpawnWork templates={templates} goals={goals || []} initial={view.payload}
        onCancel={goList} onNewWorkflow={() => setView({ name: "builder" })}
        onSpawned={(run) => { setLocalRun(run); reload(); setView({ name: "run", payload: run }); }} />;
    }
    if (view.name === "builder") {
      return <WorkflowBuilder initial={view.payload}
        onCancel={() => setView(view.payload ? { name: "template", payload: view.payload } : { name: "list" })}
        onSaved={(tpl, open) => { reload(); setView(open ? { name: "template", payload: tpl } : { name: "list" }); }} />;
    }
    if (view.name === "template" && openTpl) {
      return <TemplateDetail tpl={openTpl} runs={runs} onBack={goList}
        onSpawn={(tpl) => setView({ name: "spawn", payload: tpl })}
        onEdit={(tpl) => setView({ name: "builder", payload: tpl })}
        onOpenRun={(r) => { setLocalRun(r); setView({ name: "run", payload: r }); }} />;
    }
    if (view.name === "run" && openRun) {
      return <RunView run={openRun} onBack={goList} onOpenArtifact={onOpenArtifact} />;
    }
    return <WorkEngineList templates={templates} runs={runs}
      onSpawn={() => setView({ name: "spawn" })}
      onNewWorkflow={() => setView({ name: "builder" })}
      onOpenTemplate={(t) => setView({ name: "template", payload: t })}
      onEditTemplate={(t) => setView({ name: "builder", payload: t })}
      onOpenRun={(r) => { setLocalRun(r); setView({ name: "run", payload: r }); }} />;
  }

  Object.assign(window, { WorkEngineView });
})();
