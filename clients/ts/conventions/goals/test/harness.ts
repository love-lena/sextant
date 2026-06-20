// The real-bus test harness for the goals co-equality scenario (AC#4): it stands
// up the actual Go `sextant` bus as a subprocess, mints scoped creds, and builds
// the Go convention helper (test/gohelper) — so the scenario exercises the REAL Go
// goals convention against the REAL TS goals convention on ONE bus, not a mock.
// Mirrors the SDK package's harness (clients/ts/sdk/test/harness.ts) and the
// bootstrap in tests/e2e/m2_acceptance_test.go.
//
// Fail-loud, fail-early: the bus-ready wait is bounded; if bus.json never appears,
// the harness throws with the captured stderr rather than hanging.

import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, existsSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { repoRoot } from "./repoRoot.js";

const BUS_READY_TIMEOUT_MS = 30_000;

// goAvailable reports whether the `go` toolchain is on PATH. The live scenario is
// skipped-with-reason when it is not (the cheap unit/conformance tests still run
// everywhere) — fail-loud/skip-if-env-bound discipline.
export function goAvailable(): boolean {
  const r = spawnSync("go", ["version"], { stdio: "ignore" });
  return r.status === 0;
}

export interface Bus {
  url: string;
  store: string; // the bus store dir (holds operator.creds + minted .creds)
  bin: string; // path to the sextant binary
  goHelper: string; // path to the built Go convention helper
  stop(): void;
  // mint registers a held-identity (operator-minted) and returns its creds path
  // and bus-minted ULID id (parsed from the register output).
  mint(name: string, kind: string): { credsPath: string; id: string };
  // runGo runs the Go convention helper with the given args and creds, returning
  // {stdout,stderr,code}. SEXTANT_URL/SEXTANT_CREDS are set from the bus + creds.
  runGo(args: string[], credsPath: string): { stdout: string; stderr: string; code: number };
}

// ensureBinary returns a path to a `sextant` binary: $SEXTANT_BIN if set, otherwise
// it builds one (running the dash-UI generator first, since the binary go:embeds
// generated JS). Built once into the bus store dir.
function ensureBinary(store: string): string {
  const envBin = process.env["SEXTANT_BIN"];
  if (envBin && existsSync(envBin)) return envBin;

  const root = repoRoot();
  // The dash UI bundles are generated, not committed; regenerate them before the Go
  // compile so the go:embed in clients/go/apps/internal/dashapi resolves.
  const ui = spawnSync("bash", [join(root, "scripts", "build-dash-ui.sh")], {
    cwd: root,
    encoding: "utf8",
  });
  if (ui.status !== 0) {
    throw new Error(`build-dash-ui.sh failed (status ${ui.status}):\n${ui.stdout}\n${ui.stderr}`);
  }

  const bin = join(store, "sextant");
  const build = spawnSync("go", ["build", "-o", bin, "./clients/go/apps/sextant"], {
    cwd: root,
    encoding: "utf8",
  });
  if (build.status !== 0) {
    throw new Error(`go build sextant failed (status ${build.status}):\n${build.stdout}\n${build.stderr}`);
  }
  return bin;
}

// buildGoHelper compiles the Go convention helper (test/gohelper) into the store
// dir. It drives the REAL Go goals convention so the co-equality proof is genuine.
function buildGoHelper(store: string): string {
  const root = repoRoot();
  const bin = join(store, "gohelper");
  const build = spawnSync(
    "go",
    ["build", "-o", bin, "./clients/ts/conventions/goals/test/gohelper"],
    { cwd: root, encoding: "utf8" },
  );
  if (build.status !== 0) {
    throw new Error(`go build gohelper failed (status ${build.status}):\n${build.stdout}\n${build.stderr}`);
  }
  return bin;
}

// startBus spawns `sextant up --store <tmp> --port 0`, waits for the discovery
// file, builds the Go helper, and returns a Bus handle. Call stop() in teardown.
export function startBus(): Bus {
  const store = mkdtempSync(join(tmpdir(), "sextant-ts-goals-e2e-"));
  const bin = ensureBinary(store);
  const goHelper = buildGoHelper(store);

  let out = "";
  const proc: ChildProcess = spawn(bin, ["up", "--store", store, "--port", "0"], {
    cwd: repoRoot(),
    stdio: ["ignore", "pipe", "pipe"],
  });
  proc.stdout?.on("data", (d: Buffer) => {
    out += d.toString();
  });
  proc.stderr?.on("data", (d: Buffer) => {
    out += d.toString();
  });

  const busJSON = join(store, "bus.json");
  const deadline = Date.now() + BUS_READY_TIMEOUT_MS;
  // Bounded synchronous spin-wait on the discovery file; throws with the captured
  // output if the bus never comes up.
  while (Date.now() < deadline) {
    if (existsSync(busJSON)) break;
    spawnSync("sleep", ["0.05"]);
  }
  if (!existsSync(busJSON)) {
    proc.kill("SIGKILL");
    throw new Error(`bus did not come up within ${BUS_READY_TIMEOUT_MS}ms:\n${out}`);
  }
  const url = (JSON.parse(readFileSync(busJSON, "utf8")) as { url: string }).url;

  let stopped = false;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    proc.kill("SIGINT");
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
    const r = spawnSync(bin, [...args, "--store", store], { cwd: repoRoot(), encoding: "utf8" });
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
    const m = r.stdout.match(/registered .* as ([0-9A-HJKMNP-TV-Z]{26})/);
    if (!m) {
      throw new Error(`could not parse the minted id for ${name} from:\n${r.stdout}`);
    }
    return { credsPath, id: m[1]! };
  };

  const runGo = (args: string[], credsPath: string) => {
    const r = spawnSync(goHelper, args, {
      cwd: repoRoot(),
      encoding: "utf8",
      env: { ...process.env, SEXTANT_URL: url, SEXTANT_CREDS: credsPath },
    });
    return { stdout: r.stdout ?? "", stderr: r.stderr ?? "", code: r.status ?? -1 };
  };

  return { url, store, bin, goHelper, stop, mint, runGo };
}
