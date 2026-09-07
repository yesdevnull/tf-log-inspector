package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Width thresholds from the spec's degradation rules: below facetInlineWidth
// the facet pane collapses to an overlay toggled with 'f'; below
// detailInlineWidth the detail pane collapses too, leaving the list
// full-width. Both are inclusive lower bounds -- a terminal exactly this
// wide still shows the pane inline.
const (
	facetInlineWidth  = 100
	detailInlineWidth = 70
)

// minFacetPaneWidth and minDetailPaneWidth are the floor facetPaneWidth and
// detailPaneWidth return: enough for the facet pane's own section headers
// ("RESOURCE TYPES" is the longest) and the detail pane's placeholder
// ("(nothing selected)"). They are real floors, applied last in
// capPaneWidth -- a pane clamped below them shows its own furniture cut in
// half, which tells the reader less than the couple of columns it hands
// back to the centre pane are worth.
const (
	minFacetPaneWidth  = 15
	minDetailPaneWidth = 19
)

// maxFacetPaneWidth and maxDetailPaneWidth cap how wide the side panes are
// allowed to grow to fit the data, mirroring
// internal/profile.maxResourceTypeColWidth: without a cap, one
// pathologically long value would claim width from the centre pane -- the
// pane every view's actual content lives in -- out of proportion to what
// showing that one value in full is worth.
const (
	maxFacetPaneWidth  = 40
	maxDetailPaneWidth = 40
)

// hugeWidth stands in for "no clipping" when measuring a line's natural,
// untruncated length: passing it to a function that clips at a given width
// (clipWidth, spanDetailLines) is cheaper than duplicating the formatting
// logic in an unclipped variant.
const hugeWidth = 1 << 30

// facetNaturalWidth is how wide the facet pane would have to be to show
// every facet value and its count in full, the same data-driven-with-a-cap
// approach internal/profile.resourceTypeColWidth uses for its resource-type
// column: a pane width fixed in advance leaves every value longer than it
// indistinguishable from its siblings and drops the count the spec requires
// for every value, so this measures the actual data instead.
//
// It walks every value of every dimension -- the resource type dimension
// alone runs to hundreds on a real capture -- and the facets it measures
// cannot change after New, so it is called there and its result kept on the
// Model rather than recomputed per frame.
func facetNaturalWidth(facets []model.Facet) int {
	width := minFacetPaneWidth
	for _, f := range facets {
		width = max(width, lipgloss.Width(facetSectionHeader(f.Name)))
		kind := facetValueKind(f.Name)
		for _, v := range f.Values {
			width = max(width, lipgloss.Width(facetValueLine(" ", v.Value, v.Count, hugeWidth, kind)))
		}
	}
	return width
}

// detailNaturalWidth is how wide the detail pane would have to be to show
// its widest line across every row of every view, not just the currently
// selected one, so the pane's width does not jump around as the selection
// or the view changes.
//
// It measures the rollup views' detail as well as each span's, because a
// rollup line is routinely the widest the pane ever shows: a resource type
// under the types view's ten-column label ("RPC calls") outruns a provider
// address under the span detail's six ("Prov").
//
// The span tier it measures is the tier a selection can actually reach,
// which timelineTierFor decides -- the same rule timelineSpans draws by, so
// the two cannot disagree about which spans this pane will ever be asked to
// describe. A UI-hook span's detail carries an Addr line (see
// spanDetailLines) that no RPC span has, and it is the widest line this
// pane draws for a log whose only tier is that one: the timeline's cursor
// selects such spans directly (selectedTimelineSpanValue), so measured off
// its rollups alone that pane would front-clip every address it drew,
// collapsing distinct module paths to identical text with terminal width to
// spare.
//
// A log carrying BOTH tiers is measured over its RPC spans alone, because
// that is all it can show: timelineSpans draws the UI tier only where there
// is no RPC span to draw instead, and row.spanIdx only ever indexes
// RPCSpans. Measuring the UI tier there would size the pane for a line no
// keypress can produce, and the columns it claimed would come out of the
// centre pane -- in the timeline, the bar area the view exists for.
//
// It formats every span in the log and rolls the log up twice, so like
// facetNaturalWidth it is measured once in New over data that cannot change
// afterwards, not per frame. It measures the UNFILTERED rollups, which is
// what makes it a load-time measurement at all: a filter can only remove
// spans, so it can offer no group key these rows do not already carry, and
// the identifier lines -- the wide ones -- are covered exactly. A filtered
// sub-total can render at most one column wider than the total it came from
// ("999ms" against "1.0s"), which clipWidth absorbs the way it absorbs any
// other overrun.
func detailNaturalWidth(l *model.Log) int {
	width := minDetailPaneWidth
	spans := l.RPCSpans
	uiTier := timelineTierFor(l) == tierUI
	if uiTier {
		spans = l.UISpans
	}
	hasContext := l.HasAddressContext()
	for _, s := range spans {
		// Attribs is parallel to RPCSpans only -- a UI-hook span's address is
		// observed rather than inferred, so it carries none, and spanDetailLines
		// never asks attributionFields about one anyway (see its Fidelity gate).
		// Looked up by entry (model.Log.AttributionForEntry) rather than by
		// position, the one supported way to do this lookup -- see its own
		// doc comment for why a positional index is not safe in general.
		var a attrib.Attribution
		if !uiTier {
			a = l.AttributionForEntry(s.Entry)
		}
		for _, line := range spanDetailLines(s, a, hasContext, hugeWidth) {
			width = max(width, lipgloss.Width(line))
		}
	}
	rollups := append(providerRows(l.RPCSpans), typeRows(l.RPCSpans, l.UISpans)...)
	for _, r := range rollups {
		for _, s := range rollupDetailSections(r.rollup, hugeWidth) {
			for _, line := range s {
				width = max(width, lipgloss.Width(line))
			}
		}
	}
	return width
}

// facetPaneWidth and detailPaneWidth turn a natural width measured at load
// into the width to render at in a terminal w columns wide. Only the
// terminal-relative clamp belongs per-frame; it is O(1).
func facetPaneWidth(natural, w int) int {
	return capPaneWidth(natural, w, minFacetPaneWidth, maxFacetPaneWidth)
}

func detailPaneWidth(natural, w int) int {
	return capPaneWidth(natural, w, minDetailPaneWidth, maxDetailPaneWidth)
}

