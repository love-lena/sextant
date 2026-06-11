---
id: TASK-32.3
title: Go SDK getting-started + reference
status: Done
assignee: []
created_date: '2026-06-08 22:42'
updated_date: '2026-06-11 00:03'
labels:
  - docs
  - 'slug:docs-mdbook-sdk-reference'
  - P3
  - ready-for-agent
dependencies: []
parent_task_id: TASK-32
priority: medium
ordinal: 37000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The client-developer path Lena asked to fold into the book. A runnable getting-started (sextant up -> clients register -> a ~30-line Go program: Connect, Publish a chat.message, Subscribe, Create+Get a document artifact, drain) plus per-area reference (Messages / Artifacts / Clients & identity) that leads with the SDK's existing narrative doc comments and links the generated 'go doc' API reference (from 32.1).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Per-area SDK pages — Messages, Artifacts, Clients & identity — lead with the narrative doc comments and link the generated API reference
- [x] #2 API reference page wired from 32.1's 'go doc ./pkg/sextant' generation
- [x] #3 The runnable example is verified by building and running it, not just pasted
- [x] #4 A copy-pasteable Go program (Connect · publish chat.message · subscribe · create+get a document artifact · drain) that compiles and runs against a local 'sextant up', inserted into Lena's pre-stubbed getting-started narrative (agent does not author the install / first-client prose)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Getting-started narrative is voice-sensitive (Lena's voice via writing-style); the runnable example + godoc reference are AFK + verifiable. Depends on [[docs-mdbook-ia-render-pipeline]].

PROSE/AGENT SPLIT: agent owns the runnable+verified Go program, the per-area SDK pages rendered from the (already-written) package doc comments, and the go-doc API reference. The getting-started narrative and the SDK Overview are Lena's pre-stubbed prose (branch docs-mdbook-scope).

IMPLEMENTED in PR #97 (commit 05c01e6), CI green. Agent portion complete; remaining work is Lena's prose pages. Verified: docgen deterministic + CI drift-check, mdbook builds clean, quickstart compiles + runs against a live bus. getting-started narrative + SDK Overview remain Lena's prose.

Fixed in: PR #97
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped via PR #97: per-area SDK pages leading with narrative doc comments, generated API reference wired, runnable getting-started example build-verified.
<!-- SECTION:FINAL_SUMMARY:END -->
