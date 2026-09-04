package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// row is one line of the centre pane's list, already formatted into display
// cells.
type row struct {
	// cells holds exactly one formatted cell per column of the column list
	// this row's view is paired with in renderList -- providerRows with
	// providerColumns, typeRows with typeColumns, callRows with
	// callColumns. Every width and kind a cell is rendered against is
	// derived from that same list, so columnWidths and formatRow index the
	// two together without guarding: a builder that drops or adds a cell is
	// a programming error, and an out-of-range panic at the first render
	// says so where a quietly missing column would not.
	cells []string
	// spanIdx is the index into m.log.RPCSpans that this row represents, or
	// -1 when the row is a rollup rather than a single span. The raw log's
	// jump-to-log resolves a row to a span through it, so every row carries
	// one.
	spanIdx int
	// rollup is what the detail pane shows for a row that stands for a
	// GROUP of spans, and is nil for a row that is one span. Every row is
	// exactly one of the two -- a rollup row has a rollup and spanIdx -1, a
	// call row has a spanIdx and no rollup -- which is the invariant
	// renderDetail dispatches on.
	rollup *rollupDetail
}

// rollupDetail is everything the detail pane shows for a rollup row: the
// group's own aggregate, and the slowest single RPC call behind it.
//
// It is recorded by the row builders, which have the group's spans in hand
// already, rather than derived by the detail pane. renderDetail runs on
// every frame, and re-deriving a group from a few thousand spans per
// keystroke is the per-render cost facetNaturalWidth and detailNaturalWidth
// were both hoisted out of the render path to avoid.
type rollupDetail struct {
	// aggregate is the group's own figures, in the order they are shown.
	// The pane truncates from the BOTTOM (see renderDetail), so they come
	// before the slowest call: on a terminal too short for both, what
	// survives is the summary of the row the cursor is actually on.
	aggregate []detailField
	// slowest is the longest RPC-tier call in the group, or nil when the
	// group has none -- a resource type the UI-hook tier saw and the RPC
	// tier never did. The absence is rendered explicitly rather than left
	// as a missing section, which reads as a pane that failed.
	slowest *span.Span
}

// detailField is one labelled value of the detail pane: the label, the
// already-formatted value, and what KIND of value it is -- which is what
// decides the end it clips from, through the same clipValueForKind the
// tables and the facet pane route their values through. The layout of the
// line itself is layout.go's (see detailFieldLines), so a builder here
// records what to show and never how wide it is.
type detailField struct {
	label string
	value string
	kind  columnKind
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
// orders them. No row is a single span, so every spanIdx is -1 and every
// row carries a rollupDetail instead.
//
// The detail pane's aggregate adds two facts the table has no column for:
// how many distinct resource types and RPC methods the provider's calls
// span. They are the reason to look at the pane over a providers row at all
// -- the row's own four figures are already on screen beside it.
func providerRows(rpcSpans []span.Span) []row {
	buckets := model.RollupBy(rpcSpans, func(s span.Span) string { return s.Provider })
	groups := groupRPCSpans(rpcSpans, func(s span.Span) string { return s.Provider })
	rows := make([]row, len(buckets))
	for i, b := range buckets {
		g := groups[b.Key]
		rows[i] = row{
			cells: []string{
				b.Key,
				formatMs(b.TotalMs),
				strconv.Itoa(b.Count),
				formatMs(uint64(b.MaxMs)),
			},
			spanIdx: -1,
			rollup: &rollupDetail{
				aggregate: []detailField{
					{label: "Prov", value: b.Key, kind: tailIdentifierColumn},
					{label: "Calls", value: strconv.Itoa(b.Count), kind: numericColumn},
					{label: "Total", value: formatMs(b.TotalMs), kind: numericColumn},
					{label: "Max", value: formatMs(uint64(b.MaxMs)), kind: numericColumn},
					{label: "Types", value: strconv.Itoa(g.resourceTypes), kind: numericColumn},
					{label: "RPCs", value: strconv.Itoa(g.rpcs), kind: numericColumn},
				},
				slowest: g.slowest,
			},
		}
	}
	return rows
}

// typeRows joins the RPC and UI-hook tiers by resource type, as
// model.JoinByResourceType already orders them. No row is a single span --
// each is a rollup over possibly many of both -- so every spanIdx is -1 and
// every row carries a rollupDetail instead.
//
// The row spans both tiers, so its aggregate does too, under the labels
// typeColumns heads the table with: the pane and the table then report the
// same figure by the same name, and can be read against each other. Only
// the RPC tier has a single slowest CALL to name -- a UI-hook span times a
// whole resource, not a call -- and a type may have no RPC-tier span at
// all, which groupRPCSpans reports as a nil slowest.
func typeRows(rpcSpans, uiSpans []span.Span) []row {
	joined := model.JoinByResourceType(rpcSpans, uiSpans)
	groups := groupRPCSpans(rpcSpans, func(s span.Span) string { return s.ResourceType })
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
			rollup: &rollupDetail{
				aggregate: []detailField{
					{label: "Type", value: r.ResourceType, kind: tailIdentifierColumn},
					{label: "UI res.", value: strconv.Itoa(r.UIResources), kind: numericColumn},
					{label: "UI total", value: formatMs(r.UITotalMs), kind: numericColumn},
					{label: "RPC calls", value: strconv.Itoa(r.RPCCalls), kind: numericColumn},
					{label: "RPC total", value: formatMs(r.RPCTotalMs), kind: numericColumn},
					{label: "RPC max", value: formatMs(uint64(r.RPCMaxMs)), kind: numericColumn},
				},
				slowest: groups[model.FacetKey(r.ResourceType)].slowestOf(),
			},
		}
	}
	return rows
}

