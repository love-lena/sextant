/* conversations.jsx — Sextant Live Flow conversation stage (v0.5, TASK stage d).
   Extracted from app.jsx's else-branch (the sx-convstage subtree) into its own
   per-view file, following the established review.jsx / artifacts.jsx / home.jsx
   pattern. Adds TASK-129: the DM-status strip above the input — the counterpart
   agent's live state + headline shown only in DM views, with graceful degrade for
   topics, humans, and unknown agents.

   Globals consumed (assigned by sidebar.jsx):
     window.MessageList  window.Composer  window.Avatar

   Exports: ConversationView  (+ DMStatusStrip as a subcomponent for later reuse)
*/
(function () {
  const { useRef, useEffect } = React;

  // agent state → flow2 tone + label + pulse colour. The DM status strip uses it
  // to show a counterpart run's live state above the composer. (The Agents roster
  // that originally defined this map was retired in the no-personas sweep,
  // TASK-194; the map lives here now, the strip's last consumer.)
  const AGENT_STATE = {
    working:           { tone: "t-met",      label: "Working",        c: "var(--met)",  live: true },
    done:              { tone: "t-met",      label: "Done",           c: "var(--met)" },
    idle:              { tone: "t-todo",     label: "Idle",           c: "var(--todo)" },
    offline:           { tone: "t-todo",     label: "Offline",        c: "var(--todo)" },
    "waiting-for-human":  { tone: "t-waiting",  label: "Waiting · you",  c: "var(--wait)" },
    "waiting-for-agent":  { tone: "t-progress", label: "Waiting · agent", c: "var(--prog)" },
    blocked:           { tone: "t-blocked",  label: "Blocked",        c: "var(--blk)" },
  };

  // DMStatusStrip — the live-status banner shown directly above the Composer in
  // DM conversations. Intentionally a standalone subcomponent (TASK-129 AC#4) so
  // it can later be extended to topic views without rewiring ConversationView.
  //
  // Props:
  //   convo   the active convo object { type, key, name, … }
  //   agents  the derived agents array from app.jsx
  //   self    the self record { id, … }
  //
  // Degrade rules (AC#3):
  //   - renders nothing if convo.type !== "dm"
  //   - renders nothing if the counterpart id is absent or equals self.id
  //   - renders nothing if the counterpart isn't in the agents array
  //     (human participants are not in agents; only agents are)
  //   - renders nothing if the counterpart has no known state / state is absent
  // In all degrade cases returns null — no broken/empty strip visible.
  function DMStatusStrip({ convo, agents, self }) {
    // Only DM conversations show the strip.
    if (!convo || convo.type !== "dm") return null;

    // Parse counterpart id from subject msg.topic.dm.<idA>.<idB>.
    // The subject is convo.key; the DM prefix is "msg.topic.dm.".
    const subj = convo.key || "";
    const DM_PREFIX = "msg.topic.dm.";
    if (!subj.startsWith(DM_PREFIX)) return null;

    // The two participant ids are the two dot-segments after the prefix.
    // They may themselves contain hyphens/underscores but not dots (ULID/UUID format).
    const rest = subj.slice(DM_PREFIX.length);
    const dot = rest.indexOf(".");
    if (dot < 0) return null; // malformed subject → degrade

    const idA = rest.slice(0, dot);
    const idB = rest.slice(dot + 1);
    const selfId = (self && self.id) || "";
    // Self must be one of the two participants; if neither id is ours this
    // isn't our DM (malformed/rogue subject) → degrade rather than guess.
    if (idA !== selfId && idB !== selfId) return null;
    // Counterpart = whichever id is NOT ours.
    const counterId = idA === selfId ? idB : idA;
    if (!counterId || counterId === selfId) return null;

    // Look up the counterpart in agents (humans are not in this array → degrade).
    const agent = (agents || []).find((a) => a.id === counterId);
    if (!agent) return null;

    // A counterpart with no meaningful state → degrade (don't show a blank strip).
    const s = AGENT_STATE[agent.state];
    if (!s) return null;

    return (
      <div className="sx-dm-strip">
        <window.Avatar name={agent.name} kind="agent" size={18} />
        <span className={"fx-pulse" + (s.live ? " is-live" : "")} style={{ background: s.c }} />
        <span className="sx-dm-strip-label" style={{ color: s.c }}>{s.label}</span>
        {agent.headline && (
          <>
            <span className="sx-dm-strip-sep">·</span>
            <span className="sx-dm-strip-headline">{agent.headline}</span>
          </>
        )}
      </div>
    );
  }

  // ConversationView — the conversation/message-thread stage. Extracted from the
  // else-branch of app.jsx's stage render (previously the sx-convstage subtree).
  //
  // Props (all passed directly from app.jsx):
  //   convo          the active convo shape { type, key, name, … }
  //   messages       the shaped message array for MessageList
  //   draft          the shared composer buffer (string)
  //   setDraft       draft setter
  //   onSend         fire the send action
  //   onArtifactRef  open an artifact by name
  //   artifactNames  known artifact name list for wikilink resolution
  //   agents         derived agents array (for DMStatusStrip)
  //   self           self record { id, … }
  function ConversationView(props) {
    const {
      convo, messages, draft, setDraft, onSend,
      onArtifactRef, artifactNames,
      agents, self,
    } = props;

    // Scroll the message body to the bottom when messages arrive/send or the
    // conversation changes. This is the sole scroll driver — the conversation view
    // owns its own scroll now (app.jsx no longer keeps a ref for it).
    const convBodyRef = useRef(null);
    // Key the scroll on the NEWEST message's id, not the array length. A long
    // conversation is capped at 200 (app.jsx slice(-200)), so once it fills, a new
    // message drops the oldest and length stays 200 — a length-keyed effect never
    // re-fires, and the view stops following new messages (Lena's #ui-feedback:
    // "scrolling on new message isn't working again … some edge case"). The last
    // message's id changes on every append, capped or not, so this fires every time.
    const lastMsgId = (messages && messages.length) ? messages[messages.length - 1].id : null;
    useEffect(() => {
      const el = convBodyRef.current;
      if (!el) return;
      // Defer to the next frame so the just-sent/just-arrived message is laid out
      // before we measure scrollHeight — a synchronous read here lands short of the
      // newest line (the message you just sent stays just out of view). (Lena's
      // #ui-feedback: "sending messages doesn't scroll the conversation down".)
      const id = requestAnimationFrame(() => { el.scrollTop = el.scrollHeight; });
      return () => cancelAnimationFrame(id);
    }, [lastMsgId, messages && messages.length, convo && convo.key]);

    const isDM = convo && convo.type === "dm";
    // A read-only stream (an agent.activity observability feed, TASK-235) is watched,
    // never posted into — suppress the composer so the operator can't pollute it.
    const readOnly = !!(convo && convo.readOnly);
    const sigil = isDM ? "@ " : "# ";
    const name = (convo && convo.name) || "";
    const placeholder = "Message " + (isDM ? "@" : "#") + name;

    return (
      <div className="sx-canvas">
        <div className="sx-page sx-page--doc sx-conv-light">
          <div className="sx-convstage">
            <div className="sx-convstage-head">
              <span className="sx-convstage-title">{sigil}{name}</span>
              <span className="sx-convstage-meta">live on the bus</span>
            </div>
            <div className="sx-convstage-body" ref={convBodyRef}>
              <window.MessageList
                messages={messages}
                onArtifactRef={onArtifactRef}
                artifactNames={artifactNames}
              />
            </div>
            <DMStatusStrip convo={convo} agents={agents} self={self} />
            {readOnly
              ? <div className="sx-conv-readonly">Read-only — this is {name}'s live work stream.</div>
              : <window.Composer
                  draft={draft}
                  setDraft={setDraft}
                  onSend={onSend}
                  placeholder={placeholder}
                />}
          </div>
        </div>
      </div>
    );
  }

  Object.assign(window, { ConversationView, DMStatusStrip });
})();
