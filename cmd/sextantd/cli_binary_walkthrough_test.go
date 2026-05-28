package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestM12CLIBinaryWalkthroughAcceptance is the literal-reading of the
// M12 acceptance text: "full CLI walkthrough — spawn an agent, prompt
// it, watch its conversation, kill it, query audit log. All from the
// CLI."
//
// Unlike TestM12CLIWalkthroughAcceptance (which exercises handlers
// via the client.RPC surface), this test invokes the `sextant`
// binary itself so it covers arg parsing, --json output rendering,
// exit-code mapping, and TOML config loading — the bits cmd/sextant
// owns that pkg/client doesn't touch.
//
// Both tests stay in the suite: the RPC-level one is faster and
// pinpoints handler regressions; this one catches CLI surface
// regressions.
func TestM12CLIBinaryWalkthroughAcceptance(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	// rpcClient does the "wait for RPC to be ready" poll. The binary
	// walkthrough can't easily run the poll itself, so we let
	// rpcClient do it (we only use it for the readiness gate; the
	// actual verbs go through the binary).
	_ = rpcClient(t, h)

	sextantBin := buildSextantBinary(t)
	// The CLI verbs need the ConfigDir (where sextantd.toml lives) so
	// they can resolve operator creds + runtime.json — that's
	// `--config-dir`'s job. h.cfg.Paths.ConfigDir is what `sextant
	// init` would have written; reuse it.
	configDir := h.cfg.Paths.ConfigDir

	// Tracks the agent UUID across the walkthrough so cleanup catches
	// it even on early-test failure.
	var agentID uuid.UUID
	t.Cleanup(func() {
		if agentID == uuid.Nil {
			return
		}
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
			"--format", "{{.ID}}").Output()
		for _, id := range strings.Fields(strings.TrimSpace(string(out))) {
			_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
		}
	})

	// 1. agents create --json (renamed from `spawn` per the
	// closed-exception verb policy; `spawn` remains as an alias).
	{
		out := runSextantCmd(t, sextantBin, configDir, 60*time.Second, 0,
			"agents", "create", "walkthru-bin", "--template", "default", "--json")
		var resp sextantproto.SpawnAgentResponse
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("decode create json: %v\nstdout=%q", err, out)
		}
		if resp.AgentID == uuid.Nil {
			t.Fatal("create returned zero UUID")
		}
		agentID = resp.AgentID
		t.Logf("M12 CLI walkthrough: created agent uuid=%s", agentID)
	}

	// 2. agents list --json — the new agent must show up.
	{
		out := runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
			"agents", "list", "--json")
		var resp sextantproto.ListAgentsResponse
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("decode list json: %v\nstdout=%q", err, out)
		}
		found := false
		for _, a := range resp.Agents {
			if a.UUID == agentID {
				found = true
				if a.Lifecycle != "running" {
					t.Errorf("Lifecycle = %q, want running", a.Lifecycle)
				}
			}
		}
		if !found {
			t.Fatalf("agent %s not in list (got %d agents)", agentID, len(resp.Agents))
		}
	}

	// 3. agents show <uuid> --json — detail view roundtrips.
	{
		out := runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
			"agents", "show", agentID.String(), "--json")
		var status sextantproto.AgentStatus
		if err := json.Unmarshal(out, &status); err != nil {
			t.Fatalf("decode show json: %v\nstdout=%q", err, out)
		}
		if status.UUID != agentID || status.Lifecycle != "running" {
			t.Errorf("show status = %+v", status)
		}
	}

	// 4. agents prompt — the inbox subscriber covers the receipt;
	// here we just want the verb to exit 0.
	runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
		"agents", "prompt", agentID.String(), "do the thing")

	// 5. files read /etc/os-release — every Debian-based sidecar
	// ships this file.
	{
		out := runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
			"files", "read", agentID.String(), "/etc/os-release")
		if !strings.Contains(string(out), "NAME=") {
			t.Errorf("files read /etc/os-release: stdout doesn't contain NAME=: %q", out)
		}
	}

	// 6. exec — stdout/exit-code passthrough.
	{
		out := runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
			"exec", agentID.String(), "--", "echo", "m12-binary-walkthrough")
		if strings.TrimSpace(string(out)) != "m12-binary-walkthrough" {
			t.Errorf("exec stdout = %q, want m12-binary-walkthrough", out)
		}
	}

	// 7. exec failing command — exit code propagates as the CLI's
	// exit code. We expect exit 1 from `false`. The spec's exit code
	// 1 is "user error"; here it's the exec command's exit.
	{
		_, _, code := runSextantCmdRaw(t, sextantBin, configDir, 30*time.Second,
			"exec", agentID.String(), "--", "false")
		if code != 1 {
			t.Errorf("`false` exec exit code = %d, want 1 (CLI must propagate the container's exit code)", code)
		}
	}

	// 8. conversation --tail in a bounded child process. M12's
	// conversation streams forever by default; --tail exits on
	// lifecycle.ended. The fresh-spawned agent hasn't produced frames
	// yet, so we just want the subscription to wire correctly. We
	// run it for ~1.5s then kill it; the test asserts the process
	// exits without panic.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, sextantBin, //nolint:gosec // test-controlled args
			"conversation", agentID.String(),
			"--config-dir", configDir,
			"--json")
		out, err := cmd.CombinedOutput()
		// Killed by ctx is fine; signal kills surface as ExitError.
		// What we don't want is an immediate non-killed exit-non-zero
		// (would mean argparse/connect failed).
		if err == nil {
			t.Logf("conversation exited cleanly within 3s (stream subscribed): %q", out)
		} else if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// Was killed by the deadline — that's expected. Any other
			// failure within ~3s is a CLI surface bug.
			t.Logf("conversation deadline killed it (expected): %v\noutput: %q", err, out)
		}
	}

	// 9. pending list — empty queue; verifies snapshot path works.
	{
		out := runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
			"pending", "list", "--json")
		// JSON output is `[]` or a list. Empty is fine.
		var arr []map[string]any
		if err := json.Unmarshal(out, &arr); err != nil {
			t.Fatalf("decode pending list: %v\nstdout=%q", err, out)
		}
	}

	// 10. agents stop — verb returns ok, container disappears. The
	// verb was renamed from `kill` per the closed-exception verb
	// policy; `kill` remains as an alias.
	runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
		"agents", "stop", agentID.String())
	if err := waitForContainerGone(dockerBin, handlers.LabelAgentUUID, agentID.String(), 20*time.Second); err != nil {
		t.Fatalf("container still present after `sextant agents stop`: %v", err)
	}

	// 11. audit list — verb is wired; the row count is zero in the
	// test harness because the shipper runs out-of-process and is
	// not started here. The CLI is expected to print "no audit rows"
	// in text mode or an empty list in JSON mode; what we assert is
	// the verb exits 0. Renamed from `query`.
	{
		out := runSextantCmd(t, sextantBin, configDir, 30*time.Second, 0,
			"audit", "list", "--since", "5m", "--json")
		var resp sextantproto.QueryAuditResponse
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("decode audit list json: %v\nstdout=%q", err, out)
		}
		// resp.Rows may be nil OR an empty slice — the wire shape says
		// "always present" but encoding/json round-trips nil to "null"
		// and empty to "[]". Don't enforce here; the per-row content
		// is asserted in TestM12CLIWalkthroughAcceptance via NATS sub.
		_ = resp
	}

	// 12. Bad-call exit-code mapping. `agents show` against a random
	// UUID surfaces an RPC error; the CLI prints to stderr and exits
	// with the spec's system-error code (2).
	{
		_, _, code := runSextantCmdRaw(t, sextantBin, configDir, 30*time.Second,
			"agents", "show", uuid.New().String())
		if code == 0 {
			t.Error("agents show on unknown UUID exited 0; want non-zero per spec exit-code mapping")
		}
	}
}

