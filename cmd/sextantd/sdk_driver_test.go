package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestSidecarSDKDriverMockRoundTrip is the integration test for the
// post-Phase-1 SDK driver wire-up. It runs the sidecar in --driver=mock
// mode (canned events, no Anthropic API call) so CI doesn't depend on
// the operator's ANTHROPIC_API_KEY.
//
// Flow:
//
//  1. Boot the full daemon stack via startDaemonHarness.
//  2. Spawn an agent against the `mock-driver` template (writeMinimalInstall
//     seeds it with env.SEXTANT_DRIVER=mock).
//  3. Subscribe to agents.<uuid>.frames AND agents.<uuid>.lifecycle BEFORE
//     prompting so we don't race the publishes.
//  4. Send a prompt via the prompt_agent RPC.
//  5. Assert: an agent_frame envelope arrives with frame_kind=assistant_text
//     and body.text echoing "ack: <prompt>".
//  6. Assert: a lifecycle envelope arrives with transition=turn_ended.
//  7. kill_agent and verify cleanup.
//
// The mock driver mirrors the real SDK driver's contract on the bus:
// publishing the same frame kinds and the same lifecycle.turn_ended
// envelope, so this test proves the *integration* surface end-to-end
// (RPC → inbox → driver → frames / lifecycle). The live SDK path is
// exercised by the manual smoke walkthrough captured in
// plans/wire-up-complete.md.
func TestSidecarSDKDriverMockRoundTrip(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	// Subscribe BEFORE spawning so we capture every envelope on the
	// agent's subjects (JetStream replays from-seq=0 won't help once
	// the subjects are agents.<uuid>.* — the uuid is allocated by
	// spawn). We use a wildcard then filter.
	frameCtx, frameCancel := context.WithCancel(context.Background())
	defer frameCancel()
	frameMsgs, err := cli.Subscribe(frameCtx, "agents.*.frames", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe frames: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	defer lifeCancel()
	lifeMsgs, err := cli.Subscribe(lifeCtx, "agents.*.lifecycle", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe lifecycle: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}

	// Spawn against the mock-driver template.
	spawnCtx, spawnCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "sdk-mock-" + uuid.New().String()[:8],
		Template: "mock-driver",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	if agentID == uuid.Nil {
		t.Fatal("spawn_agent returned zero UUID")
	}
	t.Logf("spawned mock-driver agent uuid=%s", agentID)

	// Safety-net cleanup before the assertion chain.
	t.Cleanup(func() {
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
			"--format", "{{.ID}}").Output()
		ids := strings.Fields(strings.TrimSpace(string(out)))
		for _, id := range ids {
			_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
		}
	})

	// Wait for lifecycle.started so we know the sidecar is up and
	// subscribed to its inbox before we prompt.
	if err := waitForLifecycleStarted(t, lifeMsgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v\n--- container logs ---\n%s",
			err, containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// Send the prompt.
	promptText := "reply with just the word ack"
	promptCtx, promptCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer promptCancel()
	var promptResp sextantproto.PromptAgentResponse
	if err := cli.RPC(promptCtx, rpc.VerbPromptAgent, sextantproto.PromptAgentRequest{
		AgentID: agentID,
		Content: promptText,
	}, &promptResp); err != nil {
		t.Fatalf("prompt_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if !promptResp.OK {
		t.Fatal("prompt_agent returned ok=false")
	}

	// Frame: an agent_frame with frame_kind=assistant_text and body
	// containing "ack". 30s is generous for the mock driver — the
	// turn-around time is dominated by container/runtime overhead, not
	// the canned publish.
	if err := waitForAssistantText(frameMsgs, agentID, "ack", 30*time.Second); err != nil {
		t.Fatalf("assistant_text frame: %v\n--- container logs ---\n%s",
			err, containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// Lifecycle: turn_ended (the mock driver publishes this after the
	// canned frame).
	if err := waitForTurnEnded(lifeMsgs, agentID, 15*time.Second); err != nil {
		t.Fatalf("lifecycle.turn_ended: %v\n--- container logs ---\n%s",
			err, containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// Tear down.
	killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer killCancel()
	var killResp sextantproto.KillAgentResponse
	if err := cli.RPC(killCtx, rpc.VerbKillAgent, sextantproto.KillAgentRequest{
		AgentID:      agentID,
		GraceSeconds: 5,
	}, &killResp); err != nil {
		t.Fatalf("kill_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if err := waitForContainerGone(dockerBin, handlers.LabelAgentUUID, agentID.String(), 20*time.Second); err != nil {
		t.Fatalf("container still present after kill_agent: %v", err)
	}
}

// waitForAssistantText drains the subscription channel until an
// agent_frame envelope with frame_kind=assistant_text and body.text
// containing `wantSubstr` (case-insensitive) for agentID arrives.
func waitForAssistantText(
	ch <-chan client.Message,
	agentID uuid.UUID,
	wantSubstr string,
	timeout time.Duration,
) error {
	wantLower := strings.ToLower(wantSubstr)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return errors.New("frame subscription closed")
			}
			if msg.Err != nil {
				continue
			}
			env := msg.Envelope
			if env.Kind != sextantproto.KindAgentFrame {
				_ = msg.Ack()
				continue
			}
			// The envelope's From is the agent address; assert it matches.
			if env.From.Kind != sextantproto.AddressAgent || env.From.ID != agentID.String() {
				_ = msg.Ack()
				continue
			}
			var p sextantproto.AgentFramePayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				_ = msg.Ack()
				continue
			}
			if p.FrameKind != sextantproto.FrameAssistantText {
				_ = msg.Ack()
				continue
			}
			text, _ := p.Body["text"].(string)
			if strings.Contains(strings.ToLower(text), wantLower) {
				_ = msg.Ack()
				return nil
			}
			_ = msg.Ack()
		case <-deadline.C:
			return errors.New("timeout waiting for assistant_text frame")
		}
	}
}

// waitForTurnEnded drains the subscription channel until a lifecycle
// envelope with transition=turn_ended for agentID arrives.
func waitForTurnEnded(
	ch <-chan client.Message,
	agentID uuid.UUID,
	timeout time.Duration,
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return errors.New("lifecycle subscription closed")
			}
			if msg.Err != nil {
				continue
			}
			if msg.Envelope.Kind != sextantproto.KindLifecycle {
				_ = msg.Ack()
				continue
			}
			var p sextantproto.LifecyclePayload
			if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
				_ = msg.Ack()
				continue
			}
			if p.AgentUUID == agentID && p.Transition == sextantproto.LifecycleTurnEnded {
				_ = msg.Ack()
				return nil
			}
			_ = msg.Ack()
		case <-deadline.C:
			return errors.New("timeout waiting for lifecycle.turn_ended")
		}
	}
}
