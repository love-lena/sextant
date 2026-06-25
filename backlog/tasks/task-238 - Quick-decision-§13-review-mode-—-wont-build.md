---
id: TASK-238
title: Quick-decision (§13) review mode — won't build
status: Done
assignee: []
created_date: '2026-06-25 19:26'
updated_date: '2026-06-25 19:26'
labels:
  - decision
  - dash
  - review
  - wontfix
  - 'slug:feat-quick-decision-mode'
  - P3
dependencies: []
priority: low
ordinal: 226000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The design (claude.ai/design a879e5e0-7130-4a48-bc63-c65cfc9502ad, sx-overlays.jsx QuickDecision; AC §13) includes a 'Quick decision' mode: an options-based micro-decision with no document — the run poses a question with N options, the operator picks one (or types their own) in a short chat. It was cut from the v0.8.0 build (TASK-208 noted §13 not built). Decision (Lena, 2026-06-25): kill it permanently — it overcomplicates things and adds little over the unified five-verb review. Recorded so design-fidelity audits treat §13 as an INTENTIONAL omission, not a gap, and so it is not revived from the design.
<!-- SECTION:DESCRIPTION:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Won't build. Quick-decision (design §13 / sx-overlays.jsx QuickDecision) is intentionally omitted: it overcomplicates the review model and the unified five-verb artifact review ([[feat-unify-artifact-brief-review-five-verbs]], TASK-237) covers operator decisions. Design still contains the QuickDecision component; the dash deliberately does not implement it. Future design audits: §13 = intentional omission, not a divergence. Relates [[task-208]] (cut it from the build).
<!-- SECTION:FINAL_SUMMARY:END -->
