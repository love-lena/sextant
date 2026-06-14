---
id: TASK-92
title: 'TUI + web dash: ''DM'' opens the 2-party DM topic, not the client inbox'
status: To Do
assignee: []
created_date: '2026-06-14 22:21'
labels:
  - feature
  - tui
  - dash
  - ux
  - 'slug:feat-dm-ui-2party-topic'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 94000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Under the DM-as-2-party-topic convention (ADR-0034, TASK-90), 'DM this client' should open the back-and-forth topic sx.DMSubject(self, peer), not the one-way inbox msg.client.<id>. Today pkg/tui/surface/clients_browser.go opens a Stream on sx.ClientSubject(ci.ID) (the inbox), and the web dash (internal/dashapi/web/app) constructs DM subjects in JS (dmSubject in app.jsx). Point both at the 2-party DM topic and centralize the format on sx.DMSubject (Go) / a single JS helper mirroring it. Verify the TUI in a PTY (a DM opens the shared 2-party topic, both sides see each other's messages).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 TUI clients_browser 'DM' opens sx.DMSubject(self, peer); driven in a PTY showing a 2-way exchange
- [ ] #2 Web dash starts/opens DMs on msg.topic.dm.<sorted ids>; the inline JS dmSubject matches sx.DMSubject exactly (a test or shared constant guards drift)
- [ ] #3 Inbox view (msg.client.<id>) remains available as the one-way mailbox, distinct from DM
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up to [[feat-plugin-dm-default-over-inbox]] (TASK-90). ready-for-human: UI behavior change lena should eyeball; verify TUIs in a PTY.
<!-- SECTION:NOTES:END -->
