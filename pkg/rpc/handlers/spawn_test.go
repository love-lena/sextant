package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/containermgr"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// fakeMutableKV adds Put/Delete to the read-only fakeKV (in agents_test.go).
// Keys live in one in-memory map; concurrent access is guarded so the
// kill handler's list+update can race-test cleanly. The map is keyed by
// the same UUID string the production code uses.
type fakeMutableKV struct {
	mu      sync.Mutex
	entries map[string][]byte
	// putHook, if non-nil, runs before every Put and can return an
	// error to abort the write. Used by the lifecycle-flip rollback
	// test to fail the second Put on the definitions bucket.
	putHook func(key string, callIdx int) error
	putN    int
}

func newFakeMutableKV() *fakeMutableKV {
	return &fakeMutableKV{entries: map[string][]byte{}}
}

func (f *fakeMutableKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return mutableEntry{key: key, value: v}, nil
}

func (f *fakeMutableKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	f.mu.Lock()
	keys := make([]string, 0, len(f.entries))
	for k := range f.entries {
		keys = append(keys, k)
	}
	f.mu.Unlock()
	out := make(chan string, len(keys))
	for _, k := range keys {
		out <- k
	}
	close(out)
	return mutableLister{ch: out}, nil
}

func (f *fakeMutableKV) Put(_ context.Context, key string, value []byte) (uint64, error) {
	f.mu.Lock()
	f.putN++
	hook := f.putHook
	call := f.putN
	f.mu.Unlock()
	if hook != nil {
		if err := hook(key, call); err != nil {
			return 0, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[key] = append([]byte(nil), value...)
	return uint64(len(f.entries)), nil
}

func (f *fakeMutableKV) Delete(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, key)
	return nil
}

func (f *fakeMutableKV) snapshot() map[string][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]byte, len(f.entries))
	for k, v := range f.entries {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

type mutableLister struct{ ch chan string }

func (l mutableLister) Keys() <-chan string { return l.ch }
func (l mutableLister) Stop() error         { return nil }

type mutableEntry struct {
	key   string
	value []byte
}

func (e mutableEntry) Bucket() string                  { return "" }
func (e mutableEntry) Key() string                     { return e.key }
func (e mutableEntry) Value() []byte                   { return e.value }
func (e mutableEntry) Revision() uint64                { return 1 }
func (e mutableEntry) Created() time.Time              { return time.Time{} }
func (e mutableEntry) Delta() uint64                   { return 0 }
func (e mutableEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

// fakeTemplatesKV satisfies templates.KV with a hard-coded entry set.
type fakeTemplatesKV struct {
	entries map[string][]byte
}

func (f *fakeTemplatesKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	v, ok := f.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return mutableEntry{key: key, value: v}, nil
}

func (f *fakeTemplatesKV) Put(_ context.Context, key string, value []byte) (uint64, error) {
	if f.entries == nil {
		f.entries = map[string][]byte{}
	}
	f.entries[key] = append([]byte(nil), value...)
	return uint64(len(f.entries)), nil
}

func (f *fakeTemplatesKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	keys := make([]string, 0, len(f.entries))
	for k := range f.entries {
		keys = append(keys, k)
	}
	out := make(chan string, len(keys))
	for _, k := range keys {
		out <- k
	}
	close(out)
	return mutableLister{ch: out}, nil
}

// stubRunner records the last spec passed in and returns a fixed
// Container id. Stop tracks the IDs it received so tests can assert
// kill_agent reached it.
type stubRunner struct {
	mu      sync.Mutex
	lastID  string
	specs   []containermgr.ContainerSpec
	stopped []string
	runErr  error
}

func (s *stubRunner) Run(_ context.Context, spec containermgr.ContainerSpec) (*containermgr.Container, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runErr != nil {
		return nil, s.runErr
	}
	s.specs = append(s.specs, spec)
	s.lastID = "ctr-" + spec.Name
	return &containermgr.Container{ID: s.lastID, Name: spec.Name}, nil
}

func (s *stubRunner) Stop(_ context.Context, id string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, id)
	return nil
}

// fakeHistory captures Exec args so the test can assert one
// agent_definitions_history row was written.
type fakeHistory struct {
	mu    sync.Mutex
	calls int
}

func (h *fakeHistory) Exec(_ context.Context, _ string, _ ...any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	return nil
}

// buildDeps wires a complete SpawnDeps for the happy-path test. The
// templates KV has one "default" template; everything else is fresh.
func buildDeps(t *testing.T) (handlers.SpawnDeps, *fakeMutableKV, *fakeMutableKV, *stubRunner, *fakeHistory) {
	t.Helper()

	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	tplKV := &fakeTemplatesKV{}

	// Seed the template KV with a default template.
	tplJSON, err := json.Marshal(map[string]any{
		"name":        "default",
		"image":       "sextant-sidecar:latest",
		"permissions": []string{"read.agents", "control.prompt"},
		"mounts":      []string{"worktree"},
		"model":       "claude-opus-4-7[1m]",
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	if _, err := tplKV.Put(context.Background(), "default", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	// Real CA — issuing the JWT must produce a verifiable token; using
	// the real authjwt.CA keeps test wiring honest.
	privPEM, pubPEM, err := authjwt.GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	caDir := t.TempDir()
	keyPath := caDir + "/ca.key"
	pubPath := caDir + "/ca.pub"
	if err := writeAll(keyPath, privPEM); err != nil {
		t.Fatalf("write ca.key: %v", err)
	}
	if err := writeAll(pubPath, pubPEM); err != nil {
		t.Fatalf("write ca.pub: %v", err)
	}
	ca, err := authjwt.LoadCA(keyPath, pubPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	runner := &stubRunner{}
	hist := &fakeHistory{}

	deps := handlers.SpawnDeps{
		Definitions:   defs,
		Incarnations:  incs,
		Templates:     tplKV,
		Containers:    runner,
		CA:            ca,
		History:       hist,
		WorkspaceRoot: t.TempDir(),
		HostID:        "test-host",
		NATSURL:       "nats://host.docker.internal:4222",
		NATSUser:      "operator",
		NATSPassword:  "supersecret",
		MCPURL:        "http://host.docker.internal:5172/mcp",
		Issuer:        "sextantd@test",
	}
	return deps, defs, incs, runner, hist
}

func writeAll(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func TestSpawnAgentHappyPath(t *testing.T) {
	deps, defs, incs, runner, hist := buildDeps(t)
	h := handlers.NewSpawnAgent(deps)

	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{
		Name:     "alpha",
		Template: "default",
	})
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
	if resp.AgentID == uuid.Nil {
		t.Fatal("AgentID is zero")
	}

	// Definitions bucket has one entry with lifecycle=running and version=2.
	defSnap := defs.snapshot()
	if len(defSnap) != 1 {
		t.Fatalf("definitions count = %d, want 1", len(defSnap))
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(defSnap[resp.AgentID.String()], &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	if def.Lifecycle != sextantproto.LifecycleRunning {
		t.Errorf("Lifecycle = %s, want running", def.Lifecycle)
	}
	if def.Version != 2 {
		t.Errorf("Version = %d, want 2", def.Version)
	}
	if def.Name != "alpha" {
		t.Errorf("Name = %q", def.Name)
	}
	if len(def.Tools) == 0 || def.Tools[0] != "read.agents" {
		t.Errorf("Tools = %v", def.Tools)
	}

	// Incarnations bucket has exactly one entry, container ID set.
	incSnap := incs.snapshot()
	if len(incSnap) != 1 {
		t.Fatalf("incarnations count = %d, want 1", len(incSnap))
	}
	var inc sextantproto.AgentIncarnation
	for _, v := range incSnap {
		if err := json.Unmarshal(v, &inc); err != nil {
			t.Fatalf("decode inc: %v", err)
		}
	}
	if inc.ContainerID == "" {
		t.Error("ContainerID empty")
	}
	if inc.AgentUUID != resp.AgentID {
		t.Errorf("inc.AgentUUID = %s, want %s", inc.AgentUUID, resp.AgentID)
	}

	// Container was started with the right env and labels.
	if len(runner.specs) != 1 {
		t.Fatalf("runner.specs = %d, want 1", len(runner.specs))
	}
	spec := runner.specs[0]
	if spec.Image != "sextant-sidecar:latest" {
		t.Errorf("Image = %q", spec.Image)
	}
	mustEnv := []string{
		"SEXTANT_AGENT_UUID", "SEXTANT_AGENT_NAME", "SEXTANT_INCARNATION_ID",
		"SEXTANT_HOST_ID", "SEXTANT_NATS_URL", "SEXTANT_NATS_USER",
		"SEXTANT_NATS_PASSWORD", "SEXTANT_JWT", "SEXTANT_MCP_URL",
	}
	for _, k := range mustEnv {
		if spec.Env[k] == "" {
			t.Errorf("env %s is empty: %v", k, spec.Env)
		}
	}
	if spec.Labels[handlers.LabelAgentUUID] != resp.AgentID.String() {
		t.Errorf("LabelAgentUUID = %q", spec.Labels[handlers.LabelAgentUUID])
	}

	// The issued JWT in env is a real, verifiable token signed by the CA.
	claims, err := deps.CA.Verify(spec.Env["SEXTANT_JWT"])
	if err != nil {
		t.Fatalf("verify jwt: %v", err)
	}
	if claims.AgentUUID != resp.AgentID {
		t.Errorf("jwt agent uuid = %s, want %s", claims.AgentUUID, resp.AgentID)
	}
	if len(claims.Capabilities) == 0 {
		t.Error("jwt has no capabilities")
	}

	// History was written twice (initial + running).
	if hist.calls != 2 {
		t.Errorf("hist.calls = %d, want 2", hist.calls)
	}
}

func TestSpawnAgentRejectsDuplicateName(t *testing.T) {
	deps, defs, _, _, _ := buildDeps(t)

	// Pre-seed the definitions bucket with an existing "alpha".
	existing := sextantproto.AgentDefinition{
		UUID:      uuid.New(),
		Name:      "alpha",
		Lifecycle: sextantproto.LifecycleRunning,
	}
	raw, _ := json.Marshal(existing)
	if _, err := defs.Put(context.Background(), existing.UUID.String(), raw); err != nil {
		t.Fatalf("Put existing: %v", err)
	}

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{Name: "alpha", Template: "default"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected duplicate-name error")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("Code = %q", cap.resp.Error.Code)
	}
}

func TestSpawnAgentRejectsMissingTemplate(t *testing.T) {
	deps, _, _, _, _ := buildDeps(t)
	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{Name: "alpha", Template: "no-such"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
}

func TestSpawnAgentRollsBackOnContainerStartFailure(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	runner.runErr = errors.New("dockerd is asleep")

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{Name: "alpha", Template: "default"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error")
	}
	// Roll back: no definition, no incarnation.
	if got := len(defs.snapshot()); got != 0 {
		t.Errorf("definitions count after rollback = %d, want 0", got)
	}
	if got := len(incs.snapshot()); got != 0 {
		t.Errorf("incarnations count after rollback = %d, want 0", got)
	}
}

// TestSpawnAgentRollsBackWorkspaceOnContainerFailure pins the
// workspace-dir leak fix: a container-start failure must remove the
// per-agent workspace dir from the rollback ledger, not leave it on
// disk for the next failed spawn to accumulate. Reads the workspace
// root from deps.WorkspaceRoot and asserts no children remain.
func TestSpawnAgentRollsBackWorkspaceOnContainerFailure(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	runner.runErr = errors.New("dockerd is asleep")

	// Sanity: root exists and is empty before the spawn attempt.
	entries, err := os.ReadDir(deps.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace root not empty pre-spawn: %d entries", len(entries))
	}

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{Name: "alpha", Template: "default"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error")
	}

	entries, err = os.ReadDir(deps.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir root post-spawn: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("workspace root has %d leftover entry/entries after rollback: %v",
			len(entries), names)
	}
}

// TestSpawnAgentRollsBackEverythingOnLifecycleFlipFailure pins the
// lifecycle-flip rollback fix: when the final definitions Put (the
// one that flips lifecycle defined→running) fails, the rollback ledger
// must:
//
//   - stop the spawned container,
//   - delete the incarnation KV entry,
//   - delete the definition KV entry,
//   - remove the workspace dir.
//
// We inject the failure by counting Put calls on the definitions KV:
// the first Put is the initial definition (success), the second is
// the lifecycle flip (fail). The incarnation Put is on a different
// bucket so it doesn't interfere with the counter.
func TestSpawnAgentRollsBackEverythingOnLifecycleFlipFailure(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	// Fail the SECOND Put on the definitions bucket — that's the
	// lifecycle flip; the first is the initial definition write.
	defs.putHook = func(_ string, callIdx int) error {
		if callIdx == 2 {
			return errors.New("nats KV unhealthy")
		}
		return nil
	}

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.SpawnAgentRequest{Name: "alpha", Template: "default"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeInternal {
		t.Errorf("Code = %q, want internal", cap.resp.Error.Code)
	}

	// Container was stopped via the rollback (Stop received the ID).
	runner.mu.Lock()
	stopped := append([]string(nil), runner.stopped...)
	runner.mu.Unlock()
	if len(stopped) != 1 {
		t.Errorf("runner.stopped = %v, want 1 entry (the rollback Stop)", stopped)
	}

	// No definition, no incarnation left in KV.
	if got := len(defs.snapshot()); got != 0 {
		t.Errorf("definitions count = %d, want 0", got)
	}
	if got := len(incs.snapshot()); got != 0 {
		t.Errorf("incarnations count = %d, want 0", got)
	}

	// Workspace dir gone too.
	entries, err := os.ReadDir(deps.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("workspace root has %d leftover entry/entries: %v", len(entries), names)
	}
}

func TestKillAgentStopsContainerAndUpdatesKV(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)

	// Run spawn first so we have a live agent.
	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn resp: %v", err)
	}

	killH := handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	killCap := &captureEmit{}
	if err := killH(context.Background(), makeReq(t, sextantproto.KillAgentRequest{
		AgentID: spawnResp.AgentID,
	}), killCap.emit()); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if killCap.resp.Error != nil {
		t.Fatalf("kill error: %+v", killCap.resp.Error)
	}

	// Container was stopped.
	runner.mu.Lock()
	stopped := append([]string(nil), runner.stopped...)
	runner.mu.Unlock()
	if len(stopped) != 1 {
		t.Fatalf("stopped count = %d, want 1", len(stopped))
	}

	// Incarnation state flipped to exited with EndedAt set.
	incSnap := incs.snapshot()
	var inc sextantproto.AgentIncarnation
	for _, v := range incSnap {
		_ = json.Unmarshal(v, &inc)
	}
	if inc.State != sextantproto.IncarnationExited {
		t.Errorf("State = %s, want exited", inc.State)
	}
	if inc.EndedAt == nil {
		t.Error("EndedAt is nil after kill")
	}

	// Definition lifecycle back to defined.
	defSnap := defs.snapshot()
	var def sextantproto.AgentDefinition
	_ = json.Unmarshal(defSnap[spawnResp.AgentID.String()], &def)
	if def.Lifecycle != sextantproto.LifecycleDefined {
		t.Errorf("def.Lifecycle = %s, want defined", def.Lifecycle)
	}
}

func TestKillAgentUnknownAgentReturnsNotFound(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	_ = deps
	h := handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.KillAgentRequest{AgentID: uuid.New()})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeAgentNotFound {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
}
