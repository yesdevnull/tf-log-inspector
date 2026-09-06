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
// span.Span's StartMs/EndMs), and the two tiers are built by different
// builders, so the timeline can only ever draw one of them.
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
	switch tier := timelineTierFor(m.log); tier {
	case tierRPC:
		return tier, m.filter().SpansMatching(m.log.RPCSpans)
	case tierUI:
		return tier, m.uiFilter().SpansMatching(m.log.UISpans)
	default:
		return tierNone, nil
	}
}

// timelineTierFor is the tier decision itself, as a property of the LOG
// alone: RPC where the log has one, otherwise UI, and tierNone only for a
// log carrying neither. It is separate from filteredTimelineSpans because
// the question is asked outside the timeline too -- detailNaturalWidth
// sizes the detail pane over the spans a selection can actually reach, and
// a UI-hook span is reachable only in a log this answers tierUI for. Two
// copies of the rule would let the pane be measured for a tier the view
// never draws, sizing it for a line no keypress can produce out of columns
// the centre pane would otherwise have.
func timelineTierFor(l *model.Log) timelineTier {
	switch {
	case len(l.RPCSpans) > 0:
		return tierRPC
	case len(l.UISpans) > 0:
		return tierUI
	}
	return tierNone
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
		// timelineSpans hands PackLanes spans of a single FIDELITY by
		// construction -- which holds while ReportedBuilder is the sole
		// RPC-tier builder, since the guard is on span.Fidelity and the RPC
		// tier has three fidelities defined for it. A second RPC-tier
		// builder anchoring to its own zero point, as UIHookBuilder already
		// must, would put two fidelities in this slice and reach here.
		// Until then ErrMixedTimelines means that guarantee broke somewhere
		// upstream -- a programming error to fail loudly on, the same
		// treatment unhandledView gives its own can't-happen case, rather
		// than a blank pane that looks like this view was never built.
		//
		// Failing loudly is contained to the view that draws the timeline:
		// see invalidateRows on why the clamp does not pack lanes for a
		// view that does not.
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
// shrink or empty the very lane the cursor was on, which invalidateRows
// catches the same way it catches a row selection run off the end of a
// shortened table, whenever the timeline is the view on screen (see
// invalidateRows for why it waits until then).
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
// current lane range, and re-seeks the within-lane span cursor to the span
// of the new lane nearest IN TIME to the one it was on.
//
// Carrying the ordinal across instead -- which clamping alone does -- lands
// the cursor on an unrelated call at an unrelated time: lane 0's third call
// and lane 1's third call have nothing to do with each other, and on a real
// capture a lane holds hundreds. The cursor drives the detail pane and
// Enter's jump target, so what it lands on is what the reader is then
// reading and jumping to, and a reader stepping down a column of bars is
// looking at one moment in the plan, not at one ordinal.
//
// Only a move that actually CHANGES lane re-seeks. A ↓ on the last lane
// clamps back to where it was, and re-seeking there would quietly move the
// within-lane cursor off the span the reader had chosen in reply to a key
// that did nothing else.
func (m *Model) moveTimelineLane(delta int) {
	was := m.timeline.lane
	at, seek := m.selectedTimelineSpanValue()
	m.timeline.lane += delta
	m.clampTimelineSelection()
	if !seek || m.timeline.lane == was {
		return
	}
	lanes := m.timelineLanes()
	if m.timeline.lane >= len(lanes) {
		return
	}
	_, spans := m.timelineSpans()
	m.timeline.span = nearestSpanByStart(spans, lanes[m.timeline.lane].Spans, at.StartMs)
}

// nearestSpanByStart is the position WITHIN lane of the span whose start is
// closest to startMs, which is what a lane change re-seeks the cursor to.
//
// Distance is on StartMs alone rather than on the interval, because that is
// the coordinate the bars are drawn from and the one the reader's eye is
// travelling down: the span that begins nearest the moment they were
// looking at is the span under that column. Ties go to the earlier span,
// the same lower-index tie-break model.outranksAsBlocking uses, so the
// answer does not depend on which end of a lane the cursor arrived from.
//
// A lane always holds at least one span (PackLanes never opens an empty
// one), so 0 is a real answer here and not a stand-in for "none".
func nearestSpanByStart(spans []span.Span, lane []int, startMs uint32) int {
	best, bestDist := 0, uint32(0)
	for i, idx := range lane {
		// Subtracted the larger way round each time: these are unsigned
		// milliseconds, so the other order wraps rather than going negative.
		var dist uint32
		if at := spans[idx].StartMs; at > startMs {
			dist = at - startMs
		} else {
			dist = startMs - at
		}
		if i == 0 || dist < bestDist {
			best, bestDist = i, dist
		}
	}
	return best
}

// moveTimelineSpan shifts the within-lane span cursor by delta, clamping it
// to the selected lane's own span range.
func (m *Model) moveTimelineSpan(delta int) {
	m.timeline.span += delta
	m.clampTimelineSelection()
}

// selectedLaneStepsThroughSpans reports whether ←/→ have anywhere to go
// right now: the timeline is showing, and the lane the cursor is on holds
// more than one span. It is what the footer's span hint is shown on (see
// actionKeys), read off the same timelineLanes the key handler moves over,
// so the hint and the handler cannot disagree about whether the key does
// anything.
//
// It is per-LANE rather than per-view because the rule this package applies
// to the open hint is that a hint names a key that does something, and on a
// lane of one span these keys do not. That is the same granularity
// selectedRowOpens already has, which changes as ↑/↓ moves between a rollup
// row and a call row.
func (m *Model) selectedLaneStepsThroughSpans() bool {
	if m.view != ViewTimeline {
		return false
	}
	lanes := m.timelineLanes()
	if m.timeline.lane < 0 || m.timeline.lane >= len(lanes) {
		return false
	}
	return len(lanes[m.timeline.lane].Spans) > 1
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

// timelineLaneLabels is every lane's label and the width the label column
// is drawn at, for the current tier and filter, measured ONCE per frame and
// cached beside the spans and lanes it is derived from (see
// timelineLabelsCache).
//
// The pair is returned together, and cached together, because the two
// callers must agree on both. renderTimeline draws each lane row's label
// clipped to that width, and stallAnnotation names a stall's lane with the
// same label clipped the same way -- "waiting on aws/1" points at exactly
// one bar only if the bar carries that same text, so a label measured to a
// different width in the two places sends the reader looking for a row that
// is not on screen. Two hand-kept copies of that rule are what the one
// measurement replaces.
//
// Every caller must reach it through a POINTER, or it fills a cache on a
// copy that is immediately discarded -- the hazard rowsCache's own doc
// comment describes, pinned here by
// TestRenderFillsTheTimelineLabelCacheOnTheModelItRendered.
func (m *Model) timelineLaneLabels() ([]string, int) {
	if !m.timelineLabelsCached {
		_, spans := m.timelineSpans()
		m.timelineLabelsCache = laneLabels(spans, m.timelineLanes())
		m.timelineLabelWidthCache = laneLabelWidth(m.timelineLabelsCache)
		m.timelineLabelsCached = true
	}
	return m.timelineLabelsCache, m.timelineLabelWidthCache
}

// timelineWallClock is the window the axis, the bars and the busy summary
// are all scaled to (see timelineWallClockMs), for the current tier and
// filter, measured once per frame and cached with them.
//
// One measurement rather than three per frame is the point: the axis's
// right-hand label, every bar's column arithmetic, the busy percentage's
// denominator and the stall threshold are all the same window, and a frame
// in which they were not would draw bars against one scale and label them
// with another. The same pointer-receiver rule applies as above.
func (m *Model) timelineWallClock() uint32 {
	if !m.timelineWallClockCached {
		_, spans := m.timelineSpans()
		m.timelineWallClockCache = timelineWallClockMs(spans)
		m.timelineWallClockCached = true
	}
	return m.timelineWallClockCache
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
// one place that width is divided between the two.
//
// The selected lane is marked by drawing its LABEL COLUMN, and only its
// label column, as the cursor bar (see cursorBar) -- the same reverse-video
// treatment the table views give their selected row, styled or dimmed by
// whether the list pane has focus, so the cursor reads as the same thing it
// does in every other view. It stops at the label because reverse video
// swaps foreground and background, and this row's payload is drawn in that
// distinction: a filled cell (█) painted in the old background reads as
// empty and a space painted in the old foreground reads as solid, so a
// full-row bar rendered the selected lane's busy and idle columns swapped.
// That inverted the one claim this view exists to make, on the one row the
// reader was looking at, and changed which row it inverted on every ↑/↓.
// TestTheCursorDoesNotRedrawTheSelectedLanesBar holds the bar to being
// byte-identical selected or not.
//
// The within-lane span cursor has no glyph of its own in the bar -- a
// packed lane can draw several spans into one column (see laneBar), so a
// marker on individual columns would often point at spans it cannot tell
// apart -- and is instead named in the detail pane beside it.
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
// of always showing the first screenful. A window that left lanes off
// screen says how many in the axis row's label gutter (see laneCutMark):
// a lane that scrolled away leaves no gap behind it, so without the count a
// five-lane log drawing one lane looks exactly like a one-lane log.
//
// The axis is indented under the bar area by the same label width the lane
// rows reserve, so it still names both ends of what the bars above it are
// measuring against, and it outranks every lane row but the first -- it is
// the one thing they are all drawn relative to, so a pane too short for all
// of them keeps it and drops the surplus rows. It is not the LAST line once
// there is anything to note beneath it: the notes block -- the clamped-start
// caveat where there is one, the filter note where the figures are
// narrowed, then the busy summary and the stall annotation (see
// timelineNotes) -- is appended after it, so on any pane tall enough to
// show both, the axis sits second-to-last and the notes close the pane.
//
// What the axis does NOT outrank is the first lane row or the first line of
// notes; see the height budget below.
func (m *Model) renderTimeline(w, h int) string {
	if h <= 0 {
		return ""
	}
	tier, spans := m.timelineSpans()
	if tier == tierNone {
		return fitCaptureGuidance(w, h)
	}
	if len(spans) == 0 {
		return clipWidth(noMatchNote, w)
	}

	lanes := m.timelineLanes()
	wallClock := m.timelineWallClock()
	labels, labelW := m.timelineLaneLabels()
	barW := max(w-labelW-1, 0)

	// h is spent in priority order: one lane row, then one line of notes,
	// then the axis, and only then more lane rows and more notes. The notes
	// are bounded to a handful of short lines (see maxStallsShown and
	// clampedStartNote), so on a pane with room to spare this costs the
	// lanes nothing; it decides only what a pane too short for everything
	// gives up.
	//
	// A lane row comes first because the bars are the only thing this view
	// is for -- idle time rendered as visible blank space -- and because an
	// empty lane area is how this view SAYS nothing ran: a pane drawing the
	// axis and three stall lines and not one bar does not show less than
	// the truth, it shows the opposite of it. That is the same
	// content-outranks-chrome ordering the frame states when it shortens
	// the logging caveat rather than the footer.
	//
	// One line of notes comes next because it is the line that carries the
	// cut mark: with the notes dropped whole and unmarked, a one-lane
	// timeline reads as a complete frame with nothing worth reporting
	// beneath it, while the detail pane in the same frame marks its own
	// height cut with an ellipsis. Silence there is exactly what
	// stallAnnotationNoStalls exists to refuse.
	notes := m.timelineNotes(w)
	laneReserve, noteReserve := min(1, len(lanes)), min(1, len(notes))
	// The axis is drawn only once those two reservations are met, and takes
	// no line before them.
	axisH := 0
	if h > laneReserve+noteReserve {
		axisH = 1
	}
	if room := max(h-axisH-laneReserve, 0); len(notes) > room {
		notes = notes[:room]
		// The cut is marked the same way the detail pane marks its own
		// height cut (see detailCutMark), and for the stronger version of
		// the same reason: the stalls are ordered longest first, so what a
		// short pane drops is the tail of the ranking, and an annotation
		// that merely stopped early would read as the whole of what there
		// was to report. timelineNotes orders the block so that what a cut
		// reaches first is a finding rather than the caveat about the
		// bars. The mark takes the last line it has room for rather than
		// being added beside them, since by definition there is no room to
		// add one.
		//
		// A pane with no room for even the mark cannot say so. That is a
		// pane of one line, which the lane row has taken -- the same
		// unmarked case fitPaneSections has at that same height.
		if room > 0 {
			notes[room-1] = clipWidth(detailCutMark, w)
		}
	}

	dataH := h - axisH - len(notes)
	top, visible := scrollWindow(m.timeline.lane, len(lanes), dataH)
	lines := make([]string, 0, visible+axisH+len(notes))
	for i := top; i < top+visible; i++ {
		// The label column and the bar are composed separately because
		// only the label may carry the cursor's styling; see the comment
		// on the cursor treatment above.
		label := padRight(clipValueForKind(labels[i], labelW, tailIdentifierColumn), labelW) + " "
		if i == m.timeline.lane {
			label = cursorBar(label, labelW+1, m.pane == PaneList)
		}
		lines = append(lines, clipWidth(label+laneBar(spans, lanes[i], wallClock, barW), w))
	}
	if axisH > 0 {
		gutter := padRight(clipValueEnd(laneCutMark(len(lanes)-visible), labelW), labelW) + " "
		lines = append(lines, clipWidth(gutter+timeAxis(wallClock, barW), w))
	}
	lines = append(lines, notes...)
	return strings.Join(lines, "\n")
}

// laneCutMark is what the axis row's label gutter says when the pane had
// more lanes than it had room to draw: "+3" for three lanes not on screen.
// Nothing was cut means nothing is said, so a one-lane log and a five-lane
// log showing one lane no longer render the same frame -- the silence this
// view's own cut marks (detailCutMark on the notes, on the stall list, and
// on the detail pane beside it) all exist to refuse.
//
// It is a count rather than a bare ellipsis because the number is the whole
// finding: a reader who cannot see four lanes needs to know there are four,
// not merely that there are some. The gutter is blank space the axis row
// already spends, so the mark costs no lane row -- which matters more here
// than anywhere else in the pane, since a lane row is the one thing this
// view exists to draw. It sits under the labels, in the same column and
// clipped the same way, because it names lanes.
//
// A pane too short even for the axis cannot say it: at that height the
// timeline has one lane row and, at best, the notes' own cut mark, which is
// the same limit fitPaneSections states for a one-line detail pane.
func laneCutMark(hidden int) string {
	if hidden <= 0 {
		return ""
	}
	return fmt.Sprintf("+%d", hidden)
}

// clampedStartNote is what the timeline says when any span it draws had its
// start forced to zero because its reported duration exceeded its offset
// from the log's first entry (span.Span.StartClamped). Such a span is drawn
// anchored at column 0 with a length of EndMs -- a start it does not have,
// and a length shorter than its own DurationMs -- and this is the one view
// that renders extents POSITIONALLY, so it is the one view where saying
// nothing leaves the reader a picture that is wrong rather than merely
// incomplete. --diagnose counts these spans and --profile prints a note
// about them; the phrasing here is --profile's own ("a span whose reported
// duration exceeds its offset from the log's first entry has its start
// clamped to zero"), shortened, so a reader who has met one recognises the
// other rather than learning a second account of one fact. What differs is
// the CONSEQUENCE each states: --profile explains a peak concurrency that
// reads low, this explains a bar that starts and ends somewhere it did not.
//
// It is one sentence WRAPPED TO THE PANE (see wrapToWidth), not pre-wrapped
// the way captureGuidance is. captureGuidance's fixed 40 columns earn their
// place: it names environment variables that must not be broken across a
// line, and it is the pane's whole content, so the lines it spends displace
// nothing. This note is the opposite on both counts -- it is prose with no
// identifier in it, and it is chrome competing with the lane rows for a
// twelve-line pane. Pre-wrapped to captureGuidance's 40 columns it would
// take four of those lines at every width, including the 74-column pane a
// 160-column terminal gives it, where wrapping to the pane costs two.
//
// It is also kept SHORT for the same reason. The cause is stated once here
// and in full by --profile and the detail pane's own Start field
// (clampedStartValue); what this note owes the reader is that the bars they
// are looking at are drawn somewhere the spans did not run.
const clampedStartNote = "Note: a clamped start -- duration exceeding the offset from the log's start -- draws from 0, shorter than its duration."

// timelineNotes is everything drawn beneath the axis: the clamped-start
// note where there is one, the filter note where the figures cover only
// part of the log, then the busy summary, then the stall annotation.
//
// The two notes come FIRST because a short pane cuts this block from its
// tail (see renderTimeline), and of the four they are the ones whose
// absence misleads. Each qualifies something that has no room to carry a
// caveat of its own -- the clamped-start note the bars' positions, the
// filter note every figure below it -- and a reader who never sees one
// reads what it qualifies as a fact about the plan. A stall line lost to
// the cut is a finding not shown, and detailCutMark says so on the reader's
// behalf -- the same distinction noMatchNote is justified by, between a
// pane that shows less and a pane that misleads.
//
// The clamped-start note precedes the filter note only because it is about
// the bars ABOVE the block while the filter note is about the figures
// BELOW it, so each sits beside what it qualifies. What matters to the cut
// is that both outrank the busy summary: the summary can never be the
// first line a reader sees with its qualification gone.
//
// The busy summary sits above the stall list, and so outranks it in the
// cut, because it is the whole answer where the list is the breakdown: a
// pane with room for one line should spend it on how much of the window was
// work rather than on the longest of the waits (see busyNote).
func (m *Model) timelineNotes(w int) []string {
	_, spans := m.timelineSpans()
	var lines []string
	if slices.ContainsFunc(spans, func(s span.Span) bool { return s.StartClamped }) {
		lines = wrapToWidth(clampedStartNote, w)
	}
	if m.timelineNarrowed() {
		lines = append(lines, wrapToWidth(timelineFilterNote, w)...)
	}
	lines = append(lines, clipValueEnd(busyNote(spans, m.timelineWallClock()), w))
	return append(lines, strings.Split(m.stallAnnotation(w), "\n")...)
}

// timelineFilterNote is what the notes block says when the figures beneath
// it were computed over a SUBSET of the log.
//
// Every one of those figures -- the busy summary, and every window the
// stall annotation names -- is swept over the spans the filter left, while
// each is worded as a statement about the plan. On testdata/timeline.log,
// ticking aws deletes the log's one real finding and opens a window
// reporting "nothing running -- between calls" across a stretch google was
// working in: the clause this view reserves for "the time is not in the
// providers", handed to a reader who would act on it by going to look at
// Terraform core. The busy percentage moves the same way, and is the figure
// most likely to be quoted.
//
// The note therefore sits WHERE THE FIGURES ARE. The header's "1 of 3 RPC
// spans" already says a filter is on, but it is three panes away and is a
// sentence about span counts, not about these numbers. This is the class
// noMatchNote and jumpBlockedNote exist for -- a filtered state that
// renders as an unfiltered one -- and it names Esc as they both do.
//
// It says "everything below" rather than naming the summary, because the
// all-idle stall lines are the sentences most wrong under a filter and they
// have no room to carry the qualification themselves: both fit the common
// centre pane only just (see betweenCallsClause), with a column or two of
// slack that a longer pair of offsets spends, so a word added to either is
// a word clipped off its end.
const timelineFilterNote = "Note: a filter is active -- everything below covers the spans shown only. Esc clears it."

// timelineNarrowed reports whether the active filter is actually hiding
// spans of the tier the timeline draws, which is the question the note
// above answers -- not whether a facet is ticked.
//
// The two differ in both directions. A level-only selection filters the raw
// log and no spans at all (see filter), so filterActive alone would qualify
// figures that are the whole log's. A selection covering every value hides
// nothing either. And the UI tier is narrowed through uiFilter's own
// translation, so what a ticked provider hides there is not read off the
// same map. Counting what survived answers all three at once, over slices
// both already computed.
func (m *Model) timelineNarrowed() bool {
	tier, spans := m.timelineSpans()
	switch tier {
	case tierRPC:
		return len(spans) < len(m.log.RPCSpans)
	case tierUI:
		return len(spans) < len(m.log.UISpans)
	}
	return false
}

// busyNote is the timeline's headline figure: how much of the window the
// axis draws had ANY span running, as a duration and as a percentage of
// that window.
//
// It exists because the stall list structurally cannot answer the question
// this view is for. A stall is a window in which fewer spans ran than the
// log's peak concurrency -- including none at all -- AND which lasted long
// enough to clear stallThresholdMs; the threshold applies to every such
// window, not only to the ones nothing ran in.
//
// A run of sixty forty-millisecond calls three seconds apart --
// rate-limited API polling, an ordinary shape -- opens a window in every
// one of its fifty-nine gaps, and concurrency really does fall to zero in
// each. What silences the annotation is the THRESHOLD alone: each gap is
// 2.96s against the 9s stallThresholdMs takes off a 180s window, so all
// sixty windows are dropped and "no stalls" is the correct answer to the
// question the annotation asks. It is the wrong answer to the question this
// view is for, and no threshold tuning fixes that: the idle time is real,
// 98% of the run, and is simply spread too thin for any window to reach a
// threshold worth having. testdata/timeline-dense-lane.log is that shape,
// measured above.
//
// The number is a UNION of the spans' intervals (model.BusyMs), not a sum
// of their durations, so a window with four providers working in parallel
// reports the wall clock they covered rather than four times it. What it
// claims is therefore exactly "something was running", which is what the
// bars above it draw -- it is deliberately NOT a claim about how much work
// was done, since one busy millisecond looks the same here whether one span
// or forty covered it. The word is "busy" because that is already this
// view's word for occupancy: laneShadeFor shades each column by how much of
// it was busy, and this is the same measure taken over the whole window.
//
// The denominator is the window handed in, which is the one the axis is
// scaled to (Model.timelineWallClock, measured over these same filtered
// spans), so the percentage is a fraction of the window a reader can see
// rather than of some other span of time. It is a parameter for the reason
// laneBar and timeAxis take theirs: the frame measures the window once and
// every part of it drawn against that scale is given the same number. A
// zero window -- every span zero-extent, which the UI tier can produce --
// reports 0%, there being no window for anything to be a fraction of.
//
// The percentage truncates rather than rounding, so it never reports 100%
// for a window with idle time in it, and never rounds a run that was 0.4%
// busy up to 1%.
func busyNote(spans []span.Span, window uint32) string {
	busy, err := model.BusyMs(spans)
	if err != nil {
		// One fidelity by construction, the same guarantee timelineLanes
		// and stallAnnotation lean on: see timelineLanes' own panic
		// comment, including what would break it.
		panic(fmt.Sprintf("tui: busyNote: %v", err))
	}
	var pct uint64
	if window > 0 {
		pct = uint64(busy) * 100 / uint64(window)
	}
	return fmt.Sprintf("busy %s of %s (%d%%)", formatMs(uint64(busy)), formatMs(uint64(window)), pct)
}

// laneBar renders one lane's spans as a bar row barW columns wide covering
// [0, spanMs). Each span occupies the columns its interval maps to, with a
// minimum of one column so a short span is visible rather than rounded
// away, and each column is SHADED by how much of the time it stands for was
// actually busy (see laneShadeFor).
//
// Two spans that map to the same column merely add their occupied time to
// it, which is sound because PackLanes guarantees the spans in one lane
// never overlap: their busy milliseconds are disjoint, so the sum is the
// column's true occupancy and can never exceed the column's own width.
//
// The shading is what stops a busy-looking lane from being a lie. A lane
// with more spans than barW has columns -- 200 sub-100ms calls scattered
// over an eight-minute plan, on a bar 65 columns wide -- touches every
// column, and drawing each touched column solid would render a lane that
// was idle 99% of the time as fully busy: waiting drawn as work, the exact
// inversion of the question this view exists to answer. Shading leaves that
// lane at its lightest glyph throughout, so its density is visible instead.
//
// What remains, and cannot be fixed by shading, is the COUNT: several
// adjacent calls still compress into one mark, so the row understates how
// many calls ran. PackLanes' no-overlap guarantee means that is compression
// of adjacent calls into one visible mark, not a false claim that unrelated
// calls ran together.
func laneBar(spans []span.Span, lane model.Lane, spanMs uint32, barW int) string {
	if barW <= 0 {
		return ""
	}
	touched := make([]bool, barW)
	covered := make([]bool, barW)
	busyMs := make([]uint64, barW)
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
			// touched is kept separately from the milliseconds because the
			// two answer different questions: touched is what the
			// minimum-one-column rule promises -- a call the log recorded
			// leaves a visible mark -- while busyMs is how much of the
			// column that call actually accounts for, which for a call
			// shorter than a column rounds to nothing at all.
			touched[c] = true
			colStart, colEnd := laneColBounds(c, spanMs, barW)
			lo, hi := max(uint64(s.StartMs), colStart), min(uint64(s.EndMs), colEnd)
			if hi > lo {
				busyMs[c] += hi - lo
			}
			// covered is the same question busyMs answers, asked in the one
			// form that still works for a column standing for NO
			// milliseconds: does this one span's interval contain the
			// column's whole interval? For a column with milliseconds of its
			// own that is already implied by busyMs reaching colMs, so this
			// changes nothing there; for an empty column, where the fraction
			// would be 0/0, it is the only thing that can distinguish a span
			// running through the column from one that merely marked it.
			// A zero-extent span occupies no instant and so covers nothing,
			// which is what keeps its minimum-one-column mark a mark.
			if s.EndMs > s.StartMs && uint64(s.StartMs) <= colStart && colEnd <= uint64(s.EndMs) {
				covered[c] = true
			}
		}
	}

	var b strings.Builder
	for c := range barW {
		colStart, colEnd := laneColBounds(c, spanMs, barW)
		b.WriteRune(laneShadeFor(touched[c], covered[c], busyMs[c], colEnd-colStart))
	}
	return b.String()
}

// laneShades are the glyphs a lane column can be drawn in, lightest first.
// They are the Block Elements shade ramp, which reads as a density scale in
// any font that has it, rather than as four unrelated marks a reader has to
// learn an order for.
//
// Each is one display column. U+2591 is East Asian width class Neutral;
// U+2592, U+2593 and U+2588 are Ambiguous, the same class as the │ this
// package already renders at one column each (see paneSepWidth) -- so what a
// terminal resolves Ambiguous to, it resolves for the pane separators too
// and not for the bars alone. Width is measured with lipgloss.Width wherever
// it matters -- never a rune count -- and the whole ramp is held to a single
// column each by TestEveryLaneShadeIsOneDisplayColumn.
var laneShades = [4]rune{'░', '▒', '▓', '█'}

// laneShadeFor picks the glyph for one column: the space for a column no
// span touched, and otherwise a shade chosen by what fraction of the
// column's own milliseconds were busy.
//
// The bands are thirds, with the top of the ramp reserved for a column a
// span occupies ENTIRELY. That reservation is the point of the scheme: █
// then means "solid, nothing waiting here", so a reader can trust a run of
// █ to be continuous work, and a column that is merely mostly busy is
// visibly not that. Thirds below it because three intermediate bands is as
// much resolution as a shade ramp carries legibly -- an eye reading a bar
// at a glance is judging light from dark, not measuring -- and because it
// puts the lightest glyph on everything under a third, which is the band
// the compression hazard lives in.
//
// A column with no milliseconds of its own -- barW columns over a window of
// fewer than barW milliseconds, or the zero window laneCol handles -- cannot
// be judged by that fraction at all, since it would be 0/0. covered answers
// it instead (see laneBar): a span with real extent running through the
// column covers the whole of it, there being no time in it to be idle, and
// gets the top of the ramp on the same terms every other solid column does.
// A column merely TOUCHED -- by a zero-extent span, which occupies no
// instant -- keeps the lightest shade, so the minimum-one-column rule still
// draws a mark rather than a claim about work.
//
// covered is redundant wherever the column has milliseconds of its own: one
// span containing the whole column contributes colMs of busy time by itself.
// It is asked first because it is the only test that survives an empty
// column, not because the two can disagree.
//
// The comparisons are integer throughout, matching the rest of this
// package's column arithmetic: busyMs and colMs are both bounded by a
// uint32 window, so tripling either cannot overflow a uint64.
func laneShadeFor(touched, covered bool, busyMs, colMs uint64) rune {
	switch {
	case !touched:
		return ' '
	case covered:
		return laneShades[3]
	case colMs == 0:
		return laneShades[0]
	case busyMs >= colMs:
		return laneShades[3]
	case busyMs*3 >= colMs*2:
		return laneShades[2]
	case busyMs*3 >= colMs:
		return laneShades[1]
	}
	return laneShades[0]
}

// laneColBounds is the half-open millisecond interval column c stands for,
// [start, end). Boundaries are the same integer division laneCol floors
// with, so the columns tile [0, spanMs) exactly with no millisecond in two
// columns and none in neither -- which is what makes a column's occupied
// milliseconds comparable against its own width. A zero window collapses
// every column to an empty interval, which laneShadeFor answers on its own.
func laneColBounds(c int, spanMs uint32, barW int) (uint64, uint64) {
	return uint64(c) * uint64(spanMs) / uint64(barW), uint64(c+1) * uint64(spanMs) / uint64(barW)
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

// timeAxis renders the axis line beneath the lanes: zero at the left, the
// total at the right, both formatted the same way formatMs renders every
// other duration in this package rather than a second formatter for the
// same number. The left end goes through formatMs too, rather than being
// written out as "0s", so that the axis and the annotation beneath it
// cannot spell the same instant two ways -- see formatMs on why zero is
// spelled in seconds.
//
// The right label is drawn only where it fits WHOLE, with a column of space
// separating it from the left one, and is otherwise replaced by
// detailCutMark. Neither half of that is presentation. A bar exactly as
// wide as the two labels together left no gap, so they ran into each other
// as a single token ("0s521.4s"); a bar narrower still had the pair cut by
// clipWidth, which marks nothing, leaving a fragment that reads as a whole
// number and a wrong one -- "0s521" for a 521.4s window, "0s800" for one of
// 800ms, both off by orders of magnitude from either end of the axis they
// claim to label. Losing the total is the cost; stating it wrongly is not
// an alternative, and the notes beneath still carry the window (see
// busyNote). The mark is the same ellipsis the notes block, the stall list
// and the detail pane already cut with.
//
// "0s" is what survives at the last: it anchors the axis under the lane
// bars' own left edge. A bar with no room even for the mark beside it keeps
// the left label alone, which is where the closing clipWidth takes over.
//
// The result is exactly barW columns wide, whichever of those it drew.
func timeAxis(spanMs uint32, barW int) string {
	if barW <= 0 {
		return ""
	}
	left := formatMs(0)
	right := formatMs(uint64(spanMs))
	if !axisLabelsFit(left, right, barW) {
		right = detailCutMark
		if !axisLabelsFit(left, right, barW) {
			right = ""
		}
	}

	gap := max(barW-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return clipWidth(left+strings.Repeat(" ", gap)+right, barW)
}

// axisLabelsFit reports whether the axis can carry both labels at barW
// columns: their own widths plus the one column of space that keeps them
// two labels rather than one token.
func axisLabelsFit(left, right string, barW int) bool {
	return lipgloss.Width(left)+1+lipgloss.Width(right) <= barW
}

// stallAnnotationNoStalls is what stallAnnotation renders when the current
// tier and filter have nothing worth reporting -- whether because no window
// cleared the threshold, or because a filter has emptied the timeline
// entirely. Rendering nothing here would be indistinguishable from a blank
// pane that failed to draw at all, the same reasoning captureGuidance and
// noMatchNote already state for the lanes above it.
const stallAnnotationNoStalls = "no stalls"

// nothingRunningClause opens the two lines that report a window with no
// span running anywhere. It replaces the count the other line carries
// rather than sitting beside it: "concurrency 0 of 3" is arithmetic where
// "nothing running" is the finding, and these two windows are the ones a
// reader most needs to pick out of the block at a glance -- they are the
// ones no amount of provider tuning will touch.
const nothingRunningClause = "nothing running"

// betweenCallsClause and beforeAnyCallClause close the two all-idle lines,
// and are the only thing telling them apart. Both fit
// commonCentrePaneWidth whole with the clause attached (measured in
// TestTheAllIdleStallLinesFitTheCommonPaneWidth), which is what these two
// lines can promise and a blocked wait cannot: they carry no lane label, so
// their width does not vary with the log.
const (
	betweenCallsClause  = " — between calls"
	beforeAnyCallClause = " — before any call"
)

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
//
// Every fixture in this repository has a window of 20s or less, which is
// the crossover: below it the floor governs everywhere, so no fixture can
// pin what the percentage is. Both terms are held to their values directly
// instead, by TestStallThresholdTakesTheLargerOfItsTwoTerms, and the
// percentage is shown deciding which waits survive on a window long enough
// for it to govern by TestALongWindowsPercentageDecidesWhichWaitsAreNamed.
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

// callStartedBefore reports whether any span begins before ms. It tells
// stallAnnotation's two all-idle windows apart: a window is the gap before
// any provider call only when no call has started by the time it ends, and
// is a window between calls otherwise.
//
// It asks the spans rather than testing the window's own start against
// zero, because a window beginning at 0 can still hold a call that
// completed inside it. A zero-extent span occupies no instant and so is
// never running (see model.PeakConcurrency), which leaves model.Stalls
// reporting nothing running across a stretch a call finished in. The
// UI-hook tier makes that the ordinary shape rather than an edge case:
// Terraform rounds hook timings to whole seconds, so every resource that
// refreshed in "0s" produces one, and the lane row draws it as a lit
// column the reader can see inside the window. "Before any call" over a
// window with a bar in it is a line the picture beneath it contradicts.
//
// A zero-extent span is still a CALL, which is why the other clause stays
// true of the same window: something did run in there, instantaneously.
func callStartedBefore(spans []span.Span, ms uint32) bool {
	for _, s := range spans {
		if s.StartMs < ms {
			return true
		}
	}
	return false
}

// concurrencyClause is how many spans a blocked wait left running, against
// the capacity model.Stalls measured it against: "concurrency 1 of 3".
//
// Both numbers come off the Stall itself. They are only a pair while they
// come from ONE sweep of ONE slice -- a capacity taken over some other
// slice, or over the lanes this view happens to draw, prints an "of N" the
// figure beside it was never measured against -- so the annotation reads
// the pair model gave it rather than recomputing half of it per frame.
//
// A window merged out of segments of differing depth reports the RANGE it
// covered ("concurrency 1–2 of 3"), because its floor alone would say the
// whole window ran that shallow when part of it did not. The range is
// written low to high and states no direction: model.Stalls merges on
// contiguity, which admits a window whose depth falls and rises again, so
// "2→1" would be a claim about the shape of the wait that the numbers do
// not carry. The en dash is the same one the window's own offsets are
// joined with directly beside it, rather than a second spelling of "to".
func concurrencyClause(s model.Stall) string {
	if s.MinRunning == s.MaxRunning {
		return fmt.Sprintf("concurrency %d of %d", s.MinRunning, s.Capacity)
	}
	return fmt.Sprintf("concurrency %d–%d of %d", s.MinRunning, s.MaxRunning, s.Capacity)
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
//     goes to look ("waiting on aws/1, 3.3s–20.0s — concurrency 1 of 2",
//     or "concurrency 1–2 of 3" for a window whose depth changed inside it;
//     see concurrencyClause).
//   - A window with nothing running anywhere has no lane to blame, and
//     says so ("nothing running, 5.5s–9.0s — between calls"): the time is
//     not in the providers, so tuning provider parallelism will not touch
//     it.
//   - The window BEFORE any provider call is that same finding about
//     Terraform core starting up -- loading plugins, fetching schemas --
//     and is named for it ("nothing running, 0s–3.0s — before any call").
//     It appears on nearly every capture, so wording it as a mid-plan
//     collapse would tell every reader their plan fell over at the start.
//     Which of the two an all-idle window is comes from the spans, not
//     from its own start: see callStartedBefore.
//
// The lane comes FIRST on a blocked wait, ahead of the window and the
// concurrency figure, because the line is clipped from its end
// (clipValueEnd) and the centre pane is 44 columns at the 100-column
// terminal this tool is actually run at -- narrower than at 70 or 160, both
// of which give it more. With the lane last, that pane's clip lands on the
// lane itself: it keeps the arithmetic and drops the one thing the line
// names a lane FOR. Ordering it first makes the lane structurally safe
// rather than usually safe: it is what survives at every width, whatever the
// label and the window measure, and what a narrow pane gives up is the tail
// of "concurrency 1 of 2", which is arithmetic the reader can also take off
// the bars.
//
// None of the three says "lane", and that is the point of the wording. The
// number model.Stalls carries is measured against the spans' PEAK
// CONCURRENCY, not against the rows this view draws, and per-provider
// packing (see packLanesByProvider) makes the two diverge by construction:
// a provider that has finished still occupies a row but is no longer
// capacity a later window can leave idle. "N lanes idle" would therefore
// make a claim about the screen that its number is not entitled to make,
// and could be read straight off as false -- "no stalls" beside a visibly
// blank row, or "2 lanes idle" on a five-row timeline. Concurrency is the
// right measure for "was this work or waiting", so the line names what the
// number is ("concurrency 1 of 2") rather than what a reader might count.
//
// A blocked wait states the concurrency that REMAINED and the peak it fell
// from, because a drop is only legible against what it dropped from -- "1"
// alone says nothing about whether the plan was running wide or narrow.
// Both numbers are read off the Stall rather than swept again here, which is
// what keeps the two halves of "M of N" one measurement; see
// concurrencyClause. The two all-idle windows state the finding in words
// instead (see nothingRunningClause).
//
// Every one of the three is measured over the spans the active filter
// leaves, so a narrowed timeline can report a window nothing SHOWN ran in
// across a stretch a hidden provider was working through. The all-idle
// wording is the reading that goes furthest wrong there, and it is a
// sentence with no room to qualify itself -- both all-idle lines only just
// fit the common centre pane (see betweenCallsClause) -- so the
// qualification is carried above the block instead, by timelineFilterNote.
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
	stalls, err := model.Stalls(spans, stallThresholdMs(m.timelineWallClock()))
	if err != nil {
		// timelineSpans hands spans of a single fidelity by construction,
		// the same guarantee timelineLanes leans on for PackLanes -- see
		// its own panic comment for what that rests on, and for why this is
		// a programming error to fail loudly on rather than an annotation
		// that quietly says nothing.
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

	// The lane is named with the label the LANE ROW renders, from the same
	// measurement it renders from (timelineLaneLabels) and clipped by the
	// same rule to the same width. "waiting on aws/1" points at exactly one
	// bar only if the bar carries that same text: naming a lane
	// "googleworkspace/1" beside a row reading "…workspace/1" would send
	// the reader looking for a bar that is not on screen. One measurement
	// read by both is what makes that structural rather than a rule two
	// call sites have to keep in step by hand.
	labels, labelW := m.timelineLaneLabels()
	lines := make([]string, 0, len(stalls)+1)
	for i, s := range stalls {
		// The window is the SECOND field of every line, whichever of the
		// three a line is, so the offsets can be read down the block and
		// against the axis directly above it.
		window := formatMs(uint64(s.StartMs)) + "–" + formatMs(uint64(s.EndMs))
		var line string
		switch {
		case s.Blocking < 0 && !callStartedBefore(spans, s.EndMs):
			line = nothingRunningClause + ", " + window + beforeAnyCallClause
		case s.Blocking < 0:
			line = nothingRunningClause + ", " + window + betweenCallsClause
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
			line = fmt.Sprintf("waiting on %s, %s — %s",
				clipValueForKind(labels[l], labelW, tailIdentifierColumn), window, concurrencyClause(s))
		}
		// clipValueEnd rather than clipWidth: clipWidth cuts with no marker,
		// so a pane too narrow for the whole sentence would render the
		// surviving head as though it were all there was to say. The head is
		// what the line is ordered to keep -- the lane to go and look at, or
		// that there was no lane to blame, and then the window locating it on
		// the axis above -- and the ellipsis is what tells the reader
		// something was said that they have not seen.
		lines = append(lines, clipValueEnd(line, w))
	}
	if truncated {
		lines = append(lines, clipWidth(detailCutMark, w))
	}
	return strings.Join(lines, "\n")
}
