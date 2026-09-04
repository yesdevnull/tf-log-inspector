// Package tui is the terminal interface for tfli. It is the only package in
// this project permitted to import a third-party dependency: internal/model,
// internal/profile, internal/span, internal/logfmt and internal/diagnose
// remain dependency-free and terminal-unaware.
package tui

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// View identifies which of the top-level views the interface is showing.
// Views 3 (resource addresses) and 5 (timeline) belong to later phases and
// have no key bound to them yet: ViewCalls and ViewRawLog take the key
// numbers either side of the gaps they leave.
type View uint8

const (
	ViewProviders View = iota // key 1
	ViewTypes                 // key 2
	ViewCalls                 // key 4
	ViewRawLog                // key 6
)

// viewKeys maps the bound number keys to the view they switch to. Keys "3"
// and "5" are deliberately absent, so pressing them falls through to no-op
// rather than an index lookup into unbound state.
var viewKeys = map[string]View{
	"1": ViewProviders,
	"2": ViewTypes,
	"4": ViewCalls,
	"6": ViewRawLog,
}

// Pane identifies which of the interface's three panes has keyboard focus.
type Pane uint8

const (
	PaneFacets Pane = iota
	PaneList
	PaneDetail
)

// paneCount is how many values Pane has. Tab cycles over the panes the
// current width actually draws (focusablePanes), which is a subset of
// these, so this sizes that list rather than serving as a modulus.
const paneCount = PaneDetail + 1

// Model is the bubbletea model for tfli's full-screen interface. It wraps a
// loaded log; nothing here mutates the log.
//
// It is driven through a POINTER and never copied: selectedFacets is a map,
// so a copy shares the user's filter with the model it was copied from and
// a toggle applied to one silently rewrites the other's ranked numbers.
// Init, Update and View all take pointer receivers for that reason, and
// the compile-time assertion below pins it.
type Model struct {
	log  *model.Log
	name string

	view     View
	pane     Pane
	selected int

	// facets is built once from the whole log -- its RPC spans for the
	// three span dimensions, its entries for the level dimension (see
	// levelFacet) -- so a value's count always reflects the log, never the
	// current filter: a facet pane where narrowing the filter also shrank
	// the other options' counts would make it hard to see what widening the
	// filter again would show.
	facets []model.Facet
	// selectedFacets holds, per facet dimension name, the values the user
	// has toggled on. Nothing selected in a dimension means unconstrained;
	// filter() turns this into the model.Filter every view is built from.
	selectedFacets map[string]map[string]bool
	// facetCursor is the pane's highlighted value: which dimension (index
	// into facets) and which value within it space would toggle, and what
	// up/down/j/k move when the facet pane has focus.
	facetCursor facetCursor

	// rowsCache memoises rows() for the current view and filter, so the
	// several callers a single keystroke has -- the RowCount that clamps
	// the selection, then the centre table and the detail pane of the
	// render that follows -- share one full RollupBy/JoinByResourceType/
	// sort rather than each paying for its own. Every method that can serve
	// or fill it takes a POINTER receiver, so they all address the one
	// Model bubbletea holds (see Run) instead of caching into a copy that
	// is then discarded. Anything that can change what rows() returns --
	// the view, the filter -- must invalidate this via invalidateRows.
	rowsCache  []row
	rowsCached bool

	// facetPaneNatural and detailPaneNatural are how wide each side pane
	// would have to be to show its widest line in full. Both are functions
	// of data that never changes after New -- the log's RPC spans, and the
	// facets built from them -- so both are measured there rather than per
	// frame: measuring the detail pane means formatting every span in the
	// log, which on a real capture is thousands of lines built and thrown
	// away for every keystroke. Only the terminal-relative clamp
	// (capPaneWidth) depends on the current width, and that is O(1).
	facetPaneNatural  int
	detailPaneNatural int

	// raw is the raw log view's own state: which entry sits at its top, and
	// any free-text search in progress or last run. See rawlog.go.
	raw rawLogState

	// blockedJump records that the last Enter refused to jump because the
	// active filter hides the target entry (see jumpToSpan). It is a
	// derivation of one keypress and the filter it was pressed under, so
	// Update drops it on the NEXT keypress rather than letting it stand over
	// a table the user has since moved through.
	blockedJump bool

	// showFacetOverlay is whether the facet pane is open as an overlay, in
	// place of the list and detail panes, below the width it would otherwise
	// show inline at. It is only ever set below that width (see
	// toggleFacetFocus): a terminal wide enough to show facets inline has
	// nothing to overlay, and a flag left set there would pop the overlay
	// open unasked the moment the terminal was narrowed.
	showFacetOverlay bool

	width, height int
	quitting      bool
}

// facetCursor is the coordinate of one facet value within Model.facets.
type facetCursor struct {
	dim, val int
}

