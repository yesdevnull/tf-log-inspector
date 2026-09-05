package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// timelineModel is a model showing the timeline over timeline.log, sized so
// every pane is drawn. Each test starts from its own, because Model is
// driven through a pointer and shares its filter maps when copied.
func timelineModel(t *testing.T) Model {
	t.Helper()
	m := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
}

func TestTimelineCursorStopsAtTheLastLane(t *testing.T) {
	m := timelineModel(t)
	for i := 0; i < 50; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	lanes := m.timelineLanes()
	if m.timeline.lane != len(lanes)-1 {
		t.Errorf("lane = %d after walking off the end, want %d", m.timeline.lane, len(lanes)-1)
	}
}

func TestTimelineCursorStopsAtTheLastSpanInALane(t *testing.T) {
	m := timelineModel(t)
	for i := 0; i < 50; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	lanes := m.timelineLanes()
	if last := len(lanes[m.timeline.lane].Spans) - 1; m.timeline.span != last {
		t.Errorf("span = %d after walking off the end, want %d", m.timeline.span, last)
	}
}

func TestChangingLanesClampsTheSpanCursor(t *testing.T) {
	// Step deep into a long lane, then move to a shorter one. Leaving the
	// index where it was would point past the end of the new lane, and
	// every reader of it would have to bounds-check for itself.
	m := timelineModel(t)
	for i := 0; i < 50; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	lanes := m.timelineLanes()
	if m.timeline.span >= len(lanes[m.timeline.lane].Spans) {
		t.Errorf("span = %d in a lane of %d spans", m.timeline.span, len(lanes[m.timeline.lane].Spans))
	}
	if _, ok := m.selectedTimelineSpan(); !ok {
		t.Error("selectedTimelineSpan reports nothing selected in a non-empty lane")
	}
}

func TestAFilterThatEmptiesTheTimelineSelectsNothing(t *testing.T) {
	m := timelineModel(t)
	m.selectedFacets = map[string]map[string]bool{dimProvider: {"registry.terraform.io/hashicorp/nothing": true}}
	m.invalidateRows()
	if idx, ok := m.selectedTimelineSpan(); ok {
		t.Errorf("selectedTimelineSpan = (%d, true) over an empty timeline, want ok == false", idx)
	}
}

func TestEnterFromTheTimelineOpensTheSelectedSpanInTheRawLog(t *testing.T) {
	m := timelineModel(t)
	idx, ok := m.selectedTimelineSpan()
	if !ok {
		t.Fatal("nothing selected on a timeline with spans")
	}
	_, spans := m.timelineSpans()
	want := int(spans[idx].Entry)

	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("view = %v after Enter, want ViewRawLog", m.view)
	}
	if m.TopEntry() != want {
		t.Errorf("top entry = %d, want %d (the entry that closed the selected span)", m.TopEntry(), want)
	}
}

// TestTimelineDetailPaneShowsTheSelectedSpan checks that the detail pane
// beside the timeline describes the span the cursor is on, the same way it
// describes a call row's span in the table views
// (TestDetailPaneShowsTheSelectedSpan).
func TestTimelineDetailPaneShowsTheSelectedSpan(t *testing.T) {
	m := timelineModel(t)
	idx, ok := m.selectedTimelineSpan()
	if !ok {
		t.Fatal("nothing selected on a timeline with spans")
	}
	_, spans := m.timelineSpans()
	want := spans[idx]

	// 80 columns is wide enough that the full provider address is not
	// clipped, matching TestDetailPaneShowsTheSelectedSpan.
	got := detailBody(t, m, spanDetailTitle, 80, 20)
	for _, s := range []string{want.RPC, want.Provider, formatMs(uint64(want.DurationMs))} {
		if !strings.Contains(got, s) {
			t.Errorf("timeline detail pane missing %q:\n%s", s, got)
		}
	}
}