// capPaneWidth clamps a data-driven side-pane width to at most a quarter of
// the terminal width and at most maxWidth, then back up to minWidth. A
// quarter each for facets and detail leaves the centre pane most of w --
// the centre is where the answer lives (the ranked list itself), so it must
// not lose more than the two side panes combined get.
//
// minWidth is applied LAST, so it wins over the quarter: below roughly 76
// columns a quarter is narrower than the detail pane's own placeholder, and
// a pane too narrow to label itself is worth less to the reader than the
// two or three columns it would return to the centre. That ordering is what
// makes minWidth a floor rather than a preference.
func capPaneWidth(width, terminalWidth, minWidth, maxWidth int) int {
	if quarter := terminalWidth / 4; width > quarter {
		width = quarter
	}
	if width > maxWidth {
		width = maxWidth
	}
	if width < minWidth {
		width = minWidth
	}
	return width
}

// paneSep separates adjacent panes when composing a row. It is one visible
// column (the │ itself) plus a space of breathing room on each side.
const paneSep = " │ "

// paneSepWidth is how many terminal columns paneSep costs a pane row, which
// is what the width arithmetic in renderPanes has to subtract. It is
// measured with lipgloss.Width, the same measure every other width in this
// package uses, so that the subtraction here and the padding in joinPanes
// can never disagree about one string.
//
// The distinction from a rune count is not hypothetical for this string: │
// (U+2502) is East Asian AMBIGUOUS, so how many columns it occupies is a
// property of the terminal's configuration rather than of the character.
// Measuring it once, the same way every rendered line is measured, is what
// keeps the two in step whatever that measurement turns out to be.
var paneSepWidth = lipgloss.Width(paneSep)

// defaultWidth and defaultHeight size the view before the first
// tea.WindowSizeMsg arrives -- bubbletea does not report a size until the
// program has actually started, but View must still render something
// legible if asked to before then.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

// View composes the three-pane layout: facets left, list centre, detail
// right, degrading by width per the design spec's width-degradation rules.
// The header naming the file is the top line of every frame and the footer
// the last of every frame two lines or taller; the observer-effect caveat
// between them is the part that gives way, shortening to one sentence and
// then dropping out as the terminal loses height (see loggingCaveat).
//
// None of the three is exempt from the width clip: at a narrow enough w the
// header loses the tail of the file name and the caveat is cut mid-sentence,
// the same as any other line. What they never do is collapse or trade places
// the way the panes do.
//
// The result carries no trailing newline and never exceeds h lines.
// bubbletea's renderer keeps only the LAST h lines of what View returns --
// it cannot scroll the cursor back into the terminal's scrollback buffer --
// so a view even one line too tall loses its topmost line off the top of
// the screen, and the topmost line here is the header naming the open file.
//
// h is a ceiling rather than a target, and the layouts differ on whether
// they reach it: joinPanes pads every pane it composes out to h, so the two-
// and three-pane widths fill the frame exactly, while the single-pane
// layouts -- renderPanes' sub-detailInlineWidth branch and the facet overlay
// -- clip at h without padding to it and stop wherever their content ran
// out. A short frame simply leaves blank terminal beneath it, which costs
// the reader nothing.
//
// The footer is composed onto the END of an already-trimmed frame rather
// than trimmed along with everything else, because a frame trimmed from
// the bottom takes the footer first. The footer is the only channel the
// search prompt, the "pattern not found" report and the quit hint have: at
// a height that dropped it, '/' captured every keystroke with nothing on
// screen to say so -- the query invisible, 'q' no longer quitting -- which
// is the trap the Ctrl+C handling in handleSearchKey exists to escape.
//
// m.footer(w) returns one line for the search and blocked-jump messages and
// two for the ordinary key hints, so the room reserved for it and the number
// of lines appended both follow len(footerLines) rather than assuming
// either count. Each line is clipped on its own rather than the joined
// block being clipped as one string: clipWidth measures display columns
// across a "\n" as it would across any other character, so clipping the
// two-line block whole would spend the SECOND line's width budget
// continuing from wherever the first line left off, cutting "q quit" away
// on exactly the terminals the two-line footer exists to keep it on.
//
// The footer is never given more than h-1 of those lines, so the header
// always keeps at least one -- the h == 1 guard above already refuses to
// let the footer push the header off the only line there is, and a
// two-line footer must not undo that one line later at h == 2. What gives
// way is the view-key line, not the action line: a footer trimmed to one
// line keeps its LAST line, which is the one carrying "q quit", so the
// reminder survives here as well.
func (m *Model) View() string {
	w, h := m.paneWidth(), m.height
	if h <= 0 {
		h = defaultHeight
	}
	head := styles.title.Render(clipWidth(header(m), w))
	// One line of terminal is the header's: it names the file the reader is
	// looking at, and a frame that showed only key hints could belong to any
	// file at all.
	if h == 1 {
		return head
	}

	// The caveat qualifies DURATIONS, and the help pane shows none. Leaving
	// it up spends five of a short frame's lines on numbers that are not on
	// screen, and takes them from the only content that is: at h of 12 it
	// reduces the key table to the word KEYS. Suppressing it is the same
	// "what is DRAWN decides" rule actionKeys applies through
	// detailPaneDrawn, and it withholds no qualification, because there is
	// no figure on this frame to qualify.
	var caveat []string
	if !m.showHelp {
		caveat = loggingCaveat(h)
	}
	lines := []string{head, ""}
	lines = append(lines, strings.Split(m.renderPanes(w, paneHeight(h, len(caveat))), "\n")...)
	if len(caveat) > 0 {
		lines = append(lines, "")
		for _, line := range caveat {
			lines = append(lines, styles.note.Render(clipWidth(line, w)))
		}
	}
	lines = append(lines, "")

	footerLines := strings.Split(m.footer(w), "\n")
	if avail := h - 1; len(footerLines) > avail {
		footerLines = footerLines[len(footerLines)-avail:]
	}
	if room := h - len(footerLines); len(lines) > room {
		lines = lines[:room]
	}
	for _, line := range footerLines {
		lines = append(lines, clipWidth(line, w))
	}
	return strings.Join(lines, "\n")
}

// header names the file and its span counts.
//
// While a filter is active it reports the matching count against the whole
// log's -- "12 of 3184 RPC spans" -- because every other number on screen
// is then a filtered number, and a ranked table holding twelve rows looks
// exactly like a log that only ever had twelve calls in it. With no filter
// active it reads as the plain count it always did: there is nothing to
// compare against, and "3184 of 3184" would be noise on every frame.
//
// A level-only selection narrows the raw log rather than the spans (see
// levelFacet), so its counts read "3184 of 3184" -- which is the honest
// answer to "what is this filter doing to the rankings", not a rounding of
// it.
func header(m *Model) string {
	rpc, ui := len(m.log.RPCSpans), len(m.log.UISpans)
	if !m.filterActive() {
		return fmt.Sprintf("tfli -- %s -- %d RPC spans, %d UI spans", m.name, rpc, ui)
	}
	f := m.filter()
	return fmt.Sprintf("tfli -- %s -- %d of %d RPC spans, %d of %d UI spans",
		m.name, countMatching(f, m.log.RPCSpans), rpc, countMatching(m.uiFilter(), m.log.UISpans), ui)
}

