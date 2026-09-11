package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
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
		{"local_file", "1", "1.0s", "0", "n/a", "n/a"},       // UI tier only
		{"aws_subnet", "0", "0s", "1", "40ms", "40ms"},       // RPC tier only
	} {
		got, _ := paneRowStartingWith(t, centre, want[0])
		if !slices.Equal(got, want) {
			t.Errorf("types row for %s = %v, want %v:\n%s", want[0], got, want, centre)
		}
	}
}

// Provider filters describe RPC evidence only. The UI rows remain observed
// facts even when their apparent provider differs from the selected RPC
// provider, while the RPC columns still narrow to the selected address.
func TestAProviderFacetLeavesTheUITierIndependent(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-provider-addrs.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	showOnly(t, &m, dimProvider, "registry.terraform.io/hashicorp/local")
	if !m.filterActive() {
		t.Fatal("unticking a provider did not activate a filter")
	}

	centre := strings.TrimRight(centrePaneOf(m.View()), " \n")
	// Cells in typeColumns' order: resource type, UI res., UI total,
	// RPC calls, RPC total, RPC max.
	for _, want := range [][]string{
		{"github_repository", "1", "4.0s", "0", "n/a", "n/a"},
		{"local_file", "1", "1.0s", "1", "900ms", "900ms"},
	} {
		got, _ := paneRowStartingWith(t, centre, want[0])
		if !slices.Equal(got, want) {
			t.Errorf("narrowed to the local RPC provider, types row for %s = %v, want %v:\n%s", want[0], got, want, centre)
		}
	}
	if !strings.Contains(centre, "whole seconds") {
		t.Errorf("the UI-hook resolution caveat went with the UI figures, so nothing on screen qualifies them:\n%s", centre)
	}
	if header := strings.SplitN(m.View(), "\n", 2)[0]; !strings.Contains(header, "2 of 2 UI spans") {
		t.Errorf("header = %q, want provider filtering to preserve both UI observations", header)
	}
}

// An empty provider allow-list admits no RPC evidence but still cannot erase
// observed UI operations, which carry no trustworthy provider relationship.
func TestUntickingEveryProviderEmptiesOnlyTheRPCTier(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-provider-addrs.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	if len(m.rows()) == 0 {
		t.Fatal("fixture assumption changed: the types view is already empty, so emptying it asserts nothing")
	}
	showOnly(t, &m, dimProvider)

	if got := m.rows(); len(got) != 2 {
		t.Errorf("every provider unticked lists %d types, want the two UI-only rows: %v", len(got), got)
	}
	if header := strings.SplitN(m.View(), "\n", 2)[0]; !strings.Contains(header, "0 of 2 RPC spans, 2 of 2 UI spans") {
		t.Errorf("header = %q, want provider filtering to empty RPCs and preserve UI", header)
	}
}

// An RPC facet is the same defect in the other dimension: "create" is what
// a UI-hook span calls its action and "ApplyResourceChange" is what the RPC
// tier calls its method, so neither checkbox can ever match the other tier.
func TestAnRPCFacetDoesNotZeroTheUITier(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	showOnly(t, &m, dimRPC, "ApplyResourceChange")

	centre := strings.TrimRight(centrePaneOf(m.View()), " \n")
	got, _ := paneRowStartingWith(t, centre, "aws_instance")
	want := []string{"aws_instance", "2", "5.0s", "1", "250ms", "250ms"}
	if !slices.Equal(got, want) {
		t.Errorf("narrowed to one RPC, types row for aws_instance = %v, want %v -- the RPC filter belongs to the RPC tier alone:\n%s", got, want, centre)
	}
}

