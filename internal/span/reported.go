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
	// Comps is optional. When set, it enables the fallback described on
	// bareProviderAddr; when nil the raw tf_provider_addr is used as-is, so
	// a zero-value builder stays usable.
	Comps *logfmt.Interner

	spans []Span
	kept  dedupCache // dedup cache for retained RPC/Provider/ResourceType strings
}

// bareProviderAddr is what terraform-plugin-sdk reports for tf_provider_addr
// when a provider is served without a ProviderAddr in its ServeOpts. Measured
// on terraform-provider-github v6.3.1 across 1,104 lines of a real HCP log;
// the provider fixed it in v6.13.0 by setting ProviderAddr explicitly.
//
// The value is not merely ugly. Every provider that omits ProviderAddr
// reports the same string, so two of them in one plan would collapse into a
// single rollup row and attribute one provider's time to the other. The
// component -- "provider.terraform-provider-github_v6.3.1" -- names the
// plugin binary unambiguously, so it stands in. The component is used
// verbatim rather than parsed into a registry address: the binary's name does
// not reliably yield its namespace, and inventing one would be a guess
// presented as a measurement.
const bareProviderAddr = "provider"

// componentPrefix marks a component that names a provider plugin. The
// fallback is restricted to these so a core component can never be mistaken
// for a provider address.
const componentPrefix = "provider."

// providerAddr resolves the provider address for an entry, substituting the
// component when the log carries only the useless bare string.
func (b *ReportedBuilder) providerAddr(addr string, e logfmt.Entry) string {
	if addr != bareProviderAddr || b.Comps == nil {
		return addr
	}
	comp := b.Comps.Lookup(e.Comp)
	if !strings.HasPrefix(comp, componentPrefix) {
		return addr
	}
	return comp
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
	if e.TSms >= ms { // inclusive: an exact-base start is 0, not clamped
		start, clamped = e.TSms-ms, false
	}

	rpc, _ := f.Get("tf_rpc")
	provider, _ := f.Get("tf_provider_addr")
	provider = b.providerAddr(provider, e)
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
