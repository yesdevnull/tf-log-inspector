package tui

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// focusFacets tabs until the facet pane has focus, so a test can act on it
// without depending on which pane New starts focused. It widens the terminal
// first: Tab cycles only the panes the current width actually draws, and
// below facetInlineWidth the facet pane is not one of them.
func focusFacets(t *testing.T, m Model) Model {
	t.Helper()
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	for i := 0; i < 8; i++ {
		if m.Focus() == PaneFacets {
			return m
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	}
	t.Fatal("focus never reached PaneFacets")
	return m
}

// An empty filter shows everything. Toggling one value narrows every view at
// once -- the spec calls facets cumulative -- and toggling it back restores.
// two-providers.log exists specifically so this has a second provider to
// narrow away: every other RPC fixture in this repo has just one, and
// selecting the only value present can never narrow anything.
func TestFacetToggleFiltersEveryView(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	before := len(m.rows())
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.rows()) >= before {
		t.Errorf("toggling a facet did not narrow the list: %d then %d", before, len(m.rows()))
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.rows()) != before {
		t.Errorf("untoggling did not restore the list: %d, want %d", len(m.rows()), before)
	}
}

func TestEscClearsAllFilters(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	before := len(m.rows())
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.rows()) != before {
		t.Errorf("Esc did not clear filters: %d rows, want %d", len(m.rows()), before)
	}
}

// Esc clears every filter from whichever pane has focus -- the spec binds it
// globally, not to the facet pane. Gated to the facet pane it would strand a
// user who has since pressed Tab with a narrowed view and no visible way
// back: the key hint line still offers "Esc clear".
//
// TestEscClearsAllFilters focuses the facet pane before pressing Esc, so it
// cannot see such a gate; this presses it from the list.
func TestEscClearsFiltersFromAnyPane(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	before := len(m.rows())
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := len(m.rows()); got >= before {
		t.Fatalf("toggling a facet left %d rows, want fewer than %d -- nothing for Esc to clear", got, before)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.Focus() == PaneFacets {
		t.Fatalf("focus is still on the facet pane after Tab, so this cannot press Esc from anywhere else")
	}
	if got := update(t, m, tea.KeyMsg{Type: tea.KeyEsc}); len(got.rows()) != before {
		t.Errorf("Esc from %v left %d rows, want the unfiltered %d", m.Focus(), len(got.rows()), before)
	}
}

// Space toggles the facet the facet pane's cursor points at, and only while
// that pane has focus. The facet cursor is still drawn -- dimmed -- in an
// unfocused pane, so a space accepted from the list or the detail pane
// rewrites the ranked numbers this tool exists to report with nothing on
// screen that was behaving like a control.
//
// The terminal is wide enough for all three panes, so the facet pane is
// drawn and its cursor sits on a value that would narrow the list if space
// reached it.
func TestSpaceOnlyTogglesFromTheFacetPane(t *testing.T) {
	base := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	before := len(base.rows())
	if before < 2 {
		t.Fatalf("fixture assumption changed: %d call rows, want at least 2 so a stray toggle would be visible", before)
	}
	for _, want := range []Pane{PaneList, PaneDetail} {
		m := base
		for i := 0; i < int(paneCount) && m.Focus() != want; i++ {
			m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		}
		if m.Focus() != want {
			t.Fatalf("focus never reached %v", want)
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
		if got := len(m.rows()); got != before {
			t.Errorf("space with %v focused left %d rows, want the unfiltered %d", want, got, before)
		}
		if len(m.excludedFacets) != 0 {
			t.Errorf("space with %v focused unticked %v, want nothing", want, m.excludedFacets)
		}
	}
}

// The spec requires a count beside every facet value: the count is what
// tells the user how much ticking a checkbox will narrow the view, and a
// dimension of bare labels says nothing about which value is worth
// selecting.
//
// Asserted against renderFacets directly -- the facet pane is the only pane
// that renders a count -- and against two-tier.log, whose dimensions have
// values with DIFFERENT counts, so a count that renders as a constant is
// told apart from one read off the value.
func TestFacetPaneShowsCountsPerValue(t *testing.T) {
	m := New(testLog(t, "two-tier.log"), "x.log")
	const w = 60 // wide and tall enough that nothing is clipped or windowed away
	lines := unstyledLines(strings.Split(m.renderFacets(w, 40), "\n"))
	for _, want := range []struct{ value, count string }{
		{"[x] registry.terraform.io/hashicorp/aws", "3"},
		{"[x] ApplyResourceChange", "2"},
		{"[x] PlanResourceChange", "1"},
		{"[x] aws_instance", "2"},
		{"[x] aws_subnet", "1"},
	} {
		found := false
		for _, ln := range lines {
			// The count is flush against the pane's right edge, so the
			// value and its count sit at opposite ends of the line with
			// padding between: assert on the two ends rather than on one
			// substring spanning both.
			if strings.HasPrefix(ln, want.value) && strings.HasSuffix(ln, want.count) && lipgloss.Width(ln) == w {
				found = true
			}
		}
		if !found {
			t.Errorf("facet pane has no line reading %q ... %q at %d columns:\n%s", want.value, want.count, w, strings.Join(lines, "\n"))
		}
	}
}

// Without a visible cursor, space's "toggle whatever the cursor points at"
// is unusable -- the user cannot tell what they are about to untick.
// two-providers.log's provider values are sorted alphabetically (aws, then
// google), so the cursor starts on the aws value.
func TestRenderFacetsHighlightsTheCursorValue(t *testing.T) {
	m := focusFacets(t, New(testLog(t, "two-providers.log"), "x.log"))
	out := m.renderFacets(50, 20)
	var highlighted []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "\x1b[7m") {
			highlighted = append(highlighted, ln)
		}
	}
	if len(highlighted) != 1 {
		t.Fatalf("got %d highlighted lines, want exactly 1:\n%s", len(highlighted), out)
	}
	if !strings.Contains(highlighted[0], "registry.terraform.io/hashicorp/aws") {
		t.Errorf("highlighted line is not the cursor's value:\n%s", highlighted[0])
	}
}

