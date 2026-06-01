package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// drift_e2e_test.go is the P2 drift acceptance e2e (real daemon + docker),
// feat-ctl-p2-drift. It exercises the two operator-visible convergence
// promises against a live sidecar:
//
//   - EDIT a live agent's spec (bump Spec.Generation + change the desired
//     image): the reconciler re-actuates onto the new image IMMEDIATELY
//     (a deliberate edit is not gated on a turn boundary) and
//     observed_generation catches up — the "edit the record → reality
//     follows" promise (RFC §5.6);
//   - a caught-up agent whose BUILD INPUTS drifted underneath it (the
//     stale-sidecar-after-daemon-upgrade case: the desired fingerprint
//     moved but generation is unchanged): the reconciler converges it by
//     restart ONLY at a turn boundary (lifecycle.turn_ended), never
//     mid-turn (RFC §5.6, §5.8).
//
// It is docker-backed and (for the drift-at-boundary case) turn-timing
// sensitive, so it is CI-only — it self-skips when docker / the sidecar
// image are absent, like the other e2e tests in this package. Do NOT run
// it on the watchdog'd local path; CI's sidecar job runs it.

// openDefsKV connects to the daemon's agent_definitions KV so the e2e can
// simulate a live spec edit (there is no spec-edit RPC verb yet — the
// generation seam is the mechanism, RFC §5.6). Reads stay off the RPC
// gauntlet (the TUIs read KV directly too).
func openDefsKV(t *testing.T, cli *client.Client) jetstream.KeyValue {
	t.Helper()
	js, err := jetstream.New(cli.Conn())
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kv, err := js.KeyValue(ctx, handlers.AgentDefinitionsBucket)
	if err != nil {
		t.Fatalf("open %s KV: %v", handlers.AgentDefinitionsBucket, err)
	}
	return kv
}

// editLiveSpec read-modify-writes the agent's desired spec in KV, applying
// mutate. It uses the KV revision for an optimistic CAS, retrying once on
// conflict (the reconciler is the status writer; a spec edit rarely races
// it, but the retry keeps the test non-flaky).
func editLiveSpec(t *testing.T, kv jetstream.KeyValue, agentID uuid.UUID, mutate func(*sextantproto.AgentDefinition)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		entry, err := kv.Get(ctx, agentID.String())
		if err != nil {
			t.Fatalf("kv get %s: %v", agentID, err)
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			t.Fatalf("decode def: %v", err)
		}
		mutate(&def)
		raw, err := json.Marshal(def)
		if err != nil {
			t.Fatalf("marshal def: %v", err)
		}
		if _, err := kv.Update(ctx, agentID.String(), raw, entry.Revision()); err != nil {
			if strings.Contains(err.Error(), "wrong last sequence") && attempt < 2 {
				continue // CAS conflict — re-read + retry
			}
			t.Fatalf("kv update %s: %v", agentID, err)
		}
		return
	}
}

