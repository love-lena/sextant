package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorAgainstFreshInit(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)

	// Expect: every static check (config, ca, operator-creds, clickhouse-password,
	// templates, data-dirs) passes; daemon check is "not-running".
	var failures, notRunning int
	wantPass := map[string]bool{
		"config":              true,
		"ca":                  true,
		"operator-creds":      true,
		"clickhouse-password": true,
		"templates":           true,
	}
	seen := map[string]bool{}
	for _, r := range results {
		if r.Status == StatusFail {
			failures++
		}
		if r.Status == StatusNotRunning {
			notRunning++
		}
		if wantPass[r.Kind] {
			if r.Status != StatusPass {
				t.Errorf("kind %s status = %s, want pass: %s", r.Kind, r.Status, r.Detail)
			}
			seen[r.Kind] = true
		}
	}
	for k := range wantPass {
		if !seen[k] {
			t.Errorf("missing check %s in doctor output", k)
		}
	}
	if failures != 0 {
		t.Errorf("expected zero failures, got %d (%+v)", failures, results)
	}
	if notRunning != 1 {
		t.Errorf("expected exactly one 'not-running' row, got %d", notRunning)
	}
}

func TestDoctorReportsCorruptedCA(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	// Corrupt the CA private key.
	if err := os.WriteFile(filepath.Join(opts.ConfigDir, "ca.key"), []byte("not a real key"), 0o600); err != nil {
		t.Fatalf("corrupt ca.key: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)
	var caRow *CheckResult
	for i := range results {
		if results[i].Kind == "ca" {
			caRow = &results[i]
		}
	}
	if caRow == nil {
		t.Fatal("no ca row in results")
	}
	if caRow.Status != StatusFail {
		t.Errorf("ca status = %s, want fail", caRow.Status)
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)
	var out bytes.Buffer
	emit(&out, results, true)
	var parsed []CheckResult
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("emit JSON output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(parsed) != len(results) {
		t.Errorf("parsed %d rows, want %d", len(parsed), len(results))
	}
}

func TestDoctorFailsOnMissingConfig(t *testing.T) {
	dir := t.TempDir()
	results := collectChecks(context.Background(), filepath.Join(dir, "cfg"), filepath.Join(dir, "data"))
	hasFail := false
	for _, r := range results {
		if r.Status == StatusFail {
			hasFail = true
		}
	}
	if !hasFail {
		t.Error("expected at least one fail row when nothing is initialized")
	}
}
