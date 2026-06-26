// @sextant/conv-spawn — the spawn convention in TypeScript, a co-equal peer of
// conventions/spawn/go (ADR-0041, TASK-239). A spawn.request asks a dispatcher to
// spawn a new client; it carries data only, never a command to run. The verb LOGIC
// is hand-written (concept, not codegen) and reaches the bus only through the Ops
// seam; the emitted record matches the Go convention byte-for-byte, pinned by the
// shared conformance vector under protocol/conformance/vectors/spawn.

export {
  type Ops,
  type SpawnRequest,
  type SpawnAck,
  RequestSubject,
  StatusOK,
  StatusError,
  spawnRequestRecord,
  requestSpawn,
  parseSpawnAck,
} from "./spawn.js";
