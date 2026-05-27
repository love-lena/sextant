// templates.go owns `sextant templates <verb>` — manage agent
// templates. M16 ships one verb: `reload`.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/sextantd"
)

func newTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "Manage agent templates",
	}
	cmd.AddCommand(newTemplatesReloadCmd())
	return cmd
}

func newTemplatesReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Re-scan the templates dir and push every *.toml into NATS KV",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
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
			out := cmd.OutOrStdout()
			if resp.Error != "" {
				if globalFlags.asJSON {
					return writeJSON(cmd, out, resp)
				}
				return fmt.Errorf("daemon: %s", resp.Error)
			}
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			_, err = fmt.Fprintf(out, "synced %d template(s)\n", resp.Count)
			return err
		},
	}
}
