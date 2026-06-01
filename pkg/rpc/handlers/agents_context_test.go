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

// TestActuateNeverBindsClaudeProjects — the persistent claude-projects
// bind-mount was REMOVED in S0 (RFC §5.10). Even with AgentsDataRoot set,
// the Actuator must NOT add a /home/agent/.claude/projects bind, and must
// NOT create a per-agent claude-projects host dir: the SDK session JSONL
// stays in-container ground-truth, read on demand (read_file) and
// snapshotted on stop. This is the root fix for the #49/#50 mount-drift
// class — restart has no mount to forget.
func TestActuateNeverBindsClaudeProjects(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	root := t.TempDir()
	deps.AgentsDataRoot = root

	agentID := spawnAndActuate(t, deps, "alpha", "default")

	// No host claude-projects dir is created.
	projects := filepath.Join(root, agentID.String(), "claude-projects")
	if _, err := os.Stat(projects); !os.IsNotExist(err) {
		t.Errorf("claude-projects host dir %s exists (err=%v); it must not be created", projects, err)
	}

	// No claude-projects bind in the container spec.
	if len(runner.specs) != 1 {
		t.Fatalf("runner.specs = %d, want 1", len(runner.specs))
	}
	for _, m := range runner.specs[0].Mounts {
		if m.ContainerPath == "/home/agent/.claude/projects" {
			t.Errorf("unexpected claude-projects bind mount (removed in S0): %+v", m)
		}
	}
}

// TestGetAgentStatusSurfacesSessionLog — when AgentsDataRoot is wired
// and the agent definition has a session_id, the handler must echo the
// in-container projects base, the deterministic in-container JSONL path,
// the host snapshot path, and the session id back on AgentStatus so the
// CLI verb can read the authoritative .jsonl on demand.
func TestGetAgentStatusSurfacesSessionLog(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	id := uuid.New()
	sid := "session-xyz"
	def := sextantproto.AgentDefinition{
		UUID:    id,
		Name:    "alpha",
		Version: 2,
		Spec: sextantproto.AgentSpec{
			Desired: sextantproto.DesiredRun,
			Runtime: sextantproto.RuntimeConfig{SessionID: &sid},
		},
		Status: sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedRunning, ObservedGeneration: 1},
	}
	raw, _ := json.Marshal(def)
	kv.entries[id.String()] = raw

	root := t.TempDir()
	h := handlers.NewGetAgentStatusWithDeps(handlers.GetAgentStatusDeps{
		KV:             kv,
		AgentsDataRoot: root,
	})

	cap := &captureEmit{}
	req := makeReq(t, sextantproto.GetAgentStatusRequest{AgentID: id})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.GetAgentStatusResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status.SessionLog == nil {
		t.Fatal("Status.SessionLog is nil, want non-nil")
	}
	if resp.Status.SessionLog.SessionID != sid {
		t.Errorf("SessionLog.SessionID = %q, want %q", resp.Status.SessionLog.SessionID, sid)
	}
	// ProjectsDir is now the IN-CONTAINER base, not a host path.
	if got, want := resp.Status.SessionLog.ProjectsDir, handlers.ContainerProjectsDir; got != want {
		t.Errorf("SessionLog.ProjectsDir = %q, want in-container base %q", got, want)
	}
	if got, want := resp.Status.SessionLog.ContainerJSONLPath, handlers.ContainerSessionJSONLPath(sid); got != want {
		t.Errorf("SessionLog.ContainerJSONLPath = %q, want %q", got, want)
	}
	wantSnap := filepath.Join(root, id.String(), "session-snapshot.jsonl")
	if resp.Status.SessionLog.SnapshotPath != wantSnap {
		t.Errorf("SessionLog.SnapshotPath = %q, want %q", resp.Status.SessionLog.SnapshotPath, wantSnap)
	}
}

// TestGetAgentStatusSessionLogEmptyWhenRootUnset — older daemons (or
// tests) leave AgentsDataRoot empty; the handler must omit
// SessionLog entirely so the CLI can detect the case and tell the
// operator to upgrade.
func TestGetAgentStatusSessionLogEmptyWhenRootUnset(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	id := uuid.New()
	def := sextantproto.AgentDefinition{UUID: id, Name: "alpha", Spec: sextantproto.AgentSpec{Desired: sextantproto.DesiredRun}, Status: sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedRunning, ObservedGeneration: 1}}
	raw, _ := json.Marshal(def)
	kv.entries[id.String()] = raw

	h := handlers.NewGetAgentStatusWithDeps(handlers.GetAgentStatusDeps{KV: kv})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.GetAgentStatusRequest{AgentID: id}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp sextantproto.GetAgentStatusResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status.SessionLog != nil {
		t.Errorf("SessionLog = %+v, want nil (root unset)", resp.Status.SessionLog)
	}
}

// TestGetAgentStatusSessionLogIDEmptyBeforeFirstTurn — until the
// sidecar has captured the first SDK-issued session_id, SessionID is
// empty even though ProjectsDir is set. The CLI uses the empty
// session_id as a "warm up window" signal.
func TestGetAgentStatusSessionLogIDEmptyBeforeFirstTurn(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	id := uuid.New()
	def := sextantproto.AgentDefinition{UUID: id, Name: "alpha", Spec: sextantproto.AgentSpec{Desired: sextantproto.DesiredRun}, Status: sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedRunning, ObservedGeneration: 1}}
	raw, _ := json.Marshal(def)
	kv.entries[id.String()] = raw

	root := t.TempDir()
	h := handlers.NewGetAgentStatusWithDeps(handlers.GetAgentStatusDeps{
		KV:             kv,
		AgentsDataRoot: root,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.GetAgentStatusRequest{AgentID: id}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp sextantproto.GetAgentStatusResponse
	_ = json.Unmarshal(cap.resp.Result, &resp)
	if resp.Status.SessionLog == nil {
		t.Fatal("SessionLog is nil with root set, want non-nil")
	}
	if resp.Status.SessionLog.SessionID != "" {
		t.Errorf("SessionLog.SessionID = %q, want empty", resp.Status.SessionLog.SessionID)
	}
	// Before the first turn there's no session id, so no in-container JSONL
	// path either — but the in-container projects base is always set.
	if resp.Status.SessionLog.ContainerJSONLPath != "" {
		t.Errorf("ContainerJSONLPath = %q, want empty before first turn", resp.Status.SessionLog.ContainerJSONLPath)
	}
	if got, want := resp.Status.SessionLog.ProjectsDir, handlers.ContainerProjectsDir; got != want {
		t.Errorf("ProjectsDir = %q, want in-container base %q", got, want)
	}
}
