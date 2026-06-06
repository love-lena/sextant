#!/usr/bin/env node
// Single source for the Sextant high-level architecture diagram.
// Emits BOTH architecture.excalidraw (editable) and architecture.svg (B&W, for rendering).
// Run: node gen-arch.mjs   (from docs/adr/assets/)
import { writeFileSync } from "node:fs";

const W = 1280, H = 920;
const BLACK = "#1e1e1e", GRAY = "#f1f3f5", DKGRAY = "#e9ecef";

// ---- diagram data -------------------------------------------------------
const boxes = [
  // clients (top) — built on the SDK; Sextant's are forkable reference clients
  { id: "c1", x: 40,   y: 60, w: 220, h: 62, label: "Harness\n(Claude Code, Codex, …)" },
  { id: "c2", x: 290,  y: 60, w: 220, h: 62, label: "Human messaging UI\n(reference)" },
  { id: "c3", x: 540,  y: 60, w: 200, h: 62, label: "Monitor\n(reference)" },
  { id: "c4", x: 770,  y: 60, w: 200, h: 62, label: "Dispatcher\n(reference)" },
  { id: "c5", x: 1000, y: 60, w: 240, h: 62, label: "Workflow coordinator\n(sextant.workflow/v1)" },

  // SDK bar (CORE)
  { id: "sdk", x: 60, y: 220, w: 1160, h: 52, fill: DKGRAY,
    label: "SDK  (Go · TypeScript) · CORE  —  build any client · authn + protocol-epoch check on connect" },

  // Sextant conventions — dashed = optional / forkable, NOT core
  { id: "k1", x: 200, y: 336, w: 190, h: 60, dash: true, fill: "#fff", label: "Clients registry" },
  { id: "k2", x: 430, y: 336, w: 190, h: 60, dash: true, fill: "#fff", label: "Workflow contract\n(Layer-0)" },
  { id: "k3", x: 660, y: 336, w: 190, h: 60, dash: true, fill: "#fff", label: "Spawn-request" },
  { id: "k4", x: 890, y: 336, w: 190, h: 60, dash: true, fill: "#fff", label: "Request / Reply" },

  // the bus (CORE)
  { id: "bus", x: 90, y: 460, w: 1100, h: 360, fill: GRAY, label: "" },
  { id: "msg", x: 150, y: 575, w: 460, h: 140, fill: "#fff",
    label: "Messages\ndurable stream · pub/sub · replayable" },
  { id: "art", x: 680, y: 575, w: 460, h: 140, fill: "#fff",
    label: "Artifacts\nKV · versioned · single-author · CAS" },
];

const texts = [
  { x: 640, y: 30,  text: "Sextant — High-Level Architecture", size: 26, align: "center" },
  { x: 640, y: 156, text: "CLIENTS  —  build on the SDK & implement conventions; run anywhere", size: 15, align: "center" },
  { x: 640, y: 315, text: "SEXTANT CONVENTIONS  —  opinionated · owned · OPTIONAL   (built on the primitives, not core)", size: 14.5, align: "center" },
  { x: 640, y: 430, text: "clients ↔ bus, through the SDK:   pub/sub · read & write artifacts", size: 13, align: "center" },
  { x: 640, y: 492, text: "THE BUS (NATS) · CORE  —  `sextant up`, or any NATS you point the SDK at", size: 16, align: "center" },
  { x: 640, y: 760, text: "TWO PRIMITIVES (core)        ·        reserved  sx.  namespace (system subjects + buckets)", size: 14, align: "center" },
  { x: 640, y: 850, text: "CORE (required):  the bus · two primitives · SDK · wire.        Everything above is built on top — optional & forkable.", size: 13.5, align: "center" },
  { x: 640, y: 876, text: "Sextant ships forkable reference clients (Dispatcher · Workflow coordinator · Monitor · Human UI); bring your own, like the harness.", size: 12.5, align: "center" },
];

const arrows = [
  // clients -> SDK
  { x1: 150, y1: 122, x2: 150, y2: 220 },
  { x1: 400, y1: 122, x2: 400, y2: 220 },
  { x1: 640, y1: 122, x2: 640, y2: 220 },
  { x1: 870, y1: 122, x2: 870, y2: 220 },
  { x1: 1120,y1: 122, x2: 1120,y2: 220 },
  // SDK <-> BUS, down the clear margins (outside the conventions band)
  { x1: 150,  y1: 272, x2: 150,  y2: 460, both: true },
  { x1: 1130, y1: 272, x2: 1130, y2: 460, both: true },
];