// countMatching counts the spans passing f. It exists rather than a call to
// Filter.SpansMatching because the header is rebuilt on every frame and
// SpansMatching materialises a slice: on a real capture that is thousands
// of spans copied per keystroke, for two numbers.
func countMatching(f model.Filter, spans []span.Span) int {
	n := 0
	for _, s := range spans {
		if f.MatchSpan(s) {
			n++
		}
	}
	return n
}

// footer is the line beneath the panes. It is the search prompt while a
// query is being typed -- '/' captures every key, so the prompt is what
// tells the user their keyboard has been taken over and shows them what
// they have typed -- and the result of a search that found nothing, which
// otherwise looks exactly like a search that matched the entry already on
// screen. Otherwise it is the key hints.
//
// Both search states belong to the raw log, the only view '/' searches, so
// both are shown only there: a miss reported over the calls table would
// describe a search whose result is not on screen.
//
// A blocked jump is reported wherever it happened, which is never the raw
// log: jumpToSpan refuses the jump precisely so the view does NOT change,
// leaving the report beneath the table the user pressed Enter over.
func (m *Model) footer(w int) string {
	// The help is modal in Update, so it is modal here too. The raw log's
	// search report below describes a view the help is not drawing, and it
	// REPLACES the whole footer -- so without this a reader who opened the
	// help after a failed search sees "pattern not found" where the only
	// two keys that still work should be, on a screen where every other key
	// is inert. At a height that also cuts the key table down to its title,
	// nothing on the frame names a working key at all. That is the trap
	// View's own comment says the footer exists to prevent, and the reason
	// renderHelp is allowed to cut without a mark.
	if m.showHelp {
		return styleHintKeys(m.keyHints(w))
	}
	if m.blockedJump {
		return styles.alert.Render(jumpBlockedNote)
	}
	if m.view == ViewRawLog {
		switch {
		// The query is left unstyled. It is what the reader is typing, and
		// the one line on the frame whose content they chose: giving it a
		// treatment of this interface's own would make their own text look
		// like part of the furniture.
		case m.raw.searching:
			return "/" + m.raw.query
		case m.raw.notFound:
			return styles.alert.Render("/" + m.raw.lastQuery + "  pattern not found")
		}
	}
	return styleHintKeys(m.keyHints(w))
}

// styleHintKeys accents the KEY at the head of every hint on a composed hint
// line, leaving the words that describe it plain. A reader scanning the
// footer is looking for which key to press, and accenting the whole hint
// would accent the entire line -- which marks nothing at all.
//
// It works on the composed line rather than on the hints before they are
// joined, so that viewKeyHints and actionKeys keep returning plain text.
// Those two are what the footer's width budget is measured against, at
// several widths and in every view, and a measurement taken over escape
// sequences is a measurement of something other than what reaches the
// screen.
//
// Every hint in this footer is one key, a space, and the words for what it
// does -- including the two whose words contain a space of their own ("6 raw
// log", "Esc clear"), which is why the split is at the FIRST space and the
// remainder is left whole. A hint carrying no space is left alone rather
// than accented entire.
func styleHintKeys(line string) string {
	lines := strings.Split(line, "\n")
	for i, ln := range lines {
		hints := strings.Split(ln, hintSep)
		for j, h := range hints {
			key, rest, ok := strings.Cut(h, " ")
			if !ok {
				continue
			}
			hints[j] = styles.key.Render(key) + " " + rest
		}
		lines[i] = strings.Join(hints, hintSep)
	}
	return strings.Join(lines, "\n")
}

// hintSep separates adjacent hints on a footer line. Two spaces, so that the
// single space inside a hint reads as binding its key to its words rather
// than as separating one hint from the next.
const hintSep = "  "

// jumpBlockedNote is what the footer says when Enter refused to jump to a
// call's log entry because the active filter hides it. Landing on a blank
// pane, or on some other call's lines further down the log, is
// indistinguishable from a jump that worked, so the refusal is stated and
// the key that lifts it is named.
const jumpBlockedNote = "target entry hidden by the active filter -- Esc clears it"

// keyHints is the footer's two hint lines: which number keys switch views,
// then which keys act on what is on screen.
//
// They are two lines rather than one because a single composed line runs to
// 123 display columns at its widest, and the line is clipped from its END --
// so the tail it loses is "q quit", the one key a user must never lose sight
// of. The widest is the CALLS view, which drops only its own key from the
// view-key group and carries both the sort hint and the open hint on the
// action line; no other view's composed line exceeds 120. Splitting them
// lets both groups keep their full names at every width this interface
// renders at, and costs one line of pane height. Each line is still clipped
// independently, because a 60-column terminal cannot show 62 columns of
// action keys however they are arranged.
func (m *Model) keyHints(w int) string {
	// While the help is open only three keys do anything: ? and Esc close
	// it, q quits (see Update). The footer names two of them. Esc is left
	// out because the meaning it is advertised under here -- "Esc clear" --
	// describes clearing filters, which is not what it does in this state;
	// the key table above says what it does instead. Leaving the ordinary
	// hints up would offer ⏎, s, f and the number keys over a screen where
	// none of them act -- the defect this group removes wherever it finds
	// it, at its most obvious: the key table saying what each key does is on
	// screen at the time.
	//
	// It is not a loss of guidance either. Everything the footer would have
	// abbreviated is spelled out in the pane above it, so what is left to
	// say is how to leave.
	if m.showHelp {
		return clipWidth(helpCloseHint, w) + "\n" + clipWidth(quitHint, w)
	}
	return clipWidth(viewKeyHints(m.view), w) + "\n" + clipWidth(m.actionKeys(w), w)
}

// The help and quit hints, named because the footer composes them two ways:
// into the ordinary hint groups, and alone while the help is open.
//
// helpHint rides the VIEW-KEY line rather than the action line: the action
// line reaches 70 columns, the narrowest width that draws every pane, while
// the view-key group is 51 at its widest, so the room is here. It is the
// group ? belongs to besides: help is a screen the key takes you to, like
// the number keys beside it.
const (
	helpHint      = "? help"
	helpCloseHint = "? close"
	quitHint      = "q quit"
)

// openHint names the key that jumps from the selected row to the log entry
// that closed its span.
const openHint = "⏎ open"

// spanCursorHint names the keys that step the timeline's cursor along the
// selected lane (←/→, and h/l beside them).
//
// It is six display columns -- the arrows are written as one ↔ rather than
// as "←→", and the noun is the pane's own word for what is selected (SPAN
// DETAIL) -- because the action line fitted a 70-column terminal exactly at
// 62 columns and the line is clipped from its END, where "q quit" is. Six
// columns plus the two-space separator lands it on 70 exactly; seven would
// cost the quit hint its last letter at detailInlineWidth, the narrowest
// terminal that still draws the detail pane these keys step through --
// renderPanes collapses the facet pane well above that, at facetInlineWidth,
// so the three-pane layout is never the width under pressure here.
const spanCursorHint = "↔ span"

