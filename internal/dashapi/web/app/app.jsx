/* app.jsx — Sextant cockpit, wired to the dash D1 local API (TASK-71, ADR-0032).
   The prototype's seed data is replaced with live reads from /api/* and the SSE
   live stream; the Go process stays the single bus client and the browser only
   ever talks to this local API.

   Review loop (TASK-66): an artifact's review-state lives as a `review` block in
   its record (absent ⇒ "review"); approve / request-changes persist it via
   POST /api/artifacts/{name}/review and post an event to the companion topic
   msg.topic.artifact.<name>.

   Still stubbed and labelled: goal metrics (no primitive), the curated Home
   greeting / banner / links (assistant-owned, static here).
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
  function apiPost(path, body){
    return fetch(path, {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, AUTH),
      body: JSON.stringify(body),
    }).then(r => { if (!r.ok) throw new Error(path + " -> " + r.status); });
  }
  function apiPublish(subject, record){ return apiPost("/api/publish", { subject, record }); }
  function apiReview(name, state){ return apiPost("/api/artifacts/"+encodeURIComponent(name)+"/review", { state }); }

  // The review convention (TASK-66): states + the per-artifact companion topic.
  const REVIEW_STATES = ["review","approved","changes","draft","rejected","archived"];
  const REVIEW_VERB = { approved:"approved", changes:"requested changes on", rejected:"rejected", archived:"archived", review:"reopened", draft:"reset to draft" };
  function companionTopic(name){ return "msg.topic.artifact." + name; }

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
    const [records, setRecords] = useState({});          // name -> Record (status + instant open)
    const [home, setHome] = useState(null);              // curated Home config (the 'home' artifact, TASK-71 #2)
    const [convos, setConvos] = useState({});            // subject -> {msgs:[{id,author,text,ts}], last, lastText}
    const [activity, setActivity] = useState([]);        // recent frames across all subjects
    const [activeArtifact, setActiveArtifact] = useState("");
    const [artRecord, setArtRecord] = useState(null);    // active artifact Record
    const [activeConvo, setActiveConvo] = useState("");
    const [stageMode, setStageMode] = useState("home");  // home | artifact | conversation
    const [draft, setDraft] = useState("");
    const convBodyRef = useRef(null);
    const [hidden, setHidden] = useState(()=>{ try{ return new Set(JSON.parse(localStorage.getItem("sx-hidden-convos")||"[]")); }catch(_){ return new Set(); } });

    const nameOf = useCallback((id)=>{ const c=clients.find(c=>c.ID===id); return c?c.DisplayName:(id||"").slice(0,8); },[clients]);
    const kindOf = useCallback((id)=>{ const c=clients.find(c=>c.ID===id); return c?c.Kind:"agent"; },[clients]);

    // initial directory loads
    useEffect(()=>{
      apiGet("/api/self").then(setSelf).catch(()=>{});
      apiGet("/api/clients").then(cs=>setClients(Array.isArray(cs)?cs:[])).catch(()=>{});
      apiGet("/api/artifacts").then(as=>setArtifacts(Array.isArray(as)?as:[])).catch(()=>{});
      apiGet("/api/artifacts/home").then(a=>setHome((a&&a.Record)||null)).catch(()=>{});
      // seed the conversation list with subjects the dash already knows about
      apiGet("/api/subjects").then(subs=>{
        if(!Array.isArray(subs)) return;
        setConvos(prev=>{ const next={...prev}; for(const s of subs){ if(s&&s.subject&&!next[s.subject]) next[s.subject]={msgs:[],last:0,lastText:""}; } return next; });
      }).catch(()=>{});
    },[]);

    // prefetch artifact records so the sidebar can group by review-state and an
    // open is instant. Fine at dash scale; a very large bucket would want paging.
    useEffect(()=>{
      let cancelled=false;
      Promise.all(artifacts.map(a=>apiGet("/api/artifacts/"+encodeURIComponent(a.Name))
        .then(r=>[a.Name,(r&&r.Record)||null]).catch(()=>[a.Name,null])))
        .then(pairs=>{ if(!cancelled) setRecords(Object.fromEntries(pairs)); });
      return ()=>{ cancelled=true; };
    },[artifacts]);

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

    // poll the directory so newly-created artifacts (and subjects) appear without
    // a manual refresh. Artifacts are KV, not a message stream, so there's no live
    // push for them yet — poll, but keep the state reference stable when nothing
    // changed so we don't needlessly re-render or re-fetch records.
    useEffect(()=>{
      const sig = a => a.map(x=>x.Name+":"+x.Revision).join(",");
      const id = setInterval(()=>{
        apiGet("/api/artifacts").then(as=>{
          if(!Array.isArray(as)) return;
          setArtifacts(prev => sig(prev)===sig(as) ? prev : as);
        }).catch(()=>{});
        apiGet("/api/subjects").then(subs=>{
          if(!Array.isArray(subs)) return;
          setConvos(prev=>{ let changed=false; const next={...prev}; for(const s of subs){ if(s&&s.subject&&!next[s.subject]){ next[s.subject]={msgs:[],last:0,lastText:""}; changed=true; } } return changed?next:prev; });
        }).catch(()=>{});
      }, 4000);
      return ()=>clearInterval(id);
    },[]);

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
      id:c.ID, name:c.DisplayName, state:c.Online?"working":"offline",
      meta:(c.Kind||"agent")+(c.Online?" · online":" · offline"),
    })),[clients]);

    // review-state from the artifact's record (convention); absent ⇒ "review"
    const statusOf = useCallback((name)=>{
      const rec = records[name];
      const st = rec && rec.review && rec.review.state;
      return REVIEW_STATES.indexOf(st)>=0 ? st : "review";
    },[records]);

    // derived: artifacts in the component shape (topic/author stay stubbed — no
    // primitive yet; status now comes from the review convention)
    // 'home' is special-cased as the curated Home page, so hide it from the list.
    const artItems = useMemo(()=>artifacts.filter(a=>a.Name!=="home").map(a=>({
      name:a.Name, version:a.Revision, status:statusOf(a.Name), topic:"", type:"markdown",
      id:a.Name, author:{ name:"", kind:"agent" }, updated:relTime(a.Updated),
    })),[artifacts, statusOf]);

    // derived: conversation list from discovered subjects (newest first)
    // classify each discovered subject: inbox (a one-way client drop), dm (a
    // 2-participant topic), or a regular topic. An inbox is NOT a conversation.
    const convList = useMemo(()=>Object.entries(convos)
      .sort((a,b)=>(b[1].last||0)-(a[1].last||0))
      .map(([subj,c])=>{
        let type="topic", name=topicLabel(subj);
        if(subj.startsWith("msg.client.")){ type="inbox"; name=nameOf(subj.slice(11))+" · inbox"; }
        else if(subj.startsWith("msg.topic.dm.")){
          const ids=subj.slice(13).split("."); const other=ids.find(x=>x!==self.id)||ids[0]||"";
          type="dm"; name=nameOf(other);
        }
        return { key:subj, type, name, snippet:c.lastText||"", time:relMs(c.last), unread:0, participants:0 };
      }),[convos, nameOf, self.id]);

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
      { name:"", version:0, status:"review", topic:"", author:{name:"",kind:"agent"}, updated:"" };
    const status = artifact.status;
    const reviewRev = (artRecord && artRecord.review && artRecord.review.rev) || 0;
    const convo = convList.find(c=>c.key===activeConvo) || convList[0] || { type:"topic", name:"", participants:0 };

    // keep the conversation pinned to the newest message: scroll to the bottom on
    // open and whenever a message arrives.
    useEffect(()=>{
      if(stageMode!=="conversation") return;
      const el = convBodyRef.current;
      if(el) el.scrollTop = el.scrollHeight;
    },[messages, stageMode, activeConvo]);

    function openArtifact(name){
      setActiveArtifact(name); setStageMode("artifact");
      const cached = records[name];
      setArtRecord(cached!==undefined ? cached : null);
      apiGet("/api/artifacts/"+encodeURIComponent(name)).then(a=>{
        const rec=(a&&a.Record)||null; setArtRecord(rec); setRecords(prev=>({...prev,[name]:rec}));
      }).catch(()=>{});
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
    function ensureConvo(subj){ setConvos(prev=>prev[subj]?prev:{ ...prev, [subj]:{ msgs:[], last:Date.now(), lastText:"" } }); }
    function openConvo(key){ ensureConvo(key); setActiveConvo(key); backfill(key); }
    function expandConvo(key){ ensureConvo(key); setActiveConvo(key); setStageMode("conversation"); backfill(key); }
    function send(){
      if(!draft.trim()||!activeConvo) return;
      const text=draft.trim();
      apiPublish(activeConvo,{ "$type":"chat.message", text }).then(()=>setDraft("")).catch(()=>{});
    }
    // approve / request-changes: persist the review-state, refresh the record,
    // and post an event to the artifact's companion discussion topic.
    function setReview(name, state){
      apiReview(name, state)
        .then(()=>apiGet("/api/artifacts/"+encodeURIComponent(name)))
        .then(a=>{ const rec=(a&&a.Record)||null; setRecords(prev=>({...prev,[name]:rec})); if(name===activeArtifact) setArtRecord(rec); })
        .then(()=>apiPublish(companionTopic(name),{ "$type":"chat.message", text:(REVIEW_VERB[state]||state)+" "+name }))
        .catch(()=>{});
    }
    // a DM is a 2-participant topic with a canonical subject from the sorted
    // pair, so both ends derive the same one (distinct from the one-way inbox).
    function dmSubject(a,b){ return "msg.topic.dm."+[a,b].sort().join("."); }
    function startDM(otherId){ if(!self.id||!otherId) return; expandConvo(dmSubject(self.id, otherId)); }
    // hiding a conversation is a per-operator view preference (local only).
    function persistHidden(set){ try{ localStorage.setItem("sx-hidden-convos", JSON.stringify([...set])); }catch(_){} }
    function hideConvo(key){ setHidden(prev=>{ const n=new Set(prev); n.add(key); persistHidden(n); return n; }); }
    function unhideConvo(key){ setHidden(prev=>{ const n=new Set(prev); n.delete(key); persistHidden(n); return n; }); }

    const ctx = {
      conversations:convList, activeConvo, stageMode, onOpenConvo:openConvo, onExpandConvo:expandConvo,
      messages, draft, setDraft, onSend:send, onArtifactRef:openArtifact,
      artifacts:artItems, activeArtifact, onOpenArtifact:openArtifact,
      goals:GOALS, agents, activity:homeActivity, self, onGoHome:goHome, home, onDM:startDM,
      hidden, onHide:hideConvo, onUnhide:unhideConvo,
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
                    {artifact.updated && <span className="sx-arthead-time">updated {artifact.updated} ago</span>}
                    {status==="approved" && reviewRev>0 && <span className="sx-arthead-time">· approved at v{reviewRev}</span>}
                    <span className="sx-arthead-v mono" style={{opacity:.5}}>· rev {artifact.version}</span>
                  </div>
                </div>
                <div className="sx-arthead-r">
                  <StatusPill status={status} big />
                  {(status==="archived"||status==="rejected") ? (
                    <button className="sx-sbtn sx-sbtn-req" onClick={()=>setReview(artifact.name,"review")}>Reopen</button>
                  ) : (
                    <React.Fragment>
                      {status!=="approved" && <button className="sx-sbtn sx-sbtn-approve" onClick={()=>setReview(artifact.name,"approved")}>✓ Approve</button>}
                      {status!=="changes" && <button className="sx-sbtn sx-sbtn-req" onClick={()=>setReview(artifact.name,"changes")}>Request changes</button>}
                      <button className="sx-sbtn sx-sbtn-req" onClick={()=>setReview(artifact.name,"archived")}>Archive</button>
                      <button className="sx-sbtn sx-sbtn-req" onClick={()=>setReview(artifact.name,"rejected")}>Reject</button>
                    </React.Fragment>
                  )}
                  <button className="sx-sbtn sx-sbtn-req" onClick={()=>expandConvo(companionTopic(artifact.name))}>Discussion ↗</button>
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
                  <div className="sx-convstage-body" ref={convBodyRef}>
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