// TestTimelineDetailPaneShowsAddressForAUIHookSpan checks the timeline's own
// reachable path to spanDetailLines' UI-hook branch: structured-ui.log has
// no RPC spans, so the timeline falls back to the UI tier (see
// timelineSpans), and its cursor's selected span is a UI-hook span whose
// detail must carry its resource address.
func TestTimelineDetailPaneShowsAddressForAUIHookSpan(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	idx, ok := m.selectedTimelineSpan()
	if !ok {
		t.Fatal("nothing selected on a timeline with spans")
	}
	_, spans := m.timelineSpans()
	want := spans[idx]
	if want.Fidelity != span.FidelityUIReported {
		t.Fatalf("selected span fidelity = %v, want FidelityUIReported", want.Fidelity)
	}

	got := detailBody(t, m, spanDetailTitle, 80, 20)
	if !strings.Contains(got, want.Address) {
		t.Errorf("timeline detail pane omits the UI-hook span's address %q:\n%s", want.Address, got)
	}
}

// laneLabelSpans builds a minimal span slice for laneLabels tests: only
// Provider matters to them, so every other field is left zero.
func laneLabelSpans(providers ...string) []span.Span {
	spans := make([]span.Span, len(providers))
	for i, p := range providers {
		spans[i] = span.Span{Provider: p}
	}
	return spans
}

// TestLaneLabelsNameEachLaneByItsProvider checks the one-provider-per-lane
// case: each label is that lane's provider's short name (the last
// "/"-segment of a registry address).
func TestLaneLabelsNameEachLaneByItsProvider(t *testing.T) {
	spans := laneLabelSpans("registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google")
	lanes := []model.Lane{{Spans: []int{0}}, {Spans: []int{1}}}
	got := laneLabels(spans, lanes)
	want := []string{"aws/1", "google/1"}
	if !slices.Equal(got, want) {
		t.Errorf("laneLabels = %v, want %v", got, want)
	}
}

// TestLaneLabelsNumberEachProviderIndependently is the case Task 6's review
// found the plain row-index numbering got wrong: with lanes ["aws",
// "google", "aws"] in that order, the THIRD lane must read "aws/2" -- aws's
// own second lane -- not "aws/3", which would count google's lane as one of
// aws's own. A reader (and Task 7's stall text, which names a lane like
// "aws/1") is looking for a provider's Nth lane, not the Nth row.
func TestLaneLabelsNumberEachProviderIndependently(t *testing.T) {
	spans := laneLabelSpans(
		"registry.terraform.io/hashicorp/aws",
		"registry.terraform.io/hashicorp/google",
		"registry.terraform.io/hashicorp/aws",
	)
	lanes := []model.Lane{{Spans: []int{0}}, {Spans: []int{1}}, {Spans: []int{2}}}
	got := laneLabels(spans, lanes)
	want := []string{"aws/1", "google/1", "aws/2"}
	if !slices.Equal(got, want) {
		t.Errorf("laneLabels = %v, want %v", got, want)
	}
}

// TestLaneLabelsReportMixedForALaneSpanningProviders checks the case
// PackLanes can produce and this package must decide something for: it
// packs purely on timing, with no notion of provider, so two different
// providers' spans can land in the same lane whenever their intervals do
// not overlap. Naming just the first span's provider would misattribute
// every other span in the lane to a provider it is not from, so the label
// says "mixed" instead.
func TestLaneLabelsReportMixedForALaneSpanningProviders(t *testing.T) {
	spans := laneLabelSpans("registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google")
	lanes := []model.Lane{{Spans: []int{0, 1}}}
	got := laneLabels(spans, lanes)
	want := []string{"mixed/1"}
	if !slices.Equal(got, want) {
		t.Errorf("laneLabels = %v, want %v", got, want)
	}
}

// TestLaneLabelsNumberMixedLanesAsTheirOwnSeries checks that "mixed" is
// counted the same way every other provider bucket is: a second mixed lane
// is "mixed/2", not a second "mixed/1" -- laneLabels treats it as one more
// bucket among the others, not as a special case exempt from numbering.
func TestLaneLabelsNumberMixedLanesAsTheirOwnSeries(t *testing.T) {
	spans := laneLabelSpans(
		"registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google", // lane 0: mixed
		"registry.terraform.io/hashicorp/aws",                                           // lane 1: aws
		"registry.terraform.io/hashicorp/google", "registry.terraform.io/hashicorp/aws", // lane 2: mixed
	)
	lanes := []model.Lane{{Spans: []int{0, 1}}, {Spans: []int{2}}, {Spans: []int{3, 4}}}
	got := laneLabels(spans, lanes)
	want := []string{"mixed/1", "aws/1", "mixed/2"}
	if !slices.Equal(got, want) {
		t.Errorf("laneLabels = %v, want %v", got, want)
	}
}

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

