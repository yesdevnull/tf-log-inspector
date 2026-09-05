package tui

import (
	"fmt"
	"maps"
	"slices"
	"sort"
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
//
// The result is cached on m (see timelineSpansCache), alongside the lanes
// packed from it, because a single frame asks for it five times over and
// each answer runs the filter over the whole tier into a fresh
// full-capacity slice. Every caller must reach this through a POINTER, or
// it fills a cache on a copy that is immediately discarded -- the hazard
// rowsCache's own doc comment describes.
func (m *Model) timelineSpans() (timelineTier, []span.Span) {
	if !m.timelineSpansCached {
		m.timelineTierCache, m.timelineSpansCache = m.filteredTimelineSpans()
		m.timelineSpansCached = true
	}
	return m.timelineTierCache, m.timelineSpansCache
}

// filteredTimelineSpans is timelineSpans' answer built from scratch. It is
// separate only so the cache above it is one branch rather than three
// returns each having to remember to fill it.
func (m *Model) filteredTimelineSpans() (timelineTier, []span.Span) {
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

// timelineWallClockMs is the total window the timeline's axis and lane bars
// scale against: the latest EndMs among spans, matching
// internal/profile.writeConcurrency's own wallClock measurement of the same
// idea. It is computed over whatever spans the caller hands it -- the
// active filter's own subset, not the tier's whole span set -- so a
// narrowed filter re-scales the axis's RIGHT end to the last span it still
// shows rather than leaving idle space sized for spans no longer on screen.
//
// The left end stays pinned at 0 either way: a span's StartMs is an offset
// from the log's own zero point, and re-basing the axis on the earliest
// SURVIVING span would redraw those offsets as something they are not. The
// visible cost is that a filter keeping only spans starting late in the plan
// packs their bars against the right of a mostly blank pane -- which is an
// honest picture of when that work ran, not a scaling fault.
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
// into lanes (see packLanesByProvider), the same packing renderTimeline
// draws and the lane/span cursor (see timelineState) moves over. It is the
// one place the timeline's lanes are packed, so the renderer and the cursor
// cannot disagree about how many lanes there are or which spans sit in
// which.
//
// The result is cached on m (see timelineLanesCache), the same reason
// rowsCache exists: one keystroke's render/Update cycle calls this from
// renderTimeline, selectedRowOpens (via jumpTarget), selectedDetail (via
// selectedTimelineSpanValue) and clampTimelineSelection, and each packing
// sorts every span it is handed. Every caller must reach this through a
// pointer, or it fills a cache on a copy that is immediately discarded, the
// same hazard rowsCache's own doc comment describes.
//
// INVARIANT: a model.Lane holds INDICES into the slice timelineSpans()
// returns, so the two must always be the same generation. That pairing is
// structural rather than incidental -- both are cached on m, filled and
// dropped together by the same invalidateRows -- so a caller cannot hold a
// cached lane against a freshly rebuilt span slice. The failure it rules
// out is silent: a lane read against spans built from different filter
// state indexes the wrong spans, and the detail pane would describe one
// call while Enter jumped to another with nothing on screen saying so.
// Anything that changes what timelineSpans() returns must still go through
// invalidateRows, which is what keeps both halves in step.
func (m *Model) timelineLanes() []model.Lane {
	if m.timelineLanesCached {
		return m.timelineLanesCache
	}
	_, spans := m.timelineSpans()
	lanes, err := packLanesByProvider(spans)
	if err != nil {
		// timelineSpans hands PackLanes spans of a single tier by
		// construction, so ErrMixedTimelines reaching here means that
		// guarantee broke somewhere upstream -- a programming error to fail
		// loudly on, the same treatment unhandledView gives its own
		// can't-happen case, rather than a blank pane that looks like this
		// view was never built.
		panic(fmt.Sprintf("tui: timelineLanes: %v", err))
	}
	m.timelineLanesCache = lanes
	m.timelineLanesCached = true
	return lanes
}

// packLanesByProvider packs EACH PROVIDER's spans into its own lanes, as
// the design spec specifies, and concatenates the results.
//
// Packing the tier as one set is greedy on timing alone, so two providers
// whose calls merely happen not to overlap share a lane -- and on a real
// multi-provider capture most lanes end up that way. Such a lane belongs to
// no provider, which leaves the stall annotation naming a row and blaming
// nobody ("waiting on mixed/2"). That destroys the annotation's whole
// justification for naming lanes rather than spans: "waiting on aws/1"
// earns its place by pointing at exactly one bar a reader can go and look
// at. The cost is rows: a provider whose calls never overlap anything still
// gets a row of its own, so the timeline is taller than the log's peak
// concurrency. That is the honest picture -- those really are separate
// providers' calls -- and it is why model.Stalls measures idle lanes
// against the spans' own peak rather than against this count.
//
// Providers are packed in ascending order of the label they are drawn under
// (laneLabelProvider), so the rows are in a stable order a reader can
// predict and no map iteration order reaches the screen. Grouping is on
// that same label rather than on the raw provider address, so that two
// addresses sharing a short name cannot produce two different lanes both
// labelled "aws/1"; the label already cannot tell them apart, and a lane
// the reader cannot name is what this function exists to avoid.
//
// The returned indices are remapped from each group back into spans, so a
// lane resolves against the tier's own slice, as every caller reads it.
func packLanesByProvider(spans []span.Span) ([]model.Lane, error) {
	grouped := make(map[string][]int)
	for i, s := range spans {
		p := laneLabelProvider(s.Provider)
		grouped[p] = append(grouped[p], i)
	}

	var lanes []model.Lane
	for _, provider := range slices.Sorted(maps.Keys(grouped)) {
		idx := grouped[provider]
		group := make([]span.Span, len(idx))
		for i, s := range idx {
			group[i] = spans[s]
		}
		packed, err := model.PackLanes(group)
		if err != nil {
			return nil, err
		}
		for _, lane := range packed {
			mapped := make([]int, len(lane.Spans))
			for i, g := range lane.Spans {
				mapped[i] = idx[g]
			}
			lanes = append(lanes, model.Lane{Spans: mapped})
		}
	}
	return lanes, nil
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

// laneLabels derives every lane's left-hand identifier in one pass over
// lanes, in order: the lane's provider, followed by a running,
// PER-PROVIDER ordinal, e.g. "aws/2" for aws's second lane.
//
// A lane's provider is read from any one of its spans, because
// packLanesByProvider gives a lane only one provider's spans -- the label
// is a fact about the lane rather than a summary of it.
//
// The ordinal counts occurrences of that lane's own provider among the
// lanes seen so far, not the lane's position in the slice: with lanes
// ["aws", "aws", "google"], the third lane is "google/1", not "google/3" --
// google only has one lane. This is what makes the label answer "which of
// THIS provider's lanes is this" rather than "which row is this", the
// distinction the stall text ("...waiting on aws/1") depends on: a reader
// matching that text to a bar is looking for aws's own first lane, wherever
// it sits among the rows google or azurerm also occupy.
func laneLabels(spans []span.Span, lanes []model.Lane) []string {
	labels := make([]string, len(lanes))
	counts := make(map[string]int, len(lanes))
	for i, lane := range lanes {
		provider := laneLabelProvider(spans[lane.Spans[0]].Provider)
		counts[provider]++
		labels[i] = fmt.Sprintf("%s/%d", provider, counts[provider])
	}
	return labels
}

// laneLabelProvider shortens a span's provider to the identifier a lane is
// grouped and labelled by: the last "/"-separated segment, which is the provider's short type
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
// claim, so a lane holding a long provider name cannot squeeze the bar
// area, the point of this view, down to nothing. Provider names are short,
// closed-vocabulary slugs ("aws", "google", "kubernetes"), so this is a
// defensive ceiling rather than a width real logs are expected to reach.
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
// labelled lane bar per lane the active tier's spans pack into (see
// timelineLanes), then the time axis, then the stall annotation (see
// stallAnnotation) naming the windows model.Stalls found beneath it.
//
// A log with neither span tier gets the same capture guidance the table
// views give it (captureGuidance, rendered the way renderList renders it);
// a tier that exists but whose filter hides every span gets the same
// "nothing matches" note those views give (noMatchNote). Both are
// deliberately the SAME text, so a reader who has seen one in another view
// recognises it here rather than learning a second phrasing for one
// situation -- and captureGuidance is pre-wrapped to 40 columns, so its
// environment variable names survive the narrowest centre pane a supported
// width produces, which a single long line does not. Those are the two
// states renderList's own empty handling separates for the same reason; see
// timelineSpans for why they can never collapse into each other.
//
// Each lane's row is its label (laneLabels), padded to the widest label
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
// That makes the within-lane cursor a DETAIL-PANE affordance, with a known
// limit: below detailInlineWidth the detail pane is gone (see renderPanes),
// and left/right then move a selection nothing on screen reflects. A marker
// added only at the widths where the detail pane exists would make one key's
// visible behaviour depend on the terminal's width, which is a worse answer
// than a stated limitation; the key still drives Enter's jump target at
// every width, so it is not inert.
//
// The lane rows are windowed around the lane cursor by the same
// scrollWindow the centre table uses for its own row cursor, so a log with
// more lanes than the pane is tall keeps the selected one on screen instead
// of always showing the first screenful. The axis, indented under the bar
// area by the same label width the lane rows reserve so it still names
// both ends of what the bars above it are measuring against, is never
// dropped for want of height -- it is the one thing every lane bar is
// drawn relative to. It is not the LAST line once there is a stall
// to report, though: the annotation reserves its own room below the axis
// (see the comment on that reservation, below) and is appended after it,
// so on any pane tall enough to show both, the axis sits second-to-last
// and the annotation's line or lines close the pane instead.
func (m *Model) renderTimeline(w, h int) string {
	if h <= 0 {
		return ""
	}
	tier, spans := m.timelineSpans()
	if tier == tierNone {
		return clipLines(clipEachWidth(captureGuidance, w), h)
	}
	if len(spans) == 0 {
		return clipWidth(noMatchNote, w)
	}

	lanes := m.timelineLanes()
	wallClock := timelineWallClockMs(spans)

	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	barW := max(w-labelW-1, 0)

	// The annotation is reserved room below the axis before the lane rows
	// get whatever is left of h: it is bounded to a handful of short lines
	// (see maxStallsShown), so on a pane with room to spare it costs
	// nothing the lanes would otherwise have used, and only competes with
	// them on a pane too short to show both.
	//
	// On such a pane the annotation gives way, not the bars. It exists to
	// explain the lanes, so it can never be worth the last lane row: a
	// timeline drawing the axis and three stall lines and not one bar has
	// dropped the only thing this view is for -- idle time rendered as
	// visible blank space. One lane row is therefore held back from the
	// annotation's room whenever there is a lane to draw, the same
	// content-outranks-chrome ordering the axis's own never-dropped rule
	// states above, and the frame states again when it shortens the logging
	// caveat rather than the footer.
	annotationLines := strings.Split(m.stallAnnotation(w), "\n")
	if room := max(h-1-min(1, len(lanes)), 0); len(annotationLines) > room {
		annotationLines = annotationLines[:room]
		// The cut is marked the same way the detail pane marks its own
		// height cut (see detailCutMark), and for the stronger version of
		// the same reason: the stalls are ordered longest first, so what a
		// short pane drops is the tail of the ranking, and an annotation
		// that merely stopped early would read as the whole of what there
		// was to report. The mark takes the last line it has room for
		// rather than being added beside them, since by definition there is
		// no room to add one.
		//
		// A pane with no annotation room at all cannot say so -- the same
		// unmarked case fitDetailSections has at a height of one line.
		if room > 0 {
			annotationLines[room-1] = clipWidth(detailCutMark, w)
		}
	}

	dataH := h - 1 - len(annotationLines)
	top, visible := scrollWindow(m.timeline.lane, len(lanes), dataH)
	lines := make([]string, 0, visible+1+len(annotationLines))
	for i := top; i < top+visible; i++ {
		line := padRight(clipValueForKind(labels[i], labelW, tailIdentifierColumn), labelW) + " " + laneBar(spans, lanes[i], wallClock, barW)
		line = clipWidth(line, w)
		if i == m.timeline.lane {
			line = cursorBar(line, w, m.pane == PaneList)
		}
		lines = append(lines, line)
	}
	lines = append(lines, clipWidth(strings.Repeat(" ", labelW+1)+timeAxis(wallClock, barW), w))
	lines = append(lines, annotationLines...)
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

// stallAnnotationNoStalls is what stallAnnotation renders when the current
// tier and filter have nothing worth reporting -- whether because no window
// cleared the threshold, or because a filter has emptied the timeline
// entirely. Rendering nothing here would be indistinguishable from a blank
// pane that failed to draw at all, the same reasoning captureGuidance and
// noMatchNote already state for the lanes above it.
const stallAnnotationNoStalls = "no stalls"

// maxStallsShown bounds how many stall windows the annotation names. The
// pane has finite height, and a log with many short idle windows would
// otherwise crowd the axis and lane rows off screen with entries nobody
// asked to see; the handful longest by duration are what answers the
// question this view exists for -- was the plan slow because of work or
// because of waiting.
const maxStallsShown = 3

// stallThresholdMs is the minimum stall duration worth naming: 5% of the
// window, or 1s, whichever is larger. A short window has plenty of genuine
// sub-second handovers between spans that are not worth calling a stall; a
// long one can have a stall that is a small fraction of it and still be the
// reason the plan felt slow, so the floor alone would drown a real signal
// in noise on a short log and a bare percentage would flag noise on a long
// one.
//
// Measured against testdata/timeline.log, a 9s window: 5% is 450ms, so the
// 1s floor governs. That drops the fixture's three 500ms handover slivers
// (see model.Stalls' own merge-then-threshold doc comment) and keeps its
// 5s solo window and the 1s of core start-up before either provider was
// called -- exactly the distinction this annotation exists to draw.
func stallThresholdMs(wallClock uint32) uint32 {
	return max(wallClock/20, 1000)
}

// stallLane reports which lane contains the span at idx, so a stall can be
// named by the same lane label the bars above it already carry. -1 if idx
// names no span in any lane, which cannot happen for a Blocking index
// model.Stalls produced from the very spans lanes was packed from, but is
// reported rather than assumed so a broken invariant fails visibly instead
// of indexing lanes with a value that silently means something else.
//
// It is never asked about model.Stall's own -1 -- the sentinel for a window
// with nothing running at all. That is a legitimate value with its own
// wording (see stallAnnotation), which is decided before a lane is looked
// for, so a negative answer HERE still means only one thing.
func stallLane(lanes []model.Lane, idx int) int {
	for i, lane := range lanes {
		if slices.Contains(lane.Spans, idx) {
			return i
		}
	}
	return -1
}

// stallAnnotation renders the windows model.Stalls found beneath the lanes
// and axis, naming each by the lane label the reader already sees the bars
// under -- "waiting on aws/1" points at exactly one bar, the way naming the
// blocking span's RPC and resource type would not once a lane has packed
// more than one call sharing that name. At most maxStallsShown, longest
// first: the pane's height is finite, and a screenful of short stalls is
// noise beside the one that mattered. A list SHORTENED to that few carries
// detailCutMark, so that what was left out is visible rather than silently
// absent -- see the mark's own doc comment, and writeSlowestCalls in
// internal/profile, which states the same rule as "(top N)" only when it
// actually truncated.
//
// Three kinds of window are worded differently, because they are three
// different findings:
//
//   - A blocked wait names the lane still working, which is where a reader
//     goes to look ("1 lane idle 3.3s–20.0s waiting on aws/1").
//   - A window with nothing running anywhere has no lane to blame, and
//     says so ("2 lanes idle 5.5s–9.0s — nothing running"): the time is
//     not in the providers, so tuning provider parallelism will not touch
//     it.
//   - The window BEFORE any provider call is that same finding about
//     Terraform core starting up -- loading plugins, fetching schemas --
//     and is named for it ("2 lanes idle 0ms–3.0s — before any provider
//     call"). It appears on nearly every capture, so wording it as a
//     mid-plan collapse would tell every reader their plan fell over at
//     the start. model.Stall documents why Blocking -1 at StartMs 0 is
//     that window and can be no other.
//
// The idle count is capacity the log actually used at once (model.Stalls
// measures against the spans' peak), which can be fewer than the rows
// drawn: packing per provider opens a row for a provider whose calls never
// overlap anything. So the count is left as a plain "N lanes idle" rather
// than claimed as "ALL N lanes", which would be a statement about the rows
// on screen that this number is not entitled to make.
//
// Offsets are rendered with formatMs, the same duration formatter every
// other number in this package uses, rather than a wall-clock time: span
// times are milliseconds from a per-builder zero point (see the doc
// comment on span.Span), and rendering a time of day out of that offset
// would be inventing a fact the log does not carry.
//
// A tier with no spans -- including one a filter has emptied entirely --
// reports stallAnnotationNoStalls rather than an empty string: see its own
// doc comment for why silence is not an acceptable answer here.
func (m *Model) stallAnnotation(w int) string {
	_, spans := m.timelineSpans()
	if len(spans) == 0 {
		return clipWidth(stallAnnotationNoStalls, w)
	}

	lanes := m.timelineLanes()
	wallClock := timelineWallClockMs(spans)
	stalls, err := model.Stalls(spans, stallThresholdMs(wallClock))
	if err != nil {
		// timelineSpans hands a single tier's spans by construction, the
		// same guarantee timelineLanes leans on for PackLanes -- see its
		// own panic comment for why this is a programming error to fail
		// loudly on rather than an annotation that quietly says nothing.
		panic(fmt.Sprintf("tui: stallAnnotation: %v", err))
	}
	if len(stalls) == 0 {
		return clipWidth(stallAnnotationNoStalls, w)
	}

	sort.SliceStable(stalls, func(i, j int) bool {
		di, dj := stalls[i].EndMs-stalls[i].StartMs, stalls[j].EndMs-stalls[j].StartMs
		if di != dj {
			return di > dj
		}
		// Duration ties break by start time so the chosen top few, and
		// their order, do not depend on model.Stalls' own internal
		// ordering -- sort.SliceStable alone only guarantees ties keep
		// their INPUT order, which is an implementation detail of the
		// sweep, not a property this function should expose.
		return stalls[i].StartMs < stalls[j].StartMs
	})
	truncated := len(stalls) > maxStallsShown
	if truncated {
		stalls = stalls[:maxStallsShown]
	}

	// The lane is named with the label the LANE ROW renders, clipped by the
	// same rule and to the same width renderTimeline's label column uses
	// (laneLabelWidth, capped at maxLaneLabelWidth). "waiting on aws/1"
	// points at exactly one bar only if the bar carries that same text:
	// naming a lane "googleworkspace/1" beside a row reading
	// "…workspace/1" would send the reader looking for a bar that is not
	// on screen.
	labels := laneLabels(spans, lanes)
	labelW := laneLabelWidth(labels)
	lines := make([]string, 0, len(stalls)+1)
	for i, s := range stalls {
		unit := "lanes"
		if s.Idle == 1 {
			unit = "lane"
		}
		// Every line opens on the same clause -- how much capacity sat
		// idle, and over which window -- so the three endings below are the
		// only thing a reader has to tell apart, and the offsets land in
		// the same place on each line to be read against the axis above.
		window := fmt.Sprintf("%d %s idle %s–%s", s.Idle, unit, formatMs(uint64(s.StartMs)), formatMs(uint64(s.EndMs)))
		var line string
		switch {
		case s.Blocking < 0 && s.StartMs == 0:
			line = window + " — before any provider call"
		case s.Blocking < 0:
			line = window + " — nothing running"
		default:
			l := stallLane(lanes, s.Blocking)
			if l < 0 {
				// model.Stalls produced Blocking as an index into the very
				// spans slice lanes was packed from, so every stall's
				// blocking span must sit in exactly one lane -- the same
				// invariant the panic above relies on for model.Stalls
				// itself. Failing loudly here matches that convention,
				// rather than naming a stall's lane "?", which would read
				// as a deliberate answer instead of the broken guarantee it
				// would actually be.
				panic(fmt.Sprintf("tui: stallAnnotation: span %d (stall %d's Blocking) is not in any lane", s.Blocking, i))
			}
			line = window + " waiting on " + clipValueForKind(labels[l], labelW, tailIdentifierColumn)
		}
		// clipValueEnd rather than clipWidth: this line's payload is its
		// TAIL -- which lane the idle ones waited on, or that there was no
		// lane to wait on -- and clipWidth cuts with no marker, so a pane
		// too narrow for the whole sentence would otherwise render "2 lanes
		// idle 3.3s–9.0s waiting" as though that were all there was to say.
		// The head is what survives the cut, since the offsets locate the
		// window on the axis directly above; the ellipsis is what tells the
		// reader something was said that they have not seen.
		lines = append(lines, clipValueEnd(line, w))
	}
	if truncated {
		lines = append(lines, clipWidth(detailCutMark, w))
	}
	return strings.Join(lines, "\n")
}