// rpcGroup is what one group of RPC spans holds that a model rollup does
// not carry: the slowest call in it, and how many distinct resource types
// and RPC methods it spans.
type rpcGroup struct {
	slowest       *span.Span
	resourceTypes int
	rpcs          int
}

// slowestOf is the group's slowest call, and nil for a group that does not
// exist at all. JoinByResourceType emits a row for every resource type
// EITHER tier saw, so a UI-tier-only type has no entry here, and the caller
// wants the same nil either way rather than a map lookup guarded at each
// site.
func (g *rpcGroup) slowestOf() *span.Span {
	if g == nil {
		return nil
	}
	return g.slowest
}

// groupRPCSpans collects each group's rpcGroup in a single pass over spans,
// keyed the way model.RollupBy and model.JoinByResourceType key their own
// groups (model.FacetKey), so a group found here is the same group the row
// beside it was rolled up from.
//
// It runs once per rows() build -- which is once per view or filter change,
// memoised on the model thereafter -- not once per frame.
func groupRPCSpans(spans []span.Span, key func(span.Span) string) map[string]*rpcGroup {
	groups := make(map[string]*rpcGroup)
	seenTypes, seenRPCs := make(map[string]map[string]bool), make(map[string]map[string]bool)
	seen := func(m map[string]map[string]bool, k, v string) bool {
		if m[k] == nil {
			m[k] = make(map[string]bool)
		}
		if m[k][v] {
			return true
		}
		m[k][v] = true
		return false
	}
	for i, s := range spans {
		k := model.FacetKey(key(s))
		g := groups[k]
		if g == nil {
			g = &rpcGroup{}
			groups[k] = g
		}
		if g.slowest == nil || s.DurationMs > g.slowest.DurationMs {
			g.slowest = &spans[i]
		}
		if !seen(seenTypes, k, model.FacetKey(s.ResourceType)) {
			g.resourceTypes++
		}
		if !seen(seenRPCs, k, model.FacetKey(s.RPC)) {
			g.rpcs++
		}
	}
	return groups
}

// callRows ranks the RPC spans matching f by duration descending, ties
// broken by RPC name so the ordering is total. Each row's spanIdx indexes
// into rpcSpans itself, never into a filtered subset, so jump-to-log keeps
// landing on the right span even while a filter narrows the list.
// It sorts a slice of indices rather than the spans themselves: m.log.RPCSpans
// must not be mutated, since every other view reads it too and a sort here
// would reorder theirs.
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

// noMatchNote is what a pane says when the active filter has left it with
// nothing to show. Without it an empty pane is byte-identical to one caused
// by a parse failure or by opening the wrong file, and a reader who cannot
// tell those apart draws a wrong conclusion from a tool whose whole job is
// reporting numbers accurately. It names Esc because Esc is what clears the
// filter, and it is short enough (42 columns) to survive the narrowest
// centre pane any supported width produces.
const noMatchNote = "nothing matches the filter -- Esc clears it"

// noRowsNote is the same honesty for a view that has no rows to show with no
// filter to blame: the providers and calls views of a log carrying UI-hook
// spans only, say, where the answer really does live in another view.
const noRowsNote = "this view has no rows for this log"

// renderList renders the current view's rows as a table at most w columns
// wide and h lines tall, with the row at Selected() highlighted and the
// window scrolled just far enough to keep it visible.
//
// A log with no spans of either tier gets capture guidance in place of the
// table: an empty table there is the most likely FIRST-RUN result -- a
// capture taken without TF_LOG_PROVIDER=TRACE -- and four empty panes say
// nothing about how to take a usable one. --diagnose already answers this
// question, so the guidance is its wording rather than a second phrasing of
// the same advice.
func (m *Model) renderList(w, h int) string {
	if len(m.log.RPCSpans) == 0 && len(m.log.UISpans) == 0 {
		return clipLines(clipEachWidth(captureGuidance, w), h)
	}
	empty := noRowsNote
	if m.filterActive() {
		empty = noMatchNote
	}
	// Only the columns and the preamble differ between the table views;
	// everything else renderTable needs is the same for all of them, so the
	// switch selects those two and the call itself is made once.
	var cols []column
	var preamble []string
	switch m.view {
	case ViewProviders:
		cols = providerColumns
	case ViewTypes:
		cols = typeColumns
		preamble = typesPreamble(m.filter().SpansMatching(m.log.UISpans))
	case ViewCalls:
		cols = callColumns
	default:
		return ""
	}
	return renderTable(preamble, cols, m.rows(), empty, m.selected, m.pane == PaneList, w, h)
}

