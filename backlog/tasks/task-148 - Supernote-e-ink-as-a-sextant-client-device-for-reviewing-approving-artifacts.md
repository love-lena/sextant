---
id: TASK-148
title: >-
  Supernote (e-ink) as a sextant client device for reviewing + approving
  artifacts
status: To Do
assignee: []
created_date: '2026-06-17 02:11'
labels:
  - feature
  - client
  - review
  - supernote
  - 'slug:feat-supernote-review-client'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 138000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena's outbox idea (2026-06-16): add her Supernote e-ink tablet as a client device for reviews — read + approve artifacts on e-ink (focused, pleasant reading; ties directly to the 'pleasant/effective to read' attention-quality principle in violet-curation-design). Mechanism TBD + needs design: the Supernote is a constrained e-ink device, so likely a lightweight e-ink-friendly web view of for-review artifacts + a way to submit a verdict (approve/request-changes/comment) back onto the bus, OR a sync-to-device + verdict-back bridge. Must respect identity/trust (the device acts as Lena or a delegated client). Post-v0.5.0.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 For-review artifacts are readable on the Supernote (e-ink-friendly rendering)
- [ ] #2 A verdict (approve / request-changes / comment) can be submitted from the device and lands on the bus as a proper /review
- [ ] #3 The device's identity/trust is sound (acts as Lena or an explicitly delegated client; no token leak)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Lena outbox 2026-06-16. Post-v0.5.0. Ties to the attention-quality / pleasant-reading principle (violet-curation-design). Needs a design pass (device constraints + identity).
<!-- SECTION:NOTES:END -->
