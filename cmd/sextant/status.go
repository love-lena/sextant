package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// runStatus implements `sextant status`. It surfaces the daemon's
// runtime.json contents in a human-readable table (or JSON via --json)
// and uses exit codes to signal liveness so it composes well with
// shell scripts and supervisors:
//
//	0 — running and PID is alive
//	1 — no runtime.json OR runtime.json points at a dead PID
//
// Plan: plans/issues/feat-daemon-lifecycle-ergonomics.md (fix #3).
func runStatus(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "config directory (default ~/.config/sextant)")
	dataDir := fs.String("data-dir", "", "data directory (default ~/.local/share/sextant)")
	asJSON := fs.Bool("json", false, "emit machine-parseable JSON")
	help := fs.Bool("help", false, "print help")
	if err := fs.Parse(args); err != nil {
		return errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	if *help {
		fmt.Println(statusUsage)
		return nil
	}

	cfg, err := loadDaemonConfig(*configDir, *dataDir)
	if err != nil {
		return err
	}
	return doStatus(os.Stdout, cfg, *asJSON)
}

const statusUsage = `usage: sextant status [--config-dir DIR] [--data-dir DIR] [--json]

Reads runtime.json, probes the recorded PID with signal 0, and prints a
table. Exit 0 if alive, 1 if not running or stale.`

// statusRow is the JSON shape returned by --json. Mirrors the human
// table 1:1 so an operator who's debugged the text output can pivot to
// scripting without re-learning the field names.
type statusRow struct {
	Daemon      daemonStatusJSON `json:"daemon"`
	NATS        subprocessJSON   `json:"nats"`
	ClickHouse  subprocessJSON   `json:"clickhouse"`
	MCP         mcpStatusJSON    `json:"mcp"`
	Log         string           `json:"log"`
	RuntimeFile string           `json:"runtime_file"`
	State       string           `json:"state"`
	StateReason string           `json:"state_reason,omitempty"`
}

type daemonStatusJSON struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Uptime    string    `json:"uptime"`
	Version   string    `json:"version,omitempty"`
}

type subprocessJSON struct {
	PID  int    `json:"pid"`
	Addr string `json:"addr,omitempty"`
}

type mcpStatusJSON struct {
	Addr  string `json:"addr,omitempty"`
	Stdio string `json:"stdio,omitempty"`
}

// doStatus is the testable body of `sextant status`. Returns
// errStatusNotRunning when the daemon is not up so the dispatcher can
// translate to exit code 1.
func doStatus(w io.Writer, cfg sextantd.Config, asJSON bool) error {
	logPath := daemonLogPath(cfg.Paths.DataDir, sextantd.RuntimeInfo{})
	st, err := readDaemonState(cfg.Paths.RuntimeFile)
	switch {
	case errors.Is(err, errDaemonNotRunning):
		if asJSON {
			row := statusRow{
				RuntimeFile: cfg.Paths.RuntimeFile, Log: logPath,
				State: "not_running", StateReason: "runtime.json absent",
			}
			emitStatusJSON(w, row)
		} else {
			printf(w, "daemon: not running\n")
		}
		return errStatusNotRunning
	case err != nil:
		return fmt.Errorf("read runtime.json: %w", err)
	}
	if !st.Alive {
		if asJSON {
			row := statusRow{
				Daemon: daemonStatusJSON{
					PID: st.Info.PID, StartedAt: st.Info.StartedAt, Version: st.Info.Version,
				},
				RuntimeFile: cfg.Paths.RuntimeFile, Log: logPath,
				State:       "stale",
				StateReason: fmt.Sprintf("pid %d not running", st.Info.PID),
			}
			emitStatusJSON(w, row)
		} else {
			printf(w, "daemon: stale runtime.json (pid %d not running)\n", st.Info.PID)
		}
		return errStatusNotRunning
	}

	// Update log path with whatever runtime.json may have advertised.
	logPath = daemonLogPath(cfg.Paths.DataDir, st.Info)
	uptime := time.Since(st.Info.StartedAt).Round(time.Second)
	if asJSON {
		row := statusRow{
			Daemon: daemonStatusJSON{
				PID: st.Info.PID, StartedAt: st.Info.StartedAt,
				Uptime: uptime.String(), Version: st.Info.Version,
			},
			NATS:        subprocessJSON{PID: st.Info.NATSPID, Addr: st.Info.NATSAddr},
			ClickHouse:  subprocessJSON{PID: st.Info.ClickHousePID, Addr: st.Info.ClickHouseTCP},
			MCP:         mcpStatusJSON{Addr: st.Info.MCPHTTPAddr, Stdio: st.Info.MCPStdioSocket},
			Log:         logPath,
			RuntimeFile: cfg.Paths.RuntimeFile,
			State:       "running",
		}
		emitStatusJSON(w, row)
		return nil
	}
	printf(w, "daemon          pid %d, uptime %s\n", st.Info.PID, uptime)
	printf(w, "nats            pid %d, addr %s\n", st.Info.NATSPID, defaultStr(st.Info.NATSAddr, "(unset)"))
	printf(w, "clickhouse      pid %d, tcp %s\n", st.Info.ClickHousePID, defaultStr(st.Info.ClickHouseTCP, "(unset)"))
	printf(w, "mcp             addr %s, stdio %s\n",
		defaultStr(st.Info.MCPHTTPAddr, "(unset)"),
		defaultStr(st.Info.MCPStdioSocket, "(unset)"),
	)
	printf(w, "log             %s\n", logPath)
	return nil
}

// errStatusNotRunning is the sentinel that tells main() to translate
// the result into exit code 1. Distinct from doctor's failure so we can
// keep the exit semantics separable.
var errStatusNotRunning = errors.New("daemon not running")

func isStatusNotRunningErr(err error) bool { return errors.Is(err, errStatusNotRunning) }

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func emitStatusJSON(w io.Writer, row statusRow) {
	raw, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		// MarshalIndent can only fail on a programming-level type error
		// here; nothing meaningful to recover from.
		printf(w, "{\"error\":\"marshal: %v\"}\n", err)
		return
	}
	println(w, string(raw))
}
