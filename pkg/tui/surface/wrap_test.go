package surface_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rivo/uniseg"

	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// authorWidth mirrors the stream's fixed author column; continuation lines indent
// to authorWidth+1 (one space after the column). The test asserts against this
// offset rather than reaching into the package.
const authorWidth = 14

// TestStreamWrapsLongMessage pins Fix 1: a message wider than the content area
// soft-wraps to multiple stream lines instead of clipping at the right edge. The
// first line carries the author label + the first segment; continuation lines
// indent past the author column with no repeated label. The wrapped text, read
// back across lines, reconstructs the original message.
func TestStreamWrapsLongMessage(t *testing.T) {
	th := theme.Dark()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(),
		surface.WithAuthors(map[string]surface.Author{"lena": {Name: "lena", Role: theme.RoleHuman}}))

	// A narrow inner width so a modest message must wrap; tall enough that every
	// wrapped line is visible (no viewport truncation hiding the proof).
	const innerW, innerH = 40, 20
	s.SetSize(innerW, innerH)
	s.SetFocus(widget.FocusSelected)

	const msg = "this is a deliberately long chat message that must wrap across several lines instead of clipping off the right edge of the pane"
	s.Update(chatEvent("lena", msg))

	lines := visibleLines(s.View())
	if len(lines) < 2 {
		t.Fatalf("a long message should span ≥2 lines; got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	// The first content line carries the author; continuation lines do not repeat
	// it and indent to the author column.
	if !strings.Contains(lines[0], "lena") {
		t.Errorf("first line should carry the author label, got %q", lines[0])
	}
	for i, line := range lines[1:] {
		if strings.Contains(line, "lena") {
			t.Errorf("continuation line %d should not repeat the author, got %q", i+1, line)
		}
		// Continuation lines indent past the author column: the body starts at
		// column authorWidth+1, so the line begins with at least that many spaces.
		indent := authorWidth + 1
		if !strings.HasPrefix(line, strings.Repeat(" ", indent)) {
			t.Errorf("continuation line %d not indented to the author column (%d spaces), got %q", i+1, indent, line)
		}
		// No content line may exceed the pane's inner width (proof it wrapped, not
		// clipped beyond the edge by the viewport's safety net only).
		if w := widthOf(line); w > innerW {
			t.Errorf("line %d width %d exceeds inner width %d: %q", i+1, w, innerW, line)
		}
	}

	// The reconstructed text (author label + all segments, whitespace-collapsed)
	// contains the whole message — nothing was clipped away.
	joined := collapse(strings.Join(lines, " "))
	if !strings.Contains(joined, collapse(msg)) {
		t.Errorf("wrapped lines lost text; reconstructed %q does not contain %q", joined, msg)
	}
}

// TestStreamWrapsUnbrokenToken pins that a single token longer than the content
// width is hard-broken across lines rather than overflowing — a URL or hash still
// folds.
func TestStreamWrapsUnbrokenToken(t *testing.T) {
	th := theme.Dark()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(),
		surface.WithAuthors(map[string]surface.Author{"lena": {Name: "lena", Role: theme.RoleHuman}}))
	const innerW, innerH = 36, 20
	s.SetSize(innerW, innerH)
	s.SetFocus(widget.FocusSelected)

	token := strings.Repeat("x", 80) // one unbroken word, far wider than innerW
	s.Update(chatEvent("lena", token))

	lines := visibleLines(s.View())
	if len(lines) < 2 {
		t.Fatalf("an over-wide token should hard-break to ≥2 lines; got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for i, line := range lines {
		if w := widthOf(line); w > innerW {
			t.Errorf("line %d width %d exceeds inner width %d (token not hard-broken): %q", i, w, innerW, line)
		}
	}
}

// TestStreamReflowsOnResize pins that a width change re-wraps the buffered log:
// a message wrapped at a wide width re-wraps to more lines when the pane narrows.
func TestStreamReflowsOnResize(t *testing.T) {
	th := theme.Dark()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(),
		surface.WithAuthors(map[string]surface.Author{"lena": {Name: "lena", Role: theme.RoleHuman}}))
	s.SetSize(70, 30)
	s.SetFocus(widget.FocusSelected)

	const msg = "the dash assembles pane surfaces into a layout the operator controls and reflows to fill the freed space"
	s.Update(chatEvent("lena", msg))
	wide := len(visibleLines(s.View()))

	s.SetSize(30, 30) // narrow: must re-wrap to more lines
	narrow := len(visibleLines(s.View()))
	if narrow <= wide {
		t.Errorf("narrowing did not re-wrap the buffer: %d lines at width 30 not > %d at width 70", narrow, wide)
	}
}

