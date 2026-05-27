// files.go owns `sextant files <verb>` — read/ls/tail files in an
// agent's container.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// newFilesCmd builds the `sextant files` parent command.
func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Read/list/tail files in an agent's container",
	}
	cmd.AddCommand(newFilesReadCmd())
	cmd.AddCommand(newFilesLsCmd())
	cmd.AddCommand(newFilesTailCmd())
	return cmd
}

// parseFilesPositional validates the two-arg <agent_uuid> <path> shape.
func parseFilesPositional(label string, args []string) (uuid.UUID, string, error) {
	if len(args) != 2 {
		return uuid.Nil, "", errUserUsage(fmt.Sprintf("%s <agent_uuid> <path>", label))
	}
	id, err := uuid.Parse(args[0])
	if err != nil {
		return uuid.Nil, "", errUserUsage(fmt.Sprintf("agent_uuid: %v", err))
	}
	if args[1] == "" {
		return uuid.Nil, "", errUserUsage("path must be non-empty")
	}
	return id, args[1], nil
}

func newFilesReadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read <agent_uuid> <path>",
		Short: "Read a file from the agent's container",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, path, err := parseFilesPositional("sextant files read", args)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			var resp sextantproto.ReadFileResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbReadFile,
				sextantproto.ReadFileRequest{AgentID: id, Path: path}, &resp); err != nil {
				return fmt.Errorf("read_file: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(out, resp)
			}
			_, err = out.Write(resp.Content)
			return err
		},
	}
}

func newFilesLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <agent_uuid> <path>",
		Short: "List a directory in the agent's container",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, path, err := parseFilesPositional("sextant files ls", args)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			var resp sextantproto.ListDirResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbListDir,
				sextantproto.ListDirRequest{AgentID: id, Path: path}, &resp); err != nil {
				return fmt.Errorf("list_dir: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(out, resp)
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			for _, e := range resp.Entries {
				marker := " "
				if e.IsDir {
					marker = "/"
				}
				printf(tw, "%s%s\n", e.Name, marker)
			}
			return tw.Flush()
		},
	}
}

func newFilesTailCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "tail <agent_uuid> <path>",
		Short: "Poll the file for new content (M12 stop-gap)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, path, err := parseFilesPositional("sextant files tail", args)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close
			return tailFile(ctx, cmd.OutOrStdout(), cli, id, path, interval, globalFlags.asJSON)
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 500*time.Millisecond,
		"poll interval (M12 stop-gap; streaming RPC TBD)")
	return cmd
}

// tailFile is the testable core of `files tail`.
func tailFile(ctx context.Context, w io.Writer, cli *client.Client, id uuid.UUID, path string, interval time.Duration, asJSON bool) error {
	var seen []byte
	tick := time.NewTicker(interval)
	defer tick.Stop()
	if err := tailOnce(ctx, w, cli, id, path, &seen, asJSON); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := tailOnce(ctx, w, cli, id, path, &seen, asJSON); err != nil {
				return err
			}
		}
	}
}

func tailOnce(ctx context.Context, w io.Writer, cli *client.Client, id uuid.UUID, path string, seen *[]byte, asJSON bool) error {
	var resp sextantproto.ReadFileResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbReadFile,
		sextantproto.ReadFileRequest{AgentID: id, Path: path}, &resp); err != nil {
		return fmt.Errorf("read_file: %w", err)
	}
	cur := resp.Content
	switch {
	case bytes.HasPrefix(cur, *seen):
		newBytes := cur[len(*seen):]
		if len(newBytes) > 0 {
			if asJSON {
				if _, err := fmt.Fprintln(w, string(newBytes)); err != nil {
					return err
				}
			} else {
				if _, err := w.Write(newBytes); err != nil {
					return err
				}
			}
		}
		*seen = append((*seen)[:0], cur...)
	default:
		if !asJSON {
			printf(w, "[file truncated, resetting]\n")
		}
		if _, err := w.Write(cur); err != nil {
			return err
		}
		*seen = append((*seen)[:0], cur...)
	}
	return nil
}

// keep os used import in case test code referenced it transitively.
var _ = os.Stdout
