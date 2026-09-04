package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// update applies one message and returns the concrete model. Update's
// signature returns tea.Model, so every test would otherwise repeat this
// assertion.
func update(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", next)
	}
	return got
}

func testLog(t *testing.T, name string) *model.Log {
	t.Helper()
	l, err := model.Load("../../testdata/" + name)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return l
}

func TestViewNamesTheFileAndSpanCounts(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "provider-rpc.log")
	out := m.View()
	if !strings.Contains(out, "provider-rpc.log") {
		t.Errorf("view does not name the file:\n%s", out)
	}
	if !strings.Contains(out, "spans") {
		t.Errorf("view does not report span counts:\n%s", out)
	}
}

// Every duration this interface renders was measured under logging, which
// measured roughly 20x on real captures. A reader taking these figures for
// wall-clock truth would optimise time that does not exist without the log,
// so the caveat is part of the interface, not documentation.
func TestViewCarriesTheObserverEffectCaveat(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "provider-rpc.log")
	if !strings.Contains(m.View(), "under logging") {
		t.Errorf("view omits the logging caveat:\n%s", m.View())
	}
}

func TestQuitKeysSetQuitting(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
	} {
		m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), key)
		if !m.Quitting() {
			t.Errorf("%v did not set quitting", key)
		}
	}
}

// A key the model does not handle must leave it unchanged rather than
// panicking or silently resetting state.
func TestUnknownKeyIsInert(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "x.log")
	got := update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Z'}})
	if got.Quitting() {
		t.Error("an unhandled key set quitting")
	}
}
