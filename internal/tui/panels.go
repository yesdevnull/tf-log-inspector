package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// Panels reserve a border and one inset column on each side.
func panelContentWidth(w int) int { return max(0, w-4) }

func panelBorder(p pane) lipgloss.Style {
	if p.focused {
		return styles.key
	}
	return styles.chrome
}

func panelTitle(s string) string {
	r := []rune(strings.ToLower(s))
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}
