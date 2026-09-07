// Package tui is the terminal interface for tfli. It is the only package in
// this project permitted to import a third-party dependency: internal/model,
// internal/profile, internal/span, internal/logfmt and internal/diagnose
// remain dependency-free and terminal-unaware.
package tui

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// View identifies which of the top-level views the interface is showing.
// View 3 (resource addresses) belongs to a later phase and has no key bound
// to it yet: ViewCalls and ViewTimeline take the key numbers either side of
// the gap it leaves.
type View uint8

const (
	ViewProviders View = iota // key 1
	ViewTypes                 // key 2
	ViewCalls                 // key 4
	ViewTimeline              // key 5
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
// Key "3" (resource addresses) is deliberately absent: it is specified but
// unimplemented, so nothing binds it and nothing advertises it. Pressing it
// falls through to a no-op rather than an index lookup into unbound state.
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
	// TIMELINE here is a placeholder title: the timeline renders from its
	// own state, not from rows(), so centreTitle overrides it at render
	// time to name the tier the log actually has (see timelineTitle). This
	// entry exists so viewTitle(ViewTimeline) is never the empty string
	// TestEveryViewHasABinding treats as "no title" -- an unreachable branch
	// kept reachable by that sweep, not by anything that reads this title.
	{key: "5", view: ViewTimeline, title: "TIMELINE", name: "timeline"},
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

	// paneCount is not a pane: it is how many values Pane has, kept at the
	// END of the enum so that adding a pane above it grows the count without
	// anyone remembering to -- the same reasoning viewCount's own doc comment
	// gives for View. Tab cycles over the panes the current width actually
	// draws (focusablePanes), which is a subset of these, so this sizes that
	// list rather than serving as a modulus.
	paneCount
)