// ---- excalidraw emitter -------------------------------------------------
const rnd = () => Math.floor(Math.random() * 2 ** 31);
const exEls = [];
function exText(t) {
  const lines = t.text.split("\n");
  exEls.push({
    id: "t" + rnd(), type: "text", x: t.x, y: t.y, width: 10, height: 25 * lines.length, angle: 0,
    strokeColor: BLACK, backgroundColor: "transparent", fillStyle: "solid", strokeWidth: 1,
    strokeStyle: "solid", roughness: 1, opacity: 100, groupIds: [], frameId: null, roundness: null,
    seed: rnd(), version: 1, versionNonce: rnd(), isDeleted: false, boundElements: [], updated: 1,
    link: null, locked: false, fontSize: t.size || 16, fontFamily: 5, text: t.text,
    textAlign: t.align || "center", verticalAlign: "middle", baseline: 18, containerId: null,
    originalText: t.text, lineHeight: 1.25, autoResize: true,
  });
}
function exBox(b) {
  exEls.push({
    id: b.id, type: "rectangle", x: b.x, y: b.y, width: b.w, height: b.h, angle: 0, strokeColor: BLACK,
    backgroundColor: b.fill && b.fill !== "transparent" ? b.fill : "transparent", fillStyle: "solid",
    strokeWidth: 2, strokeStyle: b.dash ? "dashed" : "solid", roughness: 1, opacity: 100, groupIds: [],
    frameId: null, roundness: { type: 3 }, seed: rnd(), version: 1, versionNonce: rnd(), isDeleted: false,
    boundElements: [], updated: 1, link: null, locked: false,
  });
  if (b.label) exText({ x: b.x + b.w / 2, y: b.y + b.h / 2 - 10, text: b.label, size: 16, align: "center" });
}
function exArrow(a) {
  exEls.push({
    id: "a" + rnd(), type: "arrow", x: a.x1, y: a.y1, width: Math.abs(a.x2 - a.x1), height: Math.abs(a.y2 - a.y1),
    angle: 0, strokeColor: BLACK, backgroundColor: "transparent", fillStyle: "solid", strokeWidth: 2,
    strokeStyle: a.dash ? "dashed" : "solid", roughness: 1, opacity: 100, groupIds: [], frameId: null,
    roundness: { type: 2 }, seed: rnd(), version: 1, versionNonce: rnd(), isDeleted: false, boundElements: [],
    updated: 1, link: null, locked: false, points: [[0, 0], [a.x2 - a.x1, a.y2 - a.y1]],
    lastCommittedPoint: null, startBinding: null, endBinding: null,
    startArrowhead: a.both ? "arrow" : null, endArrowhead: "arrow",
  });
}
boxes.forEach(exBox);
texts.forEach(exText);
arrows.forEach(exArrow);
writeFileSync("architecture.excalidraw", JSON.stringify({
  type: "excalidraw", version: 2, source: "sextant", elements: exEls,
  appState: { viewBackgroundColor: "#ffffff", gridSize: 20 }, files: {},
}, null, 2));

// ---- SVG emitter (B&W) --------------------------------------------------
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
let svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" font-family="Helvetica, Arial, sans-serif">
<rect width="${W}" height="${H}" fill="#ffffff"/>
<defs><marker id="ah" markerWidth="10" markerHeight="10" refX="8" refY="3" orient="auto"><path d="M0,0 L8,3 L0,6 Z" fill="${BLACK}"/></marker>
<marker id="ah0" markerWidth="10" markerHeight="10" refX="2" refY="3" orient="auto"><path d="M8,0 L0,3 L8,6 Z" fill="${BLACK}"/></marker></defs>\n`;
for (const b of boxes) {
  const fill = !b.fill || b.fill === "transparent" ? "none" : b.fill;
  svg += `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" rx="8" fill="${fill}" stroke="${BLACK}" stroke-width="2"${b.dash ? ' stroke-dasharray="8 6"' : ""}/>\n`;
  if (b.label) {
    const lines = b.label.split("\n");
    lines.forEach((ln, i) => {
      const fs = b.id === "sdk" ? 15 : 16;
      const col = i === 0 ? BLACK : "#495057";
      const fw = i === 0 ? ' font-weight="600"' : "";
      svg += `<text x="${b.x + b.w / 2}" y="${b.y + b.h / 2 - (lines.length - 1) * 11 + i * 22 + 5}" font-size="${fs}" text-anchor="middle" fill="${col}"${fw}>${esc(ln)}</text>\n`;
    });
  }
}
for (const a of arrows) {
  svg += `<line x1="${a.x1}" y1="${a.y1}" x2="${a.x2}" y2="${a.y2}" stroke="${BLACK}" stroke-width="2"${a.dash ? ' stroke-dasharray="6 5"' : ""} marker-end="url(#ah)"${a.both ? ' marker-start="url(#ah0)"' : ""}/>\n`;
}
for (const t of texts) {
  svg += `<text x="${t.x}" y="${t.y}" font-size="${t.size}" text-anchor="middle" fill="${BLACK}" font-weight="${t.size >= 20 ? 700 : 600}">${esc(t.text)}</text>\n`;
}
svg += "</svg>\n";
writeFileSync("architecture.svg", svg);
console.log("wrote architecture.excalidraw (" + exEls.length + " elements) and architecture.svg");
