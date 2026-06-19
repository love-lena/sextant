---
id: TASK-152
title: >-
  violet-runtime.sh: session-id capture broken on claude array output -> answer
  duty never fires
status: To Do
assignee: []
created_date: '2026-06-17 19:02'
labels:
  - bug
  - violet
  - runtime
  - P1
  - release-blocker
  - ready-for-agent
  - 'slug:bug-violet-runtime-session-id-array'
dependencies: []
priority: high
ordinal: 142000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The live violet supervisor (docs/demos/violet-runtime.sh) captures claude's session id with `jq -r '.session_id // empty'`, assuming `claude -p --output-format json` returns a single object. It actually returns a JSON ARRAY of stream events (session_id is on the init/result events), so jq yields empty -> violet.session stays 0 bytes -> EVERY wake takes the first-turn 'defend orient' branch instead of the --resume/answer branch. Effect: violet re-curates Home on every wake and NEVER answers an operator DM (the wake fires, wrong command runs). Caught LIVE in Lena's pre-release violet test (2026-06-17): her FAB DM woke the supervisor (turn 2) but it ran another defend pass instead of answering. The hermetic demo missed it because the claude STUB emits a single object {session_id:...}, masking the real array shape. Fix verified live: extract format-agnostically, e.g. `grep -oE '\"session_id\": ?\"[^\"]+\"' | head -1 | cut -d'\"' -f4`. After the fix, resume + the answer duty work (violet correctly answered Lena + down-ranked stale flags).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 violet.session is populated with a valid session id after the first turn against REAL claude (not the stub)
- [ ] #2 An operator DM triggers an ANSWER turn (resume + reply on VL_DM), not a repeat defend pass
- [ ] #3 The hermetic stub emits claude's real array shape so the demo would catch this regression
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Replace the jq '.session_id' extraction with a format-agnostic grep|cut. Update the demo stub to emit an array like real claude. Land in v0.5 (docs/demos/violet-runtime.sh) BEFORE the tag — violet go-live + the helper-you-message criterion depend on it.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Lena pre-release violet test 2026-06-17. Fix verified live in /tmp working copy. MUST land in v0.5 before the tag. Relates: [[violet-runtime]] (#173), ADR-0039. NOTE: 'TASK-152' is referenced elsewhere for 124 doc-nits — reconcile numbering at merge if this lands as 152.
<!-- SECTION:NOTES:END -->
