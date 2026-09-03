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

// maxDistinctValues bounds a dedupCache so a pathological log cannot grow it
// without limit. Past the cap, values are cloned individually: correctness
// never depends on the cap, only memory efficiency does.
const maxDistinctValues = 4096

// dedupCache deduplicates retained strings drawn from a tiny vocabulary --
// RPC names, provider addresses, resource types -- so that retained
// allocation is proportional to distinct values rather than to spans. Both
// ReportedBuilder and UIHookBuilder use it for exactly that shape of field.
// It is a defined map type rather than a wrapper struct so existing direct
// map operations (len, range) keep working on it unchanged.
type dedupCache map[string]string

// retain returns a copy of s that does not alias the scanner's per-line
// buffer. Sink.Entry's and StructuredSink.Structured's strings are valid
// only for the duration of the call, and a retained string would otherwise
// pin its whole source line alive.
func (c *dedupCache) retain(s string) string {
	if s == "" {
		return ""
	}
	if got, ok := (*c)[s]; ok {
		return got
	}
	clone := strings.Clone(s)
	if len(*c) < maxDistinctValues {
		if *c == nil {
			*c = make(dedupCache)
		}
		(*c)[clone] = clone
	}
	return clone
}

// ReportedBuilder is the tier 1 span builder. It reads durations directly off
// response entries rather than inferring them, so its spans are exact.
// It satisfies logfmt.Sink.
type ReportedBuilder struct {
	spans []Span
	kept  dedupCache // dedup cache for retained RPC/Provider/ResourceType strings
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
		RPC:          b.kept.retain(rpc),
		Provider:     b.kept.retain(provider),
		ResourceType: b.kept.retain(resType),
		Fidelity:     FidelityReported,
	})
}

// Spans returns the spans built so far, in the order they were logged.
func (b *ReportedBuilder) Spans() []Span { return b.spans }
