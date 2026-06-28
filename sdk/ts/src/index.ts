// @sextant/sdk — the TypeScript Wire client for Sextant, a co-equal SDK peer to
// the Go SDK (ADR-0041). It connects to the one Go bus over NATS/TCP with its own
// scoped credentials, does publish / read / subscribe + the artifact CRUD and
// watch, implements its own frame codec verified against the wire conformance
// vectors, and anchors on the protocol epoch.
//
// Co-equal means: passes the wire conformance suite for the protocol epoch
// (EPOCH), not "looks like the Go output."

// Connecting + the two roles (a collaborating Client, a mint/retire Issuer). The
// Node connect() lives in connect.ts (it reads the filesystem); Client +
// connectCore are transport-agnostic in client.ts (the browser entry reuses them).
export { connect } from "./connect.js";
export { Client, connectCore, ResumeDeferredError } from "./client.js";
export type { SubOptions, Subscription, Watch } from "./client.js";
export { connectIssuer, Issuer } from "./issuer.js";

// Connection options + the bus-error type.
export type { ConnectOptions } from "./transport/conn.js";
export { BusError } from "./transport/conn.js";

// The wire codec + the protocol epoch — the co-equality surface.
export { EPOCH, EpochError, SkewError, checkEpoch, checkSkew } from "./wire/epoch.js";
export {
  type Frame,
  KIND_MESSAGE,
  KIND_ARTIFACT,
  validateFrame,
  isValidULID,
  parseULIDMillis,
} from "./wire/frame.js";
export { canonical, encode, encodeHex, decode, decodeHex, parseJSON, bytesToHex, hexToBytes } from "./wire/codec.js";

// Pure subject helpers (no I/O).
export { topicSubject, clientSubject, dmSubject, agentActivitySubject, MESSAGE_PREFIX } from "./transport/subjects.js";

// Supporting types.
export type {
  JSONValue,
  Message,
  Artifact,
  ArtifactInfo,
  ArtifactChange,
  ClientInfo,
  IssuedClient,
  HeartbeatState,
} from "./types.js";
