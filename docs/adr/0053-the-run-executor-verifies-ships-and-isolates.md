---
status: accepted
signed_off_by: lena
date: 2026-06-30
---

# The run executor verifies the deliverable, ships it on a trusted path, and isolates each run

[ADR-0051](0051-the-run-executor.md) made the coordinator the single writer that
adopts a run and walks its steps by kind; [ADR-0052](0052-the-work-engine-harness-is-pi.md)
made pi the dispatched worker. This ADR records what the run executor grew into
once a managed run had to produce a *trustworthy, shipped* deliverable: it gains an
independent **verify** step, a host-side **pr-open** step that ships on a trusted
path, a per-run isolated **worktree**, and durable `run.start` adoption. Each is
additive — the existing `work` / `checkpoint` / `brief` kinds are unchanged.

**Verify — an independent check that can report blocked.** A new step kind `verify`
(`KindVerify`) dispatches a SEPARATE worker — a producer cannot verify itself —
charged with rigorously verifying the run's deliverable: fetch the prior steps' real
artifacts, BUILD the change and RUN the relevant tests, check EACH acceptance
criterion as a property with an adversarial/negative case, and report HONESTLY. The
step reports its result on the same `run.event` step-done channel: `outcome` is
`done` only if every AC is met AND the build/tests are green, else `blocked` with a
one-line `reason` and a verdict artifact enumerating what failed. A `verify` step is
placed BEFORE a `brief` so a run cannot reach `done` over a failed verification — a
blocked verify drives the run to `blocked` like any failed step.

**PR-open — ship on the host-side trusted path.** A new step kind `pr-open`
(`KindPROpen`) is NOT a dispatched worker. The sandboxed pi worker's egress is
walled to the model API (github.com is denied) and it is never handed git/gh
credentials, so it CANNOT push or open a PR. Instead the coordinator — a managed
host-side Go service running with the operator's ambient git/gh auth — runs this step
itself against the run's isolated worktree: it commits the worktree's pending changes
on the run-namespaced branch `sxrun/<runID>`, pushes that branch to origin (scoped to
`sxrun/<runID>`, NEVER a force-push to a shared branch), opens a PR against the run's
base ref, and records the resulting PR URL as the step's produced artifact. It is
placed AFTER verify/brief so a run only opens a PR for a verified deliverable. This
is the trust split: the credential-free worker proposes the change; the trusted
coordinator ships it.

**Repo isolation — one worktree per run.** A run carries `Run.Repo` (the absolute
path to its git repository) and optional `Run.RepoRef` (the base ref to branch from;
HEAD when empty). When `Repo` is set, the coordinator provisions ONE isolated git
worktree per run — a fresh branch `sxrun/<id>` off `RepoRef` — runs every step inside
it (threaded to the worker as its workdir), and tears it down when the run goes
terminal. `Repo`/`RepoRef` come from the RUN/TEMPLATE definition, never an
operator-set env var, so a request cannot point a worker at an arbitrary checkout.
Omitted = no provisioning (the worker falls back to the recipe's scratch default —
today's behaviour for repo-less runs). Concurrent runs never share a checkout, and
the pr-open step has a clean, run-scoped branch to push.

**Durable run.start adoption.** A `run.start{id}` published while no coordinator is
listening is durably replayed on (re)subscribe (`DeliverAll`) and adopted, not lost;
an idempotent guard keyed on the durable run envelope keeps replay from re-running
finished work (a CAS on the single-writer envelope — two coordinators racing an
adoption cannot double-dispatch). This is the anti-crash-loop discipline living in
the guard rather than in dropping history.

All of this composes with the locked core untouched: the coordinator is an ordinary
bus client, the verdict and PR-URL are regular Artifacts, and `verify`/`pr-open` are
conventions over Messages + Artifacts, co-equal in Go and TS, evolving by `$type`
version — no epoch bump. It **extends [ADR-0051](0051-the-run-executor.md)** (the
executor now verifies, ships, and isolates, beyond walking work/checkpoint/brief) and
composes with **[ADR-0052](0052-the-work-engine-harness-is-pi.md)** (pi is the
dispatched worker for `work` and `verify`; the host-side coordinator, not pi, runs
`pr-open`).
