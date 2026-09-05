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
		// column inside the row rather than the minimum-one-column bump
		// pushing end past barW with nothing left to draw.
		start := min(laneCol(s.StartMs, spanMs, barW), barW-1)
		end := laneCol(s.EndMs, spanMs, barW)
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
// linearly across barW columns over a window of spanMs. spanMs of 0 means
// every span in the lane is zero-duration, so there is no time axis to
// scale against -- every instant maps to column 0 rather than dividing by
// zero, and laneBar's minimum-one-column rule then lights that column
// instead of leaving the row blank.
//
// The result is capped at barW rather than barW-1: callers use it for both
// a span's start (which they clamp themselves, since a start belongs to a
// specific column) and its end (an exclusive bound, for which barW is the
// correct value when the span runs to the edge of the window). PackLanes
// builds every span from the same slice this window is measured over, so ms
// should never exceed spanMs in practice, but the cap keeps a stray
// out-of-range value from producing a column index laneBar cannot index
// with.
func laneCol(ms, spanMs uint32, barW int) int {
	if spanMs == 0 {
		return 0
	}
	col := int(uint64(ms) * uint64(barW) / uint64(spanMs))
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
