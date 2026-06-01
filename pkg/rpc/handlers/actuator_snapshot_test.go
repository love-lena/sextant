package handlers_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// fakeCopier records CopyFileFromContainer calls and returns canned bytes.
type fakeCopier struct {
	calls   []copierCall
	content []byte
	err     error
}

type copierCall struct {
	containerID string
	srcPath     string
}

func (c *fakeCopier) CopyFileFromContainer(_ context.Context, id, srcPath string) ([]byte, error) {
	c.calls = append(c.calls, copierCall{containerID: id, srcPath: srcPath})
	if c.err != nil {
		return nil, c.err
	}
	return c.content, nil
}

// TestSnapshotOnStopWritesDurableCopy is the S0 snapshot-on-stop
// regression (RFC §5.10): when the reconciler/actuator stops an agent that
// has a recorded session, it copies the authoritative in-container .jsonl
// out (works on an exited container via CopyFileFromContainer) and writes
// a durable host snapshot into the agent data dir. The CLI's
// `agents context --backup` reads exactly this file after the (AutoRemove)
// container is gone.
func TestSnapshotOnStopWritesDurableCopy(t *testing.T) {
	deps, _, incs, _, _ := buildDeps(t)
	root := t.TempDir()
	deps.AgentsDataRoot = root

	sid := "sess-abc"
	transcript := []byte(`{"type":"assistant","sessionId":"sess-abc"}` + "\n")
	copier := &fakeCopier{content: transcript}

	adeps := actuatorDepsFrom(deps)
	adeps.SnapshotCopier = copier

	// Spawn + actuate an agent, then record a session id on its def (the
	// sidecar persists this after the first turn).
	agentID := spawnAndActuate(t, deps, "alpha", "default")
	setSessionID(t, deps, agentID, sid)

	act := handlers.NewActuator(adeps)
	def := getDef(t, deps, agentID)
	if err := act.Stop(context.Background(), def); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// The copier was asked for the deterministic in-container JSONL path.
	if len(copier.calls) != 1 {
		t.Fatalf("CopyFileFromContainer calls = %d, want 1", len(copier.calls))
	}
	wantSrc := handlers.ContainerSessionJSONLPath(sid)
	if copier.calls[0].srcPath != wantSrc {
		t.Errorf("copy src = %q, want %q", copier.calls[0].srcPath, wantSrc)
	}

	// The durable snapshot was written byte-for-byte to the agent data dir.
	snap := filepath.Join(root, agentID.String(), "session-snapshot.jsonl")
	got, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("read snapshot %s: %v", snap, err)
	}
	if string(got) != string(transcript) {
		t.Errorf("snapshot content = %q, want %q", got, transcript)
	}
	_ = incs
}

// TestSnapshotOnStopSkippedWithoutSession — an agent that never recorded a
// session id has nothing to snapshot; the stop path must not call the
// copier and must not write a snapshot file.
func TestSnapshotOnStopSkippedWithoutSession(t *testing.T) {
	deps, _, _, _, _ := buildDeps(t)
	root := t.TempDir()
	deps.AgentsDataRoot = root

	copier := &fakeCopier{content: []byte("unused")}
	adeps := actuatorDepsFrom(deps)
	adeps.SnapshotCopier = copier

	agentID := spawnAndActuate(t, deps, "beta", "default")
	// No session id recorded.

	act := handlers.NewActuator(adeps)
	def := getDef(t, deps, agentID)
	if err := act.Stop(context.Background(), def); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if len(copier.calls) != 0 {
		t.Errorf("CopyFileFromContainer called %d times for a session-less agent, want 0", len(copier.calls))
	}
	snap := filepath.Join(root, agentID.String(), "session-snapshot.jsonl")
	if _, err := os.Stat(snap); !os.IsNotExist(err) {
		t.Errorf("snapshot %s exists (err=%v) for a session-less agent; want none", snap, err)
	}
}

// setSessionID records an SDK session id on the agent's def in KV, mirroring
// the sidecar's post-first-turn persist.
func setSessionID(t *testing.T, deps handlers.SpawnDeps, agentID uuid.UUID, sid string) {
	t.Helper()
	def := getDef(t, deps, agentID)
	def.Spec.Runtime.SessionID = &sid
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	if _, err := deps.Definitions.Put(context.Background(), agentID.String(), raw); err != nil {
		t.Fatalf("put def: %v", err)
	}
}

func getDef(t *testing.T, deps handlers.SpawnDeps, agentID uuid.UUID) sextantproto.AgentDefinition {
	t.Helper()
	entry, err := deps.Definitions.Get(context.Background(), agentID.String())
	if err != nil {
		t.Fatalf("get def: %v", err)
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(entry.Value(), &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	return def
}
