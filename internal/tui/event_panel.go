package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// Event evidence has its own scope: timing source/action filters do not
// describe starts, diagnostics or planned changes without timing observations.
type eventPanelState struct {
	open          bool
	mode, address string
	selected      int
	viewport      viewport.Model
	parent        bool
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
	m.events = eventPanelState{open: true, mode: mode, address: address, viewport: viewport.New(1, 1)}
	m.events.viewport.MouseWheelEnabled = false
}

func (m *Model) eventPanelTitle() string {
	switch m.events.mode {
	case "p":
		return "OUTCOMES (whole capture)"
	case "u":
		return "INCOMPLETE (whole capture)"
	default:
		if m.events.address != "" {
			return "EVENT HISTORY: " + logfmt.DisplayText(m.events.address)
		}
		return "EVENT HISTORY (whole capture)"
	}
}

func (m *Model) eventPanelRecords() []qualityRecord {
	var records []qualityRecord
	switch m.events.mode {
	case "p":
		records = outcomeRecords(m.log)
	case "u":
		records = incompleteRecords(m.log)
	default:
		records = eventRecords(m.log, m.events.address)
	}
	if len(records) == 0 {
		records = []qualityRecord{{text: "No events observed for this scope."}}
	}
	records = append([]qualityRecord{{text: "Event evidence; timing filters do not apply."}}, records...)
	for i := range records {
		if records[i].id.kind != "" {
			records[i].id.index = i
		}
	}
	return records
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
				m.events = eventPanelState{open: true, mode: "v", address: e.Address, parent: true, viewport: viewport.New(1, 1)}
				m.events.viewport.MouseWheelEnabled = false
				break
			}
		}
	}
	return m, nil
}

func eventPanelFooter(w int) string {
	return clipWidth("Esc return/close  ↑↓ select  PgUp/PgDn page", w) + "\n" + clipWidth("Enter source  r resource events  q quit", w)
}