// readObservedGeneration polls the agent_definitions KV for the
// reconciler-written observed_generation until it reaches want or the
// deadline lapses.
func waitObservedGeneration(t *testing.T, kv jetstream.KeyValue, agentID uuid.UUID, want int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		entry, err := kv.Get(ctx, agentID.String())
		cancel()
		if err == nil {
			var def sextantproto.AgentDefinition
			if json.Unmarshal(entry.Value(), &def) == nil {
				got = def.Status.ObservedGeneration
				if got >= want {
					return got
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return got
}

// dropMockTurnEnded sends a prompt to the mock-driver agent and waits for
// the resulting lifecycle.turn_ended — i.e. drives the agent to a TURN
// BOUNDARY. The mock driver emits turn_ended at the end of every prompt
// turn without an Anthropic API call.
func dropMockTurnEnded(t *testing.T, cli *client.Client, lifeMsgs <-chan client.Message, agentID uuid.UUID) {
	t.Helper()
	promptCtx, promptCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer promptCancel()
	var promptResp sextantproto.PromptAgentResponse
	if err := cli.RPC(promptCtx, rpc.VerbPromptAgent, sextantproto.PromptAgentRequest{
		AgentID: agentID,
		Content: "drive one turn to a boundary",
	}, &promptResp); err != nil {
		t.Fatalf("prompt_agent: %v", err)
	}
	if err := waitForLifecycleTransition(t, lifeMsgs, agentID, sextantproto.LifecycleTurnEnded, 30*time.Second); err != nil {
		t.Fatalf("turn_ended: %v", err)
	}
}

// waitForLifecycleTransition drains lifeMsgs until a lifecycle envelope
// with the wanted transition for agentID arrives.
func waitForLifecycleTransition(t *testing.T, lifeMsgs <-chan client.Message, agentID uuid.UUID, want sextantproto.LifecycleEvent, timeout time.Duration) error {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case msg, ok := <-lifeMsgs:
			if !ok {
				return context.Canceled
			}
			if msg.Err != nil {
				_ = msg.Ack()
				continue
			}
			env := msg.Envelope
			if env.Kind != sextantproto.KindLifecycle {
				_ = msg.Ack()
				continue
			}
			var p sextantproto.LifecyclePayload
			if json.Unmarshal(env.Payload, &p) != nil {
				_ = msg.Ack()
				continue
			}
			_ = msg.Ack()
			if p.AgentUUID == agentID && p.Transition == want {
				return nil
			}
		case <-deadline.C:
			return context.DeadlineExceeded
		}
	}
}

// containerWireEpoch reads the sextant.wire_epoch label off the agent's
// running container — the runtime half of the skew check (RFC §5.8).
func containerWireEpoch(t *testing.T, dockerBin string, containerID string) (int, bool) {
	t.Helper()
	out, err := exec.Command(dockerBin, "inspect", //nolint:gosec // test-controlled args
		"--format", "{{ index .Config.Labels \""+handlers.LabelWireEpoch+"\" }}", containerID).Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// TestDrift_E2E_SpecEditConvergesViaGeneration: edit a LIVE agent's desired
// spec (bump Spec.Generation + change the desired image) directly in KV;
// the reconciler re-actuates onto the new image and observed_generation
// catches up — the "edit the record → reality follows" promise (RFC §5.6).
// A generation bump is a deliberate edit, so it converges immediately (no
// turn-boundary wait).
func TestDrift_E2E_SpecEditConvergesViaGeneration(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	// A re-tagged alias of the same image: a DIFFERENT image ref (so the
	// spec fingerprint moves) that is still runnable.
	const newImage = "sextant-sidecar:p2drift-genedit"
	if err := exec.Command(dockerBin, "tag", "sextant-sidecar:latest", newImage).Run(); err != nil { //nolint:gosec // test-controlled args
		t.Fatalf("docker tag: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command(dockerBin, "rmi", newImage).Run() }) //nolint:gosec // test-controlled args

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	lifeMsgs, err := cli.Subscribe(subCtx, "agents.*.lifecycle", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe lifecycle: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}

	agentID := spawnMockAgent(t, h, cli, dockerBin, "drift-genedit-")
	if err := waitForLifecycleStarted(t, lifeMsgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v", err)
	}
	firstContainer := waitForContainer(t, dockerBin, agentID, 30*time.Second)
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	// Edit the live spec: new image + bump Generation (the spec-edit seam).
	kv := openDefsKV(t, cli)
	editLiveSpec(t, kv, agentID, func(def *sextantproto.AgentDefinition) {
		def.Spec.Sandbox.Image = newImage
		def.Spec.Generation++ // the load-bearing edit: observed_generation now lags
	})

	// The reconciler sees observed_generation < generation and re-actuates a
	// FRESH incarnation onto the new image (no turn-boundary wait — an edit
	// is deliberate intent).
	newContainer := waitForNewContainer(t, dockerBin, agentID, firstContainer, 90*time.Second)
	if newContainer == firstContainer {
		t.Fatalf("spec edit did not converge to a fresh incarnation\n--- daemon log ---\n%s", h.tail(t))
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	// The fresh container runs the EDITED image.
	gotImage, _ := exec.Command(dockerBin, "inspect", //nolint:gosec // test-controlled args
		"--format", "{{.Config.Image}}", newContainer).Output()
	if img := strings.TrimSpace(string(gotImage)); img != newImage {
		t.Errorf("converged container image = %q, want %q", img, newImage)
	}

	// observed_generation catches up — the reconciler has applied the edit.
	if got := waitObservedGeneration(t, kv, agentID, 2, 30*time.Second); got < 2 {
		t.Fatalf("observed_generation = %d, want >= 2 (reconciler did not catch up to the edit)\n--- daemon log ---\n%s", got, h.tail(t))
	}

	cleanUpAgent(t, cli, agentID)
}

// TestDrift_E2E_StaleImageConvergesAtTurnBoundary: the daemon-upgrade case
// (RFC §5.6, §5.8). Change ONLY the desired image (no generation bump), so
// generation/nonce stay caught up and the ONLY signal is the moved spec
// fingerprint. The reconciler must NOT interrupt the agent mid-turn; it
// converges by restart only after the next lifecycle.turn_ended.
func TestDrift_E2E_StaleImageConvergesAtTurnBoundary(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	const newImage = "sextant-sidecar:p2drift-boundary"
	if err := exec.Command(dockerBin, "tag", "sextant-sidecar:latest", newImage).Run(); err != nil { //nolint:gosec // test-controlled args
		t.Fatalf("docker tag: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command(dockerBin, "rmi", newImage).Run() }) //nolint:gosec // test-controlled args

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	lifeMsgs, err := cli.Subscribe(subCtx, "agents.*.lifecycle", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe lifecycle: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}

	agentID := spawnMockAgent(t, h, cli, dockerBin, "drift-boundary-")
	if err := waitForLifecycleStarted(t, lifeMsgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v", err)
	}
	firstContainer := waitForContainer(t, dockerBin, agentID, 30*time.Second)
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	// Drive one turn to a known boundary, then drift the image WITHOUT a
	// generation bump (the silent build-input change of a daemon upgrade).
	dropMockTurnEnded(t, cli, lifeMsgs, agentID)
	kv := openDefsKV(t, cli)
	beforeGen := -1
	editLiveSpec(t, kv, agentID, func(def *sextantproto.AgentDefinition) {
		beforeGen = def.Spec.Generation
		def.Spec.Sandbox.Image = newImage // fingerprint moves; generation unchanged
	})

	// Drive a fresh turn to a new boundary; on (or shortly after) that
	// turn_ended the reconciler converges the drifted spec by restart.
	dropMockTurnEnded(t, cli, lifeMsgs, agentID)

	newContainer := waitForNewContainer(t, dockerBin, agentID, firstContainer, 90*time.Second)
	if newContainer == firstContainer {
		t.Fatalf("stale-image agent did not converge at a turn boundary\n--- daemon log ---\n%s", h.tail(t))
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	// The converged container runs the new image and the CURRENT wire epoch.
	gotImage, _ := exec.Command(dockerBin, "inspect", //nolint:gosec // test-controlled args
		"--format", "{{.Config.Image}}", newContainer).Output()
	if img := strings.TrimSpace(string(gotImage)); img != newImage {
		t.Errorf("converged container image = %q, want %q", img, newImage)
	}
	if epoch, ok := containerWireEpoch(t, dockerBin, newContainer); !ok || epoch != sextantproto.WireEpoch {
		t.Errorf("converged container wire_epoch = %d (ok=%v), want %d", epoch, ok, sextantproto.WireEpoch)
	}

	// Drift convergence is a DELIBERATE restart, not a crash: it must not
	// flip the agent terminal (crashed) or be charged to the crash budget —
	// the agent is healthy/running again, not crash-looped.
	st := pollAgentStatus(t, cli, agentID)
	if st.Lifecycle == string(sextantproto.LifecycleCrashedState) {
		t.Errorf("drift convergence flipped the agent to crashed (lifecycle=%q)", st.Lifecycle)
	}
	_ = beforeGen // generation was deliberately NOT bumped; documented for the reader

	cleanUpAgent(t, cli, agentID)
}
