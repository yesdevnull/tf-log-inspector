// Package tui is the terminal interface for tfli. It is the only package in
// this project permitted to import a third-party dependency: internal/model,
// internal/profile, internal/span, internal/logfmt and internal/diagnose
// remain dependency-free and terminal-unaware.
package tui

import (
	"fmt"
	"path/filepath"
	"strings"

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

// paneCount is how many values Pane has, so Tab can cycle through them with
// a single modulo rather than a switch that must be kept in sync by hand.
const paneCount = PaneDetail + 1

// Model is the bubbletea model for tfli's full-screen interface. It wraps a
// loaded log; nothing here mutates the log, so a Model can be freely copied
// the way bubbletea's value-receiver Update requires.
type Model struct {
	log  *model.Log
	name string

	view     View
	pane     Pane
	selected int

	// facets is built once from the whole log's RPC spans, so a value's
	// count always reflects the log, never the current filter -- a facet
	// pane where narrowing the filter also shrank the other options' counts
	// would make it hard to see what widening the filter again would show.
	facets []model.Facet
	// selectedFacets holds, per facet dimension name, the values the user
	// has toggled on. Nothing selected in a dimension means unconstrained;
	// filter() turns this into the model.Filter every view is built from.
	selectedFacets map[string]map[string]bool
	// facetCursor is the pane's highlighted value: which dimension (index
	// into facets) and which value within it space would toggle, and what
	// up/down/j/k move when the facet pane has focus.
	facetCursor facetCursor

	// rowsCache memoises rows() for the current view and filter. Recomputing
	// on every call would redo a full RollupBy/JoinByResourceType/sort on
	// every arrow-key press and, once View is wired into renderList, on
	// every render too. Anything that can change what rows() returns --
	// the view, the filter -- must invalidate this via invalidateRows.
	rowsCache  []row
	rowsCached bool

	// raw is the raw log view's own state: which entry sits at its top, and
	// any free-text search in progress or last run. See rawlog.go.
	raw rawLogState

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
	return Model{log: l, name: filepath.Base(path), pane: PaneList, facets: model.FacetsForSpans(l.RPCSpans)}
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
// Task 5 renders it directly from m.log.Entries -- so it is the one view
// counted separately rather than through rows().
func (m Model) RowCount() int {
	if m.view == ViewRawLog {
		return len(m.log.Entries)
	}
	return len(m.rows())
}

// Init starts no commands: the model has everything it needs from New, and
// nothing here depends on bubbletea's runtime to load.
func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// While a search query is being typed, every key is text for the
		// query rather than a command -- including keys bound elsewhere,
		// such as "j" or "q" -- so this is handled before anything else.
		if m.raw.searching {
			m = m.handleSearchKey(msg)
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
			m.pane = (m.pane + 1) % paneCount
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
					m = m.jumpToSpan(rows[m.selected].spanIdx)
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
			}
		case "n":
			if m.view == ViewRawLog {
				m.searchAgain(1)
			}
		case "N":
			if m.view == ViewRawLog {
				m.searchAgain(-1)
			}
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
	}
	return m, nil
}

// invalidateRows drops any cached rows so the next call to rows() rebuilds
// them from the current view and filter, rather than serving stale ones.
func (m *Model) invalidateRows() {
	m.rowsCache = nil
	m.rowsCached = false
}

// moveCursor routes an up/down/j/k press to whichever pane has focus: the
// list's row selection, or the facet pane's value cursor. PaneDetail has no
// scrollable content of its own yet, so a press while it has focus is inert.
func (m *Model) moveCursor(delta int) {
	switch m.pane {
	case PaneList:
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
	if max := m.RowCount() - 1; m.selected > max {
		m.selected = max
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

// View renders the header naming the file and its span counts, followed by
// the observer-effect caveat.
func (m Model) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tfli -- %s\n", m.name)
	fmt.Fprintf(&b, "%d RPC spans, %d UI spans\n\n", len(m.log.RPCSpans), len(m.log.UISpans))
	writeLoggingCaveat(&b)
	fmt.Fprintf(&b, "\nq to quit\n")
	return b.String()
}

// writeLoggingCaveat states that every duration this interface renders was
// measured under logging. Terraform re-logs each line of a provider's stderr
// through its own logger, so a provider that dumps HTTP bodies at DEBUG pays
// that cost per line: four captures of one workspace measured 24.1s with no
// logging enabled against 522.2s with debug plus provider TRACE. A reader
// who mistook these figures for wall-clock truth would be optimising time
// that does not exist without the log, so the caveat travels with every
// rendered duration rather than living only in documentation.
func writeLoggingCaveat(b *strings.Builder) {
	fmt.Fprintf(b, "Durations here are measured under logging, which is not\n")
	fmt.Fprintf(b, "free: one workspace planned in 24.1s unlogged and 522.2s\n")
	fmt.Fprintf(b, "with debug plus provider TRACE. Rankings hold, since every\n")
	fmt.Fprintf(b, "span paid the same cost, but absolute times do not transfer\n")
	fmt.Fprintf(b, "to an unlogged run.\n")
}

// Run opens the full-screen interface for l, loaded from path, and blocks
// until the user quits.
func Run(l *model.Log, path string) error {
	p := tea.NewProgram(New(l, path), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
