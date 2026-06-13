/* artifact.jsx — the artifact under review: a rendered MARKDOWN document.
   Exports MarkdownArtifact (rendered), MD_SOURCE (raw .md), MD_DIFF (line diff). */
(function () {

  function MarkdownArtifact() {
    return (
      <article className="md-doc">
        <style>{MD_CSS}</style>
        <div className="md-inner">
          <div className="md-kicker">BRIEF · #release-q3</div>
          <h1>Q3 Launch Brief — Lumen Agents GA</h1>
          <p className="md-lede">
            Lumen Agents moves from limited beta to general availability. This brief defines the
            launch goals, scope, sequencing, and the open risks we need resolved before we commit
            a date publicly.
          </p>

          <h2>Summary</h2>
          <p>
            We are targeting <strong>August 14</strong> for GA. Beta has run for nine weeks across
            <strong> 240 workspaces</strong>; agent-handled conversations are holding at parity with
            human-handled on resolution time and CSAT. The remaining work is commercial
            (usage-based billing) and trust (audit export), not core capability.
          </p>

          <h2>Goals</h2>
          <ol>
            <li>Convert <strong>30%</strong> of beta workspaces to a paid Agents plan within 30 days of GA.</li>
            <li>Keep agent-handled conversations at <strong>≥ 95% CSAT</strong>, matching human-handled.</li>
            <li>Ship the marketing site, docs, and in-product upgrade path in the same window.</li>
          </ol>

          <h2>Scope</h2>
          <h3>In scope</h3>
          <ul>
            <li>Agent autonomy controls — <code>resolve</code>, <code>route</code>, <code>follow_up</code></li>
            <li>Usage-based billing for agent actions</li>
            <li>Audit-log export for every agent decision</li>
          </ul>
          <h3>Out of scope <span className="md-tag">fast-follow · Q4</span></h3>
          <ul>
            <li>Multi-language agent responses</li>
            <li>Self-serve agent fine-tuning</li>
          </ul>

          <h2>Timeline</h2>
          <figure className="md-fig">
            <div className="md-flow">
              <div className="md-step is-now"><b>Code freeze</b><span>Jul 28</span></div>
              <div className="md-arrow">→</div>
              <div className="md-step"><b>Staging soak</b><span>Jul 29 – Aug 6</span></div>
              <div className="md-arrow">→</div>
              <div className="md-step"><b>Marketing live</b><span>Aug 12</span></div>
              <div className="md-arrow">→</div>
              <div className="md-step"><b>GA</b><span>Aug 14</span></div>
            </div>
            <figcaption className="md-figcap">Fig 1 — launch sequence, four phases</figcaption>
          </figure>

          <table>
            <thead><tr><th>Phase</th><th>Dates</th><th>Owner</th></tr></thead>
            <tbody>
              <tr><td>Code freeze</td><td>Jul 28</td><td>platform</td></tr>
              <tr><td>Staging soak + load test</td><td>Jul 29 – Aug 6</td><td>qa-agent</td></tr>
              <tr><td>Marketing + docs live</td><td>Aug 12</td><td>designer-agent</td></tr>
              <tr><td>General availability</td><td>Aug 14</td><td>research-agent</td></tr>
            </tbody>
          </table>

          <h2>Risks &amp; mitigations</h2>
          <table>
            <thead><tr><th>Risk</th><th>Severity</th><th>Mitigation</th></tr></thead>
            <tbody>
              <tr>
                <td>Usage-based billing not load-tested at GA volume</td>
                <td><span className="md-sev high">High</span></td>
                <td>Load test in soak; owner: <strong>platform</strong></td>
              </tr>
              <tr>
                <td>Audit export schema may change post-GA</td>
                <td><span className="md-sev med">Medium</span></td>
                <td>Version the export; document stability guarantees</td>
              </tr>
              <tr>
                <td>Upgrade path friction for beta workspaces</td>
                <td><span className="md-sev low">Low</span></td>
                <td>In-product banner + one-click migrate</td>
              </tr>
            </tbody>
          </table>

          <h2>Feature flags at GA</h2>
          <pre><code>{`agents.ga            = true
agents.billing.meter = true     # usage-based, see goal 1
agents.audit.export  = true
agents.i18n          = false    # out of scope, Q4`}</code></pre>

          <h2>Open questions</h2>
          <ul>
            <li>Who owns the billing load-test sign-off before freeze?</li>
            <li>Do we gate GA on the audit export, or fast-follow it in the first week?</li>
          </ul>
          <blockquote>
            Decision needed by <strong>Jul 24</strong> so we protect the soak window. If billing
            isn't signed off, we hold the date rather than ship a commercial risk.
          </blockquote>
        </div>
      </article>);

  }

  const MD_SOURCE = `# Q3 Launch Brief — Lumen Agents GA

> BRIEF · #release-q3 · owner: research-agent

Lumen Agents moves from limited beta to general availability. This brief
defines the launch goals, scope, sequencing, and the open risks we need
resolved before we commit a date publicly.

## Summary

We are targeting **August 14** for GA. Beta has run for nine weeks across
**240 workspaces**; agent-handled conversations are holding at parity with
human-handled on resolution time and CSAT.

## Goals

1. Convert **30%** of beta workspaces to a paid Agents plan within 30 days.
2. Keep agent-handled conversations at **>= 95% CSAT**.
3. Ship marketing, docs, and the in-product upgrade path together.

## Scope

### In scope
- Agent autonomy controls — \`resolve\`, \`route\`, \`follow_up\`
- Usage-based billing for agent actions
- Audit-log export for every agent decision

### Out of scope (fast-follow, Q4)
- Multi-language agent responses
- Self-serve agent fine-tuning

## Timeline

| Phase                     | Dates          | Owner          |
| ------------------------- | -------------- | -------------- |
| Code freeze               | Jul 28         | platform       |
| Staging soak + load test  | Jul 29 – Aug 6 | qa-agent       |
| Marketing + docs live     | Aug 12         | designer-agent |
| General availability      | Aug 14         | research-agent |
`;

  // v3 -> v4 line diff
  const MD_DIFF = [
  { t: "ctx", l: "## Summary" },
  { t: "del", l: "We are targeting **August 21** for GA. Beta has run for seven weeks" },
  { t: "add", l: "We are targeting **August 14** for GA. Beta has run for nine weeks" },
  { t: "ctx", l: "" },
  { t: "ctx", l: "## Goals" },
  { t: "del", l: "2. Keep agent-handled conversations at **>= 90% CSAT**." },
  { t: "add", l: "2. Keep agent-handled conversations at **>= 95% CSAT**." },
  { t: "ctx", l: "" },
  { t: "ctx", l: "## Risks & mitigations" },
  { t: "add", l: "| Usage-based billing not load-tested at GA volume | High | ... |" }];


  const MD_CSS = `
  .md-doc{font-family:'Newsreader',Georgia,serif;color:#23262c;}
  .md-doc *{box-sizing:border-box;}
  .md-inner{max-width:760px;margin:0 auto;padding:46px 32px 96px;}
  .md-kicker{font-family:var(--font-mono);font-size:11.5px;letter-spacing:.16em;color:var(--brand-strong);margin-bottom:16px;}
  .md-doc h1{font-family:var(--font-ui);font-size:39px;line-height:1.1;letter-spacing:-.03em;font-weight:600;color:#14161b;margin:0 0 18px;}
  .md-lede{font-size:19px;line-height:1.55;color:#41454d;margin:0 0 8px;}
  .md-doc h2{font-family:var(--font-ui);font-size:23px;font-weight:600;letter-spacing:-.02em;color:#181b20;margin:40px 0 14px;padding-top:22px;border-top:1px solid #e8e8ea;}
  .md-doc h3{font-family:var(--font-ui);font-size:16px;font-weight:600;color:#2a2d33;margin:22px 0 9px;display:flex;align-items:center;gap:10px;}
  .md-tag{font-family:var(--font-mono);font-size:10px;letter-spacing:.06em;text-transform:uppercase;color:#9aa0a6;border:1px solid #e8e8ea;border-radius:5px;padding:2px 7px;font-weight:400;}
  .md-doc p{font-size:17px;line-height:1.64;margin:0 0 16px;}
  .md-doc strong{font-weight:600;color:#14161b;}
  .md-doc ul,.md-doc ol{font-size:17px;line-height:1.5;margin:0 0 16px;padding-left:22px;}
  .md-doc li{margin-bottom:8px;padding-left:4px;}
  .md-doc code{font-family:var(--font-mono);font-size:14px;background:#f0f0f1;padding:1.5px 6px;border-radius:5px;color:#3a3f47;}
  .md-doc pre{background:#16181d;color:#e6e4de;font-family:var(--font-mono);font-size:13px;line-height:1.7;padding:18px 20px;border-radius:11px;overflow:auto;margin:0 0 18px;}
  .md-doc pre code{background:none;padding:0;color:inherit;font-size:13px;}
  .md-doc blockquote{border-left:3px solid var(--brand-strong);background:rgba(0,0,0,.025);margin:20px 0 0;padding:13px 20px;font-size:16.5px;line-height:1.55;color:#41454d;font-style:italic;border-radius:0 9px 9px 0;}
  .md-doc table{width:100%;border-collapse:collapse;margin:4px 0 22px;font-family:var(--font-ui);font-size:14.5px;}
  .md-doc th{text-align:left;font-weight:600;color:#181b20;border-bottom:2px solid #e8e8ea;padding:10px 14px 9px;font-size:11.5px;text-transform:uppercase;letter-spacing:.05em;}
  .md-doc td{padding:11px 14px;border-bottom:1px solid #f0f0f1;color:#3f444b;vertical-align:top;line-height:1.45;}
  .md-doc td:first-child{color:#23262c;}
  .md-sev{font-family:var(--font-mono);font-size:11px;font-weight:600;padding:2px 9px;border-radius:30px;white-space:nowrap;}
  .md-sev.high{color:#c0573b;background:rgba(192,87,59,.13);}
  .md-sev.med{color:#b9842a;background:rgba(216,162,63,.16);}
  .md-sev.low{color:#3f8f59;background:rgba(87,176,111,.16);}
  .md-fig{margin:6px 0 26px;}
  .md-flow{display:flex;align-items:stretch;font-family:var(--font-ui);}
  .md-step{flex:1;background:#f5f5f6;border:1px solid #e8e8ea;border-radius:11px;padding:13px 12px;text-align:center;}
  .md-step b{display:block;font-size:13.5px;color:#181b20;font-weight:600;letter-spacing:-.01em;}
  .md-step span{font-family:var(--font-mono);font-size:11px;color:#8b8f96;display:block;margin-top:3px;}
  .md-step.is-now{border-color:var(--brand-strong);background:var(--brand-soft);}
  .md-arrow{display:flex;align-items:center;color:#cdcdd1;padding:0 10px;font-size:15px;}
  .md-figcap{font-family:var(--font-mono);font-size:11px;color:#a0a4ab;text-align:center;margin-top:10px;}
  @container (max-width:720px){
    .md-inner{padding:34px 22px 64px;}
    .md-doc h1{font-size:30px;} .md-lede{font-size:17px;}
    .md-flow{flex-direction:column;gap:8px;} .md-arrow{transform:rotate(90deg);padding:2px 0;}
  }`;

  window.MarkdownArtifact = MarkdownArtifact;
  window.MD_SOURCE = MD_SOURCE;
  window.MD_DIFF = MD_DIFF;
})();