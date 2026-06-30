package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for TASK-245: per-step model routing — the model declared in a
// spawn.request ACTUALLY REACHES the worker's SX_AGENT_MODEL.
//
// These tests cover the three ACs at the dispatcher's pre-spawn gate:
//   - AC#1: a declared model flows end-to-end into SX_AGENT_MODEL
//   - AC#2: no declared model → DefaultModel is used
//   - AC#3: an unsupported/unknown model → fails loud at dispatch, no worker spawned

// TestResolveModel_DeclaredModel covers AC#1: a supported declared model is
// returned verbatim, unchanged.
func TestResolveModel_DeclaredModel(t *testing.T) {
	// Use a model that is NOT the default to prove routing is real.
	model, err := resolveModel("claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("resolveModel(supported): unexpected error: %v", err)
	}
	if model != "claude-sonnet-4-5" {
		t.Fatalf("resolveModel returned %q, want %q (model must not be changed)", model, "claude-sonnet-4-5")
	}
	// Adversarial: the returned model MUST differ from the default to prove we
	// are not silently collapsing to the default.
	if model == DefaultModel {
		t.Fatalf("declared non-default model %q resolved to DefaultModel %q — routing is broken", "claude-sonnet-4-5", DefaultModel)
	}
}

// TestResolveModel_EmptyIsDefault covers AC#2: a step with no declared model
// gets the dispatcher's documented default. The assertion is against the ACTUAL
// returned value — not a hardcoded constant — so a change to DefaultModel is
// caught here too.
func TestResolveModel_EmptyIsDefault(t *testing.T) {
	model, err := resolveModel("")
	if err != nil {
		t.Fatalf("resolveModel(empty): unexpected error: %v", err)
	}
	if model != DefaultModel {
		t.Fatalf("empty model resolved to %q, want DefaultModel %q", model, DefaultModel)
	}
}

// TestResolveModel_UnsupportedModel covers AC#3: an unknown model FAILS LOUD
// at dispatch, before any worker is spawned.
//
// RED reproduction: remove the supported-model check from resolveModel (i.e.
// always return the requested model). The test asserts err != nil for
// "gpt5-ultra-bogus" — without the check err is nil and this test goes RED.
func TestResolveModel_UnsupportedModel(t *testing.T) {
	_, err := resolveModel("gpt5-ultra-bogus")
	if err == nil {
		t.Fatal("resolveModel(unsupported): expected an error for an unknown model, got nil — AC#3 gate is broken; a bogus model must fail loud before any worker is spawned")
	}
	// The error message must name the model and mention the supported set.
	msg := err.Error()
	if !strings.Contains(msg, "gpt5-ultra-bogus") {
		t.Errorf("error must name the unsupported model; got: %v", err)
	}
	if !strings.Contains(msg, "supported") {
		t.Errorf("error must reference the supported-model set; got: %v", err)
	}
}

// TestResolveModel_AllSupportedModelsResolve guards against SupportedModels
// containing a model that resolveModel itself rejects.
func TestResolveModel_AllSupportedModelsResolve(t *testing.T) {
	for m := range SupportedModels {
		got, err := resolveModel(m)
		if err != nil {
			t.Errorf("resolveModel(%q) = err %v, want nil (it is in SupportedModels)", m, err)
		}
		if got != m {
			t.Errorf("resolveModel(%q) = %q, want the exact model back", m, got)
		}
	}
}

// TestLaunchHarness_ModelEnvVar covers AC#1's end-to-end flow: the model
// declared for an agent actually reaches SX_AGENT_MODEL in the harness
// environment. Uses a fake harness (a shell one-liner) that writes env to a
// temp file so we can assert the value without a real pi binary.
//
// RED reproduction: remove "SX_AGENT_MODEL=" + model from launchHarness env.
// The harness then sees no SX_AGENT_MODEL (or the ambient env's value, never
// "claude-sonnet-4-5") and this test goes RED.
func TestLaunchHarness_ModelEnvVar(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.txt")

	// Fake harness: writes SX_AGENT_MODEL to the env file and exits.
	harness := "env | grep '^SX_AGENT_MODEL=' > " + envFile

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d := &dispatcher{
		ctx:     ctx,
		store:   t.TempDir(),
		harness: harness,
	}
	ag := &managedAgent{
		id:        "test-agent-01",
		nick:      "test",
		credsPath: "/dev/null",
		job:       "test-job",
		model:     "claude-sonnet-4-5", // non-default to prove routing is real
	}

	if err := d.launchHarness(ag, "test prompt"); err != nil {
		t.Fatalf("launchHarness: %v", err)
	}

	// Wait for the harness process to write the file.
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
	got := strings.TrimSpace(string(b))
	want := "SX_AGENT_MODEL=claude-sonnet-4-5"
	if got != want {
		t.Fatalf("SX_AGENT_MODEL not relayed correctly:\n  got:  %q\n  want: %q\n"+
			"(AC#1 FAIL: the declared model did not reach the worker's environment)", got, want)
	}
}

// TestLaunchHarness_DefaultModelEnvVar covers AC#2's env-relay: when no model
// is declared (ag.model == ""), SX_AGENT_MODEL is set to DefaultModel.
func TestLaunchHarness_DefaultModelEnvVar(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.txt")
	harness := "env | grep '^SX_AGENT_MODEL=' > " + envFile

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d := &dispatcher{
		ctx:     ctx,
		store:   t.TempDir(),
		harness: harness,
	}
	ag := &managedAgent{
		id:        "test-agent-02",
		nick:      "test-default",
		credsPath: "/dev/null",
		job:       "test-job",
		model:     "", // no declared model → DefaultModel
	}

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
		t.Fatalf("env file not written: %v", err)
	}
	got := strings.TrimSpace(string(b))
	want := "SX_AGENT_MODEL=" + DefaultModel
	if got != want {
		t.Fatalf("default model not relayed: harness saw %q, want %q\n"+
			"(AC#2 FAIL: a step with no model should use the documented default)", got, want)
	}
}
