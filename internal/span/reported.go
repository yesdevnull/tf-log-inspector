package span

import (
	"strconv"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// responseMarker is the message Terraform's plugin protocol layer logs when a
// provider RPC returns. It carries tf_req_duration_ms, the provider's own
// measurement of the call.
const responseMarker = "Received downstream response"

// ReportedBuilder is the tier 1 span builder. It reads durations directly off
// response entries rather than inferring them, so its spans are exact.
// It satisfies logfmt.Sink.
type ReportedBuilder struct {
	spans []Span
}

// Entry implements logfmt.Sink.
func (b *ReportedBuilder) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {
	if !strings.HasPrefix(msg, responseMarker) {
		return
	}
	raw, ok := f.Get("tf_req_duration_ms")
	if !ok {
		return
	}
	ms64, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return
	}
	ms := uint32(ms64)

	start, clamped := uint32(0), true
	if e.TSms > ms {
		start, clamped = e.TSms-ms, false
	}

	rpc, _ := f.Get("tf_rpc")
	provider, _ := f.Get("tf_provider_addr")
	resType, ok := f.Get("tf_resource_type")
	if !ok {
		resType, _ = f.Get("tf_data_source_type")
	}

	b.spans = append(b.spans, Span{
		Entry:        ord,
		StartMs:      start,
		EndMs:        e.TSms,
		DurationMs:   ms,
		StartClamped: clamped,
		RPC:          rpc,
		Provider:     provider,
		ResourceType: resType,
		Fidelity:     FidelityReported,
	})
}

// Spans returns the spans built so far, in the order they were logged.
func (b *ReportedBuilder) Spans() []Span { return b.spans }
