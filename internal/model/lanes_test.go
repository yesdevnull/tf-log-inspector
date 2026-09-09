package model

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func timed(start, end uint32, f span.Fidelity) span.Span {
	return span.Span{StartMs: start, EndMs: end, DurationMs: end - start, Fidelity: f, TimestampStatus: logfmt.TimestampValid}
}

func positioned(spans []span.Span) []span.Span {
	for i := range spans {
		spans[i].TimestampStatus = logfmt.TimestampValid
	}
	return spans
}

func TestPackLanesPutsOverlappingSpansInSeparateLanes(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(500, 1500, span.FidelityReported),
		timed(2000, 2500, span.FidelityReported),
	}
	lanes, err := PackLanes(positioned(spans))
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
	lanes, err := PackLanes(positioned(spans))
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
	if _, err := PackLanes(positioned(spans)); err == nil {
		t.Fatal("PackLanes accepted spans from two different timelines")
	}
}

func TestTemporalFunctionsRejectUnavailablePositions(t *testing.T) {
	spans := []span.Span{{Fidelity: span.FidelityReported}}
	for name, call := range map[string]func() error{
		"PackLanes":       func() error { _, err := PackLanes(spans); return err },
		"PeakConcurrency": func() error { _, err := PeakConcurrency(spans); return err },
		"BusyMs":          func() error { _, err := BusyMs(spans); return err },
		"Stalls":          func() error { _, err := Stalls(spans, 0); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrUnavailablePosition) {
				t.Errorf("error = %v, want ErrUnavailablePosition", err)
			}
		})
	}
}

func TestPeakConcurrency(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(100, 900, span.FidelityReported),
		timed(200, 800, span.FidelityReported),
		timed(5000, 6000, span.FidelityReported),
	}
	got, err := PeakConcurrency(positioned(spans))
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
	if _, err := PeakConcurrency(positioned(spans)); err == nil {
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
	got, err := PeakConcurrency(positioned(spans))
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
	got, err := PeakConcurrency(positioned(spans))
	if err != nil {
		t.Fatalf("PeakConcurrency: %v", err)
	}
	if got != 2 {
		t.Errorf("PeakConcurrency = %d, want 2 (the zero-duration span's empty interval overlaps nothing)", got)
	}

	lanes, err := PackLanes(positioned(spans))
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 3 {
		t.Errorf("PackLanes packed %d lanes, want 3 -- the zero-duration span still needs a row to draw, even though it overlaps nothing", len(lanes))
	}
}

func TestStallsFindsTheWindowWhereOnlyOneSpanRan(t *testing.T) {
	// Three spans. Two finish together at 100ms; one runs long past them,
	// so from 100ms to 500ms a single span is running against a peak of
	// three. They finish together deliberately: this window holds one depth
	// from end to end, and a wait whose depth changes partway is
	// TestStallsMergesAWaitWhoseDepthChanges.
	spans := []span.Span{
		{StartMs: 0, EndMs: 500, DurationMs: 500, RPC: "Configure"},
		{StartMs: 0, EndMs: 100, DurationMs: 100, RPC: "ReadResource"},
		{StartMs: 0, EndMs: 100, DurationMs: 100, RPC: "ReadResource"},
	}
	got, err := Stalls(positioned(spans), 50)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 100, EndMs: 500, MinRunning: 1, MaxRunning: 1, Capacity: 3, Blocking: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v", got, want)
	}
}

func TestStallsIgnoresWindowsBelowTheThreshold(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 0, EndMs: 90, DurationMs: 90},
	}
	got, err := Stalls(positioned(spans), 50)
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
	if _, err := Stalls(positioned(spans), 0); !errors.Is(err, ErrMixedTimelines) {
		t.Errorf("Stalls over mixed fidelities = %v, want ErrMixedTimelines", err)
	}
}

func TestStallsReportsNoStallWhenEverySpanRunsThroughout(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 500, DurationMs: 500},
		{StartMs: 0, EndMs: 500, DurationMs: 500},
	}
	got, err := Stalls(positioned(spans), 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stalls = %+v, want none: both lanes are busy for the whole window", got)
	}
}

