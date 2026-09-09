package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

type responseState struct {
	open      bool
	fragments int
	viewport  viewport.Model
	lines     []string
	searching bool
	input     textinput.Model
	query     string
	notFound  bool
	matchLine int
}

const responseNavigation = "Esc/r back  ↑↓/←→ scroll  PgUp/PgDn page"

func (m *Model) openResponse() {
	r := responseState{open: true, viewport: viewport.New(1, 1)}
	r.viewport.MouseWheelEnabled = false
	r.viewport.SetHorizontalStep(4)
	text := "No reconstructed response for this entry."
	visible := m.rawLogVisible()
	for i, ok := m.nextRawEntry(m.raw.top); ok; i, ok = m.nextRawEntry(i + 1) {
		if !visible(i) {
			continue
		}
		response, err := m.log.ProviderResponse(m.log.Entries[i])
		if err != nil {
			text = "Cannot reconstruct provider response: malformed or incomplete log. Raw Log remains available."
		} else if response.Text != "" {
			r.fragments = len(response.Fragments)
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, []byte(response.Text), "", "  "); err == nil {
				text = pretty.String()
			}
			var envelope struct {
				Message string `json:"@message"`
			}
			if json.Unmarshal([]byte(response.Text), &envelope) == nil && strings.Contains(envelope.Message, "\n") {
				text = "Decoded @message:\n" + envelope.Message + "\n\nJSON:\n" + text
			}
		}
		break
	}
	r.lines = strings.Split(text, "\n")
	for i, line := range r.lines {
		r.lines[i] = logfmt.DisplayText(line)
	}
	r.viewport.SetContent(strings.Join(r.lines, "\n"))
	m.response = r
}

func (m *Model) renderResponse(w, h int) string {
	m.response.viewport.Width, m.response.viewport.Height = max(1, w), max(1, h)
	m.response.viewport.SetYOffset(m.response.viewport.YOffset)
	return m.response.viewport.View()
}

func (m *Model) handleResponseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &m.response
	if msg.String() == "ctrl+c" || (!r.searching && msg.String() == "q") {
		m.quitting = true
		return m, tea.Quit
	}
	if r.searching {
		switch msg.Type {
		case tea.KeyEsc:
			r.searching = false
		case tea.KeyEnter:
			r.searching = false
			r.query = r.input.Value()
			m.searchResponse(1, true)
		default:
			if msg.Type == tea.KeyRunes {
				msg.Runes = []rune(logfmt.DisplayText(string(msg.Runes)))
			}
			r.input, _ = r.input.Update(msg)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "r":
		r.open = false
	case "/":
		r.searching, r.notFound = true, false
		r.input = newSearchInput()
	case "n":
		m.searchResponse(1, false)
	case "N":
		m.searchResponse(-1, false)
	case "up", "down", "j", "k", "pgup", "pgdown", "left", "right", "h", "l":
		m.View()
		r.viewport, _ = r.viewport.Update(msg)
		r.notFound = false
		r.matchLine = r.viewport.YOffset
	}
	return m, nil
}

func (m *Model) searchResponse(direction int, includeCurrent bool) {
	r := &m.response
	if r.query == "" {
		return
	}
	start := r.viewport.YOffset
	if !includeCurrent {
		start = r.matchLine + direction
	}
	for i := start; i >= 0 && i < len(r.lines); i += direction {
		if column := strings.Index(r.lines[i], r.query); column >= 0 {
			r.viewport.SetYOffset(i)
			r.viewport.SetXOffset(ansi.StringWidth(r.lines[i][:column]))
			r.matchLine = i
			r.notFound = false
			return
		}
	}
	r.notFound = true
}

func (m *Model) responseFooter(w int) string {
	r := &m.response
	if r.searching {
		r.input.Width = max(1, w-2)
		return clipWidth(r.input.View(), w)
	}
	if r.notFound {
		return clipWidth("/"+logfmt.DisplayText(r.query)+"  pattern not found", w)
	}
	return clipWidth(responseNavigation, w) + "\n" + clipWidth("/ search  n/N next/previous  q quit", w)
}

func (m *Model) responseTitle() string {
	return fmt.Sprintf("RECONSTRUCTED RESPONSE (%d fragments)", m.response.fragments)
}
