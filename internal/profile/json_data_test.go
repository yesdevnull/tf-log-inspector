package profile

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestJSONProjectionRetainsPhysicalSources(t *testing.T) {
	l, err := model.Load("../../testdata/resources-accounting.log")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := buildJSONProfile(report, JSONMetadata{ToolVersion: "test", InputBasename: "run.log"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.RPCObservations) != len(report.RPC) {
		t.Fatal("lost observations")
	}
	found := false
	for _, row := range doc.RPCObservations {
		if row.Source != nil && row.Source.StartLine == 4 {
			found = true
			if row.Attribution.Address == nil || *row.Attribution.Address != "aws_instance.a" || row.Attribution.Confidence != "contained" {
				t.Fatalf("wrong source attribution: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("missing physical line 4")
	}
}

func TestJSONProjectionRetainsCompleteOrderedEvidence(t *testing.T) {
	for _, path := range []string{"../../testdata/two-tier.log", "../../testdata/resources-long-lower-bound.log", "../../testdata/core-only.log", "../tui/testdata/resource-association-confidence.log"} {
		l, err := model.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Build(l)
		if err != nil {
			t.Fatal(err)
		}
		d, err := buildJSONProfile(r, JSONMetadata{})
		if err != nil {
			t.Fatal(err)
		}
		if len(d.RPCObservations) != len(r.RPC) || len(d.UIObservations) != len(r.UI) || len(d.Aggregates.Providers) != len(r.Providers) || len(d.Aggregates.ResourceTypes) != len(r.Types) || len(d.Aggregates.Resources) != len(r.Resources.Rows) {
			t.Fatalf("%s: incomplete projection", path)
		}
		for i, row := range d.RPCObservations {
			if row.Index != i {
				t.Fatalf("%s: rpc order", path)
			}
		}
		for i, row := range d.UIObservations {
			if row.Index != i {
				t.Fatalf("%s: ui order", path)
			}
		}
		if l.ReconstructionQuality().State != "not_checked" || d.Quality.Reconstruction.State != "not_checked" {
			t.Fatalf("%s: reconstruction triggered", path)
		}
		switch path {
		case "../../testdata/two-tier.log":
			if d.Tiers.RPC.Admitted != 3 || d.Tiers.RPC.DurationMs != 410 || d.Tiers.UI.Admitted != 3 || d.Tiers.UI.DurationMs != 6000 || d.Timeline.Tier == nil || *d.Timeline.Tier != "rpc" || d.Timeline.Status != "complete" || d.Timeline.ClockOrigin == nil || *d.Timeline.ClockOrigin != "2026-09-03T23:15:03.4Z" {
				t.Fatalf("two-tier evidence = tiers=%+v timeline=%+v", d.Tiers, d.Timeline)
			}
			if got := []string{d.Aggregates.ResourceTypes[0].ResourceType, d.Aggregates.ResourceTypes[1].ResourceType, d.Aggregates.ResourceTypes[2].ResourceType}; !reflect.DeepEqual(got, []string{"aws_instance", "local_file", "aws_subnet"}) {
				t.Fatalf("two-tier type order = %v", got)
			}
			if d.Aggregates.ResourceTypes[0].UI.TotalMs != 5000 || d.Aggregates.ResourceTypes[0].RPC.TotalMs != 370 || d.Aggregates.ResourceTypes[0].UI.Count != 2 || d.Aggregates.ResourceTypes[0].RPC.Count != 2 {
				t.Fatalf("separate two-tier totals = %+v", d.Aggregates.ResourceTypes[0])
			}
		case "../../testdata/resources-long-lower-bound.log":
			if len(d.UIObservations) != 1 || !d.UIObservations[0].DurationLowerBound || d.UIObservations[0].DurationMs != ^uint32(0) || d.UIObservations[0].Position.Reasons[0] != "duration_saturated" || d.Tiers.UI.Excluded != 1 || !d.Aggregates.UI.LowerBound || !d.Aggregates.ResourceTypes[0].UI.LowerBound || d.Timeline.Status != "unavailable" {
				t.Fatalf("lower-bound fixture = ui=%+v tier=%+v timeline=%+v", d.UIObservations, d.Tiers.UI, d.Timeline)
			}
		case "../../testdata/core-only.log":
			if d.Timeline.Tier != nil || d.Timeline.Metrics != nil || d.Quality.Attribution != nil || d.Tiers.RPC.DurationAvailable || d.Tiers.UI.DurationAvailable {
				t.Fatalf("core-only absence = %+v %+v", d.Timeline, d.Quality)
			}
		case "../tui/testdata/resource-association-confidence.log":
			got := make([]string, len(d.RPCObservations))
			for i, row := range d.RPCObservations {
				got[i] = row.Attribution.Confidence
			}
			if !reflect.DeepEqual(got, []string{"unattributed", "contained", "likely", "overlapping", "ambiguous", "unattributed"}) {
				t.Fatalf("fixture confidence order = %v", got)
			}
			if d.Quality.NameableMs != 1500 || d.Quality.RPCDurationMs != 4000 || d.Quality.NameableShare == nil || *d.Quality.NameableShare != 0.375 {
				t.Fatalf("fixture quality totals = %+v", d.Quality)
			}
		}
	}
}

func TestJSONProjectionMapsExactValuesNullsAndOrdering(t *testing.T) {
	origin := time.Date(2026, 9, 11, 1, 2, 3, 4, time.FixedZone("x", 3600))
	share := 0.25
	first := uint32(0)
	tier := span.FidelityReported
	r := Report{
		Bytes: 1<<53 + 9, HasContext: true, Reconstruction: model.ReconstructionQuality{State: "checked", Responses: 0},
		Quality:   model.CaptureQuality{HasContext: true, RPC: model.TierQuality{Records: 3, Admitted: 2, Rejected: 1, Positioned: 1, DurationMs: 1<<53 + 7, PositionedMs: 0, ExcludedMs: 9, DurationLowerBound: true, Origin: &origin, Exclusions: map[string]uint64{"z": 2}}, Issues: []model.QualityIssue{{Stage: "scan", Code: "x", Count: 1, FirstEntry: &first}}, Attribution: attrib.Coverage{Spans: 2, TotalMs: 9, Candidates: map[uint32]int{10: 1, 2: 3}}, NameableMs: 2, RPCDurationMs: 9, NameableShare: &share},
		RPC:       []Observation{{Index: 0, Span: span.Span{Entry: 5, DurationMs: 9, TimestampStatus: logfmt.TimestampMissing}, Attribution: attrib.Attribution{Confidence: attrib.Ambiguous, Candidates: 2}}, {Index: 1, Span: span.Span{Entry: 4, StartMs: 0, EndMs: 0, DurationMs: 0, StartClamped: true, TimestampStatus: logfmt.TimestampValid, RPC: "m", Provider: "p", ResourceType: "t"}, Attribution: attrib.Attribution{Confidence: attrib.Overlapping, Address: "a", Candidates: 1}}},
		Timeline:  Timeline{Tier: &tier, PositionedIndices: []int{1}, Analysis: model.TimingAnalysis{Timing: model.TimingSelection{AdmittedCount: 2, ExcludedCount: 1, AdmittedMs: 9, PositionedMs: 0, ExcludedMs: 9, AdmittedLowerBound: true, ExcludedLowerBound: true, Exclusions: map[string]uint64{"timestamp_missing": 1}}, Metrics: &model.TimingMetrics{WindowMs: 0, Peak: 1, BusyMs: 0}, Intervals: []model.Stall{{StartMs: 0, EndMs: 0, MinRunning: 0, MaxRunning: 1, Capacity: 1, Blocking: 0}, {StartMs: 1, EndMs: 2, Blocking: -1}}}},
		Resources: model.ResourceProjection{UI: model.DurationTotal{Count: 1, TotalMs: 1<<53 + 3}, UnnamedUI: model.DurationTotal{Count: 1}, Evidence: model.ResourceEvidence{Baseline: model.DurationTotal{Count: 2, TotalMs: 1<<53 + 5}}},
	}
	d, err := buildJSONProfile(r, JSONMetadata{ToolVersion: "v", InputBasename: "x.log"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Input.Bytes != 1<<53+9 || d.Tiers.RPC.DurationMs != 1<<53+7 || d.Aggregates.UI.TotalMs != 1<<53+3 {
		t.Fatal("wide integers changed")
	}
	if d.RPCObservations[1].Position.StartMs == nil || *d.RPCObservations[1].Position.StartMs != 0 || !d.RPCObservations[1].Position.StartClamped || d.RPCObservations[0].Position.StartMs != nil {
		t.Fatal("position zero/null changed")
	}
	if d.RPCObservations[1].Attribution.Address == nil || d.RPCObservations[0].Attribution.Address != nil {
		t.Fatal("attribution address contract")
	}
	if got := []uint32{d.Quality.Attribution.CandidateCounts[0].Candidates, d.Quality.Attribution.CandidateCounts[1].Candidates}; !reflect.DeepEqual(got, []uint32{2, 10}) {
		t.Fatalf("candidate order %v", got)
	}
	if d.Quality.Reconstruction.Responses == nil || *d.Quality.Reconstruction.Responses != 0 || d.Quality.Reconstruction.Code != nil {
		t.Fatal("checked-zero reconstruction")
	}
	if d.Timeline.Status != "partial" || d.Timeline.Metrics == nil || d.Timeline.Metrics.SummedWindowRatio != nil || d.Timeline.Intervals[0].ActiveObservationIndex == nil || *d.Timeline.Intervals[0].ActiveObservationIndex != 1 || d.Timeline.Intervals[1].ActiveObservationIndex != nil {
		t.Fatal("timeline mapping/nulls")
	}
	if d.Tiers.RPC.ClockOrigin == nil || *d.Tiers.RPC.ClockOrigin != "2026-09-11T00:02:03.000000004Z" {
		t.Fatalf("origin %v", d.Tiers.RPC.ClockOrigin)
	}
}

func TestJSONProjectionRejectsInvalidStatesAndUTF8(t *testing.T) {
	bad := string([]byte{0xff})
	for _, tc := range []struct {
		name     string
		report   Report
		metadata JSONMetadata
		want     string
	}{
		{"metadata", Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, JSONMetadata{ToolVersion: bad}, "profile JSON contains invalid UTF-8"},
		{"identifier", Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}, RPC: []Observation{{Span: span.Span{RPC: bad}}}}, JSONMetadata{}, "profile JSON contains invalid UTF-8"},
		{"reconstruction", Report{Reconstruction: model.ReconstructionQuality{State: "mystery"}}, JSONMetadata{}, "profile JSON has invalid reconstruction state"},
		{"confidence", Report{HasContext: true, Quality: model.CaptureQuality{HasContext: true}, Reconstruction: model.ReconstructionQuality{State: "not_checked"}, RPC: []Observation{{Attribution: attrib.Attribution{Confidence: 99}}}}, JSONMetadata{}, "profile JSON has invalid attribution confidence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildJSONProfile(tc.report, tc.metadata)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v", err)
			}
			if errors.Is(err, errInvalidProfileUTF8) && err.Error() != tc.want {
				t.Fatal("error echoed input")
			}
		})
	}
	badTier := span.FidelityPaired
	_, err := buildJSONProfile(Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}, Timeline: Timeline{Tier: &badTier}}, JSONMetadata{})
	if err == nil || err.Error() != "profile JSON has invalid timing tier" {
		t.Fatalf("tier error %v", err)
	}
	goodTier := span.FidelityReported
	for _, blocking := range []int{-2, 1} {
		r := Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}, Timeline: Timeline{Tier: &goodTier, Analysis: model.TimingAnalysis{Metrics: &model.TimingMetrics{}, Intervals: []model.Stall{{Blocking: blocking}}}}}
		_, err := buildJSONProfile(r, JSONMetadata{})
		if err == nil || err.Error() != "profile interval observation index out of range" {
			t.Fatalf("blocking %d error %v", blocking, err)
		}
	}
	if _, err := buildJSONProfile(Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}, RPC: []Observation{{Attribution: attrib.Attribution{Address: bad}}}}, JSONMetadata{}); err != nil {
		t.Fatalf("unexported no-context attribution was validated: %v", err)
	}
}

