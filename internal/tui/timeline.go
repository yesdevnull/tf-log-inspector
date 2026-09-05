package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// timelineState is the timeline view's own state, kept as its own struct as
// rawLogState is, so the view's concerns stay grouped with the file that
// owns them: which lane the cursor is on, and which of that lane's spans is
// selected within it.
type timelineState struct {
	lane int
	span int // index into the selected lane's Spans, not into the span slice
}

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
//
// tierNone is reported only when the log carries neither tier at all: a
// filter narrowing a tier that DOES exist down to nothing is reported as
// tierRPC or tierUI with zero spans, which renderTimeline reads as "the
// filter hid them" rather than "this log was never captured with timing".
// Those are different states for a reader to act on -- one clears with Esc,
// the other needs a different capture -- and collapsing them into the same
// tierUI-with-nothing-in-it result would make tierNone unreachable and the
// two states indistinguishable on screen.
func (m *Model) timelineSpans() (timelineTier, []span.Span) {
	if len(m.log.RPCSpans) == 0 && len(m.log.UISpans) == 0 {
		return tierNone, nil
	}
	if len(m.log.RPCSpans) == 0 {
		return tierUI, m.uiFilter().SpansMatching(m.log.UISpans)
	}
	return tierRPC, m.filter().SpansMatching(m.log.RPCSpans)
}

// timelineTitle names the pane after the tier timelineSpans has chosen for
// the log. The UI tier is stated explicitly as whole-second resolution --
// Terraform's UI hooks round a resource's start and end to the nearest
// second before the log ever sees them -- so a reader does not carry RPC's
// millisecond precision over to bars that do not have it.
func (m *Model) timelineTitle() string {
	switch tier, _ := m.timelineSpans(); tier {
	case tierRPC:
		return "TIMELINE (rpc)"
	case tierUI:
		return "TIMELINE (ui, whole seconds)"
	default:
		return "TIMELINE"
	}
}

// noTimedSpansNote is what the timeline shows for a log carrying neither
// span tier: what is missing, and what to capture to get it. It names the
// same two environment variables writeRPCCaptureHint's gates require,
// because a reader who only sees a bare "no spans" here has no next step
// beyond guessing.
const noTimedSpansNote = "no timed spans in this log; RPC timings need TF_LOG_SDK_PROTO=TRACE and TF_LOG_PROVIDER=TRACE"

// timelineWallClockMs is the total window the timeline's axis and lane bars
// scale against: the latest EndMs among spans, matching
// internal/profile.writeConcurrency's own wallClock measurement of the same
// idea. It is computed over whatever spans the caller hands it -- the
// active filter's own subset, not the tier's whole span set -- so a
// narrowed filter re-scales the axis to what it actually shows rather than
// leaving idle space sized for spans no longer on screen.
func timelineWallClockMs(spans []span.Span) uint32 {
	var wallClock uint32
	for _, s := range spans {
		if s.EndMs > wallClock {
			wallClock = s.EndMs
		}
	}
	return wallClock
}

// timelineLanes packs the timeline's current tier and active filter's spans
// into lanes, the same packing renderTimeline draws and the lane/span cursor
// (see timelineState) moves over. It is the one place PackLanes is called
// for the timeline, so the renderer and the cursor cannot disagree about how
// many lanes there are or which spans sit in which.
func (m *Model) timelineLanes() []model.Lane {
	_, spans := m.timelineSpans()
	lanes, err := model.PackLanes(spans)
	if err != nil {
		// timelineSpans hands PackLanes spans of a single tier by
		// construction, so ErrMixedTimelines reaching here means that
		// guarantee broke somewhere upstream -- a programming error to fail
		// loudly on, the same treatment unhandledView gives its own
		// can't-happen case, rather than a blank pane that looks like this
		// view was never built.
		panic(fmt.Sprintf("tui: timelineLanes: %v", err))
	}
	return lanes
}

