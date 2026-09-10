package model

import (
	"errors"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestAnalyseTimingSeparatesBusyExtentFromDurations(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 4000, DurationMs: 6000, StartClamped: true, TimestampStatus: logfmt.TimestampValid},
		{StartMs: 2000, EndMs: 6000, DurationMs: 4000, TimestampStatus: logfmt.TimestampValid},
	}
	got, err := AnalyseTiming(SelectTiming(spans))
	if err != nil {
		t.Fatal(err)
	}
	if got.Metrics == nil || got.Metrics.WindowMs != 6000 ||
		got.Metrics.BusyMs != 6000 || got.Metrics.Peak != 2 ||
		got.Timing.AdmittedMs != 10000 {
		t.Fatalf("incorrect duration/extent analysis: %+v", got)
	}
	if got.Metrics.BusyFraction == nil || *got.Metrics.BusyFraction != 1 {
		t.Fatal("busy fraction must use union extent")
	}
}

func TestAnalyseTimingLeavesMetricsUnavailableWithoutPositionedSpans(t *testing.T) {
	tests := []struct {
		name   string
		spans  []span.Span
		count  int
		ms     uint64
		reason string
	}{
		{name: "nil input"},
		{
			name:   "missing timestamps",
			spans:  []span.Span{{DurationMs: 2500, TimestampStatus: logfmt.TimestampMissing}},
			count:  1,
			ms:     2500,
			reason: "timestamp_missing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AnalyseTiming(SelectTiming(tc.spans))
			if err != nil {
				t.Fatal(err)
			}
			if got.Metrics != nil || got.ThresholdMs != 0 || len(got.Intervals) != 0 {
				t.Fatalf("analysis = %+v, want unavailable temporal values", got)
			}
			if got.Timing.AdmittedCount != tc.count || got.Timing.AdmittedMs != tc.ms {
				t.Fatalf("timing = %+v, want admitted count %d and duration %d", got.Timing, tc.count, tc.ms)
			}
			if tc.reason != "" && got.Timing.Exclusions[tc.reason] != 1 {
				t.Fatalf("exclusions = %v, want %s", got.Timing.Exclusions, tc.reason)
			}
		})
	}
}

func TestAnalyseTimingPreservesExcludedEvidenceAlongsidePositionedMetrics(t *testing.T) {
	spans := []span.Span{
		{StartMs: 1000, EndMs: 3000, DurationMs: 2000, TimestampStatus: logfmt.TimestampValid},
		{DurationMs: 4000, TimestampStatus: logfmt.TimestampMissing},
		{DurationMs: 5000, TimestampStatus: logfmt.TimestampValid, DurationSaturated: true},
	}
	got, err := AnalyseTiming(SelectTiming(spans))
	if err != nil {
		t.Fatal(err)
	}
	if got.Metrics == nil || got.Metrics.WindowMs != 3000 || got.Metrics.BusyMs != 2000 || got.Metrics.Peak != 1 {
		t.Fatalf("metrics = %+v, want positioned span's clock", got.Metrics)
	}
	if got.Metrics.BusyFraction == nil || *got.Metrics.BusyFraction != 2.0/3.0 {
		t.Fatalf("busy fraction = %v, want 2/3", got.Metrics.BusyFraction)
	}
	if got.Timing.AdmittedMs != 11000 || got.Timing.PositionedMs != 2000 || got.Timing.ExcludedMs != 9000 ||
		got.Timing.ExcludedCount != 2 || !got.Timing.AdmittedLowerBound || !got.Timing.ExcludedLowerBound {
		t.Fatalf("timing evidence = %+v", got.Timing)
	}
	if got.Timing.Exclusions["timestamp_missing"] != 1 || got.Timing.Exclusions["duration_saturated"] != 1 {
		t.Fatalf("exclusions = %v", got.Timing.Exclusions)
	}
}

func TestAnalyseTimingKeepsUISaturationOutOfTheUIClock(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 2000, DurationMs: 2000, TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityUIReported},
		{DurationMs: 4000, TimestampStatus: logfmt.TimestampValid, DurationSaturated: true, Fidelity: span.FidelityUIReported},
	}
	got, err := AnalyseTiming(SelectTiming(spans))
	if err != nil {
		t.Fatal(err)
	}
	if got.Metrics == nil || got.Metrics.WindowMs != 2000 || got.Metrics.BusyMs != 2000 || got.Timing.AdmittedMs != 6000 {
		t.Fatalf("analysis = %+v", got)
	}
	if !got.Timing.AdmittedLowerBound || !got.Timing.ExcludedLowerBound || got.Timing.Exclusions["duration_saturated"] != 1 {
		t.Fatalf("saturation evidence = %+v", got.Timing)
	}
}

