package diagnose

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/qualitytext"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

const (
	// maxTemplates caps how many recurring message shapes are printed.
	maxTemplates = 15
	// maxFieldKeys caps how many recurring field keys are printed.
	maxFieldKeys = 40
	// maxDistinct caps how many distinct keys any map retains, so a
	// high-cardinality log cannot grow them without bound.
	maxDistinct = 4096

	// structuredMajority is the share of physical lines that must be
	// detected structured-output (terraform.ui JSON) lines before
	// EXTRACTION's "no provider entries" guidance is swapped for guidance
	// specific to structured output. HCP Terraform's structured-output
	// setting emits nothing but JSON lines, so a log produced that way sits
	// close to 100% here; a bare majority is used rather than a threshold
	// nearer 100% so a log that is genuinely structured output but carries a
	// few interleaved hclog lines (e.g. a startup banner emitted before the
	// mode switches) still gets the more useful diagnosis, while a log that
	// merely has a handful of stray JSON lines mixed through an otherwise
	// ordinary hclog file does not.
	structuredMajority = 0.5
)

// Template is a message shape with its content masked.
type Template struct {
	Text  string
	Count uint64
}

// CandidateCount is one row of ADDRESS ATTRIBUTION's candidate-count
// breakdown: how many spans saw exactly this many overlapping candidates.
// Counts of candidates, never the candidates themselves (see attrib.
// Coverage.Candidates), so it carries no address disclosure.
type CandidateCount struct {
	Candidates uint32
	Spans      int
}

// Collector accumulates shape information during a scan. It satisfies
// logfmt.Sink.
type Collector struct {
	comps     *logfmt.Interner
	fieldKeys map[string]uint64 // key -> number of distinct entries it was seen on
	templates map[string]uint64
	compCount map[string]uint64
	dropped   uint64

	// entrySeen is scratch space reused across Entry calls so a key
	// repeated on one line is credited to fieldKeys only once. It is reset
	// at the start of each call, so nothing survives past the call the way
	// Sink.Entry's own strings do not.
	entrySeen []string
}

// NewCollector returns a Collector resolving component ids via comps.
func NewCollector(comps *logfmt.Interner) *Collector {
	return &Collector{
		comps:     comps,
		fieldKeys: map[string]uint64{},
		templates: map[string]uint64{},
		compCount: map[string]uint64{},
	}
}

// bump increments m[key], refusing new keys once the map is full.
func (c *Collector) bump(m map[string]uint64, key string) {
	if _, ok := m[key]; !ok && len(m) >= maxDistinct {
		c.dropped++
		return
	}
	m[key]++
}

// Entry implements logfmt.Sink.
func (c *Collector) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {
	c.bump(c.compCount, MaskComponent(c.comps.Lookup(e.Comp)))
	c.bumpFieldKeys(f)
	// A structured-output line always has an empty msg and no fields (Scan
	// never gives Entry a structured line's content -- see the doc comment
	// on logfmt.StructuredSink), so template(msg, f) is "" for every one of
	// them by design, not by coincidence. That is not a message shape worth
	// reporting: on a structured-only log it would otherwise be the *only*
	// row MESSAGE TEMPLATES ever shows.
	if t := template(msg, f); t != "" {
		c.bump(c.templates, t)
	}
}

