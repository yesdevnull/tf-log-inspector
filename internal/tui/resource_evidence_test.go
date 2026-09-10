package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourceEvidenceShowsDisjointSelectionAndPreselectionBuckets(t *testing.T) {
	l := &model.Log{
		RPCSpans: []span.Span{
			{DurationMs: 10, ResourceType: "aws_instance", TimestampStatus: logfmt.TimestampValid},
			{DurationMs: 20, ResourceType: "aws_instance", TimestampStatus: logfmt.TimestampValid},
			{DurationMs: 5, ResourceType: "aws_instance", TimestampStatus: logfmt.TimestampValid},
		},
		Attribs: []attrib.Attribution{
			{Address: "aws_instance.a", ModuleKnown: true, Confidence: attrib.Contained},
			{Address: "aws_instance.b", ModuleKnown: true, Confidence: attrib.Likely},
			{Confidence: attrib.Ambiguous},
		},
		Contexts: []attrib.Context{{Address: "aws_instance.a"}},
	}
	m := New(l, "synthetic.log")
	m.resourceSelection = model.ResourceSelection{Addresses: map[string]bool{"aws_instance.a": true}}
	m.invalidateRows()
	m.openResourceEvidence()
	got := m.renderResourceEvidence(60, 100)
	for _, want := range []string{"baseline: 3 calls, 35ms", "selected: 1 call, 10ms", "other: 1 call, 20ms", "unresolved: 1 call, 5ms", "resolved named associations", "potentially relevant work", "PRESELECTION RESOURCE EVIDENCE", "different denominator", "i quality"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence missing %q:\n%s", want, got)
		}
	}
}

func TestResourceEvidenceIsModalAndScrollable(t *testing.T) {
	m := New(&model.Log{}, "empty.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if !m.showResourceEvidence {
		t.Fatal("evidence did not open from a timing view")
	}
	view, pane, selected := m.view, m.pane, m.selected
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.view != view || m.pane != pane || m.selected != selected {
		t.Fatal("modal key changed underlying state")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.resourceEvidenceViewport.YOffset == 0 {
		t.Fatal("evidence did not scroll")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if m.showResourceEvidence {
		t.Fatal("evidence did not close")
	}
}

func TestResourceEvidenceMethodFilterChangesOnlyRPCBaseline(t *testing.T) {
	l := &model.Log{
		RPCSpans: []span.Span{
			{DurationMs: 10, RPC: "Apply", ResourceType: "aws_instance"},
			{DurationMs: 20, RPC: "Read", ResourceType: "aws_instance"},
		},
		UISpans: []span.Span{{Address: "aws_instance.a", DurationMs: 7, ResourceType: "aws_instance"}},
	}
	m := New(l, "synthetic.log")
	m.excludedFacets = map[string]map[string]bool{dimRPC: {"Read": true}}
	m.invalidateRows()
	got := m.resourceEvidenceText()
	for _, want := range []string{"RPC methods (RPC only): Apply", "baseline: 1 call, 10ms", "selected UI: 1 operation, 7ms"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence missing %q:\n%s", want, got)
		}
	}
}

func TestResourceEvidenceDistinguishesUnavailableFromMeasuredZero(t *testing.T) {
	l := &model.Log{RPCSpans: []span.Span{
		{RPC: "Apply", DurationMs: 0},
		{RPC: "Read", DurationMs: 5, ResourceType: "aws_instance"},
	}}
	m := New(l, "synthetic.log")
	got := m.resourceEvidenceText()
	for _, want := range []string{"baseline: 2 calls, 5ms", "missing resource type: 1 call, 0s", "no address context: 1 call, 5ms"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "percentages unavailable") {
		t.Fatal("observations with zero-duration evidence were described as unavailable")
	}

	empty := New(&model.Log{}, "empty.log")
	if got := empty.resourceEvidenceText(); !strings.Contains(got, "percentages unavailable") {
		t.Fatalf("empty evidence did not explain unavailable denominator:\n%s", got)
	}
}

func TestResourceEvidenceMakesSelectedResourceDetailReachable(t *testing.T) {
	address := "module.app.aws_instance.accounting"
	l := &model.Log{UISpans: []span.Span{{
		Address: address, ResourceType: "aws_instance", DurationMs: 1000,
		DurationSaturated: true, TimestampStatus: logfmt.TimestampMissing,
	}}}
	m := New(l, "synthetic.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got := unstyled(m.View())
	for _, want := range []string{"SELECTED RESOURCE ROW", address, "operations: 1", "lower bound", "position unavailable"} {
		if !strings.Contains(got, want) {
			t.Errorf("60-column evidence missing %q:\n%s", want, got)
		}
	}

	longAddress := "module." + strings.Repeat("long_key.", 12) + "aws_instance.accounting"
	m = New(&model.Log{UISpans: []span.Span{{Address: longAddress, ResourceType: "aws_instance"}}}, "synthetic.log")
	m.setView(ViewResources)
	m.openResourceEvidence()
	if content := m.resourceEvidenceText(); !strings.Contains(content, longAddress) {
		t.Fatal("long selected address is absent from the scrollable evidence content")
	}
}

func TestResourceEvidenceLabelsSelectedRootModule(t *testing.T) {
	m := New(&model.Log{}, "empty.log")
	m.resourceSelection.Modules = map[string]bool{"": true}
	got := m.resourceEvidenceText()
	if !strings.Contains(got, "module subtrees: (root subtree)") {
		t.Fatalf("root module evidence =\n%s", got)
	}
}
