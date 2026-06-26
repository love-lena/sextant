package surface_test

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/surface"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
)

// trimMarkerRe matches the stream's truncation marker and captures the trimmed
// message count, so the test can assert the marker stays honest (trimmed +
// retained = everything that arrived).
var trimMarkerRe = regexp.MustCompile(`older history trimmed \((\d+) message\(s\)\)`)

// TestStreamEntryLogIsBounded pins the memory bound on an open conversation:
// the feed subscribes DeliverAll and an open detail holds its place for the
// dash's whole life (ADR-0026), so a busy topic must not grow the entry log —
// or the widget's rendered lines, which replay from it — without bound. Past
// MaxStreamEntries the oldest entries are dropped, the newest are retained,
// and an honest trim marker reports exactly how many messages fell off.
func TestStreamEntryLogIsBounded(t *testing.T) {
	old := surface.MaxStreamEntries
	surface.MaxStreamEntries = 20
	t.Cleanup(func() { surface.MaxStreamEntries = old })

	const total = 50
	th := theme.Dark()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(), surface.WithAuthors(sampleAuthors()))
	// Height roomier than the cap, so the View shows the WHOLE bounded buffer
	// and the assertions below read it directly.
	s.SetSize(60, 40)
	s.SetFocus(widget.FocusSelected)
	for i := range total {
		s.Update(chatEvent("lena", fmt.Sprintf("msg-%03d", i)))
	}

	out := ansi.Strip(s.View())
	lines := strings.Split(out, "\n")

	// Bounded: each short message renders one line, so the buffer is at most
	// the cap plus the one marker line.
	retained := 0
	for _, l := range lines {
		if strings.Contains(l, "msg-") {
			retained++
		}
	}
	if retained > surface.MaxStreamEntries {
		t.Errorf("entry log not bounded: %d message lines retained, cap %d", retained, surface.MaxStreamEntries)
	}
	if len(lines) > surface.MaxStreamEntries+1 {
		t.Errorf("rendered buffer not bounded: %d lines, want ≤ cap+marker = %d", len(lines), surface.MaxStreamEntries+1)
	}

	// Newest retained, oldest gone.
	if !strings.Contains(out, fmt.Sprintf("msg-%03d", total-1)) {
		t.Error("the newest message was not retained")
	}
	if strings.Contains(out, "msg-000") {
		t.Error("the oldest message survived the trim")
	}

	// The marker is present, first, and honest: trimmed + retained = total.
	m := trimMarkerRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no trim marker in the bounded view:\n%s", out)
	}
	if !strings.Contains(lines[0], "older history trimmed") {
		t.Errorf("the trim marker should lead the buffer, got first line %q", lines[0])
	}
	trimmed, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unparseable trim count %q", m[1])
	}
	if trimmed+retained != total {
		t.Errorf("dishonest trim marker: trimmed %d + retained %d != %d sent", trimmed, retained, total)
	}
}

// TestStreamUnderCapHasNoTrimMarker: a stream that never exceeds the cap shows
// no marker — the truncation notice appears only when history was actually
// dropped.
func TestStreamUnderCapHasNoTrimMarker(t *testing.T) {
	th := theme.Dark()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap())
	s.SetSize(60, 40)
	s.SetFocus(widget.FocusSelected)
	for i := range 5 {
		s.Update(chatEvent("lena", fmt.Sprintf("msg-%03d", i)))
	}
	if out := ansi.Strip(s.View()); strings.Contains(out, "older history trimmed") {
		t.Errorf("an untrimmed stream must not show the trim marker:\n%s", out)
	}
}
