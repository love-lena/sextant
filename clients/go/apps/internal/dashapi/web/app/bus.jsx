/* bus.jsx — the Bus inspector (TASK-195 JetStream + TASK-196 Key-Value).

   A self-contained surface for the dash's "Bus" nav page: a NATS JetStream +
   Key-Value explorer the operator (a developer) uses to see what's actually on
   the live bus. It reads over the page's OWN bus connection (window.SX, the
   ADR-0044 browser-direct co-equal client) — there is NO new Go relay endpoint.

   --- the permission reality (ADR-0019 / bus/auth.go) ---
   The bus is an allow-list account: EVERY credential (client, operator, and the
   browser SESSION this page holds) is scoped to publish ONLY under its own
   `sx.api.<id>.>` Wire-API prefix. `$JS.API.>` and `$KV.>` — the NATS JetStream
   and KV management subjects — are DENIED to every client; only the bus's own
   in-process connection wields them. Empirically, a browser-session credential
   issuing `$JS.API.STREAM.NAMES` over the WS listener gets PERMISSIONS_VIOLATION,
   and there is no stream/consumer/bucket inspection op in the Wire API.

   So a literal browser-direct `jsm.streams.list()` / `kvm` walk is IMPOSSIBLE
   with today's bus, and faking it would be a lie about the live system. Instead
   this inspector is built against what the browser session CAN reach — the Wire
   API — and is honest about the rest:

   • JetStream: the retention log is one stream carrying every `msg.>` subject.
     We enumerate the live SUBJECTS (the same msg.> discovery app.jsx already does)
     and treat each as an inspectable channel: message count + last activity via
     message.read paging, and a FULLY FUNCTIONAL message browser (subject filter,
     newest/oldest order, "showing X–Y of Z" pagination, a row that expands to its
     record viewable as JSON / Raw / Hex). Stream-level config chips
     (storage/retention/replicas) and the Consumers tab are surfaced as a labelled
     "needs a bus-side inspection op" notice — the browser session cannot read them.

   • Key-Value: the `artifacts` bucket IS a real KV bucket reachable via the
     artifact Wire API (artifact.list / artifact.get). We present it as a KV bucket
     with a filterable key list (key + current revision), and the selected key's
     current value as JSON / Raw / Hex. Per-key REVISION HISTORY and DELETED-key
     TOMBSTONES need the `$KV` history API the browser cannot reach — shown as a
     labelled notice, not invented.

   Final nav wiring (the "Bus" sidebar row) attaches to the new app shell being
   built in parallel (TASK-220); this file ships the surface + a minimal #bus hash
   route so it is reachable + testable on its own without touching app.jsx /
   sidebar.jsx. See BusInspector (the surface) and the self-mount at the bottom. */
