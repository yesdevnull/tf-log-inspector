package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// updateGolden regenerates testdata/golden when set. Never pass -update to
// make a failing test pass without first confirming the new output by eye --
// a golden that changes because behaviour changed needs a human deciding the
// new output is correct.
var updateGolden = flag.Bool("update", false, "update golden files in internal/tui/testdata/golden")

// compareGolden compares got against testdata/golden/<name>, or writes it
// there when -update is set.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v (run go test -update to create it, then read it before committing)", path, err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s (run go test -update to regenerate, then read it before committing):\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func TestLayoutDegradesByWidth(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, c := range []struct {
		width      int
		wantFacets bool
		wantDetail bool
	}{
		{160, true, true},
		{100, true, true},
		{99, false, true},
		{70, false, true},
		{69, false, false},
	} {
		m := update(t, base, tea.WindowSizeMsg{Width: c.width, Height: 40})
		out := m.View()
		if got := strings.Contains(out, "PROVIDERS"); got != c.wantFacets {
			t.Errorf("width %d: facet pane present = %v, want %v", c.width, got, c.wantFacets)
		}
		if got := strings.Contains(out, "SPAN DETAIL"); got != c.wantDetail {
			t.Errorf("width %d: detail pane present = %v, want %v", c.width, got, c.wantDetail)
		}
	}
}

// No rendered line may exceed the terminal width at any supported size, or
// the terminal wraps and the layout becomes wreckage.
//
// The measurement is display columns (lipgloss.Width), not runes: the
// selected row and the facet cursor carry ANSI escapes that occupy no
// columns at all, so a rune count both overstates their width and lets a
// row that is actually SHORT than the rest pass unnoticed. The companion
// check below pins that short case, which a per-line maximum cannot see.
func TestNoLineExceedsTerminalWidth(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, w := range []int{70, 100, 160} {
		m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
		for _, line := range strings.Split(m.View(), "\n") {
			if n := lipgloss.Width(line); n > w {
				t.Errorf("width %d: line of %d columns: %q", w, n, line)
			}
		}
	}
}

// Every line of the pane row must be the SAME width, not merely within the
// terminal width: the panes are composed by padding each one to its
// declared width and joining them with a separator, so a row one column
// short puts its │ separators a column to the left of every other row's.
// What this pins is joinPanes' own padding -- it re-pads every pane line to
// that pane's declared width, measured in display columns, so a pane whose
// content is short or whose line carries ANSI escapes still occupies its
// full share of the row.
func TestEveryPaneRowIsTheSameDisplayWidth(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, w := range []int{70, 100, 160} {
		m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
		for _, line := range strings.Split(m.View(), "\n") {
			if !strings.Contains(line, paneSep) {
				continue // header, caveat and footer are not pane rows
			}
			if n := lipgloss.Width(line); n != w {
				t.Errorf("width %d: pane row is %d columns, want %d: %q", w, n, w, line)
			}
		}
	}
}

// bubbletea's renderer keeps only the last h lines of what View returns
// (standardRenderer.flush: it cannot move the cursor into the terminal's
// scrollback buffer), so a view of h+1 lines -- one trailing newline is
// enough -- silently loses its FIRST line off the top of the screen. That
// line is the header naming the open file, so the defect is invisible to
// any test that asserts on View()'s text. This pins the invariant the
// renderer actually enforces instead.
func TestViewNeverEmitsMoreLinesThanTheTerminalHeight(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, h := range []int{60, 40, 24, 12, 11, 10, 5, 1} {
		m := update(t, base, tea.WindowSizeMsg{Width: 100, Height: h})
		view := m.View()
		if n := len(strings.Split(view, "\n")); n > h {
			t.Errorf("height %d: View() is %d lines, which loses its top %d:\n%s", h, n, n-h, view)
		}
		if !strings.HasPrefix(view, "tfli -- ") {
			t.Errorf("height %d: View() does not start with the header line:\n%s", h, view)
		}
	}
}

// Golden files lock the layout at the three widths the spec names. They use
// two-providers.log, not mixed-hcp.log: a golden commits whatever the view
// renders into the repository, and two-providers.log is wholly synthesised
// (its header says so) rather than drawn from a real capture, so there is
// nothing in it that could later turn out to be sensitive. Regenerate
// deliberately with -update, never to make a failure go away.
func TestGoldenLayouts(t *testing.T) {
	base := New(testLog(t, "two-providers.log"), "plan.log")
	for _, w := range []int{70, 100, 160} {
		m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
		compareGolden(t, fmt.Sprintf("layout-%d.txt", w), m.View())
	}
}

