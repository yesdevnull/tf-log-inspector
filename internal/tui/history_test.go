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

func TestBlockedJumpGuidanceMatchesEsc(t *testing.T) {
	for _, tc := range []struct {
		name          string
		nested, query bool
		want          string
	}{
		{"root", false, false, "Esc clears it"},
		{"chooser query", false, true, "Esc clears query"},
		{"nested", true, false, "Esc goes back"},
		{"nested query", true, true, "Esc goes back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testLog(t, "provider-rpc.log"), "provider-rpc.log")
			m.setFacetExclusions(dimLevel, map[string]bool{"TRACE": true, "DEBUG": true, "UNKNOWN": true})
			m.invalidateRows()
			if tc.query {
				m.facetSearch.query = "aws"
			}
			if tc.nested {
				pressRune(t, &m, '1')
				pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			if !m.blockedJump {
				t.Fatal("source jump was not blocked")
			}
			if got := unstyled(m.footer(100)); !strings.Contains(got, tc.want) {
				t.Errorf("footer = %q, want %q", got, tc.want)
			}
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
			if tc.nested {
				if m.view != ViewProviders || !m.filterActive() {
					t.Fatal("Esc did not restore filtered parent")
				}
			} else if tc.query {
				if m.facetSearch.query != "" || !m.filterActive() {
					t.Fatal("Esc did not clear only query")
				}
			} else if m.filterActive() {
				t.Fatal("Esc did not clear filters")
			}
		})
	}
}

func TestHistoryPreservesRememberedCallsCursor(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "provider-rpc.log")
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selected != 1 {
		t.Fatal("fixture did not select the second call")
	}
	pressRune(t, &m, '1')
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	pressRune(t, &m, '4')
	if m.view != ViewCalls || m.selected != 1 {
		t.Fatalf("remembered Calls position = view %v row %d, want Calls row 1", m.view, m.selected)
	}
}

func TestHistoryRestoresNonzeroQualityOffset(t *testing.T) {
	m := qualityModel(t, "capture.log")
	m.openQuality()
	m.renderQuality(30, 3)
	m.quality.viewport.SetYOffset(7)
	m.quality.selected = qualityItemID{}
	frame := m.captureNavigation()
	m.quality.viewport.SetYOffset(0)
	m.restoreNavigation(frame)
	if !m.quality.open || m.quality.viewport.YOffset != 7 {
		t.Fatalf("restored quality state = %+v", m.captureQualityNavigation())
	}
}

func TestQualityJumpRestoresTimelineInvestigationState(t *testing.T) {
	m := New(testLog(t, "timing-tiers.log"), "timing-tiers.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	m.setView(ViewTimeline)
	m.timeline.tier = tierUI
	m.invalidateRows()
	m.clampTimelineSelection()
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyRight})
	if id := m.selectedIdentity(); id.kind != "ui" {
		t.Fatalf("fixture selection = %+v, want UI", id)
	}
	m.setFacetExclusions(dimLevel, map[string]bool{"ERROR": true})
	m.invalidateRows()
	m.raw.lastQuery = "apply"
	m.searchFrom(0, true, true)
	m.openQuality()
	m.renderQuality(40, 4)
	m.quality.selected = qualityItemID{kind: "anomaly", stage: "test", code: "located"}
	m.quality.viewport.SetYOffset(6)
	want := m.captureNavigation()
	m.activateQualityAction(qualityActionRow{id: m.quality.selected, sourceLine: 1})
	if m.view != ViewRawLog || len(m.history) != 1 {
		t.Fatal("quality action did not open raw child")
	}
	m.setFacetExclusions(dimLevel, nil)
	m.raw.query, m.raw.lastQuery, m.raw.match = "child", "changed", nil
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	got := m.captureNavigation()
	if got.view != want.view || got.pane != want.pane || got.identity != want.identity || got.timeline != want.timeline || !reflect.DeepEqual(got.excludedFacets, want.excludedFacets) {
		t.Fatalf("timeline parent mismatch:\ngot  %+v\nwant %+v", got, want)
	}
	if got.raw.query != want.raw.query || got.raw.lastQuery != want.raw.lastQuery || !reflect.DeepEqual(got.raw.match, want.raw.match) {
		t.Fatal("raw query/match was not restored")
	}
	if !got.quality.open || got.quality.selected != want.quality.selected || got.quality.offset == 0 {
		t.Fatalf("quality state = %+v, want selected %+v and nonzero offset", got.quality, want.quality.selected)
	}
}

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

