package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

const (
	qualityTitle      = "CAPTURE QUALITY (whole log)"
	qualityNavigation = "Esc/i close  ↑↓ select  PgUp/PgDn page"
)

type qualityState struct {
	open     bool
	viewport viewport.Model
	selected qualityItemID
	notice   string
}

func (m *Model) openQuality() {
	m.quality = qualityState{open: true, viewport: viewport.New(1, 1), selected: qualityItemID{kind: "check"}}
	m.quality.viewport.MouseWheelEnabled = false
}

func (m *Model) handleQualityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.quality.notice = ""
	switch msg.String() {
	case "esc", "i":
		m.quality.open = false
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "enter":
		_, actions := m.buildQualityContent(m.quality.viewport.Width)
		for _, action := range actions {
			if action.id != m.quality.selected {
				continue
			}
			return m, m.activateQualityAction(action)
		}
	case "up", "k", "down", "j":
		m.View()
		_, actions := m.buildQualityContent(m.quality.viewport.Width)
		m.moveQualitySelection(actions, msg.String() == "down" || msg.String() == "j")
	case "pgup", "pgdown":
		m.View()
		v := &m.quality.viewport
		if msg.String() == "pgdown" {
			v.PageDown()
		} else {
			v.PageUp()
		}
		_, actions := m.buildQualityContent(v.Width)
		m.selectVisibleQualityAction(actions, msg.String() == "pgdown")
	}
	return m, nil
}

func (m *Model) activateQualityAction(action qualityActionRow) tea.Cmd {
	if action.id.kind == "check" {
		return m.requestResponseCheck()
	}
	if action.sourceLine == 0 {
		return nil
	}
	if m.jumpToSourceLine(action.sourceLine) {
		m.quality.open = false
		return nil
	}
	m.quality.notice = "Source location unavailable."
	return nil
}

func (m *Model) buildQualityContent(w int) ([]string, []qualityActionRow) {
	reconstruction := m.log.ReconstructionQuality()
	records := m.qualityRecords(m.log.CaptureQuality(), reconstruction)
	return wrapQualityRecords(records, w)
}

func (m *Model) revealQualityAction(action qualityActionRow) {
	v := &m.quality.viewport
	if v.Height <= 0 {
		return
	}
	if action.end > v.YOffset && action.start < v.YOffset+v.Height {
		return
	}
	if action.start < v.YOffset {
		v.SetYOffset(action.start)
	}
	if action.start >= v.YOffset+v.Height {
		v.SetYOffset(action.start - v.Height + 1)
	}
}

func (m *Model) moveQualitySelection(actions []qualityActionRow, down bool) {
	if len(actions) == 0 {
		m.quality.selected = qualityItemID{}
		return
	}
	i := -1
	for n := range actions {
		if actions[n].id == m.quality.selected {
			i = n
			break
		}
	}
	if i < 0 {
		if down {
			for n := range actions {
				if actions[n].start >= m.quality.viewport.YOffset {
					i = n
					break
				}
			}
		} else {
			for n := len(actions) - 1; n >= 0; n-- {
				if actions[n].end <= m.quality.viewport.YOffset+m.quality.viewport.Height {
					i = n
					break
				}
			}
		}
	} else if down && i < len(actions)-1 {
		i++
	} else if !down && i > 0 {
		i--
	}
	if i >= 0 {
		m.quality.selected = actions[i].id
		m.revealQualityAction(actions[i])
	}
}

func (m *Model) selectVisibleQualityAction(actions []qualityActionRow, down bool) {
	start, end := m.quality.viewport.YOffset, m.quality.viewport.YOffset+m.quality.viewport.Height
	m.quality.selected = qualityItemID{}
	if down {
		for _, a := range actions {
			if a.end > start && a.start < end {
				m.quality.selected = a.id
				return
			}
		}
	} else {
		for i := len(actions) - 1; i >= 0; i-- {
			a := actions[i]
			if a.end > start && a.start < end {
				m.quality.selected = a.id
				return
			}
		}
	}
}

