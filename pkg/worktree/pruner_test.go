package worktree_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
	"github.com/love-lena/sextant-initial/pkg/worktree"
)

// pruneFixture wires a Manager + ArchiveRoot + a captured-audit hook
// for pruner tests. Returns the manager, the registry KV, the
// worktrees root, the archive root, and a *[]worktree.PruneAudit so
// the test can assert which envelopes the pruner emitted.
type pruneFixture struct {
	mgr           *worktree.Manager
	reg           *fakeKV
	repo          string
	worktreesRoot string
	archiveRoot   string
	audits        *[]worktree.PruneAudit
}

func buildPruneFixture(t *testing.T) pruneFixture {
	t.Helper()
	mgr, reg, _, repo := buildManager(t)

	archiveRoot := filepath.Join(t.TempDir(), "worktree-archive")
	audits := []worktree.PruneAudit{}
	return pruneFixture{
		mgr:           mgr,
		reg:           reg,
		repo:          repo,
		worktreesRoot: mgr.WorktreesRoot(),
		archiveRoot:   archiveRoot,
		audits:        &audits,
	}
}

// seedWorktree creates a worktree via the manager and back-dates its
// LastActivity in KV to `age` ago. Returns the on-disk path.
func seedWorktree(t *testing.T, fx *pruneFixture, name string, age time.Duration) string {
	t.Helper()
	ctx := context.Background()
	info, err := fx.mgr.Create(ctx, name, "main", uuid.Nil)
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	// Back-date LastActivity. The pruner reads it from KV; we overwrite
	// the entry directly to simulate a worktree that has been idle for
	// `age` real time.
	info.LastActivity = time.Now().UTC().Add(-age)
	info.CreatedAt = info.LastActivity
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := fx.reg.Put(ctx, name, raw); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return info.Path
}

// newPruner wires a Pruner against the fixture with the given Now
// function. captureAudits is appended to fx.audits.
func newPruner(t *testing.T, fx *pruneFixture, now func() time.Time) *worktree.Pruner {
	t.Helper()
	p, err := worktree.NewPruner(worktree.PrunerConfig{
		Manager:     fx.mgr,
		ArchiveRoot: fx.archiveRoot,
		Now:         now,
		AuditFn: func(audit worktree.PruneAudit) {
			*fx.audits = append(*fx.audits, audit)
		},
	})
	if err != nil {
		t.Fatalf("NewPruner: %v", err)
	}
	return p
}

