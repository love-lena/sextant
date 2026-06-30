package components

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRecipeLiftsRunStepEnv guards the managed step-done handshake at the exact
// seam the live "work step never completes" defect implicated: the dispatcher
// appends "RUN_EVENTS=<subject> RUN_STEP=<id>" to the worker PROMPT TEXT
// (coordinator.workPrompt), and the embedded recipe must LIFT those into the
// SEXTANT_PI_RUN_EVENTS / SEXTANT_PI_RUN_STEP env vars the pi-bus extension reads
// (config.resolveConfig) so it knows which run-events subject + step to publish
// the step-done run.event on. If that lift silently breaks (a bad sed, a recipe
// edit, prompt drift), the extension's runEventsSubject is "" → isRunStep()
// false → it NEVER publishes → the coordinator hangs forever at the work step
// (no step-done, no block). The pi-bus UNIT tests drive RunReporter with deps
// already populated, so they cannot catch a broken lift; this exercises the REAL
// embedded recipe end to end through `exec pi` with the env the dispatcher sets.
//
// It is model-free + hermetic: a stub `pi` on PATH dumps the run-step env and
// exits, so the recipe runs through its preflight + the lift + the final
// `exec pi` without a model, a bus, or the srt runtime (automode with a stub
// pi-auto entry). Runs in CI.
func TestRecipeLiftsRunStepEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recipe is a POSIX sh script")
	}
	recipe, err := EmbeddedRecipe()
	if err != nil {
		t.Fatalf("read embedded recipe: %v", err)
	}

	dir := t.TempDir()
	recipePath := filepath.Join(dir, "pi.sh")
	if err := os.WriteFile(recipePath, recipe, 0o755); err != nil {
		t.Fatalf("write recipe: %v", err)
	}

	// Minimal on-disk assets the recipe's required-var checks demand.
	store := filepath.Join(dir, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	mustWrite(t, filepath.Join(store, "bus.json"), `{"url":"nats://127.0.0.1:4222"}`)
	credsPath := filepath.Join(dir, "child.creds")
	mustWrite(t, credsPath, `{"fake":"creds"}`)
	extPath := filepath.Join(dir, "ext.mjs")
	mustWrite(t, extPath, "export default function(){}\n")

	// automode skips srt; supply a stub pi-auto entry so the automode preflight
	// passes (it only checks the file exists, plus sandbox-exec on Darwin, which
	// is always present on a mac runner).
	autoEntry := filepath.Join(dir, "pi-auto.ts")
	mustWrite(t, autoEntry, "// stub\n")

	// A stub `pi` that dumps the lifted run-step env and exits 0 — so the recipe
	// runs all the way to its final `exec "$PI_BIN" ...`. A stub `sandbox-exec` is
	// also placed (harmless on Linux; the real one on macOS is used by the
	// automode preflight check, which only does `command -v`).
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	stubPi := "#!/bin/sh\n" +
		`printf 'RUNEV=%s\n' "${SEXTANT_PI_RUN_EVENTS:-<unset>}"` + "\n" +
		`printf 'RUNSTEP=%s\n' "${SEXTANT_PI_RUN_STEP:-<unset>}"` + "\n" +
		`printf 'DRAIN=%s\n' "${SEXTANT_PI_DRAIN_WHEN_IDLE:-<unset>}"` + "\n" +
		`printf 'CREDS=%s\n' "${SEXTANT_PI_CREDS:-<unset>}"` + "\n" +
		"exit 0\n"
	mustWrite(t, filepath.Join(bin, "pi"), stubPi)
	if err := os.Chmod(filepath.Join(bin, "pi"), 0o755); err != nil {
		t.Fatalf("chmod stub pi: %v", err)
	}

	const (
		subject = "msg.workflow.run.01KWREPRO0000000000000000.events"
		stepID  = "n1reprostep"
	)
	// A realistic multi-line work-step prompt EXACTLY as coordinator.workPrompt
	// builds it: objective + label, an INPUT ARTIFACTS block, the PROOF preamble,
	// then the trailing "RUN_EVENTS=<subject> RUN_STEP=<id>" directive line.
	prompt := strings.Join([]string{
		"Write and improve the work-engine canon document",
		"",
		"Draft the plan",
		"",
		"INPUT ARTIFACTS (produced by prior steps of this run — fetch each with sextant_artifact_get and build on its content; do NOT start from scratch):",
		"- some.input (kind document, v1)",
		"",
		"PROOF MUST BE REAL: any deliverable you cite as proof of completion MUST be a durable artifact you actually CREATED (via sextant_artifact_put). The run is GATED on the artifacts you report producing. Never report producing an artifact you did not create.",
		"RUN_EVENTS=" + subject + " RUN_STEP=" + stepID,
	}, "\n")

	cmd := exec.Command("sh", recipePath)
	cmd.Env = append(
		os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SEXTANT_CREDS="+credsPath,
		"SEXTANT_STORE="+store,
		"SEXTANT_PI_EXTENSION="+extPath,
		"SX_PROMPT="+prompt,
		"SX_CHILD_ID=01CHILDAAAAAAAAAAAAAAAAAAA",
		"SX_CHILD_NICK=writer",
		"SX_AGENT_MODEL=claude-haiku-4-5",
		"SEXTANT_PI_WORKDIR="+filepath.Join(dir, "wd"),
		"SX_PI_SANDBOX_MODE=automode",
		"SX_PI_AUTO_ENTRY="+autoEntry,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recipe run failed: %v\noutput:\n%s", err, out)
	}
	got := string(out)

	wantRunEv := "RUNEV=" + subject
	if !strings.Contains(got, wantRunEv) {
		t.Errorf("recipe did NOT lift RUN_EVENTS into SEXTANT_PI_RUN_EVENTS — the managed step-done would never publish.\nwant a line %q\ngot:\n%s", wantRunEv, got)
	}
	wantStep := "RUNSTEP=" + stepID
	if !strings.Contains(got, wantStep) {
		t.Errorf("recipe did NOT lift RUN_STEP into SEXTANT_PI_RUN_STEP.\nwant a line %q\ngot:\n%s", wantStep, got)
	}
	// drain-when-idle must default ON for the dispatcher recipe, or the worker
	// stays resident and the step-done waits until session_shutdown (a slower,
	// less deterministic path the auto-drain replaced).
	if !strings.Contains(got, "DRAIN=1") {
		t.Errorf("recipe did NOT default SEXTANT_PI_DRAIN_WHEN_IDLE=1 (the drain-and-revive auto-report).\ngot:\n%s", got)
	}
}

