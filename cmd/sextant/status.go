// status.go owns `doStatus` — the testable body of `sextant daemon
// status`. The cobra wiring lives in daemon.go.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// statusRow is the JSON shape returned by --json. Mirrors the human
// table 1:1.
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

// doStatus is the testable body of `sextant daemon status`. Returns
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
			fmt.Fprintf(w, "daemon: not running\n")
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
			fmt.Fprintf(w, "daemon: stale runtime.json (pid %d not running)\n", st.Info.PID)
		}
		return errStatusNotRunning
	}

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
	fmt.Fprintf(w, "daemon          pid %d, uptime %s\n", st.Info.PID, uptime)
	fmt.Fprintf(w, "nats            pid %d, addr %s\n", st.Info.NATSPID, defaultStr(st.Info.NATSAddr, "(unset)"))
	fmt.Fprintf(w, "clickhouse      pid %d, tcp %s\n", st.Info.ClickHousePID, defaultStr(st.Info.ClickHouseTCP, "(unset)"))
	fmt.Fprintf(w, "mcp             addr %s, stdio %s\n",
		defaultStr(st.Info.MCPHTTPAddr, "(unset)"),
		defaultStr(st.Info.MCPStdioSocket, "(unset)"),
	)
	fmt.Fprintf(w, "log             %s\n", logPath)
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
		fmt.Fprintf(w, "{\"error\":\"marshal: %v\"}\n", err)
		return
	}
	fmt.Fprintln(w, string(raw))
}
