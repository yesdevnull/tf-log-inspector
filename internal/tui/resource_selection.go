package tui

import (
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// selectedResources returns the one D1 projection shared by every timing
// consumer until a filter or named selection changes.
func (m *Model) selectedResources() model.ResourceProjection {
	if !m.resourceProjectionCached {
		m.resourceProjection = model.SelectResources(m.log, m.resourceIndex, m.filter(), m.resourceSelection)
		m.resourceProjectionCached = true
	}
	return m.resourceProjection
}

// selectedRPCSpans materialises the projected RPC tier in source order.
func (m *Model) selectedRPCSpans() []span.Span {
	projection := m.selectedResources()
	result := make([]span.Span, 0, len(projection.RPCIndices))
	for _, i := range projection.RPCIndices {
		result = append(result, m.log.RPCSpans[i])
	}
	return result
}

// selectedUISpans materialises the projected UI tier in source order.
func (m *Model) selectedUISpans() []span.Span {
	projection := m.selectedResources()
	result := make([]span.Span, 0, len(projection.UIIndices))
	for _, i := range projection.UIIndices {
		result = append(result, m.log.UISpans[i])
	}
	return result
}
