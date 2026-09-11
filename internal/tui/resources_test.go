package tui

import (
	"math"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourcesKeyAndEvidence(t *testing.T) {
	m := New(&model.Log{}, "empty.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.view != ViewResources {
		t.Fatalf("view=%v", m.view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if !m.showResourceEvidence {
		t.Fatal("evidence did not open")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.showResourceEvidence || m.view != ViewResources {
		t.Fatal("return state lost")
	}
}

func TestResourcesGroupObservedOperationsAndOpenTheirList(t *testing.T) {
	l := &model.Log{UISpans: []span.Span{
		{Address: "aws_instance.b", DurationMs: 5, ResourceType: "aws_instance"},
		{Address: "aws_instance.a", DurationMs: 10, ResourceType: "aws_instance"},
		{Address: "aws_instance.a", DurationMs: 20, ResourceType: "aws_instance"},
	}}
	m := New(l, "synthetic.log")
	m.setView(ViewResources)
	rows := m.rows()
	if len(rows) != 2 || rows[0].resource.Address != "aws_instance.a" || rows[0].resource.UI.Count != 2 {
		t.Fatalf("resource rows = %+v, want A first with two operations", rows)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewResources || !m.resourceOperations || len(m.history) != 1 || len(m.rows()) != 2 || m.selectedRowOpens() {
		t.Fatal("Enter did not open the two observed operations without inventing source")
	}
}

func TestResourcesRenderObservedQualificationsAndSafeAddress(t *testing.T) {
	l := &model.Log{UISpans: []span.Span{{
		Address: "aws_instance.bad\x1b[31m", DurationMs: math.MaxUint32,
		DurationSaturated: true, ResourceType: "aws_instance", TimestampStatus: logfmt.TimestampMissing,
	}}}
	m := New(l, "synthetic.log")
	m.setView(ViewResources)
	got := m.renderResources(180, 20)
	for _, want := range []string{"\\x1b[31m", "lower bound", "whole seconds", "position unavailable"} {
		if !strings.Contains(got, want) {
			t.Errorf("resources = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "\x1b[31m") {
		t.Fatal("untrusted address emitted a terminal control sequence")
	}
}

func TestResourcesSortOnlyUsesObservedColumns(t *testing.T) {
	m := New(&model.Log{}, "empty.log")
	m.setView(ViewResources)
	want := []int{3, 0, 1, 2}
	for _, col := range want {
		m.cycleSort()
		if got := m.sortCol[ViewResources]; got != col {
			t.Fatalf("sort column = %d, want %d", got, col)
		}
	}
}

func TestResourcesRPCOnlyExplainsObservedRankingUnavailable(t *testing.T) {
	m := New(&model.Log{RPCSpans: []span.Span{{DurationMs: 10, ResourceType: "aws_instance"}}}, "rpc.log")
	m.setView(ViewResources)
	got := m.renderResources(100, 20)
	for _, want := range []string{"no observed resource operations", "4 calls", "2 types", "i quality", "e evidence"} {
		if !strings.Contains(got, want) {
			t.Errorf("resources = %q, want %q", got, want)
		}
	}
}

func TestResourcesNameSeparateUIAndRPCScopes(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{{Address: "aws_instance.a", ResourceType: "aws_instance"}}}, "ui.log")
	m.setView(ViewResources)
	got := m.renderResources(180, 20)
	for _, want := range []string{
		"Scopes: UI type/resource/module; RPC provider/type/method/resource/module.",
		"Inferred RPC evidence is partial",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("resources missing %q:\n%s", want, got)
		}
	}
}

func TestResourcesDistinguishUnnamedRejectedAndFilteredEmpty(t *testing.T) {
	tests := []struct {
		name string
		log  *model.Log
		prep func(*Model)
		want string
	}{
		{"unnamed", &model.Log{UISpans: []span.Span{{ResourceType: "aws_instance"}}}, nil, "no exact address: observed resource operations are ungrouped"},
		{"rejected", &model.Log{UIEvidence: span.TimingEvidence{Records: 1, Rejected: map[string]span.IssueCount{"duration_missing": {Count: 1}}}}, nil, "completion records were rejected"},
		{"filtered", &model.Log{UISpans: []span.Span{{Address: "aws_instance.a", ResourceType: "aws_instance"}}}, func(m *Model) {
			m.resourceSelection.Addresses = map[string]bool{"aws_instance.a": false}
			m.invalidateRows()
		}, noMatchNote},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.log, "synthetic.log")
			m.setView(ViewResources)
			if tt.prep != nil {
				tt.prep(&m)
			}
			if got := m.renderResources(180, 20); !strings.Contains(got, tt.want) {
				t.Errorf("resources missing %q:\n%s", tt.want, got)
			}
		})
	}
}

func TestResourcesKeepAddressAndObservedMeasurementsAtOrdinaryWidth(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{{Address: "aws_instance.accounting", DurationMs: 1200, ResourceType: "aws_instance"}}}, "ui.log")
	m.setView(ViewResources)
	got := unstyled(m.renderResources(46, 20))
	for _, want := range []string{"accounting", "operations", "res total", "res max"} {
		if !strings.Contains(got, want) {
			t.Errorf("46-column resources lost %q:\n%s", want, got)
		}
	}
}

func TestResourceDetailWrapsTheFullEscapedAddress(t *testing.T) {
	r := &model.ResourceRow{
		Address:        `module.long.aws_instance.accounting`,
		UI:             model.DurationTotal{Count: 2, TotalMs: 3000, MaxMs: 2000},
		NamedRPC:       model.DurationTotal{Count: 1, TotalMs: 10},
		OverlappingRPC: model.DurationTotal{Count: 1, TotalMs: 5},
	}
	got := strings.Join(resourceDetailSections(r, 20)[0], "\n")
	joined := strings.ReplaceAll(got, "\n", "")
	if strings.Contains(got, "…") || !strings.Contains(joined, r.Address) {
		t.Fatalf("resource detail did not preserve the full address:\n%s", got)
	}
	prose := strings.Join(strings.Fields(got), " ")
	for _, want := range []string{"observed resource total: 3.0s", "inferred Contained/Likely RPCs: 1, 10ms", "inferred Overlapping RPCs: 1, 5ms"} {
		if !strings.Contains(prose, want) {
			t.Errorf("resource detail lost %q:\n%s", want, got)
		}
	}
}

func TestResourcesShortEmptyFramePrioritisesExplanation(t *testing.T) {
	tests := []struct {
		name string
		log  *model.Log
		want []string
	}{
		{"rpc only", &model.Log{RPCSpans: []span.Span{{ResourceType: "aws_instance"}}}, []string{"no observed resource operations", "4 calls", "e evidence"}},
		{"unnamed", &model.Log{UISpans: []span.Span{{ResourceType: "aws_instance"}}}, []string{"ungrouped", "exact address"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.log, "synthetic.log")
			m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
			got := unstyled(m.View())
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("short Resources frame missing %q:\n%s", want, got)
				}
			}
		})
	}
}
