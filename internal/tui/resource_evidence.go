package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

const (
	resourceEvidenceTitle      = "RESOURCE EVIDENCE (selected scope)"
	resourceEvidenceNavigation = "Esc/e close  ↑↓ scroll  PgUp/PgDn page"
)

func (m *Model) openResourceEvidence() {
	m.showResourceEvidence = true
	m.resourceEvidenceViewport = viewport.New(1, 1)
	m.resourceEvidenceViewport.MouseWheelEnabled = false
}

func (m *Model) handleResourceEvidenceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "e":
		m.showResourceEvidence = false
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "up", "down", "j", "k", "pgup", "pgdown":
		m.View()
		m.resourceEvidenceViewport, _ = m.resourceEvidenceViewport.Update(msg)
	}
	return m, nil
}

func (m *Model) renderResourceEvidence(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(m.resourceEvidenceText(), "\n") {
		lines = append(lines, strings.Split(ansi.Wrap(line, max(1, w), ""), "\n")...)
	}
	v := &m.resourceEvidenceViewport
	if v.Width == 0 {
		*v = viewport.New(w, h)
		v.MouseWheelEnabled = false
	}
	v.Width, v.Height = w, h
	v.SetContent(strings.Join(lines, "\n"))
	v.SetYOffset(v.YOffset)
	return v.View()
}

