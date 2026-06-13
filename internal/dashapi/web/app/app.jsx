/* app.jsx — Sextant: splittable sidebar navigator + flexible stage (artifact or conversation). */
(function () {
  const { useState, useRef, useEffect, useMemo } = React;

  function shade(hex, amt){
    const n=parseInt(hex.slice(1),16); let r=n>>16&255,g=n>>8&255,b=n&255;
    const f=1+amt; r=Math.round(r*f);g=Math.round(g*f);b=Math.round(b*f);
    const cl=v=>Math.max(0,Math.min(255,v));
    return "#"+[cl(r),cl(g),cl(b)].map(v=>v.toString(16).padStart(2,"0")).join("");
  }
  function hexA(hex,a){ const n=parseInt(hex.slice(1),16); return `rgba(${n>>16&255},${n>>8&255},${n&255},${a})`; }

  const TWEAK_DEFAULTS = /*EDITMODE-BEGIN*/{
    "accent": "#4f9d68",
    "sidePos": "left",
    "sideTone": "paper",
    "sideNav": "sections",
    "livePulse": true
  }/*EDITMODE-END*/;

  // ---- seed data ----
  const ARTIFACTS = [
    { name:"Q3 Launch Brief", version:4, status:"review",   topic:"release-q3", type:"markdown", id:"art_9f3a1c", author:{name:"research-agent",kind:"agent"}, updated:"3m" },
    { name:"Onboarding PRD",  version:5, status:"review",   topic:"product",    type:"markdown", id:"art_2bd740", author:{name:"research-agent",kind:"agent"}, updated:"12m" },
    { name:"Comp Analysis",   version:2, status:"draft",    topic:"release-q3", type:"markdown", id:"art_77ae09", author:{name:"research-agent",kind:"agent"}, updated:"30m" },
    { name:"API Design Note", version:9, status:"approved", topic:"platform",   type:"markdown", id:"art_4e1c08", author:{name:"qa-agent",kind:"agent"},        updated:"1h" },
    { name:"Pricing Model",   version:7, status:"review",   topic:"release-q3", type:"sheet",    id:"art_b2c7d5", author:{name:"research-agent",kind:"agent"}, updated:"1h" },
  ];

  const PARTICIPANTS = [
    { name:"designer-agent", kind:"agent", role:"designer", key:"ed25519:9f…3a", verified:true, online:true },
    { name:"research-agent", kind:"agent", role:"research", key:"ed25519:b2…7d", verified:true, online:true },
    { name:"qa-agent",       kind:"agent", role:"qa",       key:"ed25519:4e…c0", verified:true, online:false },
    { name:"you",            kind:"human", role:"operator", key:"ed25519:7c…e1", verified:true, online:true },
  ];

  const CONVERSATIONS = [
    { key:"release-q3", type:"topic", name:"release-q3", snippet:"research-agent: assigned the billing load-test to platform", time:"40s", unread:2, participants:4 },
    { key:"platform",   type:"topic", name:"platform",   snippet:"qa-agent: audit export schema bumped to v2", time:"18m", unread:0, participants:3 },
    { key:"product",    type:"topic", name:"product",    snippet:"you: let's cut multi-language from v1", time:"1h", unread:0, participants:5 },
    { key:"brand",      type:"topic", name:"brand",      snippet:"designer-agent: logo set is up for review", time:"2h", unread:0, participants:2 },
    { key:"dm-designer",type:"dm",    name:"designer-agent", snippet:"shipping the logo set now", time:"5m", unread:1, participants:2 },
    { key:"dm-research",type:"dm",    name:"research-agent",  snippet:"draft brief is up — take a look?", time:"2h", unread:0, participants:2 },
  ];

  const CONV_MSGS = {
    "release-q3":[
      { id:1, kind:"event", text:"research-agent published Q3 Launch Brief · v4", time:"3m" },
      { id:2, kind:"msg", author:"research-agent", role:"agent", time:"3m", artifactRef:"Q3 Launch Brief",
        text:"v4 of the launch brief — moved GA to Aug 14 and tightened the CSAT goal to 95%. Ready for your review." },
      { id:3, kind:"msg", author:"qa-agent", role:"agent", time:"2m",
        text:"Flagged a risk: usage-based billing isn’t load-tested at GA volume yet. Added it to the Risks table as High." },
      { id:4, kind:"msg", author:"you", role:"human", self:true, time:"1m",
        text:"Aug 14 works and scope looks right. I’ll approve once the billing risk has a named owner." },
      { id:5, kind:"msg", author:"research-agent", role:"agent", time:"40s",
        text:"Assigned the billing load-test to platform — will reflect it in v5." },
    ],
    "platform":[
      { id:1, kind:"msg", author:"qa-agent", role:"agent", time:"22m", artifactRef:"API Design Note",
        text:"Bumped the audit export schema to v2 — backwards compatible, documented the stability guarantee." },
      { id:2, kind:"msg", author:"you", role:"human", self:true, time:"18m", text:"Approved. Good to ship." },
    ],
    "product":[
      { id:1, kind:"msg", author:"you", role:"human", self:true, time:"1h", text:"Let's cut multi-language from v1 — it's stretching the timeline." },
      { id:2, kind:"msg", author:"research-agent", role:"agent", time:"1h", artifactRef:"Onboarding PRD", text:"Agreed — moved it to Q4 in the PRD." },
    ],
    "brand":[
      { id:1, kind:"msg", author:"designer-agent", role:"agent", time:"2h", text:"Logo set v1 is up for review whenever you have a minute." },
    ],
    "dm-designer":[
      { id:1, kind:"msg", author:"designer-agent", role:"agent", time:"5m", text:"Shipping the logo set now — DM me if the mark feels too heavy." },
      { id:2, kind:"msg", author:"you", role:"human", self:true, time:"4m", text:"Looks great, no notes." },
    ],
    "dm-research":[
      { id:1, kind:"msg", author:"research-agent", role:"agent", time:"2h", artifactRef:"Q3 Launch Brief", text:"Draft brief is up — take a look when you can?" },
      { id:2, kind:"msg", author:"you", role:"human", self:true, time:"2h", text:"On it." },
    ],
  };

  const GOALS = [
    { label:"Tasks merged this sprint", value:14, target:20, display:"14 / 20", note:"6 open · 2 in review" },
    { label:"CI pipeline green", value:97, target:95, display:"97%", met:true, note:"last 50 runs · target ≥ 95%" },
    { label:"Test coverage", value:81, target:85, display:"81%", note:"target 85% before GA cut" },
    { label:"Open P0 bugs", value:0, target:1, display:"blocked", blocked:true, note:"1 P0 in billing meter · owner: platform" },
  ];

  const AGENTS = [
    { name:"designer-agent", state:"working", meta:"editing Q3 Launch Brief · 2m", tone:"review" },
    { name:"research-agent", state:"working", meta:"running comp analysis · 5m", tone:"review" },
    { name:"qa-agent",       state:"idle",    meta:"last active 18m ago", tone:"draft" },
    { name:"build-agent",    state:"blocked", meta:"waiting on your approval · 3m", tone:"changes" },
    { name:"deploy-agent",   state:"offline", meta:"disconnected 1h ago", tone:"draft" },
  ];

  function App() {
    const [t, setTweak] = useTweaks(TWEAK_DEFAULTS);

    const [statuses, setStatuses] = useState(()=>Object.fromEntries(ARTIFACTS.map(a=>[a.name,a.status])));
    const [activeArtifact, setActiveArtifact] = useState("Q3 Launch Brief");
    const [activeConvo, setActiveConvo] = useState("release-q3");
    const [convMsgs, setConvMsgs] = useState(CONV_MSGS);
    const [draft, setDraft] = useState("");
    const [stageMode, setStageMode] = useState("home"); // home | artifact | conversation
    const msgId = useRef(1000);

    useEffect(()=>{
      const r=document.getElementById("app");
      r.style.setProperty("--brand", t.accent);
      r.style.setProperty("--brand-strong", shade(t.accent,-0.16));
      r.style.setProperty("--brand-soft", hexA(t.accent,0.16));
      r.classList.toggle("tone-paper", t.sideTone==="paper");
      r.classList.toggle("side-right", t.sidePos==="right");
      r.classList.toggle("no-pulse", !t.livePulse);
    },[t.accent,t.sideTone,t.sidePos,t.livePulse]);

    const artifacts = useMemo(()=>ARTIFACTS.map(a=>({...a,status:statuses[a.name]})),[statuses]);
    const artifact = artifacts.find(a=>a.name===activeArtifact) || artifacts[0];
    const status = artifact.status;
    const convo = CONVERSATIONS.find(c=>c.key===activeConvo) || CONVERSATIONS[0];
    const messages = convMsgs[activeConvo] || [];

    function openArtifact(name){ setActiveArtifact(name); setStageMode("artifact"); }
    function goHome(){ setStageMode("home"); }
    function openConvo(key){ setActiveConvo(key); }
    function expandConvo(key){ setActiveConvo(key); setStageMode("conversation"); }

    function postTo(key, msg){ setConvMsgs(m=>({...m,[key]:[...(m[key]||[]),{ id:++msgId.current, ...msg }]})); }
    function send(){ if(!draft.trim()) return; postTo(activeConvo,{ kind:"msg", author:"you", role:"human", self:true, time:"now", text:draft.trim() }); setDraft(""); }
    function approve(){ setStatuses(s=>({...s,[activeArtifact]:"approved"})); postTo(artifact.topic,{ kind:"event", time:"now", text:`you approved ${artifact.name} · v${artifact.version}` }); }
    function request(){ setStatuses(s=>({...s,[activeArtifact]:"changes"})); postTo(artifact.topic,{ kind:"event", time:"now", text:`you requested changes on ${artifact.name} · v${artifact.version}` }); }

    const ctx = {
      conversations:CONVERSATIONS, activeConvo, stageMode, onOpenConvo:openConvo, onExpandConvo:expandConvo,
      messages, draft, setDraft, onSend:send, onArtifactRef:openArtifact,
      artifacts, activeArtifact, onOpenArtifact:openArtifact,
      goals:GOALS, agents:AGENTS, onGoHome:goHome,
    };

    return (
      <div className="sx-app">
        <div style={{display:"contents"}}>
          <Sidebar ctx={ctx} busName="release-q3" navMode={t.sideNav} />
        </div>

        <main className="sx-stage">
          <div className="sx-topbar">
            <div className="sx-crumb">
              {stageMode==="home" ? (
                <React.Fragment>
                  <span className="sx-crumb-topic">Home</span>
                  <span className="sx-crumb-sep">/</span>
                  <span className="sx-crumb-art">curated by your assistant</span>
                </React.Fragment>
              ) : stageMode==="artifact" ? (
                <React.Fragment>
                  <span className="sx-crumb-topic"># {artifact.topic}</span>
                  <span className="sx-crumb-sep">/</span>
                  <span className="sx-crumb-art">{artifact.name}</span>
                </React.Fragment>
              ) : (
                <React.Fragment>
                  <span className="sx-crumb-topic">Conversations</span>
                  <span className="sx-crumb-sep">/</span>
                  <span className="sx-crumb-art">{convo.type==="topic"?"# ":"@ "}{convo.name}</span>
                </React.Fragment>
              )}
            </div>
            <div className="sx-stage-tools">
              <span className="sx-live"><span className="sx-live-dot" />live</span>
              <button className="sx-icon-btn" title="Fullscreen">⤢</button>
            </div>
          </div>

          {stageMode==="home" ? (
            <div className="sx-canvas">
              <div className="sx-page sx-page--doc sx-page--home"><HomePage ctx={ctx} /></div>
            </div>
          ) : stageMode==="artifact" ? (
            <React.Fragment>
              <div className="sx-arthead">
                <div className="sx-arthead-l">
                  <div className="sx-arthead-title">{artifact.name}</div>
                  <div className="sx-arthead-meta">
                    <span className="mono sx-arthead-v">v{artifact.version}</span>
                    <span className="sx-dotsep">·</span>
                    <Avatar name={artifact.author.name} kind={artifact.author.kind} size={18} />
                    <span className="sx-arthead-by">{artifact.author.name}</span>
                    <span className="sx-verified sm">✓ signed</span>
                    <span className="sx-arthead-time">· updated {artifact.updated} ago</span>
                  </div>
                </div>
                <div className="sx-arthead-r">
                  <StatusPill status={status} big />
                  {status==="review" && (
                    <React.Fragment>
                      <button className="sx-sbtn sx-sbtn-approve" onClick={approve}>✓ Approve v{artifact.version}</button>
                      <button className="sx-sbtn sx-sbtn-req" onClick={request}>Request changes</button>
                    </React.Fragment>
                  )}
                </div>
              </div>
              <div className="sx-canvas">
                <div className="sx-page sx-page--doc"><MarkdownArtifact /></div>
              </div>
            </React.Fragment>
          ) : (
            <div className="sx-canvas">
              <div className="sx-page sx-page--doc sx-conv-light">
                <div className="sx-convstage">
                  <div className="sx-convstage-head">
                    <span className="sx-convstage-title">{convo.type==="topic"?"# ":"@ "}{convo.name}</span>
                    <span className="sx-convstage-meta">{convo.participants} participants on the bus</span>
                  </div>
                  <div className="sx-convstage-body">
                    <MessageList messages={messages} onArtifactRef={openArtifact} />
                  </div>
                  <Composer draft={draft} setDraft={setDraft} onSend={send} placeholder={"Message "+(convo.type==="topic"?"#":"@")+convo.name} />
                </div>
              </div>
            </div>
          )}
        </main>

        <TweaksPanel title="Tweaks">
          <TweakSection label="Accent" />
          <TweakColor label="Brand signal" value={t.accent}
            options={["#4f9d68","#3a93d2","#7c6df0","#1a1c1f"]} onChange={v=>setTweak("accent",v)} />
          <TweakSection label="Sidebar" />
          <TweakRadio label="Position" value={t.sidePos} options={["left","right"]} onChange={v=>setTweak("sidePos",v)} />
          <TweakRadio label="Tone" value={t.sideTone} options={["charcoal","paper"]} onChange={v=>setTweak("sideTone",v)} />
          <TweakRadio label="Navigation" value={t.sideNav} options={["sections","tabs"]} onChange={v=>setTweak("sideNav",v)} />
          <TweakSection label="Motion" />
          <TweakToggle label="Live pulse" value={t.livePulse} onChange={v=>setTweak("livePulse",v)} />
        </TweaksPanel>
      </div>
    );
  }

  ReactDOM.createRoot(document.getElementById("root")).render(<App />);
})();