// Pressing 'f' below the facet-pane's inline width threshold must open it as
// an overlay AND give it the keyboard. The overlay exists precisely so
// facets are usable on a narrow terminal, and an overlay without focus is a
// column of checkboxes drawn with a faint cursor that space does nothing to,
// while j and k move a selection in the list it is covering.
//
// two-providers.log is used rather than mixed-hcp.log because it has a
// second provider to narrow away: selecting the only value a dimension has
// can never change a row count, so it could not tell a working space from
// an inert one.
func TestFTogglesTheFacetOverlayBelowInlineWidth(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 90, Height: 40})
	before := len(m.rows())
	if before < 2 {
		t.Fatalf("fixture assumption changed: %d provider rows, want at least 2 so a filter can be seen to narrow them", before)
	}
	if strings.Contains(m.View(), "PROVIDERS") {
		t.Fatalf("facets shown inline at width 90, want collapsed:\n%s", m.View())
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if !strings.Contains(m.View(), "PROVIDERS") {
		t.Errorf("'f' did not open the facet overlay:\n%s", m.View())
	}
	if m.Focus() != PaneFacets {
		t.Fatalf("focus = %v with the facet overlay open, want PaneFacets", m.Focus())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(m.rows()); got >= before {
		t.Errorf("space in the facet overlay left %d rows, want fewer than %d -- the overlay cannot be operated", got, before)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if strings.Contains(m.View(), "PROVIDERS") {
		t.Errorf("second 'f' did not close the facet overlay:\n%s", m.View())
	}
	if m.Focus() != PaneList {
		t.Errorf("focus = %v after closing the overlay, want it back on the list that is now on screen", m.Focus())
	}
}

// At or above facetInlineWidth the facet pane is already drawn, so 'f' must
// move focus onto it rather than set an overlay flag with no visible effect.
// A flag set there is dead state that pops an overlay open unasked the
// moment the terminal is narrowed.
func TestFFocusesTheFacetPaneAtInlineWidth(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.Focus() != PaneFacets {
		t.Errorf("focus = %v after 'f' at 160 columns, want the facet pane already on screen", m.Focus())
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 90, Height: 40})
	if strings.Contains(m.View(), "PROVIDERS") {
		t.Errorf("narrowing the terminal popped open a facet overlay the user never asked for:\n%s", m.View())
	}
}

// The facet overlay replaces the whole pane row, so it may only ever be
// drawn at a width that has no room for the facet pane inline. Drawn at 160
// columns it would blank the ranked list and the detail pane -- the two
// panes the answer actually lives in -- on a terminal with room for all
// three.
//
// Both routes to that state are pinned: pressing 'f' at 160 columns, and
// opening the overlay on a narrow terminal and then widening it, which
// leaves the flag set with no keypress in between.
func TestTheFacetOverlayIsNeverDrawnAtInlineWidth(t *testing.T) {
	wide := tea.WindowSizeMsg{Width: 160, Height: 40}
	for _, c := range []struct {
		name string
		open func(t *testing.T, m Model) Model
	}{
		{"pressing f at 160 columns", func(t *testing.T, m Model) Model {
			m = update(t, m, wide)
			return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		}},
		{"opening the overlay at 90 columns then widening", func(t *testing.T, m Model) Model {
			m = update(t, m, tea.WindowSizeMsg{Width: 90, Height: 40})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			return update(t, m, wide)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := c.open(t, New(testLog(t, "two-providers.log"), "x.log"))
			out := m.View()
			if !strings.Contains(out, "SPAN DETAIL") {
				t.Errorf("the detail pane is off screen at 160 columns:\n%s", out)
			}
			if centre := centrePaneOf(out); !strings.Contains(centre, "hashicorp/google") {
				t.Errorf("the ranked list is blank at 160 columns:\n%s", out)
			}
		})
	}
}

// Below detailInlineWidth the detail pane collapses too and the list is
// rendered ALONE at the full terminal width -- not at the narrower width it
// had beside a pane that is no longer drawn. The width is the whole point of
// collapsing the pane: at 69 columns the provider column has room for a
// registry address in full, which at 70 -- where the detail pane still takes
// its share of the row -- it does not.
func TestTheListTakesTheWholeRowOnceTheDetailPaneCollapses(t *testing.T) {
	const addr = "registry.terraform.io/hashicorp/google"
	base := New(testLog(t, "two-providers.log"), "x.log")

	const narrow = detailInlineWidth - 1
	m := update(t, base, tea.WindowSizeMsg{Width: narrow, Height: 40})
	if got := m.renderPanes(narrow, 10); !strings.Contains(got, addr) {
		t.Errorf("%q is still clipped at %d columns -- the list did not get the whole row once the detail pane collapsed:\n%s", addr, narrow, got)
	}
	m = update(t, base, tea.WindowSizeMsg{Width: detailInlineWidth, Height: 40})
	if got := m.renderPanes(detailInlineWidth, 10); strings.Contains(got, addr) {
		t.Fatalf("%q fits whole at %d columns beside the detail pane, so this no longer measures what the collapse hands back:\n%s", addr, detailInlineWidth, got)
	}
}

// The detail pane shows the selected call's RPC, provider and duration.
func TestDetailPaneShowsTheSelectedSpan(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	want := m.log.RPCSpans[m.rows()[0].spanIdx]
	out := m.renderDetail(80, 20) // wide enough that the full provider address is not clipped
	for _, s := range []string{want.RPC, want.Provider} {
		if !strings.Contains(out, s) {
			t.Errorf("detail pane missing %q:\n%s", s, out)
		}
	}
}

// A rollup row (ViewProviders, the default view) rolls up many spans, so
// there is no single span for the detail pane to describe. It must say so,
// rather than picking one span and presenting its fields as the selected
// row's: a pane showing one call's RPC and duration beside a row that
// totals several is a wrong answer, not a missing one.
//
// 40 columns is wide enough for the placeholder and for an unclipped RPC
// name, so both halves of the assertion measure what they say.
func TestDetailPaneOnARollupRowIsHonest(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "x.log")
	out := m.renderDetail(40, 20)
	if !strings.Contains(out, "(no call selected)") {
		t.Errorf("detail pane does not say a rollup row has no single span to show:\n%s", out)
	}
	if rpc := m.log.RPCSpans[0].RPC; strings.Contains(out, rpc) {
		t.Errorf("detail pane shows the span %q for a rollup row:\n%s", rpc, out)
	}
}

