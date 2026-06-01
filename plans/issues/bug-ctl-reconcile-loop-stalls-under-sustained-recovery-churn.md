---
title:          Reconcile loop stops making progress under sustained recovery churn (crash-loop + wedged-liveness e2e)
status:         open
priority:       P1
created_at:     2026-06-01T14:10:00-07:00
labels:         [bug, ctl, reconcile, recovery, e2e, needs-input]
discovered_in:  control-plane docker e2e green-up (fix/ctl-e2e-greenup)
---

Two P1-recovery acceptance e2e — `TestRecovery_E2E_CrashLoopTripsBudgetToTerminal`
and `TestRecovery_E2E_WedgedAgentLivenessRestart` (both in `cmd/sextantd/`) —
fail on a real OrbStack host because **the daemon's reconcile loop stops making
forward progress ~2 minutes into a sustained recovery scenario**. The reconcile
*logic* is correct; the loop just stalls before it can converge.

This is filed distinct from the `$JS.ACK` / `$KV.agent_definitions` front-door
perms gaps and the sidecar-shutdown hang (all fixed on `fix/ctl-e2e-greenup`);
those were the OTHER root causes the same e2e suite surfaced. See
[[feat-ctl-p1-recovery]] for the intended behavior.

## Symptoms

- **Crash-loop test**: the operator's `get_agent_status` poll (the test's loop
  body) times out after ~133s. With the client retrying for up to 200s the RPC
  outage *outlasts the retry budget* — the daemon stops servicing the request
  for >3 minutes. The reconcile recovery itself was observed working up to that
  point (restarts 1→3, `crash_window.count` 0→2 climbing toward the budget of
  5), so the budget would trip if the loop kept running.
- **Wedged-liveness test**: with a *correct* wedge (see "Test-mechanism note"
  below) the liveness probe accumulates failures `0→1` over two 45s sweeps,
  then the **periodic sweep ticker stops firing entirely** — no third sweep, no
  further reconcile passes — so it never reaches the 3rd consecutive failure
  that trips the restart. The wedged container is never restarted within 4
  minutes.

## What the instrumented runs proved

Per-pass logging in the reconcile loop showed, in BOTH tests, that after
~90–130s:

- The reconcile **sweep ticker** (`Reconciler.sweepLoop`, a plain 45s
  `time.Ticker`) stops emitting ticks. In the wedge run it ticked at +45s and
  +90s, then never again (the +135s tick that would have set
  `LivenessFailed=true` never happened).
- The single reconcile **worker** (`Reconciler.Run` → `processOne`) likewise
  goes silent — its last `processOne` returns cleanly, then no more.
- The RPC handler path is *fast* whenever it runs (`get_agent_status` services
  in ~250µs) — until it abruptly stops *receiving* requests (the server-side
  subscription stops being delivered messages).
- The daemon's NATS connection reports **healthy throughout**: `connected=true`,
  `reconnects=0`, no `DisconnectErrHandler`, no slow-consumer async error. So
  it is NOT a NATS reconnect/slow-consumer drop that the existing handlers
  would surface.

The shared shape — sweep ticker + worker + RPC-receive all going quiet at once,
with the connection nominally healthy — points at a **daemon-wide
goroutine-progress halt** rather than any single blocking call we could see, OR
at host-level degradation (OrbStack under sustained container create/kill/
auto-remove churn, plus a long-lived `docker events` stream and a wedged
PID-1 container) starving the daemon + its embedded NATS subprocess.

## Hypotheses to chase (not yet confirmed)

1. **Host/OrbStack saturation.** The anti-stall note in the harness already
   documents OrbStack exhaustion under heavy docker load causing flaky stalls.
   These two tests are by far the most docker-churn-intensive in the suite. If
   this is the cause, the fix is environmental (resource headroom / serialized
   docker ops) and/or making the daemon's NATS+reconcile resilient to a
   transient host stall, not a logic change.
2. **A blocking docker call on the reconcile worker that backpressures the
   loop.** A wedged container (PID 1 SIGSTOPped) makes `docker stop` wait the
   full SIGTERM grace before SIGKILL; the crash loop creates/kills containers
   continuously. If a docker API call blocks longer than expected (or the
   shared docker client's connection pool stalls), the single worker can't
   advance — but the `sweepLoop` ticker stopping too is not explained by the
   worker alone.
3. **The embedded NATS server subprocess being scheduled-out under load**, so
   JetStream/KV operations (`ListKeys` in `sweep`, `KV.Get` in
   `get_agent_status`) and core-sub delivery all stall together. Consistent
   with "connection healthy, no disconnect, everything just slow/stopped."

## Acceptance

- Both e2e go green on a real docker host: the crash budget trips to terminal
  `crashed` (crash-loop) and the wedged agent is restarted by liveness (wedge),
  within their deadlines, with the reconcile loop demonstrably still ticking
  throughout.
- If the root cause is environmental, document the resource floor the suite
  needs and/or add daemon-side resilience (e.g. a watchdog that detects a
  stalled reconcile loop), and re-derive the test timeouts from the real
  sweep-gated liveness floor (~3–4 × `SweepInterval`).

## Test-mechanism note (already understood; fold into the fix)

The wedge test as written wedges with an in-container `kill -STOP 1`, which is
a **no-op**: the sidecar runs `node` as PID 1 (the entrypoint `exec`s it for
clean SIGTERM handling), and the kernel shields a PID-namespace init from a
SIGSTOP delivered from *inside* its own namespace — so node keeps heartbeating
and liveness correctly never fires. The working wedge is
`docker kill --signal=STOP <container>` (delivered by the docker daemon, an
ancestor namespace): PID 1 goes to state `T`, heartbeats stop, and the
container State stays `running` (unlike `docker pause`, which sets
`State.Paused` and would read as not-running). The test must also wait for the
agent to heartbeat ONCE before wedging — the liveness probe deliberately does
not fault an agent that has never beat. These test corrections are necessary
but NOT sufficient: even with a correct wedge the loop-stall above still blocks
the restart. Both belong in the same fix.
