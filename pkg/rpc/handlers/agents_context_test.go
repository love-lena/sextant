package handlers_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestSpawnBindsAgentProjectsDir — when AgentsDataRoot is set, the
// spawn handler must (a) create <root>/<uuid>/claude-projects/ on
// disk and (b) include the bind mount at /home/agent/.claude/projects
// in the container spec.
//
// Phase A of slug:feat-agents-context-view. The mount is
// what makes the SDK session JSONL host-readable; without it the
// `agents context` verb has nothing to read.
func TestSpawnBindsAgentProjectsDir(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	root := t.TempDir()
	deps.AgentsDataRoot = root
	h := handlers.NewSpawnAgent(deps)

	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{Name: "alpha", Template: "default"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Host dir created with the right layout.
	projects := filepath.Join(root, resp.AgentID.String(), "claude-projects")
	st, err := os.Stat(projects)
	if err != nil {
		t.Fatalf("stat %s: %v", projects, err)
	}
	if !st.IsDir() {
		t.Errorf("%s is not a directory", projects)
	}
	if got := st.Mode().Perm(); got != 0o700 {
		t.Errorf("mode = %v, want 0o700", got)
	}

	// AgentProjectsDir returns the same path the spawn handler created.
	if got := handlers.AgentProjectsDir(root, resp.AgentID); got != projects {
		t.Errorf("AgentProjectsDir = %q, want %q", got, projects)
	}

	// Container spec carries the bind mount at the SDK's expected path.
	if len(runner.specs) != 1 {
		t.Fatalf("runner.specs = %d, want 1", len(runner.specs))
	}
	spec := runner.specs[0]
	var bind *containermgr.MountSpec
	for i := range spec.Mounts {
		if spec.Mounts[i].ContainerPath == "/home/agent/.claude/projects" {
			bind = &spec.Mounts[i]
			break
		}
	}
	if bind == nil {
		t.Fatalf("no mount at /home/agent/.claude/projects in:\n%+v", spec.Mounts)
	}
	if bind.HostPath != projects {
		t.Errorf("mount HostPath = %q, want %q", bind.HostPath, projects)
	}
	if bind.ReadOnly {
		t.Errorf("mount is read-only; SDK writes session JSONL, must be rw")
	}
}

// TestSpawnSkipsBindWhenAgentsDataRootEmpty — legacy fall-back
// behavior. Daemons that haven't been upgraded (or unit-test wirings
// that don't care about session-log visibility) skip the mount
// entirely.
func TestSpawnSkipsBindWhenAgentsDataRootEmpty(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	// deps.AgentsDataRoot intentionally left empty
	h := handlers.NewSpawnAgent(deps)

	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{Name: "beta", Template: "default"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("runner.specs = %d, want 1", len(runner.specs))
	}
	for _, m := range runner.specs[0].Mounts {
		if m.ContainerPath == "/home/agent/.claude/projects" {
			t.Errorf("unexpected projects bind mount in legacy mode: %+v", m)
		}
	}
}

// TestGetAgentStatusSurfacesSessionLog — when AgentsDataRoot is wired
// and the agent definition has a session_id, the handler must echo
// the per-agent projects host path + session id back on AgentStatus
// so the CLI verb can resolve the JSONL.
func TestGetAgentStatusSurfacesSessionLog(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	id := uuid.New()
	sid := "session-xyz"
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      "alpha",
		Lifecycle: sextantproto.LifecycleRunning,
		Version:   2,
		Runtime:   sextantproto.RuntimeConfig{SessionID: &sid},
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
	wantDir := filepath.Join(root, id.String(), "claude-projects")
	if resp.Status.SessionLog.ProjectsDir != wantDir {
		t.Errorf("SessionLog.ProjectsDir = %q, want %q", resp.Status.SessionLog.ProjectsDir, wantDir)
	}
}

// TestGetAgentStatusSessionLogEmptyWhenRootUnset — older daemons (or
// tests) leave AgentsDataRoot empty; the handler must omit
// SessionLog entirely so the CLI can detect the case and tell the
// operator to upgrade.
func TestGetAgentStatusSessionLogEmptyWhenRootUnset(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	id := uuid.New()
	def := sextantproto.AgentDefinition{UUID: id, Name: "alpha", Lifecycle: sextantproto.LifecycleRunning}
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
	def := sextantproto.AgentDefinition{UUID: id, Name: "alpha", Lifecycle: sextantproto.LifecycleRunning}
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
	if !strings.HasSuffix(resp.Status.SessionLog.ProjectsDir, "claude-projects") {
		t.Errorf("ProjectsDir = %q, want suffix claude-projects", resp.Status.SessionLog.ProjectsDir)
	}
}
