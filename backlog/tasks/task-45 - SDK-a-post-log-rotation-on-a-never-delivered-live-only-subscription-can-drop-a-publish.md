---
id: TASK-45
title: >-
  SDK: a post-log rotation on a never-delivered live-only subscription can drop
  a publish
status: To Do
assignee: []
created_date: '2026-06-10 23:30'
labels:
  - bug
  - sdk
  - reconnect
  - 'slug:bug-sdk-livesub-zero-cursor-rotation-window'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 51000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The async resume pass (PR #99) fires 'reconnected to the bus' at the end of a completed, non-superseded pass — but a straggler rotation can land after that log (a superseded pass's in-flight rotation completing late). A rotation on a subscription with lastSeq==0 re-subscribes with its ORIGINAL start option; for a live-only subscription that's 'new messages only', so a message published into that rotation's stop→subscribe window is never delivered and never replayed. With lastSeq>0 the SinceSeq=last+1 replay closes the window — only the zero-cursor live-only case leaks. Narrow: needs a reconnect racing a reconnect plus a publish into the exact window on a sub that has never delivered. Fix shape: once a first generation existed, rotate from the bus's current head instead of the original start option (likely needs the subscribe reply to carry the relay's starting position, or a head query) — an API-shape decision, hence not patched in-PR. A code comment at the rotation start-option site (pkg/sextant/messages.go, reestablish) names the window.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A rotation of a live-only subscription that has delivered nothing does not lose messages published during the rotation window (test drives the straggler-rotation interleaving)
- [ ] #2 ADR-0027's guarantees section stays accurate once fixed
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #99 round-2 fold review (2026-06-10), finding 3. ready-for-human: the fix shape needs an API decision (carry the relay start position in the subscribe reply vs head query). Related: [[bug-sdk-resume-deferral-no-retry-cadence]], [[bug-sdk-reconnect-delivery-inversion]]
<!-- SECTION:NOTES:END -->
