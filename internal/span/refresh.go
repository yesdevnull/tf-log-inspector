package span

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/resourceaddr"
)

// Cap retained open addresses even when a corrupt stream never closes them.
const maxOpenRefresh = 65536

type refreshStart struct {
	entry     uint32
	timestamp time.Time
	position  logfmt.ClockPosition
	ambiguous bool
	pending   uint32
}

func isLifecycleType(typ string) bool {
	switch typ {
	case "apply_start", "apply_progress", "apply_complete", "apply_errored",
		"refresh_start", "refresh_complete", "provision_start", "provision_progress", "provision_complete", "provision_errored",
		"ephemeral_op_start", "ephemeral_op_progress", "ephemeral_op_complete", "ephemeral_op_errored":
		return true
	default:
		return false
	}
}

func (b *UIHookBuilder) refresh(ord uint32, typ string, timestamp, rawHook json.RawMessage, position logfmt.ClockPosition) {
	complete := typ == "refresh_complete"
	if complete {
		b.evidence.Records++
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(rawHook, &fields) != nil || fields == nil {
		if !complete {
			b.evidence.Records++
		}
		b.reject("refresh_schema_invalid", ord)
		return
	}
	hook, schema := parseUIHook(fields)
	if schema || hook.Resource == nil || hook.Resource.Addr == "" {
		if !complete {
			b.evidence.Records++
		}
		b.reject("refresh_address_invalid", ord)
		return
	}
	address := hook.Resource.Addr
	if _, valid := resourceaddr.Parse(address); !valid {
		if !complete {
			b.evidence.Records++
		}
		b.reject("refresh_address_invalid", ord)
		return
	}
	var ts string
	_ = json.Unmarshal(timestamp, &ts)
	t, _ := time.Parse(time.RFC3339Nano, ts)
	if !complete {
		if existing, ok := b.refreshOpen[address]; ok {
			existing.ambiguous = true
			if existing.pending < math.MaxUint32 {
				existing.pending++
			}
			b.refreshOpen[address] = existing
			b.evidence.Records++
			b.reject("refresh_repeated_open", ord)
			return
		}
		if b.refreshOverflow || len(b.refreshOpen) >= maxOpenRefresh {
			// Once tracking is exhausted, a later start might repeat one
			// we could not retain. Do not resume guessing pairs after space
			// becomes available.
			b.refreshOverflow = true
			b.evidence.Records++
			b.reject("refresh_tracking_overflow", ord)
			return
		}
		if b.refreshOpen == nil {
			b.refreshOpen = make(map[string]refreshStart)
		}
		b.refreshOpen[strings.Clone(address)] = refreshStart{entry: ord, timestamp: t, position: position, pending: 1}
		return
	}
	start, ok := b.refreshOpen[address]
	if !ok {
		b.reject("refresh_unmatched", ord)
		return
	}
	if start.ambiguous {
		if start.pending < math.MaxUint32 {
			start.pending--
		}
		if start.pending == 0 {
			delete(b.refreshOpen, address)
		} else {
			b.refreshOpen[address] = start
		}
		b.reject("refresh_ambiguous", ord)
		return
	}
	delete(b.refreshOpen, address)
	if start.timestamp.IsZero() || t.IsZero() {
		b.reject("refresh_timestamp_invalid", ord)
		return
	}
	if t.Before(start.timestamp) {
		b.reject("refresh_backwards", ord)
		return
	}
	ms := t.Sub(start.timestamp).Milliseconds()
	saturated := ms > math.MaxUint32
	if saturated {
		ms = math.MaxUint32
		b.saturated++
	}
	status := position.Status
	if start.position.Status != logfmt.TimestampValid {
		status = start.position.Status
	}
	s := Span{
		Entry: ord, StartEntry: start.entry, HasStartEntry: true,
		DurationMs: uint32(ms), DurationSource: SourceRefreshWindow,
		DurationSaturated: saturated, TimestampStatus: status,
		RPC: "refresh", Fidelity: FidelityUIReported,
		Address: strings.Clone(address), Module: strings.Clone(hook.Resource.Module),
		ModuleKnown: hook.Resource.ModuleKnown, ModuleInvalid: hook.Resource.ModuleInvalid,
		ResourceType: b.kept.retain(hook.Resource.ResourceType), Provider: b.kept.retain(hook.Resource.ImpliedProvider),
	}
	if s.HasPosition() {
		s.StartMs = start.position.OffsetMs
		s.EndMs = position.OffsetMs
	}
	b.spans = append(b.spans, s)
}
