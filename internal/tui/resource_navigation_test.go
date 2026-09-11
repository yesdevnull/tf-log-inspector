package tui

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestAssociatedCallsQualificationsSurviveSplitLayouts(t *testing.T) {
	for _, width := range []int{60, 70, 100} {
		for _, fixture := range []string{"resources-accounting.log", "resources-modules.log"} {
			t.Run(fmt.Sprintf("%s/%d", fixture, width), func(t *testing.T) {
				m := New(testLog(t, fixture), fixture)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 9})
				pressRune(t, &m, '3')
				pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
				pressRune(t, &m, 'c')
				got := strings.ToLower(unstyled(m.View()))
				if fixture == "resources-accounting.log" {
					if !strings.Contains(got, "partial inferred") || !(strings.Contains(got, "not one operation") || strings.Contains(got, "not per operation")) {
						t.Errorf("association qualification incomplete:\n%s", got)
					}
					if !strings.Contains(got, "10ms") || !strings.Contains(got, "conta") {
						t.Errorf("selected call or confidence hidden:\n%s", got)
					}
				} else {
					// Borders separate wrapped lines, so assert complete clauses
					// individually rather than treating borders as message text.
					if !(strings.Contains(got, "this does not establish that no provider calls occurred.") || strings.Contains(got, "provider calls may still have occurred.")) {
						t.Errorf("empty result caveat incomplete:\n%s", got)
					}
				}
			})
		}
	}
}

