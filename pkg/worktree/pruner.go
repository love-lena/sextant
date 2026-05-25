package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// Default age thresholds. The policy lives in
// conventions/git-workflow.md "Disk hygiene": worktrees idle > 14 days
// are archived; > 30 days are deleted. The Pruner reads them from
// PrunerConfig.ArchiveAge / DeleteAge with these constants as the
// fallbacks.
const (
	DefaultArchiveAge = 14 * 24 * time.Hour
	DefaultDeleteAge  = 30 * 24 * time.Hour
)

// Audit action names. The values match
// `plans/issues/feat-worktree-pruner.md` so an operator querying
// audit by action="audit.worktree_pruned" gets the rows the pruner
// produced. They are exported so the wiring layer (sextantd) can
// publish them on the bus without re-defining the strings.
const (
	AuditActionArchived = "audit.worktree_archived"
	AuditActionPruned   = "audit.worktree_pruned"
	AuditActionOrphan   = "audit.worktree_pruned_orphan"
)

// PrunerConfig bundles a Pruner's wiring.
//
//   - Manager (required) — the worktree manager whose registry +
//     worktrees root the Pruner inspects. The pruner reuses Manager's
//     git execution path for `git worktree remove`.
//   - ArchiveRoot (required) — where archived worktrees land. A copy
//     of the on-disk worktree is moved here under a subdir named for
//     the original worktree.
//   - ArchiveAge / DeleteAge — override the spec defaults. Both zero
//     means "use the defaults".
//   - Now — injected for deterministic tests. Production passes
//     time.Now.
//   - AuditFn — optional. Invoked once per archived / deleted / orphan
//     action with a structured PruneAudit so the wiring layer can emit
//     envelopes on the bus. Dry-run does NOT call AuditFn — see
//     PruneRunOptions.DryRun.
type PrunerConfig struct {
	Manager     *Manager
	ArchiveRoot string
	ArchiveAge  time.Duration
	DeleteAge   time.Duration
	Now         func() time.Time
	AuditFn     func(PruneAudit)
}

// PruneRunOptions parameterizes one Pruner.Run invocation. DryRun=true
// reports what would happen without performing any disk or KV
// mutation; AuditFn is not called either (the dry-run is an
// inspection, not an action).
//
// AllowOrphanDelete=false (default) makes the pruner SKIP any disk
// dir not in the worktrees KV registry — even if old enough by the
// archive threshold. Orphans are by definition things sextant did
// not create or has lost track of, and silently deleting them on
// the operator's disk is high-blast-radius. Operators opt in via
// `sextant worktree prune --apply --orphan-delete` or by setting it
// explicitly in the daemon ticker path. Registered worktrees are
// unaffected by this flag — they always follow the archive/delete
// policy.
type PruneRunOptions struct {
	DryRun            bool
	AllowOrphanDelete bool
}

// PruneAudit is the structured record the Pruner hands to AuditFn for
// each action. Wiring code translates this into a sextantproto.Envelope
// + AuditPayload and publishes it on the bus.
type PruneAudit struct {
	// Action is one of AuditActionArchived / AuditActionPruned /
	// AuditActionOrphan.
	Action string

	// Name is the worktree's registry name (or, for orphans, the
	// directory basename).
	Name string

	// Path is the absolute on-disk path acted on.
	Path string

	// ArchivePath, when non-empty, is where an archived worktree
	// landed.
	ArchivePath string

	// LastActivity is the registered last-activity timestamp the
	// decision was based on. Zero for orphans (we use mtime instead;
	// see Mtime).
	LastActivity time.Time

	// Mtime is the disk mtime used for orphan decisions.
	Mtime time.Time

	// AgeDays is the number of whole days the worktree had been idle
	// when the Pruner acted. Convenience field for audit-log
	// formatting; derived from LastActivity / Mtime as appropriate.
	AgeDays int
}

