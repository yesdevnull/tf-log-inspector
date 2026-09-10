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
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestSaturatedUIDurationsAreMarkedByTheIndicatorAndPanel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.log")
	data := []byte(`{"@level":"info","@timestamp":"2026-09-04T09:15:02Z","type":"apply_complete","hook":{"action":"read","elapsed_seconds":1e300,"resource":{"addr":"local_file.example","implied_provider":"local","resource_type":"local_file"}}}` + "\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, path)
	for _, width := range []int{60, 100, 160} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		head := strings.SplitN(unstyled(m.View()), "\n", 2)[0]
		if !strings.Contains(head, "i limitations") {
			t.Errorf("at width %d the header hides saturated durations: %q", width, head)
		}
	}
	m.openQuality()
	if panel := m.renderQuality(100, 200); !strings.Contains(panel, "lower bound") || !strings.Contains(panel, "duration_saturated") {
		t.Errorf("quality panel does not explain saturation:\n%s", panel)
	}
}

// updateGolden regenerates testdata/golden when set. The goldens hold what
// the interface actually renders, escape sequences and all, so read a
// regenerated one with scripts/read-golden.sh, which strips the styling and
// leaves the layout. Never pass -update to
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
		t.Errorf("golden mismatch for %s (run go test -update to regenerate, then read it with scripts/read-golden.sh before committing):\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
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
		if got := strings.Contains(out, "Span detail"); got != c.wantDetail {
			t.Errorf("width %d: detail pane present = %v, want %v", c.width, got, c.wantDetail)
		}
	}
}

// wholeFrameCase is one frame the three whole-frame invariants below are
// swept over, named so a failure says which one produced it.
type wholeFrameCase struct {
	name string
	m    Model
}

// wholeFrameCases are the frames those invariants sweep: the view the
// interface opens on, and the timeline in each of its two tiers.
//
// The timeline earns its place because it is the most arithmetic-heavy
// renderer in this package. Every lane row is composed from a label column
// whose width is measured over the labels and a bar whose width is whatever
// is left of the pane (see renderTimeline), and its notes block is cut
// against a height budget of its own -- so it is the renderer most able to
// produce a row a column too wide or a frame a line too tall. Both tiers are
// swept because they draw different spans through different builders (see
// timelineSpans): an RPC-only pass leaves the UI tier's own lanes, labels
// and whole-second axis unmeasured, and testdata/structured-ui.log is the
// fixture that has them.
func wholeFrameCases(t *testing.T) []wholeFrameCase {
	t.Helper()
	press := func(m Model, keys ...rune) Model {
		for _, k := range keys {
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}})
		}
		return m
	}
	open := func(name string) Model { return New(testLog(t, name), "x.log") }
	return []wholeFrameCase{
		{"calls/mixed-hcp.log", open("mixed-hcp.log")},
		{"resources/resources-accounting.log", press(open("resources-accounting.log"), '3')},
		{"resources/resources-modules.log", press(open("resources-modules.log"), '3')},
		{"resources/resources-rpc-only.log", press(open("resources-rpc-only.log"), '3')},
		{"resources/resources-long-lower-bound.log", press(open("resources-long-lower-bound.log"), '3')},
		{"timeline/mixed-hcp.log", press(open("mixed-hcp.log"), '5')},
		{"timeline/structured-ui.log", press(open("structured-ui.log"), '5')},
		// The facet overlay is the third of the three layouts renderPanes
		// composes, and the only one with no golden behind it. Swept here it
		// gets the width and height invariants the other two have: its
		// framing was held by nothing at all, so returning the facet pane
		// unframed passed the whole package.
		{"facet overlay/mixed-hcp.log", press(open("mixed-hcp.log"), 'f')},
	}
}

// searchingFrame is a raw-log search in progress: the frame whose FOOTER is
// one line rather than two, the prompt replacing both hint lines.
//
// It is not in wholeFrameCases, because the sweeps there Tab to the facet
// pane to inspect both cursors and a search has the keyboard -- Tab does
// nothing while the prompt is open. What it is for is the height budget,
// which is measured against the footer's actual size.
func searchingFrame(t *testing.T) Model {
	t.Helper()
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, k := range []rune{'6', '/', 'z'} {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}})
	}
	return m
}