(function () {
  const { useState, useEffect, useMemo, useCallback, useRef } = React;

  // SX is the shared bus-backed data layer app.jsx assigns (get/publish/subscribe/
  // ready). get(path) resolves over the page's own bus Client — the same browser-
  // direct connection the rest of the dash uses (no Go relay).
  const SX = () => window.SX || null;

  // ---- helpers ----
  function fmtBytes(n) {
    if (!n && n !== 0) return "—";
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    if (n < 1024 * 1024 * 1024) return (n / (1024 * 1024)).toFixed(1) + " MB";
    return (n / (1024 * 1024 * 1024)).toFixed(2) + " GB";
  }
  function fmtNum(n) { return (n || 0).toLocaleString(); }
  function relMs(ms) {
    if (!ms) return "";
    const s = Math.max(0, (Date.now() - ms) / 1000);
    if (s < 60) return Math.floor(s) + "s ago";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
  }
  function absTime(ms) {
    if (!ms) return "—";
    try { return new Date(ms).toLocaleString(); } catch (_) { return "—"; }
  }
  // ULID → embedded millisecond timestamp (first 10 Crockford-base32 chars), the
  // same decode app.jsx uses so a frame's real time is available without a separate
  // timestamp field.
  function ulidTime(id) {
    if (!id || id.length < 10) return 0;
    const A = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
    let t = 0;
    for (let i = 0; i < 10; i++) { const v = A.indexOf((id[i] || "").toUpperCase()); if (v < 0) return 0; t = t * 32 + v; }
    return t;
  }
  function frameTime(f) { const t = Date.parse((f && f.createdAt) || ""); return isNaN(t) ? ulidTime(f && f.id) : t; }
  // The on-the-wire size of a frame's record (the payload), in bytes.
  function recordSize(rec) {
    try { return new TextEncoder().encode(JSON.stringify(rec == null ? "" : rec)).length; } catch (_) { return 0; }
  }
  // A frame's "headers" — the bus-stamped envelope fields (the Wire API does not
  // expose raw NATS message headers to a client, so these are the frame's own
  // metadata: the closest honest analogue to a JetStream message's headers).
  function frameHeaders(f) {
    const h = {};
    if (f.id) h["Nats-Msg-Id"] = f.id;
    if (f.author) h["Sextant-Author"] = f.author;
    if (f.kind) h["Sextant-Kind"] = f.kind;
    if (f.epoch != null) h["Sextant-Epoch"] = String(f.epoch);
    if (f.createdAt) h["Sextant-Created"] = f.createdAt;
    if (f.revision != null) h["Sextant-Revision"] = String(f.revision);
    return h;
  }
  function topicLabel(subject) {
    if (subject.startsWith("msg.topic.")) return subject.slice(10);
    if (subject.startsWith("msg.client.")) return subject.slice(11);
    return subject;
  }

  // pageAll(subject): read the whole retained log for a subject by following the
  // message.read cursor (since=0 is the oldest; one page returns the oldest first).
  // Bounded so a very busy subject can't loop forever. Returns frames oldest-first.
  async function pageAll(subject, cap) {
    const sx = SX(); if (!sx) return [];
    const PAGE = 200, MAX_PAGES = cap || 50;
    const acc = [];
    let since = 0;
    for (let i = 0; i < MAX_PAGES; i++) {
      const res = await sx.get("/api/messages?subject=" + encodeURIComponent(subject) + "&since=" + since + "&limit=" + PAGE).catch(() => null);
      const frames = (res && res.messages) || [];
      acc.push(...frames);
      const next = res && res.next_cursor;
      if (frames.length < PAGE || !next || next <= since) break;
      since = next;
    }
    return acc;
  }

  // ============================ JSON / Raw / Hex viewer ============================
  // Shared value viewer used by both modes (a message record and a KV value). The
  // operator picks how to read the payload: pretty JSON, the raw string, or a hex
  // dump — the S19 contract for both the message-row payload and the KV value.
  function ValueViewer({ value, idPrefix }) {
    const [mode, setMode] = useState("json"); // json | raw | hex
    const raw = useMemo(() => {
      if (value == null) return "";
      if (typeof value === "string") return value;
      try { return JSON.stringify(value); } catch (_) { return String(value); }
    }, [value]);
    const pretty = useMemo(() => {
      try { return JSON.stringify(value, null, 2); } catch (_) { return raw; }
    }, [value, raw]);
    const hex = useMemo(() => {
      const bytes = new TextEncoder().encode(raw);
      const lines = [];
      for (let i = 0; i < bytes.length; i += 16) {
        const slice = bytes.slice(i, i + 16);
        const off = i.toString(16).padStart(8, "0");
        const hexCols = Array.from(slice).map((b) => b.toString(16).padStart(2, "0")).join(" ");
        const ascii = Array.from(slice).map((b) => (b >= 32 && b < 127) ? String.fromCharCode(b) : ".").join("");
        lines.push(off + "  " + hexCols.padEnd(16 * 3 - 1, " ") + "  " + ascii);
      }
      return lines.join("\n") || "(empty)";
    }, [raw]);
    const body = mode === "json" ? pretty : mode === "raw" ? (raw || "(empty)") : hex;
    return (
      <div className="sx-bus-viewer">
        <div className="sx-bus-seg" role="tablist">
          {["json", "raw", "hex"].map((m) => (
            <button key={m} role="tab" aria-selected={mode === m}
              className={"sx-bus-seg-btn" + (mode === m ? " is-on" : "")}
              onClick={() => setMode(m)}>{m.toUpperCase()}</button>
          ))}
        </div>
        <pre className="sx-bus-pre" data-mode={mode}>{body}</pre>
      </div>
    );
  }

  // A small "not reachable from the browser" notice — used where an S19 panel needs
  // a bus-side inspection op the browser session is denied (deny is honest, not a
  // blank). Keeps the surface truthful about the live system.
  function Unreachable({ what, why }) {
    return (
      <div className="sx-bus-unreach">
        <span className="sx-bus-unreach-ic">⚿</span>
        <div>
          <div className="sx-bus-unreach-t">{what} isn’t reachable over the browser’s bus credential.</div>
          <div className="sx-bus-unreach-s">{why}</div>
        </div>
      </div>
    );
  }

  // ============================ chips / stat row ============================
  function Chip({ label, tone }) { return <span className={"sx-bus-chip" + (tone ? " t-" + tone : "")}>{label}</span>; }
  function Stat({ k, v, mono }) {
    return (
      <div className="sx-bus-stat">
        <div className="sx-bus-stat-k">{k}</div>
        <div className={"sx-bus-stat-v" + (mono ? " mono" : "")}>{v}</div>
      </div>
    );
  }

  // ============================ JetStream: message browser ============================
  // The genuinely functional half: per-subject message browsing over message.read.
  function MessageBrowser({ subject }) {
    const [frames, setFrames] = useState(null); // null = loading
    const [err, setErr] = useState("");
    const [order, setOrder] = useState("newest"); // newest | oldest
    const [filter, setFilter] = useState("");     // sub-subject / author / text filter
    const [page, setPage] = useState(0);
    const [open, setOpen] = useState({});          // index -> expanded
    const PER = 25;

    const load = useCallback(() => {
      setFrames(null); setErr("");
      pageAll(subject, 50).then((fs) => setFrames(fs)).catch((e) => { setErr(String(e && e.message || e)); setFrames([]); });
    }, [subject]);
    useEffect(() => { load(); setPage(0); setOpen({}); }, [load]);

    const rows = useMemo(() => {
      const list = (frames || []).map((f) => ({
        id: f.id, subject: f.subject || subject, author: f.author, kind: f.kind,
        time: frameTime(f), size: recordSize(f.record), record: f.record, headers: frameHeaders(f),
      }));
      const q = filter.trim().toLowerCase();
      const filtered = q ? list.filter((r) =>
        (r.subject || "").toLowerCase().includes(q) ||
        (r.author || "").toLowerCase().includes(q) ||
        JSON.stringify(r.record || "").toLowerCase().includes(q)) : list;
      filtered.sort((a, b) => order === "newest" ? (b.time - a.time) : (a.time - b.time));
      return filtered;
    }, [frames, filter, order, subject]);

    if (frames === null) return <div className="sx-bus-loading">Reading messages…</div>;
    const total = rows.length;
    const start = page * PER;
    const slice = rows.slice(start, start + PER);
    const showFrom = total === 0 ? 0 : start + 1;
    const showTo = Math.min(start + PER, total);
    const maxPage = Math.max(0, Math.ceil(total / PER) - 1);

    return (
      <div className="sx-bus-msgs">
        <div className="sx-bus-msgbar">
          <input className="sx-bus-input" placeholder="filter by subject / author / payload…"
            value={filter} onChange={(e) => { setFilter(e.target.value); setPage(0); }} />
          <div className="sx-bus-seg">
            <button className={"sx-bus-seg-btn" + (order === "newest" ? " is-on" : "")} onClick={() => setOrder("newest")}>Newest</button>
            <button className={"sx-bus-seg-btn" + (order === "oldest" ? " is-on" : "")} onClick={() => setOrder("oldest")}>Oldest</button>
          </div>
          <button className="sx-bus-iconbtn" title="Reload" onClick={load}>↻</button>
        </div>
        {err ? <div className="sx-bus-err">read error: {err}</div> : null}
        <div className="sx-bus-pager">
          <span className="sx-bus-pager-txt">showing {showFrom}–{showTo} of {total}</span>
          <span className="sx-bus-pager-sp" />
          <button className="sx-bus-iconbtn" disabled={page <= 0} onClick={() => setPage((p) => Math.max(0, p - 1))}>‹</button>
          <span className="sx-bus-pager-pg">{page + 1} / {maxPage + 1}</span>
          <button className="sx-bus-iconbtn" disabled={page >= maxPage} onClick={() => setPage((p) => Math.min(maxPage, p + 1))}>›</button>
        </div>
        {total === 0 ? (
          <div className="sx-bus-empty">No messages match.</div>
        ) : (
          <div className="sx-bus-rows">
            {slice.map((r, i) => {
              const idx = start + i;
              const isOpen = !!open[idx];
              return (
                <div key={r.id || idx} className={"sx-bus-row" + (isOpen ? " is-open" : "")}>
                  <button className="sx-bus-rowhead" onClick={() => setOpen((o) => ({ ...o, [idx]: !o[idx] }))}>
                    <span className="sx-bus-chev">{isOpen ? "▾" : "▸"}</span>
                    <span className="sx-bus-row-subj mono">{r.subject}</span>
                    <span className="sx-bus-row-author">{r.author ? r.author.slice(0, 10) : "—"}</span>
                    <span className="sx-bus-row-time">{r.time ? relMs(r.time) : "—"}</span>
                    <span className="sx-bus-row-size mono">{fmtBytes(r.size)}</span>
                  </button>
                  {isOpen ? (
                    <div className="sx-bus-rowbody">
                      <div className="sx-bus-sub">Headers</div>
                      <table className="sx-bus-hdr">
                        <tbody>
                          {Object.entries(r.headers).map(([k, v]) => (
                            <tr key={k}><td className="sx-bus-hdr-k mono">{k}</td><td className="sx-bus-hdr-v mono">{v}</td></tr>
                          ))}
                        </tbody>
                      </table>
                      <div className="sx-bus-sub">Payload</div>
                      <ValueViewer value={r.record} idPrefix={"m" + idx} />
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}
      </div>
    );
  }

  // ============================ JetStream: stream detail ============================
  function StreamDetail({ stream, onBack }) {
    const [tab, setTab] = useState("messages"); // messages | consumers | config
    return (
      <div className="sx-bus-detail">
        <div className="sx-bus-dethead">
          <button className="sx-bus-back" onClick={onBack}>← Streams</button>
          <h2 className="sx-bus-title mono">{stream.subject}</h2>
          <div className="sx-bus-chips">
            <Chip label="JetStream" tone="brand" />
            <Chip label="file storage" />
            <Chip label="retention: limits" />
            <Chip label={(stream.live ? "live" : "idle")} tone={stream.live ? "live" : ""} />
          </div>
        </div>
        <div className="sx-bus-statrow">
          <Stat k="messages" v={fmtNum(stream.count)} mono />
          <Stat k="bytes" v={fmtBytes(stream.bytes)} mono />
          <Stat k="first activity" v={stream.first ? absTime(stream.first) : "—"} />
          <Stat k="last activity" v={stream.last ? relMs(stream.last) : "—"} />
          <Stat k="consumers" v={"—"} mono />
        </div>
        <div className="sx-bus-tabs" role="tablist">
          {[["messages", "Messages"], ["consumers", "Consumers"], ["config", "Config"]].map(([k, label]) => (
            <button key={k} role="tab" aria-selected={tab === k}
              className={"sx-bus-tab" + (tab === k ? " is-on" : "")} onClick={() => setTab(k)}>{label}</button>
          ))}
        </div>
        <div className="sx-bus-tabbody">
          {tab === "messages" ? (
            <MessageBrowser subject={stream.subject} />
          ) : tab === "consumers" ? (
            <Unreachable what="Consumer state"
              why="Consumer durable/ephemeral kind, ack policy, ack-wait, max-deliver and lag come from $JS.API.CONSUMER.*, which the bus denies to every client credential. A bus-side `stream.consumers` Wire-API op would surface it browser-direct." />
          ) : (
            <div className="sx-bus-config">
              <div className="sx-bus-sub">Configuration (browser-visible)</div>
              <table className="sx-bus-kv">
                <tbody>
                  <tr><td>subject</td><td className="mono">{stream.subject}</td></tr>
                  <tr><td>messages</td><td className="mono">{fmtNum(stream.count)}</td></tr>
                  <tr><td>bytes</td><td className="mono">{fmtBytes(stream.bytes)}</td></tr>
                  <tr><td>first / last</td><td className="mono">{stream.first ? absTime(stream.first) : "—"} · {stream.last ? absTime(stream.last) : "—"}</td></tr>
                </tbody>
              </table>
              <Unreachable what="Full stream config (storage, retention, replicas, max-age, discard)"
                why="These live in $JS.API.STREAM.INFO, denied to client credentials. The chips above are the bus's known defaults; a bus-side `stream.info` op would make the live config browser-direct." />
            </div>
          )}
        </div>
      </div>
    );
  }

  // ============================ JetStream: stream list ============================
  function JetStreamMode({ subjects, onOpen, openSubject }) {
    // For each discovered subject, lazily compute count + last activity + bytes by
    // paging the retained log. Cached per subject so re-renders don't re-read.
    const [stats, setStats] = useState({}); // subject -> {count,bytes,first,last,live}
    const [loading, setLoading] = useState(true);
    const mounted = useRef(true);
    useEffect(() => () => { mounted.current = false; }, []);

    useEffect(() => {
      let cancelled = false;
      setLoading(true);
      (async () => {
        const out = {};
        // Bound concurrency-free serial reads at dash scale (a handful of subjects).
        for (const subj of subjects) {
          const fs = await pageAll(subj, 25).catch(() => []);
          let bytes = 0, first = 0, last = 0;
          for (const f of fs) { bytes += recordSize(f.record); const t = frameTime(f); if (t) { if (!first || t < first) first = t; if (t > last) last = t; } }
          out[subj] = { count: fs.length, bytes, first, last, live: last && (Date.now() - last < 60000) };
          if (cancelled) return;
          setStats((prev) => ({ ...prev, [subj]: out[subj] }));
        }
        if (!cancelled) setLoading(false);
      })();
      return () => { cancelled = true; };
    }, [subjects.join("|")]);

    const open = openSubject ? { subject: openSubject, ...(stats[openSubject] || { count: 0, bytes: 0 }) } : null;
    if (open) return <StreamDetail stream={open} onBack={() => onOpen(null)} />;

    const rows = subjects.map((s) => ({ subject: s, ...(stats[s] || {}) }))
      .sort((a, b) => (b.last || 0) - (a.last || 0));
    return (
      <div className="sx-bus-list">
        <div className="sx-bus-note">
          The bus retains every <span className="mono">msg.&gt;</span> subject in one JetStream stream; each subject is shown here as an inspectable channel.
          {loading ? <span className="sx-bus-note-load"> reading counts…</span> : null}
        </div>
        <table className="sx-bus-table">
          <thead><tr><th></th><th>subject</th><th className="r">messages</th><th className="r">storage</th><th className="r">last activity</th></tr></thead>
          <tbody>
            {rows.length === 0 ? (
              <tr><td colSpan={5} className="sx-bus-empty">No subjects discovered yet — traffic populates this list.</td></tr>
            ) : rows.map((r) => (
              <tr key={r.subject} className="sx-bus-trow" onClick={() => onOpen(r.subject)}>
                <td><span className={"sx-bus-dot" + (r.live ? " is-live" : "")} /></td>
                <td className="mono sx-bus-tname">{r.subject}</td>
                <td className="r mono">{r.count != null ? fmtNum(r.count) : "…"}</td>
                <td className="r mono">{r.bytes != null ? fmtBytes(r.bytes) : "…"}</td>
                <td className="r">{r.last ? relMs(r.last) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  // ============================ KV: bucket detail ============================
  // The `artifacts` bucket is reachable via the artifact Wire API. Keys = artifact
  // names; the current value + revision come from artifact.get / artifact.list.
  function KVBucketDetail({ onBack }) {
    const [keys, setKeys] = useState(null); // [{name,revision,updated}]
    const [filter, setFilter] = useState("");
    const [sel, setSel] = useState(null);
    const [selVal, setSelVal] = useState(undefined); // undefined=not loaded, null=tombstone/missing
    const [selRev, setSelRev] = useState(null);
    const [err, setErr] = useState("");

    const loadKeys = useCallback(() => {
      const sx = SX(); if (!sx) return;
      setKeys(null); setErr("");
      sx.get("/api/artifacts").then((as) => {
        const list = (Array.isArray(as) ? as : []).map((a) => ({ name: a.Name, revision: a.Revision, updated: Date.parse(a.Updated || "") || 0 }));
        list.sort((a, b) => a.name.localeCompare(b.name));
        setKeys(list);
      }).catch((e) => { setErr(String(e && e.message || e)); setKeys([]); });
    }, []);
    useEffect(loadKeys, [loadKeys]);

    const openKey = useCallback((name) => {
      const sx = SX(); if (!sx) return;
      setSel(name); setSelVal(undefined); setSelRev(null);
      sx.get("/api/artifacts/" + encodeURIComponent(name)).then((a) => {
        setSelVal((a && a.Record) || null);
        setSelRev((a && typeof a.Revision === "number") ? a.Revision : null);
      }).catch(() => setSelVal(null));
    }, []);

    const shown = useMemo(() => {
      const q = filter.trim().toLowerCase();
      return (keys || []).filter((k) => !q || k.name.toLowerCase().includes(q));
    }, [keys, filter]);

    const bytesTotal = null; // per-key byte sizes need the $KV API; not browser-reachable
    return (
      <div className="sx-bus-detail">
        <div className="sx-bus-dethead">
          <button className="sx-bus-back" onClick={onBack}>← Buckets</button>
          <h2 className="sx-bus-title mono">artifacts</h2>
          <div className="sx-bus-chips">
            <Chip label="Key-Value" tone="brand" />
            <Chip label="file storage" />
          </div>
        </div>
        <div className="sx-bus-statrow">
          <Stat k="values" v={keys ? fmtNum(keys.length) : "…"} mono />
          <Stat k="history depth" v={"—"} />
          <Stat k="ttl" v={"none"} />
          <Stat k="bytes" v={"—"} mono />
          <Stat k="max value" v={"—"} />
        </div>
        {err ? <div className="sx-bus-err">read error: {err}</div> : null}
        <div className="sx-bus-kvsplit">
          <div className="sx-bus-keys">
            <div className="sx-bus-msgbar">
              <input className="sx-bus-input" placeholder="filter keys…" value={filter} onChange={(e) => setFilter(e.target.value)} />
              <button className="sx-bus-iconbtn" title="Reload" onClick={loadKeys}>↻</button>
            </div>
            {keys === null ? <div className="sx-bus-loading">Listing keys…</div> : (
              <div className="sx-bus-keylist">
                {shown.length === 0 ? <div className="sx-bus-empty">No keys.</div> : shown.map((k) => (
                  <button key={k.name} className={"sx-bus-keyrow" + (sel === k.name ? " is-sel" : "")} onClick={() => openKey(k.name)}>
                    <span className="sx-bus-keyname mono">{k.name}</span>
                    <span className="sx-bus-keyop t-put">PUT</span>
                    <span className="sx-bus-keyrev mono">rev {k.revision}</span>
                  </button>
                ))}
              </div>
            )}
          </div>
          <div className="sx-bus-keyval">
            {sel == null ? (
              <div className="sx-bus-empty">Select a key to see its current value.</div>
            ) : (
              <React.Fragment>
                <div className="sx-bus-sub">Current value <span className="sx-bus-keyrev mono">rev {selRev != null ? selRev : "?"}</span></div>
                {selVal === undefined ? <div className="sx-bus-loading">Reading…</div>
                  : selVal === null ? (
                    <div className="sx-bus-tomb"><span className="sx-bus-tomb-ic">⊘</span> This key has no current value (deleted or never set).</div>
                  ) : <ValueViewer value={selVal} idPrefix="kv" />}
                <div className="sx-bus-sub">Revision history</div>
                <Unreachable what="Per-key revision history + tombstones"
                  why="The KV history walk (and the DEL/PURGE tombstone markers) come from the $KV.artifacts.> subject, denied to client credentials. The current revision above is the latest from artifact.get; a bus-side `artifact.history` op would surface the full newest-first history browser-direct." />
              </React.Fragment>
            )}
          </div>
        </div>
      </div>
    );
  }

  // ============================ KV: bucket list ============================
  function KVMode({ onOpen, open }) {
    const [count, setCount] = useState(null);
    useEffect(() => {
      const sx = SX(); if (!sx) return;
      sx.get("/api/artifacts").then((as) => setCount(Array.isArray(as) ? as.length : 0)).catch(() => setCount(0));
    }, []);
    if (open) return <KVBucketDetail onBack={() => onOpen(false)} />;
    return (
      <div className="sx-bus-list">
        <div className="sx-bus-note">
          The artifact store is a JetStream Key-Value bucket; the operator’s artifacts are its keys.
        </div>
        <table className="sx-bus-table">
          <thead><tr><th></th><th>bucket</th><th className="r">keys</th><th className="r">storage</th></tr></thead>
          <tbody>
            <tr className="sx-bus-trow" onClick={() => onOpen(true)}>
              <td><span className="sx-bus-dot is-live" /></td>
              <td className="mono sx-bus-tname">artifacts</td>
              <td className="r mono">{count != null ? fmtNum(count) : "…"}</td>
              <td className="r mono">file</td>
            </tr>
          </tbody>
        </table>
      </div>
    );
  }

  // ============================ the surface ============================
  // BusInspector is the self-contained Bus nav page: a JetStream ⇆ Key-Value mode
  // toggle over the two list/detail views. It reads `subjects` from props (the
  // app's discovered msg.> set) when given, else discovers them itself via
  // /api/subjects — so it works both wired into app.jsx and standalone (#bus route).
  function BusInspector({ subjects: subjectsProp }) {
    const [mode, setMode] = useState("jetstream"); // jetstream | kv
    const [openSubject, setOpenSubject] = useState(null);
    const [kvOpen, setKvOpen] = useState(false);
    const [discovered, setDiscovered] = useState(null);

    // Discover subjects ourselves when not handed them (standalone route). Polls so
    // newly-active subjects appear; merges with any prop set.
    useEffect(() => {
      if (subjectsProp) return;
      let cancelled = false;
      const tick = () => {
        const sx = SX(); if (!sx) return;
        sx.get("/api/subjects").then((subs) => {
          if (cancelled || !Array.isArray(subs)) return;
          setDiscovered(subs.map((s) => s.subject).filter(Boolean).sort());
        }).catch(() => {});
      };
      tick();
      const id = setInterval(tick, 4000);
      return () => { cancelled = true; clearInterval(id); };
    }, [subjectsProp]);

    const subjects = useMemo(() => {
      const set = new Set();
      for (const s of (subjectsProp || [])) if (s) set.add(s);
      for (const s of (discovered || [])) if (s) set.add(s);
      return [...set].sort();
    }, [subjectsProp, discovered]);

    return (
      <div className="sx-bus">
        <div className="sx-bus-top">
          <div className="sx-bus-h">
            <span className="sx-bus-h-star">✦</span>
            <span className="sx-bus-h-t">Bus inspector</span>
          </div>
          <div className="sx-bus-modeseg" role="tablist" aria-label="Inspector mode">
            <button role="tab" aria-selected={mode === "jetstream"}
              className={"sx-bus-mode" + (mode === "jetstream" ? " is-on" : "")}
              onClick={() => setMode("jetstream")}>JetStream</button>
            <button role="tab" aria-selected={mode === "kv"}
              className={"sx-bus-mode" + (mode === "kv" ? " is-on" : "")}
              onClick={() => setMode("kv")}>Key-Value</button>
          </div>
        </div>
        <div className="sx-bus-bodyscroll">
          {mode === "jetstream"
            ? <JetStreamMode subjects={subjects} onOpen={setOpenSubject} openSubject={openSubject} />
            : <KVMode onOpen={setKvOpen} open={kvOpen} />}
        </div>
      </div>
    );
  }

  // Exported for the app shell (TASK-220): app.jsx renders <BusInspector /> in the
  // Bus nav page. It self-discovers subjects over window.SX when handed no prop, so
  // the shell needs to pass nothing. (The earlier standalone #bus hash route was
  // retired at integration — the shell nav is the real surface now.)
  Object.assign(window, { BusInspector });
})();
