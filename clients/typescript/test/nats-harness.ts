/**
 * Test harness: spawn nats-server in a temp dir with JetStream, create
 * the M2 streams + KV buckets we need, return the URL + creds so the
 * test can build a Client against it.
 *
 * Mirrors pkg/natsboot's config rendering and stream/bucket layout —
 * we render the same conf file from TypeScript so the TS suite is
 * self-contained (no Go subprocess required during CI).
 */

import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { randomBytes } from "node:crypto";
import net from "node:net";

import { connect as natsConnect, type NatsConnection } from "nats";

interface HarnessSpec {
  /** Optional override binary path. Defaults to `nats-server` on $PATH. */
  binary?: string;
}

export interface HarnessHandle {
  url: string;
  operatorUser: string;
  operatorPassword: string;
  dataDir: string;
  configPath: string;
  /** Shut down the server + clean up the temp dir. */
  stop(): Promise<void>;
}

/** Pick a free localhost TCP port. */
async function pickFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      if (!addr || typeof addr === "string") {
        srv.close();
        reject(new Error("no addr"));
        return;
      }
      const port = addr.port;
      srv.close(() => resolve(port));
    });
  });
}

/** Render a nats-server .conf matching pkg/natsboot/conf.go. */
function renderConfig(opts: {
  serverName: string;
  listenHost: string;
  listenPort: number;
  dataDir: string;
  operatorUser: string;
  operatorPassword: string;
}): string {
  const q = (s: string) => `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
  return [
    `server_name: ${q(opts.serverName)}`,
    `listen: ${q(`${opts.listenHost}:${opts.listenPort}`)}`,
    `jetstream {\n  store_dir: ${q(opts.dataDir)}\n}`,
    `authorization {`,
    `  users = [`,
    `    { user: ${q(opts.operatorUser)}, password: ${q(opts.operatorPassword)}, permissions: { publish: ">", subscribe: ">" } }`,
    `  ]`,
    `}`,
  ].join("\n");
}

/** Wait until a TCP port answers or the deadline passes. */
async function waitForPort(host: string, port: number, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown;
  while (Date.now() < deadline) {
    try {
      await new Promise<void>((resolve, reject) => {
        const sock = net.connect({ host, port });
        sock.once("connect", () => {
          sock.end();
          resolve();
        });
        sock.once("error", reject);
      });
      return;
    } catch (err) {
      lastErr = err;
      await new Promise((r) => setTimeout(r, 100));
    }
  }
  throw new Error(
    `harness: nats-server on ${host}:${port} didn't open within ${timeoutMs}ms: ${
      lastErr instanceof Error ? lastErr.message : String(lastErr)
    }`,
  );
}

/**
 * Spawn nats-server. Returns when the listener accepts a TCP
 * connection (we don't try to use the /healthz HTTP endpoint here —
 * the listener test is the wire we care about).
 */
export async function startNATS(spec: HarnessSpec = {}): Promise<HarnessHandle> {
  const baseDir = await mkdtemp(path.join(tmpdir(), "sextant-ts-"));
  const dataDir = path.join(baseDir, "jetstream");
  await mkdir(dataDir, { recursive: true });
  const configPath = path.join(baseDir, "nats.conf");
  const port = await pickFreePort();
  const operatorUser = "operator";
  const operatorPassword = randomBytes(16).toString("hex");
  const conf = renderConfig({
    serverName: "sextant-ts-test",
    listenHost: "127.0.0.1",
    listenPort: port,
    dataDir,
    operatorUser,
    operatorPassword,
  });
  await writeFile(configPath, conf, "utf8");

  const binary = spec.binary ?? "nats-server";
  const proc: ChildProcessWithoutNullStreams = spawn(binary, ["-c", configPath, "-js"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  proc.unref();

  let stderrBuf = "";
  proc.stderr.on("data", (b: Buffer) => {
    stderrBuf += b.toString();
  });

  const exitPromise = new Promise<number | null>((resolve) => {
    proc.once("exit", (code) => resolve(code));
  });

  try {
    await waitForPort("127.0.0.1", port, 10_000);
  } catch (err) {
    proc.kill("SIGTERM");
    await exitPromise;
    await rm(baseDir, { recursive: true, force: true });
    throw new Error(`${(err as Error).message}\n--- nats stderr ---\n${stderrBuf}`);
  }

  const url = `nats://127.0.0.1:${port}`;

  const stop = async (): Promise<void> => {
    if (!proc.killed) {
      proc.kill("SIGTERM");
      const stopped = await Promise.race([
        exitPromise,
        new Promise<"timeout">((resolve) => setTimeout(() => resolve("timeout"), 5_000)),
      ]);
      if (stopped === "timeout") {
        proc.kill("SIGKILL");
        await exitPromise;
      }
    }
    await rm(baseDir, { recursive: true, force: true });
  };

  return { url, operatorUser, operatorPassword, dataDir, configPath, stop };
}

/**
 * Bootstrap the JetStream streams + KV buckets needed for the
 * integration tests. Mirrors pkg/natsboot/layout.go so the TS client
 * resolves streams the same way the daemon does.
 */
export async function bootstrapJetStream(handle: HarnessHandle): Promise<void> {
  const nc = await natsConnect({
    servers: handle.url,
    user: handle.operatorUser,
    pass: handle.operatorPassword,
  });
  try {
    const jsm = await nc.jetstreamManager();
    // Streams the tests touch.
    const streams = [
      { name: "agent_frames", subjects: ["agents.*.frames"] },
      { name: "agent_lifecycle", subjects: ["agents.*.lifecycle"] },
      { name: "control_rpc", subjects: ["sextant.rpc.>"] },
      { name: "audit", subjects: ["audit.>"] },
    ];
    for (const s of streams) {
      try {
        await jsm.streams.add({ name: s.name, subjects: s.subjects });
      } catch (err) {
        // "stream name already in use" is fine — the daemon would
        // have created it the same way.
        const msg = err instanceof Error ? err.message : String(err);
        if (!/already in use|name in use/i.test(msg)) throw err;
      }
    }
    // KV buckets the tests touch. nats.js auto-creates the
    // KV-backing stream when js.views.kv is first called, but
    // pre-creating mirrors the daemon's behaviour.
    const buckets = ["ui_state", "templates"];
    const js = nc.jetstream();
    for (const b of buckets) {
      await js.views.kv(b);
    }
  } finally {
    await nc.close();
  }
}

/** Convenience: start + bootstrap in one call. */
export async function startBootstrappedNATS(spec: HarnessSpec = {}): Promise<HarnessHandle> {
  const handle = await startNATS(spec);
  try {
    await bootstrapJetStream(handle);
    return handle;
  } catch (err) {
    await handle.stop();
    throw err;
  }
}

/** Build a raw NATS connection authorized as the operator. */
export async function connectAsOperator(handle: HarnessHandle): Promise<NatsConnection> {
  return natsConnect({
    servers: handle.url,
    user: handle.operatorUser,
    pass: handle.operatorPassword,
  });
}