// bumpFieldKeys credits each distinct key on this entry once, even if the
// same key appears more than once on the line, so a key repeated within a
// single entry cannot inflate its own recurrence count and thereby qualify
// itself for disclosure under the two-or-more rule.
func (c *Collector) bumpFieldKeys(f logfmt.Fields) {
	c.entrySeen = c.entrySeen[:0]
	for _, fl := range f {
		seen := false
		for _, k := range c.entrySeen {
			if k == fl.Key {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		c.entrySeen = append(c.entrySeen, fl.Key)
		c.bumpFieldKey(fl.Key)
	}
}

// bumpFieldKey increments fieldKeys[key], refusing new keys once the map is
// full. Field.Key aliases logfmt's per-call msg buffer -- Sink.Entry
// documents both msg and f as valid only for the duration of the call, and
// the underlying bufio buffer is reused as the scan continues -- so a key
// entering the map for the first time is cloned, once, rather than on every
// occurrence. This is the same dedup-on-first-insert pattern already used in
// span.Sniffer.trackRequestID and span.ReportedBuilder.retain for the same
// hazard.
func (c *Collector) bumpFieldKey(key string) {
	if _, ok := c.fieldKeys[key]; ok {
		c.fieldKeys[key]++
		return
	}
	if len(c.fieldKeys) >= maxDistinct {
		c.dropped++
		return
	}
	c.fieldKeys[strings.Clone(key)] = 1
}

// template renders a message with prose masked and every field value replaced
// by a placeholder, so a shape can be reported without disclosing content.
func template(msg string, f logfmt.Fields) string {
	prose := msg
	if len(f) > 0 {
		if i := strings.Index(msg, f[0].Key+"="); i >= 0 {
			prose = msg[:i]
		}
	}
	prose = MaskProse(prose)

	keys := make([]string, 0, len(f))
	for _, fl := range f {
		keys = append(keys, fl.Key+"=<v>")
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return prose
	}
	if prose == "" {
		return strings.Join(keys, " ")
	}
	return prose + " " + strings.Join(keys, " ")
}

// ResourceRow is one row of the SLOWEST RESOURCES table: a single UI-hook
// span with its address masked, but its resource type and action -- public
// provider schema names such as "aws_instance" or "create", never customer
// data -- left visible, once passed through maskIdentifier's guard.
type ResourceRow struct {
	DurationMs   uint32
	Action       string
	ResourceType string
	MaskedAddr   string
}

// ResourceTypeTotal is one row of the BY RESOURCE TYPE rollup: total
// duration and count for one resource type, with no addresses at all.
type ResourceTypeTotal struct {
	ResourceType string
	TotalMs      uint64
	Count        uint64
}

// Report is the finished diagnostic summary.
type Report struct {
	Stats              logfmt.Stats
	Caps               span.Capabilities
	Quality            model.CaptureQuality
	Tier               span.Fidelity
	TierUsable         bool
	SpanCount          int
	SlowestMs          uint32
	Elapsed            time.Duration
	TopFieldKeys       []Template // keys seen on two or more entries, sorted, capped
	WithheldFieldKeys  uint64     // distinct keys withheld for appearing on only one entry
	TopComponents      []Template // component shapes seen on two or more entries, sorted, capped
	WithheldComponents uint64     // distinct component shapes withheld for appearing on only one entry
	TopTemplates       []Template // shapes seen on two or more entries, sorted, capped
	WithheldTemplates  uint64     // distinct shapes withheld for appearing only once
	DistinctComps      int
	InternOverflow     uint64
	DroppedKeys        uint64

	// UI-hook figures. These describe span.UIHookBuilder's spans, which sit
	// on their own timeline (see the doc comment on span.Span.StartMs) and
	// so are never summed with the RPC-tier figures above.
	UISpanCount int
	UISlowestMs uint32
	// UIWallClockMs spans the first and last parseable timestamps on structured
	// lines. UIWallClockAvailable distinguishes coincident parsed endpoints
	// from a capture with no usable clock.
	UIWallClockMs        uint64
	UIWallClockAvailable bool

	SlowestResources      []ResourceRow       // ranked by duration descending, top 10
	ByResourceType        []ResourceTypeTotal // ranked by total duration descending, top 10
	UIResourceTypeCount   int                 // distinct resource types seen, before the top-10 cap on ByResourceType
	UIActionCounts        []Template          // action -> count, ranked by count descending
	UIMalformedLines      uint64              // structured lines that failed to decode as JSON (span.UIHookBuilder.Malformed)
	UIBackwardsTimestamps uint64              // UI-hook timestamps earlier than the builder's base, clamped to 0
	UISaturatedDurations  uint64              // UI-hook durations that hit math.MaxUint32ms rather than the real value

	// Address-attribution figures. Counts and totals only -- no address
	// appears in this report, which is the one output that leaves the
	// machine.
	//
	// HookTypes is never withheld for rarity, unlike TopFieldKeys/
	// TopComponents/TopTemplates above: a type seen once -- e.g.
	// apply_errored, the one signal a create failed -- is exactly the
	// finding this histogram exists to surface, and rarity alone is not
	// evidence of hostile content the way it is for a field key or a
	// message template.
	//
	// That is a decision about WITHHOLDING, not about trust in the value
	// itself. Hook "type" values are EXPECTED to be a small closed
	// vocabulary from Terraform's own source (see the
	// opensContext/closesContext switches in internal/attrib/context.go),
	// but nothing enforces that: the value is read straight off a
	// structured-output line's JSON, the same hazard maskIdentifier
	// already guards resource type and action names against, so it is
	// masked the same way before ever becoming a histogram key (see Build).
	HookTypes []Template // every structured-output "type" value and its count
	// CandidateBreakdown is Quality.Attribution.Candidates in deterministic,
	// renderable order (see candidateBreakdown) -- the spec's "what the candidate-count
	// distribution is across them" figure, computed alongside attribution since
	// nothing cheaper yields it. Populated only when Coverage itself is (see
	// HasSpans).
	CandidateBreakdown []CandidateCount
	// HasSpans is true only when context exists (HasContext) AND there is
	// at least one RPC span to attribute it against -- both conditions
	// gate the assignment in Build. A log can carry address context with
	// zero RPC spans -- e.g. INFO-level terraform.ui without TRACE
	// provider logging -- and Coverage/NameableShare must never be
	// rendered as a measurement in that case: there was nothing to
	// measure, which reads very differently from "everything measured
	// failed". HasSpans is never read outside the HasContext branch of
	// Render, so a log with RPC spans and no context (HasContext false)
	// reporting HasSpans false as well is not a case anything relies on.
	HasSpans bool
	Contexts int // address context windows collected
	// UnclosedContexts and ZeroExtentContexts are counted so their loss is
	// visible rather than silent, per the spec: an unclosed context was
	// still open when the log ended, and a zero-extent one -- Start not
	// strictly before End -- can never be a candidate for any span (see
	// attrib.Context's half-open-window doc comment). The same capture cut
	// that produces an unclosed context can produce a zero-extent one: a
	// resource still running when the log ends gets an End equal to the
	// log's own last timestamp, which is also its Start.
	UnclosedContexts   int
	ZeroExtentContexts int
	// MalformedStructuredLines and UnmatchedTerminators are
	// attrib.ContextCollector's own Malformed and UnmatchedTerminators
	// counts: structured lines the context collector could not decode as
	// JSON, and terminators seen with no matching start (a capture that
	// begins mid-run). Counts only, so reporting them adds no disclosure
	// surface.
	MalformedStructuredLines uint64
	UnmatchedTerminators     uint64
	HasContext               bool
	// StreamOffsetMs is only meaningful when StreamOffsetKnown is true. A
	// log with no hclog-timestamped lines at all (a pure structured-output
	// capture) can never derive it, and the zero value must not be
	// mistaken for a genuine "streams start at the same instant"
	// measurement.
	StreamOffsetMs    int64
	StreamOffsetKnown bool

	fieldKeys     map[string]uint64 // every distinct key seen, unfiltered -- unexported so a future caller cannot range over the singletons Amendment 1 withholds
	templateCount map[string]uint64
}

// maxResourceRows caps SLOWEST RESOURCES and BY RESOURCE TYPE at their top N,
// per the diagnostic report's design: enough to see shape and rank, not a
// full listing.
const maxResourceRows = 10

// Build assembles a Report from a completed scan. spans are span.
// ReportedBuilder's RPC-tier spans; uiSpans are span.UIHookBuilder's
// UI-hook spans. The two are reported separately and never merged: they sit
// on different timelines (see span.Span's StartMs/EndMs doc comment), so
// concatenating and sorting them together would silently misorder them.
// uiMalformed, uiBackwards and uiSaturated are the same UIHookBuilder's
// Malformed, BackwardsTimestamps and Saturated counters: events that did
// not stop the scan but must not go unmentioned in ANOMALIES, since a log
// this tool cannot be run again on (the whole reason it exists) that had
// its schema drift or get truncated would otherwise read as ordinary.
// cc is the same scan's attrib.ContextCollector: its hook-type histogram and
// address contexts are what let this report measure attribution coverage
// without ever naming an address itself.
func Build(st logfmt.Stats, caps span.Capabilities, spans []span.Span, uiSpans []span.Span, quality model.CaptureQuality,
	uiMalformed, uiBackwards, uiSaturated uint64, cc *attrib.ContextCollector,
	c *Collector, comps *logfmt.Interner, elapsed time.Duration) Report {
	var tier span.Fidelity
	usable := quality.RPC.Admitted > 0 || quality.UI.Admitted > 0
	if quality.RPC.Admitted > 0 {
		tier = span.FidelityReported
	} else if quality.UI.Admitted > 0 {
		tier = span.FidelityUIReported
	}
	topFieldKeys, withheldFieldKeys := recurringTopN(c.fieldKeys, maxFieldKeys)
	topComponents, withheldComponents := recurringTopN(c.compCount, maxTemplates)
	topTemplates, withheldTemplates := recurringTopN(c.templates, maxTemplates)

	// comps.Len() always includes the Interner's pre-seeded "" at id 0 once
	// Intern has been called at least once (see Interner.init) -- so it
	// overcounts by one unless a "(none)" component was genuinely seen.
	// c.compCount[""] records that case: it is only populated when an
	// entry's masked component was empty. But Interner.init is lazy: a log
	// that never calls Intern at all -- every entry structured-output, so
	// Collector only ever Looks up id 0 and never interns a real component
	// -- leaves comps.Len() at 0 even though id 0 ("no component") was
	// still seen on every entry and is credited in compCount[""]. That
	// pre-seeded slot is real whenever compCount[""] is populated, so it is
	// added back rather than only ever subtracted.
	distinctComps := comps.Len()
	_, sawNone := c.compCount[""]
	switch {
	case sawNone && distinctComps == 0:
		distinctComps = 1
	case !sawNone && distinctComps > 0:
		distinctComps--
	}

	r := Report{
		Stats:              st,
		Caps:               caps,
		Quality:            quality,
		Tier:               tier,
		TierUsable:         usable,
		SpanCount:          len(spans),
		Elapsed:            elapsed,
		TopFieldKeys:       topFieldKeys,
		WithheldFieldKeys:  withheldFieldKeys,
		TopComponents:      topComponents,
		WithheldComponents: withheldComponents,
		TopTemplates:       topTemplates,
		WithheldTemplates:  withheldTemplates,
		DistinctComps:      distinctComps,
		InternOverflow:     comps.Overflowed(),
		DroppedKeys:        c.dropped,
		fieldKeys:          c.fieldKeys,
		templateCount:      c.templates,

		UIMalformedLines:      uiMalformed,
		UIBackwardsTimestamps: uiBackwards,
		UISaturatedDurations:  uiSaturated,
	}
	for _, s := range spans {
		if s.DurationMs > r.SlowestMs {
			r.SlowestMs = s.DurationMs
		}
	}

	// cc parses every structured line's timestamp independently of whether
	// that line builds a UI span. Its endpoints therefore describe observed
	// capture time without deriving a clock from reported durations.
	if !cc.FirstTS().IsZero() {
		r.UIWallClockMs = uint64(cc.LastTS().Sub(cc.FirstTS()).Milliseconds())
		r.UIWallClockAvailable = true
	}

	r.UISpanCount = len(uiSpans)
	rows := make([]ResourceRow, 0, len(uiSpans))
	typeTotals := map[string]*ResourceTypeTotal{}
	actionCounts := map[string]uint64{}
	for _, s := range uiSpans {
		if s.DurationMs > r.UISlowestMs {
			r.UISlowestMs = s.DurationMs
		}
		// ResourceType and Action come straight from a structured-output
		// line's JSON, with nothing upstream constraining their shape --
		// unlike a field key, which logfmt.ValidKey already restricts to an
		// identifier charset. maskIdentifier applies MaskComponent's
		// posture to the same hazard: a genuine Terraform type or action
		// name passes unchanged, anything else masks to "<other>" rather
		// than reach the report (and its column-aligned layout) unguarded.
		action := maskIdentifier(s.RPC)
		resourceType := maskIdentifier(s.ResourceType)

		rows = append(rows, ResourceRow{
			DurationMs:   s.DurationMs,
			Action:       action,
			ResourceType: resourceType,
			MaskedAddr:   MaskAddress(s.Address),
		})

		rt, ok := typeTotals[resourceType]
		if !ok {
			rt = &ResourceTypeTotal{ResourceType: resourceType}
			typeTotals[resourceType] = rt
		}
		rt.TotalMs += uint64(s.DurationMs)
		rt.Count++

		actionCounts[action]++
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DurationMs != rows[j].DurationMs {
			return rows[i].DurationMs > rows[j].DurationMs
		}
		return rows[i].MaskedAddr < rows[j].MaskedAddr
	})
	if len(rows) > maxResourceRows {
		rows = rows[:maxResourceRows]
	}
	r.SlowestResources = rows

	typeRows := make([]ResourceTypeTotal, 0, len(typeTotals))
	for _, rt := range typeTotals {
		typeRows = append(typeRows, *rt)
	}
	sort.Slice(typeRows, func(i, j int) bool {
		if typeRows[i].TotalMs != typeRows[j].TotalMs {
			return typeRows[i].TotalMs > typeRows[j].TotalMs
		}
		return typeRows[i].ResourceType < typeRows[j].ResourceType
	})
	r.UIResourceTypeCount = len(typeRows)
	if len(typeRows) > maxResourceRows {
		typeRows = typeRows[:maxResourceRows]
	}
	r.ByResourceType = typeRows

	actions := make([]Template, 0, len(actionCounts))
	for action, n := range actionCounts {
		actions = append(actions, Template{Text: action, Count: n})
	}
	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Count != actions[j].Count {
			return actions[i].Count > actions[j].Count
		}
		return actions[i].Text < actions[j].Text
	})
	r.UIActionCounts = actions

	// No withholding here (see the HookTypes doc comment): maskedTypeCounts
	// is already the whole map, so topN's cap is set to its length rather
	// than maxTemplates, which would silently drop real hook types once a
	// capture used more than maxTemplates distinct ones.
	typeCounts := maskedTypeCounts(cc.TypeCounts())
	r.HookTypes = topN(typeCounts, len(typeCounts))
	r.MalformedStructuredLines = cc.Malformed()
	r.UnmatchedTerminators = cc.UnmatchedTerminators()
	ctxs := cc.Contexts()
	// The gate is whether any context was collected at all, not whether one
	// has CLOSED: a context is appended the moment a _start hook is seen, so
	// a capture killed mid-run -- every resource started, none finished --
	// has real context windows despite zero completed pairs. CompletedPairs()
	// alone would miss exactly that case, telling a reader with this kind of
	// log to enable a stream their log already carries.
	if len(ctxs) > 0 {
		r.HasContext = true
		r.Contexts = len(ctxs)
		for _, ctx := range ctxs {
			if ctx.Unclosed {
				r.UnclosedContexts++
			}
		}
		r.ZeroExtentContexts = int(cc.ZeroExtentContexts())
		if quality.RPC.Admitted > 0 {
			r.HasSpans = true
			r.CandidateBreakdown = candidateBreakdown(quality.Attribution.Candidates)
		}
		if !st.FirstTS.IsZero() && !cc.FirstTS().IsZero() {
			r.StreamOffsetMs = cc.FirstTS().Sub(st.FirstTS).Milliseconds()
			r.StreamOffsetKnown = true
		}
	}

	return r
}

