package layout

// The built-in preset names (ADR-0023: "a few built-in preset layouts",
// ADR-0024: the cockpit is the three master-detail browsers side by side). A
// preset is a named arrangement that maps the set of *visible* pane ids onto
// rectangles for a given terminal size. The dash ships two:
//
//   - cockpit: the default. Every visible pane gets a full-height column, side
//     by side in registration order — the three browsers (clients · topics ·
//     artifacts) standing as equals, each a list you step into (ADR-0024).
//   - split: an even grid. Every visible pane gets an equal cell in a balanced
//     grid that reflows as panes toggle — the most btop-like arrangement.
//
// A preset never invents panes: it lays out exactly the ids it is given (the
// visible set), and reflows to fill the whole area among them.
const (
	PresetCockpit = "cockpit"
	PresetSplit   = "split"
)

// presetOrder is the cycle order for "switch preset" — the order the options
// menu and a preset-cycle key step through. It is also the list of valid preset
// names.
var presetOrder = []string{PresetCockpit, PresetSplit}

// Rect is an outer pane rectangle in terminal cells: the box's top-left origin
// and its full width and height (border included). The layout computes a Rect
// per visible pane, then sizes the surface to the box's inner area (w-4, h-2)
// and draws widget.Box at the Rect.
type Rect struct {
	X, Y, W, H int
}

// minPaneW and minPaneH are the smallest outer pane rectangle that renders
// cleanly: widget.Box clamps anything below 4×3 up to 4×3 and draws three rows
// into the slot regardless, so a rect handed out below this would make Box
// overrun its slot and overwrite a neighbour. arrange never returns a rect below
// this minimum — it instead drops panes that don't fit (btop-style graceful
// degradation), so the render is clean at any terminal size.
const (
	minPaneW = 4
	minPaneH = 3
)

// arrange computes the outer rectangle for each visible pane under a preset, for
// a terminal of size w×h. The visible slice is in the host's pane order (the
// order surfaces were registered); arrange honours that order when filling
// slots. The result maps pane id → Rect and tiles the whole w×h area with no
// gaps and no overflow: every cell's right/bottom edge meets its neighbour or
// the terminal edge exactly, because splits are computed by cumulative integer
// boundaries (the last cell in a run absorbs the rounding remainder).
//
// Graceful degradation: when the area is too small to give every visible pane at
// least minPaneW×minPaneH, arrange lays out only the largest prefix of the
// visible set that fits and drops the rest (the host/View can note the drop).
// When not even one pane fits, it returns an empty map and the caller renders a
// "terminal too small" notice. Either way NO returned rect is ever below the Box
// minimum, so the composite never overlaps.
//
// arrange is a pure function of (preset, visible, w, h) — the testable heart of
// the layout. Switching preset, toggling a pane, and resizing all reduce to
// calling arrange again with a new argument and reflowing onto the result.
func arrange(preset string, visible []string, w, h int) map[string]Rect {
	if len(visible) == 0 || w < minPaneW || h < minPaneH {
		return map[string]Rect{}
	}
	// Find the largest prefix of the visible set whose every rect meets the Box
	// minimum, by trying decreasing counts against the real geometry (so the fit
	// check can never drift from what arrange actually produces).
	for k := len(visible); k >= 1; k-- {
		out := arrangeExactly(preset, visible[:k], w, h)
		if fitsMin(out) {
			return out
		}
	}
	return map[string]Rect{}
}

// arrangeExactly lays out exactly the given panes under a preset, with no fit
// check — the raw geometry arrange's degradation loop probes. Callers other than
// arrange should not use it directly; it can return sub-minimum rects.
func arrangeExactly(preset string, visible []string, w, h int) map[string]Rect {
	out := make(map[string]Rect, len(visible))
	if len(visible) == 0 {
		return out
	}
	if len(visible) == 1 {
		out[visible[0]] = Rect{0, 0, w, h}
		return out
	}
	switch preset {
	case PresetSplit:
		arrangeGrid(out, visible, w, h)
	default:
		arrangeCockpit(out, visible, w, h)
	}
	return out
}

// fitsMin reports whether every rect in an arrangement meets the Box minimum.
func fitsMin(rects map[string]Rect) bool {
	for _, r := range rects {
		if r.W < minPaneW || r.H < minPaneH {
			return false
		}
	}
	return true
}

// arrangeCockpit lays out the default cockpit (ADR-0024): every visible pane a
// full-height column, side by side in registration order. The three browsers
// stand as equals — a browser's detail opens INSIDE its own pane, so no pane is
// a secondary slot and no column is privileged.
func arrangeCockpit(out map[string]Rect, visible []string, w, h int) {
	tileRow(out, visible, 0, 0, w, h)
}

// arrangeGrid lays the visible panes into the most balanced grid that holds them
// (the btop "even split"): cols = ceil(sqrt(n)) columns, rows filled row-major,
// the last row stretched to fill the bottom. Cells reflow as panes toggle
// because the column/row counts are recomputed from the live count.
func arrangeGrid(out map[string]Rect, visible []string, w, h int) {
	n := len(visible)
	cols := 1
	for cols*cols < n {
		cols++
	}
	rows := (n + cols - 1) / cols

	colBounds := splitInto(w, cols)
	rowBounds := splitInto(h, rows)
	for i, id := range visible {
		c, r := i%cols, i/cols
		// The final pane may sit in a short last row; widen it to the terminal's
		// right edge so that row still fills to the edge.
		cEnd := colBounds[c+1]
		if i == n-1 {
			cEnd = w
		}
		out[id] = Rect{
			X: colBounds[c],
			Y: rowBounds[r],
			W: cEnd - colBounds[c],
			H: rowBounds[r+1] - rowBounds[r],
		}
	}
}

// tileRow tiles ids horizontally inside the rect (x,y,w,h), each an equal-ish
// width; the last absorbs the remainder so the row fills exactly to the right.
func tileRow(out map[string]Rect, ids []string, x, y, w, h int) {
	if len(ids) == 0 {
		return
	}
	bounds := splitInto(w, len(ids))
	for i, id := range ids {
		out[id] = Rect{X: x + bounds[i], Y: y, W: bounds[i+1] - bounds[i], H: h}
	}
}

// splitInto returns n+1 cumulative integer boundaries dividing total into n
// near-equal segments, with the rounding remainder spread across the earliest
// segments (so segment widths differ by at most one) and the boundaries reaching
// exactly total. bounds[i]..bounds[i+1] is segment i; bounds[0]=0, bounds[n]=total.
//
// When total < n there is not enough to give every segment a full cell: the
// first `total` segments get width 1 and the rest width 0 (the boundaries still
// reach exactly total, just with zero-width trailing segments). arrange never
// reaches this case — its fit loop drops panes before a split would go below the
// Box minimum — but the helper degrades predictably rather than panicking.
func splitInto(total, n int) []int {
	bounds := make([]int, n+1)
	if n <= 0 {
		return bounds
	}
	base := total / n
	rem := total % n
	acc := 0
	for i := range n {
		seg := base
		if i < rem {
			seg++
		}
		acc += seg
		bounds[i+1] = acc
	}
	return bounds
}

// validPreset reports whether name is a known built-in preset.
func validPreset(name string) bool {
	for _, p := range presetOrder {
		if p == name {
			return true
		}
	}
	return false
}

// nextPreset returns the preset after name in the cycle order, wrapping around.
// An unknown name starts the cycle at the default.
func nextPreset(name string) string {
	for i, p := range presetOrder {
		if p == name {
			return presetOrder[(i+1)%len(presetOrder)]
		}
	}
	return PresetCockpit
}
