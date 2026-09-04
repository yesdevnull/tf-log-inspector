package tui

import (
	"fmt"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// Facet dimension names, matching model.FacetsForSpans exactly. Named
// constants avoid the three call sites below drifting out of sync with a
// typo in a literal string.
const (
	dimProvider = "provider"
	dimRPC      = "rpc"
	dimType     = "resource type"
)

// filter derives the model.Filter this model's facet exclusions represent.
// A dimension with nothing excluded contributes a nil map, so it keeps
// Filter's own "no opinion" meaning; a dimension with every known value
// excluded contributes a non-nil, empty map, which Filter treats as
// matching nothing. The two must stay distinguishable, or clearing the last
// excluded value in a dimension would look identical to never having
// filtered it at all.
func (m Model) filter() model.Filter {
	return model.Filter{
		Providers: m.includedValues(dimProvider),
		RPCs:      m.includedValues(dimRPC),
		Types:     m.includedValues(dimType),
	}
}

// includedValues turns this model's excluded-value state for one dimension
// into the allow-list model.Filter expects: every known value in the
// dimension that has not been excluded. It returns nil, not an empty map,
// when nothing in the dimension is excluded, since a nil map is what tells
// Filter the dimension is unconstrained.
func (m Model) includedValues(dim string) map[string]bool {
	excluded := m.excluded[dim]
	if len(excluded) == 0 {
		return nil
	}
	included := make(map[string]bool, len(excluded))
	for _, f := range m.facets {
		if f.Name != dim {
			continue
		}
		for _, v := range f.Values {
			if !excluded[v.Value] {
				included[v.Value] = true
			}
		}
		break
	}
	return included
}

// cursorFacetValue resolves the facet pane's cursor to the dimension name
// and value it currently points at. ok is false only for a log with no
// facets at all -- nothing for space to act on.
func (m Model) cursorFacetValue() (dim, val string, ok bool) {
	if m.facetCursor.dim < 0 || m.facetCursor.dim >= len(m.facets) {
		return "", "", false
	}
	f := m.facets[m.facetCursor.dim]
	if m.facetCursor.val < 0 || m.facetCursor.val >= len(f.Values) {
		return "", "", false
	}
	return f.Name, f.Values[m.facetCursor.val].Value, true
}

// toggleSelectedFacetValue flips whether the facet pane's cursor value is
// excluded from the filter, and invalidates any cached rows so the next
// render reflects the change.
func (m *Model) toggleSelectedFacetValue() {
	dim, val, ok := m.cursorFacetValue()
	if !ok {
		return
	}
	if m.excluded == nil {
		m.excluded = map[string]map[string]bool{}
	}
	inner := m.excluded[dim]
	if inner == nil {
		inner = map[string]bool{}
		m.excluded[dim] = inner
	}
	if inner[val] {
		delete(inner, val)
		if len(inner) == 0 {
			delete(m.excluded, dim)
		}
	} else {
		inner[val] = true
	}
	m.invalidateRows()
}

// clearFilters removes every facet exclusion, restoring every view to the
// unfiltered log. Esc is bound to this per the spec's key table.
func (m *Model) clearFilters() {
	if len(m.excluded) == 0 {
		return
	}
	m.excluded = nil
	m.invalidateRows()
}

// renderFacets renders the facet pane: each dimension's name followed by its
// values and their span counts (counts always reflect the whole log, not
// the current filter -- see the doc comment on Model.facets), at most w
// runes wide and h lines tall. The cursor marks the value space would
// toggle; an excluded value is marked separately so its absence from the
// current filter is visible without leaving the pane.
func (m Model) renderFacets(w, h int) string {
	var lines []string
	for dimIdx, f := range m.facets {
		lines = append(lines, clipWidth(f.Name, w))
		for valIdx, v := range f.Values {
			cursor := "  "
			if dimIdx == m.facetCursor.dim && valIdx == m.facetCursor.val {
				cursor = "> "
			}
			line := fmt.Sprintf("%s%s  %d", cursor, v.Value, v.Count)
			if m.excluded[f.Name][v.Value] {
				line += "  (hidden)"
			}
			lines = append(lines, clipWidth(line, w))
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}
