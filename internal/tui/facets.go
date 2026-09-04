package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// Facet dimension names. The first three match model.FacetsForSpans
// exactly; dimLevel is this package's own, built here from the log's
// entries (see levelFacet). Named constants avoid the call sites below
// drifting out of sync with a typo in a literal string.
const (
	dimProvider = "provider"
	dimRPC      = "rpc"
	dimType     = "resource type"
	dimLevel    = "level"
)

// levelFacet builds the level dimension from the log's ENTRIES rather than
// its spans. A level is an attribute of a log line: a span is assembled
// from several lines and carries no level of its own, which is why
// model.FacetsForSpans cannot produce this dimension and it is built here.
//
// Selecting a level therefore narrows the RAW LOG only, through
// Filter.MatchEntry. The ranked views roll up spans, and no span has a
// level to match, so ticking TRACE leaves the providers, types and calls
// tables exactly as they were -- not a bug, but the reason has to be
// written down somewhere, because it is not visible from the screen.
//
// The dimension is present whichever view is showing, so the facet pane
// does not reshape as the user switches between them.
//
// Its counts are ENTRY counts, not span counts, and like every other
// dimension's they reflect the whole log rather than the current filter
// (see the doc comment on Model.facets). Values are ordered count
// descending then value ascending, the same total order
// model.FacetsForSpans gives the span dimensions.
func levelFacet(entries []logfmt.Entry) model.Facet {
	counts := map[logfmt.Level]int{}
	for _, e := range entries {
		counts[e.Level]++
	}
	f := model.Facet{Name: dimLevel, Values: make([]model.FacetValue, 0, len(counts))}
	for l, c := range counts {
		f.Values = append(f.Values, model.FacetValue{Value: l.String(), Count: c})
	}
	sort.Slice(f.Values, func(i, j int) bool {
		if f.Values[i].Count != f.Values[j].Count {
			return f.Values[i].Count > f.Values[j].Count
		}
		return f.Values[i].Value < f.Values[j].Value
	})
	return f
}

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
		Levels:    m.selectedLevels(),
	}
}

// selectedLevels turns the level dimension's selected value names back into
// the logfmt.Level values Filter.MatchEntry compares against. The names are
// Level.String()'s own, so ParseLevel reverses them exactly, "UNKNOWN"
// included. Nothing selected contributes a nil map, which is Filter's "no
// opinion".
func (m Model) selectedLevels() map[logfmt.Level]bool {
	sel := m.selectedFacets[dimLevel]
	if len(sel) == 0 {
		return nil
	}
	levels := make(map[logfmt.Level]bool, len(sel))
	for name := range sel {
		levels[logfmt.ParseLevel(name)] = true
	}
	return levels
}

// cursorFacetValue resolves the facet pane's cursor to the dimension name
// and value it currently points at. ok is false when the cursor's own
// dimension holds no values -- a dimension can be empty while others are
// not, so this is not the same as a log with no facet values at all -- and
// the pane then draws no cursor bar anywhere and space has nothing to act
// on. New seeds the cursor onto a dimension that has values
// (firstFacetCursor) so that state is not where a log starts.
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

