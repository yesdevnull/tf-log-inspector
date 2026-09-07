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
	// numeric holds the NUMBER behind each of cells, one per column, and is
	// meaningful only where that column's kind is numericColumn -- it is
	// zero everywhere else and nothing reads it there.
	//
	// It exists because cells are formatted for a reader and sorting them as
	// text gives the wrong order: formatMs renders 2000ms as "2.0s" and
	// 500ms as "500ms", and "2.0s" sorts below "500ms" on every rule a
	// string comparison has. The builders hold the raw figure already, so
	// they record it here rather than leaving the sort to parse a rendered
	// duration back into one.
	numeric []uint64
	// spanIdx is the index into m.log.RPCSpans that this row represents, or
	// noSpanIdx when the row is a rollup rather than a single span. The raw
	// log's jump-to-log resolves a row to a span through it, so every row
	// carries one. Ask isCall before indexing with it.
	spanIdx int
	// rollup is what the detail pane shows for a row that stands for a
	// GROUP of spans, and is nil for a row that is one span. Every row is
	// exactly one of the two -- a rollup row has a rollup and spanIdx -1, a
	// call row has a spanIdx and no rollup.
	//
	// Rows are built through callRow and rollupRow rather than as literals,
	// so that invariant is established by construction instead of being
	// restated at each site; isCall is the one question anything asks of it.
	rollup *rollupDetail
}

// callRow builds the row standing for ONE span: its display cells, and the
// index into m.log.RPCSpans of the span it is.
func callRow(cells []string, numeric []uint64, spanIdx int) row {
	return row{cells: cells, numeric: numeric, spanIdx: spanIdx}
}

// rollupRow builds the row standing for a GROUP of spans: its display cells,
// and what the detail pane shows for the group.
//
// The sentinel spanIdx is DERIVED here rather than asked of each caller. A
// rollup row resolves to no single span, and a caller that forgot the
// sentinel would leave a row indexing whichever span happens to sit at
// index 0 -- rendering that span's RPC, provider and duration as the
// selected GROUP's own figures.
func rollupRow(cells []string, numeric []uint64, d *rollupDetail) row {
	return row{cells: cells, numeric: numeric, spanIdx: noSpanIdx, rollup: d}
}

// noSpanIdx is the spanIdx of a row that stands for no single span. It is
// deliberately outside the range of any slice, so a row carrying it can only
// be used to index m.log.RPCSpans by a caller that failed to ask isCall
// first -- and that failure is a panic at the first frame rather than a
// plausible wrong answer.
const noSpanIdx = -1

// isCall reports whether the row stands for one span, and so whether
// spanIdx may be used to index m.log.RPCSpans.
//
// It is the single predicate for the question, asked by the detail pane
// (which span's fields to show) and by Enter (which span to jump to). Those
// two once asked it of different fields -- spanIdx against rollup -- and
// two questions that must agree, asked of two fields, are two questions
// that can disagree.
func (r row) isCall() bool {
	return r.spanIdx >= 0
}

