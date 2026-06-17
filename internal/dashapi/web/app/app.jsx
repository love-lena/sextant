/* app.jsx — Sextant cockpit, wired to the dash D1 local API (TASK-71, ADR-0032).
   The prototype's seed data is replaced with live reads from /api/* and the SSE
   live stream; the Go process stays the single bus client and the browser only
   ever talks to this local API.

   Review loop (TASK-66): an artifact's review-state lives as a `review` block in
   its record (absent ⇒ neutral (draft); needs-review is set explicitly by the
   producer); approve / request-changes persist it via
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
  // REVIEW_ACTION: action-phrased human-readable text for status-change events posted
  // to the companion topic (the marker's `text` field, read as a timeline entry in review.jsx).
  const REVIEW_ACTION = {
    approved:  "approved this",
    changes:   "requested changes",
    rejected:  "rejected this",
    archived:  "archived this",
    review:    "marked this needs review",
    draft:     "reset this to draft",
  };
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

  // the five criterion statuses (the goal.<id> lexicon); an unknown value is
  // normalized to not-started so the Goals view never indexes an empty STATUS slot.
  const GOAL_CRIT_STATES = ["met", "in-progress", "waiting-on-you", "blocked", "not-started"];

  function App() {
    const [t, setTweak] = useTweaks(TWEAK_DEFAULTS);

    const [self, setSelf] = useState({ id:"", display_name:"", principal:"" });
    const [clients, setClients] = useState([]);          // raw ClientInfo[]
    const [artifacts, setArtifacts] = useState([]);      // raw ArtifactInfo[]
    const [records, setRecords] = useState({});          // name -> Record (status + instant open)
    const [home, setHome] = useState(null);              // curated Home config (the 'home' artifact, TASK-71 #2)
    const [assistant, setAssistant] = useState(null);    // the 'assistant' artifact (ADR-0039): { client_id, name, accent } — absent pre-v0.5.0
    const [convos, setConvos] = useState({});            // subject -> {msgs:[{id,author,text,ts}], last, lastText}
    const [activity, setActivity] = useState([]);        // recent frames across all subjects
    const [activeArtifact, setActiveArtifact] = useState("");
    const [artRecord, setArtRecord] = useState(null);    // active artifact Record
    const [artMissing, setArtMissing] = useState(false); // the open artifact resolved to nothing (stale ref guard)
    // The artifact-review MODAL is an OPTION (Lena's #ui-feedback): full-page is the
    // default for every open, but an artifact opened FROM A CONVERSATION (a chat
    // artifact-reference) pops as a dismissible modal floating over the chat — with an
    // "Open in full page" button to escalate to the full-page review.
    const [artifactOpen, setArtifactOpen] = useState(false);
    const artReqRef = useRef(""); // the name of the latest-opened artifact — guards openArtifact's async fetch so a slow earlier fetch can't clobber the current review
    const [activeConvo, setActiveConvo] = useState("");
    // stage mode: home | artifacts | goals | agents | artifact (one open) | conversation
    const [stageMode, setStageMode] = useState("home");
    // bumped each time the Goals nav is clicked so GoalsView remounts to the
    // portfolio (its open-goal is internal state; the nav should always land you
    // on the list, not strand you in a detail you opened earlier).
    const [goalsEpoch, setGoalsEpoch] = useState(0);
    // the goal to open on the next Goals nav (TASK-157 deep-link): set when a
    // review-flagged goal is opened from the needs-you queue, so the nav lands on
    // that goal's detail; cleared on a plain Goals nav (lands on the portfolio).
    const [goalsOpenId, setGoalsOpenId] = useState(null);
    const [palette, setPalette] = useState(false);       // ⌘K command palette (TASK stage a)
    // Assistant FAB (stub, not wired): lifted here so ⌘K can open it with a
    // prefilled prompt. asstPrompt is the query carried over from a no-match search.
    const [asstOpen, setAsstOpen] = useState(false);
    const [asstPrompt, setAsstPrompt] = useState("");
    // a dedicated composer buffer for the FAB's violet DM, so it never collides
    // with the main stage `draft` (the operator can be mid-typing in a thread).
    const [asstDraft, setAsstDraft] = useState("");
    const [draft, setDraft] = useState("");
    const [hidden, setHidden] = useState(()=>{ try{ return new Set(JSON.parse(localStorage.getItem("sx-hidden-convos")||"[]")); }catch(_){ return new Set(); } });

    // ---- ⌘K recency store ----
    // Tracks the last-opened timestamp per destination keyed by the entry's
    // stable key (e.g. "nav:home", "art:<name>", "conv:<subject>").
    // Persisted in localStorage (sx-cmdk-recents) as a plain {key:ms} object,
    // capped to the 50 most-recently-touched entries so it never grows unbounded.
    const RECENTS_KEY = "sx-cmdk-recents";
    const RECENTS_CAP = 50;
    const [recents, setRecents] = useState(()=>{
      try { return JSON.parse(localStorage.getItem(RECENTS_KEY)||"{}"); } catch(_) { return {}; }
    });
    const touchRecent = useCallback((key)=>{
      setRecents(prev=>{
        const next = { ...prev, [key]: Date.now() };
        // Evict oldest entries beyond the cap so localStorage stays bounded.
        const entries = Object.entries(next).sort((a,b)=>b[1]-a[1]);
        const capped = Object.fromEntries(entries.slice(0, RECENTS_CAP));
        try { localStorage.setItem(RECENTS_KEY, JSON.stringify(capped)); } catch(_) {}
        return capped;
      });
    }, []);
    const [dark, setDark] = useState(()=>{ try{ return localStorage.getItem("sx-dark")==="1"; }catch(_){ return false; } });
    // Review view comments rail (TASK-141): a resizable + collapsible right-side
    // "artifact chat" pane. Both the width and the collapsed flag are persisted in
    // localStorage (mirrors the dark-mode persistence pattern). The width is clamped
    // to a sane range so a drag can't shrink it to nothing or eat the doc column.
    const RAIL_MIN = 280, RAIL_MAX = 620;
    const clampRail = (w)=>Math.max(RAIL_MIN, Math.min(RAIL_MAX, Math.round(w)));
    const [railWidth, setRailWidth] = useState(()=>{ try{ const v=parseInt(localStorage.getItem("sx-rail-w")||"",10); return isNaN(v)?344:clampRail(v); }catch(_){ return 344; } });
    const [railCollapsed, setRailCollapsed] = useState(()=>{ try{ return localStorage.getItem("sx-rail-collapsed")==="1"; }catch(_){ return false; } });
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
      // the assistant convention (ADR-0039): a latest-value `assistant` artifact
      // names the live operator-assistant. Absent pre-v0.5.0 (404) → stays null.
      apiGet("/api/artifacts/assistant").then(a=>setAssistant((a&&a.Record)||null)).catch(()=>setAssistant(null));
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

    // Esc dismisses the artifact-review modal (one of its exits: × · scrim · Esc).
    // Only listens while the modal is open, so it doesn't shadow other keys.
    useEffect(()=>{
      if(!artifactOpen) return;
      const h = (e)=>{ if(e.key==="Escape"){ e.preventDefault(); closeArtifact(); } };
      window.addEventListener("keydown", h);
      return ()=>window.removeEventListener("keydown", h);
    },[artifactOpen]);

    // dark mode: toggle the class on #app + persist (topbar toggle)
    useEffect(()=>{
      const r=document.getElementById("app");
      if(r) r.classList.toggle("dark", dark);
      try{ localStorage.setItem("sx-dark", dark?"1":"0"); }catch(_){}
    },[dark]);

    // Review rail (TASK-141): persist the width + collapsed choice (localStorage).
    useEffect(()=>{ try{ localStorage.setItem("sx-rail-w", String(railWidth)); }catch(_){} },[railWidth]);
    useEffect(()=>{ try{ localStorage.setItem("sx-rail-collapsed", railCollapsed?"1":"0"); }catch(_){} },[railCollapsed]);
    const onRailWidth = useCallback((w)=>setRailWidth(clampRail(w)),[]);
    const toggleRail = useCallback(()=>setRailCollapsed(v=>!v),[]);

    // Left nav (TASK-141): a resizable + collapsible shell sidebar — mirrors the
    // right rail pattern. Width clamped to 200–420px (default 284 = the flow2 width).
    const SIDE_MIN = 200, SIDE_MAX = 420;
    const clampSide = (w)=>Math.max(SIDE_MIN, Math.min(SIDE_MAX, Math.round(w)));
    const [sideWidth, setSideWidth] = useState(()=>{ try{ const v=parseInt(localStorage.getItem("sx-side-w")||"",10); return isNaN(v)?284:clampSide(v); }catch(_){ return 284; } });
    const [sideCollapsed, setSideCollapsed] = useState(()=>{ try{ return localStorage.getItem("sx-side-collapsed")==="1"; }catch(_){ return false; } });
    useEffect(()=>{ try{ localStorage.setItem("sx-side-w", String(sideWidth)); }catch(_){} },[sideWidth]);
    useEffect(()=>{ try{ localStorage.setItem("sx-side-collapsed", sideCollapsed?"1":"0"); }catch(_){} },[sideCollapsed]);
    const onSideWidth = useCallback((w)=>setSideWidth(clampSide(w)),[]);
    const toggleSide = useCallback(()=>setSideCollapsed(v=>!v),[]);

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
        // carry the raw record so companion-topic status-change markers survive into discussion
        const msg = { id:f.id, author:f.author, text, ts:at, record:f.record||null };
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
        // the `assistant` artifact (ADR-0039) lights up at v0.5.0 — poll so the FAB
        // wires to violet's DM the moment it appears (and degrades if it's removed).
        apiGet("/api/artifacts/assistant").then(a=>{
          const rec=(a&&a.Record)||null;
          setAssistant(prev => JSON.stringify(prev)===JSON.stringify(rec) ? prev : rec);
        }).catch(()=>setAssistant(prev => prev===null ? prev : null));
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

    // derived: goals (the goal primitive, ADR-0035). Each goal is a latest-value
    // artifact named goal.<id> whose record carries $type:"goal" + a northstar +
    // criteria[]. Goal STATUS is derived from the criteria rollup (goals.jsx) —
    // there is no stored goal-status field. Evidence is found by scanning ALL
    // records for a `relates` entry pointing at this goal (+crit): kind:"proof"
    // backs a met criterion, kind:"related" is a generic association. Everything
    // is guarded against missing/malformed fields so a half-written goal can't
    // crash the view.
    const goals = useMemo(()=>{
      // index relates entries once: goalId -> { crit:{<critId>:[{name,kind}]}, goal:[{name,kind}] }
      const rel = {};
      for(const a of artifacts){
        const rec = records[a.Name];
        const rs = rec && Array.isArray(rec.relates) ? rec.relates : null;
        if(!rs) continue;
        for(const e of rs){
          if(!e || typeof e.goal!=="string" || !e.goal) continue;
          const bucket = rel[e.goal] || (rel[e.goal]={ crit:{}, goal:[] });
          const ref = { name:a.Name, kind:(e.kind==="proof"?"proof":"related") };
          if(typeof e.crit==="string" && e.crit){ (bucket.crit[e.crit] || (bucket.crit[e.crit]=[])).push(ref); }
          else bucket.goal.push(ref);
        }
      }
      // a goal is the latest-value artifact goal.<id> carrying a goal record;
      // require BOTH the goal. name and the $type so a stray $type:"goal" under
      // another name can't surface in Goals while still showing in the Artifacts
      // list (which excludes the goal. namespace) — no cross-view double-listing.
      return artifacts.filter(a=>{ const r=records[a.Name]; return a.Name.startsWith("goal.") && r && r.$type==="goal"; }).map(a=>{
        const r = records[a.Name] || {};
        const id = a.Name.replace(/^goal\./,"");
        const bucket = rel[id] || { crit:{}, goal:[] };
        const criteria = (Array.isArray(r.criteria)?r.criteria:[]).map((c,i)=>{
          const cid = (c && typeof c.id==="string" && c.id) || ("crit-"+(i+1));
          return {
            id: cid,
            text: (c && typeof c.text==="string") ? c.text : "",
            status: (c && GOAL_CRIT_STATES.indexOf(c.status)>=0) ? c.status : "not-started",
            owner: (c && typeof c.owner==="string") ? c.owner : "",
            evidence: bucket.crit[cid] || [],
          };
        });
        return {
          id, name:a.Name,
          stream: (typeof r.stream==="string"?r.stream:""),
          northstar: (typeof r.northstar==="string"?r.northstar:""),
          updated: r.updated||"", by: r.by||"",
          // the artifact revision — so a review-flagged goal sorts into the needs-you
          // queue by recency ALONGSIDE review artifacts (same key artItems sorts on),
          // not always behind them (TASK-157).
          version: a.Revision,
          // review-state from the goal artifact's record (same convention as any
          // artifact — TASK-157). A goal flagged review.state="review" is awaiting
          // the operator's sign-off; it projects into the needs-you/review queue
          // and is signable in the Goals view. Absent/invalid ⇒ "" (neutral).
          review: (r.review && REVIEW_STATES.indexOf(r.review.state)>=0) ? r.review.state : "",
          criteria,
          evidence: bucket.goal, // goal-level relates (no crit) — optional
        };
      });
    },[artifacts, records]);

    // review-state from the artifact's record (convention); absent ⇒ neutral
    // (draft) — needs-review is set explicitly by the producer. Reads only
    // rec.review.state (no by/at/rev assumed), so a state-only block is fine.
    const statusOf = useCallback((name)=>{
      const rec = records[name];
      const st = rec && rec.review && rec.review.state;
      return REVIEW_STATES.indexOf(st)>=0 ? st : "draft";
    },[records]);

    // derived: artifacts in the component shape (topic/author stay stubbed — no
    // primitive yet; status now comes from the review convention)
    // 'home' is the curated Home page, 'status.<id>' artifacts are the per-agent
    // status records (rendered in the Agent-status panel), and 'goal.<id>'
    // artifacts are the goal primitive (rendered in the Goals view), so hide all
    // three from the plain documents list.
    const artItems = useMemo(()=>artifacts.filter(a=>a.Name!=="home" && !a.Name.startsWith("status.") && !a.Name.startsWith("goal.")).map(a=>({
      name:a.Name, version:a.Revision, status:statusOf(a.Name), topic:"", type:"markdown",
      id:a.Name, author:{ name:"", kind:"agent" }, updated:relTime(a.Updated),
    })),[artifacts, statusOf]);

    // derived: conversation list from discovered subjects (newest first)
    // classify each discovered subject: inbox (a one-way client drop), dm (a
    // 2-participant topic), or a regular topic. An inbox is NOT a conversation.
    const convList = useMemo(()=>Object.entries(convos)
      // artifact-discussion topics live only in the artifact view's inline panel, not the convo list (TASK-128)
      .filter(([subj])=>!subj.startsWith("msg.topic.artifact."))
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

    // derived: violet, the operator's assistant (ADR-0039). The `assistant`
    // artifact names the live assistant by its bus client_id; absent (pre-v0.5.0)
    // or malformed ⇒ null, and the FAB falls back to its "not live yet" state.
    const violet = (assistant && typeof assistant.client_id==="string" && assistant.client_id)
      ? { id:assistant.client_id, name:(typeof assistant.name==="string" && assistant.name ? assistant.name : "violet"), accent:(typeof assistant.accent==="string"?assistant.accent:"") }
      : null;
    // violet is "live" when a matching online bus client is present — drives the
    // header dot. Absent client / offline ⇒ no dot (the convention is just an
    // artifact; the agent need not be connected).
    const violetOnline = !!(violet && clients.some(c=>c.ID===violet.id && c.Online));

    // the violet DM subject (the same canonical 2-party topic startDM derives) and
    // the discovered+backfilled message thread, shaped exactly like `messages` so
    // the FAB can feed window.MessageList. Both null/empty when violet is absent.
    const asstSubject = (violet && self.id) ? dmSubject(self.id, violet.id) : "";
    const assistantMessages = useMemo(()=>{
      const c = asstSubject ? convos[asstSubject] : null; if(!c) return [];
      return c.msgs.map((m,i)=>({
        id:m.id||i, kind:"msg", author:nameOf(m.author),
        role: (kindOf(m.author)==="client"||kindOf(m.author)==="human")?"human":"agent",
        self: m.author===self.id, time:relMs(m.ts), text:m.text,
      }));
    },[convos, asstSubject, nameOf, kindOf, self.id]);

    // discover + backfill the violet DM as soon as both ends are known, so the
    // existing thread loads into `convos` (the same ensureConvo+backfill openArtifact
    // / startDM use). Re-runs only when the subject changes (not per render).
    useEffect(()=>{
      if(!asstSubject) return;
      ensureConvo(asstSubject); backfill(asstSubject);
    },[asstSubject]);

    // the open artifact's companion-topic discussion, rendered inline in the
    // artifact view (TASK-83). Same shape as `messages`, keyed on the artifact's
    // companion subject msg.topic.artifact.<name>.
    // Each item carries a `review` field (or null) so review.jsx can render
    // status-change events inline as timeline entries (distinct from plain comments).
    const discussion = useMemo(()=>{
      const c = activeArtifact ? convos[companionTopic(activeArtifact)] : null;
      if(!c) return [];
      return c.msgs.map((m,i)=>({
        id:m.id||i, kind:"msg", author:nameOf(m.author),
        role: kindOf(m.author)==="client"?"human":"agent",
        self: m.author===self.id, time:relMs(m.ts), text:m.text,
        review: (m.record && m.record.review) || null,
      }));
    },[convos, activeArtifact, nameOf, kindOf, self.id]);

    const homeActivity = useMemo(()=>activity.map(a=>({
      who:nameOf(a.author), text:a.text, time:relMs(a.ts),
    })),[activity, nameOf]);

    // No artItems[0] fallback — when activeArtifact isn't cached yet, fall back to a
    // minimal object named for it (NOT the first artifact, which would flash the wrong
    // doc); the record streams in via openArtifact's fetch.
    const artifact = artItems.find(a=>a.name===activeArtifact) ||
      { name:activeArtifact, version:0, status:statusOf(activeArtifact), topic:"", author:{name:"",kind:"agent"}, updated:"" };
    const convo = convList.find(c=>c.key===activeConvo) || convList[0] || { type:"topic", name:"", participants:0 };

    // Open an artifact. Default → full-page review stage (Artifacts list, Home,
    // Goals, doc-body [[wikilinks]]). With opts.popup → the dismissible modal over
    // the current stage (used for a chat artifact-reference, so the conversation
    // stays behind it). Both paths share the same load + the stale-fetch guard.
    function openArtifact(name, opts){
      touchRecent("art:"+name);
      setActiveArtifact(name); setArtMissing(false);
      if(opts && opts.popup){ setArtifactOpen(true); /* leave stageMode — the convo stays behind the modal */ }
      else { setStageMode("artifact"); setArtifactOpen(false); }
      artReqRef.current = name; // mark this as the current open — the fetch below only applies if it's still current
      const subj = companionTopic(name); ensureConvo(subj); backfill(subj); // load the inline discussion (TASK-83)
      const cached = records[name];
      setArtRecord(cached!==undefined ? cached : null);
      // Fetch by name (the API resolves names not in the cached list). A 404 or a
      // null record for a name that isn't in the directory means the ref is stale
      // — flag it so the stage/modal shows a graceful "not found" instead of the
      // wrong (fallback) document. Cache the record regardless, but only apply it to
      // the view (artRecord/artMissing) if THIS open is still current — a slower
      // earlier fetch must not clobber a newer review.
      apiGet("/api/artifacts/"+encodeURIComponent(name)).then(a=>{
        const rec=(a&&a.Record)||null; setRecords(prev=>({...prev,[name]:rec}));
        if(artReqRef.current!==name) return;
        setArtRecord(rec);
        if(!rec && !artifacts.some(x=>x.Name===name)) setArtMissing(true);
      }).catch(()=>{ if(artReqRef.current===name && !artifacts.some(x=>x.Name===name)) setArtMissing(true); });
    }
    // dismiss the review modal — the stage underneath is untouched, so this drops
    // you back exactly where you were. Clear the active artifact + the request ref
    // so a re-open is a fresh load and a late fetch can't repopulate a closed modal.
    function closeArtifact(){ setArtifactOpen(false); setActiveArtifact(""); setArtMissing(false); artReqRef.current=""; }
    // the modal's "Open in full page" action: close the modal, then re-open the
    // same artifact on the full-page stage.
    function openArtifactFullPage(name){ closeArtifact(); openArtifact(name); }
    function goHome(){ setStageMode("home"); }
    // ⌘K no-match → open the Assistant FAB with the typed query prefilled in the
    // composer (never auto-sent — the operator hits send). When violet is live the
    // FAB is the DM thread; when absent it shows the "not live yet" state. We set
    // BOTH asstPrompt (shown as the carried query) and asstDraft (the live composer
    // value) so the operator can edit + send.
    function askAssistant(query){ setPalette(false); const q=query||""; setAsstPrompt(q); setAsstDraft(q); setAsstOpen(true); }
    // send the FAB composer to violet's DM (the canonical 2-party topic), then
    // clear the FAB draft. No-op until violet + self are both known.
    function sendToAssistant(text){
      const body=(text||"").trim(); if(!body || !violet || !self.id) return;
      apiPublish(dmSubject(self.id, violet.id),{ "$type":"chat.message", text:body }).then(()=>{ setAsstDraft(""); setAsstPrompt(""); }).catch(()=>{});
    }
    // Workspace nav (flow2 chrome): Home / Artifacts / Goals / Agents swap the
    // white stage.
    // onNav(key[, arg]): swap the stage. For "goals", an optional arg is a goal id
    // to deep-link to (TASK-157) — set it so the remounted GoalsView opens that
    // goal's detail; a plain Goals nav (no arg) clears it and lands on the portfolio.
    function onNav(key, arg){ touchRecent("nav:"+key); if(key==="goals"){ setGoalsOpenId(typeof arg==="string"?arg:null); setGoalsEpoch(e=>e+1); } setStageMode(key); }

    // renderWiki: shared wikilink renderer for any view that shows goal/artifact
    // wikilinks in plain-text fields (goals north-star, criteria, etc.).
    // Splits text on [[name]] / [[name|alias]], resolves each target against the
    // known artifact names and goal names/ids, and returns an array of React nodes.
    // Known → clickable sx-artlink span; unknown → muted sx-artlink-dead span.
    // goal.<id> targets navigate to the Goals view; other targets open the artifact.
    function renderWiki(text) {
      if (!text) return text;
      const known = new Set();
      for (const a of artifacts) { if (a && a.Name) known.add(a.Name); }
      for (const g of goals) {
        if (g && g.name) known.add(g.name);
        if (g && g.id) known.add("goal." + g.id);
      }
      const parts = text.split(/(\[\[[^\]|]+(?:\|[^\]]+)?\]\])/g);
      if (parts.length === 1) return text;
      return parts.map((part, i) => {
        const m = part.match(/^\[\[([^\]|]+)(?:\|([^\]]+))?\]\]$/);
        if (!m) return part;
        const target = m[1].trim();
        const display = m[2] != null ? m[2].trim() : target;
        if (known.has(target)) {
          const onClick = (e) => {
            e.stopPropagation();
            if (target.indexOf("goal.") === 0) { onNav("goals"); }
            else { openArtifact(target); }
          };
          return <span key={i} className="sx-artlink" role="link" tabIndex={0} onClick={onClick}
            onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onClick(e); } }}>{display}</span>;
        }
        return <span key={i} className="sx-artlink-dead">{display}</span>;
      });
    }

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
        // carry record so review-marker fields survive into discussion (same as live stream)
        const hist=acc.map(f=>({ id:f.id, author:f.author, text:frameText(f.record), ts:0, record:f.record||null }));
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
    function expandConvo(key){ touchRecent("conv:"+key); ensureConvo(key); setActiveConvo(key); setStageMode("conversation"); backfill(key); }
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
    // and post a status-change event to the artifact's companion discussion topic.
    // The event is a backward-compatible chat.message with an extra `review` marker
    // so review.jsx can render it as a timeline status-change entry inline with comments.
    function setReview(name, state){
      let latestRev = null;
      apiReview(name, state)
        .then(()=>apiGet("/api/artifacts/"+encodeURIComponent(name)))
        .then(a=>{
          const rec=(a&&a.Record)||null;
          // capture the current revision for the marker (null if unavailable)
          latestRev = (a && typeof a.Revision==="number") ? a.Revision : (rec && rec.review && rec.review.rev) || null;
          setRecords(prev=>({...prev,[name]:rec}));
          if(name===activeArtifact) setArtRecord(rec);
        })
        .then(()=>{
          const marker = { state };
          if(latestRev !== null) marker.rev = latestRev;
          // returned into the chain so a publish failure is caught below.
          return apiPublish(companionTopic(name),{
            "$type":"chat.message",
            text: REVIEW_ACTION[state] || state,
            review: marker,
          });
        })
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

    // per-view review counts (the sidebar nav badges): the Artifacts badge counts
    // review-pending artifacts; the Goals badge counts goals awaiting the operator's
    // sign-off (TASK-157). Kept separate so each badge reflects what that view holds
    // (a review goal lives under Goals, not in the Artifacts list).
    const reviewCount = artItems.filter(a=>a.status==="review").length;
    const goalReviewCount = goals.filter(g=>g.review==="review").length;
    const workingCount = agents.filter(a=>a.state==="working").length;

    const ctx = {
      conversations:convList, activeConvo, stageMode, onOpenConvo:openConvo, onExpandConvo:expandConvo,
      messages, draft, setDraft, onSend:send, onArtifactRef:openArtifact,
      artifacts:artItems, activeArtifact, onOpenArtifact:openArtifact,
      goals, agents, activity:homeActivity, self, onGoHome:goHome, home, onDM:startDM,
      hidden, onHide:hideConvo, onUnhide:unhideConvo,
      onNav, onSearch:()=>setPalette(true), reviewCount, goalReviewCount, workingCount,
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
      // Agent rows keep a distinct "agent:<id>" key (a DM subject can also surface
      // as a Channel row, so reusing "conv:<subject>" would collide). startDM
      // records recency under the conversation; we ALSO touch the agent key here
      // so the Agent row itself accumulates recency and ranks up over time.
      agents.forEach(a=>items.push({ key:"agent:"+a.id, type:"Agent", label:a.name, sub:a.meta,
        kw:(a.name+" "+(a.headline||"")+" "+a.state).toLowerCase(),
        go:()=>{ if(a.id){ touchRecent("agent:"+a.id); startDM(a.id); } else onNav("agents"); } }));
      convList.forEach(c=>items.push({ key:"conv:"+c.key, type:"Channel",
        label:(c.type==="topic"?"# ":"@ ")+c.name, sub:c.snippet||"conversation",
        kw:(c.name+" "+(c.snippet||"")).toLowerCase(), go:()=>expandConvo(c.key) }));
      return items;
    };

    const hasAuthor = artifact.author && artifact.author.name;

    return (
      <div className="sx-app" style={{"--sx-side-w": sideCollapsed ? "0px" : sideWidth+"px"}}>
        <div style={{display:"contents"}}>
          <Sidebar ctx={ctx} busName={(self.display_name||"bus")} navMode={t.sideNav}
            sideWidth={sideWidth} sideCollapsed={sideCollapsed}
            onSideWidth={onSideWidth} onToggleSide={toggleSide} />
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
              <div className="sx-page sx-page--doc"><GoalsView key={goalsEpoch} goals={goals} initialGoalId={goalsOpenId} onOpenArtifact={openArtifact} onSetReview={setReview} renderWiki={renderWiki} /></div>
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
            <div className="sx-canvas sx-canvas--review sx-conv-light">
              <ReviewView
                artifact={artifact}
                record={artRecord}
                discussion={discussion}
                draft={draft} setDraft={setDraft}
                onSetReview={setReview}
                onSendComment={sendDiscussion}
                onExpandDiscussion={(n)=>expandConvo(companionTopic(n))}
                onBrowse={()=>setStageMode("artifacts")}
                railWidth={railWidth} railCollapsed={railCollapsed}
                onRailWidth={onRailWidth} onToggleRail={toggleRail}
                onOpenArtifact={openArtifact}
                artifactNames={artifacts.map(a=>a.Name)}
              />
            </div>
          ) : (
            <ConversationView
              convo={convo}
              messages={messages}
              draft={draft} setDraft={setDraft} onSend={send}
              onArtifactRef={(n)=>openArtifact(n,{popup:true})}
              artifactNames={artifacts.map(a=>a.Name)}
              agents={agents}
              self={self}
            />
          )}
        </main>

        {/* The artifact-review MODAL — an OPTION, not the default (Lena's
            #ui-feedback). Opened only when an artifact is referenced from a
            conversation (openArtifact(name,{popup:true})); it floats over the chat
            instead of taking the stage over, so dismissing returns you to the thread.
            Exits: the "Open in full page" button (escalates to the full-page review)
            · × · scrim · Esc. The scrim closes on a click of itself only — the panel
            stops propagation. */}
        {artifactOpen && (
          <div className="sx-artmodal-scrim" onMouseDown={(e)=>{ if(e.target===e.currentTarget) closeArtifact(); }}>
            <div className="sx-artmodal-panel" role="dialog" aria-modal="true"
              aria-label={artMissing ? "Artifact not found" : ("Review "+(artifact.name||activeArtifact))}
              tabIndex={-1} ref={el=>{ if(el && !el.dataset.focused){ el.dataset.focused="1"; el.focus(); } }}
              onMouseDown={(e)=>e.stopPropagation()}>
              <div className="sx-artmodal-actions">
                <button className="sx-artmodal-fp" title="Open in full page view" onClick={()=>openArtifactFullPage(activeArtifact)}>Open in full page ⤢</button>
                <button className="sx-artmodal-x" aria-label="Close" title="Close (Esc)" onClick={closeArtifact}>×</button>
              </div>
              {artMissing ? (
                <div className="sx-artmodal-missing sx-conv-light">
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
                    <button className="sx-sbtn sx-sbtn-req" onClick={()=>{ closeArtifact(); setStageMode("artifacts"); }}>Browse artifacts →</button>
                  </div>
                </div>
              ) : (
                <div className="sx-canvas--review sx-conv-light">
                  <ReviewView
                    artifact={artifact}
                    record={artRecord}
                    discussion={discussion}
                    draft={draft} setDraft={setDraft}
                    onSetReview={setReview}
                    onSendComment={sendDiscussion}
                    onExpandDiscussion={(n)=>{ closeArtifact(); expandConvo(companionTopic(n)); }}
                    inModal={true}
                    onBrowse={closeArtifact}
                    onClose={closeArtifact}
                    railWidth={railWidth} railCollapsed={railCollapsed}
                    onRailWidth={onRailWidth} onToggleRail={toggleRail}
                    onOpenArtifact={openArtifact}
                    artifactNames={artifacts.map(a=>a.Name)}
                  />
                </div>
              )}
            </div>
          </div>
        )}

        <AssistantFab open={asstOpen} prompt={asstPrompt}
          assistant={violet} online={violetOnline}
          messages={assistantMessages} self={self}
          draft={asstDraft} setDraft={setAsstDraft}
          onSend={sendToAssistant} onArtifactRef={openArtifact}
          artifactNames={artifacts.map(a=>a.Name)}
          onOpen={()=>{ setAsstPrompt(""); setAsstDraft(""); setAsstOpen(true); }}
          onClose={()=>{ setAsstOpen(false); setAsstPrompt(""); }} />
        {palette && <CmdK index={searchIndex()} recents={recents} assistantLive={!!violet} onClose={()=>setPalette(false)} onAsk={askAssistant} />}

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
