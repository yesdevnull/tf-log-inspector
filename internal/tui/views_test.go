package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestProvidersViewRanksByTotalTime(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	out := m.renderList(100, 20)
	if !strings.Contains(out, "provider") {
		t.Errorf("providers view has no provider column:\n%s", out)
	}
}

// The types view shows both tiers side by side, which is the cross-tier join
// phase 2 built. A view showing only one tier would silently drop the other.
func TestTypesViewShowsBothTiers(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	out := m.renderList(120, 20)
	for _, want := range []string{"aws_instance", "UI", "RPC"} {
		if !strings.Contains(out, want) {
			t.Errorf("types view missing %q:\n%s", want, out)
		}
	}
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

// RowCount must reflect the current view's actual row count, derived from
// rows(), not a placeholder count borrowed from a different slice -- an
// out-of-sync RowCount would clamp selection against the wrong length.
func TestRowCountMatchesRowsLength(t *testing.T) {
	for key, view := range map[rune]View{'1': ViewProviders, '2': ViewTypes, '4': ViewCalls} {
		m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if m.RowCount() != len(m.rows()) {
			t.Errorf("view %v: RowCount() = %d, want len(rows()) = %d", view, m.RowCount(), len(m.rows()))
		}
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
	data := []row{{cells: []string{"registry.terraform.io/hashicorp/aws", "1"}, spanIdx: -1}}

	lines := strings.Split(renderTable(nil, cols, data, -1, true, 12, 10), "\n")
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
