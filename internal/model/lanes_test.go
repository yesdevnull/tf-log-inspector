package model

import (
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
	if got := PeakConcurrency(spans); got != 3 {
		t.Errorf("PeakConcurrency = %d, want 3", got)
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
	if got := PeakConcurrency(spans); got != 1 {
		t.Errorf("PeakConcurrency = %d, want 1 (handover not overlap)", got)
	}
}
