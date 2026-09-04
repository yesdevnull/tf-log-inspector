package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// row is one line of the centre pane's list, already formatted into display
// cells.
type row struct {
	cells []string
	// spanIdx is the index into m.log.RPCSpans that this row represents, or
	// -1 when the row is a rollup rather than a single span. Task 5's
	// jump-to-log needs it, so it is established here rather than
	// retrofitted.
	spanIdx int
}

// columnKind says what a column holds. Which end of an over-long value
// survives clipping is a property of the value itself, not of the render
// site, so a column declares its kind once and formatRow derives both the
// alignment and the clip direction from it -- there is no separate
// alignment field that could disagree with the kind.
//
// It is the whole package's value taxonomy, not just the tables': the facet
// pane resolves each dimension's name to a kind (facetValueKind) and clips
// its values by the same rule, so the same value is clipped the same way
// wherever it appears.
type columnKind int

const (
	// tailIdentifierColumn holds an identifier distinguished from its
	// siblings by its TAIL -- a provider address, a resource type. It is
	// left-aligned and front-clipped via clipValueFront, so
	// "…/hashicorp/aws" and "…/hashicorp/google" stay apart. It is the zero
	// value: a column that forgets to declare a kind gets the conservative
	// treatment rather than being reserved at full width as a number.
	tailIdentifierColumn columnKind = iota
	// headIdentifierColumn holds an identifier distinguished by its HEAD --
	// an RPC name, one of a closed set of plugin-protocol methods that share
	// long suffixes (...ResourceChange, ...ResourceConfig,
	// ...ResourceState) and diverge in their first few characters. It is
	// left-aligned and end-clipped via clipValueEnd: front-clipping
	// PlanResourceChange and ApplyResourceChange renders both as "…eChange".
	headIdentifierColumn
	// numericColumn holds a formatted number -- a duration or a count. It is
	// right-aligned and never clipped: fitColumnWidths reserves it at its
	// full natural width, because a half-shown number tells the reader
	// nothing.
	numericColumn
)

// column describes one column of a rendered list table.
type column struct {
	header string
	kind   columnKind
}

var providerColumns = []column{
	{header: "provider", kind: tailIdentifierColumn},
	{header: "total", kind: numericColumn},
	{header: "calls", kind: numericColumn},
	{header: "max", kind: numericColumn},
}

var typeColumns = []column{
	{header: "resource type", kind: tailIdentifierColumn},
	{header: "UI res.", kind: numericColumn},
	{header: "UI total", kind: numericColumn},
	{header: "RPC calls", kind: numericColumn},
	{header: "RPC total", kind: numericColumn},
	{header: "RPC max", kind: numericColumn},
}

var callColumns = []column{
	{header: "duration", kind: numericColumn},
	{header: "RPC", kind: headIdentifierColumn},
	{header: "resource type", kind: tailIdentifierColumn},
	{header: "provider", kind: tailIdentifierColumn},
}

// rows returns the current view's rows, restricted to the active facet
// filter. ViewRawLog is not a rollup and has no rows of its own -- it
// renders directly from m.log.Entries -- so it returns nil here.
//
// The result is cached on m (see invalidateRows) so repeated calls between
// filter or view changes -- moveSelection calling RowCount() on every
// arrow-key press, or a render calling rows() again after RowCount already
// did -- reuse it rather than redoing a full RollupBy/JoinByResourceType/sort
// each time. Every caller must reach this through a pointer, or it fills a
// cache on a copy that is immediately discarded.
func (m *Model) rows() []row {
	if m.rowsCached {
		return m.rowsCache
	}
	f := m.filter()
	var r []row
	switch m.view {
	case ViewProviders:
		r = providerRows(f.SpansMatching(m.log.RPCSpans))
	case ViewTypes:
		r = typeRows(f.SpansMatching(m.log.RPCSpans), f.SpansMatching(m.log.UISpans))
	case ViewCalls:
		r = callRows(m.log.RPCSpans, f)
	}
	m.rowsCache = r
	m.rowsCached = true
	return r
}