// spanDetailLines is unit-tested directly against a UI-hook span: no
// current view lets a user select one interactively -- row.spanIdx only
// ever indexes m.log.RPCSpans, and a UI-hook span lives only in
// m.log.UISpans -- but the formatting logic itself must be correct per the
// spec ("for a UI-hook span its unmasked address"), ready for whichever
// later task makes one selectable.
func TestSpanDetailLinesShowsAddressForUIHookSpans(t *testing.T) {
	s := span.Span{RPC: "create", Provider: "aws", DurationMs: 2500, Fidelity: span.FidelityUIReported, Address: "aws_instance.example"}
	out := strings.Join(spanDetailLines(s, 60), "\n")
	if !strings.Contains(out, "aws_instance.example") {
		t.Errorf("UI-hook span detail omits its address:\n%s", out)
	}
}

// An RPC-tier span never carries an address (Span.Address is populated only
// for UI-hook spans), so its detail must not show an address line at all.
func TestSpanDetailLinesOmitsAddressForRPCSpans(t *testing.T) {
	s := span.Span{RPC: "ApplyResourceChange", Provider: "aws", DurationMs: 5, Fidelity: span.FidelityReported}
	out := strings.Join(spanDetailLines(s, 60), "\n")
	if strings.Contains(out, "Addr") {
		t.Errorf("RPC-fidelity span detail shows an address line:\n%s", out)
	}
}

