# @sextant/conv-workflow — the TS workflow convention

The TS peer of the workflow convention (ADR-0011, ADR-0041, TASK-239), co-equal with
`conventions/workflow/go`. A workflow is a convention over the two primitives, run by
an ordinary coordinator client; a requester (the browser dash) asks it to start a run
by publishing a `workflow.start`, and renders a run by parsing the
`sextant.workflow/v1` state envelope.

A convention is a library over the SDK (ADR-0041), never a bus feature: the start
verb reaches the bus only through the `Ops` seam (one `publish`). The emitted
`workflow.start` record matches the Go convention byte-for-byte, pinned by the SAME
conformance vector both suites replay
(`protocol/conformance/vectors/workflow/requestWorkflowStart.json`).

- `src/records.ts` — the record types (`Workflow`/`Step`/`WorkflowEvent`),
  `parseWorkflow` (the render helper the dash uses to replace its hand-rolled `$type`
  check), `nextPending`, `isTerminal`, `marshalWorkflow`, the lexicon constants.
- `src/workflow.ts` — `requestWorkflowStart` (publish a workflow.start),
  `workflowStartRecord` (the wire-record builder the dash uses to replace its
  hand-rolled literal), `parseWorkflowStartAck`, the `Ops` seam.
- `test/conformance.test.ts` — replays the language-neutral op-transcript vector the
  Go suite recorded.