// providerRows ranks providers by total RPC time, as model.RollupBy already
// orders them. No row is a single span, so every spanIdx is -1.
func providerRows(rpcSpans []span.Span) []row {
	buckets := model.RollupBy(rpcSpans, func(s span.Span) string { return s.Provider })
	rows := make([]row, len(buckets))
	for i, b := range buckets {
		rows[i] = row{
			cells: []string{
				b.Key,
				formatMs(b.TotalMs),
				strconv.Itoa(b.Count),
				formatMs(uint64(b.MaxMs)),
			},
			spanIdx: -1,
		}
	}
	return rows
}

// typeRows joins the RPC and UI-hook tiers by resource type, as
// model.JoinByResourceType already orders them. No row is a single span --
// each is a rollup over possibly many of both -- so every spanIdx is -1.
func typeRows(rpcSpans, uiSpans []span.Span) []row {
	joined := model.JoinByResourceType(rpcSpans, uiSpans)
	rows := make([]row, len(joined))
	for i, r := range joined {
		rows[i] = row{
			cells: []string{
				r.ResourceType,
				strconv.Itoa(r.UIResources),
				formatMs(r.UITotalMs),
				strconv.Itoa(r.RPCCalls),
				formatMs(r.RPCTotalMs),
				formatMs(uint64(r.RPCMaxMs)),
			},
			spanIdx: -1,
		}
	}
	return rows
}

// callRows ranks the RPC spans matching f by duration descending, ties
// broken by RPC name so the ordering is total. Each row's spanIdx indexes
// into rpcSpans itself, never into a filtered subset, so jump-to-log (Task
// 5) keeps landing on the right span even while a filter narrows the list.
// It sorts a slice of indices rather than the spans themselves: m.log.RPCSpans
// must not be mutated, since every other view and any later --profile run
// over the same Log reads it too.
func callRows(rpcSpans []span.Span, f model.Filter) []row {
	idx := make([]int, 0, len(rpcSpans))
	for i, s := range rpcSpans {
		if f.MatchSpan(s) {
			idx = append(idx, i)
		}
	}
	sort.Slice(idx, func(i, j int) bool {
		a, b := rpcSpans[idx[i]], rpcSpans[idx[j]]
		if a.DurationMs != b.DurationMs {
			return a.DurationMs > b.DurationMs
		}
		return a.RPC < b.RPC
	})

	rows := make([]row, len(idx))
	for i, si := range idx {
		s := rpcSpans[si]
		rows[i] = row{
			cells: []string{
				formatMs(uint64(s.DurationMs)),
				s.RPC,
				s.ResourceType,
				s.Provider,
			},
			spanIdx: si,
		}
	}
	return rows
}

// renderList renders the current view's rows as a table at most w runes
// wide and h lines tall, with the row at Selected() highlighted and the
// window scrolled just far enough to keep it visible.
func (m *Model) renderList(w, h int) string {
	switch m.view {
	case ViewProviders:
		return renderTable(nil, providerColumns, m.rows(), m.selected, w, h)
	case ViewTypes:
		preamble := typesPreamble(m.filter().SpansMatching(m.log.UISpans))
		return renderTable(preamble, typeColumns, m.rows(), m.selected, w, h)
	case ViewCalls:
		return renderTable(nil, callColumns, m.rows(), m.selected, w, h)
	default:
		return ""
	}
}

// typesPreamble states the UI-hook resolution caveat above the types table,
// but only when UI-hook figures are actually present to rank -- a log with
// RPC spans only has nothing to caveat. Terraform rounds a resource's start
// and end to the nearest second before subtracting them, so these figures
// carry up to a second of error each; see the identical caveat in
// internal/profile.Render's BY RESOURCE TYPE section, whose wording this
// matches.
func typesPreamble(uiSpans []span.Span) []string {
	if len(uiSpans) == 0 {
		return nil
	}
	return []string{"UI-hook figures are sums of measurements rounded to whole seconds, +/- 1s each."}
}

