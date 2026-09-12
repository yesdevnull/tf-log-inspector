package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// workbenchPaneHeight reserves file identity, navigation, status and actions.
// A taller frame also separates its navigation from the content with a blank.
func workbenchPaneHeight(h int) int {
	if h <= 0 {
		h = defaultHeight
	}
	if h >= 8 {
		return h - 5
	}
	return max(0, h-4)
}

// workbenchView keeps the navigation above the content and the active prompt
// at the bottom, including when the terminal cannot fit a content pane.
func (m *Model) workbenchView() string {
	w, h := m.paneWidth(), m.height
	if h <= 0 {
		h = defaultHeight
	}
	identity := strings.TrimPrefix(header(m), "tfli "+headerSep+" ")
	head := styles.selected.Render(" tfli ") + "  "
	separator := " " + headerSep + " "
	if split := strings.LastIndex(identity, separator); split >= 0 {
		name, counts := identity[:split], identity[split+len(separator):]
		gap := max(2, w-lipgloss.Width(head+name+counts))
		head += styles.title.Render(name) + strings.Repeat(" ", gap) + styles.note.Render(counts)
	} else {
		head += styles.title.Render(identity)
	}
	head = clipWidth(head, w)
	lines := []string{head}
	if h == 1 {
		return head
	}
	footer := strings.Split(m.footer(w), "\n")
	action := clipWidth(footer[len(footer)-1], w)
	if h >= 3 {
		navigation := m.navigation(w)
		if m.showHelp {
			navigation = styleHintKeys(helpCloseHint) + "  " + styles.note.Render("Esc closes help")
		} else if m.response.open {
			navigation = m.responseNavigationHint()
		} else if m.quality.open {
			navigation = qualityNavigation
		} else if m.showResourceEvidence {
			navigation = resourceEvidenceNavigation
		}
		lines = append(lines, clipWidth(navigation, w))
	}
	if h >= 8 {
		summary := ""
		if len(m.log.UISpans) > 0 {
			summary = clipWidth(styles.note.Render(m.captureSummary()), w)
		}
		lines = append(lines, summary)
	}
	if paneH := workbenchPaneHeight(h); paneH > 0 {
		panes := strings.Split(m.renderPanes(w, paneH), "\n")
		for i := 0; i < paneH; i++ {
			line := ""
			if i < len(panes) {
				line = clipWidth(panes[i], w)
			}
			lines = append(lines, line)
		}
	}
	if h >= 4 {
		status := shortLoggingCaveat
		switch {
		case m.response.open:
			status = m.responseTitle()
		case m.quality.open:
			status = qualityTitle
			if m.quality.notice != "" {
				status = m.quality.notice
			}
		case m.showHelp:
			status = "↑↓ scroll  PgUp/PgDn page  ?/Esc close help"
		case m.showResourceEvidence:
			status = resourceEvidenceTitle
		case m.timelineNoticeVisible(w):
			status = m.timeline.notice
		case m.view == ViewRawLog:
			scope := "whole log"
			if m.raw.scope != nil {
				scope = "call scope"
			}
			if m.facetOverlayShowing(w) {
				status = "No visible source line · " + scope
				break
			}
			rows := m.rawLogRows(paneBodyHeight(workbenchPaneHeight(h)))
			visible := make([]string, len(rows))
			for i, row := range rows {
				visible[i] = row.text
			}
			column := min(m.raw.column, rawLogMaxColumn(visible, m.rawLogViewportWidth()))
			switch {
			case len(rows) == 0:
				status = "No visible source line · " + scope
			case rows[0].sourceLine == 0:
				status = fmt.Sprintf("Source line unavailable · entry %d/%d · %s", rows[0].entry+1, len(m.log.Entries), scope)
			default:
				first := rows[0]
				status = fmt.Sprintf("Line %d · entry %d/%d · column %d · %s", first.sourceLine, first.entry+1, len(m.log.Entries), column+1, scope)
			}
			status = clipValueEnd(status, w)
		}
		lines = append(lines, styles.note.Render(clipWidth(status, w)))
	}
	return strings.Join(append(lines, action), "\n")
}