// A window where every lane is idle at once is the commonest shape of a
// wait: Terraform core working with no provider call in flight at all.
// There is no span to blame for it, which is the whole reason it is easy to
// skip -- but "cannot name a blocker" is not "is not a wait", and a
// timeline that draws that blank space while its annotation reports no
// stalls contradicts its own picture. Such a window is reported with
// Blocking -1.
//
// It never merges into a blocked window on either side, however closely
// they abut: see TestStallsNeverMergesAcrossTheBlockedAndAllIdleBoundary.
func TestStallsReportsWindowsWhereNothingWasRunning(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 200, EndMs: 260, DurationMs: 60},
	}
	got, err := Stalls(positioned(spans), 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{
		{StartMs: 100, EndMs: 200, MinRunning: 0, MaxRunning: 0, Capacity: 2, Blocking: -1},
		{StartMs: 200, EndMs: 260, MinRunning: 1, MaxRunning: 1, Capacity: 2, Blocking: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v (the 100ms-200ms window has nothing running, which is a wait with no span to name)", got, want)
	}
}

// Windows merge on contiguity, so the one rule holding two findings apart is
// the sign of Blocking: a window with a span still running is "waiting on
// aws/1" and one with nothing running at all is "nothing running", and they
// have different causes even when they abut. Merging them would report a
// span as blocking a stretch it had already finished before -- the same
// reader-facing failure the merge rule exists to remove, one level up.
//
// The three windows here put the boundary in both directions: span 1
// finishes at 50ms leaving span 0 running alone, span 0 finishes at 100ms
// leaving nothing running, and span 2 opens at 150ms running alone again.
// Every pair is contiguous, and no pair may merge.
func TestStallsNeverMergesAcrossTheBlockedAndAllIdleBoundary(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 0, EndMs: 50, DurationMs: 50},
		{StartMs: 150, EndMs: 200, DurationMs: 50},
	}
	got, err := Stalls(positioned(spans), 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{
		{StartMs: 50, EndMs: 100, MinRunning: 1, MaxRunning: 1, Capacity: 2, Blocking: 0},
		{StartMs: 100, EndMs: 150, MinRunning: 0, MaxRunning: 0, Capacity: 2, Blocking: -1},
		{StartMs: 150, EndMs: 200, MinRunning: 1, MaxRunning: 1, Capacity: 2, Blocking: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v (a blocked wait and an all-idle window are different findings and never merge, in either order)", got, want)
	}
}

// The ordinary tail of a plan: three providers draining off one at a time.
// google's lane is idle continuously from 20.0s to the end, and merging on
// the running count alone ends that wait and starts another the moment
// azurerm finishes too -- reporting one 40s wait as two of 20s, and
// spending two of the three lines a caller has room for on one ramp-down.
//
// The merged window reports the range it covered rather than its floor.
// Reporting only the floor would say "1 of 3" for a stretch that ran two-up
// for half its length, which overstates how bad the earlier half was; and
// no direction is claimed, because merging on contiguity admits a window
// that fell and rose again.
func TestStallsMergesAWaitWhoseDepthChanges(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 60000, DurationMs: 60000}, // aws, running throughout
		{StartMs: 0, EndMs: 20000, DurationMs: 20000}, // google
		{StartMs: 0, EndMs: 40000, DurationMs: 40000}, // azurerm
	}
	peak, err := PeakConcurrency(positioned(spans))
	if err != nil {
		t.Fatalf("PeakConcurrency: %v", err)
	}
	got, err := Stalls(positioned(spans), 1000)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 20000, EndMs: 60000, MinRunning: 1, MaxRunning: 2, Capacity: 3, Blocking: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v (one 40s wait at a depth that changes, not two waits of 20s)", got, want)
	}
	// Capacity is carried on the stall so a caller renders both halves of
	// "M of N" from one measurement of one slice, rather than sweeping the
	// spans a second time for the denominator.
	if got[0].Capacity != peak {
		t.Errorf("stall Capacity = %d, want PeakConcurrency's %d", got[0].Capacity, peak)
	}
}

// One wait split by a handover is one stall: the 10ms-30ms and 30ms-60ms
// segments are 20ms and 30ms apart, so thresholding before merging would
// drop both halves of a wait that starts at 10ms and never lets up.
// Thresholding after merging keeps it, which is what the reported window
// starting at 10ms rather than at 60ms shows.
//
// The fourth span exists to lift the spans' peak concurrency to three:
// without it nothing here ever runs three-up, so the first two segments are
// running at capacity and the merge has nothing to act on.
func TestStallsMergesAdjacentSegmentsBeforeThresholding(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 1000, DurationMs: 1000}, // always running; the blocking span throughout
		{StartMs: 0, EndMs: 30, DurationMs: 30},
		{StartMs: 30, EndMs: 60, DurationMs: 30},
		{StartMs: 0, EndMs: 10, DurationMs: 10},
	}
	got, err := Stalls(positioned(spans), 50)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 10, EndMs: 1000, MinRunning: 1, MaxRunning: 2, Capacity: 3, Blocking: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v", got, want)
	}
}

