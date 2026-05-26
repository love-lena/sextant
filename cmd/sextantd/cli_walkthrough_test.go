package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestM12CLIWalkthroughAcceptance is the M12 acceptance test. It
// replays the spec's full operator walkthrough against a real
// daemon:
//
//  1. Spawn an agent (rpc.spawn_agent).
//  2. Subscribe to its frames subject (proves `sextant conversation`'s
//     subscribe wiring round-trips).
//  3. Prompt it (rpc.prompt_agent) — assert the inbox envelope lands.
//  4. Run an `exec_in_container` against the agent's container.
//  5. Read a file via `read_file` (the sidecar image has /etc/os-release).
//  6. Kill the agent.
//  7. Query the audit table (rpc.query_audit) — assert the spawn was
//     audited.
//
// Skipped when Docker isn't available, matching the M11 acceptance
// test's gate.
func TestM12CLIWalkthroughAcceptance(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 1. Spawn.
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(ctx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "walkthrough",
		Template: "default",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	t.Logf("spawned agent uuid=%s", agentID)

	// Belt-and-suspenders cleanup so a mid-test panic doesn't leak the
	// container.
	t.Cleanup(func() {
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
			"--format", "{{.ID}}").Output()
		for _, id := range strings.Fields(strings.TrimSpace(string(out))) {
			_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
		}
	})

	// 2. Subscribe to inbox so prompt_agent has a witness.
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	inboxSub := "agents." + agentID.String() + ".inbox"
	inboxMsgs, err := cli.Subscribe(subCtx, inboxSub)
	if err != nil {
		t.Fatalf("subscribe inbox: %v", err)
	}

	// 3. Prompt.
	var promptResp sextantproto.PromptAgentResponse
	if err := cli.RPC(ctx, rpc.VerbPromptAgent, sextantproto.PromptAgentRequest{
		AgentID: agentID,
		Content: "do the thing",
	}, &promptResp); err != nil {
		t.Fatalf("prompt_agent: %v", err)
	}
	if !promptResp.OK {
		t.Fatal("prompt_agent returned ok=false")
	}
	select {
	case msg, ok := <-inboxMsgs:
		if !ok {
			t.Fatal("inbox subscription closed before prompt landed")
		}
		if msg.Err != nil {
			t.Fatalf("inbox msg.Err: %v", msg.Err)
		}
		var p struct {
			Kind    string `json:"kind"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
			t.Fatalf("decode prompt payload: %v", err)
		}
		if p.Content != "do the thing" {
			t.Errorf("inbox content = %q, want %q", p.Content, "do the thing")
		}
		_ = msg.Ack()
	case <-time.After(15 * time.Second):
		t.Fatalf("prompt envelope did not arrive on %s within 15s", inboxSub)
	}

	// 4. exec_in_container — run `echo m12-ack` and assert output.
	var execResp sextantproto.ExecInContainerResponse
	if err := cli.RPC(ctx, rpc.VerbExecInContainer, sextantproto.ExecInContainerRequest{
		AgentID: agentID,
		Cmd:     []string{"echo", "m12-ack"},
	}, &execResp); err != nil {
		t.Fatalf("exec_in_container: %v", err)
	}
	if strings.TrimSpace(execResp.Stdout) != "m12-ack" {
		t.Errorf("exec stdout = %q, want m12-ack", execResp.Stdout)
	}
	if execResp.ExitCode != 0 {
		t.Errorf("exec exit code = %d, want 0", execResp.ExitCode)
	}

	// 5. read_file — every alpine-based sidecar ships /etc/os-release.
	var readResp sextantproto.ReadFileResponse
	if err := cli.RPC(ctx, rpc.VerbReadFile, sextantproto.ReadFileRequest{
		AgentID: agentID, Path: "/etc/os-release",
	}, &readResp); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(string(readResp.Content), "NAME=") {
		t.Errorf("/etc/os-release didn't contain NAME=: %q", readResp.Content)
	}

	// 6. Kill.
	var killResp sextantproto.KillAgentResponse
	if err := cli.RPC(ctx, rpc.VerbKillAgent, sextantproto.KillAgentRequest{
		AgentID: agentID, GraceSeconds: 5,
	}, &killResp); err != nil {
		t.Fatalf("kill_agent: %v", err)
	}
	if !killResp.OK {
		t.Error("kill_agent returned ok=false")
	}
	if err := waitForContainerGone(dockerBin, handlers.LabelAgentUUID, agentID.String(), 20*time.Second); err != nil {
		t.Fatalf("container still present after kill: %v", err)
	}

	// 7. Audit — assert the spawn was audited. The shipper runs as a
	// separate binary; the daemon's NATS publishes `audit.rpc` for
	// every dispatch, but the rows reach ClickHouse only when the
	// shipper subscribes. To keep the acceptance test self-contained,
	// we subscribe directly to audit.> via the same client and assert
	// the rpc.spawn_agent envelope is in the stream.
	//
	// This proves the same wiring query_audit's CLI surface depends on
	// (the daemon emits audit envelopes for every RPC dispatch);
	// asserting the shipper round-trips into ClickHouse is M9 turf
	// (covered in pkg/shipper tests).
	auditCtx, auditCancel := context.WithCancel(context.Background())
	defer auditCancel()
	auditMsgs, err := cli.Subscribe(auditCtx, "audit.>", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("subscribe audit.>: %v", err)
	}
	spawnAudited := false
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
auditScan:
	for !spawnAudited {
		select {
		case msg, ok := <-auditMsgs:
			if !ok {
				break auditScan
			}
			if msg.Err != nil {
				continue
			}
			var ap sextantproto.AuditPayload
			if err := json.Unmarshal(msg.Envelope.Payload, &ap); err != nil {
				continue
			}
			if ap.Action == "rpc.spawn_agent" {
				spawnAudited = true
			}
			_ = msg.Ack()
		case <-deadline.C:
			break auditScan
		}
	}
	if !spawnAudited {
		t.Fatalf("audit.rpc spawn envelope never observed on audit.>")
	}

	// Exercise the query_audit RPC and assert it returns non-empty
	// rows. Pre-feat-shipper-auto-supervise the audit table would be
	// empty (operator hadn't started the shipper), but with sextantd
	// now auto-supervising sextant-shipper (Shipper.AutoSupervise=true
	// by default) the rpc.spawn_agent envelope we already saw on
	// audit.> must land in ClickHouse within the shipper's flush
	// interval. Poll up to 15s so the assertion tolerates a slow first
	// shipper batch.
	var auditResp sextantproto.QueryAuditResponse
	queryDeadline := time.Now().Add(15 * time.Second)
	for {
		queryCtx, queryCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := cli.RPC(queryCtx, rpc.VerbQueryAudit, sextantproto.QueryAuditRequest{
			Limit: 50,
		}, &auditResp)
		queryCancel()
		if err != nil {
			t.Fatalf("query_audit RPC: %v", err)
		}
		if auditResp.Rows == nil {
			t.Fatal("query_audit returned nil Rows (spec says always non-nil)")
		}
		if len(auditResp.Rows) > 0 {
			t.Logf("query_audit rows=%d (shipper landed the envelope)", len(auditResp.Rows))
			break
		}
		if !time.Now().Before(queryDeadline) {
			t.Fatalf("query_audit returned 0 rows after 15s; expected the spawn envelope to be shipped\n--- daemon log ---\n%s",
				h.tail(t))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestM12RestartRoundtrip exercises the restart_agent flow against a
// live daemon. Same Docker gate as the walkthrough above.
func TestM12RestartRoundtrip(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(ctx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "restartme",
		Template: "default",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent: %v\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID

	t.Cleanup(func() {
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
			"--format", "{{.ID}}").Output()
		for _, id := range strings.Fields(strings.TrimSpace(string(out))) {
			_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
		}
	})

	// Capture the original container id via docker ps.
	orig := containersWithLabel(t, dockerBin, handlers.LabelAgentUUID, agentID.String())
	if len(orig) != 1 {
		t.Fatalf("expected 1 container pre-restart, got %v", orig)
	}
	origID := orig[0]

	// Restart. The handler stops the live container + starts a fresh
	// one — both Docker operations can take 5-10s each, so we override
	// the default 10s client timeout.
	var restartResp sextantproto.RestartAgentResponse
	if err := cli.RPC(ctx, rpc.VerbRestartAgent, sextantproto.RestartAgentRequest{
		AgentID: agentID,
	}, &restartResp, client.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("restart_agent: %v\n%s", err, h.tail(t))
	}
	if !restartResp.OK || restartResp.AgentID != agentID {
		t.Fatalf("RestartAgentResponse = %+v", restartResp)
	}

	// Old container is gone, a new one is up (within ~20s).
	if err := waitForContainerID(dockerBin, handlers.LabelAgentUUID, agentID.String(), origID, 20*time.Second); err != nil {
		t.Fatalf("restart never produced a new container: %v\n%s", err, h.tail(t))
	}

	// Now kill the agent and assert clean cleanup.
	var killResp sextantproto.KillAgentResponse
	if err := cli.RPC(ctx, rpc.VerbKillAgent, sextantproto.KillAgentRequest{
		AgentID: agentID, GraceSeconds: 5,
	}, &killResp); err != nil {
		t.Fatalf("kill_agent post-restart: %v", err)
	}
	if err := waitForContainerGone(dockerBin, handlers.LabelAgentUUID, agentID.String(), 20*time.Second); err != nil {
		t.Fatalf("container still present after kill: %v", err)
	}
}

// waitForContainerID polls `docker ps` until at least one container
// matches the label AND its ID is not in the `exclude` set. Returns
// nil on success.
func waitForContainerID(dockerBin, key, value, exclude string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := exec.Command(dockerBin, "ps", //nolint:gosec // test-controlled args
			"--filter", "label="+key+"="+value,
			"--format", "{{.ID}}").Output()
		for _, id := range strings.Fields(strings.TrimSpace(string(out))) {
			if id != exclude {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("timeout waiting for a fresh container id")
}

// Avoid "imported and not used" if client is only referenced through
// rpcClient (which lives in rpc_test.go).
var _ = (*client.Client)(nil)
