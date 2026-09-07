package span

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

const requestMarker = "Sending request downstream"

// maxTrackedReqIDs bounds the request-id correlation set so a pathological
// log cannot grow it without limit -- the same bound as the dedup caches
// elsewhere in this package. Past the cap, additional request ids are not
// tracked, so CorrelatedReqIDs under-reports rather than the set growing
// without limit, which is the safe direction.
const maxTrackedReqIDs = 4096

// Capabilities records what evidence a log contains for each extraction tier.
// It answers "what could this log support", which is a different question from
// "what spans were built", and is the core of the diagnostic report.
type Capabilities struct {
	ResponseEntries   uint64 // "Received downstream response" entries
	RequestEntries    uint64 // "Sending request downstream" entries
	DurationFields    uint64 // response entries carrying tf_req_duration_ms
	ReqIDFields       uint64 // entries carrying tf_req_id
	CorrelatedReqIDs  uint64 // response entries whose tf_req_id was also seen on a request entry
	ProviderEntries   uint64 // entries whose component starts with "provider."
	CoreVertexLines   uint64 // core graph-walk lines naming a resource address
	CoreGRPCLines     uint64 // core "GRPCProvider: <RPC>" lines
	UIHookCompletions uint64 // structured-output completion-bearing hook lines, UIHookBuilder's precondition

	// DistinctReqIDs is how many different tf_req_id values the log carries,
	// and Min/Median/MaxEntriesPerReqID the spread of how many entries each
	// of them appears on. Together they size what grouping a log by request
	// id would actually yield.
	//
	// The spread is reported rather than a mean because the mean cannot tell
	// the two shapes apart that matter: a log where every call carries eleven
	// entries and one where most carry two and a handful carry hundreds share
	// a mean and mean opposite things for anything that shows a reader one
	// call.
	//
	// All four count HEADER-borne ids only, as everything reading fields
	// does. An id written onto a continuation line is invisible here, and
	// Stats.ContinuationOnlyReqIDEntries is what sizes that blind spot --
	// the two are read together or not at all.
	//
	// All four are capped at maxTrackedReqIDs distinct ids. ReqIDTrackingFull
	// says the cap was reached, so they under-report -- which is the safe
	// direction, but only if the reader is told, since a ceiling read as a
	// measurement is worse than no measurement.
	DistinctReqIDs        uint64
	MinEntriesPerReqID    uint64
	MedianEntriesPerReqID uint64
	MaxEntriesPerReqID    uint64
	ReqIDTrackingFull     bool
}

// BestFidelity reports the highest-fidelity tier this log can support, and
// whether any tier is usable at all. UIHookCompletions ranks below
// DurationFields (RPC-level evidence is finer-grained than per-resource
// evidence) but above every other tier: in practice the two never coexist in
// one log, because HCP's structured output is info level only and enabling
// debug logging to get RPC-level evidence replaces the JSON stream with
// hclog text.
func (c Capabilities) BestFidelity() (Fidelity, bool) {
	switch {
	case c.DurationFields > 0:
		return FidelityReported, true
	case c.UIHookCompletions > 0:
		return FidelityUIReported, true
	case c.CorrelatedReqIDs > 0:
		return FidelityPaired, true
	case c.RequestEntries > 0 && c.ResponseEntries > 0:
		return FidelitySequential, true
	case c.ProviderEntries > 1:
		return FidelityInferred, true
	}
	return FidelityReported, false
}

// Sniffer accumulates Capabilities during a scan. It satisfies logfmt.Sink.
// Construct it with NewSniffer: a zero value panics on first use, because its
// interner is nil. NewSniffer is the only supported construction.
type Sniffer struct {
	caps   Capabilities
	comps  *logfmt.Interner
	reqIDs map[string]struct{} // request-side tf_req_id seen, capped at maxTrackedReqIDs
	// reqIDCounts is entries-per-id over EVERY entry carrying one, where
	// reqIDs above holds only the request side. They answer different
	// questions -- correlation needs to know an id was requested, the spread
	// needs to know how much traffic wears it -- so they are two sets rather
	// than one carrying a flag.
	reqIDCounts map[string]uint32
	// reqIDsFull records that a new id was refused for the cap, so the
	// figures derived from reqIDCounts can say they under-report.
	reqIDsFull bool
}

// NewSniffer returns a Sniffer resolving component ids via comps.
func NewSniffer(comps *logfmt.Interner) *Sniffer { return &Sniffer{comps: comps} }

