package surface_test

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"go.uber.org/goleak"
)

// TestMain does two things:
//
//   - Pins lipgloss to a TrueColor profile so golden renders carry the real hue
//     ANSI deterministically, regardless of TTY/CI/pipe — the same pin the widget
//     package uses. glamour (the artifact surface) renders with its own pinned
//     TrueColor profile and a fixed standard style, so its output is stable too.
//   - Runs goleak after the whole package, so a surface that opens a feed (or a
//     watch) in an integration test and forgets to Stop it fails loudly. This
//     guards the Surface contract's teardown against regression. The bus /
//     JetStream stack and the Go runtime spin up background goroutines that are
//     not ours; ignore those by signature (the same set busfeed ignores).
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("github.com/nats-io/nats%2ego.(*Conn).doReconnect"),
		goleak.IgnoreAnyFunction("github.com/nats-io/nats-server/v2/server.(*Server).Run"),
		goleak.IgnoreAnyFunction("github.com/nats-io/nats-server/v2/server.(*Server).startGoRoutine"),
	)
}
