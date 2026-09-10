package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

type facetSearchState struct {
	editing         bool
	dimension       string
	query, previous string
	input           textinput.Model
}

func (m *Model) visibleFacetIndices(dim string) []int {
	values := m.facetValues(dim)
	indices := make([]int, 0, len(values))
	for i, value := range values {
		if m.facetSearch.dimension != dim || strings.Contains(displayFacetValue(dim, value.Value), m.facetSearch.query) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *Model) beginFacetSearch() {
	if m.facetCursor.dim < 0 || m.facetCursor.dim >= len(m.facets) {
		return
	}
	dim := m.facets[m.facetCursor.dim].Name
	if !namedFacetDimension(dim) {
		return
	}
	if m.facetSearch.dimension != dim {
		m.facetSearch.dimension = dim
		m.facetSearch.query = ""
	}
	m.facetSearch.previous = m.facetSearch.query
	m.facetSearch.input = newSearchInput()
	m.facetSearch.input.SetValue(m.facetSearch.query)
	m.facetSearch.input.CursorEnd()
	m.facetSearch.editing = true
}

func (m Model) facetSearchAvailable() bool {
	return m.pane == PaneFacets && m.facetCursor.dim >= 0 && m.facetCursor.dim < len(m.facets) && namedFacetDimension(m.facets[m.facetCursor.dim].Name)
}

func (m *Model) clampFacetCursor() {
	if m.facetCursor.dim < 0 || m.facetCursor.dim >= len(m.facets) {
		return
	}
	indices := m.visibleFacetIndices(m.facets[m.facetCursor.dim].Name)
	for _, index := range indices {
		if index == m.facetCursor.val {
			return
		}
	}
	if len(indices) == 0 {
		m.facetCursor.val = -1
		return
	}
	m.facetCursor.val = indices[0]
}

func (m *Model) handleFacetSearchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.facetSearch.editing = false
		m.facetSearch.input.Blur()
		m.quitting = true
	case tea.KeyEnter:
		m.facetSearch.editing = false
		m.facetSearch.input.Blur()
	case tea.KeyEsc:
		m.facetSearch.editing = false
		m.facetSearch.input.Blur()
		m.facetSearch.query = m.facetSearch.previous
		m.facetSearch.input.SetValue(m.facetSearch.query)
		m.clampFacetCursor()
	default:
		if msg.Type == tea.KeySpace {
			msg.Runes = []rune{' '}
		}
		if msg.Type == tea.KeyRunes {
			msg.Runes = []rune(logfmt.DisplayText(string(msg.Runes)))
		}
		m.facetSearch.input, _ = m.facetSearch.input.Update(msg)
		m.facetSearch.query = m.facetSearch.input.Value()
		m.clampFacetCursor()
	}
}

func (m Model) facetSearchPrompt(w int) string {
	if w < 2 {
		return clipWidth("/", w)
	}
	input := newSearchInput()
	input.Width = max(1, w-2)
	input.SetValue(logfmt.DisplayText(m.facetSearch.query))
	if m.facetSearch.input.Value() == m.facetSearch.query {
		input.SetCursor(m.facetSearch.input.Position())
	} else {
		input.CursorEnd()
	}
	return clipWidth(input.View(), w)
}

func (m Model) facetHeading(dim string) string {
	heading := facetSectionHeader(dim)
	if m.facetSearch.dimension == dim && m.facetSearch.query != "" {
		heading += " /" + logfmt.DisplayText(m.facetSearch.query)
	}
	return heading
}

func namedFacetDimension(dim string) bool {
	return dim == dimResource || dim == dimModule
}

func displayFacetValue(dim, value string) string {
	if dim == dimModule && value == "" {
		return "(root subtree)"
	}
	return logfmt.DisplayText(value)
}
