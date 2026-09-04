package tui

import (
	"fmt"
	"strings"

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
		if n := len([]rune(facetSectionHeader(f.Name))); n > width {
			width = n
		}
		kind := facetValueKind(f.Name)
		for _, v := range f.Values {
			if n := len([]rune(facetValueLine(" ", v.Value, v.Count, hugeWidth, kind))); n > width {
				width = n
			}
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
			if n := len([]rune(line)); n > width {
				width = n
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
// The header naming the file and the observer-effect caveat are always
// shown in full, regardless of how the panes below them degrade.
//
// The result is exactly h lines with no trailing newline, and never more.
// bubbletea's renderer keeps only the LAST h lines of what View returns --
// it cannot scroll the cursor back into the terminal's scrollback buffer --
// so a view even one line too tall loses its topmost line off the top of
// the screen, and the topmost line here is the header naming the open file.
func (m *Model) View() string {
	w, h := m.width, m.height
	if w <= 0 {
		w = defaultWidth
	}
	if h <= 0 {
		h = defaultHeight
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", clipWidth(header(m), w))
	paneH := paneHeight(h)
	b.WriteString(m.renderPanes(w, paneH))
	b.WriteString("\n\n")
	writeLoggingCaveat(&b, w)
	fmt.Fprintf(&b, "\n%s", clipWidth(m.footer(), w))

	lines := strings.Split(b.String(), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// header names the file and its span counts.
func header(m *Model) string {
	return fmt.Sprintf("tfli -- %s -- %d RPC spans, %d UI spans", m.name, len(m.log.RPCSpans), len(m.log.UISpans))
}

// footer is the line beneath the panes. It is the search prompt while a
// query is being typed -- '/' captures every key, so the prompt is what
// tells the user their keyboard has been taken over and shows them what
// they have typed -- and the result of a search that found nothing, which
// otherwise looks exactly like a search that matched the entry already on
// screen. Otherwise it is the key hints.
func (m *Model) footer() string {
	switch {
	case m.raw.searching:
		return "/" + m.raw.query
	case m.raw.notFound:
		return "/" + m.raw.lastQuery + "  pattern not found"
	default:
		return footerKeys()
	}
}

// footerKeys is the key-binding hint line shown beneath the panes. It is 62
// runes, comfortably inside the narrowest supported width (70): "q quit" was
// the tail of a longer version of this line and was the first thing clipped
// off at 70 columns, which is the one key a user must never lose sight of.
func footerKeys() string {
	return "⇥ pane  ␣ facet  ⏎ open  f facets  / search  Esc clear  q quit"
}

// paneHeight is how many lines the pane row itself gets once the header (2
// lines, including its trailing blank), the caveat (writeLoggingCaveatLines
// lines) and its surrounding blank lines, and the footer (1 line) are taken
// out of the total height h. It never goes below 1: a terminal too short to
// show everything still shows something rather than an empty pane, and View
// then trims the surplus off the BOTTOM, keeping the header and the pane row
// rather than whatever happened to fit last.
func paneHeight(h int) int {
	fixed := 2 + 1 + writeLoggingCaveatLines + 1 + 1
	if paneH := h - fixed; paneH > 0 {
		return paneH
	}
	return 1
}

// writeLoggingCaveat states that every duration this interface renders was
// measured under logging. Terraform re-logs each line of a provider's stderr
// through its own logger, so a provider that dumps HTTP bodies at DEBUG pays
// that cost per line: four captures of one workspace measured 24.1s with no
// logging enabled against 522.2s with debug plus provider TRACE. A reader
// who mistook these figures for wall-clock truth would be optimising time
// that does not exist without the log, so the caveat travels with every
// rendered duration rather than living only in documentation. Its longest
// line is 59 runes, so it already fits at the narrowest supported width
// (70) without needing to be shortened further.
func writeLoggingCaveat(b *strings.Builder, w int) {
	for _, line := range []string{
		"Durations here are measured under logging, which is not",
		"free: one workspace planned in 24.1s unlogged and 522.2s",
		"with debug plus provider TRACE. Rankings hold, since every",
		"span paid the same cost, but absolute times do not transfer",
		"to an unlogged run.",
	} {
		fmt.Fprintf(b, "%s\n", clipWidth(line, w))
	}
}

// writeLoggingCaveatLines is the number of lines writeLoggingCaveat always
// emits, so paneHeight can budget for it without the two ever drifting out
// of sync.
const writeLoggingCaveatLines = 5

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
		listW := w - facetW - detailW - 2*len([]rune(paneSep))
		return joinPanes(h,
			pane{m.renderFacets(facetW, h), facetW},
			pane{m.renderCentre(listW, h), listW},
			pane{m.renderDetail(detailW, h), detailW},
		)
	case w >= detailInlineWidth:
		detailW := detailPaneWidth(m.detailPaneNatural, w)
		listW := w - detailW - len([]rune(paneSep))
		return joinPanes(h,
			pane{m.renderCentre(listW, h), listW},
			pane{m.renderDetail(detailW, h), detailW},
		)
	default:
		return m.renderCentre(w, h)
	}
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
// current view has one to show, at most w runes wide and h lines tall.
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
// and is ready for whichever later task makes a UI-hook span selectable.
//
// Prov and Addr are identifiers (see clipValueFront) and go through
// clipIdentifierField, front-clipped rather than end-clipped, so two spans
// with different providers or addresses that happen to share a long prefix
// -- ".../hashicorp/azuread" and ".../azurerm" -- stay distinguishable
// instead of both clipping down to their identical shared head. RPC is
// deliberately left on plain clipWidth: it names one of a short, closed set
// of plugin-protocol methods (ApplyResourceChange, ReadDataSource, ...)
// that diverge within their first few characters, so an end-clip of an RPC
// name does not collide the way an end-clip of a registry address does --
// the same reasoning internal/profile.actionColWidth uses for its own
// closed-vocabulary action column. Dur is a formatted number and is never
// long enough to need either treatment.
func spanDetailLines(s span.Span, w int) []string {
	lines := []string{
		clipWidth(fmt.Sprintf("RPC   %s", s.RPC), w),
		clipIdentifierField("Prov  ", s.Provider, "", w, tailIdentifierColumn),
		clipWidth(fmt.Sprintf("Dur   %s", formatMs(uint64(s.DurationMs))), w),
	}
	if s.Fidelity == span.FidelityUIReported {
		lines = append(lines, clipIdentifierField("Addr  ", s.Address, "", w, tailIdentifierColumn))
	}
	return lines
}
