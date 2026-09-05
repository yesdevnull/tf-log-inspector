package model

import (
	"errors"
	"math/bits"
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Lane holds indices into the slice passed to PackLanes, in start order.
// Indices rather than copies so a caller can look up the original span
// without the lanes duplicating span data.
type Lane struct{ Spans []int }

// ErrMixedTimelines is returned when PackLanes or PeakConcurrency is handed
// spans from more than one builder. See the doc comment on span.Span: the
// builders anchor StartMs/EndMs to different zero points, so sweeping a mixed
// slice interleaves two unrelated timelines into a result that looks
// plausible and means nothing. Refusing is the only safe behaviour, because
// there is no signal in the output that would let a reader notice.
var ErrMixedTimelines = errors.New("model: cannot pack lanes across spans of different fidelity")

// sameFidelity reports ErrMixedTimelines when spans holds more than one
// span.Fidelity. Both PackLanes and PeakConcurrency sweep StartMs/EndMs, so
// both need this guard: those fields are comparable only within spans built
// by the same builder -- see the doc comment on span.Span.
func sameFidelity(spans []span.Span) error {
	for _, s := range spans[1:] {
		if s.Fidelity != spans[0].Fidelity {
			return ErrMixedTimelines
		}
	}
	return nil
}

// PackLanes assigns spans to execution lanes by greedy interval packing: each
// span goes into the first lane whose last span has already finished.
//
// Lanes are computed, not read from the log. Terraform's log carries no worker
// identifier and none is needed -- packing depends only on start and end
// times, so this behaves identically at every extraction tier.
func PackLanes(spans []span.Span) ([]Lane, error) {
	if len(spans) == 0 {
		return nil, nil
	}
	if err := sameFidelity(spans); err != nil {
		return nil, err
	}

	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		x, y := spans[order[a]], spans[order[b]]
		if x.StartMs != y.StartMs {
			return x.StartMs < y.StartMs
		}
		return x.EndMs < y.EndMs
	})

	var lanes []Lane
	var laneEnd []uint32
	for _, i := range order {
		s := spans[i]
		placed := false
		for l := range lanes {
			if laneEnd[l] <= s.StartMs {
				lanes[l].Spans = append(lanes[l].Spans, i)
				laneEnd[l] = s.EndMs
				placed = true
				break
			}
		}
		if !placed {
			lanes = append(lanes, Lane{Spans: []int{i}})
			laneEnd = append(laneEnd, s.EndMs)
		}
	}
	return lanes, nil
}

// spanEvent is one span's start or end instant in a half-open-interval
// sweep: delta is +1 at StartMs and -1 at EndMs. idx is the span's index in
// the slice spanEvents was built from; PeakConcurrency ignores it, Stalls
// needs it to know which span opened or closed.
type spanEvent struct {
	at    uint32
	delta int
	idx   int
}

// spanEvents returns spans' start and end events, sorted by time with ends
// before starts at the same instant: a span ending exactly as another
// begins is a handover, not overlap. PeakConcurrency's peak sweep and
// Stalls' idle sweep both depend on this exact ordering, so it is built
// once here rather than duplicated -- the zero-duration edge case
// documented below is subtle enough that two independently maintained
// copies of this ordering is exactly where they would drift out of step
// with each other.
func spanEvents(spans []span.Span) []spanEvent {
	events := make([]spanEvent, 0, len(spans)*2)
	for i, s := range spans {
		events = append(events, spanEvent{s.StartMs, 1, i}, spanEvent{s.EndMs, -1, i})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		return events[i].delta < events[j].delta
	})
	return events
}

