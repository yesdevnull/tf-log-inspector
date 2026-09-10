package tui

import (
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
	"github.com/yesdevnull/tf-log-inspector/internal/profile"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

const mixedPositionFixture = `2022-12-15T00:16:20.790Z [TRACE] terraform: origin
2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=10
2021-12-15T00:16:20.800Z [TRACE] provider.google: Received downstream response: tf_rpc=PlanResourceChange tf_req_duration_ms=20
{"@level":"info","@timestamp":"2026-09-04T09:15:03Z","type":"apply_complete","hook":{"action":"read","elapsed_seconds":1,"resource":{"addr":"aws_instance.example","resource_type":"aws_instance","implied_provider":"aws"}}}`

func loadedMixedPositionLog(t *testing.T) *model.Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mixed-position.log")
	if err := os.WriteFile(path, []byte(mixedPositionFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// timelineModel is a model showing the timeline over timeline.log, sized so
// every pane is drawn. Each test starts from its own, because Model is
// driven through a pointer and shares its filter maps when copied.
func timelineModel(t *testing.T) Model {
	t.Helper()
	m := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
}

// positionedTimelineModel marks synthetic coordinates as intentional. Real
// scanner fixtures retain their recorded timestamp status.
func positionedTimelineModel(l *model.Log, name string) Model {
	for i := range l.RPCSpans {
		l.RPCSpans[i].TimestampStatus = logfmt.TimestampValid
	}
	for i := range l.UISpans {
		l.UISpans[i].TimestampStatus = logfmt.TimestampValid
	}
	return New(l, name)
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

// TestHAndLStepTheSpanCursorLikeTheArrowKeys pins the vi aliases the README
// and spanCursorHint both promise. ←/→ and h/l are the only way to reach any
// span in a lane but the first, and the footer advertises the pair as one
// hint, so a reader who reaches for h and l is reaching for what the
// interface told them was there.
func TestHAndLStepTheSpanCursorLikeTheArrowKeys(t *testing.T) {
	m := timelineModel(t)
	if n := len(m.timelineLanes()[m.timeline.lane].Spans); n < 2 {
		t.Fatalf("the selected lane holds %d spans, want at least 2 so the cursor has somewhere to step", n)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if m.timeline.span != 1 {
		t.Errorf("span = %d after l, want 1", m.timeline.span)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.timeline.span != 0 {
		t.Errorf("span = %d after h, want 0", m.timeline.span)
	}
}

// TestTheSpanCursorDoesNotMoveWhileTheFacetPaneHasFocus pins the focus gate
// on ←/→ and h/l. The within-lane cursor chooses what the detail pane
// describes and what Enter jumps to, so moving it while the keyboard is on
// the facet pane would rewrite one pane's selection from another pane's
// keys, with nothing on screen tying the two together -- the same rule Enter
// itself follows, acting only from the list pane.
func TestTheSpanCursorDoesNotMoveWhileTheFacetPaneHasFocus(t *testing.T) {
	// A lane of three, with the cursor parked in the middle of it, so that
	// BOTH directions have somewhere to go: on a lane of two the cursor
	// reaches an end after one step and an ungated key is indistinguishable
	// from a clamp.
	const aws = "registry.terraform.io/hashicorp/aws"
	spans := []span.Span{
		{Provider: aws, RPC: "ReadResource", StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: aws, RPC: "ReadResource", StartMs: 2000, EndMs: 3000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: aws, RPC: "ReadResource", StartMs: 4000, EndMs: 5000, DurationMs: 1000, Fidelity: span.FidelityReported},
	}
	m := update(t, positionedTimelineModel(&model.Log{RPCSpans: spans}, "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	was := m.timeline.span
	if was != 1 {
		t.Fatalf("span = %d after one → from the list pane, want 1 -- the cursor must start with a step available in each direction", was)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.Focus() != PaneFacets {
		t.Fatalf("focus = %v after f, want the facet pane", m.Focus())
	}
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRight},
		{Type: tea.KeyLeft},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'h'}},
	} {
		m = update(t, m, key)
		if m.timeline.span != was {
			t.Errorf("%s moved the span cursor to %d from the facet pane, want it left where the list pane put it, %d", key, m.timeline.span, was)
		}
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
	showOnly(t, &m, dimProvider)
	if idx, ok := m.selectedTimelineSpan(); ok {
		t.Errorf("selectedTimelineSpan = (%d, true) over an empty timeline, want ok == false", idx)
	}
}

// mixedFidelityModel is a model over one provider's spans carrying two
// different span.Fidelity values, which is what model.PackLanes refuses
// (model.ErrMixedTimelines) and what timelineLanes deliberately panics on.
//
// No builder produces such a log today -- ReportedBuilder is the only
// RPC-tier builder, so the tier is single-fidelity by construction -- so
// the shape is written here rather than captured.
func mixedFidelityModel(t *testing.T) Model {
	t.Helper()
	const aws = "registry.terraform.io/hashicorp/aws"
	return positionedTimelineModel(&model.Log{RPCSpans: []span.Span{
		{Provider: aws, RPC: "ReadResource", StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: aws, RPC: "ReadResource", StartMs: 2000, EndMs: 3000, DurationMs: 1000, Fidelity: span.FidelitySequential},
	}}, "x.log")
}

// TestAViewThatDrawsNoTimelineDoesNotPackItsLanes covers the blast radius
// of that panic. clampTimelineSelection packs lanes, and invalidateRows
// called it on every filter change and view switch, so a condition confined
// to view 5 fired in views that never draw a timeline -- an alt-screen
// crash, which is what leaves the user's terminal wrecked, on a keystroke
// that has nothing to do with the timeline. internal/profile returns this
// same condition as an error.
func TestAViewThatDrawsNoTimelineDoesNotPackItsLanes(t *testing.T) {
	m := update(t, mixedFidelityModel(t), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.ActiveView() != ViewProviders {
		t.Fatalf("view = %v after pressing 1, want the providers view", m.ActiveView())
	}
	if got := m.renderList(80, 20); got == "" {
		t.Error("the providers view drew nothing")
	}
}

// The gate must not cost the timeline its own clamp: a filter change while
// view 5 is on screen can empty the very lane the cursor is on, and a lane
// cursor left past the end of the lanes is what the clamp exists to catch.
func TestAFilterChangeStillClampsTheLaneCursorInTheTimeline(t *testing.T) {
	m := timelineModel(t)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.timeline.lane != 1 {
		t.Fatalf("lane = %d after ↓, want 1 -- timeline.log is meant to pack into two lanes", m.timeline.lane)
	}
	showOnly(t, &m, dimProvider, "registry.terraform.io/hashicorp/aws")
	if lanes := m.timelineLanes(); len(lanes) != 1 {
		t.Fatalf("aws alone packs into %d lanes, want 1", len(lanes))
	}
	if m.timeline.lane != 0 {
		t.Errorf("lane = %d over a single-lane timeline, want 0", m.timeline.lane)
	}
}

// TestAFilterThatNarrowsTheTimelineKeepsALiveCursor covers the state that
// makes invalidateRows' clamp load-bearing: a filter that NARROWS the
// timeline rather than emptying it.
//
// selectedTimelineSpan bounds-checks its own lane and span indices, so a
// filter that leaves nothing at all reads as "nothing selected" whether the
// clamp ran or not -- which is why TestAFilterThatEmptiesTheTimelineSelects
// Nothing cannot see the clamp. A filter that leaves lanes the cursor is
// past the end of is where the two answers differ: unclamped, the lane
// cursor names a lane that no longer exists and the span cursor sits past
// the end of the one it would land on, so the pane draws no cursor at all
// and the detail pane beside it falls to its placeholder, with nothing on
// screen saying why.
//
// The spans are synthetic because both cursors have to be out of range at
// once: the lane the cursor is on must disappear, and the lane it falls back
// to must hold fewer spans than the span cursor is at. No fixture packs a
// second lane deep enough for that.
func TestAFilterThatNarrowsTheTimelineKeepsALiveCursor(t *testing.T) {
	const aws, google = "registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google"
	spans := []span.Span{
		{Provider: aws, RPC: "ApplyResourceChange", StartMs: 0, EndMs: 9000, DurationMs: 9000, Fidelity: span.FidelityReported},
		{Provider: google, RPC: "ReadResource", StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: google, RPC: "ReadResource", StartMs: 2000, EndMs: 3000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: google, RPC: "ReadResource", StartMs: 4000, EndMs: 5000, DurationMs: 1000, Fidelity: span.FidelityReported},
	}
	m := update(t, positionedTimelineModel(&model.Log{RPCSpans: spans}, "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.timeline.lane != 1 || m.timeline.span != 2 {
		t.Fatalf("cursor = lane %d span %d, want lane 1 span 2 -- google's three calls do not overlap, so they pack into one lane", m.timeline.lane, m.timeline.span)
	}

	showOnly(t, &m, dimProvider, aws)

	lanes := m.timelineLanes()
	if len(lanes) != 1 || len(lanes[0].Spans) != 1 {
		t.Fatalf("aws alone packs into %d lanes, the first holding %d spans, want 1 and 1", len(lanes), len(lanes[0].Spans))
	}
	if m.timeline.lane != 0 || m.timeline.span != 0 {
		t.Errorf("cursor = lane %d span %d over a one-lane, one-span timeline, want lane 0 span 0", m.timeline.lane, m.timeline.span)
	}
	if _, ok := m.selectedTimelineSpan(); !ok {
		t.Error("selectedTimelineSpan reports nothing selected over a timeline that still draws a bar")
	}
	row := strings.Split(m.renderTimeline(60, 10), "\n")[0]
	if !strings.Contains(row, "\x1b[") {
		t.Errorf("the lane row %q carries no cursor styling, so the timeline draws no cursor at all", row)
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
	// The pane opens on the SCOPE's first member (see jumpToSpan), not
	// jumpContextLines above the entry that closed the span. timeline.log's
	// selected span is a single standalone "Received downstream response"
	// line, with no other traffic in the file sharing its id, so its scope
	// holds only its own entry and the pane lands exactly there -- "at or
	// before" is what this assertion actually needs.
	if m.TopEntry() > want {
		t.Errorf("top entry = %d, past the entry that closed the selected span, %d", m.TopEntry(), want)
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
// found the plain row-index numbering got wrong: the ordinal counts a
// provider's OWN lanes, so with lanes ["aws", "aws", "google"] the third
// reads "google/1" -- google's first lane -- not "google/3", which would
// count aws's two lanes as google's own. A reader (and the stall text,
// which names a lane like "aws/1") is looking for a provider's Nth lane,
// not the Nth row.
func TestLaneLabelsNumberEachProviderIndependently(t *testing.T) {
	spans := laneLabelSpans(
		"registry.terraform.io/hashicorp/aws",
		"registry.terraform.io/hashicorp/aws",
		"registry.terraform.io/hashicorp/google",
	)
	lanes := []model.Lane{{Spans: []int{0}}, {Spans: []int{1}}, {Spans: []int{2}}}
	got := laneLabels(spans, lanes)
	want := []string{"aws/1", "aws/2", "google/1"}
	if !slices.Equal(got, want) {
		t.Errorf("laneLabels = %v, want %v", got, want)
	}
}

// TestTimelineLanesPackEachProviderSeparately pins the rule the design spec
// states: lanes are packed over EACH PROVIDER's spans, not over the tier's
// whole set. These two calls never overlap, so packing on timing alone fits
// both in one lane -- a row that belongs to neither provider, and that the
// stall annotation could then only name after a provider neither of them
// is. "waiting on aws/1" pointing at exactly one bar is the annotation's
// whole justification for naming lanes at all, so the packing has to keep
// a lane nameable.
func TestTimelineLanesPackEachProviderSeparately(t *testing.T) {
	m := positionedTimelineModel(&model.Log{RPCSpans: []span.Span{
		{Provider: "registry.terraform.io/hashicorp/aws", RPC: "PlanResourceChange", StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: "registry.terraform.io/hashicorp/google", RPC: "PlanResourceChange", StartMs: 2000, EndMs: 3000, DurationMs: 1000, Fidelity: span.FidelityReported},
	}}, "x.log")
	lanes := m.timelineLanes()
	if len(lanes) != 2 {
		t.Fatalf("two non-overlapping providers packed into %d lanes, want one lane each", len(lanes))
	}
	_, spans := m.timelineSpans()
	if got := laneLabels(spans, lanes); !slices.Equal(got, []string{"aws/1", "google/1"}) {
		t.Errorf("laneLabels = %v, want [aws/1 google/1]", got)
	}
}

// TestTimelineLanesKeepEveryLaneIndexPointingAtItsOwnSpan is the aliasing
// hazard per-provider packing introduces: model.Lane holds indices into the
// slice PackLanes was given, and packing per provider gives it one GROUP at
// a time, so an unmapped index would resolve, in the tier's own span slice,
// to whichever span happened to sit at that position -- the detail pane
// describing one call while Enter jumped to another, with nothing on screen
// saying so.
//
// The fixture interleaves the providers (google, aws, google) so that group
// positions and tier positions disagree for every span but the first, and
// leaves the two google calls non-overlapping so google's own lane holds
// two of them.
func TestTimelineLanesKeepEveryLaneIndexPointingAtItsOwnSpan(t *testing.T) {
	m := positionedTimelineModel(&model.Log{RPCSpans: []span.Span{
		{Provider: "registry.terraform.io/hashicorp/google", RPC: "ReadResource", StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: "registry.terraform.io/hashicorp/aws", RPC: "PlanResourceChange", StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: "registry.terraform.io/hashicorp/google", RPC: "ApplyResourceChange", StartMs: 2000, EndMs: 3000, DurationMs: 1000, Fidelity: span.FidelityReported},
	}}, "x.log")

	lanes := m.timelineLanes()
	_, spans := m.timelineSpans()
	labels := laneLabels(spans, lanes)
	if !slices.Equal(labels, []string{"aws/1", "google/1"}) {
		t.Fatalf("lane labels = %v, want [aws/1 google/1]: providers are concatenated in ascending label order", labels)
	}
	for i, lane := range lanes {
		for _, idx := range lane.Spans {
			if got := laneLabelProvider(spans[idx].Provider); !strings.HasPrefix(labels[i], got+"/") {
				t.Errorf("lane %q holds span %d, whose provider is %q", labels[i], idx, got)
			}
		}
	}
	if got := len(lanes[1].Spans); got != 2 {
		t.Errorf("google's lane holds %d spans, want its 2 non-overlapping calls", got)
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

// TestTimelineKeepsRPCDurationsWhenTheirPositionsAreUnavailable catches the
// tempting but false fallback from unavailable RPC positions to UI timings.
// The RPC tier remains the selected evidence tier, and its admitted duration
// remains available to totals even though it cannot draw a temporal lane.
func TestTimelineKeepsRPCDurationsWhenTheirPositionsAreUnavailable(t *testing.T) {
	m := New(&model.Log{
		RPCSpans: []span.Span{{
			Entry: 1, DurationMs: 250, TimestampStatus: logfmt.TimestampMissing,
			Fidelity: span.FidelityReported,
		}},
		UISpans: []span.Span{{
			Entry: 2, StartMs: 0, EndMs: 100, DurationMs: 100,
			TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityUIReported,
		}},
	}, "x.log")

	tier, positioned := m.timelineSpans()
	if tier != tierRPC {
		t.Fatalf("tier = %v, want RPC", tier)
	}
	if len(positioned) != 0 {
		t.Fatalf("positioned spans = %d, want no drawable RPC spans", len(positioned))
	}
	if got := unstyled(m.renderTimeline(100, 10)); !strings.Contains(got, "Timeline positions unavailable: 1 admitted observations; 250ms retained in duration totals.") {
		t.Fatalf("timeline = %q, want unavailable-position explanation", got)
	}
}

func TestTimelineQualifiesPartialPositionCoverage(t *testing.T) {
	m := New(&model.Log{RPCSpans: []span.Span{
		{Entry: 1, StartMs: 0, EndMs: 100, DurationMs: 100, TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityReported},
		{Entry: 2, DurationMs: 50, TimestampStatus: logfmt.TimestampMissing, Fidelity: span.FidelityReported},
	}}, "x.log")
	if got := unstyled(m.renderTimeline(120, 10)); !strings.Contains(got, "Positioned 1 of 2 observations; excluded 1 (50ms).") {
		t.Fatalf("timeline = %q, want partial-position qualification", got)
	}
}

func TestTimelineExplainsExcludedPositionReasonsWithoutClippingTotals(t *testing.T) {
	m := New(&model.Log{RPCSpans: []span.Span{{
		DurationMs: 20, TimestampStatus: logfmt.TimestampBeforeOrigin, Fidelity: span.FidelityReported,
	}}}, "x.log")
	out := unstyled(m.renderTimeline(60, 25))
	for _, want := range []string{"20ms retained in duration totals.", "timestamp_before_origin"} {
		if !strings.Contains(out, want) {
			t.Errorf("timeline missing %q:\n%s", want, out)
		}
	}
}

func TestUnavailableTimelineMarksAHeightCut(t *testing.T) {
	m := New(&model.Log{RPCSpans: []span.Span{{
		DurationMs: 20, TimestampStatus: logfmt.TimestampBeforeOrigin, Fidelity: span.FidelityReported,
	}}}, "x.log")
	lines := strings.Split(unstyled(m.renderTimeline(60, 2)), "\n")
	if len(lines) != 2 {
		t.Fatalf("timeline rendered %d lines at height 2:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if strings.TrimRight(lines[1], " ") != moreBelowMark {
		t.Errorf("short unavailable timeline ends on %q, want marked cut %q", lines[1], moreBelowMark)
	}
}

func TestLoadedMixedTimingKeepsAdmittedTotalsAndChoosesRPC(t *testing.T) {
	l := loadedMixedPositionLog(t)
	if len(l.RPCSpans) != 2 || len(l.UISpans) != 1 {
		t.Fatalf("fixture spans = RPC %d UI %d, want 2 and 1", len(l.RPCSpans), len(l.UISpans))
	}
	m := New(l, "x.log")
	tier, timing := m.timelineTiming()
	if tier != tierRPC || len(timing.Positioned) != 1 || timing.AdmittedMs != 30 || timing.ExcludedMs != 20 {
		t.Fatalf("timeline = tier %v positioned %d admitted %d excluded %d, want RPC/1/30/20", tier, len(timing.Positioned), timing.AdmittedMs, timing.ExcludedMs)
	}
	var report strings.Builder
	if err := profile.Render(&report, l); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "duration 30ms") || !strings.Contains(report.String(), "positioned duration   10ms (1 excluded, 20ms)") {
		t.Errorf("profile does not retain the same admitted/positioned totals:\n%s", report.String())
	}
}

func TestLoadedFilterDistinguishesUnavailablePositionsFromNoMatch(t *testing.T) {
	m := New(loadedMixedPositionLog(t), "x.log")
	showOnly(t, &m, dimRPC, "PlanResourceChange")
	if got := unstyled(m.renderTimeline(100, 20)); !strings.Contains(got, "Timeline positions unavailable") {
		t.Errorf("filter selecting only unpositioned timing = %q, want unavailable positions", got)
	}
	showOnly(t, &m, dimRPC)
	if got := unstyled(m.renderTimeline(100, 20)); got != noMatchNote {
		t.Errorf("filter with no matches = %q, want %q", got, noMatchNote)
	}
}

func TestLoadedAllUnavailableRPCDoesNotFallBackToValidUI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unpositioned-rpc.log")
	content := strings.Replace(mixedPositionFixture,
		"2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=10",
		"2022-12-15T00:16:20.800Z [TRACE] terraform: after origin", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, "x.log")
	tier, positioned := m.timelineSpans()
	if tier != tierRPC || len(positioned) != 0 || len(l.UISpans) == 0 {
		t.Fatalf("tier=%v positioned=%d UI=%d, want unavailable RPC tier with available UI evidence", tier, len(positioned), len(l.UISpans))
	}
	if got := unstyled(m.renderTimeline(100, 20)); !strings.Contains(got, "Timeline positions unavailable") {
		t.Errorf("timeline switched away from unavailable RPC evidence:\n%s", got)
	}
}

func TestBusyNoteMarksAZeroLengthWindowUnavailable(t *testing.T) {
	spans := []span.Span{{TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityReported}}
	if got := busyNote(spans, 0); !strings.Contains(got, "unavailable") {
		t.Errorf("busyNote = %q, want unavailable fraction", got)
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
	// Untick every provider, so the dimension admits nothing and no span
	// survives. The facet maps are unexported and toggleFacetValue works off
	// the facet pane's cursor, so this test uses facets_test.go's helper
	// rather than driving the cursor across the pane.
	showOnly(t, &m, dimProvider)
	tier, spans := m.timelineSpans()
	if tier != tierRPC {
		t.Errorf("tier = %v, want tierRPC: a filter must not change which tier is drawn", tier)
	}
	if len(spans) != 0 {
		t.Errorf("spans = %d, want 0: the filter matches nothing", len(spans))
	}
}

// A facet filter compacts timelineSpans' copy of m.log.RPCSpans (see
// Filter.SpansMatching), so a span after a REMOVED one shifts position while
// m.log.Attribs stays indexed against the unfiltered slice. selectedDetail
// resolves the selected timeline span's attribution through
// model.Log.AttributionForEntry, keyed on span.Span.Entry rather than
// position, specifically so this cannot happen; this pins that against a
// naive positional lookup, which would misreport the span after the removed
// one as carrying the removed span's own neighbour's attribution.
//
// two-tier.log's RPCSpans are, in order: [0] PlanResourceChange, Ambiguous
// (2 candidates); [1] ApplyResourceChange/aws_instance, Contained ("web");
// [2] ApplyResourceChange/aws_subnet, Unattributed. Filtering to
// ApplyResourceChange only drops [0], so the filtered slice is [1, 2] at
// filtered positions 0 and 1. A positional lookup for filtered position 1
// would find Attribs[1] -- "web", Contained -- when the span actually
// there is [2], Unattributed.
func TestTimelineAttributionSurvivesAFacetFilterThatReindexesSpans(t *testing.T) {
	l := testLog(t, "two-tier.log")
	if len(l.RPCSpans) < 3 || len(l.Attribs) < 3 {
		t.Fatalf("fixture assumption changed: two-tier.log has %d RPC spans and %d attributions, want at least 3 of each", len(l.RPCSpans), len(l.Attribs))
	}
	if l.Attribs[0].Confidence != attrib.Ambiguous {
		t.Fatalf("fixture assumption changed: RPCSpans[0] is %v, want Ambiguous", l.Attribs[0].Confidence)
	}
	if l.Attribs[1].Confidence != attrib.Contained || l.Attribs[1].Name != "web" {
		t.Fatalf("fixture assumption changed: RPCSpans[1] is %v %q, want Contained \"web\"", l.Attribs[1].Confidence, l.Attribs[1].Name)
	}
	if l.Attribs[2].Confidence != attrib.Unattributed {
		t.Fatalf("fixture assumption changed: RPCSpans[2] is %v, want Unattributed", l.Attribs[2].Confidence)
	}

	m := update(t, New(l, "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	// Narrow to ApplyResourceChange, dropping the Ambiguous PlanResourceChange
	// span at RPCSpans[0]. See TestFilteringOutEveryRPCSpanDoesNotSwitchTiers
	// for why this test sets the filter through a helper rather than driving
	// the facet pane's own cursor.
	showOnly(t, &m, dimRPC, "ApplyResourceChange")

	_, spans := m.timelineSpans()
	if len(spans) != 2 {
		t.Fatalf("filtered RPC spans = %d, want 2", len(spans))
	}

	// Put the cursor on wherever RPCSpans[2] (by Entry, since the filter
	// shifted its position) landed among the packed lanes.
	var onTarget bool
	for laneIdx, lane := range m.timelineLanes() {
		for pos, si := range lane.Spans {
			if spans[si].Entry == l.RPCSpans[2].Entry {
				m.timeline.lane, m.timeline.span, onTarget = laneIdx, pos, true
			}
		}
	}
	if !onTarget {
		t.Fatal("RPCSpans[2] did not survive the filter into any lane")
	}

	_, sections := m.selectedDetail(80)
	var got string
	for _, s := range sections {
		got += strings.Join(s, "\n")
	}
	if !strings.Contains(got, unattributedValue) {
		t.Errorf("filtered timeline selection =\n%s\nwant it to contain %q, RPCSpans[2]'s own verdict", got, unattributedValue)
	}
	if strings.Contains(got, "web") || strings.Contains(got, attrib.Contained.String()) {
		t.Errorf("filtered timeline selection =\n%s\nwrongly carries RPCSpans[1]'s attribution (\"web\", Contained) -- a positional lookup reused RPCSpans[1] instead of following RPCSpans[2] to its new position", got)
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
	// makes the lane look emptier than it was. It gets the LIGHTEST
	// column, because 1ms of a 500ms column is what it is -- a mark saying
	// a call happened here, not a claim that the column was busy.
	spans := []span.Span{{StartMs: 5000, EndMs: 5001, DurationMs: 1}}
	got := laneBar(spans, model.Lane{Spans: []int{0}}, 10000, 20)
	if strings.Count(got, " ") != 19 {
		t.Errorf("laneBar = %q, want exactly one non-blank column", got)
	}
	if strings.Count(got, string(laneShades[0])) != 1 {
		t.Errorf("laneBar = %q, want its one column at the lightest shade %q", got, string(laneShades[0]))
	}
}

// TestLaneBarDrawsAZeroExtentSpanOnAColumnBoundary covers the two rules that
// keep an instantaneous call visible, both of which only bite on a span of
// no extent landing on a column boundary.
//
// A zero-extent span's start floors and its end ceils to the SAME column, so
// without the minimum-one-column bump its column range is empty and the call
// leaves no mark at all. A span at the window's LAST instant maps to column
// barW, one past the end, so without the start clamp the bump opens a range
// beyond the bar that the barW cap then closes straight back down to empty.
// Either failure draws a lane row with no bar in it over calls the log
// recorded -- work rendered as idle time, which is the inversion this view
// exists to refuse.
//
// The UI-hook tier makes this the ordinary shape rather than an edge case:
// Terraform rounds hook timings to whole seconds, so every resource it
// reports as "0s" is a zero-extent span (see callStartedBefore).
func TestLaneBarDrawsAZeroExtentSpanOnAColumnBoundary(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 0},
		{StartMs: 500, EndMs: 500},
		{StartMs: 1000, EndMs: 1000},
	}
	got := laneBar(spans, model.Lane{Spans: []int{0, 1, 2}}, 1000, 20)
	want := "░         ░        ░"
	if got != want {
		t.Errorf("laneBar = %q, want %q -- one mark per call, at the window's first column, its middle and its last", got, want)
	}
}

// TestTheUITierDrawsALaneOfZeroSecondWork is the same rule reached the way a
// reader reaches it. testdata/structured-ui.log has one of its two resources
// reported as "0s", and that resource is the only one of its provider, so
// per-provider packing gives it a lane of its own: lose the mark and the
// timeline draws a labelled row with nothing in it beside a lane that is
// nearly all bar.
func TestTheUITierDrawsALaneOfZeroSecondWork(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	tier, spans := m.timelineSpans()
	if tier != tierUI {
		t.Fatalf("tier = %v, want tierUI", tier)
	}
	lanes := m.timelineLanes()
	labels := laneLabels(spans, lanes)
	i := slices.Index(labels, "local/1")
	if i < 0 {
		t.Fatalf("lanes = %v, want one labelled local/1 (the 0s resource's own provider)", labels)
	}
	if len(lanes[i].Spans) != 1 {
		t.Fatalf("local/1 holds %d spans, want 1", len(lanes[i].Spans))
	}
	if s := spans[lanes[i].Spans[0]]; s.StartMs != s.EndMs {
		t.Fatalf("local/1's span runs %d-%dms, so the fixture no longer carries a 0s resource", s.StartMs, s.EndMs)
	}

	const barW = 46
	got := laneBar(spans, lanes[i], timelineWallClockMs(spans), barW)
	if strings.Count(got, string(laneShades[0])) != 1 {
		t.Errorf("the 0s resource's lane renders as %q, want exactly one %q mark", got, string(laneShades[0]))
	}
	if strings.Count(got, " ") != barW-1 {
		t.Errorf("the 0s resource's lane renders as %q, want its one mark and %d blank columns", got, barW-1)
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
	// be lit too, not just the one the span started in. Both are lit at the
	// lightest shade: the span accounts for 10 of each column's 50ms, and
	// neither column was anywhere near busy.
	spans := []span.Span{{StartMs: 40, EndMs: 60, DurationMs: 20}}
	got := laneBar(spans, model.Lane{Spans: []int{0}}, 1000, 20)
	want := strings.Repeat("░", 2) + strings.Repeat(" ", 18)
	if got != want {
		t.Errorf("laneBar = %q, want %q", got, want)
	}
}

// TestLaneBarPacksExactlyTheColumnsEachSpanCovers checks CONTENT, not just
// width, over the same three-span fixture TestLaneBarIsAlwaysExactlyBarWColumns
// uses. That test allocates its comparison string as barW columns and writes
// every one of them, so it cannot fail on a geometry error -- it is a fixed
// point of the string-building loop, not of the column mapping. The rows
// here are instead written out by hand from the column-boundary rule (a
// start floors into the column it falls in; an end ceils, since a span still
// running through any part of a column occupies it) together with the
// shading bands (█ only for a column a span fills entirely, ▓ from two
// thirds, ▒ from one third, ░ below that or merely touched), rather than
// by calling laneCol, laneEndCol or laneShadeFor, so a regression in any of
// them that still produces a barW-long string is still caught.
//
// The three spans are 1ms at 0, 200ms at [400,600) and 1ms at 999, over a
// 1000ms window. At barW=1 the single column stands for the whole window
// and holds 202 of its 1000ms, so the bar is the lightest shade rather than
// solid: the row is honest about a lane that was idle four fifths of the
// time, which the previous lit-or-unlit rendering was not. At barW=79 the
// columns are 12 or 13ms wide, and the 200ms span's first and last hold 5
// and 6 of those milliseconds -- both in the middle band, either side of
// fifteen columns it fills outright.
func TestLaneBarPacksExactlyTheColumnsEachSpanCovers(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 1, DurationMs: 1},
		{StartMs: 999, EndMs: 1000, DurationMs: 1},
		{StartMs: 400, EndMs: 600, DurationMs: 200},
	}
	cases := []struct {
		barW int
		want string
	}{
		{1, "░"},
		{7, "░ ░█░ ░"},
		{20, "░       ████       ░"},
		{79, "░" + strings.Repeat(" ", 30) + "▒" + strings.Repeat("█", 15) + "▒" + strings.Repeat(" ", 30) + "░"},
	}
	for _, c := range cases {
		if w := lipgloss.Width(c.want); w != c.barW {
			t.Fatalf("the hand-written row for barW=%d is %d columns wide", c.barW, w)
		}
		got := laneBar(spans, model.Lane{Spans: []int{0, 2, 1}}, 1000, c.barW)
		if got != c.want {
			t.Errorf("laneBar(barW=%d) = %q, want %q", c.barW, got, c.want)
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
	showOnly(t, &m, dimProvider)
	if got := unstyled(m.renderTimeline(80, 10)); got != noMatchNote {
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
	annotationLines := m.timelineNotes(40)
	wantLines := len(lanes) + 1 + len(annotationLines)
	if len(lines) != wantLines {
		t.Fatalf("renderTimeline produced %d lines, want %d (one per lane, the axis, and the notes beneath it)", len(lines), wantLines)
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
// drawn against, so it survives ahead of the SECOND lane row and of every
// one after it.
//
// It does not survive ahead of the FIRST: at the two heights where the axis
// and one bar cannot both be drawn, the bar is what the pane is for. See
// TestTheShortestPanesStillDrawALaneRow, which pins those.
//
// timeline.log packs into two lanes, and h=3 has room for one of them, the
// axis, and the mark for the notes that then go unsaid.
func TestRenderTimelineKeepsTheAxisWhenLanesDoNotFit(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	lanes := m.timelineLanes()
	if len(lanes) != 2 {
		t.Fatalf("timeline.log packs into %d lanes, want 2", len(lanes))
	}
	got := m.renderTimeline(40, 3)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("renderTimeline(h=3) produced %d lines, want 3", len(lines))
	}

	_, spans := m.timelineSpans()
	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	barW := 40 - labelW - 1
	// One of the two lanes is drawn, so the axis gutter carries the mark for
	// the other; see TestTheAxisMarksLaneRowsScrolledOffScreen.
	want := padRight("+1", labelW) + " " + timeAxis(timelineWallClockMs(spans), barW)
	if lines[1] != want {
		t.Errorf("renderTimeline(h=3) = %q, want the axis on its second line: %q", lines, want)
	}
}

// The axis used to cut its right label with clipWidth, which marks nothing,
// and it cut the two labels as one concatenated string. Both failures read
// as a complete number rather than a missing one: a 521.4s window on a bar
// eight columns wide rendered "0s521.4s", with the labels run together into
// one token, and five columns rendered "0s521" -- a number that is neither
// end of the axis and is off by three orders of magnitude. A sub-second
// window was worse still: 800ms rendered "0s800", which reads as seconds.
//
// The right label is therefore drawn only where it fits WHOLE with a column
// of space telling it from the left one, and where it does not the same
// ellipsis every other cut on this view carries stands in its place. What
// the reader loses is the window's size, which the notes below still carry;
// what they no longer get is a wrong one.
//
// These widths are reachable: barW is w-labelW-1, so a terminal of roughly
// 21 columns or fewer produces them.
func TestTheAxisDropsARightLabelItCannotShowWhole(t *testing.T) {
	for _, c := range []struct {
		name   string
		barW   int
		spanMs uint32
		want   string
	}{
		{"labels that would run together as one token", 8, 521400, "0s     " + axisLabelCutMark},
		{"a label that would be cut to a shorter number", 5, 521400, "0s  " + axisLabelCutMark},
		{"a sub-second window that would read as seconds", 5, 800, "0s  " + axisLabelCutMark},
		{"no room for even the mark", 3, 521400, "0s "},
		{"one column of separation is all it needs", 9, 521400, "0s 521.4s"},
		{"room to spare", 12, 521400, "0s    521.4s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := timeAxis(c.spanMs, c.barW)
			if got != c.want {
				t.Errorf("timeAxis(%d, %d) = %q, want %q", c.spanMs, c.barW, got, c.want)
			}
			if n := lipgloss.Width(got); n != c.barW {
				t.Errorf("timeAxis(%d, %d) = %q, %d columns wide, want exactly %d", c.spanMs, c.barW, got, n, c.barW)
			}
		})
	}
}

// A pane too short for every lane must SAY that lanes are missing. Without
// it, "five lanes, one drawn" and "one lane" render as the same frame:
// there is no scrollbar, and a lane that scrolled off leaves no gap behind
// it. Every other truncation on this view marks itself -- the stall list
// its top-N cut, the notes block its height cut, the detail pane beside it
// its own -- and a silence that reads as completeness is what all three
// exist to refuse.
//
// The mark rides in the axis row's label gutter, which is blank space the
// pane already spends, so it costs no lane row. timeline-many-lanes.log
// packs five lanes: h=5 has room for two of them, the axis and the two
// notes, and h=8 has room for all five.
func TestTheAxisMarksLaneRowsScrolledOffScreen(t *testing.T) {
	m := New(testLog(t, "timeline-many-lanes.log"), "x.log")
	lanes := m.timelineLanes()
	if len(lanes) != 5 {
		t.Fatalf("timeline-many-lanes.log packs into %d lanes, want 5 (see the fixture's own doc comment)", len(lanes))
	}
	_, spans := m.timelineSpans()
	labelW := laneLabelWidth(laneLabels(spans, lanes))
	const w = 60
	axis := timeAxis(timelineWallClockMs(spans), w-labelW-1)

	short := strings.Split(m.renderTimeline(w, 5), "\n")
	if len(short) != 5 {
		t.Fatalf("renderTimeline(h=5) produced %d lines, want 5 (two lane rows, the axis and two notes)", len(short))
	}
	if got, want := short[2], padRight("+3", labelW)+" "+axis; got != want {
		t.Errorf("axis row with two of five lanes drawn = %q, want %q -- the three that scrolled off go unmarked", got, want)
	}

	tall := strings.Split(m.renderTimeline(w, 8), "\n")
	if len(tall) != 8 {
		t.Fatalf("renderTimeline(h=8) produced %d lines, want 8 (five lane rows, the axis and two notes)", len(tall))
	}
	if got, want := tall[5], strings.Repeat(" ", labelW+1)+axis; got != want {
		t.Errorf("axis row with every lane drawn = %q, want %q -- nothing was cut, so nothing may be marked", got, want)
	}
}

// TestTheShortestPanesStillDrawALaneRow covers the two heights at which
// this view used to draw everything except the thing it exists for.
//
// At h == 1 the axis was appended unconditionally and the lane rows got
// what was left of the height, which was nothing: two lanes holding 7.5s of
// work rendered as an axis over empty space. Blank lane area is precisely
// how this view says "nothing ran", so that frame did not show less than
// the truth, it showed the opposite of it.
//
// At h == 2 the notes block had no room at all and was dropped whole -- the
// busy summary and every stall line -- with no mark, so the frame read as a
// complete one-lane timeline with nothing worth noting beneath it. The
// detail pane in the very same frame marks its own height cut with an
// ellipsis.
func TestTheShortestPanesStillDrawALaneRow(t *testing.T) {
	m := timelineModel(t)
	if len(m.timelineNotes(commonCentrePaneWidth)) < 2 {
		t.Fatal("timeline.log is meant to have notes worth cutting, so this test no longer covers the unmarked cut")
	}
	for h := 1; h <= 3; h++ {
		lines := strings.Split(m.renderTimeline(commonCentrePaneWidth, h), "\n")
		if len(lines) != h {
			t.Fatalf("h=%d: renderTimeline produced %d lines, want %d", h, len(lines), h)
		}
		bar, _ := logfmt.StripANSI(lines[0], nil)
		if !strings.ContainsAny(bar, "░▒▓█") {
			t.Errorf("h=%d: the first line is not a lane bar, so the pane drew chrome in place of the only content this view has: %q", h, bar)
		}
		if h == 1 {
			// One line has room for the lane row and nothing else, the
			// same unmarked cut fitPaneSections has at that height.
			continue
		}
		if got := lines[len(lines)-1]; got != moreBelowMark {
			t.Errorf("h=%d: last line = %q, want %q -- the notes went unsaid with nothing marking it", h, got, moreBelowMark)
		}
	}
}

// TestCaptureGuidanceCutForHeightSaysSo covers the first-run case the
// guidance was written for: a log captured without TF_LOG_PROVIDER=TRACE,
// read in a pane too short for eighteen lines of advice. The block was
// clipped to the pane's height with no marker, leaving "This log contains
// no provider RPC" as a fragment that reads as a finished sentence with the
// actionable half -- the two variables to set -- gone.
//
// This is the height twin of the width defect captureGuidance's own 40-column
// pre-wrap closes.
func TestCaptureGuidanceCutForHeightSaysSo(t *testing.T) {
	m := New(&model.Log{}, "x.log")
	const w = commonCentrePaneWidth

	// Tall enough for every line: the full guidance, unchanged.
	if got, want := m.renderTimeline(w, 20), clipEachWidth(captureGuidance, w); got != want {
		t.Errorf("renderTimeline(h=20) =\n%s\nwant the full guidance:\n%s", got, want)
	}

	// Too short for that, but with room for a whole shorter answer: both
	// variables survive, because they are what the reader has to act on.
	short := m.renderTimeline(w, 4)
	for _, name := range []string{"TF_LOG_PROVIDER=TRACE", "TF_LOG_SDK_PROTO=TRACE"} {
		if !strings.Contains(short, name) {
			t.Errorf("guidance in a %d-line pane lost %s, the actionable half of it:\n%s", 4, name, short)
		}
	}
	for _, line := range strings.Split(short, "\n") {
		if n := lipgloss.Width(line); n > w {
			t.Errorf("guidance line %q is %d columns, want at most %d", line, n, w)
		}
	}

	// Too short even for that: the cut is marked rather than left reading
	// as a finished sentence.
	if lines := strings.Split(m.renderTimeline(w, 2), "\n"); lines[len(lines)-1] != moreBelowMark {
		t.Errorf("guidance in a 2-line pane = %q, want its cut marked with %q", lines, moreBelowMark)
	}

	// A pane of one line has no room for the mark either -- the same
	// unmarked cut fitPaneSections has at that height -- so what it does
	// show has to stand on its own.
	if got := m.renderTimeline(w, 1); !strings.HasSuffix(got, ".") {
		t.Errorf("guidance in a 1-line pane = %q, want a finished sentence: there is no room to mark what follows it", got)
	}

	// The table views answer this log with the same text at the same
	// height, so a reader who has seen one recognises the other.
	for _, h := range []int{2, 4, 20} {
		if got, want := m.renderTimeline(w, h), m.renderList(w, h); got != want {
			t.Errorf("h=%d: renderTimeline =\n%s\nwant renderList's own guidance:\n%s", h, got, want)
		}
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
// everyKindOfWaitModel reports maxStallsShown stalls, which is exactly what
// a four-line pane has room for once the axis has taken its line: the case
// where an unreserved lane row is lost entirely.
func TestRenderTimelineKeepsALaneRowWhenTheAnnotationWouldFillThePane(t *testing.T) {
	m := everyKindOfWaitModel(t)
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
	label := cursorBar(padRight(labels[0], labelW)+" ", labelW+1, true)
	want := label + hueOf(t, m, spans, lanes, 0).Render(laneBar(spans, lanes[0], timelineWallClockMs(spans), barW))
	if lines[0] != want {
		t.Errorf("first line = %q, want lane 0's bar %q: the annotation must not consume the last lane row", lines[0], want)
	}
}

// TestRenderTimelineMarksAnAnnotationCutForHeight checks the other end of
// that trade: the lane row a short pane keeps is taken out of the
// annotation's own room, so stalls the annotation had to say go unsaid --
// and an annotation that simply stopped early would read as the whole of
// what there was to report. This package already refuses that equivalence
// for the detail pane (see moreBelowMark), and the stall list is where it
// matters most: the reason to look at this view is the LONGEST wait, and a
// silent cut is indistinguishable from there being no more.
//
// The same pane and height TestRenderTimelineKeepsALaneRowWhenThe
// AnnotationWouldFillThePane uses, looked at from the annotation's end.
func TestRenderTimelineMarksAnAnnotationCutForHeight(t *testing.T) {
	m := everyKindOfWaitModel(t)
	full := m.timelineNotes(40)

	lines := strings.Split(m.renderTimeline(40, 4), "\n")
	if len(lines) != 4 {
		t.Fatalf("renderTimeline(h=4) produced %d lines, want 4:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	// One lane row, the axis, then whatever annotation room is left.
	shown := lines[2:]
	if len(shown) >= len(full) {
		t.Fatalf("annotation was not cut at h=4 (%d of %d lines shown), so this test asserts nothing", len(shown), len(full))
	}
	if got := shown[len(shown)-1]; got != moreBelowMark {
		t.Errorf("last annotation line = %q, want %q: %d of %d notes went unsaid with nothing marking it", got, moreBelowMark, len(full)-len(shown), len(full))
	}
	// The cut takes the line it marks, so the notes above it survive
	// intact rather than the mark replacing the first of them.
	if shown[0] != full[0] {
		t.Errorf("first annotation line = %q, want the head of the block %q", shown[0], full[0])
	}
}

// TestStallAnnotationNamesTheBlockingSpan checks the annotation's basic
// shape against timeline.log's own solo window.
func TestStallAnnotationNamesTheBlockingSpan(t *testing.T) {
	// timeline.log has one long span running alone after the others
	// finish; that span is what the annotation must name.
	m := timelineModel(t)
	got := m.stallAnnotation(80)
	if !strings.Contains(got, "concurrency 1 of 2") {
		t.Fatalf("stallAnnotation says nothing about capacity going unused: %q", got)
	}
	if !strings.Contains(got, "waiting on") {
		t.Errorf("stallAnnotation does not name what the unused capacity waited on: %q", got)
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
	showOnly(t, &m, dimProvider)
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
	return update(t, positionedTimelineModel(&model.Log{RPCSpans: []span.Span{
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

// TestStallAnnotationReportsOneContinuousWaitAsOneStall is
// timeline-many-stalls.log's real shape: google's single call finishes at
// 3.3s and nothing of google's runs again, while aws hands over from one
// call to the next four times before the log ends at 20.0s. That is ONE
// 16.7-second wait behind one lane, and the annotation must say so.
//
// Merging on the blocking SPAN rather than on contiguity splits it into four
// fragments (3.3s-9.0s, 9.0s-14.0s, 14.0s-18.0s, 18.0s-20.0s), the last of
// which falls under the threshold and is dropped: three lines spent on one
// fact, and the wait understated by the two seconds that fragment covered. A
// reader deciding whether to tune provider parallelism is looking at how long
// the wait was; three numbers that each understate it are worse than no
// annotation.
//
// The 3-second window before aws's first call is the fixture's only other
// wait, and reads as core start-up rather than a mid-plan collapse (see
// TestStallAnnotationNamesEachKindOfWaitDistinctly).
func TestStallAnnotationReportsOneContinuousWaitAsOneStall(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-many-stalls.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	got := m.stallAnnotation(80)
	want := []string{
		"waiting on aws/1, 3.3s–20.0s — concurrency 1 of 2",
		"nothing running, 0s–3.0s — before any call",
	}
	if lines := strings.Split(got, "\n"); !slices.Equal(lines, want) {
		t.Errorf("stallAnnotation = %v, want %v (one continuous wait, longest first)", lines, want)
	}
	// The fragment boundaries a per-span merge produces. Each is a handover
	// INSIDE the blocking lane, not the end of the wait, so any of them
	// surfacing here means the merge has come apart.
	for _, boundary := range []string{"3.3s–9.0s", "9.0s–14.0s", "14.0s–18.0s", "18.0s–20.0s"} {
		if strings.Contains(got, boundary) {
			t.Errorf("stallAnnotation reports %s, a handover inside the blocking lane, as the end of a wait: %q", boundary, got)
		}
	}
	// Nothing was dropped here, so nothing may claim it was: the cut mark
	// and its absence are what tell a complete list from a shortened one.
	if strings.Contains(got, moreBelowMark) {
		t.Errorf("stallAnnotation marks a cut over a fixture with %d stalls: %q", len(want), got)
	}
}

// everyKindOfWaitModel is a model over synthetic spans producing one wait
// of each kind the annotation words differently, and nothing else: three
// seconds before either provider's first call, six seconds of aws and
// google running together, eleven seconds in which nothing runs at all, and
// ten seconds of aws running alone. Every one of them clears the
// annotation's threshold, and there are exactly maxStallsShown of them, so
// nothing is dropped.
//
// No log fixture carries all three, and one that did would be a fourth
// fixture to keep in step with three tests. The spans are what the
// annotation reads; a fixture would only be a longer way to write them.
func everyKindOfWaitModel(t *testing.T) Model {
	t.Helper()
	const aws, google = "registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google"
	return update(t, positionedTimelineModel(&model.Log{RPCSpans: []span.Span{
		{Provider: aws, RPC: "PlanResourceChange", StartMs: 3000, EndMs: 9000, DurationMs: 6000, Fidelity: span.FidelityReported},
		{Provider: google, RPC: "PlanResourceChange", StartMs: 3000, EndMs: 9000, DurationMs: 6000, Fidelity: span.FidelityReported},
		{Provider: aws, RPC: "ApplyResourceChange", StartMs: 20000, EndMs: 30000, DurationMs: 10000, Fidelity: span.FidelityReported},
	}}, "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
}

// TestStallAnnotationNamesEachKindOfWaitDistinctly pins the three things a
// stall line can be.
//
// They are different findings and must not be worded the same. A blocked
// wait names the lane to go and look at. A mid-plan window with nothing
// running anywhere says the time is not in the providers at all, so there
// is no lane to blame and no provider parallelism to tune -- and the
// leading window says that about Terraform core starting up, which appears
// on nearly EVERY capture and would read as a plan that collapsed at its
// start if it were worded as a mid-plan dead window.
func TestStallAnnotationNamesEachKindOfWaitDistinctly(t *testing.T) {
	m := everyKindOfWaitModel(t)
	want := []string{
		"nothing running, 9.0s–20.0s — between calls",
		"waiting on aws/1, 20.0s–30.0s — concurrency 1 of 2",
		"nothing running, 0s–3.0s — before any call",
	}
	if lines := strings.Split(m.stallAnnotation(80), "\n"); !slices.Equal(lines, want) {
		t.Errorf("stallAnnotation = %v, want %v", lines, want)
	}
}

// drainingLanesModel is a model over three providers that finish one at a
// time -- google at 20s, azurerm at 40s, aws at 60s, all three starting
// together -- which is what produces ONE contiguous wait whose depth
// CHANGES inside it: two spans still running from 20s, one from 40s.
// model.Stalls merges contiguous segments of the same kind, so the window
// it reports covers a range of depths rather than a single one.
//
// It is synthetic because no fixture in this repository produces a merged
// window of differing depth at the timeline's own threshold, so the
// annotation's range wording has no other way to be exercised. Three
// providers draining one at a time is the ordinary shape that makes one.
func drainingLanesModel(t *testing.T) Model {
	t.Helper()
	const base = "registry.terraform.io/hashicorp/"
	spans := []span.Span{
		{Provider: base + "aws", RPC: "ApplyResourceChange", StartMs: 0, EndMs: 60000, DurationMs: 60000, Fidelity: span.FidelityReported},
		{Provider: base + "google", RPC: "ReadResource", StartMs: 0, EndMs: 20000, DurationMs: 20000, Fidelity: span.FidelityReported},
		{Provider: base + "azurerm", RPC: "ReadResource", StartMs: 0, EndMs: 40000, DurationMs: 40000, Fidelity: span.FidelityReported},
	}
	return update(t, positionedTimelineModel(&model.Log{RPCSpans: spans}, "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
}

// TestAWaitWhoseDepthChangesReportsBothEnds covers the window a merge
// produces out of segments of differing depth. Reporting its floor alone
// ("concurrency 1 of 3") would say the whole forty seconds ran one-up, when
// half of it ran two-up; reporting it as a direction ("2 to 1") would claim
// a ramp the merge cannot promise, since merging on contiguity admits a
// window whose depth falls and rises again.
func TestAWaitWhoseDepthChangesReportsBothEnds(t *testing.T) {
	m := drainingLanesModel(t)
	want := []string{"waiting on aws/1, 20.0s–60.0s — concurrency 1–2 of 3"}
	if lines := strings.Split(m.stallAnnotation(80), "\n"); !slices.Equal(lines, want) {
		t.Errorf("stallAnnotation = %v, want %v", lines, want)
	}
}

// TestARangeClipsItsOwnTailAtTheCommonPaneWidth measures what the wider
// range wording costs at the pane this tool is actually read in. The line
// is ordered lane-first precisely so clipValueEnd eats the concurrency tail
// rather than the lane name (see stallAnnotation), and a range pushes that
// cut earlier: the reader keeps the lane to go and look at and the window
// on the axis above, and gives up arithmetic the bars themselves carry.
func TestARangeClipsItsOwnTailAtTheCommonPaneWidth(t *testing.T) {
	m := drainingLanesModel(t)
	got := m.stallAnnotation(commonCentrePaneWidth)
	if n := lipgloss.Width(got); n > commonCentrePaneWidth {
		t.Errorf("line %q is %d columns, want at most %d", got, n, commonCentrePaneWidth)
	}
	if want := "waiting on aws/1, 20.0s–60.0s"; !strings.HasPrefix(got, want) {
		t.Errorf("stallAnnotation at %d columns = %q, which no longer opens on %q -- the lane and the window are what the ordering exists to keep", commonCentrePaneWidth, got, want)
	}
	// clipValueEnd's marker, not a height cut: this line was too WIDE, and
	// the two cuts carry different marks (see moreBelowMark).
	if !strings.HasSuffix(got, "…") {
		t.Errorf("stallAnnotation at %d columns = %q, want the cut marked", commonCentrePaneWidth, got)
	}
}

// TestALeadingWindowContainingACompletedCallIsNotTheLeadingGap covers the
// window a zero-extent span sits inside. A span with StartMs == EndMs
// occupies no instant, so model.Stalls never counts it as running (see
// model.PeakConcurrency) and reports the stretch around it as all-idle --
// but a call did complete in there, and the lane row draws a lit column at
// it. Wording that window "before any call" contradicts the bar the reader
// can see inside it.
//
// The UI-hook tier makes this the ordinary case rather than an edge one:
// Terraform rounds hook timings to whole seconds, so every resource that
// refreshed in "0s" produces a zero-extent span.
//
// The spans are the UI tier's because that is the tier that produces them,
// and the window is 3 seconds so it clears stallAnnotation's 1s floor
// instead of being filtered out the way testdata/structured-ui.log's own
// 886ms leading window is.
func TestALeadingWindowContainingACompletedCallIsNotTheLeadingGap(t *testing.T) {
	m := update(t, positionedTimelineModel(&model.Log{UISpans: []span.Span{
		{Provider: "aws", ResourceType: "aws_instance", Address: "aws_instance.web", StartMs: 500, EndMs: 500, DurationMs: 0, Fidelity: span.FidelityUIReported},
		{Provider: "aws", ResourceType: "aws_vpc", Address: "aws_vpc.main", StartMs: 3000, EndMs: 13000, DurationMs: 10000, Fidelity: span.FidelityUIReported},
	}}, "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	got := m.stallAnnotation(80)
	if strings.Contains(got, beforeAnyCallClause) {
		t.Errorf("stallAnnotation = %q, but a call completed at 500ms, inside the window it calls the gap before any call", got)
	}
	want := []string{"nothing running, 0s–3.0s" + betweenCallsClause}
	if lines := strings.Split(got, "\n"); !slices.Equal(lines, want) {
		t.Errorf("stallAnnotation = %v, want %v", lines, want)
	}
}

// stallThresholdMs has two terms and each one governs somewhere, so each
// needs a case where it is the one deciding. Neither had one: every fixture
// in this repository has a window of 20s or less, where 5% never reaches
// the 1s floor, so the percentage was free to be any percentage at all.
//
// The floor is the constant this view's wording rests on -- it is what
// drops testdata/timeline.log's three 500ms handover slivers while keeping
// its 5s solo window -- and the percentage is what stops a long capture
// reporting every sub-second gap in it. 20s is the crossover, where the two
// terms agree, and is included so the floor and the percentage are pinned
// on both sides of it rather than only in their own halves.
func TestStallThresholdTakesTheLargerOfItsTwoTerms(t *testing.T) {
	for _, c := range []struct {
		name      string
		wallClock uint32
		want      uint32
	}{
		{"an empty window still has a floor", 0, 1000},
		{"the floor governs a short window", 9000, 1000},
		{"the terms agree at the crossover", 20000, 1000},
		{"the percentage governs just past it", 21000, 1050},
		{"the percentage governs a long window", 180000, 9000},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := stallThresholdMs(c.wallClock); got != c.want {
				t.Errorf("stallThresholdMs(%d) = %d, want %d", c.wallClock, got, c.want)
			}
		})
	}
}

// The threshold's percentage has to decide something a reader can see, not
// just return a number, so this is the same question asked of the
// annotation: on a window long enough for the percentage to govern, a wait
// shorter than it must be dropped and one longer than it kept.
//
// The window is 200s, where 5% is 10s. aws runs the whole of it while
// google calls twice, leaving a 13s wait and a 183s one -- one either side
// of a 10% threshold and both above a 5% one, so what survives says which
// percentage is in force. It is synthetic for the reason topStallsModel is:
// no fixture has a window anywhere near long enough.
func TestALongWindowsPercentageDecidesWhichWaitsAreNamed(t *testing.T) {
	const aws, google = "registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google"
	spans := []span.Span{{Provider: aws, RPC: "ApplyResourceChange", StartMs: 0, EndMs: 200000, DurationMs: 200000, Fidelity: span.FidelityReported}}
	for _, g := range [][2]uint32{{0, 2000}, {15000, 17000}} {
		spans = append(spans, span.Span{Provider: google, RPC: "ReadResource", StartMs: g[0], EndMs: g[1], DurationMs: g[1] - g[0], Fidelity: span.FidelityReported})
	}
	m := update(t, positionedTimelineModel(&model.Log{RPCSpans: spans}, "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	if _, got := m.timelineSpans(); timelineWallClockMs(got) != 200000 {
		t.Fatalf("the window is %dms, want 200000", timelineWallClockMs(got))
	}
	if got := stallThresholdMs(200000); got != 10000 {
		t.Fatalf("the threshold on a 200s window is %dms; this case is built for 5%% of it", got)
	}

	want := []string{
		"waiting on aws/1, 17.0s–200.0s — concurrency 1 of 2",
		"waiting on aws/1, 2.0s–15.0s — concurrency 1 of 2",
	}
	if got := strings.Split(m.stallAnnotation(80), "\n"); !slices.Equal(got, want) {
		t.Errorf("stallAnnotation = %v, want %v -- the 13s wait clears 5%% of the window and not 10%%", got, want)
	}
}

// topStallsModel is a model over synthetic spans producing FIVE separate
// waits of distinct duration (10s, 8s, 6s, 4s, 2s), all clearing
// stallAnnotation's threshold: one aws call running for the whole window,
// and five google calls punctuating it, so each gap between google's calls
// is its own stall with aws to blame and no two gaps can merge.
//
// It is synthetic rather than a log fixture because no fixture produces
// more than maxStallsShown genuinely separate stalls -- the one that used
// to appear to (timeline-many-stalls.log) was reporting four fragments of a
// single wait, which is the defect
// TestStallAnnotationReportsOneContinuousWaitAsOneStall now pins the fix for.
func topStallsModel(t *testing.T) Model {
	t.Helper()
	const aws, google = "registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google"
	spans := []span.Span{{Provider: aws, RPC: "ApplyResourceChange", StartMs: 0, EndMs: 40000, DurationMs: 40000, Fidelity: span.FidelityReported}}
	for _, g := range [][2]uint32{{0, 2000}, {12000, 14000}, {22000, 24000}, {30000, 32000}, {36000, 38000}} {
		spans = append(spans, span.Span{Provider: google, RPC: "ReadResource", StartMs: g[0], EndMs: g[1], DurationMs: g[1] - g[0], Fidelity: span.FidelityReported})
	}
	return update(t, positionedTimelineModel(&model.Log{RPCSpans: spans}, "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
}

// TestStallAnnotationShowsAtMostTheTopFewByDuration checks the two things
// maxStallsShown exists for: the stalls kept are the LONGEST by duration,
// not the first chronologically, and no more than maxStallsShown are shown
// even when every stall in the log clears the threshold.
func TestStallAnnotationShowsAtMostTheTopFewByDuration(t *testing.T) {
	m := topStallsModel(t)
	got := m.stallAnnotation(80)
	lines := strings.Split(got, "\n")

	want := []string{
		"waiting on aws/1, 2.0s–12.0s — concurrency 1 of 2",
		"waiting on aws/1, 14.0s–22.0s — concurrency 1 of 2",
		"waiting on aws/1, 24.0s–30.0s — concurrency 1 of 2",
		moreBelowMark,
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("stallAnnotation = %v, want %v (the three longest, longest first, and a mark for the rest)", lines, want)
	}
	for _, dropped := range []string{"32.0s–36.0s", "38.0s–40.0s"} {
		if strings.Contains(got, dropped) {
			t.Errorf("stallAnnotation names the %s stall, which maxStallsShown should have dropped: %q", dropped, got)
		}
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

	// h=5 gives renderTimeline one line for the axis and two for the notes
	// -- the busy summary, then the fixture's only wait, the second of
	// capture time before its five fully-overlapping spans start -- leaving
	// two lane rows, fewer than the fixture's five lanes. With
	// the cursor on lane 4 (0-based) and a two-row window, scrollWindow's
	// own pin-to-edge rule puts the window at lanes [3, 4]: lanes 0-2 must
	// have scrolled off, and the cursor's own lane 4 must be the LAST lane
	// row drawn, ahead of the axis and the notes.
	got := m.renderTimeline(40, 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("renderTimeline(h=5) produced %d lines, want 5 (2 lane rows, the axis, and two note lines)", len(lines))
	}

	_, spans := m.timelineSpans()
	wallClock := timelineWallClockMs(spans)
	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	barW := 40 - labelW - 1
	rowContent := func(i int) string {
		return padRight(labels[i], labelW) + " " + hueOf(t, m, spans, lanes, i).Render(laneBar(spans, lanes[i], wallClock, barW))
	}

	wantVisible := []int{3, 4}
	var scratch []byte
	for row, laneIdx := range wantVisible {
		want := rowContent(laneIdx)
		if laneIdx == m.timeline.lane {
			// Only the label column carries the cursor; see
			// TestTheCursorDoesNotRedrawTheSelectedLanesBar.
			want = cursorBar(padRight(labels[laneIdx], labelW)+" ", labelW+1, true) + hueOf(t, m, spans, lanes, laneIdx).Render(laneBar(spans, lanes[laneIdx], wallClock, barW))
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
	if want := m.timelineNotes(40); !slices.Equal(lines[3:], want) {
		t.Errorf("the lines beneath the axis are %q, want the notes %q", lines[3:], want)
	}
}

// laneBarOf strips the styling and the label column off one rendered lane
// row, leaving the bar: the block-and-space pattern that is what this view
// actually says. Labels are ASCII and are padded to a fixed column count, so
// dropping labelW+1 runes drops exactly labelW+1 display columns.
func laneBarOf(t *testing.T, row string, labelW int) string {
	t.Helper()
	plain, _ := logfmt.StripANSI(row, nil)
	r := []rune(plain)
	if len(r) < labelW+1 {
		t.Fatalf("lane row %q is shorter than its own label column", plain)
	}
	return string(r[labelW+1:])
}

// hueOf is the style lane idx's bar is drawn in, looked up the way
// renderTimeline looks it up. The expected-row builders that call it
// compose from the production functions rather than from literals, and the
// hue is one of those: leaving it out would make them assert the bar is
// UNCOLOURED.
//
// A lane whose provider has no position is fatal rather than silently
// unstyled. The zero lipgloss.Style renders its argument unchanged, so a
// builder that fell through would compose an uncoloured expectation and
// match an uncoloured frame -- passing while asserting nothing about the
// colour it was added to carry.
func hueOf(t *testing.T, m Model, spans []span.Span, lanes []model.Lane, idx int) lipgloss.Style {
	t.Helper()
	p := laneProvider(spans, lanes[idx])
	at, ok := m.laneOrder[p]
	if !ok {
		t.Fatalf("lane %d's provider %q has no place in the lane palette (%v)", idx, p, m.laneOrder)
	}
	return semantic.lane(at)
}

// barHueOf is the escape sequence a rendered lane row opens its BAR with, or
// "" if the bar carries none. The label column is skipped: on the selected
// lane it is the cursor bar, whose own reverse video would otherwise be
// mistaken for the hue.
func barHueOf(t *testing.T, row string) string {
	t.Helper()
	area := row
	for _, open := range reverseOpen {
		if !strings.HasPrefix(row, open) {
			continue
		}
		_, rest, ok := strings.Cut(row, "\x1b[0m")
		if !ok {
			t.Fatalf("row %q opens a cursor bar and never closes it", row)
		}
		area = rest
		break
	}
	at := strings.Index(area, "\x1b[")
	if at < 0 {
		return ""
	}
	end := strings.IndexByte(area[at:], 'm')
	if end < 0 {
		t.Fatalf("row %q carries an unterminated escape sequence", row)
	}
	return area[at : at+end+1]
}

// TestTheCursorDoesNotRedrawTheSelectedLanesBar is the property the
// full-row reverse-video cursor violated. Reverse video swaps foreground
// and background, and a bar is drawn as filled cells against empty ones --
// so wrapping the whole row in it painted the busy columns as empty and the
// idle columns as solid. The one row that inverted the view's central claim
// was the row the cursor was on, and every ↑/↓ inverted a different one.
//
// The bar a lane renders must therefore be byte-identical whether or not
// the cursor is on it. The two assertions below it pin the mechanism rather
// than the symptom: no styling may reach the bar area at all, and the
// selected row must still be styled somewhere, so this cannot be satisfied
// by dropping the cursor.
func TestTheCursorDoesNotRedrawTheSelectedLanesBar(t *testing.T) {
	m := timelineModel(t)
	const w, h = 60, 10
	lanes := m.timelineLanes()
	if len(lanes) < 2 {
		t.Fatalf("timeline.log packs into %d lanes, want at least 2 so one lane can be drawn both selected and not", len(lanes))
	}
	_, spans := m.timelineSpans()
	labelW := laneLabelWidth(laneLabels(spans, lanes))

	onFirst := strings.Split(m.renderTimeline(w, h), "\n")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.timeline.lane != 1 {
		t.Fatalf("lane = %d after one ↓, want 1", m.timeline.lane)
	}
	onSecond := strings.Split(m.renderTimeline(w, h), "\n")

	for i := range lanes {
		got, want := laneBarOf(t, onSecond[i], labelW), laneBarOf(t, onFirst[i], labelW)
		if got != want {
			t.Errorf("lane %d's bar is %q with the cursor on lane 1 and %q with it on lane 0", i, got, want)
		}
	}

	// Asserted against REVERSE VIDEO rather than against styling in general:
	// the bar carries its provider's hue, which is a style of its own and
	// not the cursor's. What must not reach the bar is the highlight.
	labelRun, barArea, closed := strings.Cut(onFirst[0], "\x1b[0m")
	if !closed {
		t.Fatalf("the selected lane's row %q never closes the cursor bar", onFirst[0])
	}
	if !strings.HasPrefix(labelRun, "\x1b[7m") {
		t.Errorf("the selected lane's row %q does not open with the cursor bar", onFirst[0])
	}
	for _, open := range reverseOpen {
		if strings.Contains(barArea, open) {
			t.Errorf("the cursor's reverse video reaches into the bar area of %q", onFirst[0])
		}
	}
	if got, want := unstyled(barArea), laneBarOf(t, onFirst[0], labelW); got != want {
		t.Errorf("the selected lane's bar area reads %q, want its bar %q", got, want)
	}
	for _, open := range reverseOpen {
		if strings.Contains(onFirst[1], open) {
			t.Errorf("an unselected lane's row %q carries cursor styling", onFirst[1])
		}
	}
}

// TestEveryLaneShadeIsOneDisplayColumn measures each glyph laneBar can draw
// the way every width in this package is measured. A shade that resolved to
// two columns would silently widen every bar carrying it and push the pane
// separator beside it out of line on that row alone.
func TestEveryLaneShadeIsOneDisplayColumn(t *testing.T) {
	for _, shade := range laneShades {
		if w := lipgloss.Width(string(shade)); w != 1 {
			t.Errorf("lane shade %q is %d display columns, want 1", shade, w)
		}
	}
}

// TestLaneBarShadesAColumnByHowMuchOfItIsBusy pins the shading thresholds AT
// their boundaries, one millisecond either side of each. 20 columns over a
// 1000ms window is 50ms per column, so a span's length in column 0 IS its
// occupancy as a percentage of 50, and each boundary is one millisecond
// wide: a sample sitting comfortably inside a band moves with the band and
// reports nothing when it does.
//
// The top boundary is the one that matters most. The solid glyph is reserved
// for a column a span occupies ENTIRELY (see laneShadeFor), which is what
// lets a reader trust a run of █ to be continuous work; a threshold admitting
// 49 of a column's 50ms would draw a column with a millisecond of waiting in
// it as unbroken work -- waiting drawn as work, the same class of defect as a
// dense lane drawn solid (TestADenseLaneIsNotDrawnSolid).
func TestLaneBarShadesAColumnByHowMuchOfItIsBusy(t *testing.T) {
	cases := []struct {
		name       string
		start, end uint32
		want       rune
	}{
		{"the whole column", 0, 50, '█'},
		{"all but a millisecond of it", 0, 49, '▓'},
		{"two thirds of it", 0, 34, '▓'},
		{"a millisecond short of two thirds", 0, 33, '▒'},
		{"a third of it", 0, 17, '▒'},
		{"a millisecond short of a third", 0, 16, '░'},
		{"a single millisecond", 0, 1, '░'},
	}
	for _, c := range cases {
		spans := []span.Span{{StartMs: c.start, EndMs: c.end, DurationMs: c.end - c.start}}
		got := laneBar(spans, model.Lane{Spans: []int{0}}, 1000, 20)
		if []rune(got)[0] != c.want {
			t.Errorf("a span covering %s renders column 0 as %q, want %q", c.name, string([]rune(got)[0]), string(c.want))
		}
	}
}

// TestADenseLaneIsNotDrawnSolid is the fixture form of the same defect the
// cursor had, in the other direction: every span lights at least one whole
// column, so a lane holding more calls than the bar has columns lit all of
// them and rendered as fully busy while its true occupancy was about 1.3%.
// Idle time drawn as work is the exact inversion of what this view is for.
func TestADenseLaneIsNotDrawnSolid(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-dense-lane.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	lanes := m.timelineLanes()
	if len(lanes) != 1 {
		t.Fatalf("timeline-dense-lane.log packs into %d lanes, want 1 (see the fixture's own doc comment)", len(lanes))
	}
	_, spans := m.timelineSpans()
	const barW = 46
	if len(lanes[0].Spans) <= barW {
		t.Fatalf("the lane holds %d spans, want more than the %d columns drawn for it", len(lanes[0].Spans), barW)
	}

	got := laneBar(spans, lanes[0], timelineWallClockMs(spans), barW)
	if strings.ContainsRune(got, '█') {
		t.Errorf("a lane 1.3%% occupied draws a fully-busy column: %q", got)
	}
	if strings.ContainsRune(got, ' ') {
		t.Fatalf("this fixture is meant to touch every column, so a blank one means it no longer exercises the defect: %q", got)
	}
	if !strings.ContainsRune(got, '░') {
		t.Errorf("a barely-occupied lane is not drawn at its lightest shade: %q", got)
	}
}

// TestAColumnStandingForNoTimeIsShadedByWhatCoversIt covers the columns
// laneColBounds tiles to an EMPTY millisecond interval: a window shorter in
// milliseconds than the bar is wide in columns divides into columns most of
// which stand for no time at all.
//
// Such a column has no occupancy to divide, so the busy fraction that shades
// every other column is 0/0 there. Answering it with the lightest glyph drew
// a lane busy for every millisecond of the window as a mostly idle bar --
// TestADenseLaneIsNotDrawnSolid's defect inverted, and the reading a
// light-dominated bar is meant to rule out. A span with real extent running
// through such a column covers the whole of it, there being nothing there to
// be idle, so the column is solid.
//
// A ZERO-EXTENT span is the case that keeps the rule honest. It occupies no
// instant, so it covers nothing however narrow the column: it keeps the
// minimum-one-column mark that says a call happened here, and is not
// promoted to a claim that work ran through the column.
//
// The first case is reachable in the interface through a filter leaving only
// spans inside a very short offset window; laneBar is measured directly here
// because the bar is what the defect is about.
func TestAColumnStandingForNoTimeIsShadedByWhatCoversIt(t *testing.T) {
	// 65 columns over a 20ms window: every column but a handful is empty,
	// and one span covers the whole window.
	busy := []span.Span{{StartMs: 0, EndMs: 20, DurationMs: 20}}
	if got, want := laneBar(busy, model.Lane{Spans: []int{0}}, 20, 65), strings.Repeat("█", 65); got != want {
		t.Errorf("a lane busy for the whole window renders as %q, want %q", got, want)
	}

	// 40 columns over a 20ms window puts column 20 at [10,10). A call
	// Terraform reported as instantaneous is a mark, not continuous work.
	instant := []span.Span{{StartMs: 10, EndMs: 10}}
	got := laneBar(instant, model.Lane{Spans: []int{0}}, 20, 40)
	if strings.ContainsRune(got, '█') {
		t.Errorf("a zero-extent span renders as %q, which claims a column of continuous work", got)
	}
	if strings.Count(got, string(laneShades[0])) != 1 {
		t.Errorf("a zero-extent span renders as %q, want exactly one %q mark", got, string(laneShades[0]))
	}
}

// TestTheTimelineDeclaresAClampedStart covers the one view that draws
// extents POSITIONALLY saying nothing about a span whose start it does not
// know. --profile already prints a note whenever a span is clamped; the
// timeline drew such a span anchored at column 0 with a length shorter than
// its own duration and left the reader to notice.
func TestTheTimelineDeclaresAClampedStart(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-clamped-start.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	_, spans := m.timelineSpans()
	if !slices.ContainsFunc(spans, func(s span.Span) bool { return s.StartClamped }) {
		t.Fatalf("timeline-clamped-start.log carries no clamped span, so it no longer exercises this")
	}
	got := m.renderTimeline(60, 20)
	if !strings.Contains(got, "clamped") {
		t.Errorf("the timeline says nothing about its clamped span:\n%s", got)
	}

	// A pane too short for the whole block cuts it from the tail, and what
	// must survive that cut is the caveat, not the stall line: a stall
	// dropped is a finding not shown, and moreBelowMark says so, while the
	// caveat dropped leaves the bars above reading as facts they are not.
	short := strings.Split(m.renderTimeline(60, 5), "\n")
	if len(short) != 5 {
		t.Fatalf("renderTimeline(h=5) produced %d lines, want 5", len(short))
	}
	if !strings.Contains(short[2], "clamped") {
		t.Errorf("a cut pane dropped the clamped-start note ahead of the stall line:\n%s", strings.Join(short, "\n"))
	}
	if got := short[len(short)-1]; got != moreBelowMark {
		t.Errorf("last line = %q, want %q: the block was cut with nothing marking it", got, moreBelowMark)
	}

	// A log with nothing clamped must not carry the note: a caveat shown
	// unconditionally is one a reader learns to ignore.
	clean := timelineModel(t)
	if out := clean.renderTimeline(60, 20); strings.Contains(out, "clamped") {
		t.Errorf("timeline.log has no clamped span but the view claims one:\n%s", out)
	}
}

// TestTheStallAnnotationDoesNotClaimToCountRows is why the wording changed.
// The count a stall carries is peak CONCURRENCY, not the rows drawn, and
// per-provider packing makes the two diverge by construction: a provider
// that has finished still occupies a row but is no longer capacity. Calling
// it "N lanes idle" beside a five-row timeline is a statement about the
// screen that the number is not entitled to make.
func TestTheStallAnnotationDoesNotClaimToCountRows(t *testing.T) {
	for _, m := range []Model{timelineModel(t), everyKindOfWaitModel(t), topStallsModel(t)} {
		got := m.stallAnnotation(80)
		if strings.Contains(got, "lane idle") || strings.Contains(got, "lanes idle") {
			t.Errorf("stallAnnotation counts lanes, which is not what the number measures: %q", got)
		}
	}
}

// TestTheAnnotationSpellsZeroTheWayTheAxisDoes closes a two-spellings-of-one-
// number gap: the leading-gap line read "0ms–1.0s" directly beneath an axis
// whose own left end read "0s". A reader matching the annotation's window to
// the axis above it is matching two spellings of the same instant.
func TestTheAnnotationSpellsZeroTheWayTheAxisDoes(t *testing.T) {
	m := everyKindOfWaitModel(t)
	got := m.stallAnnotation(80)
	if strings.Contains(got, "0ms") {
		t.Errorf("stallAnnotation spells zero as 0ms, where the axis above it spells it 0s: %q", got)
	}
	if axis := timeAxis(9000, 40); !strings.HasPrefix(axis, "0s") {
		t.Errorf("timeAxis no longer opens on 0s: %q", axis)
	}
}

// TestTheTimelineReportsHowMuchOfTheWindowWasBusy covers the hole the stall
// list structurally cannot: timeline-dense-lane.log is 2.4s of work spread
// over a 180s window, and the annotation is correctly silent about it.
// Concurrency does fall to zero between every pair of calls -- model.Stalls
// opens a window in each of the 59 gaps -- but each gap is 2.96s against a
// 9s threshold, so every one of them is dropped. "no stalls" on a run that
// was 98% idle answers the one question this view exists for wrongly.
func TestTheTimelineReportsHowMuchOfTheWindowWasBusy(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-dense-lane.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	notes := m.timelineNotes(80)
	if len(notes) == 0 {
		t.Fatal("the timeline draws no notes at all")
	}
	if !slices.Contains(notes, stallAnnotationNoStalls) {
		t.Fatalf("this fixture is meant to have no stalls, so it no longer exercises the defect: %q", notes)
	}
	const want = "busy 2.4s of 180.0s (1%)"
	if notes[0] != want {
		t.Errorf("first note = %q, want %q -- 60 calls of 40ms over a 180s window", notes[0], want)
	}
}

// The summary sits ABOVE the stall list, so a pane too short for both keeps
// the headline figure and gives up the ranked detail beneath it.
func TestTheBusySummarySitsAboveTheStallList(t *testing.T) {
	m := timelineModel(t)
	notes := m.timelineNotes(80)
	if len(notes) < 2 {
		t.Fatalf("timeline.log is meant to carry both a summary and stalls, got %q", notes)
	}
	if !strings.HasPrefix(notes[0], "busy ") {
		t.Errorf("notes[0] = %q, want the busy summary first", notes[0])
	}
	if !strings.Contains(notes[1], "concurrency") {
		t.Errorf("notes[1] = %q, want the stall list beneath the summary", notes[1])
	}
}

// TestAFilteredTimelineSaysSoWhereTheFiguresAre covers the whole notes
// block being computed over the FILTERED spans while wording itself as a
// statement about the plan.
//
// timeline.log's google call runs 1.5s-3.5s. Ticking aws deletes the log's
// one real finding and manufactures a window reporting "nothing running --
// between calls" over a stretch in which google was demonstrably working --
// the clause this annotation reserves for "the time is not in the
// providers, so tuning provider parallelism will not touch it". A reader
// acting on that goes to look at Terraform core. The busy percentage moves
// the same way, from 83% to 77%, and is the figure most likely to be
// quoted.
//
// The header's "1 of 3 RPC spans" is three panes away and is a sentence
// about span counts, not about these figures, so the note has to sit where
// the figures are.
func TestAFilteredTimelineSaysSoWhereTheFiguresAre(t *testing.T) {
	m := timelineModel(t)
	if got := m.timelineNotes(commonCentrePaneWidth); slices.ContainsFunc(got, mentionsTheFilter) {
		t.Fatalf("the unfiltered notes already mention a filter (%q), so this test asserts nothing", got)
	}

	showOnly(t, &m, dimProvider, "registry.terraform.io/hashicorp/aws")
	notes := m.timelineNotes(commonCentrePaneWidth)

	busy := slices.IndexFunc(notes, func(l string) bool { return strings.HasPrefix(l, "busy ") })
	if busy < 0 {
		t.Fatalf("notes = %q, with no busy summary in them", notes)
	}
	if !slices.ContainsFunc(notes[:busy], mentionsTheFilter) {
		t.Errorf("notes = %q: every figure from the busy summary down covers the filtered spans only, and nothing above it says so", notes)
	}
	if !strings.Contains(strings.Join(notes[:busy], " "), "Esc") {
		t.Errorf("notes = %q: the filter note does not name the key that clears the filter, as every other filtered-state note in this package does", notes)
	}
	for _, line := range notes {
		if n := lipgloss.Width(line); n > commonCentrePaneWidth {
			t.Errorf("line %q is %d columns, want at most %d", line, n, commonCentrePaneWidth)
		}
	}
}

// mentionsTheFilter reports whether a note line tells the reader a filter
// is narrowing what the figures beneath it cover. It matches on the word
// rather than on the note's exact text, so the wording can be changed
// without the test having to be rewritten to keep asserting the same thing.
func mentionsTheFilter(line string) bool {
	return strings.Contains(line, "filter")
}

// A filter that hides no SPAN leaves the figures whole, so there is nothing
// to qualify: the note is about what the figures COVER, not about which
// checkboxes are ticked. Unticking a level is that case -- a level belongs
// to an entry and no span carries one, so the level dimension narrows the
// raw log and nothing the timeline draws (see levelFacet) -- and a note
// here would tell a reader their numbers are narrowed when they are not.
func TestATimelineFilterThatHidesNothingIsNotAnnounced(t *testing.T) {
	m := timelineModel(t)
	untick(t, &m, dimLevel, levelValue(t, m))
	if !m.filterActive() {
		t.Fatal("no filter is active, so this asserts nothing about one that hides nothing")
	}
	_, spans := m.timelineSpans()
	if len(spans) != len(m.log.RPCSpans) {
		t.Fatalf("the filter hides %d of %d spans, so this test no longer covers a filter that hides nothing", len(m.log.RPCSpans)-len(spans), len(m.log.RPCSpans))
	}
	if got := m.timelineNotes(commonCentrePaneWidth); slices.ContainsFunc(got, mentionsTheFilter) {
		t.Errorf("notes = %q, which qualifies figures that are the whole log's", got)
	}
}

// levelValue is the first value the level dimension offers, so a test can
// untick a level without hard-coding which levels a fixture happens to
// carry.
func levelValue(t *testing.T, m Model) string {
	t.Helper()
	for _, f := range m.facets {
		if f.Name == dimLevel && len(f.Values) > 0 {
			return f.Values[0].Value
		}
	}
	t.Fatal("the level dimension offers no value to untick")
	return ""
}

// The union, not the sum: two spans overlapping cover less wall clock than
// their durations add to, and a summary claiming more busy time than the
// window holds would be arithmetic nobody could check against the bars.
func TestTheBusySummaryNeverExceedsTheWindow(t *testing.T) {
	for _, name := range []string{"timeline.log", "timeline-dense-lane.log", "timeline-many-lanes.log", "timeline-many-stalls.log", "two-tier.log"} {
		m := update(t, New(testLog(t, name), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
		_, spans := m.timelineSpans()
		busy, err := model.BusyMs(spans)
		if err != nil {
			t.Fatalf("%s: BusyMs: %v", name, err)
		}
		if window := timelineWallClockMs(spans); busy > window {
			t.Errorf("%s: busy %dms exceeds the %dms window the axis draws", name, busy, window)
		}
	}
}

// TestTheClampedStartNoteUsesThePaneItIsGiven covers a caveat that wraps to
// the pane rather than to a fixed width. Pre-wrapped to captureGuidance's 40
// columns it would take four lines of a twelve-line pane however wide the
// pane actually was; wrapped to the pane it takes three at 44 columns and two
// at the 74 a 160-column terminal gives it. Chrome never displaces content
// here, so the note wraps to the width it has.
func TestTheClampedStartNoteUsesThePaneItIsGiven(t *testing.T) {
	for _, w := range []int{44, 60, 74, 160} {
		lines := wrapToWidth(clampedStartNote, w)
		for i, l := range lines {
			if n := lipgloss.Width(l); n > w {
				t.Errorf("width %d: line %d is %d columns: %q", w, i, n, l)
			}
			if i == len(lines)-1 {
				continue
			}
			// Greedy wrap: no line but the last may have had room for the
			// first word of the one below it, or the note is still wrapped
			// narrower than the pane it was handed.
			next, _, _ := strings.Cut(lines[i+1], " ")
			if lipgloss.Width(l)+1+lipgloss.Width(next) <= w {
				t.Errorf("width %d: line %d (%q) had room for %q from the line below", w, i, l, next)
			}
		}
	}
	if got := len(wrapToWidth(clampedStartNote, 44)); got > 3 {
		t.Errorf("the clamped-start note is %d lines in a 44-column pane, want at most 3 of the twelve a pane has", got)
	}
	if got := len(wrapToWidth(clampedStartNote, 160)); got != 1 {
		t.Errorf("the clamped-start note is %d lines in a 160-column pane, want 1", got)
	}
}

// A pane with lanes to draw always draws at least one of them, however much
// the notes beneath would like the room.
func TestTheNotesNeverTakeTheLastLaneRow(t *testing.T) {
	m := update(t, New(testLog(t, "timeline-clamped-start.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	for h := 2; h <= 14; h++ {
		lines := strings.Split(m.renderTimeline(44, h), "\n")
		if len(lines) > h {
			t.Fatalf("h=%d: renderTimeline produced %d lines", h, len(lines))
		}
		bar, _ := logfmt.StripANSI(lines[0], nil)
		if !strings.ContainsAny(bar, "░▒▓█") {
			t.Errorf("h=%d: the first line is not a lane bar, so the notes took the last row: %q", h, bar)
		}
	}
}

// commonCentrePaneWidth is what the centre pane measures at the 100-column
// terminal this tool is actually run at: two side panes capped at a quarter
// of the width each, and two pane separators. It is the NARROWEST centre
// pane any supported terminal width produces -- 70 columns gives 48 and 160
// gives 74 -- so it is the width a line has to survive to survive at all.
const commonCentrePaneWidth = 44

// TestTheStallLineKeepsTheLaneNameAtTheCommonPaneWidth is why the wording
// changed. The line ran to 59 columns with the lane name at its END, and
// the end is what clipValueEnd cuts: at 44 columns it rendered as "…
// waiting on aws…" and lost the one thing it names a lane FOR. Naming a
// lane a reader can go and find is the annotation's whole justification for
// naming lanes rather than spans.
func TestTheStallLineKeepsTheLaneNameAtTheCommonPaneWidth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		m     Model
		label string
	}{
		{"a short provider name", timelineModel(t), "aws/1"},
		{"a name longer than the label column", longLabelStallModel(t), "…workspace/1"},
	} {
		got := tc.m.stallAnnotation(commonCentrePaneWidth)
		if !strings.Contains(got, "waiting on "+tc.label) {
			t.Errorf("%s: stallAnnotation at %d columns = %q, which no longer names the lane %q", tc.name, commonCentrePaneWidth, got, tc.label)
		}
		for _, line := range strings.Split(got, "\n") {
			if n := lipgloss.Width(line); n > commonCentrePaneWidth {
				t.Errorf("%s: line %q is %d columns, want at most %d", tc.name, line, n, commonCentrePaneWidth)
			}
		}
	}
}

// The two lines with no lane to name have no variable-width payload in
// them, so unlike a blocked wait they can be held to the common pane width
// whole: what a cut would take is the clause telling the two apart.
func TestTheAllIdleStallLinesFitTheCommonPaneWidth(t *testing.T) {
	m := everyKindOfWaitModel(t)
	lines := strings.Split(m.stallAnnotation(commonCentrePaneWidth), "\n")
	var idle []string
	for _, l := range lines {
		if strings.HasPrefix(l, nothingRunningClause) {
			idle = append(idle, l)
		}
	}
	if len(idle) != 2 {
		t.Fatalf("got %d all-idle lines in %q, want 2 (a mid-plan window and the leading gap)", len(idle), lines)
	}
	for _, l := range idle {
		if strings.HasSuffix(l, "…") {
			t.Errorf("line %q is cut at %d columns, losing the clause that tells it from the other kind of dead window", l, commonCentrePaneWidth)
		}
	}
	if idle[0] == idle[1] {
		t.Errorf("both all-idle lines read %q, so the mid-plan window and the leading gap are indistinguishable", idle[0])
	}
}

// unevenLanesModel is a model over two providers whose lanes hold different
// numbers of calls at different times: aws twice, google four times. It is
// the shape that tells a cursor carried by ORDINAL from one carried by
// time -- aws's second call is google's fourth in time, but google's second
// by index.
func unevenLanesModel(t *testing.T) Model {
	t.Helper()
	const aws, google = "registry.terraform.io/hashicorp/aws", "registry.terraform.io/hashicorp/google"
	spans := []span.Span{
		{Provider: aws, RPC: "ReadResource", StartMs: 0, EndMs: 1000, DurationMs: 1000, Fidelity: span.FidelityReported},
		{Provider: aws, RPC: "ReadResource", StartMs: 30000, EndMs: 31000, DurationMs: 1000, Fidelity: span.FidelityReported},
	}
	for _, at := range []uint32{0, 10000, 20000, 30000} {
		spans = append(spans, span.Span{Provider: google, RPC: "ReadResource", StartMs: at, EndMs: at + 1000, DurationMs: 1000, Fidelity: span.FidelityReported})
	}
	m := update(t, positionedTimelineModel(&model.Log{RPCSpans: spans}, "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
}

// TestChangingLanesKeepsTheCursorWhereTheReaderWasLooking covers a cursor
// that carried its ORDINAL across a lane change: on lane 0's third call,
// ↓ landed on lane 1's third call, an unrelated call at an unrelated time.
// The cursor drives the detail pane and Enter's jump target, so what it
// lands on has to be what the reader was looking at.
func TestChangingLanesKeepsTheCursorWhereTheReaderWasLooking(t *testing.T) {
	m := unevenLanesModel(t)
	lanes := m.timelineLanes()
	if len(lanes) != 2 || len(lanes[0].Spans) != 2 || len(lanes[1].Spans) != 4 {
		t.Fatalf("lanes hold %d and %d spans, want 2 and 4", len(lanes[0].Spans), len(lanes[1].Spans))
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	from, ok := m.selectedTimelineSpanValue()
	if !ok || from.StartMs != 30000 {
		t.Fatalf("selected span starts at %dms, want the 30s call in aws's lane", from.StartMs)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	got, ok := m.selectedTimelineSpanValue()
	if !ok {
		t.Fatal("nothing selected after moving to a lane with spans in it")
	}
	if got.StartMs != 30000 {
		t.Errorf("after ↓ the cursor is on the call starting at %dms, want the 30s one nearest where it was", got.StartMs)
	}
	if m.timeline.span != 3 {
		t.Errorf("span index = %d, want 3 -- the ordinal was carried across instead of the time", m.timeline.span)
	}

	// And back the other way, where the ordinal would now run off the end
	// of the shorter lane and be clamped to it rather than chosen.
	m = update(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if back, _ := m.selectedTimelineSpanValue(); back.StartMs != 30000 {
		t.Errorf("after ↑ the cursor is on the call starting at %dms, want the 30s one", back.StartMs)
	}
}

// A lane change that lands on the same lane -- the cursor already at the
// end -- must leave the within-lane cursor alone, or ↓ at the bottom would
// quietly re-seek it.
func TestALaneChangeThatGoesNowhereLeavesTheSpanCursorAlone(t *testing.T) {
	m := unevenLanesModel(t)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	for i := 0; i < 3; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.timeline.span != 3 {
		t.Fatalf("span = %d after stepping to the end of lane 1, want 3", m.timeline.span)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.timeline.span != 3 {
		t.Errorf("span = %d after ↓ on the last lane, want it left at 3", m.timeline.span)
	}
}

// Two full provider addresses can shorten to one lane label -- the same
// provider type served from two registries -- and the lanes they name are
// then already indistinguishable to a reader. What must not happen is the
// second address taking the position and leaving the first sharing a colour
// with somebody else: positions are handed out by counting the entries
// already made, so an overwrite gives the next provider one already in use.
//
// Built from a log rather than a fixture, because two registries serving one
// provider type is a shape no fixture here has and none is needed to state
// the rule.
func TestLaneOrderGivesOnePlaceToEachShortProviderName(t *testing.T) {
	l := &model.Log{RPCSpans: []span.Span{
		{Provider: "registry.terraform.io/hashicorp/aws"},
		{Provider: "registry.example.com/acme/aws"},
		{Provider: "registry.terraform.io/hashicorp/google"},
	}}

	order := laneOrderFor(l)
	if len(order) != 2 {
		t.Fatalf("laneOrderFor placed %d providers over two short names (%v)", len(order), order)
	}
	if order["aws"] == order["google"] {
		t.Errorf("aws and google share place %d, so their lanes are drawn alike", order["aws"])
	}
	if order["aws"] != 0 {
		t.Errorf("aws is at place %d, want 0 -- the places are handed out in sorted order", order["aws"])
	}
}

// The UI tier is drawn whenever the log has no RPC spans, which is what a
// capture taken without TF_LOG_PROVIDER=TRACE gives -- the most likely
// first-run result. Its lanes are per-provider exactly as the RPC tier's
// are, so they take hues on the same terms.
//
// This is what the provider FACET could not do: the facets are built from
// RPCSpans, so on this tier the palette was empty and every lane came out
// uncoloured.
func TestTheUITierDrawsItsLanesInProviderHues(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 120, Height: 30})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if tier, _ := m.timelineSpans(); tier != tierUI {
		t.Fatalf("the fixture draws tier %v, not the UI tier this is about", tier)
	}

	lanes := m.timelineLanes()
	if len(lanes) < 2 {
		t.Fatalf("the fixture packs into %d lanes, want two providers' worth", len(lanes))
	}
	rows := timelineLaneRows(t, m, lanes)
	var hues []string
	for i := range lanes {
		hue := barHueOf(t, rows[i])
		if hue == "" {
			t.Errorf("lane %d's bar carries no colour: %q", i, rows[i])
		}
		hues = append(hues, hue)
	}
	if len(hues) > 1 && hues[0] == hues[1] {
		t.Errorf("two providers' lanes are drawn in the same colour %q", hues[0])
	}
}

// Every lane is drawn in ITS OWN provider's hue. The expected-row builders
// elsewhere derive the hue the same way the render site does, so a render
// site that looked up the wrong lane would agree with them and be caught
// only by a golden -- which go test -update rewrites.
//
// Asserted as agreement and difference rather than against named colours, so
// a deliberate recolour does not have to come here to be re-stated.
func TestEachLanesBarIsDrawnInItsOwnProvidersHue(t *testing.T) {
	m := timelineModel(t)
	lanes := m.timelineLanes()
	_, spans := m.timelineSpans()
	rows := timelineLaneRows(t, m, lanes)

	byProvider := map[string]string{}
	for i := range lanes {
		p, hue := laneProvider(spans, lanes[i]), barHueOf(t, rows[i])
		if hue == "" {
			t.Fatalf("lane %d (%s) carries no colour, so this compares nothing: %q", i, p, rows[i])
		}
		if seen, ok := byProvider[p]; ok && seen != hue {
			t.Errorf("provider %s has lanes in %q and %q", p, seen, hue)
		}
		for q, seen := range byProvider {
			if q != p && seen == hue {
				t.Errorf("providers %s and %s are both drawn in %q", p, q, hue)
			}
		}
		byProvider[p] = hue
	}
	if len(byProvider) < 2 {
		t.Fatalf("the fixture draws %d providers, want at least two so the colours can differ", len(byProvider))
	}
}

// A hue belongs to the LOG, not to what a filter has left on screen.
// Recoloured on every toggle, the lanes would read as having changed when
// only the selection did -- on the one view whose whole subject is which
// work ran when.
//
// google is the provider filtered TO and it is not the first, so assigning
// from the visible lanes would hand it the first colour and the two
// arrangements disagree here. Filtering to the first provider would pass
// either way.
func TestALanesHueSurvivesAFilterHidingAnotherProvider(t *testing.T) {
	m := timelineModel(t)
	lanes := m.timelineLanes()
	_, spans := m.timelineSpans()
	row := -1
	for i := range lanes {
		if laneProvider(spans, lanes[i]) == "google" {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("the fixture draws no google lane")
	}
	rows := timelineLaneRows(t, m, lanes)
	before := barHueOf(t, rows[row])
	if before == "" || before == barHueOf(t, rows[0]) {
		t.Fatalf("google's lane is drawn in %q, the first lane's own colour, so a recolour could not show", before)
	}

	showOnly(t, &m, dimProvider, "registry.terraform.io/hashicorp/google")

	if got := barHueOf(t, timelineLaneRows(t, m, m.timelineLanes())[0]); got != before {
		t.Errorf("google's lane went from %q to %q when a filter hid the other provider", before, got)
	}
}

// timelineLaneRows renders the timeline into a pane tall enough for every
// lane to get a row, and returns just those rows. The axis and the notes sit
// beneath the lanes, so a pane too short drops lanes rather than notes and
// the row at index i stops being lane i.
func timelineLaneRows(t *testing.T, m Model, lanes []model.Lane) []string {
	t.Helper()
	const roomForAxisAndNotes = 8
	rows := strings.Split(m.renderTimeline(100, len(lanes)+roomForAxisAndNotes), "\n")
	if len(rows) < len(lanes) {
		t.Fatalf("the pane drew %d rows for %d lanes:\n%s", len(rows), len(lanes), strings.Join(rows, "\n"))
	}
	return rows[:len(lanes)]
}
