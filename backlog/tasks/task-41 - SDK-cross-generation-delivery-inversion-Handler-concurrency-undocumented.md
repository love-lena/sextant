---
id: TASK-41
title: 'SDK: cross-generation delivery inversion + Handler concurrency undocumented'
status: To Do
assignee: []
created_date: '2026-06-10 21:35'
labels:
  - bug
  - sdk
  - reconnect
  - 'slug:bug-sdk-reconnect-delivery-inversion'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 47000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The relay-generation design (PR #99) makes duplicates impossible (CAS-max cursor) but not inversions: an old-generation dispatcher goroutine that passed the epoch check and won the advanceLastSeq CAS can be descheduled before invoking the Handler while the new generation delivers a later frame first — the subscriber observes seq N+1 before seq N in a microsecond window around a reconnect. Additionally, the two generations can invoke the Handler concurrently, and Handler's docs don't state it must be goroutine-safe (busfeed's happens to be). Fix shape: serialize deliver per subscription (closes both), or document the concurrency contract and accept the theoretical inversion.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Either delivery is serialized per subscription (inversion impossible), or the Handler doc explicitly states the concurrency + ordering contract around reconnects
- [ ] #2 Decision recorded against ADR-0027's ordering guarantee wording
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #99 fold review (2026-06-10), finding 5. Theoretical — strict-order test passes consistently; needs adversarial scheduling. Related: [[bug-sdk-resume-deferral-no-retry-cadence]]
<!-- SECTION:NOTES:END -->