// spanForRow resolves a row to the span it stands for, and reports whether
// there is one. A rollup row stands for no single span and gets false.
//
// A call row's index is checked against the slice it indexes as well --
// the same both-ends check jumpToSpan makes of its own argument, since
// Span.Entry taught this package that an index carried in a struct is
// worth revalidating at the point of use. renderDetail runs on every
// frame, so an index out of range there is a panic inside the alt screen,
// which leaves the user's terminal wrecked; a pane that says it has
// nothing to describe costs them one pane.
func (m *Model) spanForRow(r row) (span.Span, bool) {
	if !r.isCall() || r.spanIdx >= len(m.log.RPCSpans) {
		return span.Span{}, false
	}
	return m.log.RPCSpans[r.spanIdx], true
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
	// It is the pane's FIRST section, so a pane too short for the whole of
	// a rollup's detail keeps this and drops the slowest call beneath it
	// (see fitPaneSections): what survives is the summary of the row the
	// cursor is actually on.
	aggregate []detailField
	// slowest is the longest RPC-tier call in the group, or nil when the
	// group has none -- a resource type the UI-hook tier saw and the RPC
	// tier never did. The absence is rendered explicitly rather than left
	// as a missing line, which reads as a pane that failed.
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

// tableBinding is the table one view draws: its columns, and the index of
// the column its row builder ALREADY ranks by.
//
// The two are held together for the reason viewBinding gives for its own
// three fields. defaultCol is not a preference -- it is a statement about
// what providerRows, typeRows and callRows produce, and stating it beside
// the columns it indexes is what stops it drifting into naming a column the
// builder does not rank by. Two tests hold that claim true:
// TestTheDefaultSortServesTheBuildersOrderRatherThanReSortingIt and
// TestEveryDefaultSortColumnNamesTheOrderItsBuilderProduces.
type tableBinding struct {
	cols       []column
	defaultCol int
}

// tables is the single source of truth for which columns a view draws and
// which of them it arrives sorted by. renderList selects its columns from
// here, the sort cycles through them, and the header marker names one of
// them, so a view cannot draw one set of columns while the sort cycles
// another.
//
// A view absent from this map has no table: the timeline draws lanes and the
// raw log draws entries, and neither has a column for a sort to reorder. The
// absence is what makes 's' inert there, rather than a condition spelled out
// at the key handler.
var tables = map[View]tableBinding{
	// RollupBy ranks buckets by TotalMs descending, breaking ties by
	// provider name.
	ViewProviders: {cols: providerColumns, defaultCol: 1},
	// model.JoinByResourceType ranks by UITotalMs descending, breaking ties
	// by RPCTotalMs and then by name.
	ViewTypes: {cols: typeColumns, defaultCol: 2},
	// callRows sorts by rankedBefore: duration descending, ties by RPC name.
	ViewCalls: {cols: callColumns, defaultCol: 0},
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
		r = typeRows(f.SpansMatching(m.log.RPCSpans), m.uiFilter().SpansMatching(m.log.UISpans))
	case ViewCalls:
		r = callRows(m.log.RPCSpans, f)
	case ViewTimeline:
		// The timeline renders from m.timelineSpans() and model.PackLanes,
		// not from rows(): a lane is neither a rollup of many spans nor one
		// span, so forcing it into row would break the isCall invariant
		// renderDetail dispatches on. nil here is the same deliberate
		// omission ViewRawLog's case is.
		r = nil
	case ViewRawLog:
		// The raw log renders straight from m.log.Entries and has no rows
		// of its own, so nil is its answer rather than an omission.
		r = nil
	default:
		panic(unhandledView(m.view))
	}
	// The sort runs ONLY where the user has moved it off the column the
	// builder already ranks by. On the default it does not run at all, and
	// the builder's own order -- including its own tie-break, which a
	// generic sort by one column knows nothing about -- is what reaches the
	// table. See tableBinding.
	if t, ok := tables[m.view]; ok && m.sortCol[m.view] != t.defaultCol {
		sortRows(t.cols, r, m.sortCol[m.view])
	}
	m.rowsCache = r
	m.rowsCached = true
	return r
}

// unhandledView is the message for a View that reached a switch with no case
// for it. That can only happen by adding a value to the View enum and not to
// the switches that project it -- Update reaches views through viewKeys,
// which holds only bound keys -- so it is a programming error, and this
// package treats those loudly.
//
// Degrading instead is what the dead pane was: rows() returning nil and
// renderList returning "" compose a centre pane titled with the new view and
// holding nothing, beside a detail pane saying nothing is selected and a
// footer advertising the key that got there. Nothing on screen, and nothing
// in the test suite, says the view was never built -- and view 3 (resource
// addresses) is specified and waiting to be added.
func unhandledView(v View) string {
	return fmt.Sprintf("tui: view %d has no rows or columns; add it to rows() and renderList()", v)
}

// selectedRow is the row the list cursor is on, and reports whether there is
// one. A view with no rows, or a selection outside the rows there are, gets
// false -- the single bounds check for the question, so the detail pane,
// the Enter handler and the footer's open hint cannot disagree about which
// row is selected or whether one is.
func (m *Model) selectedRow() (row, bool) {
	rows := m.rows()
	if m.selected < 0 || m.selected >= len(rows) {
		return row{}, false
	}
	return rows[m.selected], true
}

