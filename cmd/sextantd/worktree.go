package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
	"github.com/love-lena/sextant-initial/pkg/worktree"
)

// osHostname returns the host's name; falls back to "unknown" on
// error. Used to stamp audit envelopes from the daemon with a stable
// actor ID.
func osHostname() (string, error) {
	h, err := os.Hostname()
	if err != nil {
		return "unknown", err
	}
	return h, nil
}

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

	// pruner + its periodic ticker. Both are nil when worktree is
	// disabled. The ticker goroutine stops when stopPrune is closed.
	pruner        *worktree.Pruner
	pruneInterval time.Duration
	autoPrune     bool
	nc            *nats.Conn
	from          sextantproto.Address

	pruneSub      *nats.Subscription
	stopPrune     chan struct{}
	pruneStopOnce sync.Once
	pruneWG       sync.WaitGroup
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

	// Build the pruner. ArchiveRoot is required; Resolve guarantees it
	// is non-empty when DataDir is set, but fall back defensively here
	// so a hand-rolled config without a data_dir still gets a sane
	// path under the worktrees root's parent.
	archiveRoot := d.cfg.Worktree.ArchiveRoot
	if archiveRoot == "" {
		archiveRoot = filepath.Join(filepath.Dir(d.cfg.Worktree.WorktreesRoot), "worktree-archive")
	}
	hostname := "sextantd"
	if h, errH := osHostname(); errH == nil {
		hostname = h
	}
	from := sextantproto.Address{
		Kind: sextantproto.AddressDaemon,
		ID:   fmt.Sprintf("daemon-%s", hostname),
	}
	pruner, err := worktree.NewPruner(worktree.PrunerConfig{
		Manager:     mgr,
		ArchiveRoot: archiveRoot,
		Now:         time.Now,
		AuditFn:     newWorktreeAuditPublisher(nc, from),
	})
	if err != nil {
		return nil, fmt.Errorf("worktree: build pruner: %w", err)
	}

	pruneInterval := d.cfg.Worktree.PruneInterval.AsDuration()
	if pruneInterval <= 0 {
		pruneInterval = sextantd.DefaultPruneInterval
	}

	log.Printf("sextantd: worktree manager ready (repo=%s worktrees_root=%s archive=%s prune_interval=%s auto_prune=%v)",
		repoRoot, d.cfg.Worktree.WorktreesRoot, archiveRoot, pruneInterval, d.cfg.Worktree.AutoPrune)
	return &worktreeRuntime{
		mgr:           mgr,
		registryKV:    regKV,
		locksKV:       locksKV,
		repoRoot:      repoRoot,
		worktreesDir:  d.cfg.Worktree.WorktreesRoot,
		pruner:        pruner,
		pruneInterval: pruneInterval,
		autoPrune:     d.cfg.Worktree.AutoPrune,
		nc:            nc,
		from:          from,
		stopPrune:     make(chan struct{}),
	}, nil
}

// startPruneLoop kicks off the periodic prune ticker and the control
// subscription for `sextant.control.worktree_prune`. Idempotent — a
// nil runtime is a no-op so the caller can invoke unconditionally.
//
// The ticker fires the first prune after `pruneInterval` (NOT
// immediately on boot); a daemon that bounces every few minutes
// wouldn't want to thrash the disk. Operators who want an immediate
// sweep run `sextant worktree prune`.
func (r *worktreeRuntime) startPruneLoop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.pruner == nil {
		return nil
	}
	// Subscribe to the control subject FIRST so the CLI verb works
	// even if the ticker is mid-sleep — and so operator-driven prune
	// works regardless of auto_prune.
	sub, err := r.nc.Subscribe(sextantd.ControlWorktreePruneSubject, r.handlePruneRequest)
	if err != nil {
		return fmt.Errorf("worktree: subscribe %s: %w", sextantd.ControlWorktreePruneSubject, err)
	}
	r.pruneSub = sub

	// The auto-fire ticker is OFF by default. The CLI verb stays
	// available either way — operators drive `sextant worktree prune
	// --apply` when they want a sweep. To enable hands-off cleanup,
	// set `[worktree] auto_prune = true` in sextantd.toml after
	// verifying a dry-run won't kill anything you still want.
	if !r.autoPrune {
		log.Printf("sextantd: worktree pruner ticker disabled (auto_prune=false); use `sextant worktree prune --apply` to run on demand")
		return nil
	}

	r.pruneWG.Add(1)
	go r.pruneTickerLoop(ctx)
	return nil
}