// TestTimelineDrawsTheRPCTierForALogCarryingBoth pins the tier choice on
// the one case the other tier tests cannot reach: a log carrying BOTH
// tiers. timeline.log is RPC-only and structured-ui.log UI-only, so an
// implementation that preferred the UI tier wherever UI spans exist would
// satisfy every one of them. A wrong tier choice is silent -- it draws the
// other tier's bars and numbers rather than failing -- so the ambiguous log
// needs its own pin.
func TestTimelineDrawsTheRPCTierForALogCarryingBoth(t *testing.T) {
	l := testLog(t, "two-tier.log")
	if len(l.RPCSpans) == 0 || len(l.UISpans) == 0 {
		t.Fatalf("fixture assumption changed: two-tier.log has %d RPC and %d UI spans, want both non-empty", len(l.RPCSpans), len(l.UISpans))
	}
	m := New(l, "x.log")
	tier, spans := m.timelineSpans()
	if tier != tierRPC {
		t.Fatalf("tier = %v, want tierRPC: a log carrying both tiers draws RPC", tier)
	}
	for _, s := range spans {
		if s.Fidelity != span.FidelityReported {
			t.Fatalf("span %+v is not RPC fidelity; the UI tier leaked into the RPC tier's spans", s)
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

func TestLaneBarLightsAPartiallyOccupiedFinalColumn(t *testing.T) {
	// 50ms/column at this width. [40,60) starts inside column 0 and ends
	// inside column 1 -- ms 50-60 belong to column 1, so that column must
	// be lit too, not just the one the span started in.
	spans := []span.Span{{StartMs: 40, EndMs: 60, DurationMs: 20}}
	got := laneBar(spans, model.Lane{Spans: []int{0}}, 1000, 20)
	want := strings.Repeat("█", 2) + strings.Repeat(" ", 18)
	if got != want {
		t.Errorf("laneBar = %q, want %q", got, want)
	}
}

// TestLaneBarPacksExactlyTheColumnsEachSpanCovers checks CONTENT, not just
// width, over the same three-span fixture TestLaneBarIsAlwaysExactlyBarWColumns
// uses. That test allocates its comparison string as barW bools and writes
// every one of them, so it cannot fail on a geometry error -- it is a fixed
// point of the string-building loop, not of the column mapping. The lit
// columns here are instead worked out by hand from the column-boundary rule
// (a start floors into the column it falls in; an end ceils, since a span
// still running through any part of a column occupies it) rather than by
// calling laneCol or laneEndCol, so a regression in that mapping that still
// produces a barW-long string is still caught.
func TestLaneBarPacksExactlyTheColumnsEachSpanCovers(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 1, DurationMs: 1},
		{StartMs: 999, EndMs: 1000, DurationMs: 1},
		{StartMs: 400, EndMs: 600, DurationMs: 200},
	}
	cases := []struct {
		barW int
		lit  []int // columns each of the three spans covers, hand-derived
	}{
		{1, []int{0}},
		{7, []int{0, 2, 3, 4, 6}},
		{20, []int{0, 8, 9, 10, 11, 19}},
		{79, []int{0, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 78}},
	}
	for _, c := range cases {
		on := make([]bool, c.barW)
		for _, col := range c.lit {
			on[col] = true
		}
		var b strings.Builder
		for _, filled := range on {
			if filled {
				b.WriteRune('█')
			} else {
				b.WriteByte(' ')
			}
		}
		want := b.String()

		got := laneBar(spans, model.Lane{Spans: []int{0, 2, 1}}, 1000, c.barW)
		if got != want {
			t.Errorf("laneBar(barW=%d) = %q, want %q", c.barW, got, want)
		}
	}
}

// TestTimelineTierIsNoneForALogWithNeitherTier is the reachability test for
// tierNone: without it, timelineSpans falls through to tierUI with zero
// spans, and the empty pane could never tell "no timed spans in this log"
// apart from "the filter hid them".
func TestTimelineTierIsNoneForALogWithNeitherTier(t *testing.T) {
	m := New(&model.Log{}, "x.log")
	tier, spans := m.timelineSpans()
	if tier != tierNone {
		t.Fatalf("tier = %v, want tierNone", tier)
	}
	if spans != nil {
		t.Errorf("spans = %v, want nil", spans)
	}
}

// TestTimelineTitleNamesTheTier checks the pane title states which tier is
// drawn, and flags the UI tier's whole-second resolution so a reader does
// not mistake it for RPC precision.
func TestTimelineTitleNamesTheTier(t *testing.T) {
	rpc := New(testLog(t, "timeline.log"), "x.log")
	if got := rpc.timelineTitle(); got != "TIMELINE (rpc)" {
		t.Errorf("timelineTitle = %q, want %q", got, "TIMELINE (rpc)")
	}
	ui := New(testLog(t, "structured-ui.log"), "x.log")
	if got := ui.timelineTitle(); got != "TIMELINE (ui, whole seconds)" {
		t.Errorf("timelineTitle = %q, want %q", got, "TIMELINE (ui, whole seconds)")
	}
}

// TestRenderTimelineGivesCaptureGuidanceForALogWithNoTimedSpans checks a log
// with neither span tier gets the SAME guidance the table views give it, and
// that the guidance survives the pane it is drawn in.
//
// 44 columns is the centre pane at a 100-column terminal, not a generous
// width chosen to keep the text intact: captureGuidance is pre-wrapped to 40
// specifically so it fits there, and a single long line -- which clipWidth
// cuts with no ellipsis -- loses the environment variable names that are the
// whole point of the guidance while still reading as a finished sentence.
func TestRenderTimelineGivesCaptureGuidanceForALogWithNoTimedSpans(t *testing.T) {
	m := New(&model.Log{}, "x.log")
	const w, h = 44, 20
	// renderList is what the table views answer this log with; the timeline
	// must not teach a second phrasing of the same advice.
	want := m.renderList(w, h)
	got := m.renderTimeline(w, h)
	if got != want {
		t.Fatalf("renderTimeline over a log with no spans =\n%s\nwant renderList's own guidance:\n%s", got, want)
	}
	for _, name := range []string{"TF_LOG_PROVIDER=TRACE", "TF_LOG_SDK_PROTO=TRACE"} {
		if !strings.Contains(got, name) {
			t.Errorf("guidance at %d columns lost %s, the actionable half of it:\n%s", w, name, got)
		}
	}
}

// TestRenderTimelineReportsTheFilterWhenItHidesEverySpan checks that an
// active filter matching nothing reads differently from a log that never had
// timed spans -- the two are different states and must not look the same on
// screen (see TestRenderTimelineGivesCaptureGuidanceForALogWithNoTimedSpans).
func TestRenderTimelineReportsTheFilterWhenItHidesEverySpan(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	m.selectedFacets = map[string]map[string]bool{dimProvider: {"registry.terraform.io/hashicorp/nothing": true}}
	m.invalidateRows()
	if got := m.renderTimeline(80, 10); got != noMatchNote {
		t.Errorf("renderTimeline over a filter matching nothing = %q, want %q", got, noMatchNote)
	}
}

// TestRenderTimelineDrawsOneRowPerLanePlusTheAxis pins the composition this
// package owns: laneBar, timeAxis and laneLabels are tested in isolation
// elsewhere, so this checks only that renderTimeline stacks one labelled bar
// per lane, in PackLanes' order (via timelineLanes), then the axis --
// indented to sit under the bar area, past the label column every lane row
// reserves -- then the stall annotation (see stallAnnotation) as the final
// line(s).
//
// Cursor styling is stripped before comparing: lane 0 carries the cursor by
// default, and the point of this test is the composition, not cursorBar's own
// escape sequences.
func TestRenderTimelineDrawsOneRowPerLanePlusTheAxis(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	got := m.renderTimeline(40, 10)
	lines := strings.Split(got, "\n")

	lanes := m.timelineLanes()
	annotationLines := strings.Split(m.stallAnnotation(40), "\n")
	wantLines := len(lanes) + 1 + len(annotationLines)
	if len(lines) != wantLines {
		t.Fatalf("renderTimeline produced %d lines, want %d (one per lane, the axis, and the stall annotation)", len(lines), wantLines)
	}

	_, spans := m.timelineSpans()
	wallClock := timelineWallClockMs(spans)
	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	barW := 40 - labelW - 1

	var scratch []byte
	for i, lane := range lanes {
		want := padRight(labels[i], labelW) + " " + laneBar(spans, lane, wallClock, barW)
		var got string
		got, scratch = logfmt.StripANSI(lines[i], scratch)
		if got != want {
			t.Errorf("lane %d = %q, want %q", i, got, want)
		}
	}
	if want := strings.Repeat(" ", labelW+1) + timeAxis(wallClock, barW); lines[len(lanes)] != want {
		t.Errorf("axis line = %q, want %q", lines[len(lanes)], want)
	}
	for i, want := range annotationLines {
		if got := lines[len(lanes)+1+i]; got != want {
			t.Errorf("annotation line %d = %q, want %q", i, got, want)
		}
	}
}

// TestRenderTimelineKeepsTheAxisWhenLanesDoNotFit checks that a pane too
// short for every lane still keeps the axis rather than losing it to
// whichever lane happened to come last -- the axis is what every lane bar is
// drawn against, so it survives ahead of any of them.
func TestRenderTimelineKeepsTheAxisWhenLanesDoNotFit(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	got := m.renderTimeline(40, 1)
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Fatalf("renderTimeline(h=1) produced %d lines, want 1", len(lines))
	}

	lanes := m.timelineLanes()
	_, spans := m.timelineSpans()
	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	barW := 40 - labelW - 1
	want := strings.Repeat(" ", labelW+1) + timeAxis(timelineWallClockMs(spans), barW)
	if lines[0] != want {
		t.Errorf("renderTimeline(h=1) = %q, want the axis alone: %q", lines[0], want)
	}
}

