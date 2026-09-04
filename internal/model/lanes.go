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

// ErrMixedTimelines is returned when PackLanes is handed spans from more than
// one builder. See the doc comment on span.Span: the builders anchor
// StartMs/EndMs to different zero points, so packing a mixed slice
// interleaves two unrelated timelines into lanes that look plausible and mean
// nothing. Refusing is the only safe behaviour, because there is no signal in
// the output that would let a reader notice.
var ErrMixedTimelines = errors.New("model: cannot pack lanes across spans of different fidelity")

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
	for _, s := range spans[1:] {
		if s.Fidelity != spans[0].Fidelity {
			return nil, ErrMixedTimelines
		}
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
// Events at the same instant are ordered by priority, not just by delta: a
// genuine handover -- one span's end coinciding with a different span's
// start -- must retire before the new start is counted (peakEndPriority
// before peakStartPriority), or two adjacent, non-overlapping spans would
// double-count that instant. But a zero-duration span's own end coincides
// with its own start at the same instant, and if that end retired first --
// as an ordinary end always should -- the pair would always net to zero and
// the span would never register as concurrent with anything, even sitting
// squarely inside a window where other spans are running. So a zero-duration
// span's end is given its own, lowest priority: it retires only after every
// start at that instant, including its own, has been counted.
func PeakConcurrency(spans []span.Span) int {
	const (
		peakEndPriority     = 0 // end of a span whose StartMs != EndMs: a genuine handover
		peakStartPriority   = 1
		peakZeroEndPriority = 2 // end of a span whose StartMs == EndMs: retire only after counted
	)
	type event struct {
		at       uint32
		priority int
		delta    int
	}
	events := make([]event, 0, len(spans)*2)
	for _, s := range spans {
		endPriority := peakEndPriority
		if s.StartMs == s.EndMs {
			endPriority = peakZeroEndPriority
		}
		events = append(events,
			event{at: s.StartMs, priority: peakStartPriority, delta: 1},
			event{at: s.EndMs, priority: endPriority, delta: -1})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		return events[i].priority < events[j].priority
	})
	var cur, peak int
	for _, e := range events {
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak
}
