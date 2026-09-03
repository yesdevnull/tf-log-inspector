package span

import (
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

const requestMarker = "Sending request downstream"

// Capabilities records what evidence a log contains for each extraction tier.
// It answers "what could this log support", which is a different question from
// "what spans were built", and is the core of the diagnostic report.
type Capabilities struct {
	ResponseEntries uint64 // "Received downstream response" entries
	RequestEntries  uint64 // "Sending request downstream" entries
	DurationFields  uint64 // entries carrying tf_req_duration_ms
	ReqIDFields     uint64 // entries carrying tf_req_id
	ProviderEntries uint64 // entries whose component starts with "provider."
	CoreVertexLines uint64 // core graph-walk lines naming a resource address
	CoreGRPCLines   uint64 // core "GRPCProvider: <RPC>" lines
}

// BestFidelity reports the highest-fidelity tier this log can support, and
// whether any tier is usable at all.
func (c Capabilities) BestFidelity() (Fidelity, bool) {
	switch {
	case c.DurationFields > 0:
		return FidelityReported, true
	case c.RequestEntries > 0 && c.ResponseEntries > 0 && c.ReqIDFields > 0:
		return FidelityPaired, true
	case c.ResponseEntries > 0:
		return FidelitySequential, true
	case c.ProviderEntries > 0:
		return FidelityInferred, true
	}
	return FidelityReported, false
}

// Sniffer accumulates Capabilities during a scan. It satisfies logfmt.Sink.
// Construct it with NewSniffer: it needs the interner to resolve components.
type Sniffer struct {
	caps  Capabilities
	comps *logfmt.Interner
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
	switch {
	case strings.HasPrefix(msg, responseMarker):
		s.caps.ResponseEntries++
	case strings.HasPrefix(msg, requestMarker):
		s.caps.RequestEntries++
	}
	if _, ok := f.Get("tf_req_duration_ms"); ok {
		s.caps.DurationFields++
	}
	if _, ok := f.Get("tf_req_id"); ok {
		s.caps.ReqIDFields++
	}
	if strings.HasPrefix(msg, `vertex "`) {
		s.caps.CoreVertexLines++
	}
}

// Report returns the accumulated capabilities.
func (s *Sniffer) Report() Capabilities { return s.caps }
