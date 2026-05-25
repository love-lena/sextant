package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

const worktreeUsage = `usage: sextant worktree <verb> [args...]

Verbs:
  list                                   List every worktree in the registry.
  create <name> [--base main]            Create a new worktree on a fresh branch.
  destroy <name> [--force]               Remove a worktree's dir + registry entry.
  merge <name> [--target main]           Merge a worktree's branch into target.
  diff <name> [--against main]           Show the diff against a target branch.
  prune [--apply] [--orphan-delete]      Enforce the idle-worktree policy now:
                                         archive >14d, delete >30d. Defaults to
                                         dry-run; pass --apply to act. Pass
                                         --orphan-delete to also remove on-disk
                                         dirs without a registry entry.

Every verb supports --json for machine-parseable output. Use
--config-dir to point at a non-default sextant install.

Worktree names must match the <kind>-<short-description>-<seq> rule
from conventions/git-workflow.md (kind ∈ feat|fix|refactor|docs|
test|chore|spec, seq=NNN).`

func runWorktree(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, worktreeUsage)
		return errUserUsage("missing worktree verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		return runWorktreeList(ctx, rest)
	case "create":
		return runWorktreeCreate(ctx, rest)
	case "destroy":
		return runWorktreeDestroy(ctx, rest)
	case "merge":
		return runWorktreeMerge(ctx, rest)
	case "diff":
		return runWorktreeDiff(ctx, rest)
	case "prune":
		return runWorktreePrune(ctx, rest)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, worktreeUsage)
		return nil
	default:
		_, _ = fmt.Fprintln(os.Stderr, worktreeUsage)
		return errUserUsage(fmt.Sprintf("unknown worktree verb %q", verb))
	}
}

// runWorktreeList — `sextant worktree list`.
func runWorktreeList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant worktree list", flag.ContinueOnError)
	opts, _, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	var resp sextantproto.WorktreeListResponse
	if err := cli.RPC(ctx, rpc.VerbWorktreeList, sextantproto.WorktreeListRequest{}, &resp); err != nil {
		return fmt.Errorf("worktree_list: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if len(resp.Worktrees) == 0 {
		println(os.Stdout, "no worktrees")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	printf(tw, "NAME\tBRANCH\tBASE\tSTATUS\tCREATED\tPATH\n")
	for _, w := range resp.Worktrees {
		printf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			w.Name, w.Branch, w.BaseBranch, w.Status,
			w.CreatedAt.Format(time.RFC3339), w.Path)
	}
	return tw.Flush()
}

// runWorktreeCreate — `sextant worktree create <name> [--base main]`.
func runWorktreeCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant worktree create", flag.ContinueOnError)
	var base string
	fs.StringVar(&base, "base", "main", "base branch to fork from")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return errUserUsage("sextant worktree create <name> [--base main]")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	req := sextantproto.WorktreeCreateRequest{Name: rest[0], BaseBranch: base}
	var resp sextantproto.WorktreeCreateResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbWorktreeCreate, req, &resp); err != nil {
		return fmt.Errorf("worktree_create: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	printf(os.Stdout, "name:   %s\n", resp.Worktree.Name)
	printf(os.Stdout, "path:   %s\n", resp.Worktree.Path)
	printf(os.Stdout, "branch: %s\n", resp.Worktree.Branch)
	return nil
}

// runWorktreeDestroy — `sextant worktree destroy <name> [--force]`.
func runWorktreeDestroy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant worktree destroy", flag.ContinueOnError)
	var force bool
	fs.BoolVar(&force, "force", false, "destroy even when status != archived/merged")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant worktree destroy <name> [--force]")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	req := sextantproto.WorktreeDestroyRequest{Name: rest[0], Force: force}
	var resp sextantproto.WorktreeDestroyResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbWorktreeDestroy, req, &resp); err != nil {
		return fmt.Errorf("worktree_destroy: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if resp.OK {
		println(os.Stdout, "ok")
	} else {
		println(os.Stdout, "not ok")
	}
	return nil
}

