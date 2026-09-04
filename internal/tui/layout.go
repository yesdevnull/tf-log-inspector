package tui

import (
	"fmt"
	"strings"

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

// Fixed widths for the side panes. The list (centre) pane takes whatever is
// left, since it is the pane every view's actual content lives in -- the
// side panes exist to summarise or filter it, not compete with it for
// space.
const (
	facetPaneWidth  = 24
	detailPaneWidth = 24
)

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
func (m Model) View() string {
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
	fmt.Fprintf(&b, "\n%s\n", clipWidth(footerKeys(), w))
	return b.String()
}

// header names the file and its span counts.
func header(m Model) string {
	return fmt.Sprintf("tfli -- %s -- %d RPC spans, %d UI spans", m.name, len(m.log.RPCSpans), len(m.log.UISpans))
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
// show everything still shows something rather than an empty pane.
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
func (m Model) renderPanes(w, h int) string {
	if w < facetInlineWidth && m.showFacetOverlay {
		return m.renderFacets(w, h)
	}
	switch {
	case w >= facetInlineWidth:
		listW := w - facetPaneWidth - detailPaneWidth - 2*len([]rune(paneSep))
		return joinPanes(h,
			pane{m.renderFacets(facetPaneWidth, h), facetPaneWidth},
			pane{m.renderCentre(listW, h), listW},
			pane{m.renderDetail(detailPaneWidth, h), detailPaneWidth},
		)
	case w >= detailInlineWidth:
		listW := w - detailPaneWidth - len([]rune(paneSep))
		return joinPanes(h,
			pane{m.renderCentre(listW, h), listW},
			pane{m.renderDetail(detailPaneWidth, h), detailPaneWidth},
		)
	default:
		return m.renderCentre(w, h)
	}
}

// renderCentre renders the centre pane's content for the active view.
// ViewRawLog is not one of renderList's rollup/call tables -- it renders
// directly from m.log.Entries via renderRawLog (Task 5) -- so it is
// dispatched separately here rather than inside renderList itself.
func (m Model) renderCentre(w, h int) string {
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
func (m Model) renderDetail(w, h int) string {
	lines := []string{clipWidth("SPAN DETAIL", w)}
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
func spanDetailLines(s span.Span, w int) []string {
	lines := []string{
		clipWidth(fmt.Sprintf("RPC   %s", s.RPC), w),
		clipWidth(fmt.Sprintf("Prov  %s", s.Provider), w),
		clipWidth(fmt.Sprintf("Dur   %s", formatMs(uint64(s.DurationMs))), w),
	}
	if s.Fidelity == span.FidelityUIReported {
		lines = append(lines, clipWidth(fmt.Sprintf("Addr  %s", s.Address), w))
	}
	return lines
}
