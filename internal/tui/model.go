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

	// viewCount is not a view: it is how many values View has, kept at the
	// END of the enum so that adding a view above it grows the count
	// without anyone remembering to. Sweeps over every view bound
	// themselves to this rather than to the last named view, which a view
	// added after it would slip past.
	viewCount
)

// viewBinding is everything the interface has to say about one view: the
// number key that switches to it, the TITLE the centre pane wears while it
// is showing, and the short NAME the footer's key hint calls it. All three
// are held together so a view cannot be bound to a key it is never
// advertised under, or advertised under a key nothing binds.
type viewBinding struct {
	key   string
	view  View
	title string
	name  string
}

// views lists every view that has a key, in key order. It is the single
// source of truth for what a number key does, what the centre pane calls
// itself, and what the footer offers.
//
// Keys "3" (resource addresses) and "5" (timeline) are deliberately absent:
// they are specified but unimplemented, so nothing binds them and nothing
// advertises them. Pressing one falls through to a no-op rather than an
// index lookup into unbound state.
//
// The titles deliberately do not repeat the facet pane's section headers.
// The facet pane's PROVIDERS is a list of values to FILTER by, ranked by
// span count; the centre pane's BY PROVIDER is a rollup ranked by total
// time. Labelling both "PROVIDERS" is what made two different things look
// like one thing listed twice. "BY PROVIDER" and "BY RESOURCE TYPE" are the
// names internal/profile's report already gives those same rollups.
var views = []viewBinding{
	{key: "1", view: ViewProviders, title: "BY PROVIDER", name: "providers"},
	{key: "2", view: ViewTypes, title: "BY RESOURCE TYPE", name: "types"},
	{key: "4", view: ViewCalls, title: "CALLS", name: "calls"},
	{key: "6", view: ViewRawLog, title: "RAW LOG", name: "raw log"},
}

// viewKeys maps the bound number keys to the view they switch to, derived
// from views so a key can never be bound to one view and advertised as
// another.
var viewKeys = func() map[string]View {
	keys := make(map[string]View, len(views))
	for _, b := range views {
		keys[b.key] = b.view
	}
	return keys
}()

// viewTitle is what the centre pane calls the view it is showing. Every
// View value has a binding in views, so the empty fallback is unreachable;
// TestEveryViewHasABinding is what keeps it that way when a view is added.
func viewTitle(v View) string {
	for _, b := range views {
		if b.view == v {
			return b.title
		}
	}
	return ""
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
	// is then discarded. The `var _ tea.Model = (*Model)(nil)` assertion at
	// the foot of this file is what holds that shape: give View or Update a
	// value receiver and the VALUE type satisfies tea.Model, so a copy can
	// be handed to bubbletea and the render path fills this cache on
	// something thrown away a frame later. The assertion fails to compile
	// first. Anything that can change what rows() returns -- the view, the
	// filter -- must invalidate this via invalidateRows.
	rowsCache  []row
	rowsCached bool

	// facetPaneNatural and detailPaneNatural are how wide each side pane
	// would have to be to show its widest line in full. Both are functions
	// of data that never changes after New -- the log's spans, and the
	// facets built from them -- so both are measured there rather than per
	// frame: measuring the detail pane means formatting every span in the
	// log and rolling it up by provider and by resource type, which on a
	// real capture is thousands of lines built and thrown away for every
	// keystroke. Only the terminal-relative clamp (capPaneWidth) depends on
	// the current width, and that is O(1).
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
//
// The opening view is ViewCalls: the individual calls, with the facet pane
// beside them. That is the shape a reader expects of a list and a sidebar --
// the rows are the calls, the sidebar filters them -- whereas opening on a
// rollup put a providers table next to a PROVIDERS facet list and left the
// sidebar's filtering role to be guessed. View's own zero value is
// ViewProviders, so this is set explicitly rather than left to the zero
// value.
func New(l *model.Log, path string) Model {
	// The level dimension goes last, after the span dimensions
	// FacetsForSpans builds: it is the one dimension drawn from entries
	// rather than spans, and it filters only the raw log.
	facets := append(model.FacetsForSpans(l.RPCSpans), levelFacet(l.Entries))
	m := Model{log: l, name: filepath.Base(path), view: ViewCalls, pane: PaneList, facets: facets}
	m.facetCursor = firstFacetCursor(facets)
	m.facetPaneNatural = facetNaturalWidth(m.facets)
	m.detailPaneNatural = detailNaturalWidth(l)
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

// RowCount reports what the selection is clamped against for the active
// view. For the rollup and call views that is the row count itself. For
// ViewRawLog, which has no rows of its own, it is the log's TOTAL entry
// count -- not the number of entries the pane draws, which is only those
// passing entryVisible. Nothing reads the selection in that view (the pane
// renders from m.raw.top, and scrollRawLog does its own clamping against
// the same total), so the two never have to agree.
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
			// Enter jumps from a call row -- currently only ViewCalls' rows
			// are calls -- to the log entry that closed its span. A rollup
			// row stands for a group and resolves to no single span, so
			// there is nothing to jump to and the view stays where it is.
			// row.isCall is the same question the detail pane asks before
			// indexing RPCSpans, so the two cannot disagree about which
			// rows carry a span.
			if m.pane == PaneList {
				if rows := m.rows(); m.selected >= 0 && m.selected < len(rows) {
					if r := rows[m.selected]; r.isCall() {
						m.jumpToSpan(r.spanIdx)
					}
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

// invalidateRows drops any cached rows so they are rebuilt from the current
// view and filter rather than served stale, and re-clamps the selection
// against the list it rebuilds.
//
// CALLERS MUST SET m.view FIRST. Clamping needs a row count, so this
// rebuilds the cache before it returns -- for whichever view m.view names at
// the moment of the call. Assigning the view afterwards therefore leaves a
// cache built for the view being left behind, and rows() will serve it. Both
// callers that switch views (Update's number keys, jumpToSpan) assign
// m.view before they call this, for that reason.
//
// The clamp belongs here because everything that can shorten the list comes
// through here: narrowing a filter can leave the selection past the end of
// what is left, and a table whose cursor is off its own end highlights
// nothing while the detail pane beside it falls to its placeholder --
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
