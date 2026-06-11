---
id: TASK-36
title: 'Dash: compose input wraps as you type'
status: Done
assignee: []
created_date: '2026-06-09 20:42'
updated_date: '2026-06-10 23:58'
labels:
  - ready-for-agent
dependencies: []
ordinal: 42000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Dogfood 2026-06-09 (Lena, DM compose): the compose line is a single-line text input — a long message scrolls horizontally instead of wrapping, so you can't see what you wrote. Fix shape: multi-line compose widget (grow to N rows as content wraps, Enter still sends; consider shift+enter for literal newline later). Lives in pkg/tui/widget compose + the surfaces' relayout (compose height changes body height).
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented on feat/dash @ 2e9561b (textarea-backed Compose, wraps + grows to 4 rows, Enter still sends, body relayout tracks compose height) + 45f-followup commit restoring the empty-compose placeholders. ADR-0026 invariants kept green (alphabet capture, draft-survives-blur, esc no-op, paste-as-content). Closes with PR #99 merge.

Fixed in: 4887258 (PR #99)
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #99 (squash 4887258): bubbles/textarea compose, 4-row cap, dynamic relayout; height matches the textarea's real word-wrap (grapheme-safe), blurred compose renders exactly Height() rows.
<!-- SECTION:FINAL_SUMMARY:END -->
