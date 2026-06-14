---
id: TASK-93
title: 'SDK rename: Client.DMs()/subscribeDM/c.dms -> Inbox vocabulary'
status: To Do
assignee: []
created_date: '2026-06-14 22:21'
labels:
  - chore
  - sdk
  - naming
  - 'slug:chore-sdk-rename-dms-to-inbox'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 95000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The SDK names the inbox auto-subscription with DM vocabulary — Client.DMs(), subscribeDM, c.dms, the 'DM channel full' log — but msg.client.<self> is the one-way INBOX, and a DM is now a 2-party topic (TASK-90). The mismatched names perpetuate the exact confusion TASK-90 fixed in the docs. Rename to Inbox()/subscribeInbox/c.inbox (keep behavior identical) so the code matches the vocabulary. Public-API change: update all callers (cmd/sextant-mcp channel bridge, tests).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Client.DMs() -> Client.Inbox() (or equivalent), subscribeDM -> subscribeInbox, c.dms -> c.inbox, log text says 'inbox'; behavior unchanged
- [ ] #2 All callers updated; make lint && make test green
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up to [[feat-plugin-dm-default-over-inbox]] (TASK-90). Pure rename; no behavior change. Aligns code with the inbox-vs-DM split.
<!-- SECTION:NOTES:END -->
