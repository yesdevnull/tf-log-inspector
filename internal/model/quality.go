package model

import (
	"fmt"
	"sort"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// QualityIssue counts one quality limitation and optionally locates its first occurrence.
type QualityIssue struct {
	Stage, Code string
	Count       uint64
	FirstEntry  *uint32
}

// TierQuality reconciles recognised, admitted, positioned and excluded timing evidence.
type TierQuality struct {
	Records, Admitted, Rejected, Positioned uint64
	DurationMs, PositionedMs, ExcludedMs    uint64
	DurationLowerBound                      bool
	Origin                                  *time.Time
	Exclusions                              map[string]uint64
}

// CaptureQuality is the immutable, value-free quality summary of one loaded capture.
type CaptureQuality struct {
	RPC, UI                          TierQuality
	ProviderEntries, StructuredLines uint64
	Issues                           []QualityIssue
	HasContext                       bool
	Attribution                      attrib.Coverage
	NameableMs, RPCDurationMs        uint64
	NameableShare                    *float64
}

// CaptureQualityInput contains the facts produced by one scan of a capture.
type CaptureQualityInput struct {
	Stats                                logfmt.Stats
	Caps                                 span.Capabilities
	RPCSpans, UISpans                    []span.Span
	RPCEvidence, UIEvidence              span.TimingEvidence
	UIOrigin                             time.Time
	Contexts                             []attrib.Context
	ContextEvidence                      attrib.ContextEvidence
	Attributions                         []attrib.Attribution
	ComponentOverflow, RequestIDOverflow uint64
}

// BuildCaptureQuality reconciles scan products without retaining its input slices or maps.
func BuildCaptureQuality(in CaptureQualityInput) CaptureQuality {
	hasContext := len(in.Contexts) > 0
	if hasContext && len(in.Attributions) != len(in.RPCSpans) {
		panic(fmt.Sprintf("model: %d attributions for %d RPC spans", len(in.Attributions), len(in.RPCSpans)))
	}
	rpcTiming, uiTiming := SelectTiming(in.RPCSpans), SelectTiming(in.UISpans)
	q := CaptureQuality{
		RPC:             tierQuality(in.RPCEvidence, rpcTiming, in.Stats.FirstTS),
		UI:              tierQuality(in.UIEvidence, uiTiming, in.UIOrigin),
		ProviderEntries: in.Caps.ProviderEntries,
		StructuredLines: in.Stats.StructuredLines,
		HasContext:      hasContext,
		RPCDurationMs:   rpcTiming.AdmittedMs,
		Attribution:     attrib.Coverage{Candidates: make(map[uint32]int)},
	}
	if hasContext {
		q.Attribution = attrib.Summarise(in.RPCSpans, in.Attributions)
		q.NameableMs = q.Attribution.MsByConfidence[attrib.Contained] + q.Attribution.MsByConfidence[attrib.Likely]
		if q.RPCDurationMs > 0 {
			share := float64(q.NameableMs) / float64(q.RPCDurationMs)
			q.NameableShare = &share
		}
	}
	issues := &q.Issues
	addEvidenceIssues(issues, "rpc_duration", span.TimingEvidence{Rejected: in.RPCEvidence.Rejected})
	addRPCPositionIssues(issues, in.RPCSpans)
	addEvidenceIssues(issues, "ui_duration", span.TimingEvidence{Rejected: in.UIEvidence.Rejected, TimestampIssues: in.UIEvidence.TimestampIssues})
	addIssueCount(issues, "ui_decode", "json_syntax", in.UIEvidence.SyntaxErrors)
	addIssueCount(issues, "ui_decode", "schema_invalid", in.UIEvidence.SchemaErrors)
	addIssueCount(issues, "context", "json_syntax", in.ContextEvidence.SyntaxErrors)
	addIssueCount(issues, "context", "schema_invalid", in.ContextEvidence.SchemaErrors)
	addIssueCount(issues, "context", "timestamp_missing", in.ContextEvidence.MissingTimestamps)
	addIssueCount(issues, "context", "timestamp_invalid", in.ContextEvidence.InvalidTimestamps)
	addIssueCount(issues, "context", "terminator_unmatched", in.ContextEvidence.UnmatchedTerminators)
	addIssueCount(issues, "context", "context_incomplete", in.ContextEvidence.IncompleteContexts)
	addCounterIssue(issues, "scan", "timestamp_before_origin", in.Stats.BackwardsTimestamps)
	addCounterIssue(issues, "scan", "timestamp_out_of_range", in.Stats.TimestampOffsetsOutOfRange)
	addCounterIssue(issues, "scan", "line_count_saturated", in.Stats.LinesSaturated)
	addCounterIssue(issues, "interning", "component_overflow", in.ComponentOverflow)
	addCounterIssue(issues, "interning", "request_id_overflow", in.RequestIDOverflow)
	if in.Caps.ReqIDTrackingFull {
		addCounterIssue(issues, "capability", "request_tracking_capped", 1)
	}
	addSpanIssues(issues, "rpc_duration", in.RPCSpans)
	addSpanIssues(issues, "ui_duration", in.UISpans)
	sort.Slice(q.Issues, func(i, j int) bool {
		if q.Issues[i].Stage != q.Issues[j].Stage {
			return q.Issues[i].Stage < q.Issues[j].Stage
		}
		return q.Issues[i].Code < q.Issues[j].Code
	})
	return q
}

func tierQuality(e span.TimingEvidence, timing TimingSelection, origin time.Time) TierQuality {
	q := TierQuality{Records: e.Records, Admitted: uint64(timing.AdmittedCount), Positioned: uint64(len(timing.Positioned)), DurationMs: timing.AdmittedMs, PositionedMs: timing.PositionedMs, ExcludedMs: timing.ExcludedMs, DurationLowerBound: timing.AdmittedLowerBound, Exclusions: cloneUint64Map(timing.Exclusions)}
	for _, issue := range e.Rejected {
		q.Rejected += issue.Count
	}
	if !origin.IsZero() {
		copied := origin
		q.Origin = &copied
	}
	return q
}

func addEvidenceIssues(dst *[]QualityIssue, stage string, e span.TimingEvidence) {
	for code, issue := range e.Rejected {
		addIssueCount(dst, stage, code, issue)
	}
	addIssueCount(dst, stage, "json_syntax", e.SyntaxErrors)
	addIssueCount(dst, stage, "schema_invalid", e.SchemaErrors)
	for code, issue := range e.TimestampIssues {
		addIssueCount(dst, stage, code, issue)
	}
}

func addIssueCount(dst *[]QualityIssue, stage, code string, issue span.IssueCount) {
	if issue.Count == 0 {
		return
	}
	first := issue.FirstEntry
	*dst = append(*dst, QualityIssue{Stage: stage, Code: code, Count: issue.Count, FirstEntry: &first})
}

func addCounterIssue(dst *[]QualityIssue, stage, code string, count uint64) {
	if count > 0 {
		*dst = append(*dst, QualityIssue{Stage: stage, Code: code, Count: count})
	}
}

func addSpanIssues(dst *[]QualityIssue, stage string, spans []span.Span) {
	var saturated, clamped span.IssueCount
	for _, s := range spans {
		if s.DurationSaturated {
			if saturated.Count == 0 {
				saturated.FirstEntry = s.Entry
			}
			saturated.Count++
		}
		if s.StartClamped {
			if clamped.Count == 0 {
				clamped.FirstEntry = s.Entry
			}
			clamped.Count++
		}
	}
	addIssueCount(dst, stage, "duration_saturated", saturated)
	addIssueCount(dst, stage, "start_clamped", clamped)
}

func addRPCPositionIssues(dst *[]QualityIssue, spans []span.Span) {
	issues := make(map[string]span.IssueCount)
	for _, s := range spans {
		for _, code := range s.PositionReasons() {
			if code == "duration_saturated" {
				continue
			}
			issue := issues[code]
			if issue.Count == 0 {
				issue.FirstEntry = s.Entry
			}
			issue.Count++
			issues[code] = issue
		}
	}
	for code, issue := range issues {
		addIssueCount(dst, "rpc_duration", code, issue)
	}
}

// CaptureQuality returns a detached copy of the summary built during Load.
func (l *Log) CaptureQuality() CaptureQuality { return detachedCaptureQuality(l.quality) }

func detachedCaptureQuality(q CaptureQuality) CaptureQuality {
	q.RPC = detachedTierQuality(q.RPC)
	q.UI = detachedTierQuality(q.UI)
	q.Issues = append([]QualityIssue(nil), q.Issues...)
	for i := range q.Issues {
		if q.Issues[i].FirstEntry != nil {
			v := *q.Issues[i].FirstEntry
			q.Issues[i].FirstEntry = &v
		}
	}
	q.Attribution.Candidates = cloneCandidates(q.Attribution.Candidates)
	if q.NameableShare != nil {
		v := *q.NameableShare
		q.NameableShare = &v
	}
	return q
}

func detachedTierQuality(q TierQuality) TierQuality {
	q.Exclusions = cloneUint64Map(q.Exclusions)
	if q.Origin != nil {
		v := *q.Origin
		q.Origin = &v
	}
	return q
}

func cloneUint64Map(src map[string]uint64) map[string]uint64 {
	dst := make(map[string]uint64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func cloneCandidates(src map[uint32]int) map[uint32]int {
	dst := make(map[uint32]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ReconstructionQuality reports whether lazy provider-response reconstruction completed.
type ReconstructionQuality struct {
	State     string
	Responses int
	Code      string
}

// ReconstructionQuality returns status without triggering response reconstruction.
func (l *Log) ReconstructionQuality() ReconstructionQuality {
	if !l.responseChecked.Load() {
		return ReconstructionQuality{State: "not_checked"}
	}
	if l.responseErr != nil {
		return ReconstructionQuality{State: "failed", Code: "reconstruction_failed"}
	}
	return ReconstructionQuality{State: "checked", Responses: len(l.responses)}
}