// The facet cursor must actually move, and space must act on whatever it
// currently points at -- not always the first value of the first dimension.
// two-providers.log's provider dimension has two values sorted
// alphabetically (aws, then google), so moving down once and unticking must
// hide GOOGLE and leave aws. The surviving row's identity is what carries
// this: a space that acted on the cursor's value and a space that acted on
// the dimension's first value both leave exactly one row.
func TestFacetCursorMovesAndSpaceTogglesValueUnderCursor(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	// The providers view is what makes one row per provider, which is what
	// the row count below counts. It is selected here rather than assumed.
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // move onto the second facet value
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})

	rows := m.rows()
	if len(rows) != 1 {
		t.Fatalf("got %d provider rows after unticking one value, want 1: %+v", len(rows), rows)
	}
	if rows[0].cells[0] != "registry.terraform.io/hashicorp/aws" {
		t.Errorf("surviving provider row = %q, want the aws provider -- space acted on the wrong value", rows[0].cells[0])
	}
}

// A cache that survived a filter change would silently keep serving the
// pre-toggle rows -- a wrong answer, not a crash -- so this pins invalidation
// directly rather than relying on TestFacetToggleFiltersEveryView's narrowing
// check to catch it incidentally.
func TestRowsCacheInvalidatesOnFilterChange(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	warm := m.rows() // populate any cache before the filter changes
	if len(warm) == 0 {
		t.Fatal("need at least one row to prove filtering narrows it")
	}
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.rows(); len(got) >= len(warm) {
		t.Errorf("rows() returned %d rows after filtering, want fewer than %d -- looks like a stale cache", len(got), len(warm))
	}
}

// Two facet values sharing a long common prefix -- two provider registry
// addresses, most often -- must still render as distinguishable lines even
// when neither fits in full. Clipping from the end would give both
// "[ ] registry.terraform.i", one checkbox indistinguishable from the
// other; clipping from the front keeps each one's distinguishing tail.
func TestFacetValueLineKeepsTailWhenClippingASharedPrefix(t *testing.T) {
	aws := facetValueLine(" ", "registry.terraform.io/hashicorp/aws", 1, 1, 20, facetValueKind(dimProvider))
	google := facetValueLine(" ", "registry.terraform.io/hashicorp/google", 1, 1, 20, facetValueKind(dimProvider))
	if aws == google {
		t.Fatalf("two values sharing a long prefix rendered identically at width 20: %q", aws)
	}
	for _, c := range []struct{ line, tail string }{{aws, "aws"}, {google, "google"}} {
		if !strings.HasSuffix(facetValueCell(c.line), c.tail) {
			t.Errorf("clipped line lost the tail %q that distinguishes it: %q", c.tail, c.line)
		}
		if !strings.HasSuffix(c.line, "1") {
			t.Errorf("clipped line lost its count: %q", c.line)
		}
	}
}

// facetValueCell is a facet line's value column: everything left of the
// count, with the padding that pushes the count against the pane's right
// edge taken off. Tests about the VALUE have to look here rather than at the
// whole line, which carries the count at its far end.
//
// It strips trailing digits, so a fixture whose value ENDS in a digit would
// have that digit eaten along with the count.
func facetValueCell(line string) string {
	return strings.TrimRight(strings.TrimRight(line, "0123456789"), " ")
}

// The spec requires facets to show a count for every value. A count sliced
// off by clipping the whole assembled line would be a spec miss, not just a
// squeeze, so the count must survive even when the value itself is clipped
// hard.
func TestFacetValueLineNeverDropsTheCount(t *testing.T) {
	line := facetValueLine(" ", "registry.terraform.io/hashicorp/google", 42, 2, 20, facetValueKind(dimProvider))
	if !strings.HasSuffix(line, "42") {
		t.Errorf("count was dropped even though the value was clipped to make room: %q", line)
	}
}

