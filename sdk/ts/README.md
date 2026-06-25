# @sextant/sdk — the TypeScript Wire client

A **co-equal** TypeScript implementation of the client half of the Sextant
protocol ([ADR-0041](../../../docs/adr/0041-clients-are-co-equal-across-languages.md)),
a peer to [`sdk/go`](../../go). It connects to the one Go bus over
NATS/TCP with its **own scoped credentials**, does publish / read / subscribe +
the artifact CRUD and watch, implements its **own frame codec** verified against
the wire conformance vectors, and anchors on the protocol epoch.

> **Co-equal means** the codec passes the wire conformance suite for the protocol
> epoch — not "looks like the Go output." The protocol is the authority; this SDK
> conforms to it (`protocol/conformance/FORMAT.md`), not to the Go SDK's internals.

## Toolchain

- **Runtime:** Node (CI runs node 26; `engines.node >= 22` is the LTS floor that
  ships a stable `node:test`). See `.nvmrc`.
- **Package manager:** npm (commit `package-lock.json`).
- **Compiler:** `tsc` 6 → ESM (`module: node16`, `target: es2023`), with `.d.ts`.
- **Test runner:** the built-in `node:test` — no test framework. Fewer transitive
  deps on a credential-holding client (a smaller supply-chain surface), and the
  conformance replay is a plain data loop over JSON files.
- **Only runtime dependency:** [`nats`](https://www.npmjs.com/package/nats)
  `2.29.3` (creds auth via `credsAuthenticator`, a per-client `inboxPrefix`,
  request/reply, push subscriptions).

The package version anchors on the **protocol epoch**, not on the Go SDK's
SemVer: `EPOCH` is exported as a public constant and recorded in `package.json`
(`sextant.epoch`). A wire-breaking epoch bump is a new major.

## Layout

```
sdk/ts/
  src/
    index.ts              public barrel
    types.ts              Message, Artifact, ClientInfo, JSONValue, …
    client.ts             connect() + the Client class
    issuer.ts             connectIssuer() + the Issuer (mint/retire)
    wire/
      frame.ts            Frame + ULID parse + validateFrame
      codec.ts            canonical() + encode/decode (+ a big-int JSON parser)
      epoch.ts            EPOCH=1 + checkEpoch/checkSkew
    transport/
      conn.ts             creds identity, URL resolution, the call envelope
      callsubjects.ts     the Wire API subjects + operation names
      subjects.ts         topicSubject / clientSubject / dmSubject
  test/
    codec.test.ts         canonicalization + frame-codec edge cases
    conformance.test.ts   replays protocol/conformance/vectors/wire/*.json
    integration.test.ts   cross-language round-trip on a real Go bus
    harness.ts            spawns `sextant up`, mints scoped creds
```

## Usage

```ts
import { connect, topicSubject } from "@sextant/sdk";

// connect() loads THIS client's own scoped .creds file — never the operator's
// ambient credentials. The id and display name come from the credential's JWT,
// so the bus-stamped frame author is unforgeable.
const c = await connect({ credsPath: "/path/to/ts-agent.creds", url: "nats://127.0.0.1:4222" });

await c.publish(topicSubject("plan"), { $type: "chat.message", text: "hello, bus" });

const sub = await c.subscribe(topicSubject("plan"), (m) => {
  console.log(m.frame.author, m.frame.record);
});

const rev = await c.createArtifact("the-plan", { title: "v1" });
const next = await c.updateArtifact("the-plan", { title: "v2" }, rev); // compare-and-set

await sub.stop();
await c.close(); // does NOT retire the identity — just goes offline
```

### Getting a scoped credential

The TS client connects as its **own** bus-issued identity, a distinct entry in
the clients registry. Two documented paths mint one (both authorized by
`operator.creds` / `enroll.creds` that `sextant up` writes into the store):

- **CLI:** `sextant clients register <name> --kind agent --store <store>` →
  writes `<store>/<name>.creds` (0600).
- **Wire API from TS:** connect an `Issuer` with `operator.creds` and call
  `register(displayName, kind)` → `{ id, creds }`; write `creds` to a 0600 file.

```ts
import { connectIssuer } from "@sextant/sdk";
const iss = await connectIssuer({ credsPath: "/path/to/operator.creds", url });
const issued = await iss.register("ts-agent", "agent"); // { id, creds }
await iss.close();
// write issued.creds to a 0600 file, then connect() as that identity
```

## Tests

```bash
npm ci
npm run build                # tsc → dist/ (a type error fails the build)
npm run test:conformance     # codec + conformance vectors — no bus, runs everywhere
npm run test:integration     # cross-language round-trip — needs the Go toolchain
npm test                     # all of the above
```

The conformance suite reads the **same** JSON the Go SDK replays, directly from
`protocol/conformance/vectors/wire/` (resolved by walking up to the repo root, or
via `SEXTANT_REPO_ROOT`). It is never copied. Passing it for the epoch is what
makes this client co-equal.

The integration suite spawns the real Go `sextant` bus as a subprocess (building
it on demand, or reusing `$SEXTANT_BIN`). It **skips with a reason** when the
`go` toolchain is not on PATH, so the codec/conformance tests still run
everywhere.