// TestWorktreePrunerDeletesIdleOver30d covers the policy:
//   - 5d  → untouched
//   - 20d → archived (moved to ArchiveRoot, KV.Status=archived)
//   - 40d → deleted (worktree removed, KV entry gone)
//
// The pruner emits exactly one envelope per archived/deleted worktree.
func TestWorktreePrunerDeletesIdleOver30d(t *testing.T) {
	mgr, reg, _, repo := buildManager(t)
	_ = repo
	// Re-derive the worktrees root: each Manager has a WorktreesRoot we
	// can recover via WorktreesRoot().
	wtRoot := mgr.WorktreesRoot()
	archiveRoot := filepath.Join(t.TempDir(), "archive")
	audits := []worktree.PruneAudit{}
	fx := &pruneFixture{
		mgr:           mgr,
		reg:           reg,
		repo:          repo,
		worktreesRoot: wtRoot,
		archiveRoot:   archiveRoot,
		audits:        &audits,
	}

	fivePath := seedWorktree(t, fx, "feat-fresh-young-001", 5*24*time.Hour)
	twentyPath := seedWorktree(t, fx, "feat-aging-mid-001", 20*24*time.Hour)
	fortyPath := seedWorktree(t, fx, "feat-stale-old-001", 40*24*time.Hour)

	now := time.Now
	pr := newPruner(t, fx, now)

	report, err := pr.Run(context.Background(), worktree.PruneRunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 5d untouched.
	if _, err := os.Stat(fivePath); err != nil {
		t.Errorf("5d worktree path missing: %v", err)
	}
	if _, ok := reg.entries["feat-fresh-young-001"]; !ok {
		t.Errorf("5d worktree KV entry missing")
	}

	// 20d archived: KV entry remains with status=archived; original path
	// should be gone; archive copy should exist.
	if _, err := os.Stat(twentyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("20d worktree original path still exists: %v", err)
	}
	archived, ok := reg.entries["feat-aging-mid-001"]
	if !ok {
		t.Fatalf("20d KV entry missing post-archive")
	}
	var archivedInfo sextantproto.WorktreeInfo
	if err := json.Unmarshal(archived, &archivedInfo); err != nil {
		t.Fatalf("decode archived: %v", err)
	}
	if archivedInfo.Status != sextantproto.WorktreeStatusArchived {
		t.Errorf("20d status = %s, want archived", archivedInfo.Status)
	}
	archivedDir := filepath.Join(archiveRoot, "feat-aging-mid-001")
	if _, err := os.Stat(archivedDir); err != nil {
		t.Errorf("archive copy missing: %v", err)
	}

	// 40d deleted: original path gone, KV entry gone.
	if _, err := os.Stat(fortyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("40d worktree path still exists: %v", err)
	}
	if _, ok := reg.entries["feat-stale-old-001"]; ok {
		t.Errorf("40d KV entry still present")
	}

	// Report tallies match.
	if report.Archived != 1 || report.Deleted != 1 || report.Skipped < 1 {
		t.Errorf("report = %+v, want Archived=1 Deleted=1 Skipped>=1", report)
	}

	// Audit envelopes: one archived + one pruned.
	var sawArchived, sawPruned bool
	for _, a := range audits {
		switch a.Action {
		case "audit.worktree_archived":
			if a.Name == "feat-aging-mid-001" {
				sawArchived = true
			}
		case "audit.worktree_pruned":
			if a.Name == "feat-stale-old-001" {
				sawPruned = true
			}
		}
	}
	if !sawArchived {
		t.Errorf("missing audit.worktree_archived; got %+v", audits)
	}
	if !sawPruned {
		t.Errorf("missing audit.worktree_pruned; got %+v", audits)
	}
}

// TestWorktreePrunerHandlesKVOrphans covers the "directory on disk
// with no KV entry" path: the pruner deletes it (best-effort) and
// emits an audit.worktree_pruned_orphan envelope. Recent orphans
// (mtime < archive threshold) are left in place with a warning.
func TestWorktreePrunerHandlesKVOrphans(t *testing.T) {
	mgr, _, _, _ := buildManager(t)
	wtRoot := mgr.WorktreesRoot()

	// Stranded orphan: directory on disk with old mtime, no KV entry.
	orphanName := "feat-orphan-old-001"
	orphanPath := filepath.Join(wtRoot, orphanName)
	if err := os.MkdirAll(orphanPath, 0o750); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	// Drop a sentinel file so we can prove deletion happened.
	if err := os.WriteFile(filepath.Join(orphanPath, "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// Push mtime 40 days back so the pruner's delete threshold fires.
	oldTime := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(orphanPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Fresh orphan: directory with recent mtime, no KV entry.
	freshName := "feat-orphan-fresh-001"
	freshPath := filepath.Join(wtRoot, freshName)
	if err := os.MkdirAll(freshPath, 0o750); err != nil {
		t.Fatalf("mkdir fresh: %v", err)
	}

	audits := []worktree.PruneAudit{}
	pr, err := worktree.NewPruner(worktree.PrunerConfig{
		Manager:     mgr,
		ArchiveRoot: filepath.Join(t.TempDir(), "archive"),
		Now:         time.Now,
		AuditFn:     func(a worktree.PruneAudit) { audits = append(audits, a) },
	})
	if err != nil {
		t.Fatalf("NewPruner: %v", err)
	}
	// Opt into orphan deletion; without AllowOrphanDelete the pruner
	// refuses to touch on-disk dirs that aren't in the registry — a
	// defensive default that protects operator-curated paths the
	// daemon doesn't know about.
	report, err := pr.Run(context.Background(), worktree.PruneRunOptions{AllowOrphanDelete: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Old orphan deleted, fresh orphan kept.
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old orphan still exists: %v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("fresh orphan should remain: %v", err)
	}
	if report.OrphansDeleted < 1 {
		t.Errorf("report OrphansDeleted = %d, want >= 1", report.OrphansDeleted)
	}
	if report.OrphansKept < 1 {
		t.Errorf("report OrphansKept = %d, want >= 1", report.OrphansKept)
	}

	// Audit envelopes — exactly one audit.worktree_pruned_orphan.
	var sawOrphan bool
	for _, a := range audits {
		if a.Action == "audit.worktree_pruned_orphan" && a.Name == orphanName {
			sawOrphan = true
		}
	}
	if !sawOrphan {
		t.Errorf("missing audit.worktree_pruned_orphan; got %+v", audits)
	}
}

// TestWorktreePrunerRefusesUnregisteredPaths pins the safe-by-default
// behavior: an old on-disk directory without a registry entry is NOT
// deleted unless the caller explicitly passes AllowOrphanDelete=true.
// This is the guard against the pruner wiping operator-curated
// directories that happen to live in worktreesRoot but were created
// outside sextant.
func TestWorktreePrunerRefusesUnregisteredPaths(t *testing.T) {
	mgr, _, _, _ := buildManager(t)
	wtRoot := mgr.WorktreesRoot()

	// Old on-disk dir (100d) with no KV entry — exactly the shape
	// the operator's pre-existing worktrees would have.
	orphanName := "feat-operator-keepme-001"
	orphanPath := filepath.Join(wtRoot, orphanName)
	if err := os.MkdirAll(orphanPath, 0o750); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	if err := os.Chtimes(orphanPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	audits := []worktree.PruneAudit{}
	pr, err := worktree.NewPruner(worktree.PrunerConfig{
		Manager:     mgr,
		ArchiveRoot: filepath.Join(t.TempDir(), "archive"),
		Now:         time.Now,
		AuditFn:     func(a worktree.PruneAudit) { audits = append(audits, a) },
	})
	if err != nil {
		t.Fatalf("NewPruner: %v", err)
	}

	report, err := pr.Run(context.Background(), worktree.PruneRunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(orphanPath); err != nil {
		t.Errorf("safe-by-default failed: unregistered orphan was removed (or stat err): %v", err)
	}
	if report.OrphansDeleted != 0 {
		t.Errorf("OrphansDeleted = %d, want 0 (orphan delete is opt-in)", report.OrphansDeleted)
	}
	for _, a := range audits {
		if a.Action == "audit.worktree_pruned_orphan" {
			t.Errorf("audit.worktree_pruned_orphan should not fire when AllowOrphanDelete=false; got %+v", a)
		}
	}
}

// TestWorktreePrunerDryRun verifies --dry-run / DryRun=true reports
// what would happen without performing any disk or KV mutation.
func TestWorktreePrunerDryRun(t *testing.T) {
	mgr, reg, _, _ := buildManager(t)
	wtRoot := mgr.WorktreesRoot()
	archiveRoot := filepath.Join(t.TempDir(), "archive")
	audits := []worktree.PruneAudit{}
	fx := &pruneFixture{
		mgr:           mgr,
		reg:           reg,
		repo:          "",
		worktreesRoot: wtRoot,
		archiveRoot:   archiveRoot,
		audits:        &audits,
	}

	twentyPath := seedWorktree(t, fx, "feat-dryrun-mid-001", 20*24*time.Hour)
	fortyPath := seedWorktree(t, fx, "feat-dryrun-old-001", 40*24*time.Hour)

	pr := newPruner(t, fx, time.Now)
	report, err := pr.Run(context.Background(), worktree.PruneRunOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Nothing should have moved.
	if _, err := os.Stat(twentyPath); err != nil {
		t.Errorf("20d path missing during dry-run: %v", err)
	}
	if _, err := os.Stat(fortyPath); err != nil {
		t.Errorf("40d path missing during dry-run: %v", err)
	}
	// KV entries untouched.
	if _, ok := reg.entries["feat-dryrun-mid-001"]; !ok {
		t.Errorf("20d KV entry removed during dry-run")
	}
	if _, ok := reg.entries["feat-dryrun-old-001"]; !ok {
		t.Errorf("40d KV entry removed during dry-run")
	}
	// Archive root shouldn't exist (we never copied).
	if _, err := os.Stat(filepath.Join(archiveRoot, "feat-dryrun-mid-001")); err == nil {
		t.Errorf("archive copy created during dry-run")
	}
	// Report still counts the would-be actions so the operator sees the
	// plan.
	if report.Archived != 1 || report.Deleted != 1 {
		t.Errorf("dry-run report = %+v, want Archived=1 Deleted=1", report)
	}
	// No audit envelopes during dry-run — we don't want spurious audit
	// rows from inspections.
	if len(audits) != 0 {
		t.Errorf("dry-run emitted audits: %+v", audits)
	}
}

// TestWorktreePrunerSkipsMergeTransientDirs makes sure the `.merge-*`
// transient dirs the merge handler creates are never treated as
// orphans. Otherwise a long-running merge could be killed mid-flight.
func TestWorktreePrunerSkipsMergeTransientDirs(t *testing.T) {
	mgr, _, _, _ := buildManager(t)
	wtRoot := mgr.WorktreesRoot()

	// .merge-<rand>/ is what allocMergeWorktreeDir produces. Push mtime
	// far past the delete threshold to prove the pruner ignores it on
	// name alone.
	mergeDir := filepath.Join(wtRoot, ".merge-deadbeef")
	if err := os.MkdirAll(mergeDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldTime := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(mergeDir, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	pr, err := worktree.NewPruner(worktree.PrunerConfig{
		Manager:     mgr,
		ArchiveRoot: filepath.Join(t.TempDir(), "archive"),
		Now:         time.Now,
	})
	if err != nil {
		t.Fatalf("NewPruner: %v", err)
	}
	if _, err := pr.Run(context.Background(), worktree.PruneRunOptions{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(mergeDir); err != nil {
		t.Errorf(".merge-* dir was removed: %v", err)
	}
}

// TestPrunerConfigDefaults proves NewPruner fills sensible defaults so
// callers without injection work. ArchiveAge=14d, DeleteAge=30d.
func TestPrunerConfigDefaults(t *testing.T) {
	mgr, _, _, _ := buildManager(t)
	p, err := worktree.NewPruner(worktree.PrunerConfig{
		Manager:     mgr,
		ArchiveRoot: filepath.Join(t.TempDir(), "a"),
	})
	if err != nil {
		t.Fatalf("NewPruner: %v", err)
	}
	got := p.Thresholds()
	if got.ArchiveAge != 14*24*time.Hour {
		t.Errorf("ArchiveAge = %s, want 14d", got.ArchiveAge)
	}
	if got.DeleteAge != 30*24*time.Hour {
		t.Errorf("DeleteAge = %s, want 30d", got.DeleteAge)
	}
}

// silence unused import warning under selective test builds.
var _ = jetstream.ErrKeyNotFound