// A facet value is a control, not a caption: two values that clip to the
// same text are two checkboxes the user cannot choose between. The rpc
// dimension's values are plugin-protocol method names, told apart by their
// head, so they must be end-clipped like the calls view's RPC column rather
// than front-clipped like a provider address.
//
// The width matters. At a 100-column terminal the facet pane is capped at a
// quarter of the terminal, 25 display columns; the checkbox costs 4 and a
// four-digit count -- what an apply over a thousand-resource workspace
// produces -- costs 6, leaving 15 for the value. PlanResourceChange and
// ApplyResourceChange share the 14-character suffix "ResourceChange", so
// front-clipping both to 15 leaves both reading "…ResourceChange", against
// counts that such a run makes equal as well. At 120 and 160 columns the
// pane is wide enough that neither value is clipped at all, so this pins
// the width where it bites.
//
// The provider dimension comes first, as FacetsForSpans orders them, which
// is also where the cursor starts: the two rpc lines are then both plain,
// so comparing them compares text rather than highlighting.
func TestFacetPaneKeepsRPCNamesDistinctAtOneHundredColumns(t *testing.T) {
	m := Model{facets: []model.Facet{
		{Name: dimProvider, Values: []model.FacetValue{{Value: "registry.terraform.io/hashicorp/aws", Count: 2326}}},
		{Name: dimRPC, Values: []model.FacetValue{
			{Value: "PlanResourceChange", Count: 1284},
			{Value: "ApplyResourceChange", Count: 1284},
		}},
	}}
	w := facetPaneWidth(facetNaturalWidth(m.facets), 100) // sized exactly as renderPanes sizes it at width 100
	lines := strings.Split(m.renderFacets(w, 20), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d facet lines, want two headers and three values:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	plan, apply := lines[3], lines[4]
	if plan == apply {
		t.Fatalf("PlanResourceChange and ApplyResourceChange both rendered as %q at pane width %d -- two checkboxes the user cannot tell apart", plan, w)
	}
	for _, c := range []struct{ line, head string }{{plan, "Plan"}, {apply, "Apply"}} {
		if !strings.Contains(c.line, c.head) {
			t.Errorf("facet line %q lost the head %q that distinguishes it", c.line, c.head)
		}
	}
}

// A clipped value must be marked as clipped, whichever end it was cut
// from. Unmarked, a head-distinguished value such as an RPC name renders as
// "[ ] ApplyResourceChang  2": a truncated control that reads as a complete
// one, so a user picking it expecting the whole name gets no hint they are
// choosing blind.
func TestFacetValueLineMarksAnEndClippedValue(t *testing.T) {
	const value = "ApplyResourceChange"
	line := facetValueLine(" ", value, 2, 1, 24, facetValueKind(dimRPC))
	if strings.Contains(line, value) {
		t.Fatalf("value fits whole at width 24, so this no longer exercises clipping: %q", line)
	}
	if !strings.HasPrefix(line, "[ ] Apply") {
		t.Errorf("end-clipped value lost its distinguishing head: %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("end-clipped value is not marked as clipped, so it reads as a complete one: %q", line)
	}
	if !strings.HasSuffix(line, "2") {
		t.Errorf("count was dropped: %q", line)
	}
}

// manyFacetValues builds n values wide enough apart to tell apart in a
// rendered pane, so a windowing test can name the exact value it expects.
func manyFacetValues(n int) []model.FacetValue {
	vs := make([]model.FacetValue, n)
	for i := range vs {
		vs[i] = model.FacetValue{Value: fmt.Sprintf("value-%03d", i), Count: 1}
	}
	return vs
}

// The facet cursor moves through every value of every dimension, so the
// window has to follow it: a pane that showed its first h lines whatever
// the cursor was doing would put everything below the fold out of reach --
// on a real capture the resource type dimension alone runs to hundreds of
// values -- with j moving an invisible cursor and space toggling a filter
// the user cannot see. The dimension the cursor is in must stay labelled.
func TestFacetPaneWindowsAroundTheCursor(t *testing.T) {
	const height, target = 20, 45
	m := Model{pane: PaneFacets, facets: []model.Facet{{Name: dimProvider, Values: manyFacetValues(60)}}}
	for i := 0; i < target; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	_, val, ok := m.cursorFacetValue()
	if want := fmt.Sprintf("value-%03d", target); !ok || val != want {
		t.Fatalf("cursor is on %q (ok=%v), want %q -- the test never reached the value it renders for", val, ok, want)
	}

	out := m.renderFacets(30, height)
	lines := strings.Split(out, "\n")
	if len(lines) != height {
		t.Errorf("facet pane rendered %d lines, want exactly %d", len(lines), height)
	}
	var cursorLines []string
	for _, ln := range lines {
		if strings.Contains(ln, "\x1b[7m") {
			cursorLines = append(cursorLines, ln)
		}
	}
	if len(cursorLines) != 1 {
		t.Fatalf("got %d cursor lines, want exactly 1 -- the cursor is off the visible window:\n%s", len(cursorLines), out)
	}
	if want := fmt.Sprintf("value-%03d", target); !strings.Contains(cursorLines[0], want) {
		t.Errorf("cursor line %q is not the cursor's value %q", cursorLines[0], want)
	}
	if !strings.Contains(out, facetSectionHeader(dimProvider)) {
		t.Errorf("the cursor's dimension lost its header, so its checkboxes say nothing about what they select:\n%s", out)
	}
}

// Narrowing a filter can leave the selection past the end of what is left.
// The table then highlights nothing and the detail pane beside it falls to
// its placeholder -- a list with no cursor at all until the user presses an
// arrow key.
func TestFilteringClampsTheSelection(t *testing.T) {
	m := callsModel(t, "two-providers.log", "x.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.Selected() != 1 {
		t.Fatalf("Selected = %d, want the second call selected before the filter narrows the list", m.Selected())
	}
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace}) // narrow to one provider's single call
	if m.RowCount() != 1 {
		t.Fatalf("fixture assumption changed: %d calls after selecting one provider, want 1", m.RowCount())
	}
	if m.Selected() != 0 {
		t.Errorf("Selected = %d after the filter narrowed the list to 1 row, want 0", m.Selected())
	}
	if _, got := m.renderDetail(60, 20); strings.Contains(got, noSelectionNote) {
		t.Errorf("detail pane lost its span because the selection was left past the end of the list:\n%s", got)
	}
}

// Both panes carry a cursor at all times, so drawing both the same way
// leaves Tab -- the spec's first key -- with no visible effect. The pane
// without focus dims its cursor rather than dropping it.
func TestOnlyTheFocusedPaneDrawsALiveCursor(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log") // focus starts on the list
	facets, list := m.renderFacets(40, 20), m.renderList(60, 20)
	if strings.Contains(facets, "\x1b[7m") {
		t.Errorf("facet cursor is drawn as focused while the list has focus:\n%s", facets)
	}
	if !strings.Contains(facets, "\x1b[7;2m") {
		t.Errorf("unfocused facet cursor is not drawn at all:\n%s", facets)
	}
	if !strings.Contains(list, "\x1b[7m") {
		t.Errorf("focused list has no live cursor:\n%s", list)
	}

	m = focusFacets(t, m)
	facets, list = m.renderFacets(40, 20), m.renderList(60, 20)
	if !strings.Contains(facets, "\x1b[7m") {
		t.Errorf("focused facet pane has no live cursor:\n%s", facets)
	}
	if strings.Contains(list, "\x1b[7m") {
		t.Errorf("list cursor is still drawn as focused after Tab moved focus away:\n%s", list)
	}
	if !strings.Contains(list, "\x1b[7;2m") {
		t.Errorf("unfocused list cursor is not drawn at all:\n%s", list)
	}
}