func TestAnalyseTimingMeasuresPositionedZeroExtents(t *testing.T) {
	tests := []struct {
		name       string
		at         uint32
		wantWindow uint32
	}{
		{name: "at origin", at: 0, wantWindow: 0},
		{name: "after origin", at: 2500, wantWindow: 2500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spans := []span.Span{{StartMs: tc.at, EndMs: tc.at, TimestampStatus: logfmt.TimestampValid}}
			got, err := AnalyseTiming(SelectTiming(spans))
			if err != nil {
				t.Fatal(err)
			}
			if got.Metrics == nil || got.Metrics.WindowMs != tc.wantWindow || got.Metrics.Peak != 0 || got.Metrics.BusyMs != 0 {
				t.Fatalf("metrics = %+v", got.Metrics)
			}
			if got.ThresholdMs != 1000 || len(got.Intervals) != 0 {
				t.Fatalf("threshold/intervals = %d/%+v", got.ThresholdMs, got.Intervals)
			}
			if tc.wantWindow == 0 && got.Metrics.BusyFraction != nil {
				t.Fatalf("busy fraction = %v, want unavailable for a zero window", got.Metrics.BusyFraction)
			}
			if tc.wantWindow > 0 && (got.Metrics.BusyFraction == nil || *got.Metrics.BusyFraction != 0) {
				t.Fatalf("busy fraction = %v, want measured zero", got.Metrics.BusyFraction)
			}
		})
	}
}

func TestAnalyseTimingTreatsSimultaneousHandoversAsContinuouslyBusy(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 2000, DurationMs: 2000, TimestampStatus: logfmt.TimestampValid},
		{StartMs: 2000, EndMs: 4000, DurationMs: 2000, TimestampStatus: logfmt.TimestampValid},
	}
	got, err := AnalyseTiming(SelectTiming(spans))
	if err != nil {
		t.Fatal(err)
	}
	if got.Metrics == nil || got.Metrics.WindowMs != 4000 || got.Metrics.BusyMs != 4000 || got.Metrics.Peak != 1 || len(got.Intervals) != 0 {
		t.Fatalf("analysis = %+v", got)
	}
}

func TestAnalyseTimingRejectsMixedTimelineClocks(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 2000, TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityReported},
		{StartMs: 0, EndMs: 2000, TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityUIReported},
	}
	got, err := AnalyseTiming(SelectTiming(spans))
	if !errors.Is(err, ErrMixedTimelines) {
		t.Fatalf("AnalyseTiming() error = %v, want %v", err, ErrMixedTimelines)
	}
	if got.Metrics != nil || got.Timing.AdmittedCount != 0 {
		t.Fatalf("analysis on error = %+v, want zero value", got)
	}
}

func TestAnalyseTimingReturnsEveryIntervalInChronologicalOrder(t *testing.T) {
	spans := []span.Span{
		{StartMs: 1000, EndMs: 2000, DurationMs: 1000, TimestampStatus: logfmt.TimestampValid},
		{StartMs: 3000, EndMs: 4000, DurationMs: 1000, TimestampStatus: logfmt.TimestampValid},
		{StartMs: 5000, EndMs: 6000, DurationMs: 1000, TimestampStatus: logfmt.TimestampValid},
		{StartMs: 7000, EndMs: 8000, DurationMs: 1000, TimestampStatus: logfmt.TimestampValid},
		{StartMs: 9000, EndMs: 10000, DurationMs: 1000, TimestampStatus: logfmt.TimestampValid},
	}
	got, err := AnalyseTiming(SelectTiming(spans))
	if err != nil {
		t.Fatal(err)
	}
	if got.ThresholdMs != 1000 || len(got.Intervals) != 5 {
		t.Fatalf("threshold/intervals = %d/%+v, want all five equal intervals", got.ThresholdMs, got.Intervals)
	}
	for i, interval := range got.Intervals {
		wantStart := uint32(i * 2000)
		if interval.StartMs != wantStart || interval.EndMs != wantStart+1000 || interval.Blocking != -1 {
			t.Fatalf("interval %d = %+v, want [%d,%d) idle", i, interval, wantStart, wantStart+1000)
		}
	}
}
