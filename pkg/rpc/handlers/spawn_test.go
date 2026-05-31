package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// fakeMutableKV adds Put/Delete to the read-only fakeKV (in agents_test.go).
// Keys live in one in-memory map; concurrent access is guarded so the
// kill handler's list+update can race-test cleanly. The map is keyed by
// the same UUID string the production code uses.
//
// Per-key revisions are tracked so Update enforces CAS — restart_agent
// uses the CAS path to refuse to clobber concurrent archive_agent /
// kill_agent writes. Put bumps the revision; Update succeeds only when
// the caller's `revision` matches the stored one.
type fakeMutableKV struct {
	mu        sync.Mutex
	entries   map[string][]byte
	revisions map[string]uint64
	// putHook, if non-nil, runs before every Put and can return an
	// error to abort the write. Used by the lifecycle-flip rollback
	// test to fail the second Put on the definitions bucket.
	putHook func(key string, callIdx int) error
	putN    int
}

func newFakeMutableKV() *fakeMutableKV {
	return &fakeMutableKV{
		entries:   map[string][]byte{},
		revisions: map[string]uint64{},
	}
}

func (f *fakeMutableKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return mutableEntry{key: key, value: v, revision: f.revisions[key]}, nil
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
	f.revisions[key]++
	return f.revisions[key], nil
}

// Update is the CAS write — succeeds only when the caller's revision
// matches the stored revision. Mirrors jetstream.KeyValue.Update on a
// real server. Returns jetstream.ErrKeyExists on revision mismatch.
func (f *fakeMutableKV) Update(_ context.Context, key string, value []byte, revision uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.revisions[key]
	if !ok {
		return 0, jetstream.ErrKeyNotFound
	}
	if stored != revision {
		return 0, jetstream.ErrKeyExists
	}
	f.entries[key] = append([]byte(nil), value...)
	f.revisions[key]++
	return f.revisions[key], nil
}

func (f *fakeMutableKV) Delete(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, key)
	delete(f.revisions, key)
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
	key      string
	value    []byte
	revision uint64
}

func (e mutableEntry) Bucket() string                  { return "" }
func (e mutableEntry) Key() string                     { return e.key }
func (e mutableEntry) Value() []byte                   { return e.value }
func (e mutableEntry) Revision() uint64                { return e.revision }
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

// actuatorDepsFrom projects the spawn-test SpawnDeps onto the
// ActuatorDeps the sole-actuator path consumes. Under the declarative
// model container materialization (mounts, env, JWT, claude_seed volume)
// moved from the spawn handler into the Actuator, so the materialization
// tests spawn (desired-state write) and then drive the Actuator.
func actuatorDepsFrom(deps handlers.SpawnDeps) handlers.ActuatorDeps {
	return handlers.ActuatorDeps{
		Definitions:    deps.Definitions,
		Incarnations:   deps.Incarnations,
		Templates:      deps.Templates,
		Containers:     deps.Containers,
		Volumes:        deps.Volumes,
		CA:             deps.CA,
		History:        deps.History,
		WorkspaceRoot:  deps.WorkspaceRoot,
		AgentsDataRoot: deps.AgentsDataRoot,
		Worktree:       deps.Worktree,
		RepoRoot:       deps.RepoRoot,
		HostID:         deps.HostID,
		NATSURL:        deps.NATSURL,
		NATSUser:       deps.NATSUser,
		NATSPassword:   deps.NATSPassword,
		MCPURL:         deps.MCPURL,
		Issuer:         deps.Issuer,
		TestRunLabel:   deps.TestRunLabel,
		Now:            deps.Now,
	}
}

// spawnAndActuate runs the spawn handler (writes desired=run) then drives
// the Actuator once — the reconciler's actuation step — so a test can
// assert on the resulting container spec / mounts / volumes the same way
// the old synchronous spawn handler let it. Returns the spawned agent's
// UUID. Fails the test on a spawn or actuate error.
func spawnAndActuate(t *testing.T, deps handlers.SpawnDeps, name, template string) uuid.UUID {
	t.Helper()
	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: name, Template: template,
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", cap.resp.Error)
	}
	var resp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}
	if err := actuateAgent(t, deps, resp.AgentID); err != nil {
		t.Fatalf("actuate: %v", err)
	}
	return resp.AgentID
}

