---
title: natsboot-backed integration tests fail with "nats: connection closed" inside subscription handler
status: open
priority: P3
created_at: 2026-05-26T22:00-07:00
labels: [bug, test, natsboot, jetstream, observability, needs-input]
discovered_in: writing TestLifecycleWatcherUpdatesAgentRecord — the natsboot harness boots, the test's own Put succeeds, but the watcher's NATS callback handler hits `nats: connection closed` on its Get against the same `defs jetstream.KeyValue` handle
---

## Needs Lena's input

The ticket lists four hypotheses for the failure mode — JetStream-from-NATS-dispatcher reentry, ctx capture in the JS KV handle, auth flap, concurrent goroutine. Picking which to investigate first (or whether to dodge the issue with a different harness shape) is a judgment call on engineering time vs. test-coverage value; the lifecycle watcher's behavior is already verified via the fake-KV unit test in `bug-agents-list-stale-lifecycle`.

## Summary

A test harness pattern that's intuitive — spin up a `natsboot.Server`, Connect once, run `natsboot.Bootstrap`, open a `jetstream.KeyValue` against `agent_definitions`, seed a record, publish an envelope, wait — fails when the **NATS subscription callback** tries to read the same KV. The test's foreground Put succeeds; the callback's Get fails with `nats: connection closed`. There's no explicit close in flight.

Concrete repro: the original test harness in the now-superseded `pkg/sextantd/lifecycle_watcher_test.go` (see git log on `feat/agents-list-lifecycle` and `feat/agents-list-lifecycle-v2` for the variant that exhibited this).

```
2026/05/26 21:57:45 sextantd: lifecycle watcher: apply <uuid>/ended: get <uuid>: nats: connection closed
    lifecycle_watcher_test.go: timeout: Lifecycle = "" after 2s, want "ended"
```

The mapping unit test (`TestMapLifecycleTransitionExhaustive`) — which doesn't touch NATS — passes. Only the natsboot-backed tests fail. Failure is deterministic (every run, every subtest).

## Why P3

The watcher itself was verified via a fake-KV unit test in [[bug-agents-list-stale-lifecycle]]'s resolution; functionality is shipped. This ticket exists so the harness gap doesn't silently propagate to other ticket attempts — anyone reaching for `natsboot.Start + Bootstrap + jetstream.New + KeyValue + NATS subscription` should know it's currently broken in this combination.

## Hypotheses to investigate

1. **JetStream context vs core NATS subscription on the same conn.** The watcher's `nc.Subscribe` runs callbacks on the NATS dispatcher goroutine. Inside that goroutine, calling `defs.Get(ctx, key)` (where `defs` is the JetStream KV handle) issues a JetStream API request. Maybe there's a deadlock or reentry issue when the JS API request is issued from the same connection's dispatcher.
2. **Bootstrap context lifecycle.** `startWatcherHarness` runs `natsboot.Bootstrap` with a 30s context, then immediately returns (canceling the ctx via deferred cancel). If the JS KV handle captures that ctx anywhere, post-return calls would see a canceled ctx and might surface as "connection closed".
3. **Operator auth dropping after Bootstrap.** `srv.Connect()` uses `nats.UserInfo(operator, …)`. If Bootstrap's calls happen on a separate auth path and the subscription's conn loses the auth context, subsequent JS calls would close.
4. **Concurrent goroutine racing the connection.** Some background goroutine in the test (`t.Cleanup`?) closing nc before the test body completes.

## Fix shape

Probably (1) — switch the subscription to a goroutine that's not the NATS dispatcher (use a buffered channel from `Subscribe` and process off-goroutine), or use `jetstream.SubscribeSync` / pull subscription pattern. Investigate `nats.go`'s reentry semantics for JS-from-callback.

OR: wire a different test harness — `nats-server` via a different setup, or test against the existing `cmd/sextantd/sextantd_test.go` daemon harness which does work.

## Acceptance

- A natsboot-backed test fixture exists in `pkg/sextantd/` (or `pkg/testharness/` if extracted) that:
  - Boots nats-server.
  - Opens a JS KV.
  - Subscribes a callback that reads the JS KV.
  - Verifies the callback's reads succeed.
- The lifecycle watcher's integration test (revived from `feat/agents-list-lifecycle-v2`) passes against this fixture.

## Related

- [[bug-agents-list-stale-lifecycle]] — shipped via mock-based test because of this gap.
- `pkg/natsboot/` — the bootstrap surface this harness builds on.
- `cmd/sextantd/sextantd_test.go` — daemon-level test that doesn't hit this issue (uses the full daemon Start path).
