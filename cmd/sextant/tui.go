// tui.go owns the `sextant tui` subcommand — a Huh-driven discovery
// menu listing every Tier 1 component registered via
// `pkg/tui/component`'s registry. Selecting an entry execs the
// equivalent `-i` invocation (e.g. picking "Browse and manage running
// agents" → `sextant agents list -i`).
//
// Resolves plans/issues/feat-sextant-tui-discovery.md.
//
// Why exec rather than re-enter the cobra dispatcher in-process?
//
//   - The `-i` path takes over the terminal with tea.NewProgram +
//     AltScreen. Re-entering after the Huh form already restored the
//     screen state is awkward and easy to get wrong (signal handling,
//     stdin ownership). Forking a child via os/exec sidesteps the
//     coordination problem entirely — the child owns the terminal
//     end-to-end, the parent just waits for exit.
//   - The menu stays trivially extensible: any package that calls
//     `component.Register` from init() shows up automatically, and
//     the command string from `Meta.Command` is the only contract
//     `sextant tui` cares about.
//
// New components appear automatically as they self-register via
// init() — no edit to this file required.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"

	// Side-effect imports: each component package's init() calls
	// component.Register, populating the registry walked below. Without
	// these blank imports the registry would be empty in this binary
	// because nothing else in cmd/sextant pulls these packages directly
	// at the package level (chat.go imports pkg/tui/chat from a function
	// body, which is enough for Go to run its init, but agents is
	// imported only by iflag.go — also fine — yet making the dependency
	// explicit here is the safest hedge against future refactors that
	// might prune those imports).
	_ "github.com/love-lena/sextant/pkg/tui/agentdetail"
	_ "github.com/love-lena/sextant/pkg/tui/agents"
	_ "github.com/love-lena/sextant/pkg/tui/auditlist"
	_ "github.com/love-lena/sextant/pkg/tui/chat"
	_ "github.com/love-lena/sextant/pkg/tui/contextview"
	_ "github.com/love-lena/sextant/pkg/tui/logsview"
	_ "github.com/love-lena/sextant/pkg/tui/pending"
	_ "github.com/love-lena/sextant/pkg/tui/traces"
	_ "github.com/love-lena/sextant/pkg/tui/worktreelist"

	"github.com/love-lena/sextant/pkg/tui/component"
)

// newTUICmd wires `sextant tui` — a discovery menu of registered Tier 1
// components. No positional args; selecting an entry launches the
// matching `-i` surface via os/exec.
func newTUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open a menu of available interactive TUIs",
		Long: `tui opens a Huh-driven menu listing every Tier 1 component
registered with the discovery registry. Selecting an entry launches the
corresponding ` + "`sextant <command> -i`" + ` surface.

The menu is built dynamically — new components appear automatically as
they self-register via init(), without any edit to this command.

Press q or esc to exit the menu without launching anything.`,
		Args: cobra.NoArgs,
		RunE: runTUIMenu,
	}
	return cmd
}

// runTUIMenu is the cobra RunE for `sextant tui`. Walks the registry,
// shows a Huh select, then execs the chosen component's `-i` flow.
func runTUIMenu(cmd *cobra.Command, _ []string) error {
	metas := component.List()
	if len(metas) == 0 {
		// Friendly message + exit 0. Empty registry is a build-time
		// surprise (nothing self-registered), not an operator-input
		// error — but we don't want to crash the process either.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(),
			"sextant tui: no components are registered.\n"+
				"This is a build-time issue — see the docs for which "+
				"Tier 1 components should be available.")
		return nil
	}

	options := buildSelectOptions(metas)

	var chosen string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("sextant").
			Description("Pick a TUI to open (q/esc to quit)").
			Options(options...).
			Value(&chosen),
	)).WithKeyMap(quitKeyMap())
	if err := form.Run(); err != nil {
		// huh returns ErrUserAborted on q/esc/ctrl+c; treat that as a
		// clean exit rather than a process error.
		if isUserAbort(err) {
			return nil
		}
		return fmt.Errorf("tui menu: %w", err)
	}
	if chosen == "" {
		// Defensive — huh should always set the bound value on a
		// successful Run, but guard against future versions changing
		// the contract.
		return nil
	}

	meta, ok := findMetaByName(metas, chosen)
	if !ok {
		return fmt.Errorf("tui: selected component %q not found in registry", chosen)
	}

	// Surfaces that take a positional (an agent ref, a trace id) can't be
	// launched bare — walk the operator through supplying it.
	arg, err := resolveArg(cmd.Context(), meta)
	if err != nil {
		if isUserAbort(err) {
			return nil
		}
		return err
	}

	return execComponent(cmd, meta, arg)
}