// maskedTypeCounts rebuilds a hook-type histogram with every key masked by
// maskIdentifier before use, summing the counts of any keys that collide
// once masked. A structured-output line's "type" value has nothing upstream
// constraining its shape (see the HookTypes doc comment), so this is what
// keeps a crafted value from reaching --diagnose, the one artefact that
// leaves the machine, verbatim: it collapses into the shared "<other>"
// bucket with every other hostile value instead.
// writeReqIDSpread reports how many distinct request ids the log carries and
// how much traffic each wears, which is what sizes anything that groups a
// log by call.
//
// Nothing is written when the log carries no request ids: a spread over an
// empty set would read as a measurement of zero-entry calls rather than of
// no calls at all.
func writeReqIDSpread(b *strings.Builder, c span.Capabilities) {
	if c.DistinctReqIDs == 0 {
		return
	}
	fmt.Fprintf(b, "  %-25s %d\n", "distinct req ids", c.DistinctReqIDs)
	fmt.Fprintf(b, "  %-25s min %d, median %d, max %d\n", "entries per req id",
		c.MinEntriesPerReqID, c.MedianEntriesPerReqID, c.MaxEntriesPerReqID)
	// The cap is stated where the figures it bounds are read, not in a
	// footnote: a saturated count mistaken for a measurement is the whole
	// hazard, and it bites hardest on the large capture where the number
	// would otherwise be most interesting.
	if c.ReqIDTrackingFull {
		fmt.Fprintf(b, "  %-25s tracking capped -- the four figures above under-report\n", "")
	}
}

