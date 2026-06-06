#!/usr/bin/env node
// Generator for the Sextant system-architecture diagram (ADR 0018).
// Emits BOTH /tmp/0018-architecture.excalidraw (editable) and /tmp/0018-architecture.svg (B&W).
// Style matches docs/adr/assets/gen-arch.mjs: black strokes, gray layer bars, white
// inner boxes, rounded rects, fontFamily 5, B&W only.
import { writeFileSync } from "node:fs";

const W = 1100, H = 920;
const BLACK = "#1e1e1e", GRAY = "#f1f3f5", DKGRAY = "#e9ecef";
const SUB = "#495057"; // caption / secondary text color (SVG only; excalidraw stays black)

// ---- layout geometry ----------------------------------------------------
// Layer 1: Clients (top row).
//   c1, c2 are plain SDK clients (each sits over a Go/TS SDK box);
//   c3 is "Client (no SDK)" and speaks the Wire API straight to the bus.
const cH = 64, cY = 70;
// Access (SDK) boxes: two only — Go SDK, TS SDK. The gray bar wraps ONLY these
// two boxes (left/centre), so the bypass client sits clear to its right.
const accY = 256, accH = 64, accW = 300;
const accXs = [100, 410];                 // Go SDK, TS SDK  (span 100..710)
const accLabels = ["Go SDK", "TS SDK"];
const goCx = accXs[0] + accW / 2;         // 250 — Go SDK centre
const tsCx = accXs[1] + accW / 2;         // 560 — TS SDK centre
// gray bar hugs the two SDK boxes with even padding.
const accBarY = 240, accBarH = 96;
const accBarX = 60;                        // 40px left pad before Go SDK (100)
const accBarRight = 750;                   // 40px right pad after TS SDK (710)
const accBarW = accBarRight - accBarX;     // 690 → bar spans 60..750

const bypassCx = 880;                      // clear column RIGHT of the bar edge (750)

// clients: c1 over Go SDK, c2 over TS SDK, c3 (no SDK) over the bypass column.
const cW = 180, cW3 = 210;
const cXs = [goCx - cW / 2, tsCx - cW / 2, bypassCx - cW3 / 2]; // 160, 470, 775
const ellipsisX = 1000, ellipsisW = 80;    // … to the far right

// Layer 3: Sextant — one wide box.
const sxX = 60, sxY = 470, sxW = 980, sxH = 96;

// Layer 4: Backend — one wide box at the bottom.
const beX = 60, beY = 700, beW = 980, beH = 96;

// ---- diagram data -------------------------------------------------------
const boxes = [
  // clients
  { id: "c1", x: cXs[0], y: cY, w: cW, h: cH, label: "Client" },
  { id: "c2", x: cXs[1], y: cY, w: cW, h: cH, label: "Client" },
  { id: "c3", x: cXs[2], y: cY, w: cW3, h: cH, label: "Client (no SDK)" },
  { id: "cdots", x: ellipsisX, y: cY, w: ellipsisW, h: cH, label: "…", noStroke: true },

  // access-layer gray bar + two SDK boxes
  { id: "accbar", x: accBarX, y: accBarY, w: accBarW, h: accBarH, fill: DKGRAY, label: "" },
  { id: "acc0", x: accXs[0], y: accY, w: accW, h: accH, fill: "#fff", label: accLabels[0] },
  { id: "acc1", x: accXs[1], y: accY, w: accW, h: accH, fill: "#fff", label: accLabels[1] },

  // Sextant (the bus) — one wide box
  { id: "sextant", x: sxX, y: sxY, w: sxW, h: sxH, fill: GRAY, label: "Sextant" },

  // Backend — one wide box
  { id: "backend", x: beX, y: beY, w: beW, h: beH, fill: GRAY, label: "batteries: NATS / Redis" },
];

const cx = W / 2; // 550

