package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func fakeLookup(present ...string) lookPathFn {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/fake/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func okDocker(context.Context) error  { return nil }
func badDocker(context.Context) error { return errors.New("connection refused") }

func fakeGoVersion(ver string) func(string, ...string) ([]byte, error) {
	return func(_ string, _ ...string) ([]byte, error) {
		return []byte("go version go" + ver + " darwin/arm64\n"), nil
	}
}

func TestPreflight_AllPresent(t *testing.T) {
	lookFn := fakeLookup("nats-server", "clickhouse", "docker", "go", "node", "npm")
	results := collectHostDepChecks(context.Background(), true, lookFn, okDocker, fakeGoVersion("1.26.1"))
	for _, r := range results {
		if r.Status != StatusPass {
			t.Errorf("check %s: status = %s, want pass (%s)", r.Check, r.Status, r.Detail)
		}
	}
}

func TestPreflight_NatsMissing(t *testing.T) {
	lookFn := fakeLookup("clickhouse", "docker")
	results := collectHostDepChecks(context.Background(), false, lookFn, okDocker, fakeGoVersion("1.26.1"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "nats-server" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no nats-server row in results")
	}
	if row.Status != StatusFail {
		t.Errorf("nats-server status = %s, want fail", row.Status)
	}
	if !strings.Contains(row.Remedy, "nats-server") {
		t.Errorf("remedy = %q, want it to mention nats-server", row.Remedy)
	}
}

func TestPreflight_DockerBinaryPresentDaemonDown(t *testing.T) {
	lookFn := fakeLookup("docker")
	results := collectHostDepChecks(context.Background(), false, lookFn, badDocker, fakeGoVersion("1.26.1"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "docker-daemon" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no docker-daemon row in results")
	}
	if row.Status != StatusWarn {
		t.Errorf("docker-daemon status = %s, want warn (binary present but daemon down)", row.Status)
	}
	if !strings.Contains(strings.ToLower(row.Remedy), "orbstack") && !strings.Contains(strings.ToLower(row.Remedy), "docker") {
		t.Errorf("remedy = %q, want it to mention OrbStack or Docker", row.Remedy)
	}
}

func TestPreflight_DockerBinaryMissingDaemonProbeWarns(t *testing.T) {
	lookFn := fakeLookup()
	results := collectHostDepChecks(context.Background(), false, lookFn, okDocker, fakeGoVersion("1.26.1"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "docker-daemon" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no docker-daemon row in results")
	}
	if row.Status != StatusWarn {
		t.Errorf("docker-daemon status = %s, want warn (binary missing so daemon probe skipped)", row.Status)
	}
}

func TestPreflight_ContributorModeIncludesGoNodeNpm(t *testing.T) {
	lookFn := fakeLookup("nats-server", "clickhouse", "docker", "go", "node", "npm")
	contrib := collectHostDepChecks(context.Background(), true, lookFn, okDocker, fakeGoVersion("1.26.1"))
	operator := collectHostDepChecks(context.Background(), false, lookFn, okDocker, fakeGoVersion("1.26.1"))
	seen := func(rs []CheckResult, name string) bool {
		for _, r := range rs {
			if r.Check == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"go", "node", "npm"} {
		if !seen(contrib, name) {
			t.Errorf("contributor mode missing %s row", name)
		}
		if seen(operator, name) {
			t.Errorf("operator mode unexpectedly includes %s row", name)
		}
	}
}

func TestPreflight_GoVersionUnparseable(t *testing.T) {
	lookFn := fakeLookup("go")
	garbledRunFn := func(_ string, _ ...string) ([]byte, error) {
		return []byte("totally garbled\n"), nil
	}
	results := collectHostDepChecks(context.Background(), true, lookFn, okDocker, garbledRunFn)
	var row *CheckResult
	for i := range results {
		if results[i].Check == "go" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no go row in results")
	}
	if row.Status != StatusFail {
		t.Errorf("go status = %s, want fail on unparseable output", row.Status)
	}
	if !strings.Contains(row.Detail, "unparseable") {
		t.Errorf("detail = %q, want it to mention 'unparseable'", row.Detail)
	}
}

func TestPreflight_GoTooOld(t *testing.T) {
	lookFn := fakeLookup("go")
	results := collectHostDepChecks(context.Background(), true, lookFn, okDocker, fakeGoVersion("1.20.0"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "go" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no go row in results")
	}
	if row.Status != StatusFail {
		t.Errorf("go status = %s, want fail (too old)", row.Status)
	}
	if !strings.Contains(row.Detail, "1.20") {
		t.Errorf("detail = %q, want it to mention the found version", row.Detail)
	}
}
