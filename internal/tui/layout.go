package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
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
	countWidth := facetCountWidth(facets)
	for _, f := range facets {
		width = max(width, lipgloss.Width(facetSectionHeader(f.Name)))
		for _, v := range f.Values {
			width = max(width, facetValueNaturalWidth(v.Value, countWidth))
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
// rollup can name something no span detail does. A resource type reported
// by the UI tier alone reaches the pane through typeRows and through
// nothing else: in a log carrying both tiers, the span loop below measures
// the RPC spans, and no RPC span carries that type.
//
// The span tier it measures is the tier a selection can actually reach,
// which timelineTierFor decides -- the same rule timelineSpans draws by, so
// the two cannot disagree about which spans this pane will ever be asked to
// describe. A UI-hook span's detail carries an address field (see
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

// paneRule is the character the pane row's top and bottom rules are drawn
// in, and paneRuleTopSep and paneRuleBottomSep are what those rules cross a
// pane separator with. Each crossing is exactly paneSep's three columns, so
// the ┬ and the ┴ land in the │'s own column and the frame's verticals read
// as one line from the top rule to the bottom.
const (
	paneRule          = "─"
	paneRuleTopSep    = "─┬─"
	paneRuleBottomSep = "─┴─"
)

// paneTitleLead is the rule drawn before a pane's name in the top rule. Two
// columns, so a name reads as inset INTO the rule rather than as a caption
// sitting to the left of one.
const paneTitleLead = "──"

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
// h is a target rather than a ceiling, at every width: framePanes composes
// one pane the same way it composes three, padding each out to the row's
// height and closing the row with a rule, so the frame fills the terminal
// exactly whether the layout is the full three panes, the facet overlay, or
// a full-width raw log. A row that stopped wherever its content ran out
// would leave its bottom rule floating in the middle of the screen, which
// says the content ended somewhere it did not.
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
// line keeps its LAST line, which is the one carrying "q quit" -- as far as
// WIDTH allows, that line being clipped from its end like any other (see
// actionKeys), so a narrow terminal can still lose the hint off the right.
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

	// The footer is composed FIRST, because how many lines it takes is what
	// the pane row is budgeted against and it is not a constant: the two
	// hint lines are the usual answer, but a search prompt, a miss report
	// and a refused jump each replace the whole footer with ONE line (see
	// footer). Budgeted at two regardless, those three states leave the
	// frame a line short of the terminal, and the pane row's closing rule
	// floating a line above the bottom of the screen.
	footerLines := strings.Split(m.footer(w), "\n")
	if avail := h - 1; len(footerLines) > avail {
		footerLines = footerLines[len(footerLines)-avail:]
	}

	// The caveat qualifies DURATIONS, and the help pane shows none. Leaving
	// it up spends six of a short frame's lines -- its five and the blank
	// above them -- on numbers that are not on screen, and takes them from
	// the only content that is: at h of 12 it leaves the key table its first
	// heading and nothing beneath it, one line being the whole of the pane
	// row's body. Suppressing it is the
	// same "what is DRAWN decides" rule actionKeys applies through
	// detailPaneDrawn, and it withholds no qualification, because there is
	// no figure on this frame to qualify.
	var caveat []string
	if !m.showHelp {
		caveat = loggingCaveat(h, len(footerLines))
	}
	// No blank line between the header and the pane row: the row opens with
	// its own rule, which separates the two as well as a blank would and
	// says something besides. That line is what pays for the rule closing
	// the row at the bottom, so the frame gains both rules for nothing.
	lines := []string{head}
	lines = append(lines, strings.Split(m.renderPanes(w, paneHeight(h, len(caveat), len(footerLines))), "\n")...)
	if len(caveat) > 0 {
		lines = append(lines, "")
		for _, line := range caveat {
			lines = append(lines, styles.note.Render(clipWidth(line, w)))
		}
	}
	lines = append(lines, "")

	if room := h - len(footerLines); len(lines) > room {
		lines = lines[:room]
	}
	for _, line := range footerLines {
		lines = append(lines, clipWidth(line, w))
	}
	return strings.Join(lines, "\n")
}

// headerSep divides the header's three fields. A middle dot rather than the
// double hyphen this codebase's prose uses, because these are FIELDS rather
// than a sentence with an aside in it -- and because "tfli -- plan.log"
// reads as a command line whose flags have been terminated, which is a
// sentence about invoking the tool rather than a heading naming what it has
// open.
const headerSep = "·"

// header names the file and its span counts.
//
// While a filter is active it reports the matching count against the whole
// log's -- "12 of 3184 RPC spans" -- because every other number on screen
// is then a filtered number, and a ranked table holding twelve rows looks
// exactly like a log that only ever had twelve calls in it. With no filter
// active it reads as the plain count it always did: there is nothing to
// compare against, and "3184 of 3184" would be noise on every frame.
//
// Unticking only levels narrows the raw log rather than the spans (see
// levelFacet), so its counts read "3184 of 3184" -- which is the honest
// answer to "what is this filter doing to the rankings", not a rounding of
// it.
func header(m *Model) string {
	rpc, ui := len(m.log.RPCSpans), len(m.log.UISpans)
	name := logfmt.DisplayText(m.name)
	if n := m.log.UISaturatedDurations; n > 0 {
		name = fmt.Sprintf("WARNING: %d UI durations capped %s %s", n, headerSep, name)
	}
	if !m.filterActive() {
		return fmt.Sprintf("tfli %s %s %s %d RPC spans, %d UI spans", headerSep, name, headerSep, rpc, ui)
	}
	f := m.filter()
	return fmt.Sprintf("tfli %s %s %s %d of %d RPC spans, %d of %d UI spans",
		headerSep, name, headerSep, countMatching(f, m.log.RPCSpans), rpc, countMatching(m.uiFilter(), m.log.UISpans), ui)
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
	// is inert. At a height that also cuts the key table down to a heading
	// with no keys under it, nothing on the frame names a working key at
	// all. That is the trap
	// View's own comment says the footer exists to prevent, and the reason
	// renderHelp is allowed to cut without a mark.
	if m.showHelp {
		return m.renderKeyHints(w)
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
			return m.searchPrompt(w)
		case m.raw.notFound:
			return styles.alert.Render("/" + logfmt.DisplayText(m.raw.lastQuery) + "  pattern not found")
		}
	}
	return m.renderKeyHints(w)
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
// does -- including the one whose words contain a space of their own ("6 raw
// log"), which is why the split is at the FIRST space and the remainder is
// left whole. A hint carrying no space at all is left alone rather than
// accented entire: that is a hint clipped down to its bare key, and
// accenting the fragment whole would mark a word that is not there.
func styleHintKeys(line string) string {
	lines := strings.Split(line, "\n")
	for i, ln := range lines {
		hints := strings.Split(ln, hintSep)
		for j, h := range hints {
			key, rest, ok := strings.Cut(h, " ")
			if !ok {
				continue
			}
			hints[j] = helpBinding(key, rest)
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
	return ansi.Strip(m.navigation(w)) + "\n" + ansi.Strip(m.actionHelp(w))
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
// wherever it finds it, and this hint would be inert in three of the five
// views.
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
// One working key is deliberately absent: o, which narrows a facet
// dimension to the value under the cursor (soloFacetValue). This line is 70
// columns in the calls and timeline views already, against a budget of 70
// -- the narrowest terminal that draws three panes -- so a hint of its own
// costs eight columns it does not have, and folding it into "␣ facet" costs
// the one column that cuts "q quit" short. The rule the span hint sets is
// that a key earns footer space when it is the ONLY route to something, and
// o is not: space reaches the same state, slower. It is named in the help's
// key table (see helpGroups), which is where a shortcut belongs.
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
	if m.raw.scope != nil {
		keys = append(keys, scopeHint)
	}
	esc := escClearHint
	if m.hasReturn {
		esc = escBackHint
	}
	return strings.Join(append(keys, "f facets", "/ search", esc, quitHint), hintSep)
}

// escClearHint and escBackHint are Esc's two meanings, and exactly one is
// ever on the frame. Esc unwinds the innermost thing first: a jump waiting
// to be undone, then the filters. Advertising the wrong one is worse than
// advertising neither -- a reader pressing Esc to clear a filter and landing
// in another view has been told something false about the key -- so the hint
// asks the same hasReturn the handler does.
//
// "back" is a column shorter than "clear", so the switch cannot push the
// action line over its budget: the widest line carrying it is the calls
// view's at exactly 70 columns, and it is 69 with the return standing.
const (
	escClearHint = "Esc clear"
	escBackHint  = "Esc back"
)

// scopeHint offers the key that drops a raw-log scope. It is shown only
// while a scope is live: a key advertised with nothing to act on is the
// defect this package removes wherever it finds it.
//
// "log" rather than "whole log": every other hint on this line names a
// target or an action ("pane", "facet", "facets", "search", "back", "quit"),
// so this one names the destination the key returns to rather than a bare
// quantifier, and the scoped action line has no width to spare for the
// longer phrasing. At 11 columns "whole log" would run the scoped line to 66
// columns against 53 unscoped, clipping "q quit" at every width from 30 to
// 65, including 60, a width two committed goldens depend on (help-60.txt,
// timeline-60.txt). At 5 columns the scoped line is 60 exactly, and "q quit"
// survives from there up (see
// TestTheFooterNeverLosesQuitAtWidthsTheScopedActionLineFits).
const scopeHint = "\\ log"

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

// frameFixedLines is what a frame spends on everything but the pane row, the
// caveat block and the footer: the header, and the blank line above the
// footer.
//
// The footer is not counted here because it is not a fixed height -- it is
// two hint lines usually and one line in the three states that replace it
// (see View) -- so every caller passes its measured height in beside this.
//
// The pane row's own rules are not counted here either. They are drawn by
// framePanes, which needs the pane widths to cross them at the separators,
// so they belong to the row rather than to the frame around it.
const frameFixedLines = 2

// minPaneRowHeight is the least a pane row can be given before the caveat
// starts taking lines from it: its top rule, which names the panes, and one
// line of content beneath. A row of one line is a rule over nothing.
const minPaneRowHeight = 2

// paneHeight is how many lines the pane row itself gets in a frame h lines
// tall carrying caveatLines lines of caveat. The caveat block costs one line
// more than its text, for the blank line above it, and costs nothing at all
// when there is no caveat to separate.
//
// It never goes below 1: a terminal too short to show everything still shows
// something rather than an empty pane, and View then trims the surplus out
// from ABOVE its footer.
func paneHeight(h, caveatLines, footerLines int) int {
	if paneH := h - frameFixedLines - footerLines - caveatBlockLines(caveatLines); paneH > 0 {
		return paneH
	}
	return 1
}

// caveatBlockLines is what n lines of caveat cost a frame: the lines
// themselves plus the blank separating them from the pane row, and nothing
// at all when there is no caveat to separate.
//
// It is named because two callers need it and they must not disagree --
// paneHeight, budgeting the pane row against it, and loggingCaveat, deciding
// which caveat there is room for. Spelled out at the second of those it
// reads "+1+1", two ones that are different things.
func caveatBlockLines(n int) int {
	if n == 0 {
		return 0
	}
	return n + 1
}

// fullLoggingCaveat states that every duration this interface renders was
// measured under logging. Terraform re-logs each line of a provider's stderr
// through its own logger, so a provider that dumps HTTP bodies at DEBUG pays
// that cost per line: four captures of one workspace measured 24.1s with no
// logging enabled against 522.2s with debug plus provider TRACE. A reader
// who mistook these figures for wall-clock truth would be optimising time
// that does not exist without the log, so the caveat travels with every
// rendered duration rather than living only in documentation. Below
// caveatWidth it is clipped mid-sentence like any other line, since no width
// floor protects it. shortLoggingCaveat is the answer to a frame short of
// HEIGHT, not of width.
//
// It does NOT promise the rankings survive, and said so until 2026-09-07:
// "Rankings hold, since every span paid the same cost". Spans do not pay the
// same cost. The tax is charged per LINE, and --diagnose's entries-per-
// request-id spread measured 4 to 1934 lines under one call's id in a single
// capture -- a 484-fold range, inside one log. A call that waits on a network
// round trip logs almost nothing while it waits; one doing per-attribute work
// logs constantly. So logging inflates chatty local work against genuine
// waiting, and the order the two are ranked in is approximate. Telling a
// reader otherwise, on the one line shown over every frame, is the
// reassurance this tool can least afford to give.
var fullLoggingCaveat = []string{
	"Durations here are measured under logging, which is not",
	"free: one workspace planned in 24.1s unlogged and 522.2s",
	"with debug plus provider TRACE. A call that logs heavily",
	"is inflated more than one that waits, so rankings are",
	"approximate and absolute times do not transfer.",
}

// caveatWidth is the full caveat's longest line, and the bound the short form
// is held to as well. It is a constant rather than a sentence in a doc
// comment because it is a MEASURED number that an edit to the wording moves:
// stated in prose it went stale silently, and it decides where the text
// starts being clipped mid-sentence.
const caveatWidth = 56

// shortLoggingCaveat is the same warning in one whole sentence, for a frame
// with no room for the full text. It is a rewrite rather than the first line
// of fullLoggingCaveat, because a caveat cut off mid-sentence reads as a
// rendering fault rather than as a warning that was deliberately shortened.
//
// "approximate" rather than the "only rankings transfer" it read until
// 2026-09-07, for the reason given above: rankings do not transfer intact
// either, and a short frame is no excuse for a shorter truth.
const shortLoggingCaveat = "Durations measured under logging; rankings approximate."

// loggingCaveat is the caveat's lines for a frame h lines tall: the full
// text where it fits, one sentence where it does not, and nothing at all
// below the height where even one line would cost the footer.
//
// The footer is budgeted ahead of the caveat, not after it. The caveat is a
// fixed warning a reader can take in once; the footer carries live state --
// the search query being typed, the miss report, and the reminder that 'q'
// quits -- that exists nowhere else on screen, so it is the one line that
// must survive a short terminal.
func loggingCaveat(h, footerLines int) []string {
	// Each bound is frameFixedLines and the footer, plus the least a pane
	// row can be worth drawing at, plus what that caveat's own block costs.
	fixed := frameFixedLines + footerLines + minPaneRowHeight
	switch {
	case h >= fixed+caveatBlockLines(len(fullLoggingCaveat)):
		return fullLoggingCaveat
	case h >= fixed+caveatBlockLines(1):
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
	// bodyH is what a pane's own renderer is given: the row's height less
	// the rules framePanes draws around it. Every branch below renders to
	// it, so the frame's arithmetic is stated once here rather than at each
	// of the five sites that would otherwise each have to subtract.
	bodyH := paneBodyHeight(h)
	if m.showHelp {
		return framePanes(h, pane{title: helpTitle, content: renderHelp(w, bodyH), width: w})
	}
	if m.facetOverlayShowing(w) {
		return framePanes(h, pane{title: m.filterTitle(), focused: m.pane == PaneFacets, content: m.renderFacets(w, bodyH), width: w})
	}
	switch {
	case w >= facetInlineWidth:
		facetW := facetPaneWidth(m.facetPaneNatural, w)
		if !m.detailPaneDrawn(w) {
			listW := w - facetW - paneSepWidth
			return framePanes(h,
				pane{title: m.filterTitle(), focused: m.pane == PaneFacets, content: m.renderFacets(facetW, bodyH), width: facetW},
				pane{title: m.centreTitle(), focused: m.pane == PaneList, content: m.renderCentre(listW, bodyH), width: listW},
			)
		}
		detailW := detailPaneWidth(m.detailPaneNatural, w)
		listW := w - facetW - detailW - 2*paneSepWidth
		detailTitle, detail := m.renderDetail(detailW, bodyH)
		return framePanes(h,
			pane{title: m.filterTitle(), focused: m.pane == PaneFacets, content: m.renderFacets(facetW, bodyH), width: facetW},
			pane{title: m.centreTitle(), focused: m.pane == PaneList, content: m.renderCentre(listW, bodyH), width: listW},
			pane{title: detailTitle, content: detail, width: detailW, focused: m.pane == PaneDetail},
		)
	case m.detailPaneDrawn(w):
		detailW := detailPaneWidth(m.detailPaneNatural, w)
		listW := w - detailW - paneSepWidth
		detailTitle, detail := m.renderDetail(detailW, bodyH)
		return framePanes(h,
			pane{title: m.centreTitle(), focused: m.pane == PaneList, content: m.renderCentre(listW, bodyH), width: listW},
			pane{title: detailTitle, content: detail, width: detailW, focused: m.pane == PaneDetail},
		)
	default:
		// renderRawLog renders its first visible entry from the top down
		// whatever that entry's height -- a provider's multi-line HTTP body
		// dump is the realistic case -- so the centre pane can overflow the
		// height it was given. framePanes holds it to the row regardless,
		// the same way it holds every other pane, which is why the one
		// layout with no second pane beside it needs no clamp of its own.
		return framePanes(h, pane{title: m.centreTitle(), focused: m.pane == PaneList, content: m.renderCentre(w, bodyH), width: w})
	}
}

// paneBodyHeight is how many lines of a pane row h lines tall are the panes'
// own, the rest being the rules framePanes draws around them. It mirrors
// framePanes' short-row rule exactly -- content before chrome -- so a pane
// is never rendered to a height the row cannot show.
func paneBodyHeight(h int) int {
	switch {
	case h <= 1:
		return 0
	case h == 2:
		return 1
	default:
		return h - 2
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
	return m.view != ViewRawLog && !m.facetOverlayShowing(w) && w >= detailInlineWidth
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

// renderCentre renders the centre pane's body for the active view.
//
// ViewRawLog and ViewTimeline are not one of renderList's rollup/call
// tables -- the raw log renders directly from m.log.Entries via
// renderRawLog, and the timeline renders from m.timelineSpans() and
// model.PackLanes via renderTimeline -- so both are dispatched separately
// here rather than inside renderList itself.
//
// The pane's name is not here. It is inset into the pane row's top rule
// (centreTitle, titledRule), which is what names every pane.
func (m *Model) renderCentre(w, h int) string {
	if h <= 0 {
		return ""
	}
	switch m.view {
	case ViewRawLog:
		return m.renderRawLog(w, h)
	case ViewTimeline:
		return m.renderTimeline(w, h)
	default:
		return m.renderList(w, h)
	}
}

// centreTitle names the centre pane for the top rule it is inset into.
//
// The name is what says which view the rows beneath it belong to. Unnamed,
// the pane holding a providers rollup is indistinguishable at a glance from
// the facet pane's list of providers to filter by, and nothing on screen
// says the interface has other views to switch to.
//
// The timeline's name states the TIER it is drawing (see timelineTitle),
// which views' own static "TIMELINE" cannot: that table has no notion of the
// current log, and the tier is a property of it.
//
// It is not focus-marked, matching the facet pane rather than the detail
// pane: both of those have a cursor of their own to carry focus (the
// selected row here, the highlighted value there), and a second reverse-
// video bar on the name would say nothing the row cursor does not already
// say. The detail pane's name is marked precisely because it has no cursor
// (see pane.focused).
func (m *Model) centreTitle() string {
	if m.view == ViewRawLog && m.raw.scope != nil {
		// The COUNT, not merely the fact. The measured spread is 4 to 1934
		// entries, so a word like "one call" would read the same over a
		// pane the reader can take in whole and over one holding a
		// twentieth of the log. It is free: the scope is a slice.
		return fmt.Sprintf("%s (%d %s)", viewTitle(ViewRawLog), len(m.raw.scope),
			plural(len(m.raw.scope), "entry", "entries"))
	}
	if m.view == ViewTimeline {
		return m.timelineTitle()
	}
	return viewTitle(m.view)
}

// plural picks a suffix for n, so a count of one does not read as a count of
// several. internal/diagnose carries the same function, but the two packages
// share no utility layer -- one shared function is not a layer worth
// building for -- so this is a deliberate duplicate rather than an import
// across packages.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// pane is one column of a composed pane row: its name, its rendered body,
// the width it was rendered at, and whether the keyboard is in it.
//
// The name and the body are separate because the name is not drawn in the
// body's space: it is inset into the pane row's top rule (see titledRule),
// which is what lets the frame be closed top and bottom without costing the
// body a line. A pane renderer therefore returns its body ALONE, and the
// title travels beside it.
//
// title must arrive UNSTYLED, the same contract cursorBar carries and for
// the same reason: titledRule draws it in reverse video where the pane has
// the keyboard, and reverse video ends at the first reset inside what it
// wraps -- so a title carrying styling of its own would light up as far as
// its first escape and no further.
//
// focused is read only by the detail pane, the one pane with no cursor row
// of its own to mark. The other panes answer Tab with their cursor bars.
type pane struct {
	title   string
	content string
	width   int
	focused bool
}

// framePanes composes panes side by side into one h-line block, ruled top
// and bottom, with each pane's name inset into the top rule.
//
// The rules are what close the frame. Without the bottom one the panes trail
// off into a column of separators -- at the default height that is most of
// the frame -- and nothing on screen says where the content ends.
//
// A short row spends its lines on CONTENT before chrome, in that order: the
// top rule first, since it carries the pane names and so is the only line
// that says what the reader is looking at; then the body; then the bottom
// rule, which is pure scaffolding and is the first thing dropped. A two-line
// row is therefore a named rule and one line of content, not two rules and
// nothing between them.
//
// How many lines that leaves the body is paneBodyHeight's to say, and this
// asks it rather than working it out again: the same number is handed to
// every pane's renderer (see renderPanes), and a rule stated in two places
// is a rule that holds by test rather than by construction.
func framePanes(h int, panes ...pane) string {
	if h <= 0 {
		return ""
	}
	top := topRule(panes)
	// A one-line row is the top rule alone, and it returns HERE rather than
	// falling through with a body of zero lines: joinPanes(0) is the empty
	// string, which strings.Split turns into one empty line, and the row
	// would come back a line taller than it was asked for.
	if h == 1 {
		return top
	}
	rows := append([]string{top}, strings.Split(joinPanes(paneBodyHeight(h), panes...), "\n")...)
	if h >= 3 {
		rows = append(rows, bottomRule(panes))
	}
	return strings.Join(rows, "\n")
}

// topRule is the pane row's opening line: each pane's name inset into a
// rule, crossed at every separator.
func topRule(panes []pane) string {
	segments := make([]string, len(panes))
	for i, p := range panes {
		segments[i] = titledRule(p)
	}
	return strings.Join(segments, styles.chrome.Render(paneRuleTopSep))
}

// bottomRule is the pane row's closing line: a plain rule at each pane's
// width, crossed at every separator. It is styled whole rather than per
// segment, there being nothing inside it carrying styling of its own.
func bottomRule(panes []pane) string {
	segments := make([]string, len(panes))
	for i, p := range panes {
		segments[i] = strings.Repeat(paneRule, max(p.width, 0))
	}
	return styles.chrome.Render(strings.Join(segments, paneRuleBottomSep))
}

// titledRule is one pane's share of the top rule: the lead, the pane's name
// with a space either side, and rule to the pane's full width.
//
// The name takes the title style, or reverse video where the pane has the
// keyboard -- the same bar the other panes mark their cursor rows with, so
// Tab's effect reads the same wherever it lands. Only the NAME is reversed,
// not the rule around it: the bar marks a thing, and the rule is not one.
//
// The name is drawn WHOLE or not at all: a pane with no room for it gets a
// plain rule. Clipping it instead would break the rule open for a fragment
// -- "── FILTE" at eight columns -- which names nothing, costs the line its
// continuity, and is unmarked besides, this being the one place in the
// package that would end-clip a NAME rather than a value.
//
// No pane in a two- or three-pane row can reach that: capPaneWidth applies
// its floor LAST, so the facet pane is never under minFacetPaneWidth's 15
// against FILTERS' 11, nor the detail pane under minDetailPaneWidth's 19
// against GROUP DETAIL's 16 -- the longer of that pane's two names, and so
// the binding one. The centre pane is never under 44 in a three-pane row nor
// 48 in a two-pane one, against 32 for the widest name it draws.
//
// What reaches it is the full-width single pane: the facet overlay, the
// help, and whichever view is active below detailInlineWidth. The widest
// name any of them carries is the timeline's, which states its tier, so
// "TIMELINE (ui, whole seconds)" goes unnamed below 32 columns where
// "RAW LOG" survives to 11.
func titledRule(p pane) string {
	label := " " + p.title + " "
	fill := p.width - lipgloss.Width(paneTitleLead) - lipgloss.Width(label)
	if fill < 0 {
		return styles.chrome.Render(strings.Repeat(paneRule, max(p.width, 0)))
	}
	style := styles.title
	if p.focused {
		style = styles.selected
	}
	lead := paneTitleLead
	if p.focused {
		lead = "▶─"
	}
	return styles.chrome.Render(lead) + style.Render(label) + styles.chrome.Render(strings.Repeat(paneRule, fill))
}

// joinPanes composes panes' BODIES side by side into one h-line block. Each
// pane's lines are right-padded to its declared width so every pane starts
// at the same column on every row, and each pane's line list is padded with
// blank lines up to h so a shorter pane (an empty detail pane, say) does not
// shrink the row it appears in. A pane with MORE lines than h is truncated
// to it, which is what holds a row to its height whether it composes one
// pane or three.
//
// It draws no rules and reads no titles: framePanes puts those around what
// this returns.
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
// The title and the body come from one dispatch (selectedDetail), so the
// heading and what it heads cannot disagree, and the height budget is
// applied by another (fitPaneSections). Both are returned rather than
// composed here: the title is drawn in the pane row's top rule, which is
// also where this pane marks keyboard focus -- it has no cursor row of its
// own -- and that rule is framePanes' to draw.
func (m *Model) renderDetail(w, h int) (title, body string) {
	title, sections := m.selectedDetail(w)
	// The title is settled BEFORE the height is, because it is a claim about
	// what the cursor is on and not about how much room there is to describe
	// it. Answering a zero-height pane with noSelectionTitle would say
	// "there is nothing selected" over a live selection -- and it says it on
	// the one frame where the rule is the only thing the reader gets, a
	// terminal of five lines leaving the pane row a single line.
	if h <= 0 {
		return title, ""
	}
	return title, strings.Join(fitPaneSections(sections, w, h), "\n")
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

// moreBelowMark is the last line of a pane that had more to show than h
// lines to show it in -- the detail pane, the help, the timeline's notes
// and stall list.
//
// It NAMES what it means rather than marking the cut with a bare ellipsis.
// A pane makes two kinds of cut and they must not read alike: a value
// clipped for WIDTH carries an ellipsis in the value position (see
// clipValueFront), and a height cut carries one where a LABEL would sit.
// Two columns of indentation is the whole difference between "this figure
// is too wide to show" and "this figure is below the fold", and a reader
// cannot be asked to read a figure's absence off an indent. Saying "more"
// answers the only question the line exists to answer.
//
// It is not the mark a timeline axis uses when its right-hand label will
// not fit (see timeAxis). That is a value that did not fit, not content
// below a fold, and the two say different things.
const moreBelowMark = "… more"

// fitPaneSections composes a pane's body sections into at most h lines,
// marking any cut with moreBelowMark.
//
// The pane's title is not among them. It is inset into the pane row's top
// rule (see titledRule), outside this budget entirely, so it survives every
// height without this having to spend a line keeping it.
//
// The FIRST section is always appended, so a pane with room for anything at
// all shows as much of its most important block as fits, clipped by line as
// a last resort -- the selected row's own fields in the detail pane, the
// number keys in the help. Every LATER section is kept or dropped whole --
// the height is checked before it is appended, not after -- so a pane cannot
// end on a heading with nothing under it, or on a figure's label with the
// figure gone.
//
// At h of 1 there is no line to spare for the mark, so a one-line pane is
// the one case where a cut goes unmarked. What it shows instead is the first
// line of its first section, which says more than a bare mark would.
func fitPaneSections(sections []paneSection, w, h int) []string {
	var lines []string
	cut := false
	for i, s := range sections {
		if i > 0 && len(lines)+len(s) > h {
			cut = true
			break
		}
		lines = append(lines, s...)
	}
	if cut {
		lines = append(lines, clipWidth(moreBelowMark, w))
	}
	if h >= 0 && len(lines) > h {
		lines = lines[:h]
		if h > 1 {
			lines[h-1] = clipWidth(moreBelowMark, w)
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
// The start field appears only for a span whose start was forced to zero
// because its reported duration exceeded its offset from the log's first
// entry (span.Span.StartClamped). It sits directly under duration because
// duration is what it qualifies: the timeline draws such a span from column
// 0 with a length of EndMs, so the pane would otherwise read "duration"
// over "45.0s" beside a three-second bar with nothing accounting for the
// difference. It is prose rather than an identifier and is told apart by
// its head, so it end-clips (see columnKind).
//
// RPC, resource type, provider and address are all identifier values, and
// all four go through clipIdentifierField, each clipped from the end its
// kind allows (see columnKind). The kind is what travels, not the helper:
// resolved through clipValueForKind here, in the facet pane and in the calls
// table alike, the same value is clipped the same way in all three and
// carries the same marker. Resource type, provider and address front-clip, so two
// resource types, providers or addresses sharing a long prefix --
// ".../hashicorp/azuread" and ".../azurerm" -- do not both clip down to
// their identical shared head. RPC end-clips, since it names one of a
// short, closed set of plugin-protocol methods that share long suffixes and
// diverge within their first few characters -- the same reasoning
// internal/profile.actionColWidth uses for its own closed-vocabulary action
// column. Duration is a formatted number and is never long enough to need
// either treatment.
//
// The resource type field closes a standing gap between this pane and
// callColumns, which has carried a resource-type column since the calls
// table existed: without it
// the pane showed less about the selected call than the row describing it.
func spanDetailLines(s span.Span, a attrib.Attribution, hasContext bool, w int) []string {
	fields := []detailField{
		{label: "RPC", value: s.RPC, kind: headIdentifierColumn},
		{label: "resource type", value: s.ResourceType, kind: tailIdentifierColumn},
		{label: "provider", value: s.Provider, kind: tailIdentifierColumn},
		{label: "duration", value: formatMs(uint64(s.DurationMs)), kind: numericColumn},
	}
	if s.StartClamped {
		fields = append(fields, detailField{label: "start", value: clampedStartValue, kind: headIdentifierColumn})
	}
	if s.Fidelity == span.FidelityUIReported {
		// An observed address, stated by the log rather than inferred from
		// it, so it carries no confidence marker.
		fields = append(fields, detailField{label: "address", value: s.Address, kind: tailIdentifierColumn})
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
		return []detailField{{label: "resource", value: noAddressContextValue, kind: headIdentifierColumn}}
	}
	switch a.Confidence {
	case attrib.Ambiguous:
		return []detailField{
			{label: "resource", value: fmt.Sprintf("%d candidates", a.Candidates), kind: headIdentifierColumn},
			{label: "attribution", value: a.Confidence.String(), kind: headIdentifierColumn},
		}
	case attrib.Unattributed:
		return []detailField{{label: "resource", value: unattributedValue, kind: headIdentifierColumn}}
	}

	name := a.Name
	if a.IsData {
		// Terraform's own address syntax puts "data." before the type, not
		// the name -- but the resource type has its own field elsewhere in
		// the pane (spanDetailLines), and this resource field is the only
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
		{label: "resource", value: name, kind: headIdentifierColumn},
	}
	if a.Module != "" {
		fields = append(fields, detailField{label: "module", value: a.Module, kind: tailIdentifierColumn})
	}
	return append(fields, detailField{label: "attribution", value: a.Confidence.String(), kind: headIdentifierColumn})
}

// noAddressContextValue and unattributedValue say two different things. The
// first is a fact about the LOG -- it carries no terraform.ui stream, so no
// call in it can be attributed. The second is a fact about this CALL -- the
// log could answer the question and did not answer it here.
const (
	noAddressContextValue = "no address context in log"
	unattributedValue     = "not matched"
)

// clampedStartValue is what the detail pane puts under its start field for
// a span whose start was clamped. It states the clamp and the value it was
// clamped to, in --profile's own words ("has its start clamped to zero"),
// so a
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
// longest. Without it the RPC name beneath it reads as the ROW's own, which
// for a row totalling several calls is a wrong answer rather than a missing
// one. It names the call rather than saying "slowest" alone, because on its
// own line above the value there is room to say what the superlative is
// about.
const slowestHeading = "slowest call"

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
		append(paneSection{""}, slowestLines(d.slowest, w)...),
	}
}

// slowestLines names the group's longest RPC-tier call, or states that the
// group has none. It is a field like any other -- the heading, then the
// call beneath it -- and both lines are returned together, so a caller
// cannot draw the heading without what it heads.
//
// It names the call and stops there. The call's DURATION is by construction
// the same number as the aggregate's max ("RPC max" in the types view),
// which the same pane already carries a few lines up -- model.RollupBy's
// MaxMs and groupRPCSpans' slowest run over the same spans under the same
// key, so they cannot differ -- and one
// figure shown twice under two labels invites the reader to treat them as
// two independent measurements. Which RPC method it was is the fact this
// line adds; the group's own identity is already the aggregate's first line.
//
// The RPC name and the no-calls note are both told apart by their HEAD, so
// both end-clip: see columnKind.
func slowestLines(slowest *span.Span, w int) []string {
	value := noRPCCallsNote
	if slowest != nil {
		value = slowest.RPC
	}
	return detailFieldLines([]detailField{
		{label: slowestHeading, value: value, kind: headIdentifierColumn},
	}, w)
}

// detailIndent is what a detail value is set in from the left margin, under
// the label naming it. Two columns: enough that the pair reads as one unit
// without colour -- which is the case that has to work, since NO_COLOR
// leaves the label's accent off (see theme.fieldLabel) -- and cheap enough
// that a value keeps nearly the whole pane.
const detailIndent = "  "

// detailFieldLines lays fields out as a definition list: each label on its
// own line, then its value indented beneath it, every line at most w
// terminal columns.
//
// Two lines rather than one because the pane is capped at
// maxDetailPaneWidth, and a label column took columns from every line of
// every block -- the columns a provider address needs for its registry host
// and a resource type for its prefix. The value now has the pane less the
// two-column indent, whatever its label, where before it had the pane less
// a column as wide as the longest label its block carried.
//
// It costs height: a span with attribution runs to twelve lines or more. A
// pane too short for that clips by line and says there is more below
// (fitPaneSections), which is the trade -- a value cut in half says less
// than a field the reader is told exists.
//
// Which end of an over-long value gives way is the field's KIND, routed
// through the same clipValueForKind the tables and the facet pane resolve
// their own kinds with, so a provider address clipped here keeps the tail that
// tells it from its siblings, an RPC name keeps its head, and a number is
// not clipped by kind at all -- left whole for clipWidth beneath, so what
// gives way is its tail rather than the digits carrying its magnitude.
// Every kind goes through the one call: the taxonomy owns the exemption,
// not this render site.
func detailFieldLines(fields []detailField, w int) []string {
	lines := make([]string, 0, 2*len(fields))
	for _, f := range fields {
		// An empty value is a fact about the call -- an RPC-level method
		// belongs to no resource type -- and it is spelled with the word
		// model.FacetKey gives an empty value, so the detail pane and the
		// facet pane call one absence one thing on the same frame. Left
		// empty it would leave the label heading a blank line, which reads
		// as a field the pane failed to fill in rather than as a field with
		// nothing in it.
		if f.value == "" {
			f.value = model.FacetKey("")
			f.kind = headIdentifierColumn
		}
		// The label is styled whole, the way a table's header cell is: a
		// style wrapped around part of it would put escapes inside a value
		// the frame is read for. It is held to width by clipWidth, which
		// cuts without a marker -- unlike a value, which clips by its kind
		// and is marked. That is sound only because labels are literals
		// shorter than the pane's own floor: the longest is "resource
		// types" at 14 columns against minDetailPaneWidth of 19, so no
		// label can reach the cut. A label longer than that would shorten
		// silently into something that still reads as a label.
		lines = append(lines,
			styles.fieldLabel.Render(clipWidth(f.label, w)),
			clipIdentifierField(detailIndent, logfmt.DisplayText(f.value), "", w, f.kind),
		)
	}
	return lines
}