// Model is the bubbletea model for tfli's full-screen interface. It wraps a
// loaded log; nothing here mutates the log.
//
// It is driven through a POINTER and never copied: excludedFacets is a map,
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

	// sortCol is which column each table view's rows are sorted by, indexed
	// by View. It is per view because a column index names a DIFFERENT
	// column in each table -- column 2 is "calls" among providers and
	// "resource type" among calls -- so a single index carried across a view
	// switch would sort by whatever column happened to sit at that index
	// there. New sets each to the column that view's row builder already
	// ranks by, so the interface opens on each builder's own ranking; see
	// tables and rows().
	sortCol [viewCount]int

	// facets is built once from the whole log -- its RPC spans for the
	// three span dimensions, its entries for the level dimension (see
	// levelFacet) -- so a value's count always reflects the log, never the
	// current filter: a facet pane where narrowing the filter also shrank
	// the other options' counts would make it hard to see what widening the
	// filter again would show.
	facets []model.Facet
	// excludedFacets holds, per facet dimension name, the values the user
	// has UNTICKED. Every value starts ticked and admitted, so an untouched
	// pane shows the whole log with every box marked: the checkboxes are a
	// legend for what the views beside them are showing, not a tally of
	// picks the reader has accumulated. Nothing excluded in a dimension
	// leaves it unconstrained; filter() turns this into the model.Filter
	// every view is built from.
	excludedFacets map[string]map[string]bool
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

	// timelineSpansCache and timelineTierCache memoise timelineSpans() --
	// which tier the timeline draws, and that tier's spans under the active
	// filter -- and timelineLanesCache memoises the lanes packed from them.
	// They are one cache in two parts and are filled, served and dropped
	// together, because a model.Lane holds INDICES into the span slice: a
	// lane read against a slice built from different filter state indexes
	// the wrong spans, and nothing would report it -- the detail pane would
	// describe one call while Enter jumped to another.
	//
	// Both exist for the same reason rowsCache does. One render/keystroke
	// cycle calls timelineSpans() from the pane title, renderTimeline, the
	// stall annotation, the detail pane and Enter's jump target, and every
	// call runs the filter over the whole tier into a fresh
	// full-capacity slice; timelineLanes() is called nearly as often and
	// sorts what it is given. On a real capture that is thousands of spans
	// copied and sorted several times over for one keystroke.
	//
	// Anything that can change what timelineSpans() returns -- the filter,
	// since the tier itself is a property of the log rather than of the
	// selection (see timelineSpans) -- must invalidate these via
	// invalidateRows, along with everything derived from them below.
	timelineTierCache   timelineTier
	timelineSpansCache  []span.Span
	timelineSpansCached bool
	timelineLanesCache  []model.Lane
	timelineLanesCached bool

	// timelineLabelsCache and timelineLabelWidthCache memoise the lane
	// labels and the width the label column is drawn at, and
	// timelineWallClockCache the window the axis and every bar are scaled
	// to. They belong to the same group for the same reason and are dropped
	// on the same path: all three are derived from the spans and lanes
	// above, so a stale one describes a packing that is gone.
	//
	// They are cached for a reason beyond cost. renderTimeline and
	// stallAnnotation each derived the labels and their width for
	// themselves, and the annotation's copy HAD to match the renderer's
	// clip rule and width or "waiting on aws/1" would name a bar drawn as
	// something else -- an invariant held by two hand-kept copies. One
	// measurement per frame, read by both, is what removes it. The cost is
	// real as well: laneLabels walks every lane into a fresh map, twice per
	// frame, on a log that can pack lanes into the hundreds.
	timelineLabelsCache     []string
	timelineLabelWidthCache int
	timelineLabelsCached    bool
	timelineWallClockCache  uint32
	timelineWallClockCached bool

	// facetPaneNatural and detailPaneNatural are how wide each side pane
	// would have to be to show its widest line in full. Both are functions
	// of data that never changes after New -- the log's spans, and the
	// facets built from them -- so both are measured there rather than per
	// frame: measuring the detail pane means formatting every span in the
	// log and rolling it up by provider and by resource type, which on a
	// real capture is thousands of lines built and thrown away for every
	// keystroke. Only the terminal-relative clamp (capPaneWidth) depends on
	// the current width, and that is O(1).
	// laneOrder is each provider's position in the timeline's lane palette,
	// keyed by the short name the lane labels use. The POSITION is stored
	// rather than the style, so a palette rebuilt for a NO_COLOR terminal
	// reaches lanes drawn from a model that was built before it.
	laneOrder map[string]int

	facetPaneNatural  int
	detailPaneNatural int

	// raw is the raw log view's own state: which entry sits at its top, and
	// any free-text search in progress or last run. See rawlog.go.
	raw rawLogState

	// timeline is the timeline view's own state: which lane the cursor is on,
	// and which of that lane's spans is selected within it. See
	// timelineState in timeline.go.
	timeline timelineState

	// viewSelected is each view's own cursor row, so a reader coming back to
	// a view finds it as they left it. The cursor does not TRAVEL between
	// views -- row 40 of one means nothing in another, which is why setView
	// does not simply carry m.selected across -- but a view being re-entered
	// is not a new view, and resetting it there loses a place the reader
	// chose. The filter already survives a view switch; this is the cursor
	// keeping the same promise.
	//
	// Indexed by View, so a view added to the enum grows the array with it
	// (see viewCount). ViewRawLog and ViewTimeline keep their own cursors
	// elsewhere -- m.raw.top and m.timeline -- and their entry here is
	// unused rather than special-cased: an unused int costs nothing, where a
	// gap in the indexing would have to be remembered at every access.
	viewSelected [viewCount]int
	// returnTo is the view Enter jumped OUT of, and hasReturn whether there
	// is one. Together they are what Esc spends to put the reader back.
	//
	// It is set after the jump's own setView and cleared by every other one,
	// so it names a jump the reader actually made rather than the last view
	// they happened to be in: gone to the timeline and back to the raw log
	// by hand, there is nothing to return FROM, and Esc keeps the meaning
	// the footer has always given it.
	returnTo  View
	hasReturn bool
	// blockedJump records that the last Enter refused to jump because the
	// active filter hides the target entry (see jumpToSpan). It is a
	// derivation of one keypress and the filter it was pressed under, so
	// Update drops it on the NEXT keypress rather than letting it stand over
	// a table the user has since moved through.
	blockedJump bool

	// showHelp is whether the key table is open in place of the pane row.
	// It is a MODAL state: while it is set every key but the three that
	// leave it is inert (see Update), the same treatment a search in
	// progress gets, so a reader who opened it cannot move the view or the
	// filter underneath it without seeing that they have.
	showHelp bool

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
	// Every table view starts on the column its own builder already ranks
	// by, so the table is served in that builder's own order -- tie-break
	// included -- until the reader moves the sort off it. A view with no
	// table has no entry here and keeps the zero value, which nothing reads.
	for v, t := range tables {
		m.sortCol[v] = t.defaultCol
	}
	m.facetCursor = firstFacetCursor(facets)
	// Settled at load for the reason the pane widths beside it are: the
	// timeline is redrawn on every keystroke and this walks every span in
	// the tier, which is thousands of them on a real capture.
	m.laneOrder = laneOrderFor(l)
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

