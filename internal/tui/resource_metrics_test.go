package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
	"strings"
	"testing"
)

func TestSourceActionFacetsShareProjectionAndRestore(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{
		{Address: "aws_instance.a", ResourceType: "aws_instance", RPC: "create", DurationMs: 1000, DurationSource: span.SourceUIElapsed},
		{Address: "aws_instance.a", ResourceType: "aws_instance", RPC: "delete", DurationMs: 3000, DurationSource: span.SourceUIElapsed},
		{Address: "aws_instance.b", ResourceType: "aws_instance", RPC: "create", DurationMs: 9000, DurationSource: span.SourceCLIElapsed},
	}}, "metrics.log")
	if len(m.facetValues("duration source")) != 2 || len(m.facetValues("lifecycle action")) != 2 {
		t.Fatal("missing source/action choices")
	}
	m.restrictFacet("duration source", "ui_elapsed")
	m.restrictFacet("lifecycle action", "create")
	m.invalidateRows()
	m.setView(ViewResources)
	if p := m.selectedResources(); p.UI.Count != 1 || p.UI.TotalMs != 1000 {
		t.Fatalf("projection: %+v", p)
	}
	if got := m.timelineTitle(); strings.Contains(got, "mixed") {
		t.Fatalf("timeline: %s", got)
	}
	if got := m.resourceEvidenceText(); strings.Contains(got, "cli_elapsed: 1") {
		t.Fatalf("evidence retained excluded source: %s", got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.filterActive() || m.selectedResources().UI.Count != 1 {
		t.Fatal("Esc lost source/action selection")
	}
	m.clearFilters()
	if m.selectedResources().UI.Count != 3 {
		t.Fatal("clear did not restore observations")
	}
}

func TestModuleRankingDrillsIntoExactInstanceAndRestores(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{
		{Address: "module.a[0].aws_instance.x", ResourceType: "aws_instance", DurationMs: 1000},
		{Address: "module.a[0].aws_instance.y", ResourceType: "aws_instance", DurationMs: 3000, DurationSaturated: true},
		{Address: "module.a[1].aws_instance.x", ResourceType: "aws_instance", DurationMs: 500},
	}}, "metrics.log")
	m.setView(ViewResources)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	rows := m.rows()
	if len(rows) != 2 || rows[0].identity.kind != "module" {
		t.Fatalf("module toggle: %+v", rows)
	}
	if !strings.Contains(m.renderResources(120, 20), "≥2.0s") {
		t.Fatal("module mean missing lower bound")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.rows()) != 2 || m.rows()[0].identity.kind != "resource" || m.selectedResources().UI.Count != 2 {
		t.Fatal("module did not open exact scoped resources")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.rows()) != 2 || m.rows()[0].identity.kind != "module" {
		t.Fatal("Esc did not restore module ranking")
	}
	m.setView(ViewTypes)
	if !strings.Contains(m.renderList(160, 25), "res mean") {
		t.Fatal("Types missing mean")
	}
}

func TestNarrowResourceMeanSortRemainsVisible(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{
		{Address: "aws_instance.a", DurationMs: 1},
		{Address: "aws_instance.a", DurationMs: 2},
	}}, "means.log")
	m.setView(ViewResources)
	m.setActiveSort(9)
	m.invalidateRows()
	got := m.renderResources(56, 20)
	if !strings.Contains(got, "mean▾") || !strings.Contains(got, "1.5ms") {
		t.Fatalf("narrow selected mean lost: %s", got)
	}
}