// 100 columns is the width Dan actually runs at, and it is where the two
// side panes and the centre pane compete hardest: give the side panes too
// much and the providers view's numeric columns are pushed out of the
// centre entirely. The composed view at 100 columns must show a
// front-clipped provider name in the centre pane AND all three of its
// numeric columns whole.
// Both assertions are made against the centre pane alone. The facet pane
// at 100 columns front-clips its own provider values, so a search of the
// whole composed view for an ellipsis is satisfied whatever the centre
// pane renders -- it cannot fail, and so cannot report anything.
func TestProvidersViewAt100ColumnsKeepsNumbersAndFrontClipsTheProvider(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "plan.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	centre := centrePaneOf(m.View())
	if !strings.Contains(centre, "…") {
		t.Errorf("centre pane's provider column was not front-clipped at 100 columns:\n%s", centre)
	}
	// "5ms      1  5ms" is aws's total, calls and max columns rendered
	// together in one row -- all three numeric values, whole, in the order
	// the table actually renders them.
	if !strings.Contains(centre, "5ms      1  5ms") {
		t.Errorf("providers view at 100 columns is missing one or more numeric columns:\n%s", centre)
	}
}

// The calls view has three text columns (RPC, resource type, provider), and
// every one of them has to be able to give way. A column left reserved at
// its full natural width while the row as a whole overflows is cut by the
// trailing clipWidth instead -- from the wrong end, since that keeps a
// value's head and two provider addresses diverge only in their tail, so
// two rows with different providers render the same text. A test asserting
// only that some provider text appears would not catch that, since the
// collided values are all non-empty: it must assert the two rows' provider
// text actually differs. two-providers.log's two calls have different
// providers (aws, google) for exactly this reason.
//
// The expected clipped text is computed via fitColumnWidths and
// clipValueFront themselves, the same functions renderTable calls, rather
// than a hand-picked width: this is a white-box check that the real
// column-width policy -- whatever it is today or becomes later -- still
// keeps two different providers apart, not a pinned guess at its output.
//
// The assertion is made against the centre pane alone. The facet pane and
// the detail pane both render provider addresses of their own, at their own
// widths, so a search of the whole composed view can be satisfied by a pane
// other than the one under test.
func TestCallsViewAt160ColumnsRendersDifferentProvidersDifferently(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "plan.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	rows := m.rows()
	if len(rows) != 2 {
		t.Fatalf("fixture assumption changed: got %d calls, want 2", len(rows))
	}
	aws, google := m.log.RPCSpans[rows[0].spanIdx].Provider, m.log.RPCSpans[rows[1].spanIdx].Provider
	if aws == google {
		t.Fatalf("fixture assumption changed: both calls have the same provider %q", aws)
	}

	centreWidth := layoutCentreWidth(m, 160)
	widths := fitColumnWidths(callColumns, columnWidths(callColumns, rows), centreWidth)
	providerWidth := widths[len(callColumns)-1] // provider is callColumns' last column
	if providerWidth >= lipgloss.Width(aws) && providerWidth >= lipgloss.Width(google) {
		t.Fatalf("provider column is %d display columns wide at 160 columns, wide enough for both addresses whole -- this test no longer exercises clipping", providerWidth)
	}
	awsWant, googleWant := clipValueFront(aws, providerWidth), clipValueFront(google, providerWidth)
	if awsWant == googleWant {
		t.Fatalf("front-clipped provider text collided at the computed width %d -- test assumption is wrong: %q", providerWidth, awsWant)
	}

	centre := centrePaneOf(m.View())
	if !strings.Contains(centre, awsWant) {
		t.Errorf("calls view at 160 columns is missing the aws provider's distinguishing text %q:\n%s", awsWant, centre)
	}
	if !strings.Contains(centre, googleWant) {
		t.Errorf("calls view at 160 columns is missing the google provider's distinguishing text %q:\n%s", googleWant, centre)
	}
}

