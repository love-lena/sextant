package layout

import (
	"sort"
	"testing"
)

// The geometry tests assert the testable heart of the layout: arrange tiles the
// whole w×h area with no gaps and no overflow, for every preset and every count
// of visible panes. They are internal (package layout) because arrange and Rect
// are unexported — geometry is an implementation detail behind the Model.

// assertTiles checks that the laid-out rects tile exactly [0,w)×[0,h): every
// rect meets the Box minimum, no two overlap, and the union leaves no uncovered
// cell. It does NOT require a rect per id — graceful degradation may drop panes
// that don't fit — but whatever panes ARE laid out must still tile the whole
// area cleanly (the task's invariant: no overflow, no gaps, no sub-minimum
// rects). It returns the number of panes laid out so a caller can assert on the
// drop count.
func assertTiles(t *testing.T, rects map[string]Rect, w, h int) int {
	t.Helper()
	covered := make([][]bool, h)
	for y := range covered {
		covered[y] = make([]bool, w)
	}
	for id, r := range rects {
		if r.W < minPaneW || r.H < minPaneH {
			t.Errorf("pane %q rect %+v is below the Box minimum %dx%d", id, r, minPaneW, minPaneH)
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > w || r.Y+r.H > h {
			t.Errorf("pane %q rect %+v overflows %dx%d", id, r, w, h)
			continue
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if covered[y][x] {
					t.Errorf("pane %q overlaps at (%d,%d)", id, x, y)
				}
				covered[y][x] = true
			}
		}
	}
	// When no pane was laid out (terminal too small), there is nothing to cover.
	if len(rects) == 0 {
		return 0
	}
	for y := range h {
		for x := range w {
			if !covered[y][x] {
				t.Errorf("cell (%d,%d) uncovered (gap)", x, y)
			}
		}
	}
	return len(rects)
}

func TestArrangeTilesEveryPresetAndCount(t *testing.T) {
	presets := []string{PresetCockpit, PresetSplit}
	sizes := []struct{ w, h int }{
		{80, 24}, {120, 40}, {100, 30}, {60, 20},
	}
	for _, preset := range presets {
		for n := 1; n <= 5; n++ {
			ids := make([]string, n)
			for i := range ids {
				ids[i] = string(rune('a' + i))
			}
			for _, sz := range sizes {
				t.Run(preset, func(t *testing.T) {
					rects := arrange(preset, ids, sz.w, sz.h)
					// These sizes are roomy enough to fit every pane.
					if got := assertTiles(t, rects, sz.w, sz.h); got != n {
						t.Errorf("expected all %d panes laid out at %dx%d, got %d", n, sz.w, sz.h, got)
					}
				})
			}
		}
	}
}

// TestArrangeDegradesGracefullyAtTinyTerminals is the small-terminal +
// dense-count coverage the review asked for: across tiny areas and panel counts
// up to 8, arrange must NEVER hand out a rect below the Box minimum, must still
// tile cleanly with whatever it keeps, and must drop the overflow rather than
// overlap. The 20×6 / 4-pane case the review demonstrated is included.
func TestArrangeDegradesGracefullyAtTinyTerminals(t *testing.T) {
	presets := []string{PresetCockpit, PresetSplit}
	sizes := []struct{ w, h int }{
		{20, 6}, {20, 5}, {16, 4}, {40, 8}, {30, 7}, {12, 9}, {80, 6},
	}
	for _, preset := range presets {
		for n := 1; n <= 8; n++ {
			ids := make([]string, n)
			for i := range ids {
				ids[i] = string(rune('a' + i))
			}
			for _, sz := range sizes {
				t.Run(preset, func(t *testing.T) {
					rects := arrange(preset, ids, sz.w, sz.h)
					kept := assertTiles(t, rects, sz.w, sz.h) // asserts ≥min, no overlap, no gap
					if kept > n {
						t.Errorf("laid out %d panes but only %d were requested", kept, n)
					}
					// If the area can hold at least one min pane plus the rest of the
					// layout, at least one pane must be kept (never blank when something fits).
					if sz.w >= minPaneW && sz.h >= minPaneH && kept == 0 {
						t.Errorf("%s %dx%d n=%d dropped every pane though one fits", preset, sz.w, sz.h, n)
					}
				})
			}
		}
	}
}