// selectedRowOpens reports whether Enter has anything to open: a call row's
// own span in the table views, or the timeline's selected span, which has no
// row at all. It is what the footer's open hint is shown on, built on
// jumpTarget, so the hint and the Enter handler cannot come to disagree
// about what there is to open.
func (m *Model) selectedRowOpens() bool {
	_, _, ok := m.jumpTarget()
	return ok
}

// jumpTarget resolves what Enter would jump to right now: the span slice and
// the index within it jumpToSpan should be given, and whether there is one
// at all. It is the single predicate both selectedRowOpens (the footer's
// open hint) and the Enter handler ask, generalising row.isCall -- which
// answers the question for one table row -- over the timeline, which has no
// rows of its own and resolves through selectedTimelineSpan instead.
//
// Like selectedRow, it does not consider which pane has focus: the footer's
// hint is shown for whatever the cursor is on regardless of which pane the
// keyboard is currently in (see actionKeys), and the Enter handler applies
// its own pane check before acting on this.
func (m *Model) jumpTarget() (spans []span.Span, idx int, ok bool) {
	if m.view == ViewTimeline {
		idx, ok := m.selectedTimelineSpan()
		if !ok {
			return nil, 0, false
		}
		_, spans := m.timelineSpans()
		return spans, idx, true
	}
	r, ok := m.selectedRow()
	if !ok || !r.isCall() {
		return nil, 0, false
	}
	return m.log.RPCSpans, r.spanIdx, true
}

