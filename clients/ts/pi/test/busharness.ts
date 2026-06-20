// A self-contained real-bus harness for the spike: stands up the actual Go
// `sextant` bus as a subprocess and mints scoped creds. A trimmed sibling of
// clients/ts/sdk/test/harness.ts (which is test-internal, not an exported
// module) — same bootstrap, narrowed to what the spike needs. Fail-loud: the
// bus-ready wait is bounded and throws with captured output rather than hanging.

import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, existsSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const BUS_READY_TIMEOUT_MS = 60_000;

// repoRoot walks up to the single Go module root (go.mod + protocol/conformance).
export function repoRoot(): string {
  const override = process.env["SEXTANT_REPO_ROOT"];
  if (override) return override;
  let dir = dirname(fileURLToPath(import.meta.url));
  for (;;) {
    if (existsSync(join(dir, "go.mod")) && existsSync(join(dir, "protocol", "conformance"))) {
      return dir;
    }
    const parent = dirname(dir);
    if (parent === dir) {
      throw new Error("could not find the repo root; set SEXTANT_REPO_ROOT");
    }
    dir = parent;
  }
}

export function goAvailable(): boolean {
  return spawnSync("go", ["version"], { stdio: "ignore" }).status === 0;
}

export interface Bus {
  url: string;
  store: string;
  bin: string;
  stop(): void;
  mint(name: string, kind: string): { credsPath: string; id: string };
  // mintSelf enrolls this process as a self seat (`register --self`), which
  // claims the bus principal when it is still unclaimed (ADR-0031). Used to make
  // the driven harness's "operator" the actual principal, so the pi agent's
  // trust-tiering classifies its DM as PRINCIPAL (operator-equivalent).
  mintSelf(name: string, kind: string): { credsPath: string; id: string; claimedPrincipal: boolean };
  run(args: string[]): { stdout: string; stderr: string; code: number };
}

function ensureBinary(store: string): string {
  const envBin = process.env["SEXTANT_BIN"];
  if (envBin && existsSync(envBin)) return envBin;

  const root = repoRoot();
  // The dash UI bundles are generated, not committed; regenerate before the Go
  // compile so the go:embed in the dash resolves.
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

export function startBus(): Bus {
  const store = mkdtempSync(join(tmpdir(), "sextant-pi-spike-"));
  const bin = ensureBinary(store);

  let out = "";
  const proc: ChildProcess = spawn(bin, ["up", "--store", store, "--port", "0"], {
    cwd: repoRoot(),
    stdio: ["ignore", "pipe", "pipe"],
  });
  proc.stdout?.on("data", (d: Buffer) => (out += d.toString()));
  proc.stderr?.on("data", (d: Buffer) => (out += d.toString()));

  const busJSON = join(store, "bus.json");
  const deadline = Date.now() + BUS_READY_TIMEOUT_MS;
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
    if (proc.exitCode === null && !proc.killed) proc.kill("SIGKILL");
    if (process.env["SEXTANT_KEEP_STORE"]) {
      console.error(`SEXTANT_KEEP_STORE: leaving bus store at ${store}`);
      return;
    }
    try {
      rmSync(store, { recursive: true, force: true });
    } catch {
      /* best-effort */
    }
  };

  const run = (args: string[]) => {
    // HERMETIC: pin SEXTANT_HOME to the throwaway store so any context the CLI
    // writes (notably `register --self`, which writes a context + flips the
    // active context) lands in the store, NEVER the operator's real
    // ~/Library/Application Support/sextant. A bare CLI resolves the operator's
    // real home otherwise — the reference_bare_sextant_cli hazard.
    const r = spawnSync(bin, [...args, "--store", store], {
      cwd: repoRoot(),
      encoding: "utf8",
      env: { ...process.env, SEXTANT_HOME: store },
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
    const m = r.stdout.match(/registered .* as ([0-9A-HJKMNP-TV-Z]{26})/);
    if (!m) {
      throw new Error(`could not parse the minted id for ${name} from:\n${r.stdout}`);
    }
    return { credsPath, id: m[1]! };
  };

  const mintSelf = (name: string, kind: string): { credsPath: string; id: string; claimedPrincipal: boolean } => {
    const r = run(["clients", "register", "--self", "--name", name, "--kind", kind]);
    if (r.code !== 0) {
      throw new Error(`mintSelf ${name} failed (code ${r.code}):\n${r.stdout}\n${r.stderr}`);
    }
    const idMatch = r.stdout.match(/enrolled as ([0-9A-HJKMNP-TV-Z]{26})/);
    // The creds path can contain spaces (e.g. ~/Library/Application Support/...),
    // so capture the rest of the line, not just up to the first space.
    const credsMatch = r.stdout.match(/creds:\s*(.+?)\s*$/m);
    if (!idMatch || !credsMatch) {
      throw new Error(`could not parse the self-enrolled id/creds for ${name} from:\n${r.stdout}`);
    }
    return { credsPath: credsMatch[1]!, id: idMatch[1]!, claimedPrincipal: /this seat is now the bus principal/.test(r.stdout) };
  };

  return { url, store, bin, stop, mint, mintSelf, run };
}
