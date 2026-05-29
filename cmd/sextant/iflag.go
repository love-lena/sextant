// iflag.go owns the `-i` / `--tui` flag wiring for Tier 1 cobra
// commands. The flag mounts the corresponding component (from
// `pkg/tui/component`'s registry) via `component.Host` instead of
// printing the static text output.
//
// Resolves plans/issues/feat-cli-iflag-tier1-components.md.
//
// Today the wired commands are:
//
//   - sextant agents list -i        → pkg/tui/agents.Model
//   - sextant agents show <id> -i   → pkg/tui/agents.Model, seeded
//     with the requested UUID via LoadMsg.
//   - sextant pending list -i       → pkg/tui/pending.Model
//     (ListPane over the user_input.> subject).
//   - sextant traces show <id> -i   → pkg/tui/traces.Model
//     (ListPane over the span-tree projection; shared with the static
//     renderer).
//   - sextant agents context <id> -i → pkg/tui/contextview.Model
//     (StreamViewport tail over the SDK session JSONL; mode keys 1-6).
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxadm/tail"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/sessionlog"
	"github.com/love-lena/sextant/pkg/tui/agents"
	"github.com/love-lena/sextant/pkg/tui/contextview"
	"github.com/love-lena/sextant/pkg/tui/pending"
	"github.com/love-lena/sextant/pkg/tui/traces"
)

// tuiLauncher is the seam tests substitute to assert that the `-i`
// path was taken without booting a tea.Program. Production code uses
// the runXxxTUI functions; tests overwrite this with a recorder.
type tuiLauncher interface {
	RunAgentsList(ctx context.Context, configDir, selectedID string) error
	RunPendingList(ctx context.Context, configDir string) error
	RunTracesShow(ctx context.Context, configDir, traceID string) error
	RunAgentsContext(ctx context.Context, configDir, projectsDir, sessionID string) error
}

// realTUI is the live launcher; tests overwrite via newAgentsListIRunE's
// injection seam below.
type realTUI struct{}

func (realTUI) RunAgentsList(ctx context.Context, configDir, selectedID string) error {
	return runAgentsListTUI(ctx, configDir, selectedID)
}

func (realTUI) RunPendingList(ctx context.Context, configDir string) error {
	return runPendingListTUI(ctx, configDir)
}

func (realTUI) RunTracesShow(ctx context.Context, configDir, traceID string) error {
	return runTracesShowTUI(ctx, configDir, traceID)
}

func (realTUI) RunAgentsContext(ctx context.Context, _ /*configDir*/, projectsDir, sessionID string) error {
	return runAgentsContextTUI(ctx, projectsDir, sessionID)
}

