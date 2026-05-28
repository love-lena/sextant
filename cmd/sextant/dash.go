// dash.go owns the `sextant dash` flagship multi-pane TUI.
//
// Shape (per `plans/issues/feat-sextant-dash-multipane.md`):
//
//   - Each pane mounts a registered Tier 1 Component from
//     `pkg/tui/component`'s registry, looked up by the pane's
//     `command` string.
//   - Stickers (`github.com/76creates/stickers/flexbox`) handles flex
//     layout; the default is a horizontal row of equal-ratio columns.
//   - BubbleZone (`github.com/lrstanley/bubblezone`) marks each pane's
//     rendered region so mouse clicks can focus the right pane.
//   - Tab / Shift+Tab cycle focus; number keys 1-9 jump directly to a
//     numbered pane; click-to-focus uses bubblezone region lookup.
//   - `q` (when no pane is in INSERT mode) exits cleanly.
//   - Inter-pane routing uses the existing `component.OpenMsg` /
//     `component.LoadMsg` convention — when a pane emits OpenMsg with
//     Target="agent", the dash dispatches a LoadMsg{ID} to the
//     conversation pane.
//   - The `$selected_agent` template variable is resolved against the
//     `ui_state.<operator>.selected_agent` NATS KV key at mount time.
//
// Pane commands not backed by a registered Component (e.g. `pending
// list` until `feat-tui-pending-component` lands) render a static
// placeholder pane with a pointer to the follow-up ticket.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	flexbox "github.com/76creates/stickers/flexbox"
	zone "github.com/lrstanley/bubblezone"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/tui/agents"
	"github.com/love-lena/sextant/pkg/tui/component"

	// Side-effect imports: each component package's init() calls
	// component.Register, populating the registry walked below.
	// Mirrors cmd/sextant/tui.go's blank-imports rationale — the dash
	// must see every Tier 1 component without relying on transitive
	// imports from other cobra commands.
	_ "github.com/love-lena/sextant/pkg/tui/agents"
	_ "github.com/love-lena/sextant/pkg/tui/chat"
)

