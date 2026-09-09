package span

import (
	"bytes"
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
	Type string  `json:"type"`
	Hook *uiHook `json:"hook"`
}

type uiHook struct {
	Resource *struct {
		Addr            string `json:"addr"`
		ResourceType    string `json:"resource_type"`
		ImpliedProvider string `json:"implied_provider"`
	} `json:"resource"`
	Action string `json:"action"`
}

// isCompletionType reports whether t is a UI-hook type carrying a real,
// total elapsed_seconds. apply_progress is deliberately excluded: it also
// carries elapsed_seconds, but as a partial "still working" figure -- taking
// it as a completion would double-count and inflate durations.
//
// provision_complete/provision_errored and refresh_complete are also
// deliberately excluded, even though they close a context in package attrib:
// verified against hashicorp/terraform tag v1.14.9's
// internal/command/views/json/hook.go, their backing structs --
// provisionComplete/provisionErrored ({Resource, Provisioner}) and
// refreshComplete ({Resource, IDKey, IDValue}) -- carry no elapsed_seconds
// field at all, unlike operationComplete/operationErrored (used by
// apply_complete/apply_errored and ephemeral_op_complete/ephemeral_op_errored),
// which do. attrib's opensContext/closesContext admit provision_*/refresh_*
// anyway because that package takes its context windows from the lines' own
// timestamps rather than from elapsed_seconds -- a different admission
// criterion answering a different question, not an inconsistency to reconcile.
func isCompletionType(t string) bool {
	switch t {
	case "apply_complete", "apply_errored",
		"ephemeral_op_complete", "ephemeral_op_errored":
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
	evidence  TimingEvidence
}

// Entry implements logfmt.Sink as a no-op: UIHookBuilder only cares about
// structured lines, delivered via Structured, but every sink passed to
// logfmt.Scan must satisfy Sink.
func (b *UIHookBuilder) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {}

// relativePosition places a parsed UI timestamp on this builder's clock.
// Duration admission is independent of the returned position status.
func (b *UIHookBuilder) relativePosition(t time.Time) logfmt.ClockPosition {
	if !b.haveBase {
		b.base, b.haveBase = t, true
	}
	position := logfmt.RelativePosition(t, b.base)
	if position.Status == logfmt.TimestampBeforeOrigin {
		b.backwards++
	}
	return position
}

// Structured implements logfmt.StructuredSink.
func (b *UIHookBuilder) Structured(ord uint32, e logfmt.Entry, line string) {
	if !json.Valid([]byte(line)) {
		addStage(&b.evidence.SyntaxErrors, ord)
		b.malformed++
		return
	}
	var ul struct {
		Timestamp json.RawMessage `json:"@timestamp"`
		Type      json.RawMessage `json:"type"`
		Hook      json.RawMessage `json:"hook"`
	}
	if err := json.Unmarshal([]byte(line), &ul); err != nil {
		addStage(&b.evidence.SchemaErrors, ord)
		return
	}
	position, timestampSchema := b.timestampPosition(ul.Timestamp)
	if reason := timestampReason(position.Status); reason != "" {
		if b.evidence.TimestampIssues == nil {
			b.evidence.TimestampIssues = make(map[string]IssueCount)
		}
		addIssue(b.evidence.TimestampIssues, reason, ord)
	}
	var typ string
	typeSchema := false
	if len(ul.Type) > 0 {
		typeSchema = !decodeJSONString(ul.Type, &typ)
	}
	if timestampSchema || typeSchema {
		addStage(&b.evidence.SchemaErrors, ord)
	}
	if typeSchema || !isCompletionType(typ) {
		return
	}
	b.evidence.Records++
	if len(ul.Hook) == 0 || bytes.Equal(bytes.TrimSpace(ul.Hook), []byte("null")) {
		b.reject("duration_missing", ord)
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(ul.Hook, &fields); err != nil || fields == nil {
		if !timestampSchema && !typeSchema {
			addStage(&b.evidence.SchemaErrors, ord)
		}
		b.reject("record_schema_invalid", ord)
		return
	}
	hook, schema := parseUIHook(fields)
	if schema && !timestampSchema && !typeSchema {
		addStage(&b.evidence.SchemaErrors, ord)
	}
	durationMs, saturated, rejection := uiDuration(fields["elapsed_seconds"])
	if rejection != "" {
		b.reject(rejection, ord)
		return
	}
	if saturated {
		b.saturated++
	}
	start, end, clamped := positionedDuration(position, durationMs, saturated)
	var address, provider, resourceType string
	if hook.Resource != nil {
		address = strings.Clone(hook.Resource.Addr)
		provider = b.kept.retain(hook.Resource.ImpliedProvider)
		resourceType = b.kept.retain(hook.Resource.ResourceType)
	}

	b.spans = append(b.spans, Span{
		Entry:             ord,
		ReqID:             0,
		StartMs:           start,
		EndMs:             end,
		DurationMs:        durationMs,
		StartClamped:      clamped,
		TimestampStatus:   position.Status,
		DurationSaturated: saturated,
		RPC:               b.kept.retain(hook.Action),
		Provider:          provider,
		ResourceType:      resourceType,
		Address:           address,
		Fidelity:          FidelityUIReported,
	})
}

func (b *UIHookBuilder) timestampPosition(raw json.RawMessage) (logfmt.ClockPosition, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return logfmt.ClockPosition{Status: logfmt.TimestampMissing}, false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return logfmt.ClockPosition{Status: logfmt.TimestampInvalid}, true
	}
	if value == "" {
		return logfmt.ClockPosition{Status: logfmt.TimestampMissing}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return logfmt.ClockPosition{Status: logfmt.TimestampInvalid}, false
	}
	return b.relativePosition(t), false
}

func uiDuration(raw json.RawMessage) (uint32, bool, string) {
	if len(raw) == 0 {
		return 0, false, "duration_missing"
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return 0, false, "duration_null"
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return 0, false, "duration_invalid"
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, false, "duration_invalid"
	}
	literal := string(number)
	mantissa := strings.SplitN(strings.ToLower(literal), "e", 2)[0]
	if strings.HasPrefix(mantissa, "-") && strings.ContainsAny(mantissa, "123456789") {
		return 0, false, "duration_negative"
	}
	seconds, err := number.Float64()
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, false, "duration_invalid"
	}
	if seconds < 0 {
		return 0, false, "duration_negative"
	}
	scaled := math.Round(seconds * 1000)
	if scaled > math.MaxUint32 {
		return math.MaxUint32, true, ""
	}
	return uint32(scaled), false, ""
}

