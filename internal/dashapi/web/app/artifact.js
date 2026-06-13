(function() {
  function MarkdownArtifact({ record, name }) {
    const body = record && typeof record.body === "string" ? record.body : "";
    const title = record && record.title || name || "";
    const html = body && window.marked ? window.marked.parse(body) : "";
    return /* @__PURE__ */ React.createElement("article", { className: "md-doc" }, /* @__PURE__ */ React.createElement("style", null, MD_CSS), /* @__PURE__ */ React.createElement("div", { className: "md-inner" }, title && /* @__PURE__ */ React.createElement("div", { className: "md-kicker" }, title), html ? /* @__PURE__ */ React.createElement("div", { dangerouslySetInnerHTML: { __html: html } }) : record ? /* @__PURE__ */ React.createElement("pre", { className: "md-raw" }, JSON.stringify(record, null, 2)) : /* @__PURE__ */ React.createElement("p", { className: "md-lede" }, "Loading\u2026")));
  }
  const MD_CSS = `
  .md-doc{font-family:'Newsreader',Georgia,serif;color:#23262c;}
  .md-doc *{box-sizing:border-box;}
  .md-inner{max-width:760px;margin:0 auto;padding:46px 32px 96px;}
  .md-kicker{font-family:var(--font-mono);font-size:11.5px;letter-spacing:.16em;color:var(--brand-strong);margin-bottom:16px;text-transform:uppercase;}
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
  .md-doc code{font-family:var(--font-mono);font-size:14px;background:#f0f0f1;padding:1.5px 6px;border-radius:5px;color:#3a3f47;}
  .md-doc pre{background:#16181d;color:#e6e4de;font-family:var(--font-mono);font-size:13px;line-height:1.7;padding:18px 20px;border-radius:11px;overflow:auto;margin:0 0 18px;}
  .md-doc pre code{background:none;padding:0;color:inherit;font-size:13px;}
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
})();