// A zero-duration span's interval [StartMs, EndMs) is empty, so it must
// never win Blocking once it has both started and ended: the sweep has to
// tell "started and finished in the same instant" apart from "still
// running", and a scheme keyed by last write wins leaves the elapsed span
// marked running for the rest of the sweep, pointing the annotation at a
// span whose interval had already elapsed instead of the one still going.
//
// Span 1's shape -- a 100ms extent with DurationMs 0 -- is synthetic:
// both builders derive StartMs by subtracting DurationMs from EndMs, so a
// real span's extent never exceeds its stored duration, and a real
// DurationMs-0 span is therefore always zero-extent. It is written that way
// deliberately, because the tie at DurationMs 0 is the only arrangement in
// which a wrongly-live elapsed span could beat the span that is genuinely
// running, and a test for that bookkeeping has to be able to express it.
//
// Span 2 lifts the timeline's capacity to two lanes, so that there is an
// idle lane to report a blocking span for at all.
func TestStallsIgnoresElapsedZeroDurationSpansWhenChoosingBlocking(t *testing.T) {
	spans := []span.Span{
		{StartMs: 30, EndMs: 30, DurationMs: 0}, // elapsed by t=30; contributes nothing
		{StartMs: 0, EndMs: 100, DurationMs: 0}, // genuinely running throughout
		{StartMs: 0, EndMs: 20, DurationMs: 20}, // the second lane's work
	}
	got, err := Stalls(positioned(spans), 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 20, EndMs: 100, MinRunning: 1, MaxRunning: 1, Capacity: 2, Blocking: 1}}
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
	got, err := Stalls(positioned(spans), 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 100, EndMs: 200, MinRunning: 2, MaxRunning: 2, Capacity: 3, Blocking: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v", got, want)
	}
}

// PackLanes hands a zero-extent span its own lane, because the timeline
// needs a row to draw it in (see PeakConcurrency's doc comment), so on a log
// where every real span overlaps, the lane count exceeds the number of lanes
// any span can occupy. That surplus is a drawing artefact, not idle
// capacity: counted as idle it reports the whole run as one stall on the
// strength of a single instantaneous span. The UI-hook tier produces those
// routinely -- a completion hook carrying no elapsed_seconds, which is what
// Terraform's "complete after 0s" lines are, is stored with DurationMs 0.
func TestStallsDoesNotCountTheLaneAZeroDurationSpanOpens(t *testing.T) {
	busy := []span.Span{
		{StartMs: 0, EndMs: 30000, DurationMs: 30000},
		{StartMs: 0, EndMs: 30000, DurationMs: 30000},
		{StartMs: 0, EndMs: 30000, DurationMs: 30000},
	}
	if got, err := Stalls(positioned(busy), 1000); err != nil {
		t.Fatalf("Stalls: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("Stalls over three spans that all run throughout = %+v, want none", got)
	}

	withZero := []span.Span{busy[0], busy[1], busy[2], {StartMs: 5000, EndMs: 5000, DurationMs: 0}}
	lanes, err := PackLanes(positioned(withZero))
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 4 {
		t.Fatalf("PackLanes packed %d lanes, want 4 -- this test's premise is that the zero-duration span opens a fourth", len(lanes))
	}
	got, err := Stalls(positioned(withZero), 1000)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stalls = %+v, want none: the lane a zero-duration span opens can hold work in no window, so it is not idle capacity", got)
	}
}

