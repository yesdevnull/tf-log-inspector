package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

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

// column describes one column of a rendered list table.
type column struct {
	header string
	right  bool // numeric columns are right-aligned; text columns are left-aligned
}

var providerColumns = []column{
	{header: "provider"},
	{header: "total", right: true},
	{header: "calls", right: true},
	{header: "max", right: true},
}

var typeColumns = []column{
	{header: "resource type"},
	{header: "UI res.", right: true},
	{header: "UI total", right: true},
	{header: "RPC calls", right: true},
	{header: "RPC total", right: true},
	{header: "RPC max", right: true},
}

var callColumns = []column{
	{header: "duration", right: true},
	{header: "RPC"},
	{header: "resource type"},
	{header: "provider"},
}

// rows returns the current view's rows, restricted to the active facet
// filter. ViewRawLog is not a rollup and has no rows of its own -- Task 5
// renders it directly from m.log.Entries -- so it returns nil here.
//
// The result is cached on m (see invalidateRows) so repeated calls between
// filter or view changes -- moveSelection calling RowCount() on every
// arrow-key press, or a render calling rows() again after RowCount already
// did -- reuse it rather than redoing a full RollupBy/JoinByResourceType/sort
// each time.
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
// wide and h lines tall.
func (m Model) renderList(w, h int) string {
	switch m.view {
	case ViewProviders:
		return renderTable(nil, providerColumns, m.rows(), w, h)
	case ViewTypes:
		preamble := typesPreamble(m.filter().SpansMatching(m.log.UISpans))
		return renderTable(preamble, typeColumns, m.rows(), w, h)
	case ViewCalls:
		return renderTable(nil, callColumns, m.rows(), w, h)
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
// Every line, including preamble, is clipped to w runes so a table wider
// than its pane can never wrap into unreadable wreckage -- real column
// degradation is Task 6's job, this only guarantees the width bound. The
// result is capped at h lines total.
func renderTable(preamble []string, cols []column, data []row, w, h int) string {
	widths := columnWidths(cols, data)

	lines := make([]string, 0, len(preamble)+1+len(data))
	for _, p := range preamble {
		lines = append(lines, clipWidth(p, w))
	}
	lines = append(lines, clipWidth(formatRow(headerCells(cols), cols, widths), w))
	for _, r := range data {
		lines = append(lines, clipWidth(formatRow(r.cells, cols, widths), w))
	}

	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func headerCells(cols []column) []string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = c.header
	}
	return cells
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

// formatRow pads cells to widths and joins them with two spaces, aligning
// each column per cols' right flag.
func formatRow(cells []string, cols []column, widths []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		w := widths[i]
		if i < len(cols) && cols[i].right {
			parts[i] = fmt.Sprintf("%*s", w, c)
		} else {
			parts[i] = fmt.Sprintf("%-*s", w, c)
		}
	}
	return strings.Join(parts, "  ")
}

// clipWidth truncates s to at most w runes. It is the last line of defence
// against a line overrunning its pane -- column widths are otherwise
// data-driven and unbounded -- so real degradation (Task 6) can be added
// without this guarantee ever regressing.
func clipWidth(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	return string(r[:w])
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
