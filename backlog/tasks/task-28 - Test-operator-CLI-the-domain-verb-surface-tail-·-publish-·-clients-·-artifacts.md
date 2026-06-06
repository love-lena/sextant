---
id: TASK-28
title: >-
  Test/operator CLI: the verb surface (publish, subscribe, read, clients,
  artifacts)
status: To Do
assignee: []
created_date: '2026-06-04 18:11'
updated_date: '2026-06-06 06:51'
labels: []
milestone: 'M2: MVP'
dependencies: []
references:
  - docs/adr/0014-the-tui-is-a-client.md
priority: high
ordinal: 27000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The operator/test CLI - the human face of the verb surface and the M2 e2e test harness (the dash moved to M4). 9 verbs with exact name parity to the protocol: publish, subscribe, read, clients list, artifact create|update|get|delete|watch - no 'tail' alias (parity is the guarantee). Adds message.read (cursor-pull over the durable stream; cursor = stream sequence) backed by a new SDK FetchMessages. Output: human text by default + --json + --format <template> for custom per-record lines, so an ad-hoc 'sextant subscribe .. --format ..' drives a Claude Monitor. Includes the conformance test: reads protocol/methods.json and asserts every one-shot + pull-batch verb has a matching CLI command and MCP tool, and push-stream verbs are absent from MCP tools - making 'one surface, many faces' mechanical, not disciplinary. Full design: .work/rfcs/rfc-m2-verb-surface.md.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 CLI has one command per verb with exact name parity: publish, subscribe, read, clients list, artifact create|update|get|delete|watch (no tail alias)
- [ ] #2 message.read (cursor-pull, returns batch + next_cursor) works, backed by a new SDK FetchMessages; sextant subscribe streams live
- [ ] #3 Output supports --json and --format <template> (monitor-friendly per-record lines)
- [ ] #4 A conformance test reads protocol/methods.json and asserts CLI<->MCP verb parity (one-shot + pull-batch on both; push-stream absent from MCP tools)
- [ ] #5 The CLI alone can drive a full MVP e2e test: bus up + >=2 manual clients exchanging messages + artifacts, observed via subscribe/read
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
MOSTLY DONE: publish/read(cursor)/subscribe/clients/artifacts with exact operation parity + conformance test + e2e (cmd/sextant, ADR-0017). REMAINING: --format <template> output (only --json today) and the MCP-tools half of the parity test (MCP server is TASK-22).
<!-- SECTION:NOTES:END -->