// An RPC name is not distinguished by its tail: it names one of a closed
// set of plugin-protocol methods sharing long suffixes
// (...ResourceChange, ...ResourceConfig, ...ResourceState) that diverge at
// the HEAD, so the calls view's RPC column must end-clip. Front-clipping it
// collides names that differ: at 100 columns -- the width Dan runs at --
// the column renders 10 display columns wide, where a front-clip renders
// both PlanResourceChange and ApplyResourceChange as "…rceChange".
//
// Asserting only that some RPC text appears would not catch this, since a
// collided value is still non-empty: the two rows' RPC cells must be shown
// to differ. two-rpcs.log's two calls carry those two names, which share
// the 14-column tail "ResourceChange", for exactly this reason.
//
// The expected clipped text is computed via fitColumnWidths and
// clipValueEnd themselves, the same functions renderTable calls, rather
// than a hand-picked width -- the same white-box approach as the provider test
// above. The assertion is made against the centre pane alone, since the
// facet list and the detail pane both spell RPC names out in full and
// would satisfy a search of the whole composed view whatever the calls
// table rendered.
func TestCallsViewAt100ColumnsRendersDifferentRPCsDifferently(t *testing.T) {
	m := update(t, New(testLog(t, "two-rpcs.log"), "plan.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	rows := m.rows()
	if len(rows) != 2 {
		t.Fatalf("fixture assumption changed: got %d calls, want 2", len(rows))
	}
	first, second := m.log.RPCSpans[rows[0].spanIdx].RPC, m.log.RPCSpans[rows[1].spanIdx].RPC
	if first == second {
		t.Fatalf("fixture assumption changed: both calls have the same RPC %q", first)
	}

	centreWidth := layoutCentreWidth(m, 100)
	widths := fitColumnWidths(callColumns, columnWidths(callColumns, rows), centreWidth)
	rpcWidth := widths[1] // RPC is callColumns' second column, after duration
	if rpcWidth >= lipgloss.Width(first) && rpcWidth >= lipgloss.Width(second) {
		t.Fatalf("RPC column is %d display columns wide at 100 columns, wide enough for both names whole -- this test no longer exercises clipping", rpcWidth)
	}
	firstWant, secondWant := clipValueEnd(first, rpcWidth), clipValueEnd(second, rpcWidth)
	if firstWant == secondWant {
		t.Fatalf("clipped RPC text collided at the computed width %d: %q", rpcWidth, firstWant)
	}

	out := m.View()
	centre := centrePaneOf(out)
	if !strings.Contains(centre, firstWant) {
		t.Errorf("calls view at 100 columns is missing %q's distinguishing text %q:\n%s", first, firstWant, out)
	}
	if !strings.Contains(centre, secondWant) {
		t.Errorf("calls view at 100 columns is missing %q's distinguishing text %q:\n%s", second, secondWant, out)
	}
}

// centrePaneOf returns just the centre (list) pane's text from a composed
// view, one line per line of the input. Panes are joined with paneSep, so a
// line that carries every pane splits into a facet, centre and detail
// field; a line rendered outside the pane row (the header, the caveat, the
// key line) contributes nothing.
func centrePaneOf(view string) string {
	var centre []string
	for _, line := range strings.Split(view, "\n") {
		if fields := strings.Split(line, paneSep); len(fields) == 3 {
			centre = append(centre, fields[1])
		}
	}
	return strings.Join(centre, "\n")
}

// layoutCentreWidth returns the centre (list) pane's width for a terminal
// w columns wide, replicating renderPanes' own arithmetic for the
// w >= facetInlineWidth case this test renders at. Kept here rather than
// exported from layout.go, since nothing outside a test needs a pane width
// in isolation from actually rendering into it.
func layoutCentreWidth(m Model, w int) int {
	facetW := facetPaneWidth(m.facetPaneNatural, w)
	detailW := detailPaneWidth(m.detailPaneNatural, w)
	return w - facetW - detailW - 2*paneSepWidth
}

// minDetailPaneWidth exists so the detail pane always has room for its own
// placeholder. It is only a floor if it is applied after the quarter-of-the-
// terminal cap: at 70 columns -- one of the three widths the spec names --
// a quarter is 17, which renders "(no call selected" with the closing paren
// cut off, a pane too narrow to label itself.
func TestDetailPaneFitsItsPlaceholderAtTheNarrowestSupportedWidth(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "plan.log"), tea.WindowSizeMsg{Width: detailInlineWidth, Height: 40})
	if got := detailPaneWidth(m.detailPaneNatural, detailInlineWidth); got < minDetailPaneWidth {
		t.Errorf("detail pane is %d columns at width %d, below its own minimum of %d", got, detailInlineWidth, minDetailPaneWidth)
	}
	if !strings.Contains(m.View(), "(no call selected)") {
		t.Errorf("detail pane placeholder is cut off at %d columns:\n%s", detailInlineWidth, m.View())
	}
}

