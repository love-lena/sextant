(function() {
  const { useState, useRef, useEffect } = React;
  const SPAWN_SUBJECT = "msg.topic.spawn";
  const POLL_INTERVAL_MS = 800;
  const TIMEOUT_MS = 1e4;
  const TOKEN = new URLSearchParams(location.search).get("token") || "";
  const AUTH = { "Authorization": "Bearer " + TOKEN };
  function mbPost(path, body) {
    return fetch(path, {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, AUTH),
      body: JSON.stringify(body)
    }).then(function(r) {
      if (!r.ok) throw new Error(path + " -> " + r.status);
    });
  }
  function mbGet(path) {
    return fetch(path, { headers: AUTH }).then(function(r) {
      if (!r.ok) throw new Error(path + " -> " + r.status);
      return r.json();
    });
  }
  function seedPrompt(context) {
    if (!context) return "";
    if (context.type === "artifact") return "Interpret and act on [[" + context.name + "]]";
    if (context.type === "goal") return "Advance: " + (context.northstar || context.id || "");
    return context.label || "";
  }
  function MobilizeButton({ context, onDM }) {
    const [phase, setPhase] = useState("idle");
    const [promptText, setPromptText] = useState("");
    const [errMsg, setErrMsg] = useState("");
    const [spawnedId, setSpawnedId] = useState(null);
    const popoverRef = useRef(null);
    const mountedRef = useRef(true);
    useEffect(function() {
      return function() {
        mountedRef.current = false;
      };
    }, []);
    useEffect(function() {
      if (phase === "idle") return;
      function onDown(e) {
        if (popoverRef.current && !popoverRef.current.contains(e.target)) {
          setPhase("idle");
        }
      }
      document.addEventListener("mousedown", onDown);
      return function() {
        document.removeEventListener("mousedown", onDown);
      };
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
        setPhFn("sent");
        return;
      }
      mbGet("/api/clients").then(function(cs) {
        if (!mountedRef.current) return;
        var agents = (Array.isArray(cs) ? cs : []).filter(function(c) {
          return c.Kind !== "client" && c.Kind !== "human";
        });
        var found = null;
        for (var i = 0; i < agents.length; i++) {
          if (!knownIds.has(agents[i].ID)) {
            found = agents[i];
            break;
          }
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
      var knownIds = /* @__PURE__ */ new Set();
      mbGet("/api/clients").then(function(cs) {
        if (Array.isArray(cs)) {
          cs.filter(function(c) {
            return c.Kind !== "client" && c.Kind !== "human";
          }).forEach(function(c) {
            knownIds.add(c.ID);
          });
        }
        return mbPost("/api/publish", {
          subject: SPAWN_SUBJECT,
          record: { "$type": "spawn.request", "prompt": p }
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
      if (e) {
        e.stopPropagation();
        e.preventDefault();
      }
      setPhase("idle");
    }
    var busy = phase === "sending" || phase === "polling";
    if (phase === "idle") {
      return /* @__PURE__ */ React.createElement(
        "button",
        {
          className: "mb-btn",
          title: "Mobilize agent" + (context && context.name ? " on " + context.name : ""),
          onClick: openPopover,
          type: "button"
        },
        "\u2B21 Mobilize"
      );
    }
    return /* @__PURE__ */ React.createElement("div", { className: "mb-anchor", onClick: function(e) {
      e.stopPropagation();
    } }, /* @__PURE__ */ React.createElement("button", { className: "mb-btn mb-btn--active", type: "button", onClick: handleClose }, "\u2B21 Mobilize"), /* @__PURE__ */ React.createElement("div", { className: "mb-popover", ref: popoverRef, role: "dialog", "aria-label": "Mobilize agent" }, /* @__PURE__ */ React.createElement("div", { className: "mb-pop-head" }, /* @__PURE__ */ React.createElement("span", { className: "mb-pop-title" }, "Mobilize agent"), /* @__PURE__ */ React.createElement("button", { className: "mb-pop-x", "aria-label": "Close", onClick: handleClose, type: "button" }, "\xD7")), (phase === "open" || phase === "sending" || phase === "polling" || phase === "error") && /* @__PURE__ */ React.createElement("div", null, /* @__PURE__ */ React.createElement("label", { className: "wf-label", htmlFor: "mb-prompt" }, "Prompt"), /* @__PURE__ */ React.createElement(
      "textarea",
      {
        id: "mb-prompt",
        className: "wf-textarea mb-ta" + (busy ? " is-disabled" : ""),
        rows: 3,
        value: promptText,
        disabled: busy,
        onChange: function(e) {
          setPromptText(e.target.value);
        },
        autoFocus: true
      }
    ), phase === "error" && /* @__PURE__ */ React.createElement("div", { className: "wf-status wf-status--error", style: { marginTop: "8px" } }, /* @__PURE__ */ React.createElement("span", { className: "wf-status-ic" }, "\u2298"), /* @__PURE__ */ React.createElement("span", null, errMsg)), (phase === "sending" || phase === "polling") && /* @__PURE__ */ React.createElement("div", { className: "wf-status wf-status--polling", style: { marginTop: "8px" } }, /* @__PURE__ */ React.createElement("span", { className: "wf-spin", "aria-hidden": "true" }, "\u25CC"), phase === "sending" ? "Publishing\u2026" : "Waiting for dispatcher\u2026"), /* @__PURE__ */ React.createElement("div", { className: "mb-pop-actions" }, /* @__PURE__ */ React.createElement(
      "button",
      {
        className: "wf-btn-primary mb-btn-send",
        disabled: !promptText.trim() || busy,
        onClick: handleSend,
        type: "button"
      },
      busy ? "Mobilizing\u2026" : "Send"
    ))), phase === "sent" && /* @__PURE__ */ React.createElement("div", { className: "mb-sent" }, spawnedId ? /* @__PURE__ */ React.createElement(React.Fragment, null, /* @__PURE__ */ React.createElement("span", { className: "wf-status-ic", style: { color: "var(--met)" } }, "\u2713"), /* @__PURE__ */ React.createElement("span", { className: "mb-sent-txt" }, "Agent spawned"), onDM && /* @__PURE__ */ React.createElement(
      "button",
      {
        className: "wf-msg-btn",
        type: "button",
        onClick: function() {
          onDM(spawnedId);
          handleClose();
        }
      },
      "Message \u2192"
    )) : /* @__PURE__ */ React.createElement(React.Fragment, null, /* @__PURE__ */ React.createElement("span", { className: "wf-status-ic", style: { color: "var(--prog)" } }, "\u2191"), /* @__PURE__ */ React.createElement("span", { className: "mb-sent-txt" }, "Spawn request sent \u2014 check Agents list")))));
  }
  Object.assign(window, { MobilizeButton });
})();