// pruneTickerLoop runs prunes on a periodic ticker until stopPrune is
// closed or ctx is canceled.
func (r *worktreeRuntime) pruneTickerLoop(ctx context.Context) {
	defer r.pruneWG.Done()
	t := time.NewTicker(r.pruneInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopPrune:
			return
		case <-t.C:
			runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			report, err := r.pruner.Run(runCtx, worktree.PruneRunOptions{})
			cancel()
			if err != nil {
				log.Printf("sextantd: worktree pruner: %v", err)
				continue
			}
			if report.Archived+report.Deleted+report.OrphansDeleted > 0 || len(report.Errors) > 0 {
				log.Printf("sextantd: worktree pruner ran: archived=%d deleted=%d orphans_deleted=%d skipped=%d errors=%d",
					report.Archived, report.Deleted, report.OrphansDeleted, report.Skipped, len(report.Errors))
			}
		}
	}
}

// handlePruneRequest answers a sextant.control.worktree_prune NATS
// request. The CLI publishes WorktreePruneRequest{DryRun: ...} and
// expects a JSON-encoded WorktreePruneResponse back.
func (r *worktreeRuntime) handlePruneRequest(msg *nats.Msg) {
	if msg.Reply == "" {
		log.Printf("sextantd: worktree_prune request without reply inbox; ignored")
		return
	}
	var req sextantd.WorktreePruneRequest
	if len(msg.Data) > 0 {
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			r.respondPrune(msg, sextantd.WorktreePruneResponse{Error: fmt.Sprintf("decode request: %v", err)})
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := r.pruner.Run(ctx, worktree.PruneRunOptions{
		DryRun:            req.DryRun,
		AllowOrphanDelete: req.AllowOrphanDelete,
	})
	if err != nil {
		r.respondPrune(msg, sextantd.WorktreePruneResponse{Error: err.Error(), DryRun: req.DryRun})
		return
	}
	thresholds := r.pruner.Thresholds()
	resp := sextantd.WorktreePruneResponse{
		Archived:       report.Archived,
		Deleted:        report.Deleted,
		Skipped:        report.Skipped,
		OrphansDeleted: report.OrphansDeleted,
		OrphansKept:    report.OrphansKept,
		Errors:         report.Errors,
		DryRun:         req.DryRun,
		ArchiveAge:     thresholds.ArchiveAge,
		DeleteAge:      thresholds.DeleteAge,
	}
	for _, plan := range report.Plans {
		resp.Plans = append(resp.Plans, sextantd.WorktreePrunePlan{
			Name:   plan.Name,
			Path:   plan.Path,
			Action: plan.Action,
			Reason: plan.Reason,
		})
	}
	r.respondPrune(msg, resp)
}

func (r *worktreeRuntime) respondPrune(msg *nats.Msg, resp sextantd.WorktreePruneResponse) {
	raw, err := json.Marshal(resp)
	if err != nil {
		log.Printf("sextantd: marshal worktree_prune response: %v", err)
		return
	}
	if err := msg.Respond(raw); err != nil {
		log.Printf("sextantd: respond on %s: %v", msg.Reply, err)
	}
}

// stopPruneLoop unsubscribes the control subject and signals the
// ticker goroutine to drain. Idempotent.
func (r *worktreeRuntime) stopPruneLoop() {
	if r == nil {
		return
	}
	r.pruneStopOnce.Do(func() {
		if r.stopPrune != nil {
			close(r.stopPrune)
		}
		if r.pruneSub != nil {
			if err := r.pruneSub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
				log.Printf("sextantd: worktree_prune unsubscribe: %v", err)
			}
			r.pruneSub = nil
		}
	})
	r.pruneWG.Wait()
}

// newWorktreeAuditPublisher returns a closure the Pruner calls with
// each archive / delete / orphan action. The closure marshals an
// AuditPayload + envelope and publishes it on the appropriate
// `audit.worktree_*` subject. Best-effort: a publish failure is
// logged but does not block the prune.
func newWorktreeAuditPublisher(nc *nats.Conn, from sextantproto.Address) func(worktree.PruneAudit) {
	return func(a worktree.PruneAudit) {
		details := map[string]any{
			"name": a.Name,
			"path": a.Path,
		}
		if a.ArchivePath != "" {
			details["archive_path"] = a.ArchivePath
		}
		if !a.LastActivity.IsZero() {
			details["last_activity"] = a.LastActivity.UTC().Format(time.RFC3339)
		}
		if !a.Mtime.IsZero() {
			details["mtime"] = a.Mtime.UTC().Format(time.RFC3339)
		}
		if a.AgeDays > 0 {
			details["age_days"] = a.AgeDays
		}
		payload := sextantproto.AuditPayload{
			Actor:   from.ID,
			Action:  a.Action,
			Result:  sextantproto.AuditAllowed,
			Details: details,
		}
		env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAudit, from, payload)
		if err != nil {
			log.Printf("sextantd: build worktree audit envelope: %v", err)
			return
		}
		envRaw, err := json.Marshal(env)
		if err != nil {
			log.Printf("sextantd: marshal worktree audit envelope: %v", err)
			return
		}
		if err := nc.Publish(a.Action, envRaw); err != nil {
			log.Printf("sextantd: publish %s: %v", a.Action, err)
		}
	}
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
