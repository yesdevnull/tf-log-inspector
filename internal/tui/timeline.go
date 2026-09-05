package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// timelineTier is which of the log's two span sets the timeline draws.
// model.PackLanes refuses a slice mixing fidelities (see the doc comment on
// span.Span's StartMs/EndMs), so the timeline can only ever draw one of
// them.
type timelineTier uint8

const (
	tierNone timelineTier = iota
	tierRPC
	tierUI
)

// timelineSpans reports which tier the timeline draws under the active
// filter, and the filtered spans of that tier.
//
// The tier is decided from the log's WHOLE RPC span set, before filtering:
// a facet selection that happens to hide every RPC span must still draw an
// empty RPC timeline rather than silently swapping in the UI tier, which
// would redraw the whole pane under the user and change what its numbers
// mean without saying so. The project owner's chosen fallback -- RPC where
// the log has one, otherwise UI -- is therefore a property of the LOG, not
// of the current selection.
func (m *Model) timelineSpans() (timelineTier, []span.Span) {
	if len(m.log.RPCSpans) == 0 {
		return tierUI, m.uiFilter().SpansMatching(m.log.UISpans)
	}
	return tierRPC, m.filter().SpansMatching(m.log.RPCSpans)
}

// laneBar renders one lane's spans as a bar row barW columns wide covering
// [0, spanMs). Each span occupies the columns its interval maps to, with a
// minimum of one column so a short span is visible rather than rounded
// away.
//
// Two spans that map to the same column merely draw over each other: the
// column is already lit and marking it again changes nothing, so no
// separate overlap bookkeeping is needed. A lane with more spans than barW
// has columns -- a busy lane packed onto a narrow pane -- degrades the same
// way: several spans compress into one lit column. That understates how
// many calls ran there, but PackLanes already guarantees the spans in one
// lane never overlap in time, so it is compression of adjacent calls into
// one visible mark, not a false claim that unrelated calls ran together.
func laneBar(spans []span.Span, lane model.Lane, spanMs uint32, barW int) string {
	if barW <= 0 {
		return ""
	}
	cols := make([]bool, barW)
	for _, idx := range lane.Spans {
		s := spans[idx]
		// start is clamped into range before end is derived from it, so a
		// span whose start lands beyond the window (which laneCol cannot
		// itself rule out -- see its own comment) still gets its one
		// column drawn below rather than being silently dropped: the
		// minimum-one-column bump adds to whatever start ends up being,
		// so an unclamped out-of-range start would open a range beyond
		// barW that the barW cap below then closes back down to empty.
		start := min(laneCol(s.StartMs, spanMs, barW), barW-1)
		// end ceils rather than floors: a span's last millisecond can fall
		// anywhere inside its final column, not just on that column's
		// boundary, and a column the span is still running through must
		// count as occupied. Flooring the end the way start floors would
		// silently render that column idle -- exactly the failure mode
		// this view exists to avoid, only inverted: busy time drawn as
		// waiting rather than waiting drawn as busy.
		end := laneEndCol(s.EndMs, spanMs, barW)
		if end <= start {
			end = start + 1
		}
		if end > barW {
			end = barW
		}
		for c := start; c < end; c++ {
			cols[c] = true
		}
	}

	var b strings.Builder
	for _, filled := range cols {
		if filled {
			b.WriteRune('█')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// laneCol maps a millisecond instant to the column it falls in, scaling
// linearly across barW columns over a window of spanMs and flooring: an
// instant belongs to whichever column its own millisecond falls inside.
// laneBar uses it only for a span's START, which is exactly what floor
// answers -- the column the span begins occupying. spanMs of 0 means every
// span in the lane is zero-duration, so there is no time axis to scale
// against -- every instant maps to column 0 rather than dividing by zero,
// and laneBar's minimum-one-column rule then lights that column instead of
// leaving the row blank.
//
// The result is capped at barW as a defensive bound against a ms beyond
// spanMs, which PackLanes building every span from the same slice this
// window is measured over should rule out but this function cannot verify
// on its own.
func laneCol(ms, spanMs uint32, barW int) int {
	if spanMs == 0 {
		return 0
	}
	col := int(uint64(ms) * uint64(barW) / uint64(spanMs))
	return min(col, barW)
}

// laneEndCol maps a millisecond instant to the EXCLUSIVE end column of the
// span it closes, ceiling rather than flooring: a span still running
// through any part of a column -- even its last millisecond -- must count
// that column as occupied, so the boundary is rounded away from the span's
// start rather than towards it. Flooring here the way laneCol floors a
// start would silently drop the column a span's end merely touches, which
// draws work that happened as idle time it did not spend.
//
// The ceiling-division idiom (numerator + denominator - 1) / denominator
// avoids floating point, matching the rest of this package's integer
// column arithmetic. spanMs of 0 shares laneCol's zero-window handling: no
// time axis to scale against, so every instant maps to column 0 and the
// caller's minimum-one-column bump takes it from there.
func laneEndCol(ms, spanMs uint32, barW int) int {
	if spanMs == 0 {
		return 0
	}
	col := int((uint64(ms)*uint64(barW) + uint64(spanMs) - 1) / uint64(spanMs))
	return min(col, barW)
}

// timeAxis renders the axis line beneath the lanes: 0s at the left, the
// total at the right, formatted the same way formatMs renders every other
// duration in this package rather than a second formatter for the same
// number.
func timeAxis(spanMs uint32, barW int) string {
	if barW <= 0 {
		return ""
	}
	left := "0s"
	right := formatMs(uint64(spanMs))

	gap := max(barW-lipgloss.Width(left)-lipgloss.Width(right), 0)
	axis := left + strings.Repeat(" ", gap) + right

	// A pane too narrow to hold both labels truncates from the right: "0s"
	// anchors the axis under the lane bars' own left edge, and the row
	// beneath it is already unreadable at that width regardless of which
	// label survives, so keeping the left one is no more than a tie-break.
	if w := lipgloss.Width(axis); w > barW {
		axis = clipWidth(axis, barW)
	} else if w < barW {
		axis += strings.Repeat(" ", barW-w)
	}
	return axis
}