// renderTable formats preamble lines followed by a header and data rows as a
// table, column widths taken from the widest header or cell in each column.
//
// Numbers are the point of a ranked view: an identifier clipped to its tail
// is still recognisable, but a missing duration or count tells the reader
// nothing. So every numeric (right-aligned) column keeps its full natural
// width unconditionally -- dropping or shrinking a number is what round 1
// got wrong -- and fitColumnWidths distributes whatever width is left among
// the text columns (every one of this package's tables carries only
// identifiers in its text columns: provider addresses, resource types and
// RPC names). A text column narrower than its natural width is clipped by
// formatRow, not dropped, from whichever end its columnKind says carries
// the less distinguishing part of the value: two rows whose providers or
// RPCs differ must still render differently, or the ranking they sit in is
// unreadable. clipWidth remains a safety net beneath all of this for the
// pathological case where even the numeric columns alone exceed w.
//
// selected is highlighted, and the data rows are windowed so it stays
// visible within h lines total: the preamble and header are never scrolled,
// only the data rows beneath them are.
//
// Every row, highlighted or not, is fit and clipped to the full w: the
// escape sequences highlightLine wraps the selected row in occupy no
// terminal columns, so the highlighted row has exactly as many columns of
// content as its neighbours and column widths stay shared across the whole
// table.
//
// The header is clipped as prose, not as an identifier: a column header
// ("resource type") is told apart by its head even where the column's
// values are told apart by their tails, so it end-clips while they
// front-clip. See headerKinds.
func renderTable(preamble []string, cols []column, data []row, selected, w, h int) string {
	widths := fitColumnWidths(cols, columnWidths(cols, data), w)
	kinds := columnKinds(cols)

	lines := make([]string, 0, len(preamble)+1+len(data))
	for _, p := range preamble {
		lines = append(lines, clipWidth(p, w))
	}
	lines = append(lines, clipWidth(formatRow(headerCells(cols), headerKinds(cols), widths), w))

	dataH := h - len(lines)
	top, visible := scrollWindow(selected, len(data), dataH)
	for i := top; i < top+visible; i++ {
		line := clipWidth(formatRow(data[i].cells, kinds, widths), w)
		if i == selected {
			line = highlightLine(line, w)
		}
		lines = append(lines, line)
	}

	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// fitColumnWidths returns each column's final width: every numeric column
// keeps its natural width from `natural` unconditionally, and the text
// columns -- both identifier kinds alike, since only their clip direction
// differs -- share whatever remains after those and the two-space gaps
// between every column are reserved, each capped at its own natural
// width -- so a column that already fits never shrinks just because a
// sibling needs more.
//
// When the text columns collectively need more than what remains, the
// shortfall is distributed by water-filling rather than shrinking whichever
// column happens to be widest (or first, or last): repeatedly split the
// space still available evenly across the columns still competing for it,
// settle any column whose natural width is at or under that even share at
// its full natural width, and remove it from the competition; what it did
// not need goes back into the pool for the columns still competing. This
// is the general form of round 2's single-flex-column shrink -- correct
// for the one-text-column tables (providers, types) it was built for, and
// also correct for the calls view's three text columns (RPC, resource
// type, provider), where letting only the single widest one flex left the
// other two either always full width (risking the total exceeding w, which
// pushed the overflow onto whichever column the final clipWidth reached)
// or never shrinking when they should share the burden.
func fitColumnWidths(cols []column, natural []int, w int) []int {
	widths := append([]int(nil), natural...)

	reserved, pool := 2*(len(cols)-1), make([]int, 0, len(cols)) // gap between every adjacent column pair
	for i, c := range cols {
		if c.kind == numericColumn {
			reserved += natural[i]
		} else {
			pool = append(pool, i)
		}
	}
	if len(pool) == 0 {
		return widths
	}
	remaining := w - reserved
	if remaining < 0 {
		remaining = 0
	}

	for len(pool) > 0 {
		share := remaining / len(pool)
		var stillCompeting []int
		settledAny := false
		for _, i := range pool {
			if natural[i] <= share {
				remaining -= natural[i]
				settledAny = true
			} else {
				stillCompeting = append(stillCompeting, i)
			}
		}
		if !settledAny {
			// Every remaining column wants more than an even share: split
			// what's left evenly, one extra rune to each of the first
			// remaining%len(pool) columns so the total assigned is exactly
			// remaining rather than falling short to integer rounding.
			base, extra := remaining/len(pool), remaining%len(pool)
			for k, i := range pool {
				widths[i] = base
				if k < extra {
					widths[i]++
				}
			}
			return widths
		}
		pool = stillCompeting
	}
	return widths
}

// scrollWindow returns the first visible data-row index and how many rows
// fit in dataH lines, keeping selected on screen. It pins the window to
// whichever edge selected has crossed rather than centring it in the
// window, so moving the selection by one row scrolls by at most one row
// once past the first screenful, instead of jumping to keep it centred.
func scrollWindow(selected, total, dataH int) (top, visible int) {
	if dataH <= 0 || total == 0 {
		return 0, 0
	}
	if selected >= dataH {
		top = selected - dataH + 1
	}
	if top+dataH > total {
		top = total - dataH
	}
	if top < 0 {
		top = 0
	}
	visible = total - top
	if visible > dataH {
		visible = dataH
	}
	return top, visible
}

func headerCells(cols []column) []string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = c.header
	}
	return cells
}

