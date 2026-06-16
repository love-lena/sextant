---
name: spike
description: Run a research spike over the sextant bus. A Claude agent researches the question and writes a report artifact, then a gpt-5.5 agent rewrites it from scratch into a second artifact — so you can compare the two. Use when the user types /spike, says "research spike", "spike this", or asks to research a question and get a written report back.
---

# Research spike

Kicks off the **research-spike** workflow (`docs/demos/research-spike-workflow.sh`):
a Claude worker researches the question → writes the `research-report` artifact;
then a **gpt-5.5** worker reads that report + the question and rewrites it from
scratch → the `research-report-gpt5` artifact. Two independent reports on the
bus to compare.

## How to run

The question is the skill's argument (everything after `/spike`). If it's empty,
ask the operator for the research question first.

Run the workflow against the operator's **live bus** (the same store the dash
serves), in the background — it spawns real `claude` + `codex` workers and takes
a few minutes:

```
SEXTANT_STORE="$HOME/Library/Application Support/sextant/jetstream" \
  docs/demos/research-spike-workflow.sh run "<the question>"
```

Run it with `run_in_background: true` (it's long-running). It needs `claude`,
`codex`, and `sextant`/`sextant-mcp` on PATH. The live run is **operator-driven**
— the safety classifier blocks an unattended session from launching the spawning
workers, so this works when *you* (the operator) invoke `/spike`, not from an
unattended agent. Overrides: `WF_CODEX_MODEL` (default `gpt-5.5`),
`WF_CLAUDE_MODEL` (default `claude-haiku-4-5`).

## When it finishes

Point the operator at the two artifacts to compare (open them in the dash, or
`sextant artifact get research-report` / `research-report-gpt5`):
- **`research-report`** — Claude's research write-up.
- **`research-report-gpt5`** — gpt-5.5's independent from-scratch rewrite.

Both land on the live bus, so they appear in the dash's Artifacts panel.

## Notes
- It's reusable: run `/spike "<any question>"` again for a new topic; each run
  overwrites the two report artifacts (the workflow is a spike, not an archive —
  copy a report elsewhere if you want to keep it before the next run).
- The workflow has no human gate — it just produces the two artifacts.