// quitKeyMap is huh's default keymap with q + esc added to Quit (huh's
// default binds Quit to ctrl+c only). In a Select, esc still clears an
// active filter first and q is still typed into the filter when one is
// open — so q/esc only quit from the normal nav state, which is what the
// menu's own help text promises.
func quitKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit"))
	return km
}

// resolveArg collects the positional a surface needs before launch.
// Returns "" when the surface takes no arg. Walks the operator through a
// picker (ArgKind=="agent" → choose from live agents) or a free-text
// prompt, falling back to free text when the picker can't reach the
// daemon. Propagates huh.ErrUserAborted so the caller can exit cleanly.
func resolveArg(ctx context.Context, m component.Meta) (string, error) {
	if m.Arg == "" {
		return "", nil
	}
	if m.ArgKind == "agent" {
		if v, err := pickAgent(ctx, m); err == nil {
			return v, nil
		} else if isUserAbort(err) {
			return "", err
		}
		// Daemon unreachable / no agents → fall back to free text.
	}
	return promptArg(m)
}

// pickAgent lists live agents and lets the operator choose one; the
// chosen value is the agent's UUID.
func pickAgent(ctx context.Context, m component.Meta) (string, error) {
	cli, _, err := connectAgent(ctx, globalFlags.configDir)
	if err != nil {
		return "", err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	rpcCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var resp sextantproto.ListAgentsResponse
	if err := cli.RPC(rpcCtx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp); err != nil {
		return "", err
	}
	if len(resp.Agents) == 0 {
		return "", fmt.Errorf("no agents to choose from")
	}
	opts := make([]huh.Option[string], 0, len(resp.Agents))
	for _, a := range resp.Agents {
		label := fmt.Sprintf("%-24s %s  %s", a.Name, a.UUID.String()[:8], a.Lifecycle)
		opts = append(opts, huh.NewOption(label, a.UUID.String()))
	}
	var chosen string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(m.Description).
			Description(fmt.Sprintf("Pick an agent for `%s` (q/esc to cancel)", m.Command)).
			Options(opts...).
			Value(&chosen),
	)).WithKeyMap(quitKeyMap())
	if err := form.Run(); err != nil {
		return "", err
	}
	return chosen, nil
}

// promptArg asks for the surface's positional as free text.
func promptArg(m component.Meta) (string, error) {
	var val string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title(fmt.Sprintf("%s — enter %s", m.Description, m.Arg)).
			Value(&val),
	)).WithKeyMap(quitKeyMap())
	if err := form.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(val), nil
}

// buildSelectOptions converts the registry metadata into Huh options.
// Extracted so tests can assert the mapping without driving Huh
// interactively. Label = Description (one-line summary the operator
// sees), Value = Name (stable identifier used to look the entry back
// up after selection).
func buildSelectOptions(metas []component.Meta) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(metas))
	for _, m := range metas {
		options = append(options, huh.NewOption(m.Description, m.Name))
	}
	return options
}

// findMetaByName looks up a registered Meta by its Name field. Returns
// (zero, false) when no match exists.
func findMetaByName(metas []component.Meta, name string) (component.Meta, bool) {
	for _, m := range metas {
		if m.Name == name {
			return m, true
		}
	}
	return component.Meta{}, false
}

// launchArgs assembles the argv (after the binary) for a chosen surface:
// the command path, the optional positional arg, and `-i` unless the
// surface is interactive-by-default (NoIFlag). Pure — unit-tested.
func launchArgs(m component.Meta, arg string) []string {
	parts := strings.Fields(m.Command)
	if arg != "" {
		parts = append(parts, arg)
	}
	if !m.NoIFlag {
		parts = append(parts, "-i")
	}
	return parts
}

// execComponent forks `sextant <args>` with stdio passed through, after
// printing the resolved command (so the operator can copy/paste + reuse
// it). The child's exit code surfaces back via exitCodeError so the
// operator sees the same status they'd get running the verb directly.
func execComponent(cmd *cobra.Command, m component.Meta, arg string) error {
	args := launchArgs(m, arg)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "→ sextant %s\n", strings.Join(args, " "))

	c := exec.Command(os.Args[0], args...) //nolint:gosec // os.Args[0] is the running binary path
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &exitCodeError{code: exitErr.ExitCode()}
		}
		return fmt.Errorf("launch %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// isUserAbort reports whether the error from huh.Form.Run is the
// expected user-cancel signal (q / esc / ctrl-c). huh exposes
// huh.ErrUserAborted as a wrappable sentinel.
func isUserAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}