// providerRows ranks providers by total RPC time, as model.RollupBy already
// orders them. No row is a single span, so every spanIdx is -1 and every
// row carries a rollupDetail instead.
//
// The aggregate repeats the table's four figures IN THE TABLE'S OWN ORDER
// (provider, total, calls, max), the way the types builder repeats its
// table's, so a reader moving between the row and the pane beside it is
// reading the same sequence twice rather than matching label to label. It
// then adds the two facts the table has no column for: how many distinct
// resource types and RPC methods the provider's calls span, which are the
// reason to look at the pane over a providers row at all.
func providerRows(rpcSpans []span.Span) []row {
	buckets := model.RollupBy(rpcSpans, func(s span.Span) string { return s.Provider })
	groups := groupRPCSpans(rpcSpans, func(s span.Span) string { return s.Provider })
	rows := make([]row, len(buckets))
	for i, b := range buckets {
		g := groups[b.Key]
		rows[i] = rollupRow(
			[]string{
				b.Key,
				formatMs(b.TotalMs),
				strconv.Itoa(b.Count),
				formatMs(uint64(b.MaxMs)),
			},
			[]uint64{0, b.TotalMs, uint64(b.Count), uint64(b.MaxMs)},
			&rollupDetail{
				aggregate: []detailField{
					{label: "Prov", value: b.Key, kind: tailIdentifierColumn},
					{label: "Total", value: formatMs(b.TotalMs), kind: numericColumn},
					{label: "Calls", value: strconv.Itoa(b.Count), kind: numericColumn},
					{label: "Max", value: formatMs(uint64(b.MaxMs)), kind: numericColumn},
					{label: "Types", value: strconv.Itoa(g.resourceTypes), kind: numericColumn},
					{label: "RPCs", value: strconv.Itoa(g.rpcs), kind: numericColumn},
				},
				slowest: g.slowest,
			},
		)
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
		rows[i] = rollupRow(
			[]string{
				r.ResourceType,
				strconv.Itoa(r.UIResources),
				formatMs(r.UITotalMs),
				strconv.Itoa(r.RPCCalls),
				formatMs(r.RPCTotalMs),
				formatMs(uint64(r.RPCMaxMs)),
			},
			[]uint64{0, uint64(r.UIResources), r.UITotalMs, uint64(r.RPCCalls), r.RPCTotalMs, uint64(r.RPCMaxMs)},
			&rollupDetail{
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
		)
	}
	return rows
}

// rankedBefore reports whether a comes before b when RPC spans are ranked
// by how long they took: longer first, ties broken by RPC name ascending so
// the ordering is total rather than left to whichever span the log happened
// to record first.
//
// It is the one rule for the question, shared by the calls table (which
// sorts by it) and by each rollup group's slowest call (which is its
// maximum). Two rules would let one screen answer "which call was slowest"
// two ways: in a group whose two longest calls are equal, the detail pane
// would name the earlier-logged one while the calls table ranked the other
// above it.
//
// It is NOT a total order: two calls of the same RPC name at the same
// duration are equal under it, and on a real capture that is the ordinary
// case rather than a corner. What settles those is the stability of the
// sort applied over it, not this rule -- see callRows.
func rankedBefore(a, b span.Span) bool {
	if a.DurationMs != b.DurationMs {
		return a.DurationMs > b.DurationMs
	}
	return a.RPC < b.RPC
}

// sortRows reorders data by column col of cols, in the direction that
// column's KIND implies: a numeric column descending, because biggest-first
// is what every ranked view in this tool means by an order, and an
// identifier column ascending, because alphabetical is how a reader scans a
// list of names. The direction is therefore not a second thing the user
// chooses, and there is no key to reverse it.
//
// Ties break on column 0, in the direction THAT column's kind implies by the
// same rule: ascending in the two rollup tables, whose first column is an
// identifier, and descending in the calls view, whose first column is
// duration -- so sorting calls by RPC name puts the slowest call of each
// name first. Sorting BY column 0 has no tie-break to fall to, and rows no
// tie-break can separate hold the order they arrived in: the sort is STABLE,
// so two rows it genuinely cannot tell apart do not swap places from one
// keystroke to the next.
func sortRows(cols []column, data []row, col int) {
	sort.SliceStable(data, func(i, j int) bool {
		if less, decided := compareCell(cols[col].kind, data[i], data[j], col); decided {
			return less
		}
		if col == 0 {
			return false
		}
		less, _ := compareCell(cols[0].kind, data[i], data[j], 0)
		return less
	})
}

// compareCell reports whether a comes before b at column col, and whether
// that column tells them apart at all. The second return is what lets
// sortRows fall through to its tie-break rather than reading "not before"
// as "after".
//
// A numeric column compares the NUMBERS behind the cells, never the cells:
// see row.numeric for why the rendered text sorts wrongly.
func compareCell(kind columnKind, a, b row, col int) (less, decided bool) {
	if kind == numericColumn {
		if a.numeric[col] != b.numeric[col] {
			return a.numeric[col] > b.numeric[col], true
		}
		return false, false
	}
	if a.cells[col] != b.cells[col] {
		return a.cells[col] < b.cells[col], true
	}
	return false, false
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
		if g.slowest == nil || rankedBefore(s, *g.slowest) {
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

// callRows ranks the RPC spans matching f by rankedBefore -- duration
// descending, ties broken by RPC name -- so the ordering is total. Each
// row's spanIdx indexes
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
	// STABLE, because rankedBefore is not a total order: two calls of the
	// same RPC name at the same duration are equal under it, which is the
	// ordinary case on a real capture. Sorted unstably they arrive in
	// whatever order the sort's internals produce, and that order changes
	// when the SET changes -- so toggling a facet reshuffles tied rows the
	// filter did not touch, with nothing on screen accounting for the move.
	// Stable, they hold the order the log recorded them in.
	sort.SliceStable(idx, func(i, j int) bool {
		return rankedBefore(rpcSpans[idx[i]], rpcSpans[idx[j]])
	})

	rows := make([]row, len(idx))
	for i, si := range idx {
		s := rpcSpans[si]
		rows[i] = callRow([]string{
			formatMs(uint64(s.DurationMs)),
			s.RPC,
			s.ResourceType,
			s.Provider,
		}, []uint64{uint64(s.DurationMs), 0, 0, 0}, si)
	}
	return rows
}

// noMatchNote is what a pane says when the active filter has left it with
// nothing to show. Without it an empty pane is byte-identical to one caused
// by a parse failure or by opening the wrong file, and a reader who cannot
// tell those apart draws a wrong conclusion from a tool whose whole job is
// reporting numbers accurately. It names Esc because Esc is what clears the
// filter, and it is short enough (43 columns) to survive the narrowest
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
		return fitCaptureGuidance(w, h)
	}
	empty := noRowsNote
	if m.filterActive() {
		empty = noMatchNote
	}
	// The columns come from tables, which the sort cycle and the header
	// marker read as well, so the table DRAWN and the table sorted cannot
	// come to be two different tables. Only the preamble is left to select
	// here, and everything else renderTable needs is the same for every
	// table view, so the call itself is made once.
	t, ok := tables[m.view]
	if !ok {
		// ViewRawLog and ViewTimeline never reach here -- renderCentre
		// routes them to renderRawLog and renderTimeline respectively -- so
		// anything landing here is a view with no table of its own.
		// See unhandledView.
		panic(unhandledView(m.view))
	}
	var preamble []string
	if m.view == ViewTypes {
		preamble = typesPreamble(m.uiFilter().SpansMatching(m.log.UISpans))
	}
	return renderTable(preamble, t.cols, m.sortCol[m.view], m.rows(), empty, m.selected, m.pane == PaneList, w, h)
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

// shortCaptureGuidance is the same advice in one sentence, for a pane with
// no room for the full text. It is a rewrite rather than the first lines of
// captureGuidance, the same answer shortLoggingCaveat is to a frame short of
// HEIGHT: a block cut off partway reads as a rendering fault, and what the
// cut takes here is the actionable half -- "This log contains no provider
// RPC" is a finished-looking sentence with the two variables to set gone.
//
// Both variables are named because both gate the entries: the provider must
// write them and Terraform must keep them. It is wrapped to the pane rather
// than pre-wrapped, since a fixed wrap would spend lines a short pane does
// not have (see wrapToWidth).
//
// It opens on the full guidance's own first sentence, complete, so that the
// one height too short even for the mark -- a pane of one line -- still
// leaves a finished sentence naming what this log is missing, with only the
// remedy cut.
const shortCaptureGuidance = "This log contains no provider RPC entries. Set TF_LOG_PROVIDER=TRACE and TF_LOG_SDK_PROTO=TRACE, then re-run."

// fitCaptureGuidance is the capture guidance for a pane w columns wide and h
// lines tall: the full text where it fits, one sentence where it does not,
// and a marked cut where even that does not.
//
// The mark is detailCutMark, the same ellipsis the detail pane marks its own
// height cut with, so the two cuts tell a reader the same amount about
// themselves. A pane of one line has no room for it, the single unmarked
// case fitPaneSections has for the same reason.
func fitCaptureGuidance(w, h int) string {
	if h <= 0 {
		return ""
	}
	lines := captureGuidance
	if h < len(lines) {
		lines = wrapToWidth(shortCaptureGuidance, w)
	}
	if len(lines) > h {
		// Copied rather than resliced: captureGuidance is a package-level
		// block every pane shares, and the mark is written over the last
		// line the pane has room for.
		cut := make([]string, h)
		copy(cut, lines)
		if h > 1 {
			cut[h-1] = detailCutMark
		}
		lines = cut
	}
	return clipEachWidth(lines, w)
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

// wrapToWidth greedily breaks one line of prose into lines of at most w
// display columns, splitting on spaces. It exists for the blocks that
// COMPETE for pane height with the content they annotate: a caveat
// pre-wrapped to a fixed narrow width spends the same number of lines in a
// 74-column pane as in a 44-column one, and every line it spends there is a
// line of the thing it was explaining (see clampedStartNote, and
// captureGuidance for the opposite case, where a fixed wrap is the right
// answer).
//
// Every returned line is guaranteed to fit w, so a caller can append the
// result to a pane without a further clip. A single word wider than w --
// which no prose in this package has, but which a narrow enough pane
// manufactures out of any word -- is clipped with clipValueEnd rather than
// left to overflow, so the cut carries a marker instead of being made
// silently by the pane's own last-resort clip.
//
// Width is display columns via lipgloss.Width throughout, the measure every
// other width in this package uses; a rune count would wrap a line of
// double-width characters to twice the pane.
func wrapToWidth(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
			line += " " + word
		default:
			lines = append(lines, clipValueEnd(line, w))
			line = word
		}
	}
	if line != "" {
		lines = append(lines, clipValueEnd(line, w))
	}
	return lines
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
func renderTable(preamble []string, cols []column, sortCol int, data []row, emptyNote string, selected int, focused bool, w, h int) string {
	// The headers are built ONCE and then both measured and drawn, so the
	// width a sorted column is reserved and the marked header rendered into
	// it are the same string. Deriving them twice is how a marker comes to
	// be drawn a column wider than the space measured for it.
	headers := headerCells(cols, sortCol)
	widths := fitColumnWidths(cols, columnWidths(headers, data), w)
	kinds := columnKinds(cols)

	lines := make([]string, 0, len(preamble)+1+len(data))
	for _, p := range preamble {
		lines = append(lines, clipWidth(p, w))
	}
	lines = append(lines, clipWidth(formatHeaderRow(headers, headerKinds(cols), widths, sortCol), w))

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

func headerCells(cols []column, sortCol int) []string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = c.header
		if i == sortCol {
			cells[i] += sortMark(c.kind)
		}
	}
	return cells
}

