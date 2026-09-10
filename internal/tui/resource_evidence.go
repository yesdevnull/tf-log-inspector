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
	b.WriteString("BASELINE FILTERS\n")
	f := m.filter()
	fmt.Fprintf(&b, "  providers (RPC only): %s\n", selectedValues(f.Providers))
	fmt.Fprintf(&b, "  resource types (RPC and UI): %s\n", selectedValues(f.Types))
	fmt.Fprintf(&b, "  RPC methods (RPC only): %s\n", selectedValues(f.RPCs))
	fmt.Fprintf(&b, "  exact addresses: %s\n", selectedValues(m.resourceSelection.Addresses))
	fmt.Fprintf(&b, "  module subtrees: %s\n", selectedValues(m.resourceSelection.Modules))

	b.WriteString("\nSELECTED RPC PARTITION\n")
	writeEvidenceTotal(&b, "baseline", p.Selection.Baseline)
	writeNamedEvidence(&b, "selected", p.Selection.Selected)
	writeNamedEvidence(&b, "other", p.Selection.Other)
	writeEvidenceTotal(&b, "unresolved", p.Selection.Unresolved)
	b.WriteString("  Selected and other are resolved named associations; unresolved is potentially relevant work. Together they divide the provider/type/method baseline.\n")

	b.WriteString("\nOBSERVED UI OPERATIONS\n")
	writeEvidenceTotalWithNoun(&b, "selected UI", p.UI, "operation", "operations")
	writeEvidenceTotalWithNoun(&b, "unnamed UI", p.UnnamedUI, "operation", "operations")
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

func selectedValues(values map[string]bool) string {
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
		selected[i] = logfmt.DisplayText(selected[i])
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
