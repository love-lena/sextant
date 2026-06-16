/* artifacts.jsx — the Artifacts view (the white-stage list, TASK-112).
   Extracted from sidebar.jsx (Lena's per-view split): a standalone component
   file like home.jsx/artifact.jsx. Renders /api/artifacts grouped by the
   review-state convention (ADR-0034), each group sorted most-recently-touched
   first by artifact Revision (#160). Behavior is identical to the prior in-
   sidebar version. Reads Avatar from window (defined in sidebar.jsx, looked up
   at render time). Exports ArtifactsView to window. */
(function () {

  // review-state → flow2 group label + status-chip tone (the v0.5 token scale).
  // Needs review/changes are "waiting on you"; approved is "met"; draft/rejected/
  // archived are calmer "todo" grey so the eye lands on what actually needs action.
  const ART_GROUPS = [
  ["review", "Needs review", "Needs review", "t-waiting"],
  ["changes", "Changes requested", "Changes requested", "t-waiting"],
  ["draft", "Draft", "Draft", "t-todo"],
  ["approved", "Approved", "Approved", "t-met"],
  ["rejected", "Rejected", "Rejected", "t-blocked"],
  ["archived", "Archived", "Archived", "t-todo"]];
  const ART_DOTC = { "t-waiting": "var(--wait)", "t-met": "var(--met)", "t-todo": "var(--todo)", "t-blocked": "var(--blk)" };

  function ArtifactsView({ artifacts, activeArtifact, onOpenArtifact }) {
    const awaiting = artifacts.filter((a) => a.status === "review" || a.status === "changes").length;
    const settled = artifacts.length - awaiting;
    return (
      <div className="fx-scroll"><div className="fx-col sx-conv-light">
        <h1 className="fx-h1 fx-in">Artifacts</h1>
        <p className="fx-psub fx-in" style={{ animationDelay: ".03s" }}>{artifacts.length} documents · {awaiting} awaiting you, {settled} settled</p>
        {ART_GROUPS.map(([st, label, chipLabel, tone]) => {
          // recently-edited first within the group: sort by artifact Revision (the bus-wide
          // write-seq — bumps on every write), the robust "most-recently-touched" signal.
          const items = artifacts.filter((a) => a.status === st).sort((a, b) => (b.version || 0) - (a.version || 0));
          if (!items.length) return null;
          return (
            <div className="fx-group" key={st}>
              <div className="fx-group-h"><span className="fx-dot" style={{ background: ART_DOTC[tone] }} /><span>{label}</span><span className="fx-group-n">{items.length}</span></div>
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
            </div>);
        })}
        {artifacts.length === 0 && <p className="fx-psub" style={{ marginTop: "26px" }}>No artifacts on the bus yet.</p>}
      </div></div>);

  }

  Object.assign(window, { ArtifactsView });
})();
