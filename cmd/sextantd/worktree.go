package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/worktree"
)

// worktreeRuntime holds the per-daemon worktree manager + the KV
// handles it reads/writes. One runtime per daemon; the daemon's
// worktreeRT field. Built after the spawn runtime is up so we reuse
// the same NATS conn for the registry + lock buckets.
type worktreeRuntime struct {
	mgr          *worktree.Manager
	registryKV   jetstream.KeyValue
	locksKV      jetstream.KeyValue
	repoRoot     string
	worktreesDir string
}

// buildWorktreeRuntime wires the worktree manager. Returns nil (and
// a nil error) when RepoRoot is empty in the config — the M14
// surface is then disabled but the daemon still boots. Callers
// guard against the nil runtime when registering verbs/tools.
func (d *daemon) buildWorktreeRuntime(ctx context.Context, nc *nats.Conn) (*worktreeRuntime, error) {
	if d.cfg.Worktree.RepoRoot == "" {
		log.Printf("sextantd: worktree.repo_root is empty; M14 worktree surface disabled")
		return nil, nil
	}

	// Resolve symlinks in the repo root so the value we hand to the
	// spawn handler matches what git writes into the worktree's `.git`
	// pointer file (git always resolves to canonical paths). On macOS
	// /var is a symlink to /private/var, so without this the spawn-
	// side gitdir bind mount lands on the wrong path and `git status`
	// inside the container errors with "not a git repository". See
	// plans/issues/bug-worktree-gitdir-unreachable-in-container.md.
	repoRoot := d.cfg.Worktree.RepoRoot
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = resolved
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("worktree: jetstream: %w", err)
	}
	regKV, err := js.KeyValue(ctx, worktree.WorktreesBucket)
	if err != nil {
		return nil, fmt.Errorf("worktree: open registry kv %s: %w", worktree.WorktreesBucket, err)
	}
	locksKV, err := js.KeyValue(ctx, worktree.MergeLockBucket)
	if err != nil {
		return nil, fmt.Errorf("worktree: open locks kv %s: %w", worktree.MergeLockBucket, err)
	}

	mgr, err := worktree.New(worktree.Config{
		RepoRoot:      repoRoot,
		WorktreesRoot: d.cfg.Worktree.WorktreesRoot,
		Registry:      kvMutableAdapter{kv: regKV},
		Locks:         lockKVAdapter{kv: locksKV},
		HolderID:      fmt.Sprintf("sextantd-%d", d.startedAt.UnixNano()),
		MergeLockTTL:  worktree.DefaultMergeLockTTL,
		Now:           time.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("worktree: build manager: %w", err)
	}
	log.Printf("sextantd: worktree manager ready (repo=%s worktrees_root=%s)",
		repoRoot, d.cfg.Worktree.WorktreesRoot)
	return &worktreeRuntime{
		mgr:          mgr,
		registryKV:   regKV,
		locksKV:      locksKV,
		repoRoot:     repoRoot,
		worktreesDir: d.cfg.Worktree.WorktreesRoot,
	}, nil
}

// registerWorktreeVerbs installs the M14 worktree RPC verbs onto the
// server. Idempotent registration: if r is nil (worktree disabled)
// the call is a no-op so callers can unconditionally invoke it.
func (r *rpcRuntime) registerWorktreeVerbs(wt *worktreeRuntime) error {
	if wt == nil {
		return nil
	}
	deps := handlers.WorktreeDeps{Manager: wt.mgr}
	if err := r.server.Register(rpc.VerbWorktreeCreate, handlers.NewWorktreeCreate(deps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbWorktreeDestroy, handlers.NewWorktreeDestroy(deps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbWorktreeList, handlers.NewWorktreeList(deps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbWorktreeMerge, handlers.NewWorktreeMerge(deps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbWorktreeDiff, handlers.NewWorktreeDiff(deps)); err != nil {
		return err
	}
	return nil
}

// lockKVAdapter wraps a jetstream.KeyValue so it satisfies
// worktree.LockKV. The interface is intentionally narrower than the
// adapter's full surface — keeps the worktree pkg free of jetstream
// imports beyond the lister types.
type lockKVAdapter struct {
	kv jetstream.KeyValue
}

func (a lockKVAdapter) Create(ctx context.Context, key string, value []byte) (uint64, error) {
	return a.kv.Create(ctx, key, value)
}

func (a lockKVAdapter) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	return a.kv.Get(ctx, key)
}

func (a lockKVAdapter) Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	return a.kv.Delete(ctx, key, opts...)
}
