package theme

import (
	"reflect"
	"slices"
	"testing"

	"github.com/charmbracelet/bubbles/key"
)

// TestActionsMirrorsKeymapStruct pins the hand-maintained actions table to the
// Keymap struct. Merge's collision scan and any whole-keymap walker iterate
// actions, so a Binding field that is added to the struct but not the table
// would silently escape collision checking — this test makes that a loud
// failure instead. Order matters too: actions is documented as the canonical
// (struct) order, which keeps collision errors deterministic.
func TestActionsMirrorsKeymapStruct(t *testing.T) {
	typ := reflect.TypeOf(Keymap{})
	bindingType := reflect.TypeOf(key.Binding{})
	var fields []string
	for i := range typ.NumField() {
		if f := typ.Field(i); f.Type == bindingType {
			fields = append(fields, f.Name)
		}
	}
	if !slices.Equal(fields, actions) {
		t.Fatalf("actions table out of step with Keymap struct:\n  struct fields: %v\n  actions:       %v", fields, actions)
	}
	km := DefaultKeymap()
	for _, name := range actions {
		if km.binding(name) == nil {
			t.Errorf("binding(%q) returns nil; the accessor switch is missing the case", name)
		}
	}
}
