package span

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// uiLine is the subset of Terraform's structured-output (terraform.ui JSON)
// line schema this package needs, verified against hashicorp/terraform's
// internal/command/views/json/hook.go and message_types.go. It deliberately
// has no field for "@message", "id_key" or "id_value": encoding/json ignores
// unknown JSON keys rather than erroring on them, so those three -- which
// between them can carry a full resource address and its id value -- are
// never materialised at all. That makes the disclosure guarantee a property
// of this struct's shape, not of code elsewhere remembering not to read
// them.
type uiLine struct {
	Timestamp string `json:"@timestamp"`
	Type      string `json:"type"`
	Hook      *struct {
		Resource *struct {
			Addr            string `json:"addr"`
			ResourceType    string `json:"resource_type"`
			ImpliedProvider string `json:"implied_provider"`
		} `json:"resource"`
		Action  string  `json:"action"`
		Elapsed float64 `json:"elapsed_seconds"`
	} `json:"hook"`
}

// isCompletionType reports whether t is a UI-hook type carrying a real,
// total elapsed_seconds. apply_progress is deliberately excluded: it also
// carries elapsed_seconds, but as a partial "still working" figure -- taking
// it as a completion would double-count and inflate durations.
func isCompletionType(t string) bool {
	switch t {
	case "apply_complete", "apply_errored",
		"ephemeral_op_complete", "ephemeral_op_errored",
		"provision_complete", "provision_errored":
		return true
	}
	return false
}

// UIHookBuilder builds spans from Terraform's structured-output UI hook
// stream, one span per completion-bearing hook line. It satisfies
// logfmt.StructuredSink, and Entry (a no-op) so it also satisfies
// logfmt.Sink and can be passed to logfmt.Scan directly.
type UIHookBuilder struct {
	spans     []Span
	kept      dedupCache // dedup cache for retained ResourceType/Provider/RPC strings, shared with ReportedBuilder
	malformed uint64

	base      time.Time // first parseable @timestamp seen, any line
	haveBase  bool
	backwards uint64 // timestamps earlier than base, clamped to 0 rather than wrapping
	saturated uint64 // durations that hit math.MaxUint32 rather than overflowing
}

// Entry implements logfmt.Sink as a no-op: UIHookBuilder only cares about
// structured lines, delivered via Structured, but every sink passed to
// logfmt.Scan must satisfy Sink.
func (b *UIHookBuilder) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {}

// relativeMs parses ts as RFC3339Nano -- the format Terraform's
// structured-output stream uses -- and returns its offset in milliseconds
// from the first successfully parsed timestamp this builder has seen,
// mirroring how logfmt.Scan establishes its own baseline from the first
// timestamped hclog entry. A line whose timestamp will not parse yields 0:
// the duration is the valuable part of a UI-hook span, so a missing offset
// must not discard the whole span. A timestamp earlier than the base clamps
// to 0 rather than wrapping the unsigned result, the same rule
// logfmt.Scan applies to backwards timestamps.
func (b *UIHookBuilder) relativeMs(ts string) uint32 {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	if !b.haveBase {
		b.base, b.haveBase = t, true
	}
	delta := t.Sub(b.base).Milliseconds()
	switch {
	case delta < 0:
		// Concurrent goroutines can emit out of order, the same rationale
		// logfmt.Scan's own BackwardsTimestamps counter documents. Counted
		// here, not just clamped, because a silent clamp shortens the
		// derived UI-hook wall-clock without any visible trace of why.
		b.backwards++
		return 0
	case delta > math.MaxUint32:
		return math.MaxUint32
	}
	return uint32(delta)
}

// Structured implements logfmt.StructuredSink.
func (b *UIHookBuilder) Structured(ord uint32, e logfmt.Entry, line string) {
	var ul uiLine
	if err := json.Unmarshal([]byte(line), &ul); err != nil {
		b.malformed++
		return
	}

	endMs := b.relativeMs(ul.Timestamp)

	if !isCompletionType(ul.Type) {
		return
	}
	if ul.Hook == nil || ul.Hook.Resource == nil {
		return
	}

	// DurationMs is stored, never derived, exactly as tf_req_duration_ms is
	// for ReportedBuilder: guard against a negative elapsed_seconds (should
	// not occur, but a stored duration must never be allowed to underflow
	// the unsigned field) and against overflowing uint32 once scaled to
	// milliseconds.
	var durationMs uint32
	if ul.Hook.Elapsed > 0 {
		scaled := math.Round(ul.Hook.Elapsed * 1000)
		if scaled > math.MaxUint32 {
			durationMs = math.MaxUint32
			// Counted, not just clamped: an unmarked saturation is
			// indistinguishable from a real multi-million-second duration,
			// and silently poisons any sum (UI-hook total time, the
			// per-type rollup) it is folded into.
			b.saturated++
		} else {
			durationMs = uint32(scaled)
		}
	}

	start, clamped := uint32(0), true
	if endMs > durationMs {
		start, clamped = endMs-durationMs, false
	}

	b.spans = append(b.spans, Span{
		Entry:        ord,
		StartMs:      start,
		EndMs:        endMs,
		DurationMs:   durationMs,
		StartClamped: clamped,
		RPC:          b.kept.retain(ul.Hook.Action),
		Provider:     b.kept.retain(ul.Hook.Resource.ImpliedProvider),
		ResourceType: b.kept.retain(ul.Hook.Resource.ResourceType),
		Address:      strings.Clone(ul.Hook.Resource.Addr),
		Fidelity:     FidelityUIReported,
	})
}

// Spans returns the spans built so far, in the order their lines appeared.
func (b *UIHookBuilder) Spans() []Span { return b.spans }

// Malformed reports how many structured lines failed to decode as JSON. Such
// a line is skipped, never fatal to the scan.
func (b *UIHookBuilder) Malformed() uint64 { return b.malformed }

// BackwardsTimestamps reports how many UI-hook lines carried a timestamp
// earlier than this builder's base. Each one clamps to a 0 offset rather
// than wrapping, which silently shortens the derived UI-hook wall-clock, so
// this is what lets that be surfaced instead of hidden.
func (b *UIHookBuilder) BackwardsTimestamps() uint64 { return b.backwards }

// Saturated reports how many span durations hit math.MaxUint32 milliseconds
// rather than the real (and enormously larger) value computed from
// elapsed_seconds. A saturated duration is otherwise indistinguishable from
// a real one once folded into a sum.
func (b *UIHookBuilder) Saturated() uint64 { return b.saturated }
