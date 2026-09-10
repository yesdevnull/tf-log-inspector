package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

const (
	qualityTitle      = "CAPTURE QUALITY (whole log)"
	qualityNavigation = "Esc/i close  ↑↓ scroll  PgUp/PgDn page"
)

type qualityState struct {
	open     bool
	viewport viewport.Model
}

func (m *Model) openQuality() {
	m.quality = qualityState{open: true, viewport: viewport.New(1, 1)}
	m.quality.viewport.MouseWheelEnabled = false
}

func (m *Model) handleQualityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "i":
		m.quality.open = false
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "up", "down", "j", "k", "pgup", "pgdown":
		m.View()
		m.quality.viewport, _ = m.quality.viewport.Update(msg)
	}
	return m, nil
}

func (m *Model) renderQuality(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	q := m.log.CaptureQuality()
	text := m.qualityText(q, m.log.ReconstructionQuality())
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, strings.Split(ansi.Wrap(line, max(1, w), ""), "\n")...)
	}
	v := &m.quality.viewport
	if v.Width == 0 {
		*v = viewport.New(w, h)
		v.MouseWheelEnabled = false
	}
	v.Width, v.Height = w, h
	v.SetContent(strings.Join(lines, "\n"))
	v.SetYOffset(v.YOffset)
	return v.View()
}

func (m *Model) qualityText(q model.CaptureQuality, reconstruction model.ReconstructionQuality) string {
	var b strings.Builder
	b.WriteString("TIMING AVAILABILITY\n")
	writeQualityTier(&b, "RPC", q.RPC)
	writeQualityTier(&b, "UI", q.UI)

	b.WriteString("\nANOMALIES\n")
	if len(q.Issues) == 0 {
		b.WriteString("  none recorded\n")
	}
	stage := ""
	for _, issue := range q.Issues {
		if issue.Count == 0 {
			continue
		}
		if issue.Stage != stage {
			stage = issue.Stage
			fmt.Fprintf(&b, "  %s\n", logfmt.DisplayText(stage))
		}
		fmt.Fprintf(&b, "    %s: %d", logfmt.DisplayText(issue.Code), issue.Count)
		if issue.FirstEntry != nil {
			if location, ok := m.log.SourceLocation(*issue.FirstEntry); ok {
				fmt.Fprintf(&b, " (first at %s, line %d", logfmt.DisplayText(m.name), location.StartLine)
				if location.EndLine != location.StartLine {
					fmt.Fprintf(&b, "-%d", location.EndLine)
				}
				b.WriteByte(')')
			} else {
				b.WriteString(" (location unavailable)")
			}
		}
		b.WriteByte('\n')
	}

	b.WriteString("\nATTRIBUTION\n")
	if !q.HasContext {
		b.WriteString("  unavailable: no address context was recognised\n")
	} else {
		if q.NameableShare == nil {
			b.WriteString("  nameable duration: unavailable (total RPC duration is 0ms)\n")
		} else {
			fmt.Fprintf(&b, "  nameable duration: %dms / %dms (%.1f%%)\n", q.NameableMs, q.RPCDurationMs, *q.NameableShare*100)
		}
		fmt.Fprintf(&b, "  attributed spans: %d / %d\n", q.Attribution.ByConfidence[attrib.Contained]+q.Attribution.ByConfidence[attrib.Likely], q.Attribution.Spans)
	}
	for _, confidence := range []attrib.Confidence{attrib.Contained, attrib.Likely, attrib.Overlapping, attrib.Ambiguous, attrib.Unattributed} {
		fmt.Fprintf(&b, "  %-12s %d spans, %dms\n", confidence.String(), q.Attribution.ByConfidence[confidence], q.Attribution.MsByConfidence[confidence])
	}

	b.WriteString("\nCONTEXT LIMITATIONS\n")
	if hasQualityIssue(q.Issues, "context", "context_incomplete") {
		b.WriteString("  Context evidence is incomplete; this limits attribution and does not establish that an operation failed.\n")
	} else if q.HasContext {
		b.WriteString("  no incomplete contexts recorded\n")
	} else {
		b.WriteString("  no address context was recognised\n")
	}
	b.WriteString("  Recognition requires structured records to carry literal @level and @timestamp keys.\n")
	b.WriteString("  Malformed hclog headers are not recovered as independent RPC records.\n")

	b.WriteString("\nRECONSTRUCTION\n")
	switch reconstruction.State {
	case "checked":
		fmt.Fprintf(&b, "  checked: %d responses available\n", reconstruction.Responses)
	case "failed":
		fmt.Fprintf(&b, "  failed: %s\n", logfmt.DisplayText(reconstruction.Code))
	default:
		b.WriteString("  not checked (response reconstruction is lazy)\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
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
	limited := q.RPC.Rejected > 0 || q.UI.Rejected > 0 || q.RPC.Admitted > q.RPC.Positioned || q.UI.Admitted > q.UI.Positioned ||
		q.RPC.DurationLowerBound || q.UI.DurationLowerBound || len(q.Issues) > 0 || !q.HasContext
	if q.HasContext {
		limited = limited || q.Attribution.ByConfidence[attrib.Overlapping] > 0 || q.Attribution.ByConfidence[attrib.Ambiguous] > 0 || q.Attribution.ByConfidence[attrib.Unattributed] > 0
	}
	if m.log.ReconstructionQuality().State == "failed" {
		limited = true
	}
	if limited {
		return "i limitations"
	}
	return "i quality"
}

func hasQualityIssue(issues []model.QualityIssue, stage, code string) bool {
	for _, issue := range issues {
		if issue.Stage == stage && issue.Code == code && issue.Count > 0 {
			return true
		}
	}
	return false
}
