// Package span turns parsed log entries into timed provider operations.
package span

// Fidelity records how a span's duration was established. It is surfaced in
// the UI so an inferred number is never mistaken for a measured one.
type Fidelity uint8

const (
	// FidelityReported means the provider logged its own duration.
	FidelityReported Fidelity = iota
	// FidelityUIReported means Terraform's structured-output UI hook stream
	// logged its own per-resource duration. It ranks below FidelityReported:
	// an RPC-level measurement is finer-grained than a per-resource one. In
	// practice the two never coexist in one log, because HCP's structured
	// output is info level only -- enabling debug logging to get RPC-level
	// evidence replaces the JSON stream with hclog text.
	FidelityUIReported
	// FidelityPaired means request and response lines were correlated by id.
	FidelityPaired
	// FidelitySequential means calls were paired within one plugin stream.
	FidelitySequential
	// FidelityInferred means the duration is a wall-clock gap attribution.
	FidelityInferred
)

func (f Fidelity) String() string {
	switch f {
	case FidelityReported:
		return "reported"
	case FidelityUIReported:
		return "ui-reported"
	case FidelityPaired:
		return "paired"
	case FidelitySequential:
		return "sequential"
	case FidelityInferred:
		return "inferred"
	}
	return "unknown"
}

// Span is one provider operation with a measured duration. Times are
// milliseconds relative to the first timestamped entry in the log.
//
// DurationMs is stored rather than derived from StartMs and EndMs. A span
// whose duration exceeds its offset from the first entry has its start clamped
// to zero, and deriving the duration from the clamped start would silently
// shrink it -- which would hit the early GetProviderSchema and Configure calls
// that are most often the slow ones.
type Span struct {
	Entry        uint32 // ordinal of the entry that closed this span
	StartMs      uint32 // clamped at zero; see StartClamped
	EndMs        uint32
	DurationMs   uint32 // as reported by the provider
	StartClamped bool
	RPC          string
	Provider     string
	ResourceType string
	// Address is the Terraform resource address (e.g.
	// module.m["key"].aws_instance.foo). It is populated only for spans
	// built from Terraform's structured-output UI hook stream, where the
	// address is the only per-resource identifier available.
	Address  string
	Fidelity Fidelity
}