// sortHint names the key that moves the sort to the next column of the
// table on screen. It is shown only where there IS a table -- the two rollup
// views and the calls view -- for the same reason the open hint is shown
// only over a row that opens: the timeline and the raw log have no columns
// for a sort to reorder, so s does nothing there.
const sortHint = "s sort"

// actionKeys is the hint group for the keys that DO something to what is on
// screen, as opposed to the ones that change which view is on screen. It is
// 54 display columns in the raw log, which carries none of its conditional
// hints; 62 in the two rollup views, which carry the sort hint; and 70 in
// both the calls view, which carries the sort and open hints, and the
// timeline at a width that draws the detail pane, which carries the open and
// span hints instead. Every binding added to it pushes "q quit" closer to
// the edge a narrow terminal cuts from, which is what keeps it this terse,
// and TestNoViewsActionLineOutgrowsTheNarrowestThreePaneWidth is what holds
// the widest of them inside the budget.
//
// The open hint is shown only where Enter has something to open: a call
// row's own span in the table views, or the timeline's selected span, which
// has no row of its own. A rollup row stands for a group and resolves to no
// single span, so in the two rollup views the key returns immediately, and
// in the raw log there is no row or span to press it over at all.
// Advertising a key that does nothing is the defect this package removes
// wherever it finds it, and this hint stood in three of the four views
// before it was added.
//
// The span hint is the SAME rule read the other way: a working key
// advertised nowhere. ←/→ (and h/l) are the only way to reach any span in a
// lane but the first, and they choose what the detail pane describes and
// what Enter jumps to, so a reader who does not know they exist sees one
// span of the hundreds a real lane holds. It is shown where those keys
// actually step -- the timeline, on a lane holding more than one span (see
// selectedLaneStepsThroughSpans) -- and nowhere else, since no other view
// has a within-lane cursor for them to move.
//
// The span hint is the one hint here that also asks what the FRAME
// contains, and that is the same rule again rather than a width budget. The
// within-lane cursor's only visible effect is in the detail pane:
// renderTimeline states the limit -- the bar draws no marker for it,
// because a packed lane can put several spans in one column -- so wherever
// that pane is absent, ←/→ move a selection nothing on screen reflects. A
// hint there names a key that does nothing observable, which is exactly
// what this group refuses. It costs nothing to drop: the keys still drive
// Enter's jump target wherever they are bound, and a reader who cannot see
// what they select has no use for being told they exist. That it drops the
// line from 70 columns to 62 at a 60-column terminal -- moving the clip off
// the middle of "Esc clear", though "q quit" is still cut short there, to
// "q qu" -- is the consequence, not the reason.
//
// It asks detailPaneDrawn rather than the width, because width alone gets
// the answer wrong in the band between detailInlineWidth and
// facetInlineWidth: an open facet overlay replaces the whole pane row
// there, leaving a frame with no timeline and no detail pane while the
// terminal is still wide enough for both. In that state the keys are inert
// as well, focus having moved to the facet pane.
//
// Each hint asks a predicate built on the same state its key handler reads
// -- selectedRowOpens through jumpTarget, selectedLaneStepsThroughSpans
// through timelineLanes -- so the footer cannot come to advertise a key the
// handler has stopped acting on. None asks which pane has focus: Enter is
// inert from the detail pane the way space is inert outside the facet pane,
// and "␣ facet" is shown regardless for the same reason -- a hint that
// flickered as Tab moved would describe the keyboard rather than the view.
// The width is different in kind from focus: it decides what is DRAWN, and
// a pane that is not drawn is not somewhere the reader can look.
func (m *Model) actionKeys(w int) string {
	keys := []string{"⇥ pane", "␣ facet"}
	if m.selectedRowOpens() {
		keys = append(keys, openHint)
	}
	if m.detailPaneDrawn(w) && m.selectedLaneStepsThroughSpans() {
		keys = append(keys, spanCursorHint)
	}
	if _, sortable := tables[m.view]; sortable && len(m.rows()) > 0 {
		keys = append(keys, sortHint)
	}
	return strings.Join(append(keys, "f facets", "/ search", "Esc clear", quitHint), hintSep)
}

// viewKeyHints is the hint group naming the number keys that switch views,
// and the view each one switches to. It carries the help hint at its end as
// well: ? takes the reader to a screen the way the number keys do, and this
// is the line with room for it (see helpHint).
//
// The view the user is ALREADY in is left out. Its key is a no-op -- Update
// only acts on a number key that names a different view -- and advertising a
// key that does nothing is the defect this package removes wherever it finds
// it. Leaving it out is also what keeps the group inside the footer's width
// budget at 100 columns, and nothing is lost by it: the centre pane's title
// names the current view already.
func viewKeyHints(v View) string {
	hints := make([]string, 0, len(views))
	for _, b := range views {
		if b.view != v {
			hints = append(hints, b.key+" "+b.name)
		}
	}
	return strings.Join(append(hints, helpHint), hintSep)
}

// frameFixedLines is what a frame spends on everything but the pane row and
// the caveat block: the header, the blank line beneath it, the blank line
// above the footer, and the footer's two hint lines.
const frameFixedLines = 5

// paneHeight is how many lines the pane row itself gets in a frame h lines
// tall carrying caveatLines lines of caveat. The caveat block costs one line
// more than its text, for the blank line above it, and costs nothing at all
// when there is no caveat to separate.
//
// It never goes below 1: a terminal too short to show everything still shows
// something rather than an empty pane, and View then trims the surplus out
// from ABOVE its footer.
func paneHeight(h, caveatLines int) int {
	block := 0
	if caveatLines > 0 {
		block = caveatLines + 1
	}
	if paneH := h - frameFixedLines - block; paneH > 0 {
		return paneH
	}
	return 1
}

// fullLoggingCaveat states that every duration this interface renders was
// measured under logging. Terraform re-logs each line of a provider's stderr
// through its own logger, so a provider that dumps HTTP bodies at DEBUG pays
// that cost per line: four captures of one workspace measured 24.1s with no
// logging enabled against 522.2s with debug plus provider TRACE. A reader
// who mistook these figures for wall-clock truth would be optimising time
// that does not exist without the log, so the caveat travels with every
// rendered duration rather than living only in documentation. Its longest
// line is 59 display columns; below that it is clipped mid-sentence like any
// other line, since no width floor protects it. shortLoggingCaveat is the
// answer to a frame short of HEIGHT, not of width.
var fullLoggingCaveat = []string{
	"Durations here are measured under logging, which is not",
	"free: one workspace planned in 24.1s unlogged and 522.2s",
	"with debug plus provider TRACE. Rankings hold, since every",
	"span paid the same cost, but absolute times do not transfer",
	"to an unlogged run.",
}

