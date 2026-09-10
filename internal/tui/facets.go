package tui

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
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
	dimResource = "resource"
	dimModule   = "module subtree"
)

// levelFacet builds the level dimension from the log's ENTRIES rather than
// its spans. A level is an attribute of a log line: a span is assembled
// from several lines and carries no level of its own, which is why
// model.FacetsForSpans cannot produce this dimension and it is built here.
//
// Unticking a level therefore narrows the RAW LOG only, through
// Filter.MatchEntry. The ranked views roll up spans, and no span has a
// level to match, so unticking TRACE leaves the providers, types and calls
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

// resourceFacets builds whole-log choices from the cached resource index.
// Counts are distinct resource addresses: one for an exact address and the
// number of known addresses contained by a module subtree. A module's parent
// is the nearest known structural ancestor, found independently of the
// modules' lexical order.
func resourceFacets(index model.ResourceIndex) []model.Facet {
	resources := model.Facet{Name: dimResource, Values: make([]model.FacetValue, len(index.Choices))}
	exactModuleCounts := make(map[string]int, len(index.Modules))
	for i, choice := range index.Choices {
		resources.Values[i] = model.FacetValue{Value: choice.Address, Count: 1}
		if choice.Module.Known {
			exactModuleCounts[choice.Module.Path]++
		}
	}

	parents := make([]int, len(index.Modules))
	counts := make([]int, len(index.Modules))
	moduleIndices := make(map[string]int, len(index.Modules))
	for i, module := range index.Modules {
		moduleIndices[module] = i
	}
	for i, module := range index.Modules {
		parents[i] = -1
		counts[i] = exactModuleCounts[module]
		for candidate := module; candidate != ""; {
			dot := strings.LastIndex(candidate, ".")
			if dot < 0 {
				break
			}
			candidate = candidate[:dot]
			if parent, ok := moduleIndices[candidate]; ok && model.ModuleContains(candidate, module) {
				parents[i] = parent
				break
			}
		}
		if parents[i] < 0 && module != "" {
			if root, ok := moduleIndices[""]; ok {
				parents[i] = root
			}
		}
	}
	for i := len(index.Modules) - 1; i >= 0; i-- {
		if parents[i] >= 0 {
			counts[parents[i]] += counts[i]
		}
	}
	modules := model.Facet{Name: dimModule, Values: make([]model.FacetValue, len(index.Modules))}
	for i, module := range index.Modules {
		modules.Values[i] = model.FacetValue{Value: module, Count: counts[i]}
	}
	return []model.Facet{resources, modules}
}

func mergeUIResourceTypes(facets []model.Facet, spans []span.Span) {
	for i := range facets {
		if facets[i].Name != dimType {
			continue
		}
		counts := make(map[string]int, len(facets[i].Values))
		for _, value := range facets[i].Values {
			counts[value.Value] = value.Count
		}
		for _, s := range spans {
			counts[model.FacetKey(s.ResourceType)]++
		}
		facets[i].Values = facets[i].Values[:0]
		for value, count := range counts {
			facets[i].Values = append(facets[i].Values, model.FacetValue{Value: value, Count: count})
		}
		sort.Slice(facets[i].Values, func(a, b int) bool {
			if facets[i].Values[a].Count != facets[i].Values[b].Count {
				return facets[i].Values[a].Count > facets[i].Values[b].Count
			}
			return facets[i].Values[a].Value < facets[i].Values[b].Value
		})
		return
	}
}

// filter derives the model.Filter this model's ticked checkboxes represent:
// per dimension, every value the pane offers except the ones the reader has
// unticked (allowedFacetValues).
//
// This is the filter as it applies to the RPC tier and to log entries. D1's
// resource projection applies only its type dimension to UI observations.
func (m Model) filter() model.Filter {
	return model.Filter{
		Providers: m.allowedFacetValues(dimProvider),
		RPCs:      m.allowedFacetValues(dimRPC),
		Types:     m.allowedFacetValues(dimType),
		Levels:    m.allowedLevels(),
	}
}

