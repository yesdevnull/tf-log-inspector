package model

import (
	"errors"
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
// A zero-duration span is a synthetic edge case; the degenerate case that
// actually turns up in real logs is StartClamped: a span whose reported
// duration exceeds its offset from the log's first entry has its start
// clamped to zero, which collapses its timeline extent to zero even though
// its DurationMs is not, so it too overlaps nothing here.
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

	type event struct {
		at    uint32
		delta int
	}
	events := make([]event, 0, len(spans)*2)
	for _, s := range spans {
		events = append(events, event{s.StartMs, 1}, event{s.EndMs, -1})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		// Ends before starts at the same instant: a span ending exactly as
		// another begins is a handover, not overlap.
		return events[i].delta < events[j].delta
	})
	var cur, peak int
	for _, e := range events {
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak, nil
}

// Stall is a window during which fewer lanes were busy than the timeline
// has, named by the span that was still running when the others were not.
type Stall struct {
	StartMs, EndMs uint32
	Idle           int // lanes with nothing to do in this window
	Blocking       int // index into the spans slice: the longest span running throughout
}

// Stalls sweeps the same start/end events PeakConcurrency does, but keeps
// the running set rather than just its size, so each window can be named by
// the span still running while the others are not.
//
// lanes is the caller's lane count -- normally len(PackLanes(spans)) -- and
// minMs is the caller's own noise threshold; Stalls invents neither. It
// merges adjacent windows before applying minMs, because a stall split by a
// handover between two other spans is still one stall: thresholding first
// would drop both halves of a genuine wait that only looks short in pieces.
func Stalls(spans []span.Span, lanes int, minMs uint32) ([]Stall, error) {
	if len(spans) == 0 {
		return nil, nil
	}
	if err := sameFidelity(spans); err != nil {
		return nil, err
	}

	type event struct {
		at    uint32
		delta int
		idx   int
	}
	events := make([]event, 0, len(spans)*2)
	for i, s := range spans {
		events = append(events, event{s.StartMs, 1, i}, event{s.EndMs, -1, i})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		// Ends before starts at the same instant, as PeakConcurrency does:
		// a span ending exactly as another begins is a handover, not overlap.
		return events[i].delta < events[j].delta
	})

	var raw []Stall
	// running is indexed by span, not a map, so a tie in DurationMs breaks
	// towards the lowest span index deterministically rather than however
	// Go's map iteration happens to land.
	running := make([]bool, len(spans))
	var runningCount int
	for i := 0; i < len(events); {
		at := events[i].at
		for i < len(events) && events[i].at == at {
			if events[i].delta > 0 {
				running[events[i].idx] = true
				runningCount++
			} else {
				running[events[i].idx] = false
				runningCount--
			}
			i++
		}
		if i == len(events) {
			// No further event, so no window follows: the sweep never
			// invents a segment past the last thing it observed.
			break
		}
		next := events[i].at
		idle := lanes - runningCount
		if idle <= 0 || runningCount == 0 {
			// idle <= 0: every lane is busy. runningCount == 0: nothing is
			// running at all, so this is the gap between two phases of
			// work, not a stall -- reporting it would blame a span that had
			// already finished for a wait it played no part in.
			continue
		}
		blocking := -1
		for idx, live := range running {
			if live && (blocking == -1 || spans[idx].DurationMs > spans[blocking].DurationMs) {
				blocking = idx
			}
		}
		raw = append(raw, Stall{StartMs: at, EndMs: next, Idle: idle, Blocking: blocking})
	}

	var merged []Stall
	for _, s := range raw {
		if n := len(merged); n > 0 && merged[n-1].EndMs == s.StartMs && merged[n-1].Idle == s.Idle && merged[n-1].Blocking == s.Blocking {
			merged[n-1].EndMs = s.EndMs
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