// RowCount reports what m.selected is clamped against for the active view.
// For the rollup and call views that is the row count itself. For
// ViewRawLog, which has no rows of its own, it is the log's TOTAL entry
// count -- not the number of entries the pane draws, which is only those
// passing entryVisible. Nothing reads m.selected in that view (the pane
// renders from m.raw.top, and scrollRawLog does its own clamping against
// the same total), so the two never have to agree.
//
// ViewTimeline reports 0, because rows() is nil for it (see rows' own
// switch, which says why a lane is not a row):
// the timeline has a cursor, but it is m.timeline's lane and within-lane
// span, clamped by clampTimelineSelection against the packed lanes rather
// than by anything here. m.selected is unused in that view, so 0 is an
// honest answer about m.selected rather than a claim the view has nothing
// selected.
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
// which mutates the excludedFacets map in place -- cannot be written to one
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
		// The help is modal, so it is answered before any other binding --
		// the same precedence a search in progress takes above. Only the
		// keys that LEAVE it do anything: ? and Esc close it, q quits, and
		// every other key is swallowed rather than acting on a view the
		// reader cannot see. Quit is the exception because help is the
		// screen a lost reader opens, and being unable to leave the program
		// from it is the worst place to strand them.
		if m.showHelp {
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "?", "esc":
				m.showHelp = false
			}
			return m, nil
		}
		if msg.Type == tea.KeySpace {
			if m.pane == PaneFacets {
				m.toggleFacetValue()
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
		case "o":
			// o narrows the cursor's dimension to the one value under the
			// cursor, and is bound to the facet pane for the same reason
			// space is: the facet cursor stays drawn, dimmed, in an
			// unfocused pane, so a key accepted from the list or the detail
			// pane would rewrite the ranked numbers with nothing on screen
			// behaving like a control. Space needs a case of its own above
			// the switch because it arrives as tea.KeySpace; o is dispatched
			// by msg.String() like every other key here.
			if m.pane == PaneFacets {
				m.soloFacetValue()
			}
		case "esc":
			// Esc first puts the reader back where Enter took them from,
			// and only then clears the filters -- unwinding what is
			// innermost, the way an Esc nested in anything does.
			//
			// The two never have to be told apart by guesswork: the footer
			// names whichever is live (see actionKeys), and the return is
			// spent by using it, so the next Esc is the ordinary one. A
			// reader who wants their filter cleared from inside a jump
			// presses Esc twice.
			//
			// Clearing is otherwise global, regardless of which pane has
			// focus -- the spec binds it that way, not to the facet pane.
			if !m.returnFromJump() {
				m.clearFilters()
			}
		case "enter":
			// Enter jumps to the log entry that closed the selected span: a
			// call row's own span in the table views -- a rollup row stands
			// for a group and resolves to no single span, so there is
			// nothing to jump to there -- or the timeline's selected span,
			// which has no row at all. jumpTarget is the single predicate
			// for what that is, asked here and by the footer's open hint
			// (selectedRowOpens), so the two cannot disagree about what
			// Enter does.
			if m.pane == PaneList {
				if spans, idx, ok := m.jumpTarget(); ok {
					m.jumpToSpan(spans, idx)
				}
			}
		case "left", "h":
			// Left/right step through the selected lane's spans, in start
			// order. Bound only in the timeline, with the list pane
			// focused, the same way pgup/pgdown are bound only in the raw
			// log: elsewhere there is no within-lane cursor for them to
			// move.
			if m.view == ViewTimeline && m.pane == PaneList {
				m.moveTimelineSpan(-1)
			}
		case "right", "l":
			if m.view == ViewTimeline && m.pane == PaneList {
				m.moveTimelineSpan(1)
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
		case "\\":
			// Backslash drops the scope and leaves the position, so the
			// entry on the pane's first line stays there and the rest of
			// the log resumes beneath it. Bound in the raw log only:
			// nowhere else has a scope to drop, and a key that acts
			// invisibly elsewhere is worse than one that does nothing.
			//
			// notFound is dropped with it, for the reason invalidateRows
			// drops it on a filter change: a miss is cached against the
			// filter it searched under, and dropping the scope widens the
			// search domain exactly as a filter change does, so a miss
			// reported against one call would keep standing over a pane
			// that is now the whole log.
			if m.view == ViewRawLog {
				m.raw.scope = nil
				m.raw.notFound = false
			}
		case "?":
			m.showHelp = true
		case "s":
			m.cycleSort()
		case "f":
			m.toggleFacetFocus()
		default:
			// Key "3" (resource addresses) is not in viewKeys, so pressing
			// it lands here and does nothing -- it is unbound, not broken.
			if v, ok := viewKeys[msg.String()]; ok && v != m.view {
				m.setView(v)
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

// cycleSort is 's': it moves the sort to the next column of the table on
// screen, wrapping back round to the column the view's builder already ranks
// by after every column has had a turn.
//
// A view with no table -- the timeline, the raw log -- has no entry in
// tables, so the press does nothing there. That is the absence doing the
// work rather than a condition spelled out here: a view added with a table
// gets the sort by being listed in tables, and one added without a table
// cannot acquire a sort that reorders nothing.
//
// The selection is left on its index rather than followed to the row it was
// on. The table reordering under a fixed cursor is what every other thing
// that reorders this list does -- a facet toggle, a view switch -- and
// invalidateRows is what clamps it against the list it rebuilds.
func (m *Model) cycleSort() {
	t, ok := tables[m.view]
	if !ok {
		return
	}
	m.sortCol[m.view] = (m.sortCol[m.view] + 1) % len(t.cols)
	m.invalidateRows()
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

// setView switches the interface to v: it assigns the view, puts the
// selection back on the first row, and rebuilds the row cache -- in that
// order.
//
// The order is the reason this exists rather than three statements at each
// call site. invalidateRows rebuilds the cache before it returns, in order
// to clamp the selection against the list it rebuilds, so it builds for
// whichever view m.view names at the moment of the call: assign the view
// afterwards and the cache holds rows for the view being LEFT, which rows()
// then serves to the table and the detail pane. Held together here, the
// order cannot be stated wrongly by a caller.
//
// The selection does not travel across, because row 40 of one view is
// meaningless in another. It is remembered PER VIEW instead (viewSelected),
// so a view re-entered opens where the reader left it while a view entered
// for the first time opens at its top. The clamp inside invalidateRows then
// holds whatever is restored against the rows that are actually there,
// which is what makes a remembered row safe across a filter narrowed in the
// meantime -- the same clamp, for the same reason, as a list that shrank
// under the reader's feet.
//
// Any pending return is spent here. Reaching a view by its own key is the
// reader saying where they want to be, so the jump that put them somewhere
// else is no longer something to undo. The raw log's scope goes the same
// way, for the same reason: it too belongs to the jump that made it.
func (m *Model) setView(v View) {
	m.viewSelected[m.view] = m.selected
	m.view = v
	m.selected = m.viewSelected[v]
	m.hasReturn = false
	m.raw.scope = nil
	m.invalidateRows()
}

// returnFromJump puts the reader back where Enter took them from, and
// reports whether there was anywhere to go. The row comes back with the
// view, setView restoring that view's own cursor.
func (m *Model) returnFromJump() bool {
	if !m.hasReturn {
		return false
	}
	m.setView(m.returnTo)
	return true
}

// invalidateRows drops any cached rows so they are rebuilt from the current
// view and filter rather than served stale, and re-clamps the selection
// against the list it rebuilds.
//
// Clamping needs a row count, so this rebuilds the cache before it returns,
// for whichever view m.view names at the moment of the call. Everything
// that changes the VIEW goes through setView, which owns that ordering; the
// callers that reach this directly (a facet toggle, Esc) change the filter
// and leave the view where it is.
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
//
// The timeline's lane and span cursors are clamped here too, for the same
// reason the row selection is: a filter change can shrink or empty the lane
// the cursor was on, and clampTimelineSelection is what keeps it pointing
// at something that still exists.
//
// That clamp runs ONLY while the timeline is the view on screen, because
// clamping packs the lanes (timelineLanes), and packing is the one thing in
// this package that can fail: model.PackLanes refuses a mixed-fidelity
// slice and timelineLanes turns that refusal into a panic. Run from here
// unconditionally, a condition confined to view 5 would take down a facet
// toggle or a view switch in any view -- an alt-screen crash on a keystroke
// with nothing to do with the timeline, which is the outcome spanForRow
// argues against by name. Nothing is missed by waiting: every route INTO
// the timeline goes through setView, which sets m.view before calling this,
// so the clamp runs on arrival against the filter in force then.
func (m *Model) invalidateRows() {
	m.rowsCache = nil
	m.rowsCached = false
	m.timelineTierCache = tierNone
	m.timelineSpansCache = nil
	m.timelineSpansCached = false
	m.timelineLanesCache = nil
	m.timelineLanesCached = false
	m.timelineLabelsCache = nil
	m.timelineLabelWidthCache = 0
	m.timelineLabelsCached = false
	m.timelineWallClockCache = 0
	m.timelineWallClockCached = false
	m.raw.notFound = false
	m.clampSelection()
	if m.view == ViewTimeline {
		m.clampTimelineSelection()
	}
}

// moveCursor routes an up/down/j/k press to whichever pane has focus: the
// centre pane's own cursor, or the facet pane's value cursor. PaneDetail has
// no scrollable content of its own yet, so a press while it has focus is
// inert.
//
// What the centre pane's cursor IS depends on the view: the rollup and call
// views have a selected row, the raw log has none -- it renders from its top
// entry and nothing there reads the selection -- so in that view a press
// scrolls the pane instead, and the timeline has a lane cursor of its own
// (see timelineState), which up/down move between lanes rather than through
// rows.
func (m *Model) moveCursor(delta int) {
	switch m.pane {
	case PaneList:
		switch m.view {
		case ViewRawLog:
			m.scrollRawLog(delta)
		case ViewTimeline:
			m.moveTimelineLane(delta)
		default:
			m.moveSelection(delta)
		}
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
	applyColourPreference()
	m := New(l, path)
	p := tea.NewProgram(&m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