// runWorktreeMerge — `sextant worktree merge <name> [--target main]`.
func runWorktreeMerge(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant worktree merge", flag.ContinueOnError)
	var target string
	fs.StringVar(&target, "target", "main", "target branch")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant worktree merge <name> [--target main]")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	req := sextantproto.WorktreeMergeRequest{Name: rest[0], Target: target}
	var resp sextantproto.WorktreeMergeResponse
	// Merge can take a while on a cold repo; bump the timeout.
	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbWorktreeMerge, req, &resp); err != nil {
		return fmt.Errorf("worktree_merge: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if resp.OK {
		printf(os.Stdout, "merged %s into %s\n", resp.Branch, resp.Target)
		return nil
	}
	printf(os.Stdout, "merge conflict (%s into %s):\n", resp.Branch, resp.Target)
	for _, f := range resp.Conflicts {
		printf(os.Stdout, "  %s\n", f)
	}
	return errUserUsage("merge conflict")
}

// runWorktreePrune — `sextant worktree prune [--dry-run]`.
//
// Publishes on sextant.control.worktree_prune and waits for the
// daemon's reply. The wire shape mirrors `sextant templates reload`:
// native NATS request/reply, no full RPC envelope.
//
// In dry-run mode the daemon's pruner returns the per-worktree plans
// without performing any disk or KV mutation; this verb formats them
// as a human-readable table.
func runWorktreePrune(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant worktree prune", flag.ContinueOnError)
	// Defaults to dry-run. The operator opts into real action with
	// --apply; this is deliberately the inverse of the spec's
	// original --dry-run shape because the blast radius of an
	// accidental sweep is high. Orphan deletion is doubly opt-in.
	var apply bool
	var orphanDelete bool
	fs.BoolVar(&apply, "apply", false, "perform the planned actions (default is dry-run)")
	fs.BoolVar(&orphanDelete, "orphan-delete", false, "also delete on-disk dirs that aren't in the worktrees registry (requires --apply to take effect)")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errUserUsage("sextant worktree prune [--apply] [--orphan-delete]")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	dryRun := !apply
	reqRaw, err := json.Marshal(sextantd.WorktreePruneRequest{
		DryRun:            dryRun,
		AllowOrphanDelete: orphanDelete,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	msg, err := cli.Conn().RequestWithContext(reqCtx, sextantd.ControlWorktreePruneSubject, reqRaw)
	if err != nil {
		return fmt.Errorf("worktree_prune: %w (is sextantd running?)", err)
	}
	var resp sextantd.WorktreePruneResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if resp.Error != "" {
		return fmt.Errorf("daemon: %s", resp.Error)
	}
	mode := "performed"
	if resp.DryRun {
		mode = "dry-run"
	}
	printf(os.Stdout, "worktree prune (%s)\n", mode)
	printf(os.Stdout, "  policy: archive ≥%s, delete ≥%s\n",
		formatDays(resp.ArchiveAge), formatDays(resp.DeleteAge))
	printf(os.Stdout, "  archived=%d deleted=%d skipped=%d orphans_deleted=%d orphans_kept=%d errors=%d\n",
		resp.Archived, resp.Deleted, resp.Skipped, resp.OrphansDeleted, resp.OrphansKept, len(resp.Errors))
	if len(resp.Plans) > 0 {
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		printf(tw, "ACTION\tNAME\tREASON\n")
		for _, p := range resp.Plans {
			printf(tw, "%s\t%s\t%s\n", p.Action, p.Name, p.Reason)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	for _, e := range resp.Errors {
		printf(os.Stderr, "error: %s\n", e)
	}
	return nil
}

// formatDays renders the duration as "Nd" when it's a whole-day
// multiple, otherwise falls back to the stdlib string form. Used by
// the prune CLI so "14d / 30d" don't show as "336h0m0s".
func formatDays(d time.Duration) string {
	if d <= 0 {
		return d.String()
	}
	days := int(d / (24 * time.Hour))
	rem := d - time.Duration(days)*24*time.Hour
	if days > 0 && rem == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return d.String()
}

// runWorktreeDiff — `sextant worktree diff <name> [--against main]`.
func runWorktreeDiff(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant worktree diff", flag.ContinueOnError)
	var against string
	fs.StringVar(&against, "against", "main", "branch to diff against")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant worktree diff <name> [--against main]")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	req := sextantproto.WorktreeDiffRequest{Name: rest[0], Against: against}
	var resp sextantproto.WorktreeDiffResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbWorktreeDiff, req, &resp); err != nil {
		return fmt.Errorf("worktree_diff: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	_, _ = io.WriteString(os.Stdout, resp.Diff)
	if !strings.HasSuffix(resp.Diff, "\n") {
		_, _ = io.WriteString(os.Stdout, "\n")
	}
	return nil
}
