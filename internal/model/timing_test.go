package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestSelectTimingSeparatesPositionedSpansFromDurationTotals(t *testing.T) {
	valid := span.Span{Entry: 7, DurationMs: 10, TimestampStatus: logfmt.TimestampValid}
	unpositioned := span.Span{Entry: 9, DurationMs: 20, TimestampStatus: logfmt.TimestampInvalid}
	got := SelectTiming([]span.Span{valid, unpositioned})

	if got.AdmittedCount != 2 || got.AdmittedMs != 30 || got.PositionedMs != 10 || got.ExcludedCount != 1 || got.ExcludedMs != 20 {
		t.Fatalf("selection totals = %+v", got)
	}
	if len(got.Positioned) != 1 || got.Positioned[0].Entry != 7 {
		t.Fatalf("positioned = %+v, want entry 7 retained in input order", got.Positioned)
	}
	if got.Exclusions["timestamp_invalid"] != 1 || len(got.Exclusions) != 1 {
		t.Fatalf("exclusions = %v, want one timestamp_invalid observation", got.Exclusions)
	}
}

func TestSelectTimingCountsOneExcludedObservationForEachPositionReason(t *testing.T) {
	s := span.Span{DurationMs: 20, TimestampStatus: logfmt.TimestampInvalid, DurationSaturated: true}
	got := SelectTiming([]span.Span{s})

	if got.ExcludedCount != 1 || got.ExcludedMs != 20 || !got.AdmittedLowerBound || !got.ExcludedLowerBound {
		t.Fatalf("selection = %+v", got)
	}
	if got.Exclusions["timestamp_invalid"] != 1 || got.Exclusions["duration_saturated"] != 1 || len(got.Exclusions) != 2 {
		t.Fatalf("exclusions = %v, want one count for each reason", got.Exclusions)
	}
}

func TestSelectTimingHandlesAllUnpositionedAndEmptyInputs(t *testing.T) {
	unpositioned := span.Span{DurationMs: 20, TimestampStatus: logfmt.TimestampMissing}
	got := SelectTiming([]span.Span{unpositioned})
	if got.AdmittedCount != 1 || got.ExcludedCount != 1 || len(got.Positioned) != 0 || got.Exclusions["timestamp_missing"] != 1 {
		t.Fatalf("all-unpositioned selection = %+v", got)
	}

	empty := SelectTiming(nil)
	if empty.AdmittedCount != 0 || empty.ExcludedCount != 0 || empty.AdmittedMs != 0 || empty.PositionedMs != 0 || empty.ExcludedMs != 0 || len(empty.Positioned) != 0 || len(empty.Exclusions) != 0 {
		t.Fatalf("empty selection = %+v", empty)
	}
}

func TestPreferredTimingDoesNotMergeTimingTiers(t *testing.T) {
	cases := []struct {
		name string
		rpc  []span.Span
		ui   []span.Span
		want span.Fidelity
		ok   bool
	}{
		{name: "rpc preferred over ui", rpc: []span.Span{{DurationMs: 10}}, ui: []span.Span{{DurationMs: 20}}, want: span.FidelityReported, ok: true},
		{name: "ui when rpc absent", ui: []span.Span{{DurationMs: 20}}, want: span.FidelityUIReported, ok: true},
		{name: "no admitted timing", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PreferredTiming(&Log{RPCSpans: tc.rpc, UISpans: tc.ui})
			if got != tc.want || ok != tc.ok {
				t.Fatalf("PreferredTiming() = (%v, %t), want (%v, %t)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
