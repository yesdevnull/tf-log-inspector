package tui

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestHistoryRestoresParentAfterRawChildFilterEdit(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	before := m.filter()
	selected := m.rows()[m.selected].spanIdx
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatal("fixture did not open raw log")
	}
	setFacetCursor(t, &m, dimType, "aws_instance")
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewCalls || !reflect.DeepEqual(m.filter(), before) {
		t.Fatalf("parent view/filter not restored: %v %+v", m.view, m.filter())
	}
	if m.rows()[m.selected].spanIdx != selected {
		t.Fatal("returned to another original RPC observation")
	}
}

func TestHistoryRestoresNilAndEmptyResourceSelections(t *testing.T) {
	for _, tc := range []struct {
		name         string
		dim          string
		value        string
		wantChildLen int
		get          func(Model) map[string]bool
	}{
		{"resource", dimResource, "aws_instance.a", 2, func(m Model) map[string]bool { return m.resourceSelection.Addresses }},
		{"module empty", dimModule, "", 0, func(m Model) map[string]bool { return m.resourceSelection.Modules }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
			if tc.get(m) != nil {
				t.Fatal("fresh parent selection is not nil")
			}
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			setFacetCursor(t, &m, tc.dim, tc.value)
			m.pane = PaneFacets
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
			if child := tc.get(m); child == nil || len(child) != tc.wantChildLen {
				t.Fatalf("child key edit selection = %+v, want non-nil length %d", child, tc.wantChildLen)
			}
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
			if tc.get(m) != nil {
				t.Fatalf("nil parent selection restored as non-nil: %+v", tc.get(m))
			}
		})
	}
}

func TestHistoryRestoresParentSortAndOriginalCall(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "provider-rpc.log")
	pressRune(t, &m, 's')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyDown})
	wantSort := m.sortCol[ViewCalls]
	wantCall := m.rows()[m.selected].spanIdx
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &m, dimProvider, m.log.RPCSpans[wantCall].Provider)
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.sortCol[ViewCalls] != wantSort || m.rows()[m.selected].spanIdx != wantCall {
		t.Fatalf("restored sort/selection = %d/%d, want %d/%d", m.sortCol[ViewCalls], m.rows()[m.selected].spanIdx, wantSort, wantCall)
	}
}

func TestHistoryRestoresTimelineObservationAfterChildFiltering(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "timeline.log")
	pressRune(t, &m, '5')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyRight})
	want := m.selectedIdentity()
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	setFacetCursor(t, &m, dimType, "aws_instance")
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := m.selectedIdentity(); got != want {
		t.Fatalf("timeline identity = %+v, want %+v", got, want)
	}
}

func TestHistoryRestoresRawSearchAndRequestState(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.raw.top, m.raw.topLine, m.raw.column = 2, 0, 7
	m.raw.query, m.raw.lastQuery, m.raw.notFound = "child", "parent", true
	m.raw.scope = []int{2}
	m.raw.match = &rawMatch{entry: 2, line: 0, text: literalPosition{byteOffset: 1, column: 2}}
	want := cloneRawState(m.raw)
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	pressRune(t, &m, '\\')
	if len(m.history) != 1 || m.raw.scope != nil {
		t.Fatalf("request expansion changed history: depth %d scope %v", len(m.history), m.raw.scope)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.raw.top != want.top || m.raw.topLine != want.topLine || m.raw.column != want.column || m.raw.query != want.query || m.raw.lastQuery != want.lastQuery || m.raw.notFound != want.notFound || !slices.Equal(m.raw.scope, want.scope) || m.raw.match == nil || *m.raw.match != *want.match {
		t.Fatalf("raw parent state = %+v, want %+v", m.raw, want)
	}
}

func TestHistoryModalEscPrecedesReturn(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*Model)
		shut func(Model) bool
	}{
		{"help", func(m *Model) { pressRune(t, m, '?') }, func(m Model) bool { return !m.showHelp }},
		{"quality", func(m *Model) { pressRune(t, m, 'i') }, func(m Model) bool { return !m.quality.open }},
		{"resource evidence", func(m *Model) { m.showResourceEvidence = true }, func(m Model) bool { return !m.showResourceEvidence }},
		{"response", func(m *Model) { pressRune(t, m, 'r') }, func(m Model) bool { return !m.response.open }},
		{"raw search", func(m *Model) { pressRune(t, m, '/') }, func(m Model) bool { return !m.raw.searching }},
		{"facet search", func(m *Model) {
			m.pane = PaneFacets
			setFacetCursor(t, m, dimResource, "aws_instance.a")
			pressRune(t, m, '/')
		}, func(m Model) bool { return !m.facetSearch.editing }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			tc.open(&m)
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.view != ViewRawLog || len(m.history) != 1 || !tc.shut(m) {
				t.Fatalf("modal dismissal spent navigation history: view %v depth %d", m.view, len(m.history))
			}
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.view != ViewCalls || len(m.history) != 0 {
				t.Fatalf("second Esc did not return: view %v depth %d", m.view, len(m.history))
			}
		})
	}
}

func TestHistoryManualSameViewKeyClearsReturn(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	pressRune(t, &m, '6')
	if m.view != ViewRawLog || len(m.history) != 0 {
		t.Fatalf("same-view key retained history: view %v depth %d", m.view, len(m.history))
	}
}

func TestHistoryRefusedJumpLeavesHistoryUnchanged(t *testing.T) {
	m := New(testLog(t, "interleaved-calls.log"), "interleaved-calls.log")
	m.setFacetExclusions(dimLevel, map[string]bool{"TRACE": true, "DEBUG": true, "UNKNOWN": true})
	m.invalidateRows()
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.blockedJump || len(m.history) != 0 {
		t.Fatalf("refused jump state = blocked %v depth %d", m.blockedJump, len(m.history))
	}
}

func TestNavigationFramesDoNotShareMutableMaps(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.excludedFacets = map[string]map[string]bool{dimType: {"aws_subnet": true}}
	m.resourceSelection = model.ResourceSelection{Addresses: map[string]bool{"aws_instance.a": true}}
	frame := m.captureNavigation()
	m.excludedFacets[dimType]["aws_instance"] = true
	m.resourceSelection.Addresses["aws_instance.b"] = true
	if maps.Equal(frame.excludedFacets[dimType], m.excludedFacets[dimType]) || maps.Equal(frame.resourceSelection.Addresses, m.resourceSelection.Addresses) {
		t.Fatal("navigation frame shares mutable selection maps")
	}
}
