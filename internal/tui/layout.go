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
// ("(no call selected)"). They are real floors, applied last in
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
// the widest RPC/provider/duration/address line across EVERY RPC span, not
// just the currently selected one, so the pane's width does not jump around
// as the selection changes.
//
// It formats every span in the log, so like facetNaturalWidth it is
// measured once in New over data that cannot change afterwards, not per
// frame.
func detailNaturalWidth(rpcSpans []span.Span) int {
	width := minDetailPaneWidth
	for _, s := range rpcSpans {
		for _, line := range spanDetailLines(s, hugeWidth) {
			width = max(width, lipgloss.Width(line))
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
// package uses: a rune count agrees with it only for as long as paneSep
// stays ASCII-plus-a-narrow-box-drawing-glyph, and the whole package's rule
// is that display columns are never inferred from rune counts.
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
// The header naming the file and the footer are always shown; the
// observer-effect caveat gives way between them when the terminal is too
// short for all three (see loggingCaveat).
//
// The result is exactly h lines with no trailing newline, and never more.
// bubbletea's renderer keeps only the LAST h lines of what View returns --
// it cannot scroll the cursor back into the terminal's scrollback buffer --
// so a view even one line too tall loses its topmost line off the top of
// the screen, and the topmost line here is the header naming the open file.
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
	return strings.Join(append(lines, clipWidth(m.footer(), w)), "\n")
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
		m.name, countMatching(f, m.log.RPCSpans), rpc, countMatching(f, m.log.UISpans), ui)
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
func (m *Model) footer() string {
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
	return footerKeys()
}

// jumpBlockedNote is what the footer says when Enter refused to jump to a
// call's log entry because the active filter hides it. Landing on a blank
// pane, or on some other call's lines further down the log, is
// indistinguishable from a jump that worked, so the refusal is stated and
// the key that lifts it is named.
const jumpBlockedNote = "target entry hidden by the active filter -- Esc clears it"

// footerKeys is the key-binding hint line shown beneath the panes. It is 62
// runes, comfortably inside the narrowest supported width (70): "q quit" was
// the tail of a longer version of this line and was the first thing clipped
// off at 70 columns, which is the one key a user must never lose sight of.
func footerKeys() string {
	return "⇥ pane  ␣ facet  ⏎ open  f facets  / search  Esc clear  q quit"
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
// line is 59 runes, so it already fits at the narrowest supported width
// (70) without needing to be shortened further.
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

// renderCentre renders the centre pane's content for the active view.
// ViewRawLog is not one of renderList's rollup/call tables -- it renders
// directly from m.log.Entries via renderRawLog -- so it is dispatched
// separately here rather than inside renderList itself.
func (m *Model) renderCentre(w, h int) string {
	if m.view == ViewRawLog {
		return m.renderRawLog(w, h)
	}
	return m.renderList(w, h)
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

// renderDetail renders the detail pane: the selected row's span, when the
// current view has one to show, at most w columns wide and h lines tall.
// ViewRawLog and the rollup views (ViewProviders, ViewTypes) have no single
// span behind the selection -- rows() returns nil for the former and every
// row's spanIdx is -1 for the latter -- so both fall through to the same
// honest placeholder rather than showing stale or zero-valued fields.
func (m *Model) renderDetail(w, h int) string {
	// The detail pane has no cursor of its own to mark, so its title
	// carries the focus instead: Tab's third stop would otherwise be
	// invisible, leaving the user no way to tell that the keyboard had
	// moved off the list.
	title := clipWidth("SPAN DETAIL", w)
	if m.pane == PaneDetail {
		title = cursorBar(title, w, true)
	}
	lines := []string{title}
	rows := m.rows()
	if m.selected < 0 || m.selected >= len(rows) || rows[m.selected].spanIdx < 0 {
		lines = append(lines, clipWidth("(no call selected)", w))
	} else {
		s := m.log.RPCSpans[rows[m.selected].spanIdx]
		lines = append(lines, spanDetailLines(s, w)...)
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
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
	lines := []string{
		clipIdentifierField("RPC   ", s.RPC, "", w, headIdentifierColumn),
		clipIdentifierField("Prov  ", s.Provider, "", w, tailIdentifierColumn),
		clipWidth(fmt.Sprintf("Dur   %s", formatMs(uint64(s.DurationMs))), w),
	}
	if s.Fidelity == span.FidelityUIReported {
		lines = append(lines, clipIdentifierField("Addr  ", s.Address, "", w, tailIdentifierColumn))
	}
	return lines
}
