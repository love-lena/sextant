/**
 * @sextant/client — TypeScript client for the sextant bus.
 *
 * Mirrors the Go pkg/client API. See specs/components/client-libraries.md
 * for the canonical surface.
 */

export { Client, connect, connectWithConfig, type ConnectOptions } from "./client.js";
export {
  loadConfig,
  validateAndFill,
  defaultConfigPath,
  parseDurationMs,
  expandHome,
  type ClientConfig,
  type ClientOptionsConfig,
  type NATSConfig,
  type OperatorConfig,
  type LogLevel,
} from "./config.js";
export {
  newEnvelope,
  childEnvelope,
  validateEnvelope,
  decodeEnvelope,
  encodeEnvelope,
  formatTimestamp,
  parseTimestamp,
} from "./envelope.js";
// Wire contract constants — generated from pkg/sextantproto/schemas/wire.json
// (single source of truth; no hand-sync). See src/proto_version.ts.
export {
  PROTO_VERSION,
  WIRE_EPOCH,
  KIND_AGENT_FRAME,
  KIND_LIFECYCLE,
  KIND_AUDIT,
  KIND_TELEMETRY_SPAN,
  KIND_TELEMETRY_METRIC,
  KIND_TELEMETRY_LOG,
  KIND_USER_INPUT_REQUEST,
  KIND_USER_INPUT_RESPONSE,
  KIND_RPC_REQUEST,
  KIND_RPC_RESPONSE,
  KIND_HEARTBEAT,
  ADDRESS_AGENT,
  ADDRESS_OPERATOR,
  ADDRESS_DAEMON,
  ADDRESS_UI,
  ADDRESS_EXTERNAL,
  ADDRESS_KINDS,
  FRAME_ASSISTANT_TEXT,
  FRAME_TOOL_CALL,
  FRAME_TOOL_RESULT,
  FRAME_SYSTEM_NOTE,
  FRAME_ERROR,
} from "./proto_version.js";
export {
  ClientClosedError,
  KVCASConflictError,
  KVKeyNotFoundError,
  RPCError,
  RPCTimeoutError,
} from "./errors.js";
export type { Message, SubscribeOptions } from "./subscribe.js";
export type { RPCOptions } from "./rpc.js";
export type { QueryFilter } from "./query.js";
export type { KVEntryWithRevision, KVOp, KVUpdate } from "./kv.js";

// Re-export the generated wire types so callers don't have to dig into
// the internal path.
export type {
  Address,
  AgentDefinition,
  AgentFramePayload,
  AgentIncarnation,
  AgentStatus,
  AgentSummary,
  AuditPayload,
  Envelope,
  GetAgentStatusRequest,
  GetAgentStatusResponse,
  HeartbeatPayload,
  LifecyclePayload,
  ListAgentsFilter,
  ListAgentsRequest,
  ListAgentsResponse,
  LogRecord,
  Metric,
  QueryHistoryFilter,
  QueryHistoryRequest,
  QueryHistoryResponse,
  ReadFileRequest,
  ReadFileResponse,
  RPCError as RPCErrorPayload,
  RPCRequest,
  RPCResponse,
  RuntimeConfig,
  SandboxConfig,
  Span,
  SpanEvent,
  SpanLink,
  Timestamp,
  UUID,
  UserInputRequestPayload,
  UserInputResponsePayload,
} from "./types.generated.js";