// PrunerThresholds is the resolved (post-defaulting) ArchiveAge +
// DeleteAge. Exposed via Pruner.Thresholds for testing and reporting.
type PrunerThresholds struct {
	ArchiveAge time.Duration
	DeleteAge  time.Duration
}

// PruneReport is the structured return of Pruner.Run. Counts are
// post-action when DryRun=false and counterfactual when DryRun=true.
// Plans is populated with the per-worktree decisions so the CLI can
// display "would archive X" / "would delete Y" lines.
type PruneReport struct {
	// Archived is the count of registered worktrees moved to the
	// archive root this tick.
	Archived int

	// Deleted is the count of registered worktrees removed this tick.
	Deleted int

	// Skipped is the count of registered worktrees that did not meet
	// either threshold (i.e. still active).
	Skipped int

	// OrphansDeleted is the count of disk-only directories the
	// Pruner cleaned up.
	OrphansDeleted int

	// OrphansKept is the count of disk-only directories the Pruner
	// left in place (typically because they are recent enough to be
	// operator-recoverable).
	OrphansKept int

	// Errors is the accumulated set of non-fatal errors. The Pruner
	// is best-effort: a failure on one worktree does not abort the
	// loop.
	Errors []string

	// Plans lists every per-worktree decision the Pruner made (or
	// would make on DryRun). Useful for the CLI's --dry-run output.
	Plans []PrunePlan
}

// PrunePlan is a per-worktree decision.
type PrunePlan struct {
	Name   string
	Path   string
	Action string // "archive" | "delete" | "skip" | "orphan_delete" | "orphan_keep"
	Reason string // human-readable, e.g. "idle 42d, > 30d delete threshold"
}

// Pruner enforces the worktree retention policy.
//
// One Pruner is spun up by the sextantd supervisor and Run on a
// periodic ticker (see pkg/sextantd wiring). The Pruner is also
// invocable on demand via the `sextant worktree prune` CLI verb.
//
// Per spec:
//   - Worktrees with LastActivity older than ArchiveAge → archived
//     (moved to ArchiveRoot/<name>/, KV.Status flipped to archived).
//   - Worktrees with LastActivity older than DeleteAge → deleted
//     (git worktree remove --force + KV entry dropped).
//   - Disk-only directories (no KV entry) older than ArchiveAge are
//     deleted best-effort; younger ones are left for the operator.
//   - .merge-* transient dirs are skipped — the merge handler owns
//     their cleanup.
//
// The Pruner DOES NOT update LastActivity. That would be a cycle —
// the field is updated by the spawn flow and by worktree_merge.
type Pruner struct {
	mgr         *Manager
	archiveRoot string
	archiveAge  time.Duration
	deleteAge   time.Duration
	now         func() time.Time
	auditFn     func(PruneAudit)
}

