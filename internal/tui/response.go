package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

type responseMatch struct {
	line int
	text literalPosition
}

type responseState struct {
	open      bool
	fragments int
	notice    string
	status    bool
	viewport  viewport.Model
	lines     []string
	searching bool
	input     textinput.Model
	query     string
	notFound  bool
	match     *responseMatch
	column    int
}

const responseNavigation = "Esc/r back  ↑↓/←→ scroll  PgUp/PgDn page"

const (
	responsePartialNotice = "Partial reconstruction: other responses could not be reconstructed. Raw Log remains available."
	responseReturnText    = "Raw Log remains available. Press Esc or r to return."
)

func (m *Model) rawResponsePosition() (entry, lineOffset int, ok bool) {
	visible := m.rawLogVisible()
	for i, found := m.nextRawEntry(m.raw.top); found; i, found = m.nextRawEntry(i + 1) {
		if !visible(i) {
			continue
		}
		line := 0
		if i == m.raw.top && m.raw.topLine > 0 {
			if m.raw.topLine >= len(m.entryLines(m.log.Entries[i])) {
				continue
			}
			line = m.raw.topLine
		}
		return i, line, true
	}
	return 0, 0, false
}

func (m *Model) openResponse() {
	r := responseState{open: true, viewport: viewport.New(1, 1)}
	r.viewport.MouseWheelEnabled = false
	r.viewport.SetHorizontalStep(4)
	entry, lineOffset, ok := m.rawResponsePosition()
	selection := model.ProviderResponseSelection{State: "none"}
	if ok {
		selection = m.log.ProviderResponseAt(uint32(entry), lineOffset)
	}
	text, status := responsePresentation(selection)
	r.status = status
	if selection.State == "complete" {
		response := selection.Response
		r.fragments = len(response.Fragments)
		text = response.Text
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
		if selection.HasDiagnostics {
			r.notice = responsePartialNotice
		}
	}
	r.lines = strings.Split(text, "\n")
	for i, line := range r.lines {
		r.lines[i] = logfmt.DisplayText(line)
	}
	r.viewport.SetContent(strings.Join(r.lines, "\n"))
	m.response = r
}

func responsePresentation(selection model.ProviderResponseSelection) (text string, status bool) {
	if selection.State == "complete" {
		return selection.Response.Text, false
	}
	if selection.State == "none" {
		if selection.SourceLine == 0 {
			return "No reconstructed response at this position.\n\n" + responseReturnText, true
		}
		return fmt.Sprintf("No reconstructed response at this physical line.\n\nSource line %d.\n\n%s", selection.SourceLine, responseReturnText), true
	}
	if selection.Diagnostic == nil {
		return "Response details are unavailable.\n\n" + responseReturnText, true
	}

	headline := "This response is incomplete or invalid."
	if selection.State == "unavailable" {
		headline = "This stream is unavailable after an earlier failure."
		if selection.Diagnostic.Code == "ambiguous_ownership" {
			headline = "Response ownership is unavailable at this position."
		}
	}
	return fmt.Sprintf("%s\n\nSource line %d.\n\n%s\n\n%s", headline, selection.SourceLine, selection.Diagnostic.Error(), responseReturnText), true
}

func (m *Model) renderResponse(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	r := &m.response
	if r.notice != "" && h == 1 {
		return clipWidth(r.notice, w)
	}
	bodyHeight := h
	if r.notice != "" {
		bodyHeight--
	}
	r.viewport.Width, r.viewport.Height = w, bodyHeight
	r.viewport.SetYOffset(r.viewport.YOffset)
	r.column = min(r.column, rawLogMaxColumn(r.lines, r.viewport.Width))
	r.viewport.SetXOffset(r.column)
	if r.notice != "" {
		return clipWidth(r.notice, w) + "\n" + r.viewport.View()
	}
	return r.viewport.View()
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
			if query := r.input.Value(); query != "" {
				r.query = query
				r.match = nil
				m.searchResponse(1, true)
			}
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
	case "left", "h":
		m.View()
		r.column = max(0, min(r.column, rawLogMaxColumn(r.lines, r.viewport.Width))-4)
		r.viewport.SetXOffset(r.column)
		r.notFound = false
		r.match = nil
	case "right", "l":
		m.View()
		r.column = min(rawLogMaxColumn(r.lines, r.viewport.Width), min(r.column, rawLogMaxColumn(r.lines, r.viewport.Width))+4)
		r.viewport.SetXOffset(r.column)
		r.notFound = false
		r.match = nil
	case "up", "down", "j", "k", "pgup", "pgdown":
		m.View()
		r.viewport, _ = r.viewport.Update(msg)
		r.notFound = false
		r.match = nil
	}
	return m, nil
}

func (m *Model) searchResponse(direction int, includeCurrent bool) {
	r := &m.response
	if r.query == "" {
		return
	}
	start := r.viewport.YOffset
	column := min(r.column, rawLogMaxColumn(r.lines, r.viewport.Width))
	var anchor *literalPosition
	if !includeCurrent && r.match != nil {
		start = r.match.line
		anchor = &r.match.text
		column = -1
	}
	for i := start; i >= 0 && i < len(r.lines); i += direction {
		lineAnchor, lineColumn := (*literalPosition)(nil), -1
		if i == start {
			lineAnchor, lineColumn = anchor, column
		}
		if p, ok := findLiteral(r.lines[i], r.query, direction > 0, lineAnchor, lineColumn); ok {
			r.viewport.SetYOffset(i)
			r.column = p.column
			r.viewport.SetXOffset(r.column)
			r.match = &responseMatch{line: i, text: p}
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
	if m.response.status {
		return "RESPONSE STATUS"
	}
	title := fmt.Sprintf("RECONSTRUCTED RESPONSE (%d fragments)", m.response.fragments)
	if m.response.notice != "" {
		title += " — partial capture"
	}
	return title
}
