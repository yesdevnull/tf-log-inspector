package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCallsDetailUsesExactAttributionAddressWhenSplitNameMissing(t *testing.T) {
	l := testLog(t, "resources-accounting.log")
	if len(l.Attribs) < 1 || l.Attribs[0].Address != "aws_instance.a" || l.Attribs[0].Name != "" {
		t.Fatalf("fixture attribution = %+v, want exact address with unavailable split name", l.Attribs)
	}
	m := New(l, "resources-accounting.log")
	for i, r := range m.rows() {
		if l.AttributionForEntry(l.RPCSpans[r.spanIdx].Entry).Address == "aws_instance.a" {
			m.selected = i
			break
		}
	}
	_, sections := m.selectedDetail(40)
	if got := unstyled(strings.Join(sections[0], "\n")); !strings.Contains(got, "aws_instance.a") {
		t.Fatalf("Calls detail denied the exact attributed address:\n%s", got)
	}
}

func TestResourcesInvestigationWorkflowPreservesEvidenceQualityAndRawState(t *testing.T) {
	l := testLog(t, "resources-accounting.log")
	quality := l.CaptureQuality()
	m := New(l, "resources-accounting.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pressRune(t, &m, '3')
	pressRune(t, &m, 'f')
	setFacetCursor(t, &m, dimResource, "aws_instance.a")
	pressRune(t, &m, '/')
	for _, r := range "aws_instance.a" {
		pressRune(t, &m, r)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	pressRune(t, &m, 'o')

	p := m.selectedResources()
	selectedMs := p.Selection.Selected.Contained.TotalMs + p.Selection.Selected.Likely.TotalMs + p.Selection.Selected.Overlapping.TotalMs
	otherMs := p.Selection.Other.Contained.TotalMs + p.Selection.Other.Likely.TotalMs + p.Selection.Other.Overlapping.TotalMs
	if p.Selection.Baseline.TotalMs != 35 || selectedMs != 10 || otherMs != 20 || p.Selection.Unresolved.TotalMs != 5 || p.UI.Count != 1 {
		t.Fatalf("selected A evidence drifted: %+v", p)
	}
	pressRune(t, &m, 'e')
	if got := unstyled(m.View()); !strings.Contains(got, "selected: 1 call, 10ms") {
		t.Fatalf("evidence modal did not show selected A:\n%s", got)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	pressRune(t, &m, 'i')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if !reflect.DeepEqual(quality, l.CaptureQuality()) {
		t.Fatal("resource workflow changed whole-log capture quality")
	}

	pressRune(t, &m, '4')
	if len(m.rows()) != 1 {
		t.Fatalf("selected Calls rows = %d, want one exact association", len(m.rows()))
	}
	pressRune(t, &m, 'f')
	if m.pane != PaneList {
		t.Fatalf("f left workflow focus on pane %v, want list", m.pane)
	}
	callRow := m.selected
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || !slices.Equal(m.raw.scope, []int{3}) || m.raw.top != 3 || m.raw.topLine != 0 || m.raw.column != 0 {
		t.Fatalf("raw jump state = view %v scope %v top %d:%d col %d", m.view, m.raw.scope, m.raw.top, m.raw.topLine, m.raw.column)
	}
	m.raw.lastQuery = "ReadResource"
	if !m.searchFrom(m.raw.top, true, true) || m.raw.match == nil {
		t.Fatal("request scope did not retain searchable RPC text")
	}
	if m.raw.top != 3 || m.raw.topLine != 0 || m.raw.column != 180 {
		t.Fatalf("raw query anchor = entry %d line %d column %d, want entry 3 line 0 column 180", m.raw.top, m.raw.topLine, m.raw.column)
	}
	beforeTop, beforeLine, beforeColumn := m.raw.top, m.raw.topLine, m.raw.column
	beforeQuery, beforeMatch, beforeScope := m.raw.lastQuery, *m.raw.match, slices.Clone(m.raw.scope)
	pressRune(t, &m, 'i')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewRawLog || m.raw.top != beforeTop || m.raw.topLine != beforeLine || m.raw.column != beforeColumn || m.raw.lastQuery != beforeQuery || m.raw.match == nil || *m.raw.match != beforeMatch || !slices.Equal(m.raw.scope, beforeScope) {
		t.Fatalf("quality round trip changed active request state: %+v", m.raw)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewCalls || m.selected != callRow {
		t.Fatalf("raw return lost Calls row: view %v row %d", m.view, m.selected)
	}
	if m.raw.scope != nil {
		t.Fatalf("raw return retained request scope %v", m.raw.scope)
	}
	m.raw.lastQuery = "ReadResource"
	if !m.searchFrom(m.raw.top, true, true) || m.raw.match == nil {
		t.Fatal("returned raw state did not retain a searchable anchor")
	}
	beforeTop, beforeLine, beforeColumn = m.raw.top, m.raw.topLine, m.raw.column
	beforeQuery, beforeMatch = m.raw.lastQuery, *m.raw.match
	pressRune(t, &m, 'e')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.raw.top != beforeTop || m.raw.topLine != beforeLine || m.raw.column != beforeColumn || m.raw.lastQuery != beforeQuery || m.raw.match == nil || *m.raw.match != beforeMatch || m.raw.scope != nil {
		t.Fatalf("evidence round trip changed raw state: %+v", m.raw)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || !slices.Equal(m.raw.scope, []int{3}) || m.raw.top != 3 || m.raw.topLine != 0 || m.raw.column != 0 {
		t.Fatalf("post-evidence raw jump state = view %v scope %v top %d:%d col %d", m.view, m.raw.scope, m.raw.top, m.raw.topLine, m.raw.column)
	}
}

func TestRepeatedResourceOperationsKeepOriginalIdentitiesThroughSelection(t *testing.T) {
	m := New(testLog(t, "resources-modules.log"), "resources-modules.log")
	m.setView(ViewResources)
	setFacetCursor(t, &m, dimResource, `module.app["a.b"].module.db[0].aws_instance.web["key[part"]`)
	m.pane = PaneFacets
	pressRune(t, &m, 'o')
	rows := m.rows()
	if len(rows) != 1 || len(rows[0].resource.Operations) != 2 || rows[0].resource.Operations[0].UIIndex != 0 || rows[0].resource.Operations[1].UIIndex != 1 {
		t.Fatalf("repeated operation identities = %+v", rows)
	}
	pressRune(t, &m, 'f')
	if m.pane != PaneList {
		t.Fatalf("f left aggregate focus on pane %v, want list", m.pane)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewResources || !m.resourceOperations || len(m.history) != 1 || len(m.rows()) != 2 {
		t.Fatal("aggregate Enter did not open the repeated operations")
	}
}

func pressRune(t *testing.T, m *Model, r rune) {
	t.Helper()
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func pressKey(t *testing.T, m *Model, key tea.KeyMsg) {
	t.Helper()
	next, _ := m.Update(key)
	if next != m {
		t.Fatalf("Update replaced model %p with %T", m, next)
	}
}
