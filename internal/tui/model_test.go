package tui

import (
	"slices"
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

// callsModel is a fresh model showing the CALLS view, for the many tests
// whose subject is something other than which view is on -- a jump, a clip,
// a facet, a pane width -- but which need call rows to work with.
//
// It asserts the view rather than pressing the key for it. New opens on the
// calls view, so a '4' press there changes nothing: it reads as "put this
// model on the calls view" while doing no such thing, and the day the
// opening view moves it silently stops putting the model anywhere. The
// assertion holds whatever New opens on, and says so where the model is
// built.
func callsModel(t *testing.T, fixture, name string) Model {
	t.Helper()
	m := New(testLog(t, fixture), name)
	if m.ActiveView() != ViewCalls {
		t.Fatalf("model opened on %v, but this test needs the calls view", m.ActiveView())
	}
	return m
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

// Each key is pressed from a view it is not already showing. Pressed from
// its own view a number key is a deliberate no-op, so a test that started
// every press from one fixed view would report "the key works" for whichever
// key happened to name that view even if nothing bound it at all.
func TestNumberKeysSwitchViews(t *testing.T) {
	for key, want := range map[rune]View{'1': ViewProviders, '2': ViewTypes, '4': ViewCalls, '6': ViewRawLog} {
		m := New(testLog(t, "mixed-hcp.log"), "x.log")
		m.view = ViewRawLog
		if want == ViewRawLog {
			m.view = ViewProviders
		}
		from := m.ActiveView()
		got := update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if got.ActiveView() != want {
			t.Errorf("key %q pressed in %v selected view %v, want %v", key, from, got.ActiveView(), want)
		}
	}
}

// New opens on the calls view: individual calls beside a facet pane that
// filters them. Nothing else in the package asserts it, so a later change
// could move it without a single test noticing.
func TestNewOpensOnTheCallsView(t *testing.T) {
	if got := New(testLog(t, "mixed-hcp.log"), "x.log").ActiveView(); got != ViewCalls {
		t.Errorf("ActiveView = %v on a fresh model, want ViewCalls", got)
	}
}

// Every View must have a binding: the centre pane takes its title from that
// table and the footer takes its key hint from it, so a view added to the
// enum and not to the table renders a nameless pane and is reachable by no
// key at all.
//
// The sweep is bounded by viewCount, the sentinel at the end of the enum,
// rather than by ViewRawLog: a view added AFTER the last named one would
// otherwise fall outside the very test meant to catch it.
//
// Two views bound to one KEY is the same defect from the other side.
// viewKeys is built by writing each binding into a map, so a repeated key
// silently keeps the last one and the earlier view becomes unreachable --
// with its own hint still advertised in the footer, naming a key that
// switches to something else.
func TestEveryViewHasABinding(t *testing.T) {
	for v := View(0); v < viewCount; v++ {
		if viewTitle(v) == "" {
			t.Errorf("view %v has no title in views", v)
		}
		if !slices.ContainsFunc(views, func(b viewBinding) bool { return b.view == v }) {
			t.Errorf("view %v has no key in views", v)
		}
	}
	seen := make(map[string]View, len(views))
	for _, b := range views {
		if other, dup := seen[b.key]; dup {
			t.Errorf("key %q is bound to both %v and %v; only the last survives viewKeys", b.key, other, b.view)
		}
		seen[b.key] = b.view
	}
	if len(viewKeys) != len(views) {
		t.Errorf("viewKeys has %d entries for %d bindings -- a key is bound twice", len(viewKeys), len(views))
	}
}

// A view the centre pane cannot build rows for, or cannot choose columns
// for, must fail where a developer sees it. Left to degrade, the two
// switches return nil and "": the frame then renders a centre pane titled
// with the new view and holding nothing, a detail pane saying nothing is
// selected, and a footer advertising the key that got there -- a dead pane
// that looks like a rendering fault and passes every test in this package.
// Views 3 and 5 are specified and unbuilt, so this is one edit away.
//
// A View outside the enum stands in for that view here: Update can never
// produce one (viewKeys holds only bound keys), so it is the same
// programming error reached without editing the enum.
func TestAnUnhandledViewFailsLoudly(t *testing.T) {
	for _, c := range []struct {
		name string
		call func(m *Model)
	}{
		{"rows", func(m *Model) { m.rows() }},
		{"renderList", func(m *Model) { m.renderList(80, 20) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := New(testLog(t, "provider-rpc.log"), "x.log")
			m.view = viewCount // past every view the switches handle
			defer func() {
				if recover() == nil {
					t.Errorf("%s returned quietly for an unhandled view, leaving a dead pane to render", c.name)
				}
			}()
			c.call(&m)
		})
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
// at a different point. The terminal is wide enough for all three panes to
// be drawn, which is what makes all three focusable.
func TestTabCyclesPaneFocus(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	want := []Pane{PaneDetail, PaneFacets, PaneList}
	for _, w := range want {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		if m.Focus() != w {
			t.Fatalf("focus = %v, want %v", m.Focus(), w)
		}
	}
}

// The detail pane has no scrollable content of its own, so an arrow key
// while it holds focus is inert. Moving the list's selection from there
// would move a cursor bar in a pane the keyboard has left -- and take the
// detail pane's own contents with it, since the pane describes whatever the
// list has selected.
func TestArrowKeysAreInertWhileTheDetailPaneHasFocus(t *testing.T) {
	m := callsModel(t, "provider-rpc.log", "x.log")
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	if m.RowCount() < 2 {
		t.Fatalf("fixture assumption changed: %d call rows, want at least 2 so a selection has somewhere to move", m.RowCount())
	}
	for i := 0; i < int(paneCount) && m.Focus() != PaneDetail; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	}
	if m.Focus() != PaneDetail {
		t.Fatalf("focus never reached PaneDetail")
	}
	before := m.Selected()
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyDown},
		{Type: tea.KeyRunes, Runes: []rune{'k'}},
		{Type: tea.KeyUp},
	} {
		if got := update(t, m, key); got.Selected() != before {
			t.Errorf("%v with the detail pane focused moved the selection to %d, want it left at %d", key, got.Selected(), before)
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

// A view switch must serve the NEW view's rows. The cache is rebuilt before
// the switch returns -- it has to be, to clamp the selection against the
// list it rebuilds -- so a switch that assigned the view after rebuilding
// would leave the table and the detail pane reading a cache built for the
// view being left. Every figure on screen would then be a real figure from
// the wrong projection, under the new view's title.
//
// provider-rpc.log is the fixture because its counts differ by view: two
// calls roll up to one provider row, so a stale cache is visible as a row
// count as well as a row KIND.
func TestSwitchingViewsRebuildsTheRowsForTheViewBeingEntered(t *testing.T) {
	m := callsModel(t, "provider-rpc.log", "x.log")
	if got := len(m.rows()); got != 2 {
		t.Fatalf("fixture assumption changed: %d call rows, want 2", got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	rows := m.rows()
	if len(rows) != 1 {
		t.Fatalf("after switching to the providers view, rows() returned %d rows, want the 1 provider row -- the cache was built for the view being left", len(rows))
	}
	if rows[0].rollup == nil {
		t.Errorf("the providers view served a call row: %+v", rows[0])
	}
}

// Re-pressing the key for the view already active must not zero the user's
// scroll position: the reason selection resets on a view change -- row 40 of
// one view is meaningless in another -- does not apply when the view has not
// changed.
func TestRepeatingTheActiveViewKeyDoesNotResetSelection(t *testing.T) {
	m := callsModel(t, "provider-rpc.log", "x.log")
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
	live := liveModel(t, "two-providers.log")
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
	live := liveModel(t, "two-providers.log")

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

// Tab must not focus a pane the frame does not draw. At 80 columns -- the
// default terminal, and narrower than facetInlineWidth -- the facet pane is
// collapsed, but the keys bound to it are not inert: space toggles a facet,
// which changes the ranked numbers this tool exists to report, with nothing
// on screen to say why a row vanished. The reproduced sequence is two Tabs,
// then Down, then Space.
func TestTabSkipsPanesTheWidthHasCollapsed(t *testing.T) {
	size := tea.WindowSizeMsg{Width: 80, Height: 24}
	base := update(t, New(testLog(t, "two-providers.log"), "x.log"), size)
	before := len(base.rows())
	if before < 2 {
		t.Fatalf("fixture assumption changed: %d provider rows, want at least 2 so a filter can be seen to narrow them", before)
	}

	m := base
	for i := 0; i < 2*int(paneCount); i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		if m.Focus() == PaneFacets {
			t.Fatalf("Tab %d focused the facet pane, which is not drawn at %d columns", i+1, size.Width)
		}
	}

	m = update(t, base, tea.KeyMsg{Type: tea.KeyTab})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(m.rows()); got != before {
		t.Errorf("space after two Tabs left %d provider rows, want the unfiltered %d -- a filter was applied through a pane the user cannot see", got, before)
	}
	if len(m.selectedFacets) != 0 {
		t.Errorf("selectedFacets = %v after keys pressed at 80 columns, want nothing selected", m.selectedFacets)
	}
}

// Below detailInlineWidth the detail pane collapses too, leaving the list
// alone on screen, so Tab has nowhere else to go and must leave focus where
// it is rather than cycling through two invisible panes.
func TestTabIsInertWhenOnlyTheListIsDrawn(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 60, Height: 24})
	for i := 0; i < 3; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		if m.Focus() != PaneList {
			t.Fatalf("Tab %d moved focus to %v, but the list is the only pane drawn at 60 columns", i+1, m.Focus())
		}
	}
}

// Focus can also be stranded by dragging the terminal's edge rather than by
// pressing Tab: a pane focused while it was drawn stops being drawn when the
// window narrows past its threshold, and the keys bound to it would go on
// rewriting the ranked numbers invisibly.
func TestNarrowingTheTerminalMovesFocusOffACollapsedPane(t *testing.T) {
	m := focusFacets(t, New(testLog(t, "two-providers.log"), "x.log"))
	before := len(m.rows())
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.Focus() == PaneFacets {
		t.Fatalf("focus stayed on the facet pane after the terminal narrowed to 80 columns, where it is not drawn")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(m.rows()); got != before {
		t.Errorf("space after narrowing left %d rows, want the unfiltered %d", got, before)
	}
}

// Update must drive the model it was called on, not a copy of it. Run
// registers a *Model and bubbletea re-updates whatever Update returns; a
// value receiver made every message start from a fresh copy, so the model
// the caller held and the model bubbletea rendered were two models -- and
// selectedFacets, a map, was shared between them, so a facet toggle written
// to one silently rewrote the other's ranked numbers.
func TestUpdateDrivesTheModelItWasCalledOn(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	// The starting view is set here, to one the keypress below will move
	// the model OFF. Left at whatever New opens on, the assertion is
	// satisfied by a model that was already showing the view being asserted
	// -- so it would hold whether Update wrote to this model, to a copy of
	// it, or to nothing at all.
	m.view = ViewRawLog
	var prog tea.Model = &m
	next, _ := prog.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if next != prog {
		t.Errorf("Update returned %p, want the model it was called on (%p)", next, prog)
	}
	if m.ActiveView() != ViewProviders {
		t.Errorf("ActiveView = %v on the model Update was called on, want ViewProviders", m.ActiveView())
	}
}
