/* workflow.jsx — Operator-triggered mobilize: spawn a real-identity agent from a
   prompt, then DM it immediately.

   Wired to msg.topic.spawn (the spawn.request / spawn.ack protocol from
   cmd/sextant-dispatch/records.go). On Mobilize, publishes a spawn.request and
   then polls /api/clients every ~800ms for up to ~10s. Detects the newly-minted
   agent as any new kind=agent client (whose DisplayName matches the requested
   nickname when one is given). Success → show the spawned agent + "Message" button.
   No new agent within ~10s → fail-loud state ("No dispatcher is listening —
   start one with `sextant-dispatch`").

   The "Start a workflow" second action (WorkflowStartCard, v0.5.3 S3) publishes a
   workflow.start on msg.topic.workflow.start with a dash-generated `nonce`, listens
   on the bus stream for the workflow.start.ack echoing that nonce, then tracks the
   run by polling the workflow.<id> state artifact until it's terminal. No ack within
   ~10s → fail-loud ("No workflow runner is listening — start one with
   `sextant-workflow`"). Wire contract: artifact `workflow-start-contract`.

   Exports WorkflowView to window. */
(function () {
  const { useState, useRef, useEffect } = React;

  const SPAWN_SUBJECT = "msg.topic.spawn";
  const POLL_INTERVAL_MS = 800;
  const TIMEOUT_MS = 10000;

  // workflow.start action (S3): subject, ack timeout, run-state poll cadence + cap.
  const WORKFLOW_SUBJECT = "msg.topic.workflow.start";
  const ACK_TIMEOUT_MS = 10000;
  const STATE_POLL_MS = 1000;
  const STATE_POLL_CAP_MS = 600000; // stop polling run-state after ~10min (leave last-known)

  /* The data layer is window.SX (set up by app.jsx, ADR-0044): every read/write
     goes over the one bus Client (wss), not the deleted Go relay. wfGet/wfPost keep
     their names + path shape so the view body is unchanged — wfPost("/api/publish",
     {subject, record}) becomes a bus publish; wfGet("/api/clients" | "/api/artifacts
     /<name>") becomes a bus read. */
  function wfPost(path, body) {
    if (path === "/api/publish") return window.SX.publish(body.subject, body.record);
    return Promise.reject(new Error("workflow: no bus route for POST " + path));
  }
  function wfGet(path) {
    return window.SX.get(path);
  }

  // wfNonce — an opaque per-publish correlation handle (workflow-start-contract).
  // The dash matches the ack to its own request on this; it is NOT the fence key
  // (that stays the unforgeable bus Frame.ID on the consumer side).
  function wfNonce() {
    try { if (window.crypto && window.crypto.randomUUID) return window.crypto.randomUUID(); } catch (_) {}
    return "wf-" + Date.now() + "-" + Math.floor(Math.random() * 1e9);
  }

  /* WorkflowStartCard — the 2nd Workflow action (v0.5.3 S3). Publishes a
     workflow.start{prompt,nonce,nickname?,target?} on msg.topic.workflow.start, then
     correlates the workflow.start.ack by nonce off the bus stream, then tracks the
     run via the workflow.<id> state artifact. Self-contained (own fetch wrappers +
     EventSource); reuses the mobilize card's wf-* classes. Phases:
       idle → sending → waiting (ack) → running (poll state) → done | failed
       any publish/timeout/error → error (fail-loud). */
  function WorkflowStartCard() {
    const [prompt, setPrompt] = useState("");
    const [nickname, setNickname] = useState("");
    const [target, setTarget] = useState("");
    const [phase, setPhase] = useState("idle"); // idle|sending|waiting|running|done|failed|error
    const [run, setRun] = useState(null);        // { id, status, done, total }
    const [errMsg, setErrMsg] = useState("");

    const mountedRef = useRef(true);
    const subRef = useRef(null); // the bus subscription stop-handle (was an EventSource)
    const ackTimerRef = useRef(null);
    const pollRef = useRef(null);

    function stopSub() {
      if (subRef.current) { try { subRef.current.stop(); } catch (_) {} subRef.current = null; }
    }
    function cleanup() {
      stopSub();
      if (ackTimerRef.current) { clearTimeout(ackTimerRef.current); ackTimerRef.current = null; }
      if (pollRef.current) { clearTimeout(pollRef.current); pollRef.current = null; }
    }
    useEffect(function () { return function () { mountedRef.current = false; cleanup(); }; }, []);

    function fail(msg) {
      if (!mountedRef.current) return;
      cleanup();
      setErrMsg(msg);
      setPhase("error");
    }

    // Poll the workflow.<id> state artifact (sextant.workflow/v1) until terminal.
    function pollState(id, deadline) {
      if (!mountedRef.current || Date.now() > deadline) return;
      wfGet("/api/artifacts/" + encodeURIComponent("workflow." + id)).then(function (a) {
        if (!mountedRef.current) return;
        var rec = (a && a.Record) || null;
        if (rec && rec["$type"] === "sextant.workflow/v1") {
          var steps = Array.isArray(rec.steps) ? rec.steps : [];
          var done = steps.filter(function (s) { return s && s.status === "done"; }).length;
          setRun({ id: id, status: rec.status || "running", done: done, total: steps.length });
          if (rec.status === "done") { setPhase("done"); return; }
          if (rec.status === "failed" || rec.status === "cancelled") { setPhase("failed"); return; }
        }
        // not terminal yet (or the artifact hasn't appeared) — keep polling
        pollRef.current = setTimeout(function () { pollState(id, deadline); }, STATE_POLL_MS);
      }).catch(function () {
        if (!mountedRef.current) return;
        pollRef.current = setTimeout(function () { pollState(id, deadline); }, STATE_POLL_MS);
      });
    }

    function handleStart() {
      var p = prompt.trim();
      if (!p) return;
      cleanup();
      setPhase("sending"); setRun(null); setErrMsg("");
      var nonce = wfNonce();
      var acked = false; // true once we've reached a terminal outcome (ack/error/timeout) — single-transition guard
      // Single fail-loud deadline, armed BEFORE the stream opens so it fires even if
      // onopen never does (a fatal or stuck stream) — not only when a live consumer
      // never acks. Setting `acked` first blocks a late onmessage double-transition.
      ackTimerRef.current = setTimeout(function () {
        if (acked || !mountedRef.current) return;
        acked = true;
        fail("No workflow runner is listening — start one with `sextant-workflow`");
      }, ACK_TIMEOUT_MS);
      // Subscribe to the ack subject BEFORE publishing so the ack (same subject)
      // can't slip past before we're subscribed (ADR-0044: a real bus subscription
      // over wss, replacing the SSE stream). deliverAll:false — only new frames,
      // since the ack is a fresh reply to this publish.
      window.SX.subscribe(WORKFLOW_SUBJECT, function (ev) {
        if (acked || !mountedRef.current) return;
        var rec = ev && ev.frame && ev.frame.record;
        if (!rec || rec["$type"] !== "workflow.start.ack" || rec.nonce !== nonce) return;
        acked = true;
        if (ackTimerRef.current) { clearTimeout(ackTimerRef.current); ackTimerRef.current = null; }
        stopSub();
        if (rec.status === "error" || !rec.workflowId) {
          fail(rec.error ? ("Workflow runner error: " + rec.error) : "Workflow runner returned an error");
          return;
        }
        setRun({ id: rec.workflowId, status: "running", done: 0, total: 0 });
        setPhase("running");
        pollState(rec.workflowId, Date.now() + STATE_POLL_CAP_MS);
      }, { deliverAll: false }).then(function (sub) {
        // If we already acked/unmounted while subscribing, drop the late sub.
        if (acked || !mountedRef.current) { try { sub.stop(); } catch (_) {} return; }
        subRef.current = sub;
        // Subscribed: publish the workflow.start now that the ack can't be missed.
        var record = { "$type": "workflow.start", prompt: p, nonce: nonce };
        var nk = nickname.trim(); if (nk) record.nickname = nk;
        var tg = target.trim(); if (tg) record.target = tg;
        wfPost("/api/publish", { subject: WORKFLOW_SUBJECT, record: record })
          .then(function () { if (mountedRef.current && !acked) setPhase("waiting"); })
          .catch(function (e) { if (acked) return; acked = true; fail("Failed to publish workflow.start: " + (e && e.message ? e.message : String(e))); });
      }).catch(function (e) {
        if (acked || !mountedRef.current) return;
        acked = true;
        fail("Lost the bus before the workflow runner replied: " + (e && e.message ? e.message : String(e)));
      });
    }

    function handleReset() { cleanup(); setPhase("idle"); setRun(null); setErrMsg(""); }

    var busy = phase === "sending" || phase === "waiting" || phase === "running";
    var terminal = phase === "done" || phase === "failed" || phase === "error";

    return (
      <div className="wf-card fx-in" style={{ animationDelay: ".10s" }}>
        <div className="wf-card-header">
          <span className="wf-card-ic">⬡</span>
          <div>
            <div className="wf-card-title">Start a workflow</div>
            <div className="wf-card-sub">
              A runner on <span className="mono">msg.topic.workflow.start</span> runs a multi-step workflow from your prompt.
            </div>
          </div>
        </div>

        <label className="wf-label" htmlFor="wf-wprompt">Prompt</label>
        <textarea
          id="wf-wprompt"
          className={"wf-textarea" + (busy ? " is-disabled" : "")}
          rows={4}
          placeholder="Describe the workflow — e.g. 'Plan, implement, and review a fix for TASK-123, then open a PR'"
          value={prompt}
          disabled={busy}
          onChange={function (e) { setPrompt(e.target.value); }}
        />

        <label className="wf-label" htmlFor="wf-wnick" style={{ marginTop: "12px" }}>
          Nickname <span className="wf-optional">(optional)</span>
        </label>
        <input
          id="wf-wnick"
          className={"wf-input" + (busy ? " is-disabled" : "")}
          type="text"
          placeholder="e.g. fix-task-123"
          value={nickname}
          disabled={busy}
          onChange={function (e) { setNickname(e.target.value); }}
        />

        <label className="wf-label" htmlFor="wf-wtarget" style={{ marginTop: "12px" }}>
          Target goal/artifact <span className="wf-optional">(optional)</span>
        </label>
        <input
          id="wf-wtarget"
          className={"wf-input" + (busy ? " is-disabled" : "")}
          type="text"
          placeholder="e.g. goal.v0-5-3"
          value={target}
          disabled={busy}
          onChange={function (e) { setTarget(e.target.value); }}
        />

        {(phase === "sending" || phase === "waiting") && (
          <div className="wf-status wf-status--polling">
            <span className="wf-spin" aria-hidden="true">◌</span>
            {phase === "sending" ? "Publishing workflow.start…" : "Waiting for a workflow runner…"}
          </div>
        )}
        {phase === "running" && run && (
          <div className="wf-status wf-status--polling">
            <span className="wf-spin" aria-hidden="true">◌</span>
            <span>Running <span className="mono">{run.id}</span>{run.total ? " · " + run.done + "/" + run.total + " steps" : ""}…</span>
          </div>
        )}
        {phase === "done" && run && (
          <div className="wf-status wf-status--ok">
            <span className="wf-status-ic">✓</span>
            <div className="wf-ok-body">
              <div className="wf-ok-name">Workflow done</div>
              <div className="wf-ok-id mono">{run.id}{run.total ? " · " + run.done + "/" + run.total + " steps" : ""}</div>
            </div>
          </div>
        )}
        {phase === "failed" && run && (
          <div className="wf-status wf-status--error">
            <span className="wf-status-ic">⊘</span>
            <span>Workflow {run.status} — <span className="mono">{run.id}</span></span>
          </div>
        )}
        {phase === "error" && (
          <div className="wf-status wf-status--error">
            <span className="wf-status-ic">⊘</span>
            <span>{errMsg}</span>
          </div>
        )}

        <div className="wf-actions">
          {terminal ? (
            <button className="wf-btn-secondary" onClick={handleReset}>Start another</button>
          ) : (
            <button
              className="wf-btn-primary"
              disabled={!prompt.trim() || busy}
              onClick={handleStart}
            >
              {phase === "sending" ? "Starting…" : phase === "waiting" ? "Waiting…" : phase === "running" ? "Running…" : "Start workflow"}
            </button>
          )}
        </div>
      </div>
    );
  }

  /* WorkflowView — the Workflow page canvas.
     Props:
       onDM(agentId) — calls app.jsx's startDM to open the DM conversation */
  function WorkflowView({ onDM }) {
    const [prompt, setPrompt] = useState("");
    const [nickname, setNickname] = useState("");

    // phase: idle | sending | polling | success | error
    const [phase, setPhase] = useState("idle");
    const [spawned, setSpawned] = useState(null);  // { id, nickname } on success
    const [errMsg, setErrMsg] = useState("");

    const mountedRef = useRef(true);
    useEffect(function() { return function() { mountedRef.current = false; }; }, []);

    // Poll /api/clients until we spot a new kind=agent client or time out.
    // knownIds: Set of agent IDs that existed before we published.
    // requestedNick: the nickname the operator typed (may be "").
    function pollForAgent(requestedNick, knownIds, deadline, setSpawnedFn, setPhFn, setErrFn) {
      if (!mountedRef.current) return;
      if (Date.now() > deadline) {
        setErrFn("No dispatcher is listening — start one with `sextant-dispatch`");
        setPhFn("error");
        return;
      }
      wfGet("/api/clients").then(function(cs) {
        if (!mountedRef.current) return;
        var agents = (Array.isArray(cs) ? cs : [])
          .filter(function(c) { return c.Kind !== "client" && c.Kind !== "human"; });
        var nick = requestedNick.trim().toLowerCase();
        var found = null;
        for (var i = 0; i < agents.length; i++) {
          var c = agents[i];
          if (knownIds.has(c.ID)) continue; // pre-existing
          if (!nick || (c.DisplayName && c.DisplayName.toLowerCase() === nick)) {
            found = c;
            break;
          }
        }
        // fallback: any new agent when no nickname filter
        if (!found && !nick) {
          for (var j = 0; j < agents.length; j++) {
            if (!knownIds.has(agents[j].ID)) { found = agents[j]; break; }
          }
        }
        if (found) {
          setSpawnedFn({ id: found.ID, nickname: found.DisplayName });
          setPhFn("success");
        } else {
          setTimeout(function() {
            pollForAgent(requestedNick, knownIds, deadline, setSpawnedFn, setPhFn, setErrFn);
          }, POLL_INTERVAL_MS);
        }
      }).catch(function() {
        if (!mountedRef.current) return;
        setTimeout(function() {
          pollForAgent(requestedNick, knownIds, deadline, setSpawnedFn, setPhFn, setErrFn);
        }, POLL_INTERVAL_MS);
      });
    }

    function handleMobilize() {
      var p = prompt.trim();
      if (!p) return;
      setPhase("sending");
      setSpawned(null);
      setErrMsg("");

      // capture stable references to setters for async callbacks
      var setSpawnedFn = setSpawned;
      var setPhFn = setPhase;
      var setErrFn = setErrMsg;

      // snapshot known agent IDs before publishing
      var knownIds = new Set();
      wfGet("/api/clients").then(function(cs) {
        if (Array.isArray(cs)) {
          cs.filter(function(c) { return c.Kind !== "client" && c.Kind !== "human"; })
            .forEach(function(c) { knownIds.add(c.ID); });
        }
        var record = { "$type": "spawn.request", "prompt": p };
        var nick = nickname.trim();
        if (nick) record["nickname"] = nick;
        return wfPost("/api/publish", { subject: SPAWN_SUBJECT, record: record })
          .then(function() {
            if (!mountedRef.current) return;
            setPhFn("polling");
            var deadline = Date.now() + TIMEOUT_MS;
            setTimeout(function() {
              pollForAgent(nick, knownIds, deadline, setSpawnedFn, setPhFn, setErrFn);
            }, POLL_INTERVAL_MS);
          });
      }).catch(function(e) {
        if (!mountedRef.current) return;
        setErrFn("Failed to publish spawn.request: " + (e && e.message ? e.message : String(e)));
        setPhFn("error");
      });
    }

    function handleReset() {
      setPhase("idle");
      setSpawned(null);
      setErrMsg("");
    }

    var busy = phase === "sending" || phase === "polling";

    return (
      <div className="fx-scroll">
        <div className="fx-col">
          <h1 className="fx-h1 fx-in">Workflow</h1>
          <p className="fx-psub fx-in" style={{ animationDelay: ".03s" }}>
            Spawn a real-identity agent from a prompt — it gets its own bus credentials and DM channel.
          </p>

          {/* ---- spawn card ---- */}
          <div className="wf-card fx-in" style={{ animationDelay: ".06s" }}>
            <div className="wf-card-header">
              <span className="wf-card-ic">⬡</span>
              <div>
                <div className="wf-card-title">Mobilize an agent</div>
                <div className="wf-card-sub">
                  A dispatcher on <span className="mono">msg.topic.spawn</span> mints a scoped identity and launches the agent.
                </div>
              </div>
            </div>

            <label className="wf-label" htmlFor="wf-prompt">Prompt</label>
            <textarea
              id="wf-prompt"
              className={"wf-textarea" + (busy ? " is-disabled" : "")}
              rows={4}
              placeholder="Describe what the agent should do — e.g. 'Review the open PRs against the acceptance criteria and post a summary artifact'"
              value={prompt}
              disabled={busy}
              onChange={function(e) { setPrompt(e.target.value); }}
            />

            <label className="wf-label" htmlFor="wf-nick" style={{ marginTop: "12px" }}>
              Nickname <span className="wf-optional">(optional)</span>
            </label>
            <input
              id="wf-nick"
              className={"wf-input" + (busy ? " is-disabled" : "")}
              type="text"
              placeholder="e.g. reviewer-1"
              value={nickname}
              disabled={busy}
              onChange={function(e) { setNickname(e.target.value); }}
            />

            {(phase === "sending" || phase === "polling") && (
              <div className="wf-status wf-status--polling">
                <span className="wf-spin" aria-hidden="true">◌</span>
                {phase === "sending" ? "Publishing spawn.request…" : "Waiting for dispatcher to mint and launch…"}
              </div>
            )}
            {phase === "error" && (
              <div className="wf-status wf-status--error">
                <span className="wf-status-ic">⊘</span>
                <span>{errMsg}</span>
                <button className="wf-retry" onClick={handleReset}>Try again</button>
              </div>
            )}
            {phase === "success" && spawned && (
              <div className="wf-status wf-status--ok">
                <span className="wf-status-ic">✓</span>
                <div className="wf-ok-body">
                  <div className="wf-ok-name">{spawned.nickname || spawned.id}</div>
                  <div className="wf-ok-id mono">{spawned.id}</div>
                </div>
                <button
                  className="wf-msg-btn"
                  onClick={function() { onDM && onDM(spawned.id); }}
                  title={"Open DM with " + (spawned.nickname || spawned.id)}
                >
                  Message →
                </button>
              </div>
            )}

            <div className="wf-actions">
              {phase === "success" ? (
                <button className="wf-btn-secondary" onClick={handleReset}>Mobilize another</button>
              ) : (
                <button
                  className="wf-btn-primary"
                  disabled={!prompt.trim() || busy}
                  onClick={handleMobilize}
                >
                  {busy ? "Mobilizing…" : "Mobilize"}
                </button>
              )}
            </div>
          </div>

          {/* ---- Start a workflow (v0.5.3 S3) ---- */}
          <WorkflowStartCard />
        </div>
      </div>
    );
  }

  Object.assign(window, { WorkflowView });
})();