// Resource type IS shared between the tiers -- it is why
// model.JoinByResourceType keys on it and on nothing else -- so narrowing to
// one must narrow BOTH tiers. Without this, "filter the UI tier by less" is
// satisfied by not filtering it at all.
func TestATypeFacetNarrowsBothTiers(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	showOnly(t, &m, dimType, "aws_instance")

	rows := m.rows()
	if len(rows) != 1 || rows[0].cells[0] != "aws_instance" {
		t.Fatalf("narrowing to resource type aws_instance left %d rows, want just that type: %v", len(rows), rows)
	}
	// local_file's UI-hook resource is the one the type filter has to
	// remove: it belongs to no RPC-tier span, so only the UI slice can
	// carry it.
	if header := strings.SplitN(m.View(), "\n", 2)[0]; !strings.Contains(header, "2 of 3 UI spans") {
		t.Errorf("header = %q, want the type filter to have narrowed the UI tier as well", header)
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
	cells, line, ok := findPaneRow(pane, want)
	if !ok {
		t.Fatalf("no row starting with %q in:\n%s", want, pane)
	}
	return cells, line
}

// findPaneRow is paneRowStartingWith without the assertion, for the callers
// that are checking a row is ABSENT -- a filter that removed it -- where a
// miss is the expected outcome rather than a failed fixture assumption.
func findPaneRow(pane, want string) (cells []string, line int, ok bool) {
	var scratch []byte
	for i, ln := range strings.Split(pane, "\n") {
		var plain string
		plain, scratch = logfmt.StripANSI(ln, scratch)
		if f := strings.Fields(plain); len(f) > 0 && f[0] == want {
			return f, i, true
		}
	}
	return nil, 0, false
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
	m := callsModel(t, "provider-rpc.log", "x.log")
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

// "Which call was slowest" is answered twice on one screen -- by the calls
// table's top row, and by the slowest-call field of every rollup pane --
// and the
// two must answer it the same way. The calls table breaks a duration tie by
// RPC name ascending, so a rollup that kept whichever equal call the log
// recorded FIRST would name one call while the table ranked another above
// it, both correct by their own rule and visibly contradicting each other.
//
// The two spans are built here rather than loaded, because the tie is the
// whole case: no fixture has two calls of equal duration in one group, and
// the earlier-logged one carries the alphabetically LATER name so that log
// order and the ranking rule disagree.
func TestTheSlowestCallAgreesWithTheCallsRanking(t *testing.T) {
	const provider = "registry.terraform.io/hashicorp/aws"
	spans := []span.Span{
		{RPC: "PlanResourceChange", Provider: provider, ResourceType: "aws_subnet", DurationMs: 25},
		{RPC: "ApplyResourceChange", Provider: provider, ResourceType: "aws_subnet", DurationMs: 25},
	}

	calls := callRows(spans, model.Filter{})
	if len(calls) != 2 {
		t.Fatalf("callRows returned %d rows, want 2", len(calls))
	}
	top := spans[calls[0].spanIdx].RPC
	if top != "ApplyResourceChange" {
		t.Fatalf("the calls view ranks %q first, so this no longer exercises a tie broken against log order", top)
	}

	for _, c := range []struct {
		name string
		rows []row
	}{
		{"providers", providerRows(spans)},
		{"types", typeRows(spans, nil)},
	} {
		if len(c.rows) != 1 {
			t.Fatalf("%s view returned %d rows, want the one group both spans fall in", c.name, len(c.rows))
		}
		slowest := c.rows[0].rollup.slowest
		if slowest == nil {
			t.Fatalf("%s view: the group names no slowest call", c.name)
		}
		if slowest.RPC != top {
			t.Errorf("%s view names %q slowest while the calls table ranks %q first -- one screen, two answers", c.name, slowest.RPC, top)
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
		m := callsModel(t, "provider-rpc.log", "x.log")
		m.rowsCache, m.rowsCached, m.selected = []row{c.r}, true, 0
		if got := detailBody(t, m, noSelectionTitle, 50, 20); got != noSelectionNote {
			t.Errorf("%s: detail pane = %q, want the placeholder %q", c.name, got, noSelectionNote)
		}
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
	m := callsModel(t, "mixed-hcp.log", "x.log")
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
	m := callsModel(t, "provider-rpc.log", "x.log")
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
	m := callsModel(t, "provider-rpc.log", "x.log")
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
	m := callsModel(t, "provider-rpc.log", "x.log")
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
	data := []row{rollupRow([]string{"registry.terraform.io/hashicorp/aws", "1"}, []uint64{0, 1}, nil)}

	// Sorted on the numeric column, so its header carries a marker and is
	// reserved a column wider than the bare "n" -- which is exactly the
	// column the identifier beside it loses. Measuring the marked header is
	// what keeps the two in step; see columnWidths.
	lines := strings.Split(unstyled(renderTable(nil, cols, 1, data, "", -1, true, 12, 10)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want a header and one data row:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if want := "resourc…  n▾"; lines[0] != want {
		t.Errorf("header row = %q, want %q -- a header must keep its head and mark the cut", lines[0], want)
	}
	if want := "…orp/aws   1"; lines[1] != want {
		t.Errorf("data row = %q, want %q -- an identifier value must keep its tail", lines[1], want)
	}
}

// clipValueForKind is where the columnKind taxonomy becomes a clip
// DIRECTION, and it has to answer for all three kinds. numericColumn's
// contract is that a number is never clipped by kind at all -- half a number
// tells the reader nothing -- and a fall-through that front-clipped one
// would cut it from the end that carries its magnitude, turning "742.4s"
// into a tail that reads as a smaller number rather than as a cut one.
//
// All three kinds are asserted together at one width too narrow for the
// value, so the function is pinned as a total mapping rather than as two
// cases and a default.
func TestClipValueForKindClipsEachKindFromItsOwnEnd(t *testing.T) {
	for _, c := range []struct {
		name  string
		value string
		kind  columnKind
		want  string
	}{
		{"a number is not clipped at all", "742.4s", numericColumn, "742.4s"},
		{"a tail-distinguished identifier keeps its tail", "hashicorp/aws", tailIdentifierColumn, "…/aws"},
		{"a head-distinguished identifier keeps its head", "ApplyResourceChange", headIdentifierColumn, "Appl…"},
	} {
		if got := clipValueForKind(c.value, 5, c.kind); got != c.want {
			t.Errorf("%s: clipValueForKind(%q, 5) = %q, want %q", c.name, c.value, got, c.want)
		}
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

// The same emptiness with NO filter to blame must not accuse one: the calls
// view of a log carrying UI-hook spans only really does have nothing to
// rank -- every call row is an RPC-tier span and there are none -- and
// telling the user to press Esc there sends them after a filter that was
// never set.
func TestATableWithNoRowsAndNoFilterDoesNotBlameAFilter(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if len(m.log.RPCSpans) != 0 || len(m.log.UISpans) == 0 {
		t.Fatalf("fixture assumption changed: %d RPC and %d UI spans, want a UI-only log", len(m.log.RPCSpans), len(m.log.UISpans))
	}
	centre := centrePaneOf(m.View())
	if !strings.Contains(centre, "this view has no rows for this log") {
		t.Errorf("the calls view of a UI-only log says nothing about being empty:\n%s", centre)
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
		"No supported timing observations",
		"Plain-text CLI timings are not parsed",
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

func TestCompactCaptureGuidanceExplainsUnsupportedTiming(t *testing.T) {
	m := New(testLog(t, "core-only.log"), "x.log")
	// Available list space in the 80-by-24 terminal layout.
	text := strings.Join(strings.Fields(unstyled(m.renderList(52, 18))), " ")
	for _, want := range []string{"No supported timing observations", "Plain-text CLI timings are not parsed", "terraform.ui", "TF_LOG_PROVIDER=TRACE", "TF_LOG_SDK_PROTO=TRACE", "Raw Log: press 6"} {
		if !strings.Contains(text, want) {
			t.Errorf("compact guidance missing %q: %s", want, text)
		}
	}
}

// sortKey is the 's' press, spelled once: every sort test sends it several
// times over and the literal is long enough to bury what the test is doing.
var sortKey = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}

// The sort cycle is PER VIEW because a column index means a different column
// in each table: column 2 is "calls" among providers and "resource type"
// among calls. Cycling through one view's columns and carrying the index
// into another would sort by whatever column happened to sit at that index
// there, which is not a thing the user asked for.
//
// Every stop is asserted distinct rather than just counted, because a cycle
// that revisits a column before it has shown them all leaves columns the
// user cannot reach at all -- the failure a modulus off by one produces, and
// one a bare "returns to the start after N presses" assertion passes over.
func TestSortCycleVisitsEveryColumnAndReturnsToTheDefault(t *testing.T) {
	for _, tc := range []struct {
		view    View
		key     rune
		fixture string
	}{
		{ViewProviders, '1', "two-providers.log"},
		{ViewTypes, '2', "two-tier.log"},
		{ViewCalls, '4', "provider-rpc.log"},
	} {
		t.Run(viewTitle(tc.view), func(t *testing.T) {
			m := update(t, New(testLog(t, tc.fixture), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			cols := tables[tc.view].cols
			start := m.sortCol[tc.view]
			if want := tables[tc.view].defaultCol; start != want {
				t.Fatalf("a fresh model sorts by column %d, want the column the builder already ranks by (%d)", start, want)
			}
			seen := map[int]bool{start: true}
			for press := 1; press < len(cols); press++ {
				m = update(t, m, sortKey)
				got := m.sortCol[tc.view]
				if got < 0 || got >= len(cols) {
					t.Fatalf("press %d put the sort on column %d, outside this table's %d columns", press, got, len(cols))
				}
				if seen[got] {
					t.Fatalf("press %d returned to column %d before every column had a turn -- %d of %d columns are unreachable", press, got, len(cols)-len(seen), len(cols))
				}
				seen[got] = true
			}
			if m = update(t, m, sortKey); m.sortCol[tc.view] != start {
				t.Errorf("after %d presses the sort is on column %d, want it wrapped back to %d", len(cols), m.sortCol[tc.view], start)
			}
		})
	}
}

// An identifier column sorts ASCENDING -- alphabetically, the order a reader
// scans a list of names in -- where a numeric column sorts descending. The
// direction is not a separate choice the user makes: it falls out of the
// column's kind, so there is no second key for it.
//
// provider-rpc.log is what tells the two orders apart. Its two calls rank
// aws_subnet (5ms) above aws_internet_gateway (1ms) by duration, which is
// the exact reverse of their alphabetical order, so a sort that quietly did
// nothing would fail here rather than pass by coincidence.
func TestSortingByAnIdentifierColumnOrdersItAscending(t *testing.T) {
	m := update(t, callsModel(t, "provider-rpc.log", "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	_, gateway, _ := findPaneRow(centrePaneOf(m.View()), "1ms")
	_, subnet, _ := findPaneRow(centrePaneOf(m.View()), "5ms")
	if gateway <= subnet {
		t.Fatalf("fixture assumption changed: the 1ms call already sorts above the 5ms one by duration, so this test cannot tell a reversal from a no-op")
	}

	// callColumns is duration, RPC, resource type, provider: two presses
	// from the duration default lands on resource type.
	m = update(t, update(t, m, sortKey), sortKey)
	if got := callColumns[m.sortCol[ViewCalls]].header; got != "resource type" {
		t.Fatalf("two presses landed the sort on %q, want resource type", got)
	}

	centre := strings.TrimRight(centrePaneOf(m.View()), " \n")
	_, gatewayLine := paneRowStartingWith(t, centre, "1ms")
	_, subnetLine := paneRowStartingWith(t, centre, "5ms")
	if gatewayLine >= subnetLine {
		t.Errorf("aws_internet_gateway rendered on line %d, at or below aws_subnet on line %d -- sorting by resource type did not order it ascending:\n%s", gatewayLine, subnetLine, centre)
	}
}

// A numeric column sorts DESCENDING, biggest first, because that is what
// every ranked view in this tool means by an order: the slowest thing is the
// thing worth looking at.
//
// two-tier.log tells this apart from the default. Ranked by UI total the
// order is aws_instance (5s), local_file (1s), aws_subnet (none); ranked by
// RPC total it is aws_instance (370ms), aws_subnet (40ms), local_file
// (none). local_file and aws_subnet swap, so the assertion fails if the
// press left the table on its default ranking.
func TestSortingByANumericColumnOrdersItDescending(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	_, subnet, _ := findPaneRow(centrePaneOf(m.View()), "aws_subnet")
	_, local, _ := findPaneRow(centrePaneOf(m.View()), "local_file")
	if subnet <= local {
		t.Fatalf("fixture assumption changed: aws_subnet already ranks above local_file by UI total, so this test cannot tell a re-sort from the default")
	}

	// typeColumns is resource type, UI res., UI total, RPC calls, RPC total,
	// RPC max: two presses from the UI total default lands on RPC total.
	m = update(t, update(t, m, sortKey), sortKey)
	if got := typeColumns[m.sortCol[ViewTypes]].header; got != "RPC total" {
		t.Fatalf("two presses landed the sort on %q, want RPC total", got)
	}

	centre := strings.TrimRight(centrePaneOf(m.View()), " \n")
	_, subnetLine := paneRowStartingWith(t, centre, "aws_subnet")
	_, localLine := paneRowStartingWith(t, centre, "local_file")
	if subnetLine >= localLine {
		t.Errorf("aws_subnet (40ms of RPC) rendered on line %d, at or below local_file (none) on line %d -- sorting by RPC total did not order it descending:\n%s", subnetLine, localLine, centre)
	}
}

// While the sort sits on the column a view's builder already ranks by, the
// builder's order is served UNTOUCHED rather than re-sorted into an
// equivalent one. The two are not equivalent: each builder breaks ties its
// own way -- model.JoinByResourceType breaks a UI-total tie by RPC total,
// and rankedBefore breaks a duration tie by RPC name -- and a generic
// re-sort by one column knows none of that.
//
// It matters beyond tidiness for the calls view, where rankedBefore is also
// what each rollup group's slowest call is chosen by: re-sort the table by a
// different tie-break and the detail pane can name one call as a group's
// slowest while the table ranks another above it, which is the disagreement
// rankedBefore exists as a single rule to prevent.
//
// tied-ui-totals.log is the only fixture that can observe this. Both its
// types total 2s of UI-hook time, so the join's tie-break decides the order
// (aws_zulu first, on 500ms of RPC against 10ms) and ranking by UI total
// alone would put aws_alpha first on its name.
func TestTheDefaultSortServesTheBuildersOrderRatherThanReSortingIt(t *testing.T) {
	m := update(t, New(testLog(t, "tied-ui-totals.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	centre := strings.TrimRight(centrePaneOf(m.View()), " \n")

	alpha, alphaLine := paneRowStartingWith(t, centre, "aws_alpha")
	zulu, zuluLine := paneRowStartingWith(t, centre, "aws_zulu")
	if alpha[2] != zulu[2] {
		t.Fatalf("fixture assumption changed: UI totals are %q and %q, want them tied so the join's tie-break decides the order", alpha[2], zulu[2])
	}
	if zuluLine >= alphaLine {
		t.Errorf("aws_zulu (500ms of RPC) rendered on line %d, at or below aws_alpha (10ms) on line %d -- the table was re-sorted by UI total and broke the tie by name, discarding the join's own tie-break:\n%s", zuluLine, alphaLine, centre)
	}
}

// A sort the reader cannot see is a keystroke that reorders the table and
// accounts for nothing. The marker names the sorted column in the place a
// reader already looks to find out what a column means, and it is present
// from the first frame: the default ranking is a sort too, and was
// unstated before this.
//
// Exactly one marker, because two would name two sorted columns and the
// table has one.
func TestTheSortedColumnIsMarkedInTheHeader(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})

	for press := 0; press < len(providerColumns); press++ {
		header := tableHeaderOf(t, centrePaneOf(m.View()))
		if got := strings.Count(header, sortDescMark) + strings.Count(header, sortAscMark); got != 1 {
			t.Fatalf("press %d: header carries %d sort markers, want exactly one:\n%s", press, got, header)
		}
		col := providerColumns[m.sortCol[ViewProviders]]
		want := col.header + sortDescMark
		if col.kind != numericColumn {
			want = col.header + sortAscMark
		}
		if !strings.Contains(header, want) {
			t.Errorf("press %d: sort is on %q but the header does not carry %q:\n%s", press, col.header, want, header)
		}
		m = update(t, m, sortKey)
	}
}

// The timeline and the raw log have no rows and no columns, so there is
// nothing for a sort to reorder. Pressing s there must do nothing at all --
// not panic reaching for a column set that does not exist, and not quietly
// advance a counter that would then apply itself the next time a table view
// came up.
func TestSortIsInertInTheViewsWithNoTable(t *testing.T) {
	for _, tc := range []struct {
		view View
		key  rune
	}{
		{ViewTimeline, '5'},
		{ViewRawLog, '6'},
	} {
		t.Run(viewTitle(tc.view), func(t *testing.T) {
			m := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			before := m.sortCol
			after := update(t, m, sortKey)
			if after.sortCol != before {
				t.Errorf("s in %s moved the sort from %v to %v, but this view has no table to sort", viewTitle(tc.view), before, after.sortCol)
			}
		})
	}
}

// tableHeaderOf returns the centre pane's column-header line: the first line
// of the pane that is not part of a preamble. The types view carries a
// two-line preamble about UI-hook resolution, so the header is not reliably
// the first line, and every table's header ends with a column name rather
// than a sentence.
func tableHeaderOf(t *testing.T, centre string) string {
	t.Helper()
	for _, ln := range strings.Split(centre, "\n") {
		plain, _ := logfmt.StripANSI(ln, nil)
		for _, c := range append(append(append([]column(nil), providerColumns...), typeColumns...), callColumns...) {
			if strings.Contains(plain, c.header) && !strings.Contains(plain, "whole seconds") {
				return strings.TrimRight(plain, " ")
			}
		}
	}
	t.Fatalf("no column header line in:\n%s", centre)
	return ""
}

// rankedBefore cannot separate two calls of the same RPC name at the same
// duration, and on a real capture that is the ordinary case rather than a
// corner -- a plan makes thousands of ApplyResourceChange calls and many
// land on the same millisecond. What orders those is the STABILITY of the
// sort applied over it, so ties hold the order the log recorded them in.
//
// The shape matters. Spans that are ALL equal go down sort.Slice's
// equal-elements fast path, where an unstable sort is indistinguishable
// from a stable one; so does any input under pdqsort's insertion-sort
// cutoff. Several duration groups with ties inside each is what actually
// separates them, and is also what a real capture looks like.
//
// The second half is the consequence a reader sees: removing one span
// reshuffles rows the removal did not touch, which is what a facet toggle
// does. The header reports "12 of 3184 RPC spans", so the reader knows the
// SET changed -- nothing accounts for rows moving that the filter kept.
func TestCallRowsResolvesTiesByLogOrderRatherThanBySortInternals(t *testing.T) {
	const groups, perGroup = 8, 5
	spans := make([]span.Span, 0, groups*perGroup)
	for i := 0; i < groups*perGroup; i++ {
		spans = append(spans, span.Span{
			Entry:      uint32(i),
			DurationMs: uint32(i%groups) + 1,
			RPC:        "ApplyResourceChange",
			Provider:   "registry.terraform.io/hashicorp/aws",
		})
	}

	full := callRows(spans, model.Filter{})
	if len(full) != len(spans) {
		t.Fatalf("got %d rows for %d spans", len(full), len(spans))
	}
	for i := 1; i < len(full); i++ {
		prev, cur := spans[full[i-1].spanIdx], spans[full[i].spanIdx]
		if prev.DurationMs != cur.DurationMs {
			continue // a boundary between duration groups
		}
		if full[i].spanIdx < full[i-1].spanIdx {
			t.Fatalf("rows %d and %d are both %dms but carry spans %d then %d -- ties are being resolved by the sort's internals rather than by log order",
				i-1, i, cur.DurationMs, full[i-1].spanIdx, full[i].spanIdx)
		}
	}

	// Drop the slowest span. Every surviving row is untouched by that
	// removal, so every surviving row must hold its place relative to the
	// others.
	slowest := full[0].spanIdx
	kept := append(append([]span.Span{}, spans[:slowest]...), spans[slowest+1:]...)
	narrowed := callRows(kept, model.Filter{})
	var want []uint32
	for _, r := range full[1:] {
		want = append(want, spans[r.spanIdx].Entry)
	}
	for i, r := range narrowed {
		if got := kept[r.spanIdx].Entry; got != want[i] {
			t.Errorf("after removing an unrelated span, row %d carries entry %d, want %d -- the filter reshuffled rows it did not touch", i, got, want[i])
		}
	}
}

// The tie-break is what sortRows spends most of its doc comment on, and
// nothing exercised it: replacing the whole thing with "return false"
// passed every package. It is tested here as a pure function over
// hand-built rows, because reaching a tie through a log fixture needs two
// rows equal in the sort column and different in column 0, which no fixture
// has.
func TestSortRowsBreaksTiesOnTheFirstColumn(t *testing.T) {
	// typeColumns: column 0 is the resource type, an identifier, so a tie
	// in a numeric column falls to it ASCENDING.
	tied := []row{
		rollupRow([]string{"aws_zulu", "1", "1s", "2", "40ms", "20ms"}, []uint64{0, 1, 1000, 2, 40, 20}, nil),
		rollupRow([]string{"aws_alpha", "1", "1s", "2", "90ms", "50ms"}, []uint64{0, 1, 1000, 2, 90, 50}, nil),
	}
	sortRows(typeColumns, tied, 3) // RPC calls: both 2
	if got := tied[0].cells[0]; got != "aws_alpha" {
		t.Errorf("a tie in a numeric column put %q first, want aws_alpha -- ties must fall to column 0, ascending for an identifier", got)
	}

	// callColumns: column 0 is duration, a number, so a tie in an
	// identifier column falls to it DESCENDING -- the slowest call of each
	// RPC name first, which is what the calls view means by an order.
	calls := []row{
		callRow([]string{"5ms", "ApplyResourceChange", "aws_subnet", "p"}, []uint64{5, 0, 0, 0}, 0),
		callRow([]string{"90ms", "ApplyResourceChange", "aws_vpc", "p"}, []uint64{90, 0, 0, 0}, 1),
	}
	sortRows(callColumns, calls, 1) // RPC: both ApplyResourceChange
	if got := calls[0].cells[0]; got != "90ms" {
		t.Errorf("a tie in an identifier column put %q first, want 90ms -- ties must fall to column 0, descending for a number", got)
	}

	// Sorting BY column 0 has no tie-break to fall to, so rows it cannot
	// separate hold the order they arrived in.
	equal := []row{
		callRow([]string{"5ms", "A", "t", "p"}, []uint64{5, 0, 0, 0}, 7),
		callRow([]string{"5ms", "A", "t", "p"}, []uint64{5, 0, 0, 0}, 3),
	}
	sortRows(callColumns, equal, 0)
	if equal[0].spanIdx != 7 || equal[1].spanIdx != 3 {
		t.Errorf("rows equal in every column were reordered (%d, %d), want their arrival order (7, 3) -- the sort is not stable", equal[0].spanIdx, equal[1].spanIdx)
	}
}

// Every numeric cell must be the RENDERING of the number recorded beside
// it. row.numeric is a slice parallel to row.cells, written as a SECOND
// literal at each builder, and nothing about the two literals makes their
// order agree -- so a number in the wrong slot sorts one column by another
// column's figure, under a header marker naming the column the reader asked
// for. That is a plausible number attached to the wrong noun, arriving
// through the sort.
//
// Length equality alone does not catch it: the two figures swapped are both
// uint64 and both present. This compares VALUES for that reason.
func TestEveryNumericCellRendersTheNumberRecordedBesideIt(t *testing.T) {
	for _, tc := range []struct {
		view    View
		key     rune
		fixture string
	}{
		{ViewProviders, '1', "two-providers.log"},
		{ViewTypes, '2', "two-tier.log"},
		{ViewCalls, '4', "provider-rpc.log"},
	} {
		t.Run(viewTitle(tc.view), func(t *testing.T) {
			m := update(t, New(testLog(t, tc.fixture), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			cols := tables[tc.view].cols
			rows := m.rows()
			if len(rows) == 0 {
				t.Fatalf("fixture assumption changed: no rows, so this checks nothing")
			}
			for r, rw := range rows {
				if len(rw.cells) != len(cols) || len(rw.numeric) != len(cols) {
					t.Fatalf("row %d has %d cells and %d numbers against %d columns", r, len(rw.cells), len(rw.numeric), len(cols))
				}
				for i, c := range cols {
					if c.kind != numericColumn {
						continue
					}
					if tc.view == ViewTypes && (c.header == "RPC total" || c.header == "RPC max") && rw.numeric[3] == 0 {
						if rw.cells[i] != "n/a" {
							t.Errorf("unobserved RPC duration displayed as %q", rw.cells[i])
						}
						continue
					}
					// The two renderings the builders use: a duration
					// through formatMs, a count through strconv.
					if got := rw.cells[i]; got != formatMs(rw.numeric[i]) && got != strconv.FormatUint(rw.numeric[i], 10) {
						t.Errorf("row %d column %q shows %q, which is neither rendering of the %d recorded beside it -- the number is in the wrong slot", r, c.header, got, rw.numeric[i])
					}
				}
			}
		})
	}
}

// tableBinding.defaultCol asserts a fact about ANOTHER function -- that the
// row builder already ranks by that column -- which no type can enforce. It
// is checked here directly: the builder's own output must never step
// backwards in defaultCol's direction.
//
// Ties are allowed, because the tie-breaks are exactly what differ between
// the builders and are what the default sort exists to preserve.
//
// The spans are built here rather than loaded, because a fixture only
// discriminates if ranking by the DEFAULT column disagrees with ranking by
// every other column, and the repo's fixtures rank the same either way --
// two-providers.log orders providers identically by total, by max and by
// call count, so a wrong defaultCol stayed monotonic and the check passed.
// These are shaped so each view's default order is the only order that
// holds.
func TestEveryDefaultSortColumnNamesTheOrderItsBuilderProduces(t *testing.T) {
	rpc := func(rpcName, provider, resourceType string, ms uint32, entry uint32) span.Span {
		return span.Span{Entry: entry, DurationMs: ms, RPC: rpcName, Provider: provider, ResourceType: resourceType}
	}
	// aws: three 10ms calls (total 30, max 10). google: one 20ms call
	// (total 20, max 20). Ranking by total puts aws first; by max or by
	// call count it would be google.
	providerSpans := []span.Span{
		rpc("A", "aws", "aws_subnet", 10, 0),
		rpc("A", "aws", "aws_subnet", 10, 1),
		rpc("A", "aws", "aws_subnet", 10, 2),
		rpc("A", "google", "google_vm", 20, 3),
	}
	// alpha: 1s of UI, 500ms of RPC. zulu: 3s of UI, 10ms of RPC. Ranking
	// by UI total puts zulu first; by any RPC column it would be alpha.
	typeRPC := []span.Span{rpc("A", "aws", "alpha", 500, 0), rpc("A", "aws", "zulu", 10, 1)}
	typeUI := []span.Span{
		{Entry: 2, DurationMs: 1000, ResourceType: "alpha", Fidelity: span.FidelityUIReported},
		{Entry: 3, DurationMs: 3000, ResourceType: "zulu", Fidelity: span.FidelityUIReported},
	}
	// Duration desc puts the 9ms call first; every identifier column would
	// put the other one there.
	callSpans := []span.Span{rpc("AApply", "aws", "a_type", 5, 0), rpc("ZApply", "zed", "z_type", 9, 1)}

	for _, tc := range []struct {
		view View
		rows []row
	}{
		{ViewProviders, providerRows(providerSpans)},
		{ViewTypes, typeRows(typeRPC, typeUI)},
		{ViewCalls, callRows(callSpans, model.Filter{})},
	} {
		t.Run(viewTitle(tc.view), func(t *testing.T) {
			b := tables[tc.view]
			if len(tc.rows) < 2 {
				t.Fatalf("built %d rows, want at least two to compare", len(tc.rows))
			}
			// Every OTHER column must disagree with the default, or a wrong
			// defaultCol would still look monotonic and this would pass.
			disagrees := false
			for c := range b.cols {
				if c == b.defaultCol {
					continue
				}
				if less, decided := compareCell(b.cols[c].kind, tc.rows[1], tc.rows[0], c); decided && less {
					disagrees = true
				}
			}
			if !disagrees {
				t.Fatalf("no column disagrees with column %d's order, so a wrong defaultCol would pass this unnoticed", b.defaultCol)
			}

			for i := 1; i < len(tc.rows); i++ {
				if less, decided := compareCell(b.cols[b.defaultCol].kind, tc.rows[i], tc.rows[i-1], b.defaultCol); decided && less {
					t.Errorf("row %d (%q) sorts before row %d (%q) by column %q, which this view claims its builder already ranks by",
						i, tc.rows[i].cells[b.defaultCol], i-1, tc.rows[i-1].cells[b.defaultCol], b.cols[b.defaultCol].header)
				}
			}
		})
	}
}

// sortCol is an array rather than one int because a column index names a
// different column in each table. Nothing tested that: every sort test
// builds a fresh model per view and never switches, so the claim the array
// exists for went unchecked -- writing every view's slot on each press
// passed the whole package.
//
// The rendered marker is asserted alongside the state, because a view
// switch must also rebuild the row cache: state kept and a marker drawn
// from a stale cache is the same defect one layer down.
func TestTheSortIsRememberedPerViewAcrossASwitch(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = update(t, m, sortKey)
	types := m.sortCol[ViewTypes]
	marker := typeColumns[types].header + sortMark(typeColumns[types].kind)
	if types == tables[ViewTypes].defaultCol {
		t.Fatalf("one press left the types sort on its default, so a switch cannot show it was remembered")
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = update(t, m, sortKey)
	if m.sortCol[ViewTypes] != types {
		t.Errorf("cycling the sort in the calls view moved the types view's sort from column %d to %d", types, m.sortCol[ViewTypes])
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.sortCol[ViewTypes] != types {
		t.Errorf("the types view came back sorted by column %d, want the column %d it was left on", m.sortCol[ViewTypes], types)
	}
	if header := tableHeaderOf(t, centrePaneOf(m.View())); !strings.Contains(header, marker) {
		t.Errorf("the types view came back without its marker %q:\n%s", marker, header)
	}
}

// The sorted column's header is styled as ONE cell, name and marker
// together, and this is what says so. Styled as two runs -- weight on the
// name, accent on the glyph -- escape sequences land between "duration" and
// its "▾", and nothing reading the frame can find the two together.
//
// It matters because the marker is how a reader learns which column the
// sort is on, and every other assertion about it runs through a helper that
// strips the styling first, so none of them can see the split. The goldens
// would catch it, but a golden is regenerated with -update, and this is not.
//
// Asserted on renderTable's own output rather than on a frame, because a
// frame is what the stripping helpers take apart.
func TestTheSortedHeaderKeepsItsNameAndItsMarkerTogether(t *testing.T) {
	cols := []column{
		{header: "resource type", kind: tailIdentifierColumn},
		{header: "n", kind: numericColumn},
	}
	data := []row{rollupRow([]string{"registry.terraform.io/hashicorp/aws", "1"}, []uint64{0, 1}, nil)}

	// Wide enough that nothing is clipped: a clipped header would break the
	// two apart for a reason this test is not about.
	header := strings.Split(renderTable(nil, cols, 0, data, "", -1, true, 60, 10), "\n")[0]
	if !strings.Contains(unstyled(header), sortAscMark) {
		t.Fatalf("the header carries no sort marker, so there is nothing here to keep together: %q", header)
	}
	if want := cols[0].header + sortAscMark; !strings.Contains(header, want) {
		t.Errorf("the rendered header does not carry %q as one run -- the name and its marker have been styled apart: %q", want, header)
	}
}

// The sorted column's header is marked APART from its siblings. That is how
// a reader learns which column answered the s they just pressed, and until
// now only a golden said so -- and a golden is what -update rewrites.
//
// Asserted as a difference rather than against named styles, so a deliberate
// change of treatment does not have to come here to be re-stated.
func TestTheSortedColumnHeaderIsMarkedApartFromItsSiblings(t *testing.T) {
	cols := []column{
		{header: "resource type", kind: tailIdentifierColumn},
		{header: "n", kind: numericColumn},
	}
	data := []row{rollupRow([]string{"registry.terraform.io/hashicorp/aws", "1"}, []uint64{0, 1}, nil)}
	header := strings.Split(renderTable(nil, cols, 1, data, "", -1, true, 60, 10), "\n")[0]

	var prefixes []string
	for _, part := range strings.Split(header, "\x1b[0m") {
		if at := strings.Index(part, "\x1b["); at >= 0 {
			prefixes = append(prefixes, sgrPrefix(part[at:]))
		}
	}
	if len(prefixes) != 2 {
		t.Fatalf("the header carries %d styled cells, want one per column: %q", len(prefixes), header)
	}
	sibling, sortedPrefix := prefixes[0], prefixes[1]
	if sibling == "" || sortedPrefix == "" {
		t.Fatalf("a header cell is drawn unstyled: %q", header)
	}
	if sibling == sortedPrefix {
		t.Errorf("the sorted column's header is drawn exactly like its sibling (%q), so nothing says which column the sort is on: %q", sibling, header)
	}
}

// A rollup pane's figures repeat the table's, and the pane says so by using
// the table's own column headers as its labels -- so a reader moving between
// the row and the pane beside it reads one sequence twice rather than
// matching label to label. Nothing but this holds the two together: the
// labels are string literals in the row builders and the headers are string
// literals in the column lists, so renaming a header silently produces the
// mismatch the correspondence exists to prevent.
//
// Only the leading labels are compared. Each aggregate then adds facts the
// table has no column for -- the provider pane's distinct resource-type and
// RPC-method counts -- and those have no header to agree with.
func TestEveryRollupPaneLabelThatNamesAColumnUsesItsHeader(t *testing.T) {
	l := testLog(t, "two-tier.log")
	for _, c := range []struct {
		what    string
		rows    []row
		columns []column
	}{
		{"providers", providerRows(l.RPCSpans), providerColumns},
		{"types", typeRows(l.RPCSpans, l.UISpans), typeColumns},
	} {
		if len(c.rows) == 0 {
			t.Fatalf("fixture assumption changed: no %s rows, so no aggregate to compare", c.what)
		}
		got := c.rows[0].rollup.aggregate
		if len(got) < len(c.columns) {
			t.Fatalf("the %s aggregate has %d fields, fewer than the %d columns it repeats", c.what, len(got), len(c.columns))
		}
		for i, col := range c.columns {
			if got[i].label != col.header {
				t.Errorf("the %s aggregate's field %d is labelled %q, want the column's own header %q", c.what, i, got[i].label, col.header)
			}
		}
	}
}

// The span detail pane names the same four facts the calls table has
// columns for, and by the same words, for the reason above. Its remaining
// fields -- start, address, resource, module, attribution -- describe one
// span rather than repeating a column, so they have no header to match.
func TestTheSpanDetailPaneLabelsTheCallsTablesColumnsByTheirHeaders(t *testing.T) {
	s := span.Span{RPC: "ApplyResourceChange", Provider: "aws", ResourceType: "aws_subnet", DurationMs: 5}
	lines := unstyledLines(spanDetailLines(s, attrib.Attribution{}, false, hugeWidth))
	for _, col := range callColumns {
		if !slices.Contains(lines, col.header) {
			t.Errorf("no %q field in the span detail pane, though the calls table heads a column with it:\n%s", col.header, strings.Join(lines, "\n"))
		}
	}
}
