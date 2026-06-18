---
name: live-verify-v053
description: Prove the v0.5.3 release is OPERATIONAL on the operator's live brew setup — the final verification run AFTER `sextant update` + starting the components. Checks each goal.v0-5-3 criterion (the three runtimes ship, the dispatcher is managed + online, Mobilize spawns a live agent, a workflow runs end-to-end, the runtimes survive a bus restart, and violet is deployed online) and reports a per-criterion PASS/FAIL. Use when the operator asks to verify v0.5.3, confirm the runtimes are live, or run the v0.5.3 acceptance after upgrading.
---

# v0.5.3 live-verify — prove the runtimes are operational

This is the **final acceptance** for the v0.5.3 milestone (`goal.v0-5-3`). The
operator runs it AFTER the release is deployed on their live brew setup:

```
brew upgrade  (or `sextant update`)  →  sextant components start --all
  →  sextant secret set anthropic  →  /live-verify-v053
```

It proves six criteria are genuinely **live** — not merely merged. Some checks
the skill runs directly; two require a click on the dash, so the skill guides the
operator and then verifies the observable outcome from the bus. At the end it
prints a clear per-criterion **PASS / FAIL** summary.

> **Run this only on a deployed setup.** Criteria 2–6 need the components running
> (`sextant components start --all`) and violet keyed (`sextant secret set
> anthropic`). On a non-deployed env those checks will (correctly) FAIL — that is
> the signal to finish deploying, not a bug in the skill.

## How to run it

You — the agent — drive the underlying commands; **never hand the operator a raw
shell one-liner.** The skill ships a runner that does the automatable checks and
prints the prompts for the guided ones:

```
clients/claude-code/skills/live-verify-v053/verify.sh
```

When deployed via the plugin (brew install), the same script sits next to this
SKILL.md under the installed plugin dir. Invoke it and read its output:

1. Run `verify.sh` (no args) to run the **automated** checks (criteria 1, 2, the
   online checks, and to set up the guided ones). It prints, for each automatable
   criterion, a `PASS`/`FAIL` line with the evidence it saw.
2. For the **guided** criteria (3 Mobilize, 4 workflow) it prints the exact
   operator instruction. Relay that to the operator, wait, then verify the
   observable outcome (below) yourself before marking PASS.
3. Re-run individual checks with `verify.sh <check>` (e.g. `verify.sh dispatcher`,
   `verify.sh restart`) — see `verify.sh --help`.
4. Self-check: `verify.sh --self-test` runs the script's own structure/dry-run
   validation (no live bus needed) so the skill can be sanity-checked in CI or on
   a fresh checkout.

The script resolves `sextant` from PATH and the live store/context the operator's
bus is on (it does NOT pin a test store — this verifies the REAL setup).

## The six criteria

### 1 — dispatcher-ships  (AUTOMATED)
The three agent runtimes installed onto PATH by the brew formula.

**Check:** `which sextant-dispatch sextant-violet sextant-workflow` — all three
resolve. **PASS** when all three are on PATH; **FAIL** naming any that are
`MISSING` (the operator hasn't upgraded, or the formula didn't ship them).

### 2 — dispatcher-managed  (AUTOMATED)
The dispatcher runs as an OS-managed service AND is present on the bus, so
clicking Mobilize never hits "no dispatcher listening" on a default setup.

**Check:** `sextant components status` shows the dispatcher `loaded + RUNNING`
(launchd), and `sextant clients list` shows the dispatcher **online** on the bus.
**PASS** when both hold; **FAIL** with the offending line (e.g. `service: loaded
but NOT running` → `sextant components restart dispatcher`).

### 3 — mobilize-end-to-end-live  (GUIDED)
A click on the live dash spawns a real-identity agent that joins the bus and is
DM-able.

**Guide:** tell the operator to open the dash (`sextant dash url`) and click
**Mobilize**. **Verify:** snapshot `sextant clients list` before and after — a
**new** client (kind `agent`) appears and shows online. Then DM it (publish to its
inbox / DM subject) and confirm a reply lands. **PASS** when a new agent appears,
is online, and answers a DM.

### 4 — workflow-run-live  (GUIDED)
Starting a workflow from the dash runs it end-to-end (running → done) with a fast
ok-ack — no false "no runner" timeout.

**Guide:** tell the operator to open the dash Workflow page and **Start a
workflow** from a prompt. **Verify:** the start is ack'd quickly, then the run
moves `running → done` (track the `workflow.<id>` events / the dash run view).
**PASS** when the run reaches `done` without a "no runner" / consumer timeout;
**FAIL** if it times out or never leaves `running`.

### 5 — survives-restart  (AUTOMATED + confirm)
After a bus restart, the dispatcher and a spawned agent reconnect on the same port
and keep working — no strand.

**Check:** the runner records the bus URL + the online runtimes, restarts the bus
(`brew services restart sextant`), waits for it to come back, and re-checks: the
recorded port is unchanged, `sextant components status` still shows the dispatcher
running, and the previously-online runtimes are online again in `sextant clients
list`. **PASS** when the port is unchanged and the runtimes reconnect; **FAIL** on
a port change or a runtime that doesn't return.

> Restarting the bus is disruptive to anything live on it — the runner **asks the
> operator to confirm** before restarting (warn before killing a live bus).

### 6 — violet-deployed-online  (AUTOMATED + guided FAB)
Violet — the operator's assistant — is keyed, running, online, answers a DM, and
its dash FAB is a live surface.

**Check:** the violet key is set (`sextant secret set anthropic` done — the runner
checks the violet env/component, never prints the key), `sextant components status`
shows violet running, and `sextant clients list` shows violet **online**. Then DM
violet and confirm a reply. **Guide:** ask the operator to open the dash and use
the Assistant **FAB** — confirm it responds (not a dead stub). **PASS** when violet
is online, answers a DM, and the FAB responds.

## Reporting

End with a per-criterion table the operator can read at a glance:

```
v0.5.3 live-verify — goal.v0-5-3
  1 dispatcher-ships .......... PASS  (3/3 runtimes on PATH)
  2 dispatcher-managed ........ PASS  (running + online)
  3 mobilize-end-to-end-live .. PASS  (agent <id> online, answered DM)
  4 workflow-run-live ......... PASS  (run <id> reached done in 9s)
  5 survives-restart .......... PASS  (port 4222 unchanged, both reconnected)
  6 violet-deployed-online .... PASS  (online, answered DM, FAB live)
  → 6/6 green: goal.v0-5-3 met
```

State the overall verdict plainly. If any criterion is FAIL, name it, show the
evidence, and give the one corrective command (it is in the per-criterion section
above). Do not soften a FAIL — a partial deploy reads as red until every criterion
is green.

## Boundaries

- **Verifies the real setup.** No test store, no fake bus — it resolves the
  operator's live `sextant` + context. The whole point is that it ran against the
  thing the operator actually uses.
- **Read-mostly.** The only mutating action is the criterion-5 bus restart, which
  is gated behind an explicit operator confirmation. It never spawns agents on the
  operator's behalf, never writes a verdict, never tags or merges.
- **The dash clicks are the operator's.** Criteria 3 and 4 are deliberately
  operator-driven — the skill guides and then *observes*; it does not click for
  them.
