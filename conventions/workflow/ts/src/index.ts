// @sextant/conv-workflow — the workflow convention in TypeScript, a co-equal peer of
// conventions/workflow/go (ADR-0041, TASK-239). A workflow is a convention over the
// two primitives, run by an ordinary coordinator client; a requester (the browser
// dash) asks it to start a run by publishing a workflow.start, and renders a run by
// parsing the sextant.workflow/v1 state envelope. The verb LOGIC is hand-written
// (concept, not codegen); the emitted workflow.start record matches the Go
// convention byte-for-byte, pinned by the shared conformance vector under
// protocol/conformance/vectors/workflow.

// The record types + parse/marshal + walk helpers + constants.
export {
  type Step,
  type Workflow,
  type WorkflowEvent,
  KindWorkflow,
  TypeWorkflowEvent,
  TypeWorkflowControl,
  TypeWorkflowStart,
  TypeWorkflowStartAck,
  WfRunning,
  WfPaused,
  WfDone,
  WfCancelled,
  WfFailed,
  StepPending,
  StepRunning,
  StepDone,
  StepFailed,
  StatusOK,
  StatusError,
  isTerminal,
  nextPending,
  marshalWorkflow,
  parseWorkflow,
  parseWorkflowEvent,
} from "./records.js";

// The start verb + its seam, the request/ack shapes, the builder the dash uses.
export {
  type Ops,
  type WorkflowStartRequest,
  type WorkflowStartAck,
  StartSubject,
  workflowStartRecord,
  requestWorkflowStart,
  parseWorkflowStartAck,
} from "./workflow.js";

// The run contract (ADR-0048): the run/template/event/control/start shapes + the
// run.start verb, the co-equal peer of conventions/workflow/go/run.go. Additive
// alongside the older sextant.workflow/v1 exports above (the coordinator retarget
// that consumes these is a following pass).
export {
  type RunStep,
  type RelatesLink,
  type ActivityEntry,
  type ProducedArtifact,
  type Run,
  type RunEvent,
  type RunControl,
  type RunStartRequest,
  type RunStartAck,
  type Template,
  KindRun,
  KindTemplate,
  TypeRunEvent,
  TypeRunControl,
  TypeRunStart,
  TypeRunStartAck,
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
  runStateName,
  runEventsSubject,
  runControlSubject,
  runStartRecord,
} from "./run.js";
