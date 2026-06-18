(function() {
  const { useState, useRef, useEffect } = React;
  const SPAWN_SUBJECT = "msg.topic.spawn";
  const POLL_INTERVAL_MS = 800;
  const TIMEOUT_MS = 1e4;
  const TOKEN = new URLSearchParams(location.search).get("token") || "";
  const AUTH = { "Authorization": "Bearer " + TOKEN };
  function wfPost(path, body) {
    return fetch(path, {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, AUTH),
      body: JSON.stringify(body)
    }).then(function(r) {
      if (!r.ok) throw new Error(path + " -> " + r.status);
    });
  }
  function wfGet(path) {
    return fetch(path, { headers: AUTH }).then(function(r) {
      if (!r.ok) throw new Error(path + " -> " + r.status);
      return r.json();
    });
  }
  function WorkflowView({ onDM }) {
    const [prompt, setPrompt] = useState("");
    const [nickname, setNickname] = useState("");
    const [phase, setPhase] = useState("idle");
    const [spawned, setSpawned] = useState(null);
    const [errMsg, setErrMsg] = useState("");
    const mountedRef = useRef(true);
    useEffect(function() {
      return function() {
        mountedRef.current = false;
      };
    }, []);
    function pollForAgent(requestedNick, knownIds, deadline, setSpawnedFn, setPhFn, setErrFn) {
      if (!mountedRef.current) return;
      if (Date.now() > deadline) {
        setErrFn("No dispatcher is listening \u2014 start one with `sextant-dispatch`");
        setPhFn("error");
        return;
      }
      wfGet("/api/clients").then(function(cs) {
        if (!mountedRef.current) return;
        var agents = (Array.isArray(cs) ? cs : []).filter(function(c2) {
          return c2.Kind !== "client" && c2.Kind !== "human";
        });
        var nick = requestedNick.trim().toLowerCase();
        var found = null;
        for (var i = 0; i < agents.length; i++) {
          var c = agents[i];
          if (knownIds.has(c.ID)) continue;
          if (!nick || c.DisplayName && c.DisplayName.toLowerCase() === nick) {
            found = c;
            break;
          }
        }
        if (!found && !nick) {
          for (var j = 0; j < agents.length; j++) {
            if (!knownIds.has(agents[j].ID)) {
              found = agents[j];
              break;
            }
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
      var setSpawnedFn = setSpawned;
      var setPhFn = setPhase;
      var setErrFn = setErrMsg;
      var knownIds = /* @__PURE__ */ new Set();
      wfGet("/api/clients").then(function(cs) {
        if (Array.isArray(cs)) {
          cs.filter(function(c) {
            return c.Kind !== "client" && c.Kind !== "human";
          }).forEach(function(c) {
            knownIds.add(c.ID);
          });
        }
        var record = { "$type": "spawn.request", "prompt": p };
        var nick = nickname.trim();
        if (nick) record["nickname"] = nick;
        return wfPost("/api/publish", { subject: SPAWN_SUBJECT, record }).then(function() {
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
    return /* @__PURE__ */ React.createElement("div", { className: "fx-scroll" }, /* @__PURE__ */ React.createElement("div", { className: "fx-col" }, /* @__PURE__ */ React.createElement("h1", { className: "fx-h1 fx-in" }, "Workflow"), /* @__PURE__ */ React.createElement("p", { className: "fx-psub fx-in", style: { animationDelay: ".03s" } }, "Spawn a real-identity agent from a prompt \u2014 it gets its own bus credentials and DM channel."), /* @__PURE__ */ React.createElement("div", { className: "wf-card fx-in", style: { animationDelay: ".06s" } }, /* @__PURE__ */ React.createElement("div", { className: "wf-card-header" }, /* @__PURE__ */ React.createElement("span", { className: "wf-card-ic" }, "\u2B21"), /* @__PURE__ */ React.createElement("div", null, /* @__PURE__ */ React.createElement("div", { className: "wf-card-title" }, "Mobilize an agent"), /* @__PURE__ */ React.createElement("div", { className: "wf-card-sub" }, "A dispatcher on ", /* @__PURE__ */ React.createElement("span", { className: "mono" }, "msg.topic.spawn"), " mints a scoped identity and launches the agent."))), /* @__PURE__ */ React.createElement("label", { className: "wf-label", htmlFor: "wf-prompt" }, "Prompt"), /* @__PURE__ */ React.createElement(
      "textarea",
      {
        id: "wf-prompt",
        className: "wf-textarea" + (busy ? " is-disabled" : ""),
        rows: 4,
        placeholder: "Describe what the agent should do \u2014 e.g. 'Review the open PRs against the acceptance criteria and post a summary artifact'",
        value: prompt,
        disabled: busy,
        onChange: function(e) {
          setPrompt(e.target.value);
        }
      }
    ), /* @__PURE__ */ React.createElement("label", { className: "wf-label", htmlFor: "wf-nick", style: { marginTop: "12px" } }, "Nickname ", /* @__PURE__ */ React.createElement("span", { className: "wf-optional" }, "(optional)")), /* @__PURE__ */ React.createElement(
      "input",
      {
        id: "wf-nick",
        className: "wf-input" + (busy ? " is-disabled" : ""),
        type: "text",
        placeholder: "e.g. reviewer-1",
        value: nickname,
        disabled: busy,
        onChange: function(e) {
          setNickname(e.target.value);
        }
      }
    ), (phase === "sending" || phase === "polling") && /* @__PURE__ */ React.createElement("div", { className: "wf-status wf-status--polling" }, /* @__PURE__ */ React.createElement("span", { className: "wf-spin", "aria-hidden": "true" }, "\u25CC"), phase === "sending" ? "Publishing spawn.request\u2026" : "Waiting for dispatcher to mint and launch\u2026"), phase === "error" && /* @__PURE__ */ React.createElement("div", { className: "wf-status wf-status--error" }, /* @__PURE__ */ React.createElement("span", { className: "wf-status-ic" }, "\u2298"), /* @__PURE__ */ React.createElement("span", null, errMsg), /* @__PURE__ */ React.createElement("button", { className: "wf-retry", onClick: handleReset }, "Try again")), phase === "success" && spawned && /* @__PURE__ */ React.createElement("div", { className: "wf-status wf-status--ok" }, /* @__PURE__ */ React.createElement("span", { className: "wf-status-ic" }, "\u2713"), /* @__PURE__ */ React.createElement("div", { className: "wf-ok-body" }, /* @__PURE__ */ React.createElement("div", { className: "wf-ok-name" }, spawned.nickname || spawned.id), /* @__PURE__ */ React.createElement("div", { className: "wf-ok-id mono" }, spawned.id)), /* @__PURE__ */ React.createElement(
      "button",
      {
        className: "wf-msg-btn",
        onClick: function() {
          onDM && onDM(spawned.id);
        },
        title: "Open DM with " + (spawned.nickname || spawned.id)
      },
      "Message \u2192"
    )), /* @__PURE__ */ React.createElement("div", { className: "wf-actions" }, phase === "success" ? /* @__PURE__ */ React.createElement("button", { className: "wf-btn-secondary", onClick: handleReset }, "Mobilize another") : /* @__PURE__ */ React.createElement(
      "button",
      {
        className: "wf-btn-primary",
        disabled: !prompt.trim() || busy,
        onClick: handleMobilize
      },
      busy ? "Mobilizing\u2026" : "Mobilize"
    ))), /* @__PURE__ */ React.createElement("div", { className: "wf-card wf-card--disabled fx-in", style: { animationDelay: ".10s" } }, /* @__PURE__ */ React.createElement("div", { className: "wf-card-header" }, /* @__PURE__ */ React.createElement("span", { className: "wf-card-ic wf-card-ic--muted" }, "\u2B21"), /* @__PURE__ */ React.createElement("div", null, /* @__PURE__ */ React.createElement("div", { className: "wf-card-title wf-card-title--muted" }, "Start a workflow \u2014 coming soon"), /* @__PURE__ */ React.createElement("div", { className: "wf-card-sub" }, "Run a multi-step workflow definition. Requires a workflow consumer on the bus."))), /* @__PURE__ */ React.createElement("div", { className: "wf-actions" }, /* @__PURE__ */ React.createElement("button", { className: "wf-btn-primary", disabled: true }, "Start a workflow")))));
  }
  Object.assign(window, { WorkflowView });
})();