func TestJSONProjectionMapsAllConfidenceStates(t *testing.T) {
	r := Report{HasContext: true, Quality: model.CaptureQuality{HasContext: true}, Reconstruction: model.ReconstructionQuality{State: "failed", Code: "reconstruction_failed"}}
	for i, c := range []attrib.Confidence{attrib.Unattributed, attrib.Ambiguous, attrib.Overlapping, attrib.Likely, attrib.Contained} {
		r.RPC = append(r.RPC, Observation{Index: i, Attribution: attrib.Attribution{Confidence: c, Address: "a", Candidates: uint32(i)}})
	}
	d, err := buildJSONProfile(r, JSONMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"unattributed", "ambiguous", "overlapping", "likely", "contained"}
	for i, v := range want {
		if d.RPCObservations[i].Attribution.Confidence != v {
			t.Fatalf("confidence %d", i)
		}
	}
	if d.RPCObservations[0].Attribution.Address != nil || d.RPCObservations[1].Attribution.Address != nil || d.RPCObservations[2].Attribution.Address == nil {
		t.Fatal("confidence address mapping")
	}
	if d.Quality.Reconstruction.Code == nil || *d.Quality.Reconstruction.Code != "reconstruction_failed" || d.Quality.Reconstruction.Responses != nil {
		t.Fatal("failed reconstruction mapping")
	}
}