// TestRenderTimelineKeepsALaneRowWhenTheAnnotationWouldFillThePane checks
// the other half of that priority: the stall annotation gives way before the
// last lane row does. The bars are what this view exists to draw and the
// annotation explains them, so a pane with room for only one of the two
// keeps the bar -- the same ordering the axis's own never-dropped rule
// states, and the frame states again when it shortens the logging caveat
// rather than the footer.
//
// timeline-many-stalls.log reports maxStallsShown stalls, which is exactly
// what a four-line pane has room for once the axis has taken its line: the
// case where an unreserved lane row is lost entirely.
func TestRenderTimelineKeepsALaneRowWhenTheAnnotationWouldFillThePane(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-many-stalls.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if got := len(strings.Split(m.stallAnnotation(40), "\n")); got != maxStallsShown {
		t.Fatalf("fixture assumption changed: stallAnnotation is %d lines, want maxStallsShown (%d)", got, maxStallsShown)
	}

	got := m.renderTimeline(40, 4)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("renderTimeline(h=4) produced %d lines, want 4:\n%s", len(lines), got)
	}

	lanes := m.timelineLanes()
	_, spans := m.timelineSpans()
	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	barW := 40 - labelW - 1
	row := padRight(labels[0], labelW) + " " + laneBar(spans, lanes[0], timelineWallClockMs(spans), barW)
	if want := cursorBar(row, 40, true); lines[0] != want {
		t.Errorf("first line = %q, want lane 0's bar %q: the annotation must not consume the last lane row", lines[0], want)
	}
}