// The spans testdata/timeline-many-stalls.log builds: one short google call,
// then four aws calls handing over to each other back to back. google's lane
// is idle continuously from 3.3s to 20.0s, blocked by aws throughout -- but
// by four DIFFERENT aws spans, because the blocking lane is by definition
// the one that keeps starting new work. Merging on the blocking span as well
// as the idle count splits that one wait into four, of which the last falls
// under the threshold and is dropped, understating the wait by two seconds
// and spending four annotation lines saying it.
//
// The window before the first span is its own stall, with no span to name:
// see TestStallsReportsTheIdleWindowBeforeTheFirstSpan.
func TestStallsMergesOneWaitAcrossHandoversInsideTheBlockingLane(t *testing.T) {
	spans := []span.Span{
		{StartMs: 3100, EndMs: 3300, DurationMs: 200},    // google
		{StartMs: 3000, EndMs: 9000, DurationMs: 6000},   // aws, and the longest of them
		{StartMs: 9000, EndMs: 14000, DurationMs: 5000},  // aws
		{StartMs: 14000, EndMs: 18000, DurationMs: 4000}, // aws
		{StartMs: 18000, EndMs: 20000, DurationMs: 2000}, // aws
	}
	lanes, err := PackLanes(positioned(spans))
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 2 {
		t.Fatalf("PackLanes packed %d lanes, want 2 -- every aws span reuses lane 0", len(lanes))
	}
	got, err := Stalls(positioned(spans), 1000)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{
		{StartMs: 0, EndMs: 3000, MinRunning: 0, MaxRunning: 0, Capacity: 2, Blocking: -1},
		{StartMs: 3300, EndMs: 20000, MinRunning: 1, MaxRunning: 1, Capacity: 2, Blocking: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v (one 16.7s wait, named by the longest span that blocked any part of it)", got, want)
	}
}

// The timeline's zero point is the log's own -- a span's StartMs is an
// offset from the log's first timestamped entry, not from its first span --
// so time before the first span is elapsed capture time with nothing
// running: the same wait as any other all-idle window, drawn as the same
// blank space at the left of every lane.
//
// Nothing is reported after the last span ends. Stalls is handed no
// end-of-log timestamp, so the last event it sees is the last thing it
// knows happened; a trailing window would be invented rather than measured.
func TestStallsReportsTheIdleWindowBeforeTheFirstSpan(t *testing.T) {
	spans := []span.Span{
		{StartMs: 8000, EndMs: 10000, DurationMs: 2000},
		{StartMs: 8000, EndMs: 10000, DurationMs: 2000},
	}
	got, err := Stalls(positioned(spans), 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 0, EndMs: 8000, MinRunning: 0, MaxRunning: 0, Capacity: 2, Blocking: -1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v (8s of the capture elapsed before any span started, and none after the last ended)", got, want)
	}

	atZero := []span.Span{
		{StartMs: 0, EndMs: 10000, DurationMs: 10000},
		{StartMs: 0, EndMs: 10000, DurationMs: 10000},
	}
	if got, err := Stalls(positioned(atZero), 0); err != nil {
		t.Fatalf("Stalls: %v", err)
	} else if len(got) != 0 {
		t.Errorf("Stalls = %+v, want none: work starts at the log's zero point, so there is no window before it", got)
	}
}

// Stalls maintains its running set incrementally rather than rescanning
// every span per window, so the properties a per-window rescan would make
// self-evident are pinned here instead, over spans dense enough to keep the
// bookkeeping busy: the span named as Blocking runs somewhere in the window
// and no span running in that window outranks it, -1 appears only where
// nothing runs at all, every window's depth sits inside the capacity it is
// reported against, and no two returned windows could still have been
// merged.
func TestStallsBlockingNamesTheLongestSpanRunningInTheWindow(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	spans := make([]span.Span, 200)
	for i := range spans {
		start := uint32(r.IntN(5000))
		dur := uint32(r.IntN(400))
		spans[i] = span.Span{StartMs: start, EndMs: start + dur, DurationMs: dur}
	}
	got, err := Stalls(positioned(spans), 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("Stalls found no windows over 200 overlapping spans: this test would assert nothing")
	}

	runsIn := func(s span.Span, w Stall) bool {
		return max(s.StartMs, w.StartMs) < min(s.EndMs, w.EndMs)
	}
	for _, w := range got {
		for i, s := range spans {
			switch {
			case w.Blocking < 0 && runsIn(s, w):
				t.Fatalf("stall %+v names no blocking span, but span %d ran in that window", w, i)
			case w.Blocking < 0 || !runsIn(s, w):
				continue
			case s.DurationMs > spans[w.Blocking].DurationMs,
				s.DurationMs == spans[w.Blocking].DurationMs && i < w.Blocking:
				t.Fatalf("stall %+v names span %d (%dms), but span %d (%dms) also ran in that window and outranks it", w, w.Blocking, spans[w.Blocking].DurationMs, i, s.DurationMs)
			}
		}
		if w.Blocking >= 0 && !runsIn(spans[w.Blocking], w) {
			t.Fatalf("stall %+v names span %d, which does not run anywhere in that window", w, w.Blocking)
		}
	}
	peak, err := PeakConcurrency(positioned(spans))
	if err != nil {
		t.Fatalf("PeakConcurrency: %v", err)
	}
	for _, w := range got {
		switch {
		case w.Capacity != peak:
			t.Fatalf("stall %+v is measured against capacity %d, but the spans' peak concurrency is %d", w, w.Capacity, peak)
		case w.MinRunning > w.MaxRunning || w.MaxRunning >= w.Capacity:
			t.Fatalf("stall %+v has an impossible depth: a window is reported only while fewer than %d spans run", w, w.Capacity)
		case (w.MinRunning == 0) != (w.Blocking < 0):
			t.Fatalf("stall %+v names a blocking span for a window that ran empty, or names none for a window that did not", w)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].EndMs == got[i].StartMs && (got[i-1].Blocking < 0) == (got[i].Blocking < 0) {
			t.Fatalf("stalls %+v and %+v are contiguous and are the same kind of finding: they are one window reported as two", got[i-1], got[i])
		}
	}
}

// BusyMs is the union of the spans' intervals, not the sum of their
// durations: two spans overlapping by 5ms cover 15ms between them, and a
// sum would report 20ms of work in a 15ms window.
func TestBusyMsCountsOverlappingSpansOnce(t *testing.T) {
	spans := []span.Span{
		timed(0, 10, span.FidelityReported),
		timed(5, 15, span.FidelityReported),
	}
	got, err := BusyMs(positioned(spans))
	if err != nil {
		t.Fatalf("BusyMs: %v", err)
	}
	if got != 15 {
		t.Errorf("BusyMs = %d, want 15 -- the union of [0,10) and [5,15), not the 20ms their durations sum to", got)
	}
}

func TestBusyMsLeavesOutTheGapsBetweenSpans(t *testing.T) {
	spans := []span.Span{
		timed(0, 40, span.FidelityReported),
		timed(3000, 3040, span.FidelityReported),
	}
	got, err := BusyMs(positioned(spans))
	if err != nil {
		t.Fatalf("BusyMs: %v", err)
	}
	if got != 80 {
		t.Errorf("BusyMs = %d, want 80 -- the 2.96s between the two calls is idle, not busy", got)
	}
}

func TestBusyMsCountsANestedSpanOnce(t *testing.T) {
	// A span wholly inside another adds nothing: the window it covers was
	// already busy.
	spans := []span.Span{
		timed(0, 100, span.FidelityReported),
		timed(20, 30, span.FidelityReported),
	}
	got, err := BusyMs(positioned(spans))
	if err != nil {
		t.Fatalf("BusyMs: %v", err)
	}
	if got != 100 {
		t.Errorf("BusyMs = %d, want 100 -- a nested span covers time already counted", got)
	}
}

// A zero-duration span has the empty interval [t, t), so it covers no
// millisecond at all -- the same treatment PeakConcurrency gives one, and
// the UI-hook tier emits them by the dozen.
func TestBusyMsIgnoresZeroDurationSpans(t *testing.T) {
	spans := []span.Span{
		timed(50, 50, span.FidelityUIReported),
		timed(60, 60, span.FidelityUIReported),
	}
	got, err := BusyMs(positioned(spans))
	if err != nil {
		t.Fatalf("BusyMs: %v", err)
	}
	if got != 0 {
		t.Errorf("BusyMs = %d, want 0 -- a zero-extent span covers no time", got)
	}
}

// A zero-duration span sitting inside a running one must not close it: the
// sweep nets its start and end at the same instant, the way Stalls does.
func TestBusyMsKeepsARunningSpanBusyAcrossAZeroDurationOne(t *testing.T) {
	spans := []span.Span{
		timed(0, 100, span.FidelityReported),
		timed(50, 50, span.FidelityReported),
	}
	got, err := BusyMs(positioned(spans))
	if err != nil {
		t.Fatalf("BusyMs: %v", err)
	}
	if got != 100 {
		t.Errorf("BusyMs = %d, want 100 -- an instantaneous span inside a running one does not end it", got)
	}
}

func TestBusyMsRefusesAMixedFidelitySlice(t *testing.T) {
	spans := []span.Span{
		timed(0, 100, span.FidelityReported),
		timed(0, 100, span.FidelityUIReported),
	}
	if _, err := BusyMs(positioned(spans)); !errors.Is(err, ErrMixedTimelines) {
		t.Errorf("BusyMs over mixed fidelities = %v, want ErrMixedTimelines", err)
	}
}

func TestBusyMsOfNoSpansIsZero(t *testing.T) {
	got, err := BusyMs(nil)
	if err != nil {
		t.Fatalf("BusyMs: %v", err)
	}
	if got != 0 {
		t.Errorf("BusyMs = %d, want 0", got)
	}
}