// allowedFacetValues turns one dimension's exclusions into the allow-list
// model.Filter takes: every value the facet pane offers for that dimension
// except the ones the reader has unticked.
//
// A dimension with nothing excluded contributes NIL -- Filter's "no
// opinion" -- rather than an allow-list that happens to name every value.
// The two would filter alike, but nil says what an untouched pane means and
// costs no per-span map lookup, and it is what filterActive and the empty-
// pane notes elsewhere are consistent with.
//
// A dimension with every value excluded contributes an EMPTY non-nil map,
// which model.Filter admits nothing through. That is the reading the
// checkboxes require: no box ticked is no value admitted. It is also why
// this cannot report "no opinion" by length alone.
func (m Model) allowedFacetValues(dim string) map[string]bool {
	excluded := m.excludedFacets[dim]
	if len(excluded) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, v := range m.facetValues(dim) {
		if !excluded[v.Value] {
			allowed[v.Value] = true
		}
	}
	return allowed
}

// facetValues is the values one dimension offers, in the order the pane
// draws them, or nil for a dimension the pane does not carry. Dimension
// names are unique by construction -- three from model.FacetsForSpans and
// dimLevel from levelFacet -- so the first match is the only match.
func (m Model) facetValues(dim string) []model.FacetValue {
	for _, f := range m.facets {
		if f.Name == dim {
			return f.Values
		}
	}
	return nil
}