// clampTimelineSelection brings the lane and within-lane span cursors back
// inside the current tier and filter's lanes: the lane cursor to the last
// lane when it sits past the end (or 0 for no lanes at all, the same
// past-then-back-to-zero shape clampSelection uses for an empty row list),
// and the span cursor to the last span of whichever lane that leaves it on.
//
// Both are clamped from the one place because changing lanes changes what
// the span cursor is clamped against -- a lane's own span count has nothing
// to do with the one the cursor just left -- and because a filter change can
// shrink or empty the very lane the cursor was on, which invalidateRows must
// catch the same way it catches a row selection run off the end of a
// shortened table.
func (m *Model) clampTimelineSelection() {
	lanes := m.timelineLanes()
	if last := len(lanes) - 1; m.timeline.lane > last {
		m.timeline.lane = last
	}
	if m.timeline.lane < 0 {
		m.timeline.lane = 0
	}
	spanCount := 0
	if m.timeline.lane < len(lanes) {
		spanCount = len(lanes[m.timeline.lane].Spans)
	}
	if last := spanCount - 1; m.timeline.span > last {
		m.timeline.span = last
	}
	if m.timeline.span < 0 {
		m.timeline.span = 0
	}
}

// moveTimelineLane shifts the lane cursor by delta, clamping it to the
// current lane range and the within-lane span cursor to whichever lane that
// leaves it on.
func (m *Model) moveTimelineLane(delta int) {
	m.timeline.lane += delta
	m.clampTimelineSelection()
}

// moveTimelineSpan shifts the within-lane span cursor by delta, clamping it
// to the selected lane's own span range.
func (m *Model) moveTimelineSpan(delta int) {
	m.timeline.span += delta
	m.clampTimelineSelection()
}

// selectedTimelineSpan resolves the timeline's cursor to one span and its
// index in the tier's span slice, and reports whether there is one -- false
// for a lane cursor or span cursor outside the current lanes, which is where
// both sit when a filter has emptied the timeline (see
// clampTimelineSelection).
func (m *Model) selectedTimelineSpan() (idx int, ok bool) {
	lanes := m.timelineLanes()
	if m.timeline.lane < 0 || m.timeline.lane >= len(lanes) {
		return 0, false
	}
	spans := lanes[m.timeline.lane].Spans
	if m.timeline.span < 0 || m.timeline.span >= len(spans) {
		return 0, false
	}
	return spans[m.timeline.span], true
}

// selectedTimelineSpanValue resolves the timeline's cursor to the actual
// span.Span it names. selectedTimelineSpan itself reports only the index
// into the tier's span slice, which is what jumpTarget needs to hand
// jumpToSpan; the detail pane (selectedDetail) wants the span's own fields
// instead, the way spanForRow does for a table row.
func (m *Model) selectedTimelineSpanValue() (span.Span, bool) {
	idx, ok := m.selectedTimelineSpan()
	if !ok {
		return span.Span{}, false
	}
	_, spans := m.timelineSpans()
	return spans[idx], true
}

// mixedProviderLabel is a lane's label when its packed spans come from more
// than one provider (see laneLabel).
const mixedProviderLabel = "mixed"

// laneLabel is a lane's left-hand identifier: the provider its spans belong
// to and its one-based position among the lanes drawn, e.g. "aws/2" --
// one-based to match how a reader counts rows by eye rather than the
// zero-based index PackLanes returns.
//
// PackLanes packs purely by timing, with no notion of provider, so two
// different providers' spans can land in one lane whenever their intervals
// happen not to overlap. Naming such a lane after only its first span's
// provider would misattribute every OTHER span in it to a provider it is
// not from, so laneLabel reports "mixed" instead: a fixed-width, honest
// answer over a guess that is wrong as often as it is right.
func laneLabel(spans []span.Span, lane model.Lane, n int) string {
	provider := laneLabelProvider(spans[lane.Spans[0]].Provider)
	for _, idx := range lane.Spans[1:] {
		if laneLabelProvider(spans[idx].Provider) != provider {
			provider = mixedProviderLabel
			break
		}
	}
	return fmt.Sprintf("%s/%d", provider, n)
}

// laneLabelProvider shortens a span's provider to the identifier laneLabel
// shows: the last "/"-separated segment, which is the provider's short type
// name ("aws") whichever tier the span comes from -- the RPC tier's Provider
// is the full registry address ("registry.terraform.io/hashicorp/aws") and
// the UI tier's is already that short name with no "/" to split on, so one
// rule serves both. An empty provider is normalised through model.FacetKey
// to the same "(none)" the facet pane and the rollups already use for one,
// rather than a bare label that would render as little more than "/N".
func laneLabelProvider(provider string) string {
	provider = model.FacetKey(provider)
	if slash := strings.LastIndex(provider, "/"); slash >= 0 {
		return provider[slash+1:]
	}
	return provider
}

