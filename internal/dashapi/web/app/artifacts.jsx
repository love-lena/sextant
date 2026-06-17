/* artifacts.jsx — the Artifacts view (the white-stage list, TASK-112).
   Extracted from sidebar.jsx (Lena's per-view split): a standalone component
   file like home.jsx/artifact.jsx. Renders /api/artifacts grouped by the
   review-state convention (ADR-0034), each group sorted most-recently-touched
   first by artifact Revision (#160). Behavior is identical to the prior in-
   sidebar version. Reads Avatar from window (defined in sidebar.jsx, looked up
   at render time). Exports ArtifactsView to window. */
(function () {

  const { useState, useEffect } = React;

  // review-state → flow2 group label + status-chip tone (the v0.5 token scale).
  // "Needs review" is waiting on YOU (the attention tone); "Waiting for author" is
  // the agent's turn (the calmer progress tone); approved is "met"; draft leads only
  // after the active states — rejected/archived trail so the eye lands on what needs you.
  // Order: review → changes → approved → draft → rejected → archived.
  const ART_GROUPS = [
  ["review",   "Needs review",       "Needs review",       "t-waiting"],
  ["changes",  "Waiting for author", "Waiting for author", "t-progress"],
  ["approved", "Approved",           "Approved",           "t-met"],
  ["draft",    "Draft",              "Draft",              "t-todo"],
  ["rejected", "Rejected",           "Rejected",           "t-blocked"],
  ["archived", "Archived",           "Archived",           "t-todo"]];
  const ART_DOTC = { "t-waiting": "var(--wait)", "t-progress": "var(--prog)", "t-met": "var(--met)", "t-todo": "var(--todo)", "t-blocked": "var(--blk)" };

  // Collapsed-group persistence key. Value: JSON array of group keys (strings),
  // e.g. ["draft","archived"]. Default: all expanded (empty array).
  const COLLAPSED_KEY = "sx-art-collapsed";

  function ArtifactsView({ artifacts, activeArtifact, onOpenArtifact }) {
    // Seed collapsed set from localStorage; guard malformed values with try/catch.
    const [collapsed, setCollapsed] = useState(() => {
      try { return new Set(JSON.parse(localStorage.getItem(COLLAPSED_KEY) || "[]")); }
      catch(_) { return new Set(); }
    });

    // Write-through: persist whenever the collapsed set changes.
    useEffect(() => {
      try { localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...collapsed])); } catch(_) {}
    }, [collapsed]);

    const toggleCollapsed = (key) => {
      setCollapsed((prev) => {
        const next = new Set(prev);
        next.has(key) ? next.delete(key) : next.add(key);
        return next;
      });
    };

    // "awaiting you" is review only (your turn); changes is the author's turn now.
    const awaiting = artifacts.filter((a) => a.status === "review").length;
    const settled = artifacts.filter((a) => a.status === "approved" || a.status === "rejected" || a.status === "archived").length;
    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light">
        <h1 className="fx-h1 fx-in">Artifacts</h1>
        <p className="fx-psub fx-in" style={{ animationDelay: ".03s" }}>{artifacts.length} documents · {awaiting} awaiting you, {settled} settled</p>
        {ART_GROUPS.map(([st, label, chipLabel, tone]) => {
          // recently-edited first within the group: sort by artifact Revision (the bus-wide
          // write-seq — bumps on every write), the robust "most-recently-touched" signal.
          const items = artifacts.filter((a) => a.status === st).sort((a, b) => (b.version || 0) - (a.version || 0));
          if (!items.length) return null;
          const isCollapsed = collapsed.has(st);
          return (
            <div className="fx-group" key={st}>
              <div className={"fx-group-h fx-group-h--toggle" + (isCollapsed ? " is-collapsed" : "")} onClick={() => toggleCollapsed(st)}>
                <span className="fx-dot" style={{ background: ART_DOTC[tone] }} />
                <span>{label}</span>
                <span className="fx-group-n">{items.length}</span>
                <span className="fx-group-caret" aria-hidden="true">{isCollapsed ? "▸" : "▾"}</span>
              </div>
              {!isCollapsed && (
                <div className="fx-list">
                  {items.map((a) => {
                    const ic = a.type === "sheet" ? "▦" : a.type === "markdown" ? "❡" : "◆";
                    const author = a.author && a.author.name;
                    return (
                      <button key={a.name} className={"fx-row" + (a.name === activeArtifact ? " is-on" : "")} onClick={() => onOpenArtifact(a.name)}>
                        <span className="fx-row-ic">{ic}</span>
                        <span className="fx-row-main">
                          <span className="fx-row-name">{a.name}</span>
                          <span className="fx-row-meta">{a.type}{author ? " · " + author : ""}{a.updated ? " · " + a.updated + " ago" : ""}</span>
                        </span>
                        {author && <Avatar name={author} kind={a.author.kind} size={22} />}
                        <span className={"fx-chip-status " + tone}>{chipLabel}</span>
                      </button>);
                  })}
                </div>
              )}
            </div>);
        })}
        {artifacts.length === 0 && <p className="fx-psub" style={{ marginTop: "26px" }}>No artifacts on the bus yet.</p>}
      </div></div>);

  }

  Object.assign(window, { ArtifactsView });
})();