// columnKinds is each column's kind, in order: what formatRow needs to
// align and clip that column's VALUES.
func columnKinds(cols []column) []columnKind {
	kinds := make([]columnKind, len(cols))
	for i, c := range cols {
		kinds[i] = c.kind
	}
	return kinds
}

// headerKinds is how each column's HEADER is aligned and clipped, which is
// not always how its values are. A header is prose -- "resource type",
// "provider" -- told apart by its head, so a text column's header
// end-clips even where the column's values front-clip: front-clipping
// "resource type" to "…urce type" labels the column with a word fragment.
// A numeric column's header keeps the column's own kind, staying
// right-aligned over its numbers and never clipped: fitColumnWidths
// reserves such a column at its natural width, which columnWidths already
// measured against the header itself.
func headerKinds(cols []column) []columnKind {
	kinds := make([]columnKind, len(cols))
	for i, c := range cols {
		kinds[i] = headIdentifierColumn
		if c.kind == numericColumn {
			kinds[i] = numericColumn
		}
	}
	return kinds
}

// columnWidths computes how wide each column must be to fit its header and
// every cell in data, the same data-driven approach internal/profile uses
// for its resource-type column rather than a width fixed in advance.
func columnWidths(cols []column, data []row) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len([]rune(c.header))
	}
	for _, r := range data {
		for i, c := range r.cells {
			if i >= len(widths) {
				continue
			}
			if n := len([]rune(c)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	return widths
}

// formatRow pads cells to widths and joins them with two spaces, taking
// each cell's alignment and clip direction from kinds[i] (see columnKind):
// a numeric cell is right-aligned and never clipped, since fitColumnWidths
// reserves its column at full natural width; a tail-distinguished
// identifier is front-clipped so its tail survives, and a head-distinguished
// one is end-clipped so its head does. Both clips are no-ops when the cell
// already fits its column, so they apply unconditionally rather than
// singling out whichever column fitColumnWidths happened to shrink.
//
// The kinds are passed in rather than read off cols because a header row
// and a data row of the same table clip differently: see headerKinds.
func formatRow(cells []string, kinds []columnKind, widths []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		w := widths[i]
		kind := tailIdentifierColumn
		if i < len(kinds) {
			kind = kinds[i]
		}
		if kind == numericColumn {
			parts[i] = fmt.Sprintf("%*s", w, c)
			continue
		}
		parts[i] = fmt.Sprintf("%-*s", w, clipValueForKind(c, w, kind))
	}
	return strings.Join(parts, "  ")
}

// clipWidth truncates s to at most w terminal columns, without marking the
// cut. It is the last line of defence against a line overrunning its pane
// -- column widths are otherwise data-driven and unbounded -- and applies
// to lines that have already been composed rather than to bare values,
// which clip by their kind (clipValueForKind) and do carry a marker.
//
// Width here is display columns, not runes: an ANSI escape sequence
// occupies no columns, so counting its runes would make a full-width styled
// line look over-wide and cut real content off its end. ansi.Truncate
// measures the same way lipgloss.Width does and carries escapes across the
// cut, so a clipped styled line still turns its styling off again.
func clipWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}

// formatMs renders a millisecond duration: whole milliseconds below one
// second, seconds with one decimal place at or above it. Written fresh
// rather than imported from internal/profile, which internal/tui may not
// depend on.
func formatMs(ms uint64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
