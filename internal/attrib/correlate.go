package attrib

import (
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Confidence records how well an address was established for a span. The
// zero value is Unattributed deliberately: an Attribution nobody filled in
// must claim nothing, not claim containment.
type Confidence uint8

const (
	// Unattributed means context was available in this log but no candidate
	// matched this span. It is distinct from "this log has no address
	// context at all", which is a property of the log rather than of a span
	// and is why no attribution table is built for such a log.
	Unattributed Confidence = iota
	// Ambiguous means several candidates overlapped and none uniquely
	// contained the span. It never carries an address.
	Ambiguous
	// Overlapping means exactly one candidate overlapped without containing
	// the span. Weaker than Likely: the geometry supports the association
	// but not the enclosure.
	Overlapping
	// Likely means several candidates overlapped and exactly one contained
	// the span. Containment is a QUALITATIVE difference between candidates,
	// which is why "materially better" needs no tuned ratio.
	Likely
	// Contained means exactly one candidate overlapped and it contained the
	// span entirely. This is the strongest verdict inference offers, and it
	// is deliberately not called "Exact" -- that is the word the extraction
	// tiers use for an OBSERVED duration, and inference does not get to
	// borrow observation's vocabulary.
	Contained
)

func (c Confidence) String() string {
	switch c {
	case Unattributed:
		return "unattributed"
	case Ambiguous:
		return "ambiguous"
	case Overlapping:
		return "overlapping"
	case Likely:
		return "likely"
	case Contained:
		return "contained"
	}
	return "unknown"
}

// Attribution labels one RPC span. It is held in a slice parallel to
// model.Log.RPCSpans specifically -- not to a concatenation of RPCSpans and
// UISpans, which model deliberately keeps apart because their StartMs sit on
// different zero points. UISpans need no attribution: their Address is
// observed.
type Attribution struct {
	// Address is "" for Ambiguous and Unattributed. An Ambiguous span
	// reports Candidates instead: naming one of several equally plausible
	// resources would assert something the evidence does not support.
	Address string
	Module  string // "" when the resource is not in a module
	Name    string
	// Key is "" when the resource has no index key, and otherwise carried
	// straight through from Context.Key -- already in the bracket syntax
	// Terraform's own address uses, so a caller building one concatenates
	// Name (or Module) directly against "[" + Key + "]" (see decodeKey).
	Key string

	// Candidates is the number of overlapping candidates CONSIDERED, which
	// has a well-defined value in every state: 1 for Contained and
	// Overlapping, N for Likely and Ambiguous, 0 for Unattributed.
	Candidates uint32
	Confidence Confidence
}

// rpcActions maps a provider RPC name to the UI-hook actions it can belong
// to. An RPC absent from this map matches ANY action: this map cannot be
// complete for RPCs nobody has catalogued, and excluding an unmapped name
// would silently drop that span's time rather than reporting it as
// attributable.
var rpcActions = map[string][]string{
	"ReadResource":        {"read"},
	"ReadDataSource":      {"read"},
	"PlanResourceChange":  {"create", "update", "delete"},
	"ApplyResourceChange": {"create", "update", "delete"},
}

// actionMatches reports whether ctxAction is one this RPC can belong to.
func actionMatches(rpc, ctxAction string) bool {
	allowed, mapped := rpcActions[rpc]
	if !mapped {
		return true
	}
	for _, a := range allowed {
		if a == ctxAction {
			return true
		}
	}
	return false
}

// overlaps reports whether two half-open intervals share any instant. An
// EMPTY interval occupies no instant, so it overlaps nothing -- including
// another empty interval at the same point. Both emptiness checks are
// necessary: without them a zero-extent interval inside another would report
// an overlap it does not have.
func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	if !aStart.Before(aEnd) || !bStart.Before(bEnd) {
		return false
	}
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// contains reports whether the half-open interval [oStart, oEnd) encloses
// [iStart, iEnd). An empty interval neither contains nor is contained.
func contains(oStart, oEnd, iStart, iEnd time.Time) bool {
	if !oStart.Before(oEnd) || !iStart.Before(iEnd) {
		return false
	}
	return !iStart.Before(oStart) && !iEnd.After(oEnd)
}

// Correlate attributes each span to an address context, returning one
// Attribution per span in the same order. base is logfmt.Stats.FirstTS, the
// zero point of the spans' StartMs/EndMs; spans are converted to absolute
// times so the two inputs meet on the wall clock. No span is modified:
// model.PackLanes and PeakConcurrency refuse a mixed-fidelity slice on the
// premise that StartMs is not comparable across builders, and rewriting it
// here would falsify that premise while leaving Fidelity unchanged.
func Correlate(spans []span.Span, base time.Time, ctxs []Context) []Attribution {
	out := make([]Attribution, len(spans))
	for i, s := range spans {
		out[i] = correlateOne(s, base, ctxs)
	}
	return out
}

func correlateOne(s span.Span, base time.Time, ctxs []Context) Attribution {
	start := base.Add(time.Duration(s.StartMs) * time.Millisecond)
	end := base.Add(time.Duration(s.EndMs) * time.Millisecond)

	// A clamped span's start is fabricated -- ReportedBuilder set it to zero
	// because the duration exceeded the offset from the log's first entry --
	// so its [0, End) window would overlap nearly every context in the log
	// and could appear to be contained by several. It is correlated on its
	// end instant alone, as a one-millisecond probe, and capped at
	// Overlapping. A clamped span always has DurationMs >= 1, so the probe
	// is never empty.
	capped := s.StartClamped
	if capped {
		start = end.Add(-time.Millisecond)
	}

	var (
		n         uint32
		lone      *Context
		container *Context
		multiple  bool
	)
	for i := range ctxs {
		c := &ctxs[i]
		if c.ResourceType != s.ResourceType {
			continue
		}
		if c.IsData != (s.RPC == "ReadDataSource") {
			continue
		}
		if !actionMatches(s.RPC, c.Action) {
			continue
		}
		if !overlaps(start, end, c.Start, c.End) {
			continue
		}
		n++
		if lone == nil {
			lone = c
		}
		if contains(c.Start, c.End, start, end) {
			if container != nil {
				multiple = true
			}
			container = c
		}
	}

	switch {
	case n == 0:
		return Attribution{Confidence: Unattributed}
	case capped:
		// The cap applies whatever the geometry says: the start that would
		// justify a stronger verdict is not a fact the log states.
		if n > 1 {
			return Attribution{Candidates: n, Confidence: Ambiguous}
		}
		return named(*lone, n, Overlapping)
	case n == 1:
		if container != nil {
			return named(*lone, n, Contained)
		}
		return named(*lone, n, Overlapping)
	case container != nil && !multiple:
		return named(*container, n, Likely)
	default:
		return Attribution{Candidates: n, Confidence: Ambiguous}
	}
}

func named(c Context, n uint32, conf Confidence) Attribution {
	return Attribution{
		Address:    c.Address,
		Module:     c.Module,
		Name:       c.Name,
		Key:        c.Key,
		Candidates: n,
		Confidence: conf,
	}
}
