package sextantd

import (
	"time"
)

// ControlWorktreePruneSubject is the NATS subject the daemon
// subscribes to in order to receive `sextant worktree prune` requests.
// The operator CLI publishes one request with a NATS reply inbox; the
// daemon answers with a JSON-encoded WorktreePruneResponse.
//
// Lightweight semantics — same shape as templates_reload: native
// request/reply, no idempotency cache, no envelope wrapping. The
// pruner already emits its own audit envelopes for the archive /
// delete actions it takes, so a wrap on top would double-count.
//
// See plans/issues/feat-worktree-pruner.md.
const ControlWorktreePruneSubject = "sextant.control.worktree_prune"

// WorktreePruneRequest is the inbound payload. DryRun=true reports
// what would happen without performing any disk or KV mutation.
// AllowOrphanDelete lets the caller opt into deleting on-disk
// directories that have no KV entry; false (default) skips them
// even when old enough to qualify for archive — so an operator
// can't accidentally nuke directories the daemon doesn't know
// about.
type WorktreePruneRequest struct {
	DryRun            bool `json:"dry_run,omitempty"`
	AllowOrphanDelete bool `json:"allow_orphan_delete,omitempty"`
}

// WorktreePruneResponse is the outbound payload. Mirrors
// worktree.PruneReport but renders the times the CLI cares about as
// JSON-friendly types.
type WorktreePruneResponse struct {
	Archived       int                `json:"archived"`
	Deleted        int                `json:"deleted"`
	Skipped        int                `json:"skipped"`
	OrphansDeleted int                `json:"orphans_deleted"`
	OrphansKept    int                `json:"orphans_kept"`
	Plans          []WorktreePrunePlan `json:"plans,omitempty"`
	Errors         []string           `json:"errors,omitempty"`
	DryRun         bool               `json:"dry_run,omitempty"`
	Error          string             `json:"error,omitempty"`
	// ArchiveAge and DeleteAge echo the resolved policy thresholds so
	// the CLI can print "would archive >14d, delete >30d" alongside
	// the per-worktree decisions.
	ArchiveAge time.Duration `json:"archive_age,omitempty"`
	DeleteAge  time.Duration `json:"delete_age,omitempty"`
}

// WorktreePrunePlan is one decision the pruner made (or would make on
// dry-run).
type WorktreePrunePlan struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}
