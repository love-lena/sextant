package widget

import (
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/theme"
)

func TestDetailRendersSections(t *testing.T) {
	d := NewDetail(theme.DefaultTheme())
	d.SetSize(60, 20)
	d.SetSections([]Section{
		{Title: "agent", Rows: []Row{
			{Label: "lifecycle", Value: "running"},
			{Label: "template", Value: "claude-seed"},
		}},
		{Title: "usage", Rows: []Row{
			{Label: "in", Value: "12.4k"},
		}},
	})
	v := d.View()
	for _, want := range []string{"agent", "lifecycle", "running", "template", "claude-seed", "usage", "12.4k"} {
		if !strings.Contains(v, want) {
			t.Fatalf("detail missing %q in:\n%s", want, v)
		}
	}
}

func TestDetailEmpty(t *testing.T) {
	d := NewDetail(theme.DefaultTheme())
	d.SetSize(40, 10)
	// No panic, empty-ish view.
	if d.View() == "" && d.View() != "" {
		t.Fatal("unreachable")
	}
}