// TestStallAnnotationNamesTheBlockingSpan checks the annotation's basic
// shape against timeline.log's own solo window.
func TestStallAnnotationNamesTheBlockingSpan(t *testing.T) {
	// timeline.log has one long span running alone after the others
	// finish; that span is what the annotation must name.
	m := timelineModel(t)
	got := m.stallAnnotation(80)
	if !strings.Contains(got, "idle") {
		t.Fatalf("stallAnnotation says nothing about idle lanes: %q", got)
	}
	if !strings.Contains(got, "waiting on") {
		t.Errorf("stallAnnotation does not name what the idle lanes waited on: %q", got)
	}
}

func TestStallAnnotationRendersOffsetsNotWallClock(t *testing.T) {
	// Span times are milliseconds from a per-builder zero point, not a
	// clock. Rendering them as "04:11:20" would be inventing a time of
	// day out of an offset.
	m := timelineModel(t)
	got := m.stallAnnotation(80)
	if strings.Contains(got, ":") {
		t.Errorf("stallAnnotation looks like a wall-clock time, which this log has no basis for: %q", got)
	}
}

func TestNoStallsSaysSoRatherThanRenderingNothing(t *testing.T) {
	// Silence and "no stalls" look identical on screen, and one of them
	// is a bug.
	m := timelineModel(t)
	m.selectedFacets = map[string]map[string]bool{dimProvider: {"registry.terraform.io/hashicorp/nothing": true}}
	m.invalidateRows()
	if got := m.stallAnnotation(80); strings.TrimSpace(got) == "" {
		t.Error("stallAnnotation is blank where there are no stalls to report")
	}
}