// sortMark is the glyph the sorted column's header wears, naming which way
// that column is ordered. It is derived from the column's kind rather than
// stored, for the same reason sortRows takes its direction from there: the
// marker and the order it describes cannot then disagree.
func sortMark(k columnKind) string {
	if k == numericColumn {
		return sortDescMark
	}
	return sortAscMark
}

// The sort markers. A sort nothing on screen accounts for is a keystroke
// that silently reorders the table, so the marker is drawn from the first
// frame: a view's default ranking is a sort too.
//
// The glyph is appended with no separating space, so a sorted column costs
// one display column rather than two -- fitColumnWidths reserves a numeric
// column at its full natural width, and every column reserved wider is a
// column taken off the identifier columns that share what remains.
const (
	sortDescMark = "▾"
	sortAscMark  = "▴"
)

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
//
// It measures the headers AS RENDERED -- the strings headerCells produced,
// sort marker and all -- rather than re-deriving them from cols. A column
// whose marker went unmeasured is reserved one column short of the header
// drawn into it.
func columnWidths(headers []string, data []row) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = lipgloss.Width(h)
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
		parts[i] = formatCell(c, kinds[i], widths[i])
	}
	return strings.Join(parts, "  ")
}

// formatHeaderRow formats a table's header row, marking the column the
// table is sorted by. It exists beside formatRow rather than as a flag on it
// because only a header carries styling: a data row may be redrawn as the
// cursor bar, and cursorBar's reverse video is turned off again by the first
// reset inside whatever it is given (see the theme), so a styled data cell
// would end the highlight partway along the selected row.
//
// Each cell is styled AFTER formatCell has clipped and padded it, so the
// escape sequences arrive on a string already the right width and the
// column arithmetic never sees them. The style wraps the whole cell, which
// is what keeps a header's name and its sort marker contiguous in the
// output.
func formatHeaderRow(headers []string, kinds []columnKind, widths []int, sortCol int) string {
	parts := make([]string, len(headers))
	for i, h := range headers {
		style := styles.columnHeader
		if i == sortCol {
			style = styles.sortedColumn
		}
		parts[i] = style.Render(formatCell(h, kinds[i], widths[i]))
	}
	return strings.Join(parts, "  ")
}