// moveFacetCursorTo presses j until the facet pane's cursor sits on dim's
// value, driving the cursor the way a user does rather than reaching into
// the model's coordinate.
func moveFacetCursorTo(t *testing.T, m Model, dim, value string) Model {
	t.Helper()
	m = focusFacets(t, m)
	for i := 0; i < 500; i++ {
		if d, v, ok := m.cursorFacetValue(); ok && d == dim && v == value {
			return m
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	t.Fatalf("facet cursor never reached %s=%q", dim, value)
	return m
}

// untick excludes exactly the named values of one dimension, as if the
// reader had moved the facet cursor onto each and pressed space. The facet
// maps are unexported, so tests in this package that want a particular
// filter state set it through these two helpers rather than driving the
// cursor across the pane.
func untick(t *testing.T, m *Model, dim string, values ...string) {
	t.Helper()
	offered := map[string]bool{}
	for _, f := range m.facets {
		if f.Name != dim {
			continue
		}
		for _, v := range f.Values {
			offered[v.Value] = true
		}
	}
	excluded := map[string]bool{}
	for _, v := range values {
		if !offered[v] {
			t.Fatalf("dimension %q offers no value %q, so unticking it is not a filter the reader could reach", dim, v)
		}
		excluded[v] = true
	}
	if m.excludedFacets == nil {
		m.excludedFacets = map[string]map[string]bool{}
	}
	if len(excluded) == 0 {
		delete(m.excludedFacets, dim)
	} else {
		m.excludedFacets[dim] = excluded
	}
	m.invalidateRows()
}

// showOnly narrows one dimension to exactly the named values by unticking
// every OTHER value it offers -- the complement of untick, for tests whose
// subject is what survives a filter rather than what it removes. Named with
// no values at all, it unticks the whole dimension, which is the filter
// that admits nothing.
func showOnly(t *testing.T, m *Model, dim string, values ...string) {
	t.Helper()
	keep := map[string]bool{}
	for _, v := range values {
		keep[v] = true
	}
	var drop []string
	var kept int
	for _, f := range m.facets {
		if f.Name != dim {
			continue
		}
		for _, v := range f.Values {
			if keep[v.Value] {
				kept++
				continue
			}
			drop = append(drop, v.Value)
		}
	}
	if kept != len(keep) {
		t.Fatalf("dimension %q does not offer every one of %v, so this is not the filter the test means", dim, values)
	}
	if len(drop) == 0 {
		t.Fatalf("dimension %q offers nothing beyond %v, so narrowing to them filters nothing", dim, values)
	}
	untick(t, m, dim, drop...)
}

// The spec names levels as one of the facet dimensions and its mock-up
// draws a LEVELS section. A level belongs to an ENTRY, not to a span, so
// the dimension is built here from the log's entries and its counts are
// entry counts.
func TestLevelFacetCountsEveryEntry(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	var levels *model.Facet
	for i := range m.facets {
		if m.facets[i].Name == dimLevel {
			levels = &m.facets[i]
		}
	}
	if levels == nil {
		t.Fatalf("no %q dimension in %v", dimLevel, m.facets)
	}

	want := map[string]int{}
	for _, e := range m.log.Entries {
		want[e.Level.String()]++
	}
	if len(want) < 2 {
		t.Fatalf("fixture assumption changed: %d distinct levels, want at least 2 to tell a filter apart", len(want))
	}
	got := map[string]int{}
	for _, v := range levels.Values {
		got[v.Value] = v.Count
	}
	if len(got) != len(want) {
		t.Errorf("level dimension has %d values, want %d: %v against %v", len(got), len(want), got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("level %s counted %d entries, want %d", name, got[name], n)
		}
	}
}

// A level filters the raw log, through the same Filter.MatchEntry path the
// model already had, and nothing else: the ranked views roll up spans, and
// a span has no level for a filter to match.
// multiline-body.log is chosen over mixed-hcp.log specifically because its
// span-closing entry (tf_req_duration_ms, line 7) is TRACE while another
// entry (the HTTP response body, line 2) is DEBUG: selecting DEBUG excludes
// the span's own level. A regression that let level selection reach into the
// rollup path would drop the span along with the TRACE entries, so this
// fixture -- unlike one where every entry shares the span's level -- can
// actually tell that regression apart from correct behaviour.
func TestLevelFacetNarrowsTheRawLogAndLeavesTheRollupsAlone(t *testing.T) {
	m := callsModel(t, "multiline-body.log", "x.log")
	callsBefore := m.rows()
	if len(callsBefore) == 0 {
		t.Fatal("fixture assumption changed: no call rows to compare the rollup baseline against")
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	rawBefore := m.renderRawLog(200, 100)
	if !strings.Contains(rawBefore, "Called downstream") {
		t.Fatal("fixture assumption changed: the TRACE-only line this test narrows away is not shown unfiltered")
	}

	m = moveFacetCursorTo(t, m, dimLevel, logfmt.LevelTrace.String())
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})

	rawAfter := m.renderRawLog(200, 100)
	if rawAfter == rawBefore {
		t.Errorf("unticking a level left the raw log unchanged:\n%s", rawAfter)
	}
	if strings.Contains(rawAfter, "Called downstream") {
		t.Errorf("an entry at the unticked level survived the filter:\n%s", rawAfter)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if got := m.rows(); len(got) != len(callsBefore) {
		t.Errorf("unticking a level changed the rollup rows from %d to %d -- a span has no level to filter on", len(callsBefore), len(got))
	}
}

// The facet pane advertises a count beside every value, and unticking that
// value must hide exactly that many calls and keep every other. Each span
// dimension partitions the spans -- a span has one provider, one RPC, one
// resource type -- so the complement of the advertised count is what must
// remain, which is a sharper claim than "the list got shorter": a filter
// dropping the wrong rows, or more rows than it named, still gets shorter.
//
// The two numbers come from different code paths -- model.FacetsForSpans
// counts them, model.Filter.MatchSpan filters by them -- so a value the
// pane offers but the filter cannot match hides nothing when unticked,
// leaving a box the reader can clear with no effect on screen.
//
// "(none)" is the value that broke: a provider-level RPC such as
// GetProviderSchema belongs to no resource type, so its span's ResourceType
// is empty, counted under model.FacetKey("") and -- until the two agreed --
// matched against a raw "" no span carries.
//
// The property is asserted over every value of every dimension rather than
// over the one that broke, since it is the invariant that generalises: any
// future dimension whose offered key and matched key disagree fails here.
// The level dimension is excluded deliberately and not by oversight -- its
// counts are ENTRY counts and it filters the raw log only (see levelFacet).
func TestUntickingAFacetValueHidesExactlyItsAdvertisedCount(t *testing.T) {
	facets := New(testLog(t, "provider-level-rpc.log"), "x.log").facets
	base := callsModel(t, "provider-level-rpc.log", "x.log")
	all := len(base.rows())
	var sawNone bool
	for _, f := range facets {
		if f.Name == dimLevel {
			continue
		}
		if len(f.Values) == 0 {
			t.Errorf("dimension %q has no values, so it asserts nothing", f.Name)
		}
		for _, v := range f.Values {
			if v.Value == model.FacetKey("") {
				sawNone = true
			}
			// A fresh model per value: excludedFacets is a map, so a
			// toggle applied to one model is visible to any other sharing
			// it, and each value must be measured on its own.
			m := callsModel(t, "provider-level-rpc.log", "x.log")
			m = moveFacetCursorTo(t, m, f.Name, v.Value)
			m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
			if got, want := len(m.rows()), all-v.Count; got != want {
				t.Errorf("%s=%q advertises %d calls, unticking it leaves %d of %d, want %d", f.Name, v.Value, v.Count, got, all, want)
			}
		}
	}
	if !sawNone {
		t.Fatalf("fixture assumption changed: no %q value in any dimension, so the case that broke is not covered", model.FacetKey(""))
	}
}

// Unticking the LEVEL dimension's every value must empty the raw log, the
// one pane that dimension reaches (see levelFacet). The level allow-list
// travels through allowedLevels, which rebuilds it as logfmt.Level keys, and
// a rebuild that reports "no opinion" whenever it is handed nothing to
// rebuild would answer the reader's last untick by putting the whole log
// back on screen -- the very failure the ticked-by-default pane is arranged
// to avoid, reappearing at the one tier that translates its own keys.
//
// provider-rpc.log is narrowed rather than the raw log scrolled, because at
// the top of the log its UNKNOWN comment header is itself an entry: the
// dimension has to be emptied for nothing to be left.
func TestUntickingEveryLevelEmptiesTheRawLog(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	if before := m.renderRawLog(200, 40); strings.Contains(before, noMatchNote) {
		t.Fatalf("the unfiltered raw log already reports an empty filter, so this asserts nothing:\n%s", before)
	}
	showOnly(t, &m, dimLevel)

	if got := m.renderRawLog(200, 40); !strings.Contains(got, noMatchNote) {
		t.Errorf("every level unticked still draws log entries:\n%s", got)
	}
}

// A dimension can be empty while others are not. core-only.log is the
// classic capture taken with TF_LOG=TRACE but no TF_LOG_PROVIDER: it has no
// provider RPC spans at all, so its provider, rpc and resource type
// dimensions are empty and only its level dimension has values. A cursor
// left at {0,0} points into an empty dimension, which draws no cursor bar
// anywhere in the pane and leaves space inert until j teleports it past the
// first value of the first populated dimension.
func TestFacetCursorStartsOnADimensionThatHasValues(t *testing.T) {
	m := New(testLog(t, "core-only.log"), "x.log")
	if len(m.facets[0].Values) != 0 {
		t.Fatalf("fixture assumption changed: dimension %q has values, so this cannot exercise an empty one", m.facets[0].Name)
	}
	dim, val, ok := m.cursorFacetValue()
	if !ok {
		t.Fatal("the facet cursor points at no value, so space has nothing to toggle and the pane draws no cursor")
	}
	if dim != dimLevel {
		t.Errorf("cursor started on dimension %q, want the first one with values (%q)", dim, dimLevel)
	}

	m = focusFacets(t, m)
	if out := m.renderFacets(40, 20); !strings.Contains(out, "\x1b[7m") {
		t.Errorf("no cursor bar drawn anywhere in the facet pane:\n%s", out)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if !m.excludedFacets[dim][val] {
		t.Errorf("space did not untick %s=%q, the value the cursor points at", dim, val)
	}
}

// Every facet value starts TICKED, because the pane's checkboxes say what is
// currently visible rather than what has been picked out. A pane of empty
// boxes above a list showing everything states the opposite of what the
// screen is doing, and leaves the reader to discover by experiment which
// direction the boxes run in. Starting them all ticked makes unticking the
// filtering action, and the pane a legend for the view beside it.
func TestEveryFacetValueStartsTicked(t *testing.T) {
	m := New(testLog(t, "provider-level-rpc.log"), "x.log")
	m = focusFacets(t, m)
	out := m.renderFacets(60, 40)
	if strings.Contains(out, "[ ]") {
		t.Errorf("an untouched facet pane draws an empty checkbox:\n%s", out)
	}
	if !strings.Contains(out, "[x]") {
		t.Fatalf("no ticked checkbox drawn at all, so the assertion above holds vacuously:\n%s", out)
	}
	if m.filterActive() {
		t.Error("an untouched facet pane reports an active filter, so every pane will explain an empty list as filtered")
	}
}

// o narrows a dimension to the value under the cursor by unticking every
// OTHER value it offers -- all of them, not just the next one -- and leaves
// the rest of the pane alone. two-tier.log's level dimension is the one
// with three values, which is what tells "unticked the others" apart from
// "unticked one other".
func TestSoloNarrowsADimensionToTheValueUnderTheCursor(t *testing.T) {
	m := New(testLog(t, "two-tier.log"), "x.log")
	levels := map[string]bool{}
	for _, f := range m.facets {
		if f.Name == dimLevel {
			for _, v := range f.Values {
				levels[v.Value] = true
			}
		}
	}
	if len(levels) < 3 {
		t.Fatalf("fixture assumption changed: the level dimension offers %d values, want at least 3 so soloing can leave more than one unticked", len(levels))
	}

	m = moveFacetCursorTo(t, m, dimLevel, logfmt.LevelTrace.String())
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	delete(levels, logfmt.LevelTrace.String())
	if got := m.excludedFacets[dimLevel]; !maps.Equal(got, levels) {
		t.Errorf("soloing TRACE unticked %v, want every other level (%v)", got, levels)
	}
	if len(m.excludedFacets) != 1 {
		t.Errorf("soloing one dimension touched %v, want the level dimension alone", m.excludedFacets)
	}
}

// What the reader sees of it: one keystroke leaves the views showing the
// cursor's value and nothing else. This is the whole point of the key --
// every value starts ticked, so reaching one provider otherwise costs a
// press of space for every other provider the dimension offers.
func TestSoloNarrowsTheViewsToTheCursorValue(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}) // the providers view: one row per provider
	if before := len(m.rows()); before != 2 {
		t.Fatalf("fixture assumption changed: %d provider rows, want 2 so soloing one is visible", before)
	}
	m = moveFacetCursorTo(t, m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	rows := m.rows()
	if len(rows) != 1 {
		t.Fatalf("soloing one provider left %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].cells[0] != "registry.terraform.io/hashicorp/aws" {
		t.Errorf("surviving provider row = %q, want the one the cursor was on", rows[0].cells[0])
	}
}

// o pressed again on the value it soloed puts that dimension back, and only
// that dimension. Esc is the other way to undo a solo and it clears the
// WHOLE pane, so without this the cost of soloing a provider is the resource
// type the reader narrowed to five minutes earlier.
func TestSoloAgainRestoresItsOwnDimensionAndNoOther(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	// A level exclusion is the one that survives visibly: levels narrow the
	// raw log and no span (see levelFacet), so it cannot be confused with
	// the provider filter being restored beneath it.
	m = focusFacets(t, m)
	untick(t, &m, dimLevel, logfmt.LevelTrace.String())

	m = moveFacetCursorTo(t, m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if got := len(m.rows()); got != 2 {
		t.Errorf("soloing twice left %d provider rows, want the unfiltered 2", got)
	}
	if _, still := m.excludedFacets[dimProvider]; still {
		t.Errorf("the provider dimension is still narrowed after a second o: %v", m.excludedFacets)
	}
	if !m.excludedFacets[dimLevel][logfmt.LevelTrace.String()] {
		t.Errorf("a second o cleared the level dimension too: %v, want TRACE still unticked", m.excludedFacets)
	}
}

// o reads the STATE of a dimension, not how it got there. A reader who
// unticked every value but one with space alone has reached the state o
// writes, so o restores from it -- there is no hidden "soloed" mode that
// could disagree with the checkboxes on screen about which of two identical
// panes the reader is looking at.
func TestSoloRestoresADimensionNarrowedBySpaceAlone(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = focusFacets(t, m)
	untick(t, &m, dimProvider, "registry.terraform.io/hashicorp/google")
	if got := len(m.rows()); got != 1 {
		t.Fatalf("unticking google left %d provider rows, want 1 -- the state o is meant to read", got)
	}

	m = moveFacetCursorTo(t, m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if got := len(m.rows()); got != 2 {
		t.Errorf("o over a dimension already showing one value left %d rows, want the unfiltered 2", got)
	}
}

// Soloing a dimension that offers a single value leaves it UNCONSTRAINED --
// absent from the exclusion map -- rather than holding an exclusion set with
// nothing in it. The two filter alike today, so this asserts the map's shape
// directly: allowedFacetValues reads nil as "no opinion" and filterActive
// counts a dimension by whether its set is non-empty, and an empty set
// sitting in the map is one edit away from being read as either. It is the
// invariant toggleFacetValue keeps when its last exclusion is re-ticked, and
// soloFacetValue's doc claims to keep it too.
func TestSoloingASingleValueDimensionLeavesItUnconstrained(t *testing.T) {
	m := New(testLog(t, "two-tier.log"), "x.log")
	var providers int
	for _, f := range m.facets {
		if f.Name == dimProvider {
			providers = len(f.Values)
		}
	}
	if providers != 1 {
		t.Fatalf("fixture assumption changed: the provider dimension offers %d values, want 1 so soloing has no other value to untick", providers)
	}

	m = moveFacetCursorTo(t, m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if got, present := m.excludedFacets[dimProvider]; present {
		t.Errorf("soloing the only value of a dimension left %v in the exclusion map, want the dimension absent", got)
	}
	if m.filterActive() {
		t.Error("soloing the only value of a dimension reports an active filter, so every pane will explain an empty list as filtered")
	}
}

// o acts only from the facet pane, exactly as space does. The facet cursor
// stays drawn -- dimmed -- in an unfocused pane, so an o accepted from the
// list or the detail pane rewrites the ranked numbers this tool exists to
// report with nothing on screen that was behaving like a control.
func TestSoloOnlyActsFromTheFacetPane(t *testing.T) {
	base := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	before := len(base.rows())
	if before < 2 {
		t.Fatalf("fixture assumption changed: %d call rows, want at least 2 so a stray solo would be visible", before)
	}
	for _, want := range []Pane{PaneList, PaneDetail} {
		m := base
		for i := 0; i < int(paneCount) && m.Focus() != want; i++ {
			m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		}
		if m.Focus() != want {
			t.Fatalf("focus never reached %v", want)
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		if got := len(m.rows()); got != before {
			t.Errorf("o with %v focused left %d rows, want the unfiltered %d", want, got, before)
		}
		if len(m.excludedFacets) != 0 {
			t.Errorf("o with %v focused unticked %v, want nothing", want, m.excludedFacets)
		}
	}
}

// While a search query is being typed every key is text for the query,
// including keys bound elsewhere. o is one of those, and it is held here
// explicitly rather than incidentally: the search guard sits above the o
// case in Update, and the only other test that would notice it moving is
// one whose query happens to contain the letter.
func TestOIsQueryTextWhileASearchIsOpen(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = moveFacetCursorTo(t, m, dimRPC, "ApplyResourceChange")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if len(m.excludedFacets) != 0 {
		t.Errorf("o typed into a search query narrowed the filter: %v", m.excludedFacets)
	}
	if got := footerOf(m.View()); !strings.Contains(got, "o") {
		t.Errorf("footer = %q, want the query to have taken the o as text", got)
	}
}

// Re-ticking a dimension's last unticked value leaves that dimension
// UNCONSTRAINED -- absent from the exclusion map, not present holding an
// empty set. It is the shape toggleFacetValue's doc claims and the one
// allowedFacetValues and filterActive both read: the first returns nil for
// "no opinion" and the second counts a dimension by whether its set is
// non-empty, so an empty set left behind is one edit from meaning the
// opposite of what the checkboxes show.
//
// Asserted on the TOGGLE path specifically. The same rule on the solo path
// has its own test, and until this one existed those were the only two
// holding it -- a rule stated by one function and guarded only through
// another.
func TestReTickingADimensionsLastValueLeavesItUnconstrained(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	m = focusFacets(t, m)
	dim, val, ok := m.cursorFacetValue()
	if !ok {
		t.Fatal("the facet cursor points at no value, so space has nothing to toggle")
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if !m.excludedFacets[dim][val] {
		t.Fatalf("space did not untick %s=%q, so the re-tick below asserts nothing", dim, val)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})

	if got, present := m.excludedFacets[dim]; present {
		t.Errorf("re-ticking the last unticked value left %v in the exclusion map, want the dimension absent", got)
	}
	if m.filterActive() {
		t.Error("re-ticking the last unticked value still reports an active filter, so every pane will explain an empty list as filtered")
	}
}

// An unticked value recedes. Every value starts ticked, so unticking is the
// filtering action, and the pane's job after one is to show at a glance what
// is still admitted -- which a column of identical lines distinguished by
// one character inside a bracket does not.
//
// Dimming is an attribute rather than a colour, so it survives NO_COLOR the
// same way the cursor bar does.
func TestUntickedFacetValuesAreDimmed(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m.pane = PaneFacets
	// Park the cursor in ANOTHER dimension before unticking, so what is
	// asserted is the dimming and not the cursor bar that would otherwise
	// wrap the same line (see below). The provider dimension holds a single
	// value in this fixture, so there is no second value of its own to move
	// to -- a coordinate out of that dimension's range would draw no bar
	// anywhere and make this pass for the wrong reason.
	m.facetCursor = facetCursor{dim: 1, val: 0}
	m.setFacetExclusions(m.facets[0].Name, map[string]bool{m.facets[0].Values[0].Value: true})
	m.invalidateRows()

	lines := strings.Split(m.renderFacets(60, 40), "\n")
	// The provider dimension's heading, its one (unticked) value, then the
	// rpc dimension's heading, then its first value -- which is TICKED and
	// under no cursor, the comparison this needs. Read off the rendered
	// pane, since a heading never reaches the dimming branch at all and
	// would pass the negative check without exercising it.
	if got, want := unstyled(lines[1]), "[ ] "; !strings.HasPrefix(got, want) {
		t.Fatalf("line 1 is %q, not the unticked provider value this asserts on", got)
	}
	if got, want := unstyled(lines[3]), "[x] "; !strings.HasPrefix(got, want) {
		t.Fatalf("line 3 is %q, not a ticked value", got)
	}
	if got := lines[1]; !strings.Contains(got, "\x1b[2m") {
		t.Errorf("unticked value is not dimmed: %q", got)
	}
	if got := lines[3]; strings.Contains(got, "\x1b[2m") {
		t.Errorf("ticked value is dimmed: %q", got)
	}
}

// The cursor's own line is never dimmed, however it is ticked. cursorBar
// requires unstyled input -- reverse video ends at the first reset inside
// what it wraps -- so a dimmed line under the cursor would show a bar that
// stopped partway along.
func TestTheDimmingNeverReachesTheCursorLine(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m.pane = PaneFacets
	m.facetCursor = facetCursor{dim: 0, val: 0}
	m.setFacetExclusions(m.facets[0].Name, map[string]bool{m.facets[0].Values[0].Value: true})
	m.invalidateRows()

	cursorLine := strings.Split(m.renderFacets(60, 40), "\n")[1]
	if strings.Contains(cursorLine, "\x1b[2m") {
		t.Errorf("the cursor's own unticked line is dimmed: %q", cursorLine)
	}
	if !strings.Contains(cursorLine, "\x1b[7m") {
		t.Errorf("the cursor's own line carries no bar: %q", cursorLine)
	}
}

// The counts line up in a column against the pane's right edge, so they can
// be compared down the pane rather than read one at a time. Ragged -- each
// count sitting wherever its value happened to end -- they are the one
// numeric column in this interface that cannot be scanned, which is exactly
// what the reader is in this pane to do: find the value worth filtering to.
func TestFacetCountsLineUpInAColumn(t *testing.T) {
	m := Model{facets: []model.Facet{{Name: dimType, Values: []model.FacetValue{
		{Value: "aws_subnet", Count: 7},
		{Value: "aws_instance", Count: 412},
		{Value: "tls_private_key", Count: 1},
	}}}}
	const w = 30
	// Line 0 is the dimension heading; the rest are values.
	lines := unstyledLines(strings.Split(m.renderFacets(w, 20), "\n")[1:])
	if len(lines) != 3 {
		t.Fatalf("got %d value lines, want 3:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	// Every count ends at the pane's right edge, so their right edges
	// coincide -- which is what a right-aligned column IS. Their left edges
	// do not, and must not: that is what makes the digits line up by place
	// value.
	for i, ln := range lines {
		if got := lipgloss.Width(ln); got != w {
			t.Errorf("value line %d is %d columns, not the pane's %d: %q", i, got, w, ln)
		}
	}
	for _, c := range []struct {
		i     int
		count string
	}{{0, "7"}, {1, "412"}, {2, "1"}} {
		if !strings.HasSuffix(lines[c.i], c.count) {
			t.Errorf("count %s is not flush against the right edge: %q", c.count, lines[c.i])
		}
	}
}

// The column is measured across EVERY dimension, not per dimension: a
// column that reset at each heading would leave the pane's value cells
// ending at four different columns, which is the same argument the help
// screen's key column is measured across every group for.
//
// Where that shows is a value long enough to be CLIPPED. The padding that
// pushes a short value's count right is invisible either way -- spaces
// moved from one side of the line to the other -- so the observable
// difference is how much room a clipped value is left: three columns less
// beside a four-digit count elsewhere in the pane than beside its own
// single digit.
func TestTheFacetCountColumnIsMeasuredAcrossEveryDimension(t *testing.T) {
	rpcs := model.Facet{Name: dimRPC, Values: []model.FacetValue{{Value: "ApplyResourceChange", Count: 4}}}
	withWide := Model{facets: []model.Facet{
		{Name: dimProvider, Values: []model.FacetValue{{Value: "aws", Count: 2326}}},
		rpcs,
	}}
	alone := Model{facets: []model.Facet{rpcs}}

	// The rpc dimension's one value: line 3 where a provider dimension and
	// its value precede it, line 1 in the pane holding it alone.
	const w = 25
	wide := facetValueCell(unstyled(strings.Split(withWide.renderFacets(w, 20), "\n")[3]))
	narrow := facetValueCell(unstyled(strings.Split(alone.renderFacets(w, 20), "\n")[1]))
	if !strings.Contains(narrow, "…") {
		t.Fatalf("the value is not clipped even beside its own single digit, so this exercises nothing: %q", narrow)
	}
	if got, want := lipgloss.Width(narrow)-lipgloss.Width(wide), 3; got != want {
		t.Errorf("a four-digit count elsewhere in the pane costs the clipped value %d columns, want %d:\n%q against %q", got, want, wide, narrow)
	}
}

// The pane's natural width has to hold the widest value AND the widest
// count, the two now sitting in columns of their own. Measured without the
// count column it comes out narrower than the lines it exists to fit, and
// every value is clipped by however many digits the widest count has beyond
// one -- with terminal width to spare, and nothing on screen saying why.
func TestTheFacetPaneNaturalWidthHoldsTheWidestCountToo(t *testing.T) {
	m := Model{facets: []model.Facet{{Name: dimType, Values: []model.FacetValue{
		{Value: "aws_instance", Count: 2326},
		{Value: "aws_subnet", Count: 4},
	}}}}
	w := facetNaturalWidth(m.facets)
	// Line 0 is the dimension heading; the rest are values.
	lines := unstyledLines(strings.Split(m.renderFacets(w, 20), "\n")[1:])
	for _, ln := range lines {
		if strings.Contains(ln, "…") {
			t.Errorf("a value is clipped at the pane's own natural width of %d:\n%s", w, strings.Join(lines, "\n"))
		}
	}
	if !strings.HasSuffix(lines[0], "2326") {
		t.Errorf("the widest count did not survive at the natural width: %q", lines[0])
	}
}

// The count is the last thing given up. The spec requires one for every value
// ("each with counts"), and a checkbox with no count says nothing about what
// ticking it would narrow -- so where the pane is too narrow to hold a value
// column and a count column apart, the count takes the right-hand columns
// and the value gives way, rather than the line being composed value-first
// and the count clipped off the end.
//
// These widths are reached through the facet overlay, which renders at the
// full terminal width; every inline pane has minFacetPaneWidth beneath it.
func TestTheFacetCountSurvivesEveryWidth(t *testing.T) {
	// Every kind, not just the two facetValueKind returns today: the wide
	// path's clip of the value cell is load-bearing ONLY for numericColumn,
	// which clipValueForKind passes through untouched, and the comment there
	// says the guarantee must not be assumed from the kinds in use. Driven
	// by the two clipping kinds alone, that claim is held by nothing.
	for _, kind := range []columnKind{tailIdentifierColumn, headIdentifierColumn, numericColumn} {
		for _, count := range []int{7, 42, 2326} {
			digits := strconv.Itoa(count)
			for w := 1; w <= 20; w++ {
				line := facetValueLine("x", "aws_instance", count, len(digits), w, kind)
				if got := lipgloss.Width(line); got > w {
					t.Errorf("kind %v, count %d at width %d: line is %d columns: %q", kind, count, w, got, line)
				}
				// Below the count's own width there is nowhere for it to
				// go; from there up it must be there whole.
				if w < len(digits) {
					continue
				}
				if !strings.HasSuffix(line, digits) {
					t.Errorf("kind %v, count %d at width %d was clipped away: %q", kind, count, w, line)
				}
			}
		}
	}
}
