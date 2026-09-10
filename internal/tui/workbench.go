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
			navigation = responseNavigation
		} else if m.quality.open {
			navigation = qualityNavigation
		} else if m.showResourceEvidence {
			navigation = resourceEvidenceNavigation
		}
		lines = append(lines, clipWidth(navigation, w))
	}
	if h >= 8 {
		lines = append(lines, "")
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
		case m.showHelp:
			status = "↑↓ scroll  PgUp/PgDn page  ?/Esc close help"
		case m.showResourceEvidence:
			status = resourceEvidenceTitle
		case m.view == ViewRawLog:
			scope := "all entries"
			if m.raw.scope != nil {
				scope = "call scope"
			}
			if len(m.log.Entries) == 0 {
				status = "Entry 0/0 · " + scope
				break
			}
			visible := m.rawLogLines(paneBodyHeight(workbenchPaneHeight(h)))
			column := min(m.raw.column, rawLogMaxColumn(visible, m.rawLogViewportWidth()))
			status = fmt.Sprintf("Entry %d/%d · line %d · column %d · %s", m.raw.top+1, len(m.log.Entries), m.raw.topLine+1, column+1, scope)
		}
		lines = append(lines, styles.note.Render(clipWidth(status, w)))
	}
	return strings.Join(append(lines, action), "\n")
}
