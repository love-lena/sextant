# @sextant/conv-spawn — the TS spawn convention

The TS peer of the spawn convention (ADR-0041, TASK-239), co-equal with
`conventions/spawn/go`. A `spawn.request` is an ordinary message record asking a
dispatcher to spawn a new client; it carries data only, never a command to run.

A convention is a library over the SDK (ADR-0041), never a bus feature: the verb
logic reaches the bus only through the `Ops` seam (one `publish`). The emitted
record matches the Go convention byte-for-byte, pinned by the SAME conformance
vector both suites replay (`protocol/conformance/vectors/spawn/requestSpawn.json`).

- `src/spawn.ts` — `requestSpawn` (publish a spawn.request), `spawnRequestRecord`
  (the wire-record builder the dash uses to replace its hand-rolled literal),
  `parseSpawnAck`, the `Ops` seam, the `SpawnRequest`/`SpawnAck` shapes.
- `test/conformance.test.ts` — replays the language-neutral op-transcript vector the
  Go suite recorded.
