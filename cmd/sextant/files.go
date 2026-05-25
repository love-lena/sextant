package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

const filesUsage = `usage: sextant files <verb> <agent_uuid> <path> [args...]

Verbs:
  read <agent> <path>            Read a file from the agent's container.
  ls <agent> <path>              List a directory in the agent's container.
  tail <agent> <path> [--interval 500ms]
                                 Poll the file for new content (M12 ships
                                 a poll loop; streaming RPC is deferred).

Every verb supports --json.`

func runFiles(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, filesUsage)
		return errUserUsage("missing files verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "read":
		return runFilesRead(ctx, rest)
	case "ls":
		return runFilesLs(ctx, rest)
	case "tail":
		return runFilesTail(ctx, rest)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, filesUsage)
		return nil
	default:
		_, _ = fmt.Fprintln(os.Stderr, filesUsage)
		return errUserUsage(fmt.Sprintf("unknown files verb %q", verb))
	}
}

func parseFilesArgs(label string, fs *flag.FlagSet, args []string) (commonOpts, uuid.UUID, string, error) {
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return commonOpts{}, uuid.Nil, "", err
	}
	if len(rest) != 2 {
		return commonOpts{}, uuid.Nil, "", errUserUsage(fmt.Sprintf("%s <agent_uuid> <path>", label))
	}
	id, err := uuid.Parse(rest[0])
	if err != nil {
		return commonOpts{}, uuid.Nil, "", errUserUsage(fmt.Sprintf("agent_uuid: %v", err))
	}
	if rest[1] == "" {
		return commonOpts{}, uuid.Nil, "", errUserUsage("path must be non-empty")
	}
	return opts, id, rest[1], nil
}

func runFilesRead(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant files read", flag.ContinueOnError)
	opts, id, path, err := parseFilesArgs("sextant files read", fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
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
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	_, err = os.Stdout.Write(resp.Content)
	return err
}

func runFilesLs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant files ls", flag.ContinueOnError)
	opts, id, path, err := parseFilesArgs("sextant files ls", fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
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
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, e := range resp.Entries {
		marker := " "
		if e.IsDir {
			marker = "/"
		}
		printf(tw, "%s%s\n", e.Name, marker)
	}
	return tw.Flush()
}

// runFilesTail polls the file's content until ctx is canceled. We
// poll because the M12 wire doesn't ship a streaming `read_file`
// implementation yet (see specs/protocols/rpc-catalog.md "Open").
func runFilesTail(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant files tail", flag.ContinueOnError)
	var interval time.Duration
	fs.DurationVar(&interval, "interval", 500*time.Millisecond, "poll interval (M12 stop-gap; streaming RPC TBD)")
	opts, id, path, err := parseFilesArgs("sextant files tail", fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	return tailFile(ctx, os.Stdout, cli, id, path, interval, opts.asJSON)
}

// tailFile is the testable core of `files tail`. It tracks the last
// bytes printed and only forwards the suffix on each poll.
func tailFile(ctx context.Context, w io.Writer, cli *client.Client, id uuid.UUID, path string, interval time.Duration, asJSON bool) error {
	var seen []byte
	tick := time.NewTicker(interval)
	defer tick.Stop()
	// One immediate read so the operator gets the current content on
	// connect rather than waiting an interval for the first tick.
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
	// Only forward the suffix that's new since last poll.
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
		// File was truncated or rotated — emit a marker and reset.
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