// formatCell clips and pads one cell to exactly w display columns, by the
// rule its kind names: a numeric cell is right-aligned against its numbers,
// anything else is left-aligned and gives way at whichever end
// clipValueForKind says.
//
// Both branches measure display columns rather than runes, which is what
// lets formatHeaderRow style the result: a cell padded by rune count would
// be mis-padded the moment it carried an escape sequence, and padding
// before styling is only safe if the two measures agree.
func formatCell(c string, kind columnKind, w int) string {
	if kind == numericColumn {
		return padLeft(c, w)
	}
	return padRight(clipValueForKind(c, w, kind), w)
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
//
// Exactly zero is the one value the scale does not decide, so it is spelled
// "0s" rather than "0ms": zero has no magnitude to place on a scale, and
// both spellings name the same quantity. The tie is broken here, once, for
// every caller rather than at one of them, because the alternative is two
// spellings of one number on adjacent lines of the same pane -- the
// timeline's leading-gap annotation reading "0ms" directly beneath an axis
// whose own left end reads "0s" (see timeAxis, which takes its left label
// from this function so the two cannot drift apart again). A reader
// matching that window to the axis above it should not have to work out
// that they are the same instant.
//
// The profile and diagnose report surfaces use their own formatters
// (in internal/profile and internal/diagnose) and still render an exact
// zero as "0ms", creating a surface difference for that value. The
// difference is not reconciled: --profile's output is held byte-identical,
// and changing it to match would be a deliberate change to a report format
// for a cosmetic gain.
func formatMs(ms uint64) string {
	if ms == 0 {
		return "0s"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
