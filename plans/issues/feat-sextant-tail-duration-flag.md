---
title: sextant tail needs a --for / --duration flag for bounded subscriptions
status: open
priority: P3
created_at: 2026-05-26T15:05-07:00
labels: [feature, cli, ergonomics]
discovered_in: chat TUI Checkpoint C debugging — wanted to capture a 3-second window of agent envelopes without Ctrl+C-ing
---

## Summary

`sextant tail <subject>` runs until Ctrl+C / Ctrl+D / channel close. There's no way to say "give me 3 seconds of envelopes and exit". This is a common debugging pattern — capture a short window of events around a triggering action — and currently requires shell tricks:

```
$ sextant tail "agents.X.>" &
$ TAIL_PID=$!
$ sleep 3
$ kill $TAIL_PID
```

Add a `--for <duration>` flag (Go duration syntax: `3s`, `1m`, etc.) that exits cleanly when the timer fires.

## Why P3

This is pure ergonomics — the workaround is two extra lines of shell. But every operator hits this pattern within their first few debug sessions, and the workaround doesn't compose well with other commands.

## Implementation shape

In `cmd/sextant/tail.go`, add:

```go
var dur time.Duration
fs.DurationVar(&dur, "for", 0, "exit after this duration (e.g. 3s, 1m); 0 = run until interrupted")
```

In the subscribe loop, if `dur > 0`, wrap the parent context with `WithTimeout`. The existing select already handles `ctx.Done()` for Ctrl+C; deadline expiry hits the same path.

## Acceptance

- `TestTailExitsAfterDuration` — call `tail --for 100ms` against a quiet subject, assert exit within ~200ms.
- `--for 0` keeps current behavior (run forever).

## Related

- General "debug ergonomics" tracking — flag a meta-issue if we collect more of these.
