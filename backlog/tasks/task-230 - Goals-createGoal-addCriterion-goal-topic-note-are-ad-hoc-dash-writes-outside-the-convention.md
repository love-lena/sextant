---
id: TASK-230
title: >-
  Goals: createGoal / addCriterion / goal-topic note are ad-hoc dash writes
  outside the convention
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - feature
  - goals
  - convention
  - ts-sdk
  - ready-for-human
  - 'slug:feat-goals-creategoal-addcriterion-verbs'
  - P3
dependencies: []
priority: low
ordinal: 219000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The goal lexicon declares one verb (setCriterion); both convention libs implement only that. The dash hand-rolls createGoal (app.jsx:424-427), addCriterion (read-merge-CAS + hand-built goal.update, app.jsx:158-168, self-documented 'no convention verb for add'), and postToGoalTopic ({type:note} on msg.topic.goals.<id>, app.jsx:173-175, no lexicon). These have no Go peer, no conformance vector, and no co-equality coverage — a Go client or violet cannot create a goal or add a criterion through the convention. This is the goals co-equality asymmetry: write coverage varies by client.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 createGoal and addCriterion are defined in the goal lexicon and implemented in both Go and TS conventions, with shared conformance vectors
- [ ] #2 The 'note to goal topic' record gets a lexicon type or is documented as dash-local
- [ ] #3 The dash uses the convention verbs instead of hand-rolled writes
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Lexicon design call. Relates [[task-173]] (goals convention as lexicon library), [[task-175]] (TS co-equality), [[task-183]] (vector format+runner), [[bug-creategoal-off-lexicon-todo-status]].
<!-- SECTION:NOTES:END -->
