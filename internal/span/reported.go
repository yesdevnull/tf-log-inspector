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

// maxDistinctValues bounds the dedup cache so a pathological log cannot grow
// it without limit. Past the cap, values are cloned individually: correctness
// never depends on the cap, only memory efficiency does.
const maxDistinctValues = 4096

// ReportedBuilder is the tier 1 span builder. It reads durations directly off
// response entries rather than inferring them, so its spans are exact.
// It satisfies logfmt.Sink.
type ReportedBuilder struct {
	spans []Span
	kept  map[string]string // dedup cache for retained RPC/Provider/ResourceType strings
}

// retain returns a copy of s that does not alias the scanner's per-line
// buffer. Sink.Entry's strings are valid only for the duration of the call,
// and a retained string would otherwise pin its whole source line -- RPC,
// Provider and ResourceType are drawn from a tiny vocabulary, so deduplicating
// makes retained allocation proportional to distinct values rather than to
// spans.
func (b *ReportedBuilder) retain(s string) string {
	if s == "" {
		return ""
	}
	if got, ok := b.kept[s]; ok {
		return got
	}
	c := strings.Clone(s)
	if len(b.kept) < maxDistinctValues {
		if b.kept == nil {
			b.kept = make(map[string]string)
		}
		b.kept[c] = c
	}
	return c
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
		RPC:          b.retain(rpc),
		Provider:     b.retain(provider),
		ResourceType: b.retain(resType),
		Fidelity:     FidelityReported,
	})
}

// Spans returns the spans built so far, in the order they were logged.
func (b *ReportedBuilder) Spans() []Span { return b.spans }
