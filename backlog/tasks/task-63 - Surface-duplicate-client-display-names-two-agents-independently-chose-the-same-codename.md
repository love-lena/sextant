---
id: TASK-63
title: >-
  Surface duplicate client display names (two agents independently chose the
  same codename)
status: To Do
assignee: []
created_date: '2026-06-12 17:46'
labels:
  - feature
  - identity
  - ux
  - ergonomics
  - 'slug:feat-display-name-uniqueness'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 69000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The bus accepts duplicate display names: nothing warns or stops two clients from carrying the same human label. On 2026-06-12 two independent agents both minted the display name polaris (reasoning identically: Sextant -> navigation -> the pole star), producing two polaris rows in the directory at once. The collision was only caught because a human noticed and the agents coordinated over the bus to disambiguate. Duplicate labels defeat the point of a memorable name -- the directory and dash show two identical names, distinguishable only by ULID. NOTE the bright line primitives, not policy: the ULID is the identity and display names are deliberately non-unique labels (TASK-30), so this is NOT a request to ENFORCE uniqueness -- it is to SURFACE the collision and leave the policy to the operator/client.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Minting a client with a display name already held by another live identity surfaces a non-fatal warning naming the existing holder ULID (does not block -- names stay non-unique by design)
- [ ] #2 OR the dash / clients list visibly flags when two listed clients share a display name
- [ ] #3 The ULID remains the sole identity; no uniqueness constraint is added to the bus
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Smallest primitive-respecting option is a warning. Either clients register checks the directory for an existing live holder of the name and warns (core), or the directory presentation (dash, clients list) marks duplicates (client-side). Prefer the client-side surface if a core warning reads as policy. Decide core-vs-client.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: 2026-06-12 (canopus + orion both minted polaris). Related: TASK-30 (client identity: ULID + display_name), [[feat-client-rename]], TASK-46 (shared-identity detection -- distinct: that is multiple connections under ONE identity; this is two identities sharing a label). Brushes the primitives, not policy bright line -> ready-for-human.
<!-- SECTION:NOTES:END -->
