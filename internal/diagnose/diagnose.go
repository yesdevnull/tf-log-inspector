package diagnose

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
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
	c.bump(c.templates, template(msg, f))
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

// Report is the finished diagnostic summary.
type Report struct {
	Stats              logfmt.Stats
	Caps               span.Capabilities
	Tier               span.Fidelity
	TierUsable         bool
	SpanCount          int
	ClampedSpans       int
	SlowestMs          uint32
	TotalSpanMs        uint64
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

	fieldKeys     map[string]uint64 // every distinct key seen, unfiltered -- unexported so a future caller cannot range over the singletons Amendment 1 withholds
	templateCount map[string]uint64
}

// Build assembles a Report from a completed scan.
func Build(st logfmt.Stats, caps span.Capabilities, spans []span.Span, c *Collector, comps *logfmt.Interner, elapsed time.Duration) Report {
	tier, usable := caps.BestFidelity()
	topFieldKeys, withheldFieldKeys := recurringTopN(c.fieldKeys, maxFieldKeys)
	topComponents, withheldComponents := recurringTopN(c.compCount, maxTemplates)
	topTemplates, withheldTemplates := recurringTopN(c.templates, maxTemplates)

	// comps.Len() always includes the Interner's pre-seeded "" at id 0 (see
	// Interner.init), whether or not any line actually lacked a component --
	// so it overcounts by one unless a "(none)" component was genuinely seen.
	// c.compCount[""] records that case: it is only populated when an entry's
	// masked component was empty.
	distinctComps := comps.Len()
	if _, sawNone := c.compCount[""]; !sawNone && distinctComps > 0 {
		distinctComps--
	}

	r := Report{
		Stats:              st,
		Caps:               caps,
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
	}
	for _, s := range spans {
		r.TotalSpanMs += uint64(s.DurationMs)
		if s.DurationMs > r.SlowestMs {
			r.SlowestMs = s.DurationMs
		}
		if s.StartClamped {
			r.ClampedSpans++
		}
	}
	return r
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
	if !r.Stats.FirstTS.IsZero() {
		fmt.Fprintf(b, "  log wall-clock       %.1fs\n", r.Stats.LastTS.Sub(r.Stats.FirstTS).Seconds())
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

	// EXTRACTION's labels vary widely in length -- "response duration
	// fields" is the longest -- so a field-width specifier keeps the value
	// column aligned instead of hand-counting padding spaces per line.
	fmt.Fprintf(b, "EXTRACTION\n")
	if r.TierUsable {
		fmt.Fprintf(b, "  %-25s %s\n", "selected tier", r.Tier)
	} else {
		fmt.Fprintf(b, "  %-25s NONE USABLE\n", "selected tier")
		structured := r.Stats.PhysicalLines > 0 &&
			float64(r.Stats.StructuredLines)/float64(r.Stats.PhysicalLines) > structuredMajority
		if structured {
			fmt.Fprintf(b, "  This is a structured-output (terraform.ui JSON) log.\n")
			fmt.Fprintf(b, "  It carries per-resource timings, which this version\n")
			fmt.Fprintf(b, "  does not yet parse. Structured output is info level\n")
			fmt.Fprintf(b, "  only, so it never contains provider RPC entries. For\n")
			fmt.Fprintf(b, "  those, enable debug logging on the run: the log then\n")
			fmt.Fprintf(b, "  arrives as text rather than JSON, which this tool does\n")
			fmt.Fprintf(b, "  parse.\n")
		} else {
			fmt.Fprintf(b, "  This log contains no provider RPC entries, so there is\n")
			fmt.Fprintf(b, "  nothing to profile. If the plan ran on HCP Terraform,\n")
			fmt.Fprintf(b, "  enable debug logging on the run and use its raw log.\n")
		}
	}
	fmt.Fprintf(b, "  %-25s %d\n", "response entries", r.Caps.ResponseEntries)
	fmt.Fprintf(b, "  %-25s %d\n", "request entries", r.Caps.RequestEntries)
	fmt.Fprintf(b, "  %-25s %d\n", "response duration fields", r.Caps.DurationFields)
	fmt.Fprintf(b, "  %-25s %d\n", "req id fields", r.Caps.ReqIDFields)
	fmt.Fprintf(b, "  %-25s %d\n", "correlated req ids", r.Caps.CorrelatedReqIDs)
	fmt.Fprintf(b, "  %-25s %d\n", "provider entries", r.Caps.ProviderEntries)
	fmt.Fprintf(b, "  %-25s %d\n", "core vertex lines", r.Caps.CoreVertexLines)
	fmt.Fprintf(b, "  %-25s %d\n", "core GRPC lines", r.Caps.CoreGRPCLines)
	fmt.Fprintf(b, "  %-25s %d (fields are read from header lines only -- tier above may be conservative)\n\n",
		"continuation lines not parsed for fields", r.Stats.ContinuationLines)

	fmt.Fprintf(b, "SPANS\n")
	fmt.Fprintf(b, "  spans built          %d\n", r.SpanCount)
	fmt.Fprintf(b, "  slowest span         %d ms\n", r.SlowestMs)
	fmt.Fprintf(b, "  total span time (sum, overlaps) %d ms\n", r.TotalSpanMs)
	fmt.Fprintf(b, "  starts clamped       %d\n\n", r.ClampedSpans)

	if r.Stats.BackwardsTimestamps > 0 || r.InternOverflow > 0 || r.DroppedKeys > 0 || r.Stats.LinesSaturated > 0 {
		fmt.Fprintf(b, "ANOMALIES\n")
		fmt.Fprintf(b, "  backwards timestamps %d\n", r.Stats.BackwardsTimestamps)
		fmt.Fprintf(b, "  intern overflow      %d\n", r.InternOverflow)
		fmt.Fprintf(b, "  dropped map keys     %d\n", r.DroppedKeys)
		fmt.Fprintf(b, "  line-count saturated %d\n\n", r.Stats.LinesSaturated)
	}

	fmt.Fprintf(b, "COMPONENTS (%d distinct, recurring only, top %d)\n", r.DistinctComps, len(r.TopComponents))
	for _, t := range r.TopComponents {
		name := t.Text
		if name == "" {
			name = "(none)"
		}
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, name)
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
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, t.Text)
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
