// Shared building blocks of the workflow convention in TypeScript (ADR-0011,
// ADR-0048) — the co-equal peer of conventions/workflow/go/records.go. A workflow is
// a CONVENTION over the two primitives, run by an ordinary coordinator client; the
// run-record contract lives in run.ts. The symbols here (step statuses, ack
// statuses) are shared by it and reused verbatim.

// Step statuses shared with the run contract (run.ts reuses StepDone).
export const StepRunning = "running";
export const StepDone = "done";

// Ack/request statuses shared by the run.start lexicon (run.ts).
export const StatusOK = "ok";
export const StatusError = "error";
