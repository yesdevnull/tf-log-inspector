package span

import (
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
	ResponseEntries  uint64 // "Received downstream response" entries
	RequestEntries   uint64 // "Sending request downstream" entries
	DurationFields   uint64 // response entries carrying tf_req_duration_ms
	ReqIDFields      uint64 // entries carrying tf_req_id
	CorrelatedReqIDs uint64 // response entries whose tf_req_id was also seen on a request entry
	ProviderEntries  uint64 // entries whose component starts with "provider."
	CoreVertexLines  uint64 // core graph-walk lines naming a resource address
	CoreGRPCLines    uint64 // core "GRPCProvider: <RPC>" lines
}

// BestFidelity reports the highest-fidelity tier this log can support, and
// whether any tier is usable at all.
func (c Capabilities) BestFidelity() (Fidelity, bool) {
	switch {
	case c.DurationFields > 0:
		return FidelityReported, true
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

// Report returns the accumulated capabilities.
func (s *Sniffer) Report() Capabilities { return s.caps }
