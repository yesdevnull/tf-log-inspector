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
	got, ok := next.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *tui.Model", next)
	}
	return *got
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

func TestNumberKeysSwitchViews(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	for key, want := range map[rune]View{'1': ViewProviders, '2': ViewTypes, '4': ViewCalls, '6': ViewRawLog} {
		got := update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if got.ActiveView() != want {
			t.Errorf("key %q selected view %v, want %v", key, got.ActiveView(), want)
		}
	}
}

// Views 3 and 5 belong to later phases. Pressing them must do nothing.
func TestUnimplementedViewKeysAreInert(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	for _, key := range []rune{'3', '5'} {
		got := update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if got.ActiveView() != ViewTypes {
			t.Errorf("key %q changed the view to %v, want it left on ViewTypes", key, got.ActiveView())
		}
	}
}

// New starts focus on PaneList, so the cycle from there visits Detail, then
// Facets, then back to List -- the same cycle order as always, just entered
// at a different point.
func TestTabCyclesPaneFocus(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	want := []Pane{PaneDetail, PaneFacets, PaneList}
	for _, w := range want {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		if m.Focus() != w {
			t.Fatalf("focus = %v, want %v", m.Focus(), w)
		}
	}
}

// Selection must not run past the end of the list or below zero, and it must
// reset when the view changes, since row 40 of one view is meaningless in
// another.
func TestSelectionIsClampedAndResetsOnViewChange(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.Selected() != 0 {
		t.Errorf("Selected = %d after moving up from the top, want 0", m.Selected())
	}
	for i := 0; i < 500; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.Selected() >= m.RowCount() {
		t.Errorf("Selected = %d, want less than RowCount %d", m.Selected(), m.RowCount())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.Selected() != 0 {
		t.Errorf("Selected = %d after a view change, want 0", m.Selected())
	}
}

// Re-pressing the key for the view already active must not zero the user's
// scroll position: the reason selection resets on a view change -- row 40 of
// one view is meaningless in another -- does not apply when the view has not
// changed.
func TestRepeatingTheActiveViewKeyDoesNotResetSelection(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.Selected() != 1 {
		t.Fatalf("Selected = %d after moving down once, want 1", m.Selected())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if m.Selected() != 1 {
		t.Errorf("Selected = %d after repeating the active view's key, want 1 (unchanged)", m.Selected())
	}
}

// The rows cache has to survive the path bubbletea actually drives. Run
// registers a *Model, so the model Update returns, the render that follows
// it and the next RowCount all address one Model: a cache filled inside a
// value receiver's own copy is discarded with that copy, and the rollup is
// rebuilt for the RowCount that clamps the selection, again for the centre
// table and again for the detail pane -- three times per arrow-key press --
// while still passing any test that calls rows() on an addressable local of
// its own.
//
// The cache is observed rather than counted: once rows are cached, swapping
// the log out from under the model cannot change what the next caller sees.
// Each test cools the cache by clearing the fields directly rather than
// through invalidateRows, which refills it as a side effect of clamping the
// selection.
func TestRenderFillsTheRowsCacheOnTheModelItRendered(t *testing.T) {
	live := liveModel(t, "two-providers.log", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	want := live.RowCount()
	if want == 0 {
		t.Fatal("fixture has no calls, so a stale count could not be told from a freshly built one")
	}

	live.rowsCache, live.rowsCached = nil, false
	_ = live.View()
	live.log = &model.Log{} // a rebuild would now find no spans at all
	if got := live.RowCount(); got != want {
		t.Errorf("RowCount = %d after a render, want the cached %d -- the render cached into a copy", got, want)
	}
}

func TestRowCountFillsTheRowsCacheOnTheModelItCounted(t *testing.T) {
	live := liveModel(t, "two-providers.log", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})

	live.rowsCache, live.rowsCached = nil, false
	want := live.RowCount()
	if want == 0 {
		t.Fatal("fixture has no calls, so a stale count could not be told from a freshly built one")
	}
	live.log = &model.Log{}
	if got := len(live.rows()); got != want {
		t.Errorf("rows() returned %d rows after RowCount, want the cached %d -- RowCount counted a copy", got, want)
	}
}

// liveModel returns the model bubbletea would hold after keys: New's model
// registered as a tea.Model, as Run registers it, and then updated once per
// key through the interface rather than through the concrete type.
func liveModel(t *testing.T, fixture string, keys ...tea.Msg) *Model {
	t.Helper()
	m := New(testLog(t, fixture), "x.log")
	var prog tea.Model = &m
	for _, k := range keys {
		prog, _ = prog.Update(k)
	}
	live, ok := prog.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *tui.Model -- bubbletea would render a different model from the one it updated", prog)
	}
	return live
}
