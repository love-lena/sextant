package layout

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// canvas is a fixed-size grid of rendered rows the layout composites pane boxes
// onto. Presets tile the area without overlap, so place writes each box's rows
// into the canvas at its origin and the rows never collide. A box that would run
// past the canvas edge is clipped (defensive — a correct preset never overruns).
//
// The canvas is built from full-width background rows so the gaps a preset never
// leaves still render in the theme background, and so JoinVertical sees a
// rectangular block. Rows are stored as already-styled strings; place splices a
// box's row over the background row by visible-cell width (ANSI-aware), so the
// background's escape codes never bleed into the box and vice versa.
type canvas struct {
	w, h  int
	rows  []string
	blank string
}

// newCanvas builds a w×h canvas filled with background-coloured blank rows.
func newCanvas(w, h int, bg lipgloss.Color) *canvas {
	blank := lipgloss.NewStyle().Background(bg).Width(w).MaxWidth(w).Render(strings.Repeat(" ", w))
	rows := make([]string, h)
	for i := range rows {
		rows[i] = blank
	}
	return &canvas{w: w, h: h, rows: rows, blank: blank}
}

// place writes a rendered box (a multi-line string of exact outer size) onto the
// canvas at origin (x, y). Each box row replaces the canvas row's cells from x
// for the box row's visible width, leaving the rest of the background row intact.
func (c *canvas) place(box string, x, y int) {
	for i, line := range strings.Split(box, "\n") {
		row := y + i
		if row < 0 || row >= c.h {
			continue
		}
		c.rows[row] = spliceAt(c.rows[row], line, x, c.w)
	}
}

// render joins the canvas rows into the final block.
func (c *canvas) render() string {
	return strings.Join(c.rows, "\n")
}

// spliceAt overlays seg onto base starting at visible column x, returning a row
// of exactly width visible cells. It cuts base into a left part [0,x) and a right
// part [x+width(seg), …) by visible width (ANSI escapes preserved), and stitches
// left + seg + right. This keeps each segment's own styling self-contained, the
// same per-segment discipline widget.Box uses to avoid ANSI bleed.
func spliceAt(base, seg string, x, width int) string {
	segW := ansi.StringWidth(seg)
	left := ansi.Truncate(base, x, "")
	// Pad the left part if base was shorter than x (shouldn't happen with a
	// full-width background row, but stay safe).
	if lw := ansi.StringWidth(left); lw < x {
		left += strings.Repeat(" ", x-lw)
	}
	rightStart := x + segW
	right := ""
	if rightStart < width {
		right = ansi.Cut(base, rightStart, width)
	}
	return left + seg + right
}