func TestHistorySnapshotRestoresNonNilEmptyResourceSelections(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.resourceSelection.Addresses = map[string]bool{}
	m.resourceSelection.Modules = map[string]bool{}
	frame := m.captureNavigation()
	m.resourceSelection.Addresses["aws_instance.a"] = true
	m.resourceSelection.Modules[""] = true
	m.restoreNavigation(frame)
	if m.resourceSelection.Addresses == nil || len(m.resourceSelection.Addresses) != 0 || m.resourceSelection.Modules == nil || len(m.resourceSelection.Modules) != 0 {
		t.Fatalf("non-nil empty parent selections were not restored: %+v", m.resourceSelection)
	}
}

func TestHistoryEnterIsInertWithEmptyResourceOrModuleSelection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection model.ResourceSelection
	}{
		{"resource", model.ResourceSelection{Addresses: map[string]bool{}}},
		{"module", model.ResourceSelection{Modules: map[string]bool{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
			m.resourceSelection = tc.selection
			m.invalidateRows()
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.view != ViewCalls || len(m.history) != 0 {
				t.Fatalf("Enter with empty selection changed view/history: view %v depth %d", m.view, len(m.history))
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

	m.setView(ViewRawLog)
	m.raw.scope = nil
	m.raw.top, m.raw.topLine, m.raw.column = 2, 0, 0
	m.raw.lastQuery, m.raw.notFound, m.raw.match = "apply_start", false, nil
	if !m.searchFrom(2, true, true) || reversedText(m.renderRawLog(200, 1)) != "apply_start" {
		t.Fatal("valid parent occurrence was not highlighted")
	}
	m.history = append(m.history, m.captureNavigation())
	m.raw.match = nil
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := reversedText(m.renderRawLog(200, 1)); got != "apply_start" {
		t.Fatalf("Esc restored raw match state without its styling: %q", got)
	}
}

func TestHistoryModalEscPrecedesReturn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		open   func(*Model)
		opened func(Model) bool
		shut   func(Model) bool
	}{
		{"help", func(m *Model) { pressRune(t, m, '?') }, func(m Model) bool { return m.showHelp }, func(m Model) bool { return !m.showHelp }},
		{"quality", func(m *Model) { pressRune(t, m, 'i') }, func(m Model) bool { return m.quality.open }, func(m Model) bool { return !m.quality.open }},
		{"resource evidence", func(m *Model) { m.showResourceEvidence = true }, func(m Model) bool { return m.showResourceEvidence }, func(m Model) bool { return !m.showResourceEvidence }},
		{"response", func(m *Model) { pressRune(t, m, 'r') }, func(m Model) bool { return m.response.open }, func(m Model) bool { return !m.response.open }},
		{"raw search", func(m *Model) { pressRune(t, m, '/') }, func(m Model) bool { return m.raw.searching }, func(m Model) bool { return !m.raw.searching }},
		{"source line", func(m *Model) { pressRune(t, m, 'g') }, func(m Model) bool { return m.sourceLine.editing }, func(m Model) bool { return !m.sourceLine.editing }},
		{"facet search", func(m *Model) {
			m.pane = PaneFacets
			setFacetCursor(t, m, dimResource, "aws_instance.a")
			pressRune(t, m, '/')
		}, func(m Model) bool { return m.facetSearch.editing }, func(m Model) bool { return !m.facetSearch.editing }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
			pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			tc.open(&m)
			if !tc.opened(m) {
				t.Fatal("modal did not open")
			}
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

func TestHistorySourceLineJumpReturnsToExactFilteredScope(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.width, m.height = 100, 30
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || m.raw.scope == nil {
		t.Fatal("Enter did not establish a call-scoped raw log")
	}
	line := uint64(0)
	for candidate := uint64(1); candidate <= m.log.PhysicalLineCount(); candidate++ {
		position, ok := m.log.SourcePosition(candidate)
		if ok && !slices.Contains(m.raw.scope, int(position.Entry)) {
			line = candidate
			break
		}
	}
	if line == 0 {
		t.Fatal("fixture has no source line outside the call scope")
	}
	m.setFacetExclusions(dimProvider, map[string]bool{"registry.terraform.io/hashicorp/aws": true})
	m.setFacetExclusions(dimLevel, map[string]bool{"DEBUG": true})
	m.setFacetExclusions(dimRPC, map[string]bool{"Other": true})
	m.setFacetExclusions(dimType, map[string]bool{"aws_subnet": true})
	m.resourceSelection = model.ResourceSelection{Addresses: map[string]bool{"aws_instance.a": true}, Modules: map[string]bool{"": true}}
	m.invalidateRows()
	m.facetSearch.query = "aws"
	m.raw.top, m.raw.topLine, m.raw.column = m.raw.scope[0], 0, 4
	m.raw.query, m.raw.lastQuery, m.raw.notFound = "typed", "kept", true
	m.raw.match = &rawMatch{entry: m.raw.top, line: 0, text: literalPosition{byteOffset: 1, column: 1}}
	m.timeline = timelineState{lane: 1, span: 2}
	parent := m.captureNavigation()
	filtered := m.selectedResources()
	depth := len(m.history)
	m = submitSourceLine(t, m, fmt.Sprint(line))
	if m.raw.scope != nil || len(m.excludedFacets[dimProvider]) != 0 || len(m.excludedFacets[dimLevel]) != 0 {
		t.Fatalf("jump did not widen raw visibility: scope=%v exclusions=%v", m.raw.scope, m.excludedFacets)
	}
	if !maps.Equal(m.excludedFacets[dimRPC], parent.excludedFacets[dimRPC]) || !maps.Equal(m.excludedFacets[dimType], parent.excludedFacets[dimType]) || !reflect.DeepEqual(m.resourceSelection, parent.resourceSelection) {
		t.Fatal("jump discarded timing/resource filters")
	}
	widened := m.selectedResources()
	if len(widened.RPCIndices)+len(widened.UIIndices) <= len(filtered.RPCIndices)+len(filtered.UIIndices) {
		t.Fatalf("clearing the provider filter did not widen timing projection: before %+v after %+v", filtered, widened)
	}
	position, ok := m.log.SourcePosition(line)
	rows := m.rawLogRows(1)
	if !ok || len(rows) != 1 || rows[0].entry != int(position.Entry) || rows[0].entryLine != int(position.EntryLine) || rows[0].sourceLine != line {
		t.Fatalf("first rendered row = %+v, want requested position %+v on line %d", rows, position, line)
	}
	if m.raw.query != "typed" || m.raw.lastQuery != "kept" || m.raw.match != nil || m.raw.notFound || m.raw.column != 0 || len(m.history) != depth+1 {
		t.Fatalf("jump child state wrong: history=%d raw=%+v", len(m.history), m.raw)
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if !reflect.DeepEqual(m.captureNavigation(), parent) {
		t.Fatal("Esc did not restore the complete parent investigation")
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

func TestHistoryRestoresParentFocusAndClampsItAfterResize(t *testing.T) {
	t.Run("child focus does not replace parent focus", func(t *testing.T) {
		m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
		pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
		pressRune(t, &m, 'f')
		pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.view != ViewCalls || m.pane != PaneList {
			t.Fatalf("returned view %v pane %v, want Calls list", m.view, m.pane)
		}
	})

	t.Run("restored focus is clamped to the resized layout", func(t *testing.T) {
		m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
		pressRune(t, &m, 'f')
		if m.pane != PaneFacets {
			t.Fatal("parent did not focus facets")
		}
		parent := m.captureNavigation()
		m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
		m.restoreNavigation(parent)
		if m.view != ViewCalls || m.pane != PaneList {
			t.Fatalf("resized return = view %v pane %v, want Calls list", m.view, m.pane)
		}
	})
}