func (m *Model) resourceEvidenceText() string {
	p := m.selectedResources()
	var b strings.Builder
	if index, ok := m.selectedUIOperation(); ok {
		s := m.log.UISpans[index]
		action := logfmt.DisplayText(s.RPC)
		if action == "" {
			action = "unavailable"
		}
		b.WriteString("SELECTED OBSERVED UI OPERATION\n")
		fmt.Fprintf(&b, "  address: %s\n", logfmt.DisplayText(s.Address))
		fmt.Fprintf(&b, "  action: %s\n", action)
		if location, ok := m.log.SourceLocation(s.Entry); ok {
			fmt.Fprintf(&b, "  source: %s, line %d", logfmt.DisplayText(m.name), location.StartLine)
			if location.EndLine != location.StartLine {
				fmt.Fprintf(&b, "-%d", location.EndLine)
			}
			b.WriteByte('\n')
		} else {
			b.WriteString("  source: unavailable\n")
		}
		fmt.Fprintf(&b, "  observed UI duration: %s\n", durationTotalText(operationDuration(s.DurationMs, s.DurationSaturated)))
		m.writeUIQualifications(&b, operationDuration(s.DurationMs, s.DurationSaturated), []int{index}, "UI timing", "Observed UI duration is a lower bound (≥).")
		if s.StartClamped {
			b.WriteString("  Operation start was clamped to the capture origin.\n")
		}
		b.WriteByte('\n')
	}
	if r := m.selectedResourceRow(); r != nil {
		b.WriteString("SELECTED RESOURCE ROW (separate from baseline scope)\n")
		fmt.Fprintf(&b, "  address: %s\n", logfmt.DisplayText(r.Address))
		fmt.Fprintf(&b, "  operations: %d\n", r.UI.Count)
		fmt.Fprintf(&b, "  observed UI total: %s\n", durationTotalText(r.UI))
		fmt.Fprintf(&b, "  observed UI max: %s\n", durationMaxText(r.UI))
		fmt.Fprintf(&b, "  inferred Contained/Likely RPCs: %d, %s\n", r.NamedRPC.Count, durationTotalText(r.NamedRPC))
		fmt.Fprintf(&b, "  inferred Overlapping RPCs: %d, %s\n", r.OverlappingRPC.Count, durationTotalText(r.OverlappingRPC))
		b.WriteString("  RPC evidence is inferred and partial.\n")
		b.WriteString("  It does not recover every RPC call.\n")
		indices := make([]int, 0, len(r.Operations))
		for _, op := range r.Operations {
			indices = append(indices, op.UIIndex)
		}
		m.writeUIQualifications(&b, r.UI, indices, "UI timings", "Observed UI total and max are lower bounds (≥).")
		b.WriteByte('\n')
	}
	b.WriteString("BASELINE FILTERS\n")
	f := m.filter()
	fmt.Fprintf(&b, "  providers (RPC only): %s\n", selectedValues(f.Providers))
	fmt.Fprintf(&b, "  resource types (RPC and UI): %s\n", selectedValues(f.Types))
	fmt.Fprintf(&b, "  RPC methods (RPC only): %s\n", selectedValues(f.RPCs))
	fmt.Fprintf(&b, "  exact addresses: %s\n", selectedValues(m.resourceSelection.Addresses))
	fmt.Fprintf(&b, "  module subtrees: %s\n", selectedModuleValues(m.resourceSelection.Modules))

	b.WriteString("\nSELECTED RPC PARTITION\n")
	writeEvidenceTotal(&b, "baseline", p.Selection.Baseline)
	writeNamedEvidence(&b, "selected", p.Selection.Selected)
	writeNamedEvidence(&b, "other", p.Selection.Other)
	writeEvidenceTotal(&b, "unresolved", p.Selection.Unresolved)
	b.WriteString("  Selected and other are resolved named associations; unresolved is potentially relevant work. Together they divide the provider/type/method baseline.\n")

	b.WriteString("\nOBSERVED UI OPERATIONS\n")
	writeEvidenceTotalWithNoun(&b, "selected UI", p.UI, "operation", "operations")
	writeEvidenceTotalWithNoun(&b, "unnamed UI", p.UnnamedUI, "operation", "operations")
	m.writeUIQualifications(&b, p.UI, p.UIIndices, "Selected UI timings, including unnamed operations", "Selected UI total is a lower bound (≥).")
	b.WriteString("  UI scope uses resource type plus exact address/module selection; provider and RPC method do not apply.\n")

	b.WriteString("\nPRESELECTION RESOURCE EVIDENCE\n")
	writeEvidenceTotal(&b, "baseline", p.Evidence.Baseline)
	writeEvidenceTotal(&b, "missing resource type", p.Evidence.MissingType)
	writeEvidenceTotal(&b, "no address context", p.Evidence.NoContext)
	writeEvidenceTotal(&b, "contained", p.Evidence.Contained)
	writeEvidenceTotal(&b, "likely", p.Evidence.Likely)
	writeEvidenceTotal(&b, "overlapping", p.Evidence.Overlapping)
	writeEvidenceTotal(&b, "ambiguous", p.Evidence.Ambiguous)
	writeEvidenceTotal(&b, "unattributed", p.Evidence.Unattributed)
	b.WriteString("  These scoped buckets use a different denominator from whole-log attribution quality; use i quality for the complete whole-log facts.\n")
	if p.Evidence.Baseline.Count == 0 {
		b.WriteString("  percentages unavailable: baseline has no observations\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (m *Model) writeUIQualifications(b *strings.Builder, total model.DurationTotal, indices []int, timingSubject, lowerBoundLine string) {
	if total.Count == 0 {
		return
	}
	fmt.Fprintf(b, "  %s are rounded to whole seconds, +/- 1s each.\n", timingSubject)
	if total.LowerBound {
		b.WriteString("  " + lowerBoundLine + "\n")
	}
	unpositioned := 0
	for _, i := range indices {
		if !m.log.UISpans[i].HasPosition() {
			unpositioned++
		}
	}
	if unpositioned > 0 {
		fmt.Fprintf(b, "  Timeline position unavailable for %d of %d observed operations.\n", unpositioned, len(indices))
	}
}

func (m *Model) selectedResourceRow() *model.ResourceRow {
	if m.view != ViewResources || m.resourceOperations {
		return nil
	}
	rows := m.rows()
	if m.selected < 0 || m.selected >= len(rows) {
		return nil
	}
	return rows[m.selected].resource
}

func selectedValues(values map[string]bool) string {
	return selectedDisplayValues(values, logfmt.DisplayText)
}

func selectedModuleValues(values map[string]bool) string {
	return selectedDisplayValues(values, func(value string) string { return displayFacetValue(dimModule, value) })
}

func selectedDisplayValues(values map[string]bool, display func(string) string) string {
	if values == nil {
		return "all"
	}
	var selected []string
	for value, included := range values {
		if included {
			selected = append(selected, value)
		}
	}
	if len(selected) == 0 {
		return "none"
	}
	sort.Strings(selected)
	for i := range selected {
		selected[i] = display(selected[i])
	}
	return strings.Join(selected, ", ")
}

func writeEvidenceTotal(b *strings.Builder, label string, d model.DurationTotal) {
	writeEvidenceTotalWithNoun(b, label, d, "call", "calls")
}

func writeEvidenceTotalWithNoun(b *strings.Builder, label string, d model.DurationTotal, singular, pluralNoun string) {
	fmt.Fprintf(b, "  %s: %d %s, %s\n", label, d.Count, plural(int(d.Count), singular, pluralNoun), durationTotalText(d))
}

func writeNamedEvidence(b *strings.Builder, label string, e model.NamedEvidence) {
	writeEvidenceTotal(b, label, combineDurationTotals(e.Contained, e.Likely, e.Overlapping))
	writeEvidenceTotal(b, "  contained", e.Contained)
	writeEvidenceTotal(b, "  likely", e.Likely)
	writeEvidenceTotal(b, "  overlapping", e.Overlapping)
}

func combineDurationTotals(totals ...model.DurationTotal) model.DurationTotal {
	var result model.DurationTotal
	for _, d := range totals {
		result.Count += d.Count
		result.TotalMs += d.TotalMs
		result.MaxMs = max(result.MaxMs, d.MaxMs)
		result.LowerBound = result.LowerBound || d.LowerBound
	}
	return result
}