// TestArrangeNeverOverlapsExhaustive sweeps a wide range of sizes and counts and
// asserts the no-overlap / no-sub-minimum invariant holds everywhere — the
// single guarantee the canvas relies on to never overwrite a neighbour.
func TestArrangeNeverOverlapsExhaustive(t *testing.T) {
	presets := []string{PresetCockpit, PresetSplit}
	for _, preset := range presets {
		for w := 1; w <= 40; w += 3 {
			for h := 1; h <= 24; h += 2 {
				for n := 0; n <= 6; n++ {
					ids := make([]string, n)
					for i := range ids {
						ids[i] = string(rune('a' + i))
					}
					rects := arrange(preset, ids, w, h)
					assertTiles(t, rects, w, h)
				}
			}
		}
	}
}

func TestArrangeEmptyAndDegenerate(t *testing.T) {
	if got := arrange(PresetCockpit, nil, 80, 24); len(got) != 0 {
		t.Errorf("empty visible set should give no rects, got %v", got)
	}
	if got := arrange(PresetCockpit, []string{"a"}, 0, 24); len(got) != 0 {
		t.Errorf("zero width should give no rects, got %v", got)
	}
	// A single pane fills the whole area regardless of preset.
	for _, p := range []string{PresetCockpit, PresetSplit} {
		got := arrange(p, []string{"only"}, 80, 24)
		want := Rect{0, 0, 80, 24}
		if got["only"] != want {
			t.Errorf("preset %s single pane = %+v, want %+v", p, got["only"], want)
		}
	}
}

func TestSplitIntoFillsExactly(t *testing.T) {
	// total >= n: every segment is positive, balanced (differ by ≤1), reaching total.
	for _, tc := range []struct{ total, n int }{
		{24, 1}, {24, 2}, {25, 3}, {80, 7}, {10, 4},
	} {
		bounds := splitInto(tc.total, tc.n)
		if len(bounds) != tc.n+1 {
			t.Fatalf("splitInto(%d,%d) gave %d bounds, want %d", tc.total, tc.n, len(bounds), tc.n+1)
		}
		if bounds[0] != 0 || bounds[tc.n] != tc.total {
			t.Errorf("splitInto(%d,%d) bounds [%d..%d], want [0..%d]", tc.total, tc.n, bounds[0], bounds[tc.n], tc.total)
		}
		var sizes []int
		for i := range tc.n {
			seg := bounds[i+1] - bounds[i]
			if seg <= 0 {
				t.Errorf("splitInto(%d,%d) segment %d is %d (non-positive)", tc.total, tc.n, i, seg)
			}
			sizes = append(sizes, seg)
		}
		sort.Ints(sizes)
		if len(sizes) > 0 && sizes[len(sizes)-1]-sizes[0] > 1 {
			t.Errorf("splitInto(%d,%d) segments unbalanced: %v", tc.total, tc.n, sizes)
		}
	}
}

// TestSplitIntoUnderfilledDegrades pins the documented total<n behaviour: the
// first `total` segments get width 1, the rest width 0, and the boundaries still
// reach exactly total (no panic, no negative). arrange never feeds this case
// (its fit loop drops panes first), but the helper must degrade predictably.
func TestSplitIntoUnderfilledDegrades(t *testing.T) {
	for _, tc := range []struct{ total, n int }{
		{2, 5}, {0, 3}, {3, 3}, {1, 4},
	} {
		bounds := splitInto(tc.total, tc.n)
		if bounds[0] != 0 || bounds[tc.n] != tc.total {
			t.Errorf("splitInto(%d,%d) bounds must reach exactly total: [%d..%d]", tc.total, tc.n, bounds[0], bounds[tc.n])
		}
		ones := 0
		for i := range tc.n {
			seg := bounds[i+1] - bounds[i]
			if seg < 0 {
				t.Errorf("splitInto(%d,%d) segment %d negative: %d", tc.total, tc.n, i, seg)
			}
			if seg > 1 {
				t.Errorf("splitInto(%d,%d) underfilled segment %d should be 0 or 1, got %d", tc.total, tc.n, i, seg)
			}
			ones += seg
		}
		if ones != tc.total {
			t.Errorf("splitInto(%d,%d) widths sum to %d, want %d", tc.total, tc.n, ones, tc.total)
		}
	}
}

func TestNextPresetCycles(t *testing.T) {
	seen := map[string]bool{}
	p := PresetCockpit
	for range presetOrder {
		seen[p] = true
		p = nextPreset(p)
	}
	if p != PresetCockpit {
		t.Errorf("cycle did not return to start: ended at %q", p)
	}
	if len(seen) != len(presetOrder) {
		t.Errorf("cycle visited %d presets, want %d", len(seen), len(presetOrder))
	}
	if nextPreset("bogus") != PresetCockpit {
		t.Errorf("unknown preset should restart at cockpit")
	}
}
