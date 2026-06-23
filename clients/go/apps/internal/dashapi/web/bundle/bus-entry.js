// bus-entry.js — the browser bundle entry (ADR-0044). esbuild bundles this (and
// its imports: @sextant/sdk/browser, @sextant/conv-goals, @sextant/conv-review,
// nats.ws) into vendor/sextant-bus.js as a single IIFE that assigns a global, so
// the classic-script SPA (app.js, an IIFE — no module system, no runtime CDN, no
// in-browser Babel, the ADR-0034 property) reads window.SextantBus and runs the
// conventions directly over its own bus Client.
//
// This is the one ESM file in the dash bundle; everything else stays a plain
// <script>. Keeping it a thin re-export means the SPA's data layer is the only
// place that names the SDK — app.js calls window.SextantBus.* and is otherwise
// unchanged in shape.

import { browserConnect, identityFromCreds } from "@sextant/sdk/browser";
import { project, setCriterion, GoalsSubject } from "@sextant/conv-goals";
import { setReview, REVIEW_STATES } from "@sextant/conv-review";

globalThis.SextantBus = {
  browserConnect,
  identityFromCreds,
  // goals: the read-model projection + the single write verb (set a goal criterion).
  project,
  setCriterion,
  GoalsSubject,
  // review: persist the operator's verdict (read-merge-CAS + approve→met loop).
  setReview,
  REVIEW_STATES,
};