const texts = [
  { x: cx, y: 36, text: "Sextant — system architecture", size: 26, weight: 700, align: "center" },

  // Wire API band — the protocol spoken from the access layer (and direct
  // clients) down to the bus. Centred between the flanking arrows.
  { x: cx, y: 392, text: "Wire API", size: 18, weight: 700, align: "center" },
  { x: cx, y: 416, size: 13.5, align: "center", weight: 600,
    text: "the wire protocol — what every client speaks to the bus." },

  // Sextant caption
  { x: cx, y: 596, size: 13.5, align: "center", weight: 600,
    text: "the bus — implements the operations · stamps the frame (bus space) · enforces identity + namespace · the sole access point." },

  // Backend caption
  { x: cx, y: 826, size: 13.5, align: "center", weight: 600,
    text: "the pluggable stream backend, behind one interface." },
];

const cBot = cY + cH;        // 134 — bottom of the clients row
const accBot = accBarY + accBarH; // 336 — bottom of the gray SDK bar

// The two flanking Wire-API arrows sit clear of the centred Wire API
// label + caption (which spans roughly x 380..720).
const sdkPathCx = 250;       // SDK layer → Sextant (Go SDK column, left of label)
const bypassPathCx = bypassCx; // Client (no SDK) → Sextant, in the clear right of the bar (750)

const arrows = [
  // c1 -> Go SDK, c2 -> TS SDK (one-way down onto the SDK boxes)
  { x1: goCx, y1: cBot, x2: goCx, y2: accBarY },
  { x1: tsCx, y1: cBot, x2: tsCx, y2: accBarY },

  // SDK layer <-> Sextant, over the Wire API (bidirectional)
  { x1: sdkPathCx, y1: accBot, x2: sdkPathCx, y2: sxY, both: true },

  // Client (no SDK) <-> Sextant directly, over the Wire API — bypasses the
  // SDK row entirely (bidirectional, runs the full height past the bar).
  { x1: bypassPathCx, y1: cBot, x2: bypassPathCx, y2: sxY, both: true },

  // Sextant <-> backend (bidirectional)
  { x1: cx, y1: 622, x2: cx, y2: beY, both: true },
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
    id: b.id, type: "rectangle", x: b.x, y: b.y, width: b.w, height: b.h, angle: 0,
    strokeColor: b.noStroke ? "transparent" : BLACK,
    backgroundColor: b.fill && b.fill !== "transparent" ? b.fill : "transparent", fillStyle: "solid",
    strokeWidth: 2, strokeStyle: b.dash ? "dashed" : "solid", roughness: 1, opacity: 100, groupIds: [],
    frameId: null, roundness: { type: 3 }, seed: rnd(), version: 1, versionNonce: rnd(), isDeleted: false,
    boundElements: [], updated: 1, link: null, locked: false,
  });
  if (b.label) {
    const size = b.id === "cdots" ? 28 : (b.id === "sextant" ? 20 : 18);
    exText({ x: b.x + b.w / 2, y: b.y + b.h / 2 - 12, text: b.label, size, align: "center" });
  }
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
writeFileSync("/tmp/0018-architecture.excalidraw", JSON.stringify({
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
  if (!b.noStroke) {
    svg += `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" rx="8" fill="${fill}" stroke="${BLACK}" stroke-width="2"${b.dash ? ' stroke-dasharray="8 6"' : ""}/>\n`;
  }
  if (b.label) {
    const fs = b.id === "cdots" ? 30 : (b.id === "sextant" ? 20 : 18);
    svg += `<text x="${b.x + b.w / 2}" y="${b.y + b.h / 2 + fs / 3}" font-size="${fs}" text-anchor="middle" fill="${BLACK}" font-weight="600">${esc(b.label)}</text>\n`;
  }
}
for (const a of arrows) {
  svg += `<line x1="${a.x1}" y1="${a.y1}" x2="${a.x2}" y2="${a.y2}" stroke="${BLACK}" stroke-width="2"${a.dash ? ' stroke-dasharray="6 5"' : ""} marker-end="url(#ah)"${a.both ? ' marker-start="url(#ah0)"' : ""}/>\n`;
}
for (const t of texts) {
  svg += `<text x="${t.x}" y="${t.y}" font-size="${t.size}" text-anchor="middle" fill="${BLACK}" font-weight="${t.weight || 600}">${esc(t.text)}</text>\n`;
}
svg += "</svg>\n";
writeFileSync("/tmp/0018-architecture.svg", svg);
console.log("wrote /tmp/0018-architecture.excalidraw (" + exEls.length + " elements) and /tmp/0018-architecture.svg");
