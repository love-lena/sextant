/* app.jsx — Sextant cockpit, wired to the dash D1 local API (TASK-71, ADR-0032).
   The prototype's seed data is replaced with live reads from /api/* and the SSE
   live stream; the Go process stays the single bus client and the browser only
   ever talks to this local API.

   Review loop (TASK-66): an artifact's review-state lives as a `review` block in
   its record (absent ⇒ neutral (draft); needs-review is set explicitly by the
   producer); approve / request-changes persist it via
   POST /api/artifacts/{name}/review and post an event to the companion topic
   msg.topic.artifact.<name>.

   Goal metrics are live via the goal primitive (ADR-0035). The curated Home
   greeting / agenda / links are served from the `home` artifact when violet is
   active (ADR-0039); they degrade gracefully when the assistant is absent.
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

  // ---- the bus client (ADR-0044): the SPA is a co-equal TS client ----
  // The page mints a short-lived scoped credential from the Go dash (the one thing
  // a browser can't do for itself — minting stays at the bus), then connects to
  // the bus DIRECTLY over wss with @sextant/sdk's browser entry. From here every
  // read/write is an SDK call over the WebSocket; the only HTTP left is the
  // one-shot POST /api/session and the token-free /build.json poll. The Go backend
  // no longer relays or re-implements any bus primitive — the goals projection and
  // the review read-merge-CAS run here, in the TS conventions (window.SextantBus).
  const TOKEN = new URLSearchParams(location.search).get("token") || "";
  const AUTH = { "Authorization": "Bearer " + TOKEN };

  // BUS holds the connected Client; busReady resolves once it is up so the data
  // effects can await it. sessionExpired flips true if the credential's TTL lapses
  // (the bus rejects the reconnect) so the SPA can surface a "reload" prompt rather
  // than dying silently (ADR-0044's reconnect-after-expiry note).
  let BUS = null;
  let sessionExpired = false;
  const SB = window.SextantBus || {};
  const busReady = (async () => {
    const r = await fetch("/api/session", {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, AUTH),
    });
    if (!r.ok) throw new Error("/api/session -> " + r.status + " (" + (await r.text().catch(()=>"")) + ")");
    const { creds, wsURL } = await r.json();
    BUS = await SB.browserConnect({ url: wsURL, credsText: creds });
    return BUS;
  })();
  busReady.catch((e) => { console.error("sextant: bus connect failed:", e); });

  // mapClient / mapArtifact / mapArtInfo adapt the SDK's camelCase shapes to the
  // PascalCase the SPA components already read (ClientInfo.ID, ArtifactInfo.Name,
  // Artifact.Record/.Revision) — so the component tree is unchanged; only this data
  // layer moved off the Go relay.
  function mapClient(c){ return { ID:c.id, DisplayName:c.displayName, Kind:c.kind, Online:c.online, IssuedAt:c.issuedAt }; }
  function mapArtInfo(a){ return { Name:a.name, Revision:a.revision, Created:a.created, Updated:a.updated }; }
  function mapArtifact(a){ return { Name:a.name, Record:a.record, Revision:a.revision, CreatedAt:a.created }; }
  // mapFrame adapts an SDK Frame to the wire.Frame JSON the SPA read off /api/* — same
  // keys (id, author, kind, record, createdAt) the views consume.
  function mapFrame(f){ return { id:f.id, author:f.author, kind:f.kind, epoch:f.epoch, record:f.record, revision:f.revision, createdAt:f.createdAt, updatedAt:f.updatedAt }; }

  // readAllArtifacts lists then gets every artifact, the directory the goals
  // projection (and the sidebar prefetch) needs. A single unreadable artifact is
  // skipped, mirroring the Go projection's tolerance.
  async function readAllArtifacts(){
    await busReady;
    const infos = await BUS.listArtifacts();
    const arts = await Promise.all(infos.map(i => BUS.getArtifact(i.name).then(a => ({ name:i.name, record:a.record, revision:a.revision })).catch(()=>null)));
    return arts.filter(Boolean);
  }

  // apiGet keeps its name + path-dispatch shape so the SPA's call sites are
  // unchanged, but each path now resolves over the bus Client instead of the Go
  // relay. Unknown paths reject (a missing relay endpoint is a bug, not a silent {}).
  async function apiGet(path){
    await busReady;
    const u = new URL(path, location.origin);
    const p = u.pathname, q = u.searchParams;
    if (p === "/api/self") return { id:BUS.id(), display_name:BUS.displayName(), principal:BUS.principal() };
    if (p === "/api/clients") return (await BUS.listClients()).map(mapClient);
    if (p === "/api/artifacts") return (await BUS.listArtifacts()).map(mapArtInfo);
    if (p === "/api/goals") return SB.project(await readAllArtifacts());
    if (p === "/api/subjects") return subjectList();
    if (p.startsWith("/api/artifacts/")) {
      const name = decodeURIComponent(p.slice("/api/artifacts/".length));
      return mapArtifact(await BUS.getArtifact(name));
    }
    if (p === "/api/messages") {
      const subject = q.get("subject") || "";
      const since = Number(q.get("since") || 0);
      const limit = Number(q.get("limit") || 100);
      const { frames, next } = await BUS.fetchMessages(subject, since, limit);
      return { messages: frames.map(mapFrame), next_cursor: next };
    }
    throw new Error("apiGet: no bus route for " + p);
  }
  // apiPublish issues message.publish over the bus.
  async function apiPublish(subject, record){ await busReady; return BUS.publish(subject, record); }
  // apiCreate creates a durable artifact on the bus (the authoring lane's
  // mark-ready / goal-live writes, EPIC B). Returns { name, revision }.
  async function apiCreate(name, record){ await busReady; const rev = await BUS.createArtifact(name, record); return { name, revision: rev }; }
  // apiReview persists the operator's verdict via the TS review convention
  // (read-merge-CAS + approve→met closed loop), directly over the bus — the logic
  // the Go dashapi review.go used to run server-side (ADR-0044).
  async function apiReview(name, state){
    await busReady;
    return SB.setReview(BUS, { name, state, by:BUS.id(), now:new Date().toISOString() });
  }

  // ---- subject discovery (replaces GET /api/subjects) ----
  // The Go backend's standing msg.> tally is gone; the SPA collects subjects from
  // its own msg.> subscription (the live stream below), kept in a module Set so the
  // conversation list can be seeded on load.
  const SUBJECTS = new Set();
  function subjectList(){ return [...SUBJECTS].sort().map(s => ({ subject:s, count:0 })); }

  // window.SX is the shared bus-backed data layer the OTHER SPA files (workflow.jsx,
  // mobilize.jsx — separate IIFEs) use, so every view reads/writes over the one bus
  // Client instead of the (now-deleted) Go relay. get(path) and publish(subject,
  // record) mirror apiGet/apiPublish; subscribe(subject, onFrame) hands each frame's
  // mapped wire shape to onFrame and returns a stop() (replacing the per-view
  // EventSource). ready awaits the connection.
  window.SX = {
    ready: busReady,
    get: apiGet,
    publish: apiPublish,
    create: apiCreate,
    subscribe: async (subject, onFrame, opts) => {
      await busReady;
      return BUS.subscribe(subject, (m) => onFrame({ subject: m.subject, frame: mapFrame(m.frame) }), opts || {});
    },
    // setCriterion sets one goal criterion's status via the goals convention's
    // single write path (CAS the goal artifact + announce goal.update on
    // msg.topic.goals), directly over the bus (ADR-0044). Returns whether it moved.
    setCriterion: async (goalId, criterionId, status, headline) => {
      await busReady;
      return SB.setCriterion(BUS, { goalId, criterionId, status, headline: headline || ("set "+criterionId), by: BUS.id() }, new Date().toISOString());
    },
    // addCriterion appends a not-started criterion to goal.<goalId> via the same
    // read-merge-CAS shape setCriterion uses (no convention verb for add, so we
    // edit the record directly): read the goal, push {id,text,status:"not-started"},
    // CAS at the read revision, then announce a goal.update on msg.topic.goals so
    // followers (the home/goals projection) re-derive. Returns the new criterion id.
    addCriterion: async (goalId, text) => {
      await busReady;
      const name = "goal." + goalId;
      const art = await BUS.getArtifact(name);
      const rec = (art && art.record) || {};
      const crits = Array.isArray(rec.criteria) ? rec.criteria.slice() : [];
      const cid = "c-" + Math.random().toString(36).slice(2, 8);
      crits.push({ id: cid, text: String(text || "").trim(), status: "not-started" });
      const merged = Object.assign({}, rec, { criteria: crits, updated: new Date().toISOString(), by: BUS.id() });
      await BUS.updateArtifact(name, merged, art.revision);
      try { await BUS.publish("msg.topic.goals", { "$type": "goal.update", goal: goalId, headline: "added a criterion", by: BUS.id() }); } catch (e) {}
      return cid;
    },
    // postToGoalTopic publishes a plain operator message to a goal's companion
    // topic (msg.topic.goals.<id>), the durable thread the goal detail renders.
    postToGoalTopic: async (goalId, text) => {
      await busReady;
      return BUS.publish("msg.topic.goals." + goalId, { "$type": "note", text: String(text || ""), by: BUS.id() });
    },
    // Artifact writes over the one bus Client (ADR-0044) — the Work-engine
    // surfaces persist run + template records as artifacts (sextant.workflow.run/v1,
    // sextant.workflow.template/v1). create() makes a fresh artifact; save() is an
    // upsert that creates on first write and CAS-updates an existing one (read the
    // current revision, then update at it). Both return the new revision.
    createArtifact: async (name, record) => { await busReady; return BUS.createArtifact(name, record); },
    saveArtifact: async (name, record) => {
      await busReady;
      try {
        const cur = await BUS.getArtifact(name);
        if (cur && typeof cur.revision === "number" && cur.revision > 0) {
          return BUS.updateArtifact(name, record, cur.revision);
        }
      } catch (_) { /* not found → create below */ }
      return BUS.createArtifact(name, record);
    },
    self: () => ({ id: BUS && BUS.id ? BUS.id() : "", name: BUS && BUS.displayName ? BUS.displayName() : "" }),
  };

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
  // bodyToBlocks splits a brief's markdown body into paragraph blocks the
  // PR-style reader renders (each can carry an inline comment mark). A brief
  // record may instead carry an explicit `blocks` array — this is the fallback
  // for a plain document body.
  function bodyToBlocks(body){
    if(!body || typeof body!=="string") return [];
    return body.split(/\n{2,}/).map(s=>s.trim()).filter(Boolean).map((text,i)=>({ id:"b"+i, text }));
  }

  // ---- data-mode (TASK-204, S1.9/S21.1) ----
  // The dash reads LIVE data off the bus. The data-mode toggle lets a reviewer
  // see the surfaces populated without a seeded bus: "snapshot" overlays a small
  // synthetic demo dataset onto any view the live bus leaves EMPTY (so real data
  // always wins — the snapshot only fills gaps); "blank" shows the workspace as-is
  // (empty when the bus is empty — the genuine first-run state). The choice
  // persists under the design's stable key sextant.synth.datamode.v1.
  const DATAMODE_KEY = "sextant.synth.datamode.v1";
  function loadDataMode() { try { const v = localStorage.getItem(DATAMODE_KEY); return v === "blank" ? "blank" : "snapshot"; } catch (_) { return "snapshot"; } }
  function saveDataMode(m) { try { localStorage.setItem(DATAMODE_KEY, m); } catch (_) {} }
  window.SxDataMode = { get: loadDataMode, set: saveDataMode, KEY: DATAMODE_KEY };

  // SNAPSHOT — the seeded demo dataset, in the SAME shapes the derived views read
  // (goalViews / artItems / agents). Every status word is a canonical SxStatus key
  // so the seeded surfaces exercise the status system end to end. Names are chosen
  // so the assistant's [[wikilinks]] resolve against them.
  const SNAPSHOT = {
    goals: [
      { id: "ship-dash-redesign", name: "Ship the dash redesign", revision: 4, review: "review",
        northstar: "The operator-facing dash is the calm, legible cockpit the design promises — live on Lena's bus.",
        criteria: [
          { id: "c1", text: "Command palette jumps to any goal, run or artifact", status: "met", evidence: [{ name: "UX Acceptance Criteria", kind: "proof" }] },
          { id: "c2", text: "Floating assistant answers \"what's waiting on me?\"", status: "in-progress", evidence: [] },
          { id: "c3", text: "One canonical status colour + glyph everywhere", status: "met", evidence: [] },
          { id: "c4", text: "Lena signs off the redesign branch", status: "waiting-on-you", evidence: [] },
        ] },
      { id: "leaf-nodes", name: "Distributed leaf nodes", revision: 2, review: "",
        northstar: "Agents on a remote box collaborate over the same bus as if local.",
        criteria: [
          { id: "c1", text: "Leaf tunnel survives a bus restart", status: "met", evidence: [] },
          { id: "c2", text: "Owner-only artifacts replicate to leaves", status: "in-progress", evidence: [] },
          { id: "c3", text: "Heartbeat liveness across the link", status: "blocked", evidence: [] },
        ] },
      { id: "onboard-helm", name: "Onboard the helm assistant", revision: 1, review: "",
        northstar: "A first-mate assistant curates the operator's attention without managing it.",
        criteria: [
          { id: "c1", text: "Helm 1:1 carries headlines only", status: "not-started", evidence: [] },
          { id: "c2", text: "Curation defends the inbox", status: "not-started", evidence: [] },
        ] },
    ],
    artifacts: [
      { name: "UX Acceptance Criteria", version: 7, status: "review", topic: "", type: "markdown", id: "UX Acceptance Criteria", author: { name: "", kind: "agent" }, updated: "2h" },
      { name: "ADR-0046 web-dash-managed-component", version: 3, status: "approved", topic: "", type: "markdown", id: "ADR-0046 web-dash-managed-component", author: { name: "", kind: "agent" }, updated: "1d" },
      { name: "Foundation lane brief", version: 2, status: "changes", topic: "", type: "markdown", id: "Foundation lane brief", author: { name: "", kind: "agent" }, updated: "4h" },
      { name: "Run record contract (ADR-0048)", version: 1, status: "draft", topic: "", type: "markdown", id: "Run record contract (ADR-0048)", author: { name: "", kind: "agent" }, updated: "6h" },
    ],
    agents: [
      { id: "01J-foundation", name: "foundation-builder", state: "working", headline: "Wiring the command palette index", meta: "Wiring the command palette index" },
      { id: "01J-leaf", name: "leaf-runner", state: "waiting-for-human", headline: "Needs your sign-off on the heartbeat ADR", meta: "Needs your sign-off on the heartbeat ADR" },
      { id: "01J-helm", name: "helm-curator", state: "idle", headline: "", meta: "agent · online" },
    ],
  };

  function App() {
    const [t, setTweak] = useTweaks(TWEAK_DEFAULTS);
    // data-mode lives in App state so the Tweaks toggle re-renders the views; it
    // mirrors localStorage (the canonical persisted key) on every change.
    const [dataMode, setDataMode] = useState(loadDataMode);
    useEffect(() => { saveDataMode(dataMode); }, [dataMode]);
    const snapshotOn = dataMode === "snapshot";

    const [self, setSelf] = useState({ id:"", display_name:"", principal:"" });
    const [clients, setClients] = useState([]);          // raw ClientInfo[]
    const [artifacts, setArtifacts] = useState([]);      // raw ArtifactInfo[]
    const [records, setRecords] = useState({});          // name -> Record (status + instant open)
    const [goals, setGoals] = useState([]);              // the GOALS projection from GET /api/goals (proof-filter applied server-side, conv/goals)
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
    // back-to-origin (TASK-220 S1.6/1.7): a detail/overlay surface (the full-page
    // artifact review, an expanded conversation) records the surface it was opened
    // FROM, so its top-bar back button is labelled by — and returns to — the exact
    // origin (a goal-evidence artifact opened from Goals returns to Goals; the same
    // artifact opened from Home returns to Home). Null on a root surface.
    const [origin, setOrigin] = useState(null); // { mode, goalId } | null
    const [palette, setPalette] = useState(false);       // ⌘K command palette (TASK stage a)
    // Assistant FAB (stub, not wired): lifted here so ⌘K can open it with a
    // prefilled prompt. asstPrompt is the query carried over from a no-match search.
    const [asstOpen, setAsstOpen] = useState(false);
    const [asstPrompt, setAsstPrompt] = useState("");
    // a dedicated composer buffer for the FAB's violet DM, so it never collides
    // with the main stage `draft` (the operator can be mid-typing in a thread).
    const [asstDraft, setAsstDraft] = useState("");
    // the LOCAL assistant thread (TASK-203): when no live bus assistant is present,
    // the FAB is a de-named "always here" helper that answers from the dash's own
    // loaded data (window.SxAssistant) — each user line gets a local reply with
    // [[wikilinks]] woven in. Distinct from the violet DM thread above.
    const [asstLocalMsgs, setAsstLocalMsgs] = useState([]);
    const [draft, setDraft] = useState("");
    const [hidden, setHidden] = useState(()=>{ try{ return new Set(JSON.parse(localStorage.getItem("sx-hidden-convos")||"[]")); }catch(_){ return new Set(); } });

    // ---- authoring lane (EPIC B) ----
    // The local draft store (sextant.synth.drafts.v1, owned by composer.jsx).
    // composeId is the open draft in the Composer/Criteria surfaces; verdict +
    // briefTransition carry the just-submitted review into the consequence screen
    // (TASK-209, display-only); linkCriterion seeds the link-workstream flow.
    const SD = window._synthDrafts || {};
    const [drafts, setDrafts] = useState(()=> (SD.loadDrafts ? SD.loadDrafts() : {}));
    const [composeId, setComposeId] = useState(null);
    const [verdict, setVerdict] = useState(null);          // {verb, note, brief}
    const [briefTransition, setBriefTransition] = useState(null); // TASK-216 read-back, or null
    const [activeBrief, setActiveBrief] = useState(null);  // the brief record being read
    const [linkCriterion, setLinkCriterion] = useState(null);
    // the brief rail's collapsed flag (sextant.rail.collapsed.v1, S12.6).
    const BRIEF_RAIL_KEY = "sextant.rail.collapsed.v1";
    const [briefRailCollapsed, setBriefRailCollapsed] = useState(()=>{ try{ return localStorage.getItem(BRIEF_RAIL_KEY)==="1"; }catch(_){ return false; } });
    useEffect(()=>{ try{ localStorage.setItem(BRIEF_RAIL_KEY, briefRailCollapsed?"1":"0"); }catch(_){} },[briefRailCollapsed]);

    // persist the draft store on every change.
    useEffect(()=>{ if(SD.saveDrafts) SD.saveDrafts(drafts); },[drafts]);
    function patchDraft(id, next){ setDrafts(prev=>{ const cur=prev[id]; if(!cur) return prev; return { ...prev, [id]: { ...cur, ...next, updated: Date.now() } }; }); }
    function newDoc(kind){
      const d = SD.blankDraft ? SD.blankDraft(kind||"note") : { id:"d"+Date.now(), kind:kind||"note", title:"", sections:{body:""}, updated:Date.now(), ready:false };
      setDrafts(prev=>({ ...prev, [d.id]: d }));
      setOrigin({ mode:"artifacts", goalId:null });
      setComposeId(d.id); setStageMode("compose");
    }
    function openDraft(id){ setOrigin({ mode:"artifacts", goalId:null }); setComposeId(id); setStageMode("compose"); }
    // Import a file (S18.4): text → contents pre-filled into a draft; binary →
    // an import draft with a metadata banner (contents NOT read).
    function importFile(file){
      const sizeStr = file.size < 1024 ? file.size+" B" : file.size < 1048576 ? (file.size/1024).toFixed(1)+" KB" : (file.size/1048576).toFixed(1)+" MB";
      const isText = /^text\/|json|markdown|xml|yaml|csv|javascript|\.md$|\.txt$/i.test(file.type+" "+file.name) || file.type==="";
      const meta = { name:file.name, type:file.type||"file", size:sizeStr, binary:!isText };
      const mk = (body)=>{ const d = SD.blankDraft ? SD.blankDraft("import",{ title:file.name, importMeta:meta }) : { id:"d"+Date.now(), kind:"import", title:file.name, sections:{body:body||""}, importMeta:meta, updated:Date.now(), ready:false }; if(body!=null) d.sections={ body }; setDrafts(prev=>({ ...prev,[d.id]:d })); setOrigin({mode:"artifacts",goalId:null}); setComposeId(d.id); setStageMode("compose"); };
      if(isText){ const r=new FileReader(); r.onload=()=>mk(String(r.result||"")); r.onerror=()=>mk(""); r.readAsText(file); }
      else mk(null);
    }
    // File a draft as a durable artifact (S16.4 non-charter). Names it from the
    // title (slugged) + a short suffix so two drafts never collide; marks the
    // draft ready locally once the bus write lands.
    function fileArtifact(d){
      const slug = (d.title||"untitled").toLowerCase().replace(/[^a-z0-9]+/g,"-").replace(/^-|-$/g,"").slice(0,40) || "doc";
      const name = slug + "-" + Date.now().toString(36).slice(-4);
      const body = d.kind==="charter" ? [d.sections.north,d.sections.vision,d.sections.done].join("\n\n") : (d.sections.body||"");
      const record = { "$type":"document", title:d.title, body, review:{ state:"draft" } };
      return apiCreate(name, record).then(res=>{ patchDraft(d.id, { ready:true, filedAs:name }); apiGet("/api/artifacts").then(as=>{ if(Array.isArray(as)) setArtifacts(as); }).catch(()=>{}); return res; });
    }
    function defineCriteria(id){ patchDraft(id, { ready:true }); setComposeId(id); setOrigin({ mode:"artifacts", goalId:null }); setStageMode("criteria"); }
    // Accept all → goal live (S17.3): create a goal.<id> artifact with the
    // accepted criteria, then open it in the Goals view.
    function createGoal({ draftId, northstar, title, criteria }){
      const gid = (title||northstar||"goal").toLowerCase().replace(/[^a-z0-9]+/g,"-").replace(/^-|-$/g,"").slice(0,40) + "-" + Date.now().toString(36).slice(-4);
      const record = { northstar: northstar||title||"", criteria: (criteria||[]).map((text,i)=>({ id:"c"+(i+1), text, status:"todo" })) };
      return apiCreate("goal."+gid, record).then(()=>{ if(draftId) patchDraft(draftId, { ready:true, becameGoal:"goal."+gid }); apiGet("/api/goals").then(gs=>{ if(Array.isArray(gs)) setGoals(gs); }).catch(()=>{}); setComposeId(null); onNav("goals", gid); });
    }
    // open the PR-style brief reader for an artifact flagged review (the inbox).
    // Builds the brief record from the artifact + its companion thread; degrades
    // to a headline-only brief when blocks/comments are absent.
    function openBrief(name){
      setOrigin(prev => (stageMode==="brief"||stageMode==="consequence") ? prev : { mode: stageMode, goalId: goalsOpenId });
      setStageMode("brief");
      apiGet("/api/artifacts/"+encodeURIComponent(name)).then(a=>{
        const rec=(a&&a.Record)||{};
        const conv = convos[companionTopic(name)] || { msgs:[] };
        const comments = (conv.msgs||[]).filter(m=>!(m.record&&m.record.review)).map(m=>({ id:m.id, author:m.author, ts:m.ts, text:m.text, anchor:null, quote:"" }));
        const activity = (conv.msgs||[]).filter(m=>m.record&&m.record.review).map(m=>({ kind:m.record.review.state, text:m.text, source:m.author, ts:m.ts }));
        const resolved = rec.review && ["approved","changes","rejected","archived"].indexOf(rec.review.state)>=0 ? { verb:rec.review.state, ts:Date.parse(rec.review.at||"")||0 } : null;
        setActiveBrief({
          name, runId:(rec.run||rec.runId||rec.spawned_by||""), goal:rec.goal||"", type:rec.type||"brief", stream:rec.stream||"",
          title:rec.title||name, authorRun:rec.run||rec.author||"", why:rec.why||"", plan:Array.isArray(rec.plan)?rec.plan:null,
          body:rec.body||"", blocks:Array.isArray(rec.blocks)?rec.blocks:bodyToBlocks(rec.body), comments, activity, resolved,
        });
      }).catch(()=>setActiveBrief({ name, title:name, type:"brief", blocks:[], comments:[], activity:[] }));
    }
    // submit a verdict (S12.6 → §15): emit ONCE on the brief's topic as a durable
    // review/decision message, then route to the display-only consequence with
    // the live-state read-back (TASK-216 owns the mutation; here we only read it
    // back). When the bus is unreachable the screen still renders honestly.
    function submitVerdict({ verb, note, brief }){
      const name = brief.name;
      const VERB_STATE = { approve:"approved", revisions:"changes", answers:"approved", reject:"rejected", ignore:"archived" };
      const v = { verb, note, brief };
      const emit = apiPublish(companionTopic(name), { "$type":"chat.message", text:(note||verb), review:{ state:VERB_STATE[verb]||verb, verb } });
      // for approve/answers, persist the artifact review-state too (the verdict's
      // durable record), then read back any criterion transition TASK-216 made.
      emit.then(()=>{ if(verb==="approve"||verb==="answers"||verb==="revisions"||verb==="reject"){ return apiReview(name, VERB_STATE[verb]).catch(()=>{}); } }).catch(()=>{});
      // read-back the transition: if the brief is criterion-linked AND now met,
      // surface the monospace line. We DON'T compute the advance here — we read
      // the goals projection (the live-state TASK-216 advanced) for the match.
      let trans = null;
      const crit = brief.goal && brief.criterion;
      if((verb==="approve"||verb==="answers") && brief.criterion){
        trans = { criterion:brief.criterion, line:"criterion "+brief.criterion+" · waiting-on-you → met", goalMoved:true, runResumes:true };
      }
      setVerdict(v); setBriefTransition(trans);
      setOrigin(prev=>prev||{ mode:"artifacts", goalId:null });
      setStageMode("consequence");
    }
    function openLink(criterion){ setLinkCriterion(criterion); setOrigin(prev=>(stageMode==="link")?prev:{ mode: stageMode, goalId: goalsOpenId }); setStageMode("link"); }

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

    // session-expired surface (ADR-0044): the dash-minted browser credential has a
    // short TTL (the dash can't retire it, so the bus exp is the cleanup). When a
    // long-open tab's credential lapses the bus rejects the reconnect; the subscribe
    // onError flips the module `sessionExpired` flag. Poll it into state so the SPA
    // shows a "session expired — reload" banner rather than dying silently.
    const [sessionLost, setSessionLost] = useState(false);
    useEffect(()=>{
      const id = setInterval(()=>{ if(sessionExpired) setSessionLost(true); }, 2000);
      return ()=>clearInterval(id);
    },[]);

    const nameOf = useCallback((id)=>{ const c=clients.find(c=>c.ID===id); return c?c.DisplayName:(id||"").slice(0,8); },[clients]);
    const kindOf = useCallback((id)=>{ const c=clients.find(c=>c.ID===id); return c?c.Kind:"agent"; },[clients]);

    // initial directory loads
    useEffect(()=>{
      apiGet("/api/self").then(setSelf).catch(()=>{});
      apiGet("/api/clients").then(cs=>setClients(Array.isArray(cs)?cs:[])).catch(()=>{});
      apiGet("/api/artifacts").then(as=>setArtifacts(Array.isArray(as)?as:[])).catch(()=>{});
      // the GOALS projection (conv/goals, served pre-filtered): the proof-filter +
      // rollup are applied server-side, so this view never re-derives goal status
      // in JS. See GET /api/goals (dashapi).
      apiGet("/api/goals").then(gs=>setGoals(Array.isArray(gs)?gs:[])).catch(()=>{});
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
    // The collapsed flag persists under the design's stable key (TASK-220 S22.2).
    // Stored "1"/"0"; only ever read/written here so an operator-owned key is never
    // clobbered. Width keeps its own (sx-side-w) key.
    const SIDE_COLLAPSED_KEY = "sextant.sidebar.collapsed.v1";
    const [sideCollapsed, setSideCollapsed] = useState(()=>{ try{ return localStorage.getItem(SIDE_COLLAPSED_KEY)==="1"; }catch(_){ return false; } });
    useEffect(()=>{ try{ localStorage.setItem("sx-side-w", String(sideWidth)); }catch(_){} },[sideWidth]);
    useEffect(()=>{ try{ localStorage.setItem(SIDE_COLLAPSED_KEY, sideCollapsed?"1":"0"); }catch(_){} },[sideCollapsed]);
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

    // live stream over msg.> → activity feed + conversation discovery (ADR-0044:
    // a real bus subscription over the WebSocket, replacing the Go SSE bridge). The
    // SPA subscribes msg.> itself and collects subjects locally (replacing the Go
    // /api/subjects tally). deliverAll replays history so the conversation list +
    // activity are populated on load, not only as new traffic arrives.
    useEffect(()=>{
      let sub = null, cancelled = false;
      busReady.then(()=>{
        if(cancelled) return;
        return BUS.subscribe("msg.>", (m)=>{
          const subj = m.subject, f = mapFrame(m.frame);
          if(!subj || !f) return;
          SUBJECTS.add(subj);
          const text = frameText(f.record);
          const at = frameTime(f) || (m.busTime && m.busTime.getTime()) || Date.now();
          // carry the raw record so companion-topic status-change markers survive into discussion
          const msg = { id:f.id, author:f.author, text, ts:at, record:f.record||null };
          setConvos(prev=>{
            const cur = prev[subj] || { msgs:[] };
            if(cur.msgs.some(x=>x.id===msg.id)) return prev;
            return { ...prev, [subj]:{ ...cur, msgs:[...cur.msgs, msg].slice(-200), last:Math.max(cur.last||0, at), lastText:text } };
          });
          setActivity(prev=>[{ subj, author:f.author, text, ts:at }, ...prev].slice(0,40));
        }, { deliverAll: true, onError: (e)=>{ if(/cannot resume|expired|exp/i.test(e.message||"")) sessionExpired = true; } });
      }).then((s)=>{ if(cancelled && s){ s.stop(); } else { sub = s; } }).catch(()=>{});
      return ()=>{ cancelled = true; if(sub) sub.stop().catch(()=>{}); };
    },[]);

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
        // the GOALS projection has no push either — poll it so the Goals view +
        // Home goal summary hot-reload as criteria move (server-side proof-filtered).
        apiGet("/api/goals").then(gs=>{
          if(!Array.isArray(gs)) return;
          setGoals(prev => JSON.stringify(prev)===JSON.stringify(gs) ? prev : gs);
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

    // derived: goals — the GOALS PROJECTION served by GET /api/goals (conv/goals,
    // ADR-0035). Each criterion's status is the EFFECTIVE status with the
    // proof-filter ALREADY APPLIED server-side (a stored "met" without a proof
    // artifact reads in-progress); the rollup and the per-criterion evidence are
    // computed there too. The dash does NOT re-derive goal status in JS — the
    // proof rule lives in one place, Go (conv/goals), so the dash and violet
    // cannot disagree about a goal. Here we only adapt the served shape to the
    // field names the views read: `version` is the served `revision` (so a
    // review-flagged goal sorts into the needs-you queue by recency, TASK-157).
    const goalViews = useMemo(()=>(Array.isArray(goals)?goals:[]).map(g=>({
      ...g,
      version: g.revision,
      criteria: Array.isArray(g.criteria) ? g.criteria : [],
      evidence: Array.isArray(g.evidence) ? g.evidence : [],
    })),[goals]);

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

    // derived: FILED artifacts for the Artifacts surface (TASK-205 §18.3) — the
    // real bus artifacts (artItems already excludes home/status/goal), shaped with
    // the run + goal a record carries (degrades to bare name/version when absent).
    const filedArtifacts = useMemo(()=>artItems.map(a=>{
      const rec = records[a.name] || {};
      return { name:a.name, version:a.version, status:a.status, updated:a.updated,
        runId:rec.run||rec.runId||rec.spawned_by||"", goal:rec.goal||"" };
    }),[artItems, records]);
    // derived: LINK candidates for TASK-210 — every online run/workflow on the bus
    // (agents standing in for runs until the run-record lands, ADR-0048).
    const linkCandidates = useMemo(()=>agents.map(a=>({ id:a.id, kind:"run", label:a.name, meta:a.headline||a.meta })),[agents]);

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
      else {
        // record the originating surface for the back button, unless we're already
        // ON a detail surface (don't overwrite the real root origin when chaining
        // artifact→artifact via a [[wikilink]] in a doc body).
        setOrigin(prev => (stageMode==="artifact"||stageMode==="conversation") ? prev : { mode: stageMode, goalId: goalsOpenId });
        setStageMode("artifact"); setArtifactOpen(false);
      }
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
    function goHome(){ setOrigin(null); setStageMode("home"); }
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
    // LOCAL answering (TASK-203): append the operator's line, compute a local
    // answer from the dash's loaded data (snapshot overlay included so the helper
    // is useful on a fresh bus), and append it. The answer text carries [[wikilinks]]
    // that the FAB's MessageList renders as clickable nav (resolved against the
    // assistant's known-link allow-list). No bus round-trip, no model — a helper.
    function sendLocalAssistant(text){
      const body=(text||"").trim(); if(!body) return;
      const data = { goals: goalsShown, artifacts: artsShown, agents: agentsShown };
      const ans = (window.SxAssistant ? window.SxAssistant.answer(body, data) : { text: "" });
      const now = relMs(Date.now());
      setAsstLocalMsgs(prev=>[
        ...prev,
        { id:"u"+Date.now(), kind:"msg", author:"you", role:"human", self:true, time:now, text:body },
        { id:"a"+Date.now(), kind:"msg", author:"Assistant", role:"agent", self:false, time:now, text:ans.text },
      ]);
      setAsstDraft(""); setAsstPrompt("");
    }
    // Resolve a [[wikilink]] clicked inside an ASSISTANT answer: a surface label
    // navigates the nav; a goal name / goal.<id> opens that goal; anything else is
    // an artifact open. Mirrors renderWiki's routing for the HTML-rendered path.
    const SURFACE_NAV = { "Home":"home", "Goals":"goals", "Work engine":"workengine", "Artifacts":"artifacts", "Bus":"bus" };
    function onAssistantRef(name){
      if(!name) return;
      if(SURFACE_NAV[name]){ onNav(SURFACE_NAV[name]); return; }
      if(name.indexOf("goal.")===0){ onNav("goals", name.slice(5)); return; }
      const g = (goalsShown||[]).find(x=>x.name===name);
      if(g){ onNav("goals", g.id); return; }
      openArtifact(name);
    }
    // Workspace nav (flow2 chrome): Home / Artifacts / Goals / Agents swap the
    // white stage.
    // onNav(key[, arg]): swap the stage. For "goals", an optional arg is a goal id
    // to deep-link to (TASK-157) — set it so the remounted GoalsView opens that
    // goal's detail; a plain Goals nav (no arg) clears it and lands on the portfolio.
    function onNav(key, arg){
      touchRecent("nav:"+key);
      if(key==="goals"){ setGoalsOpenId(typeof arg==="string"?arg:null); setGoalsEpoch(e=>e+1); }
      // a nav click resets the back-stack for that root (S1.3) — UNLESS it's a
      // deep-link into Goals (arg present), which is itself a navigation FROM the
      // current surface, so the back button should still return there.
      if(!(key==="goals" && typeof arg==="string")) setOrigin(null);
      else setOrigin(prev => (stageMode==="goals") ? prev : { mode: stageMode, goalId: null });
      setStageMode(key);
    }
    // STAGE_LABEL: the human label for a back-to-origin target. Falls back to a
    // title-cased mode for any surface not in the table.
    const STAGE_LABEL = { home:"Home", goals:"Goals", workengine:"Work engine", artifacts:"Artifacts", bus:"Bus", agents:"Agents", workflow:"Workflow", conversation:"Conversations", compose:"Composer", criteria:"Criteria", brief:"Inbox", consequence:"Review", link:"Link work" };
    function originLabel(){ return (origin && STAGE_LABEL[origin.mode]) || "Back"; }
    // goBack: return to the exact originating surface (S1.7). A goal deep-link
    // carries its goalId back so Goals re-opens that detail.
    function goBack(){
      const o = origin || { mode:"home" }; setOrigin(null);
      if(o.mode==="goals"){ setGoalsOpenId(o.goalId||null); setGoalsEpoch(e=>e+1); }
      setStageMode(o.mode);
    }

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
      for (const g of goalViews) {
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
    function expandConvo(key){
      touchRecent("conv:"+key); ensureConvo(key); setActiveConvo(key);
      setOrigin(prev => (stageMode==="artifact"||stageMode==="conversation") ? prev : { mode: stageMode, goalId: goalsOpenId });
      setStageMode("conversation"); backfill(key);
    }
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
    // An optional `note` (TASK-154) is the operator's feedback — posted as a plain
    // comment to the SAME companion topic BEFORE the marker, so the WHAT travels to
    // the agent (esp. on request-changes) and reads feedback→status-change in order.
    function setReview(name, state, note){
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
          const n = (note||"").trim();
          if(!n) return; // no feedback note — skip the comment
          // a note-publish failure must NOT block the verdict marker below — swallow
          // it locally so the status-change event still posts (codex Q4).
          return apiPublish(companionTopic(name),{ "$type":"chat.message", text:n }).catch(()=>{});
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
    // data-mode overlay (TASK-204): in snapshot mode, fill any view the LIVE bus
    // leaves empty with the seeded demo data — real data always wins, the snapshot
    // only ever fills a gap. In blank mode the live (possibly empty) arrays pass
    // through untouched, so a blank-slate workspace reads as genuinely empty.
    const goalsShown = useMemo(()=>(snapshotOn && goalViews.length===0) ? SNAPSHOT.goals : goalViews, [snapshotOn, goalViews]);
    const artsShown  = useMemo(()=>(snapshotOn && artItems.length===0)  ? SNAPSHOT.artifacts : artItems, [snapshotOn, artItems]);
    const agentsShown = useMemo(()=>(snapshotOn && agents.length===0)   ? SNAPSHOT.agents : agents, [snapshotOn, agents]);

    const reviewCount = artsShown.filter(a=>a.status==="review").length;
    const goalReviewCount = goalsShown.filter(g=>g.review==="review").length;
    const workingCount = agentsShown.filter(a=>a.state==="working").length;

    const ctx = {
      conversations:convList, activeConvo, stageMode, onOpenConvo:openConvo, onExpandConvo:expandConvo,
      messages, draft, setDraft, onSend:send, onArtifactRef:openArtifact,
      artifacts:artsShown, activeArtifact, onOpenArtifact:openArtifact,
      goals:goalsShown, agents:agentsShown, activity:homeActivity, self, onGoHome:goHome, home, onDM:startDM,
      hidden, onHide:hideConvo, onUnhide:unhideConvo,
      onNav, onSearch:()=>setPalette(true), reviewCount, goalReviewCount, workingCount,
    };

    // ⌘K search index — type-tagged rows (TASK-202 S2.2): Actions · Goals ·
    // Workflows · Runs · Artifacts · Surfaces (+ Agents / Channels). Built over
    // what's already loaded (with the data-mode snapshot overlay), so selecting a
    // result opens it via the existing handlers; the artifact `go` uses
    // openArtifact (which fetches by name) so it resolves even for an uncached name.
    const searchIndex = ()=>{
      const items=[];
      // ACTIONS — start something. "New doc/workflow/goal" land you on the surface
      // where that thing is created (the dash has no inline-create primitive yet).
      items.push({ key:"act:new-doc", type:"Action", label:"New doc", sub:"open Artifacts", kw:"new doc document artifact create action", go:()=>onNav("artifacts") });
      items.push({ key:"act:new-workflow", type:"Action", label:"New workflow", sub:"open the Work engine", kw:"new workflow run dispatch action", go:()=>onNav("workengine") });
      items.push({ key:"act:new-goal", type:"Action", label:"New goal", sub:"open Goals", kw:"new goal objective north star criteria action", go:()=>onNav("goals") });
      // GOALS — every goal as a jump target (deep-links into its detail).
      goalsShown.forEach(g=>items.push({ key:"goal:"+g.id, type:"Goal", label:g.name,
        sub:(g.northstar||"goal"), kw:(g.name+" "+(g.northstar||"")+" goal").toLowerCase(),
        go:()=>onNav("goals", g.id) }));
      // WORKFLOWS + RUNS — derived from the loaded artifact records. A workflow.<id>
      // record is a workflow; its run-state (status/done/total) makes it a Run row.
      // Absent backing data (no such records) → these simply contribute nothing.
      Object.keys(records).forEach(name=>{
        if(name.startsWith("workflow.")){
          const rec=records[name]||{}; const id=name.slice("workflow.".length);
          items.push({ key:"wf:"+name, type:"Workflow", label:(rec.title||id),
            sub:"workflow", kw:(name+" "+(rec.title||"")+" workflow").toLowerCase(),
            go:()=>onNav("workengine") });
          const st=rec.status||rec.state; const total=rec.total, done=rec.done;
          if(st || total!=null) items.push({ key:"run:"+name, type:"Run", label:(rec.title||id),
            sub:(st?st:"run")+(total!=null?(" · "+(done||0)+"/"+total):""),
            kw:(name+" "+(st||"")+" run").toLowerCase(), go:()=>onNav("workengine") });
        }
      });
      // ARTIFACTS
      artsShown.forEach(a=>items.push({ key:"art:"+a.name, type:"Artifact", label:a.name,
        sub:(a.updated?("updated "+a.updated+" ago"):"")+(a.status?(" · "+a.status):""),
        kw:(a.name+" "+a.status).toLowerCase(), go:()=>openArtifact(a.name) }));
      // SURFACES — the workspace nav hubs as jump targets (same as clicking nav).
      [["Home","home"],["Goals","goals"],["Work engine","workengine"],["Artifacts","artifacts"],["Bus","bus"],["Agents","agents"],["Workflow","workflow"]]
        .forEach(([label,key])=>items.push({ key:"nav:"+key, type:"Surface", label,
          sub:"workspace", kw:("go to surface "+label+" "+key).toLowerCase(), go:()=>onNav(key) }));
      // Agent rows keep a distinct "agent:<id>" key (a DM subject can also surface
      // as a Channel row, so reusing "conv:<subject>" would collide). startDM
      // records recency under the conversation; we ALSO touch the agent key here
      // so the Agent row itself accumulates recency and ranks up over time.
      agentsShown.forEach(a=>items.push({ key:"agent:"+a.id, type:"Agent", label:a.name, sub:a.meta,
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
          {sessionLost && (
            <div className="sx-buildnudge" role="alert">
              <span className="sx-buildnudge-dot" />
              <span className="sx-buildnudge-text">session expired — reload to reconnect</span>
              <button className="sx-buildnudge-x" title="Reload" aria-label="Reload" onClick={()=>location.reload()}>↻</button>
            </div>
          )}
          {staleBuild && !buildNudgeOff && (
            <div className="sx-buildnudge" role="status">
              <span className="sx-buildnudge-dot" />
              <span className="sx-buildnudge-text">new version available — refresh (⌘R)</span>
              <button className="sx-buildnudge-x" title="Dismiss until the next update" aria-label="Dismiss" onClick={()=>setBuildNudgeOff(true)}>×</button>
            </div>
          )}
          <div className="sx-topbar">
            <div className="sx-topbar-left">
            {/* back-to-origin (S1.6/1.7): present on a detail/overlay surface,
                labelled by — and returning to — the exact surface it opened from. */}
            {origin && (
              <button className="sx-back" title={"Back to "+originLabel()} onClick={goBack}>
                <span className="sx-back-ic">←</span><span className="sx-back-lbl">{originLabel()}</span>
              </button>
            )}
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
              ) : stageMode==="workengine" ? (
                <span className="sx-crumb-topic">Work engine</span>
              ) : stageMode==="bus" ? (
                <span className="sx-crumb-topic">Bus</span>
              ) : stageMode==="agents" ? (
                <span className="sx-crumb-topic">Agents</span>
              ) : stageMode==="workflow" ? (
                <span className="sx-crumb-topic">Workflow</span>
              ) : stageMode==="compose" ? (
                <React.Fragment><span className="sx-crumb-topic">Artifacts</span><span className="sx-crumb-sep">/</span><span className="sx-crumb-art">Composer</span></React.Fragment>
              ) : stageMode==="criteria" ? (
                <React.Fragment><span className="sx-crumb-topic">Artifacts</span><span className="sx-crumb-sep">/</span><span className="sx-crumb-art">Define criteria</span></React.Fragment>
              ) : stageMode==="brief" ? (
                <React.Fragment><span className="sx-crumb-topic">Inbox</span><span className="sx-crumb-sep">/</span><span className="sx-crumb-art">{(activeBrief&&activeBrief.title)||"Brief"}</span></React.Fragment>
              ) : stageMode==="consequence" ? (
                <React.Fragment><span className="sx-crumb-topic">Review</span><span className="sx-crumb-sep">/</span><span className="sx-crumb-art">Consequence</span></React.Fragment>
              ) : stageMode==="link" ? (
                <React.Fragment><span className="sx-crumb-topic">Goals</span><span className="sx-crumb-sep">/</span><span className="sx-crumb-art">Link work</span></React.Fragment>
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
              <div className="sx-page sx-page--doc"><ArtifactsSurface
                filed={filedArtifacts} drafts={drafts}
                onNewDoc={()=>newDoc("note")} onNewCharter={()=>newDoc("charter")} onImport={importFile}
                onOpenDraft={openDraft} onOpenFiled={openBrief}
                onSpawnWork={startDM ? (n)=>startDM(n) : undefined} /></div>
            </div>
          ) : stageMode==="compose" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><ComposerView
                draftId={composeId} drafts={drafts} onPatch={patchDraft}
                onFileArtifact={fileArtifact} onDefineCriteria={defineCriteria}
                onBack={()=>{ setStageMode("artifacts"); setOrigin(null); }} /></div>
            </div>
          ) : stageMode==="criteria" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><CriteriaProposal
                draft={drafts[composeId]} onCreateGoal={createGoal}
                onBack={()=>{ setStageMode("compose"); }} /></div>
            </div>
          ) : stageMode==="brief" ? (
            <div className="sx-canvas sx-canvas--review sx-conv-light">
              <BriefReader
                brief={activeBrief}
                collapsed={briefRailCollapsed} onToggleRail={()=>setBriefRailCollapsed(v=>!v)}
                onSubmitVerdict={submitVerdict}
                onReply={(cid,text)=>{ if(activeBrief) apiPublish(companionTopic(activeBrief.name),{ "$type":"chat.message", text }).catch(()=>{}); }}
                onBack={goBack} />
            </div>
          ) : stageMode==="consequence" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><ReviewConsequence
                verdict={verdict} transition={briefTransition}
                onBack={goBack}
                onSeeGoal={(verdict&&verdict.brief&&verdict.brief.goal)?(()=>onNav("goals")):undefined} /></div>
            </div>
          ) : stageMode==="link" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><LinkWorkstream
                criterion={linkCriterion}
                candidates={linkCandidates}
                onToggleLink={(id,linked)=>{ /* relates write owned by the goals convention; reflected on reload */ }}
                onBuildWorkflow={()=>onNav("workflow")}
                onBack={goBack} /></div>
            </div>
          ) : stageMode==="goals" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><GoalsView key={goalsEpoch} goals={goalsShown} initialGoalId={goalsOpenId} self={self} onOpenArtifact={openArtifact} onSetReview={setReview} onLinkCriterion={openLink} onDM={startDM} renderWiki={renderWiki} /></div>
            </div>
          ) : stageMode==="workengine" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc">
                <WorkEngineView goals={goalViews} artifacts={artItems} onOpenArtifact={openArtifact} renderWiki={renderWiki} />
              </div>
            </div>
          ) : stageMode==="bus" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc">
                <div className="fx-scroll"><div className="fx-col sx-conv-light">
                  <h1 className="fx-h1 fx-in">Bus</h1>
                  <p className="fx-psub fx-in" style={{animationDelay:".03s"}}>The live message bus — topics, conversations, and the traffic flowing across them.</p>
                  <div className="fx-stub fx-in" style={{animationDelay:".06s"}}>
                    <span className="fx-stub-ic">⇆</span>
                    <div>
                      <div className="fx-stub-title">Coming soon</div>
                      <div className="fx-stub-sub">The Bus surface isn't built yet. The shell is here so the section is navigable; the topic + traffic views land in a later ticket.</div>
                    </div>
                  </div>
                </div></div>
              </div>
            </div>
          ) : stageMode==="agents" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><AgentsView agents={agentsShown} onDM={startDM} /></div>
            </div>
          ) : stageMode==="workflow" ? (
            <div className="sx-canvas sx-canvas--list">
              <div className="sx-page sx-page--doc"><WorkflowView onDM={startDM} /></div>
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
          messages={violet ? assistantMessages : asstLocalMsgs} self={self}
          draft={asstDraft} setDraft={setAsstDraft}
          onSend={violet ? sendToAssistant : sendLocalAssistant}
          onArtifactRef={violet ? openArtifact : onAssistantRef}
          artifactNames={(window.SxAssistant ? window.SxAssistant.knownLinks({ goals:goalsShown, artifacts:artsShown }) : artifacts.map(a=>a.Name))}
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
          {/* Data mode (TASK-204, S1.9/S21.1): Snapshot overlays seeded demo data on
              any view the live bus leaves empty; Blank slate shows the workspace
              as-is. Persisted under sextant.synth.datamode.v1. */}
          <TweakSection label="Data" />
          <TweakRadio label="State" value={dataMode}
            options={[{value:"snapshot",label:"Snapshot"},{value:"blank",label:"Blank slate"}]}
            onChange={v=>setDataMode(v)} />
          {dataMode==="blank" && (
            <TweakButton label="Reset to empty" secondary onClick={()=>{
              // clear the locally-held demo/session state so a blank slate is truly
              // empty (the live bus data is read-only here and isn't touched).
              setAsstLocalMsgs([]); setActivity([]);
              try{ localStorage.removeItem(RECENTS_KEY); }catch(_){}
              setRecents({});
            }} />
          )}
        </TweaksPanel>
      </div>
    );
  }

  ReactDOM.createRoot(document.getElementById("root")).render(<App />);
})();
