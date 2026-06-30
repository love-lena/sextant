package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for TASK-256: the per-run worktree path declared in a spawn.request
// (SpawnRequest.Workdir, carried on managedAgent.workdir) ACTUALLY REACHES the worker
// as SEXTANT_PI_WORKDIR — and an EMPTY workdir leaves SEXTANT_PI_WORKDIR UNSET so the
// pi recipe falls back to its per-child scratch default (today's behaviour preserved).
//
// Uses a fake harness (a shell one-liner) that writes its env to a temp file, so we
// assert the value without a real pi binary — the same pattern as model_test.go.

// runHarnessCaptureEnv launches the dispatcher's harness for ag and returns the line the
// harness captured (env | grep PATTERN), or "" if the harness emitted nothing.
func runHarnessCaptureEnv(t *testing.T, ag *managedAgent, pattern string) string {
	t.Helper()
	envFile := filepath.Join(t.TempDir(), "env.txt")
	// `|| true` so the harness exits 0 even when grep matches nothing (the unset case) —
	// then the file exists (empty) and we can distinguish "unset" from "never ran".
	harness := "env | grep '" + pattern + "' > " + envFile + " || true"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d := &dispatcher{ctx: ctx, store: t.TempDir(), harness: harness}
	if err := d.launchHarness(ag, "test prompt"); err != nil {
		t.Fatalf("launchHarness: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(envFile); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("env file not written by harness (process may not have run): %v", err)
	}
	return strings.TrimSpace(string(b))
}

// TestLaunchHarness_WorkdirEnvVar (TASK-256): a non-empty workdir reaches the worker as
// SEXTANT_PI_WORKDIR.
//
// RED reproduction: remove the `if ag.workdir != "" { ... SEXTANT_PI_WORKDIR ... }` block
// from launchHarness. The harness then sees no SEXTANT_PI_WORKDIR and this test goes RED.
func TestLaunchHarness_WorkdirEnvVar(t *testing.T) {
	workdir := "/tmp/sxrun/01RUNWORKTREEEEEEEEEEEEEEE"
	ag := &managedAgent{id: "agent-wd-1", nick: "wd", credsPath: "/dev/null", job: "job", model: DefaultModel, workdir: workdir}

	got := runHarnessCaptureEnv(t, ag, "^SEXTANT_PI_WORKDIR=")
	want := "SEXTANT_PI_WORKDIR=" + workdir
	if got != want {
		t.Fatalf("SEXTANT_PI_WORKDIR not relayed:\n  got:  %q\n  want: %q\n"+
			"(the declared per-run worktree did not reach the worker's environment)", got, want)
	}
}

// TestLaunchHarness_EmptyWorkdirUnset (TASK-256, fallback preserved): an EMPTY workdir
// leaves SEXTANT_PI_WORKDIR UNSET — the dispatcher must not export a blank value, so the
// pi recipe falls back to its per-child scratch default.
//
// RED reproduction: drop the non-empty guard and always append SEXTANT_PI_WORKDIR. The
// harness would then see "SEXTANT_PI_WORKDIR=" (empty) and this test goes RED — and the
// recipe's fail-loud guard (empty workdir → EX_CONFIG) would refuse to spawn in prod.
func TestLaunchHarness_EmptyWorkdirUnset(t *testing.T) {
	ag := &managedAgent{id: "agent-wd-2", nick: "wd-none", credsPath: "/dev/null", job: "job", model: DefaultModel, workdir: ""}

	got := runHarnessCaptureEnv(t, ag, "^SEXTANT_PI_WORKDIR=")
	if got != "" {
		t.Fatalf("SEXTANT_PI_WORKDIR must be UNSET for an empty workdir, but the harness saw %q\n"+
			"(an exported blank value breaks the recipe's scratch-default fallback)", got)
	}
}
