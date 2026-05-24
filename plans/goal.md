# Phase 1 implementor goal — sextant initial

You are the phase 1 implementor of sextant initial. Your job: ship milestones **M0 through M14** of [`plans/bootstrap.md`](bootstrap.md) in order, then run the **M15 smoke verification** against the running system, then **stop** and write a readiness report. Do not start M16 or M17 — those are for sextant agents to do after switchover.

This document is the contract that overrides anything in `skills/sextant-bootstrap-implementer.md` that would cause you to stop and ask the operator instead of deciding and proceeding.

## Rule 0 — no clarifying questions to the operator

The skill file says "request review if uncertain" and "surface to operator." **Those instructions do not apply.** You have full authority to:
- Refine sparse specs (commit your refinement, then implement against it)
- Make architecture decisions on contradictions you find (see escape valve below)
- Choose between equivalent options (pick the simpler, document the alternative as a `Considered:` note)
- Commit, branch, and merge per `conventions/git-workflow.md` without per-action approval

The **only** valid reasons to halt are the explicit stop conditions in the next section. Everything else is decide-and-proceed.

## Stop conditions

These are the only reasons to halt. Each writes a file the operator reads when they next look at the repo.

1. **All M14 code is merged AND M15 smoke checks pass.** This is the success path. Write `plans/phase1-complete.md` listing each M15 acceptance criterion and how you verified it (commands run, outputs observed). Stop. Do not dispatch the first sextant agent yourself.

2. **A test failure persists after 3 honest attempts.** Not 3 retries — 3 distinct hypotheses about the cause, each tested. Write the diagnosis (what you tried, what each failure mode looked like, what you suspect) to `plans/blockers.md` and stop.

3. **A spec contradiction where two reasonable refinements produce incompatible later milestones,** and `/codex:rescue` (see escape valve) also returns "needs operator input." Write both interpretations + codex's response to `plans/blockers.md` and stop.

4. **A security-architecture decision you cannot evaluate.** If you find yourself about to write code that controls how agents authenticate, which capabilities they get, or how secrets flow — and the spec genuinely doesn't say — escalate. Write the decision point to `plans/blockers.md` and stop. This is the only category where "I cannot decide this" is correct.

5. **M15 smoke check fails on a real design issue** you cannot resolve without changing the architecture. Write the failing trace and the architectural concern to `plans/blockers.md` and stop.

## Escape valve — `/codex:rescue` for architectural contradictions

If you hit a spec contradiction or load-bearing ambiguity you cannot resolve by reading + reasonable inference:

1. Invoke `/codex:rescue` with a clear statement of the contradiction (which specs, which paragraphs, what's incompatible, what you'd implement either way).
2. Use codex's resolution to update the affected spec(s). Commit the spec edit first, separately from the implementation commit.
3. Continue implementing.
4. Only escalate to `plans/blockers.md` (stop condition 3 or 4) if codex's resolution is "needs operator input" or makes a decision in stop-condition-4 territory.

This is your primary tool for unblocking yourself on design questions. Use it freely — it is faster and cheaper than halting.

## Sparse-spec handling

Specs that say "TBD", "open", "lean: X", "default: X" are inputs to refine, **not blockers**.

- If the spec hints at a direction (`lean: X`), go with `X` unless reading the surrounding context tells you it's wrong. Document the choice in the spec, then implement.
- If two reasonable choices exist with no principle to break the tie, pick the simpler one and write the alternative as a `Considered:` note in the spec.
- Specs are source-of-truth. Commits to specs are part of the work. Reference the spec commit in the implementation commit body.
- **Never block waiting for operator input on a sparse spec.** Decide and proceed.

## Per-milestone workflow

1. **Read** the milestone block in `plans/bootstrap.md` and every spec it references.
2. **Refine specs** (in their own commit) if any are sparse for what you need to do.
3. **Branch** per `conventions/git-workflow.md` (`<kind>-<short-description>-<seq>`).
4. **Implement** in small atomic commits. Reference the milestone in each commit body (`Plan: plans/bootstrap.md#M2`).
5. **Tests** demonstrating each acceptance criterion. Run `make lint test` until clean.
6. **Self-review checkpoint** — re-read the milestone block, diff your branch against each acceptance criterion, write down (in your head, no need to commit) what proves each one. If anything is unproven, fix before merging.
7. **Merge** to main per the workflow doc.
8. **Push to origin** (`git push`). The remote at `github.com/love-lena/sextant-initial` is already configured; main tracks origin/main. **Never `git push --force`** under any condition (per `conventions/git-workflow.md`). If a normal `git push` fails (auth, network, non-fast-forward), halt and write the failure to `plans/blockers.md` — push failures are operator-domain.
9. **Move to the next milestone** without pausing. Do not write a "milestone N complete!" status message — your commits and tests are the status.

## Toolchain — verify before M0

Lena's machine has an outdated Go installed that **must not be used**. Before starting M0:

1. Run `go version` and confirm it reports **Go 1.26 or newer**.
2. If older or missing: install Go 1.26+ via `brew install go` (check `brew info go` for the version Homebrew provides; if it's old, use the official installer at https://go.dev/dl/).
3. Confirm with `go version` again before proceeding.

Same principle for other tools as their milestones land:
- NATS server 2.10+ for M2
- ClickHouse 24.x+ for M3
- Docker (via OrbStack on macOS) for M9
- Node LTS for the sidecar / TS client

Verify each is current at its milestone, install if not.

## Principles (do not negotiate)

- **Boring code.** Follow `conventions/STYLE.md`. No cleverness, no premature abstractions, no exotic patterns.
- **Fail fast.** Errors wrapped with context. No nil-result-no-error. No swallowed errors.
- **Tests before declaring done.** Every acceptance criterion has a test or a documented manual verification. `make lint test` clean before merge.
- **Atomic commits.** One logical change per commit. Milestone reference in every body. Co-author trailer for AI commits.
- **Boring wins.** Three similar lines beats a premature abstraction. Concrete types until proven otherwise.

## Out of scope for phase 1

- M16 (self-update) and M17 (test environments). These are sextant agents' first tasks after switchover.
- Anything marked v2 or v3 in `specs/architecture.md`.
- Pulling patterns from `/Users/lena/dev/sextant/` (the pilot). It exists; **ignore it.** Initial is a clean ground-up implementation. No code carryover, no design carryover except what's already encoded in these specs.
- Refactoring earlier milestones beyond what a later milestone explicitly requires.
- Adding "for elegance" — boring is the goal.

## Reading order before starting M0

In order:

1. `README.md` — orientation
2. `specs/architecture.md` — every load-bearing design decision with rationale
3. `plans/bootstrap.md` — the milestone sequence; you'll come back to this for each one
4. `plans/reviews/2026-05-24-codex-adversarial.md` — what was wrong with the design and how it was fixed (so you don't repeat the patterns codex flagged)
5. `conventions/STYLE.md` — Go style
6. `conventions/git-workflow.md` — branches, commits, merges
7. `skills/sextant-bootstrap-implementer.md` — the skill file (read knowing this goal overrides its "ask operator" lines)
8. Per-milestone spec files referenced from `bootstrap.md` (read at the start of each milestone)

## Starting

**Begin with M0.** No confirmation needed. Verify the Go toolchain first per the section above, then read the order list, then start implementing.

The first commit you make should be the M0 scaffolding. The last commit you make before stopping should be `plans/phase1-complete.md` written under stop condition 1.