// runAgentsContextTUI tails the agent's session JSONL and runs the
// contextview Component over the parsed event stream. No daemon
// connection — the caller already resolved the session-log paths.
func runAgentsContextTUI(ctx context.Context, projectsDir, sessionID string) error {
	jsonl, err := resolveSessionJSONLPath(projectsDir, sessionID)
	if err != nil {
		return err
	}
	t, err := tail.TailFile(jsonl, tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: true,
		Logger:    tail.DiscardingLogger,
	})
	if err != nil {
		return fmt.Errorf("tail %s: %w", jsonl, err)
	}
	defer func() { _ = t.Stop() }()

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-t.Lines:
				if !ok {
					return
				}
				if line.Err != nil {
					return
				}
				if _, err := io.WriteString(pw, line.Text+"\n"); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		_ = t.Stop()
	}()

	m := contextview.New(contextview.Options{
		Events:      sessionlog.Stream(pr),
		InitialMode: sessionlog.ModeConversation,
	})
	prog := tea.NewProgram(contextview.NewStandalone(m), tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// runTracesShowTUI dials the daemon, builds the traces Component seeded
// with traceID, and runs it. Mirrors runPendingListTUI.
func runTracesShowTUI(ctx context.Context, configDir, traceID string) error {
	cli, _, err := connectAgent(ctx, configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	m := traces.New(traces.Options{Bus: cli, TraceID: traceID})
	standalone := traces.NewStandalone(m, traceID)
	prog := tea.NewProgram(standalone, tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// runPendingListTUI dials the daemon, builds the pending Component, and
// runs it under tea.NewProgram. Mirrors runAgentsListTUI.
func runPendingListTUI(ctx context.Context, configDir string) error {
	cli, _, err := connectAgent(ctx, configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	m := pending.New(pending.Options{Bus: cli})
	standalone := pending.NewStandalone(m)
	prog := tea.NewProgram(standalone, tea.WithAltScreen(), tea.WithContext(ctx))
	pending.SetSender(prog.Send)
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// activeTUILauncher is the swappable seam; tests overwrite it.
var activeTUILauncher tuiLauncher = realTUI{}

// resolveOperatorIdentity is a tiny copy of the operator-name resolver
// used by cmd/sextant-tui-agents/main.go. Threaded through here so the
// `-i` path produces the same ui_state.<operator>.selected_agent KV
// writes as the standalone binary.
func resolveOperatorIdentity() string {
	// Cheap fallback chain: $SEXTANT_OPERATOR → $USER → "operator".
	// The standalone binary has a more elaborate flow including
	// os/user.Current(); the `-i` path matches that via runtime
	// resolution in the agents.New call site below if/when we wire a
	// per-command --operator flag. For now, env-first matches the 95%
	// case (operators run sextant as themselves).
	if v := envOrEmpty("SEXTANT_OPERATOR"); v != "" {
		return sanitizeOperatorName(v)
	}
	if v := envOrEmpty("USER"); v != "" {
		return sanitizeOperatorName(v)
	}
	return "operator"
}

func envOrEmpty(name string) string {
	return os.Getenv(name)
}

// sanitizeOperatorName is the same regex sweep cmd/sextant-tui-agents
// uses. Kept local to the cmd/sextant package so importing the standalone
// binary's package main isn't necessary.
func sanitizeOperatorName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// runAgentsListTUI dials the daemon, builds the agents Component, and
// runs it under tea.NewProgram. Mirrors cmd/sextant-tui-agents/main.go
// minus the flag parsing — the cobra layer already gave us configDir.
func runAgentsListTUI(ctx context.Context, configDir, selectedID string) error {
	cli, _, err := connectAgent(ctx, configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	m := agents.New(agents.Options{
		Bus:        cli,
		Operator:   resolveOperatorIdentity(),
		SelectedID: selectedID,
	})
	var standalone tea.Model
	if selectedID != "" {
		standalone = agents.NewStandaloneWithInitialLoad(m, selectedID)
	} else {
		standalone = agents.NewStandalone(m)
	}
	prog := tea.NewProgram(standalone, tea.WithAltScreen(), tea.WithContext(ctx))
	agents.SetSender(prog.Send)

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// addAgentsListIFlag installs `-i` / `--tui` on `agents list` and
// hooks it before the RunE so the TUI takes over when set. Called
// from newAgentsListCmd in agents.go.
func addAgentsListIFlag(cmd *cobra.Command) {
	var interactive bool
	cmd.Flags().BoolVarP(&interactive, "tui", "i", false,
		"open the interactive TUI instead of printing the list")
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if interactive {
			return activeTUILauncher.RunAgentsList(cmd.Context(), globalFlags.configDir, "")
		}
		return originalRunE(cmd, args)
	}
}

// addAgentsShowIFlag installs `-i` / `--tui` on `agents show <id>`.
// The positional <id> seeds the TUI's cursor via LoadMsg.
func addAgentsShowIFlag(cmd *cobra.Command) {
	var interactive bool
	cmd.Flags().BoolVarP(&interactive, "tui", "i", false,
		"open the interactive agents TUI seeded on this agent")
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if interactive {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			id, resolveErr := resolveAgentRef(ctx, cli, args[0])
			_ = cli.Close()
			if resolveErr != nil {
				return errUserUsage(fmt.Sprintf("agent: %v", resolveErr))
			}
			return activeTUILauncher.RunAgentsList(ctx, globalFlags.configDir, id.String())
		}
		return originalRunE(cmd, args)
	}
}

// addPendingListIFlag installs `-i` / `--tui` on `pending list` and hooks
// it before the RunE so the pending TUI takes over when set.
func addPendingListIFlag(cmd *cobra.Command) {
	var interactive bool
	cmd.Flags().BoolVarP(&interactive, "tui", "i", false,
		"open the interactive pending TUI instead of printing the list")
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if interactive {
			return activeTUILauncher.RunPendingList(cmd.Context(), globalFlags.configDir)
		}
		return originalRunE(cmd, args)
	}
}

// addTracesShowIFlag installs `-i` / `--tui` on `traces show <id>`. The
// positional trace_id seeds the TUI via LoadMsg.
func addTracesShowIFlag(cmd *cobra.Command) {
	var interactive bool
	cmd.Flags().BoolVarP(&interactive, "tui", "i", false,
		"open the interactive span-tree TUI instead of printing the tree")
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if interactive {
			return activeTUILauncher.RunTracesShow(cmd.Context(), globalFlags.configDir, args[0])
		}
		return originalRunE(cmd, args)
	}
}