// captureGuidance is what the centre pane shows for a log with no spans at
// all: what such a log is missing, how to capture one that is not, and how
// to check this file's structure. Every sentence is internal/diagnose's --
// the EXTRACTION section's "nothing to profile" line, writeRPCCaptureHint's
// two-gates explanation, and the HCP capture instruction from tfli's own
// usage text -- rewrapped to 40 columns, which is narrower than the centre
// pane at any supported terminal width.
var captureGuidance = []string{
	"This log contains no provider RPC",
	"entries, so there is nothing to profile.",
	"",
	"Provider RPC entries are emitted only at",
	"TRACE, so debug logging alone will not",
	"produce them. Two levels gate them: what",
	"the provider writes, and what Terraform",
	"keeps. Set both TF_LOG_PROVIDER=TRACE",
	"and TF_LOG_SDK_PROTO=TRACE, or raise",
	"everything with TF_LOG=TRACE.",
	"",
	"For an HCP Terraform workspace, enable",
	"debug logging on a run and download its",
	"raw log.",
	"",
	"Run tfli --diagnose on this file to",
	"check its structure.",
}

// clipEachWidth clips every line of a block to w columns and joins them, the
// shape a pane's content takes.
func clipEachWidth(lines []string, w int) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = clipWidth(line, w)
	}
	return strings.Join(out, "\n")
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
// is still recognisable, but a duration or count that is missing, or shown
// half, tells the reader nothing. So every numeric (right-aligned) column
// keeps its full natural width unconditionally, and fitColumnWidths
// distributes whatever width is left among
// the text columns (every one of this package's tables carries only
// identifiers in its text columns: provider addresses, resource types and
// RPC names). A text column narrower than its natural width is clipped by
// formatRow, not dropped, from whichever end its columnKind says carries
// the less distinguishing part of the value: two rows whose providers or
// RPCs differ must still render differently, or the ranking they sit in is
// unreadable. clipWidth remains a safety net beneath all of this for the
// pathological case where even the numeric columns alone exceed w.
//
// selected is drawn as the cursor bar, in the style focused says (see
// cursorBar), and the data rows are windowed so it stays visible within h
// lines total: the preamble and header are never scrolled,
// only the data rows beneath them are.
//
// Every row, cursor or not, is fit and clipped to the full w: the
// escape sequences cursorBar wraps the selected row in occupy no
// terminal columns, so the highlighted row has exactly as many columns of
// content as its neighbours and column widths stay shared across the whole
// table.
//
// The header is clipped as prose, not as an identifier: a column header
// ("resource type") is told apart by its head even where the column's
// values are told apart by their tails, so it end-clips while they
// front-clip. See headerKinds.
//
// emptyNote is rendered in place of the data rows when there are none. A
// table of a preamble and a header with nothing beneath it reads as a
// rendering that failed rather than as a filter that matched nothing, and
// those are the two situations a reader most needs told apart.
func renderTable(preamble []string, cols []column, data []row, emptyNote string, selected int, focused bool, w, h int) string {
	widths := fitColumnWidths(cols, columnWidths(cols, data), w)
	kinds := columnKinds(cols)

	lines := make([]string, 0, len(preamble)+1+len(data))
	for _, p := range preamble {
		lines = append(lines, clipWidth(p, w))
	}
	lines = append(lines, clipWidth(formatRow(headerCells(cols), headerKinds(cols), widths), w))

	if len(data) == 0 {
		lines = append(lines, clipWidth(emptyNote, w))
	}
	dataH := h - len(lines)
	top, visible := scrollWindow(selected, len(data), dataH)
	for i := top; i < top+visible; i++ {
		line := clipWidth(formatRow(data[i].cells, kinds, widths), w)
		if i == selected {
			line = cursorBar(line, w, focused)
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
// not need goes back into the pool for the columns still competing.
//
// Water-filling is what lets one rule serve both a table with a single text
// column (providers, types) and one with three (the calls view's RPC,
// resource type and provider). Flexing only the widest column would leave
// the rest reserved at their full natural width, so the row could still
// exceed w -- and the overflow would then land on whichever column the
// final clipWidth happened to reach, from whichever end it happened to cut.
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
			// what's left evenly, one extra column to each of the first
			// remaining%len(pool) of them so the total assigned is exactly
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

// columnWidths computes how many display columns each column must be given
// to fit its header and every cell in data, the same data-driven approach
// internal/profile uses for its resource-type column rather than a width
// fixed in advance.
func columnWidths(cols []column, data []row) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = lipgloss.Width(c.header)
	}
	for _, r := range data {
		for i, c := range r.cells {
			widths[i] = max(widths[i], lipgloss.Width(c))
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
		w, kind := widths[i], kinds[i]
		if kind == numericColumn {
			// A numeric cell is formatMs or strconv output: ASCII, where a
			// rune is a column, so fmt's own rune-counted width is the same
			// measure padRight applies to the text cells beside it.
			parts[i] = fmt.Sprintf("%*s", w, c)
			continue
		}
		parts[i] = padRight(clipValueForKind(c, w, kind), w)
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
