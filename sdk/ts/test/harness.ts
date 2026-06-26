// The real-bus test harness: it stands up the actual Go `sextant` bus as a
// subprocess and mints scoped creds, so the cross-language round-trip exercises
// real identity and unforgeable authorship — not a mock (AC#5, AC#6). Mirrors
// the bootstrap in tests/e2e/m2_acceptance_test.go.
//
// Fail-loud, fail-early: the bus-ready wait is bounded with a deadline; if
// bus.json never appears, the harness throws with the captured stderr rather
// than hanging.

import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, existsSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { repoRoot } from "./repoRoot.js";

const BUS_READY_TIMEOUT_MS = 30_000;

// goAvailable reports whether the `go` toolchain is on PATH. The cross-language
// suite is skipped-with-reason when it is not (the cheap codec/conformance tests
// still run everywhere) — fail-loud/skip-if-env-bound discipline.
export function goAvailable(): boolean {
  const r = spawnSync("go", ["version"], { stdio: "ignore" });
  return r.status === 0;
}

export interface Bus {
  url: string;
  store: string; // the bus store dir (holds operator.creds + minted .creds)
  bin: string; // path to the sextant binary
  stop(): void;
  // mint registers a held-identity (operator-minted) and returns its creds path
  // and bus-minted ULID id (parsed from the register output).
  mint(name: string, kind: string): { credsPath: string; id: string };
  // run executes the sextant binary to completion, returning {stdout,stderr,code}.
  run(args: string[]): { stdout: string; stderr: string; code: number };
}

// ensureBinary returns a path to a `sextant` binary: $SEXTANT_BIN if set,
// otherwise it builds one (running the dash-UI generator first, since the binary
// go:embeds generated JS). Built once into the bus store dir.
function ensureBinary(store: string): string {
  const envBin = process.env["SEXTANT_BIN"];
  if (envBin && existsSync(envBin)) return envBin;

  const root = repoRoot();
  // The dash UI bundles are generated, not committed; regenerate them before the
  // Go compile so the go:embed in clients/sextant-dash/dashapi resolves.
  const ui = spawnSync("bash", [join(root, "scripts", "build-dash-ui.sh")], {
    cwd: root,
    encoding: "utf8",
  });
  if (ui.status !== 0) {
    throw new Error(`build-dash-ui.sh failed (status ${ui.status}):\n${ui.stdout}\n${ui.stderr}`);
  }

  const bin = join(store, "sextant");
  const build = spawnSync("go", ["build", "-o", bin, "./clients/sextant-cli"], {
    cwd: root,
    encoding: "utf8",
  });
  if (build.status !== 0) {
    throw new Error(`go build sextant failed (status ${build.status}):\n${build.stdout}\n${build.stderr}`);
  }
  return bin;
}

// startBus spawns `sextant up --store <tmp> --port 0`, waits for the discovery
// file, and returns a Bus handle. Call stop() in a test teardown.
export function startBus(): Bus {
  const store = mkdtempSync(join(tmpdir(), "sextant-ts-e2e-"));
  const bin = ensureBinary(store);

  let stderr = "";
  const proc: ChildProcess = spawn(bin, ["up", "--store", store, "--port", "0"], {
    cwd: repoRoot(),
    stdio: ["ignore", "pipe", "pipe"],
  });
  proc.stdout?.on("data", (d: Buffer) => {
    stderr += d.toString();
  });
  proc.stderr?.on("data", (d: Buffer) => {
    stderr += d.toString();
  });

  const busJSON = join(store, "bus.json");
  const deadline = Date.now() + BUS_READY_TIMEOUT_MS;
  // Synchronous spin-wait on the discovery file. Bounded; throws with the
  // captured output if the bus never comes up.
  while (Date.now() < deadline) {
    if (existsSync(busJSON)) break;
    // Busy-wait a short slice without blocking the event loop indefinitely.
    spawnSync("sleep", ["0.05"]);
  }
  if (!existsSync(busJSON)) {
    proc.kill("SIGKILL");
    throw new Error(`bus did not come up within ${BUS_READY_TIMEOUT_MS}ms:\n${stderr}`);
  }
  const url = (JSON.parse(readFileSync(busJSON, "utf8")) as { url: string }).url;

  let stopped = false;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    proc.kill("SIGINT");
    // Give it a moment to wind down, then ensure it is gone.
    spawnSync("sleep", ["0.2"]);
    if (proc.exitCode === null && !proc.killed) {
      proc.kill("SIGKILL");
    }
    if (process.env["SEXTANT_KEEP_STORE"]) {
      console.error(`SEXTANT_KEEP_STORE: leaving bus store at ${store}`);
      return;
    }
    try {
      rmSync(store, { recursive: true, force: true });
    } catch {
      /* best-effort cleanup */
    }
  };

  const run = (args: string[]) => {
    const r = spawnSync(bin, [...args, "--store", store], {
      cwd: repoRoot(),
      encoding: "utf8",
    });
    return { stdout: r.stdout ?? "", stderr: r.stderr ?? "", code: r.status ?? -1 };
  };

  const mint = (name: string, kind: string): { credsPath: string; id: string } => {
    const r = run(["clients", "register", name, "--kind", kind]);
    if (r.code !== 0) {
      throw new Error(`mint ${name} failed (code ${r.code}):\n${r.stdout}\n${r.stderr}`);
    }
    const credsPath = join(store, `${name}.creds`);
    if (!existsSync(credsPath)) {
      throw new Error(`mint ${name} did not write ${credsPath}:\n${r.stdout}`);
    }
    // "registered <name> as <ULID>" — parse the bus-minted id from the output.
    const m = r.stdout.match(/registered .* as ([0-9A-HJKMNP-TV-Z]{26})/);
    if (!m) {
      throw new Error(`could not parse the minted id for ${name} from:\n${r.stdout}`);
    }
    return { credsPath, id: m[1]! };
  };

  return { url, store, bin, stop, mint, run };
}