// newDashCmd builds the `sextant dash` cobra command. No positional
// args; `--dump-default-config` prints the embedded default TOML and
// exits without opening the TUI.
func newDashCmd() *cobra.Command {
	var dumpDefault bool
	cmd := &cobra.Command{
		Use:   "dash",
		Short: "Open the flagship multi-pane TUI",
		Long: `dash opens a multi-pane TUI composing the registered Tier 1
components into a flex layout.

The layout is read from ` + "`~/.config/sextant/config.toml`" + ` when present
and falls back to an embedded default otherwise. Print the default
template via ` + "`sextant dash --dump-default-config`" + `.

Tab / Shift+Tab cycles focus between panes; number keys (1-9) jump
directly to a numbered pane; clicking a pane focuses it. Press q to
exit (when no pane is composing).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dumpDefault {
				_, err := io.WriteString(cmd.OutOrStdout(), defaultDashConfigTOML)
				return err
			}
			return runDash(cmd.Context(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&dumpDefault, "dump-default-config", false,
		"print the embedded default dash config to stdout and exit")
	return cmd
}

// runDash is the live entry point for the multi-pane TUI. Loads the
// config, resolves the `$selected_agent` template, builds the dash
// model, and runs it under bubbletea with mouse cell motion enabled.
//
// The daemon connection is best-effort: if dialing fails (daemon not
// running, KV unreachable), the dash still opens with each pane
// reporting its own error state — failing-closed here would hide the
// agents pane's much-better-targeted error surface.
func runDash(ctx context.Context, stderr io.Writer) error {
	cfg, err := loadDashConfig(globalFlags.configDir)
	if err != nil {
		return fmt.Errorf("dash: %w", err)
	}

	operator := resolveOperatorIdentity()
	var selectedAgent string

	// Best-effort selected_agent resolution. Connection failures are
	// non-fatal — the agents pane has its own error rendering. If we
	// successfully read the KV, the value seeds the conversation
	// pane's initial LoadMsg.
	cli, _, connErr := connectAgent(ctx, globalFlags.configDir)
	if connErr == nil {
		selectedAgent = readSelectedAgent(ctx, cli, operator)
		_ = cli.Close()
	} else {
		_, _ = fmt.Fprintf(stderr, "dash: best-effort selected_agent lookup failed: %v\n", connErr)
	}

	// Initialise the global zone manager BEFORE constructing the
	// dash model. bubblezone's Mark/Scan only work after NewGlobal —
	// without it, mouse clicks would never resolve to a pane id.
	zone.NewGlobal()
	defer zone.Close()

	model := newDashModel(cfg, operator, selectedAgent)

	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("dash: %w", err)
	}
	return nil
}

// readSelectedAgent fetches the operator's current selected_agent UUID
// from the ui_state KV. Returns "" on any error — the dash treats an
// unresolved $selected_agent as "no agent selected" and renders the
// conversation pane in its empty-state.
func readSelectedAgent(ctx context.Context, cli *client.Client, operator string) string {
	if cli == nil {
		return ""
	}
	key := operator + "." + agents.SelectedAgentField
	val, err := cli.GetKV(ctx, agents.UIStateBucket, key)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(val))
	if v == "" || v == agents.NoneSelection {
		return ""
	}
	return v
}

// dashModel is the bubbletea reducer for the multi-pane TUI. Holds
// the panes (one per `[[dash.panes]]` entry), the focus cursor, and
// the Stickers flex layout that lays them out side by side.
type dashModel struct {
	panes      []*pane
	focus      int // index into panes; -1 if no panes
	width      int
	height     int
	flex       *flexbox.FlexBox
	operator   string
	selectedID string // resolved $selected_agent at boot, may be empty
}

// pane is one column in the dash. id + commandStr come from the
// TOML; host wraps the registered Component (or nil for placeholder
// panes); placeholder holds the static body rendered when no
// Component is registered for the command.
type pane struct {
	id          string
	command     string
	host        *component.Host // nil ⇒ placeholder
	placeholder string          // shown when host == nil
	width       int
	height      int
}

// newDashModel builds a dashModel from a validated config. The
// operator + selectedID are threaded into pane construction so the
// conversation pane (when registered) gets seeded with the current
// $selected_agent.
func newDashModel(cfg dashConfig, operator, selectedID string) *dashModel {
	metas := component.List()
	m := &dashModel{
		operator:   operator,
		selectedID: selectedID,
	}
	m.panes = make([]*pane, 0, len(cfg.Dash.Panes))
	for _, pc := range cfg.Dash.Panes {
		m.panes = append(m.panes, buildPane(pc, metas, operator, selectedID))
	}
	if len(m.panes) == 0 {
		m.focus = -1
	}

	// Stickers flexbox: one row, N equal-ratio cells (one per pane).
	// Horizontal split keeps the renderer simple and works well for
	// the default 3-pane layout on the typical operator's wide
	// terminal. The cell IDs match pane ids so we can look the cell
	// back up if needed.
	m.flex = flexbox.New(0, 0)
	row := m.flex.NewRow()
	for _, p := range m.panes {
		row.AddCells(flexbox.NewCell(1, 1).SetID(p.id))
	}
	m.flex.AddRows([]*flexbox.Row{row})
	return m
}

// buildPane resolves a paneConfig against the component registry,
// returning either a hosted Component pane or a placeholder pane.
//
// Resolution rule: split the command on whitespace; the result is
// the "command path" we match against Meta.Command. The token
// `conversation` is a legacy alias for `agents chat` (the chat
// component's registered command), kept so the default TOML's
// `conversation $selected_agent` resolves correctly without forcing
// operators to learn the resource-verb shape inside the config.
//
// Components are registered by Meta.Name; we match against
// Meta.Command instead because the TOML talks about CLI verbs, not
// implementation-internal component names.
func buildPane(pc paneConfig, metas []component.Meta, operator, selectedID string) *pane {
	commandPath, args := splitCommand(pc.Command)
	if commandPath == "conversation" {
		// Legacy alias kept stable so the default TOML's
		// `conversation $selected_agent` resolves to the chat
		// component without forcing operators to update their config.
		commandPath = "agents chat"
	}
	meta, ok := findMetaByCommand(metas, commandPath)
	if !ok {
		return &pane{
			id:          pc.ID,
			command:     pc.Command,
			placeholder: placeholderText(pc.Command),
		}
	}
	// Resolve $selected_agent in args so the host can fire an
	// initial LoadMsg with the resolved id.
	loadID := resolveTemplateArgs(args, selectedID)
	c := meta.New()
	var opts []component.HostOption
	if loadID != "" {
		opts = append(opts, component.WithInitialLoad(loadID))
	}
	return &pane{
		id:      pc.ID,
		command: pc.Command,
		host:    component.NewHost(c, opts...),
	}
}

// splitCommand separates a `paneConfig.Command` into the command
// path (joined back with spaces) and any trailing arguments. The
// command path is matched against `component.Meta.Command`; the
// trailing tokens (which may contain template variables like
// `$selected_agent`) are returned for substitution.
//
// Heuristic: anything starting with `$` is treated as an argument;
// everything before it is the command path. So
// `conversation $selected_agent` splits into ("conversation",
// ["$selected_agent"]) and `agents list` splits into ("agents list",
// []).
func splitCommand(cmd string) (string, []string) {
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return "", nil
	}
	cut := len(tokens)
	for i, t := range tokens {
		if strings.HasPrefix(t, "$") {
			cut = i
			break
		}
	}
	return strings.Join(tokens[:cut], " "), tokens[cut:]
}

// resolveTemplateArgs substitutes template variables in args and
// returns the first non-empty resolved value (the conversation
// pane's `$selected_agent` is the only template we support today).
// Returns "" if no template was supplied or it resolved to empty.
func resolveTemplateArgs(args []string, selectedID string) string {
	for _, a := range args {
		if a == "$selected_agent" {
			return selectedID
		}
	}
	return ""
}

// findMetaByCommand looks up a Meta by its Command path. Returns
// (zero, false) when no match exists.
func findMetaByCommand(metas []component.Meta, cmd string) (component.Meta, bool) {
	for _, m := range metas {
		if m.Command == cmd {
			return m, true
		}
	}
	return component.Meta{}, false
}

// placeholderText is the static body rendered in a pane whose
// command doesn't match any registered Component. Points operators
// at the relevant follow-up ticket so they know what's missing.
func placeholderText(command string) string {
	first := command
	if idx := strings.IndexByte(command, ' '); idx > 0 {
		first = command[:idx]
	}
	ticket := guessFollowupTicket(first)
	return fmt.Sprintf("%s: not yet implemented; see %s", command, ticket)
}

// guessFollowupTicket maps a top-level verb to its follow-up ticket
// path. Used by placeholder panes so the operator has a one-click
// jump to the open work.
func guessFollowupTicket(verb string) string {
	switch verb {
	case "pending":
		return "plans/issues/feat-tui-pending-component.md"
	case "traces":
		return "plans/issues/feat-tui-traces-component.md"
	default:
		return "plans/issues/"
	}
}

// Init kicks off every pane's host.Init in a batch and gives the
// first pane focus.
func (m *dashModel) Init() tea.Cmd {
	if m == nil {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(m.panes)+1)
	for _, p := range m.panes {
		if p == nil || p.host == nil {
			continue
		}
		if cmd := p.host.Init(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(m.panes) > 0 {
		// Focus the first pane on boot so keystrokes have a
		// meaningful target.
		if cmd := m.focusPane(0); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// focusPane sets the focus cursor to i and toggles Focus/Blur on
// the inner components. Returns the focus cmd from the newly-active
// component (typically a cursor blink subscription).
func (m *dashModel) focusPane(i int) tea.Cmd {
	if m == nil {
		return nil
	}
	if i < 0 || i >= len(m.panes) {
		return nil
	}
	for j, p := range m.panes {
		if p == nil || p.host == nil {
			continue
		}
		inner := p.host.Inner()
		if inner == nil {
			continue
		}
		if j == i {
			continue // handled below to capture the cmd
		}
		if inner.Focused() {
			inner.Blur()
		}
	}
	m.focus = i
	p := m.panes[i]
	if p == nil || p.host == nil {
		return nil
	}
	inner := p.host.Inner()
	if inner == nil {
		return nil
	}
	return inner.Focus()
}

// Update is the reducer. Mouse messages run through the zone
// manager first; KeyMsgs are routed to the focused pane unless they
// match a dash-level binding (Tab/Shift+Tab/1-9/q).
func (m *dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg), nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case component.OpenMsg:
		return m.handleOpen(msg)
	case component.DoneMsg:
		// A pane asked to be torn down. With three load-bearing
		// panes there's no "close one pane" surface yet — DoneMsg
		// at the dash level means "quit".
		return m, tea.Quit
	}
	// Default: broadcast to every pane so async messages (frame
	// streams, KV updates, lifecycle deltas) land on the right
	// receiver. Components ignore messages they don't recognize.
	return m.broadcast(msg)
}

// handleResize stores the new dimensions, walks them through
// Stickers, and pushes per-pane SetSize calls.
func (m *dashModel) handleResize(msg tea.WindowSizeMsg) *dashModel {
	if m == nil {
		return m
	}
	m.width, m.height = msg.Width, msg.Height
	if m.flex != nil {
		m.flex.SetWidth(msg.Width)
		m.flex.SetHeight(msg.Height)
		m.flex.ForceRecalculate()
	}
	// Push per-pane sizes derived from the Stickers cells.
	for i, p := range m.panes {
		if p == nil {
			continue
		}
		w, h := m.paneDims(i)
		p.width, p.height = w, h
		if p.host != nil {
			// Forward the WindowSizeMsg to the host with the cell
			// dimensions so the inner Component's SetSize call
			// reflects the pane's actual rect, not the terminal's.
			cellMsg := tea.WindowSizeMsg{Width: w, Height: h}
			next, _ := p.host.Update(cellMsg)
			if hh, ok := next.(*component.Host); ok && hh != nil {
				p.host = hh
			}
		}
	}
	return m
}

// paneDims returns the (width, height) of the i-th pane's
// content rect, derived from the Stickers cell. Falls back to an
// even split of the terminal width on Stickers misconfiguration.
func (m *dashModel) paneDims(i int) (int, int) {
	if m == nil {
		return 0, 0
	}
	if m.flex != nil && m.flex.RowsLen() > 0 {
		row := m.flex.GetRow(0)
		if row != nil && i < row.CellsLen() {
			cell := row.GetCell(i)
			if cell != nil {
				return cell.GetWidth(), cell.GetHeight()
			}
		}
	}
	// Even-split fallback. Avoids div-by-zero with the
	// max(1, len(panes)) guard.
	n := len(m.panes)
	if n < 1 {
		n = 1
	}
	w := m.width / n
	if w < 1 {
		w = 1
	}
	return w, m.height
}

// handleMouse routes a tea.MouseMsg through bubblezone to find the
// pane the cursor is over. Left-click focuses the pane; other mouse
// events are forwarded so panes that support hover/drag/scroll
// receive them.
func (m *dashModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m == nil {
		return m, nil
	}
	// Resolve the click target via bubblezone. We only act on press
	// to keep focus-stealing predictable.
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		for i, p := range m.panes {
			if p == nil {
				continue
			}
			info := zone.Get(p.id)
			if info == nil || info.IsZero() {
				continue
			}
			if info.InBounds(msg) {
				cmd := m.focusPane(i)
				return m, cmd
			}
		}
	}
	// Forward to all panes so wheel/drag/etc. reach the right
	// component without dash-level routing.
	return m.broadcast(msg)
}

// handleKey is the dash-level keymap dispatcher. Tab cycles focus,
// number keys jump, q exits, and everything else is delegated to
// the focused pane.
func (m *dashModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m == nil {
		return m, nil
	}
	switch msg.String() {
	case "tab":
		if len(m.panes) > 0 {
			cmd := m.focusPane((m.focus + 1) % len(m.panes))
			return m, cmd
		}
	case "shift+tab":
		if len(m.panes) > 0 {
			next := m.focus - 1
			if next < 0 {
				next = len(m.panes) - 1
			}
			cmd := m.focusPane(next)
			return m, cmd
		}
	case "q":
		// q exits when the focused pane isn't currently editing.
		// The agents Component treats q as quit (which we'd route
		// through DoneMsg → tea.Quit) so this branch only fires
		// when no pane is registered for the focused position
		// (placeholder pane). Empirically this matches the
		// operator's expectation: "I'm staring at a placeholder; q
		// gets me out".
		if focused := m.focusedPane(); focused == nil || focused.host == nil {
			return m, tea.Quit
		}
	}
	// Number keys 1-9 jump to the corresponding pane.
	if len(msg.Runes) == 1 {
		r := msg.Runes[0]
		if r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.panes) {
				cmd := m.focusPane(idx)
				return m, cmd
			}
		}
	}
	// Delegate to the focused pane.
	if focused := m.focusedPane(); focused != nil && focused.host != nil {
		next, cmd := focused.host.Update(msg)
		if hh, ok := next.(*component.Host); ok && hh != nil {
			focused.host = hh
		}
		return m, cmd
	}
	return m, nil
}

// handleOpen routes a component.OpenMsg between panes. The only
// target wired today is "agent", which dispatches a LoadMsg{ID} to
// the conversation pane.
func (m *dashModel) handleOpen(msg component.OpenMsg) (tea.Model, tea.Cmd) {
	if m == nil {
		return m, nil
	}
	if msg.Target == "agent" {
		// Conversation pane is the canonical recipient of a
		// "show this agent" intent. We identify it by id rather
		// than by command-string so operator overrides that
		// rename the pane still receive routing.
		for _, p := range m.panes {
			if p == nil || p.host == nil {
				continue
			}
			if p.id == "conversation" {
				load := component.LoadMsg{ID: msg.ID}
				next, cmd := p.host.Update(load)
				if hh, ok := next.(*component.Host); ok && hh != nil {
					p.host = hh
				}
				return m, cmd
			}
		}
	}
	return m, nil
}

// broadcast forwards a message to every hosted pane and batches
// the returned commands. Components ignore messages they don't
// recognize, so this is safe.
func (m *dashModel) broadcast(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m == nil {
		return m, nil
	}
	cmds := make([]tea.Cmd, 0, len(m.panes))
	for _, p := range m.panes {
		if p == nil || p.host == nil {
			continue
		}
		next, cmd := p.host.Update(msg)
		if hh, ok := next.(*component.Host); ok && hh != nil {
			p.host = hh
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// focusedPane returns the currently-focused pane or nil.
func (m *dashModel) focusedPane() *pane {
	if m == nil || m.focus < 0 || m.focus >= len(m.panes) {
		return nil
	}
	return m.panes[m.focus]
}

// View renders the flex layout. Each cell's content is the pane's
// host.View (or its placeholder body), wrapped in a bubblezone Mark
// so click-to-focus can resolve the region back to the pane.
func (m *dashModel) View() string {
	if m == nil {
		return ""
	}
	if m.flex == nil || m.flex.RowsLen() == 0 || len(m.panes) == 0 {
		return ""
	}
	row := m.flex.GetRow(0)
	if row == nil {
		return ""
	}
	for i, p := range m.panes {
		if p == nil || i >= row.CellsLen() {
			continue
		}
		cell := row.GetCell(i)
		if cell == nil {
			continue
		}
		focused := i == m.focus
		body := m.renderPaneBody(p)
		cell.SetStyle(paneStyle(focused))
		cell.SetContent(zone.Mark(p.id, body))
	}
	return zone.Scan(m.flex.Render())
}

// renderPaneBody composes the title + content for a single pane.
// Hosted panes use the inner component's View; placeholder panes
// use their static body. The title prefixes the pane id with the
// focus-marker for quick visual scanning.
func (m *dashModel) renderPaneBody(p *pane) string {
	if p == nil {
		return ""
	}
	marker := "  "
	if focused := m.focusedPane(); focused == p {
		marker = "▌ "
	}
	title := lipgloss.NewStyle().Bold(true).Render(marker + p.id)
	var body string
	if p.host != nil {
		body = p.host.View()
	} else {
		body = p.placeholder
	}
	return title + "\n" + body
}

// paneStyle returns the cell border style. Focused panes get a
// brighter border to make the focus state visible without a
// dedicated status bar (which the dash deliberately defers — the
// pane id + focus marker carries the same information for now).
func paneStyle(focused bool) lipgloss.Style {
	base := lipgloss.NewStyle().Padding(0, 1)
	if focused {
		return base.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("12"))
	}
	return base.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8"))
}

// Compile-time guards: dashModel must satisfy tea.Model.
var _ tea.Model = (*dashModel)(nil)
