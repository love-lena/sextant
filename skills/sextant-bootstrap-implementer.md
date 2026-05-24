---
name: sextant-bootstrap-implementer
description: Use when implementing milestones from sextant initial's bootstrap plan. Loads the architectural context, conventions, and current milestone scope.
---

# Sextant bootstrap implementer

You are implementing milestones from sextant initial's bootstrap plan. Initial is the first considered ground-up implementation of sextant — a Go control plane for AI coding agents — built phase 1 by classic Claude Code (you), and phase 2 by sextant agents.

**You are in phase 1.** Your job is linear, well-scoped implementation work driven by detailed specs.

## Required reading (in order)

Before writing any code, read:

1. `README.md` — orientation
2. `specs/architecture.md` — every load-bearing design decision, with rationale
3. `plans/bootstrap.md` — the milestone sequence and which one you're on
4. `conventions/STYLE.md` — how Go is written here
5. `conventions/git-workflow.md` — branching and committing
6. The component spec(s) referenced by your current milestone

Specs that are sparse or missing are *yours to fill in* before coding. Make the spec concrete first, then implement. Drafted spec → human review → implementation.

## Core principles

### Boring code

Every contributor (human and AI) produces code that reads like every other piece of sextant. No cleverness. No exotic patterns. No premature abstractions.

- Concrete types until proven otherwise
- Constructors with errors, not late init
- Errors wrapped with context
- Slices not pointer-to-slice
- Context.Context first arg on anything that blocks
- Small interfaces defined consumer-side

### Spec-driven

If the spec doesn't say it, you don't implement it. If you need to, update the spec *first* with the decision and your reasoning, then implement.

### Tests before declaring done

Every milestone has acceptance criteria. Write tests that prove those criteria are met. Run `make lint test` before considering work complete.

### Atomic commits

One logical change per commit. Reference the milestone in the commit body. Co-author trailer for AI commits.

## Workflow per milestone

1. **Read** the milestone in `plans/bootstrap.md` and its referenced spec files
2. **Refine spec** if the spec is sparse: write the concrete detail you need into the spec file as a commit, request review if uncertain
3. **Branch**: `<kind>-<short-description>-<seq>` per `conventions/git-workflow.md`
4. **Implement** in small commits
5. **Test**: write tests demonstrating the acceptance criteria, run `make lint test`
6. **Merge**: if acceptance criteria met, merge to main

## What's in scope for phase 1

- Implementing milestones M0 through M15 of `plans/bootstrap.md`
- Writing/refining specs that are sparse
- Setting up CI, linting, build infrastructure
- Writing tests

## What's NOT in scope for phase 1

- Self-update logic (M16, post-switchover work)
- Test environment provisioning (M17, post-switchover work)
- Anything from `architecture.md` marked as v2 or v3
- Features not in the active milestone
- Refactoring "for elegance" — boring code is the goal

## Key references

- Architecture: `specs/architecture.md` (the load-bearing doc — when in doubt, this is the source of truth)
- Bus subjects: `specs/protocols/bus-subjects.md`
- Envelope: `specs/protocols/envelope-schema.md`
- RPC catalog: `specs/protocols/rpc-catalog.md`
- CLI: `specs/cli/commands.md`
- Style: `conventions/STYLE.md`
- Git: `conventions/git-workflow.md`
- TUI conventions: `conventions/tui-conventions.md`

## When you get stuck

- Spec ambiguous → propose the resolution in a commit to the spec, ask for review
- Architecture question → re-read `specs/architecture.md` for that pillar; if still unclear, surface to operator
- Test failure → fix the bug, don't disable the test
- Lint failure → fix the violation, don't `//nolint:` unless you can justify in the comment

## Anti-patterns

- Copying patterns from the pilot Rust codebase — pilot is in a separate repo and intentionally not visible. We're building fresh on a considered design.
- Adding dependencies casually — every new Go module is a maintenance cost. Justify each one.
- Cleverness that "saves typing" — typing is cheap; reading is expensive. Boring wins.
- Mixing milestones in one PR — one milestone, one (or few) PRs.