// NewPruner validates cfg and returns a ready Pruner. Returns an error
// if Manager is nil, ArchiveRoot is empty, or DeleteAge < ArchiveAge
// (which would invert the policy).
func NewPruner(cfg PrunerConfig) (*Pruner, error) {
	if cfg.Manager == nil {
		return nil, fmt.Errorf("worktree: Pruner Manager is required")
	}
	if strings.TrimSpace(cfg.ArchiveRoot) == "" {
		return nil, fmt.Errorf("worktree: Pruner ArchiveRoot is required")
	}
	if cfg.ArchiveAge <= 0 {
		cfg.ArchiveAge = DefaultArchiveAge
	}
	if cfg.DeleteAge <= 0 {
		cfg.DeleteAge = DefaultDeleteAge
	}
	if cfg.DeleteAge < cfg.ArchiveAge {
		return nil, fmt.Errorf("worktree: Pruner DeleteAge (%s) < ArchiveAge (%s) — inverted policy",
			cfg.DeleteAge, cfg.ArchiveAge)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Pruner{
		mgr:         cfg.Manager,
		archiveRoot: cfg.ArchiveRoot,
		archiveAge:  cfg.ArchiveAge,
		deleteAge:   cfg.DeleteAge,
		now:         cfg.Now,
		auditFn:     cfg.AuditFn,
	}, nil
}

// Thresholds returns the resolved ArchiveAge + DeleteAge after
// defaults are applied. Useful for tests and for CLI output that
// wants to remind the operator what the policy is.
func (p *Pruner) Thresholds() PrunerThresholds {
	return PrunerThresholds{ArchiveAge: p.archiveAge, DeleteAge: p.deleteAge}
}

// Run executes one prune tick: scan the registry, apply the policy
// to each registered worktree, then reconcile disk-only orphans.
//
// The pruner is best-effort. A failure on one worktree adds an entry
// to report.Errors but does not abort the loop. A failure to read
// the registry (the top-level List call) does return an error
// because there's nothing to do without it.
func (p *Pruner) Run(ctx context.Context, opts PruneRunOptions) (PruneReport, error) {
	var report PruneReport

	// 1. Registered worktrees first.
	infos, err := p.mgr.List(ctx)
	if err != nil {
		return report, fmt.Errorf("worktree: pruner list: %w", err)
	}
	registered := make(map[string]struct{}, len(infos))
	now := p.now().UTC()
	for _, info := range infos {
		registered[info.Name] = struct{}{}
		idle := now.Sub(info.LastActivity)
		ageDays := int(idle / (24 * time.Hour))
		switch {
		case idle >= p.deleteAge:
			plan := PrunePlan{
				Name:   info.Name,
				Path:   info.Path,
				Action: "delete",
				Reason: fmt.Sprintf("idle %dd ≥ %s delete threshold", ageDays, humanDays(p.deleteAge)),
			}
			report.Plans = append(report.Plans, plan)
			if opts.DryRun {
				report.Deleted++
				continue
			}
			if err := p.deleteWorktree(ctx, info); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("delete %s: %v", info.Name, err))
				continue
			}
			report.Deleted++
			p.emit(PruneAudit{
				Action:       AuditActionPruned,
				Name:         info.Name,
				Path:         info.Path,
				LastActivity: info.LastActivity,
				AgeDays:      ageDays,
			})
		case idle >= p.archiveAge:
			archiveDest := filepath.Join(p.archiveRoot, info.Name)
			plan := PrunePlan{
				Name:   info.Name,
				Path:   info.Path,
				Action: "archive",
				Reason: fmt.Sprintf("idle %dd ≥ %s archive threshold", ageDays, humanDays(p.archiveAge)),
			}
			report.Plans = append(report.Plans, plan)
			if opts.DryRun {
				report.Archived++
				continue
			}
			if err := p.archiveWorktree(ctx, info, archiveDest); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("archive %s: %v", info.Name, err))
				continue
			}
			report.Archived++
			p.emit(PruneAudit{
				Action:       AuditActionArchived,
				Name:         info.Name,
				Path:         info.Path,
				ArchivePath:  archiveDest,
				LastActivity: info.LastActivity,
				AgeDays:      ageDays,
			})
		default:
			report.Skipped++
			report.Plans = append(report.Plans, PrunePlan{
				Name:   info.Name,
				Path:   info.Path,
				Action: "skip",
				Reason: fmt.Sprintf("idle %dd < %s archive threshold", ageDays, humanDays(p.archiveAge)),
			})
		}
	}

	// 2. Orphans — directories on disk with no KV entry. Best-effort.
	p.reconcileOrphans(ctx, opts, registered, now, &report)

	return report, nil
}

