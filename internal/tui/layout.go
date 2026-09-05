package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
// address under the span detail's six ("Prov"), and a log carrying UI-hook
// spans only has no span detail to measure at all -- there the whole pane
// would otherwise be measured at minDetailPaneWidth and clip every line it
// draws.
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
	for _, s := range l.RPCSpans {
		for _, line := range spanDetailLines(s, hugeWidth) {
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
func (m *Model) View() string {
	w, h := m.paneWidth(), m.height
	if h <= 0 {
		h = defaultHeight
	}
	head := clipWidth(header(m), w)
	// One line of terminal is the header's: it names the file the reader is
	// looking at, and a frame that showed only key hints could belong to any
	// file at all.
	if h == 1 {
		return head
	}

	caveat := loggingCaveat(h)
	lines := []string{head, ""}
	lines = append(lines, strings.Split(m.renderPanes(w, paneHeight(h, len(caveat))), "\n")...)
	if len(caveat) > 0 {
		lines = append(lines, "")
		for _, line := range caveat {
			lines = append(lines, clipWidth(line, w))
		}
	}
	lines = append(lines, "")

	if len(lines) > h-1 {
		lines = lines[:h-1]
	}
	return strings.Join(append(lines, clipWidth(m.footer(w), w)), "\n")
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
	if m.blockedJump {
		return jumpBlockedNote
	}
	if m.view == ViewRawLog {
		switch {
		case m.raw.searching:
			return "/" + m.raw.query
		case m.raw.notFound:
			return "/" + m.raw.lastQuery + "  pattern not found"
		}
	}
	return keyHints(m.view, w)
}

// jumpBlockedNote is what the footer says when Enter refused to jump to a
// call's log entry because the active filter hides it. Landing on a blank
// pane, or on some other call's lines further down the log, is
// indistinguishable from a jump that worked, so the refusal is stated and
// the key that lifts it is named.
const jumpBlockedNote = "target entry hidden by the active filter -- Esc clears it"

// keyHints is the footer's hint line for view v in a terminal w columns
// wide: which number keys switch views, then which keys act.
//
// The two groups together are 91 to 95 display columns, depending on which
// view's key is left out, and the action keys alone are 62. Neither fits
// every supported width, and the line is clipped from its END, so a line
// composed longer than w loses its tail -- which is "q quit", the one key a
// user must never lose sight of. So the view keys are an ALL-OR-NOTHING
// addition: they are shown only when the whole composed line fits w, and
// dropped entirely when it does not, rather than added and then cut. Below
// the width they fit at, the line is exactly the action keys it has always
// been, and the view the user is in is still named by the centre pane's own
// title (see renderCentre), which every width shows.
//
// That puts the view keys on screen from 95 columns up and the view's name
// on screen at every width, which is the split the two facts deserve: the
// name answers "what am I looking at" and has to survive anywhere, while
// the keys answer "what else is there" and only have to be findable.
//
// 62 columns is not a floor anyone is protected by: renderPanes has a layout
// for widths below detailInlineWidth and the spec defines one, so the
// interface really does render at 60 columns, where "q quit" is already cut
// to "q qu". Nothing should budget against 62 as though it were guaranteed.
func keyHints(v View, w int) string {
	if line := viewKeyHints(v) + "  " + actionKeys(); lipgloss.Width(line) <= w {
		return line
	}
	return actionKeys()
}

// actionKeys is the hint group for the keys that DO something to what is on
// screen, as opposed to the ones that change which view is on screen. It is
// 62 display columns; every binding added to it pushes "q quit" closer to
// the edge a narrow terminal cuts from, which is what keeps it this terse.
func actionKeys() string {
	return "⇥ pane  ␣ facet  ⏎ open  f facets  / search  Esc clear  q quit"
}

// viewKeyHints is the hint group naming the number keys that switch views,
// and the view each one switches to.
//
// The view the user is ALREADY in is left out. Its key is a no-op -- Update
// only acts on a number key that names a different view -- and advertising a
// key that does nothing is the defect this package removes wherever it finds
// it. Leaving it out is also what keeps the group inside the footer's width
// budget at 100 columns, and nothing is lost by it: the centre pane's title
// names the current view already.
func viewKeyHints(v View) string {
	hints := make([]string, 0, len(views)-1)
	for _, b := range views {
		if b.view != v {
			hints = append(hints, b.key+" "+b.name)
		}
	}
	return strings.Join(hints, "  ")
}

// frameFixedLines is what a frame spends on everything but the pane row and
// the caveat block: the header, the blank line beneath it, the blank line
// above the footer, and the footer itself.
const frameFixedLines = 4

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
	if w < facetInlineWidth && m.showFacetOverlay {
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
	case w >= detailInlineWidth:
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
	if w < facetInlineWidth && m.showFacetOverlay {
		// The overlay replaces the whole pane row, so it is the only pane
		// on screen and the only one Tab can reach.
		return []Pane{PaneFacets}
	}
	panes := make([]Pane, 0, paneCount)
	if w >= facetInlineWidth {
		panes = append(panes, PaneFacets)
	}
	panes = append(panes, PaneList)
	if w >= detailInlineWidth {
		panes = append(panes, PaneDetail)
	}
	return panes
}

// renderCentre renders the centre pane: its title, then its content for the
// active view. ViewRawLog is not one of renderList's rollup/call tables --
// it renders directly from m.log.Entries via renderRawLog -- so it is
// dispatched separately here rather than inside renderList itself.
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
	if h == 1 {
		return title
	}
	if m.view == ViewRawLog {
		return title + "\n" + m.renderRawLog(w, h-1)
	}
	return title + "\n" + m.renderList(w, h-1)
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

	rows := make([]string, h)
	for r := 0; r < h; r++ {
		cells := make([]string, len(panes))
		for i := range panes {
			cells[i] = columns[i][r]
		}
		rows[r] = strings.Join(cells, paneSep)
	}
	return strings.Join(rows, "\n")
}

// detailSection is one block of the detail pane's body: lines a short pane
// keeps or drops TOGETHER.
//
// The unit exists because the pane's lines are not independent of each
// other. A heading with nothing beneath it reads as a pane that failed to
// render, and a labelled figure whose label survived while its number was
// cut away reads as a complete answer to the question the label asks --
// which is worse, because nothing on screen says the number is missing.
type detailSection []string

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
// height budget is applied by another (fitDetailSections); all this adds is
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
	line := clipWidth(title, w)
	if m.pane == PaneDetail {
		line = cursorBar(line, w, true)
	}
	return strings.Join(fitDetailSections(line, sections, w, h), "\n")
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

// selectedDetail is the detail pane's title and body sections for the row
// the cursor is on, derived TOGETHER from one dispatch on that row's kind so
// the heading and what it heads cannot disagree.
//
// The span branch dispatches on spanForRow -- built on the same row.isCall
// predicate Enter asks before jumping -- rather than on the absence of a
// rollup. A row that is neither a call nor a rollup cannot be built (see
// callRow and rollupRow), and is reported here as nothing to describe
// rather than indexed into RPCSpans on the strength of a spanIdx that says
// it names no span: a degraded pane instead of a panic mid-frame inside the
// alt screen.
func (m *Model) selectedDetail(w int) (string, []detailSection) {
	nothing := []detailSection{{clipWidth(noSelectionNote, w)}}
	rows := m.rows()
	if m.selected < 0 || m.selected >= len(rows) {
		return noSelectionTitle, nothing
	}
	r := rows[m.selected]
	if s, ok := m.spanForRow(r); ok {
		return spanDetailTitle, []detailSection{spanDetailLines(s, w)}
	}
	if r.rollup != nil {
		return rollupDetailTitle, rollupDetailSections(r.rollup, w)
	}
	return noSelectionTitle, nothing
}

// detailCutMark is the last line of a detail pane that had more to show
// than h lines to show it in. The pane marks a value clipped for WIDTH with
// an ellipsis (see clipValueFront); a pane clipped for HEIGHT that marked
// nothing would leave the two cuts telling the reader different amounts
// about themselves, and the height cut is the one that can remove a whole
// figure rather than the tail of one.
const detailCutMark = "…"

// fitDetailSections composes a title and body sections into at most h
// lines, marking any cut with detailCutMark.
//
// The FIRST section is always appended: it describes the selected row
// itself, so a pane with room for anything at all shows as much of it as
// fits, clipped by line as a last resort. Every LATER section is kept or
// dropped whole -- the height is checked before it is appended, not after
// -- so a pane cannot end on a heading with nothing under it, or on a
// figure's label with the figure gone.
//
// The title survives every cut, since it names the pane and carries its
// focus (see renderDetail). At h of 1 that leaves no line for the mark, and
// a one-line pane is the one case where a cut goes unmarked.
func fitDetailSections(title string, sections []detailSection, w, h int) []string {
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
	if len(lines) > h {
		lines = lines[:h]
		if h > 1 {
			lines[h-1] = clipWidth(detailCutMark, w)
		}
	}
	return lines
}

// spanDetailLines formats one span's detail fields: RPC, provider and
// duration always, and its resource address in addition when it is a
// UI-hook span. Span.Address is populated only for spans FidelityUIReported
// -- an RPC-tier span never carries one -- so gating on Fidelity, not just
// on Address being non-empty, documents that this is a property of the
// span's kind rather than an incidental absence.
//
// The UI-hook branch is scaffolding, not dead code: the spec requires the
// detail pane to show a UI-hook span's unmasked address, but no view in
// this package currently exposes an individual UI-hook span for selection
// -- row.spanIdx only ever indexes m.log.RPCSpans (a pre-existing
// constraint this package does not redefine), and every FidelityUIReported
// span lives in m.log.UISpans instead. So this branch is unreachable
// through any keypress today; it is unit-tested directly
// (TestSpanDetailLinesShowsAddressForUIHookSpans) rather than end to end,
// and stays that way until some view exposes an individual UI-hook span for
// selection.
//
// RPC, Prov and Addr are all identifier values, and all three go through
// clipIdentifierField, each clipped from the end its kind allows (see
// columnKind): the same value clipped here, in the facet pane and in the
// calls table is then clipped the same way and carries the same marker.
// Prov and Addr front-clip, so two providers or addresses sharing a long
// prefix -- ".../hashicorp/azuread" and ".../azurerm" -- do not both clip
// down to their identical shared head. RPC end-clips, since it names one of
// a short, closed set of plugin-protocol methods that share long suffixes
// and diverge within their first few characters -- the same reasoning
// internal/profile.actionColWidth uses for its own closed-vocabulary action
// column. Dur is a formatted number and is never long enough to need either
// treatment.
func spanDetailLines(s span.Span, w int) []string {
	fields := []detailField{
		{label: "RPC", value: s.RPC, kind: headIdentifierColumn},
		{label: "Prov", value: s.Provider, kind: tailIdentifierColumn},
		{label: "Dur", value: formatMs(uint64(s.DurationMs)), kind: numericColumn},
	}
	if s.Fidelity == span.FidelityUIReported {
		fields = append(fields, detailField{label: "Addr", value: s.Address, kind: tailIdentifierColumn})
	}
	return detailFieldLines(fields, w)
}

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
// whole (see fitDetailSections), and that ORDER is load-bearing: what a
// pane too short for both keeps is the summary of the row the cursor is
// actually on, and what it gives up is the one call behind it.
//
// A nil rollup gets no sections, matching slowestOf one level down rather
// than contradicting it: a row that is not a rollup describes no group, and
// the two functions the pane is built from must agree on what a missing
// group means. detailNaturalWidth hands it every rollup row in the log
// without asking, and a row can be built with no detail at all.
func rollupDetailSections(d *rollupDetail, w int) []detailSection {
	if d == nil {
		return nil
	}
	return []detailSection{
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
// lines its values up on: the span fields' longest label ("Addr") plus two
// spaces. A block whose own labels are wider than that -- the types view
// heads its figures with the table's own "RPC calls" -- widens to fit them,
// so the label and the value it labels can never run together.
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
		lines[i] = clipIdentifierField(padRight(f.label, labelW), f.value, "", w, f.kind)
	}
	return lines
}