// No rendered line may exceed the terminal width at any supported size, or
// the terminal wraps and the layout becomes wreckage.
//
// The measurement is display columns (lipgloss.Width), not runes: the
// selected row and the facet cursor carry ANSI escapes that occupy no
// columns at all, so a rune count both overstates their width and lets a
// row that is actually SHORT than the rest pass unnoticed. The companion
// check below pins that short case, which a per-line maximum cannot see.
//
// Heights are swept alongside widths because what a pane DRAWS depends on
// both: the timeline's notes block is cut against its own height budget, and
// the lane rows it keeps are windowed around the cursor.
func TestNoLineExceedsTerminalWidth(t *testing.T) {
	for _, c := range wholeFrameCases(t) {
		for _, w := range []int{70, 100, 160} {
			for _, h := range []int{40, 24, 12} {
				m := update(t, c.m, tea.WindowSizeMsg{Width: w, Height: h})
				for _, line := range strings.Split(m.View(), "\n") {
					if n := lipgloss.Width(line); n > w {
						t.Errorf("%s at %dx%d: line of %d columns: %q", c.name, w, h, n, line)
					}
				}
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
//
// It measures the ROW, from renderPanes, rather than the row located inside
// a rendered frame by looking for a pane separator in each line. A
// single-pane row has no separator: identified that way, this measured the
// two- and three-pane layouts and silently exempted every full-width one --
// the facet overlay, the help, and the raw log below detailInlineWidth -- so
// a pane declared a column narrower than the row it fills passed here, and
// passed the whole package.
//
// Row heights are swept directly for the same reason. Locating the row in
// the frame means knowing where it ends, and a short row has no bottom rule
// to end it: chrome is what a short row gives up first, so the closing rule
// is simply absent at a pane height of two.
func TestEveryPaneRowIsTheSameDisplayWidth(t *testing.T) {
	for _, c := range append(wholeFrameCases(t), singlePaneFrameCases(t)...) {
		for _, w := range []int{70, 100, 160} {
			m := update(t, c.m, tea.WindowSizeMsg{Width: w, Height: 40})
			for _, rowH := range []int{1, 2, 3, 12, 30} {
				for _, line := range strings.Split(m.renderPanes(w, rowH), "\n") {
					if n := lipgloss.Width(line); n != w {
						t.Errorf("%s at %d columns, row of %d: line is %d columns, want %d: %q",
							c.name, w, rowH, n, w, line)
					}
				}
			}
		}
	}
}

// singlePaneFrameCases are the layouts renderPanes composes as ONE
// full-width pane. They are kept out of wholeFrameCases because the sweeps
// there Tab to the facet pane to inspect both cursors, and neither of these
// answers Tab: a search has the keyboard, and the help is modal.
func singlePaneFrameCases(t *testing.T) []wholeFrameCase {
	t.Helper()
	help := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	return []wholeFrameCase{
		{"search/mixed-hcp.log", searchingFrame(t)},
		{"help/mixed-hcp.log", help},
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
	for _, c := range wholeFrameCases(t) {
		for _, h := range []int{60, 40, 24, 12, 11, 10, 5, 1} {
			for _, w := range []int{60, 100, 160} {
				m := update(t, c.m, tea.WindowSizeMsg{Width: w, Height: h})
				view := m.View()
				if n := len(strings.Split(view, "\n")); n > h {
					t.Errorf("%s at %dx%d: View() is %d lines, which loses its top %d:\n%s", c.name, w, h, n, n-h, view)
				}
				if !strings.HasPrefix(unstyled(view), " tfli   ") {
					t.Errorf("%s at %dx%d: View() does not start with the header line:\n%s", c.name, w, h, view)
				}
			}
		}
	}
}

// At h == 1 View returns just the header (see the guard at the top of
// View): a frame that showed only key hints could belong to any file at
// all. That principle does not stop applying at h == 2 just because there
// is now room for a footer line beside it -- the two-line footer must give
// up its second line rather than push the header out, since a two-line
// frame naming no file is the same failure the h == 1 guard exists to
// prevent.
func TestViewKeepsTheHeaderAtHeightTwo(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 2})
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 2 {
		t.Fatalf("View() at height 2 is %d lines, want 2:\n%s", len(lines), view)
	}
	if !strings.HasPrefix(unstyled(lines[0]), " tfli   i limitations · x.log") {
		t.Errorf("first line at height 2 is %q, want the header naming the file", lines[0])
	}
	// The surviving footer line must be the ACTION line, not the view-key
	// line: "q quit" is the hint this file's own comments call
	// non-negotiable, and the view-key line has always been the one given
	// up first when there is not room for both.
	if unstyled(lines[1]) != m.actionKeys(m.paneWidth()) {
		t.Errorf("second line at height 2 is %q, want the action keys %q", unstyled(lines[1]), m.actionKeys(m.paneWidth()))
	}
}

// The span hint asks whether the detail pane is DRAWN, and between
// detailInlineWidth and facetInlineWidth that is not a question about width
// alone: the facet overlay replaces the whole pane row there, so the frame
// carries neither the timeline nor the detail pane the hint's keys step
// through. Focus is on the facet pane while it is open, so ←/→ are inert as
// well -- Update binds them to the list pane.
//
// 80 columns is the default terminal and sits inside that band.
// timeline.log packs a lane holding more than one span, which is the other
// half of what the hint is shown on.
func TestTheSpanHintIsHiddenWhileTheFacetOverlayReplacesTheDetailPane(t *testing.T) {
	m := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 80, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if !m.selectedLaneStepsThroughSpans() {
		t.Fatal("the selected lane holds one span, so the hint would be hidden for a reason this test is not about")
	}
	if !strings.Contains(unstyled(m.View()), spanCursorHint) {
		t.Fatal("the hint is already absent with the overlay shut, so opening it cannot be what removes it")
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if !m.showFacetOverlay {
		t.Fatal("f did not open the facet overlay at 80 columns")
	}
	out := unstyled(m.View())
	if strings.Contains(out, spanDetailTitle) {
		t.Fatalf("the overlay frame still draws the detail pane, so the hint has somewhere to point:\n%s", out)
	}
	if strings.Contains(out, spanCursorHint) {
		t.Errorf("the footer offers %q over a frame with no timeline and no detail pane:\n%s", spanCursorHint, out)
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
	// All three above are taken in the opening view, whose every row is one
	// span. A ROLLUP view composes a different frame from the same log: a
	// different column set in the centre, a detail pane of two sections
	// rather than one, and a different set of view-key hints in the footer.
	// None of that was locked anywhere. 100 columns is the width this tool
	// is actually run at, and the one where the side panes and the centre
	// compete hardest.
	m := update(t, base, tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	compareGolden(t, "layout-100-providers.txt", m.View())
}

// The four Resources snapshots cover its materially different evidence states
// while spanning every supported layout boundary named by the plan.
func TestGoldenResourceLayouts(t *testing.T) {
	cases := []struct {
		width   int
		fixture string
	}{
		{60, "resources-accounting.log"},
		{70, "resources-modules.log"},
		{100, "resources-rpc-only.log"},
		{160, "resources-long-lower-bound.log"},
	}
	for _, c := range cases {
		m := update(t, New(testLog(t, c.fixture), c.fixture), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
		m = update(t, m, tea.WindowSizeMsg{Width: c.width, Height: 40})
		compareGolden(t, fmt.Sprintf("resources-%d.txt", c.width), m.View())
	}
}

// Golden files lock the timeline view's own layout at the three widths the
// spec names, the same as TestGoldenLayouts does for the opening view.
//
// timeline.log, not timeline-many-lanes.log, is the fixture: it packs into
// two lanes of two DIFFERENT providers (aws/1, google/1), so a golden of it
// shows the per-provider label scheme at all, and it carries a real stall
// window, so a golden of it shows the annotation too. timeline-many-lanes.log
// packs five lanes of the SAME provider (aws/1..aws/5) with nothing idle
// between them, so a golden of it would show neither -- it exists for
// TestTimelineLaneRowsScrollToKeepTheCursorOnScreen, which is what actually
// needs five lanes to force scrolling, not for a static layout snapshot.
// The widths bracket both of the timeline's own degradations: 160 and 100
// draw every pane, 70 is detailInlineWidth's inclusive lower bound -- the
// narrowest width that still has a detail pane -- and 60 is below it, where
// the detail pane is gone and the lanes take the width it frees. That last
// one matters more here than for the table views: the within-lane span
// cursor's only visible effect is in the detail pane (see renderTimeline),
// so the full-width layout is the one arrangement no other assertion covers.
func TestGoldenTimelineLayouts(t *testing.T) {
	base := update(t, New(testLog(t, "timeline.log"), "plan.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	for _, w := range []int{60, 70, 100, 160} {
		m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
		compareGolden(t, fmt.Sprintf("timeline-%d.txt", w), m.View())
	}
}

// The footer's view-key hints must never vanish. Composed onto one line with
// the action keys, both groups share one width budget and one clip -- and
// that line runs to 123 columns at its widest (see keyHints), so a terminal
// too narrow for the pair silences one of them. Two lines gives each group
// its own budget, so both keep their full names at every width this
// interface renders at.
func TestFooterKeepsViewKeysAtEveryWidth(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	for _, w := range []int{60, 70, 100, 160} {
		got := m.footerText(w)
		lines := strings.Split(got, "\n")
		if len(lines) != 2 {
			t.Fatalf("footer(%d) = %d lines, want 2:\n%s", w, len(lines), got)
		}
		// At narrow widths the navigation reduces to the active view and
		// help; wider widths name every destination.
		if !strings.Contains(lines[0], "4 calls") {
			t.Errorf("footer(%d) view line lost its active view: %q", w, lines[0])
		}
		if w >= 70 && !strings.Contains(lines[0], "2 types") {
			t.Errorf("footer(%d) view line lost its destination hints: %q", w, lines[0])
		}
		// The action line is clipped on its own, so it can still lose its
		// own tail on a terminal narrower than the line itself -- 54 columns
		// bare, and 70 with both of actionKeys' conditional hints. The guard
		// asks the line for its own width rather than naming a number, so
		// what is pinned is that "q quit" survives wherever the line fits at
		// all, whatever actionKeys comes to carry.
		if w >= lipgloss.Width(m.actionKeys(w)) && !strings.Contains(lines[1], "q quit") {
			t.Errorf("footer(%d) action line lost q quit: %q", w, lines[1])
		}
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
		t.Fatalf("fixture assumption changed: %d rows, want at least 2 so a filter can be seen to narrow them", before)
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
			if !strings.Contains(out, "Span detail") {
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
	// The providers view: its single text column is what gives the whole
	// terminal width somewhere visible to go. The calls view the interface
	// opens on splits the same width across three text columns, so nothing
	// there renders a registry address whole at either width.
	base := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})

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
//
// A call row IS a single span, so its pane describes that span and nothing
// else: no group aggregate and no slowest-call section, both of which would be
// the same call reported twice. It is the control for the rollup panes,
// which show both: the RPC, provider and duration lines here, and nothing
// under them.
func TestDetailPaneShowsTheSelectedSpan(t *testing.T) {
	m := callsModel(t, "provider-rpc.log", "x.log")
	spanIdx := m.rows()[0].spanIdx
	want := m.log.RPCSpans[spanIdx]
	// 80 columns is wide enough that the full provider address is not clipped.
	got := detailBody(t, m, spanDetailTitle, 80, 20)
	for _, s := range []string{want.RPC, want.Provider, formatMs(uint64(want.DurationMs))} {
		if !strings.Contains(got, s) {
			t.Errorf("detail pane missing %q:\n%s", s, got)
		}
	}
	if strings.Contains(got, slowestHeading) {
		t.Errorf("calls view detail pane carries a rollup's %q section for a row that is one span:\n%s", slowestHeading, got)
	}
	// Computed independently of selectedDetail's own lookup
	// (model.Log.AttributionForEntry) rather than by calling it -- a test
	// that derives its expectation through the same helper it exercises
	// would prove nothing. The positional index is safe ground truth here
	// because spanIdx already indexes the same unfiltered m.log.RPCSpans
	// slice Attribs is parallel to. provider-rpc.log carries no address
	// context, so the two ways of asking happen to agree here, but only
	// because of that fixture property rather than because the pane never
	// reads a real attribution.
	var a attrib.Attribution
	if spanIdx < len(m.log.Attribs) {
		a = m.log.Attribs[spanIdx]
	}
	if expect := unstyled(strings.Join(spanDetailLines(want, a, m.log.HasAddressContext(), 80), "\n")); got != expect {
		t.Errorf("calls view detail pane =\n%s\nwant\n%s", got, expect)
	}
}

// spanDetailLines is unit-tested directly against a UI-hook span, rather
// than only through the timeline that can now select one: the formatting
// logic is what the spec's own requirement ("for a UI-hook span its
// unmasked address") is about, and a direct test of it does not depend on
// which view's cursor happens to reach it.
func TestSpanDetailLinesShowsAddressForUIHookSpans(t *testing.T) {
	s := span.Span{RPC: "create", Provider: "aws", DurationMs: 2500, Fidelity: span.FidelityUIReported, Address: "aws_instance.example"}
	out := strings.Join(spanDetailLines(s, attrib.Attribution{}, false, 60), "\n")
	if !strings.Contains(out, "aws_instance.example") {
		t.Errorf("UI-hook span detail omits its address:\n%s", out)
	}
}

// An RPC-tier span never carries an address (Span.Address is populated only
// for UI-hook spans), so its detail must not show an address line at all.
func TestSpanDetailLinesOmitsAddressForRPCSpans(t *testing.T) {
	s := span.Span{RPC: "ApplyResourceChange", Provider: "aws", DurationMs: 5, Fidelity: span.FidelityReported}
	out := unstyledLines(spanDetailLines(s, attrib.Attribution{}, false, 60))
	if slices.Contains(out, "address") {
		t.Errorf("RPC-fidelity span detail shows an address line:\n%s", strings.Join(out, "\n"))
	}
}

// The phase's acceptance criterion: a Contained attribution names the
// resource, its module and its resource type -- the fields the interface
// exists to add.
func TestSpanDetailNamesTheResource(t *testing.T) {
	s := span.Span{RPC: "ReadResource", Provider: "registry.terraform.io/hashicorp/aws",
		ResourceType: "aws_instance", DurationMs: 1200, Fidelity: span.FidelityReported}
	a := attrib.Attribution{
		Address: `module.m["k"].aws_instance.web[0]`, Module: `module.m["k"]`,
		Name: "web", Key: "0", Candidates: 1, Confidence: attrib.Contained,
	}
	got := strings.Join(spanDetailLines(s, a, true, 40), "\n")

	for _, want := range []string{"web", `module.m["k"]`, "aws_instance"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail pane missing %q:\n%s", want, got)
		}
	}
}

// I5: a data source and a managed resource of the same type must not render
// identically -- the correlator itself keeps their candidate pools separate
// (c.IsData != (s.RPC == "ReadDataSource")), and the pane must say which one
// this is rather than showing the same "Res thing" either way.
func TestSpanDetailPrefixesADataSourceName(t *testing.T) {
	s := span.Span{RPC: "ReadDataSource", ResourceType: "local_file", DurationMs: 5, Fidelity: span.FidelityReported}
	a := attrib.Attribution{Name: "thing", IsData: true, Candidates: 1, Confidence: attrib.Contained}
	got := strings.Join(spanDetailLines(s, a, true, 40), "\n")
	if !strings.Contains(got, "data.thing") {
		t.Errorf("detail pane does not prefix a data source's name with \"data.\":\n%s", got)
	}

	managed := attrib.Attribution{Name: "thing", Candidates: 1, Confidence: attrib.Contained}
	gotManaged := strings.Join(spanDetailLines(s, managed, true, 40), "\n")
	if strings.Contains(gotManaged, "data.thing") {
		t.Errorf("detail pane prefixes a managed resource's name with \"data.\":\n%s", gotManaged)
	}
}

// The standing defect this task closes: callColumns has a resource-type
// column and the detail pane never rendered it, so the pane showed LESS than
// the row it describes.
func TestSpanDetailRendersResourceType(t *testing.T) {
	s := span.Span{RPC: "ReadResource", ResourceType: "aws_instance", DurationMs: 5}
	got := strings.Join(spanDetailLines(s, attrib.Attribution{}, false, 40), "\n")
	if !strings.Contains(got, "aws_instance") {
		t.Errorf("detail pane omits ResourceType:\n%s", got)
	}
}

// An Ambiguous span states a count and names no resource.
func TestSpanDetailAmbiguousStatesACountAndNamesNothing(t *testing.T) {
	s := span.Span{RPC: "ReadResource", ResourceType: "azuread_service_principal", DurationMs: 5}
	// The fixture carries a real Name, Module and Address -- a fixture that
	// leaves them zero-valued cannot catch a mutant that renders
	// a.Name+"?" (the "best candidate with a ?" the spec forbids): the
	// mutant's output would still contain none of an empty Name, and the
	// assertions below would pass regardless of whether the branch withheld
	// the name or simply had none to withhold.
	a := attrib.Attribution{
		Address: "module.a.azuread_service_principal.first", Module: "module.a",
		Name: "first", Candidates: 4, Confidence: attrib.Ambiguous,
	}
	got := strings.Join(spanDetailLines(s, a, true, 40), "\n")
	if !strings.Contains(got, "4 candidates") {
		t.Errorf("detail pane does not state the candidate count:\n%s", got)
	}
	for _, forbidden := range []string{"first", "module.a", "?"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("detail pane named a resource for an Ambiguous span (%q present):\n%s", forbidden, got)
		}
	}
}

// Two facts the interface must not state in the same words.
func TestSpanDetailDistinguishesNoContextFromUnattributed(t *testing.T) {
	s := span.Span{RPC: "ReadResource", ResourceType: "aws_instance", DurationMs: 5}

	noContext := strings.Join(spanDetailLines(s, attrib.Attribution{}, false, 40), "\n")
	unattributed := strings.Join(
		spanDetailLines(s, attrib.Attribution{Confidence: attrib.Unattributed}, true, 40), "\n")

	if noContext == unattributed {
		t.Error("no-context and unattributed render identically; they are different facts")
	}
}

// The name is the identifying part and must survive a narrow pane; the
// module path is what gives way.
func TestSpanDetailKeepsTheNameWhenThePaneIsNarrow(t *testing.T) {
	a := attrib.Attribution{
		Address: `module.very_long_module_name_here.aws_instance.web`,
		Module:  "module.very_long_module_name_here",
		Name:    "web", Confidence: attrib.Contained,
	}
	s := span.Span{RPC: "ReadResource", ResourceType: "aws_instance", DurationMs: 5}
	got := strings.Join(spanDetailLines(s, a, true, 19), "\n")
	if !strings.Contains(got, "web") {
		t.Errorf("the resource name did not survive a %d-column pane:\n%s", 19, got)
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
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
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
	m := callsModel(t, "two-providers.log", "plan.log")
	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	rows := m.rows()
	if len(rows) != 2 {
		t.Fatalf("fixture assumption changed: got %d calls, want 2", len(rows))
	}
	aws, google := m.log.RPCSpans[rows[0].spanIdx].Provider, m.log.RPCSpans[rows[1].spanIdx].Provider
	if aws == google {
		t.Fatalf("fixture assumption changed: both calls have the same provider %q", aws)
	}

	centreWidth := layoutCentreWidth(m, 160) - 4
	widths := fitColumnWidths(callColumns, columnWidths(headerCells(callColumns, tables[ViewCalls].defaultCol), rows), centreWidth)
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
	m := callsModel(t, "two-rpcs.log", "plan.log")
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	rows := m.rows()
	if len(rows) != 2 {
		t.Fatalf("fixture assumption changed: got %d calls, want 2", len(rows))
	}
	first, second := m.log.RPCSpans[rows[0].spanIdx].RPC, m.log.RPCSpans[rows[1].spanIdx].RPC
	if first == second {
		t.Fatalf("fixture assumption changed: both calls have the same RPC %q", first)
	}

	centreWidth := layoutCentreWidth(m, 100) - 4
	widths := fitColumnWidths(callColumns, columnWidths(headerCells(callColumns, tables[ViewCalls].defaultCol), rows), centreWidth)
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

// centrePaneOf extracts the middle card's body between adjacent borders.
func centrePaneOf(view string) string {
	var centre []string
	for _, line := range strings.Split(unstyled(view), "\n") {
		if fields := strings.Split(line, "│ │"); len(fields) == 3 {
			centre = append(centre, fields[1])
		}
	}
	return strings.Join(centre, "\n")
}

// footerText is m.footer with the theme's escapes stripped. The footer's
// tests are about what it SAYS -- which hints are offered, which are left
// out, how wide the line runs -- and every one of those questions is asked
// of the text. The styling has its own tests, over the frame.
func (m *Model) footerText(w int) string {
	return unstyled(m.footer(w))
}

// unstyled strips the theme's escape sequences from a rendered frame, so
// the helpers that take a frame apart can find the structural strings they
// split on.
//
// It is needed because the pane separator is itself styled: a splitter
// looking for the bare paneSep in a styled frame still finds it, but takes
// the surrounding escapes into the panes on either side, and a helper
// counting DISPLAY columns by walking runes counts each byte of an escape
// as a column of its own. Both failures are quiet -- the wrong answer, not
// an error -- so the stripping happens here rather than at each call site.
//
// Tests about the styling itself must not go through these helpers: they
// assert on what renderFacets, renderList and renderDetail return directly,
// which is where the escapes still are.
func unstyled(s string) string {
	plain, _ := logfmt.StripANSI(s, nil)
	return plain
}

// unstyledLines is unstyled over a block of lines, for the render functions
// that return one line per field.
func unstyledLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = unstyled(ln)
	}
	return out
}

// layoutCentreWidth returns the centre (list) pane's width for a terminal
// w columns wide, replicating renderPanes' own arithmetic for the
// w >= facetInlineWidth case this test renders at. Kept here rather than
// exported from layout.go, since nothing outside a test needs a pane width
// in isolation from actually rendering into it.
func layoutCentreWidth(m Model, w int) int {
	facetW := facetPaneWidth(m.facetPaneNatural+4, w)
	detailW := detailPaneWidth(m.detailPaneNatural+4, w)
	return w - facetW - detailW - 2*paneSepWidth
}

// minDetailPaneWidth exists so the detail pane always has room for its own
// placeholder. It is only a floor if it is applied after the quarter-of-the-
// terminal cap: at 70 columns -- one of the three widths the spec names --
// a quarter is 17, which renders "(nothing selected" with the closing paren
// cut off, a pane too narrow to label itself.
//
// An empty calls view has no selection for the detail pane to describe.
func TestDetailPaneFitsItsPlaceholderAtTheNarrowestSupportedWidth(t *testing.T) {
	m := update(t, New(&model.Log{}, "plan.log"), tea.WindowSizeMsg{Width: detailInlineWidth, Height: 40})
	if got := detailPaneWidth(m.detailPaneNatural+4, detailInlineWidth); got < minDetailPaneWidth {
		t.Errorf("detail pane is %d columns at width %d, below its own minimum of %d", got, detailInlineWidth, minDetailPaneWidth)
	}
	if !strings.Contains(m.View(), noSelectionNote) {
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
	if got, want := m.detailPaneNatural, detailNaturalWidth(m.log); got != want {
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
	for _, line := range strings.Split(unstyled(view), "\n") {
		if !strings.HasPrefix(line, "│") {
			continue
		}
		return runeColumns(line, '│')
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
	line := detailValueFor(t, unstyledLines(spanDetailLines(s, attrib.Attribution{}, false, 20)), "RPC")
	if strings.Contains(line, s.RPC) {
		t.Fatalf("the RPC name fits whole at width 20, so this no longer exercises clipping: %q", line)
	}
	if !strings.HasPrefix(line, "  Valid") {
		t.Errorf("RPC detail line %q lost the head that distinguishes the name", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("RPC detail line %q is not marked as clipped, so it reads as a complete name", line)
	}
}

// A detail pane too narrow for a labelled number must cut the number's TAIL,
// never its head: "742.4s" front-clipped to "…2.4s" reads as a smaller
// number rather than as a cut one, and nothing on the line says which. The
// pane is routinely this narrow -- 19 columns at 70 terminal columns, which
// is one of the three widths the spec names.
//
// The direction is clipValueForKind's, taken from the field's kind, so this
// pins the render site against the taxonomy rather than against a branch of
// its own.
func TestADetailPaneNumberIsCutFromItsTail(t *testing.T) {
	fields := []detailField{
		{label: "total", value: "742.4s", kind: numericColumn},
		{label: "provider", value: "registry.terraform.io/hashicorp/aws", kind: tailIdentifierColumn},
	}
	// Six columns, so the two-column indent leaves a four-column budget --
	// the same squeeze the old label column produced at ten.
	// Indexed rather than looked up by label: at six columns the LABELS
	// clip too ("provider" renders as "provid"), so detailValueFor cannot
	// find them. Each field is two lines, so the values are at 1 and 3.
	got := unstyledLines(detailFieldLines(fields, 6))
	if want := "  742."; got[1] != want {
		t.Errorf("numeric line at 6 columns = %q, want %q -- a number keeps its head", got[1], want)
	}
	if want := "  …aws"; got[3] != want {
		t.Errorf("identifier line at 6 columns = %q, want %q -- an identifier keeps its tail", got[3], want)
	}
}

// A ranked table narrowed to twelve rows looks exactly like a log that only
// ever had twelve calls in it, and nothing else on screen says otherwise. So
// while a filter is active the header reports the matching count against the
// whole log's; with no filter it must read as the plain count it always did,
// since "2 of 2" on every frame is noise that says nothing.
func TestHeaderReportsFilteredCountsOnlyWhileAFilterIsActive(t *testing.T) {
	const unfiltered = "tfli · i limitations · x.log · 2 RPC spans, 0 UI spans"
	m := New(testLog(t, "two-providers.log"), "x.log")
	if got := header(&m); got != unfiltered {
		t.Fatalf("fixture assumption changed: unfiltered header = %q, want %q", got, unfiltered)
	}

	// The provider dimension's first value is aws (values tie on count, so
	// they order by value ascending), which carries one of the two spans.
	m = moveFacetCursorTo(t, m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if want := "tfli · i limitations · x.log · 1 of 2 RPC spans, 0 of 0 UI spans"; header(&m) != want {
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
		if got := strings.TrimSpace(unstyled(lines[len(lines)-1])); got != "/zz" {
			t.Errorf("height %d: last line is %q, want the search prompt %q -- '/' has taken the keyboard with nothing on screen to say so:\n%s", h, got, "/zz", m.View())
		}
	}
}

// The compact timing caveat keeps the action line available on short frames.
func TestTheCompactCaveatKeepsTheFooterAvailable(t *testing.T) {
	for _, h := range []int{40, 12, 8, 7, 4, 3, 2} {
		m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: h})
		view := unstyled(m.View())
		if got := strings.Count(view, "under logging"); got != 0 && h < 4 || got != 1 && h >= 4 {
			t.Errorf("height %d: timing qualification appears %d times", h, got)
		}
		lines := strings.Split(view, "\n")
		if !strings.Contains(lines[len(lines)-1], "q quit") {
			t.Errorf("height %d: action line lost: %q", h, lines[len(lines)-1])
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

// Below detailInlineWidth the centre pane is the whole row, and an entry
// taller than the pane must not push the caveat out of View's frame -- which
// would contradict View's own account of what it always shows. It takes a
// realistic entry to do it, not a pathological one: one measured API
// response accounted for 49% of a 30MB log.
//
// What holds it is framePanes, which composes a single pane exactly as it
// composes three, joinPanes truncating each to the row's height.
func TestTheNarrowLayoutClampsATallCentrePane(t *testing.T) {
	const h = 24
	m := update(t, New(tallEntryLog(60), "x.log"), tea.WindowSizeMsg{Width: detailInlineWidth - 1, Height: h})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})

	view := unstyled(m.View())
	// The pane separator, rather than any one pane's title: the titles vary
	// with the row the cursor is on, and this has to fail when a second
	// pane is drawn whatever that pane says about itself.
	if strings.Count(strings.Split(view, "\n")[3], "╭") != 1 {
		t.Fatalf("width %d still draws a second pane, so this is not the single-pane branch:\n%s", detailInlineWidth-1, view)
	}
	if n := len(strings.Split(view, "\n")); n != h {
		t.Errorf("View() is %d lines at height %d", n, h)
	}
	if !strings.Contains(view, "column 1") {
		t.Errorf("a tall raw-log entry pushed its position status out of the frame:\n%s", view)
	}
	if !strings.Contains(view, "q quit") {
		t.Errorf("a tall raw-log entry pushed the footer out of the frame:\n%s", view)
	}
}

// detailBody is the detail pane's rendered body, with the title it comes
// back beside checked and discarded -- the title being drawn in the pane
// row's rule rather than in the body, and so not part of what a caller
// asserting on the pane's content is measuring.
//
// It asserts the title on the way past, and every caller states which one it
// expects. The title is a claim about the body -- SPAN DETAIL over a group's
// aggregate presents a sum of several calls as one call's figure -- so a
// helper that stripped whatever title it found would leave that claim
// checked by nothing.
//
// Every assertion about the pane is made against this rather than against a
// composed view. The facet pane and the centre table render the same
// provider addresses, resource types and durations at their own widths, so
// a strings.Contains over a whole view is satisfied by a pane other than
// the one under test -- which is exactly how a detail pane wired to the
// wrong row goes unnoticed.
func detailBody(t *testing.T, m Model, wantTitle string, w, h int) string {
	t.Helper()
	title, body := m.renderDetail(w, h)
	if title != wantTitle {
		t.Fatalf("detail pane is headed %q, want %q:\n%s", title, wantTitle, body)
	}
	return strings.TrimRight(unstyled(body), " \n")
}

// selectRow moves the list cursor down to the row whose first cell is want,
// by the same key the user presses, and reports the model with that row
// selected.
func selectRow(t *testing.T, m Model, want string) Model {
	t.Helper()
	for i, r := range m.rows() {
		if r.cells[0] != want {
			continue
		}
		for range i {
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		}
		if m.Selected() != i {
			t.Fatalf("selection is row %d after moving to %q, want row %d", m.Selected(), want, i)
		}
		return m
	}
	t.Fatalf("no row whose first cell is %q", want)
	return m
}

// A providers row is a rollup, and its detail pane describes the GROUP: the
// provider, its total, call count and max -- in the order the providers
// TABLE puts those same four columns in, so the row and the pane beside it
// read as one sequence -- plus the two facts the table has
// no column for -- how many distinct resource types and RPC methods the
// group spans -- and then which single call behind it took longest.
//
// Every figure is written out as a literal. Reading the expectation off the
// selected row's own cells, as this once did, asserts only that the pane
// agrees with the table: a rollup that miscounted would produce a matching
// pair of wrong numbers and pass.
//
// provider-rpc.log is the fixture because its two distinct counts DIFFER:
// two resource types (aws_subnet, aws_internet_gateway) across one RPC name
// (ApplyResourceChange). Were they equal, one of the two lines could be
// reporting the other's number and nothing here would show it.
func TestProvidersDetailPaneShowsTheGroupAggregateAndItsSlowestCall(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.ActiveView() != ViewProviders {
		t.Fatalf("ActiveView = %v after '1', want the providers view this test is about", m.ActiveView())
	}
	want := strings.Join([]string{
		"provider",
		"  registry.terraform.io/hashicorp/aws",
		"total",
		"  6ms",
		"calls",
		"  2",
		"max",
		"  5ms",
		"resource types",
		"  2",
		"RPC methods",
		"  1",
		"",
		"slowest call",
		"  ApplyResourceChange",
	}, "\n")
	// 50 columns is wider than the longest line here, so nothing is clipped
	// and the comparison is against the values themselves.
	if got := detailBody(t, m, rollupDetailTitle, 50, 20); got != want {
		t.Errorf("providers detail pane =\n%s\n\nwant\n%s", got, want)
	}
}

// A rollup row can be built carrying no detail, and detailNaturalWidth
// hands every rollup row in the log to rollupDetailSections without asking
// whether it has any. Its neighbour one level down (slowestOf) deliberately
// tolerates a nil group, so these two must agree on what a missing group
// means rather than one degrading while the other panics mid-measurement.
func TestARollupWithNoDetailProducesNoSections(t *testing.T) {
	if got := rollupDetailSections(nil, 40); got != nil {
		t.Errorf("rollupDetailSections(nil) = %v, want no sections", got)
	}
}

// The rollup detail pane's own clip DIRECTION, at a width where it actually
// clips. Every other assertion about the pane is made at 50 columns, where
// nothing does -- and the pane never renders at 50: it renders at
// minDetailPaneWidth on a 70-column terminal, one of the three widths the
// spec names, and at a quarter of a 100-column one.
//
// Front-clipping is the whole reason two "…/hashicorp/…" addresses stay
// apart there, and the same for two resource types sharing a provider's
// prefix. Cut from the wrong end, every provider line reads
// "  registry.terra…" and every aws type "  aws_…": a pane that cannot tell
// its own rows apart, rendering plausible text for whichever row the cursor
// is on.
//
// The width comes from the pane's own policy at that terminal width rather
// than from a number written here, so this follows the layout instead of
// pinning a guess at it. Every row of the view is asserted, since the point
// is that the rows differ.
//
// Each case is rendered at a width where its own identifier still clips.
// The providers case takes the width the pane's policy gives it at
// detailInlineWidth; the types case names a narrower one, because the
// definition-list layout handed the value the columns the label column had
// been taking and "aws_instance" now fits the pane's own width whole. The
// case is kept rather than dropped: without it, flipping the types
// aggregate to a head-clip fails no test and no golden, and three resource
// types sharing a prefix render as identical rows.
//
// The guard below counts RENDERED ellipses, not expected ones, so a case
// whose values all start fitting fails here saying so rather than through
// the ordinary comparison's "an identifier keeps its tail". A case needs
// one clipped row to be doing its job; the rows that fit whole are still
// asserted, since the point is also that the rows differ.
func TestTheRollupDetailPaneFrontClipsItsIdentifier(t *testing.T) {
	for _, c := range []struct {
		fixture string
		key     rune
		narrow  int // 0 takes the width the pane's own policy gives it
		want    []string
	}{
		{"two-providers.log", '1', 0, []string{"  …hashicorp/google", "  …io/hashicorp/aws"}},
		{"two-tier.log", '2', 12, []string{"  …_instance", "  local_file", "  aws_subnet"}},
	} {
		m := update(t, New(testLog(t, c.fixture), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		w := c.narrow
		if w == 0 {
			w = detailPaneWidth(m.detailPaneNatural+4, detailInlineWidth) - 4
			if w >= m.detailPaneNatural {
				t.Fatalf("%s: the pane renders at %d columns against a natural width of %d, so nothing clips and this measures nothing", c.fixture, w, m.detailPaneNatural)
			}
		}
		if got := len(m.rows()); got != len(c.want) {
			t.Fatalf("fixture assumption changed: %s view %c has %d rows, want %d", c.fixture, c.key, got, len(c.want))
		}
		clipped := 0
		for i, want := range c.want {
			m.selected = i
			// The identifier is the FIRST field's value, so it is the
			// second line of the block: the label heads it.
			lines := strings.Split(detailBody(t, m, rollupDetailTitle, w, 20), "\n")
			if len(lines) < 2 {
				t.Fatalf("%s view %c row %d: the pane rendered %q, too few lines to hold a labelled value", c.fixture, c.key, i, lines)
			}
			if strings.Contains(lines[1], "…") {
				clipped++
			}
			if lines[1] != want {
				t.Errorf("%s view %c row %d: pane's identifier at %d columns = %q, want %q -- an identifier keeps its tail", c.fixture, c.key, i, w, lines[1], want)
			}
		}
		if clipped == 0 {
			t.Fatalf("%s view %c: no row's identifier clipped at %d columns, so this case no longer exercises the direction", c.fixture, c.key, w)
		}
	}
}

// The pane is a projection of the SELECTION, so moving the cursor must move
// it. A pane wired to row 0, or to the whole log, renders identically
// whatever the cursor is on -- and it renders plausible figures while doing
// it.
//
// Each row's pane is asserted WHOLE, against literals. Checking only the
// provider and the total leaves the resource-type count, the RPC-method
// count and the slowest call free to come from some other row, or from the
// log at large, with every checked line still correct -- and the group
// lookup behind those three is a different lookup from the one behind the
// aggregate.
//
// two-providers.log is the fixture because its two rows differ in every
// figure (google 8ms on google_compute_instance, aws 5ms on aws_subnet): in
// a fixture whose rows agree, a pane stuck on row 0 is indistinguishable
// from one that follows the cursor.
func TestTheDetailPaneFollowsTheSelection(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if got := len(m.rows()); got != 2 {
		t.Fatalf("fixture assumption changed: %d provider rows, want 2", got)
	}
	want := []string{
		strings.Join([]string{
			"provider",
			"  registry.terraform.io/hashicorp/google",
			"total",
			"  8ms",
			"calls",
			"  1",
			"max",
			"  8ms",
			"resource types",
			"  1",
			"RPC methods",
			"  1",
			"",
			"slowest call",
			"  ApplyResourceChange",
		}, "\n"),
		strings.Join([]string{
			"provider",
			"  registry.terraform.io/hashicorp/aws",
			"total",
			"  5ms",
			"calls",
			"  1",
			"max",
			"  5ms",
			"resource types",
			"  1",
			"RPC methods",
			"  1",
			"",
			"slowest call",
			"  ApplyResourceChange",
		}, "\n"),
	}
	for i := range want {
		if i > 0 {
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		}
		if m.Selected() != i {
			t.Fatalf("selection is row %d, want row %d", m.Selected(), i)
		}
		if got := detailBody(t, m, rollupDetailTitle, 50, 20); got != want[i] {
			t.Errorf("detail pane on row %d =\n%s\n\nwant\n%s", i, got, want[i])
		}
	}
}

// The same for CALL rows, in the view the interface now opens on. Nothing
// pinned the calls-view pane against a moving cursor at all, so a pane
// hard-wired to rows[0] rendered a real span's RPC, provider and duration
// as the figures of whichever row the user had actually selected.
//
// two-providers.log is the fixture because its two calls differ in BOTH
// fields the span pane can tell rows apart by -- provider and duration --
// so a pane stuck on either row fails here rather than matching on the
// field the two happen to share.
func TestTheCallsDetailPaneFollowsTheSelection(t *testing.T) {
	m := callsModel(t, "two-providers.log", "x.log")
	if got := len(m.rows()); got != 2 {
		t.Fatalf("fixture assumption changed: %d call rows, want 2", got)
	}
	// The calls view ranks by duration descending: the 8ms google call
	// first, then the 5ms aws one.
	want := []string{
		strings.Join([]string{
			"RPC",
			"  ApplyResourceChange",
			"resource type",
			"  google_compute_instance",
			"provider",
			"  registry.terraform.io/hashicorp/google",
			"duration",
			"  8ms",
			// two-providers.log carries no terraform.ui stream, so no call
			// in it can be attributed to a resource -- a fact about the LOG,
			// not about this call.
			"resource",
			"  no address context in log",
		}, "\n"),
		strings.Join([]string{
			"RPC",
			"  ApplyResourceChange",
			"resource type",
			"  aws_subnet",
			"provider",
			"  registry.terraform.io/hashicorp/aws",
			"duration",
			"  5ms",
			// two-providers.log opens on this very call, so its 5ms
			// duration exceeds its own 0ms offset from the log's first
			// entry and ReportedBuilder clamps the start (see
			// span.Span.StartClamped). The pane says so: the timeline
			// draws such a span somewhere it did not run.
			"start",
			"  clamped to zero",
			"resource",
			"  no address context in log",
		}, "\n"),
	}
	for i := range want {
		if i > 0 {
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		}
		if m.Selected() != i {
			t.Fatalf("selection is row %d, want row %d", m.Selected(), i)
		}
		if got := detailBody(t, m, spanDetailTitle, 50, 20); got != want[i] {
			t.Errorf("calls detail pane on row %d =\n%s\n\nwant\n%s", i, got, want[i])
		}
	}
}

// The phase's acceptance criterion, exercised through the REAL pipeline --
// model.Log.Attribs, indexed by row.spanIdx, reaching the screen through
// selectedDetail and View() -- rather than by calling spanDetailLines
// directly the way the unit tests above do. two-tier.log is the fixture
// that carries completed address context alongside RPC spans, so it is the
// one place this can be exercised end to end.
//
// Ranked by duration descending: 250ms is ApplyResourceChange/aws_instance,
// Contained as "web" (a top-level resource, so no Mod line -- that half of
// attributionFields is exercised directly by TestSpanDetailNamesTheResource,
// which supplies a moduled one); 120ms is PlanResourceChange, Ambiguous over
// 2 candidates and names neither of them.
func TestTheCallsDetailPaneNamesTheResourceThroughAttribs(t *testing.T) {
	m := callsModel(t, "two-tier.log", "x.log")
	if got := len(m.rows()); got != 3 {
		t.Fatalf("fixture assumption changed: %d call rows, want 3", got)
	}
	named := strings.Join([]string{
		"RPC",
		"  ApplyResourceChange",
		"resource type",
		"  aws_instance",
		"provider",
		"  registry.terraform.io/hashicorp/aws",
		"duration",
		"  250ms",
		"resource",
		"  web",
		"attribution",
		"  contained",
	}, "\n")
	if got := detailBody(t, m, spanDetailTitle, 100, 20); got != named {
		t.Errorf("named row's detail pane =\n%s\n\nwant\n%s", got, named)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	ambiguous := strings.Join([]string{
		"RPC",
		"  PlanResourceChange",
		"resource type",
		"  aws_instance",
		"provider",
		"  registry.terraform.io/hashicorp/aws",
		"duration",
		"  120ms",
		"start",
		"  clamped to zero",
		"resource",
		"  2 candidates",
		"attribution",
		"  ambiguous",
	}, "\n")
	if got := detailBody(t, m, spanDetailTitle, 100, 20); got != ambiguous {
		t.Errorf("ambiguous row's detail pane =\n%s\n\nwant\n%s", got, ambiguous)
	}
}

// A resource-type row spans both tiers, so its aggregate must too: the
// UI-hook tier's resource count and total beside the RPC tier's calls,
// total and max, under the labels the table's own header uses so the pane
// and the table can be read against each other. Then the slowest RPC-tier
// call for that type.
//
// two-tier.log's aws_instance is the row carrying both tiers: two UI-hook
// resources totalling 5s, and two RPC calls totalling 370ms of which the
// slowest is ApplyResourceChange at 250ms. Reading the RPC figures off the
// UI tier, or the other way round, changes every number here.
func TestTypesDetailPaneShowsBothTiers(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = selectRow(t, m, "aws_instance")
	want := strings.Join([]string{
		"resource type",
		"  aws_instance",
		"UI res.",
		"  2",
		"UI total",
		"  5.0s",
		"RPC calls",
		"  2",
		"RPC total",
		"  370ms",
		"RPC max",
		"  250ms",
		"",
		"slowest call",
		"  ApplyResourceChange",
	}, "\n")
	if got := detailBody(t, m, rollupDetailTitle, 50, 20); got != want {
		t.Errorf("types detail pane =\n%s\n\nwant\n%s", got, want)
	}
}

// The pane describes the SELECTED group, not the log. two-tier.log's
// aws_subnet is the only row in any fixture whose own slowest call is not
// also the whole log's slowest: one 40ms call, against aws_instance's
// 250ms. A rollup wired to the log at large, or to the first group it
// found, renders every other row correctly and only this one wrong.
//
// The pane's own figures cannot show that on their own -- both groups'
// slowest call is an ApplyResourceChange, so the slowest-call field reads
// the same either way -- which is why the group lookup is also asserted
// directly, against the row's RPC max, by
// TestARollupRowsSlowestCallBelongsToItsOwnGroup.
func TestTypesDetailPaneDescribesAnRPCOnlyType(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = selectRow(t, m, "aws_subnet")
	want := strings.Join([]string{
		"resource type",
		"  aws_subnet",
		"UI res.",
		"  0",
		"UI total",
		"  0s",
		"RPC calls",
		"  1",
		"RPC total",
		"  40ms",
		"RPC max",
		"  40ms",
		"",
		"slowest call",
		"  ApplyResourceChange",
	}, "\n")
	if got := detailBody(t, m, rollupDetailTitle, 50, 20); got != want {
		t.Errorf("aws_subnet detail pane =\n%s\n\nwant\n%s", got, want)
	}
}

// The slowest call a rollup row names must come from THAT row's group. The
// group lookup behind it is a second lookup, separate from the aggregate
// the row's cells come from, so the two can drift apart without a single
// cell changing.
//
// Its duration is by construction the row's own max -- model.RollupBy's
// MaxMs and groupRPCSpans' slowest run over the same spans under the same
// key -- so the row's max cell is a free expectation for it, maintained by
// nobody and available in every fixture and every row. A rollup reporting
// some other group's slowest, or the whole log's, breaks the equality on
// any row whose max differs from that group's. Sweeping every row of both
// rollup views over every fixture is what makes that a general assertion
// rather than one that happens to hold for the row a test selected.
func TestARollupRowsSlowestCallBelongsToItsOwnGroup(t *testing.T) {
	for _, fixture := range []string{"two-tier.log", "two-providers.log", "provider-rpc.log", "mixed-hcp.log", "structured-ui.log"} {
		for _, c := range []struct {
			key rune
			// maxCell is which of the view's cells holds the group's
			// longest call: providerColumns' max, typeColumns' RPC max.
			maxCell int
			// callsCell is the group's RPC-tier call count, which is 0 for
			// a type only the UI tier saw -- the one case with no slowest
			// call to name.
			callsCell int
		}{
			{key: '1', maxCell: 3, callsCell: 2},
			{key: '2', maxCell: 5, callsCell: 3},
		} {
			m := update(t, New(testLog(t, fixture), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
			for i, r := range m.rows() {
				if r.rollup == nil {
					t.Fatalf("%s view %c row %d is not a rollup", fixture, c.key, i)
				}
				if r.cells[c.callsCell] == "0" {
					if r.rollup.slowest != nil {
						t.Errorf("%s view %c row %q has no RPC-tier calls but names %q as its slowest", fixture, c.key, r.cells[0], r.rollup.slowest.RPC)
					}
					continue
				}
				if r.rollup.slowest == nil {
					t.Errorf("%s view %c row %q has %s RPC-tier calls but names no slowest", fixture, c.key, r.cells[0], r.cells[c.callsCell])
					continue
				}
				if got := formatMs(uint64(r.rollup.slowest.DurationMs)); got != r.cells[c.maxCell] {
					t.Errorf("%s view %c row %q: slowest call is %s, but the row's max is %s -- the slowest call belongs to another group", fixture, c.key, r.cells[0], got, r.cells[c.maxCell])
				}
			}
		}
	}
}

// A resource type can be seen by the UI-hook tier and never by the RPC tier
// -- two-tier.log's local_file is exactly that -- and the pane must SAY so.
// An omitted section, or an empty one, is indistinguishable from a pane
// that failed to render, which is the defect class this pane already had
// once.
func TestTypesDetailPaneStatesAUIOnlyTypeHasNoRPCCalls(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = selectRow(t, m, "local_file")
	want := strings.Join([]string{
		"resource type",
		"  local_file",
		"UI res.",
		"  1",
		"UI total",
		"  1.0s",
		"RPC calls",
		"  0",
		"RPC total",
		"  0s",
		"RPC max",
		"  0s",
		"",
		"slowest call",
		"  no RPC-tier calls",
	}, "\n")
	if got := detailBody(t, m, rollupDetailTitle, 50, 20); got != want {
		t.Errorf("UI-only types detail pane =\n%s\n\nwant\n%s", got, want)
	}
}

// A short pane keeps or drops the slowest-call section WHOLE, and says when
// it dropped it.
//
// Cut line by line, the pane had a band of heights where it looked finished
// and was not: the blank separator, then the heading with nothing beneath
// it, then the heading and a labelled value with the value gone. The last
// of those is the dangerous one, because it does not look broken -- it
// reads as a complete answer to "which call was slowest" -- and the band
// sits at heights where the centre table still fits, so the frame around it
// looks healthy.
//
// The heights swept are every one either side of the boundary, from a pane
// with room for a single line of body upwards, so the assertion measures
// the cut rather than a pane that happened to fit. The pane's NAME is not
// among the lines being budgeted -- it is inset into the pane row's top
// rule -- so height 1 is one line of the pane's first section. The pane's own lines are
// counted from what it renders at full height, so this does not carry its
// own copy of the layout.
func TestAShortDetailPaneKeepsOrDropsTheSlowestSectionWhole(t *testing.T) {
	// A rollup row, since only a rollup pane has a slowest-call section
	// beneath an aggregate for a short pane to choose between.
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	_, whole := m.renderDetail(50, 40)
	full := strings.Split(unstyled(whole), "\n")
	// The blank separator, the heading, and the call beneath it: a field is
	// two lines now, which is what makes the middle height -- heading drawn,
	// value gone -- reachable at all.
	const slowestSectionLines = 3
	if n := len(full); n < 1+slowestSectionLines+1 {
		t.Fatalf("fixture assumption changed: the whole pane is %d lines, too few to have a section to drop:\n%s", n, strings.Join(full, "\n"))
	}
	aggregate := full[:len(full)-slowestSectionLines] // the group's own figures
	if heading := full[len(full)-2]; heading != slowestHeading {
		t.Fatalf("fixture assumption changed: the pane's second-last line is %q, not the slowest-call heading", heading)
	}
	if value := full[len(full)-1]; !strings.HasPrefix(value, detailIndent) {
		t.Fatalf("fixture assumption changed: the pane's last line is %q, not a value under the slowest-call heading", value)
	}

	for h := 1; h <= len(full)+2; h++ {
		_, got := m.renderDetail(50, h)
		lines := strings.Split(unstyled(got), "\n")
		if n := len(lines); n > h {
			t.Errorf("height %d: the pane is %d lines:\n%s", h, n, strings.Join(lines, "\n"))
		}
		switch {
		case h >= len(full):
			if got := strings.Join(lines, "\n"); got != strings.Join(full, "\n") {
				t.Errorf("height %d has room for the whole pane but rendered:\n%s", h, got)
			}
		case h > len(aggregate):
			// Room for the aggregate but not for the slowest section: it
			// must go whole, with a line left over to say that it did.
			if got, want := lines, append(append([]string{}, aggregate...), moreBelowMark); !slices.Equal(got, want) {
				t.Errorf("height %d rendered:\n%s\n\nwant the aggregate and a cut mark\n%s", h, strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		default:
			// Not even room for the aggregate. The FIRST section is
			// appended whatever the budget, so the pane shows as much of
			// its most important block as fits rather than nothing at all,
			// and every height but 1 -- which has no line to spare -- marks
			// the cut on its last line. The pane's name is not at stake
			// here: it is inset into the row's top rule, outside this
			// budget entirely.
			if lines[0] != full[0] {
				t.Errorf("height %d gave up the pane's first line: %q", h, lines[0])
			}
			if h > 1 && lines[h-1] != moreBelowMark {
				t.Errorf("height %d does not mark the cut on its last line:\n%s", h, strings.Join(lines, "\n"))
			}
		}
		// No label may survive without the value it heads. A field is two
		// lines, so the way that goes wrong is a pane ENDING on a label: a
		// value line is indented and moreBelowMark says content is below
		// the fold, while a bare label at the foot says neither -- it reads
		// as a field the pane failed to fill in.
		if last := lines[len(lines)-1]; len(lines) > 1 && last != moreBelowMark && !strings.HasPrefix(last, detailIndent) {
			t.Errorf("height %d ends on the bare label %q, with nothing under it:\n%s", h, last, strings.Join(lines, "\n"))
		}
	}
}

// detailNaturalWidth measures the pane once, at load, over data that cannot
// change afterwards. It has to measure the ROLLUP views' lines as well as
// each span's: a rollup line can be the widest the pane ever shows, and a
// pane measured only against span detail is then too narrow for it and
// clips content it had room for.
//
// The case where that bites is a log carrying BOTH tiers whose longest
// resource type is reported by the UI tier ALONE. detailNaturalWidth
// measures span detail over the RPC spans of such a log -- it falls to the
// UI tier only where there are no RPC spans at all -- so a UI-only type
// reaches the pane through typeRows and through nothing else.
//
// It is constructed here rather than taken from a fixture because no
// fixture shows it, and a UI-hook-ONLY log does not: there the span loop
// measures the UI spans itself, so the rollup loop raises the width by
// nothing and the test passes whether the loop runs or not.
func TestTheDetailPaneIsMeasuredWideEnoughForRollupDetail(t *testing.T) {
	// Long enough to outrun every line the span loop measures (the widest is
	// the no-address-context note at 27 columns), and short enough that the
	// pane can still show it whole under maxDetailPaneWidth.
	const uiOnlyType = "azurerm_lb_backend_address_pool"
	l := &model.Log{
		RPCSpans: []span.Span{{
			RPC: "PlanResourceChange", Provider: "aws", ResourceType: "aws_subnet",
			StartMs: 0, EndMs: 10, DurationMs: 10, Fidelity: span.FidelityReported,
		}},
		UISpans: []span.Span{{
			RPC: "create", Provider: "azurerm", ResourceType: uiOnlyType,
			Address: "azurerm_lb_backend_address_pool.this",
			StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityUIReported,
		}},
	}
	// The SPAN loop alone, which is what detailNaturalWidth measures for a
	// log with RPC spans. If it already carried this type the rollup loop
	// would be measuring nothing new and the test would pass either way.
	for _, sp := range l.RPCSpans {
		for _, line := range spanDetailLines(sp, attrib.Attribution{}, l.HasAddressContext(), hugeWidth) {
			if strings.Contains(line, uiOnlyType) {
				t.Fatalf("span detail already carries %q in %q, so the rollup measurement is not what this tests", uiOnlyType, line)
			}
		}
	}

	wantLine := detailIndent + uiOnlyType
	if got := detailNaturalWidth(l); got < lipgloss.Width(wantLine) {
		t.Errorf("detailNaturalWidth = %d, too narrow for the rollup line %q of %d columns", got, wantLine, lipgloss.Width(wantLine))
	}

	m := update(t, New(l, "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	for i, r := range m.rows() {
		if r.cells[0] != uiOnlyType {
			continue
		}
		m.selected = i
		body := detailBody(t, m, rollupDetailTitle, panelContentWidth(detailPaneWidth(m.detailPaneNatural+4, 160)), 20)
		if !strings.Contains(body, wantLine) {
			t.Errorf("detail pane does not show %q whole at the width it was measured for:\n%s", wantLine, body)
		}
		return
	}
	t.Fatalf("no types row for %q, so the pane never shows it", uiOnlyType)
}

// The placeholder means exactly what it says: there is no selection to
// describe. Every view with rows describes the row the cursor is on, so
// only the raw log -- which has no rows of its own -- reaches the
// placeholder, and a rollup view reaching it would be a pane that failed to
// describe a row that is right there on screen.
func TestTheDetailPanePlaceholderMeansThereIsNoSelection(t *testing.T) {
	base := New(testLog(t, "two-tier.log"), "x.log")
	for _, c := range []struct {
		key   rune
		title string
	}{
		{'1', rollupDetailTitle},
		{'2', rollupDetailTitle},
		{'4', spanDetailTitle},
	} {
		m := update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		if got := detailBody(t, m, c.title, 50, 20); strings.Contains(got, noSelectionNote) {
			t.Errorf("view %c has rows but its detail pane is dead:\n%s", c.key, got)
		}
	}
	m := update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	if got := detailBody(t, m, noSelectionTitle, 50, 20); got != noSelectionNote {
		t.Errorf("raw log detail pane = %q, want the placeholder %q -- there is no row there to describe", got, noSelectionNote)
	}
}

// The pane's TITLE is a claim about what is beneath it, so it has to come
// from the kind of row the cursor is on. Fixed at SPAN DETAIL, the pane
// headed a group's aggregate -- "Total 6ms" over two calls added together
// -- as though it were one span's duration: a plausible number under the
// wrong noun, which is the failure this tool can least afford.
//
// The titles are asserted in the RENDERED frame, not through renderDetail,
// because the title is also where the detail pane carries keyboard focus:
// a title composed correctly and then lost to the rule it is inset into, to
// the pane's own width, or to the focus styling would still leave the frame
// mislabelled.
func TestTheDetailPaneTitleNamesWhatItIsDescribing(t *testing.T) {
	base := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	for _, c := range []struct {
		key   rune
		title string
	}{
		{'1', rollupDetailTitle}, // every providers row is a group
		{'2', rollupDetailTitle}, // every types row is a group
		{'4', spanDetailTitle},   // every calls row is one span
	} {
		m := update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		title := paneTitlesOf(t, m.View())[detailPaneIndex]
		if title != panelTitle(c.title) {
			t.Errorf("view %c: detail pane is headed %q, want %q", c.key, title, c.title)
		}
	}
}

// paneTitlesOf reads the names inset into a rendered frame's top rule, one
// per pane, left to right. That rule is where every pane is named, so it is
// where an assertion about a pane's title has to look.
//
// At normal heights the rule follows the header, navigation and blank row.
func paneTitlesOf(t *testing.T, view string) []string {
	t.Helper()
	lines := strings.Split(unstyled(view), "\n")
	if len(lines) < 4 {
		t.Fatalf("frame of %d lines has no top rule", len(lines))
	}
	var titles []string
	for _, segment := range strings.Split(lines[3], "╮ ╭") {
		titles = append(titles, strings.TrimSpace(strings.Trim(segment, paneRule+"▶╭╮")))
	}
	// Callers index this by position, so a rule that did not split into the
	// three panes they expect has to stop the test HERE. Returning the short
	// slice instead panics inside the caller, which aborts the whole test
	// BINARY: one regression in rule composition then hides every later
	// failure in the package, including the tests that would have named it.
	if len(titles) != 3 {
		t.Fatalf("top rule split into %d segments, want 3: %q", len(titles), lines[3])
	}
	return titles
}

// The panes' positions in a three-pane frame, for the assertions that read
// one pane's title out of the top rule.
const (
	centrePaneIndex = 1
	detailPaneIndex = 2
)

// The centre pane must name the view it is showing, in the CENTRE pane
// specifically. A search of the whole frame would be satisfied by a facet
// section header or a column heading, which is exactly the ambiguity this
// title exists to remove: the pane holding a providers rollup read as a
// second, less detailed copy of the facet pane's PROVIDERS list, and
// nothing said the interface had other views at all.
func TestTheCentrePaneNamesTheActiveView(t *testing.T) {
	base := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	for _, b := range views {
		m := update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(b.key)})
		var title string
		if b.view == ViewRawLog {
			segments := strings.Split(strings.Split(unstyled(m.View()), "\n")[3], "╮ ╭")
			if len(segments) != 2 {
				t.Fatalf("raw log has %d panes, want 2", len(segments))
			}
			title = strings.TrimSpace(strings.Trim(segments[centrePaneIndex], paneRule+"▶╭╮"))
		} else {
			title = paneTitlesOf(t, m.View())[centrePaneIndex]
		}
		want := panelTitle(b.title)
		if b.view == ViewTimeline {
			// The timeline's rendered title names the TIER it draws (see
			// timelineTitle) rather than views' static placeholder, so this
			// one view's title is measured against its own function instead
			// of the table entry every other view matches exactly.
			want = panelTitle(m.timelineTitle())
		}
		if title != want {
			t.Errorf("key %q: the top rule names the centre pane %q, want %q", b.key, title, want)
		}
	}
}

// The view's NAME has to survive down to the width where the rule can no
// longer carry it whole: it is the only thing on screen that says what the
// rows beneath it are, and below that width it is dropped rather than
// clipped (see titledRule).
//
// The types view is swept because it is the widest STATIC name at 16
// columns, needing 20. It is not the widest name the centre pane can carry
// -- the timeline's names its tier, reaching 28 for "TIMELINE (ui, whole
// seconds)" -- which is why the bound asserted here is the types view's own
// and not a claim about the interface as a whole.
func TestTheViewNameSurvivesEveryWidth(t *testing.T) {
	base := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	// Spelled out rather than read back from viewTitle, so this measures the
	// rendered frame against a fixed string instead of following whatever
	// the title function happens to return.
	const want = "By resource type"
	for _, w := range []int{160, 100, 99, 70, 69, 40, 21} {
		m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
		if !strings.Contains(m.View(), want) {
			t.Errorf("width %d: the frame does not name the active view %q:\n%s", w, want, m.View())
		}
	}
}

// Navigation names all implemented views, including the active tab.
func TestTheNavigationAdvertisesTheWorkingViewKeys(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, w := range []int{100, 160} {
		for _, current := range views {
			m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(current.key)})
			if m.ActiveView() != current.view {
				t.Fatalf("key %q did not select the %s view", current.key, current.name)
			}
			got := strings.Split(unstyled(m.View()), "\n")[1]
			for _, b := range views {
				hint := b.key + " " + b.name
				if !strings.Contains(got, hint) {
					t.Errorf("width %d, %s view: navigation %q does not offer %q", w, current.name, got, hint)
				}
			}
		}
	}
}

// A key hint must name a key that does something. Enter resolves the
// selected row to the log entry that closed its span, and a ROLLUP row
// stands for a group and resolves to none, so in the two rollup views Enter
// returns immediately -- and in the raw log there is no row to press it
// over. The hint stood in all four views regardless.
//
// Whether Enter does anything is MEASURED here by pressing it and seeing
// whether the view moved, rather than listed as views this test believes
// are inert: a list would go stale the day Enter learns to act on a rollup,
// and would go stale silently, which is how the hint came to be wrong in
// the first place.
func TestTheFooterOffersTheOpenKeyOnlyWhereEnterOpens(t *testing.T) {
	base := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	if base.Focus() != PaneList {
		t.Fatalf("focus = %v, want the list so Enter is handled", base.Focus())
	}
	var sawBoth [2]bool
	for _, b := range views {
		m := update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(b.key)})
		got := footerOf(m.View())
		opens := update(t, m, tea.KeyMsg{Type: tea.KeyEnter}).ActiveView() != m.ActiveView()
		if opens {
			sawBoth[0] = true
		} else {
			sawBoth[1] = true
		}
		if strings.Contains(got, "⏎ open") != opens {
			verb := "does not offer"
			if !opens {
				verb = "offers"
			}
			t.Errorf("%s view: Enter opens = %v, but the footer %s the open hint: %q", b.name, opens, verb, got)
		}
	}
	if !sawBoth[0] || !sawBoth[1] {
		t.Fatalf("Enter behaved the same way in every view, so this cannot tell a conditional hint from an unconditional one")
	}
}

// The action line is clipped on its own, independently of the view-key line
// above it, so nothing the view-key line says can still crowd "q quit" off
// the end of the action line, which is what one composed line's shared width
// budget does. This sweeps every width from the one the action keys alone
// need, in every view -- the open hint varies by view, so the width the
// action line needs does too.
func TestTheFooterNeverLosesQuitAtWidthsTheActionLineFits(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, b := range views {
		// setView rather than an assignment: it resets the selection and
		// rebuilds the row cache, and the footer's own open hint is a
		// function of the selected row.
		m.setView(b.view)
		for w := lipgloss.Width(m.actionKeys(200)); w <= 200; w++ {
			lines := strings.Split(m.footerText(w), "\n")
			action := lines[len(lines)-1]
			if !strings.Contains(action, "q quit") {
				t.Fatalf("%s at %d columns: action line %q has lost the quit hint", b.title, w, action)
			}
			if n := lipgloss.Width(action); n > w {
				t.Fatalf("%s at %d columns: action line is %d columns: %q", b.title, w, n, action)
			}
		}
	}
}

// The interface opens on the calls view, so the opening screen's detail
// pane describes a CALL -- the top row's own span -- rather than sitting on
// the placeholder or on a group aggregate. It is the first screen a user
// sees, and the only one they see without pressing a key, so a pane that
// says nothing there is a pane most users never see working.
func TestTheOpeningScreenDescribesTheTopCall(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	rows := m.rows()
	if len(rows) == 0 {
		t.Fatal("fixture assumption changed: the opening view has no rows")
	}
	if !rows[m.Selected()].isCall() {
		t.Fatalf("the opening screen's selected row is a rollup, not a call: %+v", rows[m.Selected()])
	}
	spanIdx := rows[m.Selected()].spanIdx
	// Computed independently of selectedDetail's own lookup, the same way
	// and for the same reason as TestDetailPaneShowsTheSelectedSpan: a test
	// that derived its expectation through the helper it is exercising
	// would prove nothing, and the positional index is safe ground truth
	// here regardless, since spanIdx already indexes the same unfiltered
	// m.log.RPCSpans slice Attribs is parallel to. provider-rpc.log carries
	// no address context, so this and the zero Attribution agree, but that
	// is a fixture property, not a premise this test states without it.
	var a attrib.Attribution
	if spanIdx < len(m.log.Attribs) {
		a = m.log.Attribs[spanIdx]
	}
	want := unstyled(strings.Join(spanDetailLines(m.log.RPCSpans[spanIdx], a, m.log.HasAddressContext(), 50), "\n"))
	if got := detailBody(t, m, spanDetailTitle, 50, 20); got != want {
		t.Errorf("opening detail pane =\n%s\n\nwant the selected call's span detail\n%s", got, want)
	}
}

// A clamped start is the one thing about a span that the timeline draws
// WRONG -- anchored at column 0, with a length shorter than its own
// duration -- so the pane describing the selected span has to say so.
// Without it the pane reads "duration" over "45.0s" beside a
// three-second bar with
// nothing accounting for the difference.
func TestSpanDetailLinesReportsAClampedStart(t *testing.T) {
	s := span.Span{RPC: "GetProviderSchema", Provider: "aws", StartMs: 0, EndMs: 2000, DurationMs: 45000, StartClamped: true, Fidelity: span.FidelityReported}
	out := strings.Join(spanDetailLines(s, attrib.Attribution{}, false, 60), "\n")
	if !strings.Contains(out, "clamped") {
		t.Errorf("clamped span detail says nothing about its clamped start:\n%s", out)
	}
	unclamped := span.Span{RPC: "GetProviderSchema", Provider: "aws", StartMs: 1000, EndMs: 2000, DurationMs: 1000, Fidelity: span.FidelityReported}
	if out := strings.Join(spanDetailLines(unclamped, attrib.Attribution{}, false, 60), "\n"); strings.Contains(out, "clamped") {
		t.Errorf("an unclamped span's detail claims a clamped start:\n%s", out)
	}
}

// detailValueFor returns the value line sitting beneath label in a rendered
// detail block. Every field is two lines -- the label, then its value
// indented under it -- so a test naming a field asks for the label and gets
// what it heads. The lines must be unstyled first: the label line carries
// the accent theme.fieldLabel puts on it.
//
// The match is exact, so it holds only where labels are unique within the
// block and wide enough not to clip. Both are true at every width a pane
// actually renders at -- the longest label is 14 columns against a floor of
// minDetailPaneWidth -- but a test probing detailFieldLines below that
// floor must index instead.
func detailValueFor(t *testing.T, lines []string, label string) string {
	t.Helper()
	for i, ln := range lines {
		if ln == label {
			if i+1 >= len(lines) {
				t.Fatalf("the %q label is the last line, so it heads nothing: %q", label, lines)
			}
			return lines[i+1]
		}
	}
	t.Fatalf("no %q field among %q", label, lines)
	return ""
}

// TestTheDetailPaneIsMeasuredWideEnoughForAUIHookAddress covers a pane that
// was never sized against the only per-resource identifier the UI tier has.
// detailNaturalWidth measured spanDetailLines over l.RPCSpans alone, which
// was complete while the address field was unreachable; the timeline now
// selects
// UI-tier spans and renders it. On structured-ui.log the pane measured 25
// columns and front-clipped every module path to its tail, so two distinct
// modules' resources rendered as identical text -- with 135 columns of
// terminal to spare and maxDetailPaneWidth nowhere near reached.
func TestTheDetailPaneIsMeasuredWideEnoughForAUIHookAddress(t *testing.T) {
	l := testLog(t, "structured-ui.log")
	if len(l.UISpans) == 0 {
		t.Fatal("structured-ui.log carries no UI-hook spans, so it no longer exercises this")
	}
	var widest string
	addresses := map[string]bool{}
	for _, s := range l.UISpans {
		lines := unstyledLines(spanDetailLines(s, attrib.Attribution{}, false, hugeWidth))
		addresses[detailValueFor(t, lines, "address")] = true
		for _, line := range lines {
			if lipgloss.Width(line) > lipgloss.Width(widest) {
				widest = line
			}
		}
	}
	if !addresses[widest] {
		t.Fatalf("the widest UI-hook detail line is %q, not an address, so this asserts nothing", widest)
	}
	if got := detailNaturalWidth(l); got < lipgloss.Width(widest) {
		t.Errorf("detailNaturalWidth = %d, want at least %d for %q", got, lipgloss.Width(widest), widest)
	}
}

// A log carrying BOTH tiers reaches the detail pane through its RPC spans
// alone. timelineSpans draws the UI tier only where the log has no RPC span
// at all, and row.spanIdx only ever indexes RPCSpans, so every UI-hook
// span's address field is one no keypress in such a log can put on screen --
// and every column it claims comes out of the centre pane, which in the
// timeline is the bar area this view exists for.
//
// The case is built here rather than taken from a fixture because no
// fixture shows it. testdata/two-tier.log carries both tiers, but its
// widest RPC line -- a registry provider address at 37 columns -- already
// outruns its longest address line at 19, so the pane measures the same
// width either way and the defect is invisible in it.
func TestDetailNaturalWidthSkipsUISpansTheDetailPaneCannotReach(t *testing.T) {
	ui := span.Span{
		RPC: "create", Provider: "aws", ResourceType: "aws_subnet",
		Address:    "module.networking.module.private_subnets.aws_subnet.this[0]",
		StartMs:    0,
		EndMs:      1000,
		DurationMs: 1000,
		Fidelity:   span.FidelityUIReported,
	}
	l := &model.Log{
		RPCSpans: []span.Span{{RPC: "PlanResourceChange", Provider: "aws", ResourceType: "aws_subnet", StartMs: 0, EndMs: 10, DurationMs: 10, Fidelity: span.FidelityReported}},
		UISpans:  []span.Span{ui},
	}

	addr := detailValueFor(t, unstyledLines(spanDetailLines(ui, attrib.Attribution{}, false, hugeWidth)), "address")

	got := detailNaturalWidth(l)
	for _, line := range reachableDetailLines(l) {
		if w := lipgloss.Width(line); w >= lipgloss.Width(addr) {
			t.Fatalf("the pane can draw %q at %d columns, as wide as the address line %q -- this case no longer isolates the unreachable tier", line, w, addr)
		} else if w > got {
			t.Errorf("detailNaturalWidth = %d, too narrow for %q at %d columns", got, line, w)
		}
	}
	if got >= lipgloss.Width(addr) {
		t.Errorf("detailNaturalWidth = %d, wide enough for %q (%d columns) -- a line this log can never put in the pane", got, addr, lipgloss.Width(addr))
	}
}

// reachableDetailLines is every line the detail pane can actually draw for
// l: one line per field of each span a row or the timeline cursor can
// select, and every line of each rollup row's own sections. It is what
// detailNaturalWidth is measured to fit.
//
// It indexes Attribs positionally rather than calling
// model.Log.AttributionForEntry, deliberately: this helper is ground truth
// for detailNaturalWidth's own measurement, computed independently of it,
// and the positional index is safe here because it walks l.RPCSpans
// unfiltered -- the same slice Attribs is parallel to.
func reachableDetailLines(l *model.Log) []string {
	var lines []string
	hasContext := l.HasAddressContext()
	for i, s := range l.RPCSpans {
		var a attrib.Attribution
		if i < len(l.Attribs) {
			a = l.Attribs[i]
		}
		lines = append(lines, spanDetailLines(s, a, hasContext, hugeWidth)...)
	}
	for _, r := range append(providerRows(l.RPCSpans), typeRows(l.RPCSpans, l.UISpans)...) {
		for _, section := range rollupDetailSections(r.rollup, hugeWidth) {
			lines = append(lines, section...)
		}
	}
	return lines
}

// The measurement has to reach the SCREEN, not just the function: a pane
// measured wide enough that the terminal-relative clamp then throws away is
// no fix. 160 columns leaves maxDetailPaneWidth reachable.
func TestTheTimelineDetailPaneShowsAWholeUIHookAddress(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 30})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	s, ok := m.selectedTimelineSpanValue()
	if !ok {
		t.Fatal("nothing selected on a timeline with spans")
	}
	if !strings.Contains(m.View(), s.Address) {
		t.Errorf("the frame does not carry the selected resource's address %q in full:\n%s", s.Address, m.View())
	}
}

// TestTheFooterOffersTheSpanKeysOnlyWhereTheyStep covers the inverse of the
// defect TestTheFooterOffersTheOpenKeyOnlyWhereEnterOpens covers: a working
// key advertised nowhere. ←/→ (and h/l) are the only way to select any span
// in a lane but the first, and they drive both the detail pane and Enter's
// jump target -- on a real capture a lane holds hundreds of spans and only
// the first was reachable without knowing the keys exist.
//
// Whether they step is MEASURED by pressing → and seeing whether the
// selection moved, rather than listed as views this test believes are
// inert, the same way the open hint's own test measures Enter.
func TestTheFooterOffersTheSpanKeysOnlyWhereTheyStep(t *testing.T) {
	base := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	var sawBoth [2]bool
	for _, b := range views {
		m := update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(b.key)})
		got := footerOf(m.View())
		before := m.timeline
		steps := update(t, m, tea.KeyMsg{Type: tea.KeyRight}).timeline != before
		if steps {
			sawBoth[0] = true
		} else {
			sawBoth[1] = true
		}
		if strings.Contains(got, spanCursorHint) != steps {
			verb := "does not offer"
			if !steps {
				verb = "offers"
			}
			t.Errorf("%s view: → steps = %v, but the footer %s %q: %q", b.name, steps, verb, spanCursorHint, got)
		}
	}
	if !sawBoth[0] || !sawBoth[1] {
		t.Fatalf("→ behaved the same way in every view, so this cannot tell a conditional hint from an unconditional one")
	}
}

// The action line is clipped from its end, so every hint added to it pushes
// "q quit" towards the edge. 70 columns is detailInlineWidth, the narrowest
// width that still draws every kind of pane, and the action line fitted it
// exactly at 62 columns before the span hint was added.
func TestTheActionLineStillFitsTheNarrowestThreePaneWidth(t *testing.T) {
	m := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 70, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if !strings.Contains(m.actionKeys(detailInlineWidth), spanCursorHint) {
		t.Fatalf("the timeline's action keys do not carry the span hint, so this asserts nothing: %q", m.actionKeys(detailInlineWidth))
	}
	if n := lipgloss.Width(m.actionKeys(detailInlineWidth)); n > detailInlineWidth {
		t.Errorf("the timeline's action line is %d columns, more than the %d a %d-column terminal gives it: %q", n, detailInlineWidth, detailInlineWidth, m.actionKeys(detailInlineWidth))
	}
}

// TestTheSpanHintGivesWayToQuitBelowTheDetailPanesWidth is the same rule
// the open hint follows, applied to a width rather than to a row: below
// detailInlineWidth the detail pane collapses, and the within-lane cursor's
// only visible effect is in that pane (see renderTimeline), so ←/→ there
// move a selection nothing on screen reflects. Advertising them at that
// width advertises a key that does nothing observable -- and it costs "q
// quit", which the footer was split in two to protect, its place on the
// line.
func TestTheSpanHintGivesWayToQuitBelowTheDetailPanesWidth(t *testing.T) {
	m := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	const narrowW = 60
	small := update(t, m, tea.WindowSizeMsg{Width: narrowW, Height: 40})
	narrow := footerOf(small.View())
	if strings.Contains(narrow, spanCursorHint) {
		t.Errorf("at %d columns the footer offers %q, but the detail pane it steps is not drawn: %q", narrowW, spanCursorHint, narrow)
	}
	action := func(footer string) string {
		lines := strings.Split(footer, "\n")
		return lines[len(lines)-1]
	}
	// actionKeys takes the width it is answering for, so both lines come
	// from the one timeline model: at 100 columns it carries the span hint,
	// at 60 the detail pane it steps is gone and it does not. Other secondary
	// hints may also give way there to keep evidence and quit visible.
	withSpan, withoutSpan := m.actionKeys(100), m.actionKeys(narrowW)
	if !strings.Contains(withSpan, spanCursorHint) {
		t.Fatalf("the timeline's action line at 100 columns does not carry the span hint, so this asserts nothing: %q", withSpan)
	}
	if strings.Contains(withoutSpan, spanCursorHint) {
		t.Errorf("at %d columns action keys retain the hidden detail pane's span hint: %q", narrowW, withoutSpan)
	}
	if !strings.HasSuffix(action(narrow), "q quit") {
		t.Errorf("at %d columns the action line clips quit: %q", narrowW, action(narrow))
	}

	big := update(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	wide := footerOf(big.View())
	for _, want := range []string{spanCursorHint, "q quit"} {
		if !strings.Contains(wide, want) {
			t.Errorf("at 100 columns the footer does not offer %q: %q", want, wide)
		}
	}
}

// The sort hint follows the same rule the open and span hints do: a key is
// advertised where it does something and nowhere else. The timeline draws
// lanes and the raw log draws entries, neither of which has a column for a
// sort to reorder, so s is inert in both and must not be offered there.
func TestActionKeysOfferSortOnlyWhereThereIsATableToSort(t *testing.T) {
	for _, b := range views {
		t.Run(b.name, func(t *testing.T) {
			m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(b.key[0])}})
			_, sortable := tables[m.ActiveView()]
			if got := strings.Contains(m.actionKeys(160), sortHint); got != sortable {
				verb := "does not offer"
				if got {
					verb = "offers"
				}
				t.Errorf("%s view: has a table = %v, but the footer %s %q: %q", b.name, sortable, verb, sortHint, m.actionKeys(160))
			}
		})
	}
}

// The action line is clipped from its END, where "q quit" is, so its width
// is a budget every hint added to it spends from. detailInlineWidth is the
// narrowest terminal that still draws all three panes, and no view may
// exceed it: the reader of a 70-column terminal must still be able to see
// how to leave.
//
// This sweeps every view rather than naming one. Which view is widest is a
// function of which conditional hints that view happens to carry, so a test
// naming today's widest stops covering the question the moment a hint lands
// somewhere else.
func TestNoViewsActionLineOutgrowsTheNarrowestThreePaneWidth(t *testing.T) {
	for _, b := range views {
		t.Run(b.name, func(t *testing.T) {
			m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: detailInlineWidth, Height: 40})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(b.key[0])}})
			line := m.actionKeys(detailInlineWidth)
			if n := lipgloss.Width(line); n > detailInlineWidth {
				t.Errorf("the %s view's action line is %d columns, more than the %d a %d-column terminal gives it: %q", b.name, n, detailInlineWidth, detailInlineWidth, line)
			}
		})
	}
}

// A log with no spans of either tier draws capture guidance where the table
// would be -- no header, no columns, no rows -- so there is nothing for a
// sort to reorder even though the view is one that has a table. The hint
// asks what the frame CONTAINS, the way the open and span hints do, rather
// than which view is on: which view is on is a static fact, and this one is
// not.
func TestActionKeysDropTheSortHintWhenNoTableIsDrawn(t *testing.T) {
	m := update(t, New(testLog(t, "core-only.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	if _, sortable := tables[m.ActiveView()]; !sortable {
		t.Fatalf("the opening view has no table at all, so this cannot show the hint being dropped for want of ROWS")
	}
	if len(m.rows()) != 0 {
		t.Fatalf("fixture assumption changed: %d rows, want a log with nothing to sort", len(m.rows()))
	}
	if got := m.actionKeys(160); strings.Contains(got, sortHint) {
		t.Errorf("the footer offers %q over a frame drawing capture guidance instead of a table: %q", sortHint, got)
	}
}

// alert exists so a report answering an apparently-dead keystroke is seen.
// Drawn like ordinary text it would be a sentence in the footer where hints
// usually are, on a frame the reader is already puzzled by -- which is the
// whole of what it was added to prevent.
//
// Both states are asserted to SAY the right thing as well as to be marked,
// so a footer that lost its report entirely could not pass by being plain.
func TestAReportAnsweringADeadKeystrokeIsMarked(t *testing.T) {
	base := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	for _, tc := range []struct {
		what string
		set  func(m *Model)
		says string
	}{
		{"a jump the filter refused", func(m *Model) { m.blockedJump = true }, jumpBlockedNote},
		{"a search that found nothing", func(m *Model) {
			m.view, m.raw.notFound, m.raw.lastQuery = ViewRawLog, true, "zzzz"
		}, "/zzzz  pattern not found"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			m := base
			tc.set(&m)
			got := m.footer(100)
			if unstyled(got) != tc.says {
				t.Fatalf("the footer reads %q, not the report this is about (%q)", unstyled(got), tc.says)
			}
			if !strings.HasPrefix(got, "\x1b[") {
				t.Errorf("%s is drawn like ordinary text: %q", tc.what, got)
			}
		})
	}
}

// Every line that explains why a pane is empty is drawn as a note. They are
// the frames a reader is most likely to be staring at wondering whether the
// tool broke, and the sentence answering them should not read as log
// content.
//
// One assertion per pane that can render one, because each composes its own
// and nothing makes them agree.
func TestEveryLineExplainingAnEmptyPaneIsDrawnAsANote(t *testing.T) {
	empty := func(t *testing.T, fixture string, view View) Model {
		t.Helper()
		m := update(t, New(testLog(t, fixture), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
		m.view = view
		showOnly(t, &m, dimProvider)
		return m
	}
	for _, tc := range []struct {
		what   string
		render func(t *testing.T) string
	}{
		{"the table", func(t *testing.T) string { m := empty(t, "provider-rpc.log", ViewCalls); return m.renderList(80, 20) }},
		{"the raw log", func(t *testing.T) string {
			m := empty(t, "provider-rpc.log", ViewRawLog)
			return m.renderRawLog(80, 20)
		}},
		{"the timeline", func(t *testing.T) string {
			m := empty(t, "timeline.log", ViewTimeline)
			return m.renderTimeline(80, 20)
		}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			out := tc.render(t)
			if !strings.Contains(unstyled(out), noMatchNote) {
				t.Fatalf("%s does not explain itself, so there is nothing here to be marked:\n%s", tc.what, out)
			}
			for _, line := range strings.Split(out, "\n") {
				if !strings.Contains(unstyled(line), noMatchNote) {
					continue
				}
				if !strings.Contains(line, "\x1b[") {
					t.Errorf("%s explains itself in ordinary text: %q", tc.what, line)
				}
			}
		})
	}

	// The raw log has a second one, for a log the scanner found nothing in
	// at all. No filter is involved, so it reaches a different branch from
	// the note above it and needs asserting separately.
	t.Run("a log with no entries", func(t *testing.T) {
		m := update(t, New(&model.Log{}, "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
		out := m.renderRawLog(80, 20)
		if !strings.Contains(unstyled(out), noEntriesNote) {
			t.Fatalf("the raw log does not explain itself, so there is nothing here to be marked:\n%s", out)
		}
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("an empty log is explained in ordinary text: %q", out)
		}
	})

	// The detail pane's placeholder is the fourth, and it needs no filter --
	// only a selection that stands for nothing.
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m.rowsCache, m.rowsCached, m.selected = []row{{cells: []string{"x"}, spanIdx: noSpanIdx}}, true, 0
	_, out := m.renderDetail(50, 20)
	if !strings.Contains(unstyled(out), noSelectionNote) {
		t.Fatalf("the detail pane draws no placeholder, so there is nothing here to be marked:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(unstyled(line), noSelectionNote) && !strings.Contains(line, "\x1b[") {
			t.Errorf("the detail placeholder is drawn in ordinary text: %q", line)
		}
	}
}

// styleHintKeys accents the key and nothing else, and never changes what a
// line says or how wide it is.
//
// The shapes that matter are the ones the footer actually composes: a hint
// whose words contain a space, several hints on one line, and a line clipped
// down to a bare key -- which is left alone rather than accented whole,
// since there is no word left for the accent to be marking.
func TestStyleHintKeysAccentsTheKeyAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		in      string
		accents int
	}{
		{"1 providers  2 types  6 raw log  ? help", 4},
		{"⇥ pane  Esc clear  q quit", 3},
		{"q", 0},
		{"", 0},
		{"⇥ pane  ␣", 1},
	} {
		got := styleHintKeys(tc.in)
		if unstyled(got) != tc.in {
			t.Errorf("styleHintKeys(%q) says %q -- the text must be untouched", tc.in, unstyled(got))
		}
		if lipgloss.Width(got) != lipgloss.Width(tc.in) {
			t.Errorf("styleHintKeys(%q) is %d columns, want %d", tc.in, lipgloss.Width(got), lipgloss.Width(tc.in))
		}
		if n := strings.Count(got, "\x1b[0m"); n != tc.accents {
			t.Errorf("styleHintKeys(%q) accented %d fragments, want %d: %q", tc.in, n, tc.accents, got)
		}
	}
}

// The value is budgeted against the pane less the indent, and nothing else.
// Under the old shape it was the pane less a six-column label column, so
// this is the four columns the change buys -- the whole point of it, and the
// part a reader would notice only as an ellipsis that stopped appearing.
func TestADetailValueIsClippedAgainstThePaneLessTheIndent(t *testing.T) {
	const w = 20
	const indent = 2
	fits := strings.Repeat("a", w-indent)
	got := unstyledLines(detailFieldLines([]detailField{
		{label: "resource", value: fits, kind: headIdentifierColumn},
	}, w))
	if len(got) != 2 {
		t.Fatalf("detailFieldLines = %q, want two lines", got)
	}
	if v := detailValueFor(t, got, "resource"); v != "  "+fits {
		t.Errorf("a value of exactly %d columns was clipped: %q, want %q", w-indent, v, "  "+fits)
	}
	over := detailValueFor(t, unstyledLines(detailFieldLines([]detailField{
		{label: "resource", value: fits + "a", kind: headIdentifierColumn},
	}, w)), "resource")
	if !strings.Contains(over, "…") {
		t.Errorf("a value one column over the budget was not marked as clipped: %q", over)
	}
	if n := lipgloss.Width(over); n > w {
		t.Errorf("a clipped value line is %d columns, more than the %d the pane gives it: %q", n, w, over)
	}
}

// The label is drawn in the label style and the value is not, in the frame
// rather than in the theme. Without this the render site's choice of style
// is held only by the goldens, whose failure mode is a regenerated file --
// swap fieldLabel for chrome and every label silently loses its accent,
// with the diff reading as an intended restyle.
func TestADetailLabelIsAccentedAndItsValueIsNot(t *testing.T) {
	lines := detailFieldLines([]detailField{
		{label: "provider", value: "registry.terraform.io/hashicorp/aws", kind: tailIdentifierColumn},
	}, 40)
	if len(lines) != 2 {
		t.Fatalf("detailFieldLines = %q, want two lines", lines)
	}
	accented := sgrPrefix(styleRenderer.NewStyle().Foreground(accent).Render("x"))
	if accented == "" {
		t.Fatal("the accent renders no escape sequence, so this cannot tell an accented line from a plain one")
	}
	if got := sgrPrefix(lines[0]); !strings.Contains(got, strings.TrimSuffix(strings.TrimPrefix(accented, "\x1b["), "m")) {
		t.Errorf("the label line opens with %q, which does not carry the accent %q", got, accented)
	}
	if got := sgrPrefix(lines[1]); got != "" {
		t.Errorf("the value line opens with %q, want no styling -- the value is the content, not the scaffolding", got)
	}
}

// The label is told from the value it heads by an accent, and by the DIM
// WEIGHT underneath it. Colour is withheld by not setting a foreground (see
// newTheme), so a label carrying the accent alone would render byte-identical
// to its own value under NO_COLOR -- collapsing the pane into an
// undifferentiated column of lines for exactly the readers who cannot have
// the tint. The indent survives there too, but the indent alone does not say
// which of two lines is the label.
func TestADetailLabelStaysMarkedWithColourWithheld(t *testing.T) {
	plain := newTheme(false)
	if got := plain.fieldLabel.Render("provider"); !strings.Contains(got, "\x1b[2m") {
		t.Errorf("with colour off, a detail label is not dimmed: rendered %q, want it to contain %q", got, "\x1b[2m")
	}
}

// A field whose value is empty says so. An RPC-level call such as
// GetProviderSchema belongs to no resource type, so its span carries an
// empty ResourceType -- a fact about the call, not a failure to render it.
// Under the folded layout an empty value left its label with an empty
// column beside it, which reads as empty; on its own line it leaves the
// label heading a blank line, which reads as a field the pane could not
// fill in. It is spelled with model.FacetKey's own word, so the detail pane
// and the facet pane call the same absence the same thing on one frame.
func TestADetailFieldWithNoValueSaysSo(t *testing.T) {
	s := span.Span{RPC: "GetProviderSchema", Provider: "aws", DurationMs: 12}
	if s.ResourceType != "" {
		t.Fatal("fixture assumption changed: the span carries a resource type, so this exercises nothing")
	}
	lines := unstyledLines(spanDetailLines(s, attrib.Attribution{}, false, 40))
	if got := detailValueFor(t, lines, "resource type"); strings.TrimSpace(got) == "" {
		t.Errorf("an empty resource type renders as %q -- a label over a blank line", got)
	}
	if got := detailValueFor(t, lines, "resource type"); !strings.Contains(got, model.FacetKey("")) {
		t.Errorf("an empty resource type renders as %q, want it to state the absence as %q", got, model.FacetKey(""))
	}
}

// A pane with more to show than it has room for says there is MORE, rather
// than marking the cut with a bare ellipsis. The two cuts a pane makes are
// otherwise told apart only by two columns of indentation: a value clipped
// for width carries an ellipsis in the value position, and a height cut
// carries one where a label would sit. A reader cannot be asked to read a
// figure's absence off an indent.
func TestAPaneWithMoreToShowSaysSoRatherThanMarkingACut(t *testing.T) {
	if !strings.Contains(moreBelowMark, "more") {
		t.Fatalf("moreBelowMark is %q, which does not say there is more to show", moreBelowMark)
	}
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	_, whole := m.renderDetail(50, 40)
	full := strings.Split(unstyled(whole), "\n")
	_, cut := m.renderDetail(50, len(full)-2)
	short := strings.Split(unstyled(cut), "\n")
	if last := short[len(short)-1]; last != moreBelowMark {
		t.Errorf("a pane with content below the fold ends on %q, want %q", last, moreBelowMark)
	}
}

// A UI-hook span's address is the only per-resource identifier that tier
// has, and it is told from its siblings by its TAIL: two resources of one
// module share every character up to the last segment. Clipped from the
// wrong end, "module.networking.aws_subnet.a" and "…b" render as identical
// text in a narrow pane.
//
// It is asserted here because nothing else reaches it: no golden fixture
// carries UI-hook spans, so this direction was held by nothing at all.
func TestAUIHookAddressKeepsItsTailWhenClipped(t *testing.T) {
	s := span.Span{
		RPC: "create", Provider: "aws", ResourceType: "aws_subnet",
		Address:  "module.networking.module.private_subnets.aws_subnet.this",
		Fidelity: span.FidelityUIReported,
	}
	got := detailValueFor(t, unstyledLines(spanDetailLines(s, attrib.Attribution{}, false, 20)), "address")
	if !strings.Contains(got, "…") {
		t.Fatalf("the address renders whole as %q at 20 columns, so this exercises no clipping", got)
	}
	if !strings.HasSuffix(got, "aws_subnet.this") {
		t.Errorf("clipped address = %q, want the tail that tells it from its siblings", got)
	}
}

// The help's own key table is the only place o is named -- it carries no
// footer hint, deliberately, because the action line has no columns to
// spare. TestHelpDocumentsEveryKeyTheFooterAdvertises sweeps footer into
// help and so cannot see it, which left the entry held by the help golden
// alone: a golden's failure is answered by regenerating it.
func TestTheHelpNamesTheKeysThatHaveNoFooterHint(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	open := update(t, m, helpKey)
	rendered := unstyled(open.View())
	// Matched against the KEY COLUMN of a line, not against the screen: "o"
	// is a letter that appears in almost every description, so a substring
	// search over the whole help can never fail.
	for _, key := range []string{"o", "n N", "PgUp PgDn"} {
		named := false
		for _, line := range strings.Split(rendered, "\n") {
			if strings.HasPrefix(strings.Trim(line, " │"), key+" ") {
				named = true
				break
			}
		}
		if !named {
			t.Errorf("the help's key column does not name %q, which no footer hint advertises either:\n%s", key, rendered)
		}
	}
}

// runeColumns reports which columns of line hold r. Columns rather than byte
// offsets: the box-drawing characters are multi-byte and single-column, so a
// byte offset would answer a question no rendered frame asks.
func runeColumns(line string, r rune) []int {
	var cols []int
	for i, c := range []rune(line) {
		if c == r {
			cols = append(cols, i)
		}
	}
	return cols
}

// frameRules reads the card borders from a frame tall enough for a blank
// below navigation. The bottom border precedes the status and actions.
func frameRules(t *testing.T, frame string) (top, bottom string) {
	t.Helper()
	lines := strings.Split(unstyled(frame), "\n")
	if len(lines) < 6 {
		t.Fatalf("frame of %d lines has no pane row", len(lines))
	}
	top = lines[3]
	for _, ln := range lines[2:] {
		if ln != "" && strings.Trim(ln, paneRule+"╰╯ ") == "" {
			bottom = ln
		}
	}
	if bottom == "" {
		t.Fatalf("no bottom rule in frame:\n%s", strings.Join(lines, "\n"))
	}
	return top, bottom
}

// The pane row is closed top and bottom. Without the bottom rule the panes
// trail off into a column of separators -- at the default height that is
// most of the frame -- and nothing on screen says where the content ends
// and the empty space begins.
func TestThePaneRowIsRuledTopAndBottom(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "plan.log")
	for _, w := range []int{70, 100, 160} {
		m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
		top, bottom := frameRules(t, m.View())
		if !strings.HasPrefix(top, "╭─") && !strings.HasPrefix(top, "╭▶") {
			t.Errorf("width %d: top rule does not open with a rule: %q", w, top)
		}
		if got := lipgloss.Width(bottom); got != w {
			t.Errorf("width %d: bottom rule is %d columns", w, got)
		}
	}
}

// Each card's rounded corners line up with its own vertical borders.
func TestThePanelCornersAlignWithTheirBodyBorders(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "plan.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	top, bottom := frameRules(t, m.View())
	lines := strings.Split(unstyled(m.View()), "\n")
	facetW := facetPaneWidth(m.facetPaneNatural+4, 160)
	listW := layoutCentreWidth(m, 160)
	starts := []int{0, facetW + 1, facetW + listW + 2}
	ends := []int{facetW - 1, facetW + listW, 159}
	if got := runeColumns(top, '╭'); !slices.Equal(got, starts) {
		t.Errorf("top starts %v, want %v", got, starts)
	}
	if got := runeColumns(bottom, '╰'); !slices.Equal(got, starts) {
		t.Errorf("bottom starts %v, want %v", got, starts)
	}
	if got := runeColumns(top, '╮'); !slices.Equal(got, ends) {
		t.Errorf("top ends %v, want %v", got, ends)
	}
	if got := runeColumns(bottom, '╯'); !slices.Equal(got, ends) {
		t.Errorf("bottom ends %v, want %v", got, ends)
	}
	want := []int{starts[0], ends[0], starts[1], ends[1], starts[2], ends[2]}
	if got := runeColumns(lines[4], '│'); !slices.Equal(got, want) {
		t.Errorf("body borders %v, want %v", got, want)
	}
}

// Every pane is named in the top rule, the facet pane included. It is the
// one pane holding no name anywhere in its own body -- it windows around its
// cursor, so which dimension heading sits at its top depends on where the
// cursor is -- so it is the pane a rule-borne name can silently leave
// unnamed.
func TestEveryPaneIsNamedInTheTopRule(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "plan.log")
	m := update(t, base, tea.WindowSizeMsg{Width: 160, Height: 40})
	// By position, not by a search of the whole rule: three names present in
	// any order satisfies a Contains sweep, and two panes wearing each
	// other's name is a worse frame than one pane wearing none.
	//
	// Spelled out rather than read back from facetPaneTitle and
	// spanDetailTitle. Compared against the constants, this asserts that the
	// rule carries whatever they happen to say -- emptying one leaves "" on
	// both sides and the test passes, which leaves the names themselves held
	// by the goldens alone, and a golden's failure is answered by
	// regenerating it.
	if got, want := paneTitlesOf(t, m.View()), []string{"Filters", "Calls", "Span detail"}; !slices.Equal(got, want) {
		t.Errorf("top rule names %q, want %q", got, want)
	}
}

// The detail pane's focus is marked on its title in the rule. It is the one
// pane with no cursor row of its own, so without this Tab's third stop
// leaves no mark anywhere on the frame.
func TestTheFocusedDetailPaneIsMarkedInTheTopRule(t *testing.T) {
	base := update(t, New(testLog(t, "mixed-hcp.log"), "plan.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	unfocused := strings.Split(strings.Split(unstyled(base.View()), "\n")[3], "╮ ╭")[detailPaneIndex]
	if strings.Contains(unfocused, "▶") {
		t.Errorf("detail title is marked with the keyboard on the list pane: %q", unfocused)
	}
	m := base
	for range focusablePaneCount(&m, 160) {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
		if m.pane == PaneDetail {
			break
		}
	}
	if m.pane != PaneDetail {
		t.Fatal("Tab never reached the detail pane")
	}
	// Asserted against the detail pane's own SEGMENT of the rule, not the
	// rule as a whole. Marking any other pane's name satisfies "a bar
	// appears somewhere", which is the reading that would let the mark move
	// to the facet pane unnoticed -- and Tab's third stop would then be as
	// invisible as it was with no mark at all.
	segments := strings.Split(strings.Split(unstyled(m.View()), "\n")[3], "╮ ╭")
	if len(segments) != 3 {
		t.Fatalf("top rule split into %d segments, want 3", len(segments))
	}
	for i, segment := range segments {
		marked := strings.Contains(segment, "▶")
		if want := i == detailPaneIndex; marked != want {
			t.Errorf("rule segment %d marked = %v, want %v: %q", i, marked, want, segment)
		}
	}
}

// focusablePaneCount is how many stops Tab has at width w, so the sweep
// above bounds itself rather than looping until it happens to land.
func focusablePaneCount(m *Model, w int) int {
	return len(m.focusablePanes(w))
}

// A pane row too short for both rules keeps CONTENT over chrome. Two rules
// and no body is a frame that spends every line it has saying where the
// content would have been.
func TestAShortPaneRowKeepsContentOverChrome(t *testing.T) {
	p := pane{title: "CALLS", content: "first\nsecond\nthird", width: 20}
	for _, c := range []struct {
		h        int
		wantBody []string
	}{
		{1, nil},
		{2, []string{"first"}},
		{3, []string{"first"}},
		{4, []string{"first", "second"}},
	} {
		lines := strings.Split(unstyled(framePanes(c.h, p)), "\n")
		if len(lines) != c.h {
			t.Errorf("h %d: framePanes returned %d lines", c.h, len(lines))
			continue
		}
		var body []string
		for _, ln := range lines[1:] {
			if strings.Trim(ln, "─╰╯ ") != "" {
				body = append(body, strings.Trim(ln, " │"))
			}
		}
		if !slices.Equal(body, c.wantBody) {
			t.Errorf("h %d: body %q, want %q", c.h, body, c.wantBody)
		}
	}
}

// The frame fills the terminal exactly, at every height. Not merely "no
// taller than": a frame one line SHORT leaves a strip of dead terminal under
// the footer, and one line too tall loses its topmost line -- the header
// naming the open file -- off the top of the screen, since bubbletea's
// renderer keeps only the last h lines of what View returns.
//
// This checks the whole budget including navigation, cards, status and actions.
func TestTheFrameFillsTheTerminalExactly(t *testing.T) {
	for _, c := range append(wholeFrameCases(t), singlePaneFrameCases(t)...) {
		for _, w := range []int{160, 100, 70, 40} {
			for h := 1; h <= 40; h++ {
				m := update(t, c.m, tea.WindowSizeMsg{Width: w, Height: h})
				if n := len(strings.Split(m.View(), "\n")); n != h {
					t.Errorf("%s at %dx%d: View() is %d lines\n%s", c.name, w, h, n, unstyled(m.View()))
				}
			}
		}
	}
}

// A shortened panel name carries an ellipsis within the available border.
func TestANarrowPanelMarksItsClippedName(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  string
	}{
		{8, "Fi…"}, {11, "Filte…"}, {12, "Filters"},
	} {
		rule := unstyled(titledRule(pane{title: "FILTERS", width: tc.width}))
		if lipgloss.Width(rule) != tc.width || !strings.Contains(rule, tc.want) {
			t.Errorf("width %d: rule %q, want name %q within the border", tc.width, rule, tc.want)
		}
	}
}

// The detail pane's title says what the cursor is on, not how much room
// there is to describe it. A pane row of one line -- a terminal of five --
// draws the rule and nothing else, so a title that answered the height
// instead would put "nothing is selected" on the only line the reader gets,
// over a live selection.
func TestTheDetailTitleIsTheSameAtEveryHeight(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	want, _ := m.renderDetail(40, 20)
	if want != spanDetailTitle {
		t.Fatalf("the calls view's detail pane is headed %q, want %q -- fixture assumption changed", want, spanDetailTitle)
	}
	for _, h := range []int{20, 3, 2, 1, 0, -1} {
		if got, _ := m.renderDetail(40, h); got != want {
			t.Errorf("at body height %d the pane is headed %q, want %q", h, got, want)
		}
	}
}

// The footer says which of Esc's two meanings is live. They cannot both be
// advertised at once and the reader cannot be asked to guess: "back" where a
// jump is waiting to be undone, "clear" everywhere else.
func TestTheFooterNamesWhicheverEscMeaningIsLive(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	if got := unstyled(m.footer(160)); !strings.Contains(got, escClearHint) {
		t.Errorf("the calls view's footer does not offer %q:\n%s", escClearHint, got)
	}

	jumped := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	got := unstyled(jumped.footer(160))
	if !strings.Contains(got, escBackHint) {
		t.Errorf("the footer does not offer %q after a jump:\n%s", escBackHint, got)
	}
	if strings.Contains(got, escClearHint) {
		t.Errorf("the footer offers both of Esc's meanings at once:\n%s", got)
	}

	returned := update(t, jumped, tea.KeyMsg{Type: tea.KeyEsc})
	if got := unstyled(returned.footer(160)); strings.Contains(got, escBackHint) {
		t.Errorf("the footer still offers %q once the return has been spent:\n%s", escBackHint, got)
	}
}

// The caveat is as wide as it says it is, and the short form is no wider
// than the long one.
//
// Its width is a measured number that decides where the text starts being
// clipped mid-sentence, and it was stated in prose and held by nothing --
// so an edit to the wording moved the truth and left the claim behind.
// caveatWidth is that number, checked here against the lines themselves.
//
// The short form's bound is its own: it answers a frame short of HEIGHT, so
// nothing else stops it being written wider than the text it stands in for
// and clipping at a width the full caveat would have survived.
func TestTheLoggingCaveatIsAsWideAsItSaysItIs(t *testing.T) {
	widest := 0
	for _, line := range fullLoggingCaveat {
		widest = max(widest, lipgloss.Width(line))
	}
	if widest != caveatWidth {
		t.Errorf("the full caveat's longest line is %d columns, caveatWidth says %d", widest, caveatWidth)
	}
	if got := lipgloss.Width(shortLoggingCaveat); got > caveatWidth {
		t.Errorf("the short caveat is %d columns, wider than the full caveat's %d", got, caveatWidth)
	}
}

// Neither caveat may claim the rankings are safe. Spans emit wildly
// different volumes of logging -- a measured 4 to 1934 entries under one
// request id in a single capture -- and the tax is charged per line, so a
// chatty call is inflated more than a quiet one and the order they are
// ranked in is approximate.
//
// This is a text assertion because the defect was a text defect: the caveat
// read "Rankings hold, since every span paid the same cost", which is
// reassurance the tool's own --diagnose counters contradict, on the one
// line shown over every frame.
func TestNeitherCaveatPromisesTheRankingsHold(t *testing.T) {
	for _, s := range append(append([]string{}, fullLoggingCaveat...), shortLoggingCaveat) {
		for _, forbidden := range []string{"Rankings hold", "rankings hold", "same cost", "only rankings transfer"} {
			if strings.Contains(s, forbidden) {
				t.Errorf("a caveat line promises the rankings survive: %q contains %q", s, forbidden)
			}
		}
	}
}

// The pane title carries the scope's SIZE, not merely that there is one. The
// measured spread is min 4, median 7, max 1934 entries, so "one call" would
// be the same words over a pane the reader can read in full and over one
// holding eight per cent of the log's id-bearing lines.
func TestAScopedRawLogNamesItsSize(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got, want := m.centreTitle(), "RAW LOG (4 entries)"; got != want {
		t.Errorf("centreTitle = %q, want %q", got, want)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
	if got, want := m.centreTitle(), "RAW LOG"; got != want {
		t.Errorf("centreTitle = %q after unscoping, want %q", got, want)
	}
}

// plural's singular branch is exercised by no fixture: the spec's own
// measured minimum scope on a real capture is 4 entries, and no fixture in
// testdata puts a request id on exactly one entry. A log built directly is
// what it takes to reach a scope of one.
func TestAScopedRawLogNamesASingleEntrySingular(t *testing.T) {
	l := manyEntryLog(1)
	l.Entries[0].ReqID = 1
	l.RPCSpans = []span.Span{{Entry: 0, ReqID: 1}}

	m := New(l, "x.log")
	m.jumpToSpan(l.RPCSpans, 0)
	if got, want := len(m.raw.scope), 1; got != want {
		t.Fatalf("scope has %d entries, want %d -- this does not reach the branch it means to pin", got, want)
	}
	if got, want := m.centreTitle(), "RAW LOG (1 entry)"; got != want {
		t.Errorf("centreTitle = %q, want %q", got, want)
	}
}

// The action line offers the key that widens, and only while there is
// something to widen.
func TestTheFooterOffersTheWideningKeyOnlyWhileScoped(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	if got := unstyled(m.footer(200)); strings.Contains(got, scopeHint) {
		t.Errorf("the calls view offers %q:\n%s", scopeHint, got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := unstyled(m.footer(200)); !strings.Contains(got, scopeHint) {
		t.Errorf("a scoped raw log does not offer %q:\n%s", scopeHint, got)
	}
}

// The scoped action line fits its budget. The existing sweep reaches each
// view by pressing its number key, which goes through setView and spends the
// scope, so it measures the unscoped line and cannot see this one.
func TestTheScopedActionLineFitsTheNarrowestThreePaneWidth(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	line := strings.Split(unstyled(m.footer(200)), "\n")
	action := line[len(line)-1]
	if got := lipgloss.Width(action); got > detailInlineWidth {
		t.Errorf("the scoped action line is %d columns, over the %d budget: %q", got, detailInlineWidth, action)
	}
}

// scopeHint's key must be the one Update's "\\" case actually dispatches on,
// not merely whatever word follows it: TestHelpDocumentsEveryKeyTheFooterAdvertises
// and both help goldens all passed unchanged when scopeHint's text was
// replaced with "| whole log", because none of them reach a view through a
// jump and so never render a footer with a scope live.
func TestScopeHintNamesTheKeyThatDropsTheScope(t *testing.T) {
	key, _, ok := strings.Cut(scopeHint, " ")
	if !ok {
		t.Fatalf("scopeHint has no key field: %q", scopeHint)
	}
	if key != "\\" {
		t.Errorf("scopeHint names key %q, want %q, the key Update's case dispatches a dropped scope on", key, "\\")
	}
}

// TestTheFooterNeverLosesQuitAtWidthsTheActionLineFits reaches every view
// through setView, which spends the scope (see setView), so it renders only
// the UNSCOPED raw log's footer and cannot see a scoped one at all. This is
// its sibling for a raw log actually reached by a jump.
//
// The sweep starts at 30, not at the scoped line's own natural width, because
// the defect this covers was a natural width big enough to lose "q quit"
// at 60 columns -- a width with two committed goldens (help-60.txt,
// timeline-60.txt) -- while still being under detailInlineWidth and so
// invisible to TestTheScopedActionLineFitsTheNarrowestThreePaneWidth.
func TestTheFooterNeverLosesQuitAtWidthsTheScopedActionLineFits(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.raw.scope == nil {
		t.Fatal("Enter built no scope, so this does not exercise the scoped footer")
	}
	for w := 60; w <= 200; w++ {
		lines := strings.Split(m.footerText(w), "\n")
		action := lines[len(lines)-1]
		if !strings.Contains(action, "q quit") {
			t.Errorf("scoped raw log at %d columns: action line %q has lost the quit hint", w, action)
		}
	}
}
