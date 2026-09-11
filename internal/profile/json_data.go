package profile

import (
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

var errInvalidProfileUTF8 = errors.New("profile JSON contains invalid UTF-8")

type jsonProfile struct {
	SchemaVersion   uint8                `json:"schema_version"`
	Kind            string               `json:"kind"`
	ToolVersion     string               `json:"tool_version"`
	Input           jsonInput            `json:"input"`
	DurationUnit    string               `json:"duration_unit"`
	Tiers           jsonTiers            `json:"tiers"`
	Quality         jsonQuality          `json:"quality"`
	RPCObservations []jsonRPCObservation `json:"rpc_observations"`
	UIObservations  []jsonUIObservation  `json:"ui_observations"`
	Aggregates      jsonAggregates       `json:"aggregates"`
	Timeline        jsonTimeline         `json:"timeline"`
	Qualifications  []string             `json:"qualifications"`
}
type jsonInput struct {
	Basename string `json:"basename"`
	Bytes    uint64 `json:"bytes"`
}
type jsonCapture struct {
	Input     jsonInput            `json:"input"`
	Tiers     jsonTiers            `json:"tiers"`
	Quality   jsonQuality          `json:"quality"`
	UnnamedUI *jsonComparisonTotal `json:"unnamed_ui"`
}
type jsonTiers struct {
	RPC jsonTier `json:"rpc"`
	UI  jsonTier `json:"ui"`
}
type jsonSource struct {
	Entry     uint32 `json:"entry"`
	StartLine uint64 `json:"start_line"`
	EndLine   uint64 `json:"end_line"`
	StartByte uint64 `json:"start_byte"`
	EndByte   uint64 `json:"end_byte"`
}
type jsonTotal struct {
	Count      uint64 `json:"count"`
	TotalMs    uint64 `json:"total_ms"`
	MaxMs      uint32 `json:"max_ms"`
	LowerBound bool   `json:"lower_bound"`
}
type jsonPosition struct {
	StartMs      *uint32  `json:"start_ms"`
	EndMs        *uint32  `json:"end_ms"`
	Valid        bool     `json:"valid"`
	Reasons      []string `json:"reasons"`
	StartClamped bool     `json:"start_clamped"`
}
type jsonTier struct {
	DurationAvailable  bool              `json:"duration_available"`
	Records            uint64            `json:"records"`
	Admitted           uint64            `json:"admitted"`
	Rejected           uint64            `json:"rejected"`
	Positioned         uint64            `json:"positioned"`
	Excluded           uint64            `json:"excluded"`
	DurationMs         uint64            `json:"duration_ms"`
	PositionedMs       uint64            `json:"positioned_ms"`
	ExcludedMs         uint64            `json:"excluded_ms"`
	DurationLowerBound bool              `json:"duration_lower_bound"`
	ClockOrigin        *string           `json:"clock_origin"`
	Exclusions         map[string]uint64 `json:"exclusions"`
}
type jsonRPCObservation struct {
	Index        int             `json:"index"`
	Entry        uint32          `json:"entry"`
	Source       *jsonSource     `json:"source"`
	Method       string          `json:"method"`
	Provider     string          `json:"provider"`
	ResourceType string          `json:"resource_type"`
	DurationMs   uint32          `json:"duration_ms"`
	Position     jsonPosition    `json:"position"`
	Attribution  jsonAttribution `json:"attribution"`
}
type jsonUIObservation struct {
	Index              int          `json:"index"`
	Entry              uint32       `json:"entry"`
	Source             *jsonSource  `json:"source"`
	Address            string       `json:"address"`
	Action             string       `json:"action"`
	ResourceType       string       `json:"resource_type"`
	DurationMs         uint32       `json:"duration_ms"`
	DurationLowerBound bool         `json:"duration_lower_bound"`
	Position           jsonPosition `json:"position"`
	DurationSource     string       `json:"duration_source"`
}
type jsonDurationSource struct {
	DurationSource     string `json:"duration_source"`
	Count              uint64 `json:"count"`
	DurationMs         uint64 `json:"duration_ms"`
	MaxMs              uint32 `json:"max_ms"`
	DurationLowerBound bool   `json:"duration_lower_bound"`
}
type jsonAttribution struct {
	Confidence string  `json:"confidence"`
	Address    *string `json:"address"`
	Candidates uint32  `json:"candidates"`
}
type jsonQuality struct {
	Scope             string                  `json:"scope"`
	ProviderEntries   uint64                  `json:"provider_entries"`
	StructuredLines   uint64                  `json:"structured_lines"`
	HasAddressContext bool                    `json:"has_address_context"`
	Issues            []jsonIssue             `json:"issues"`
	Attribution       *jsonAttributionQuality `json:"attribution"`
	NameableMs        uint64                  `json:"nameable_ms"`
	RPCDurationMs     uint64                  `json:"rpc_duration_ms"`
	NameableShare     *float64                `json:"nameable_share"`
	Reconstruction    jsonReconstruction      `json:"reconstruction"`
	DurationSources   []jsonDurationSource    `json:"duration_sources"`
}
type jsonIssue struct {
	Stage      string  `json:"stage"`
	Code       string  `json:"code"`
	Count      uint64  `json:"count"`
	FirstEntry *uint32 `json:"first_entry"`
}
type jsonAttributionQuality struct {
	Spans           int                   `json:"spans"`
	DurationMs      uint64                `json:"duration_ms"`
	ByConfidence    []jsonConfidenceTotal `json:"by_confidence"`
	CandidateCounts []jsonCandidateCount  `json:"candidate_counts"`
}
type jsonConfidenceTotal struct {
	Confidence string `json:"confidence"`
	Count      int    `json:"count"`
	DurationMs uint64 `json:"duration_ms"`
}
type jsonCandidateCount struct {
	Candidates uint32 `json:"candidates"`
	Count      int    `json:"count"`
}
type jsonReconstruction struct {
	State       string  `json:"state"`
	Responses   *int    `json:"responses"`
	Diagnostics *int    `json:"diagnostics"`
	Code        *string `json:"code"`
}
type jsonAggregates struct {
	Providers       []jsonProvider       `json:"providers"`
	ResourceTypes   []jsonResourceType   `json:"resource_types"`
	Resources       []jsonResource       `json:"resources"`
	UI              jsonTotal            `json:"ui"`
	UnnamedUI       jsonTotal            `json:"unnamed_ui"`
	RPCEvidence     jsonRPCEvidence      `json:"rpc_evidence"`
	DurationSources []jsonDurationSource `json:"duration_sources"`
}
type jsonProvider struct {
	Provider string    `json:"provider"`
	RPC      jsonTotal `json:"rpc"`
}
type jsonResourceType struct {
	ResourceType    string               `json:"resource_type"`
	RPC             jsonTotal            `json:"rpc"`
	UI              jsonTotal            `json:"ui"`
	DurationSources []jsonDurationSource `json:"duration_sources"`
}
type jsonResource struct {
	Address              string               `json:"address"`
	UI                   jsonTotal            `json:"ui"`
	NamedRPC             jsonTotal            `json:"named_rpc"`
	OverlappingRPC       jsonTotal            `json:"overlapping_rpc"`
	UIObservationIndices []int                `json:"ui_observation_indices"`
	DurationSources      []jsonDurationSource `json:"duration_sources"`
}
type jsonRPCEvidence struct {
	Baseline     jsonTotal `json:"baseline"`
	MissingType  jsonTotal `json:"missing_type"`
	NoContext    jsonTotal `json:"no_context"`
	Contained    jsonTotal `json:"contained"`
	Likely       jsonTotal `json:"likely"`
	Overlapping  jsonTotal `json:"overlapping"`
	Ambiguous    jsonTotal `json:"ambiguous"`
	Unattributed jsonTotal `json:"unattributed"`
}
type jsonTimeline struct {
	Tier               *string           `json:"tier"`
	Status             string            `json:"status"`
	ClockOrigin        *string           `json:"clock_origin"`
	WindowScope        string            `json:"window_scope"`
	Admitted           uint64            `json:"admitted"`
	Positioned         uint64            `json:"positioned"`
	Excluded           uint64            `json:"excluded"`
	AdmittedMs         uint64            `json:"admitted_ms"`
	AdmittedLowerBound bool              `json:"admitted_lower_bound"`
	PositionedMs       uint64            `json:"positioned_ms"`
	ExcludedMs         uint64            `json:"excluded_ms"`
	ExcludedLowerBound bool              `json:"excluded_lower_bound"`
	Exclusions         map[string]uint64 `json:"exclusions"`
	Metrics            *jsonMetrics      `json:"metrics"`
	ThresholdMs        *uint32           `json:"threshold_ms"`
	Intervals          []jsonInterval    `json:"intervals"`
}
type jsonMetrics struct {
	WindowMs          uint32   `json:"window_ms"`
	Peak              int      `json:"peak"`
	BusyMs            uint32   `json:"busy_ms"`
	BusyFraction      *float64 `json:"busy_fraction"`
	SummedDurationMs  uint64   `json:"summed_duration_ms"`
	SummedWindowRatio *float64 `json:"summed_window_ratio"`
}
type jsonInterval struct {
	StartMs                uint32 `json:"start_ms"`
	EndMs                  uint32 `json:"end_ms"`
	DurationMs             uint32 `json:"duration_ms"`
	MinRunning             int    `json:"min_running"`
	MaxRunning             int    `json:"max_running"`
	ObservedPeak           int    `json:"observed_peak"`
	ActiveObservationIndex *int   `json:"active_observation_index"`
}

func buildJSONProfile(r Report, metadata JSONMetadata) (jsonProfile, error) {
	if err := validateCaptureJSON(r, metadata.ToolVersion, metadata.InputBasename); err != nil {
		return jsonProfile{}, err
	}
	for reason := range r.Timeline.Analysis.Timing.Exclusions {
		if err := validStrings(reason); err != nil {
			return jsonProfile{}, err
		}
	}
	d := jsonProfile{SchemaVersion: 1, Kind: "profile", ToolVersion: metadata.ToolVersion, Input: jsonInput{metadata.InputBasename, r.Bytes}, DurationUnit: "ms", RPCObservations: make([]jsonRPCObservation, 0, len(r.RPC)), UIObservations: make([]jsonUIObservation, 0, len(r.UI)), Qualifications: []string{"unmasked_identifiers", "logging_affects_durations", "rpc_and_ui_measure_different_work", "ui_elapsed_duration_rounding", "refresh_windows_are_hook_measurements", "cli_elapsed_displayed_resolution", "resource_duration_sources_not_interchangeable", "observed_gaps_do_not_prove_idleness", "active_observation_does_not_prove_blocking"}}
	d.Tiers = jsonTiers{tierJSON(r.Quality.RPC), tierJSON(r.Quality.UI)}
	q, err := qualityJSON(r)
	if err != nil {
		return jsonProfile{}, err
	}
	d.Quality = q
	for _, o := range r.RPC {
		row, err := rpcObservationJSON(o, r.HasContext)
		if err != nil {
			return jsonProfile{}, err
		}
		d.RPCObservations = append(d.RPCObservations, row)
	}
	for _, o := range r.UI {
		if err := validStrings(o.Span.Address, o.Span.RPC, o.Span.ResourceType); err != nil {
			return jsonProfile{}, err
		}
		d.UIObservations = append(d.UIObservations, jsonUIObservation{o.Index, o.Span.Entry, sourceJSON(o.Source), o.Span.Address, o.Span.RPC, o.Span.ResourceType, o.Span.DurationMs, o.Span.DurationSaturated, positionJSON(o.Span), o.Span.DurationSource.String()})
	}
	a, err := aggregatesJSON(r)
	if err != nil {
		return jsonProfile{}, err
	}
	d.Aggregates = a
	t, err := timelineJSON(r)
	if err != nil {
		return jsonProfile{}, err
	}
	d.Timeline = t
	return d, nil
}

func captureJSON(r Report, basename string, unnamedUI *model.ComparisonTotal) (jsonCapture, error) {
	if err := validateCaptureJSON(r, basename); err != nil {
		return jsonCapture{}, err
	}
	quality, err := qualityJSON(r)
	if err != nil {
		return jsonCapture{}, err
	}
	return jsonCapture{
		Input:     jsonInput{Basename: basename, Bytes: r.Bytes},
		Tiers:     jsonTiers{RPC: tierJSON(r.Quality.RPC), UI: tierJSON(r.Quality.UI)},
		Quality:   quality,
		UnnamedUI: comparisonTotalJSON(unnamedUI),
	}, nil
}

func validateCaptureJSON(r Report, values ...string) error {
	if err := validStrings(values...); err != nil {
		return err
	}
	for _, exclusions := range []map[string]uint64{r.Quality.RPC.Exclusions, r.Quality.UI.Exclusions} {
		for reason := range exclusions {
			if err := validStrings(reason); err != nil {
				return err
			}
		}
	}
	return nil
}
func sourceJSON(s *model.SourceLocation) *jsonSource {
	if s == nil {
		return nil
	}
	return &jsonSource{s.Entry, s.StartLine, s.EndLine, s.StartByte, s.EndByte}
}
func totalJSON(t model.DurationTotal) jsonTotal {
	return jsonTotal{t.Count, t.TotalMs, t.MaxMs, t.LowerBound}
}
func positionJSON(s span.Span) jsonPosition {
	p := jsonPosition{Valid: s.HasPosition(), Reasons: append([]string{}, s.PositionReasons()...), StartClamped: s.StartClamped}
	if p.Valid {
		a, b := s.StartMs, s.EndMs
		p.StartMs, p.EndMs = &a, &b
	}
	return p
}
func originJSON(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}
func tierJSON(q model.TierQuality) jsonTier {
	return jsonTier{q.Admitted > 0, q.Records, q.Admitted, q.Rejected, q.Positioned, q.Admitted - q.Positioned, q.DurationMs, q.PositionedMs, q.ExcludedMs, q.DurationLowerBound, originJSON(q.Origin), cloneMap(q.Exclusions)}
}
func cloneMap(m map[string]uint64) map[string]uint64 {
	o := make(map[string]uint64, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}
func confidenceJSON(c attrib.Confidence) (string, error) {
	switch c {
	case attrib.Unattributed:
		return "unattributed", nil
	case attrib.Ambiguous:
		return "ambiguous", nil
	case attrib.Overlapping:
		return "overlapping", nil
	case attrib.Likely:
		return "likely", nil
	case attrib.Contained:
		return "contained", nil
	}
	return "", errors.New("profile JSON has invalid attribution confidence")
}
func rpcObservationJSON(o Observation, context bool) (jsonRPCObservation, error) {
	if err := validStrings(o.Span.RPC, o.Span.Provider, o.Span.ResourceType); err != nil {
		return jsonRPCObservation{}, err
	}
	c := "no_context"
	var addr *string
	var candidates uint32
	if context {
		var err error
		c, err = confidenceJSON(o.Attribution.Confidence)
		if err != nil {
			return jsonRPCObservation{}, err
		}
		candidates = o.Attribution.Candidates
		if o.Attribution.Address != "" && (o.Attribution.Confidence == attrib.Contained || o.Attribution.Confidence == attrib.Likely || o.Attribution.Confidence == attrib.Overlapping) {
			if err := validStrings(o.Attribution.Address); err != nil {
				return jsonRPCObservation{}, err
			}
			v := o.Attribution.Address
			addr = &v
		}
	}
	return jsonRPCObservation{o.Index, o.Span.Entry, sourceJSON(o.Source), o.Span.RPC, o.Span.Provider, o.Span.ResourceType, o.Span.DurationMs, positionJSON(o.Span), jsonAttribution{c, addr, candidates}}, nil
}
func qualityJSON(r Report) (jsonQuality, error) {
	q := r.Quality
	o := jsonQuality{Scope: "whole_log", ProviderEntries: q.ProviderEntries, StructuredLines: q.StructuredLines, HasAddressContext: q.HasContext, Issues: make([]jsonIssue, 0, len(q.Issues)), NameableMs: q.NameableMs, RPCDurationMs: q.RPCDurationMs, NameableShare: copyFloat(q.NameableShare)}
	o.DurationSources = durationSourcesJSON(observationSpans(r.UI))
	for _, i := range q.Issues {
		if err := validStrings(i.Stage, i.Code); err != nil {
			return o, err
		}
		o.Issues = append(o.Issues, jsonIssue{i.Stage, i.Code, i.Count, copyUint32(i.FirstEntry)})
	}
	if q.HasContext {
		aq := jsonAttributionQuality{Spans: q.Attribution.Spans, DurationMs: q.Attribution.TotalMs, ByConfidence: make([]jsonConfidenceTotal, 0, 5), CandidateCounts: make([]jsonCandidateCount, 0, len(q.Attribution.Candidates))}
		for _, c := range []attrib.Confidence{attrib.Unattributed, attrib.Ambiguous, attrib.Overlapping, attrib.Likely, attrib.Contained} {
			name, _ := confidenceJSON(c)
			aq.ByConfidence = append(aq.ByConfidence, jsonConfidenceTotal{name, q.Attribution.ByConfidence[c], q.Attribution.MsByConfidence[c]})
		}
		keys := make([]uint32, 0, len(q.Attribution.Candidates))
		for k := range q.Attribution.Candidates {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			aq.CandidateCounts = append(aq.CandidateCounts, jsonCandidateCount{k, q.Attribution.Candidates[k]})
		}
		o.Attribution = &aq
	}
	rec, err := reconstructionJSON(r.Reconstruction)
	o.Reconstruction = rec
	return o, err
}
func reconstructionJSON(q model.ReconstructionQuality) (jsonReconstruction, error) {
	o := jsonReconstruction{State: q.State}
	switch q.State {
	case "not_checked":
		if q.Responses != 0 || q.Diagnostics != 0 || q.Code != "" {
			return o, errors.New("profile JSON has invalid reconstruction snapshot")
		}
	case "complete":
		if q.Responses < 0 || q.Diagnostics != 0 || q.Code != "" {
			return o, errors.New("profile JSON has invalid reconstruction snapshot")
		}
		responses, diagnostics := q.Responses, 0
		o.Responses, o.Diagnostics = &responses, &diagnostics
	case "partial":
		if q.Responses <= 0 || q.Diagnostics <= 0 || q.Code != "reconstruction_partial" {
			return o, errors.New("profile JSON has invalid reconstruction snapshot")
		}
		responses, diagnostics, code := q.Responses, q.Diagnostics, q.Code
		o.Responses, o.Diagnostics, o.Code = &responses, &diagnostics, &code
	case "failed":
		if q.Responses != 0 || q.Diagnostics <= 0 || q.Code != "reconstruction_failed" {
			return o, errors.New("profile JSON has invalid reconstruction snapshot")
		}
		responses, diagnostics, code := 0, q.Diagnostics, q.Code
		o.Responses, o.Diagnostics, o.Code = &responses, &diagnostics, &code
	default:
		return o, errors.New("profile JSON has invalid reconstruction state")
	}
	return o, nil
}
func aggregatesJSON(r Report) (jsonAggregates, error) {
	o := jsonAggregates{Providers: make([]jsonProvider, 0, len(r.Providers)), ResourceTypes: make([]jsonResourceType, 0, len(r.Types)), Resources: make([]jsonResource, 0, len(r.Resources.Rows)), UI: totalJSON(r.Resources.UI), UnnamedUI: totalJSON(r.Resources.UnnamedUI)}
	observations := observationSpans(r.UI)
	o.DurationSources = durationSourcesJSON(observations)
	byType, byAddress := make(map[string][]span.Span), make(map[string][]span.Span)
	for _, observation := range observations {
		byType[observation.ResourceType] = append(byType[observation.ResourceType], observation)
		byAddress[observation.Address] = append(byAddress[observation.Address], observation)
	}
	for _, p := range r.Providers {
		if err := validStrings(p.Key); err != nil {
			return o, err
		}
		o.Providers = append(o.Providers, jsonProvider{p.Key, jsonTotal{uint64(p.Count), p.TotalMs, p.MaxMs, false}})
	}
	for _, x := range r.Types {
		if err := validStrings(x.ResourceType); err != nil {
			return o, err
		}
		o.ResourceTypes = append(o.ResourceTypes, jsonResourceType{x.ResourceType, jsonTotal{uint64(x.RPCCalls), x.RPCTotalMs, x.RPCMaxMs, false}, jsonTotal{uint64(x.UIResources), x.UITotalMs, x.UIMaxMs, x.UILowerBound}, durationSourcesJSON(byType[x.ResourceType])})
	}
	for _, x := range r.Resources.Rows {
		if err := validStrings(x.Address); err != nil {
			return o, err
		}
		idx := make([]int, 0, len(x.Operations))
		for _, op := range x.Operations {
			idx = append(idx, op.UIIndex)
		}
		o.Resources = append(o.Resources, jsonResource{x.Address, totalJSON(x.UI), totalJSON(x.NamedRPC), totalJSON(x.OverlappingRPC), idx, durationSourcesJSON(byAddress[x.Address])})
	}
	e := r.Resources.Evidence
	o.RPCEvidence = jsonRPCEvidence{totalJSON(e.Baseline), totalJSON(e.MissingType), totalJSON(e.NoContext), totalJSON(e.Contained), totalJSON(e.Likely), totalJSON(e.Overlapping), totalJSON(e.Ambiguous), totalJSON(e.Unattributed)}
	return o, nil
}
func durationSourcesJSON(observations []span.Span) []jsonDurationSource {
	summaries := model.SummariseDurationSources(observations)
	rows := make([]jsonDurationSource, 0, len(summaries))
	for _, summary := range summaries {
		rows = append(rows, jsonDurationSource{summary.Source.String(), summary.Count, summary.DurationMs, summary.MaxMs, summary.DurationLowerBound})
	}
	return rows
}

func timelineJSON(r Report) (jsonTimeline, error) {
	o := jsonTimeline{Status: "unavailable", WindowScope: "zero_to_latest_positioned_end", Exclusions: map[string]uint64{}, Intervals: []jsonInterval{}}
	if r.Timeline.Tier == nil {
		return o, nil
	}
	var name string
	var origin *time.Time
	var observations []Observation
	switch *r.Timeline.Tier {
	case span.FidelityReported:
		name = "rpc"
		origin = r.Quality.RPC.Origin
		observations = r.RPC
	case span.FidelityUIReported:
		name = "ui"
		origin = r.Quality.UI.Origin
		observations = r.UI
	default:
		return o, errors.New("profile JSON has invalid timing tier")
	}
	o.Tier = &name
	o.ClockOrigin = originJSON(origin)
	t := r.Timeline.Analysis.Timing
	o.Admitted = uint64(t.AdmittedCount)
	o.Positioned = uint64(t.AdmittedCount - t.ExcludedCount)
	o.Excluded = uint64(t.ExcludedCount)
	o.AdmittedMs = t.AdmittedMs
	o.AdmittedLowerBound = t.AdmittedLowerBound
	o.PositionedMs = t.PositionedMs
	o.ExcludedMs = t.ExcludedMs
	o.ExcludedLowerBound = t.ExcludedLowerBound
	o.Exclusions = cloneMap(t.Exclusions)
	if r.Timeline.Analysis.Metrics == nil {
		return o, nil
	}
	o.Status = "complete"
	if t.ExcludedCount > 0 {
		o.Status = "partial"
	}
	m := r.Timeline.Analysis.Metrics
	o.Metrics = &jsonMetrics{WindowMs: m.WindowMs, Peak: m.Peak, BusyMs: m.BusyMs, BusyFraction: copyFloat(m.BusyFraction), SummedDurationMs: t.PositionedMs}
	if m.WindowMs > 0 {
		v := float64(t.PositionedMs) / float64(m.WindowMs)
		o.Metrics.SummedWindowRatio = &v
	}
	threshold := r.Timeline.Analysis.ThresholdMs
	o.ThresholdMs = &threshold
	for _, in := range r.Timeline.Analysis.Intervals {
		row := jsonInterval{StartMs: in.StartMs, EndMs: in.EndMs, DurationMs: in.EndMs - in.StartMs, MinRunning: in.MinRunning, MaxRunning: in.MaxRunning, ObservedPeak: in.Capacity}
		if in.Blocking == -1 {
			o.Intervals = append(o.Intervals, row)
			continue
		}
		if in.Blocking < -1 {
			return o, errors.New("profile interval observation index out of range")
		}
		obs, err := intervalObservation(r, observations, in.Blocking)
		if err != nil {
			return o, err
		}
		v := obs.Index
		row.ActiveObservationIndex = &v
		o.Intervals = append(o.Intervals, row)
	}
	return o, nil
}
func copyFloat(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func copyUint32(p *uint32) *uint32 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func validStrings(v ...string) error {
	for _, s := range v {
		if !utf8.ValidString(s) {
			return errInvalidProfileUTF8
		}
	}
	return nil
}
