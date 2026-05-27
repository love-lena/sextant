// worktree.go owns `sextant worktree <verb>` — manage agent worktrees.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage agent worktrees (list|create|destroy|merge|diff|prune)",
	}
	cmd.AddCommand(newWorktreeListCmd())
	cmd.AddCommand(newWorktreeCreateCmd())
	cmd.AddCommand(newWorktreeDestroyCmd())
	cmd.AddCommand(newWorktreeMergeCmd())
	cmd.AddCommand(newWorktreeDiffCmd())
	cmd.AddCommand(newWorktreePruneCmd())
	return cmd
}

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every worktree in the registry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			var resp sextantproto.WorktreeListResponse
			if err := cli.RPC(ctx, rpc.VerbWorktreeList, sextantproto.WorktreeListRequest{}, &resp); err != nil {
				return fmt.Errorf("worktree_list: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			if len(resp.Worktrees) == 0 {
				_, err := fmt.Fprintln(out, "no worktrees")
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			println(tw, "NAME\tBRANCH\tBASE\tSTATUS\tCREATED\tPATH")
			for _, w := range resp.Worktrees {
				printf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					w.Name, w.Branch, w.BaseBranch, w.Status,
					w.CreatedAt.Format(time.RFC3339), w.Path)
			}
			return tw.Flush()
		},
	}
}

func newWorktreeCreateCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new worktree on a fresh branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return errUserUsage("sextant worktree create <name> [--base main]")
			}
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			req := sextantproto.WorktreeCreateRequest{Name: args[0], BaseBranch: base}
			var resp sextantproto.WorktreeCreateResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbWorktreeCreate, req, &resp); err != nil {
				return fmt.Errorf("worktree_create: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			printf(out, "name:   %s\n", resp.Worktree.Name)
			printf(out, "path:   %s\n", resp.Worktree.Path)
			printf(out, "branch: %s\n", resp.Worktree.Branch)
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "main", "base branch to fork from")
	return cmd
}

func newWorktreeDestroyCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Remove a worktree's dir + registry entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			req := sextantproto.WorktreeDestroyRequest{Name: args[0], Force: force}
			var resp sextantproto.WorktreeDestroyResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbWorktreeDestroy, req, &resp); err != nil {
				return fmt.Errorf("worktree_destroy: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			if resp.OK {
				_, err = fmt.Fprintln(out, "ok")
			} else {
				_, err = fmt.Fprintln(out, "not ok")
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "destroy even when status != archived/merged")
	return cmd
}

func newWorktreeMergeCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "merge <name>",
		Short: "Merge a worktree's branch into target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			req := sextantproto.WorktreeMergeRequest{Name: args[0], Target: target}
			var resp sextantproto.WorktreeMergeResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbWorktreeMerge, req, &resp); err != nil {
				return fmt.Errorf("worktree_merge: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			if resp.OK {
				printf(out, "merged %s into %s\n", resp.Branch, resp.Target)
				return nil
			}
			printf(out, "merge conflict (%s into %s):\n", resp.Branch, resp.Target)
			for _, f := range resp.Conflicts {
				printf(out, "  %s\n", f)
			}
			return errUserUsage("merge conflict")
		},
	}
	cmd.Flags().StringVar(&target, "target", "main", "target branch")
	return cmd
}

func newWorktreeDiffCmd() *cobra.Command {
	var against string
	cmd := &cobra.Command{
		Use:   "diff <name>",
		Short: "Show the diff against a target branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			req := sextantproto.WorktreeDiffRequest{Name: args[0], Against: against}
			var resp sextantproto.WorktreeDiffResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbWorktreeDiff, req, &resp); err != nil {
				return fmt.Errorf("worktree_diff: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			_, _ = io.WriteString(out, resp.Diff)
			if !strings.HasSuffix(resp.Diff, "\n") {
				_, _ = io.WriteString(out, "\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&against, "against", "main", "branch to diff against")
	return cmd
}

func newWorktreePruneCmd() *cobra.Command {
	var apply bool
	var orphanDelete bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Enforce the idle-worktree policy (defaults to dry-run)",
		Long: `Defaults to dry-run; pass --apply to act. Pass --orphan-delete to
also remove on-disk dirs without a registry entry (requires --apply).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
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
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			if resp.Error != "" {
				return fmt.Errorf("daemon: %s", resp.Error)
			}
			mode := "performed"
			if resp.DryRun {
				mode = "dry-run"
			}
			printf(out, "worktree prune (%s)\n", mode)
			printf(out, "  policy: archive ≥%s, delete ≥%s\n",
				formatDays(resp.ArchiveAge), formatDays(resp.DeleteAge))
			printf(out, "  archived=%d deleted=%d skipped=%d orphans_deleted=%d orphans_kept=%d errors=%d\n",
				resp.Archived, resp.Deleted, resp.Skipped, resp.OrphansDeleted, resp.OrphansKept, len(resp.Errors))
			if len(resp.Plans) > 0 {
				tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
				println(tw, "ACTION\tNAME\tREASON")
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
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "perform the planned actions (default is dry-run)")
	cmd.Flags().BoolVar(&orphanDelete, "orphan-delete", false,
		"also delete on-disk dirs that aren't in the worktrees registry (requires --apply)")
	return cmd
}

// formatDays renders a duration as "Nd" when it's a whole-day multiple.
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