func TestJSONProjectionDoesNotTruncateOrShareMutableEvidence(t *testing.T) {
	r := generatedBenchmarkReport(25)
	r.UI = make([]Observation, 25)
	r.Resources.Rows = make([]model.ResourceRow, 25)
	for i := range 25 {
		r.UI[i] = Observation{Index: i, Span: span.Span{Address: "ui", RPC: "read", ResourceType: "type"}, Source: &model.SourceLocation{Entry: uint32(i), StartLine: uint64(i + 1)}}
		r.Resources.Rows[i] = model.ResourceRow{Address: "resource", Operations: []model.ResourceOperation{{UIIndex: i}}}
	}
	r.Quality.RPC.Exclusions = map[string]uint64{"timestamp_missing": 1}
	d, err := buildJSONProfile(r, JSONMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.RPCObservations) != 25 || len(d.UIObservations) != 25 || len(d.Aggregates.Providers) != 25 || len(d.Aggregates.ResourceTypes) != 25 || len(d.Aggregates.Resources) != 25 {
		t.Fatal("projection truncated complete arrays")
	}
	d.UIObservations[0].Source.StartLine = 999
	d.Tiers.RPC.Exclusions["timestamp_missing"] = 9
	d.Aggregates.Resources[0].UIObservationIndices[0] = 24
	if r.UI[0].Source.StartLine != 1 || r.Quality.RPC.Exclusions["timestamp_missing"] != 1 || r.Resources.Rows[0].Operations[0].UIIndex != 0 {
		t.Fatal("projection shares mutable report storage")
	}
}
