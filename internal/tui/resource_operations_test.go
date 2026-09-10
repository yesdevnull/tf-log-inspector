package tui

import (
	"math"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourceOperationsRetainRepeatedSourceIdentity(t *testing.T) {
	m := New(testLog(t, "resources-modules.log"), "resources-modules.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	rows := m.rows()
	if len(rows) != 2 {
		t.Fatalf("operations = %d, want 2", len(rows))
	}
	if rows[0].identity.kind != "ui" || rows[0].identity.index != 1 ||
		rows[1].identity.kind != "ui" || rows[1].identity.index != 0 {
		t.Fatal("duration-ranked operations lost original UI identity")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatal("operation did not open source")
	}
	if target := int(m.log.UISpans[1].Entry); m.raw.top > target || target-m.raw.top > jumpContextLines {
		t.Fatalf("raw top = %d, want UI index 1 entry %d within context", m.raw.top, target)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewResources || len(m.rows()) != 2 ||
		m.rows()[m.selected].identity.index != 1 {
		t.Fatal("source return lost selected operation")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewResources || len(m.rows()) != 1 || m.rows()[0].resource == nil {
		t.Fatal("second return did not restore aggregate")
	}
}

func TestResourceOperationsSortAndOpenOriginalEntries(t *testing.T) {
	m := newOperationTestModel()
	if !m.openResourceOperations() {
		t.Fatal("resource row did not open operations")
	}
	if got := operationIdentities(m.rows()); !reflect.DeepEqual(got, []int{3, 0, 1, 2}) {
		t.Fatalf("default identities = %v, want [3 0 1 2]", got)
	}
	m.cycleSort()
	if got := operationIdentities(m.rows()); !reflect.DeepEqual(got, []int{3, 1, 0, 2}) {
		t.Fatalf("source-ranked identities = %v, want [3 1 0 2]", got)
	}
	m.selected = 2
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || m.raw.top > 1 {
		t.Fatalf("sorted operation opened view %v at entry %d, want entry 1 with context", m.view, m.raw.top)
	}
}

func TestResourceOperationsRenderUnavailableAndQualifiedFacts(t *testing.T) {
	m := newOperationTestModel()
	m.openResourceOperations()
	got := unstyled(m.renderResourceOperations(100, 20))
	for _, want := range []string{"unavailable", "0s", "≥4294967.3s"} {
		if !strings.Contains(got, want) {
			t.Errorf("operation table missing %q:\n%s", want, got)
		}
	}
	m.selected = 3
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewResources || len(m.history) != 1 {
		t.Fatal("operation without a source location opened a misleading target")
	}
	m.selected = 0
	detail := unstyled(strings.Join(m.operationDetailSections(1, 60)[0], "\n"))
	for _, want := range []string{"aws_instance.a", "Apply", "line 2", "whole seconds"} {
		if !strings.Contains(detail, want) {
			t.Errorf("operation detail missing %q:\n%s", want, detail)
		}
	}
}

func TestResourceOperationWithoutTimelinePositionStillOpensSource(t *testing.T) {
	m := New(testLog(t, "resources-long-lower-bound.log"), "resources-long-lower-bound.log")
	m.setView(ViewResources)
	if !m.openResourceOperations() {
		t.Fatal("resource row did not open operations")
	}
	index, ok := m.selectedUIOperation()
	if !ok || m.log.UISpans[index].HasPosition() {
		t.Fatalf("selected operation = %d, %v; want unpositioned source operation", index, ok)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatal("unpositioned operation did not open its source")
	}
}

func TestResourceOperationReportsStartClampSeparately(t *testing.T) {
	m := newOperationTestModel()
	m.log.UISpans[1].StartClamped = true
	m.openResourceOperations()
	m.selected = 2
	detail := strings.Join(m.operationDetailSections(1, 60)[0], "\n")
	evidence := m.resourceEvidenceText()
	for name, got := range map[string]string{"detail": detail, "evidence": evidence} {
		if !strings.Contains(got, "start was clamped") || !strings.Contains(got, "rounded to whole seconds") {
			t.Errorf("%s did not report clamp and rounding separately:\n%s", name, got)
		}
	}
}

func TestResourceOperationsPreserveAndRestoreSelection(t *testing.T) {
	m := newOperationTestModel()
	m.resourceSelection.Addresses = map[string]bool{"aws_instance.a": true, "aws_instance.b": true}
	m.resourceSelection.Modules = map[string]bool{"module.app": true}
	m.excludedFacets = map[string]map[string]bool{dimProvider: {"registry.test": true}, dimRPC: {"Read": true}, dimLevel: {"WARN": true}}
	wantExclusions := cloneExclusions(m.excludedFacets)
	wantModules := map[string]bool{"module.app": true}
	if !m.openResourceOperations() {
		t.Fatal("resource row did not open operations")
	}
	if !reflect.DeepEqual(m.resourceSelection.Addresses, map[string]bool{"aws_instance.a": true}) {
		t.Fatalf("child addresses = %v", m.resourceSelection.Addresses)
	}
	if !reflect.DeepEqual(m.resourceSelection.Modules, wantModules) || !reflect.DeepEqual(m.excludedFacets, wantExclusions) {
		t.Fatal("operation route changed non-address selections")
	}
	if !m.returnFromHistory() || !reflect.DeepEqual(m.resourceSelection.Addresses, map[string]bool{"aws_instance.a": true, "aws_instance.b": true}) || !reflect.DeepEqual(m.resourceSelection.Modules, wantModules) || !reflect.DeepEqual(m.excludedFacets, wantExclusions) {
		t.Fatal("parent selection was not restored")
	}

	m = newOperationTestModel()
	if m.resourceSelection.Addresses != nil {
		t.Fatal("fixture unexpectedly starts with address selection")
	}
	m.openResourceOperations()
	m.returnFromHistory()
	if m.resourceSelection.Addresses != nil {
		t.Fatalf("nil parent address selection restored as %v", m.resourceSelection.Addresses)
	}
}

func TestResourceOperationsNarrowEvidenceRetainsFullEscapedAddress(t *testing.T) {
	m := newOperationTestModel()
	for i := range m.log.UISpans {
		m.log.UISpans[i].Address = "aws_instance.long\\x1b[31m世界世界世界世界世界"
	}
	m.resourceIndex = model.BuildResourceIndex(m.log)
	m.resourceProjectionCached = false
	m.invalidateRows()
	m.openResourceOperations()
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	frame := unstyled(m.View())
	if !strings.Contains(strings.ToUpper(frame), "OBSERVED UI OPERATIONS") || !strings.Contains(frame, "action") || !strings.Contains(frame, "duration") || !strings.Contains(frame, "source") {
		t.Fatalf("narrow operation frame lost priority fields:\n%s", frame)
	}
	evidence := m.resourceEvidenceText()
	if !strings.Contains(evidence, `aws_instance.long\x1b[31m世界世界世界世界世界`) || strings.Contains(evidence, "\x1b[31m") {
		t.Fatalf("operation evidence did not retain escaped exact address:\n%s", evidence)
	}
}

func newOperationTestModel() Model {
	data := []byte("one\ntwo\nthree\nfour\n")
	entries := []logfmt.Entry{
		{Off: 0, Len: 4, Lines: 1},
		{Off: 4, Len: 4, Lines: 1},
		{Off: 8, Len: 6, Lines: 1},
		{Off: 14, Len: 5, Lines: 1},
	}
	l := &model.Log{Data: data, Entries: entries, UISpans: []span.Span{
		{Entry: 0, Address: "aws_instance.a", RPC: "Apply", ResourceType: "aws_instance", DurationMs: 2},
		{Entry: 1, Address: "aws_instance.a", RPC: "Apply", ResourceType: "aws_instance", DurationMs: 2},
		{Entry: 99, Address: "aws_instance.a", ResourceType: "aws_instance", DurationMs: 0},
		{Entry: 3, Address: "aws_instance.a", RPC: "Plan", ResourceType: "aws_instance", DurationMs: math.MaxUint32, DurationSaturated: true},
	}}
	m := New(l, "synthetic.log")
	m.setView(ViewResources)
	return m
}

func operationIdentities(rows []row) []int {
	indices := make([]int, len(rows))
	for i, r := range rows {
		indices[i] = r.identity.index
	}
	return indices
}
