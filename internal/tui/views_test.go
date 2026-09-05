package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// The ranking IS the providers view: a table holding the right numbers in
// the wrong order answers the question backwards. two-providers.log's google
// provider totals 8ms against aws's 5ms, so google must render above aws.
//
// The assertion is made against the centre pane alone. The facet pane lists
// the same two addresses in its own (count-descending) order, so a search of
// the whole composed view can be satisfied by a pane other than the table
// under test.
func TestProvidersViewRanksByTotalTime(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	if got := len(m.rows()); got != 2 {
		t.Fatalf("fixture assumption changed: %d provider rows, want 2 with different totals to rank", got)
	}

	// The pane is padded to the full height of the frame, so its trailing
	// blank lines are trimmed off before it is quoted in a failure.
	centre := strings.TrimRight(centrePaneOf(m.View()), " \n")
	google, googleLine := paneRowStartingWith(t, centre, "registry.terraform.io/hashicorp/google")
	aws, awsLine := paneRowStartingWith(t, centre, "registry.terraform.io/hashicorp/aws")
	if google[1] != "8ms" || aws[1] != "5ms" {
		t.Fatalf("fixture assumption changed: totals are google %q and aws %q, want 8ms against 5ms", google[1], aws[1])
	}
	if googleLine >= awsLine {
		t.Errorf("google (8ms) rendered on line %d, at or below aws (5ms) on line %d -- the rows are not ranked by total time:\n%s", googleLine, awsLine, centre)
	}
}

// The types view exists to put the two span tiers side by side for one
// resource type: RPC-tier calls measured in milliseconds against UI-hook
// resources Terraform quantised to whole seconds. two-tier.log is the only
// fixture carrying both tiers, and it carries all three join outcomes -- a
// type in both tiers, one in the RPC tier only, one in the UI tier only --
// so these figures pin that each cell comes from the tier its column is
// labelled with. A column header renders whether or not either tier reached
// the table, so the numbers are what has to be asserted.
//
// The assertion is made against the centre pane alone, at a width where no
// column of the types table is clipped: the facet pane lists the same
// resource types at its own width.
func TestTypesViewShowsBothTiers(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	centre := strings.TrimRight(centrePaneOf(m.View()), " \n")

	// Cells in typeColumns' order: resource type, UI res., UI total,
	// RPC calls, RPC total, RPC max.
	for _, want := range [][]string{
		{"aws_instance", "2", "5.0s", "2", "370ms", "250ms"}, // both tiers
		{"local_file", "1", "1.0s", "0", "0ms", "0ms"},       // UI tier only
		{"aws_subnet", "0", "0ms", "1", "40ms", "40ms"},      // RPC tier only
	} {
		got, _ := paneRowStartingWith(t, centre, want[0])
		if !slices.Equal(got, want) {
			t.Errorf("types row for %s = %v, want %v:\n%s", want[0], got, want, centre)
		}
	}
}

// paneRowStartingWith finds the one row of a rendered pane whose first cell
// is want, and reports its cells and which line of the pane it was on.
//
// Each line is stripped of its escape sequences before it is split into
// cells: the cursor bar wraps the selected row in escapes that occupy no
// display columns, but that strings.Fields would otherwise glue to the cells
// at either end of that row.
func paneRowStartingWith(t *testing.T, pane, want string) (cells []string, line int) {
	t.Helper()
	var scratch []byte
	for i, ln := range strings.Split(pane, "\n") {
		var plain string
		plain, scratch = logfmt.StripANSI(ln, scratch)
		if f := strings.Fields(plain); len(f) > 0 && f[0] == want {
			return f, i
		}
	}
	t.Fatalf("no row starting with %q in:\n%s", want, pane)
	return nil, 0
}

// UI-hook figures are whole seconds carrying up to a second of error each, so
// a view ranking them must say so, exactly as --profile does.
func TestTypesViewStatesUIHookResolution(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if !strings.Contains(m.renderList(120, 20), "whole seconds") {
		t.Error("types view does not state the UI-hook resolution")
	}
}

func TestCallsViewRanksByDurationAndCarriesSpanIndex(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	rows := m.rows()
	if len(rows) == 0 {
		t.Fatal("calls view has no rows")
	}
	for i, r := range rows {
		if r.spanIdx < 0 {
			t.Errorf("row %d has no span index; jump-to-log needs one", i)
		}
	}
	for i := 1; i < len(rows); i++ {
		a := m.log.RPCSpans[rows[i-1].spanIdx].DurationMs
		b := m.log.RPCSpans[rows[i].spanIdx].DurationMs
		if a < b {
			t.Errorf("rows not ranked by duration descending at %d: %d then %d", i, a, b)
		}
	}
}