// maxLaneLabelWidth caps how much of the pane's width the label column may
// claim, so a lane holding a long provider name -- or "mixed" -- cannot
// squeeze the bar area, the point of this view, down to nothing. Provider
// names are short, closed-vocabulary slugs ("aws", "google", "kubernetes"),
// so this is a defensive ceiling rather than a width real logs are expected
// to reach.
const maxLaneLabelWidth = 12

// laneLabelWidth is the label column's width for one render: wide enough for
// the widest of labels, capped at maxLaneLabelWidth. It is computed over
// EVERY lane, not just the ones a short pane currently has room to draw, so
// scrolling the lane cursor through a log with lanes numbered into double or
// triple digits does not shift the column width -- and so the bar area --
// out from under the rows already on screen.
func laneLabelWidth(labels []string) int {
	w := 0
	for _, l := range labels {
		w = max(w, lipgloss.Width(l))
	}
	return min(w, maxLaneLabelWidth)
}

// renderTimeline renders the timeline view's centre-pane content: one
// labelled lane bar per lane PackLanes packs the active tier's spans into
// (see timelineLanes), followed by the time axis.
//
// A log with neither span tier gets capture guidance in place of any bars
// (noTimedSpansNote); a tier that exists but whose filter hides every span
// gets the same "nothing matches" note the table views give (noMatchNote) --
// deliberately the SAME note, so a reader who has seen it in another view
// recognises it here rather than learning a second phrasing for one
// situation. Those are the two states renderList's own empty handling
// separates for the same reason; see timelineSpans for why they can never
// collapse into each other.
//
// Each lane's row is its label (laneLabel), padded to the widest label
// drawn, then its bar over whatever width is left of w -- laneBar is tested
// at exact widths and does not itself know about the label, so this is the
// one place that width is divided between the two. The selected lane is
// drawn as the cursor bar (see cursorBar), the same reverse-video treatment
// the table views give their selected row, styled or dimmed by whether the
// list pane has focus; the within-lane span cursor has no glyph of its own
// in the bar -- a packed lane already draws several spans as one lit column
// (see laneBar), so a marker on individual columns would often point at
// spans it cannot tell apart -- and is instead named in the detail pane
// beside it.
//
// The lane rows are windowed around the lane cursor by the same
// scrollWindow the centre table uses for its own row cursor, so a log with
// more lanes than the pane is tall keeps the selected one on screen instead
// of always showing the first screenful. The axis is always the LAST line,
// indented under the bar area by the same label width the lane rows reserve
// so it still names both ends of what the bars above it are measuring
// against, and is never dropped for want of height -- it is the one thing
// every lane bar is drawn relative to.
func (m *Model) renderTimeline(w, h int) string {
	if h <= 0 {
		return ""
	}
	tier, spans := m.timelineSpans()
	if tier == tierNone {
		return clipWidth(noTimedSpansNote, w)
	}
	if len(spans) == 0 {
		return clipWidth(noMatchNote, w)
	}

	lanes := m.timelineLanes()
	wallClock := timelineWallClockMs(spans)

	labels := make([]string, len(lanes))
	for i, lane := range lanes {
		labels[i] = laneLabel(spans, lane, i+1)
	}
	labelW := laneLabelWidth(labels)
	barW := max(w-labelW-1, 0)

	top, visible := scrollWindow(m.timeline.lane, len(lanes), h-1)
	lines := make([]string, 0, visible+1)
	for i := top; i < top+visible; i++ {
		line := padRight(clipValueForKind(labels[i], labelW, tailIdentifierColumn), labelW) + " " + laneBar(spans, lanes[i], wallClock, barW)
		line = clipWidth(line, w)
		if i == m.timeline.lane {
			line = cursorBar(line, w, m.pane == PaneList)
		}
		lines = append(lines, line)
	}
	lines = append(lines, clipWidth(strings.Repeat(" ", labelW+1)+timeAxis(wallClock, barW), w))
	return strings.Join(lines, "\n")
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
