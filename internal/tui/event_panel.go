package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// Event evidence has its own scope: timing source/action filters do not
// describe starts, diagnostics or planned changes without timing observations.
type eventPanelState struct {
	open          bool
	mode, address string
	selected      int
	viewport      viewport.Model
	parent        bool
	editing       bool
	query         string
	previousQuery string
	input         textinput.Model
	kind          model.EventKind
	severity      string
	expanded      map[string]bool
}

func (m *Model) openEventPanel(mode string) {
	address := ""
	if mode == "v" {
		if r := m.selectedResourceRow(); r != nil {
			address = r.Address
		}
		if index, ok := m.selectedUIOperation(); ok {
			address = m.log.UISpans[index].Address
		}
	}
	m.events = eventPanelState{open: true, mode: mode, address: address, viewport: viewport.New(1, 1), expanded: make(map[string]bool)}
	m.events.viewport.MouseWheelEnabled = false
}

func (m *Model) eventPanelTitle() string {
	switch m.events.mode {
	case "p":
		return "OUTCOMES (whole capture)"
	case "u":
		return "INCOMPLETE (whole capture)"
	case "d":
		return "DIAGNOSTICS (whole capture)"
	case "M":
		return "MILESTONES (whole capture)"
	default:
		if m.events.address != "" {
			return "EVENT HISTORY: " + logfmt.DisplayText(m.events.address)
		}
		return "EVENT HISTORY (whole capture)"
	}
}

func (m *Model) eventPanelRecords() []qualityRecord {
	var records []qualityRecord
	filter := m.eventPanelFilter()
	switch m.events.mode {
	case "p":
		records = filteredOutcomeRecords(m.log, filter)
	case "u":
		records = filteredIncompleteRecords(m.log, filter)
	case "d":
		records = diagnosticGroupRecords(m.log.Events, filter, m.events.expanded)
	case "M":
		records = milestoneRecords(m.log.Events, filter)
	default:
		records = searchableEventRecords(m.log.Events, filter, m.events.expanded)
	}
	matching, total := eventPanelEvidenceCounts(m.log, m.events.mode, filter)
	if len(records) == 0 {
		records = []qualityRecord{{text: "No matching event evidence."}}
	}
	status := fmt.Sprintf("%d/%d matching evidence; event filters are separate from timing filters.", matching, total)
	var active []string
	if filter.Query != "" {
		active = append(active, "/"+logfmt.DisplayText(filter.Query))
	}
	if filter.Kind != "" {
		active = append(active, "kind "+logfmt.DisplayText(string(filter.Kind)))
	}
	if filter.Severity != "" {
		active = append(active, "severity "+logfmt.DisplayText(filter.Severity))
	}
	if len(active) != 0 {
		status += " Filters: " + strings.Join(active, "; ")
	}
	records = append([]qualityRecord{{text: status}}, records...)
	for i := range records {
		if records[i].id.kind != "" {
			records[i].id.index = i
		}
	}
	return records
}

func (m *Model) eventPanelFilter() model.EventFilter {
	return model.EventFilter{Query: m.events.query, Address: m.events.address, Kind: m.events.kind, Severity: m.events.severity}
}

func (m *Model) eventPanelContent(w int) ([]string, []qualityActionRow) {
	return wrapQualityRecords(m.eventPanelRecords(), w)
}

func (m *Model) renderEventPanel(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines, actions := m.eventPanelContent(w)
	v := &m.events.viewport
	v.Width, v.Height = w, h
	m.events.selected = min(max(0, m.events.selected), max(0, len(actions)-1))
	if len(actions) > 0 {
		a := actions[m.events.selected]
		for i := a.start; i < a.end; i++ {
			prefix := "  "
			if i == a.start {
				prefix = "> "
			}
			lines[i] = styles.selected.Render(prefix + strings.TrimPrefix(lines[i], "  "))
		}
	}
	v.SetContent(strings.Join(lines, "\n"))
	v.SetYOffset(v.YOffset)
	if len(actions) > 0 {
		a := actions[m.events.selected]
		// Preserve paging within a long record that still overlaps the viewport.
		if a.end <= v.YOffset {
			v.SetYOffset(a.start)
		} else if a.start >= v.YOffset+v.Height {
			v.SetYOffset(a.start - v.Height + 1)
		}
	}
	return v.View()
}

