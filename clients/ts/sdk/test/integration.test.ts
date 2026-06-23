// The cross-language round-trip on a REAL bus (not mocked): a TS client and the
// Go `sextant` binary exchange messages and artifacts through the actual bus, so
// identity, unforgeable authorship, and the wire codec are all exercised
// end-to-end (AC#1, AC#5, AC#6). The Go side is driven as a subprocess (the CLI),
// which is the lighter proof and still exposes frame.author via `read --json`.
//
// The whole suite is gated on the Go toolchain being present (skip-with-reason
// when `go` is not on PATH) so the codec/conformance tests still run everywhere.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { connect } from "../src/connect.js";
import { type Client } from "../src/client.js";
import { connectIssuer } from "../src/issuer.js";
import { topicSubject } from "../src/transport/subjects.js";
import type { Message } from "../src/types.js";
import { startBus, goAvailable, type Bus } from "./harness.js";

const skip = !goAvailable();
const skipReason = "the `go` toolchain is not on PATH (cross-language round-trip needs the real Go bus)";

let bus: Bus;
let tsCredsPath: string;
let goReaderCreds: string;
let goReaderID: string;

before(() => {
  if (skip) return;
  bus = startBus();
  tsCredsPath = bus.mint("ts-agent", "agent").credsPath;
  const goReader = bus.mint("go-reader", "agent");
  goReaderCreds = goReader.credsPath;
  goReaderID = goReader.id;
});

after(() => {
  if (skip || !bus) return;
  bus.stop();
});

const XLANG = "xlang";

test("TS → Go: a TS client publishes a frame a Go client reads, with the TS agent's unforgeable author", { skip: skip && skipReason }, async () => {
  const c = await connect({ credsPath: tsCredsPath, url: bus.url });
  try {
    const { id, seq } = await c.publishMsg(topicSubject(XLANG), { $type: "chat.message", text: "from-ts" });
    assert.ok(id !== "", "the bus stamps a frame id");
    assert.ok(seq > 0, "the bus stamps a stream sequence");

    // The Go CLI reads the same subject and emits each frame as JSON.
    const r = bus.run(["read", topicSubject(XLANG), "--json", "--creds", goReaderCreds]);
    assert.equal(r.code, 0, `go read failed: ${r.stderr}`);
    const frames = parseJSONFrames(r.stdout);
    const got = frames.find((f) => f.record?.text === "from-ts");
    assert.ok(got, `Go did not read the TS-published frame; saw:\n${r.stdout}`);
    // The keystone: the frame Go reads is authored by the TS agent's bus-minted
    // id (not the operator's). This is the unforgeable cross-language authorship.
    assert.equal(got!.author, c.id(), "the frame Go reads must be authored by the TS agent");
    assert.equal(got!.id, id, "the author-stamped id matches what publishMsg returned");
    assert.equal(got!.epoch, 1, "the frame carries epoch 1");
  } finally {
    await c.close();
  }
});

test("Go → TS: a Go client publishes a frame the TS client subscribes to and decodes", { skip: skip && skipReason }, async () => {
  const c = await connect({ credsPath: tsCredsPath, url: bus.url });
  try {
    const subject = topicSubject(XLANG + "-back");
    const received: Message[] = [];
    const got = new Promise<Message>((resolve) => {
      void c.subscribe(subject, (m) => {
        received.push(m);
        resolve(m);
      });
    });
    // Let the subscription's server-side relay register before the Go publish.
    await c.publishMsg(topicSubject("warmup"), { $type: "noop", v: 1 }); // round-trips a call to flush the relay
    await delay(200);

    const r = bus.run(["publish", subject, JSON.stringify({ $type: "chat.message", text: "from-go" }), "--creds", goReaderCreds]);
    assert.equal(r.code, 0, `go publish failed: ${r.stderr}`);

    const m = await withTimeout(got, 10_000, "TS did not receive the Go-published frame");
    assert.equal((m.frame.record as { text?: string }).text, "from-go");
    // The Go publisher's bus-minted id is the unforgeable author the TS codec
    // decoded.
    assert.equal(m.frame.author, goReaderID, "the frame the TS client decoded must be authored by the Go publisher");
    assert.equal(m.frame.epoch, 1);
  } finally {
    await c.close();
  }
});

