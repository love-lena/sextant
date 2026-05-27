// daemon.go owns the `sextant daemon` parent command. Wraps start /
// stop / restart / status / logs — verbs that used to live at top
// level. Migration per `plans/issues/feat-cli-resource-verb-cleanup.md`.
//
// The heavy-lifting helpers (`doStart`, `doStop`, ...) stay in
// start.go / stop.go / restart.go / status.go / logs.go so the existing
// tests don't move.
package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// newDaemonCmd builds the `sextant daemon` resource noun and its verbs.
func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the sextantd daemon process",
		Long: `Bring the daemon up, take it down, inspect its status, follow its log
file. Replaces the legacy top-level start/stop/restart/status/logs verbs
(those still work as aliases for one minor release).`,
	}
	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonRestartCmd())
	cmd.AddCommand(newDaemonStatusCmd())
	cmd.AddCommand(newDaemonLogsCmd())
	return cmd
}

// newDaemonStartCmd wires `sextant daemon start`. Detaches a fresh
// sextantd process, pipes its stdout/stderr into the canonical log
// file, and waits up to --timeout for runtime.json to appear with a
// live PID.
func newDaemonStartCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Detach sextantd and wait for runtime.json to appear",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadDaemonConfig(globalFlags.configDir, globalFlags.dataDir)
			if err != nil {
				return err
			}
			return doStart(cmd.OutOrStdout(), cfg, timeout)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second,
		"max wait for runtime.json to appear")
	return cmd
}

// newDaemonStopCmd wires `sextant daemon stop`.
func newDaemonStopCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "SIGTERM the daemon and wait for graceful shutdown",
		Args:  cobra.NoArgs,
	}
	destructive := newDestructiveFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		proceed, err := destructive.confirm(cmd, "stop the running sextant daemon")
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}
		cfg, err := loadDaemonConfig(globalFlags.configDir, globalFlags.dataDir)
		if err != nil {
			return err
		}
		return doStop(cmd.OutOrStdout(), cfg, timeout)
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second,
		"max wait for runtime.json to disappear")
	return cmd
}

// newDaemonRestartCmd wires `sextant daemon restart`.
func newDaemonRestartCmd() *cobra.Command {
	var stopTimeout, startTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Stop then start (with transition prints)",
		Args:  cobra.NoArgs,
	}
	destructive := newDestructiveFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		proceed, err := destructive.confirm(cmd, "restart the running sextant daemon (brief downtime)")
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}
		cfg, err := loadDaemonConfig(globalFlags.configDir, globalFlags.dataDir)
		if err != nil {
			return err
		}
		return doRestart(cmd.OutOrStdout(), cfg, stopTimeout, startTimeout)
	}
	cmd.Flags().DurationVar(&stopTimeout, "stop-timeout", 30*time.Second,
		"max wait for graceful shutdown")
	cmd.Flags().DurationVar(&startTimeout, "start-timeout", 30*time.Second,
		"max wait for runtime.json to reappear")
	return cmd
}

// newDaemonStatusCmd wires `sextant daemon status`.
func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print daemon liveness + subprocess pids/addrs",
		Long: `Reads runtime.json, probes the recorded PID with signal 0, prints a
table. Exit 0 if alive, 1 if not running or stale.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadDaemonConfig(globalFlags.configDir, globalFlags.dataDir)
			if err != nil {
				return err
			}
			return doStatus(cmd.OutOrStdout(), cfg, globalFlags.asJSON)
		},
	}
}

// newDaemonLogsCmd wires `sextant daemon logs`.
func newDaemonLogsCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Print or follow the daemon log file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tail < 0 {
				return errUserUsage("--tail must be >= 0")
			}
			cfg, err := loadDaemonConfig(globalFlags.configDir, globalFlags.dataDir)
			if err != nil {
				return err
			}
			logPath := resolveLogPath(cfg)
			return doLogs(cmd.Context(), cmd.OutOrStdout(), logPath, tail, follow)
		},
	}
	cmd.Flags().BoolVar(&follow, "follow", false,
		"stream new bytes (like tail -f) until cancelled")
	cmd.Flags().IntVar(&tail, "tail", 50,
		"number of trailing lines to print before following")
	return cmd
}

// newDaemonAliasCmds returns one alias command for each legacy top-level
// daemon verb (`sextant start`, `stop`, `restart`, `status`, `logs`).
// Each prints a stderr deprecation note (suppressed under --json) then
// delegates to the corresponding daemon subcommand.
//
// Cobra docs commonly use Aliases on a single command for this; here
// we want top-level visibility AND to point at a nested command, so a
// thin shadowing-cmd is the cleanest pattern.
func newDaemonAliasCmds() []*cobra.Command {
	type alias struct {
		oldName    string
		newForm    string
		short      string
		build      func() *cobra.Command
	}
	aliases := []alias{
		{"start", "sextant daemon start", "(deprecated) alias for `sextant daemon start`", newDaemonStartCmd},
		{"stop", "sextant daemon stop", "(deprecated) alias for `sextant daemon stop`", newDaemonStopCmd},
		{"restart", "sextant daemon restart", "(deprecated) alias for `sextant daemon restart`", newDaemonRestartCmd},
		{"status", "sextant daemon status", "(deprecated) alias for `sextant daemon status`", newDaemonStatusCmd},
		{"logs", "sextant daemon logs", "(deprecated) alias for `sextant daemon logs`", newDaemonLogsCmd},
	}
	out := make([]*cobra.Command, 0, len(aliases))
	for _, a := range aliases {
		c := a.build()
		c.Use = a.oldName
		c.Short = a.short
		c.Hidden = true
		c.Deprecated = fmt.Sprintf("use %q instead", a.newForm)
		oldName, newForm := a.oldName, a.newForm
		origRunE := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			deprecationNote(cmd, "sextant "+oldName, newForm)
			return origRunE(cmd, args)
		}
		out = append(out, c)
	}
	return out
}
