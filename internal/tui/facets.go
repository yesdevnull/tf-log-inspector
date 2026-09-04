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

// filter derives the model.Filter this model's facet selections represent.
// A dimension with nothing selected contributes a nil map, so it keeps
// Filter's own "no opinion" meaning -- an untouched facet pane must show
// the whole log. A dimension with one or more values selected contributes
// exactly that allow-list: a span passes only if its value for that
// dimension was selected.
func (m Model) filter() model.Filter {
	return model.Filter{
		Providers: m.selectedFacets[dimProvider],
		RPCs:      m.selectedFacets[dimRPC],
		Types:     m.selectedFacets[dimType],
	}
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
// selected, and invalidates any cached rows so the next render reflects the
// change. Selecting a value in a dimension narrows that dimension to
// exactly the selected values; deselecting the last one in a dimension
// returns it to unconstrained.
func (m *Model) toggleSelectedFacetValue() {
	dim, val, ok := m.cursorFacetValue()
	if !ok {
		return
	}
	if m.selectedFacets == nil {
		m.selectedFacets = map[string]map[string]bool{}
	}
	inner := m.selectedFacets[dim]
	if inner == nil {
		inner = map[string]bool{}
		m.selectedFacets[dim] = inner
	}
	if inner[val] {
		delete(inner, val)
		if len(inner) == 0 {
			delete(m.selectedFacets, dim)
		}
	} else {
		inner[val] = true
	}
	m.invalidateRows()
}

// clearFilters deselects every facet value, restoring every view to the
// unfiltered log. Esc is bound to this per the spec's key table.
func (m *Model) clearFilters() {
	if len(m.selectedFacets) == 0 {
		return
	}
	m.selectedFacets = nil
	m.invalidateRows()
}

// facetFlatIndex converts the facet cursor's (dim, val) coordinate into a
// single index over every dimension's values laid end to end, so
// moveFacetCursor can move it with simple arithmetic rather than a
// dimension-boundary switch in every direction.
func (m Model) facetFlatIndex() int {
	idx := 0
	for d := 0; d < m.facetCursor.dim && d < len(m.facets); d++ {
		idx += len(m.facets[d].Values)
	}
	return idx + m.facetCursor.val
}

// facetCursorAt is facetFlatIndex's inverse: it resolves a flat index back
// into the (dim, val) coordinate it names.
func (m Model) facetCursorAt(idx int) facetCursor {
	for d, f := range m.facets {
		if idx < len(f.Values) {
			return facetCursor{dim: d, val: idx}
		}
		idx -= len(f.Values)
	}
	return facetCursor{}
}

// moveFacetCursor shifts the facet pane's cursor by delta through every
// dimension's values in display order, clamped to stay within them. A log
// with no facet values at all leaves the cursor untouched.
func (m *Model) moveFacetCursor(delta int) {
	total := 0
	for _, f := range m.facets {
		total += len(f.Values)
	}
	if total == 0 {
		return
	}
	idx := m.facetFlatIndex() + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= total {
		idx = total - 1
	}
	m.facetCursor = m.facetCursorAt(idx)
}

// renderFacets renders the facet pane: each dimension's name followed by its
// values and their span counts (counts always reflect the whole log, not
// the current filter -- see the doc comment on Model.facets), at most w
// runes wide and h lines tall. The cursor marks the value space would
// toggle -- highlighted, not just prefixed with ">", so it is actually
// visible rather than merely inferable -- and a checkbox marks whether it is
// currently selected.
func (m Model) renderFacets(w, h int) string {
	var lines []string
	for dimIdx, f := range m.facets {
		lines = append(lines, clipWidth(facetSectionHeader(f.Name), w))
		for valIdx, v := range f.Values {
			check := " "
			if m.selectedFacets[f.Name][v.Value] {
				check = "x"
			}
			cursor := dimIdx == m.facetCursor.dim && valIdx == m.facetCursor.val
			// The cursor's line is budgeted at w-selectedStyleOverhead
			// before highlightLine wraps it, so highlightLine's own clip is
			// a no-op: highlightLine clips from the end, which would
			// otherwise re-truncate the count facetValueLine just went to
			// the trouble of keeping whole (or bite back into the value's
			// tail -- the part a leading ellipsis was chosen to preserve).
			lineWidth := w
			if cursor {
				lineWidth = w - selectedStyleOverhead
			}
			line := facetValueLine(check, v.Value, v.Count, lineWidth)
			if cursor {
				line = highlightLine(line, w)
			}
			lines = append(lines, line)
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// facetValueLine formats one facet value's line -- a checkbox, the value
// and its count -- clipped to at most w runes via clipIdentifierField. The
// count is never truncated: the spec requires facets to show a count for
// every value ("each with counts"), so a count dropped by clipping would
// be a spec miss, not just a squeeze. The value itself is an identifier
// (see clipValueFront), so it is the part that gives way, front-clipped
// rather than dropped from the end.
func facetValueLine(check, value string, count, w int) string {
	return clipIdentifierField(fmt.Sprintf("[%s] ", check), value, fmt.Sprintf("  %d", count), w)
}

// facetSectionHeader upper-cases and pluralises a dimension name for
// display, matching the design mock-up's PROVIDERS/LEVELS section-header
// style rather than the lower-case singular names FacetsForSpans uses
// internally as filter keys.
func facetSectionHeader(name string) string {
	upper := strings.ToUpper(name)
	if strings.HasSuffix(upper, "S") {
		return upper
	}
	return upper + "S"
}