// deleteWorktree removes the worktree dir + the KV entry. Uses
// `git worktree remove --force` so dirty trees still go.
func (p *Pruner) deleteWorktree(ctx context.Context, info sextantproto.WorktreeInfo) error {
	// Try the git path first so the .git/worktrees/<name> bookkeeping
	// stays consistent. If git refuses (e.g. the dir was already
	// removed by hand) fall back to RemoveAll so we at least free the
	// disk + drop the KV entry.
	if err := runGit(ctx, p.mgr.cfg.RepoRoot, "worktree", "remove", "--force", info.Path); err != nil {
		if _, statErr := os.Stat(info.Path); statErr == nil {
			if rmErr := os.RemoveAll(info.Path); rmErr != nil {
				return fmt.Errorf("git worktree remove: %w; RemoveAll fallback: %w", err, rmErr)
			}
		}
	}
	// Prune the git bookkeeping to drop any stale .git/worktrees entry
	// that referenced the dir we just removed. Best-effort.
	_ = runGit(ctx, p.mgr.cfg.RepoRoot, "worktree", "prune")
	if err := p.mgr.cfg.Registry.Delete(ctx, info.Name); err != nil {
		return fmt.Errorf("registry delete: %w", err)
	}
	return nil
}

// archiveWorktree copies the worktree dir to archiveDest and removes
// the original via `git worktree remove --force`. The KV entry is
// kept with Status flipped to archived so an operator can still see
// what was archived. The merge handler's lock isn't held here —
// archive is independent of merges.
func (p *Pruner) archiveWorktree(ctx context.Context, info sextantproto.WorktreeInfo, archiveDest string) error {
	if err := os.MkdirAll(filepath.Dir(archiveDest), 0o750); err != nil {
		return fmt.Errorf("mkdir archive parent: %w", err)
	}
	// Copy the directory first so a mid-flight failure leaves the
	// original intact. After the copy succeeds, remove via git.
	if _, statErr := os.Stat(archiveDest); statErr == nil {
		// Same-name archive already exists (operator ran prune twice
		// without cleaning the archive root). Rotate the existing one
		// with a `.prev-<ts>` suffix so we don't lose data.
		stamp := p.now().UTC().Format("20060102T150405Z")
		rotated := archiveDest + ".prev-" + stamp
		if err := os.Rename(archiveDest, rotated); err != nil {
			return fmt.Errorf("rotate existing archive: %w", err)
		}
	}
	if err := copyTree(info.Path, archiveDest); err != nil {
		return fmt.Errorf("copy to archive: %w", err)
	}
	if err := runGit(ctx, p.mgr.cfg.RepoRoot, "worktree", "remove", "--force", info.Path); err != nil {
		// Source still on disk; best-effort cleanup so we don't leak.
		if _, statErr := os.Stat(info.Path); statErr == nil {
			_ = os.RemoveAll(info.Path)
		}
	}
	_ = runGit(ctx, p.mgr.cfg.RepoRoot, "worktree", "prune")
	// Flip Status → archived in KV.
	info.Status = sextantproto.WorktreeStatusArchived
	info.LastActivity = p.now().UTC()
	raw, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal info: %w", err)
	}
	if _, err := p.mgr.cfg.Registry.Put(ctx, info.Name, raw); err != nil {
		return fmt.Errorf("registry put: %w", err)
	}
	return nil
}