// shortLoggingCaveat is the same warning in one whole sentence, for a frame
// with no room for the full text. It is a rewrite rather than the first line
// of fullLoggingCaveat, because a caveat cut off mid-sentence reads as a
// rendering fault rather than as a warning that was deliberately shortened.
const shortLoggingCaveat = "Durations measured under logging: only rankings transfer."

// loggingCaveat is the caveat's lines for a frame h lines tall: the full
// text where it fits, one sentence where it does not, and nothing at all
// below the height where even one line would cost the footer.
//
// The footer is budgeted ahead of the caveat, not after it. The caveat is a
// fixed warning a reader can take in once; the footer carries live state --
// the search query being typed, the miss report, and the reminder that 'q'
// quits -- that exists nowhere else on screen, so it is the one line that
// must survive a short terminal.
func loggingCaveat(h int) []string {
	// Each bound is frameFixedLines, plus one line for the pane row, plus
	// the caveat block: the caveat's own lines and the blank above them.
	switch {
	case h >= frameFixedLines+1+len(fullLoggingCaveat)+1:
		return fullLoggingCaveat
	case h >= frameFixedLines+1+1+1:
		return []string{shortLoggingCaveat}
	default:
		return nil
	}
}

// renderPanes composes the pane row for width w and height h, applying the
// spec's width degradation:
//
//   - w >= facetInlineWidth: facets, list and detail all shown side by side.
//   - detailInlineWidth <= w < facetInlineWidth: facets collapse to an
//     overlay, toggled with 'f' (showFacetOverlay), that replaces the whole
//     row when open; otherwise list and detail are shown side by side.
//   - w < detailInlineWidth: detail collapses too, leaving the list
//     full-width. The facet overlay still works the same way here.
func (m *Model) renderPanes(w, h int) string {
	// The help takes the whole pane row, at every width. It is not a fourth
	// pane and has no focus of its own: while it is open every key but the
	// three that leave it is inert, so there is nothing for Tab to reach.
	if m.showHelp {
		return renderHelp(w, h)
	}
	if m.facetOverlayShowing(w) {
		return m.renderFacets(w, h)
	}
	switch {
	case w >= facetInlineWidth:
		facetW := facetPaneWidth(m.facetPaneNatural, w)
		detailW := detailPaneWidth(m.detailPaneNatural, w)
		listW := w - facetW - detailW - 2*paneSepWidth
		return joinPanes(h,
			pane{m.renderFacets(facetW, h), facetW},
			pane{m.renderCentre(listW, h), listW},
			pane{m.renderDetail(detailW, h), detailW},
		)
	case m.detailPaneDrawn(w):
		detailW := detailPaneWidth(m.detailPaneNatural, w)
		listW := w - detailW - paneSepWidth
		return joinPanes(h,
			pane{m.renderCentre(listW, h), listW},
			pane{m.renderDetail(detailW, h), detailW},
		)
	default:
		// The centre pane is the whole row here, with no joinPanes beneath
		// it to hold it to h lines. renderRawLog renders its first visible
		// entry from the top down whatever that entry's height -- a
		// provider's multi-line HTTP body dump is the realistic case -- so
		// without this clamp that overflow pushes the caveat out of View's
		// frame, on the one layout that has no other pane to absorb it.
		return clipLines(m.renderCentre(w, h), h)
	}
}

// facetOverlayShowing reports whether the facet pane is currently taking
// the WHOLE pane row: the flag is set (see toggleFacetFocus) and the
// terminal is narrow enough that the facets have no column of their own to
// live in. It is one predicate rather than the pair of comparisons repeated
// at each site, because everything that has to know what the frame contains
// -- what renderPanes draws, which panes Tab can reach, which keys the
// footer advertises -- must read the same answer.
func (m *Model) facetOverlayShowing(w int) bool {
	return w < facetInlineWidth && m.showFacetOverlay
}

// detailPaneDrawn reports whether the frame at width w actually has a
// detail pane in it. Width alone does not answer that: between
// detailInlineWidth and facetInlineWidth an open facet overlay replaces the
// entire pane row, so a terminal wide enough for the pane still shows none.
//
// The footer asks it for the same reason renderPanes branches on it. The
// within-lane cursor's only visible effect is in that pane, so a hint for
// ←/→ shown over a frame that has neither the pane nor the timeline names a
// key with nothing on screen to reflect it -- and with the overlay open
// those keys are inert besides, since Update binds them to the list pane.
// Two copies of the rule are what let the footer and the renderer drift
// apart, so there is one.
func (m *Model) detailPaneDrawn(w int) bool {
	return !m.facetOverlayShowing(w) && w >= detailInlineWidth
}

// clipLines truncates s to at most h lines, the same bound joinPanes applies
// to every pane it composes. It is the single-pane equivalent: a pane row is
// h lines tall however many lines the pane inside it produced.
func clipLines(s string, h int) string {
	if h <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= h {
		return s
	}
	return strings.Join(lines[:h], "\n")
}

// paneWidth is the width the pane row is composed at: the terminal's own
// width once bubbletea has reported one, and defaultWidth before the first
// tea.WindowSizeMsg arrives. Key handling and rendering both go through it,
// so the two cannot disagree about which panes the frame has.
func (m *Model) paneWidth() int {
	if m.width <= 0 {
		return defaultWidth
	}
	return m.width
}

// focusablePanes lists the panes renderPanes actually draws at width w, in
// Tab's cycle order. It is derived from the same width decision renderPanes
// makes, rather than repeating those comparisons, so the two cannot drift.
//
// Only a drawn pane can hold focus. Focus on a collapsed pane is focus the
// user cannot see, and the keys bound there are not inert: space toggles a
// facet, which changes the ranked numbers this tool exists to report, with
// nothing on screen to say why the rows moved.
func (m *Model) focusablePanes(w int) []Pane {
	if m.facetOverlayShowing(w) {
		// The overlay replaces the whole pane row, so it is the only pane
		// on screen and the only one Tab can reach.
		return []Pane{PaneFacets}
	}
	panes := make([]Pane, 0, paneCount)
	if w >= facetInlineWidth {
		panes = append(panes, PaneFacets)
	}
	panes = append(panes, PaneList)
	if m.detailPaneDrawn(w) {
		panes = append(panes, PaneDetail)
	}
	return panes
}

