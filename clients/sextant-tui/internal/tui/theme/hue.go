package theme

import "github.com/charmbracelet/lipgloss"

// Client roles. One stable hue per role is the whole point of role tokens: you
// read who's who by colour. These are the canonical role strings the dash maps;
// an unknown role falls back to the default foreground.
const (
	RoleHuman       = "human"
	RoleCoordinator = "coordinator"
	RoleDispatcher  = "dispatcher"
	RoleAgent       = "agent"
	RoleSystem      = "system"
)

// Message kinds. A kind is the verb of a message — distinct from who sent it —
// and is tinted separately from the sender's role. These are illustrative
// defaults; an unknown kind falls back to a muted foreground.
const (
	KindChat           = "chat"
	KindSpawnRequest   = "spawn.request"
	KindSpawnAck       = "spawn.ack"
	KindRunEvent       = "run.event"
	KindArtifactUpdate = "artifact.update"
	KindDrain          = "drain"
)

// RoleHue maps a client role to its base16 accent slot: human=blue (base0D),
// coordinator=purple (base0E), dispatcher=orange (base09), agent=green (base0B),
// system=grey (base03). An unknown role returns the default foreground (base05).
func (t Theme) RoleHue(role string) lipgloss.Color {
	switch role {
	case RoleHuman:
		return lipgloss.Color(t.Palette.Base0D)
	case RoleCoordinator:
		return lipgloss.Color(t.Palette.Base0E)
	case RoleDispatcher:
		return lipgloss.Color(t.Palette.Base09)
	case RoleAgent:
		return lipgloss.Color(t.Palette.Base0B)
	case RoleSystem:
		return lipgloss.Color(t.Palette.Base03)
	default:
		return lipgloss.Color(t.Palette.Base05)
	}
}

// KindHue tints a message kind (the verb), distinct from the sender's role:
// chat=foreground, spawn.request=orange, spawn.ack=brown, run.event=teal,
// artifact.update=amber, drain=red. An unknown kind returns a muted foreground
// (base04).
func (t Theme) KindHue(kind string) lipgloss.Color {
	switch kind {
	case KindChat:
		return lipgloss.Color(t.Palette.Base05)
	case KindSpawnRequest:
		return lipgloss.Color(t.Palette.Base09)
	case KindSpawnAck:
		return lipgloss.Color(t.Palette.Base0F)
	case KindRunEvent:
		return lipgloss.Color(t.Palette.Base0C)
	case KindArtifactUpdate:
		return lipgloss.Color(t.Palette.Base0A)
	case KindDrain:
		return lipgloss.Color(t.Palette.Base08)
	default:
		return lipgloss.Color(t.Palette.Base04)
	}
}

// Status is a client's liveness state, read by glyph shape so colour-blind
// terminals still distinguish it.
type Status string

// The liveness states. StatusGlyph renders each as a distinct shape.
const (
	StatusConnected Status = "connected"
	StatusIdle      Status = "idle"
	StatusDraining  Status = "draining"
)

// StatusGlyph returns the shape for a liveness state: ● connected, ◔ idle,
// ⊘ draining. Status is encoded by shape, not colour, so the distinction
// survives a monochrome terminal. An unknown status renders as a hollow ○.
func StatusGlyph(s Status) string {
	switch s {
	case StatusConnected:
		return "●"
	case StatusIdle:
		return "◔"
	case StatusDraining:
		return "⊘"
	default:
		return "○"
	}
}

// StatusHue returns the colour to pair with a status glyph: connected=green
// (base0B), idle=amber (base0A), draining=red (base08). The glyph alone carries
// the meaning; the hue reinforces it.
func (t Theme) StatusHue(s Status) lipgloss.Color {
	switch s {
	case StatusConnected:
		return lipgloss.Color(t.Palette.Base0B)
	case StatusIdle:
		return lipgloss.Color(t.Palette.Base0A)
	case StatusDraining:
		return lipgloss.Color(t.Palette.Base08)
	default:
		return lipgloss.Color(t.Palette.Base03)
	}
}
