/* mobilize.jsx — reusable MobilizeButton: a thin button that opens a prompt
   popover pre-seeded from its context (artifact name or goal northstar), then
   publishes a spawn.request to msg.topic.spawn.

   Attaches to artifact cards (artifacts.jsx) and goal portfolio cards (goals.jsx).
   Uses the existing dash token system — no new visual language.

   Props:
     context  { type: "artifact", name: string }
              | { type: "goal", northstar: string, id: string }
              | { label: string }   (generic fallback)
     onDM(agentId)  optional — if the spawned agent is detected via /api/clients
                    polling within ~10s, called with the minted id so the caller
                    can open a DM. If not detected in time, the popover shows
                    "Spawn request sent — check Agents list" (still fail-loud about
                    the fact the agent may be coming).

   Exports MobilizeButton to window. */
(function () {
  const { useState, useRef, useEffect } = React;

  const SPAWN_SUBJECT = "msg.topic.spawn";
  const POLL_INTERVAL_MS = 800;
  const TIMEOUT_MS = 10000;

  // The data layer is window.SX (app.jsx, ADR-0044): reads/writes go over the one
  // bus Client (wss), not the deleted Go relay. mbGet/mbPost keep their names + path
  // shape so the view body is unchanged.
  function mbPost(path, body) {
    if (path === "/api/publish") return window.SX.publish(body.subject, body.record);
    return Promise.reject(new Error("mobilize: no bus route for POST " + path));
  }
  function mbGet(path) {
    return window.SX.get(path);
  }

  function seedPrompt(context) {
    if (!context) return "";
    if (context.type === "artifact") return "Interpret and act on [[" + context.name + "]]";
    if (context.type === "goal") return "Advance: " + (context.northstar || context.id || "");
    return context.label || "";
  }

  // phase: idle | open | sending | polling | sent | error
  function MobilizeButton({ context, onDM }) {
    const [phase, setPhase] = useState("idle");
    const [promptText, setPromptText] = useState("");
    const [errMsg, setErrMsg] = useState("");
    const [spawnedId, setSpawnedId] = useState(null);
    const popoverRef = useRef(null);
    const mountedRef = useRef(true);
    useEffect(function() { return function() { mountedRef.current = false; }; }, []);

    // close on click outside the popover
    useEffect(function() {
      if (phase === "idle") return;
      function onDown(e) {
        if (popoverRef.current && !popoverRef.current.contains(e.target)) {
          setPhase("idle");
        }
      }
      document.addEventListener("mousedown", onDown);
      return function() { document.removeEventListener("mousedown", onDown); };
    }, [phase]);

    function openPopover(e) {
      e.stopPropagation();
      e.preventDefault();
      setPromptText(seedPrompt(context));
      setErrMsg("");
      setSpawnedId(null);
      setPhase("open");
    }

    function pollForAgent(knownIds, deadline, setSpawnedFn, setPhFn) {
      if (!mountedRef.current) return;
      if (Date.now() > deadline) {
        // timed out — still show "sent" (the publish DID go through)
        setPhFn("sent");
        return;
      }
      mbGet("/api/clients").then(function(cs) {
        if (!mountedRef.current) return;
        var agents = (Array.isArray(cs) ? cs : [])
          .filter(function(c) { return c.Kind !== "client" && c.Kind !== "human"; });
        var found = null;
        for (var i = 0; i < agents.length; i++) {
          if (!knownIds.has(agents[i].ID)) { found = agents[i]; break; }
        }
        if (found) {
          setSpawnedFn(found.ID);
          setPhFn("sent");
          if (onDM) onDM(found.ID);
        } else {
          setTimeout(function() {
            pollForAgent(knownIds, deadline, setSpawnedFn, setPhFn);
          }, POLL_INTERVAL_MS);
        }
      }).catch(function() {
        if (!mountedRef.current) return;
        setTimeout(function() {
          pollForAgent(knownIds, deadline, setSpawnedFn, setPhFn);
        }, POLL_INTERVAL_MS);
      });
    }

    function handleSend(e) {
      e.stopPropagation();
      e.preventDefault();
      var p = promptText.trim();
      if (!p) return;
      setPhase("sending");
      setErrMsg("");

      var setSpawnedFn = setSpawnedId;
      var setPhFn = setPhase;

      var knownIds = new Set();
      mbGet("/api/clients").then(function(cs) {
        if (Array.isArray(cs)) {
          cs.filter(function(c) { return c.Kind !== "client" && c.Kind !== "human"; })
            .forEach(function(c) { knownIds.add(c.ID); });
        }
        return mbPost("/api/publish", {
          subject: SPAWN_SUBJECT,
          record: { "$type": "spawn.request", "prompt": p },
        });
      }).then(function() {
        if (!mountedRef.current) return;
        setPhFn("polling");
        setTimeout(function() {
          pollForAgent(knownIds, Date.now() + TIMEOUT_MS, setSpawnedFn, setPhFn);
        }, POLL_INTERVAL_MS);
      }).catch(function(err) {
        if (!mountedRef.current) return;
        setErrMsg("Publish failed: " + (err && err.message ? err.message : String(err)));
        setPhFn("error");
      });
    }

    function handleClose(e) {
      if (e) { e.stopPropagation(); e.preventDefault(); }
      setPhase("idle");
    }

    var busy = phase === "sending" || phase === "polling";

    if (phase === "idle") {
      return (
        <button
          className="mb-btn"
          title={"Mobilize agent" + (context && context.name ? " on " + context.name : "")}
          onClick={openPopover}
          type="button"
        >
          ⬡ Mobilize
        </button>
      );
    }

    return (
      <div className="mb-anchor" onClick={function(e) { e.stopPropagation(); }}>
        <button className="mb-btn mb-btn--active" type="button" onClick={handleClose}>
          ⬡ Mobilize
        </button>
        <div className="mb-popover" ref={popoverRef} role="dialog" aria-label="Mobilize agent">
          <div className="mb-pop-head">
            <span className="mb-pop-title">Mobilize agent</span>
            <button className="mb-pop-x" aria-label="Close" onClick={handleClose} type="button">×</button>
          </div>

          {(phase === "open" || phase === "sending" || phase === "polling" || phase === "error") && (
            <div>
              <label className="wf-label" htmlFor="mb-prompt">Prompt</label>
              <textarea
                id="mb-prompt"
                className={"wf-textarea mb-ta" + (busy ? " is-disabled" : "")}
                rows={3}
                value={promptText}
                disabled={busy}
                onChange={function(e) { setPromptText(e.target.value); }}
                autoFocus
              />
              {phase === "error" && (
                <div className="wf-status wf-status--error" style={{ marginTop: "8px" }}>
                  <span className="wf-status-ic">⊘</span>
                  <span>{errMsg}</span>
                </div>
              )}
              {(phase === "sending" || phase === "polling") && (
                <div className="wf-status wf-status--polling" style={{ marginTop: "8px" }}>
                  <span className="wf-spin" aria-hidden="true">◌</span>
                  {phase === "sending" ? "Publishing…" : "Waiting for dispatcher…"}
                </div>
              )}
              <div className="mb-pop-actions">
                <button
                  className="wf-btn-primary mb-btn-send"
                  disabled={!promptText.trim() || busy}
                  onClick={handleSend}
                  type="button"
                >
                  {busy ? "Mobilizing…" : "Send"}
                </button>
              </div>
            </div>
          )}

          {phase === "sent" && (
            <div className="mb-sent">
              {spawnedId ? (
                <React.Fragment>
                  <span className="wf-status-ic" style={{ color: "var(--met)" }}>✓</span>
                  <span className="mb-sent-txt">Agent spawned</span>
                  {onDM && (
                    <button className="wf-msg-btn" type="button"
                      onClick={function() { onDM(spawnedId); handleClose(); }}>
                      Message →
                    </button>
                  )}
                </React.Fragment>
              ) : (
                <React.Fragment>
                  <span className="wf-status-ic" style={{ color: "var(--prog)" }}>↑</span>
                  <span className="mb-sent-txt">Spawn request sent — check Agents list</span>
                </React.Fragment>
              )}
            </div>
          )}
        </div>
      </div>
    );
  }

  Object.assign(window, { MobilizeButton });
})();
