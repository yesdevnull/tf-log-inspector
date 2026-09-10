package profile

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestRPCIntervalRetainsAssociatedObservationEvidence(t *testing.T) {
	tier := span.FidelityReported
	report := Report{
		HasContext: true,
		RPC: []Observation{{
			Span:        span.Span{DurationMs: 4000, RPC: "ReadResource", ResourceType: "aws_instance", Provider: "registry.terraform.io/hashicorp/aws"},
			Source:      &model.SourceLocation{StartLine: 12, EndLine: 14},
			Attribution: attrib.Attribution{Address: "module.web.aws_instance.app[0]", Confidence: attrib.Contained},
		}},
		RPCRanking: []int{0},
		Timeline: Timeline{Tier: &tier, PositionedIndices: []int{0}, Analysis: model.TimingAnalysis{
			Timing:    model.TimingSelection{AdmittedCount: 1, PositionedMs: 4000},
			Metrics:   &model.TimingMetrics{WindowMs: 5000, Peak: 2},
			Intervals: []model.Stall{{StartMs: 1000, EndMs: 5000, MinRunning: 1, MaxRunning: 1, Capacity: 2, Blocking: 0}},
		}},
	}
	got := renderTemporal(t, report)
	for _, want := range []string{
		"CONCURRENCY (RPC tier)", "analysis: complete", "positioned observations 1 of 1 admitted", "window (zero to latest positioned end) 5.0s",
		"longest observed active observation: ReadResource aws_instance registry.terraform.io/hashicorp/aws",
		"resource: module.web.aws_instance.app[0] (contained)", "source: lines 12-14",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"blocking observation", "dependency-critical path"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("interval makes forbidden claim %q:\n%s", forbidden, got)
		}
	}
}

func TestUIIntervalRetainsObservedResourceEvidence(t *testing.T) {
	tier := span.FidelityUIReported
	report := Report{
		UI:        []Observation{{Span: span.Span{DurationMs: 3000, RPC: "apply", ResourceType: "aws_instance", Address: "aws_instance.example"}, Source: &model.SourceLocation{StartLine: 24, EndLine: 24}}},
		UIRanking: []int{0},
		Timeline: Timeline{Tier: &tier, PositionedIndices: []int{0}, Analysis: model.TimingAnalysis{
			Timing: model.TimingSelection{AdmittedCount: 1, PositionedMs: 3000}, Metrics: &model.TimingMetrics{WindowMs: 4000, Peak: 1},
			Intervals: []model.Stall{{StartMs: 1000, EndMs: 4000, MinRunning: 1, MaxRunning: 1, Capacity: 1, Blocking: 0}},
		}},
	}
	got := renderTemporal(t, report)
	for _, want := range []string{
		"CONCURRENCY (UI tier)", "longest observed active observation: apply aws_instance", "resource: aws_instance.example (observed UI)", "source: line 24",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestTemporalAnalysisStatesAndZeroWorkUseExactEvidenceWording(t *testing.T) {
	cases := []struct {
		name      string
		tier      span.Fidelity
		analysis  model.TimingAnalysis
		want      []string
		forbidden []string
	}{
		{name: "unavailable", tier: span.FidelityReported, analysis: model.TimingAnalysis{Timing: model.TimingSelection{AdmittedCount: 2}}, want: []string{"clock origin: unavailable", "analysis: unavailable", "temporal metrics: unavailable"}, forbidden: []string{"analysis: partial", "analysis: complete", "no qualifying intervals"}},
		{name: "partial", tier: span.FidelityReported, analysis: model.TimingAnalysis{Timing: model.TimingSelection{AdmittedCount: 2, ExcludedCount: 1, Exclusions: map[string]uint64{"timestamp_missing": 1}}, Metrics: &model.TimingMetrics{}}, want: []string{"analysis: partial", "positioned observations 1 of 2 admitted", "busy / window unavailable", "summed / window unavailable"}, forbidden: []string{"analysis: complete", "analysis: unavailable"}},
		{name: "complete and no RPC work", tier: span.FidelityReported, analysis: model.TimingAnalysis{Timing: model.TimingSelection{AdmittedCount: 1}, Metrics: &model.TimingMetrics{}, Intervals: []model.Stall{{StartMs: 0, EndMs: 1000, Blocking: -1}}}, want: []string{"analysis: complete", "busy / window unavailable", "summed / window unavailable", "no observed RPC work"}, forbidden: []string{"analysis: partial", "analysis: unavailable", "blocking"}},
		{name: "complete and no UI work", tier: span.FidelityUIReported, analysis: model.TimingAnalysis{Timing: model.TimingSelection{AdmittedCount: 1}, Metrics: &model.TimingMetrics{}, Intervals: []model.Stall{{StartMs: 0, EndMs: 1000, Blocking: -1}}}, want: []string{"CONCURRENCY (UI tier)", "no observed UI work"}, forbidden: []string{"no observed RPC work", "blocking"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := Report{RPC: []Observation{{Span: span.Span{DurationMs: 1}}}, UI: []Observation{{Span: span.Span{DurationMs: 1}}}, RPCRanking: []int{0}, UIRanking: []int{0}, Timeline: Timeline{Tier: &tc.tier, Analysis: tc.analysis}}
			got := renderTemporal(t, report)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q:\n%s", want, got)
				}
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(got, forbidden) {
					t.Errorf("unexpected %q:\n%s", forbidden, got)
				}
			}
		})
	}
}

func TestParsedMixedLogUsesRPCOriginInsteadOfUIOrigin(t *testing.T) {
	l, err := model.Load("../../testdata/two-tier.log")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Render(&out, l, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "CONCURRENCY (RPC tier)") || !strings.Contains(got, "clock origin: 2026-09-03T23:15:03.4Z") {
		t.Fatalf("mixed log did not use independently checked RPC origin:\n%s", got)
	}
	if strings.Contains(got, "clock origin: 2026-09-03T23:15:02Z") {
		t.Fatalf("mixed log used UI origin for RPC intervals:\n%s", got)
	}
}

func TestInvalidIntervalObservationMappingFailsBeforeWriting(t *testing.T) {
	tier := span.FidelityReported
	report := Report{RPC: []Observation{{Span: span.Span{DurationMs: 1}}}, RPCRanking: []int{0}, Timeline: Timeline{Tier: &tier, PositionedIndices: []int{3}, Analysis: model.TimingAnalysis{Metrics: &model.TimingMetrics{WindowMs: 2}, Intervals: []model.Stall{{StartMs: 0, EndMs: 2, Blocking: 0}}}}}
	var out strings.Builder
	err := renderReport(&out, report, TextOptions{})
	if err == nil || err.Error() != "profile interval observation index out of range" {
		t.Fatalf("mapping error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("partial report written before validation: %q", out.String())
	}
}

func renderTemporal(t *testing.T, report Report) string {
	t.Helper()
	var out strings.Builder
	if err := renderReport(&out, report, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