// renderCentre renders the centre pane: its title, then its content for the
// active view. ViewRawLog and ViewTimeline are not one of renderList's
// rollup/call tables -- the raw log renders directly from m.log.Entries via
// renderRawLog, and the timeline renders from m.timelineSpans() and
// model.PackLanes via renderTimeline -- so both are dispatched separately
// here rather than inside renderList itself.
//
// The title is what names the view. Without it the centre pane was the only
// one of the three carrying no label at all, so the pane holding a providers
// rollup was indistinguishable, at a glance, from the facet pane's list of
// providers to filter by -- and nothing on screen said the interface had
// other views to switch to. It comes FIRST, so a pane too short for its
// content still says what that content was, and it is clipped from its end
// like the other panes' titles: a title is prose, told apart by its head.
//
// The title is not focus-marked, matching the facet pane rather than the
// detail pane: both of those have a cursor of their own to carry focus (the
// selected row here, the highlighted value there), and a second reverse-video
// bar on the title would say nothing the row cursor does not already say.
// The detail pane marks its title precisely because it has no cursor.
func (m *Model) renderCentre(w, h int) string {
	if h <= 0 {
		return ""
	}
	title := clipWidth(viewTitle(m.view), w)
	// The timeline's title names the TIER it is drawing (see timelineTitle),
	// which views' own static "TIMELINE" cannot: that table has no notion of
	// the current log, and the tier is a property of it.
	if m.view == ViewTimeline {
		title = clipWidth(m.timelineTitle(), w)
	}
	title = styles.title.Render(title)
	if h == 1 {
		return title
	}
	switch m.view {
	case ViewRawLog:
		return title + "\n" + m.renderRawLog(w, h-1)
	case ViewTimeline:
		return title + "\n" + m.renderTimeline(w, h-1)
	default:
		return title + "\n" + m.renderList(w, h-1)
	}
}

// pane is one column of a composed pane row: its rendered content and the
// width it was rendered at.
type pane struct {
	content string
	width   int
}

// joinPanes composes panes side by side into one h-line block. Each pane's
// lines are right-padded to its declared width so every pane starts at the
// same column on every row, and each pane's line list is padded with blank
// lines up to h so a shorter pane (an empty detail pane, say) does not
// shrink the row it appears in.
func joinPanes(h int, panes ...pane) string {
	if h <= 0 {
		return ""
	}
	columns := make([][]string, len(panes))
	for i, p := range panes {
		var lines []string
		if p.content != "" {
			lines = strings.Split(p.content, "\n")
		}
		for len(lines) < h {
			lines = append(lines, "")
		}
		for j, ln := range lines {
			lines[j] = padRight(clipWidth(ln, p.width), p.width)
		}
		columns[i] = lines
	}

	// The separator is styled here rather than held as a package-level
	// value because Run may rebuild the theme without colour, and it does so
	// after this package's variables are initialised.
	sep := styles.chrome.Render(paneSep)
	rows := make([]string, h)
	for r := 0; r < h; r++ {
		cells := make([]string, len(panes))
		for i := range panes {
			cells[i] = columns[i][r]
		}
		rows[r] = strings.Join(cells, sep)
	}
	return strings.Join(rows, "\n")
}

// paneSection is one block of a pane's body: lines a short pane
// keeps or drops TOGETHER.
//
// The unit exists because the pane's lines are not independent of each
// other. A heading with nothing beneath it reads as a pane that failed to
// render, and a labelled figure whose label survived while its number was
// cut away reads as a complete answer to the question the label asks --
// which is worse, because nothing on screen says the number is missing.
type paneSection []string

// renderDetail renders the detail pane: what the selected row stands for,
// at most w columns wide and h lines tall. The spec has the pane persist
// across the views -- "there is one filter state and many projections of
// it" -- so every view with rows describes the row its cursor is on. A call
// row is one span and gets that span's fields; a rollup row stands for a
// group and gets the group's aggregate and the slowest call behind it.
//
// The placeholder is what is left when there is genuinely nothing to
// describe: a view with no rows at all (ViewRawLog, whose rows() is nil), or
// a selection index outside the rows there are.
//
// The title and the body come from one dispatch (selectedDetail) and the
// height budget is applied by another (fitPaneSections); all this adds is
// the focus marking, which belongs to neither.
func (m *Model) renderDetail(w, h int) string {
	if h <= 0 {
		return ""
	}
	title, sections := m.selectedDetail(w)
	// The detail pane has no cursor of its own to mark, so its title
	// carries the focus instead: Tab's third stop would otherwise be
	// invisible, leaving the user no way to tell that the keyboard had
	// moved off the list.
	// Focused, the title becomes the cursor bar and takes reverse video
	// instead of the title style: the two cannot both apply, because the
	// bar's reverse video ends at the first reset inside what it wraps
	// (see cursorBar).
	line := clipWidth(title, w)
	if m.pane == PaneDetail {
		line = cursorBar(line, w, true)
	} else {
		line = styles.title.Render(line)
	}
	return strings.Join(fitPaneSections(line, sections, w, h), "\n")
}

// The detail pane's titles, one per KIND of row it can be describing.
//
// The title is derived from the row rather than fixed, because it is a
// claim ABOUT what is beneath it. Headed SPAN DETAIL over a group's
// aggregate, the pane presents "Total 742.4s" -- 874 calls added together
// -- as though it were one span's figure: a plausible number attached to
// the wrong noun, which is this tool's worst output class. It is the same
// defect the centre pane's own view title exists to prevent, one pane to
// the left, and the same argument slowestHeading already makes one level
// down.
const (
	spanDetailTitle   = "SPAN DETAIL"
	rollupDetailTitle = "GROUP DETAIL"
	// noSelectionTitle heads a pane with no row to describe, so it claims
	// nothing about a span or a group: there is neither.
	noSelectionTitle = "DETAIL"
)

// selectedDetail is the detail pane's title and body sections for whatever
// the centre pane's cursor is on, derived TOGETHER from one dispatch so the
// heading and what it heads cannot disagree.
//
// The timeline has no rows of its own (see rows()), so it is dispatched on
// selectedTimelineSpanValue rather than a row: a hit there is always a
// single span, since a lane packs spans rather than groups of them, so
// there is no rollup case to consider.
//
// Everywhere else, the span branch dispatches on spanForRow -- built on the
// same row.isCall predicate Enter asks before jumping -- rather than on the
// absence of a rollup. A row that is neither a call nor a rollup cannot be
// built (see callRow and rollupRow), and is reported here as nothing to
// describe rather than indexed into RPCSpans on the strength of a spanIdx
// that says it names no span: a degraded pane instead of a panic mid-frame
// inside the alt screen.
func (m *Model) selectedDetail(w int) (string, []paneSection) {
	nothing := []paneSection{{styles.note.Render(clipWidth(noSelectionNote, w))}}
	hasContext := m.log.HasAddressContext()
	if m.view == ViewTimeline {
		if s, ok := m.selectedTimelineSpanValue(); ok {
			return spanDetailTitle, []paneSection{spanDetailLines(s, m.log.AttributionForEntry(s.Entry), hasContext, w)}
		}
		return noSelectionTitle, nothing
	}
	r, ok := m.selectedRow()
	if !ok {
		return noSelectionTitle, nothing
	}
	if s, ok := m.spanForRow(r); ok {
		return spanDetailTitle, []paneSection{spanDetailLines(s, m.log.AttributionForEntry(s.Entry), hasContext, w)}
	}
	if r.rollup != nil {
		return rollupDetailTitle, rollupDetailSections(r.rollup, w)
	}
	return noSelectionTitle, nothing
}