func (m *Model) renderQuality(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines, actions := m.buildQualityContent(w)
	v := &m.quality.viewport
	if v.Width == 0 {
		*v = viewport.New(w, h)
		v.MouseWheelEnabled = false
	}
	v.Width, v.Height = w, h
	for _, action := range actions {
		if action.id == m.quality.selected {
			for i := action.start; i < action.end; i++ {
				prefix := "  "
				if i == action.start {
					prefix = "> "
				}
				lines[i] = styles.selected.Render(prefix + strings.TrimPrefix(lines[i], "  "))
			}
			break
		}
	}
	v.SetContent(strings.Join(lines, "\n"))
	v.SetYOffset(v.YOffset)
	for _, action := range actions {
		if action.id == m.quality.selected {
			m.revealQualityAction(action)
			break
		}
	}
	return v.View()
}

func (m *Model) qualityText(q model.CaptureQuality, reconstruction model.ReconstructionQuality) string {
	records := m.qualityRecords(q, reconstruction)
	texts := make([]string, len(records))
	for i := range records {
		texts[i] = records[i].text
	}
	return strings.Join(texts, "\n")
}

func writeQualityTier(b *strings.Builder, name string, q model.TierQuality) {
	availability := "unavailable"
	if q.Admitted > 0 {
		availability = fmt.Sprintf("%dms", q.DurationMs)
		if q.DurationLowerBound {
			availability += " (lower bound)"
		}
	}
	excluded := fmt.Sprintf("%dms", q.ExcludedMs)
	if q.DurationLowerBound && q.ExcludedMs > 0 {
		excluded += " (lower bound)"
	}
	fmt.Fprintf(b, "  %s timing: records %d, admitted %d, rejected %d; duration %s\n",
		name, q.Records, q.Admitted, q.Rejected, availability)
	fmt.Fprintf(b, "    positioned %d / %dms; excluded %d / %s\n",
		q.Positioned, q.PositionedMs, q.Admitted-q.Positioned, excluded)
}

func (m *Model) qualityIndicator() string {
	q := m.log.CaptureQuality()
	if qualityHasLimitations(q, m.log.ReconstructionQuality()) {
		return "i limitations"
	}
	return "i quality"
}

func qualityHasLimitations(q model.CaptureQuality, reconstruction model.ReconstructionQuality) bool {
	limited := q.RPC.Rejected > 0 || q.UI.Rejected > 0 || q.RPC.Admitted > q.RPC.Positioned || q.UI.Admitted > q.UI.Positioned ||
		q.RPC.DurationLowerBound || q.UI.DurationLowerBound || len(q.Issues) > 0 || !q.HasContext
	if q.HasContext {
		limited = limited || q.Attribution.ByConfidence[attrib.Likely] > 0 || q.Attribution.ByConfidence[attrib.Overlapping] > 0 || q.Attribution.ByConfidence[attrib.Ambiguous] > 0 || q.Attribution.ByConfidence[attrib.Unattributed] > 0
	}
	if reconstruction.State == "partial" || reconstruction.State == "failed" {
		limited = true
	}
	return limited
}

func qualityStageUnits(stage string) string {
	switch stage {
	case "rpc_duration":
		return "recognised RPC response records rejected at admission, or admitted spans with position or storage limitations"
	case "ui_duration":
		return "completion records rejected at admission; timestamp issues cover all decoded structured envelopes; admitted spans may have position or storage limitations"
	case "ui_decode":
		return "structured envelopes with JSON or schema issues"
	case "context":
		return "structured envelope and context events"
	case "scan":
		return "timestamp issues count timestamped hclog entries; line_count_saturated counts excess physical continuation lines after an entry line counter reaches its cap"
	case "interning":
		return "values not retained after an interning cap"
	case "capability":
		return "state flags; 1 means the request-tracking cap was reached"
	default:
		return "events recorded at this extraction stage"
	}
}

func hasQualityIssue(issues []model.QualityIssue, stage, code string) bool {
	for _, issue := range issues {
		if issue.Stage == stage && issue.Code == code && issue.Count > 0 {
			return true
		}
	}
	return false
}
