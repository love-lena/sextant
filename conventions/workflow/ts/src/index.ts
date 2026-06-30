// @sextant/conv-workflow — the workflow convention in TypeScript, a co-equal peer of
// conventions/workflow/go (ADR-0041, ADR-0048). A workflow is a convention over the
// two primitives, run by an ordinary coordinator client; a requester (the browser
// dash) asks it to adopt a run by publishing a run.start, and renders a run by
// parsing the sextant.workflow.run/v1 state envelope. The verb LOGIC is hand-written
// (concept, not codegen); the emitted records match the Go convention byte-for-byte.

// Shared building blocks: step statuses + ack statuses.
export { StepRunning, StepDone, StatusOK, StatusError } from "./records.js";

// The run contract (ADR-0048): the run/template/event/control/start shapes + the
// run.start verb, the co-equal peer of conventions/workflow/go/run.go.
export {
  type RunStep,
  type RelatesLink,
  type ActivityEntry,
  type ProducedArtifact,
  type Run,
  type RunEvent,
  type RunControl,
  type RunReview,
  type RunDecision,
  type RunStartRequest,
  type RunStartAck,
  type Template,
  KindRun,
  KindTemplate,
  TypeRunEvent,
  TypeRunControl,
  TypeRunReview,
  TypeRunDecision,
  TypeRunStart,
  TypeRunStartAck,
  DecisionAdvance,
  DecisionRedo,
  DecisionEdit,
  DecisionStop,
  isDecisionVerb,
  RunRunning,
  RunWaiting,
  RunBlocked,
  RunDone,
  RunCancelled,
  StepUpcoming,
  StepWaiting,
  KindWork,
  KindCheckpoint,
  KindBrief,
  RunStartSubject,
  isTerminalRun,
  nextPendingRun,
  marshalRun,
  parseRun,
  marshalRunEvent,
  parseRunEvent,
  parseRunControl,
  marshalRunReview,
  parseRunReview,
  marshalRunDecision,
  parseRunDecision,
  runStateName,
  runEventsSubject,
  runControlSubject,
  runReviewSubject,
  runDecisionSubject,
  runStartRecord,
} from "./run.js";