// Both side panes are sized from data that cannot change after New -- the
// log's RPC spans, and the facets built from them -- so the measurement
// belongs at load rather than in every frame: measuring the detail pane
// formats every RPC span in the log, and measuring the facet pane walks
// every value of every dimension, of which the resource type dimension
// alone runs to hundreds on a real capture.
//
// The stored measurements are checked against the functions that produce
// them, and then the render is checked to consult the measurement rather
// than the data: with the spans and facets taken away behind it, a pane
// that re-measured per frame would collapse to its minimum width and move
// the separators.
func TestSidePaneWidthsAreMeasuredAtLoad(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	if got, want := m.facetPaneNatural, facetNaturalWidth(m.facets); got != want {
		t.Errorf("facetPaneNatural = %d, want %d", got, want)
	}
	if got, want := m.detailPaneNatural, detailNaturalWidth(m.log.RPCSpans); got != want {
		t.Errorf("detailPaneNatural = %d, want %d", got, want)
	}

	before := paneSepColumns(t, m.View())
	m.log, m.facets = &model.Log{}, nil
	m.invalidateRows()
	if got := paneSepColumns(t, m.View()); !slices.Equal(got, before) {
		t.Errorf("pane separators moved to columns %v from %v -- a pane re-measured itself from the data at render time", got, before)
	}
}

// paneSepColumns reports which display columns the pane separators sit in,
// which is what the two side panes' widths determine. It reads the first
// pane row of a composed view; every row has its separators in the same
// columns (TestEveryPaneRowIsTheSameDisplayWidth).
func paneSepColumns(t *testing.T, view string) []int {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, paneSep) {
			continue
		}
		var cols []int
		for at, col := 0, 0; at < len(line); {
			if strings.HasPrefix(line[at:], paneSep) {
				cols = append(cols, col)
			}
			r := []rune(line[at:])[0]
			at += len(string(r))
			col += lipgloss.Width(string(r))
		}
		return cols
	}
	t.Fatalf("view has no pane row:\n%s", view)
	return nil
}

