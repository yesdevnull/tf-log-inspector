package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// filterTitle counts hidden facet values so an empty result remains explainable.
func (m *Model) filterTitle() string {
	n := 0
	for _, values := range m.excludedFacets {
		for _, excluded := range values {
			if excluded {
				n++
			}
		}
	}
	if n > 0 {
		return fmt.Sprintf("%s (%d)", facetPaneTitle, n)
	}
	return facetPaneTitle
}

// navigation keeps the active view visible, shortening labels before dropping
// other views on narrow terminals. Help always provides their full names.
func (m *Model) navigation(w int) string {
	labels := make([]string, len(views))
	for i, b := range views {
		labels[i] = " " + b.key + " " + b.name + " "
	}
	if lipgloss.Width(strings.Join(labels, hintSep)+hintSep+helpHint) > w {
		short := []string{"prov", "types", "calls", "time", "log"}
		for i, b := range views {
			labels[i] = " " + b.key + " " + short[i] + " "
		}
	}
	if lipgloss.Width(strings.Join(labels, hintSep)+hintSep+helpHint) > w {
		for i, b := range views {
			if b.view == m.view {
				return clipWidth(styles.selected.Render(labels[i])+hintSep+styleHintKeys(helpHint), w)
			}
		}
	}
	for i, b := range views {
		if b.view == m.view {
			labels[i] = styles.selected.Render(labels[i])
		} else {
			labels[i] = " " + styleHintKeys(strings.TrimSpace(labels[i])) + " "
		}
	}
	return strings.Join(labels, hintSep) + hintSep + styleHintKeys(helpHint)
}

// renderKeyHints gives navigation and actions separate rows, with the same
// context-sensitive action choices used by the plain key hints.
func (m *Model) renderKeyHints(w int) string {
	if m.showHelp {
		return styleHintKeys(m.keyHints(w))
	}
	return m.navigation(w) + "\n" + m.actionHelp(w)
}

// actionHelp drops whole hints when space is tight, reserving a way to quit.
func (m *Model) actionHelp(w int) string {
	if w < len(quitHint) {
		return clipWidth(styleHintKeys(quitHint), w)
	}
	available := w - len(quitHint) - len(hintSep)
	used := 0
	hints := strings.Split(m.actionKeys(w), hintSep)
	bindings := make([]key.Binding, 0, len(hints))
	for _, hint := range hints {
		if hint == quitHint {
			continue
		}
		n := lipgloss.Width(hint)
		if len(bindings) > 0 {
			n += len(hintSep)
		}
		if used+n > available {
			break
		}
		used += n
		k, description, _ := strings.Cut(hint, " ")
		bindings = append(bindings, key.NewBinding(key.WithKeys(k), key.WithHelp(k, description)))
	}
	h := help.Model{
		ShortSeparator: hintSep,
		Styles:         help.Styles{ShortKey: styles.key},
	}
	line := h.ShortHelpView(bindings)
	if line != "" {
		line += hintSep
	}
	return line + styleHintKeys(quitHint)
}

// helpBinding uses Bubbles' help renderer with the terminal's own palette.
func helpBinding(k, description string) string {
	h := help.Model{Styles: help.Styles{ShortKey: styles.key}}
	return h.ShortHelpView([]key.Binding{key.NewBinding(key.WithKeys(k), key.WithHelp(k, description))})
}
