package handlers_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// killCASRetriesExpected mirrors the handler-side killCASRetries const.
// Pinning it here lets the test assert on the budget without exporting
// the constant from the production package.
const killCASRetriesExpected = 3

// TestKillAgentRetriesOnReconcilerRace pins the
// bug-kill-agent-cas-flakes-integration-tests fix: when a legitimate
// daemon-side writer (the L2 reconciler, the LifecycleWatcher) commits
// a def update between kill_agent's initial Get and its final Update,
// the CAS conflict must trigger a retry instead of an immediate
// BAD_REQUEST. The retry budget is 3 (mirroring lifecycle_watcher.go's
// watcherCASRetries).
//
// The injected race fires exactly once before kill's first Update
// attempt, so the first retry sees a clean revision and the kill
// commits cleanly.
func TestKillAgentRetriesOnReconcilerRace(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)

	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "kill-race", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", cap.resp.Error)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// Race injection: on the "mark incarnation exited" incs Put
	// (callIdx == 2 — spawn writes call 1, kill writes call 2),
	// simulate a reconciler-shaped def update by Putting a no-op
	// revision bump on the def key. This fires exactly once so the
	// kill's first retry sees a clean revision and succeeds.
	var injected int32
	incs.putHook = func(_ string, callIdx int) error {
		if callIdx == 2 && atomic.CompareAndSwapInt32(&injected, 0, 1) {
			bumpDefRevision(context.Background(), defs, spawnResp.AgentID.String())
		}
		return nil
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
		t.Fatalf("kill error: %+v (want clean success — the retry budget should absorb the reconciler-shaped race)",
			killCap.resp.Error)
	}

	// Container was stopped exactly once — side effects do NOT replay
	// on CAS retry. This is the kill_agent vs restart_agent asymmetry
	// the fix comment documents.
	runner.mu.Lock()
	stopped := append([]string(nil), runner.stopped...)
	runner.mu.Unlock()
	if len(stopped) != 1 {
		t.Errorf("container Stop count = %d, want 1 (side effect must not replay on retry)", len(stopped))
	}

	// Final def state is kill-intended: lifecycle=defined.
	defSnap := defs.snapshot()
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(defSnap[spawnResp.AgentID.String()], &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	if def.Lifecycle != sextantproto.LifecycleDefined {
		t.Errorf("def.Lifecycle = %s, want defined (kill's mutation must be the final state after the retry)",
			def.Lifecycle)
	}
}

// TestKillAgentExhaustsRetryBudget pins the bail path: when concurrent
// writes outpace the retry budget, kill returns BAD_REQUEST so the
// operator can re-issue against the current state. The bumping wrapper
// bumps the def revision before every Update attempt, guaranteeing all
// killCASRetries attempts collide.
func TestKillAgentExhaustsRetryBudget(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)

	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "kill-exhaust", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", cap.resp.Error)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// bumpingKV wraps defs and rewrites the def at the target key with
	// a no-op revision bump before every Update call. This simulates a
	// daemon-side reconciler that's writing on the same cadence as
	// kill's retry loop — guaranteeing every CAS attempt collides.
	bumpingDefs := &bumpingKV{wrapped: defs, key: spawnResp.AgentID.String()}

	killH := handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  bumpingDefs,
		Incarnations: incs,
		Containers:   runner,
	})
	killCap := &captureEmit{}
	if err := killH(context.Background(), makeReq(t, sextantproto.KillAgentRequest{
		AgentID: spawnResp.AgentID,
	}), killCap.emit()); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if killCap.resp.Error == nil {
		t.Fatal("expected an error — the retry budget should exhaust when every CAS attempt races")
	}
	if killCap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("Error.Code = %q, want %q (exhausted CAS budget surfaces as bad_request)",
			killCap.resp.Error.Code, sextantproto.ErrCodeBadRequest)
	}

	// The container was still stopped — the side effect ran once
	// before the CAS loop, regardless of the eventual bail.
	runner.mu.Lock()
	stopped := append([]string(nil), runner.stopped...)
	runner.mu.Unlock()
	if len(stopped) != 1 {
		t.Errorf("container Stop count = %d, want 1 (stop runs once before the retry loop)", len(stopped))
	}

	// At least killCASRetries Update attempts were made (the budget).
	if got := bumpingDefs.updateAttempts(); got < killCASRetriesExpected {
		t.Errorf("Update attempts = %d, want >= %d (must exhaust the retry budget)",
			got, killCASRetriesExpected)
	}
}

// bumpDefRevision rewrites the def at key with a no-op Version++ so the
// stored revision moves. Used to simulate a concurrent legitimate
// writer (reconciler / lifecycle watcher) for the CAS retry tests.
func bumpDefRevision(ctx context.Context, defs *fakeMutableKV, key string) {
	entry, err := defs.Get(ctx, key)
	if err != nil {
		return
	}
	var current sextantproto.AgentDefinition
	if err := json.Unmarshal(entry.Value(), &current); err != nil {
		return
	}
	current.Version++
	raw, err := json.Marshal(current)
	if err != nil {
		return
	}
	_, _ = defs.Put(ctx, key, raw)
}

// bumpingKV is an AgentMutableKV wrapper that, before every Update on
// the target key, bumps the stored revision via an out-of-band Put.
// This guarantees every kill CAS attempt against `key` collides, which
// is exactly the budget-exhaustion shape TestKillAgentExhaustsRetryBudget
// asserts against. Other keys (e.g. the incarnations bucket) pass
// through untouched.
type bumpingKV struct {
	wrapped *fakeMutableKV
	key     string
	// updateAttemptsAtomic counts Update calls observed against `key`.
	// Exposed via the updateAttempts() accessor.
	updateAttemptsAtomic int64
}

func (b *bumpingKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	return b.wrapped.Get(ctx, key)
}

func (b *bumpingKV) ListKeys(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	return b.wrapped.ListKeys(ctx, opts...)
}

func (b *bumpingKV) Put(ctx context.Context, key string, value []byte) (uint64, error) {
	return b.wrapped.Put(ctx, key, value)
}

func (b *bumpingKV) Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error) {
	if key == b.key {
		atomic.AddInt64(&b.updateAttemptsAtomic, 1)
		// Bump the revision out-of-band so the impending CAS fails.
		bumpDefRevision(ctx, b.wrapped, key)
	}
	return b.wrapped.Update(ctx, key, value, revision)
}

func (b *bumpingKV) Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	return b.wrapped.Delete(ctx, key, opts...)
}

func (b *bumpingKV) updateAttempts() int {
	return int(atomic.LoadInt64(&b.updateAttemptsAtomic))
}
