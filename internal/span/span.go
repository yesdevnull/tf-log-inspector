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
	//
	// A FidelityUIReported span's StartMs/EndMs are on a different timeline
	// from a FidelityReported span's: structured-output lines carry no
	// scanner-visible timestamp (Scan never decodes their JSON, so
	// Entry.TSms is always 0 for them), so UIHookBuilder tracks its own
	// baseline independently of Scan's. See the comment on Span's
	// StartMs/EndMs fields before comparing or merging spans across
	// fidelities by time.
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

// Span is one provider operation with a measured duration. StartMs and EndMs
// are milliseconds relative to a zero point that depends on which builder
// produced the span -- see the comment on those two fields below before
// comparing or merging spans built by more than one builder.
//
// DurationMs is stored rather than derived from StartMs and EndMs. A span
// whose duration exceeds its offset from the first entry has its start clamped
// to zero, and deriving the duration from the clamped start would silently
// shrink it -- which would hit the early GetProviderSchema and Configure calls
// that are most often the slow ones.
type Span struct {
	Entry uint32 // ordinal of the entry that closed this span

	// StartMs and EndMs are milliseconds relative to a zero point that is
	// per-builder, not per-log. ReportedBuilder anchors to Entry.TSms, the
	// offset Scan itself computes from the first hclog entry it timestamps
	// -- the only clock the hclog scanner exposes. UIHookBuilder cannot use
	// that clock: a structured-output line is never Timestamped and its
	// Entry.TSms is always 0, because Scan never decodes a structured
	// line's JSON to find a timestamp inside it. UIHookBuilder therefore
	// tracks its own baseline, independently, from the first @timestamp it
	// successfully parses.
	//
	// The consequence: when spans from both builders exist side by side
	// (nothing does this yet -- only Sniffer is wired into the CLI today),
	// their StartMs/EndMs sit on two different timelines with two
	// different zero points, even though every individual number looks
	// equally plausible. They are directly comparable only within spans
	// sharing the same Fidelity. Concatenating spans from both builders and
	// sorting or comparing by StartMs/EndMs without first re-basing one
	// timeline onto the other produces a silently wrong ordering.
	// DurationMs has no such hazard, since it is a duration rather than a
	// point in time, and is safe to compare across builders.
	StartMs uint32 // clamped at zero; see StartClamped
	EndMs   uint32

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