// TestRecipeSandboxAllowReadsExtension guards TASK-42 root cause #2: the srt sandbox
// profile the recipe generates MUST allow-read the worker's own extension bundle
// (SEXTANT_PI_EXTENSION). The bundle lives under the operator's Application Support dir,
// which the profile broadly deny-reads; without an explicit allow for the exact bundle
// file the sandboxed pi cannot READ its own extension, so the extension silently never
// loads and the worker has no sextant_* tools / never reports step-done. This runs the
// REAL recipe in sandbox mode through a stub srt CLI (so it writes the profile then
// exits without a real jail/model) and asserts the generated .sx-srt-settings.json lists
// the extension path in filesystem.allowRead. Model-free + hermetic; runs in CI.
func TestRecipeSandboxAllowReadsExtension(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recipe is a POSIX sh script")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node required to run the recipe's srt-profile generator")
	}
	recipe, err := EmbeddedRecipe()
	if err != nil {
		t.Fatalf("read embedded recipe: %v", err)
	}

	dir := t.TempDir()
	recipePath := filepath.Join(dir, "pi.sh")
	if err := os.WriteFile(recipePath, recipe, 0o755); err != nil {
		t.Fatalf("write recipe: %v", err)
	}
	store := filepath.Join(dir, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	mustWrite(t, filepath.Join(store, "bus.json"), `{"url":"nats://127.0.0.1:4222"}`)
	credsPath := filepath.Join(dir, "child.creds")
	mustWrite(t, credsPath, `{"fake":"creds"}`)
	// The extension bundle path — what must end up allow-read in the profile.
	extPath := filepath.Join(dir, "ext", "pi-bus.bundle.mjs")
	if err := os.MkdirAll(filepath.Dir(extPath), 0o755); err != nil {
		t.Fatalf("mkdir ext: %v", err)
	}
	mustWrite(t, extPath, "export default function(){}\n")
	workdir := filepath.Join(dir, "wd")

	// Stub srt CLI: the recipe resolves SX_PI_SRT_CLI and `exec node "$SRT_CLI" … pi`
	// AFTER writing the profile, so a stub that exits 0 lets the recipe complete with the
	// profile on disk and never launches a real jail or model. A stub `pi` + sandbox-exec
	// satisfy the recipe's preflight (Darwin checks `command -v sandbox-exec`, present on
	// a mac runner; on Linux the recipe does not require it).
	srtCli := filepath.Join(dir, "srt-cli.js")
	mustWrite(t, srtCli, "process.exit(0);\n")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	mustWrite(t, filepath.Join(bin, "pi"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "pi"), 0o755); err != nil {
		t.Fatalf("chmod stub pi: %v", err)
	}

	cmd := exec.Command("sh", recipePath)
	cmd.Env = append(
		os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SEXTANT_CREDS="+credsPath,
		"SEXTANT_STORE="+store,
		"SEXTANT_PI_EXTENSION="+extPath,
		"SX_PROMPT=do the thing",
		"SX_CHILD_ID=01CHILDAAAAAAAAAAAAAAAAAAA",
		"SX_CHILD_NICK=writer",
		"SX_AGENT_MODEL=claude-haiku-4-5",
		"SEXTANT_PI_WORKDIR="+workdir,
		"SX_PI_SANDBOX_MODE=sandbox",
		"SX_PI_SRT_CLI="+srtCli,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recipe (sandbox mode) run failed: %v\noutput:\n%s", err, out)
	}

	profile, err := os.ReadFile(filepath.Join(workdir, ".sx-srt-settings.json"))
	if err != nil {
		t.Fatalf("read generated srt profile: %v", err)
	}
	if !strings.Contains(string(profile), extPath) {
		t.Errorf("srt profile does NOT allow-read the extension bundle %q — the sandboxed pi cannot read its own extension, so it silently never loads (TASK-42 #2).\nprofile:\n%s", extPath, profile)
	}
	// Be specific: the path must be in allowRead, not (only) in denyRead.
	type srtProfile struct {
		Filesystem struct {
			AllowRead []string `json:"allowRead"`
		} `json:"filesystem"`
	}
	var p srtProfile
	if err := json.Unmarshal(profile, &p); err != nil {
		t.Fatalf("parse srt profile JSON: %v\n%s", err, profile)
	}
	found := false
	for _, r := range p.Filesystem.AllowRead {
		if r == extPath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("extension %q not in filesystem.allowRead %v (TASK-42 #2)", extPath, p.Filesystem.AllowRead)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