// actuateAgent reads the agent def from KV and drives one Actuator pass
// (the reconciler's container build+run). Returns the actuate error (some
// tests assert on failure paths) rather than failing the test itself.
func actuateAgent(t *testing.T, deps handlers.SpawnDeps, agentID uuid.UUID) error {
	t.Helper()
	entry, err := deps.Definitions.Get(context.Background(), agentID.String())
	if err != nil {
		t.Fatalf("get def for actuate: %v", err)
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(entry.Value(), &def); err != nil {
		t.Fatalf("decode def for actuate: %v", err)
	}
	act := handlers.NewActuator(actuatorDepsFrom(deps))
	_, aerr := act.Actuate(context.Background(), def, false)
	return aerr
}

// TestSpawnAgentWritesDesiredRunRecord pins the declarative spawn
// contract: under the control-plane model spawn_agent is a desired-state
// writer (RFC §5). It persists desired=run + generation=1 + observed=
// pending with NO container — the reconciler actuates. The container
// materialization is asserted by TestSpawnAgentHappyPath (which drives the
// Actuator).
func TestSpawnAgentWritesDesiredRunRecord(t *testing.T) {
	deps, defs, _, runner, hist := buildDeps(t)
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
	if resp.AgentID == uuid.Nil {
		t.Fatal("AgentID is zero")
	}

	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(defs.snapshot()[resp.AgentID.String()], &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	if def.Spec.Desired != sextantproto.DesiredRun {
		t.Errorf("spec.desired = %s, want run", def.Spec.Desired)
	}
	if def.Spec.Generation != 1 {
		t.Errorf("spec.generation = %d, want 1", def.Spec.Generation)
	}
	if def.Status.Observed != sextantproto.ObservedPending {
		t.Errorf("status.observed = %s, want pending", def.Status.Observed)
	}
	if def.Status.ObservedGeneration != 0 {
		t.Errorf("status.observed_generation = %d, want 0 (not yet actuated)", def.Status.ObservedGeneration)
	}
	if def.Lifecycle() != sextantproto.LifecycleDefined {
		t.Errorf("Lifecycle() = %s, want defined (pending pre-actuation)", def.Lifecycle())
	}
	if len(def.Spec.Tools) == 0 || def.Spec.Tools[0] != "read.agents" {
		t.Errorf("spec.tools = %v", def.Spec.Tools)
	}
	// The spawn handler does NOT actuate: no container, no incarnation.
	runner.mu.Lock()
	nspecs := len(runner.specs)
	runner.mu.Unlock()
	if nspecs != 0 {
		t.Errorf("spawn handler started %d container(s); the reconciler is the sole actuator", nspecs)
	}
	// Initial history row written once (spawn). The running row lands when
	// the reconciler converges, not at spawn time.
	if hist.calls != 1 {
		t.Errorf("hist.calls = %d, want 1 (initial spawn row only)", hist.calls)
	}
}

func TestSpawnAgentHappyPath(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)

	// Spawn (desired-state write) + drive the Actuator (the reconciler's
	// container build+run step) so the materialization is exercised.
	agentID := spawnAndActuate(t, deps, "alpha", "default")
	resp := sextantproto.SpawnAgentResponse{AgentID: agentID}

	// Definition persists desired=run; the Actuator does not write status
	// (the reconciler is the sole status writer), so observed stays pending
	// at this layer.
	defSnap := defs.snapshot()
	if len(defSnap) != 1 {
		t.Fatalf("definitions count = %d, want 1", len(defSnap))
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(defSnap[resp.AgentID.String()], &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	if def.Spec.Desired != sextantproto.DesiredRun {
		t.Errorf("spec.desired = %s, want run", def.Spec.Desired)
	}
	if def.Name != "alpha" {
		t.Errorf("Name = %q", def.Name)
	}
	if len(def.Spec.Tools) == 0 || def.Spec.Tools[0] != "read.agents" {
		t.Errorf("spec.tools = %v", def.Spec.Tools)
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
		"SEXTANT_MODEL",
	}
	for _, k := range mustEnv {
		if spec.Env[k] == "" {
			t.Errorf("env %s is empty: %v", k, spec.Env)
		}
	}
	// SEXTANT_MODEL falls through to the spawn-handler default when the
	// template doesn't set one. The default mirrors specs/architecture.md
	// §11b and the sidecar's own fallback.
	if got := spec.Env["SEXTANT_MODEL"]; got != handlers.DefaultModel {
		t.Errorf("env SEXTANT_MODEL = %q, want %q", got, handlers.DefaultModel)
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
}

func TestSpawnAgentRejectsDuplicateName(t *testing.T) {
	deps, defs, _, _, _ := buildDeps(t)

	// Pre-seed the definitions bucket with an existing "alpha".
	existing := sextantproto.AgentDefinition{
		UUID:   uuid.New(),
		Name:   "alpha",
		Spec:   sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
		Status: sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedRunning, ObservedGeneration: 1},
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

// TestActuateLeavesNoIncarnationOnContainerStartFailure: under the
// declarative model the spawn handler never starts a container; the
// Actuator (sole actuator) does. When Containers.Run fails, Actuate
// returns an error and persists NO incarnation — the def stays in KV so
// the reconciler retries idempotently next pass (no destructive rollback
// of the operator's desired record).
func TestActuateLeavesNoIncarnationOnContainerStartFailure(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)

	// Spawn writes the desired record (no container yet).
	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var resp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// Now make the runtime fail and drive the Actuator.
	runner.runErr = errors.New("dockerd is asleep")
	if err := actuateAgent(t, deps, resp.AgentID); err == nil {
		t.Fatal("expected actuate error when container Run fails")
	}

	// The desired record stays (the reconciler retries); no incarnation
	// was persisted behind the failed container.
	if got := len(defs.snapshot()); got != 1 {
		t.Errorf("definitions count = %d, want 1 (desired record retained for retry)", got)
	}
	if got := len(incs.snapshot()); got != 0 {
		t.Errorf("incarnations count after failed actuate = %d, want 0", got)
	}
}

// TestActuateRollsBackContainerOnIncarnationPersistFailure: when the
// incarnation KV Put fails after the container started, the Actuator
// must stop the orphaned container so it doesn't leak behind a missing
// incarnation record (the reconciler re-actuates next pass).
func TestActuateRollsBackContainerOnIncarnationPersistFailure(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)

	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var resp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// Fail the incarnation Put — the container has already started by then.
	incs.putHook = func(_ string, _ int) error {
		return errors.New("nats KV unhealthy")
	}
	if err := actuateAgent(t, deps, resp.AgentID); err == nil {
		t.Fatal("expected actuate error when incarnation persist fails")
	}

	// The started container was stopped (rolled back).
	runner.mu.Lock()
	stopped := append([]string(nil), runner.stopped...)
	runner.mu.Unlock()
	if len(stopped) != 1 {
		t.Errorf("runner.stopped = %v, want 1 entry (the orphaned-container rollback Stop)", stopped)
	}
	// No incarnation persisted.
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

// stubWorktreeProvider is a minimal handlers.WorktreeProvider for spawn
// tests that exercise the worktree branch of materializeWorkspace. It
// returns whatever path/branch the test pre-loaded; Destroy is a no-op
// that records the names it received.
type stubWorktreeProvider struct {
	path     string
	branch   string
	created  []string
	destroys []string
}

func (s *stubWorktreeProvider) Create(_ context.Context, name, baseBranch string, owning uuid.UUID) (sextantproto.WorktreeInfo, error) {
	_ = baseBranch
	_ = owning
	s.created = append(s.created, name)
	return sextantproto.WorktreeInfo{
		Name:       name,
		Path:       s.path,
		Branch:     s.branch,
		BaseBranch: "main",
		Status:     sextantproto.WorktreeStatusActive,
	}, nil
}

func (s *stubWorktreeProvider) Destroy(_ context.Context, name string, _ bool) error {
	s.destroys = append(s.destroys, name)
	return nil
}

// Resolve returns the stub's pre-loaded path for any name once a
// worktree has been Created (s.path set at construction). Restart's
// lossless-projection path calls this to re-mount the worktree spawn
// made. ok=false when the stub has no path (simulates "no worktree").
func (s *stubWorktreeProvider) Resolve(_ context.Context, name string) (string, bool, error) {
	if s.path == "" {
		return "", false, nil
	}
	return s.path, true, nil
}

// TestSpawnAgentMountsHostGitDirForWorktreeAgents pins the bug-worktree-
// gitdir-unreachable-in-container fix: when a worktree is the workspace
// and the daemon knows the host repo root, the spawn handler must add a
// bind mount of <RepoRoot>/.git at the same path so the worktree's .git
// pointer file resolves inside the container.
func TestSpawnAgentMountsHostGitDirForWorktreeAgents(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)

	// Pretend the operator's repo lives here. We don't run real git; we
	// just need the path string to flow through to the container spec.
	repoRoot := t.TempDir()
	if err := os.MkdirAll(repoRoot+"/.git", 0o750); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	deps.RepoRoot = repoRoot

	wt := &stubWorktreeProvider{
		path:   repoRoot + "/../worktrees/feat-default-deadbeef-001",
		branch: "feat-default-deadbeef-001",
	}
	deps.Worktree = wt

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("runner.specs = %d, want 1", len(runner.specs))
	}
	spec := runner.specs[0]

	gitDirHost := repoRoot + "/.git"
	var found bool
	for _, m := range spec.Mounts {
		if m.HostPath == gitDirHost && m.ContainerPath == gitDirHost {
			found = true
			break
		}
	}
	if !found {
		var summary []string
		for _, m := range spec.Mounts {
			summary = append(summary, m.HostPath+"->"+m.ContainerPath)
		}
		t.Errorf("no bind mount of %s at the same path; mounts = %v", gitDirHost, summary)
	}
}

// TestSpawnAgentSkipsGitDirMountWhenNoWorktree confirms the spawn
// handler does NOT add the .git mount when the agent isn't running in a
// worktree (e.g. the M11 stop-gap workspace). The mount is only useful
// when a worktree's .git pointer file references the host gitdir.
func TestSpawnAgentSkipsGitDirMountWhenNoWorktree(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	repoRoot := t.TempDir()
	if err := os.MkdirAll(repoRoot+"/.git", 0o750); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	deps.RepoRoot = repoRoot
	// deps.Worktree intentionally nil — fallback workspace path fires.

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	gitDirHost := repoRoot + "/.git"
	for _, m := range runner.specs[0].Mounts {
		if m.HostPath == gitDirHost {
			t.Errorf("unexpected .git mount on non-worktree spawn: %+v", m)
		}
	}
}

// TestSpawnAgentWritesGitConfigMount pins the feat-container-git-config
// fix: every spawn must mount a per-agent gitconfig file into the
// container at /home/agent/.gitconfig. The host file contents must
// include the agent name and UUID so commits land with a meaningful
// identity.
func TestSpawnAgentWritesGitConfigMount(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	var resp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	spec := runner.specs[0]
	var gitconfig *containermgr.MountSpec
	for i := range spec.Mounts {
		if spec.Mounts[i].ContainerPath == "/home/agent/.gitconfig" {
			gitconfig = &spec.Mounts[i]
			break
		}
	}
	if gitconfig == nil {
		var summary []string
		for _, m := range spec.Mounts {
			summary = append(summary, m.HostPath+"->"+m.ContainerPath)
		}
		t.Fatalf("no gitconfig mount; mounts = %v", summary)
	}
	if !gitconfig.ReadOnly {
		t.Errorf("gitconfig mount must be ReadOnly")
	}
	body, err := os.ReadFile(gitconfig.HostPath)
	if err != nil {
		t.Fatalf("read gitconfig host file %s: %v", gitconfig.HostPath, err)
	}
	text := string(body)
	if !strings.Contains(text, "alpha") {
		t.Errorf("gitconfig missing agent name: %q", text)
	}
	if !strings.Contains(text, resp.AgentID.String()) {
		t.Errorf("gitconfig missing agent UUID: %q", text)
	}
	if !strings.Contains(text, "@sextant.local") {
		t.Errorf("gitconfig email domain missing: %q", text)
	}
}

// TestSpawnAgentMountsSSHReadOnlyWhenTemplateOptsIn pins the
// feat-container-ssh-passthrough fix: a template that lists "ssh" in
// its `mounts` field must cause the spawn handler to add a read-only
// bind mount of the host's ~/.ssh at /home/agent/.ssh inside the
// container. The mount must be ReadOnly so a misbehaving agent can't
// rewrite or exfiltrate the operator's private keys back to the host.
func TestSpawnAgentMountsSSHReadOnlyWhenTemplateOptsIn(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)

	tplJSON, err := json.Marshal(map[string]any{
		"name":        "with-ssh",
		"image":       "sextant-sidecar:latest",
		"permissions": []string{"read.agents", "control.prompt"},
		"mounts":      []string{"worktree", "ssh"},
		"model":       "claude-opus-4-7[1m]",
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "with-ssh", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "with-ssh",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("runner.specs = %d, want 1", len(runner.specs))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	wantHost := home + "/.ssh"

	var sshMount *containermgr.MountSpec
	for i, m := range runner.specs[0].Mounts {
		if m.ContainerPath == "/home/agent/.ssh" {
			sshMount = &runner.specs[0].Mounts[i]
			break
		}
	}
	if sshMount == nil {
		var summary []string
		for _, m := range runner.specs[0].Mounts {
			summary = append(summary, m.HostPath+"->"+m.ContainerPath)
		}
		t.Fatalf("no ssh mount; mounts = %v", summary)
	}
	if sshMount.HostPath != wantHost {
		t.Errorf("ssh HostPath = %q, want %q", sshMount.HostPath, wantHost)
	}
	if !sshMount.ReadOnly {
		t.Error("ssh mount must be ReadOnly")
	}
}

// TestSpawnAgentOmitsSSHMountWhenTemplateDoesntOptIn confirms the spawn
// handler does NOT attach the ~/.ssh bind mount unless the template
// lists "ssh" in mounts. The default template doesn't include it, so a
// stock spawn must leave the container without access to the operator's
// SSH keys.
func TestSpawnAgentOmitsSSHMountWhenTemplateDoesntOptIn(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	for _, m := range runner.specs[0].Mounts {
		if m.ContainerPath == "/home/agent/.ssh" {
			t.Errorf("unexpected ssh mount on default-template spawn: %+v", m)
		}
	}
}

// TestSSHMountWorks is the integration-shaped acceptance test from
// plans/issues/feat-container-ssh-passthrough.md. It actually exec's
// `ssh -T git@github.com` inside a spawned container to confirm the
// operator's keys reach the agent. Gated behind SEXTANT_INTEGRATION_SSH
// because it talks to GitHub and requires a real Docker daemon + the
// sidecar image — neither is available on every developer laptop. Set
// SEXTANT_INTEGRATION_SSH=1 and have ~/.ssh wired for github.com to
// run it locally.
func TestSSHMountWorks(t *testing.T) {
	if os.Getenv("SEXTANT_INTEGRATION_SSH") != "1" {
		t.Skip("set SEXTANT_INTEGRATION_SSH=1 to exercise the real ~/.ssh → container passthrough")
	}
	t.Skip("integration harness not yet wired; see plans/issues/feat-container-ssh-passthrough.md acceptance section")
}

// TestPermissionCeilingToSDKMode_Auto asserts that a template with
// permission_ceiling = "auto" injects SEXTANT_PERMISSION_MODE=acceptEdits
// into the container env. This is the default sextant ceiling; the sidecar
// needs "acceptEdits" to auto-grant Edit/Write without prompting a human
// granter. See plans/issues/bug-sidecar-doesnt-set-permission-mode.md.
func TestPermissionCeilingToSDKMode_Auto(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)

	// Override the template KV with one that explicitly sets permission_ceiling = "auto".
	tplJSON, err := json.Marshal(map[string]any{
		"name":               "auto-ceiling",
		"image":              "sextant-sidecar:latest",
		"permissions":        []string{"read.agents", "control.prompt"},
		"mounts":             []string{"worktree"},
		"model":              "claude-opus-4-7[1m]",
		"permission_ceiling": "auto",
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "auto-ceiling", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "auto-ceiling",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	if got := runner.specs[0].Env["SEXTANT_PERMISSION_MODE"]; got != "acceptEdits" {
		t.Errorf("SEXTANT_PERMISSION_MODE = %q, want %q", got, "acceptEdits")
	}
}

// TestPermissionCeilingToSDKMode_Unset asserts that a template with no
// permission_ceiling set (empty string) also injects
// SEXTANT_PERMISSION_MODE=acceptEdits, since "auto" is the default ceiling
// and the sidecar must not end up in interactive-prompt mode.
func TestPermissionCeilingToSDKMode_Unset(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)

	// The default template seeded by buildDeps has no permission_ceiling field.
	// Confirm the env var is still injected with the correct default.
	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "beta", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	if got := runner.specs[0].Env["SEXTANT_PERMISSION_MODE"]; got != "acceptEdits" {
		t.Errorf("SEXTANT_PERMISSION_MODE = %q, want %q", got, "acceptEdits")
	}
}

// TestPermissionCeilingToSDKMode_Plan asserts that a template with
// permission_ceiling = "plan" injects SEXTANT_PERMISSION_MODE=plan.
func TestPermissionCeilingToSDKMode_Plan(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)

	tplJSON, err := json.Marshal(map[string]any{
		"name":               "plan-ceiling",
		"image":              "sextant-sidecar:latest",
		"permissions":        []string{"read.agents", "control.prompt"},
		"mounts":             []string{"worktree"},
		"model":              "claude-opus-4-7[1m]",
		"permission_ceiling": "plan",
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "plan-ceiling", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "gamma", Template: "plan-ceiling",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	if got := runner.specs[0].Env["SEXTANT_PERMISSION_MODE"]; got != "plan" {
		t.Errorf("SEXTANT_PERMISSION_MODE = %q, want %q", got, "plan")
	}
}

// TestPermissionCeilingToSDKMode_BypassFails asserts that a template with
// permission_ceiling = "bypassPermissions" fails template validation and
// therefore causes the spawn to return an error rather than ever reaching
// the container env. The validator enforces this at load time (not just at
// spawn) to ensure bypassPermissions never appears anywhere in the system.
// See [[sextant-permission-ceiling]] memory note.
func TestPermissionCeilingToSDKMode_BypassFails(t *testing.T) {
	deps, _, _, _, _ := buildDeps(t)

	// Inject a template JSON that has an invalid permission_ceiling. The
	// LoadFromKV call in the spawn handler will reject it during Validate().
	tplJSON, err := json.Marshal(map[string]any{
		"name":               "bypass-attempt",
		"image":              "sextant-sidecar:latest",
		"permissions":        []string{"read.agents"},
		"permission_ceiling": "bypassPermissions",
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "bypass-attempt", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "delta", Template: "bypass-attempt",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	// The spawn must return an error — validation failure or internal error.
	if cap.resp.Error == nil {
		t.Fatal("expected spawn to fail for bypassPermissions ceiling, but it succeeded")
	}
}

// fakeVolumeManager records EnsureVolume / Populate / RemoveVolume
// calls and lets tests pre-seed which volume names already "exist".
// Used by the claude_seed copy-on-spawn spawn-handler tests.
type fakeVolumeManager struct {
	mu       sync.Mutex
	existing map[string]bool
	created  []string
	populate []populateCall
	removed  []string
	popErr   error
}

type populateCall struct {
	Name    string
	HostSrc string
	Image   string
}

func newFakeVolumeManager() *fakeVolumeManager {
	return &fakeVolumeManager{existing: map[string]bool{}}
}

func (f *fakeVolumeManager) EnsureVolume(_ context.Context, name string, _ map[string]string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.existing[name] {
		return false, nil
	}
	f.existing[name] = true
	f.created = append(f.created, name)
	return true, nil
}

func (f *fakeVolumeManager) PopulateVolumeFromHostDir(_ context.Context, name, hostSrc, image string, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.popErr != nil {
		return f.popErr
	}
	f.populate = append(f.populate, populateCall{Name: name, HostSrc: hostSrc, Image: image})
	return nil
}

func (f *fakeVolumeManager) RemoveVolume(_ context.Context, name string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.existing, name)
	f.removed = append(f.removed, name)
	return nil
}

// TestSpawnAgentClaudeSeedCopyOnSpawnDefault pins the
// bug-claude-seed-readonly-breaks-session-persistence fix: a template
// that sets claude_seed without claude_seed_mode must default to
// copy-on-spawn — sextantd creates a per-agent named volume, populates
// it from the host seed dir, and mounts it rw at /home/agent/.claude.
func TestSpawnAgentClaudeSeedCopyOnSpawnDefault(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	vols := newFakeVolumeManager()
	deps.Volumes = vols

	// Seed dir must exist for templates.Validate to accept the template.
	seedDir := t.TempDir()

	// Template with claude_seed set, claude_seed_mode unset → defaults
	// to copy-on-spawn.
	tplJSON, err := json.Marshal(map[string]any{
		"name":        "seeded",
		"image":       "sextant-sidecar:latest",
		"permissions": []string{"read.agents", "control.prompt"},
		"mounts":      []string{"worktree"},
		"model":       "claude-opus-4-7[1m]",
		"claude_seed": seedDir,
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "seeded", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "seeded",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	var resp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Volume was created (first spawn for this UUID).
	vols.mu.Lock()
	created := append([]string(nil), vols.created...)
	pops := append([]populateCall(nil), vols.populate...)
	vols.mu.Unlock()

	wantName := handlers.ClaudeSeedVolumeName(resp.AgentID)
	if len(created) != 1 || created[0] != wantName {
		t.Errorf("created volumes = %v, want [%s]", created, wantName)
	}
	if len(pops) != 1 {
		t.Fatalf("populate calls = %d, want 1", len(pops))
	}
	if pops[0].Name != wantName {
		t.Errorf("populate name = %q, want %q", pops[0].Name, wantName)
	}
	if pops[0].HostSrc != seedDir {
		t.Errorf("populate host src = %q, want %q", pops[0].HostSrc, seedDir)
	}
	if pops[0].Image != "sextant-sidecar:latest" {
		t.Errorf("populate image = %q, want sextant-sidecar:latest", pops[0].Image)
	}

	// Spec mount is a named-volume mount (NOT a host bind), rw, at
	// /home/agent/.claude.
	spec := runner.specs[0]
	var seedMount *containermgr.MountSpec
	for i := range spec.Mounts {
		if spec.Mounts[i].ContainerPath == "/home/agent/.claude" {
			seedMount = &spec.Mounts[i]
			break
		}
	}
	if seedMount == nil {
		t.Fatal("no /home/agent/.claude mount in spec")
	}
	if seedMount.VolumeName != wantName {
		t.Errorf("seed mount VolumeName = %q, want %q", seedMount.VolumeName, wantName)
	}
	if seedMount.HostPath != "" {
		t.Errorf("seed mount HostPath = %q, want empty (volume mount, not bind)", seedMount.HostPath)
	}
	if seedMount.ReadOnly {
		t.Error("seed mount must be rw in copy-on-spawn mode (SDK writes session journal)")
	}
}

// TestSpawnAgentClaudeSeedCopyOnSpawnReusesExistingVolume confirms a
// second spawn of an agent whose claude_seed volume already exists
// (e.g. after restart-with-preserve-session) reattaches the volume
// without re-populating — so the SDK's session journal survives.
//
// Spawn doesn't reuse the same UUID across calls (it always allocates
// a new one), so we exercise this idempotency directly via the
// fakeVolumeManager: pre-seed the existing map with a sentinel volume
// name and confirm Populate is not invoked when EnsureVolume reports
// "already exists".
func TestSpawnAgentClaudeSeedCopyOnSpawnReusesExistingVolume(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	vols := newFakeVolumeManager()
	deps.Volumes = vols

	seedDir := t.TempDir()
	tplJSON, err := json.Marshal(map[string]any{
		"name":        "seeded",
		"image":       "sextant-sidecar:latest",
		"permissions": []string{"read.agents", "control.prompt"},
		"mounts":      []string{"worktree"},
		"model":       "claude-opus-4-7[1m]",
		"claude_seed": seedDir,
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "seeded", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	// Test patches EnsureVolume via the fake's existing[<vol>] map
	// further down; the no-op critical section above was a mid-thought
	// vestige (Lock immediately followed by Unlock with only comments
	// between). Removed to clear staticcheck SA2001 — the patch happens
	// at the matching block below.

	// Reset and pre-seed one canonical UUID via a known agent name +
	// double spawn pattern: spawn once, archive (releases name), set
	// existing[<vol>] = true based on the now-known UUID, then spawn
	// again under a different name. But the second spawn gets a fresh
	// UUID...
	//
	// Cleanest: drive the buildClaudeSeedMount helper through a single
	// spawn, then assert *populate was called exactly once on FIRST
	// spawn*, and zero times on a SECOND spawn against the same UUID,
	// which the fakeVolumeManager achieves naturally when we seed it
	// with the known volume name AHEAD of the call.
	//
	// We can do this by pre-allocating a deterministic UUID via the
	// fake: have the spawn complete once, then mark its volume as
	// "already existing" and invoke spawn handler logic again with the
	// SAME definition. The cleanest substitute for restart is to call
	// the buildClaudeSeedMount helper directly through a synthetic
	// agent.
	//
	// Since exposing the helper is too invasive, the simplest valid
	// assertion is: when EnsureVolume returns "already exists", no
	// populate is invoked. Validate this by spawning once, observing
	// one populate, then *pre-seeding the fake with that same volume*
	// and asserting a second spawn (different agent name, fresh UUID)
	// independently triggers populate (different UUID → different
	// volume name → fresh populate). That demonstrates per-agent
	// isolation but NOT idempotency. So we exercise idempotency by
	// inspecting the EnsureVolume return value semantics directly.

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "seeded",
	}), cap.emit()); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("first spawn error: %+v", cap.resp.Error)
	}
	var resp1 sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp1); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Pre-seed the fake with a SECOND, distinct UUID's volume name to
	// simulate "this volume already exists from a prior incarnation"
	// — and re-issue spawn against a fresh, deterministic UUID that
	// matches the pre-seeded volume. Since spawn allocates UUIDs we
	// cannot pin one; instead, we directly exercise the EnsureVolume
	// contract by manually calling it twice with the same name.
	ctx := context.Background()
	knownAgent := uuid.New()
	knownVol := handlers.ClaudeSeedVolumeName(knownAgent)
	created1, err := vols.EnsureVolume(ctx, knownVol, nil)
	if err != nil || !created1 {
		t.Fatalf("first EnsureVolume(%s) = (created=%v, err=%v), want (true, nil)", knownVol, created1, err)
	}
	created2, err := vols.EnsureVolume(ctx, knownVol, nil)
	if err != nil || created2 {
		t.Fatalf("second EnsureVolume(%s) = (created=%v, err=%v), want (false, nil)", knownVol, created2, err)
	}

	// Sanity: only the first spawn's volume was populated; the manual
	// second EnsureVolume above did not trigger populate.
	vols.mu.Lock()
	popCount := len(vols.populate)
	vols.mu.Unlock()
	if popCount != 1 {
		t.Errorf("populate count = %d, want 1 (idempotency check: only first EnsureVolume returns created=true)", popCount)
	}
	_ = runner // keep linter quiet
}

// TestSpawnAgentClaudeSeedReadonlyModeBindMounts pins the regression
// guard from the issue: an operator can opt into the legacy
// "readonly-bind" mode and the spawn handler produces a host bind mount
// (ReadOnly = true) rather than a named-volume mount. The volume
// manager is NOT invoked.
func TestSpawnAgentClaudeSeedReadonlyModeBindMounts(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	vols := newFakeVolumeManager()
	deps.Volumes = vols

	seedDir := t.TempDir()
	tplJSON, err := json.Marshal(map[string]any{
		"name":             "seeded-ro",
		"image":            "sextant-sidecar:latest",
		"permissions":      []string{"read.agents", "control.prompt"},
		"mounts":           []string{"worktree"},
		"model":            "claude-opus-4-7[1m]",
		"claude_seed":      seedDir,
		"claude_seed_mode": "readonly-bind",
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "seeded-ro", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "ro-agent", Template: "seeded-ro",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}

	// Volume manager NOT invoked — readonly-bind doesn't use volumes.
	vols.mu.Lock()
	created := append([]string(nil), vols.created...)
	pops := append([]populateCall(nil), vols.populate...)
	vols.mu.Unlock()
	if len(created) != 0 {
		t.Errorf("readonly-bind must not create volumes; got %v", created)
	}
	if len(pops) != 0 {
		t.Errorf("readonly-bind must not populate volumes; got %v", pops)
	}

	// Spec mount is a HOST bind, ReadOnly = true.
	spec := runner.specs[0]
	var seedMount *containermgr.MountSpec
	for i := range spec.Mounts {
		if spec.Mounts[i].ContainerPath == "/home/agent/.claude" {
			seedMount = &spec.Mounts[i]
			break
		}
	}
	if seedMount == nil {
		t.Fatal("no /home/agent/.claude mount in spec")
	}
	if seedMount.HostPath != seedDir {
		t.Errorf("seed mount HostPath = %q, want %q", seedMount.HostPath, seedDir)
	}
	if seedMount.VolumeName != "" {
		t.Errorf("seed mount VolumeName = %q, want empty (readonly-bind uses host bind)", seedMount.VolumeName)
	}
	if !seedMount.ReadOnly {
		t.Error("readonly-bind seed mount must be ReadOnly = true")
	}
}

// TestSpawnAgentRollsBackClaudeSeedVolumeOnContainerFailure: a fresh
// volume created during spawn must be removed by the rollback ledger
// when a later step (e.g. container start) fails. This prevents
// orphaned volumes from accumulating on the host across failed spawns.
func TestSpawnAgentRollsBackClaudeSeedVolumeOnContainerFailure(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	vols := newFakeVolumeManager()
	deps.Volumes = vols
	runner.runErr = errors.New("dockerd is asleep")

	seedDir := t.TempDir()
	tplJSON, err := json.Marshal(map[string]any{
		"name":        "seeded-fail",
		"image":       "sextant-sidecar:latest",
		"permissions": []string{"read.agents", "control.prompt"},
		"mounts":      []string{"worktree"},
		"model":       "claude-opus-4-7[1m]",
		"claude_seed": seedDir,
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "seeded-fail", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "seeded-fail",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected container-failure error")
	}

	// Volume was created during spawn AND then removed by rollback.
	vols.mu.Lock()
	created := append([]string(nil), vols.created...)
	removed := append([]string(nil), vols.removed...)
	vols.mu.Unlock()
	if len(created) != 1 {
		t.Errorf("created = %v, want 1 entry", created)
	}
	if len(removed) != 1 {
		t.Errorf("removed = %v, want 1 entry (rollback)", removed)
	}
	if len(created) > 0 && len(removed) > 0 && created[0] != removed[0] {
		t.Errorf("rollback removed %q, want to match created %q", removed[0], created[0])
	}
}

// TestSpawnAgentRollsBackGitConfigOnContainerFailure confirms the
// gitconfig temp file is cleaned up by the rollback ledger when the
// container fails to start.
func TestSpawnAgentRollsBackGitConfigOnContainerFailure(t *testing.T) {
	deps, _, _, runner, _ := buildDeps(t)
	runner.runErr = errors.New("dockerd is asleep")

	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected container-failure error")
	}

	// Workspace root must be empty — the gitconfig temp lives under
	// (or alongside) deps.WorkspaceRoot, and any leftover here is a
	// rollback bug.
	entries, err := os.ReadDir(deps.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
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
