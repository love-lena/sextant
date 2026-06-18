/* workflow.jsx — Operator-triggered mobilize: spawn a real-identity agent from a
   prompt, then DM it immediately.

   Wired to msg.topic.spawn (the spawn.request / spawn.ack protocol from
   cmd/sextant-dispatch/records.go). On Mobilize, publishes a spawn.request and
   then polls /api/clients every ~800ms for up to ~10s. Detects the newly-minted
   agent as any new kind=agent client (whose DisplayName matches the requested
   nickname when one is given). Success → show the spawned agent + "Message" button.
   No new agent within ~10s → fail-loud state ("No dispatcher is listening —
   start one with `sextant-dispatch`").

   The "Start a workflow" second action is a DISABLED placeholder only — no consumer
   exists this slice, so we never publish a dead no-op.

   Exports WorkflowView to window. */
(function () {
  const { useState, useRef, useEffect } = React;

  const SPAWN_SUBJECT = "msg.topic.spawn";
  const POLL_INTERVAL_MS = 800;
  const TIMEOUT_MS = 10000;

  /* apiPublish + apiGet are defined inside app.jsx's closure and are not exposed on
     window. We replicate the minimal fetch wrappers here so WorkflowView is
     self-contained with no shared-closure coupling. */
  const TOKEN = new URLSearchParams(location.search).get("token") || "";
  const AUTH = { "Authorization": "Bearer " + TOKEN };

  function wfPost(path, body) {
    return fetch(path, {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, AUTH),
      body: JSON.stringify(body),
    }).then(function(r) { if (!r.ok) throw new Error(path + " -> " + r.status); });
  }
  function wfGet(path) {
    return fetch(path, { headers: AUTH })
      .then(function(r) { if (!r.ok) throw new Error(path + " -> " + r.status); return r.json(); });
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

          {/* ---- disabled placeholder: Start a workflow ---- */}
          <div className="wf-card wf-card--disabled fx-in" style={{ animationDelay: ".10s" }}>
            <div className="wf-card-header">
              <span className="wf-card-ic wf-card-ic--muted">⬡</span>
              <div>
                <div className="wf-card-title wf-card-title--muted">Start a workflow — coming soon</div>
                <div className="wf-card-sub">
                  Run a multi-step workflow definition. Requires a workflow consumer on the bus.
                </div>
              </div>
            </div>
            <div className="wf-actions">
              <button className="wf-btn-primary" disabled>Start a workflow</button>
            </div>
          </div>
        </div>
      </div>
    );
  }

  Object.assign(window, { WorkflowView });
})();
