package tui

import (
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func newSearchInput() textinput.Model {
	input := textinput.New()
	input.Prompt = "/"
	input.PromptStyle = styleRenderer.NewStyle()
	input.TextStyle = styleRenderer.NewStyle()
	input.Cursor.Style = styleRenderer.NewStyle()
	input.Cursor.TextStyle = input.TextStyle
	input.Cursor.SetMode(cursor.CursorStatic)
	// Terminal bracketed paste arrives as runes; no clipboard process is needed.
	input.KeyMap.Paste.SetEnabled(false)
	input.Focus()
	return input
}

// searchPrompt scrolls the editable text to keep the cursor within the footer.
func (m Model) searchPrompt(w int) string {
	if w < 2 {
		return clipWidth("/", w)
	}
	input := newSearchInput()
	input.Width = max(1, w-2)
	input.SetValue(logfmt.DisplayText(m.raw.query))
	if m.raw.input.Value() == m.raw.query {
		input.SetCursor(m.raw.input.Position())
	} else {
		input.CursorEnd()
	}
	return clipWidth(input.View(), w)
}

// handleSearchKey keeps submission and cancellation local while the editor
// handles character movement and deletion.
func (m *Model) handleSearchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.raw.searching = false
		m.raw.input.Blur()
		m.quitting = true
	case tea.KeyEnter:
		m.raw.searching = false
		m.raw.input.Blur()
		if m.raw.query != "" {
			m.raw.lastQuery = m.raw.query
			m.raw.notFound = !m.searchFrom(m.raw.top, true, true)
		}
	case tea.KeyEsc:
		m.raw.searching = false
		m.raw.input.Blur()
		m.raw.query = ""
		m.raw.notFound = false
	default:
		if msg.Type == tea.KeySpace {
			msg.Runes = []rune{' '}
		}
		if msg.Type == tea.KeyRunes {
			msg.Runes = []rune(logfmt.DisplayText(string(msg.Runes)))
		}
		m.raw.input, _ = m.raw.input.Update(msg)
		m.raw.query = m.raw.input.Value()
	}
}