// Entry implements logfmt.Sink.
func (s *Sniffer) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {
	comp := s.comps.Lookup(e.Comp)
	if strings.HasPrefix(comp, "provider.") {
		s.caps.ProviderEntries++
	}
	if comp == "GRPCProvider" {
		s.caps.CoreGRPCLines++
	}

	isResponse := strings.HasPrefix(msg, responseMarker)
	isRequest := strings.HasPrefix(msg, requestMarker)
	switch {
	case isResponse:
		s.caps.ResponseEntries++
	case isRequest:
		s.caps.RequestEntries++
	}

	// DurationFields is exactly ReportedBuilder's precondition for building a
	// span: a tf_req_duration_ms field elsewhere on the line (or on a
	// non-response entry) never produces a span, so it must not count here.
	if isResponse {
		if _, ok := f.Get("tf_req_duration_ms"); ok {
			s.caps.DurationFields++
		}
	}

	reqID, hasReqID := f.Get("tf_req_id")
	if hasReqID {
		s.caps.ReqIDFields++
		s.countReqID(reqID)
	}
	switch {
	case isRequest && hasReqID:
		s.trackRequestID(reqID)
	case isResponse && hasReqID:
		if _, seen := s.reqIDs[reqID]; seen {
			s.caps.CorrelatedReqIDs++
		}
	}

	if strings.HasPrefix(msg, `vertex "`) {
		s.caps.CoreVertexLines++
	}
}

// trackRequestID records id as seen on the request side, so a later response
// carrying the same id can be counted as genuinely correlated rather than
// merely coexisting with unrelated requests and responses. The set is capped
// at maxTrackedReqIDs; past the cap, additional ids are silently not
// tracked -- CorrelatedReqIDs then under-reports rather than the set growing
// without limit. id is cloned because Sink.Entry's strings are valid only for
// the call, and a retained substring would otherwise pin its whole source
// line.
func (s *Sniffer) trackRequestID(id string) {
	if _, ok := s.reqIDs[id]; ok {
		return
	}
	if len(s.reqIDs) >= maxTrackedReqIDs {
		return
	}
	if s.reqIDs == nil {
		s.reqIDs = make(map[string]struct{})
	}
	s.reqIDs[strings.Clone(id)] = struct{}{}
}

// Structured implements logfmt.StructuredSink. It counts completion-bearing
// UI-hook lines carrying a resource -- exactly UIHookBuilder's precondition
// for building a span -- without retaining anything from the line itself.
// A line that fails to decode, or decodes but is not a completion-bearing
// hook line, is silently not counted: UIHookBuilder is the place that counts
// malformed lines, since Capabilities only answers "what could this log
// support".
func (s *Sniffer) Structured(ord uint32, e logfmt.Entry, line string) {
	var ul uiLine
	if err := json.Unmarshal([]byte(line), &ul); err != nil {
		return
	}
	if !isCompletionType(ul.Type) {
		return
	}
	if ul.Hook == nil || ul.Hook.Resource == nil {
		return
	}
	s.caps.UIHookCompletions++
}

// countReqID records one more entry carrying id.
//
// An id already tracked always counts, cap or no cap: refusing it would
// leave a tracked id's own total wrong, which is worse than a short set. It
// is only a NEW id past the cap that is refused, and that sets the flag the
// report reads.
//
// id is cloned on first insertion for the reason trackRequestID clones:
// Sink.Entry's strings are valid only for the call, and a retained substring
// pins its whole source line.
func (s *Sniffer) countReqID(id string) {
	if n, ok := s.reqIDCounts[id]; ok {
		s.reqIDCounts[id] = n + 1
		return
	}
	if len(s.reqIDCounts) >= maxTrackedReqIDs {
		s.reqIDsFull = true
		return
	}
	if s.reqIDCounts == nil {
		s.reqIDCounts = make(map[string]uint32)
	}
	s.reqIDCounts[strings.Clone(id)] = 1
}

// Report returns the accumulated capabilities, with the request-id spread
// derived from the tracked counts. It is derived here rather than maintained
// per entry because a median cannot be: it needs the whole set.
func (s *Sniffer) Report() Capabilities {
	c := s.caps
	c.DistinctReqIDs = uint64(len(s.reqIDCounts))
	c.ReqIDTrackingFull = s.reqIDsFull
	if len(s.reqIDCounts) == 0 {
		return c
	}
	counts := make([]uint32, 0, len(s.reqIDCounts))
	for _, n := range s.reqIDCounts {
		counts = append(counts, n)
	}
	slices.Sort(counts)
	c.MinEntriesPerReqID = uint64(counts[0])
	c.MaxEntriesPerReqID = uint64(counts[len(counts)-1])
	c.MedianEntriesPerReqID = uint64(counts[len(counts)/2])
	return c
}