// TestHardBreakKeepsZWJEmojiWhole pins that the hard-breaker splits on grapheme
// clusters, not runes: a ZWJ emoji sequence (one glyph built from several runes
// joined by zero-width joiners) that lands on the break boundary moves to the
// next chunk WHOLE. A per-rune split would shear the family into its members
// across two lines and miscount the joined glyph's width by summing its parts.
func TestHardBreakKeepsZWJEmojiWhole(t *testing.T) {
	// 👨‍👩‍👧‍👦: man ZWJ woman ZWJ girl ZWJ boy — one 2-cell grapheme cluster.
	const family = "\U0001F468\u200d\U0001F469\u200d\U0001F467\u200d\U0001F466"
	const width = 10
	// 5 single-cell letters then the family: summing the members per rune
	// (2+0+2+0+2+0+2) would overflow the chunk mid-sequence and split the glyph;
	// as one 2-cell cluster it fits whole.
	line := strings.Repeat("a", 5) + family + strings.Repeat("b", 9)

	chunks := surface.HardBreak(line, width)
	if len(chunks) < 2 {
		t.Fatalf("over-wide line should hard-break to ≥2 chunks; got %d: %q", len(chunks), chunks)
	}
	assertClustersIntact(t, chunks, line, width, family)
}

// TestHardBreakKeepsCombiningMarksWithBase pins the same property for a
// combining-mark sequence: a base letter plus its combining marks is one
// grapheme cluster, and a break boundary never strands the marks from the base.
func TestHardBreakKeepsCombiningMarksWithBase(t *testing.T) {
	const cluster = "e\u0301\u0327" // e + combining acute + combining cedilla: one 1-cell cluster
	const width = 5
	// 5 single-cell letters fill the first chunk exactly; the cluster must open
	// the second chunk with both marks still attached to the e.
	line := strings.Repeat("x", 5) + cluster + strings.Repeat("y", 5)

	chunks := surface.HardBreak(line, width)
	if len(chunks) < 2 {
		t.Fatalf("over-wide line should hard-break to ≥2 chunks; got %d: %q", len(chunks), chunks)
	}
	assertClustersIntact(t, chunks, line, width, cluster)
}

// assertClustersIntact checks the three properties a cluster-safe hard-break
// guarantees: the chunks reassemble the input losslessly, no chunk exceeds the
// width in display cells, and the sentinel cluster survives in exactly one chunk
// — never split across a boundary (no chunk carries a partial: any chunk
// containing one of the cluster's runes contains the whole sequence).
func assertClustersIntact(t *testing.T, chunks []string, line string, width int, cluster string) {
	t.Helper()
	if got := strings.Join(chunks, ""); got != line {
		t.Errorf("chunks lost or reordered text:\n got %q\nwant %q", got, line)
	}
	whole := 0
	for i, c := range chunks {
		if w := uniseg.StringWidth(c); w > width {
			t.Errorf("chunk %d width %d exceeds %d: %q", i, w, width, c)
		}
		whole += strings.Count(c, cluster)
		// A chunk that carries any piece of the cluster must carry all of it.
		for _, r := range cluster {
			if strings.ContainsRune(c, r) && !strings.Contains(c, cluster) {
				t.Errorf("chunk %d holds a partial cluster (rune %q without the full sequence): %q", i, r, c)
				break
			}
		}
	}
	if whole != 1 {
		t.Errorf("cluster should appear intact in exactly one chunk; found %d in %q", whole, chunks)
	}
}

// visibleLines strips ANSI and returns the non-empty rendered lines of a view, so
// a test can reason about the wrapped layout as plain text. Trailing blank
// viewport padding is dropped.
func visibleLines(view string) []string {
	var out []string
	for _, line := range strings.Split(stripANSI(view), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

// widthOf is the visible cell width of a plain (ANSI-stripped) line.
func widthOf(line string) int { return len([]rune(line)) }

// collapse squeezes runs of whitespace to single spaces and trims, so a wrapped
// reconstruction can be compared against the source independent of where the
// breaks fell.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