// reconcileOrphans walks the worktrees root for directories not in
// the registry. .merge-* transients are skipped (merge handler owns
// them). Old orphans are deleted best-effort; recent orphans are
// kept with an OrphansKept tally so the operator can investigate.
//
// "Old" here uses the same DeleteAge threshold as registered
// worktrees — we don't archive orphans because we have no record of
// their identity / branch / owner. The disk-mtime is the only
// "last activity" signal we have for them.
func (p *Pruner) reconcileOrphans(ctx context.Context, opts PruneRunOptions, registered map[string]struct{}, now time.Time, report *PruneReport) {
	entries, err := os.ReadDir(p.mgr.cfg.WorktreesRoot)
	if err != nil {
		// Worktrees root missing entirely is fine — nothing to
		// reconcile. Permission or I/O errors get appended.
		if !errors.Is(err, os.ErrNotExist) {
			report.Errors = append(report.Errors, fmt.Sprintf("readdir worktrees root: %v", err))
		}
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		// Skip merge transients — the merge handler owns them.
		if strings.HasPrefix(name, MergeWorktreePrefix) {
			continue
		}
		// Skip dotfiles other than the merge prefix (defensive — any
		// hidden dir is operator territory).
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, ok := registered[name]; ok {
			continue
		}
		// Orphan: not in registry. Decide by mtime.
		path := filepath.Join(p.mgr.cfg.WorktreesRoot, name)
		fi, statErr := os.Stat(path)
		if statErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("stat orphan %s: %v", name, statErr))
			continue
		}
		idle := now.Sub(fi.ModTime())
		ageDays := int(idle / (24 * time.Hour))
		if idle < p.archiveAge {
			report.OrphansKept++
			report.Plans = append(report.Plans, PrunePlan{
				Name:   name,
				Path:   path,
				Action: "orphan_keep",
				Reason: fmt.Sprintf("orphan idle %dd < %s archive threshold; recoverable", ageDays, humanDays(p.archiveAge)),
			})
			continue
		}
		// Old enough that the policy would delete it — but orphans
		// are off by default to protect operator-curated dirs that
		// the registry doesn't know about. Only act when the caller
		// explicitly opts in.
		if !opts.AllowOrphanDelete {
			report.Skipped++
			report.Plans = append(report.Plans, PrunePlan{
				Name:   name,
				Path:   path,
				Action: "orphan_keep",
				Reason: fmt.Sprintf("orphan idle %dd ≥ %s threshold but AllowOrphanDelete=false — keeping for operator review", ageDays, humanDays(p.archiveAge)),
			})
			continue
		}
		// Old enough to delete and operator opted in. Honor dry-run.
		plan := PrunePlan{
			Name:   name,
			Path:   path,
			Action: "orphan_delete",
			Reason: fmt.Sprintf("orphan idle %dd ≥ %s archive threshold", ageDays, humanDays(p.archiveAge)),
		}
		report.Plans = append(report.Plans, plan)
		if opts.DryRun {
			report.OrphansDeleted++
			continue
		}
		// Best-effort: try git first, fall back to RemoveAll.
		if err := runGit(ctx, p.mgr.cfg.RepoRoot, "worktree", "remove", "--force", path); err != nil {
			if _, statErr := os.Stat(path); statErr == nil {
				if rmErr := os.RemoveAll(path); rmErr != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("delete orphan %s: %v (RemoveAll: %v)", name, err, rmErr))
					continue
				}
			}
		}
		_ = runGit(ctx, p.mgr.cfg.RepoRoot, "worktree", "prune")
		report.OrphansDeleted++
		p.emit(PruneAudit{
			Action:  AuditActionOrphan,
			Name:    name,
			Path:    path,
			Mtime:   fi.ModTime(),
			AgeDays: ageDays,
		})
	}
}

// emit fires AuditFn if set. Defensive against nil so the wiring layer
// can leave it off for tests / dry-run.
func (p *Pruner) emit(a PruneAudit) {
	if p.auditFn != nil {
		p.auditFn(a)
	}
}

// humanDays renders a duration as Nd for the human-readable audit
// reason. Used in PrunePlan.Reason; keep one-source-of-truth so the
// CLI output is consistent.
func humanDays(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	if days <= 0 {
		return d.String()
	}
	return fmt.Sprintf("%dd", days)
}

// copyTree copies src into dst recursively, preserving file modes
// (we don't try to preserve mtimes — the archive copy is a snapshot
// stamp, not a forensic preservation). dst must not already exist.
//
// We intentionally don't use os.Rename because the archive root is
// often on a different filesystem from the worktrees root (the spec
// has them at ~/.local/share/sextant/worktree-archive/ and
// ~/dev/sextant-worktrees/ respectively, which may be on different
// volumes / mounts).
func copyTree(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("copyTree: source %s is not a directory", src)
	}
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // pruner-controlled path
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // best-effort close
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec // pruner-controlled path
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

