# @sextant/conv-goals — the goals convention in TypeScript

The goals convention in TypeScript, a **co-equal** peer of
[`conventions/goal/go`](../../../go/conventions/goals)
([ADR-0041](../../../../docs/adr/0041-clients-are-co-equal-across-languages.md),
TASK-175). Two languages, one lexicon contract, identical wire behaviour — this
package is the proof that the protocol is language-neutral.

A **goal** is a shared objective: a north-star sentence plus the acceptance
criteria that define "done", published as the latest-value artifact `goal.<id>`,
with transitions announced on `msg.topic.goals` as `goal.update` messages
(ADR-0035). The goal's status is **derived** from the criteria rollup, never
stored.

## What's here

| File | What |
|---|---|
| `src/goals.ts` | The write verb `setCriterion` (get → compare-and-set → publish `goal.update`), the `Ops` seam, status constants, the `SetCriterionError` step discriminant. **Hand-written** (concept, not codegen). |
| `src/read.ts` | The read side: parse a goal, the proof-filter (`effectiveStatus`/`criterionMet`: met needs ≥1 proof relation), the derived `rollup`. Hand-written. |
| `src/goal_gen.ts` | The record types (`Goal`, `Criterion`), **generated** from `protocol/lexicons/goal.json`. DO NOT hand-edit. |
| `tools/lexgen.ts` | The lexicon → TS type generator (the peer of the Go `lexgen`). |

The split is the bright line: **only the record *types* are generated; the verb
*logic* is hand-written** per language (ADR-0041).

## A convention is a library over the SDK

`@sextant/conv-goals` depends on [`@sextant/sdk`](../../sdk) and reaches the bus
only through the small `Ops` seam (`getArtifact` / `updateArtifact` / `publish`)
— the SDK's `Client` satisfies it structurally, and the conformance `Recorder`
implements the same shape. A verb never knows whether it is running live or being
recorded. The convention is never a bus feature.

## Generating the record types

```sh
npm run generate     # node tools/lexgen.ts → rewrites src/goal_gen.ts
```

Run it after editing `protocol/lexicons/goal.json`. A drift guard
(`test/lexgen.test.ts`) fails CI if the committed `src/goal_gen.ts` is not exactly
what `lexgen` produces — the generated types cannot silently drift from the
contract, and the generated file must never be hand-edited.

## Tests — the co-equality proof

```sh
npm run build            # tsc → dist/
npm test                 # unit + conformance + drift guard + the live scenario
npm run test:conformance # the no-bus subset (unit + op-transcript + drift guard)
npm run test:coequality  # just the live cross-language scenario (needs Go)
```

Three layers, in increasing strength:

1. **Unit** (`test/goals.test.ts`) — the verb's behaviour (idempotence, the absent-
   criterion no-op, the error steps) and the read-side proof-filter / rollup, the
   peer of the Go suite's `goals_test.go`.
2. **Op-transcript conformance** (`test/conformance.test.ts`) — runs the TS verb
   against a recording client (`test/recorder.ts`) and asserts the captured
   primitive operations equal the **same** language-neutral vector
   (`protocol/conformance/vectors/goals/setCriterion.json`) the Go suite passes,
   under the **same** canonical-JSON rule (`canonical` from `@sextant/sdk`). The
   vectors are read from the protocol tree — never copied. This is what makes the
   TS goals convention co-equal at the op level (FORMAT.md).
3. **The live scenario** (`test/coequality.test.ts`) — on **one real bus**, the TS
   convention and the Go convention (driven by `test/gohelper`) write/read the
   **same** `goal.<id>` artifact, and the record shapes are asserted
   **byte-identical** in both directions. Not two suites independently green — one
   bus, one record, two languages. Gated on the Go toolchain (skip-with-reason when
   `go` is not on PATH).

The integration/live tests spawn the real Go `sextant` binary; the cheap unit +
conformance + drift tests need no bus and run everywhere.