// New builds the model for l, loaded from path. Only path's base name is
// kept: the header names the file the user is looking at, not where it lives
// on disk. Focus starts on PaneList, since the list is what a user looks at
// first; Pane's own zero value is PaneFacets, so this is set explicitly
// rather than left to the zero value.
func New(l *model.Log, path string) Model {
	// The level dimension goes last, after the span dimensions
	// FacetsForSpans builds: it is the one dimension drawn from entries
	// rather than spans, and it filters only the raw log.
	facets := append(model.FacetsForSpans(l.RPCSpans), levelFacet(l.Entries))
	m := Model{log: l, name: filepath.Base(path), pane: PaneList, facets: facets}
	m.facetCursor = firstFacetCursor(facets)
	m.facetPaneNatural = facetNaturalWidth(m.facets)
	m.detailPaneNatural = detailNaturalWidth(l.RPCSpans)
	return m
}

// Quitting reports whether a quit key has been handled. Tests use this
// rather than reaching into Update's returned tea.Cmd, since tea.Quit is a
// sentinel command and not itself inspectable state.
func (m Model) Quitting() bool {
	return m.quitting
}

// ActiveView reports which top-level view is currently showing. Named
// ActiveView rather than View to avoid colliding with bubbletea's own View,
// which renders a string rather than reporting state.
func (m Model) ActiveView() View {
	return m.view
}

// Focus reports which pane currently has keyboard focus.
func (m Model) Focus() Pane {
	return m.pane
}

// Selected reports the index of the highlighted row within the active
// view's list.
func (m Model) Selected() int {
	return m.selected
}

// RowCount reports how many rows the active view holds, so selection has
// something to clamp against. ViewRawLog has no rollup rows of its own --
// it renders directly from m.log.Entries -- so it is the one view counted
// separately rather than through rows().
func (m *Model) RowCount() int {
	if m.view == ViewRawLog {
		return len(m.log.Entries)
	}
	return len(m.rows())
}

// Init starts no commands: the model has everything it needs from New, and
// nothing here depends on bubbletea's runtime to load.
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update handles one message and returns the model to render next, which is
// always the receiver itself. The receiver is a POINTER, so there is exactly
// one Model: the caches on it (see rowsCache) survive, and a facet toggle --
// which mutates the selectedFacets map in place -- cannot be written to one
// model while another is rendered.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// A blocked jump describes the Enter that was just refused, so it
		// lasts exactly until the next key: any other key moves the
		// selection, the filter or the view out from under it.
		m.blockedJump = false
		// While a search query is being typed, every key is text for the
		// query rather than a command -- including keys bound elsewhere,
		// such as "j" or "q" -- so this is handled before anything else.
		if m.raw.searching {
			m.handleSearchKey(msg)
			if m.quitting {
				return m, tea.Quit
			}
			return m, nil
		}
		// A lone space arrives as KeySpace, not KeyRunes{' '} -- msg.String()
		// happens to render it as " " too, but dispatching on Type is the
		// documented, unambiguous way to recognise it.
		if msg.Type == tea.KeySpace {
			if m.pane == PaneFacets {
				m.toggleSelectedFacetValue()
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab":
			m.focusNextPane()
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		case "esc":
			// Esc clears every active filter regardless of which pane has
			// focus -- the spec binds it globally, not to the facet pane.
			m.clearFilters()
		case "enter":
			// Enter jumps from a span-bearing row -- currently only
			// ViewCalls' rows carry a real spanIdx -- to the log entry that
			// closed it. A rollup row (spanIdx -1) has no single span to
			// jump to, so jumpToSpan leaves m unchanged for those.
			if m.pane == PaneList {
				if rows := m.rows(); m.selected >= 0 && m.selected < len(rows) {
					m.jumpToSpan(rows[m.selected].spanIdx)
				}
			}
		case "pgdown":
			if m.view == ViewRawLog {
				m.pageRawLog(1)
			}
		case "pgup":
			if m.view == ViewRawLog {
				m.pageRawLog(-1)
			}
		case "/":
			// Search only makes sense against the raw log's text.
			if m.view == ViewRawLog {
				m.raw.searching = true
				m.raw.query = ""
				m.raw.notFound = false
			}
		case "n":
			if m.view == ViewRawLog {
				m.searchAgain(1)
			}
		case "N":
			if m.view == ViewRawLog {
				m.searchAgain(-1)
			}
		case "f":
			m.toggleFacetFocus()
		default:
			// Keys "3" and "5" are not in viewKeys, so pressing them lands
			// here and does nothing -- they are unbound, not broken.
			if v, ok := viewKeys[msg.String()]; ok && v != m.view {
				m.view = v
				m.selected = 0
				m.invalidateRows()
			}
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.keepFocusOnADrawnPane()
	}
	return m, nil
}

// focusNextPane is Tab: it moves focus to the next pane the current frame
// actually draws, wrapping at the end. Panes the width degradation has
// collapsed are skipped rather than cycled through invisibly.
func (m *Model) focusNextPane() {
	panes := m.focusablePanes(m.paneWidth())
	for i, p := range panes {
		if p == m.pane {
			m.pane = panes[(i+1)%len(panes)]
			return
		}
	}
	m.pane = panes[0]
}

