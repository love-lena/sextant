(function() {
  const { useState, useRef, useEffect, useMemo, useCallback } = React;
  function shade(hex, amt) {
    const n = parseInt(hex.slice(1), 16);
    let r = n >> 16 & 255, g = n >> 8 & 255, b = n & 255;
    const f = 1 + amt;
    r = Math.round(r * f);
    g = Math.round(g * f);
    b = Math.round(b * f);
    const cl = (v) => Math.max(0, Math.min(255, v));
    return "#" + [cl(r), cl(g), cl(b)].map((v) => v.toString(16).padStart(2, "0")).join("");
  }
  function hexA(hex, a) {
    const n = parseInt(hex.slice(1), 16);
    return `rgba(${n >> 16 & 255},${n >> 8 & 255},${n & 255},${a})`;
  }
  const TWEAK_DEFAULTS = (
    /*EDITMODE-BEGIN*/
    {
      "accent": "#4f9d68",
      "sidePos": "left",
      "sideTone": "paper",
      "sideNav": "sections",
      "livePulse": true
    }
  );
  const TOKEN = new URLSearchParams(location.search).get("token") || "";
  const AUTH = { "Authorization": "Bearer " + TOKEN };
  async function apiGet(path) {
    const r = await fetch(path, { headers: AUTH });
    if (!r.ok) throw new Error(path + " -> " + r.status);
    return r.json();
  }
  function apiPost(path, body) {
    return fetch(path, {
      method: "POST",
      headers: Object.assign({ "Content-Type": "application/json" }, AUTH),
      body: JSON.stringify(body)
    }).then((r) => {
      if (!r.ok) throw new Error(path + " -> " + r.status);
    });
  }
  function apiPublish(subject, record) {
    return apiPost("/api/publish", { subject, record });
  }
  function apiReview(name, state) {
    return apiPost("/api/artifacts/" + encodeURIComponent(name) + "/review", { state });
  }
  const REVIEW_STATES = ["review", "approved", "changes", "draft", "rejected", "archived"];
  const REVIEW_VERB = { approved: "approved", changes: "requested changes on", rejected: "rejected", archived: "archived", review: "reopened", draft: "reset to draft" };
  function companionTopic(name) {
    return "msg.topic.artifact." + name;
  }
  function relMs(ms) {
    if (!ms) return "";
    const s = Math.max(0, (Date.now() - ms) / 1e3);
    if (s < 60) return Math.floor(s) + "s";
    if (s < 3600) return Math.floor(s / 60) + "m";
    if (s < 86400) return Math.floor(s / 3600) + "h";
    return Math.floor(s / 86400) + "d";
  }
  function relTime(iso) {
    const t = Date.parse(iso || "");
    return isNaN(t) ? "" : relMs(t);
  }
  function ulidTime(id) {
    if (!id || id.length < 10) return 0;
    const A = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
    let t = 0;
    for (let i = 0; i < 10; i++) {
      const v = A.indexOf((id[i] || "").toUpperCase());
      if (v < 0) return 0;
      t = t * 32 + v;
    }
    return t;
  }
  function frameTime(f) {
    const t = Date.parse(f && f.createdAt || "");
    return isNaN(t) ? ulidTime(f && f.id) : t;
  }
  function topicLabel(subject) {
    if (subject.startsWith("msg.topic.")) return subject.slice(10);
    if (subject.startsWith("msg.client.")) return subject.slice(11);
    return subject;
  }
  function frameText(rec) {
    if (!rec) return "\xB7";
    if (typeof rec.text === "string") return rec.text;
    if (rec.title) return rec.title;
    return rec.$type || "\xB7";
  }
  const GOALS = [
    { label: "Tasks merged this sprint", value: 14, target: 20, display: "14 / 20", note: "stub \u2014 no goals primitive yet" },
    { label: "CI pipeline green", value: 97, target: 95, display: "97%", met: true, note: "stub \u2014 no goals primitive yet" },
    { label: "Test coverage", value: 81, target: 85, display: "81%", note: "stub \u2014 no goals primitive yet" }
  ];
  function App() {
    const [t, setTweak] = useTweaks(TWEAK_DEFAULTS);
    const [self, setSelf] = useState({ id: "", display_name: "", principal: "" });
    const [clients, setClients] = useState([]);
    const [artifacts, setArtifacts] = useState([]);
    const [records, setRecords] = useState({});
    const [home, setHome] = useState(null);
    const [convos, setConvos] = useState({});
    const [activity, setActivity] = useState([]);
    const [activeArtifact, setActiveArtifact] = useState("");
    const [artRecord, setArtRecord] = useState(null);
    const [activeConvo, setActiveConvo] = useState("");
    const [stageMode, setStageMode] = useState("home");
    const [draft, setDraft] = useState("");
    const convBodyRef = useRef(null);
    const discBodyRef = useRef(null);
    const [hidden, setHidden] = useState(() => {
      try {
        return new Set(JSON.parse(localStorage.getItem("sx-hidden-convos") || "[]"));
      } catch (_) {
        return /* @__PURE__ */ new Set();
      }
    });
    const [dark, setDark] = useState(() => {
      try {
        return localStorage.getItem("sx-dark") === "1";
      } catch (_) {
        return false;
      }
    });
    const nameOf = useCallback((id) => {
      const c = clients.find((c2) => c2.ID === id);
      return c ? c.DisplayName : (id || "").slice(0, 8);
    }, [clients]);
    const kindOf = useCallback((id) => {
      const c = clients.find((c2) => c2.ID === id);
      return c ? c.Kind : "agent";
    }, [clients]);
    useEffect(() => {
      apiGet("/api/self").then(setSelf).catch(() => {
      });
      apiGet("/api/clients").then((cs) => setClients(Array.isArray(cs) ? cs : [])).catch(() => {
      });
      apiGet("/api/artifacts").then((as) => setArtifacts(Array.isArray(as) ? as : [])).catch(() => {
      });
      apiGet("/api/artifacts/home").then((a) => setHome(a && a.Record || null)).catch(() => {
      });
      apiGet("/api/subjects").then((subs) => {
        if (!Array.isArray(subs)) return;
        setConvos((prev) => {
          const next = { ...prev };
          for (const s of subs) {
            if (s && s.subject && !next[s.subject]) next[s.subject] = { msgs: [], last: 0, lastText: "" };
          }
          return next;
        });
      }).catch(() => {
      });
    }, []);
    useEffect(() => {
      const r = document.getElementById("app");
      if (r) r.classList.toggle("dark", dark);
      try {
        localStorage.setItem("sx-dark", dark ? "1" : "0");
      } catch (_) {
      }
    }, [dark]);
    useEffect(() => {
      let cancelled = false;
      Promise.all(artifacts.map((a) => apiGet("/api/artifacts/" + encodeURIComponent(a.Name)).then((r) => [a.Name, r && r.Record || null]).catch(() => [a.Name, null]))).then((pairs) => {
        if (!cancelled) setRecords(Object.fromEntries(pairs));
      });
      return () => {
        cancelled = true;
      };
    }, [artifacts]);
    useEffect(() => {
      const es = new EventSource("/api/stream?subject=" + encodeURIComponent("msg.>") + "&token=" + encodeURIComponent(TOKEN));
      es.onmessage = (m) => {
        let ev;
        try {
          ev = JSON.parse(m.data);
        } catch (_) {
          return;
        }
        const subj = ev.subject, f = ev.frame;
        if (!subj || !f) return;
        const text = frameText(f.record);
        const at = frameTime(f) || Date.now();
        const msg = { id: f.id, author: f.author, text, ts: at };
        setConvos((prev) => {
          const cur = prev[subj] || { msgs: [] };
          if (cur.msgs.some((x) => x.id === msg.id)) return prev;
          return { ...prev, [subj]: { ...cur, msgs: [...cur.msgs, msg].slice(-200), last: Math.max(cur.last || 0, at), lastText: text } };
        });
        setActivity((prev) => [{ subj, author: f.author, text, ts: at }, ...prev].slice(0, 40));
      };
      es.onerror = () => {
      };
      return () => es.close();
    }, [TOKEN]);
    const seededRef = useRef(false);
    useEffect(() => {
      if (seededRef.current) return;
      seededRef.current = true;
      let cancelled = false;
      const latestTime = (subj) => {
        const PAGE = 200, MAX = 25;
        let best = 0;
        const page = (since, guard) => apiGet("/api/messages?subject=" + encodeURIComponent(subj) + "&since=" + since + "&limit=" + PAGE).then((res) => {
          const frames = res && res.messages || [];
          for (const f of frames) {
            const t2 = frameTime(f);
            if (t2 > best) best = t2;
          }
          const next = res && res.next_cursor;
          if (guard > 1 && frames.length >= PAGE && next && next > since) return page(next, guard - 1);
          return best;
        }).catch(() => best);
        return page(0, MAX);
      };
      apiGet("/api/subjects").then((subs) => {
        if (cancelled || !Array.isArray(subs)) return;
        return Promise.all(subs.map((s) => {
          const subj = s && s.subject;
          if (!subj) return null;
          return latestTime(subj).then((t2) => ({ subj, t: t2 }));
        }).filter(Boolean));
      }).then((pairs) => {
        if (cancelled || !pairs) return;
        setConvos((prev) => {
          const next = { ...prev };
          for (const p of pairs) {
            if (!p || !p.t) continue;
            const cur = next[p.subj] || { msgs: [], last: 0, lastText: "" };
            if (p.t > (cur.last || 0)) next[p.subj] = { ...cur, last: p.t };
          }
          return next;
        });
      }).catch(() => {
      });
      return () => {
        cancelled = true;
      };
    }, [TOKEN]);
    useEffect(() => {
      const sig = (a) => a.map((x) => x.Name + ":" + x.Revision).join(",");
      const id = setInterval(() => {
        apiGet("/api/artifacts").then((as) => {
          if (!Array.isArray(as)) return;
          setArtifacts((prev) => sig(prev) === sig(as) ? prev : as);
        }).catch(() => {
        });
        apiGet("/api/subjects").then((subs) => {
          if (!Array.isArray(subs)) return;
          setConvos((prev) => {
            let changed = false;
            const next = { ...prev };
            for (const s of subs) {
              if (s && s.subject && !next[s.subject]) {
                next[s.subject] = { msgs: [], last: 0, lastText: "" };
                changed = true;
              }
            }
            return changed ? next : prev;
          });
        }).catch(() => {
        });
        apiGet("/api/artifacts/home").then((a) => {
          const rec = a && a.Record || null;
          setHome((prev) => JSON.stringify(prev) === JSON.stringify(rec) ? prev : rec);
        }).catch(() => {
        });
        apiGet("/api/clients").then((cs) => {
          if (!Array.isArray(cs)) return;
          setClients((prev) => JSON.stringify(prev) === JSON.stringify(cs) ? prev : cs);
        }).catch(() => {
        });
      }, 4e3);
      return () => clearInterval(id);
    }, []);
    useEffect(() => {
      const r = document.getElementById("app");
      r.style.setProperty("--brand", t.accent);
      r.style.setProperty("--brand-strong", shade(t.accent, -0.16));
      r.style.setProperty("--brand-soft", hexA(t.accent, 0.16));
      r.classList.toggle("tone-paper", t.sideTone === "paper");
      r.classList.toggle("side-right", t.sidePos === "right");
      r.classList.toggle("no-pulse", !t.livePulse);
    }, [t.accent, t.sideTone, t.sidePos, t.livePulse]);
    const STATUS_STATES = ["idle", "working", "waiting-for-human", "waiting-for-agent", "blocked", "done"];
    const agents = useMemo(() => clients.filter((c) => c.Kind !== "client" && c.Kind !== "human").map((c) => {
      const sr = records["status." + c.ID];
      const st = sr && sr.state;
      const known = STATUS_STATES.indexOf(st) >= 0;
      const headline = sr && sr.headline || "";
      return {
        id: c.ID,
        name: c.DisplayName,
        state: !c.Online ? "offline" : known ? st : "idle",
        headline,
        meta: headline || (c.Kind || "agent") + (c.Online ? " \xB7 online" : " \xB7 offline")
      };
    }), [clients, records]);
    const statusOf = useCallback((name) => {
      const rec = records[name];
      const st = rec && rec.review && rec.review.state;
      return REVIEW_STATES.indexOf(st) >= 0 ? st : "draft";
    }, [records]);
    const artItems = useMemo(() => artifacts.filter((a) => a.Name !== "home" && !a.Name.startsWith("status.")).map((a) => ({
      name: a.Name,
      version: a.Revision,
      status: statusOf(a.Name),
      topic: "",
      type: "markdown",
      id: a.Name,
      author: { name: "", kind: "agent" },
      updated: relTime(a.Updated)
    })), [artifacts, statusOf]);
    const convList = useMemo(() => Object.entries(convos).sort((a, b) => (b[1].last || 0) - (a[1].last || 0)).map(([subj, c]) => {
      let type = "topic", name = topicLabel(subj);
      if (subj.startsWith("msg.client.")) {
        type = "inbox";
        name = nameOf(subj.slice(11)) + " \xB7 inbox";
      } else if (subj.startsWith("msg.topic.dm.")) {
        const ids = subj.slice(13).split(".");
        const other = ids.find((x) => x !== self.id) || ids[0] || "";
        type = "dm";
        name = nameOf(other);
      }
      return { key: subj, type, name, snippet: c.lastText || "", time: relMs(c.last), unread: 0, participants: 0 };
    }), [convos, nameOf, self.id]);
    const messages = useMemo(() => {
      const c = convos[activeConvo];
      if (!c) return [];
      return c.msgs.map((m, i) => ({
        id: m.id || i,
        kind: "msg",
        author: nameOf(m.author),
        role: kindOf(m.author) === "client" || kindOf(m.author) === "human" ? "human" : "agent",
        self: m.author === self.id,
        time: relMs(m.ts),
        text: m.text
      }));
    }, [convos, activeConvo, nameOf, kindOf, self.id]);
    const discussion = useMemo(() => {
      const c = activeArtifact ? convos[companionTopic(activeArtifact)] : null;
      if (!c) return [];
      return c.msgs.map((m, i) => ({
        id: m.id || i,
        kind: "msg",
        author: nameOf(m.author),
        role: kindOf(m.author) === "client" ? "human" : "agent",
        self: m.author === self.id,
        time: relMs(m.ts),
        text: m.text
      }));
    }, [convos, activeArtifact, nameOf, kindOf, self.id]);
    const homeActivity = useMemo(() => activity.map((a) => ({
      who: nameOf(a.author),
      text: a.text,
      time: relMs(a.ts)
    })), [activity, nameOf]);
    const artifact = artItems.find((a) => a.name === activeArtifact) || artItems[0] || { name: "", version: 0, status: "review", topic: "", author: { name: "", kind: "agent" }, updated: "" };
    const status = artifact.status;
    const reviewRev = artRecord && artRecord.review && artRecord.review.rev || 0;
    const convo = convList.find((c) => c.key === activeConvo) || convList[0] || { type: "topic", name: "", participants: 0 };
    useEffect(() => {
      if (stageMode !== "conversation") return;
      const el = convBodyRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    }, [messages, stageMode, activeConvo]);
    useEffect(() => {
      if (stageMode !== "artifact") return;
      const el = discBodyRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    }, [discussion, stageMode, activeArtifact]);
    function openArtifact(name) {
      setActiveArtifact(name);
      setStageMode("artifact");
      const subj = companionTopic(name);
      ensureConvo(subj);
      backfill(subj);
      const cached = records[name];
      setArtRecord(cached !== void 0 ? cached : null);
      apiGet("/api/artifacts/" + encodeURIComponent(name)).then((a) => {
        const rec = a && a.Record || null;
        setArtRecord(rec);
        setRecords((prev) => ({ ...prev, [name]: rec }));
      }).catch(() => {
      });
    }
    function goHome() {
      setStageMode("home");
    }
    function backfill(subj) {
      const PAGE = 200, MAX_PAGES = 25;
      const acc = [];
      const page = (since, guard) => apiGet("/api/messages?subject=" + encodeURIComponent(subj) + "&since=" + since + "&limit=" + PAGE).then((res) => {
        const frames = res && res.messages || [];
        acc.push(...frames);
        const next = res && res.next_cursor;
        if (guard > 1 && frames.length >= PAGE && next && next > since) return page(next, guard - 1);
      });
      page(0, MAX_PAGES).then(() => {
        if (!acc.length) return;
        const hist = acc.map((f) => ({ id: f.id, author: f.author, text: frameText(f.record), ts: 0 }));
        setConvos((prev) => {
          const cur = prev[subj] || { msgs: [] };
          const seen = new Set(cur.msgs.map((m) => m.id));
          const merged = [...hist.filter((m) => !seen.has(m.id)), ...cur.msgs];
          return { ...prev, [subj]: { ...cur, msgs: merged.slice(-200), lastText: cur.lastText || (hist.length ? hist[hist.length - 1].text : "") } };
        });
      }).catch(() => {
      });
    }
    function ensureConvo(subj) {
      setConvos((prev) => prev[subj] ? prev : { ...prev, [subj]: { msgs: [], last: Date.now(), lastText: "" } });
    }
    function openConvo(key) {
      ensureConvo(key);
      setActiveConvo(key);
      backfill(key);
    }
    function expandConvo(key) {
      ensureConvo(key);
      setActiveConvo(key);
      setStageMode("conversation");
      backfill(key);
    }
    function send() {
      if (!draft.trim() || !activeConvo) return;
      const text = draft.trim();
      apiPublish(activeConvo, { "$type": "chat.message", text }).then(() => setDraft("")).catch(() => {
      });
    }
    function sendDiscussion() {
      if (!draft.trim() || !activeArtifact) return;
      const text = draft.trim();
      apiPublish(companionTopic(activeArtifact), { "$type": "chat.message", text }).then(() => setDraft("")).catch(() => {
      });
    }
    function setReview(name, state) {
      apiReview(name, state).then(() => apiGet("/api/artifacts/" + encodeURIComponent(name))).then((a) => {
        const rec = a && a.Record || null;
        setRecords((prev) => ({ ...prev, [name]: rec }));
        if (name === activeArtifact) setArtRecord(rec);
      }).then(() => apiPublish(companionTopic(name), { "$type": "chat.message", text: (REVIEW_VERB[state] || state) + " " + name })).catch(() => {
      });
    }
    function dmSubject(a, b) {
      return "msg.topic.dm." + [a, b].sort().join(".");
    }
    function startDM(otherId) {
      if (!self.id || !otherId) return;
      expandConvo(dmSubject(self.id, otherId));
    }
    function persistHidden(set) {
      try {
        localStorage.setItem("sx-hidden-convos", JSON.stringify([...set]));
      } catch (_) {
      }
    }
    function hideConvo(key) {
      setHidden((prev) => {
        const n = new Set(prev);
        n.add(key);
        persistHidden(n);
        return n;
      });
    }
    function unhideConvo(key) {
      setHidden((prev) => {
        const n = new Set(prev);
        n.delete(key);
        persistHidden(n);
        return n;
      });
    }
    const ctx = {
      conversations: convList,
      activeConvo,
      stageMode,
      onOpenConvo: openConvo,
      onExpandConvo: expandConvo,
      messages,
      draft,
      setDraft,
      onSend: send,
      onArtifactRef: openArtifact,
      artifacts: artItems,
      activeArtifact,
      onOpenArtifact: openArtifact,
      goals: GOALS,
      agents,
      activity: homeActivity,
      self,
      onGoHome: goHome,
      home,
      onDM: startDM,
      hidden,
      onHide: hideConvo,
      onUnhide: unhideConvo
    };
    const hasAuthor = artifact.author && artifact.author.name;
    return /* @__PURE__ */ React.createElement("div", { className: "sx-app" }, /* @__PURE__ */ React.createElement("div", { style: { display: "contents" } }, /* @__PURE__ */ React.createElement(Sidebar, { ctx, busName: self.display_name || "bus", navMode: t.sideNav })), /* @__PURE__ */ React.createElement("main", { className: "sx-stage" }, /* @__PURE__ */ React.createElement("div", { className: "sx-topbar" }, /* @__PURE__ */ React.createElement("div", { className: "sx-crumb" }, stageMode === "home" ? /* @__PURE__ */ React.createElement(React.Fragment, null, /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-topic" }, "Home"), /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-sep" }, "/"), /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-art" }, self.display_name ? "you are " + self.display_name : "live bus")) : stageMode === "artifact" ? /* @__PURE__ */ React.createElement(React.Fragment, null, /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-topic" }, "Artifact"), /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-sep" }, "/"), /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-art" }, artifact.name)) : /* @__PURE__ */ React.createElement(React.Fragment, null, /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-topic" }, "Conversations"), /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-sep" }, "/"), /* @__PURE__ */ React.createElement("span", { className: "sx-crumb-art" }, convo.type === "topic" ? "# " : "@ ", convo.name))), /* @__PURE__ */ React.createElement("div", { className: "sx-stage-tools" }, /* @__PURE__ */ React.createElement("span", { className: "sx-live" }, /* @__PURE__ */ React.createElement("span", { className: "sx-live-dot" }), "live"), /* @__PURE__ */ React.createElement("button", { className: "sx-icon-btn", title: dark ? "Light mode" : "Dark mode", onClick: () => setDark((d) => !d) }, dark ? "\u2600" : "\u263E"), /* @__PURE__ */ React.createElement("button", { className: "sx-icon-btn", title: "Fullscreen" }, "\u2922"))), stageMode === "home" ? /* @__PURE__ */ React.createElement("div", { className: "sx-canvas" }, /* @__PURE__ */ React.createElement("div", { className: "sx-page sx-page--doc sx-page--home" }, /* @__PURE__ */ React.createElement(HomePage, { ctx }))) : stageMode === "artifact" ? /* @__PURE__ */ React.createElement(React.Fragment, null, /* @__PURE__ */ React.createElement("div", { className: "sx-arthead" }, /* @__PURE__ */ React.createElement("div", { className: "sx-arthead-l" }, /* @__PURE__ */ React.createElement("div", { className: "sx-arthead-title" }, artifact.name), /* @__PURE__ */ React.createElement("div", { className: "sx-arthead-meta" }, artifact.updated && /* @__PURE__ */ React.createElement("span", { className: "sx-arthead-time" }, "updated ", artifact.updated, " ago"), status === "approved" && reviewRev > 0 && /* @__PURE__ */ React.createElement("span", { className: "sx-arthead-time" }, "\xB7 approved at v", reviewRev), /* @__PURE__ */ React.createElement("span", { className: "sx-arthead-v mono", style: { opacity: 0.5 } }, "\xB7 rev ", artifact.version))), /* @__PURE__ */ React.createElement("div", { className: "sx-arthead-r" }, /* @__PURE__ */ React.createElement(StatusPill, { status, big: true }), status === "archived" || status === "rejected" ? /* @__PURE__ */ React.createElement("button", { className: "sx-sbtn sx-sbtn-req", onClick: () => setReview(artifact.name, "review") }, "Reopen") : /* @__PURE__ */ React.createElement(React.Fragment, null, status !== "approved" && /* @__PURE__ */ React.createElement("button", { className: "sx-sbtn sx-sbtn-approve", onClick: () => setReview(artifact.name, "approved") }, "\u2713 Approve"), status !== "changes" && /* @__PURE__ */ React.createElement("button", { className: "sx-sbtn sx-sbtn-req", onClick: () => setReview(artifact.name, "changes") }, "Request changes"), /* @__PURE__ */ React.createElement("button", { className: "sx-sbtn sx-sbtn-req", onClick: () => setReview(artifact.name, "archived") }, "Archive"), /* @__PURE__ */ React.createElement("button", { className: "sx-sbtn sx-sbtn-req", onClick: () => setReview(artifact.name, "rejected") }, "Reject")), /* @__PURE__ */ React.createElement("button", { className: "sx-sbtn sx-sbtn-req", onClick: () => expandConvo(companionTopic(artifact.name)) }, "Discussion \u2197"))), /* @__PURE__ */ React.createElement("div", { className: "sx-canvas sx-canvas--artifact" }, /* @__PURE__ */ React.createElement("div", { className: "sx-page sx-page--doc" }, /* @__PURE__ */ React.createElement(MarkdownArtifact, { record: artRecord, name: artifact.name, revision: artifact.version })), /* @__PURE__ */ React.createElement("div", { className: "sx-artdisc sx-conv-light" }, /* @__PURE__ */ React.createElement("div", { className: "sx-artdisc-head" }, /* @__PURE__ */ React.createElement("span", { className: "sx-artdisc-title" }, "Discussion"), /* @__PURE__ */ React.createElement("span", { className: "sx-artdisc-sub" }, companionTopic(artifact.name))), /* @__PURE__ */ React.createElement("div", { className: "sx-artdisc-body", ref: discBodyRef }, discussion.length ? /* @__PURE__ */ React.createElement(MessageList, { messages: discussion, onArtifactRef: openArtifact }) : /* @__PURE__ */ React.createElement("div", { className: "sx-artdisc-empty" }, "No discussion yet \u2014 start the thread below.")), /* @__PURE__ */ React.createElement(Composer, { draft, setDraft, onSend: sendDiscussion, placeholder: "Discuss " + artifact.name })))) : /* @__PURE__ */ React.createElement("div", { className: "sx-canvas" }, /* @__PURE__ */ React.createElement("div", { className: "sx-page sx-page--doc sx-conv-light" }, /* @__PURE__ */ React.createElement("div", { className: "sx-convstage" }, /* @__PURE__ */ React.createElement("div", { className: "sx-convstage-head" }, /* @__PURE__ */ React.createElement("span", { className: "sx-convstage-title" }, convo.type === "topic" ? "# " : "@ ", convo.name), /* @__PURE__ */ React.createElement("span", { className: "sx-convstage-meta" }, "live on the bus")), /* @__PURE__ */ React.createElement("div", { className: "sx-convstage-body", ref: convBodyRef }, /* @__PURE__ */ React.createElement(MessageList, { messages, onArtifactRef: openArtifact, artifactNames: artifacts.map((a) => a.Name) })), /* @__PURE__ */ React.createElement(Composer, { draft, setDraft, onSend: send, placeholder: "Message " + (convo.type === "topic" ? "#" : "@") + convo.name }))))), /* @__PURE__ */ React.createElement(TweaksPanel, { title: "Tweaks" }, /* @__PURE__ */ React.createElement(TweakSection, { label: "Accent" }), /* @__PURE__ */ React.createElement(
      TweakColor,
      {
        label: "Brand signal",
        value: t.accent,
        options: ["#4f9d68", "#3a93d2", "#7c6df0", "#1a1c1f"],
        onChange: (v) => setTweak("accent", v)
      }
    ), /* @__PURE__ */ React.createElement(TweakSection, { label: "Sidebar" }), /* @__PURE__ */ React.createElement(TweakRadio, { label: "Position", value: t.sidePos, options: ["left", "right"], onChange: (v) => setTweak("sidePos", v) }), /* @__PURE__ */ React.createElement(TweakRadio, { label: "Tone", value: t.sideTone, options: ["charcoal", "paper"], onChange: (v) => setTweak("sideTone", v) }), /* @__PURE__ */ React.createElement(TweakRadio, { label: "Navigation", value: t.sideNav, options: ["sections", "tabs"], onChange: (v) => setTweak("sideNav", v) }), /* @__PURE__ */ React.createElement(TweakSection, { label: "Motion" }), /* @__PURE__ */ React.createElement(TweakToggle, { label: "Live pulse", value: t.livePulse, onChange: (v) => setTweak("livePulse", v) })));
  }
  ReactDOM.createRoot(document.getElementById("root")).render(/* @__PURE__ */ React.createElement(App, null));
})();
