package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestSidecarSDKDriverMockRoundTrip is the success-path integration
// test for the post-Phase-1 SDK driver wire-up. It runs the sidecar
// in --driver=mock mode so CI doesn't depend on the operator's
// ANTHROPIC_API_KEY.
//
// The mock driver mirrors what the real SDK driver publishes for a
// successful tool-using turn (system_note init → assistant_text →
// tool_call → tool_result → lifecycle.turn_ended success). This test
// asserts each kind explicitly so any future drift between mock and
// real shows up as a test diff.
//
// The error path is covered by TestSidecarSDKDriverMockErrorPath.
func TestSidecarSDKDriverMockRoundTrip(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	// Subscribe BEFORE spawning so we capture every envelope on the
	// agent's subjects (deliverAll catches publishes that race
	// against consumer creation).
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

	agentID := spawnMockAgent(t, h, cli, dockerBin, "sdk-mock-ok-")

	if err := waitForLifecycleStarted(t, lifeMsgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v\n--- container logs ---\n%s",
			err, containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// Send the success prompt.
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

	// Drain the frames for this agent until lifecycle.turn_ended.
	// Pull each frame_kind out of the stream so the assertions catch
	// missing kinds (e.g. if the mock stops emitting tool_call we want
	// to know — that's the kind of drift between mock and real that
	// would silently weaken coverage).
	frames := collectFramesUntilTurnEnded(t, frameMsgs, lifeMsgs, agentID, 30*time.Second)
	assertFrameKindPresent(t, frames, sextantproto.FrameSystemNote, "system_note")
	assertAssistantTextContains(t, frames, "ack")
	assertToolCall(t, frames, "mock_echo")
	assertToolResult(t, frames, false /* not error */)
	if frames.turnReason != "" {
		t.Errorf("turn_ended reason = %q, want empty (success)", frames.turnReason)
	}

	// Tear down.
	killAndConfirm(t, cli, h, dockerBin, agentID)
}

// TestSidecarSDKDriverMockErrorPath drives the same mock driver with
// an `error:` prompt; the mock surfaces a frame_kind=error frame and a
// lifecycle.turn_ended carrying reason="error". This proves the
// failure path on the bus — the real SDK driver follows the same
// shape when the SDK reports an error (see newSDKDriver's catch
// block and the SDKResultError handling in handleSDKMessage).
func TestSidecarSDKDriverMockErrorPath(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

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

	agentID := spawnMockAgent(t, h, cli, dockerBin, "sdk-mock-err-")

	if err := waitForLifecycleStarted(t, lifeMsgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v\n--- container logs ---\n%s",
			err, containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// `error:` prefix tells the mock to surface a failure.
	promptCtx, promptCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer promptCancel()
	var promptResp sextantproto.PromptAgentResponse
	if err := cli.RPC(promptCtx, rpc.VerbPromptAgent, sextantproto.PromptAgentRequest{
		AgentID: agentID,
		Content: "error: simulated turn failure",
	}, &promptResp); err != nil {
		t.Fatalf("prompt_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}

	frames := collectFramesUntilTurnEnded(t, frameMsgs, lifeMsgs, agentID, 30*time.Second)
	// Success-path frames must not be present on the error path.
	for _, f := range frames.byKind[sextantproto.FrameAssistantText] {
		t.Errorf("unexpected assistant_text frame on error path: %v", f.Body)
	}
	for _, f := range frames.byKind[sextantproto.FrameToolCall] {
		t.Errorf("unexpected tool_call frame on error path: %v", f.Body)
	}
	assertFrameKindPresent(t, frames, sextantproto.FrameSystemNote, "system_note")
	errFrames := frames.byKind[sextantproto.FrameError]
	if len(errFrames) == 0 {
		t.Fatalf("error frame missing on error path; got kinds %v\n--- container logs ---\n%s",
			frames.kindList(), containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}
	msg, _ := errFrames[0].Body["message"].(string)
	if !strings.Contains(msg, "mock_error") {
		t.Errorf("error frame body.message = %q, want substring %q", msg, "mock_error")
	}
	if frames.turnReason != "error" {
		t.Errorf("turn_ended reason = %q, want %q", frames.turnReason, "error")
	}

	killAndConfirm(t, cli, h, dockerBin, agentID)
}

// spawnMockAgent spawns one mock-driver agent and registers the
// container-cleanup safety net. Returns the agent UUID.
func spawnMockAgent(
	t *testing.T,
	h *daemonHarness,
	cli *client.Client,
	dockerBin, namePrefix string,
) uuid.UUID {
	t.Helper()
	spawnCtx, spawnCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     namePrefix + uuid.New().String()[:8],
		Template: "mock-driver",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	if agentID == uuid.Nil {
		t.Fatal("spawn_agent returned zero UUID")
	}
	t.Logf("spawned mock-driver agent uuid=%s", agentID)

	t.Cleanup(func() {
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
			"--format", "{{.ID}}").Output()
		ids := strings.Fields(strings.TrimSpace(string(out)))
		for _, id := range ids {
			_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
		}
	})
	return agentID
}

// killAndConfirm tears down the agent + verifies the container is gone.
func killAndConfirm(
	t *testing.T,
	cli *client.Client,
	h *daemonHarness,
	dockerBin string,
	agentID uuid.UUID,
) {
	t.Helper()
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
		t.Fatalf("container still present after kill_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
}

// collectedFrames is the bundle of frames + the terminating
// lifecycle.turn_ended observed during one turn, indexed by frame_kind
// for assertions. Order is preserved within each kind so an assertion
// that wants to inspect the first frame can do so.
type collectedFrames struct {
	byKind     map[sextantproto.FrameKind][]sextantproto.AgentFramePayload
	turnReason string
}

func (c *collectedFrames) kindList() []sextantproto.FrameKind {
	kinds := make([]sextantproto.FrameKind, 0, len(c.byKind))
	for k := range c.byKind {
		kinds = append(kinds, k)
	}
	return kinds
}

// collectFramesUntilTurnEnded drains both subscriptions in parallel
// until a lifecycle.turn_ended for agentID arrives, then drains the
// frame channel for a final grace window to catch frames that were
// in flight when turn_ended landed. Frame envelopes for other agents
// are dropped (concurrent-test noise).
//
// The post-turn_ended grace exists because the frames and lifecycle
// subjects live on separate JetStream streams (agent_frames and
// agent_lifecycle); each is consumed via its own pull consumer, and
// pull-consumer delivery order across streams is not synchronized.
// The publishing side does flush after every write, so the server
// has all messages when turn_ended arrives — but our consumer may
// have pulled lifecycle ahead of frames. 500ms is generous given
// frame-consumer pulls happen on millisecond timers.
func collectFramesUntilTurnEnded(
	t *testing.T,
	frameMsgs <-chan client.Message,
	lifeMsgs <-chan client.Message,
	agentID uuid.UUID,
	timeout time.Duration,
) collectedFrames {
	t.Helper()
	out := collectedFrames{byKind: map[sextantproto.FrameKind][]sextantproto.AgentFramePayload{}}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	collectFrame := func(msg client.Message) {
		if msg.Err != nil {
			return
		}
		env := msg.Envelope
		if env.Kind != sextantproto.KindAgentFrame {
			_ = msg.Ack()
			return
		}
		if env.From.Kind != sextantproto.AddressAgent || env.From.ID != agentID.String() {
			_ = msg.Ack()
			return
		}
		var p sextantproto.AgentFramePayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			_ = msg.Ack()
			return
		}
		out.byKind[p.FrameKind] = append(out.byKind[p.FrameKind], p)
		_ = msg.Ack()
	}

	for {
		select {
		case msg, ok := <-frameMsgs:
			if !ok {
				t.Fatal("frame subscription closed before turn_ended")
			}
			collectFrame(msg)
		case msg, ok := <-lifeMsgs:
			if !ok {
				t.Fatal("lifecycle subscription closed before turn_ended")
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
				out.turnReason = p.Reason
				_ = msg.Ack()
				// Final drain — see comment on the function header.
				grace := time.NewTimer(500 * time.Millisecond)
				for {
					select {
					case fm, fok := <-frameMsgs:
						if !fok {
							grace.Stop()
							return out
						}
						collectFrame(fm)
					case <-grace.C:
						return out
					}
				}
			}
			_ = msg.Ack()
		case <-deadline.C:
			t.Fatalf("timeout waiting for lifecycle.turn_ended for %s (got kinds %v)",
				agentID, out.kindList())
		}
	}
}

func assertFrameKindPresent(
	t *testing.T,
	frames collectedFrames,
	want sextantproto.FrameKind,
	label string,
) {
	t.Helper()
	if len(frames.byKind[want]) == 0 {
		t.Errorf("%s frame missing; got kinds %v", label, frames.kindList())
	}
}

func assertAssistantTextContains(
	t *testing.T,
	frames collectedFrames,
	wantSubstr string,
) {
	t.Helper()
	wantLower := strings.ToLower(wantSubstr)
	for _, p := range frames.byKind[sextantproto.FrameAssistantText] {
		text, _ := p.Body["text"].(string)
		if strings.Contains(strings.ToLower(text), wantLower) {
			return
		}
	}
	t.Errorf("no assistant_text frame contained %q; got %d assistant_text frames",
		wantSubstr, len(frames.byKind[sextantproto.FrameAssistantText]))
}

func assertToolCall(
	t *testing.T,
	frames collectedFrames,
	wantTool string,
) {
	t.Helper()
	for _, p := range frames.byKind[sextantproto.FrameToolCall] {
		if p.ToolName == wantTool {
			return
		}
	}
	t.Errorf("no tool_call frame for tool %q; got %d tool_call frames",
		wantTool, len(frames.byKind[sextantproto.FrameToolCall]))
}

func assertToolResult(t *testing.T, frames collectedFrames, wantErr bool) {
	t.Helper()
	results := frames.byKind[sextantproto.FrameToolResult]
	if len(results) == 0 {
		t.Errorf("tool_result frame missing; got kinds %v", frames.kindList())
		return
	}
	// Inspect the first one — the success path only emits one.
	gotErr, _ := results[0].Body["is_error"].(bool)
	if gotErr != wantErr {
		t.Errorf("tool_result.is_error = %v, want %v", gotErr, wantErr)
	}
}

// waitForLifecycleStarted lives in spawn_test.go and is the
// success-side helper this file relies on.
