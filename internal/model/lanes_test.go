package model

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func timed(start, end uint32, f span.Fidelity) span.Span {
	return span.Span{StartMs: start, EndMs: end, DurationMs: end - start, Fidelity: f}
}

func TestPackLanesPutsOverlappingSpansInSeparateLanes(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(500, 1500, span.FidelityReported),
		timed(2000, 2500, span.FidelityReported),
	}
	lanes, err := PackLanes(spans)
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 2 {
		t.Fatalf("got %d lanes, want 2: %+v", len(lanes), lanes)
	}
	// The third span starts after the first ends, so it reuses lane 0.
	if len(lanes[0].Spans) != 2 {
		t.Errorf("lane 0 holds %d spans, want 2 -- a non-overlapping span must reuse a free lane", len(lanes[0].Spans))
	}
}

func TestPackLanesKeepsEverySpan(t *testing.T) {
	spans := []span.Span{
		timed(0, 100, span.FidelityReported),
		timed(10, 200, span.FidelityReported),
		timed(20, 300, span.FidelityReported),
	}
	lanes, err := PackLanes(spans)
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	var n int
	seen := map[int]bool{}
	for _, l := range lanes {
		for _, i := range l.Spans {
			if seen[i] {
				t.Errorf("span %d appears in more than one lane", i)
			}
			seen[i] = true
			n++
		}
	}
	if n != len(spans) {
		t.Errorf("packed %d spans, want %d", n, len(spans))
	}
}

// The two builders anchor StartMs/EndMs to different zero points, so packing
// a mixed slice would interleave two unrelated timelines and produce lanes
// that look plausible and mean nothing. This must fail loudly, not silently.
func TestPackLanesRejectsMixedFidelity(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(0, 1000, span.FidelityUIReported),
	}
	if _, err := PackLanes(spans); err == nil {
		t.Fatal("PackLanes accepted spans from two different timelines")
	}
}

func TestPeakConcurrency(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(100, 900, span.FidelityReported),
		timed(200, 800, span.FidelityReported),
		timed(5000, 6000, span.FidelityReported),
	}
	got, err := PeakConcurrency(spans)
	if err != nil {
		t.Fatalf("PeakConcurrency: %v", err)
	}
	if got != 3 {
		t.Errorf("PeakConcurrency = %d, want 3", got)
	}
}

// PeakConcurrency sweeps the same hazardous StartMs/EndMs fields PackLanes
// does, so it must refuse a mixed-fidelity slice for the same reason:
// packing two builders' timelines together produces a plausible, silently
// wrong number.
func TestPeakConcurrencyRejectsMixedFidelity(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(0, 1000, span.FidelityUIReported),
	}
	if _, err := PeakConcurrency(spans); err == nil {
		t.Fatal("PeakConcurrency accepted spans from two different timelines")
	}
}

func TestPackLanesEmptyInput(t *testing.T) {
	lanes, err := PackLanes(nil)
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 0 {
		t.Errorf("got %d lanes from no spans, want 0", len(lanes))
	}
}

// TestPeakConcurrencyHandoverBoundary verifies that a span ending exactly when
// another begins is counted as a handover, not overlap. If the sort comparator
// were reversed, this would fail with peak = 2 instead of 1.
func TestPeakConcurrencyHandoverBoundary(t *testing.T) {
	spans := []span.Span{
		timed(0, 500, span.FidelityReported),
		timed(500, 1000, span.FidelityReported),
	}
	got, err := PeakConcurrency(spans)
	if err != nil {
		t.Fatalf("PeakConcurrency: %v", err)
	}
	if got != 1 {
		t.Errorf("PeakConcurrency = %d, want 1 (handover not overlap)", got)
	}
}

// A zero-duration span's interval [t, t) is empty under the half-open
// semantics PeakConcurrency and PackLanes both use: it contains no instant,
// so it genuinely overlaps nothing, even when it sits nested inside two
// spans that are still running at t. This is deliberate, not a gap in
// coverage: PackLanes still allocates the zero-duration span a lane, because
// the phase-4 timeline needs a row to draw it in regardless of whether it
// overlaps anything, so PackLanes' lane count can legitimately exceed
// PeakConcurrency's peak. The two functions answer different questions and
// are not expected to agree here.
func TestPeakConcurrencyIgnoresZeroDurationSpans(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(200, 800, span.FidelityReported),
		timed(500, 500, span.FidelityReported), // zero-duration, nested inside both
	}
	got, err := PeakConcurrency(spans)
	if err != nil {
		t.Fatalf("PeakConcurrency: %v", err)
	}
	if got != 2 {
		t.Errorf("PeakConcurrency = %d, want 2 (the zero-duration span's empty interval overlaps nothing)", got)
	}

	lanes, err := PackLanes(spans)
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 3 {
		t.Errorf("PackLanes packed %d lanes, want 3 -- the zero-duration span still needs a row to draw, even though it overlaps nothing", len(lanes))
	}
}

