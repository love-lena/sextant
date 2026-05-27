package chat

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
)

// Bus is the surface program.go needs from a pkg/client.Client. Defined
// as an interface so tests can wire a fake without booting NATS.
type Bus interface {
	SendPrompt(ctx context.Context, agent uuid.UUID, text string) error
}

// RunConfig collects every parameter Run needs. The frames/lifecycle
// channels are owned by the caller (cmd/sextant/conversation.go) so it
// can apply the same WithStartSeq seeding as the NDJSON path.
type RunConfig struct {
	Ctx          context.Context
	Bus          Bus
	AgentID      uuid.UUID
	AgentName    string
	Branch       string
	Read         bool
	TailUntilEnd bool
	Frames       <-chan client.Message
	Lifecycle    <-chan client.Message
	// SeedTurns is an optional initial transcript. The caller may use
	// it to backfill turns from a `--from-seq` replay before the live
	// stream takes over.
	SeedTurns []Turn
}

// Run constructs the tea.Program, wires the send hook + subscription
// pumps, and blocks until the program exits.
func Run(cfg RunConfig) error {
	m := New(Options{
		AgentName: cfg.AgentName,
		Branch:    cfg.Branch,
		Read:      cfg.Read,
	})
	if len(cfg.SeedTurns) > 0 {
		m = m.WithTurns(cfg.SeedTurns)
	}
	if !cfg.Read && cfg.Bus != nil {
		m = m.WithSendHook(makeSendHook(cfg.Ctx, cfg.Bus, cfg.AgentID))
	}
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(cfg.Ctx))

	// Pump frames + lifecycle as background goroutines that re-issue
	// themselves after every receive. Each pumpXxx returns a tea.Cmd
	// that blocks on one message and returns the corresponding tea.Msg.
	// We call the Cmd to get one message, send it to the program, and
	// loop — using split-per-type loops for clarity.
	if cfg.Frames != nil {
		go pumpFramesLoop(prog, cfg.Frames)
	}
	if cfg.Lifecycle != nil {
		go pumpLifecycleLoop(prog, cfg.Lifecycle)
	}

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("chat tui: %w", err)
	}
	return nil
}

// pumpFramesLoop drives the frames channel into the program. It issues
// pumpFrames, sends the result, then re-issues — until a
// subscriptionEndedMsg lands (meaning the channel is closed).
func pumpFramesLoop(prog *tea.Program, ch <-chan client.Message) {
	for {
		msg := pumpFrames(ch)()
		prog.Send(msg)
		if _, ended := msg.(subscriptionEndedMsg); ended {
			return
		}
	}
}

// pumpLifecycleLoop is the lifecycle-channel counterpart of
// pumpFramesLoop.
func pumpLifecycleLoop(prog *tea.Program, ch <-chan client.Message) {
	for {
		msg := pumpLifecycle(ch)()
		prog.Send(msg)
		if _, ended := msg.(subscriptionEndedMsg); ended {
			return
		}
	}
}

// makeSendHook returns a SendFunc that publishes the prompt via the
// Bus's SendPrompt method. Errors are swallowed — they'll surface on
// the next frame the daemon emits (or the lack of one), and the TUI
// stays alive.
func makeSendHook(ctx context.Context, bus Bus, id uuid.UUID) SendFunc {
	return func(text string) {
		_ = bus.SendPrompt(ctx, id, text)
	}
}

// clientBus adapts *client.Client to the Bus interface. Lives here so
// the rest of the package never imports pkg/client directly (except
// frames.go, which only uses client.Message).
type clientBus struct {
	cli *client.Client
}

// NewClientBus wraps a live client for use with Run. Exposed so
// cmd/sextant/conversation.go can build a Bus from its existing
// *client.Client.
func NewClientBus(cli *client.Client) Bus { return &clientBus{cli: cli} }

func (b *clientBus) SendPrompt(ctx context.Context, id uuid.UUID, text string) error {
	return sendPromptRPC(ctx, b.cli, id, text)
}

// sendPromptRPC is split out so the test can call the Bus seam in
// TestSendHookCallsBusSendPrompt without dragging pkg/rpc into the
// test file.
func sendPromptRPC(ctx context.Context, cli *client.Client, id uuid.UUID, text string) error {
	req := struct {
		AgentID uuid.UUID `json:"agent_id"`
		Content string    `json:"content"`
	}{AgentID: id, Content: text}
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := cli.RPC(ctx, "prompt_agent", req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("prompt_agent: daemon returned ok=false")
	}
	return nil
}