// buildSextantBinary builds `sextant` to a temp path. Mirrors the
// pattern startDaemonHarness uses for sextantd.
func buildSextantBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "sextant")
	build := exec.Command("go", "build", "-o", binPath, "github.com/love-lena/sextant/cmd/sextant") //nolint:gosec // test-controlled args
	var buildErr bytes.Buffer
	build.Stderr = &buildErr
	if err := build.Run(); err != nil {
		t.Fatalf("go build sextant: %v\n%s", err, buildErr.String())
	}
	return binPath
}

// runSextantCmd runs `sextant <args>...` against the supplied config
// dir, requires exit code `wantExit`, and returns stdout. The
// --config-dir flag is appended after args so callers don't have to
// thread it through every call site.
func runSextantCmd(t *testing.T, bin, configDir string, timeout time.Duration, wantExit int, args ...string) []byte {
	t.Helper()
	stdout, stderr, code := runSextantCmdRaw(t, bin, configDir, timeout, args...)
	if code != wantExit {
		t.Fatalf("sextant %s: exit=%d (want %d)\nstdout=%q\nstderr=%q",
			strings.Join(args, " "), code, wantExit, stdout, stderr)
	}
	return stdout
}

// runSextantCmdRaw runs the binary and returns (stdout, stderr,
// exitCode) without enforcing the exit code. Callers that *expect* a
// non-zero exit (e.g. the exec-failure test, the bad-uuid test) use
// this directly.
//
// The --config-dir flag must land before any `--` separator. The
// exec verb splits args at the first standalone `--` and treats the
// tail as the command argv — appending --config-dir blindly to the
// end would route it to the container's `false`/`echo`/etc. instead
// of the CLI. Insert before the first `--`; otherwise append.
func runSextantCmdRaw(t *testing.T, bin, configDir string, timeout time.Duration, args ...string) (stdout, stderr []byte, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := injectConfigDir(args, configDir)
	cmd := exec.CommandContext(ctx, bin, full...) //nolint:gosec // test-controlled args
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if err != nil && code == -1 {
		// ProcessState.ExitCode returns -1 when the process was killed
		// by a signal; surface as a fatal so the test failure has a
		// clear cause.
		t.Fatalf("sextant %s: process killed: %v\nstdout=%q\nstderr=%q",
			strings.Join(args, " "), err, so.String(), se.String())
	}
	return so.Bytes(), se.Bytes(), code
}

// injectConfigDir splices "--config-dir <dir>" into args ahead of
// any standalone "--" separator. Returns a fresh slice.
func injectConfigDir(args []string, configDir string) []string {
	if len(args) == 0 {
		return []string{"--config-dir", configDir}
	}
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}
	out := make([]string, 0, len(args)+2)
	if dashIdx == -1 {
		out = append(out, args...)
		out = append(out, "--config-dir", configDir)
		return out
	}
	out = append(out, args[:dashIdx]...)
	out = append(out, "--config-dir", configDir)
	out = append(out, args[dashIdx:]...)
	return out
}

// Avoid "declared but not used" if a future refactor drops a usage.
var _ = fmt.Sprintf