// detailCutMark is the last line of a pane that had more to show than h
// lines to show it in -- the detail pane, the help, the timeline's notes
// and stall list. The pane marks a value clipped for WIDTH with
// an ellipsis (see clipValueFront); a pane clipped for HEIGHT that marked
// nothing would leave the two cuts telling the reader different amounts
// about themselves, and the height cut is the one that can remove a whole
// figure rather than the tail of one.
const detailCutMark = "…"

// fitPaneSections composes a title and body sections into at most h
// lines, marking any cut with detailCutMark.
//
// The FIRST section is always appended, so a pane with room for anything at
// all shows as much of its most important block as fits, clipped by line as
// a last resort -- the selected row's own fields in the detail pane, the
// number keys in the help. Every LATER section is kept or dropped whole --
// the height is checked before it is appended, not after -- so a pane cannot
// end on a heading with nothing under it, or on a figure's label with the
// figure gone.
//
// The title survives every cut, since it names the pane and carries its
// focus (see renderDetail). At h of 1 that leaves no line for the mark, and
// a one-line pane is the one case where a cut goes unmarked.
func fitPaneSections(title string, sections []paneSection, w, h int) []string {
	lines := []string{title}
	cut := false
	for i, s := range sections {
		if i > 0 && len(lines)+len(s) > h {
			cut = true
			break
		}
		lines = append(lines, s...)
	}
	if cut {
		lines = append(lines, clipWidth(detailCutMark, w))
	}
	if h > 0 && len(lines) > h {
		lines = lines[:h]
		if h > 1 {
			lines[h-1] = clipWidth(detailCutMark, w)
		}
	}
	return lines
}

// spanDetailLines formats one span's detail fields: RPC, resource type,
// provider and duration always; its resource address in addition when it is
// a UI-hook span; and its ATTRIBUTION in addition otherwise -- which
// resource this call belongs to, when the log can answer that at all (see
// attributionFields). Span.Address is populated only for spans
// FidelityUIReported -- an RPC-tier span never carries one -- so the address
// branch gates on Fidelity, not just on Address being non-empty, to document
// that this is a property of the span's kind rather than an incidental
// absence.
//
// a is the span's own attribution and hasContext says whether the LOG
// carries any address context at all -- two different facts a caller must
// not blur together before calling this: "this log cannot answer which
// resource this call belongs to" is not "this log can, and did not for this
// call" (see noAddressContextValue and unattributedValue).
//
// The UI-hook branch is reached through the timeline, whose cursor can
// select an individual span from m.log.UISpans (via
// selectedTimelineSpanValue) whenever the log has no RPC spans to draw
// instead (see timelineSpans). row.spanIdx, by contrast, only ever indexes
// m.log.RPCSpans -- a pre-existing constraint this package does not
// redefine for the table views -- so a table row can never carry a
// UI-hook span. TestSpanDetailLinesShowsAddressForUIHookSpans keeps its own
// direct unit coverage of the formatting regardless of which caller reaches
// it.
//
// Start appears only for a span whose start was forced to zero because its
// reported duration exceeded its offset from the log's first entry
// (span.Span.StartClamped). It sits directly under Dur because Dur is what
// it qualifies: the timeline draws such a span from column 0 with a length
// of EndMs, so the pane would otherwise read "Dur 45.0s" beside a
// three-second bar with nothing accounting for the difference. It is prose
// rather than an identifier and is told apart by its head, so it end-clips
// (see columnKind).
//
// RPC, Type, Prov and Addr are all identifier values, and all four go
// through clipIdentifierField, each clipped from the end its kind allows
// (see columnKind): the same value clipped here, in the facet pane and in
// the calls table is then clipped the same way and carries the same marker.
// Type, Prov and Addr front-clip, so two resource types, providers or
// addresses sharing a long prefix -- ".../hashicorp/azuread" and
// ".../azurerm" -- do not both clip down to their identical shared head.
// RPC end-clips, since it names one of a short, closed set of
// plugin-protocol methods that share long suffixes and diverge within their
// first few characters -- the same reasoning
// internal/profile.actionColWidth uses for its own closed-vocabulary action
// column. Dur is a formatted number and is never long enough to need either
// treatment.
//
// Type closes a standing gap between this pane and callColumns, which has
// carried a resource-type column since the calls table existed: without it
// the pane showed less about the selected call than the row describing it.
func spanDetailLines(s span.Span, a attrib.Attribution, hasContext bool, w int) []string {
	fields := []detailField{
		{label: "RPC", value: s.RPC, kind: headIdentifierColumn},
		{label: "Type", value: s.ResourceType, kind: tailIdentifierColumn},
		{label: "Prov", value: s.Provider, kind: tailIdentifierColumn},
		{label: "Dur", value: formatMs(uint64(s.DurationMs)), kind: numericColumn},
	}
	if s.StartClamped {
		fields = append(fields, detailField{label: "Start", value: clampedStartValue, kind: headIdentifierColumn})
	}
	if s.Fidelity == span.FidelityUIReported {
		// An observed address, stated by the log rather than inferred from
		// it, so it carries no confidence marker.
		fields = append(fields, detailField{label: "Addr", value: s.Address, kind: tailIdentifierColumn})
		return detailFieldLines(fields, w)
	}
	return detailFieldLines(append(fields, attributionFields(a, hasContext)...), w)
}

