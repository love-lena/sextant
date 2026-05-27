package component_test

import (
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/tui/component"
)

// fakeComponent is a minimal Component used to verify the interface
// shape compiles and the Host's routing behaves as documented. It
// records the messages and SetSize calls it receives so tests can
// assert against them.
type fakeComponent struct {
	width, height int
	focused       bool
	saw           []tea.Msg
	short         []key.Binding
	full          [][]key.Binding
	initRan       bool
}

func (f *fakeComponent) Init() tea.Cmd {
	f.initRan = true
	return nil
}

func (f *fakeComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	f.saw = append(f.saw, msg)
	return f, nil
}

func (f *fakeComponent) View() string { return "fake-content" }

func (f *fakeComponent) SetSize(w, h int) { f.width, f.height = w, h }

func (f *fakeComponent) Focus() tea.Cmd { f.focused = true; return nil }
func (f *fakeComponent) Blur()          { f.focused = false }
func (f *fakeComponent) Focused() bool  { return f.focused }

func (f *fakeComponent) ShortHelp() []key.Binding  { return f.short }
func (f *fakeComponent) FullHelp() [][]key.Binding { return f.full }

// Compile-time assertion that fakeComponent satisfies Component. If
// the interface gains a method, this line breaks at build time —
// catches regressions before runtime.
var _ component.Component = (*fakeComponent)(nil)

func TestFakeComponentSatisfiesInterface(t *testing.T) {
	t.Parallel()
	var c component.Component = &fakeComponent{}
	if c.View() != "fake-content" {
		t.Errorf("View: want fake-content, got %q", c.View())
	}
}

func TestHostForwardsWindowSizeAndCallsSetSize(t *testing.T) {
	t.Parallel()
	inner := &fakeComponent{}
	host := component.NewHost(inner, component.WithChrome(nil, 4))
	_, _ = host.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if inner.width != 100 {
		t.Errorf("SetSize width: want 100, got %d", inner.width)
	}
	// 30 - 4 reserved = 26 content rows
	if inner.height != 26 {
		t.Errorf("SetSize height: want 26, got %d", inner.height)
	}
	// WindowSizeMsg also forwarded to the inner Update.
	if len(inner.saw) != 1 {
		t.Fatalf("inner saw %d messages, want 1", len(inner.saw))
	}
	if _, ok := inner.saw[0].(tea.WindowSizeMsg); !ok {
		t.Errorf("inner did not see WindowSizeMsg, got %T", inner.saw[0])
	}
}

func TestHostDoneMsgEmitsQuit(t *testing.T) {
	t.Parallel()
	inner := &fakeComponent{}
	host := component.NewHost(inner)
	_, cmd := host.Update(component.DoneMsg{})
	if cmd == nil {
		t.Fatal("DoneMsg produced nil cmd; want tea.Quit")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("DoneMsg cmd produced %T, want tea.QuitMsg", msg)
	}
}

func TestHostInitFocusesAndFiresLoad(t *testing.T) {
	t.Parallel()
	inner := &fakeComponent{}
	host := component.NewHost(
		inner,
		component.WithInitialFocus(),
		component.WithInitialLoad("abc-123"),
	)
	cmd := host.Init()
	if !inner.initRan {
		t.Error("inner Init was not called")
	}
	if !inner.focused {
		t.Error("inner was not focused")
	}
	if cmd == nil {
		t.Fatal("Init returned nil cmd; want batch with LoadMsg")
	}
	// Drain the batched cmd to find the LoadMsg.
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		// Single-cmd batch may collapse to the inner cmd directly.
		if lm, ok := msg.(component.LoadMsg); ok && lm.ID == "abc-123" {
			return
		}
		t.Fatalf("Init cmd produced %T; want BatchMsg or LoadMsg", msg)
	}
	foundLoad := false
	for _, c := range batch {
		if c == nil {
			continue
		}
		if lm, ok := c().(component.LoadMsg); ok && lm.ID == "abc-123" {
			foundLoad = true
		}
	}
	if !foundLoad {
		t.Error("expected LoadMsg{ID: abc-123} in Init batch")
	}
}

func TestHostViewAppliesChrome(t *testing.T) {
	t.Parallel()
	inner := &fakeComponent{}
	chrome := func(w, h int, content string) string {
		return "[chrome]" + content + "[/chrome]"
	}
	host := component.NewHost(inner, component.WithChrome(chrome, 0))
	out := host.View()
	want := "[chrome]fake-content[/chrome]"
	if out != want {
		t.Errorf("View: want %q, got %q", want, out)
	}
}

func TestErrorMsgCarriesError(t *testing.T) {
	t.Parallel()
	err := errors.New("boom")
	m := component.ErrorMsg{Err: err}
	if !errors.Is(m.Err, err) {
		t.Errorf("ErrorMsg.Err mismatch")
	}
}