// PeakConcurrency is the largest number of spans in flight at once, computed
// by sweeping start and end events. It is the headline number for whether a
// plan was slow because of work or because of waiting: summed span time
// divided by wall clock gives the average, and this gives the ceiling.
//
// Both this function and PackLanes use half-open interval semantics:
// [StartMs, EndMs), the same convention PackLanes' "laneEnd[l] <= s.StartMs"
// reuse test expresses. A zero-duration span has StartMs == EndMs, so its
// interval [t, t) is empty -- it contains no instant, so it overlaps
// nothing, so it correctly contributes nothing here. That holds even when
// the span sits nested inside others that are genuinely running: an empty
// interval overlaps no instant, including the instants those other spans
// occupy. PackLanes still allocates such a span a lane, because the phase-4
// timeline needs a row to draw it in even though it correctly overlaps
// nothing -- so PackLanes' lane count can legitimately exceed this
// function's peak. That is not a discrepancy to reconcile; the two answer
// different questions.
//
// A zero-duration span is not a synthetic edge case. The UI-hook tier
// produces them routinely: a completion hook that carries no
// elapsed_seconds at all -- what Terraform's "complete after 0s" lines
// are -- is stored with DurationMs 0 by span.UIHookBuilder, and both
// builders derive StartMs by subtracting DurationMs from EndMs, so such a
// span is always zero-extent.
//
// StartClamped is a separate degenerate case and not this one: a span whose
// reported duration exceeds its offset from the log's first entry has its
// start clamped to zero, leaving the extent [0, EndMs). That is empty only
// when the span closed on the log's very first timestamped entry -- a
// clamped span's DurationMs is strictly greater than that offset, so its
// DurationMs is never zero either.
//
// It rejects a mixed-fidelity slice for the same reason PackLanes does: see
// sameFidelity.
func PeakConcurrency(spans []span.Span) (int, error) {
	if len(spans) == 0 {
		return 0, nil
	}
	if err := sameFidelity(spans); err != nil {
		return 0, err
	}

	return peakFromEvents(spanEvents(spans)), nil
}