// plural picks a suffix for n, so a count of one does not read as a count of
// several.
func plural(n uint64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func maskedTypeCounts(m map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(m))
	for k, v := range m {
		out[maskIdentifier(k)] += v
	}
	return out
}

// formatMs renders a millisecond duration for SLOWEST RESOURCES and BY
// RESOURCE TYPE: whole milliseconds below one second, seconds with one
// decimal place at or above it. A fixed "%.1fs" format renders anything
// under 100ms as a flat "0.0s" -- elapsed_seconds: 0 is a genuinely common
// figure for a fast data-source refresh -- which would turn a plan
// dominated by fast resources into an undifferentiated column of zeros and
// make the ranking convey nothing.
func formatMs(ms uint64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// pluralSpans renders "span" or "spans" to match n. The confidence lines in
// ADDRESS ATTRIBUTION are the one place in this report a count sits directly
// beside its own noun in prose rather than a fixed column label, so a bare
// "%d spans" reads as a grammar mistake -- "1 spans" -- on exactly the
// output a reader pastes into a conversation.
func pluralSpans(n int) string {
	if n == 1 {
		return "span"
	}
	return "spans"
}

func topN(m map[string]uint64, n int) []Template {
	out := make([]Template, 0, len(m))
	for k, v := range m {
		out = append(out, Template{Text: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// recurringTopN returns the top n entries from m that were observed on two or
// more entries, plus how many distinct shapes were withheld for being
// observed on only one. A shape seen once carries no evidence of being
// structural rather than an incidental one-off, and is exactly the case a
// high-entropy token masquerading as a key or template would fall into, so
// withholding it costs nothing that matters and closes that disclosure path.
func recurringTopN(m map[string]uint64, n int) (top []Template, withheld uint64) {
	recurring := make(map[string]uint64, len(m))
	for k, v := range m {
		if v < 2 {
			withheld++
			continue
		}
		recurring[k] = v
	}
	return topN(recurring, n), withheld
}

// candidateBreakdown orders attrib.Coverage.Candidates -- a map, whose
// iteration order Go randomises -- into a slice safe to render
// deterministically: by span count descending (the shape a reader scans
// first, same as topN), tie-broken by candidate count ascending so two
// candidate counts seen by an equal number of spans always print in the same
// order. Spans with zero candidates (Unattributed: nothing ever overlapped)
// are excluded -- this breakdown is of spans that fell inside at least one
// candidate window, per the spec, and Unattributed's own count already
// covers that population.
func candidateBreakdown(m map[uint32]int) []CandidateCount {
	out := make([]CandidateCount, 0, len(m))
	for k, v := range m {
		if k == 0 {
			continue
		}
		out = append(out, CandidateCount{Candidates: k, Spans: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spans != out[j].Spans {
			return out[i].Spans > out[j].Spans
		}
		return out[i].Candidates < out[j].Candidates
	})
	return out
}

// pluralCandidates renders "candidate" or "candidates" to match n, the same
// grammar guard pluralSpans applies to a span count sitting beside its own
// noun in prose.
func pluralCandidates(n uint32) string {
	if n == 1 {
		return "candidate"
	}
	return "candidates"
}

// Render writes the report as plain text for pasting into a conversation.
func (r Report) Render(w io.Writer) error {
	b := &strings.Builder{}

	fmt.Fprintf(b, "tfli diagnostic report\n======================\n\n")

	fmt.Fprintf(b, "SIZE\n")
	fmt.Fprintf(b, "  bytes                %d\n", r.Stats.Bytes)
	fmt.Fprintf(b, "  physical lines       %d\n", r.Stats.PhysicalLines)
	fmt.Fprintf(b, "  logical entries      %d\n", r.Stats.Entries)
	if r.Stats.PhysicalLines > 0 {
		fmt.Fprintf(b, "  mean line length     %d bytes\n", r.Stats.Bytes/r.Stats.PhysicalLines)
		fmt.Fprintf(b, "  untimestamped lines  %d (%.1f%% of lines)\n",
			r.Stats.UntimestampedLines,
			100*float64(r.Stats.UntimestampedLines)/float64(r.Stats.PhysicalLines))
		fmt.Fprintf(b, "  structured lines     %d (%.1f%% of lines)\n",
			r.Stats.StructuredLines,
			100*float64(r.Stats.StructuredLines)/float64(r.Stats.PhysicalLines))
	}
	if r.Stats.Bytes > 0 {
		fmt.Fprintf(b, "  continuation bytes (wrapped values + interleaved output)\n")
		fmt.Fprintf(b, "                       %d (%.1f%% of file)\n",
			r.Stats.ContinuationBytes,
			100*float64(r.Stats.ContinuationBytes)/float64(r.Stats.Bytes))
	}
	fmt.Fprintf(b, "  long output blocks   %d\n", r.Stats.LongContinuationRuns)
	fmt.Fprintf(b, "  ANSI escapes         %v\n", r.Stats.SawANSI)
	switch {
	case !r.Stats.FirstTS.IsZero():
		fmt.Fprintf(b, "  log wall-clock       %.1fs\n", r.Stats.LastTS.Sub(r.Stats.FirstTS).Seconds())
	case r.UIWallClockAvailable:
		fmt.Fprintf(b, "  log wall-clock       %.1fs (derived from structured timestamps)\n", float64(r.UIWallClockMs)/1000)
	default:
		fmt.Fprintf(b, "  log wall-clock       unavailable\n")
	}
	if r.Elapsed > 0 {
		fmt.Fprintf(b, "  parse throughput     %.0f MB/s (%s)\n",
			float64(r.Stats.Bytes)/(1<<20)/r.Elapsed.Seconds(), r.Elapsed.Round(time.Millisecond))
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "LEVELS\n")
	sawLevel := false
	for l := logfmt.LevelUnknown; l <= logfmt.LevelError; l++ {
		if n := r.Stats.ByLevel[l]; n > 0 {
			fmt.Fprintf(b, "  %-8s %d\n", l, n)
			sawLevel = true
		}
	}
	if !sawLevel {
		fmt.Fprintf(b, "  none\n")
	}
	fmt.Fprintf(b, "\n")
	qualitytext.WriteCaptureQuality(b, r.Quality)

	// EXTRACTION's labels vary widely in length -- "response duration
	// fields" is the longest -- so a field-width specifier keeps the value
	// column aligned instead of hand-counting padding spaces per line.
	fmt.Fprintf(b, "EXTRACTION\n")
	if r.TierUsable {
		fmt.Fprintf(b, "  %-25s %s\n", "selected tier", r.Tier)
	} else {
		fmt.Fprintf(b, "  %-25s NONE USABLE\n", "selected tier")
	}
	// The structured-output guidance below is about explaining the shape of
	// the input, not about whether a tier was selected -- a majority-
	// structured log reads as broken (every RPC-related counter at zero)
	// without it, whether or not UI-hook completions happened to make the
	// ui-reported tier usable. So this is decided independently of
	// r.TierUsable, unlike the plain "nothing to profile" guidance below it,
	// which only makes sense once no tier at all could be selected.
	structured := r.Stats.PhysicalLines > 0 &&
		float64(r.Stats.StructuredLines)/float64(r.Stats.PhysicalLines) > structuredMajority
	// hasRPCEvidence is true when the log carries any real RPC-related
	// entries at all -- a majority-structured log can still be a mix of
	// info-level UI-hook JSON and interleaved hclog RPC-trace lines, and on
	// that log the flat claim "the counters below are expected to read
	// zero" is simply false: it would sit printed directly above a nonzero
	// response entries count.
	hasRPCEvidence := r.Caps.ResponseEntries+r.Caps.RequestEntries+r.Caps.ProviderEntries > 0
	switch {
	case structured && !hasRPCEvidence:
		fmt.Fprintf(b, "  Most of this log is structured output (terraform.ui JSON).\n")
		fmt.Fprintf(b, "  It is info level only, so it never carries provider RPC\n")
		fmt.Fprintf(b, "  entries -- the counters below are expected to read zero.\n")
		fmt.Fprintf(b, "  Its per-resource timings, from Terraform's own UI hooks,\n")
		fmt.Fprintf(b, "  appear in SLOWEST RESOURCES when this log has any.\n")
		writeRPCCaptureHint(b)
	case structured && hasRPCEvidence:
		fmt.Fprintf(b, "  Most of this log is structured output (terraform.ui JSON),\n")
		fmt.Fprintf(b, "  but it also carries provider RPC-related entries below --\n")
		fmt.Fprintf(b, "  most likely hclog output interleaved with it. Its\n")
		fmt.Fprintf(b, "  per-resource timings, from Terraform's own UI hooks, appear\n")
		fmt.Fprintf(b, "  in SLOWEST RESOURCES when this log has any.\n")
	case !r.TierUsable && hasRPCEvidence:
		fmt.Fprintf(b, "  Provider RPC evidence was observed, but no admitted duration\n")
		fmt.Fprintf(b, "  was available to profile.\n")
		writeRPCCaptureHint(b)
	case !r.TierUsable && r.Stats.StructuredLines > 0:
		fmt.Fprintf(b, "  A structured-output stream was observed, but no admitted\n")
		fmt.Fprintf(b, "  UI-hook duration was available to profile.\n")
	case !r.TierUsable:
		fmt.Fprintf(b, "  This log contains no provider RPC timing evidence to profile.\n")
		writeRPCCaptureHint(b)
	}
	fmt.Fprintf(b, "  %-25s %d\n", "response entries", r.Caps.ResponseEntries)
	fmt.Fprintf(b, "  %-25s %d\n", "request entries", r.Caps.RequestEntries)
	fmt.Fprintf(b, "  %-25s %d\n", "response duration fields", r.Caps.DurationFields)
	fmt.Fprintf(b, "  %-25s %d\n", "req id fields", r.Caps.ReqIDFields)
	fmt.Fprintf(b, "  %-25s %d of %d responses\n", "  on a response", r.Caps.ResponseReqIDFields, r.Caps.ResponseEntries)
	fmt.Fprintf(b, "  %-25s %d\n", "correlated req ids", r.Caps.CorrelatedReqIDs)
	writeReqIDSpread(b, r.Caps)
	fmt.Fprintf(b, "  %-25s %d\n", "provider entries", r.Caps.ProviderEntries)
	fmt.Fprintf(b, "  %-25s %d\n", "core vertex lines", r.Caps.CoreVertexLines)
	fmt.Fprintf(b, "  %-25s %d\n", "core GRPC lines", r.Caps.CoreGRPCLines)
	fmt.Fprintf(b, "  %-25s %d (fields are read from header lines only -- tier above may be conservative)\n",
		"continuation lines not parsed for fields", r.Stats.ContinuationLines)
	fmt.Fprintf(b, "  %-25s %d line(s), costing %d entr%s their only id (upper bound)\n\n",
		"  of those, carrying an id", r.Stats.ContinuationReqIDLines,
		r.Stats.ContinuationOnlyReqIDEntries, plural(r.Stats.ContinuationOnlyReqIDEntries, "y", "ies"))

	fmt.Fprintf(b, "SPANS\n")
	fmt.Fprintf(b, "  slowest span         %d ms\n", r.SlowestMs)
	// UI-hook spans sit on a different timeline from the RPC spans above
	// (see span.Span's StartMs/EndMs doc comment) and are reported
	// separately rather than folded into the figures above.
	fmt.Fprintf(b, "  UI-hook slowest span %d ms\n", r.UISlowestMs)
	if len(r.UIActionCounts) > 0 {
		fmt.Fprintf(b, "  UI-hook actions     ")
		for _, a := range r.UIActionCounts {
			fmt.Fprintf(b, "  %s %d", a.Text, a.Count)
		}
		fmt.Fprintf(b, "\n")
	}
	fmt.Fprintf(b, "\n")

	// SLOWEST RESOURCES and BY RESOURCE TYPE are rendered only when there
	// are UI-hook spans to show -- an empty "none" section here would be
	// noise on the far more common RPC-tier log. Each header says "top N"
	// only when the list was actually truncated to get there -- printing
	// "top 2" for a list that has exactly two rows in total reads as a
	// truncation that never happened.
	if r.UISpanCount > 0 {
		if len(r.SlowestResources) < r.UISpanCount {
			fmt.Fprintf(b, "SLOWEST RESOURCES (addresses masked, top %d)\n", len(r.SlowestResources))
		} else {
			fmt.Fprintf(b, "SLOWEST RESOURCES (addresses masked)\n")
		}
		// Terraform rounds a resource's start and end to the nearest second
		// before subtracting them, so these figures are whole seconds
		// carrying up to a second of error each. Rollups over many
		// resources survive that; ranking two rows a second apart does not,
		// and a ranked table invites exactly that reading.
		fmt.Fprintf(b, "  Terraform reports these in whole seconds, +/- 1s each, so\n")
		fmt.Fprintf(b, "  neighbouring rows are not reliably ordered. Totals below\n")
		fmt.Fprintf(b, "  are sounder than any single row.\n")
		for _, row := range r.SlowestResources {
			fmt.Fprintf(b, "  %8s  %-8s %-24s %s\n",
				formatMs(uint64(row.DurationMs)), row.Action, row.ResourceType, row.MaskedAddr)
		}
		fmt.Fprintf(b, "\n")

		if len(r.ByResourceType) < r.UIResourceTypeCount {
			fmt.Fprintf(b, "BY RESOURCE TYPE (top %d)\n", len(r.ByResourceType))
		} else {
			fmt.Fprintf(b, "BY RESOURCE TYPE\n")
		}
		for _, t := range r.ByResourceType {
			fmt.Fprintf(b, "  %8s  %6d  %s\n", formatMs(t.TotalMs), t.Count, t.ResourceType)
		}
		fmt.Fprintf(b, "\n")
	}

	fmt.Fprintf(b, "ADDRESS ATTRIBUTION\n")
	if !r.HasContext {
		// MalformedStructuredLines and UnmatchedTerminators are counted by
		// attrib.ContextCollector regardless of whether any context pair
		// ever completes (see Build). They lead here because there is
		// nothing else this branch has to report -- a log with malformed
		// structured lines or an unmatched terminator but no address context
		// at all must not have both counts silently dropped.
		fmt.Fprintf(b, "  %-25s %d\n", "context lines malformed", r.MalformedStructuredLines)
		fmt.Fprintf(b, "  %-25s %d\n", "unmatched terminators", r.UnmatchedTerminators)
		// The HCP Terraform/CLI debug-logging toggle is what produces the
		// terraform.ui stream this log is missing -- TF_LOG_PROVIDER and
		// TF_LOG_SDK_PROTO produce no terraform.ui context on their own.
		// They instead govern the provider RPC entries reported in SPANS
		// above, which this log may already carry: a reader who already set
		// those two and is missing only the toggle must not be told to set
		// what they already have.
		fmt.Fprintf(b, "  no address context in this log -- attribution needs\n")
		fmt.Fprintf(b, "  the terraform.ui stream, which the debug-logging\n")
		fmt.Fprintf(b, "  toggle produces. TF_LOG_PROVIDER=TRACE and\n")
		fmt.Fprintf(b, "  TF_LOG_SDK_PROTO=TRACE govern the provider RPC\n")
		fmt.Fprintf(b, "  entries above instead; set alone, they are not\n")
		fmt.Fprintf(b, "  expected to add a terraform.ui stream of their own\n")
	} else {
		// Substance first, caveats after: how much context there is, then
		// how much of it is suspect. context lines malformed/unmatched
		// terminators are the same attrib.ContextCollector counts as the
		// no-context branch above, just no longer leading here -- there is
		// something more informative to put first once contexts exist.
		fmt.Fprintf(b, "  %-25s %d\n", "contexts", r.Contexts)
		fmt.Fprintf(b, "  %-25s %d\n", "unclosed contexts", r.UnclosedContexts)
		fmt.Fprintf(b, "  %-25s %d\n", "zero-extent contexts", r.ZeroExtentContexts)
		fmt.Fprintf(b, "  %-25s %d\n", "context lines malformed", r.MalformedStructuredLines)
		fmt.Fprintf(b, "  %-25s %d\n", "unmatched terminators", r.UnmatchedTerminators)
		if !r.HasSpans {
			// Context exists but there is nothing to correlate it against --
			// e.g. INFO-level terraform.ui without TRACE provider RPC
			// logging. Printing a 0.0% share or 0-span confidence lines here
			// would read as "attribution ran and every span failed", which is
			// a different and false claim from "there was nothing to
			// attribute".
			fmt.Fprintf(b, "  no provider RPC spans to attribute -- this log has\n")
			fmt.Fprintf(b, "  terraform.ui context but no TRACE-level provider RPC\n")
			fmt.Fprintf(b, "  entries, so nothing was measured\n")
		} else {
			for _, c := range []attrib.Confidence{
				attrib.Contained, attrib.Likely, attrib.Overlapping,
				attrib.Ambiguous, attrib.Unattributed,
			} {
				fmt.Fprintf(b, "  %-25s %d %s, %d ms\n", c.String(),
					r.Quality.Attribution.ByConfidence[c], pluralSpans(r.Quality.Attribution.ByConfidence[c]), r.Quality.Attribution.MsByConfidence[c])
			}
			// Its own group, after the confidence list rather than nested
			// inside it: indenting this under the ambiguous row read as
			// ambiguous's own detail, when it in fact covers every span with
			// at least one candidate (see candidateBreakdown), Contained and
			// Likely included.
			if len(r.CandidateBreakdown) > 0 {
				fmt.Fprintf(b, "  %-25s (spans with a candidate)\n", "candidate counts")
				for _, cc := range r.CandidateBreakdown {
					fmt.Fprintf(b, "      %d %s: %d %s\n",
						cc.Candidates, pluralCandidates(cc.Candidates), cc.Spans, pluralSpans(cc.Spans))
				}
			}
		}
		// The offset is the constant that re-bases the two streams onto one
		// clock. It is NOT a check on clock agreement: the two baselines
		// mark different events -- the hclog stream brackets the whole
		// Terraform process, terraform.ui only the plan phase within it --
		// so their difference is real elapsed time plus any skew, and the
		// two terms are not separable.
		if r.StreamOffsetKnown {
			fmt.Fprintf(b, "  %-25s %d ms\n", "stream offset", r.StreamOffsetMs)
		} else {
			// A pure structured-output capture has no hclog-timestamped
			// lines at all, so there is no hclog baseline to re-base onto --
			// the zero value must not render as a measured "streams start at
			// the same instant".
			fmt.Fprintf(b, "  %-25s not derived (no hclog-timestamped lines)\n", "stream offset")
		}
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "HOOK TYPES (%d distinct)\n", len(r.HookTypes))
	for _, t := range r.HookTypes {
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, t.Text)
	}
	if len(r.HookTypes) == 0 {
		fmt.Fprintf(b, "  none\n")
	}
	fmt.Fprintf(b, "\n")

	if r.Stats.BackwardsTimestamps > 0 || r.InternOverflow > 0 || r.DroppedKeys > 0 || r.Stats.LinesSaturated > 0 ||
		r.UIMalformedLines > 0 || r.UIBackwardsTimestamps > 0 || r.UISaturatedDurations > 0 {
		fmt.Fprintf(b, "ANOMALIES\n")
		fmt.Fprintf(b, "  backwards timestamps %d\n", r.Stats.BackwardsTimestamps)
		fmt.Fprintf(b, "  intern overflow      %d\n", r.InternOverflow)
		fmt.Fprintf(b, "  dropped map keys     %d\n", r.DroppedKeys)
		fmt.Fprintf(b, "  line-count saturated %d\n", r.Stats.LinesSaturated)
		// UI-hook lines malformed: a structured line that failed to decode
		// as JSON, e.g. a schema drift or a truncated log -- on the one
		// machine holding the original, this may be the only trace that
		// something upstream went wrong rather than the log genuinely
		// having no UI-hook spans.
		fmt.Fprintf(b, "  UI-hook lines malformed %d\n", r.UIMalformedLines)
		fmt.Fprintf(b, "  UI-hook backwards timestamps %d\n", r.UIBackwardsTimestamps)
		fmt.Fprintf(b, "  UI-hook durations saturated %d\n\n", r.UISaturatedDurations)
	}

	fmt.Fprintf(b, "COMPONENTS (%d distinct, recurring only, top %d)\n", r.DistinctComps, len(r.TopComponents))
	for _, t := range r.TopComponents {
		name := t.Text
		if name == "" {
			name = "(none)"
		}
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, logfmt.DisplayText(name))
	}
	if len(r.TopComponents) == 0 {
		fmt.Fprintf(b, "  none\n")
	}
	if r.WithheldComponents > 0 {
		fmt.Fprintf(b, "  %d component shapes seen only once (withheld)\n", r.WithheldComponents)
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "FIELD KEYS (recurring only, top %d)\n", len(r.TopFieldKeys))
	for _, t := range r.TopFieldKeys {
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, t.Text)
	}
	if len(r.TopFieldKeys) == 0 {
		fmt.Fprintf(b, "  none\n")
	}
	if r.WithheldFieldKeys > 0 {
		fmt.Fprintf(b, "  %d key shapes seen only once (withheld)\n", r.WithheldFieldKeys)
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "MESSAGE TEMPLATES (content masked, recurring only, top %d)\n", len(r.TopTemplates))
	for _, t := range r.TopTemplates {
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, logfmt.DisplayText(t.Text))
	}
	if len(r.TopTemplates) == 0 {
		fmt.Fprintf(b, "  none\n")
	}
	if r.WithheldTemplates > 0 {
		fmt.Fprintf(b, "  %d message shapes seen only once (withheld)\n", r.WithheldTemplates)
	}
	fmt.Fprintf(b, "\n")
	fmt.Fprintf(b, "Field keys are reported verbatim and are restricted to an\n")
	fmt.Fprintf(b, "identifier charset. Message shapes are masked heuristically.\n")
	fmt.Fprintf(b, "Review this output before sharing it.\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// writeRPCCaptureHint explains how to capture provider RPC entries. Debug
// logging alone does not produce them and never has: terraform-plugin-go
// writes tf_req_duration_ms and "Sending request downstream" exclusively
// through logging.ProtocolTrace, so both are filtered out below TRACE. A
// real debug-enabled HCP run measured zero of each.
//
// Two gates sit between the provider and the log file. TF_LOG_SDK_PROTO
// raises the provider's proto subsystem, which is built as its own hclog
// logger with its own level. Terraform then re-emits plugin output through
// a logger of its own, whose level comes from TF_LOG_PROVIDER falling back
// to TF_LOG (internal/logging/logging.go, providerLogLevel), and which
// discards the provider's TRACE lines when it sits at DEBUG. Raising only
// the first was measured to yield a log with no TRACE entries at all, so
// naming one variable without the other costs the reader a whole run.
func writeRPCCaptureHint(b *strings.Builder) {
	fmt.Fprintf(b, "  Provider RPC entries are emitted only at TRACE, so debug\n")
	fmt.Fprintf(b, "  logging alone will not produce them. Two levels gate them:\n")
	fmt.Fprintf(b, "  what the provider writes, and what Terraform keeps. Set both\n")
	fmt.Fprintf(b, "  TF_LOG_PROVIDER=TRACE and TF_LOG_SDK_PROTO=TRACE, or raise\n")
	fmt.Fprintf(b, "  everything with TF_LOG=TRACE.\n")
}