// row carries both a span index and a rollup, and "exactly one of the two"
// was stated only in a doc comment. The constructors are what make it true:
// rollupRow derives the sentinel index rather than asking each caller to
// remember it, and a caller that forgot would leave a group row indexing
// whichever span sits at index 0 -- rendering that span's RPC, provider and
// duration as the GROUP's own figures, which is a wrong answer rather than
// a missing one.
func TestEveryRowIsExactlyACallOrARollup(t *testing.T) {
	for _, fixture := range []string{"two-tier.log", "two-providers.log", "provider-rpc.log", "mixed-hcp.log", "structured-ui.log"} {
		for _, key := range []rune{'1', '2', '4'} {
			m := update(t, New(testLog(t, fixture), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
			for i, r := range m.rows() {
				if r.isCall() == (r.rollup != nil) {
					t.Errorf("%s view %c row %d: isCall=%v with rollup=%v, want exactly one of the two", fixture, key, i, r.isCall(), r.rollup != nil)
				}
				if r.isCall() && r.spanIdx >= len(m.log.RPCSpans) {
					t.Errorf("%s view %c row %d: spanIdx %d is past the %d RPC spans it indexes", fixture, key, i, r.spanIdx, len(m.log.RPCSpans))
				}
			}
		}
	}
}

// A row that is neither -- no rollup, and a span index that names no span --
// must leave the pane saying it has nothing to describe. renderDetail
// reached RPCSpans through a comment asserting such a row could not exist,
// on the very line that indexed a slice with a field allowed to hold the
// sentinel; the index is out of range, so the frame panics inside the alt
// screen and takes the user's terminal with it.
//
// The rows are installed through the memoised cache, which is what rows()
// serves and what renderDetail reads, so this exercises the real render
// path rather than a function called with a hand-made argument.
func TestTheDetailPaneDegradesOnARowThatNamesNoSpan(t *testing.T) {
	for _, c := range []struct {
		name string
		r    row
	}{
		{"the sentinel index with no rollup", row{cells: []string{"x"}, spanIdx: noSpanIdx}},
		{"an index past the RPC spans", row{cells: []string{"x"}, spanIdx: 1 << 20}},
	} {
		m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
		m.rowsCache, m.rowsCached, m.selected = []row{c.r}, true, 0
		if got := detailBody(t, m, noSelectionTitle, 50, 20); got != noSelectionNote {
			t.Errorf("%s: detail pane = %q, want the placeholder %q", c.name, got, noSelectionNote)
		}
	}
}

// Enter and the detail pane must ask the same question of the same field.
// Enter jumps a row to the log entry that closed its span, which only a
// call row has; asked of the rollup field instead, the two could disagree
// about which rows carry one.
func TestEnterDoesNotJumpFromARollupRow(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if r := m.rows()[m.Selected()]; r.isCall() {
		t.Fatalf("fixture assumption changed: the selected providers row is a call, not a rollup: %+v", r)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ActiveView() != ViewProviders {
		t.Errorf("Enter on a rollup row switched to %v; a group resolves to no single entry to jump to", m.ActiveView())
	}
}

// RowCount is what clamps the selection, so it must be the active view's own
// row count and not a count borrowed from another slice.
//
// provider-rpc.log is the fixture because its numbers differ: its two RPC
// spans roll up to ONE provider row and to two type rows and two call rows,
// so a RowCount reporting the span count instead is visible here. In a
// single-span fixture every rollup happens to hold exactly one row, and the
// two are indistinguishable.
func TestRowCountMatchesRowsLength(t *testing.T) {
	for _, c := range []struct {
		key  rune
		view View
		want int
	}{
		{'1', ViewProviders, 1}, // both spans come from the one provider
		{'2', ViewTypes, 2},
		{'4', ViewCalls, 2},
	} {
		m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		if got := len(m.rows()); got != c.want {
			t.Fatalf("fixture assumption changed: view %v has %d rows, want %d", c.view, got, c.want)
		}
		if got := m.RowCount(); got != c.want {
			t.Errorf("view %v: RowCount() = %d, want %d", c.view, got, c.want)
		}
	}

	// The raw log has no rollup rows of its own -- it renders straight from
	// the entries -- so its count is the entry count, and that is what
	// clamps scrolling there.
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	if len(m.log.Entries) == 0 {
		t.Fatal("fixture assumption changed: no entries, so an entry count cannot be told from zero")
	}
	if got, want := m.RowCount(), len(m.log.Entries); got != want {
		t.Errorf("raw log: RowCount() = %d, want the %d entries it renders from", got, want)
	}
}

// A view narrower than its columns must degrade, not wrap into unreadable
// wreckage. This pins the part renderList itself owns: it never emits a
// line wider than it was given. Width is display columns:
// the selected row's ANSI escapes take up none, so counting their runes
// would flag a row that is exactly as wide as its neighbours.
func TestRenderListRespectsItsWidth(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	for _, line := range strings.Split(m.renderList(60, 20), "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("line exceeds the given width of 60: %q", line)
		}
	}
}

// Without a visible cursor bar, enter-to-jump is unusable -- the user
// cannot tell which row they are about to act on.
// New starts selection on row 0, so the header (line 0) must be plain and
// the first data row (line 1) must carry the highlight.
func TestRenderListHighlightsTheSelectedRow(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	lines := strings.Split(m.renderList(60, 20), "\n")
	if len(lines) < 2 {
		t.Fatalf("need a header and at least one data row, got %d lines", len(lines))
	}
	if strings.Contains(lines[0], "\x1b[7m") {
		t.Errorf("header row is highlighted:\n%s", lines[0])
	}
	if !strings.Contains(lines[1], "\x1b[7m") {
		t.Errorf("selected row (row 0) is not highlighted:\n%s", lines[1])
	}
	for i := 2; i < len(lines); i++ {
		if strings.Contains(lines[i], "\x1b[7m") {
			t.Errorf("line %d is highlighted but is not the selected row:\n%s", i, lines[i])
		}
	}
}

// A window pinned to the first h lines regardless of Selected() leaves a
// row selected past the first screenful off-screen. provider-rpc.log's two
// calls, rendered into a pane with room for only one data row, force the
// window to move.
//
// Row identity is checked via duration ("5ms"/"1ms"), not resource type:
// the calls view's text columns (RPC, resource type, provider) front-clip
// under a narrow pane like any identifier column, so a resource-type
// substring is not a safe way to tell the rows apart at width 60 --
// duration is numeric and never clipped.
func TestRenderListScrollsToKeepSelectionVisible(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if len(m.rows()) != 2 {
		t.Fatalf("fixture assumption changed: got %d calls, want 2", len(m.rows()))
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // select row 1
	out := m.renderList(60, 2)                                           // header + 1 data row: no room for row 0 as well
	if strings.Contains(out, "5ms") {
		t.Errorf("scrolled window still shows row 0, which should have scrolled off:\n%s", out)
	}
	if !strings.Contains(out, "1ms") {
		t.Errorf("selected row 1 scrolled off screen instead of row 0:\n%s", out)
	}
}

// Moving within the first screenful must not scroll: only once the
// selection would fall off the bottom of the window should it move. See
// TestRenderListScrollsToKeepSelectionVisible for why duration, not
// resource type, identifies the row.
func TestRenderListDoesNotScrollWithinTheFirstScreenful(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	out := m.renderList(60, 20) // room for both rows; selection (row 0) is already visible
	for _, want := range []string{"5ms", "1ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("both rows should be visible in a tall enough pane, missing %q:\n%s", want, out)
		}
	}
}

// A ranked view without its numbers ranks nothing, and an identifier cut
// from the wrong end names nothing: renderTable reserves every numeric
// column at its natural width and takes the shortfall out of the text
// columns, which front-clip. At a width too narrow for the provider
// column's long registry addresses to fit in full, the provider column
// must still show a recognisable, front-clipped tail, and every number
// must still be whole.
func TestRenderListFrontClipsTheTextColumnAndKeepsNumbersWhole(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	out := m.renderList(40, 20) // narrower than the provider column's natural width
	if !strings.Contains(out, "…") {
		t.Errorf("provider column was not front-clipped at width 40:\n%s", out)
	}
	for _, want := range []string{"8ms", "5ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("numeric column value %q missing at width 40 -- a ranked list with no ranking data:\n%s", want, out)
		}
	}
}

// A column header is prose -- it names the column -- so it must be end-clipped
// and told apart by its head, even in a column whose VALUES are front-clipped
// and told apart by their tails: front-clipping "resource type" the same way
// its values are would leave "…urce type", a word fragment as a column label.
//
// Both rows are checked together at one width, because the point is that
// the two clip in opposite directions in the same column: the width is
// chosen so neither fits whole.
func TestRenderTableEndClipsTheHeaderAndFrontClipsItsValues(t *testing.T) {
	cols := []column{
		{header: "resource type", kind: tailIdentifierColumn},
		{header: "n", kind: numericColumn},
	}
	data := []row{rollupRow([]string{"registry.terraform.io/hashicorp/aws", "1"}, nil)}

	lines := strings.Split(renderTable(nil, cols, data, "", -1, true, 12, 10), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want a header and one data row:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if want := "resource…  n"; lines[0] != want {
		t.Errorf("header row = %q, want %q -- a header must keep its head and mark the cut", lines[0], want)
	}
	if want := "…corp/aws  1"; lines[1] != want {
		t.Errorf("data row = %q, want %q -- an identifier value must keep its tail", lines[1], want)
	}
}

// clipWidth and padRight measure display columns, so the value taxonomy
// must too: a double-width rune is one rune but two columns, and a value
// "clipped to fit" by rune count still overruns the column it was clipped
// for. Invisible for ASCII identifiers, but the taxonomy and the safety net
// beneath it have to measure the same way or neither can be relied on.
func TestValueClipsMeasureDisplayColumns(t *testing.T) {
	const wide = "日本語テキスト" // 7 runes, 14 display columns
	if n := lipgloss.Width(wide); n != 14 {
		t.Fatalf("fixture assumption changed: %q is %d columns, want 14", wide, n)
	}
	for _, c := range []struct {
		name string
		got  string
		want int
	}{
		{"clipValueFront", clipValueFront(wide, 6), 6},
		{"clipValueEnd", clipValueEnd(wide, 6), 6},
		{"clipIdentifierField", clipIdentifierField("[ ] ", wide, "  12", 16, tailIdentifierColumn), 16},
	} {
		if n := lipgloss.Width(c.got); n > c.want {
			t.Errorf("%s returned %q, %d columns wide, want at most %d", c.name, c.got, n, c.want)
		}
	}
}

// A table narrowed to nothing renders as a preamble and a header with
// nothing beneath them, which is byte-identical to a parse failure or to
// having opened the wrong file. In a profiling tool a reader who cannot tell
// those apart draws a wrong conclusion, so the pane says which it is and
// names the key that undoes it.
//
// The assertion is made against the centre pane alone: the facet pane and
// the detail pane are unaffected by an empty table and would satisfy nothing
// here, while the footer's own key hints already name Esc.
func TestTheTableSaysWhenAFilterHasEmptiedIt(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	m = moveFacetCursorTo(t, m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = moveFacetCursorTo(t, m, dimType, "google_compute_instance")
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(m.rows()); got != 0 {
		t.Fatalf("fixture assumption changed: aws AND google_compute_instance left %d rows, want 0", got)
	}

	centre := centrePaneOf(m.View())
	for _, want := range []string{"nothing matches the filter", "Esc"} {
		if !strings.Contains(centre, want) {
			t.Errorf("an emptied table does not say %q, so it looks like a parse failure:\n%s", want, centre)
		}
	}
}

// The same emptiness with NO filter to blame must not accuse one: the
// providers view of a log carrying UI-hook spans only really does have
// nothing to rank, and telling the user to press Esc there sends them after
// a filter that was never set.
func TestATableWithNoRowsAndNoFilterDoesNotBlameAFilter(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	if len(m.log.RPCSpans) != 0 || len(m.log.UISpans) == 0 {
		t.Fatalf("fixture assumption changed: %d RPC and %d UI spans, want a UI-only log", len(m.log.RPCSpans), len(m.log.UISpans))
	}
	centre := centrePaneOf(m.View())
	if !strings.Contains(centre, "this view has no rows for this log") {
		t.Errorf("the providers view of a UI-only log says nothing about being empty:\n%s", centre)
	}
	if strings.Contains(centre, "nothing matches the filter") {
		t.Errorf("an unfiltered empty view blames a filter that was never set:\n%s", centre)
	}
}

// core-only.log is the most likely FIRST-RUN result: a capture taken without
// TF_LOG_PROVIDER=TRACE. Four empty panes and "0 RPC spans, 0 UI spans" say
// nothing about how to take a usable capture, where --diagnose has always
// answered exactly this. The interface reuses that wording rather than
// inventing a second phrasing of the same advice.
func TestALogWithNoSpansGetsCaptureGuidanceInsteadOfAnEmptyTable(t *testing.T) {
	m := update(t, New(testLog(t, "core-only.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	if len(m.log.RPCSpans) != 0 || len(m.log.UISpans) != 0 {
		t.Fatalf("fixture assumption changed: %d RPC and %d UI spans, want none of either", len(m.log.RPCSpans), len(m.log.UISpans))
	}

	centre := centrePaneOf(m.View())
	for _, want := range []string{
		"nothing to profile",    // internal/diagnose's EXTRACTION verdict
		"TF_LOG_PROVIDER=TRACE", // both gates, from writeRPCCaptureHint
		"TF_LOG_SDK_PROTO=TRACE",
		"debug logging on a run", // the HCP capture instruction
		"tfli --diagnose",        // how to check this file's structure
	} {
		if !strings.Contains(centre, want) {
			t.Errorf("capture guidance missing %q:\n%s", want, centre)
		}
	}
	if strings.Contains(centre, "total  calls  max") {
		t.Errorf("an empty providers table was rendered instead of the guidance:\n%s", centre)
	}
}