// keepFocusOnADrawnPane brings focus back to a pane the current width draws.
// A resize narrow enough to collapse the focused pane would otherwise leave
// the keyboard pointed at a pane that is no longer on screen -- the same
// invisible-focus hazard focusNextPane exists to prevent, reached by
// dragging the terminal's edge instead of by pressing Tab.
//
// Focus falls back to the list, the pane every width draws, except while the
// facet overlay is open: there the facet pane is the only pane on screen.
func (m *Model) keepFocusOnADrawnPane() {
	panes := m.focusablePanes(m.paneWidth())
	fallback := panes[0]
	for _, p := range panes {
		if p == m.pane {
			return
		}
		if p == PaneList {
			fallback = PaneList
		}
	}
	m.pane = fallback
}

// toggleFacetFocus is 'f': it puts the facet pane in front of the user and
// gives it the keyboard, whatever the terminal width, and pressing it again
// hands the keyboard back to the list.
//
// Below facetInlineWidth the facet pane is not drawn at all, so it is opened
// as an overlay in place of the list and detail panes (see layout.go's
// renderPanes). Focus has to move with it: the overlay exists precisely so
// facets are usable on a narrow terminal, and an overlay the keyboard cannot
// reach is a column of checkboxes space does nothing to.
//
// At or above that width the facets are already on screen, so 'f' only moves
// focus -- the same place Tab would eventually land. It never sets the
// overlay flag there, so narrowing the terminal later cannot pop an overlay
// open that the user never asked for.
func (m *Model) toggleFacetFocus() {
	if m.pane == PaneFacets {
		m.showFacetOverlay = false
		m.pane = PaneList
		return
	}
	m.showFacetOverlay = m.paneWidth() < facetInlineWidth
	m.pane = PaneFacets
}

// invalidateRows drops any cached rows so the next call to rows() rebuilds
// them from the current view and filter, rather than serving stale ones,
// and re-clamps the selection against the list it rebuilds.
//
// The clamp belongs here because everything that can shorten the list comes
// through here: narrowing a filter can leave the selection past the end of
// what is left, and a table whose cursor is off its own end highlights
// nothing while the detail pane beside it falls to "(no call selected)" --
// a list with no cursor at all until the user presses an arrow key.
//
// The raw log's "pattern not found" is dropped here for the same reason. It
// is a cached derivation of the last query, the position it searched from
// and the filter it searched under; a filter change moves the last of those,
// so the footer would otherwise keep asserting a miss for a search that no
// longer describes what is on screen -- and keep the key hints hidden in the
// one view where n and N matter most.
func (m *Model) invalidateRows() {
	m.rowsCache = nil
	m.rowsCached = false
	m.raw.notFound = false
	m.clampSelection()
}

// moveCursor routes an up/down/j/k press to whichever pane has focus: the
// centre pane's own cursor, or the facet pane's value cursor. PaneDetail has
// no scrollable content of its own yet, so a press while it has focus is
// inert.
//
// What the centre pane's cursor IS depends on the view: the rollup and call
// views have a selected row, while the raw log has none -- it renders from
// its top entry and nothing there reads the selection -- so in that view a
// press scrolls the pane instead.
func (m *Model) moveCursor(delta int) {
	switch m.pane {
	case PaneList:
		if m.view == ViewRawLog {
			m.scrollRawLog(delta)
			return
		}
		m.moveSelection(delta)
	case PaneFacets:
		m.moveFacetCursor(delta)
	}
}

// moveSelection shifts the selected row by delta, clamped to the active
// view's row range. Row 40 of one view is meaningless in another, so a view
// switch resets selection separately in Update rather than relying on this
// clamp to catch it.
func (m *Model) moveSelection(delta int) {
	m.selected += delta
	m.clampSelection()
}

// clampSelection brings the selection back inside the active view's rows:
// to the last row when it sits past the end, and to 0 for a view with no
// rows at all, which is where a selection has to sit for the first row that
// appears to be the one selected.
func (m *Model) clampSelection() {
	if last := m.RowCount() - 1; m.selected > last {
		m.selected = last
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

// View is defined in layout.go: it composes the three-pane layout (facets,
// list, detail), degrading by width, with the header and observer-effect
// caveat that this package's tests pin regardless of that composition.

// Only *Model satisfies tea.Model. Every method that can mutate the model
// or fill a cache on it takes a pointer receiver, so the value type does
// not implement the interface and no caller can hand bubbletea a copy.
var _ tea.Model = (*Model)(nil)

// Run opens the full-screen interface for l, loaded from path, and blocks
// until the user quits. It registers a *Model, and only a *Model can be
// registered: View takes a pointer receiver, so the VALUE type does not
// satisfy tea.Model and tea.NewProgram(m) -- the shape that once let the
// render path cache into a copy bubbletea then discarded -- does not
// compile. The assertion above states that invariant, so restoring a value
// receiver on View or Update breaks the build here rather than quietly
// re-splitting the model in two.
func Run(l *model.Log, path string) error {
	m := New(l, path)
	p := tea.NewProgram(&m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