func TestResourceAssociatedCallsPreserveSelectionAndReturn(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	before := maps.Clone(m.resourceSelection.Addresses)
	pressRune(t, &m, 'c')
	if m.view != ViewCalls || len(m.rows()) != 1 {
		t.Fatalf("associated Calls: view %v, rows %d", m.view, len(m.rows()))
	}
	if !reflect.DeepEqual(before, m.resourceSelection.Addresses) {
		t.Fatal("associated Calls changed resource selection")
	}
	i := m.rows()[0].spanIdx
	if m.log.AttributionForEntry(m.log.RPCSpans[i].Entry).Address != "aws_instance.a" {
		t.Fatal("associated Calls included another address")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewResources || !m.resourceOperations {
		t.Fatal("associated Calls did not return to operations")
	}
}

func TestResourceAssociatedCallsPreserveExplicitEmptySelection(t *testing.T) {
	m := New(testLog(t, "resources-modules.log"), "resources-modules.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &m, dimResource, `module.app["a.b"].module.db[0].aws_instance.web["key[part"]`)
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	if m.resourceSelection.Addresses == nil || len(m.resourceSelection.Addresses) != 0 || len(m.rows()) != 0 {
		t.Fatalf("operation selection = %#v with %d rows, want non-nil empty selection", m.resourceSelection.Addresses, len(m.rows()))
	}
	m.pane = PaneList
	pressRune(t, &m, 'c')
	if m.view != ViewCalls || len(m.rows()) != 0 || m.resourceSelection.Addresses == nil {
		t.Fatalf("empty associated Calls = view %v rows %d selection %#v", m.view, len(m.rows()), m.resourceSelection.Addresses)
	}
	if got := unstyled(m.View()); !strings.Contains(got, "No named rpc associations in this selection") {
		t.Errorf("empty associated Calls title omitted the selection result:\n%s", got)
	}
	if got := strings.Join(strings.Fields(unstyled(m.renderList(60, 20))), " "); !strings.Contains(got, "This does not establish that no provider calls occurred.") {
		t.Errorf("empty associated Calls omitted the provider-call caveat:\n%s", got)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewResources || !m.resourceOperations || m.resourceSelection.Addresses == nil || len(m.resourceSelection.Addresses) != 0 {
		t.Fatalf("empty operation parent was not restored: view %v operations %v selection %#v", m.view, m.resourceOperations, m.resourceSelection.Addresses)
	}
}

func TestEmptyResourceOperationsExplainTheActiveSelectionBeforeAndAfterCalls(t *testing.T) {
	m := New(testLog(t, "resources-modules.log"), "resources-modules.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &m, dimResource, `module.app["a.b"].module.db[0].aws_instance.web["key[part"]`)
	pressRune(t, &m, 'f')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	pressRune(t, &m, 'f')
	assertEmptyOperationGuidance(t, &m)
	pressRune(t, &m, 'c')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	assertEmptyOperationGuidance(t, &m)
}

func assertEmptyOperationGuidance(t *testing.T, m *Model) {
	t.Helper()
	if m.view != ViewResources || !m.resourceOperations || len(m.rows()) != 0 {
		t.Fatalf("operation state = view %v operations %v rows %d", m.view, m.resourceOperations, len(m.rows()))
	}
	got := strings.Join(strings.Fields(unstyled(m.View())), " ")
	if !strings.Contains(got, "nothing matches the filter") || !strings.Contains(got, "Esc back") {
		t.Fatalf("empty operation guidance does not explain selection and return:\n%s", got)
	}
	if strings.Contains(got, noRowsNote) {
		t.Fatalf("empty operation guidance blames the log:\n%s", got)
	}
}

func TestResourceAssociatedCallsUseNeutralContextAfterNamedConstraintsAreRemoved(t *testing.T) {
	l, err := model.Load("testdata/resource-association-confidence.log")
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, "resource-association-confidence.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &m, dimResource, m.rows()[0].cells[0])
	m.pane = PaneFacets
	pressRune(t, &m, 'o')
	if m.resourceSelection.Addresses != nil || m.resourceSelection.Modules != nil {
		t.Fatalf("named constraints = %#v/%#v, want nil/nil", m.resourceSelection.Addresses, m.resourceSelection.Modules)
	}
	wantIndices := slices.Clone(m.selectedResources().RPCIndices)
	m.pane = PaneList
	pressRune(t, &m, 'c')
	gotIndices := rowSpanIndices(m.rows())
	slices.Sort(wantIndices)
	slices.Sort(gotIndices)
	if !slices.Equal(wantIndices, gotIndices) {
		t.Fatalf("associated Calls indices = %v, want %v", gotIndices, wantIndices)
	}
	got := unstyled(m.View())
	if !strings.Contains(got, "Calls for current selection") || strings.Contains(got, "named RPC associations") {
		t.Fatalf("unconstrained associated Calls wording is not neutral:\n%s", got)
	}
	confidences := map[string]bool{}
	for _, row := range m.rows() {
		confidences[row.cells[len(row.cells)-1]] = true
	}
	for _, want := range []string{"contained", "likely", "overlapping", "ambiguous", "unattributed"} {
		if !confidences[want] {
			t.Errorf("associated Calls confidence values = %v, want %q", confidences, want)
		}
	}
}

func TestAssociatedCallsHaveIndependentSortAndDiscoverableNarrowFooter(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	footer := unstyled(m.footer(60))
	for _, want := range []string{"c calls", "Esc back", "q quit"} {
		if !strings.Contains(footer, want) {
			t.Errorf("operation footer missing %q: %q", want, footer)
		}
	}
	ordinarySort := m.sortCol[ViewCalls]
	pressRune(t, &m, 'c')
	if m.view != ViewCalls {
		t.Fatalf("c left view at %v, want Calls", m.view)
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	centre := unstyled(m.View())
	for _, want := range []string{"confi", "conta"} {
		if !strings.Contains(centre, want) {
			t.Errorf("60x9 associated Calls omitted %q:\n%s", want, centre)
		}
	}
	for _, want := range []string{"Esc back", "q quit"} {
		if got := unstyled(m.footer(60)); !strings.Contains(got, want) {
			t.Errorf("60x9 associated Calls footer missing %q: %q", want, got)
		}
	}
	pressRune(t, &m, 's')
	if m.associatedCallSort == ordinarySort || m.sortCol[ViewCalls] != ordinarySort {
		t.Fatalf("associated/ordinary sort = %d/%d, ordinary started %d", m.associatedCallSort, m.sortCol[ViewCalls], ordinarySort)
	}
	pressRune(t, &m, '4')
	if m.associatedCalls || m.activeSort() != ordinarySort || strings.Contains(unstyled(m.View()), "Inferred RPC associations") {
		t.Fatalf("manual Calls retained associated context/sort: context %v sort %d", m.associatedCalls, m.activeSort())
	}
}

func TestAssociatedCallsKeepQualificationAtSixtyByNine(t *testing.T) {
	populated := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	populated.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	pressRune(t, &populated, '3')
	pressKey(t, &populated, tea.KeyMsg{Type: tea.KeyEnter})
	pressRune(t, &populated, 'c')
	got := strings.Join(strings.Fields(unstyled(populated.View())), " ")
	for _, want := range []string{"Partial inferred calls", "selection, not one operation", "confi", "conta"} {
		if !strings.Contains(got, want) {
			t.Errorf("populated 60x9 associated Calls missing %q:\n%s", want, got)
		}
	}

	empty := New(testLog(t, "resources-modules.log"), "resources-modules.log")
	empty.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	pressRune(t, &empty, '3')
	pressKey(t, &empty, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &empty, dimResource, `module.app["a.b"].module.db[0].aws_instance.web["key[part"]`)
	empty.pane = PaneFacets
	pressKey(t, &empty, tea.KeyMsg{Type: tea.KeySpace})
	empty.pane = PaneList
	pressRune(t, &empty, 'c')
	got = strings.Join(strings.Fields(unstyled(empty.View())), " ")
	for _, want := range []string{"No named rpc associations in this selection", "This does not establish that no provider calls occurred."} {
		if !strings.Contains(got, want) {
			t.Errorf("empty 60x9 associated Calls missing %q:\n%s", want, got)
		}
	}
}

func TestAssociatedCallRawFooterKeepsReturnAndQuitAtSixtyColumns(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	pressRune(t, &m, 'c')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || m.raw.scope == nil {
		t.Fatal("associated call did not open scoped Raw Log")
	}
	footer := unstyled(m.footer(60))
	for _, want := range []string{"Esc back", "q quit"} {
		if !strings.Contains(footer, want) {
			t.Errorf("60-column associated Raw footer missing %q: %q", want, footer)
		}
	}
}

func TestAssociatedCallsPreserveEverySelectionDimension(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	setFacetCursor(t, &m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	setFacetCursor(t, &m, dimType, "aws_subnet")
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	setFacetCursor(t, &m, dimRPC, "Other")
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	m.pane = PaneList
	pressRune(t, &m, '3')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &m, dimResource, "aws_instance.a")
	m.pane = PaneFacets
	pressRune(t, &m, 'o')
	setFacetCursor(t, &m, dimModule, "")
	pressRune(t, &m, 'o')
	if m.resourceSelection.Addresses != nil || m.resourceSelection.Modules == nil {
		t.Fatalf("named selection = %#v/%#v, want nil address and active module", m.resourceSelection.Addresses, m.resourceSelection.Modules)
	}
	beforeFilter := m.filter()
	beforeExclusions := cloneExclusions(m.excludedFacets)
	beforeAddresses := maps.Clone(m.resourceSelection.Addresses)
	beforeModules := maps.Clone(m.resourceSelection.Modules)
	m.pane = PaneList
	pressRune(t, &m, 'c')
	if !reflect.DeepEqual(m.resourceSelection.Addresses, beforeAddresses) || !reflect.DeepEqual(m.resourceSelection.Modules, beforeModules) || !reflect.DeepEqual(m.excludedFacets, beforeExclusions) || !reflect.DeepEqual(m.filter(), beforeFilter) {
		t.Fatalf("associated Calls changed selection: addresses %#v modules %#v filter %#v", m.resourceSelection.Addresses, m.resourceSelection.Modules, m.filter())
	}
	if got := unstyled(m.View()); !strings.Contains(got, "No named rpc associations") || strings.Contains(got, "Calls for current selection") {
		t.Fatalf("active module selection got neutral wording:\n%s", got)
	}
}

func TestAssociatedCallRowsNeverNameUnresolvedAttributions(t *testing.T) {
	l, err := model.Load("testdata/resource-association-confidence.log")
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, "resource-association-confidence.log")
	m.associatedCalls = true
	for _, row := range m.rows() {
		a := l.AttributionForEntry(l.RPCSpans[row.spanIdx].Entry)
		if (a.Confidence.String() == "ambiguous" || a.Confidence.String() == "unattributed") && a.Address != "" {
			t.Errorf("%s row chose address %q", a.Confidence, a.Address)
		}
	}
}

func TestAssociatedCallsLabelLogsWithoutAddressContext(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "provider-rpc.log")
	m.associatedCalls = true
	rows := m.rows()
	if len(rows) == 0 {
		t.Fatal("no-context fixture has no calls")
	}
	for _, row := range rows {
		if got := row.cells[len(row.cells)-1]; got != noAddressContextValue {
			t.Errorf("confidence cell = %q, want %q", got, noAddressContextValue)
		}
	}
}

func TestResourceOperationAssociatedCallResponseHistoryComposition(t *testing.T) {
	l, err := model.Load("testdata/resource-association-confidence.log")
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, "resource-association-confidence.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	setFacetCursor(t, &m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	m.pane = PaneList
	pressRune(t, &m, '2')
	typeParent := m.selectedIdentity()
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewResources {
		t.Fatalf("type opened view %v, want Resources", m.view)
	}
	selectAggregate(t, &m, "resource", "aws_instance.a")
	resourceParent := m.selectedIdentity()
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &m, dimProvider, "registry.terraform.io/hashicorp/aws")
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	m.pane = PaneList
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("operation source opened view %v, want Raw (operations %v, rows %d, hint %q)", m.view, m.resourceOperations, len(m.rows()), m.enterHint())
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewResources || !m.resourceOperations {
		t.Fatal("operation source did not return to operations")
	}
	pressRune(t, &m, 'c')
	foundCall := false
	for i, row := range m.rows() {
		attribution := m.log.AttributionForEntry(m.log.RPCSpans[row.spanIdx].Entry)
		if attribution.Address == "aws_instance.a" {
			m.selected = i
			foundCall = true
			break
		}
	}
	if !foundCall {
		t.Fatal("associated Calls contained no aws_instance.a call")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("associated call source opened view %v, want Raw", m.view)
	}
	responseEntry := -1
	for _, entry := range m.raw.scope {
		if strings.Contains(string(m.log.Bytes(m.log.Entries[entry])), `{"@message"`) {
			responseEntry = entry
			break
		}
	}
	if responseEntry < 0 {
		t.Fatal("associated call scope omitted its parsed response entry")
	}
	m.raw.top = responseEntry
	responseKeyAndDrain(t, &m, "r")
	if !m.response.open {
		t.Fatal("associated call response did not open")
	}
	if got := unstyled(m.View()); !strings.Contains(got, "synthetic response") {
		t.Fatalf("reconstructed response omitted parsed body:\n%s", got)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewCalls || !m.associatedCalls {
		t.Fatal("raw source did not return to associated Calls")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewResources || !m.resourceOperations {
		t.Fatal("associated Calls did not return to operations")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewResources || m.resourceOperations || m.selectedIdentity() != resourceParent {
		t.Fatal("operation list did not return to its resource aggregate")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewTypes || m.selectedIdentity() != typeParent {
		t.Fatal("resource aggregate did not return to its type parent")
	}
}

func rowSpanIndices(rows []row) []int {
	indices := make([]int, len(rows))
	for i, row := range rows {
		indices[i] = row.spanIdx
	}
	return indices
}
