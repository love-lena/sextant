---
id: TASK-228
title: 'Bug: dash createGoal writes off-lexicon criterion status ''todo'''
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - bug
  - goals
  - dash
  - ready-for-agent
  - 'slug:bug-creategoal-off-lexicon-todo-status'
  - P2
dependencies: []
priority: medium
ordinal: 217000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
createGoal (app.jsx:426) maps accepted criteria to {id,text,status:todo}. 'todo' is not in the goal lexicon enum (protocol/lexicons/goal.json: met|in-progress|waiting-on-you|blocked|not-started). effectiveStatus passes it through; rollup counts it as none of met/waiting/blocked; the dash TONE map (goals.jsx:26) has no 'todo' key and silently falls back. So every goal created via compose -> 'Accept all' is born with off-contract criteria no status helper recognizes. addCriterion (app.jsx:165) correctly uses 'not-started'.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 createGoal writes status:not-started for new criteria
- [ ] #2 A goal created via the composer renders with recognized criterion tones and a correct rollup
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Verified app.jsx:426 vs :165. Relates [[feat-goals-creategoal-addcriterion-verbs]].
<!-- SECTION:NOTES:END -->