// attributionFields renders the inferred half of a span's identity. Every
// value here is inference and is marked as such: an Ambiguous span reports
// how many candidates there were and names none of them, because naming one
// of several equally plausible resources asserts what the evidence does not
// support. The resource name and the module path are separate fields rather
// than one address line because the pane is at most maxDetailPaneWidth
// columns and a real address routinely exceeds that; splitting them lets
// the NAME -- the identifying part, kept whole by headIdentifierColumn --
// survive a narrow pane while the module path, front-clipped by
// tailIdentifierColumn to keep the tail that distinguishes sibling modules,
// gives way instead.
func attributionFields(a attrib.Attribution, hasContext bool) []detailField {
	if !hasContext {
		return []detailField{{label: "Res", value: noAddressContextValue, kind: headIdentifierColumn}}
	}
	switch a.Confidence {
	case attrib.Ambiguous:
		return []detailField{
			{label: "Res", value: fmt.Sprintf("%d candidates", a.Candidates), kind: headIdentifierColumn},
			{label: "Attr", value: a.Confidence.String(), kind: headIdentifierColumn},
		}
	case attrib.Unattributed:
		return []detailField{{label: "Res", value: unattributedValue, kind: headIdentifierColumn}}
	}

	name := a.Name
	if a.IsData {
		// Terraform's own address syntax puts "data." before the type, not
		// the name -- but the resource type has its own field elsewhere in
		// the pane (spanDetailLines' Type), and this Res field is the only
		// place the name itself appears, so the prefix goes here instead.
		// a.IsData rather than a prefix check on a.Address is what keeps
		// this correct under a module: "data." sits after the module
		// segments there, not at the address string's front.
		name = "data." + name
	}
	if a.Key != "" {
		// a.Key already carries whatever bracket syntax it needs -- bare
		// for a count key, quoted for a for_each key -- decided at decode
		// time (attrib.decodeKey), where the raw JSON's own type is still
		// known. There is nothing left for this concatenation to decide.
		name += "[" + a.Key + "]"
	}
	fields := []detailField{
		{label: "Res", value: name, kind: headIdentifierColumn},
	}
	if a.Module != "" {
		fields = append(fields, detailField{label: "Mod", value: a.Module, kind: tailIdentifierColumn})
	}
	return append(fields, detailField{label: "Attr", value: a.Confidence.String(), kind: headIdentifierColumn})
}

// noAddressContextValue and unattributedValue say two different things. The
// first is a fact about the LOG -- it carries no terraform.ui stream, so no
// call in it can be attributed. The second is a fact about this CALL -- the
// log could answer the question and did not answer it here.
const (
	noAddressContextValue = "no address context in log"
	unattributedValue     = "not matched"
)

// clampedStartValue is what the detail pane puts under Start for a span
// whose start was clamped. It states the clamp and the value it was clamped
// to, in --profile's own words ("has its start clamped to zero"), so a
// reader who has seen the note --profile prints for the same spans
// recognises this as the same finding rather than a second one.
const clampedStartValue = "clamped to zero"

// noSelectionNote is what the detail pane says when there is no selected row
// to describe -- a view with no rows at all, or a selection outside the rows
// there are. It describes the absence of a SELECTION rather than asking for
// a call to be picked: in the raw log, the one view that reaches it, there
// is no row to pick. It is 18 display columns, so it fits the pane's own
// floor of minDetailPaneWidth.
const noSelectionNote = "(nothing selected)"

// slowestHeading labels the one call behind a rollup row's group that took
// longest. Without it the RPC name beside it reads as the ROW's own, which
// for a row totalling several calls is a wrong answer rather than a missing
// one.
const slowestHeading = "Slowest"

// noRPCCallsNote is what stands under that label for a group with no
// RPC-tier span -- a resource type Terraform's UI hooks reported and the
// provider protocol never did, which testdata/two-tier.log's local_file is.
// The absence is stated rather than left as a dropped line: a line that is
// simply absent is indistinguishable from a pane that failed to render it.
const noRPCCallsNote = "no RPC-tier calls"

// rollupDetailSections formats a rollup row's detail: the group's aggregate,
// then the one call behind it that took longest, separated by a blank line.
//
// They are two SECTIONS because a short pane keeps or drops the second
// whole (see fitPaneSections), and that ORDER is load-bearing: what a
// pane too short for both keeps is the summary of the row the cursor is
// actually on, and what it gives up is the one call behind it.
//
// A nil rollup gets no sections, matching slowestOf one level down rather
// than contradicting it: a row that is not a rollup describes no group, and
// the two functions the pane is built from must agree on what a missing
// group means. detailNaturalWidth hands it every rollup row in the log
// without asking, and a row can be built with no detail at all.
func rollupDetailSections(d *rollupDetail, w int) []paneSection {
	if d == nil {
		return nil
	}
	return []paneSection{
		detailFieldLines(d.aggregate, w),
		{"", slowestLine(d.slowest, w)},
	}
}

// slowestLine names the group's longest RPC-tier call, or states that the
// group has none.
//
// It names the call and stops there. The call's DURATION is by construction
// the same number as the aggregate's Max ("RPC max" in the types view) two
// lines above it -- model.RollupBy's MaxMs and groupRPCSpans' slowest run
// over the same spans under the same key, so they cannot differ -- and one
// figure shown twice under two labels invites the reader to treat them as
// two independent measurements. Which RPC method it was is the fact this
// line adds; the group's own identity is already the aggregate's first line.
//
// The RPC name and the no-calls note are both told apart by their HEAD, so
// both end-clip: see columnKind.
func slowestLine(slowest *span.Span, w int) string {
	value := noRPCCallsNote
	if slowest != nil {
		value = slowest.RPC
	}
	return detailFieldLines([]detailField{
		{label: slowestHeading, value: value, kind: headIdentifierColumn},
	}, w)[0]
}

// detailLabelWidth is the floor for the label column every detail block
// lines its values up on: the span fields' longest labels ("Addr", "Attr")
// plus two spaces. A block whose own labels are wider than that -- the types
// view heads its figures with the table's own "RPC calls" -- widens to fit
// them, so the label and the value it labels can never run together.
const detailLabelWidth = 6

// detailFieldLines lays fields out as one labelled value per line, at most w
// terminal columns each, with the values aligned in a common column.
//
// Which end of an over-long value gives way is the field's KIND, routed
// through the same clipIdentifierField and clipValueForKind the tables and
// the facet pane use, so a provider address clipped here keeps the tail that
// tells it from its siblings, an RPC name keeps its head, and a number is
// not clipped by kind at all -- left whole for clipWidth beneath, so what
// gives way is its tail rather than the digits carrying its magnitude.
// Every kind goes through the one call: the taxonomy owns the exemption,
// not this render site.
func detailFieldLines(fields []detailField, w int) []string {
	labelW := detailLabelWidth
	for _, f := range fields {
		labelW = max(labelW, lipgloss.Width(f.label)+1)
	}
	lines := make([]string, len(fields))
	for i, f := range fields {
		// Dimmed, and padded BEFORE it is dimmed: the label column is
		// scaffolding for the value beside it, the same as a pane separator
		// is for the panes either side, and clipIdentifierField budgets the
		// value's width from the prefix it is handed -- which it measures in
		// display columns, so the escapes cost the value nothing.
		lines[i] = clipIdentifierField(styles.chrome.Render(padRight(f.label, labelW)), f.value, "", w, f.kind)
	}
	return lines
}
