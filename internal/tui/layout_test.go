package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
func TestNoLineExceedsTerminalWidth(t *testing.T) {
	base := New(testLog(t, "mixed-hcp.log"), "x.log")
	for _, w := range []int{70, 100, 160} {
		m := update(t, base, tea.WindowSizeMsg{Width: w, Height: 40})
		for _, line := range strings.Split(m.View(), "\n") {
			if n := len([]rune(line)); n > w {
				t.Errorf("width %d: line of %d runes: %q", w, n, line)
			}
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
// an overlay; above the threshold the facet pane is already shown inline, so
// there is nothing for 'f' to do.
func TestFTogglesTheFacetOverlayBelowInlineWidth(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.WindowSizeMsg{Width: 90, Height: 40})
	if strings.Contains(m.View(), "PROVIDERS") {
		t.Fatalf("facets shown inline at width 90, want collapsed:\n%s", m.View())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if !strings.Contains(m.View(), "PROVIDERS") {
		t.Errorf("'f' did not open the facet overlay:\n%s", m.View())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if strings.Contains(m.View(), "PROVIDERS") {
		t.Errorf("second 'f' did not close the facet overlay:\n%s", m.View())
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

// A rollup row (ViewProviders, the default view) has no single span to
// describe, so the detail pane must say so honestly rather than showing
// stale or zero-valued span fields.
func TestDetailPaneOnARollupRowIsHonest(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "x.log")
	out := m.renderDetail(40, 20)
	if strings.Contains(out, "\n"+m.log.RPCSpans[0].RPC) {
		t.Errorf("detail pane shows a specific span's RPC for a rollup row:\n%s", out)
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

// Fix round 2: capping each side pane at w/3 fixed the facet pane at 100
// columns (round 1) but left only w/3 for the centre pane -- the pane the
// answer actually lives in -- which pushed the providers view's numeric
// columns out entirely. 100 columns is the width Dan actually runs at, so
// this is the regression that would have caught it: the composed view at
// 100 columns must show a front-clipped provider name in the centre pane
// AND all three of its numeric columns whole.
func TestProvidersViewAt100ColumnsKeepsNumbersAndFrontClipsTheProvider(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "plan.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	out := m.View()
	if !strings.Contains(out, "…") {
		t.Errorf("centre pane's provider column was not front-clipped at 100 columns:\n%s", out)
	}
	// "5ms      1  5ms" is aws's total, calls and max columns rendered
	// together in one row -- all three numeric values, whole, in the order
	// the table actually renders them.
	if !strings.Contains(out, "5ms      1  5ms") {
		t.Errorf("providers view at 100 columns is missing one or more numeric columns:\n%s", out)
	}
}

// Fix round 3: round 2's fix let only the single widest text column
// front-clip, reserving every other text column at full natural width. The
// calls view has three text columns (RPC, resource type, provider); when
// one of the other two happened to be the widest, provider was left
// "reserved" at full natural width by fitColumnWidths but could still be
// cut by the trailing end-clip once the row as a whole overflowed -- from
// the wrong end, since an end-clip keeps a value's head, and two provider
// addresses only diverge in their tail. Two rows with different providers
// then rendered the same clipped prefix. A test asserting only that some
// provider text appears would not catch this, since the collided values
// are all non-empty -- it must assert the two rows' provider text actually
// differs. two-providers.log's two calls have different providers (aws,
// google) for exactly this reason.
//
// The expected clipped text is computed via fitColumnWidths and
// clipValueFront themselves, the same functions renderTable calls, rather
// than a hand-picked width: this is a white-box check that the real
// column-width policy -- whatever it is today or becomes later -- still
// keeps two different providers apart, not a pinned guess at its output.
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
	budget := centreWidth - selectedStyleOverhead
	if budget < 1 {
		budget = centreWidth
	}
	widths := fitColumnWidths(callColumns, columnWidths(callColumns, rows), budget)
	providerWidth := widths[len(callColumns)-1] // provider is callColumns' last column
	awsWant, googleWant := clipValueFront(aws, providerWidth), clipValueFront(google, providerWidth)
	if awsWant == googleWant {
		t.Fatalf("front-clipped provider text collided at the computed width %d -- test assumption is wrong: %q", providerWidth, awsWant)
	}

	out := m.View()
	if !strings.Contains(out, awsWant) {
		t.Errorf("calls view at 160 columns is missing the aws provider's distinguishing text %q:\n%s", awsWant, out)
	}
	if !strings.Contains(out, googleWant) {
		t.Errorf("calls view at 160 columns is missing the google provider's distinguishing text %q:\n%s", googleWant, out)
	}
}

// layoutCentreWidth returns the centre (list) pane's width for a terminal
// w columns wide, replicating renderPanes' own arithmetic for the
// w >= facetInlineWidth case this test renders at. Kept here rather than
// exported from layout.go, since nothing outside a test needs a pane width
// in isolation from actually rendering into it.
func layoutCentreWidth(m Model, w int) int {
	facetW := facetPaneWidth(m.facets, w)
	detailW := detailPaneWidth(m.log.RPCSpans, w)
	return w - facetW - detailW - 2*len([]rune(paneSep))
}
