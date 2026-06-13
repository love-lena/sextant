/* app.jsx — Sextant cockpit, wired to the dash D1 local API (TASK-71, ADR-0032).
   The prototype's seed data is replaced with live reads from /api/* and the SSE
   live stream; the Go process stays the single bus client and the browser only
   ever talks to this local API.

   Concepts with no bus primitive yet stay stubbed and are marked as such:
   - artifact review-status / approve / companion-topic  → TASK-66 (brief workstream)
   - goal metrics                                        → no primitive
   - the curated Home greeting / banner / links / note   → assistant-owned, static here
*/
(function () {
  const { useState, useRef, useEffect, useMemo, useCallback } = React;

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

  // ---- local API client (the per-launch token rides in the page URL) ----
  const TOKEN = new URLSearchParams(location.search).get("token") || "";
  const AUTH = { "Authorization": "Bearer " + TOKEN };
  async function apiGet(path){
    const r = await fetch(path, { headers: AUTH });
    if (!r.ok) throw new Error(path + " -> " + r.status);
    return r.json();
  }
  function apiPublish(subject, record){
    return fetch("/api/publish", {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, AUTH),
      body: JSON.stringify({ subject, record }),
    }).then(r => { if (!r.ok) throw new Error("publish -> " + r.status); });
  }

  // ---- helpers ----
  function relMs(ms){
    if(!ms) return "";
    const s = Math.max(0,(Date.now()-ms)/1000);
    if(s<60) return Math.floor(s)+"s";
    if(s<3600) return Math.floor(s/60)+"m";
    if(s<86400) return Math.floor(s/3600)+"h";
    return Math.floor(s/86400)+"d";
  }
  function relTime(iso){ const t=Date.parse(iso||""); return isNaN(t)?"":relMs(t); }
  function topicLabel(subject){
    if(subject.startsWith("msg.topic.")) return subject.slice(10);
    if(subject.startsWith("msg.client.")) return subject.slice(11);
    return subject;
  }
  function frameText(rec){
    if(!rec) return "·";
    if(typeof rec.text==="string") return rec.text;
    if(rec.title) return rec.title;
    return rec.$type || "·";
  }

  // Goal metrics have no bus primitive yet — stubbed (clearly a placeholder).
  const GOALS = [
    { label:"Tasks merged this sprint", value:14, target:20, display:"14 / 20", note:"stub — no goals primitive yet" },
    { label:"CI pipeline green", value:97, target:95, display:"97%", met:true, note:"stub — no goals primitive yet" },
    { label:"Test coverage", value:81, target:85, display:"81%", note:"stub — no goals primitive yet" },
  ];

  function App() {
    const [t, setTweak] = useTweaks(TWEAK_DEFAULTS);

    const [self, setSelf] = useState({ id:"", display_name:"", principal:"" });
    const [clients, setClients] = useState([]);          // raw ClientInfo[]
    const [artifacts, setArtifacts] = useState([]);      // raw ArtifactInfo[]
    const [convos, setConvos] = useState({});            // subject -> {msgs:[{id,author,text,ts}], last, lastText}
    const [activity, setActivity] = useState([]);        // recent frames across all subjects
    const [activeArtifact, setActiveArtifact] = useState("");
    const [artRecord, setArtRecord] = useState(null);    // active artifact Record {$type,title,body}
    const [activeConvo, setActiveConvo] = useState("");
    const [stageMode, setStageMode] = useState("home");  // home | artifact | conversation
    const [draft, setDraft] = useState("");

    const nameOf = useCallback((id)=>{ const c=clients.find(c=>c.ID===id); return c?c.DisplayName:(id||"").slice(0,8); },[clients]);
    const kindOf = useCallback((id)=>{ const c=clients.find(c=>c.ID===id); return c?c.Kind:"agent"; },[clients]);

    // initial directory loads
    useEffect(()=>{
      apiGet("/api/self").then(setSelf).catch(()=>{});
      apiGet("/api/clients").then(cs=>setClients(Array.isArray(cs)?cs:[])).catch(()=>{});
      apiGet("/api/artifacts").then(as=>setArtifacts(Array.isArray(as)?as:[])).catch(()=>{});
    },[]);

    // live stream over msg.> → activity feed + conversation discovery
    useEffect(()=>{
      if(!TOKEN) return;
      const es = new EventSource("/api/stream?subject="+encodeURIComponent("msg.>")+"&token="+encodeURIComponent(TOKEN));
      es.onmessage = (m)=>{
        let ev; try { ev = JSON.parse(m.data); } catch(_) { return; }
        const subj = ev.subject, f = ev.frame;
        if(!subj || !f) return;
        const text = frameText(f.record);
        const msg = { id:f.id, author:f.author, text, ts:Date.now() };
        setConvos(prev=>{
          const cur = prev[subj] || { msgs:[] };
          if(cur.msgs.some(x=>x.id===msg.id)) return prev;
          return { ...prev, [subj]:{ ...cur, msgs:[...cur.msgs, msg].slice(-200), last:Date.now(), lastText:text } };
        });
        setActivity(prev=>[{ subj, author:f.author, text, ts:Date.now() }, ...prev].slice(0,40));
      };
      es.onerror = ()=>{};
      return ()=>es.close();
    },[TOKEN]);

    // theme application
    useEffect(()=>{
      const r=document.getElementById("app");
      r.style.setProperty("--brand", t.accent);
      r.style.setProperty("--brand-strong", shade(t.accent,-0.16));
      r.style.setProperty("--brand-soft", hexA(t.accent,0.16));
      r.classList.toggle("tone-paper", t.sideTone==="paper");
      r.classList.toggle("side-right", t.sidePos==="right");
      r.classList.toggle("no-pulse", !t.livePulse);
    },[t.accent,t.sideTone,t.sidePos,t.livePulse]);

    // derived: agents (everything that isn't a human "client" kind)
    const agents = useMemo(()=>clients.filter(c=>c.Kind!=="client").map(c=>({
      name:c.DisplayName, state:c.Online?"working":"offline",
      meta:(c.Kind||"agent")+(c.Online?" · online":" · offline"),
    })),[clients]);

    // derived: artifacts in the component shape (status/topic/author stubbed — TASK-66)
    const artItems = useMemo(()=>artifacts.map(a=>({
      name:a.Name, version:a.Revision, status:"draft", topic:"", type:"markdown",
      id:a.Name, author:{ name:"", kind:"agent" }, updated:relTime(a.Updated),
    })),[artifacts]);

    // derived: conversation list from discovered subjects (newest first)
    const convList = useMemo(()=>Object.entries(convos)
      .sort((a,b)=>(b[1].last||0)-(a[1].last||0))
      .map(([subj,c])=>({
        key:subj,
        type: subj.startsWith("msg.client.")?"dm":"topic",
        name: subj.startsWith("msg.client.")?nameOf(subj.slice(11)):topicLabel(subj),
        snippet:c.lastText||"", time:relMs(c.last), unread:0, participants:0,
      })),[convos, nameOf]);

    const messages = useMemo(()=>{
      const c = convos[activeConvo]; if(!c) return [];
      return c.msgs.map((m,i)=>({
        id:m.id||i, kind:"msg", author:nameOf(m.author),
        role: kindOf(m.author)==="client"?"human":"agent",
        self: m.author===self.id, time:relMs(m.ts), text:m.text,
      }));
    },[convos, activeConvo, nameOf, kindOf, self.id]);

    const homeActivity = useMemo(()=>activity.map(a=>({
      who:nameOf(a.author), text:a.text, time:relMs(a.ts),
    })),[activity, nameOf]);

    const artifact = artItems.find(a=>a.name===activeArtifact) || artItems[0] ||
      { name:"", version:0, status:"draft", topic:"", author:{name:"",kind:"agent"}, updated:"" };
    const status = artifact.status;
    const convo = convList.find(c=>c.key===activeConvo) || convList[0] || { type:"topic", name:"", participants:0 };

    function openArtifact(name){
      setActiveArtifact(name); setStageMode("artifact"); setArtRecord(null);
      apiGet("/api/artifacts/"+encodeURIComponent(name)).then(a=>setArtRecord((a&&a.Record)||null)).catch(()=>setArtRecord(null));
    }
    function goHome(){ setStageMode("home"); }
    function backfill(subj){
      apiGet("/api/messages?subject="+encodeURIComponent(subj)+"&limit=100").then(res=>{
        const frames=(res&&res.messages)||[]; if(!frames.length) return;
        const hist=frames.map(f=>({ id:f.id, author:f.author, text:frameText(f.record), ts:0 }));
        setConvos(prev=>{
          const cur=prev[subj]||{msgs:[]};
          const seen=new Set(cur.msgs.map(m=>m.id));
          const merged=[...hist.filter(m=>!seen.has(m.id)), ...cur.msgs];
          return { ...prev, [subj]:{ ...cur, msgs:merged.slice(-200), lastText:cur.lastText||(hist.length?hist[hist.length-1].text:"") } };
        });
      }).catch(()=>{});
    }
    function openConvo(key){ setActiveConvo(key); backfill(key); }
    function expandConvo(key){ setActiveConvo(key); setStageMode("conversation"); backfill(key); }
    function send(){
      if(!draft.trim()||!activeConvo) return;
      const text=draft.trim();
      apiPublish(activeConvo,{ "$type":"chat.message", text }).then(()=>setDraft("")).catch(()=>{});
    }

    const ctx = {
      conversations:convList, activeConvo, stageMode, onOpenConvo:openConvo, onExpandConvo:expandConvo,
      messages, draft, setDraft, onSend:send, onArtifactRef:openArtifact,
      artifacts:artItems, activeArtifact, onOpenArtifact:openArtifact,
      goals:GOALS, agents, activity:homeActivity, self, onGoHome:goHome,
    };

    const hasAuthor = artifact.author && artifact.author.name;

    return (
      <div className="sx-app">
        <div style={{display:"contents"}}>
          <Sidebar ctx={ctx} busName={(self.display_name||"bus")} navMode={t.sideNav} />
        </div>

        <main className="sx-stage">
          <div className="sx-topbar">
            <div className="sx-crumb">
              {stageMode==="home" ? (
                <React.Fragment>
                  <span className="sx-crumb-topic">Home</span>
                  <span className="sx-crumb-sep">/</span>
                  <span className="sx-crumb-art">{self.display_name?("you are "+self.display_name):"live bus"}</span>
                </React.Fragment>
              ) : stageMode==="artifact" ? (
                <React.Fragment>
                  <span className="sx-crumb-topic">Artifact</span>
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
                    {hasAuthor && <span className="sx-dotsep">·</span>}
                    {hasAuthor && <Avatar name={artifact.author.name} kind={artifact.author.kind} size={18} />}
                    {hasAuthor && <span className="sx-arthead-by">{artifact.author.name}</span>}
                    {artifact.updated && <span className="sx-arthead-time">· updated {artifact.updated} ago</span>}
                  </div>
                </div>
                <div className="sx-arthead-r">
                  <StatusPill status={status} big />
                </div>
              </div>
              <div className="sx-canvas">
                <div className="sx-page sx-page--doc"><MarkdownArtifact record={artRecord} name={artifact.name} /></div>
              </div>
            </React.Fragment>
          ) : (
            <div className="sx-canvas">
              <div className="sx-page sx-page--doc sx-conv-light">
                <div className="sx-convstage">
                  <div className="sx-convstage-head">
                    <span className="sx-convstage-title">{convo.type==="topic"?"# ":"@ "}{convo.name}</span>
                    <span className="sx-convstage-meta">live on the bus</span>
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
