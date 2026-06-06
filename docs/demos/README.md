# Demos

Recorded walkthroughs of sextant, driven through the operator CLI against a
real, live bus. Each `.tape` is the source script; the `.gif` next to it is the
rendered recording. They are produced with [VHS](https://github.com/charmbracelet/vhs).

## The M2 collaboration loop (multi-pane)

![The M2 collaboration loop](m2-collaboration-loop.gif)

Two clients — `alice` and `bob` — collaborate **through** the bus, each in its own
pane, so you watch deliveries land **live**. Nothing talks directly: the bus is the
sole minter of identities (ADR-0020), stamps every frame, and derives presence from
the live connection. This is the scenario the M2 definition-of-done codes against
([`tests/e2e/m2-acceptance.md`](../../tests/e2e/m2-acceptance.md)).

```
┌ operator — issue + directory ──┬ bob — subscribe msg.topic.plan ┐
│                                │                                │
├ alice — publish + artifact ────┼ bob — watch the-plan ──────────┤
│                                │                                │
└────────────────────────────────┴────────────────────────────────┘
```

- **operator** — the bus is the sole minter; the operator issues `alice`
  (held-identity mode) and `bob` (`register --self`, bootstrap/enrollment mode),
  then lists the directory: both **online**, presence derived from the connection.
- **alice** — publishes a message, then drafts and revises the shared artifact
  `the-plan` by compare-and-set.
- **bob · subscribe** — a live `subscribe`; when `alice` publishes, the frame
  appears here authored by `alice`'s bus-minted id (she cannot stamp it with
  anyone else's — the per-client allow-list forbids it).
- **bob · watch** — a live `artifact watch`; `alice`'s create and update land here
  as `revision 1` → `revision 2` in real time.

> The full seven-step acceptance loop — including durable identity across
> reconnect and `retire` — is the executable spec and runs in CI; see
> `tests/e2e/run.sh`. The single-pane recording of the whole loop is in this
> file's git history.

## Regenerating

Needs `tmux` and the `sextant` binary on disk. Build it, then render:

```sh
go build -o /tmp/sxdemo/sextant ./cmd/sextant
vhs docs/demos/m2-collaboration-loop.tape   # from the repo root
```

The tape starts [`m2-multipane.sh`](m2-multipane.sh) (which creates the tmux
session, the bus, and the four panes, then sends timed commands to each), attaches,
and records. Everything shown being typed is a real invocation against a live bus.
