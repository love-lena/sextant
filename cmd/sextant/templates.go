package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

const templatesUsage = `usage: sextant templates <verb> [args...]

Verbs:
  reload        Re-scan the templates dir and push every *.toml into
                NATS KV without restarting sextantd.

Every verb supports --json for machine-parseable output. Use
--config-dir to point at a non-default sextant install.`

// runTemplates dispatches the templates subcommand. M16 ships one verb:
// `reload`. Adding `list` / `show` later is mechanical — they read the
// templates KV bucket via pkg/client.
func runTemplates(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, templatesUsage)
		return errUserUsage("missing templates verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "reload":
		return runTemplatesReload(ctx, rest)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, templatesUsage)
		return nil
	default:
		_, _ = fmt.Fprintln(os.Stderr, templatesUsage)
		return errUserUsage(fmt.Sprintf("unknown templates verb %q", verb))
	}
}

// runTemplatesReload publishes a request on
// sextant.control.templates_reload and awaits the daemon's reply. The
// daemon answers with `{count: N}` on success or `{error: "..."}` on
// failure. Mirrors the failure paths the spawn handler sees so the
// operator gets a useful message even when the templates dir is broken.
func runTemplatesReload(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant templates reload", flag.ContinueOnError)
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errUserUsage("sextant templates reload takes no positional args")
	}

	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	reqRaw, err := json.Marshal(sextantd.TemplatesReloadRequest{})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := cli.Conn().RequestWithContext(reqCtx, sextantd.ControlTemplatesReloadSubject, reqRaw)
	if err != nil {
		return fmt.Errorf("templates_reload: %w (is sextantd running?)", err)
	}

	var resp sextantd.TemplatesReloadResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		// Print to stderr in text mode so a shell pipeline that depends
		// on the count line doesn't see the error string mixed in.
		if opts.asJSON {
			return writeJSON(os.Stdout, resp)
		}
		return fmt.Errorf("daemon: %s", resp.Error)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	printf(os.Stdout, "synced %d template(s)\n", resp.Count)
	return nil
}

// Silence "imported and not used" if io drops on a future refactor.
var _ = io.Discard