// longLabelStallModel is a model over synthetic spans whose provider name is
// longer than maxLaneLabelWidth, packed so one lane sits idle while the
// other runs: the case where the annotation names a lane the lane row can
// only draw clipped.
func longLabelStallModel(t *testing.T) Model {
	t.Helper()
	const provider = "registry.terraform.io/hashicorp/googleworkspace"
	return update(t, New(&model.Log{RPCSpans: []span.Span{
		{Provider: provider, StartMs: 0, EndMs: 1000, DurationMs: 1000, RPC: "PlanResourceChange", Fidelity: span.FidelityReported},
		{Provider: provider, StartMs: 0, EndMs: 1000, DurationMs: 1000, RPC: "PlanResourceChange", Fidelity: span.FidelityReported},
		{Provider: provider, StartMs: 5000, EndMs: 15000, DurationMs: 10000, RPC: "ApplyResourceChange", Fidelity: span.FidelityReported},
	}}, "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
}

// TestStallAnnotationNamesTheLaneAsTheLaneRowDrawsIt checks the annotation
// clips a lane label exactly as the lane row does. "waiting on X" is the
// annotation's whole justification for naming lanes rather than spans -- it
// points at exactly one bar -- and it points at nothing if the bar's own
// label column, capped at maxLaneLabelWidth, renders a different string.
func TestStallAnnotationNamesTheLaneAsTheLaneRowDrawsIt(t *testing.T) {
	m := longLabelStallModel(t)
	lanes := m.timelineLanes()
	if len(lanes) != 2 {
		t.Fatalf("synthetic spans packed into %d lanes, want 2 so one can sit idle", len(lanes))
	}

	got := m.stallAnnotation(80)
	if !strings.Contains(got, "waiting on …workspace/1") {
		t.Errorf("stallAnnotation = %q, want it to name the lane as the lane row draws it: …workspace/1", got)
	}
	if strings.Contains(got, "waiting on googleworkspace/1") {
		t.Errorf("stallAnnotation = %q names a label no lane row shows", got)
	}

	// The lane row is the other half of the claim: what it draws in its
	// label column must be the string the annotation just used.
	row, _ := logfmt.StripANSI(strings.Split(m.renderTimeline(80, 10), "\n")[0], nil)
	if !strings.HasPrefix(row, "…workspace/1") {
		t.Errorf("lane row = %q, want it to start with the label the annotation names", row)
	}
}

// TestStallAnnotationMarksATruncatedLine checks a pane too narrow for the
// whole sentence does not render the surviving head as though it were the
// whole of it: the tail names the lane, which is the payload, so the cut
// carries an ellipsis rather than clipWidth's silent chop.
func TestStallAnnotationMarksATruncatedLine(t *testing.T) {
	m := longLabelStallModel(t)
	const w = 30
	got := m.stallAnnotation(w)
	for _, line := range strings.Split(got, "\n") {
		if lipgloss.Width(line) > w {
			t.Fatalf("stallAnnotation line %q is %d columns, want at most %d", line, lipgloss.Width(line), w)
		}
		if !strings.HasSuffix(line, "…") {
			t.Errorf("stallAnnotation line %q is cut at %d columns but carries no marker saying so", line, w)
		}
	}
}

// TestStallAnnotationShowsAtMostTheTopFewByDuration checks the two things
// maxStallsShown exists for: the stalls kept are the LONGEST by duration,
// not the first chronologically, and no more than maxStallsShown are shown
// even when every stall in the log clears the threshold.
// timeline-many-stalls.log's four solo windows (5.7s, 5s, 4s, 2s, longest
// first) all clear it, and its own doc comment explains why
// testdata/timeline.log cannot exercise this: that fixture has only one
// surviving stall, so it never touches the sort or the truncation at all.
func TestStallAnnotationShowsAtMostTheTopFewByDuration(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-many-stalls.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	got := m.stallAnnotation(80)
	lines := strings.Split(got, "\n")
	if len(lines) != maxStallsShown {
		t.Fatalf("stallAnnotation produced %d lines, want %d (maxStallsShown): %q", len(lines), maxStallsShown, got)
	}

	want := []string{
		"1 lane idle 3.3s–9.0s waiting on aws/1",
		"1 lane idle 9.0s–14.0s waiting on aws/1",
		"1 lane idle 14.0s–18.0s waiting on aws/1",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("stallAnnotation = %v, want %v (the fixture's three longest solo windows, longest first)", lines, want)
	}
	if strings.Contains(got, "18.0s–20.0s") {
		t.Errorf("stallAnnotation names the fixture's shortest (2s) stall, which maxStallsShown should have dropped: %q", got)
	}
}

// TestTimelineLaneRowsScrollToKeepTheCursorOnScreen checks the scrolling
// renderTimeline gained when the lane cursor was added: the previous code
// always drew lanes[:laneRows] -- the first screenful, full stop -- so
// moving the cursor past it left the highlighted row off the visible pane
// entirely. timeline-many-lanes.log packs into five lanes (verified below
// against PackLanes rather than assumed); a pane with room for two of them,
// once the axis and the stall annotation have each taken their own line,
// must scroll to keep the LAST one, where the cursor is moved to, on screen.
func TestTimelineLaneRowsScrollToKeepTheCursorOnScreen(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-many-lanes.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	lanes := m.timelineLanes()
	if len(lanes) != 5 {
		t.Fatalf("timeline-many-lanes.log packs into %d lanes, want 5 (see the fixture's own doc comment)", len(lanes))
	}

	for i := 0; i < len(lanes)-1; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.timeline.lane != len(lanes)-1 {
		t.Fatalf("lane = %d after moving to the last lane, want %d", m.timeline.lane, len(lanes)-1)
	}

	// h=4 gives renderTimeline one line for the axis and one for the stall
	// annotation -- the fixture's five spans fully overlap, so no lane is
	// ever idle and stallAnnotation reports "no stalls" in a single line
	// -- leaving two lane rows, fewer than the fixture's five lanes. With
	// the cursor on lane 4 (0-based) and a two-row window, scrollWindow's
	// own pin-to-edge rule puts the window at lanes [3, 4]: lanes 0-2 must
	// have scrolled off, and the cursor's own lane 4 must be the LAST lane
	// row drawn, ahead of the axis and the annotation.
	got := m.renderTimeline(40, 4)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("renderTimeline(h=4) produced %d lines, want 4 (2 lane rows, the axis, and the stall annotation)", len(lines))
	}

	_, spans := m.timelineSpans()
	wallClock := timelineWallClockMs(spans)
	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	barW := 40 - labelW - 1
	rowContent := func(i int) string {
		return padRight(labels[i], labelW) + " " + laneBar(spans, lanes[i], wallClock, barW)
	}

	wantVisible := []int{3, 4}
	var scratch []byte
	for row, laneIdx := range wantVisible {
		want := rowContent(laneIdx)
		if laneIdx == m.timeline.lane {
			want = cursorBar(want, 40, true)
		}
		if lines[row] != want {
			t.Errorf("row %d = %q, want lane %d's row %q", row, lines[row], laneIdx, want)
		}
	}
	for _, hidden := range []int{0, 1, 2} {
		want, _ := logfmt.StripANSI(rowContent(hidden), scratch)
		for _, line := range lines[:2] {
			if stripped, s := logfmt.StripANSI(line, scratch); stripped == want {
				scratch = s
				t.Errorf("lane %d is still visible at %q; it should have scrolled off to keep the cursor's lane on screen", hidden, line)
			}
		}
	}
	if want := m.stallAnnotation(40); lines[3] != want {
		t.Errorf("last line = %q, want the stall annotation %q", lines[3], want)
	}
}
