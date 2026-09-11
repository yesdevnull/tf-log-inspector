package tui

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

type responseInspectionState struct {
	running    bool
	generation uint64
}

type responseRequest struct {
	id         uint64
	entry      uint32
	lineOffset int
	valid      bool
}

type responseInspectionDoneMsg struct {
	log        *model.Log
	generation uint64
}

type responseReadyMsg struct {
	log          *model.Log
	requestID    uint64
	presentation responseState
}

func responseInspectionCmd(l *model.Log, generation uint64) tea.Cmd {
	return func() tea.Msg {
		l.InspectProviderResponses()
		return responseInspectionDoneMsg{log: l, generation: generation}
	}
}

func (m *Model) requestResponseCheck() tea.Cmd {
	if m.log.ReconstructionQuality().State != "not_checked" || m.inspection.running {
		return nil
	}
	m.inspection.generation++
	m.inspection.running = true
	return responseInspectionCmd(m.log, m.inspection.generation)
}

func (m *Model) completeResponseInspection(msg responseInspectionDoneMsg) tea.Cmd {
	if m.quitting || msg.log != m.log || !m.inspection.running || msg.generation != m.inspection.generation {
		return nil
	}
	m.inspection.running = false
	return m.queueResponseSelection()
}

func (m *Model) queueResponseSelection() tea.Cmd {
	r := &m.response
	if m.quitting || !r.open || !r.pending || r.resolving {
		return nil
	}
	if m.log.ReconstructionQuality().State == "not_checked" {
		return nil
	}
	r.resolving = true
	r.lines = []string{"Preparing response…"}
	r.viewport.SetContent(strings.Join(r.lines, "\n"))
	return responseSelectionCmd(m.log, r.request)
}

func responseSelectionCmd(l *model.Log, request responseRequest) tea.Cmd {
	return func() tea.Msg {
		selection := model.ProviderResponseSelection{State: "none"}
		if request.valid {
			selection = l.ProviderResponseAt(request.entry, request.lineOffset)
		}
		return responseReadyMsg{log: l, requestID: request.id, presentation: presentResponse(selection)}
	}
}

func presentResponse(selection model.ProviderResponseSelection) responseState {
	r := responseState{viewport: viewport.New(1, 1)}
	r.viewport.MouseWheelEnabled = false
	r.viewport.SetHorizontalStep(4)
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
	return r
}

func (m *Model) completeResponseSelection(msg responseReadyMsg) {
	r := &m.response
	if m.quitting || msg.log != m.log || !r.open || !r.pending || !r.resolving || msg.requestID != r.request.id {
		return
	}
	request := r.request
	*r = msg.presentation
	r.open = true
	r.request = request
	r.pending = false
	r.resolving = false
}

func (m *Model) responseNavigationHint() string {
	if m.response.pending {
		return "Esc/r back"
	}
	return responseNavigation
}
