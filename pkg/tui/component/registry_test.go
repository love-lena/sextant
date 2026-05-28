package component

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// registryFake is the smallest possible Component used by the registry
// tests. Lives in the same package as the registry (this file is
// `package component`, not `component_test`) so the test can call the
// unexported resetRegistryForTest helper between cases — the global
// registry would otherwise leak entries between subtests.
type registryFake struct{}

func (registryFake) Init() tea.Cmd                       { return nil }
func (registryFake) Update(tea.Msg) (tea.Model, tea.Cmd) { return registryFake{}, nil }
func (registryFake) View() string                        { return "" }
func (registryFake) SetSize(int, int)                    {}
func (registryFake) Focus() tea.Cmd                      { return nil }
func (registryFake) Blur()                               {}
func (registryFake) Focused() bool                       { return false }
func (registryFake) ShortHelp() []key.Binding            { return nil }
func (registryFake) FullHelp() [][]key.Binding           { return nil }

// newRegistryFake is the factory used by tests as Meta.New.
func newRegistryFake() Component { return registryFake{} }

// TestRegistryAddsEntries verifies that a single Register call lands
// the entry in List output with the metadata intact.
func TestRegistryAddsEntries(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(Meta{
		Name:        "alpha",
		Description: "alpha component",
		Command:     "alpha cmd",
		New:         newRegistryFake,
	})

	got := List()
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1", len(got))
	}
	if got[0].Name != "alpha" {
		t.Errorf("Name = %q, want %q", got[0].Name, "alpha")
	}
	if got[0].Description != "alpha component" {
		t.Errorf("Description = %q, want %q", got[0].Description, "alpha component")
	}
	if got[0].Command != "alpha cmd" {
		t.Errorf("Command = %q, want %q", got[0].Command, "alpha cmd")
	}
	if got[0].New == nil {
		t.Error("New is nil")
	}
}

// TestRegistryPreservesOrder verifies that List returns entries in the
// order Register was called — important because the `sextant tui`
// discovery menu surfaces entries in this order.
func TestRegistryPreservesOrder(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(Meta{Name: "first", New: newRegistryFake})
	Register(Meta{Name: "second", New: newRegistryFake})
	Register(Meta{Name: "third", New: newRegistryFake})

	got := List()
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("List() len = %d, want %d", len(got), len(want))
	}
	for i, m := range got {
		if m.Name != want[i] {
			t.Errorf("entry %d Name = %q, want %q", i, m.Name, want[i])
		}
	}
}

// TestRegistryDuplicatePanics verifies the boot-time double-
// registration guard. Two init()s that pick the same Name should
// surface as a process-boot panic rather than silently overwriting
// the first registration.
func TestRegistryDuplicatePanics(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(Meta{Name: "dup", New: newRegistryFake})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Register did not panic on duplicate Name")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value type = %T, want string", r)
		}
		if msg == "" {
			t.Error("panic message is empty")
		}
	}()
	Register(Meta{Name: "dup", New: newRegistryFake})
}

// TestRegistryListReturnsCopy verifies that mutating the returned
// slice does not affect the registry's internal state. Callers like
// the dash launcher routinely sort/filter the list; an aliasing bug
// would corrupt subsequent reads.
func TestRegistryListReturnsCopy(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(Meta{Name: "stable", New: newRegistryFake})

	first := List()
	first[0].Name = "mutated"

	second := List()
	if second[0].Name != "stable" {
		t.Errorf("registry was mutated through List() return value: got %q", second[0].Name)
	}
}
