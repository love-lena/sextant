/* artifact.jsx — the artifact under review, rendered as a markdown document.
   Wired (TASK-71): renders the live artifact Record fetched from
   /api/artifacts/{name} — a {$type:"document", title, body} lexicon — via marked.
   Falls back to raw JSON for non-document records, and a loading note while the
   fetch is in flight. Exports MarkdownArtifact(record, name) to window.

   Wikilinks: [[name]] / [[name|display alias]] in the body render as in-dash
   links. A wikilink whose target IS a known artifact (artifactNames) becomes a
   clickable link that opens that artifact via onOpenArtifact; an UNKNOWN target
   renders muted and inert (no dead navigation to a confusing "not found" page).
   The substitution runs on the marked output BEFORE DOMPurify, and both the
   target name and the display text are HTML-escaped first, so a body like
   [[<img onerror=…>]] can't inject — the body is untrusted bus content and the
   page holds the API token. */
(function () {

  // escape the five HTML-significant chars before splicing untrusted text into
  // the markup string (DOMPurify still runs after, but we never hand it markup
  // we built from raw, unescaped body text).
  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  // [[name]] and [[name|display]] → an in-dash anchor (known target) or a muted,
  // inert span (unknown target). Both name + display are escaped before they hit
  // the markup. Runs on marked's HTML output, before sanitize.
  function renderWikilinks(html, names) {
    const known = names instanceof Set ? names : new Set(names || []);
    return html.replace(/\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g, (_m, rawName, alias) => {
      const target = rawName.trim();
      const display = (alias != null ? alias : rawName).trim();
      const escDisplay = escapeHtml(display);
      if (known.has(target)) {
        return `<a class="md-wl" data-art="${escapeHtml(target)}">${escDisplay}</a>`;
      }
      return `<span class="md-wl-bad">${escDisplay}</span>`;
    });
  }

  // Render an artifact/brief body to sanitized HTML, selecting the path by
  // `format`. "html" => the body IS raw HTML (sanitized as-is); anything else
  // (incl. absent) => the body is Markdown, parsed by marked first. Wikilinks
  // are spliced (HTML-escaped) before sanitize either way, so they go through
  // the same XSS gate. DOMPurify strips <script>, on* handlers, and js: URLs,
  // so neither path can run JS or reach the page token. Falls back to "" when
  // DOMPurify (the XSS gate) is unavailable — never inject unsanitized markup.
  function renderArtifactBody(body, format, artifactNames) {
    if (!body || typeof body !== "string" || !window.DOMPurify) return "";
    const raw = (format === "html")
      ? body
      : (window.marked ? window.marked.parse(body) : "");
    if (!raw) return "";
    return window.DOMPurify.sanitize(renderWikilinks(raw, artifactNames));
  }

  function MarkdownArtifact({ record, name, revision, onOpenArtifact, artifactNames }) {
    const body = record && typeof record.body === "string" ? record.body : "";
    const title = (record && record.title) || name || "";
    // "Updated since reviewed" flag (TASK-79): review.rev is the revision the
    // review was made against, and the review write itself bumps the rev by 1 —
    // so any FURTHER write (revision > review.rev + 1) means the content changed
    // since the review, and the verdict may be stale. (Right after a review,
    // revision === review.rev + 1, so it does not flag.)
    const rv = record && record.review;
    const stale = rv && rv.rev && revision && revision > rv.rev + 1;
    // Sanitize the rendered markdown before injecting it: artifact bodies come
    // from the bus, and the cockpit page holds the API token — unsanitized HTML
    // would be a token-exfil XSS. Require DOMPurify; without it, fall back to raw.
    // Wikilinks are spliced (already HTML-escaped) into the marked output BEFORE
    // this sanitize call, so they go through the same XSS gate as everything else.
    const html = renderArtifactBody(body, record && record.format, artifactNames);
    // delegated click: a valid wikilink anchor carries data-art; open it in-dash
    // instead of following a (hrefless) link. Muted spans have no data-art, so
    // they're inert.
    const onClick = (e) => {
      const a = e.target.closest && e.target.closest("a[data-art]");
      if (a) { e.preventDefault(); onOpenArtifact && onOpenArtifact(a.getAttribute("data-art")); }
    };
    return (
      <article className="md-doc" onClick={onClick}>
        <style>{MD_CSS}</style>
        <div className="md-inner">
          {stale && <div className="md-stale">⚠ Updated since {rv.state || "reviewed"} (reviewed at rev {rv.rev}, now rev {revision}) — the verdict may be stale; re-review.</div>}
          {title && <div className="md-kicker">{title}</div>}
          {html
            ? <div className={record && record.format === "html" ? "md-html-body" : undefined} dangerouslySetInnerHTML={{ __html: html }} />
            : record
              ? <pre className="md-raw">{JSON.stringify(record, null, 2)}</pre>
              : <p className="md-lede">Loading…</p>}
        </div>
      </article>);
  }

  const MD_CSS = `
  .md-doc{font-family:'Newsreader',Georgia,serif;color:#23262c;}
  .md-doc *{box-sizing:border-box;}
  .md-inner{max-width:760px;margin:0 auto;padding:46px 32px 96px;}
  .md-kicker{font-family:var(--font-mono);font-size:11.5px;letter-spacing:.16em;color:var(--brand-strong);margin-bottom:16px;text-transform:uppercase;}
  .md-stale{font-family:var(--font-ui);font-size:13px;line-height:1.45;background:rgba(217,119,6,.13);color:#b45309;border:1px solid rgba(217,119,6,.5);border-radius:8px;padding:9px 13px;margin-bottom:20px;font-weight:600;}
  .md-doc h1{font-family:var(--font-ui);font-size:39px;line-height:1.1;letter-spacing:-.03em;font-weight:600;color:#14161b;margin:0 0 18px;}
  .md-lede{font-size:19px;line-height:1.55;color:#41454d;margin:0 0 8px;}
  .md-doc h2{font-family:var(--font-ui);font-size:23px;font-weight:600;letter-spacing:-.02em;color:#181b20;margin:40px 0 14px;padding-top:22px;border-top:1px solid #e8e8ea;}
  .md-doc h3{font-family:var(--font-ui);font-size:16px;font-weight:600;color:#2a2d33;margin:22px 0 9px;display:flex;align-items:center;gap:10px;}
  .md-tag{font-family:var(--font-mono);font-size:10px;letter-spacing:.06em;text-transform:uppercase;color:#9aa0a6;border:1px solid #e8e8ea;border-radius:5px;padding:2px 7px;font-weight:400;}
  .md-doc p{font-size:17px;line-height:1.64;margin:0 0 16px;}
  .md-doc strong{font-weight:600;color:#14161b;}
  .md-doc ul,.md-doc ol{font-size:17px;line-height:1.5;margin:0 0 16px;padding-left:22px;}
  .md-doc li{margin-bottom:8px;padding-left:4px;}
  .md-doc a{color:var(--brand-strong);text-decoration:underline;text-underline-offset:2px;}
  .md-doc a.md-wl{color:var(--brand-strong);text-decoration:underline;text-underline-offset:2px;cursor:pointer;}
  .md-doc .md-wl-bad{color:#b8bcc2;cursor:default;}
  .md-doc code{font-family:var(--font-mono);font-size:14px;background:#f0f0f1;padding:1.5px 6px;border-radius:5px;color:#3a3f47;}
  .md-doc pre{background:#16181d;color:#e6e4de;font-family:var(--font-mono);font-size:13px;line-height:1.7;padding:18px 20px;border-radius:11px;overflow:auto;margin:0 0 18px;}
  .md-doc pre code{background:none;padding:0;color:inherit;font-size:13px;}
  .md-doc img{max-width:100%;height:auto;}
  .md-html-body{overflow-wrap:break-word;}
  .md-html-body table{display:block;max-width:100%;overflow-x:auto;}
  .md-html-body pre,.md-html-body code{white-space:pre-wrap;word-break:break-word;}
  .md-raw{background:#f5f5f6;color:#3a3f47;font-family:var(--font-mono);font-size:13px;line-height:1.6;padding:18px 20px;border-radius:11px;white-space:pre-wrap;word-break:break-word;overflow:auto;}
  .md-doc blockquote{border-left:3px solid var(--brand-strong);background:rgba(0,0,0,.025);margin:20px 0;padding:13px 20px;font-size:16.5px;line-height:1.55;color:#41454d;font-style:italic;border-radius:0 9px 9px 0;}
  .md-doc table{width:100%;border-collapse:collapse;margin:4px 0 22px;font-family:var(--font-ui);font-size:14.5px;}
  .md-doc th{text-align:left;font-weight:600;color:#181b20;border-bottom:2px solid #e8e8ea;padding:10px 14px 9px;font-size:11.5px;text-transform:uppercase;letter-spacing:.05em;}
  .md-doc td{padding:11px 14px;border-bottom:1px solid #f0f0f1;color:#3f444b;vertical-align:top;line-height:1.45;}
  .md-doc td:first-child{color:#23262c;}
  @container (max-width:720px){
    .md-inner{padding:34px 22px 64px;}
    .md-doc h1{font-size:30px;} .md-lede{font-size:17px;}
  }`;

  window.MarkdownArtifact = MarkdownArtifact;
  window.renderArtifactBody = renderArtifactBody;
})();