// peakFromEvents is PeakConcurrency's sweep over events already built and
// sorted by spanEvents. Stalls needs the same number over the same events --
// it is the capacity Stalls measures each window's running count against,
// and carries on every Stall it returns -- so the sweep lives here rather
// than being written out twice.
func peakFromEvents(events []spanEvent) int {
	var cur, peak int
	for _, e := range events {
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak
}

// Stall is a window during which fewer spans were running at once than the
// log's peak concurrency, named by a span that kept running while the
// others did not.
type Stall struct {
	StartMs, EndMs uint32
	// MinRunning and MaxRunning are the fewest and the most spans running
	// at any instant in the window: equal for a window that holds one depth
	// from end to end, and a range for one merged out of segments of
	// differing depth (see Stalls' merge rule).
	//
	// Both, because the floor alone overstates how bad the earlier part of
	// a merged window was -- a wait that ran two-up for twenty seconds and
	// one-up for twenty more is not forty seconds of running one-up. And a
	// range rather than a direction, because merging on contiguity admits a
	// window whose depth falls and rises again, so a consumer wording the
	// pair as a ramp would be claiming something the numbers do not say.
	MinRunning, MaxRunning int
	// Capacity is what those two are measured against: the peak concurrency
	// of the very spans this stall was swept from (see Stalls for why that,
	// and not a lane count the caller chose).
	//
	// It is carried here rather than left for the consumer to recompute,
	// because both halves of "M of N" are only a pair while they come from
	// one sweep of one slice. A consumer that measures N over some other
	// slice prints a running count that is too high, or negative, with
	// nothing in the output looking wrong.
	Capacity int
	// Blocking indexes the spans slice Stalls was given: the longest span
	// running at any point in this window, ties towards the lower index.
	//
	// It is -1 when NOTHING was running -- MinRunning and MaxRunning are
	// then both zero -- which is a wait with no span to blame rather than
	// an impossible value.
	// Consumers must handle it: indexing spans with -1 panics, and looking
	// -1 up in a lane finds none. See Stalls for why such windows are
	// reported rather than skipped.
	// StartMs settles only one direction of "was this before any span
	// ran". A window beginning after 0 begins at the instant some span
	// ended, so it always has a call behind it. A window beginning AT 0
	// begins at or before every span, since every span's StartMs is at or
	// after 0 -- but that does not make it a window no span ran in: a
	// zero-extent span occupies no instant and so is never counted as
	// running (see PeakConcurrency), which lets such a window merge
	// straight past a call that completed inside it. The UI-hook tier
	// produces that routinely, emitting a zero-extent span for every
	// resource Terraform reports as taking 0s. A consumer wanting to
	// explain core start-up separately from a mid-run collapse must
	// therefore ask whether any span STARTS before the window ends, which
	// it can derive from the same spans it passed in, rather than needing
	// a flag here that Stalls would have to keep in step.
	Blocking int
}

// outranksAsBlocking reports whether span a is the better name for a stall
// than span b: the longer span wins, and a tie resolves towards the lower
// span index so that repeated runs over the same log name the same span.
// -1 -- no span at all -- loses to anything and beats nothing.
//
// It is the single definition of that order, used both to rank spans for
// the sweep's per-window choice and to pick between the choices of the
// windows a merge joins together.
func outranksAsBlocking(spans []span.Span, a, b int) bool {
	switch {
	case a < 0:
		return false
	case b < 0:
		return true
	case spans[a].DurationMs != spans[b].DurationMs:
		return spans[a].DurationMs > spans[b].DurationMs
	}
	return a < b
}

// Stalls sweeps the same start/end events PeakConcurrency does, but keeps
// the running set rather than just its size, so each window can be named by
// the span still running while the others are not.
//
// The capacity a window's running count is measured against, and which
// every returned Stall carries, is the spans' own peak concurrency. Stalls
// takes no lane count from the caller, because there is
// no lane count a correct caller could pass that would change the answer: a
// lane holds one span at a time, so however a caller chooses to pack these
// spans into rows it needs at least as many rows as the deepest overlap,
// and a ceiling at or above the peak never bites. Deriving the capacity
// here instead also keeps the number honest where a lane count would not
// be. PackLanes gives a zero-extent span a lane of its own so the timeline
// has a row to draw it in (see PeakConcurrency), and that lane can hold
// work in no window at all: counted as capacity it reports the entire run
// as idle off the back of one instantaneous span, which the UI-hook tier
// emits by the dozen.
//
// The consequence a caller must word its output for: Capacity is peak
// concurrency and not a count of rows, so it can be smaller than the number
// of rows the caller draws -- a caller packing each provider's spans
// separately opens a row for a provider whose calls never overlap anything,
// and that row is real but is not capacity the log ever used at once.
//
// minMs is the caller's own noise threshold; Stalls does not invent it.
// Windows are merged before minMs is applied, because a
// stall split by a handover is still one stall: thresholding first drops
// both halves of a genuine wait that only looks short in pieces.
//
// Merging is on contiguity: windows that abut are one wait, whatever the
// depth or the blocking span does inside it. Two ordinary shapes force
// that. The blocking span is by definition the one that keeps starting new
// work, so a long wait is routinely split by handovers WITHIN it -- one
// 16.7s wait behind four consecutive calls from the same provider is four
// windows naming four different spans. And a run draining off one provider
// at a time steps the depth down with nothing starting in between: three
// spans ending 20s apart are 40s of continuous waiting, which a rule keyed
// on the running count reports as two separate waits of 20s.
// Either split understates the wait it was supposed to measure, since each
// fragment is thresholded against minMs on its own and the shortest
// vanish, and it spends a caller's whole annotation on one ramp-down.
//
// A merged window reports the range of depths it covered, MinRunning to
// MaxRunning, and is named by the longest span that blocked any part of it
// (see outranksAsBlocking), which is the same answer as scanning the merged
// window whole would give: the longest span running anywhere in it also
// wins the window it runs in.
//
// A window where NOTHING is running is reported too, with Blocking -1.
// Being unable to name a blocker is not the same as there being no wait:
// Terraform core working with no provider call in flight is the commonest
// shape of a slow plan, and it is drawn as blank space across every lane.
// An annotation silent about it contradicts the picture it sits under.
//
// The sign of Blocking is the one boundary a merge never crosses, however
// closely the two windows abut. "Nothing running" and "waiting on the span
// still going" are different findings with different causes, and a window
// merged across that line would name a span as blocking a stretch that
// began after it had already finished -- which is the reader-facing failure
// merging exists to remove, recreated one level up.
func Stalls(spans []span.Span, minMs uint32) ([]Stall, error) {
	if len(spans) == 0 {
		return nil, nil
	}
	if err := sameFidelity(spans); err != nil {
		return nil, err
	}

	events := spanEvents(spans)
	capacity := peakFromEvents(events)

	// order lists spans best-blocker first and rank is its inverse, so the
	// blocking span of a window is the lowest rank still running: running
	// carries that set as one bit per rank, and the answer is its lowest
	// set bit. The obvious alternative -- rescanning every span for the
	// longest one still open, once per window -- is quadratic in the span
	// count, and a capture opens a window per distinct event instant while
	// the timeline asks for the whole answer again on every frame.
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return outranksAsBlocking(spans, order[a], order[b]) })
	rank := make([]int, len(spans))
	for r, idx := range order {
		rank[idx] = r
	}
	running := make([]uint64, (len(spans)+63)/64)

	var raw []Stall
	// The window before the first event is elapsed capture time with
	// nothing running: a span's StartMs is an offset from the log's own
	// zero point, not from its first span, so this is measured time, not
	// invented. It is the same wait as any other all-idle window and is
	// reported as one. There is no matching window after the last event:
	// Stalls is handed no end-of-log timestamp, so the last event it saw is
	// the last thing it knows happened, and the sweep never invents a
	// segment past that.
	if first := events[0].at; first > 0 && capacity > 0 {
		raw = append(raw, Stall{StartMs: 0, EndMs: first, MinRunning: 0, MaxRunning: 0, Capacity: capacity, Blocking: -1})
	}

	// open records each span's liveness as the net of its start and end
	// deltas rather than as a bool, so a
	// zero-duration span -- whose start and end land in the same sweep
	// batch -- nets to 0 and reads as not running regardless of which of
	// its two events the tie-break in spanEvents happens to order first. A
	// bool keyed by last-write-wins got this wrong: it left such a span
	// marked running for the rest of the sweep, so an instant with no
	// extent could win Blocking from a span that was genuinely still going.
	open := make([]int, len(spans))
	var runningCount int
	for i := 0; i < len(events); {
		at := events[i].at
		for i < len(events) && events[i].at == at {
			e := events[i]
			open[e.idx] += e.delta
			runningCount += e.delta
			// Driven from open's net count, and so inheriting its
			// zero-duration behaviour: the bit is only ever read once the
			// whole batch at this instant has been applied.
			if open[e.idx] > 0 {
				running[rank[e.idx]/64] |= 1 << (rank[e.idx] % 64)
			} else {
				running[rank[e.idx]/64] &^= 1 << (rank[e.idx] % 64)
			}
			i++
		}
		if i == len(events) {
			// No further event, so no window follows: the sweep never
			// invents a segment past the last thing it observed.
			break
		}
		next := events[i].at
		if runningCount >= capacity {
			// As many spans are in flight as ever were at once, so there is
			// nothing here that was waiting.
			continue
		}
		// One segment holds one depth throughout, so it opens with its
		// range closed; only a merge widens it.
		raw = append(raw, Stall{
			StartMs: at, EndMs: next,
			MinRunning: runningCount, MaxRunning: runningCount,
			Capacity: capacity,
			Blocking: lowestRankRunning(order, running),
		})
	}

	// Contiguous windows of the same KIND are one wait, and the sign of
	// Blocking is the kind: within a kind both the depth and the blocking
	// span are free to change, across it nothing may merge at all (see the
	// doc comment above). The range widens to cover every segment taken in,
	// and the name is re-derived over them rather than kept from whichever
	// segment opened the window.
	var merged []Stall
	for _, s := range raw {
		if n := len(merged); n > 0 && merged[n-1].EndMs == s.StartMs && (merged[n-1].Blocking < 0) == (s.Blocking < 0) {
			merged[n-1].EndMs = s.EndMs
			merged[n-1].MinRunning = min(merged[n-1].MinRunning, s.MinRunning)
			merged[n-1].MaxRunning = max(merged[n-1].MaxRunning, s.MaxRunning)
			if outranksAsBlocking(spans, s.Blocking, merged[n-1].Blocking) {
				merged[n-1].Blocking = s.Blocking
			}
			continue
		}
		merged = append(merged, s)
	}

	var stalls []Stall
	for _, s := range merged {
		if s.EndMs-s.StartMs >= minMs {
			stalls = append(stalls, s)
		}
	}
	return stalls, nil
}

