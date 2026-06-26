# review/go — the Go review convention

The Go peer of the review convention (ADR-0044, TASK-239), co-equal with
`conventions/review/ts`. An artifact carries a review-state as a `review` block in
its record; the operator's verdict is persisted by a read-merge-CAS, and on approve
a closed loop advances any proof-related goal criteria to met via the sibling goals
convention (`conventions/goal/go`). A headless Go agent runs the SAME logic the
browser dash runs in TS.

A convention is a library over the SDK (ADR-0041), never a bus feature: the verb
logic reaches the bus only through the consumer-declared `Ops` seam
(get / compare-and-set / publish). `imports_test.go` pins the closure to the SDK,
the protocol bindings, and the goals convention — never the bus.

- `review.go` — the convention: `SetReview`, `MergeReview`, `closeLoop`, `Read`,
  `IsReviewState`, the `Ops` seam, the `ReviewBlock`/`SetReviewInput`/`SetReviewResult`
  shapes.
- `conformance_test.go` — replays the language-neutral op-transcript vectors under
  `protocol/conformance/vectors/review/` (the SAME files `conventions/review/ts`
  replays); `-update` re-records them.
- The live cross-language coequality proof lives on the TS side
  (`conventions/review/ts/test/coequality.test.ts`), driving this Go convention via
  `conventions/review/ts/test/gohelper`.