test("artifacts: a TS-created artifact is read by Go, and a Go-created artifact is read by TS (AC#1 closure)", { skip: skip && skipReason }, async () => {
  const c = await connect({ credsPath: tsCredsPath, url: bus.url });
  try {
    // TS creates → Go reads. createArtifact returns the bus-stamped revision (the
    // ARTIFACTS bucket is a single versioned store, so the revision is a
    // monotonic bucket counter, not per-key — assert "created and readable", not
    // a fixed starting number).
    const tsRev = await c.createArtifact("ts-made", { title: "from-ts", n: 7 });
    assert.ok(tsRev >= 1, "a created artifact carries a bus-stamped revision");
    const goGet = bus.run(["artifact", "get", "ts-made", "--json", "--creds", goReaderCreds]);
    assert.equal(goGet.code, 0, `go artifact get failed: ${goGet.stderr}`);
    assert.match(goGet.stdout, /from-ts/, "Go must read the TS-created artifact record");

    // Go creates → TS reads the same record at the same revision the Go CLI
    // reported.
    const goCreate = bus.run(["artifact", "create", "go-made", JSON.stringify({ title: "from-go" }), "--creds", goReaderCreds]);
    assert.equal(goCreate.code, 0, `go artifact create failed: ${goCreate.stderr}`);
    const goRev = revFromCreate(goCreate.stdout);
    const art = await c.getArtifact("go-made");
    assert.equal((art.record as { title?: string }).title, "from-go", "TS reads the Go-created record");
    assert.equal(art.revision, goRev, "TS reads the same revision the Go CLI stamped");

    // Compare-and-set is honored cross-language: a stale-rev TS update fails, a
    // correct one advances the revision.
    await assert.rejects(
      () => c.updateArtifact("go-made", { title: "stale" }, 99),
      "a compare-and-set update at the wrong revision must fail",
    );
    const newRev = await c.updateArtifact("go-made", { title: "from-ts-update" }, goRev);
    assert.ok(newRev > goRev, "a correct compare-and-set advances the revision");
  } finally {
    await c.close();
  }
});

test("scoped creds: the TS client is a DISTINCT identity in the clients registry, never the operator (AC#5)", { skip: skip && skipReason }, async () => {
  const c = await connect({ credsPath: tsCredsPath, url: bus.url });
  try {
    // The TS client's own id (from its minted JWT) is present in the directory
    // with kind=agent and its display name — distinct from the operator.
    const clients = await c.listClients();
    const me = clients.find((ci) => ci.id === c.id());
    assert.ok(me, "the TS agent must appear in the clients registry");
    assert.equal(me!.kind, "agent");
    assert.equal(me!.displayName, "ts-agent");
    assert.notEqual(c.id(), "operator", "the TS client must NOT be acting as the operator identity");
    assert.ok(me!.online, "the TS agent is online while connected");

    // Cross-check the same directory via the operator's Issuer connection (the
    // documented scoped-creds authority): the TS agent is a separate entry from
    // go-reader and from any operator/enroll infra identity.
    const iss = await connectIssuer({ credsPath: operatorCredsPath(bus.store), url: bus.url });
    try {
      const viaIssuer = await iss.listClients();
      const ids = new Set(viaIssuer.map((ci) => ci.id));
      assert.ok(ids.has(c.id()), "the operator's directory lists the TS agent");
      assert.ok(ids.has(goReaderID), "the operator's directory lists the Go reader");
      assert.notEqual(c.id(), goReaderID, "the TS agent and the Go reader are distinct identities");
    } finally {
      await iss.close();
    }

    // And a frame the TS client publishes carries its own id as author — proving
    // it is not acting as the operator (unforgeable authorship).
    const { id } = await c.publishMsg(topicSubject("scoped-check"), { $type: "probe", v: 1 });
    const r = bus.run(["read", topicSubject("scoped-check"), "--json", "--creds", goReaderCreds]);
    const frames = parseJSONFrames(r.stdout);
    const got = frames.find((f) => f.id === id);
    assert.ok(got, "the published frame is readable");
    assert.equal(got!.author, c.id(), "the frame author is the TS agent's own id, never the operator's");
  } finally {
    await c.close();
  }
});

// --- helpers ---

interface JSONFrame {
  id: string;
  author: string;
  kind: string;
  epoch: number;
  record: { text?: string; [k: string]: unknown };
}

// parseJSONFrames parses the Go CLI's `read --json` output: a stream of
// pretty-printed JSON frame objects concatenated on stdout. We split on the
// top-level object boundaries by brace-depth.
function parseJSONFrames(stdout: string): JSONFrame[] {
  const frames: JSONFrame[] = [];
  let depth = 0;
  let start = -1;
  let inString = false;
  let escaped = false;
  for (let i = 0; i < stdout.length; i++) {
    const ch = stdout[i]!;
    if (inString) {
      if (escaped) escaped = false;
      else if (ch === "\\") escaped = true;
      else if (ch === '"') inString = false;
      continue;
    }
    if (ch === '"') {
      inString = true;
      continue;
    }
    if (ch === "{") {
      if (depth === 0) start = i;
      depth++;
    } else if (ch === "}") {
      depth--;
      if (depth === 0 && start >= 0) {
        try {
          frames.push(JSON.parse(stdout.slice(start, i + 1)) as JSONFrame);
        } catch {
          /* not a frame object; skip */
        }
        start = -1;
      }
    }
  }
  return frames;
}

function operatorCredsPath(store: string): string {
  return `${store}/operator.creds`;
}

// revFromCreate parses "<name> now at revision <N>" from the CLI create output.
function revFromCreate(stdout: string): number {
  const m = stdout.match(/now at revision (\d+)/);
  if (!m) throw new Error(`could not parse revision from: ${JSON.stringify(stdout)}`);
  return Number(m[1]);
}

function delay(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

function withTimeout<T>(p: Promise<T>, ms: number, msg: string): Promise<T> {
  return Promise.race([
    p,
    new Promise<T>((_, reject) => setTimeout(() => reject(new Error(msg)), ms)),
  ]);
}
