package tui

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

const sourceLinePromptLabel = "Go to source line: "

type sourceLineInputState struct {
	editing bool
	value   string
	err     string
	input   textinput.Model
}

func newSourceLineInput() textinput.Model {
	input := newSearchInput()
	input.Prompt = sourceLinePromptLabel
	return input
}

func parseSourceLine(value string) (uint64, error) {
	if value == "" {
		return 0, errors.New("enter a source line")
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return 0, errors.New("use decimal digits only")
		}
	}
	line, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errors.New("source line is too large")
	}
	if line == 0 {
		return 0, errors.New("source line must be positive")
	}
	return line, nil
}

func (m *Model) beginSourceLineInput() {
	m.sourceLine = sourceLineInputState{editing: true, input: newSourceLineInput()}
}

func (m *Model) handleSourceLineInputKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.sourceLine.editing = false
		m.sourceLine.input.Blur()
		m.quitting = true
	case tea.KeyEsc:
		m.sourceLine = sourceLineInputState{}
	case tea.KeyEnter:
		line, err := parseSourceLine(m.sourceLine.value)
		if err != nil {
			m.sourceLine.err = err.Error()
			return
		}
		if !m.jumpToSourceLine(line) {
			m.sourceLine.err = "source line is outside this log"
			return
		}
		m.sourceLine = sourceLineInputState{}
	default:
		if msg.Type == tea.KeySpace {
			msg.Runes = []rune{' '}
		}
		if msg.Type == tea.KeyRunes {
			msg.Runes = []rune(logfmt.DisplayText(string(msg.Runes)))
		}
		m.sourceLine.input, _ = m.sourceLine.input.Update(msg)
		m.sourceLine.value = m.sourceLine.input.Value()
		m.sourceLine.err = ""
	}
}

func (m Model) sourceLinePrompt(width int) string {
	errText := ""
	if m.sourceLine.err != "" {
		errText = " · " + logfmt.DisplayText(m.sourceLine.err)
	}
	input := newSourceLineInput()
	remaining := max(1, width-lipgloss.Width(sourceLinePromptLabel))
	errBudget := 0
	if errText != "" {
		errBudget = min(lipgloss.Width(errText), max(8, remaining/2))
	}
	input.Width = max(1, remaining-errBudget)
	input.SetValue(logfmt.DisplayText(m.sourceLine.value))
	if m.sourceLine.input.Value() == m.sourceLine.value {
		input.SetCursor(m.sourceLine.input.Position())
	} else {
		input.CursorEnd()
	}
	return clipValueEnd(strings.TrimRight(input.View(), " ")+errText, width)
}
