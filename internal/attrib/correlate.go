package attrib

import (
	"fmt"
	"slices"
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
	// Module is empty for observed root and unavailable metadata alike;
	// ModuleKnown and ModuleInvalid distinguish those evidence states.
	Module        string
	ModuleKnown   bool
	ModuleInvalid bool
	Name          string
	// Key is "" when the resource has no index key, and otherwise carried
	// straight through from Context.Key -- already in the bracket syntax
	// Terraform's own address uses, so a caller building one concatenates
	// Name (or Module) directly against "[" + Key + "]" (see decodeKey).
	Key string
	// IsData is carried straight through from Context.IsData, rather than
	// re-derived from Address: a caller deriving it from a "data." prefix
	// would have to account for the module segments a moduled address puts
	// before that prefix, which this field lets it skip entirely. Without
	// it a data source and a managed resource of the same type render
	// identically, since Name/Module/Key alone do not say which one this is.
	IsData bool

	// Candidates is the number of overlapping candidates CONSIDERED, which
	// has a well-defined value in every state: 1 for Contained and
	// Overlapping, N for Likely and Ambiguous, 0 for Unattributed.
	Candidates uint32
	Confidence Confidence
	// PositionUnavailable means the span has a measured duration but no
	// trustworthy point on the log clock, so temporal attribution could not
	// safely consider any address context.
	PositionUnavailable bool
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

// actionMatches reports whether ctxAction is one this RPC can belong to. An
// empty ctxAction -- a refresh context, whose hook carries no action field
// at all (see opensContext) -- matches any RPC. This is the exact mirror of
// the rule below it: an RPC absent from rpcActions matches any action.
// Both decline to constrain a match on information the log does not carry,
// rather than invent one -- asserting that a refresh means "read" would be
// a mapping nothing in the log states, not an absence of one.
func actionMatches(rpc, ctxAction string) bool {
	if ctxAction == "" {
		return true
	}
	allowed, mapped := rpcActions[rpc]
	if !mapped {
		return true
	}
	return slices.Contains(allowed, ctxAction)
}

// overlaps reports whether two half-open intervals share any instant. An
// EMPTY interval occupies no instant, so it overlaps nothing -- including
// another empty interval at the same point. Both emptiness checks are
// necessary: without them a zero-extent interval inside another would report
// an overlap it does not have.
//
// This rejects an empty SPAN interval too, which is correct for a
// zero-extent CONTEXT (a truncation artefact) but not for a genuinely
// instantaneous RPC (a real sub-millisecond call, StartMs == EndMs): see
// pointOverlaps and its use in correlateOne, which is what a degenerate span
// is tested against instead of this function.
func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	if !aStart.Before(aEnd) || !bStart.Before(bEnd) {
		return false
	}
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// pointOverlaps reports whether the instant t falls inside a context's
// half-open window [c.Start, c.End). It is the point-membership analogue of
// overlaps, used only for a genuinely instantaneous span -- one whose
// StartMs equals its EndMs, a real observation (tf_req_duration_ms == 0) and
// not the truncation artefact a zero-extent CONTEXT is. overlaps excludes
// every empty interval on either side; this exists so that exclusion does
// not also fall on the span side, where it would wrongly discard an instant
// that plainly happened.
//
// A zero-extent CONTEXT still matches nothing here, with no separate guard:
// c.Start == c.End makes t.Before(c.End) and !t.Before(c.Start) mutually
// exclusive, so the same rule that admits a real instant excludes the
// truncation-artefact case too.
func pointOverlaps(t time.Time, c *Context) bool {
	return !t.Before(c.Start) && t.Before(c.End)
}

// contains reports whether the half-open interval [oStart, oEnd) encloses
// [iStart, iEnd). An empty interval neither contains nor is contained.
//
// That guard is unreachable from this function's sole call site in
// correlateOne, which only ever calls contains after overlaps has already
// confirmed both intervals are non-empty. It is kept anyway, for symmetry
// with overlaps and so contains stays correct if a future caller reaches it
// without going through that guard first -- not because it fires today. A
// test written to exercise it here would find nothing broken.
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
	if !s.HasPosition() {
		return Attribution{Confidence: Unattributed, PositionUnavailable: true}
	}

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

	// A genuinely instantaneous span -- StartMs == EndMs, a real
	// tf_req_duration_ms == 0 -- is a real observation of an instant, not
	// the truncation artefact a zero-extent CONTEXT is (see pointOverlaps).
	// capped is excluded here because its start was already moved to
	// end-1ms above, one millisecond before end, so it is never degenerate.
	degenerate := !capped && !start.Before(end)

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

		var matched, contained bool
		if degenerate {
			matched = pointOverlaps(start, c)
			// An instant that is a member of a context's window is,
			// definitionally, contained by it -- there is no weaker
			// "overlaps but does not contain" outcome for a single instant
			// the way there is for an interval.
			contained = matched
		} else {
			matched = overlaps(start, end, c.Start, c.End)
			contained = matched && contains(c.Start, c.End, start, end)
		}
		if !matched {
			continue
		}
		n++
		if lone == nil {
			lone = c
		}
		if contained {
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

// named is the only function that populates an Attribution's address fields,
// and correlateOne never calls it with Ambiguous or Unattributed -- the spec
// is emphatic that an ambiguous span must never assert an address ("a wrong
// name marked ? is still a wrong name"). That invariant currently rests
// entirely on correlateOne's switch, so it is pinned here too: a future
// change to that switch that slips one of these two confidences past this
// guard fails loudly at construction, the same way this package treats every
// other programming error (see tui.unhandledView).
func named(c Context, n uint32, conf Confidence) Attribution {
	switch conf {
	case Ambiguous, Unattributed:
		panic(fmt.Sprintf("attrib: named called with %s, which must never carry an address", conf))
	}
	return Attribution{
		Address:       c.Address,
		Module:        c.Module,
		ModuleKnown:   c.ModuleKnown,
		ModuleInvalid: c.ModuleInvalid,
		Name:          c.Name,
		Key:           c.Key,
		IsData:        c.IsData,
		Candidates:    n,
		Confidence:    conf,
	}
}
