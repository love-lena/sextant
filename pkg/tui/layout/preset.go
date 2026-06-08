package layout

// The built-in preset names (ADR-0023: "a few built-in preset layouts"). A
// preset is a named arrangement that maps the set of *visible* pane ids onto
// rectangles for a given terminal size. The dash ships three:
//
//   - cockpit: the default. A narrow left column (presence) beside a tall right
//     column split into the stream (top) and artifact (bottom) — the working
//     cockpit. When the detail pane is shown it takes the bottom of the right
//     column and artifact moves up to share the top row.
//   - stream: stream-focused. The message stream fills the main area; presence
//     keeps its left column; artifact and detail tuck into a short bottom row.
//   - split: an even grid. Every visible pane gets an equal cell in a balanced
//     grid that reflows as panes toggle — the most btop-like arrangement.
//
// A preset never invents panes: it lays out exactly the ids it is given (the
// visible set), and reflows to fill the whole area among them. The detail pane,
// when visible, is just another id in the set; the preset gives it a slot.
const (
	PresetCockpit = "cockpit"
	PresetStream  = "stream"
	PresetSplit   = "split"
)

// presetOrder is the cycle order for "switch preset" — the order the options
// menu and a preset-cycle key step through. It is also the list of valid preset
// names.
var presetOrder = []string{PresetCockpit, PresetStream, PresetSplit}

// Rect is an outer pane rectangle in terminal cells: the box's top-left origin
// and its full width and height (border included). The layout computes a Rect
// per visible pane, then sizes the surface to the box's inner area (w-4, h-2)
// and draws widget.Box at the Rect.
type Rect struct {
	X, Y, W, H int
}

// detailPaneID is the id the layout reserves for the detail-on-demand pane. The
// host mounts a surface under this id; presets give it a slot only when it is in
// the visible set. It is a layout-level convention, not a domain concept — the
// detail surface is supplied by the host like any other.
const detailPaneID = "detail"

// arrange computes the outer rectangle for each visible pane under a preset, for
// a terminal of size w×h. The visible slice is in the host's pane order (the
// order surfaces were registered); arrange honours that order when filling
// slots. The result maps pane id → Rect and tiles the whole w×h area with no
// gaps and no overflow: every cell's right/bottom edge meets its neighbour or
// the terminal edge exactly, because splits are computed by cumulative integer
// boundaries (the last cell in a run absorbs the rounding remainder).
//
// arrange is a pure function of (preset, visible, w, h) — the testable heart of
// the layout. Switching preset, toggling a pane, and resizing all reduce to
// calling arrange again with a new argument and reflowing onto the result.
func arrange(preset string, visible []string, w, h int) map[string]Rect {
	out := make(map[string]Rect, len(visible))
	if len(visible) == 0 || w <= 0 || h <= 0 {
		return out
	}
	if len(visible) == 1 {
		out[visible[0]] = Rect{0, 0, w, h}
		return out
	}
	switch preset {
	case PresetStream:
		arrangeStream(out, visible, w, h)
	case PresetSplit:
		arrangeGrid(out, visible, w, h)
	default:
		arrangeCockpit(out, visible, w, h)
	}
	return out
}

// arrangeCockpit lays out the default cockpit: a left column for the first pane
// (presence) and a right column holding the rest stacked. With the detail pane
// visible it stacks into the right column too — never an always-on extra column.
// This keeps presence as a steady directory beside the working stack.
func arrangeCockpit(out map[string]Rect, visible []string, w, h int) {
	leftW := columnWidth(w)
	left, right := visible[0], visible[1:]
	out[left] = Rect{0, 0, leftW, h}
	stackColumn(out, right, leftW, 0, w-leftW, h)
}

// arrangeStream is the stream-focused preset: presence keeps a left column, the
// message stream fills the main right area, and any remaining panes (artifact,
// detail) share a short bottom strip across the right area. With only two panes
// it degrades to a left column + a filled right pane.
func arrangeStream(out map[string]Rect, visible []string, w, h int) {
	leftW := columnWidth(w)
	left, right := visible[0], visible[1:]
	out[left] = Rect{0, 0, leftW, h}

	rightX, rightW := leftW, w-leftW
	if len(right) == 1 {
		out[right[0]] = Rect{rightX, 0, rightW, h}
		return
	}
	// The first right pane (the stream) takes the top band; the rest tile the
	// bottom band as a row.
	topH := bandHeight(h)
	out[right[0]] = Rect{rightX, 0, rightW, topH}
	tileRow(out, right[1:], rightX, topH, rightW, h-topH)
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
		// The last row may be short of a full set of columns; widen its last cell
		// to the terminal's right edge so the row still fills.
		cEnd := colBounds[c+1]
		if r == rows-1 && isLastInRow(i, n, cols) {
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

// stackColumn tiles ids vertically inside the column rect (x,y,w,h), each pane
// taking an equal-ish height; the last absorbs the remainder so the column fills
// exactly to the bottom.
func stackColumn(out map[string]Rect, ids []string, x, y, w, h int) {
	if len(ids) == 0 {
		return
	}
	bounds := splitInto(h, len(ids))
	for i, id := range ids {
		out[id] = Rect{X: x, Y: y + bounds[i], W: w, H: bounds[i+1] - bounds[i]}
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
// segments (so segment widths differ by at most one and the boundaries reach
// exactly total). bounds[i]..bounds[i+1] is segment i; bounds[0]=0, bounds[n]=total.
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

// columnWidth returns the width of the cockpit/stream left column: a third of
// the terminal, clamped to a sane minimum so presence stays usable and the right
// area keeps room. Below the clamp it takes a fixed share rather than vanishing.
func columnWidth(w int) int {
	cw := w / 3
	const minCol = 18
	if cw < minCol {
		cw = minCol
	}
	if cw > w-minCol {
		cw = w / 2
	}
	if cw < 1 {
		cw = 1
	}
	return cw
}

// bandHeight returns the height of the stream preset's top band: most of the
// height, leaving a short bottom strip for the secondary panes.
func bandHeight(h int) int {
	top := h * 2 / 3
	if top < 1 {
		top = 1
	}
	if top > h-1 {
		top = h - 1
	}
	return top
}

// isLastInRow reports whether index i is the final pane that should stretch to
// the right edge: the last pane overall, when it sits in the final (possibly
// short) row.
func isLastInRow(i, n, cols int) bool {
	return i == n-1 && (i%cols == (n-1)%cols)
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
