package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestTimelineDrawsTheRPCTierWhenTheLogHasOne(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	tier, spans := m.timelineSpans()
	if tier != tierRPC {
		t.Fatalf("tier = %v, want tierRPC", tier)
	}
	if len(spans) == 0 {
		t.Error("no spans: the RPC tier was chosen but nothing was returned")
	}
	for _, s := range spans {
		if s.Fidelity != span.FidelityReported {
			t.Fatalf("span %+v is not RPC fidelity; PackLanes will refuse this slice", s)
		}
	}
}

func TestTimelineFallsBackToTheUITierWhenThereAreNoRPCSpans(t *testing.T) {
	// structured-ui.log carries the UI-hook tier and no RPC spans at all.
	m := New(testLog(t, "structured-ui.log"), "x.log")
	tier, spans := m.timelineSpans()
	if tier != tierUI {
		t.Fatalf("tier = %v, want tierUI", tier)
	}
	for _, s := range spans {
		if s.Fidelity != span.FidelityUIReported {
			t.Fatalf("span %+v is not UI fidelity; PackLanes will refuse this slice", s)
		}
	}
}

func TestFilteringOutEveryRPCSpanDoesNotSwitchTiers(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	// Narrow the provider dimension to a value no span carries. The facet
	// maps are unexported and toggleSelectedFacetValue works off the facet
	// pane's cursor, so this test is in package tui and sets them directly,
	// as facets_test.go does.
	m.selectedFacets = map[string]map[string]bool{dimProvider: {"registry.terraform.io/hashicorp/nothing": true}}
	m.invalidateRows()
	tier, spans := m.timelineSpans()
	if tier != tierRPC {
		t.Errorf("tier = %v, want tierRPC: a filter must not change which tier is drawn", tier)
	}
	if len(spans) != 0 {
		t.Errorf("spans = %d, want 0: the filter matches nothing", len(spans))
	}
}

func TestLaneBarFillsEveryColumnForASpanCoveringTheWindow(t *testing.T) {
	spans := []span.Span{{StartMs: 0, EndMs: 1000, DurationMs: 1000}}
	got := laneBar(spans, model.Lane{Spans: []int{0}}, 1000, 20)
	if want := strings.Repeat("█", 20); got != want {
		t.Errorf("laneBar = %q, want %q", got, want)
	}
}

func TestLaneBarLeavesTheGapBetweenTwoSpansBlank(t *testing.T) {
	// Two spans in one lane, each a quarter of the window, with half the
	// window idle between them. The blank columns ARE the point of this
	// view: idle time has to be visible as space.
	spans := []span.Span{
		{StartMs: 0, EndMs: 250, DurationMs: 250},
		{StartMs: 750, EndMs: 1000, DurationMs: 250},
	}
	got := laneBar(spans, model.Lane{Spans: []int{0, 1}}, 1000, 20)
	want := strings.Repeat("█", 5) + strings.Repeat(" ", 10) + strings.Repeat("█", 5)
	if got != want {
		t.Errorf("laneBar = %q, want %q", got, want)
	}
}

func TestLaneBarGivesAShortSpanOneColumn(t *testing.T) {
	// 1ms in a 10s window is 0.002 of one column at this width. It still
	// gets a column: the log recorded a real call, and rounding it away
	// makes the lane look emptier than it was.
	spans := []span.Span{{StartMs: 5000, EndMs: 5001, DurationMs: 1}}
	got := laneBar(spans, model.Lane{Spans: []int{0}}, 10000, 20)
	if strings.Count(got, "█") != 1 {
		t.Errorf("laneBar = %q, want exactly one bar column", got)
	}
}

func TestLaneBarIsAlwaysExactlyBarWColumns(t *testing.T) {
	// Every lane row is a pane column, so a row wider or narrower than
	// barW pushes the panes beside it out of alignment on that line only
	// -- the failure mode that is hardest to see in a screenshot and
	// easiest to catch here.
	spans := []span.Span{
		{StartMs: 0, EndMs: 1, DurationMs: 1},
		{StartMs: 999, EndMs: 1000, DurationMs: 1},
		{StartMs: 400, EndMs: 600, DurationMs: 200},
	}
	for _, barW := range []int{1, 7, 20, 79} {
		got := laneBar(spans, model.Lane{Spans: []int{0, 2, 1}}, 1000, barW)
		if w := lipgloss.Width(got); w != barW {
			t.Errorf("laneBar(barW=%d) is %d display columns: %q", barW, w, got)
		}
	}
}

func TestLaneBarSurvivesAZeroWindow(t *testing.T) {
	// Every span zero-duration, so spanMs is 0. Dividing by it would panic
	// and take the whole interface down on a log that is merely unusual.
	spans := []span.Span{{StartMs: 0, EndMs: 0}}
	got := laneBar(spans, model.Lane{Spans: []int{0}}, 0, 20)
	if w := lipgloss.Width(got); w != 20 {
		t.Errorf("laneBar over a zero window is %d columns: %q", w, got)
	}
}

func TestTimeAxisNamesBothEnds(t *testing.T) {
	got := timeAxis(522200, 40)
	if w := lipgloss.Width(got); w != 40 {
		t.Fatalf("timeAxis is %d display columns, want 40: %q", w, got)
	}
	if !strings.HasPrefix(got, "0s") {
		t.Errorf("timeAxis does not start at 0s: %q", got)
	}
	if !strings.HasSuffix(got, "522.2s") {
		t.Errorf("timeAxis does not end at the window total: %q", got)
	}
}
