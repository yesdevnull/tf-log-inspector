package profile

import (
	"reflect"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestJSONProjectionMapsCompleteReportContract(t *testing.T) {
	originRPC := time.Date(2026, 9, 11, 1, 2, 3, 4, time.FixedZone("east", 3600))
	originUI := time.Date(2026, 9, 10, 23, 2, 3, 0, time.FixedZone("west", -3600))
	firstZero, share, busy := uint32(0), 0.4, 0.75
	tier := span.FidelityReported
	total := func(count, ms uint64, max uint32, lower bool) model.DurationTotal {
		return model.DurationTotal{Count: count, TotalMs: ms, MaxMs: max, LowerBound: lower}
	}
	r := Report{
		Bytes: 1<<53 + 11,
		Quality: model.CaptureQuality{
			RPC:             model.TierQuality{Records: 9, Admitted: 7, Rejected: 2, Positioned: 5, DurationMs: 701, PositionedMs: 501, ExcludedMs: 200, DurationLowerBound: true, Origin: &originRPC, Exclusions: map[string]uint64{"timestamp_missing": 2}},
			UI:              model.TierQuality{Records: 6, Admitted: 4, Rejected: 2, Positioned: 3, DurationMs: 4000, PositionedMs: 3000, ExcludedMs: 1000, Origin: &originUI, Exclusions: map[string]uint64{}},
			ProviderEntries: 17, StructuredLines: 13, HasContext: true,
			Issues:      []model.QualityIssue{{Stage: "rpc_duration", Code: "duration_invalid", Count: 2, FirstEntry: nil}, {Stage: "scan", Code: "timestamp_before_origin", Count: 1, FirstEntry: &firstZero}},
			Attribution: attrib.Coverage{Spans: 5, TotalMs: 500, ByConfidence: [5]int{1, 2, 3, 4, 5}, MsByConfidence: [5]uint64{10, 20, 30, 40, 50}, Candidates: map[uint32]int{11: 1, 2: 4}},
			NameableMs:  90, RPCDurationMs: 701, NameableShare: &share,
		},
		HasContext: true,
		RPC:        []Observation{{Index: 3, Span: span.Span{Entry: 8, StartMs: 0, EndMs: 25, DurationMs: 25, StartClamped: true, TimestampStatus: logfmt.TimestampValid, RPC: "Méthod", Provider: "provider.a", ResourceType: "type_a"}, Source: &model.SourceLocation{Entry: 8, StartLine: 10, EndLine: 12, StartByte: 100, EndByte: 190}, Attribution: attrib.Attribution{Confidence: attrib.Likely, Address: "module.x.resource.a", Candidates: 2}}},
		UI:         []Observation{{Index: 4, Span: span.Span{Entry: 9, DurationMs: 1000, DurationSaturated: true, TimestampStatus: logfmt.TimestampMissing, Address: "resource.ui", RPC: "create", ResourceType: "type_ui"}}},
		Providers:  []model.Bucket{{Key: "provider.z", Count: 2, TotalMs: 200, MaxMs: 120}, {Key: "provider.a", Count: 2, TotalMs: 200, MaxMs: 100}},
		Types:      []TypeSummary{{TypeRow: model.TypeRow{ResourceType: "type_b", UIResources: 2, UITotalMs: 2200, UIMaxMs: 1200, RPCCalls: 3, RPCTotalMs: 330, RPCMaxMs: 130}, UILowerBound: true}},
		Resources: model.ResourceProjection{
			Rows: []model.ResourceRow{{Address: "resource.ui", Operations: []model.ResourceOperation{{UIIndex: 4}, {UIIndex: 1}}, UI: total(2, 2200, 1200, true), NamedRPC: total(3, 330, 130, false), OverlappingRPC: total(1, 30, 30, false)}},
			UI:   total(4, 4000, 1200, true), UnnamedUI: total(1, 800, 800, false),
			Evidence: model.ResourceEvidence{Baseline: total(8, 800, 180, true), MissingType: total(1, 10, 10, false), NoContext: total(1, 20, 20, false), Contained: total(1, 30, 30, false), Likely: total(1, 40, 40, false), Overlapping: total(1, 50, 50, false), Ambiguous: total(1, 60, 60, false), Unattributed: total(2, 590, 380, true)},
		},
		Timeline:       Timeline{Tier: &tier, PositionedIndices: []int{0}, Analysis: model.TimingAnalysis{Timing: model.TimingSelection{AdmittedCount: 2, ExcludedCount: 1, AdmittedMs: 225, PositionedMs: 25, ExcludedMs: 200, AdmittedLowerBound: true, ExcludedLowerBound: true, Exclusions: map[string]uint64{"timestamp_missing": 1}}, Metrics: &model.TimingMetrics{WindowMs: 100, Peak: 3, BusyMs: 75, BusyFraction: &busy}, ThresholdMs: 20, Intervals: []model.Stall{{StartMs: 25, EndMs: 40, MinRunning: 1, MaxRunning: 2, Capacity: 3, Blocking: 0}, {StartMs: 40, EndMs: 50, MinRunning: 0, MaxRunning: 0, Capacity: 3, Blocking: -1}}}},
		Reconstruction: model.ReconstructionQuality{State: "complete", Responses: 0},
	}

	d, err := buildJSONProfile(r, JSONMetadata{ToolVersion: "1.2.3", InputBasename: "capture.log"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "root identity", jsonInput{"capture.log", 1<<53 + 11}, d.Input)
	assertEqual(t, "tiers", jsonTiers{
		RPC: jsonTier{true, 9, 7, 2, 5, 2, 701, 501, 200, true, stringPointer("2026-09-11T00:02:03.000000004Z"), map[string]uint64{"timestamp_missing": 2}},
		UI:  jsonTier{true, 6, 4, 2, 3, 1, 4000, 3000, 1000, false, stringPointer("2026-09-11T00:02:03Z"), map[string]uint64{}},
	}, d.Tiers)
	assertEqual(t, "quality", jsonQuality{"whole_log", 17, 13, true,
		[]jsonIssue{{"rpc_duration", "duration_invalid", 2, nil}, {"scan", "timestamp_before_origin", 1, uint32Pointer(0)}},
		&jsonAttributionQuality{5, 500, []jsonConfidenceTotal{{"unattributed", 1, 10}, {"ambiguous", 2, 20}, {"overlapping", 3, 30}, {"likely", 4, 40}, {"contained", 5, 50}}, []jsonCandidateCount{{2, 4}, {11, 1}}},
		90, 701, floatPointer(0.4), jsonReconstruction{"complete", intPointer(0), intPointer(0), nil}, []jsonDurationSource{{"ui_elapsed", 1, 1000, 1000, true}}}, d.Quality)
	assertEqual(t, "rpc observation", []jsonRPCObservation{{3, 8, &jsonSource{8, 10, 12, 100, 190}, "Méthod", "provider.a", "type_a", 25, jsonPosition{uint32Pointer(0), uint32Pointer(25), true, []string{}, true}, jsonAttribution{"likely", stringPointer("module.x.resource.a"), 2}}}, d.RPCObservations)
	assertEqual(t, "ui observation", []jsonUIObservation{{4, 9, nil, "resource.ui", "create", "type_ui", 1000, true, jsonPosition{nil, nil, false, []string{"timestamp_missing", "duration_saturated"}, false}, "ui_elapsed"}}, d.UIObservations)
	assertEqual(t, "aggregates", jsonAggregates{
		Providers:     []jsonProvider{{"provider.z", jsonTotal{2, 200, 120, false}}, {"provider.a", jsonTotal{2, 200, 100, false}}},
		ResourceTypes: []jsonResourceType{{"type_b", jsonTotal{3, 330, 130, false}, jsonTotal{2, 2200, 1200, true}, []jsonDurationSource{}}},
		Resources:     []jsonResource{{"resource.ui", jsonTotal{2, 2200, 1200, true}, jsonTotal{3, 330, 130, false}, jsonTotal{1, 30, 30, false}, []int{4, 1}, []jsonDurationSource{{"ui_elapsed", 1, 1000, 1000, true}}}},
		UI:            jsonTotal{4, 4000, 1200, true}, UnnamedUI: jsonTotal{1, 800, 800, false},
		RPCEvidence:     jsonRPCEvidence{jsonTotal{8, 800, 180, true}, jsonTotal{1, 10, 10, false}, jsonTotal{1, 20, 20, false}, jsonTotal{1, 30, 30, false}, jsonTotal{1, 40, 40, false}, jsonTotal{1, 50, 50, false}, jsonTotal{1, 60, 60, false}, jsonTotal{2, 590, 380, true}},
		DurationSources: []jsonDurationSource{{"ui_elapsed", 1, 1000, 1000, true}},
	}, d.Aggregates)
	assertEqual(t, "timeline", jsonTimeline{stringPointer("rpc"), "partial", stringPointer("2026-09-11T00:02:03.000000004Z"), "zero_to_latest_positioned_end", 2, 1, 1, 225, true, 25, 200, true, map[string]uint64{"timestamp_missing": 1}, &jsonMetrics{100, 3, 75, floatPointer(0.75), 25, floatPointer(0.25)}, uint32Pointer(20), []jsonInterval{{25, 40, 15, 1, 2, 3, intPointer(3)}, {40, 50, 10, 0, 0, 3, nil}}}, d.Timeline)
	assertEqual(t, "root constants", []any{uint8(1), "profile", "1.2.3", "ms"}, []any{d.SchemaVersion, d.Kind, d.ToolVersion, d.DurationUnit})
	assertEqual(t, "qualifications", []string{"unmasked_identifiers", "logging_affects_durations", "rpc_and_ui_measure_different_work", "ui_elapsed_duration_rounding", "refresh_windows_are_hook_measurements", "cli_elapsed_displayed_resolution", "resource_duration_sources_not_interchangeable", "observed_gaps_do_not_prove_idleness", "active_observation_does_not_prove_blocking"}, d.Qualifications)
}

func TestJSONProjectionMapsAbsentAndUIOnlyStates(t *testing.T) {
	base := Report{RPC: []Observation{{Attribution: attrib.Attribution{Confidence: attrib.Contained, Address: "hidden", Candidates: 9}}}, Reconstruction: model.ReconstructionQuality{State: "not_checked"}}
	d, err := buildJSONProfile(base, JSONMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "absent tier", jsonTimeline{Status: "unavailable", WindowScope: "zero_to_latest_positioned_end", Exclusions: map[string]uint64{}, Intervals: []jsonInterval{}}, d.Timeline)
	if d.Quality.Attribution != nil || d.Quality.NameableShare != nil || d.Quality.Reconstruction.Responses != nil || d.Quality.Reconstruction.Diagnostics != nil || d.Quality.Reconstruction.Code != nil {
		t.Fatal("absent quality values must be null")
	}
	if d.Tiers.RPC.DurationAvailable || d.Tiers.UI.DurationAvailable {
		t.Fatal("absent durations reported available")
	}
	if d.RPCObservations[0].Attribution != (jsonAttribution{Confidence: "no_context"}) {
		t.Fatalf("no-context attribution = %+v", d.RPCObservations[0].Attribution)
	}
	zeroShare := 0.0
	base.Quality.HasContext = true
	base.Quality.NameableShare = &zeroShare
	base.Quality.RPC.Admitted = 1
	d, err = buildJSONProfile(base, JSONMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Quality.NameableShare == nil || *d.Quality.NameableShare != 0 || !d.Tiers.RPC.DurationAvailable {
		t.Fatal("measured-zero share or duration unavailable")
	}

	uiOrigin := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 3600))
	uiTier := span.FidelityUIReported
	base.Quality.UI.Origin = &uiOrigin
	base.Timeline = Timeline{Tier: &uiTier, Analysis: model.TimingAnalysis{Timing: model.TimingSelection{AdmittedCount: 1, ExcludedCount: 1, AdmittedMs: 9, ExcludedMs: 9, Exclusions: map[string]uint64{"timestamp_invalid": 1}}}}
	d, err = buildJSONProfile(base, JSONMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Timeline.Tier == nil || *d.Timeline.Tier != "ui" || d.Timeline.Status != "unavailable" || d.Timeline.ClockOrigin == nil || *d.Timeline.ClockOrigin != "2026-01-02T02:04:05Z" || d.Timeline.Metrics != nil || d.Timeline.ThresholdMs != nil || d.Timeline.Admitted != 1 || d.Timeline.Positioned != 0 || d.Timeline.Excluded != 1 || d.Timeline.Exclusions["timestamp_invalid"] != 1 {
		t.Fatalf("UI-only unavailable timeline = %+v", d.Timeline)
	}
	rpcTier := span.FidelityReported
	base.Quality.RPC.Origin = nil
	base.Timeline.Tier = &rpcTier
	d, err = buildJSONProfile(base, JSONMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Timeline.Tier == nil || *d.Timeline.Tier != "rpc" || d.Timeline.ClockOrigin != nil || d.Timeline.Status != "unavailable" {
		t.Fatalf("preferred unusable RPC tier fell back to UI: %+v", d.Timeline)
	}
}

func TestJSONProjectionPreservesEqualTotalAggregateIdentities(t *testing.T) {
	r := Report{
		Reconstruction: model.ReconstructionQuality{State: "not_checked"},
		Providers:      []model.Bucket{{Key: "provider.z", TotalMs: 10}, {Key: "provider.a", TotalMs: 10}},
		Types:          []TypeSummary{{TypeRow: model.TypeRow{ResourceType: "type.z", UITotalMs: 10}}, {TypeRow: model.TypeRow{ResourceType: "type.a", UITotalMs: 10}}},
		Resources:      model.ResourceProjection{Rows: []model.ResourceRow{{Address: "resource.z", UI: model.DurationTotal{TotalMs: 10}}, {Address: "resource.a", UI: model.DurationTotal{TotalMs: 10}}}},
	}
	d, err := buildJSONProfile(r, JSONMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{d.Aggregates.Providers[0].Provider, d.Aggregates.Providers[1].Provider, d.Aggregates.ResourceTypes[0].ResourceType, d.Aggregates.ResourceTypes[1].ResourceType, d.Aggregates.Resources[0].Address, d.Aggregates.Resources[1].Address}; !reflect.DeepEqual(got, []string{"provider.z", "provider.a", "type.z", "type.a", "resource.z", "resource.a"}) {
		t.Fatalf("aggregate identities reordered: %v", got)
	}
}

func TestJSONProjectionRejectsInvalidUTF8OnEveryVariableStringRoute(t *testing.T) {
	bad := string([]byte{0xff})
	valid := func() Report { return Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}} }
	cases := map[string]func(*Report, *JSONMetadata){
		"tool version": func(_ *Report, m *JSONMetadata) { m.ToolVersion = bad }, "input basename": func(_ *Report, m *JSONMetadata) { m.InputBasename = bad },
		"rpc exclusion": func(r *Report, _ *JSONMetadata) { r.Quality.RPC.Exclusions = map[string]uint64{bad: 1} }, "ui exclusion": func(r *Report, _ *JSONMetadata) { r.Quality.UI.Exclusions = map[string]uint64{bad: 1} }, "timeline exclusion": func(r *Report, _ *JSONMetadata) { r.Timeline.Analysis.Timing.Exclusions = map[string]uint64{bad: 1} },
		"issue stage": func(r *Report, _ *JSONMetadata) { r.Quality.Issues = []model.QualityIssue{{Stage: bad}} }, "issue code": func(r *Report, _ *JSONMetadata) { r.Quality.Issues = []model.QualityIssue{{Code: bad}} },
		"rpc method": func(r *Report, _ *JSONMetadata) { r.RPC = []Observation{{Span: span.Span{RPC: bad}}} }, "rpc provider": func(r *Report, _ *JSONMetadata) { r.RPC = []Observation{{Span: span.Span{Provider: bad}}} }, "rpc type": func(r *Report, _ *JSONMetadata) { r.RPC = []Observation{{Span: span.Span{ResourceType: bad}}} },
		"attributed address": func(r *Report, _ *JSONMetadata) {
			r.HasContext = true
			r.RPC = []Observation{{Attribution: attrib.Attribution{Confidence: attrib.Contained, Address: bad}}}
		},
		"ui address": func(r *Report, _ *JSONMetadata) { r.UI = []Observation{{Span: span.Span{Address: bad}}} }, "ui action": func(r *Report, _ *JSONMetadata) { r.UI = []Observation{{Span: span.Span{RPC: bad}}} }, "ui type": func(r *Report, _ *JSONMetadata) { r.UI = []Observation{{Span: span.Span{ResourceType: bad}}} },
		"provider aggregate": func(r *Report, _ *JSONMetadata) { r.Providers = []model.Bucket{{Key: bad}} }, "type aggregate": func(r *Report, _ *JSONMetadata) { r.Types = []TypeSummary{{TypeRow: model.TypeRow{ResourceType: bad}}} }, "resource aggregate": func(r *Report, _ *JSONMetadata) { r.Resources.Rows = []model.ResourceRow{{Address: bad}} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, m := valid(), JSONMetadata{}
			mutate(&r, &m)
			_, err := buildJSONProfile(r, m)
			if err == nil || err.Error() != "profile JSON contains invalid UTF-8" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func assertEqual(t *testing.T, name string, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got: %#v\nwant: %#v", name, got, want)
	}
}
func stringPointer(v string) *string  { return &v }
func uint32Pointer(v uint32) *uint32  { return &v }
func intPointer(v int) *int           { return &v }
func floatPointer(v float64) *float64 { return &v }