// firstFacetCursor is the coordinate of the first value of the first
// dimension that has any. A dimension can be empty: a capture taken with
// TF_LOG=TRACE but no TF_LOG_PROVIDER has no provider RPC spans at all, so
// its provider, rpc and resource type dimensions are all empty while its
// level dimension is full. Left at {0,0} the cursor would point into one of
// those, drawing no cursor bar in the pane and leaving space inert until a
// j press teleported it past the first value of the first populated
// dimension.
func firstFacetCursor(facets []model.Facet) facetCursor {
	for d, f := range facets {
		if len(f.Values) > 0 {
			return facetCursor{dim: d}
		}
	}
	return facetCursor{}
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

// filterActive reports whether any facet value is selected anywhere, which
// is what several panes need in order to tell "the filter hid everything"
// apart from "there was nothing here to begin with" -- two states that
// render byte-identically without it.
//
// It checks the inner maps rather than trusting the outer one to be empty:
// deselecting a dimension's last value already deletes the dimension (see
// toggleSelectedFacetValue), and this must stay true whatever a later
// caller does with the map.
func (m Model) filterActive() bool {
	for _, values := range m.selectedFacets {
		if len(values) > 0 {
			return true
		}
	}
	return false
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
// values and their counts (counts always reflect the whole log, not the
// current filter -- see the doc comment on Model.facets), at most w columns
// wide and h lines tall. The cursor marks the value space would toggle --
// drawn as a bar, not just prefixed with ">", so it is actually visible
// rather than merely inferable -- and a checkbox marks whether it is
// currently selected.
//
// The lines are windowed around the cursor by the same pin-to-edge rule the
// centre table uses (scrollWindow). Showing the first h lines instead would
// put every value below the fold out of reach: the cursor moves through all
// of them, and on a real capture the resource type dimension alone runs to
// hundreds of values, so j would move an invisible cursor and space would
// toggle a filter the user cannot see.
func (m Model) renderFacets(w, h int) string {
	lines, cursor, headerIdx := m.facetLines(w)
	top, visible := scrollWindow(cursor, len(lines), h)
	if visible == 0 {
		return ""
	}
	out := append([]string(nil), lines[top:top+visible]...)
	// Keep the cursor's own dimension labelled once the window has scrolled
	// past that dimension's header: the first visible line stands in for it,
	// which costs one value line rather than leaving a column of checkboxes
	// with nothing to say what they select. The cursor is never the line
	// given up -- scrollWindow only pins it to the window's top edge when
	// there is a single line to show, and a single line has no room for a
	// header anyway.
	if headerIdx < top && cursor > top {
		out[0] = lines[headerIdx]
	}
	return strings.Join(out, "\n")
}

// facetLines renders every dimension's header and every value beneath it
// into one flat list of lines, and reports which line the cursor sits on
// and which line holds the header of the dimension the cursor is in.
// Building the list whole is what lets the cursor's flat value index
// (facetFlatIndex) be resolved against a display that also carries headers.
func (m Model) facetLines(w int) (lines []string, cursor, headerIdx int) {
	focused := m.pane == PaneFacets
	for dimIdx, f := range m.facets {
		if dimIdx == m.facetCursor.dim {
			headerIdx = len(lines)
		}
		lines = append(lines, clipWidth(facetSectionHeader(f.Name), w))
		kind := facetValueKind(f.Name)
		for valIdx, v := range f.Values {
			check := " "
			if m.selectedFacets[f.Name][v.Value] {
				check = "x"
			}
			// Every line is built at the full pane width, cursor or not:
			// the escapes cursorBar adds cost no terminal columns, so its
			// own end-clip is a no-op here rather than re-truncating the
			// count facetValueLine went to the trouble of keeping whole (or
			// biting back into the value's tail -- the part a leading
			// ellipsis was chosen to preserve).
			line := facetValueLine(check, v.Value, v.Count, w, kind)
			if dimIdx == m.facetCursor.dim && valIdx == m.facetCursor.val {
				cursor = len(lines)
				line = cursorBar(line, w, focused)
			}
			lines = append(lines, line)
		}
	}
	return lines, cursor, headerIdx
}

// facetValueKind is the kind of value a facet dimension holds, and so which
// end of an over-long value survives clipping (see columnKind). The rpc
// dimension's values are plugin-protocol method names, told apart by their
// HEAD -- PlanResourceChange and ApplyResourceChange share a 14-character
// suffix -- and a level name is a single word, told apart by its head as
// well, while a provider address or a resource type is told apart by its
// TAIL. This is the only place a dimension name is resolved to a kind, so
// no two render sites can disagree about a dimension.
func facetValueKind(dim string) columnKind {
	if dim == dimRPC || dim == dimLevel {
		return headIdentifierColumn
	}
	return tailIdentifierColumn
}

// facetValueLine formats one facet value's line -- a checkbox, the value
// and its count -- clipped to at most w columns via clipIdentifierField. The
// count is never truncated: the spec requires facets to show a count for
// every value ("each with counts"), so a count dropped by clipping would
// be a spec miss, not just a squeeze. The value itself is the part that
// gives way, clipped from whichever end its kind allows rather than dropped
// from the end regardless -- a facet value is a control, so two values that
// clip to the same text are two checkboxes the user cannot choose between.
func facetValueLine(check, value string, count, w int, kind columnKind) string {
	return clipIdentifierField(fmt.Sprintf("[%s] ", check), value, fmt.Sprintf("  %d", count), w, kind)
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
