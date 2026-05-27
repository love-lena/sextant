# Sextant principles

These are load-bearing values, not conventions. They constrain every
decision sextant makes; conventions are how we implement them. Three
items, each non-negotiable.

## Rebuilding must be easy and fast

A single command, end to end. No "run this, then that, then verify
this." No multi-line copy-paste install sequences. If the inner loop
is slow or fiddly, the team works around it and the loop calcifies.

Optimize for the case of "rebuild and verify in under 30 seconds." When
adding a new component that requires a build step, hide it behind the
existing entry point (`make install`, `make test`, `make lint`) — don't
ask anyone to memorize a new invocation.

If you're about to ship something that requires a sequence of commands
to validate, stop and wrap it in a single `make` target first.

## User ergonomics is a first-class deliverable

Operator-facing surfaces — CLI, TUI, error messages, help text,
diagnostic output — are not polish to add later. Treat ergonomic gaps
the same way as correctness bugs: file them, fix them, hold the bar.

Concretely:

- **Error messages should suggest the next command**, not just describe
  the failure. `ask: timeout (waited 10s)` is useless; `ask: agent has
  lifecycle=ended; restart with sextant agents restart X` is useful.
- **Defaults should match what the operator wants 90% of the time.**
  The default of `sextant conversation` opens the chat TUI, not the
  NDJSON streamer — because that's what the operator wants 90% of the
  time. `--json` is the opt-out for the other 10%.
- **"It works but you have to know" is a failure mode**, not an
  acceptable steady state. If diagnosis requires institutional
  knowledge, the diagnostic surface is incomplete.

## Agents must get human input on visual design early

When an agent is building a visual surface (TUI, CLI output rendering,
anything an operator will look at), ship a runnable mockup at the
earliest viable point. Iterate against the human's eye, not against a
spec. Specs describe structure; structure is not the same as design.

The cost of late visual feedback compounds. Each subsequent layer —
navigation, modal state, network plumbing — gets built on the wrong
visual foundation and has to be partially redone when the design
direction lands.

Lock visual direction within the first viable render, not after the
third rewrite. The runnable mockup is the contract; the spec is the
scaffold.
