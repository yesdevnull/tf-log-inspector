package tui

import "github.com/yesdevnull/tf-log-inspector/internal/model"

// restrictFacet keeps value as the only admitted value in dim. It uses the
// facet's domain keys directly, including the established (none) key for
// unavailable metadata.
func (m *Model) restrictFacet(dim, value string) {
	excluded := make(map[string]bool)
	for _, candidate := range m.facetValues(dim) {
		if candidate.Value != value {
			excluded[candidate.Value] = true
		}
	}
	m.setFacetExclusions(dim, excluded)
}

func (m *Model) openAggregate() bool {
	view, dim, value, ok := m.aggregateTarget()
	if !ok {
		return false
	}
	parent := m.captureNavigation()
	m.restrictFacet(dim, value)
	m.history = append(m.history, parent)
	m.changeView(view)
	m.selected = 0
	return true
}

func (m *Model) aggregateTarget() (View, string, string, bool) {
	r, ok := m.selectedRow()
	if !ok {
		return 0, "", "", false
	}
	switch {
	case m.view == ViewProviders && r.identity.kind == "provider":
		return ViewCalls, dimProvider, r.identity.value, true
	case m.view == ViewTypes && r.identity.kind == "type":
		projection := m.selectedResources()
		for _, index := range projection.RPCIndices {
			if model.FacetKey(m.log.RPCSpans[index].ResourceType) == r.identity.value {
				return ViewCalls, dimType, r.identity.value, true
			}
		}
		for _, index := range projection.UIIndices {
			if model.FacetKey(m.log.UISpans[index].ResourceType) == r.identity.value {
				return ViewResources, dimType, r.identity.value, true
			}
		}
	}
	return 0, "", "", false
}

// enterHint describes the action Enter would perform for the current
// selection. It shares aggregateTarget and jumpTarget with the key handler,
// so the footer cannot advertise a route Enter would refuse.
func (m *Model) enterHint() string {
	view, _, _, ok := m.aggregateTarget()
	if ok {
		if view == ViewCalls {
			return "↵ calls"
		}
		return "↵ resources"
	}
	if _, _, ok := m.jumpTarget(); ok {
		return openHint
	}
	return ""
}