// The detail pane's RPC line is a render site of the same value taxonomy
// the facet pane and the calls table use, so the same RPC name clipped at
// all three must be marked as clipped at all three: an unmarked truncation
// reads as a complete name.
func TestSpanDetailMarksAClippedRPCName(t *testing.T) {
	s := span.Span{RPC: "ValidateResourceTypeConfig", Provider: "aws", DurationMs: 5}
	line := spanDetailLines(s, 20)[0]
	if strings.Contains(line, s.RPC) {
		t.Fatalf("the RPC name fits whole at width 20, so this no longer exercises clipping: %q", line)
	}
	if !strings.HasPrefix(line, "RPC   Valid") {
		t.Errorf("RPC detail line %q lost the head that distinguishes the name", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("RPC detail line %q is not marked as clipped, so it reads as a complete name", line)
	}
}

// A ranked table narrowed to twelve rows looks exactly like a log that only
// ever had twelve calls in it, and nothing else on screen says otherwise. So
// while a filter is active the header reports the matching count against the
// whole log's; with no filter it must read as the plain count it always did,
// since "2 of 2" on every frame is noise that says nothing.
func TestHeaderReportsFilteredCountsOnlyWhileAFilterIsActive(t *testing.T) {
	const unfiltered = "tfli -- x.log -- 2 RPC spans, 0 UI spans"
	m := New(testLog(t, "two-providers.log"), "x.log")
	if got := header(&m); got != unfiltered {
		t.Fatalf("fixture assumption changed: unfiltered header = %q, want %q", got, unfiltered)
	}

	// The provider dimension's first value is aws (values tie on count, so
	// they order by value ascending), which carries one of the two spans.
	m = moveFacetCursorTo(t, m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if want := "tfli -- x.log -- 1 of 2 RPC spans, 0 of 0 UI spans"; header(&m) != want {
		t.Errorf("header under an active filter = %q, want %q -- nothing on screen says the rankings are narrowed", header(&m), want)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := header(&m); got != unfiltered {
		t.Errorf("header after Esc = %q, want the unfiltered %q", got, unfiltered)
	}
}

// At a height that dropped the footer, '/' captured every keystroke with
// ZERO on-screen indication: the query invisible, 'q' no longer quitting,
// and "pattern not found" unable to be reported at all -- exactly the trap
// the Ctrl+C handling in handleSearchKey exists to escape. The footer is the
// only channel that state has, so it is budgeted ahead of the caveat and
// composed onto the end of an already-trimmed frame.
//
// The measurement is the LAST line, not merely that the prompt appears
// somewhere: a prompt trimmed away is invisible, and a prompt anywhere but
// the footer is not where the user is looking.
func TestTheSearchPromptSurvivesAShortTerminal(t *testing.T) {
	base := New(testLog(t, "provider-rpc.log"), "x.log")
	for _, h := range []int{12, 11, 10, 9, 7, 6, 5, 3, 2} {
		m := update(t, base, tea.WindowSizeMsg{Width: 100, Height: h})
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})

		lines := strings.Split(m.View(), "\n")
		if len(lines) > h {
			t.Errorf("height %d: View() is %d lines", h, len(lines))
		}
		if got := lines[len(lines)-1]; got != "/zz" {
			t.Errorf("height %d: last line is %q, want the search prompt %q -- '/' has taken the keyboard with nothing on screen to say so:\n%s", h, got, "/zz", m.View())
		}
	}
}

// The caveat gives way to the footer rather than the other way round, but it
// gives way by degrees: the full text where it fits, one whole sentence
// where it does not, and nothing at all only once even one line would cost
// the footer. A caveat cut off mid-sentence would read as a rendering fault
// rather than as a warning deliberately shortened, so the short form is a
// rewrite and not the first line of the long one.
func TestTheCaveatShortensBeforeItCostsTheFooter(t *testing.T) {
	base := New(testLog(t, "provider-rpc.log"), "x.log")
	for _, c := range []struct {
		h                 int
		wantFull, wantAny bool
	}{
		{40, true, true},
		{11, true, true},
		{10, false, true},
		{7, false, true},
		{6, false, false},
	} {
		m := update(t, base, tea.WindowSizeMsg{Width: 100, Height: c.h})
		view := m.View()
		if got := strings.Contains(view, "one workspace planned in 24.1s"); got != c.wantFull {
			t.Errorf("height %d: full caveat present = %v, want %v:\n%s", c.h, got, c.wantFull, view)
		}
		if got := strings.Contains(view, "under logging"); got != c.wantAny {
			t.Errorf("height %d: some caveat present = %v, want %v:\n%s", c.h, got, c.wantAny, view)
		}
		if !strings.Contains(view, "q quit") {
			t.Errorf("height %d: the footer was dropped for the caveat:\n%s", c.h, view)
		}
	}
}

// tallEntryLog builds a log whose single entry runs to n physical lines --
// the shape a provider's HTTP body dump takes in a real capture, and the one
// entry the raw log renders whatever its height (see renderRawLog). Every
// fixture in testdata is shorter than a pane, so none of them can tell a
// clamped pane row from an unclamped one.
func tallEntryLog(n int) *model.Log {
	var b strings.Builder
	b.WriteString("2026-09-04T10:00:00.000+1000 [TRACE] provider: HTTP Response Received\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "  http.response.body= body line %03d\n", i)
	}
	data := []byte(b.String())
	return &model.Log{
		Data:    data,
		Entries: []logfmt.Entry{{Off: 0, Len: uint32(len(data)), Lines: uint16(n), Timestamped: true}},
	}
}

// Below detailInlineWidth the centre pane is the whole row, with no
// joinPanes beneath it to hold it to the pane height. An entry taller than
// the pane then pushed the caveat out of View's frame entirely, which
// contradicts View's own account of what it always shows -- and it takes a
// realistic entry to do it, not a pathological one: one measured API
// response accounted for 49% of a 30MB log.
func TestTheNarrowLayoutClampsATallCentrePane(t *testing.T) {
	const h = 24
	m := update(t, New(tallEntryLog(60), "x.log"), tea.WindowSizeMsg{Width: detailInlineWidth - 1, Height: h})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})

	view := m.View()
	if strings.Contains(view, "SPAN DETAIL") {
		t.Fatalf("width %d still draws the detail pane, so this is not the single-pane branch:\n%s", detailInlineWidth-1, view)
	}
	if n := len(strings.Split(view, "\n")); n != h {
		t.Errorf("View() is %d lines at height %d", n, h)
	}
	if !strings.Contains(view, "Durations here are measured under logging") {
		t.Errorf("a tall raw-log entry pushed the caveat out of the frame:\n%s", view)
	}
	if !strings.Contains(view, "q quit") {
		t.Errorf("a tall raw-log entry pushed the footer out of the frame:\n%s", view)
	}
}
