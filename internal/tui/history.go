package tui

import (
	"maps"
	"slices"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

type selectionIdentity struct {
	kind  string
	value string
	index int
}

type navigationFrame struct {
	view               View
	pane               Pane
	selected           int
	identity           selectionIdentity
	sortCol            [viewCount]int
	viewSelected       [viewCount]int
	excludedFacets     map[string]map[string]bool
	resourceSelection  model.ResourceSelection
	facetCursor        facetCursor
	facetDimension     string
	facetQuery         string
	showFacetOverlay   bool
	raw                rawLogState
	timeline           timelineState
	resourceOperations bool
	operationSort      int
	associatedCalls    bool
	associatedCallSort int
}

func cloneExclusions(src map[string]map[string]bool) map[string]map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]map[string]bool, len(src))
	for key, values := range src {
		dst[key] = maps.Clone(values)
	}
	return dst
}

func cloneRawState(src rawLogState) rawLogState {
	dst := src
	dst.scope = slices.Clone(src.scope)
	if src.match != nil {
		match := *src.match
		dst.match = &match
	}
	dst.input = newSearchInput()
	dst.input.SetValue(src.query)
	dst.input.SetCursor(src.input.Position())
	return dst
}

func (m *Model) captureNavigation() navigationFrame {
	return navigationFrame{
		view: m.view, pane: m.pane, selected: m.selected,
		identity: m.selectedIdentity(), sortCol: m.sortCol,
		viewSelected:   m.viewSelected,
		excludedFacets: cloneExclusions(m.excludedFacets),
		resourceSelection: model.ResourceSelection{
			Addresses: maps.Clone(m.resourceSelection.Addresses),
			Modules:   maps.Clone(m.resourceSelection.Modules),
		},
		facetCursor: m.facetCursor, facetDimension: m.facetSearch.dimension,
		facetQuery: m.facetSearch.query, showFacetOverlay: m.showFacetOverlay,
		raw: cloneRawState(m.raw), timeline: m.timeline,
		resourceOperations: m.resourceOperations, operationSort: m.operationSort,
		associatedCalls: m.associatedCalls, associatedCallSort: m.associatedCallSort,
	}
}

func (m *Model) restoreNavigation(frame navigationFrame) {
	m.sortCol = frame.sortCol
	m.viewSelected = frame.viewSelected
	m.excludedFacets = cloneExclusions(frame.excludedFacets)
	m.resourceSelection = model.ResourceSelection{
		Addresses: maps.Clone(frame.resourceSelection.Addresses),
		Modules:   maps.Clone(frame.resourceSelection.Modules),
	}
	m.facetCursor = frame.facetCursor
	m.facetSearch.dimension = frame.facetDimension
	m.facetSearch.query = frame.facetQuery
	m.facetSearch.previous = frame.facetQuery
	m.facetSearch.editing = false
	m.facetSearch.input = newSearchInput()
	m.facetSearch.input.SetValue(frame.facetQuery)
	m.facetSearch.input.CursorEnd()
	m.showFacetOverlay = frame.showFacetOverlay
	m.timeline = frame.timeline
	m.resourceOperations = frame.resourceOperations
	m.operationSort = frame.operationSort
	m.associatedCalls = frame.associatedCalls
	m.associatedCallSort = frame.associatedCallSort
	m.selected = frame.selected
	m.changeView(frame.view)
	m.restoreIdentity(frame.identity, frame.selected)
	m.raw = cloneRawState(frame.raw)
	m.reconcileRawCursor()
	m.keepFocusOnADrawnPane()
}

func (m *Model) returnFromHistory() bool {
	if len(m.history) == 0 {
		return false
	}
	last := len(m.history) - 1
	frame := m.history[last]
	m.history[last] = navigationFrame{}
	m.history = m.history[:last]
	m.restoreNavigation(frame)
	return true
}

func (m *Model) selectedIdentity() selectionIdentity {
	if m.view == ViewTimeline {
		return m.selectedTimelineIdentity()
	}
	r, ok := m.selectedRow()
	if !ok {
		return selectionIdentity{}
	}
	return r.identity
}

func (m *Model) selectedTimelineIdentity() selectionIdentity {
	positioned, ok := m.selectedTimelineSpan()
	if !ok {
		return selectionIdentity{}
	}
	tier, _ := m.timelineSpans()
	indices := m.selectedResources().RPCIndices
	kind := "rpc"
	spans := m.log.RPCSpans
	if tier == tierUI {
		indices, kind, spans = m.selectedResources().UIIndices, "ui", m.log.UISpans
	}
	seen := 0
	for _, original := range indices {
		if spans[original].HasPosition() {
			if seen == positioned {
				return selectionIdentity{kind: kind, index: original}
			}
			seen++
		}
	}
	return selectionIdentity{}
}

func (m *Model) restoreIdentity(id selectionIdentity, fallback int) {
	if m.view == ViewTimeline && (id.kind == "rpc" || id.kind == "ui") {
		if m.restoreTimelineIdentity(id) {
			return
		}
		m.clampTimelineSelection()
		return
	}
	rows := m.rows()
	for i, r := range rows {
		if r.identity == id && id.kind != "" {
			m.selected = i
			return
		}
	}
	if len(rows) == 0 {
		m.selected = 0
		return
	}
	m.selected = min(max(0, fallback), len(rows)-1)
}

func (m *Model) restoreTimelineIdentity(id selectionIdentity) bool {
	tier, _ := m.timelineSpans()
	if (id.kind == "rpc" && tier != tierRPC) || (id.kind == "ui" && tier != tierUI) {
		return false
	}
	indices := m.selectedResources().RPCIndices
	spans := m.log.RPCSpans
	if tier == tierUI {
		indices, spans = m.selectedResources().UIIndices, m.log.UISpans
	}
	positioned := -1
	seen := 0
	for _, original := range indices {
		if !spans[original].HasPosition() {
			continue
		}
		if original == id.index {
			positioned = seen
			break
		}
		seen++
	}
	if positioned < 0 {
		return false
	}
	for laneIndex, lane := range m.timelineLanes() {
		for spanIndex, candidate := range lane.Spans {
			if candidate == positioned {
				m.timeline = timelineState{lane: laneIndex, span: spanIndex}
				return true
			}
		}
	}
	return false
}
