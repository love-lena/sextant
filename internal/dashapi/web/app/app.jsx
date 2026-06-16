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
  // ulidTime decodes the 48-bit millisecond timestamp a ULID encodes in its first
  // 10 Crockford-base32 chars. frameTime prefers a frame's createdAt, falling back
  // to its ULID id — so a message's real send time is available for sort + "Xm ago"
  // without the bus carrying a separate timestamp field.
  function ulidTime(id){
    if(!id || id.length<10) return 0;
    const A="0123456789ABCDEFGHJKMNPQRSTVWXYZ";
    let t=0;
    for(let i=0;i<10;i++){ const v=A.indexOf((id[i]||"").toUpperCase()); if(v<0) return 0; t=t*32+v; }
    return t;
  }
  function frameTime(f){ const t=Date.parse((f&&f.createdAt)||""); return isNaN(t)?ulidTime(f&&f.id):t; }
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
    const [artMissing, setArtMissing] = useState(false); // the open artifact resolved to nothing (stale ref guard)
    const [activeConvo, setActiveConvo] = useState("");
    // stage mode: home | artifacts | goals | agents | artifact (one open) | conversation
    const [stageMode, setStageMode] = useState("home");
    const [palette, setPalette] = useState(false);       // ⌘K command palette (TASK stage a)
    // Assistant FAB (stub, not wired): lifted here so ⌘K can open it with a
    // prefilled prompt. asstPrompt is the query carried over from a no-match search.
    const [asstOpen, setAsstOpen] = useState(false);
    const [asstPrompt, setAsstPrompt] = useState("");
    const [draft, setDraft] = useState("");
    const convBodyRef = useRef(null);
    const discBodyRef = useRef(null);
    const [hidden, setHidden] = useState(()=>{ try{ return new Set(JSON.parse(localStorage.getItem("sx-hidden-convos")||"[]")); }catch(_){ return new Set(); } });
    const [dark, setDark] = useState(()=>{ try{ return localStorage.getItem("sx-dark")==="1"; }catch(_){ return false; } });
    // artifact discussion layout: split (doc | discussion) by default, toggle to stacked; persisted
    const [discSplit, setDiscSplit] = useState(()=>{ try{ return localStorage.getItem("sx-disc-split")!=="0"; }catch(_){ return true; } });
    // build-staleness nudge (TASK-140): the SHA the page loaded with, the SHA now
    // served, and whether the operator dismissed the current mismatch. On a `--ui`
    // hot-reload the served build.json gets a new SHA on each `make ui` → the
    // loaded (old) page polls, sees the mismatch, and shows a quiet refresh nudge.
    // The embedded release dash has a fixed SHA → never mismatches → no nudge.
    const [loadedBuild, setLoadedBuild] = useState(null); // {sha,builtAt} the page loaded with
    const [currentBuild, setCurrentBuild] = useState(null); // {sha,builtAt} now served
    const [buildNudgeOff, setBuildNudgeOff] = useState(false); // operator dismissed THIS mismatch

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

    // ⌘K / Ctrl-K toggles the command palette (a real client-side find & jump over
    // the already-loaded artifacts + agents + conversation subjects).
    useEffect(()=>{
      const h = (e)=>{ if((e.metaKey||e.ctrlKey) && (e.key==="k"||e.key==="K")){ e.preventDefault(); setPalette(p=>!p); } };
      window.addEventListener("keydown", h);
      return ()=>window.removeEventListener("keydown", h);
    },[]);

    // dark mode: toggle the class on #app + persist (topbar toggle)
    useEffect(()=>{
      const r=document.getElementById("app");
      if(r) r.classList.toggle("dark", dark);
      try{ localStorage.setItem("sx-dark", dark?"1":"0"); }catch(_){}
    },[dark]);

    // artifact discussion layout: persist the split↔stacked choice
    useEffect(()=>{ try{ localStorage.setItem("sx-disc-split", discSplit?"1":"0"); }catch(_){} },[discSplit]);

    // prefetch artifact records so the sidebar can group by review-state and an
    // open is instant. Fine at dash scale; a very large bucket would want paging.
    useEffect(()=>{
      let cancelled=false;
      Promise.all(artifacts.map(a=>apiGet("/api/artifacts/"+encodeURIComponent(a.Name))
        .then(r=>[a.Name,(r&&r.Record)||null]).catch(()=>[a.Name,null])))
        .then(pairs=>{ if(!cancelled) setRecords(Object.fromEntries(pairs)); });
      return ()=>{ cancelled=true; };
    },[artifacts]);

    // live stream over msg.> → activity feed + conversation discovery.
    // No TOKEN guard: loopback is token-free (TASK-115) so an empty token still
    // streams; a non-loopback page always carries the token in its URL.
    useEffect(()=>{
      const es = new EventSource("/api/stream?subject="+encodeURIComponent("msg.>")+"&token="+encodeURIComponent(TOKEN));
      es.onmessage = (m)=>{
        let ev; try { ev = JSON.parse(m.data); } catch(_) { return; }
        const subj = ev.subject, f = ev.frame;
        if(!subj || !f) return;
        const text = frameText(f.record);
        const at = frameTime(f) || Date.now();
        const msg = { id:f.id, author:f.author, text, ts:at };
        setConvos(prev=>{
          const cur = prev[subj] || { msgs:[] };
          if(cur.msgs.some(x=>x.id===msg.id)) return prev;
          return { ...prev, [subj]:{ ...cur, msgs:[...cur.msgs, msg].slice(-200), last:Math.max(cur.last||0, at), lastText:text } };
        });
        setActivity(prev=>[{ subj, author:f.author, text, ts:at }, ...prev].slice(0,40));
      };
      es.onerror = ()=>{};
      return ()=>es.close();
    },[TOKEN]);

    // Seed each conversation's last-activity from history on load, so the sidebar
    // sorts most-recent-first immediately. The stream is deliver-new (no replay),
    // so without this every discovered subject sat at last:0 → effectively random
    // order until its first live message (TASK-113). For each known subject we page
    // to its tail and take the latest frame's time. One-shot. (TASK-95's backend
    // latest-N read would replace the per-subject paging with one cheap tail read.)
    const seededRef = useRef(false);
    useEffect(()=>{
      if(seededRef.current) return; // loopback is token-free (TASK-115); don't gate the seed on a token
      seededRef.current = true;
      let cancelled=false;
      const latestTime=(subj)=>{
        const PAGE=200, MAX=25; let best=0;
        const page=(since,guard)=>apiGet("/api/messages?subject="+encodeURIComponent(subj)+"&since="+since+"&limit="+PAGE).then(res=>{
          const frames=(res&&res.messages)||[];
          for(const f of frames){ const t=frameTime(f); if(t>best) best=t; }
          const next=res&&res.next_cursor;
          if(guard>1 && frames.length>=PAGE && next && next>since) return page(next,guard-1);
          return best;
        }).catch(()=>best);
        return page(0,MAX);
      };
      apiGet("/api/subjects").then(subs=>{
        if(cancelled || !Array.isArray(subs)) return;
        return Promise.all(subs.map(s=>{
          const subj=s&&s.subject; if(!subj) return null;
          return latestTime(subj).then(t=>({subj,t}));
        }).filter(Boolean));
      }).then(pairs=>{
        if(cancelled || !pairs) return;
        setConvos(prev=>{
          const next={...prev};
          for(const p of pairs){ if(!p||!p.t) continue; const cur=next[p.subj]||{msgs:[],last:0,lastText:""}; if(p.t>(cur.last||0)) next[p.subj]={...cur,last:p.t}; }
          return next;
        });
      }).catch(()=>{});
      return ()=>{cancelled=true;};
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
        // Home config + agent presence have no push either — poll them so the
        // Home page (greeting/agenda) and Agent status hot-reload with no input.
        apiGet("/api/artifacts/home").then(a=>{
          const rec=(a&&a.Record)||null;
          setHome(prev => JSON.stringify(prev)===JSON.stringify(rec) ? prev : rec);
        }).catch(()=>{});
        apiGet("/api/clients").then(cs=>{
          if(!Array.isArray(cs)) return;
          setClients(prev => JSON.stringify(prev)===JSON.stringify(cs) ? prev : cs);
        }).catch(()=>{});
      }, 4000);
      return ()=>clearInterval(id);
    },[]);

    // build-staleness poll (TASK-140). build.json is a static file written by
    // scripts/build-dash-ui.sh at `make ui` time ({sha,builtAt}); the Go process
    // serves it from the live UIDir (--ui) or the embedded FS. Fetch it once to
    // record the SHA this page loaded with, then poll every ~20s for the SHA now
    // served. A mismatch (both SHAs present) means a newer build is live → the
    // nudge shows. Robust to absence: an older/embedded dash without build.json
    // (404 or non-JSON) leaves both null → no nudge, no errors. Plain fetch (not
    // apiGet): build.json is a static, token-free asset like index.html.
    const fetchBuild = useCallback(()=>fetch("/build.json", { cache:"no-store" })
      .then(r=> r.ok ? r.json() : null)
      .then(b=> (b && typeof b.sha==="string" && b.sha) ? b : null)
      .catch(()=>null), []);
    useEffect(()=>{
      let cancelled=false;
      fetchBuild().then(b=>{ if(!cancelled && b) setLoadedBuild(b); });
      const id=setInterval(()=>{
        fetchBuild().then(b=>{ if(cancelled || !b) return; setCurrentBuild(prev=> (prev && prev.sha===b.sha) ? prev : b); });
      }, 20000);
      return ()=>{ cancelled=true; clearInterval(id); };
    },[fetchBuild]);
    // a newer build is served iff both SHAs are present and differ. A fresh
    // mismatch (new served SHA) re-arms the nudge even if a prior one was dismissed.
    const staleBuild = !!(loadedBuild && currentBuild && loadedBuild.sha!==currentBuild.sha);
    const servedSha = currentBuild && currentBuild.sha;
    useEffect(()=>{ setBuildNudgeOff(false); },[servedSha]);

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

    // derived: agents (everything that isn't a human "client" kind). Each agent's
    // live state + headline come from its own status.<id> artifact (agent.status,
    // TASK-84) when present; otherwise we fall back to bus presence.
    const STATUS_STATES = ["idle","working","waiting-for-human","waiting-for-agent","blocked","done"];
    const agents = useMemo(()=>clients.filter(c=>c.Kind!=="client" && c.Kind!=="human").map(c=>{
      const sr = records["status."+c.ID];
      const st = sr && sr.state;
      const known = STATUS_STATES.indexOf(st)>=0;
      const headline = (sr && sr.headline) || "";
      return {
        id:c.ID, name:c.DisplayName,
        state: !c.Online ? "offline" : (known ? st : "idle"),
        headline,
        meta: headline || ((c.Kind||"agent")+(c.Online?" · online":" · offline")),
      };
    }),[clients, records]);

    // review-state from the artifact's record (convention); absent ⇒ "review"
    const statusOf = useCallback((name)=>{
      const rec = records[name];
      const st = rec && rec.review && rec.review.state;
      return REVIEW_STATES.indexOf(st)>=0 ? st : "draft";
    },[records]);

    // derived: artifacts in the component shape (topic/author stay stubbed — no
    // primitive yet; status now comes from the review convention)
    // 'home' is the curated Home page and 'status.<id>' artifacts are the per-agent
    // status records (rendered in the Agent-status panel), so hide both from the list.
    const artItems = useMemo(()=>artifacts.filter(a=>a.Name!=="home" && !a.Name.startsWith("status.")).map(a=>({
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
        role: (kindOf(m.author)==="client"||kindOf(m.author)==="human")?"human":"agent",
        self: m.author===self.id, time:relMs(m.ts), text:m.text,
      }));
    },[convos, activeConvo, nameOf, kindOf, self.id]);

    // the open artifact's companion-topic discussion, rendered inline in the
    // artifact view (TASK-83). Same shape as `messages`, keyed on the artifact's
    // companion subject msg.topic.artifact.<name>.
    const discussion = useMemo(()=>{
      const c = activeArtifact ? convos[companionTopic(activeArtifact)] : null;
      if(!c) return [];
      return c.msgs.map((m,i)=>({
        id:m.id||i, kind:"msg", author:nameOf(m.author),
        role: kindOf(m.author)==="client"?"human":"agent",
        self: m.author===self.id, time:relMs(m.ts), text:m.text,
      }));
    },[convos, activeArtifact, nameOf, kindOf, self.id]);

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

    // keep the inline artifact discussion pinned to the newest message too.
    useEffect(()=>{
      if(stageMode!=="artifact") return;
      const el = discBodyRef.current;
      if(el) el.scrollTop = el.scrollHeight;
    },[discussion, stageMode, activeArtifact]);

    function openArtifact(name){
      setActiveArtifact(name); setStageMode("artifact"); setArtMissing(false);
      const subj = companionTopic(name); ensureConvo(subj); backfill(subj); // load the inline discussion (TASK-83)
      const cached = records[name];
      setArtRecord(cached!==undefined ? cached : null);
      // Fetch by name (the API resolves names not in the cached list). A 404 or a
      // null record for a name that isn't in the directory means the ref is stale
      // — flag it so the stage shows a graceful "not found" instead of the wrong
      // (fallback) document. Ignore a stale resolution if a newer open superseded it.
      apiGet("/api/artifacts/"+encodeURIComponent(name)).then(a=>{
        const rec=(a&&a.Record)||null; setArtRecord(rec); setRecords(prev=>({...prev,[name]:rec}));
        setActiveArtifact(cur=>{ if(cur===name && !rec && !artifacts.some(x=>x.Name===name)) setArtMissing(true); return cur; });
      }).catch(()=>{ setActiveArtifact(cur=>{ if(cur===name && !artifacts.some(x=>x.Name===name)) setArtMissing(true); return cur; }); });
    }
    function goHome(){ setStageMode("home"); }
    // ⌘K no-match → open the (stub) Assistant FAB with the typed query as its
    // prompt. The assistant is NOT wired — the panel shows the prompt + the
    // "not wired yet" placeholder; it never fabricates an answer.
    function askAssistant(query){ setPalette(false); setAsstPrompt(query||""); setAsstOpen(true); }
    // Workspace nav (flow2 chrome): Home / Artifacts / Goals / Agents swap the
    // white stage. Goals is an inert placeholder (Track 2 owns the real view).
    function onNav(key){ setStageMode(key); }
    function backfill(subj){
      // /api/messages reads FORWARD from `since` (since=0 is the oldest), so a
      // single page returns the OLDEST messages. Page to the tail following
      // next_cursor and keep the newest, so a busy topic (>1 page) shows recent
      // history, not its first page. Bounded so a huge topic can't loop forever.
      const PAGE=200, MAX_PAGES=25;
      const acc=[];
      const page=(since,guard)=>apiGet("/api/messages?subject="+encodeURIComponent(subj)+"&since="+since+"&limit="+PAGE).then(res=>{
        const frames=(res&&res.messages)||[]; acc.push(...frames);
        const next=res&&res.next_cursor;
        if(guard>1 && frames.length>=PAGE && next && next>since) return page(next,guard-1);
      });
      page(0,MAX_PAGES).then(()=>{
        if(!acc.length) return;
        const hist=acc.map(f=>({ id:f.id, author:f.author, text:frameText(f.record), ts:0 }));
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
    // post to the open artifact's companion discussion topic (TASK-83 inline thread).
    function sendDiscussion(){
      if(!draft.trim()||!activeArtifact) return;
      const text=draft.trim();
      apiPublish(companionTopic(activeArtifact),{ "$type":"chat.message", text }).then(()=>setDraft("")).catch(()=>{});
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

    const reviewCount = artItems.filter(a=>a.status==="review").length;
    const workingCount = agents.filter(a=>a.state==="working").length;

    const ctx = {
      conversations:convList, activeConvo, stageMode, onOpenConvo:openConvo, onExpandConvo:expandConvo,
      messages, draft, setDraft, onSend:send, onArtifactRef:openArtifact,
      artifacts:artItems, activeArtifact, onOpenArtifact:openArtifact,
      goals:GOALS, agents, activity:homeActivity, self, onGoHome:goHome, home, onDM:startDM,
      hidden, onHide:hideConvo, onUnhide:unhideConvo,
      onNav, onSearch:()=>setPalette(true), reviewCount, workingCount,
    };

    // ⌘K search index — only what's already loaded (artifacts, agents, conversation
    // subjects). Selecting a result opens it via the existing handlers; the
    // artifact `go` uses openArtifact (which fetches by name), so it resolves even
    // for a name not in the cached list.
    const searchIndex = ()=>{
      const items=[];
      // "Go to" — the four Workspace nav hubs as jump targets (same as clicking
      // the sidebar nav). Listed first so a name-clash still surfaces the hub.
      [["Home","home"],["Artifacts","artifacts"],["Goals","goals"],["Agents","agents"]]
        .forEach(([label,key])=>items.push({ key:"nav:"+key, type:"Go to", label,
          sub:"workspace", kw:("go to "+label+" "+key).toLowerCase(), go:()=>onNav(key) }));
      artItems.forEach(a=>items.push({ key:"art:"+a.name, type:"Artifact", label:a.name,
        sub:(a.updated?("updated "+a.updated+" ago"):"")+(a.status?(" · "+a.status):""),
        kw:(a.name+" "+a.status).toLowerCase(), go:()=>openArtifact(a.name) }));
      agents.forEach(a=>items.push({ key:"agent:"+a.id, type:"Agent", label:a.name, sub:a.meta,
        kw:(a.name+" "+(a.headline||"")+" "+a.state).toLowerCase(),
        go:()=>{ if(a.id) startDM(a.id); else setStageMode("agents"); } }));
      convList.forEach(c=>items.push({ key:"conv:"+c.key, type:"Channel",
        label:(c.type==="topic"?"# ":"@ ")+c.name, sub:c.snippet||"conversation",
        kw:(c.name+" "+(c.snippet||"")).toLowerCase(), go:()=>expandConvo(c.key) }));
      return items;
    };

    const hasAuthor = artifact.author && artifact.author.name;

    return (
      <div className="sx-app">
        <div style={{display:"contents"}}>
          <Sidebar ctx={ctx} busName={(self.display_name||"bus")} navMode={t.sideNav} />
        </div>

        <main className="sx-stage">
          {staleBuild && !buildNudgeOff && (
            <div className="sx-buildnudge" role="status">
              <span className="sx-buildnudge-dot" />
              <span className="sx-buildnudge-text">new version available — refresh (⌘R)</span>
              <button className="sx-buildnudge-x" title="Dismiss until the next update" aria-label="Dismiss" onClick={()=>setBuildNudgeOff(true)}>×</button>
            </div>
          )}
          <div className="sx-topbar">
            <div className="sx-crumb">
              {stageMode==="home" ? (
                <React.Fragment>
                  <span className="sx-crumb-topic">Home</span>
                  <span className="sx-crumb-sep">/</span>
                  <span className="sx-crumb-art">{self.display_name?("you are "+self.display_name):"live bus"}</span>
                </React.Fragment>
              ) : stageMode==="artifacts" ? (
                <span className="sx-crumb-topic">Artifacts</span>
              ) : stageMode==="goals" ? (
                <span className="sx-crumb-topic">Goals</span>
              ) : stageMode==="agents" ? (
                <span className="sx-crumb-topic">Agents</span>
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
              <button className="sx-icon-btn" title={dark?"Light mode":"Dark mode"} onClick={()=>setDark(d=>!d)}>{dark?"☀":"☾"}</button>
              <button className="sx-icon-btn" title="Fullscreen">⤢</button>
            </div>
          </div>

          {stageMode==="home" ? (
            <div className="sx-canvas">
              <div className="sx-page sx-page--doc sx-page--home"><HomePage ctx={ctx} /></div>
            </div>
          ) : stageMode==="artifacts" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><ArtifactsView artifacts={artItems} activeArtifact={activeArtifact} onOpenArtifact={openArtifact} /></div>
            </div>
          ) : stageMode==="goals" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><GoalsStub /></div>
            </div>
          ) : stageMode==="agents" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><AgentsView agents={agents} onDM={startDM} /></div>
            </div>
          ) : stageMode==="artifact" && artMissing ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc">
                <div className="fx-scroll"><div className="fx-col sx-conv-light">
                  <h1 className="fx-h1">Artifact not found</h1>
                  <p className="fx-psub">Nothing on the bus is named <span className="mono">{activeArtifact}</span> right now.</p>
                  <div className="fx-stub">
                    <span className="fx-stub-ic">⌕</span>
                    <div>
                      <div className="fx-stub-title">The reference may be stale.</div>
                      <div className="fx-stub-sub">It might have been renamed or removed, or it never existed. Open the Artifacts list to see what's actually here.</div>
                    </div>
                  </div>
                  <div style={{marginTop:18}}>
                    <button className="sx-sbtn sx-sbtn-req" onClick={()=>setStageMode("artifacts")}>Browse artifacts →</button>
                  </div>
                </div></div>
              </div>
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
              <div className={"sx-canvas sx-canvas--artifact " + (discSplit?"sx-canvas--split":"sx-canvas--stacked")}>
                <div className="sx-page sx-page--doc"><MarkdownArtifact record={artRecord} name={artifact.name} revision={artifact.version} /></div>
                <div className="sx-artdisc sx-conv-light">
                  <div className="sx-artdisc-head">
                    <span className="sx-artdisc-title">Discussion</span>
                    <span className="sx-artdisc-sub">{companionTopic(artifact.name)}</span>
                    <button className="sx-icon-btn sx-artdisc-toggle" title={discSplit?"Stack below the document":"Split beside the document"} onClick={()=>setDiscSplit(v=>!v)}>{discSplit?"▤":"▥"}</button>
                  </div>
                  <div className="sx-artdisc-body" ref={discBodyRef}>
                    {discussion.length
                      ? <MessageList messages={discussion} onArtifactRef={openArtifact} />
                      : <div className="sx-artdisc-empty">No discussion yet — start the thread below.</div>}
                  </div>
                  <Composer draft={draft} setDraft={setDraft} onSend={sendDiscussion} placeholder={"Discuss " + artifact.name} />
                </div>
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
                    <MessageList messages={messages} onArtifactRef={openArtifact} artifactNames={artifacts.map(a=>a.Name)} />
                  </div>
                  <Composer draft={draft} setDraft={setDraft} onSend={send} placeholder={"Message "+(convo.type==="topic"?"#":"@")+convo.name} />
                </div>
              </div>
            </div>
          )}
        </main>

        <AssistantFab open={asstOpen} prompt={asstPrompt}
          onOpen={()=>{ setAsstPrompt(""); setAsstOpen(true); }}
          onClose={()=>{ setAsstOpen(false); setAsstPrompt(""); }} />
        {palette && <CmdK index={searchIndex()} onClose={()=>setPalette(false)} onAsk={askAssistant} />}

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