// allowedLevels turns the level dimension's allow-list of value names back
// into the logfmt.Level values Filter.MatchEntry compares against. The names
// are Level.String()'s own, so ParseLevel reverses them exactly, "UNKNOWN"
// included. Nil and empty are carried across unchanged: nil is Filter's "no
// opinion", empty admits no level at all.
func (m Model) allowedLevels() map[logfmt.Level]bool {
	allowed := m.allowedFacetValues(dimLevel)
	if allowed == nil {
		return nil
	}
	levels := make(map[logfmt.Level]bool, len(allowed))
	for name := range allowed {
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
	if !slices.Contains(m.visibleFacetIndices(f.Name), m.facetCursor.val) {
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

// toggleFacetValue flips whether the facet pane's cursor value is ticked,
// and invalidates any cached rows so the next render reflects the change.
// Unticking a value removes its explicit selection; ticking it back restores
// it. A module can remain included through a selected ancestor. Re-ticking a
// dimension's last excluded value returns that dimension to unconstrained
// -- the state a fresh model starts in, rather than an allow-list naming
// every value (see allowedFacetValues).
//
// The exclusion set is rebuilt and handed to setFacetExclusions rather than
// mutated where it sits, so both writers of this map replace rather than
// edit in place. A Model is driven through a pointer and never copied (see
// Model), but the two agreeing costs a map copy of a handful of strings and
// removes the one asymmetry between them.
func (m *Model) toggleFacetValue() {
	dim, val, ok := m.cursorFacetValue()
	if !ok {
		return
	}
	if namedFacetDimension(dim) {
		selected := m.namedFacetSelection(dim)
		if selected == nil {
			selected = make(map[string]bool, len(m.facetValues(dim)))
			for _, value := range m.facetValues(dim) {
				if value.Value != val {
					selected[value.Value] = true
				}
			}
		} else {
			selected = maps.Clone(selected)
			if selected[val] {
				delete(selected, val)
			} else {
				selected[val] = true
			}
		}
		m.setNamedFacetSelection(dim, selected)
		m.invalidateRows()
		return
	}
	excluded := map[string]bool{}
	maps.Copy(excluded, m.excludedFacets[dim])
	if excluded[val] {
		delete(excluded, val)
	} else {
		excluded[val] = true
	}
	m.setFacetExclusions(dim, excluded)
	m.invalidateRows()
}

// soloFacetValue narrows the cursor's dimension to the value the cursor is
// on, by unticking every OTHER value that dimension offers, and invalidates
// any cached rows so the next render reflects the change. Other dimensions
// are left exactly as they were, so it composes with the cumulative rule
// rather than overriding it: soloing a provider does not undo a resource
// type the reader narrowed to earlier.
//
// It exists because every value starts ticked (see Model.excludedFacets),
// which makes narrowing to ONE value of a dimension cost a press of space
// for every OTHER value that dimension offers -- a count that grows with
// the capture. Space is still the way to hide one value; this is the way to
// keep one.
//
// Pressed again on a value that is already its dimension's only ticked one,
// it puts the whole dimension back -- every value of it, including any the
// reader had unticked with space before soloing. What it spares is the
// OTHER dimensions, which Esc, the alternative undo, clears along with this
// one.
//
// "Already soloed" is compared against the exclusions this would write, not
// against a flag, so a dimension the reader narrowed to one value with
// space alone is restored by o just the same -- the two routes reach one
// state and o reads the state, not how it was reached.
//
// The write is a replacement, not an addition: soloing a value the reader
// had unticked re-ticks it, which is what "show only this" has to mean.
//
// A dimension offering a single value has no others to untick, so soloing
// it leaves the dimension unconstrained rather than holding an empty
// exclusion set -- the same shape toggleFacetValue keeps, and the one
// allowedFacetValues and filterActive both read.
func (m *Model) soloFacetValue() {
	dim, val, ok := m.cursorFacetValue()
	if !ok {
		return
	}
	if namedFacetDimension(dim) {
		selected := m.namedFacetSelection(dim)
		if len(selected) == 1 && selected[val] {
			m.setNamedFacetSelection(dim, nil)
		} else {
			m.setNamedFacetSelection(dim, map[string]bool{val: true})
		}
		m.invalidateRows()
		return
	}
	others := map[string]bool{}
	for _, v := range m.facetValues(dim) {
		if v.Value != val {
			others[v.Value] = true
		}
	}
	if maps.Equal(m.excludedFacets[dim], others) {
		// Already showing this value alone: a second press puts the whole
		// dimension back. An empty others -- a dimension offering one value
		// -- takes this branch too, since maps.Equal reads a nil exclusion
		// set and an empty one alike.
		others = nil
	}
	m.setFacetExclusions(dim, others)
	m.invalidateRows()
}

func (m Model) namedFacetSelection(dim string) map[string]bool {
	if dim == dimResource {
		return m.resourceSelection.Addresses
	}
	return m.resourceSelection.Modules
}

func (m *Model) setNamedFacetSelection(dim string, selected map[string]bool) {
	if dim == dimResource {
		m.resourceSelection.Addresses = selected
	} else {
		m.resourceSelection.Modules = selected
	}
}

// setFacetExclusions records which of a dimension's values are unticked,
// keeping the shape allowedFacetValues and filterActive read: a dimension
// with nothing excluded is ABSENT from the map, not present holding an
// empty set. It is the one place that shape is decided, so the two writers
// above cannot come to disagree about what an unconstrained dimension looks
// like.
//
// delete is a no-op on a nil map, so a model whose filter was never touched
// allocates nothing.
func (m *Model) setFacetExclusions(dim string, excluded map[string]bool) {
	if len(excluded) == 0 {
		delete(m.excludedFacets, dim)
		return
	}
	if m.excludedFacets == nil {
		m.excludedFacets = map[string]map[string]bool{}
	}
	m.excludedFacets[dim] = excluded
}

// filterActive reports whether any facet value is unticked anywhere, which
// is what several panes need in order to tell "the filter hid everything"
// apart from "there was nothing here to begin with" -- two states that
// render byte-identically without it.
//
// It checks the inner maps rather than trusting the outer one to be empty:
// re-ticking a dimension's last excluded value already deletes the
// dimension (see toggleFacetValue), and this must stay true whatever a
// later caller does with the map.
func (m Model) filterActive() bool {
	if m.resourceSelection.Addresses != nil || m.resourceSelection.Modules != nil {
		return true
	}
	for _, values := range m.excludedFacets {
		if len(values) > 0 {
			return true
		}
	}
	return false
}

// clearFilters re-ticks every facet value, restoring every view to the
// unfiltered log. Esc is bound to this per the spec's key table.
func (m *Model) clearFilters() {
	if len(m.excludedFacets) == 0 && m.resourceSelection.Addresses == nil && m.resourceSelection.Modules == nil {
		return
	}
	m.excludedFacets = nil
	m.resourceSelection = model.ResourceSelection{}
	m.invalidateRows()
}

// facetFlatIndex converts the facet cursor's (dim, val) coordinate into a
// single index over every dimension's values laid end to end, so
// moveFacetCursor can move it with simple arithmetic rather than a
// dimension-boundary switch in every direction. An empty narrowed dimension
// is an insertion point between the visible values before and after it.
func (m Model) facetFlatIndex() int {
	for i, cursor := range m.visibleFacetCursors() {
		if cursor == m.facetCursor {
			return i
		}
	}
	return m.visibleFacetCountBefore(m.facetCursor.dim)
}

func (m Model) visibleFacetCountBefore(dim int) int {
	count := 0
	for d := 0; d < dim && d < len(m.facets); d++ {
		count += len(m.visibleFacetIndices(m.facets[d].Name))
	}
	return count
}

func (m Model) visibleFacetCursors() []facetCursor {
	var cursors []facetCursor
	for d, facet := range m.facets {
		for _, value := range m.visibleFacetIndices(facet.Name) {
			cursors = append(cursors, facetCursor{dim: d, val: value})
		}
	}
	return cursors
}

// moveFacetCursor shifts the facet pane's cursor by delta through every
// dimension's values in display order, clamped to stay within them. An empty
// narrowed dimension acts as an insertion point: down selects the next
// visible value and up selects the previous one. A log with no facet values
// at all leaves the cursor untouched.
func (m *Model) moveFacetCursor(delta int) {
	cursors := m.visibleFacetCursors()
	total := len(cursors)
	if total == 0 {
		return
	}
	idx := m.facetFlatIndex() + delta
	if m.facetCursor.val < 0 && delta > 0 {
		idx--
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= total {
		idx = total - 1
	}
	m.facetCursor = cursors[idx]
}

// facetPaneTitle names the facet pane in the pane row's top rule.
//
// The pane needs a name of its own because no line inside it is one: the
// pane windows around its cursor (see renderFacets), so which dimension
// heading sits at the top depends on where the cursor is, and a heading read
// as a title is a title that changes when the user presses j.
//
// It is FILTERS rather than FACETS because what a reader does here is
// filter; "facet" is the word for the machinery, and it is already spelled
// out on every heading beneath.
const facetPaneTitle = "FILTERS"

// renderFacets renders the facet pane: each dimension's name followed by its
// values and their counts (counts always reflect the whole log, not the
// current filter -- see the doc comment on Model.facets), at most w columns
// wide and h lines tall. The cursor marks the value space would toggle --
// drawn as a bar, not just prefixed with ">", so it is actually visible
// rather than merely inferable -- and a checkbox marks whether it is
// currently admitted, every value starting ticked.
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
	// with nothing to say what they filter. The cursor is never the line
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
	countWidth := facetCountWidth(m.facets)
	for dimIdx, f := range m.facets {
		if dimIdx == m.facetCursor.dim {
			headerIdx = len(lines)
		}
		lines = append(lines, styles.title.Render(clipWidth(m.facetHeading(f.Name), w)))
		if dimIdx == m.facetCursor.dim && m.facetCursor.val < 0 {
			cursor = headerIdx
		}
		kind := facetValueKind(f.Name)
		for _, valIdx := range m.visibleFacetIndices(f.Name) {
			v := f.Values[valIdx]
			// Named values begin with one explicit-selection lookup. An
			// unchecked module then asks ResourceSelection whether a selected
			// ancestor includes it; Match resolves ancestors with map lookups.
			excluded := m.excludedFacets[f.Name][v.Value]
			inherited := false
			if namedFacetDimension(f.Name) {
				selected := m.namedFacetSelection(f.Name)
				excluded = selected != nil && !selected[v.Value]
				if excluded && f.Name == dimModule {
					membership := model.ResourceSelection{Modules: selected}.Match("", model.ResourceModule{Path: v.Value, Known: true})
					inherited = membership == model.MembershipSelected
				}
			}
			check := "x"
			if inherited {
				check = "+"
			} else if excluded {
				check = " "
			}
			// Every line is built at the full pane width, cursor or not:
			// the escapes cursorBar adds cost no terminal columns, so its
			// own end-clip is a no-op here rather than re-truncating the
			// count facetValueLine went to the trouble of keeping whole (or
			// biting back into the value's tail -- the part a leading
			// ellipsis was chosen to preserve).
			line := facetValueLine(check, displayFacetValue(f.Name, v.Value), v.Count, countWidth, w, kind)
			switch {
			case dimIdx == m.facetCursor.dim && valIdx == m.facetCursor.val:
				cursor = len(lines)
				line = cursorBar(line, w, focused)
			case excluded && !inherited:
				// An unticked value recedes, so what the filter still
				// admits reads at a glance rather than by inspecting the
				// character inside each bracket. The cursor's own line is
				// the case this must NOT reach, and the switch is what
				// keeps it out: cursorBar's reverse video ends at the
				// first reset inside what it wraps, so a dimmed line under
				// the cursor would show a bar that stopped partway along.
				line = styles.excludedValue.Render(line)
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

// facetValueLine formats one facet value's line: a checkbox, the value in a
// column of its own, and the count right-aligned against the pane's right
// edge in a column countWidth wide. Too narrow to hold those two columns
// apart, it falls back to packing them (see the branch below). The
// count is the LAST thing given up: the spec requires facets to show a count
// for every value ("each with counts"), so a count dropped while the value
// beside it still had columns would be a spec miss, not just a squeeze. Only
// a pane narrower than the count's own digits takes it, and then there is
// nowhere for it to go. The value itself is the part that
// gives way, clipped from whichever end its kind allows rather than dropped
// from the end regardless -- a facet value is a control, so two values that
// clip to the same text are two checkboxes the user cannot choose between.
func facetValueLine(check, value string, count, countWidth, w int, kind columnKind) string {
	value = logfmt.DisplayText(value)
	prefix := facetCheckbox(check)
	avail := w - facetCheckboxWidth - lipgloss.Width(facetCountGap) - countWidth
	if avail < 1 {
		// Too narrow to hold a value column and a count column apart. The
		// count takes the right-hand columns and the checkbox and value
		// share whatever is left, so the count is the LAST thing given up
		// rather than the first: the spec requires one for every value, and
		// a checkbox with no count says nothing about what ticking it would
		// narrow. Reached through the facet overlay, which renders at the
		// full terminal width; every inline pane has minFacetPaneWidth
		// beneath it.
		digits := strconv.Itoa(count)
		head := w - lipgloss.Width(digits)
		if head < 1 {
			return clipWidth(digits, w)
		}
		return padRight(clipWidth(prefix+clipValueForKind(value, head-facetCheckboxWidth, kind), head), head) + digits
	}
	// The cell is held to EXACTLY avail columns from both directions --
	// clipped down, padded up -- rather than trusting the kind to have
	// clipped it. What rests on that is the count: the closing clipWidth
	// below cuts from the END, so a cell even one column over its share
	// takes the cut out of the count, which is the one thing this function
	// promises never to drop. clipValueForKind returns a numericColumn
	// value untouched, so the guarantee has to be made here and not assumed
	// from the kinds facetValueKind happens to return today.
	cell := padRight(clipWidth(clipValueForKind(value, avail, kind), avail), avail)
	return clipWidth(prefix+cell+facetCountGap+padLeft(strconv.Itoa(count), countWidth), w)
}

// facetCountGap separates a facet value from its count.
const facetCountGap = "  "

// facetCheckbox is the tick box at the head of a value's line, with the
// space separating it from the value, and facetCheckboxWidth is what it
// costs. The width is MEASURED off the thing itself rather than written
// down beside it, the way paneSepWidth is derived from paneSep: a number
// kept alongside is a number that can disagree with what is drawn, and both
// this and the natural-width arithmetic depend on the two matching.
func facetCheckbox(check string) string {
	return "[" + check + "] "
}

var facetCheckboxWidth = lipgloss.Width(facetCheckbox(" "))

// facetCountWidth is how many columns the count column takes: the widest
// count in the pane, so every count is right-aligned into one column and
// they can be compared down the pane rather than read one at a time.
//
// It is measured across EVERY dimension, not per dimension. A column that
// reset at each heading would put four count columns on one screen and read
// as four unrelated lists -- the same argument renderHelp measures its key
// column across every group for.
//
// Unlike facetNaturalWidth beside it, this is measured per FRAME rather than
// kept on the Model. Both walk the same immutable facets, but the saving
// does not transfer: facetLines already composes a line for every value on
// every frame, so this adds one integer conversion to a pass that was
// O(values) regardless. What a stored copy would add is a third statement
// of the width -- the pane's, the rendered line's, and the field's -- and a
// Model built without New would carry a zero that silently disagrees with
// the width its own pane was sized to.
func facetCountWidth(facets []model.Facet) int {
	width := 1
	for _, f := range facets {
		for _, v := range f.Values {
			width = max(width, lipgloss.Width(strconv.Itoa(v.Count)))
		}
	}
	return width
}

// facetValueNaturalWidth is how wide a value's line is with nothing clipped:
// the checkbox, the value, the gap and the count column.
//
// It is measured rather than rendered because the rendered form PADS the
// value out to push the count against the pane's right edge, so composing a
// line at a notional infinite width would measure the padding instead of the
// content -- and allocate it.
func facetValueNaturalWidth(value string, countWidth int) int {
	return facetCheckboxWidth + lipgloss.Width(logfmt.DisplayText(value)) + lipgloss.Width(facetCountGap) + countWidth
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
