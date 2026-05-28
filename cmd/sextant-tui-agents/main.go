// sextant-tui-agents is the standalone agents-list TUI binary, kept
// for backwards-compat with operators wired up to it. The implementation
// lives in `pkg/tui/agents`; this main.go is a thin wrapper that dials
// the daemon, constructs the Component, and hands it to
// `tea.NewProgram` via the package's NewStandalone helper.
//
// The same Component is also reachable via `sextant agents list -i`,
// which is the canonical operator-facing entry point. This standalone
// binary will be deprecated once the `-i` flag is the documented path.
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
	"github.com/love-lena/sextant/pkg/tui/agents"
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

	m := agents.New(agents.Options{Bus: cli, Operator: op})
	standalone := agents.NewStandalone(m)
	prog := tea.NewProgram(standalone, tea.WithAltScreen())
	agents.SetSender(prog.Send)

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
// connectAgent pattern in cmd/sextant/agents.go.
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