func TestStallsFindsTheWindowWhereOnlyOneSpanRan(t *testing.T) {
	// Three spans. Two finish early; one runs long past them, so from
	// 100ms to 500ms two of the three lanes are idle.
	spans := []span.Span{
		{StartMs: 0, EndMs: 500, DurationMs: 500, RPC: "Configure"},
		{StartMs: 0, EndMs: 100, DurationMs: 100, RPC: "ReadResource"},
		{StartMs: 0, EndMs: 80, DurationMs: 80, RPC: "ReadResource"},
	}
	got, err := Stalls(spans, 3, 50)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 100, EndMs: 500, Idle: 2, Blocking: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v", got, want)
	}
}

func TestStallsIgnoresWindowsBelowTheThreshold(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 0, EndMs: 90, DurationMs: 90},
	}
	got, err := Stalls(spans, 2, 50)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stalls = %+v, want none: the 10ms window is below the 50ms threshold", got)
	}
}

func TestStallsRefusesMixedTimelines(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, Fidelity: span.FidelityReported},
		{StartMs: 0, EndMs: 100, Fidelity: span.FidelityUIReported},
	}
	if _, err := Stalls(spans, 2, 0); !errors.Is(err, ErrMixedTimelines) {
		t.Errorf("Stalls over mixed fidelities = %v, want ErrMixedTimelines", err)
	}
}

func TestStallsReportsNoStallWhenEverySpanRunsThroughout(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 500, DurationMs: 500},
		{StartMs: 0, EndMs: 500, DurationMs: 500},
	}
	got, err := Stalls(spans, 2, 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stalls = %+v, want none: both lanes are busy for the whole window", got)
	}
}

// A gap where every span has already finished is the space between two
// phases of work, not a stall: nothing is idle, because nothing is running
// to be idle. A sweep that reported this gap would blame a span that had
// already completed for a wait it played no part in.
func TestStallsIgnoresGapsWhereNothingIsRunning(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 200, EndMs: 260, DurationMs: 60},
	}
	got, err := Stalls(spans, 2, 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 200, EndMs: 260, Idle: 1, Blocking: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v (the 100ms-200ms gap has nothing running, so it must not appear)", got, want)
	}
}

// Two sweep segments that share the same idle count and the same blocking
// span are one stall, even though a third span's handover splits the sweep
// into two segments at the midpoint. Thresholding before merging would drop
// both 30ms halves of this 60ms stall; thresholding after merging keeps it.
func TestStallsMergesAdjacentSegmentsBeforeThresholding(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 1000, DurationMs: 1000}, // always running; the blocking span throughout
		{StartMs: 0, EndMs: 30, DurationMs: 20},
		{StartMs: 30, EndMs: 60, DurationMs: 20},
	}
	got, err := Stalls(spans, 3, 50)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{
		{StartMs: 0, EndMs: 60, Idle: 1, Blocking: 0},
		{StartMs: 60, EndMs: 1000, Idle: 2, Blocking: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v", got, want)
	}
}

// A zero-duration span's interval [StartMs, EndMs) is empty, so it must
// never win Blocking once it has both started and ended -- even when the
// genuinely running span sharing the window also happens to report
// DurationMs 0, as a StartClamped span can (see PeakConcurrency's doc
// comment). A liveness bookkeeping scheme that leaves the elapsed span
// marked "running" would let it win the tie, pointing the annotation at a
// span whose interval had already elapsed instead of the one still going.
func TestStallsIgnoresElapsedZeroDurationSpansWhenChoosingBlocking(t *testing.T) {
	spans := []span.Span{
		{StartMs: 30, EndMs: 30, DurationMs: 0}, // elapsed by t=30; contributes nothing
		{StartMs: 0, EndMs: 100, DurationMs: 0}, // genuinely running throughout
	}
	got, err := Stalls(spans, 2, 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 0, EndMs: 100, Idle: 1, Blocking: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v (the elapsed zero-duration span must not win Blocking)", got, want)
	}
}

// A tie in DurationMs between two spans running throughout the same window
// must resolve deterministically towards the lower span index, not
// however Go's map iteration order happens to land.
func TestStallsBreaksDurationTiesTowardsTheLowerIndex(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 200, DurationMs: 200},
		{StartMs: 0, EndMs: 200, DurationMs: 200},
		{StartMs: 0, EndMs: 100, DurationMs: 100},
	}
	got, err := Stalls(spans, 3, 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 100, EndMs: 200, Idle: 1, Blocking: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v", got, want)
	}
}