func parseUIHook(fields map[string]json.RawMessage) (uiHook, bool) {
	var hook uiHook
	schema := false
	if raw := fields["action"]; len(raw) > 0 {
		schema = !decodeJSONString(raw, &hook.Action)
	}
	if raw := fields["resource"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var resourceFields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &resourceFields); err != nil || resourceFields == nil {
			schema = true
		} else {
			resource := new(struct {
				Addr            string `json:"addr"`
				ResourceType    string `json:"resource_type"`
				ImpliedProvider string `json:"implied_provider"`
			})
			for rawField, destination := range map[string]*string{
				"addr":             &resource.Addr,
				"resource_type":    &resource.ResourceType,
				"implied_provider": &resource.ImpliedProvider,
			} {
				if value := resourceFields[rawField]; len(value) > 0 && !decodeJSONString(value, destination) {
					schema = true
				}
			}
			hook.Resource = resource
		}
	}
	return hook, schema
}

func decodeJSONString(raw json.RawMessage, destination *string) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	return json.Unmarshal(raw, destination) == nil
}

func positionedDuration(position logfmt.ClockPosition, duration uint32, saturated bool) (uint32, uint32, bool) {
	if position.Status != logfmt.TimestampValid || saturated {
		return 0, 0, false
	}
	clamped := duration > position.OffsetMs
	if clamped {
		return 0, position.OffsetMs, true
	}
	return position.OffsetMs - duration, position.OffsetMs, false
}

func (b *UIHookBuilder) reject(reason string, ord uint32) {
	if b.evidence.Rejected == nil {
		b.evidence.Rejected = make(map[string]IssueCount)
	}
	addIssue(b.evidence.Rejected, reason, ord)
}

func (b *UIHookBuilder) Evidence() TimingEvidence { return detachedEvidence(b.evidence) }

func (b *UIHookBuilder) Origin() (time.Time, bool) { return b.base, b.haveBase }

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