func (m *Model) handleEventPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.events.editing {
		m.handleEventSearchKey(msg)
		if m.quitting {
			return m, tea.Quit
		}
		return m, nil
	}
	m.View()
	_, actions := m.eventPanelContent(m.events.viewport.Width)
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		if m.events.parent {
			m.returnFromHistory()
		} else {
			m.events.open = false
		}
	case "up", "k", "down", "j":
		delta := 1
		if msg.String() == "up" || msg.String() == "k" {
			delta = -1
		}
		m.events.selected = min(max(0, m.events.selected+delta), max(0, len(actions)-1))
		if len(actions) > 0 {
			a := actions[m.events.selected]
			v := &m.events.viewport
			if a.start < v.YOffset {
				v.SetYOffset(a.start)
			}
			if a.start >= v.YOffset+v.Height {
				v.SetYOffset(a.start - v.Height + 1)
			}
		}
	case "pgup", "pgdown":
		v := &m.events.viewport
		if msg.String() == "pgdown" {
			v.PageDown()
		} else {
			v.PageUp()
		}
		for i, a := range actions {
			if a.end > v.YOffset && a.start < v.YOffset+v.Height {
				m.events.selected = i
				break
			}
		}
	case "enter":
		if len(actions) > 0 && m.jumpToSourceLine(actions[m.events.selected].sourceLine) {
			m.events.open = false
		}
	case "r":
		if len(actions) == 0 {
			break
		}
		line := actions[m.events.selected].sourceLine
		for _, e := range m.log.Events {
			if e.Location.StartLine == line && e.Address != "" {
				m.history = append(m.history, m.captureNavigation())
				m.events = eventPanelState{open: true, mode: "v", address: e.Address, parent: true, viewport: viewport.New(1, 1), expanded: make(map[string]bool)}
				m.events.viewport.MouseWheelEnabled = false
				break
			}
		}
	case "/":
		m.events.previousQuery = m.events.query
		m.events.input = newSearchInput()
		m.events.input.SetValue(m.events.query)
		m.events.input.CursorEnd()
		m.events.editing = true
	case " ":
		if len(actions) > 0 {
			key := actions[m.events.selected].id.kind
			if strings.HasPrefix(key, "progress-group:") || strings.HasPrefix(key, "diagnostic-group:") {
				m.events.expanded[key] = !m.events.expanded[key]
				m.events.selected = 0
			}
		}
	case "f":
		m.events.kind = nextEventKind(m.events.kind)
		m.events.selected = 0
	case "s":
		m.events.severity = nextEventSeverity(m.events.severity)
		m.events.selected = 0
	}
	return m, nil
}

func (m *Model) selectedEventPanelAction() (qualityActionRow, bool) {
	if !m.events.open {
		return qualityActionRow{}, false
	}
	m.View()
	_, actions := m.eventPanelContent(m.events.viewport.Width)
	if m.events.selected < 0 || m.events.selected >= len(actions) {
		return qualityActionRow{}, false
	}
	return actions[m.events.selected], true
}

func eventPanelFooter(w int) string {
	return clipWidth("Esc return/close  ↑↓ select  PgUp/PgDn page", w) + "\n" + clipWidth("/ search  f kind  s severity  Space expand  Enter source  r resource events  q quit", w)
}

func (m *Model) handleEventSearchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		m.events.editing = false
	case tea.KeyEnter:
		m.events.editing = false
		m.events.input.Blur()
		m.events.selected = 0
	case tea.KeyEsc:
		m.events.editing = false
		m.events.query = m.events.previousQuery
		m.events.input.Blur()
	default:
		if msg.Type == tea.KeySpace {
			msg.Runes = []rune{' '}
		}
		if msg.Type == tea.KeyRunes {
			msg.Runes = []rune(logfmt.DisplayText(string(msg.Runes)))
		}
		m.events.input, _ = m.events.input.Update(msg)
		m.events.query = m.events.input.Value()
	}
}

func nextEventKind(kind model.EventKind) model.EventKind {
	kinds := []model.EventKind{"", model.EventStart, model.EventProgress, model.EventComplete, model.EventError, model.EventDiagnostic, model.EventDrift, model.EventPlannedChange, model.EventChangeSummary}
	for i, candidate := range kinds {
		if candidate == kind {
			return kinds[(i+1)%len(kinds)]
		}
	}
	return ""
}

func nextEventSeverity(severity string) string {
	values := []string{"", "warning", "error", "info"}
	for i, candidate := range values {
		if candidate == severity {
			return values[(i+1)%len(values)]
		}
	}
	return ""
}
