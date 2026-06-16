# Agentic dev workflow — v0.5 VARIANT deltas (read after the generic playbook)

You are running the **v0.5 variant** of the agentic dev workflow. Everything in the
generic orchestrator playbook still applies — same step schema, same helpers, same
loop/gate semantics, same hard guardrails. This file states only **what differs** for the
v0.5 variant. Where the two conflict, *these deltas win*.

The variant exists so the workflow can self-drive **v0.5 work** with the **pseudo-operator
(sirius)** as the routine gate, feeding the v0.5 integration branch — while the **real
principal (Lena)** keeps the dangerous decisions (the v0.5→main gate, tags) for herself.

## 1. Base + PR target = v0.5 (never main)

- Your worktree is branched off **`origin/v0.5`** (the harness set `WF_BASE=origin/v0.5`).
  All work lands on this feature branch, on top of v0.5.
- The **release step opens the PR to v0.5**: run
  `wf-release-pr pr create --base v0.5 --title "<title>" --body "<from the brief>"`.
  (`wf-release-pr` also defaults `--base` to v0.5 under this variant, but pass it
  explicitly anyway — be unambiguous.) Never open a PR to main from this variant.

## 2. The routine gate pings the PSEUDO-OPERATOR (sirius), not the principal

- For this variant the harness points **`WF_DM` at sirius's DM** (the pseudo-operator),
  not the principal's. So when you reach the **gate step**, `wf-dm` the gate as usual —
  it goes to **sirius** (or orion). Sirius reviews the brief + diff and replies
  `approve` / `changes <feedback>` on `msg.workflow.<id>.control`, exactly as the generic
  gate describes. Nothing else about the gate mechanism changes.
- Sirius's authority here is **scoped to opening a v0.5 PR** (and, separately and outside
  this workflow, merging that PR to v0.5). That's the whole of the pseudo-operator's remit.

## 3. The workflow OPENS the PR; the pseudo-operator MERGES it (separately)

- You **only ever open a PR to v0.5.** You **never merge** — the `gh`/`git` shims hard-refuse
  `gh … merge`, push to main/master, force-push, and tag (exit 3), for you *and* your
  workers. That barrier is unchanged; do not try to route around it.
- **Sirius merges the open PR to v0.5** as a separate action, after their review — outside
  this workflow, under sirius's own v0.5 authority. Your job ends at "PR opened to v0.5 +
  `wf-event \"DONE: <PR url>\"`". Do not wait for or attempt the merge.

## 4. Dangerous / irreversible → escalate to the REAL principal (Lena), NEVER sirius

The pseudo-operator's authority is **only** the routine v0.5-PR-open gate. If a step would
do anything **dangerous or irreversible**, do NOT do it, and do NOT take it to sirius —
open a **separate escalation gate to the real principal** and stop:

- merge to **main**; create a **tag** / release; **force-push** or **history rewrite**;
  touch **another repo**; anything **destructive** (delete data/branches); anything
  involving **credentials**.

To escalate: `wf-dm-principal "<headline: what + why it needs the principal>"` (this DMs
the REAL principal, distinct from the routine sirius gate), set the gate step
`awaiting-approval`, and **YIELD**. Only the real principal's `approve` may clear an
escalation gate. The principal keeps the **v0.5→main gate and the release tag** — those are
hers, never yours and never sirius's.

If you're unsure whether something is "routine v0.5-PR-open" vs "dangerous" — treat it as
dangerous and escalate to the principal. Opening a PR to v0.5 is the *most* this variant
ever does on its own authority.

## Quick reference (variant vs generic)

| thing                | generic (run)        | v0.5 variant (run-v05)                          |
|----------------------|----------------------|-------------------------------------------------|
| base / worktree      | `origin/main`        | `origin/v0.5`                                   |
| PR base              | (gh default)         | `v0.5` (`--base v0.5`)                           |
| routine gate (WF_DM) | the principal        | the **pseudo-operator** (sirius / orion)        |
| who merges           | the principal        | **sirius**, separately (you never merge)        |
| escalation gate      | the principal        | the **real principal** (`wf-dm-principal`)      |
| merge / push / tag   | shims refuse         | shims refuse (unchanged)                        |
