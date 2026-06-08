package layout

import (
	"sort"
	"testing"
)

// The geometry tests assert the testable heart of the layout: arrange tiles the
// whole w×h area with no gaps and no overflow, for every preset and every count
// of visible panes. They are internal (package layout) because arrange and Rect
// are unexported — geometry is an implementation detail behind the Model.

// assertTiles checks that the rects cover exactly [0,w)×[0,h): every pane is
// in-bounds, no two panes overlap, and the union leaves no uncovered cell. It is
// the invariant a reflow must hold (the task's "no overflow past the area, no
// gaps").
func assertTiles(t *testing.T, rects map[string]Rect, ids []string, w, h int) {
	t.Helper()
	if len(rects) != len(ids) {
		t.Fatalf("expected %d rects, got %d", len(ids), len(rects))
	}
	covered := make([][]bool, h)
	for y := range covered {
		covered[y] = make([]bool, w)
	}
	for id, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("pane %q has non-positive size %dx%d", id, r.W, r.H)
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
	for y := range h {
		for x := range w {
			if !covered[y][x] {
				t.Errorf("cell (%d,%d) uncovered (gap)", x, y)
			}
		}
	}
}

func TestArrangeTilesEveryPresetAndCount(t *testing.T) {
	presets := []string{PresetCockpit, PresetStream, PresetSplit}
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
					assertTiles(t, rects, ids, sz.w, sz.h)
				})
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
	for _, p := range []string{PresetCockpit, PresetStream, PresetSplit} {
		got := arrange(p, []string{"only"}, 80, 24)
		want := Rect{0, 0, 80, 24}
		if got["only"] != want {
			t.Errorf("preset %s single pane = %+v, want %+v", p, got["only"], want)
		}
	}
}

func TestSplitIntoFillsExactly(t *testing.T) {
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
		// Segments differ by at most one (balanced) and are strictly increasing.
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
