// sextant-tui-agents is the M13 first-TUI binary: a Bubble Tea agent
// list driven by pkg/client. Lists every AgentDefinition (via list_agents
// RPC), re-fetches on `agents.*.lifecycle` envelopes, counts pending
// user-input requests, and writes the cursor's UUID to the
// `ui_state.<operator>.selected_agent` KV on Enter.
//
// See conventions/tui-conventions.md for the keymap and ui.state.* key
// format; model.go for the reducer; theme.go for the local Lipgloss
// palette.
//
// Size budget: the M13 spec says "~150 LOC; demonstrates the 'minimal
// TUI' pattern". main.go (this file) is ~130 LOC of non-comment code.
// model.go runs longer (~330 LOC) because it implements the full
// conventions keymap + three background subscriptions + the KV watcher;
// each one is a few lines but they add up. The "minimal TUI pattern" is
// faithfully demonstrated by main.go alone (flag parsing → client dial
// → tea.NewProgram); model.go is the reusable boilerplate every later
// TUI will import patterns from.
//
// Plan: plans/bootstrap.md#M13
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantd"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sextant-tui-agents: %v", err)
	}
}

func run() error {
	var (
		configDir   = flag.String("config-dir", "", "config directory (default ~/.config/sextant)")
		operatorArg = flag.String("operator", "", "operator name for ui_state.* keys (default $SEXTANT_OPERATOR / $USER)")
	)
	flag.Parse()

	op, err := resolveOperator(*operatorArg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cli, err := dialDaemon(ctx, *configDir)
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	m := newModel(cli, op)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	teaProgramSendOrNoop = func(msg tea.Msg) { prog.Send(msg) }

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tea run: %w", err)
	}
	return nil
}

// resolveOperator picks the operator name per conventions/tui-conventions.md
// §"Operator identity": flag > $SEXTANT_OPERATOR > os/user > $USER.
// Sanitizes to [a-zA-Z0-9_-] so it slots into a KV key.
func resolveOperator(fromFlag string) (string, error) {
	candidates := []string{fromFlag, os.Getenv("SEXTANT_OPERATOR")}
	if u, err := user.Current(); err == nil {
		candidates = append(candidates, u.Username)
	}
	candidates = append(candidates, os.Getenv("USER"))
	for _, c := range candidates {
		if c == "" {
			continue
		}
		s := sanitizeOperator(c)
		if s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("operator: cannot resolve; pass --operator or set SEXTANT_OPERATOR")
}

var operatorSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeOperator(s string) string {
	return operatorSanitizer.ReplaceAllString(s, "_")
}

// dialDaemon connects via the sextantd-managed runtime info so the TUI
// hits the auto-allocated NATS port the daemon recorded. Mirrors the
// connectAgent pattern in cmd/sextant/agents.go — the placeholder
// client.toml port is not what the daemon actually binds to.
func dialDaemon(ctx context.Context, configDir string) (*client.Client, error) {
	if configDir == "" {
		d, _, err := sextantd.DefaultPaths()
		if err != nil {
			return nil, err
		}
		configDir = d
	}
	sd, err := sextantd.LoadConfig(filepath.Join(configDir, "sextantd.toml"))
	if err != nil {
		return nil, fmt.Errorf("load sextantd.toml: %w", err)
	}
	rt, err := sextantd.ReadRuntimeInfo(sd.Paths.RuntimeFile)
	if err != nil {
		return nil, fmt.Errorf("read runtime.json: %w (is sextantd running?)", err)
	}
	creds, err := sextantd.ReadOperatorCreds(sd.NATS.OperatorCreds)
	if err != nil {
		return nil, fmt.Errorf("read operator creds: %w", err)
	}
	cfg := client.Config{
		NATS:     client.NATSConfig{URL: "nats://" + rt.NATSAddr},
		Operator: client.OperatorConfig{User: creds.User, Password: creds.Password},
		Client: client.ClientConfig{
			ConnectTimeout: client.Duration(10 * time.Second),
			RequestTimeout: client.Duration(30 * time.Second),
		},
	}
	cli, err := client.ConnectWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return cli, nil
}
