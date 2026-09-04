package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
		t.Fatalf("fixture assumption changed: %d provider rows, want at least 2 so a stray toggle would be visible", before)
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
		if len(m.selectedFacets) != 0 {
			t.Errorf("space with %v focused selected %v, want nothing", want, m.selectedFacets)
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
	out := m.renderFacets(60, 40) // wide and tall enough that nothing is clipped or windowed away
	for _, want := range []string{
		"[ ] registry.terraform.io/hashicorp/aws  3",
		"[ ] ApplyResourceChange  2",
		"[ ] PlanResourceChange  1",
		"[ ] aws_instance  2",
		"[ ] aws_subnet  1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("facet pane missing the value line %q:\n%s", want, out)
		}
	}
}

// Without a visible cursor, space's "toggle whatever the cursor points at"
// is unusable -- the user cannot tell what they are about
// to select. two-providers.log's provider values are sorted alphabetically
// (aws, then google), so the cursor starts on the aws value.
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
// two-providers.log's provider dimension has two values sorted alphabetically
// (aws, then google), so moving down once and toggling must select google,
// not aws.
func TestFacetCursorMovesAndSpaceTogglesValueUnderCursor(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // move onto the second facet value
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})

	rows := m.rows() // default view is ViewProviders
	if len(rows) != 1 {
		t.Fatalf("got %d provider rows after selecting one value, want 1: %+v", len(rows), rows)
	}
	if rows[0].cells[0] != "registry.terraform.io/hashicorp/google" {
		t.Errorf("selected provider row = %q, want the google provider -- space acted on the wrong value", rows[0].cells[0])
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
	aws := facetValueLine(" ", "registry.terraform.io/hashicorp/aws", 1, 20, facetValueKind(dimProvider))
	google := facetValueLine(" ", "registry.terraform.io/hashicorp/google", 1, 20, facetValueKind(dimProvider))
	if aws == google {
		t.Fatalf("two values sharing a long prefix rendered identically at width 20: %q", aws)
	}
	if !strings.HasSuffix(aws, "aws  1") {
		t.Errorf("clipped aws line lost its distinguishing tail or its count: %q", aws)
	}
	if !strings.HasSuffix(google, "google  1") {
		t.Errorf("clipped google line lost its distinguishing tail or its count: %q", google)
	}
}

// The spec requires facets to show a count for every value. A count sliced
// off by clipping the whole assembled line would be a spec miss, not just a
// squeeze, so the count must survive even when the value itself is clipped
// hard.
func TestFacetValueLineNeverDropsTheCount(t *testing.T) {
	line := facetValueLine(" ", "registry.terraform.io/hashicorp/google", 42, 20, facetValueKind(dimProvider))
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
	line := facetValueLine(" ", value, 2, 24, facetValueKind(dimRPC))
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
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
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
	if got := m.renderDetail(60, 20); strings.Contains(got, "(no call selected)") {
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

// The detail pane has no cursor of its own, so Tab's third stop is
// invisible unless the pane says so some other way.
func TestDetailPaneTitleMarksFocus(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	if got := m.renderDetail(40, 20); strings.Contains(got, "\x1b[7m") {
		t.Errorf("detail pane marks focus it does not have:\n%s", got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab}) // PaneList -> PaneDetail
	if m.Focus() != PaneDetail {
		t.Fatalf("focus = %v after one Tab from the list, want PaneDetail", m.Focus())
	}
	if got := m.renderDetail(40, 20); !strings.Contains(got, "\x1b[7m") {
		t.Errorf("detail pane does not show that it has focus:\n%s", got)
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
	m := update(t, New(testLog(t, "multiline-body.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	callsBefore := m.rows()
	if len(callsBefore) == 0 {
		t.Fatal("fixture assumption changed: no call rows to compare the rollup baseline against")
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	rawBefore := m.renderRawLog(200, 100)
	if !strings.Contains(rawBefore, "Called downstream") {
		t.Fatal("fixture assumption changed: the TRACE-only line this test narrows away is not shown unfiltered")
	}

	m = moveFacetCursorTo(t, m, dimLevel, logfmt.LevelDebug.String())
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})

	rawAfter := m.renderRawLog(200, 100)
	if rawAfter == rawBefore {
		t.Errorf("selecting a level left the raw log unchanged:\n%s", rawAfter)
	}
	if strings.Contains(rawAfter, "Called downstream") {
		t.Errorf("an entry outside the selected level survived the filter:\n%s", rawAfter)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if got := m.rows(); len(got) != len(callsBefore) {
		t.Errorf("selecting a level changed the rollup rows from %d to %d -- a span has no level to filter on", len(callsBefore), len(got))
	}
}

// The facet pane advertises a count beside every value, and ticking that
// value must list exactly that many calls. The two numbers come from
// different code paths -- model.FacetsForSpans counts them,
// model.Filter.MatchSpan filters by them -- so a value the pane offers but
// the filter cannot match shows a positive count against an empty table,
// which reads as "this dimension really has no calls" rather than as a bug.
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
func TestTickingAFacetValueListsItsAdvertisedCount(t *testing.T) {
	facets := New(testLog(t, "provider-level-rpc.log"), "x.log").facets
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
			// A fresh model per value: selectedFacets is a map, so a
			// toggle applied to one model is visible to any other sharing
			// it, and each value must be measured on its own.
			m := update(t, New(testLog(t, "provider-level-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
			m = moveFacetCursorTo(t, m, f.Name, v.Value)
			m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
			if got := len(m.rows()); got != v.Count {
				t.Errorf("%s=%q advertises %d calls, ticking it lists %d", f.Name, v.Value, v.Count, got)
			}
		}
	}
	if !sawNone {
		t.Fatalf("fixture assumption changed: no %q value in any dimension, so the case that broke is not covered", model.FacetKey(""))
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
	if !m.selectedFacets[dim][val] {
		t.Errorf("space did not select %s=%q, the value the cursor points at", dim, val)
	}
}
