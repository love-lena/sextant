---
id: TASK-34
title: 'Creds file briefly world-readable: chmod after close in writeOwnerOnly'
status: To Do
assignee: []
created_date: '2026-06-09 19:13'
labels:
  - bug
  - bus
  - auth
  - security
  - 'slug:bug-bus-creds-chmod-window'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 40000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Codex review of PR #100 (house-style adoption) flagged a pre-existing window in pkg/bus/auth.go writeOwnerOnly: os.CreateTemp creates the file with umask-derived permissions (often 0644), and the 0600 chmod only fires after the creds content is written and the file closed. For that window the credential material sits world-readable on disk. writeNewSeed already does this correctly (chmod via the open handle before writing). Not a PR #100 regression — identical on main.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 writeOwnerOnly chmods the handle to 0600 immediately after os.CreateTemp, before any write, mirroring writeNewSeed
- [ ] #2 pkg/bus/auth_perms_test.go still passes; no creds/seed write path writes content before the file is 0600
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Move the chmod from after f.Close() to f.Chmod(0o600) right after os.CreateTemp; sweep the other temp-file writers in auth.go for the same ordering.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Codex review of PR #100 ([[feat-adopt-golangci-lint]]). Related: the gosec G301/G306 thresholds in .golangci.yml deliberately leave secret-file perms to tests, which is why no linter catches this ordering.
<!-- SECTION:NOTES:END -->