// lowestRankRunning returns the span index of the best-ranked span in the
// running set -- the lowest set bit, since order ranks the best blocker
// first -- or -1 when nothing is running at all, which Stall.Blocking
// documents as its own answer rather than a missing one.
func lowestRankRunning(order []int, running []uint64) int {
	for w, word := range running {
		if word != 0 {
			return order[w*64+bits.TrailingZeros64(word)]
		}
	}
	return -1
}

// BusyMs is how much of the run had ANY span running: the total length of
// the union of the spans' [StartMs, EndMs) intervals. Against the same
// wall-clock window PeakConcurrency's callers scale their axes to, it
// answers the question a stall list structurally cannot -- whether an
// eight-minute plan was eight minutes of work or ninety seconds of work and
// six minutes of waiting -- for a log whose idle time is spread thinly
// rather than gathered into windows long enough to clear a stall threshold.
// Rate-limited polling is the ordinary shape that produces one, and it is
// the threshold alone that silences the stall list over it, not any absence
// of stalls: sixty forty-millisecond calls three seconds apart
// (testdata/timeline-dense-lane.log) drop the concurrency to zero in every
// one of the fifty-nine gaps between them, and Stalls opens an all-idle
// window for each. Every one of those windows is 2.96s long, against the
// 9s a caller scaling its threshold to that 180s window asks for, so all
// fifty-nine are dropped -- correctly, since a three-second gap is not what
// a reader means by a stall -- while only 1.3% of the run had a call in
// flight at all.
//
// It is a UNION, not a sum of durations. Two spans running [0,10) and
// [5,15) cover fifteen milliseconds between them, not the twenty their
// durations add to: the five they share is one stretch of busy wall clock,
// and summing would report more work than the window has room for. That is
// also why this cannot be derived from the rollups, which sum.
//
// A zero-duration span contributes nothing, the empty interval [t, t)
// containing no millisecond -- the same answer PeakConcurrency gives one,
// and for the same reason. It also cannot interrupt a span that genuinely
// is running: the sweep applies every event at one instant before measuring
// the stretch that follows, so an instantaneous span nested inside a long
// one nets to zero and leaves the long one running, the netting Stalls'
// own open counter documents.
//
// It rejects a mixed-fidelity slice for the same reason PackLanes does: see
// sameFidelity.
func BusyMs(spans []span.Span) (uint32, error) {
	if len(spans) == 0 {
		return 0, nil
	}
	if err := sameFidelity(spans); err != nil {
		return 0, err
	}

	events := spanEvents(spans)
	var busy uint32
	var running int
	for i := 0; i < len(events); {
		at := events[i].at
		for i < len(events) && events[i].at == at {
			running += events[i].delta
			i++
		}
		if i == len(events) {
			// Nothing is running after the last event -- every span that
			// opened has closed -- so there is no stretch left to measure.
			break
		}
		if running > 0 {
			busy += events[i].at - at
		}
	}
	return busy, nil
}
