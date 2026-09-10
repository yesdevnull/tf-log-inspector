package model

import (
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestCompareSeparatesVolumeAndMean(t *testing.T) {
	before := ComparisonInput{RPC: []span.Span{
		{Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 10},
		{Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 10},
	}}
	after := ComparisonInput{RPC: []span.Span{
		{Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 20},
	}}
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sections) != 5 || len(got.Sections[2].Rows) != 1 {
		t.Fatalf("sections: %+v", got.Sections)
	}
	row := got.Sections[2].Rows[0]
	if row.State != "matched" || row.Before == nil || row.After == nil {
		t.Fatalf("row: %+v", row)
	}
	if row.Changes.Count == nil || *row.Changes.Count != (SignedChange{Negative: true, Magnitude: 1}) {
		t.Fatalf("count: %+v", row.Changes.Count)
	}
	if row.Changes.TotalMs == nil || row.Changes.TotalMs.Magnitude != 0 {
		t.Fatalf("total: %+v", row.Changes.TotalMs)
	}
	if row.Changes.MeanMs == nil || *row.Changes.MeanMs != 10 {
		t.Fatalf("mean: %+v", row.Changes.MeanMs)
	}
}

func TestComparisonAvailabilityPresenceAndPercentages(t *testing.T) {
	before := ComparisonInput{RPC: []span.Span{
		{Provider: "p", ResourceType: "same", RPC: "Read", DurationMs: 0},
		{Provider: "p", ResourceType: "removed", RPC: "Read", DurationMs: 5},
	}}
	after := ComparisonInput{RPC: []span.Span{
		{Provider: "p", ResourceType: "same", RPC: "Read", DurationMs: 10},
		{Provider: "p", ResourceType: "added", RPC: "Read", DurationMs: 7},
	}}
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	rows := got.Sections[1].Rows
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	byKey := make(map[string]ComparisonRow)
	for _, row := range rows {
		byKey[row.Key.ResourceType] = row
	}
	same := byKey["same"]
	if same.State != "matched" || same.Changes.TotalMs == nil || same.Changes.TotalMs.Magnitude != 10 ||
		same.Changes.TotalPercent != nil || same.Changes.MeanPercent != nil || same.Changes.MaxPercent != nil {
		t.Fatalf("zero baseline row: %+v", same)
	}
	added := byKey["added"]
	if added.State != "added" || added.Before.Count != 0 || added.Before.MeanMs != nil || added.Before.MaxMs != nil ||
		added.Changes.Count == nil || added.Changes.Count.Magnitude != 1 || added.Changes.TotalMs.Magnitude != 7 {
		t.Fatalf("added row: %+v", added)
	}
	removed := byKey["removed"]
	if removed.State != "removed" || removed.After.Count != 0 || removed.After.MeanMs != nil || removed.After.MaxMs != nil ||
		removed.Changes.TotalMs == nil || !removed.Changes.TotalMs.Negative || removed.Changes.TotalMs.Magnitude != 5 {
		t.Fatalf("removed row: %+v", removed)
	}
}

func TestComparisonUnavailableTiers(t *testing.T) {
	got, err := Compare(ComparisonInput{}, ComparisonInput{RPC: []span.Span{{Provider: "p", DurationMs: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sections) != 5 || got.Sections[0].BeforeAvailable || !got.Sections[0].AfterAvailable ||
		len(got.Sections[0].Rows) != 1 {
		t.Fatalf("sections: %+v", got.Sections)
	}
	row := got.Sections[0].Rows[0]
	if row.State != "unavailable" || row.Before != nil || row.After == nil || row.After.MeanMs == nil ||
		row.Changes.Count != nil {
		t.Fatalf("unavailable row: %+v", row)
	}
	if got.Sections[3].BeforeAvailable || got.Sections[3].AfterAvailable || len(got.Sections[3].Rows) != 0 {
		t.Fatalf("disjoint UI tier: %+v", got.Sections[3])
	}
}

func TestComparisonUIGroupingAndUnnamedAccounting(t *testing.T) {
	before := ComparisonInput{UI: []span.Span{
		{Address: "a|b", ResourceType: "r", RPC: "create", DurationMs: 1},
		{Address: "a|b", ResourceType: "r", RPC: "create", DurationMs: 2},
		{Address: "a", ResourceType: "r", RPC: "b|create", DurationMs: 4},
		{ResourceType: "r", RPC: "ignored", DurationMs: 8},
	}}
	got, err := Compare(before, before)
	if err != nil {
		t.Fatal(err)
	}
	if got.BeforeUnnamedUI == nil || got.BeforeUnnamedUI.Count != 1 || got.BeforeUnnamedUI.TotalMs != 8 ||
		got.BeforeUnnamedUI.MeanMs == nil || *got.BeforeUnnamedUI.MeanMs != 8 {
		t.Fatalf("unnamed UI: %+v", got.BeforeUnnamedUI)
	}
	if len(got.Sections[3].Rows) != 1 || got.Sections[3].Rows[0].Before.Count != 4 ||
		len(got.Sections[4].Rows) != 2 {
		t.Fatalf("UI sections: %+v / %+v", got.Sections[3], got.Sections[4])
	}
	operations := got.Sections[4].Rows
	if operations[0].Key == operations[1].Key {
		t.Fatalf("compound keys collided: %+v", operations)
	}
}

func TestComparisonIndependentlyRenamedUIAddressesRemainDistinct(t *testing.T) {
	before := ComparisonInput{UI: []span.Span{
		{Address: "sanitised_before", ResourceType: "r", RPC: "read", DurationMs: 1},
		{Address: "stable", ResourceType: "r", RPC: "read", DurationMs: 1},
	}}
	after := ComparisonInput{UI: []span.Span{
		{Address: "sanitised_after", ResourceType: "r", RPC: "read", DurationMs: 1},
		{Address: "stable", ResourceType: "r", RPC: "read", DurationMs: 1},
	}}
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	states := make(map[string]string)
	for _, row := range got.Sections[4].Rows {
		states[row.Key.Address] = row.State
	}
	if states["sanitised_before"] != "removed" || states["sanitised_after"] != "added" ||
		states["stable"] != "matched" {
		t.Fatalf("operation states: %+v", states)
	}
}

func TestComparisonRawIdentifiersProviderIdentityAndOrdering(t *testing.T) {
	before := ComparisonInput{RPC: []span.Span{
		{Provider: "(none)", ResourceType: "a|b", RPC: "c", DurationMs: 3},
		{Provider: "", ResourceType: "a", RPC: "b|c", DurationMs: 3},
	}}
	after := ComparisonInput{RPC: slices.Clone(before.RPC)}
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderIdentityStatus != "unknown" || !slices.Equal(got.BeforeProviders, []string{"(none)"}) {
		t.Fatalf("provider identity: %q %+v", got.ProviderIdentityStatus, got.BeforeProviders)
	}
	if len(got.Sections[0].Rows) != 2 || got.Sections[0].Rows[0].Key.Provider != "" ||
		got.Sections[0].Rows[1].Key.Provider != "(none)" || len(got.Sections[2].Rows) != 2 {
		t.Fatalf("raw identifiers: %+v / %+v", got.Sections[0].Rows, got.Sections[2].Rows)
	}

	different, err := Compare(
		ComparisonInput{RPC: []span.Span{{Provider: "a"}}},
		ComparisonInput{RPC: []span.Span{{Provider: "b"}}},
	)
	if err != nil || different.ProviderIdentityStatus != "different" {
		t.Fatalf("different identity: %q, %v", different.ProviderIdentityStatus, err)
	}
	same, err := Compare(
		ComparisonInput{RPC: []span.Span{{Provider: "b"}, {Provider: "a"}}},
		ComparisonInput{RPC: []span.Span{{Provider: "a"}, {Provider: "b"}, {Provider: "a"}}},
	)
	if err != nil || same.ProviderIdentityStatus != "same" ||
		!slices.Equal(same.BeforeProviders, []string{"a", "b"}) {
		t.Fatalf("same identity: %+v, %v", same, err)
	}
}

func TestComparisonLowerBoundSuppressesTimingChanges(t *testing.T) {
	for _, saturatedSide := range []string{"before", "after"} {
		t.Run(saturatedSide, func(t *testing.T) {
			beforeSpan := span.Span{Provider: "p", DurationMs: 5}
			afterSpan := span.Span{Provider: "p", DurationMs: 10}
			if saturatedSide == "before" {
				beforeSpan.DurationSaturated = true
			} else {
				afterSpan.DurationSaturated = true
			}
			got, err := Compare(ComparisonInput{RPC: []span.Span{beforeSpan}}, ComparisonInput{RPC: []span.Span{afterSpan}})
			if err != nil {
				t.Fatal(err)
			}
			row := got.Sections[0].Rows[0]
			if row.Changes.Count == nil || row.Changes.Count.Magnitude != 0 || row.Changes.TotalMs != nil ||
				row.Changes.MeanMs != nil || row.Changes.MaxMs != nil || row.Changes.TotalPercent != nil ||
				row.Changes.MeanPercent != nil || row.Changes.MaxPercent != nil {
				t.Fatalf("changes: %+v", row.Changes)
			}
		})
	}
}

func TestComparisonDoesNotMutateInputsAndOwnsOutputPointers(t *testing.T) {
	before := ComparisonInput{RPC: []span.Span{{Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 5}}}
	after := ComparisonInput{RPC: []span.Span{{Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 10}}}
	beforeSnapshot, afterSnapshot := slices.Clone(before.RPC), slices.Clone(after.RPC)
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.RPC, beforeSnapshot) || !reflect.DeepEqual(after.RPC, afterSnapshot) {
		t.Fatal("Compare mutated input observations")
	}
	providerBefore := got.Sections[0].Rows[0].Before
	methodBefore := got.Sections[2].Rows[0].Before
	*providerBefore.MeanMs = 99
	*providerBefore.MaxMs = 99
	if *methodBefore.MeanMs != 5 || *methodBefore.MaxMs != 5 {
		t.Fatalf("summaries alias pointers: provider=%+v method=%+v", providerBefore, methodBefore)
	}
}

func TestComparisonInputOrderDoesNotAffectOutput(t *testing.T) {
	input := ComparisonInput{RPC: []span.Span{
		{Provider: "p", ResourceType: "b", RPC: "Read", DurationMs: 2},
		{Provider: "p", ResourceType: "a", RPC: "Read", DurationMs: 1},
	}}
	reversed := ComparisonInput{RPC: []span.Span{input.RPC[1], input.RPC[0]}}
	first, err := Compare(input, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compare(reversed, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("permutation changed output:\n%+v\n%+v", first, second)
	}
}

func TestComparisonMeanAndPercentages(t *testing.T) {
	before := ComparisonInput{RPC: []span.Span{
		{Provider: "p", DurationMs: 1},
		{Provider: "p", DurationMs: 2},
	}}
	after := ComparisonInput{RPC: []span.Span{
		{Provider: "p", DurationMs: 3},
		{Provider: "p", DurationMs: 3},
	}}
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Sections[0].Rows[0]
	if row.Before.MeanMs == nil || *row.Before.MeanMs != 1.5 || row.Changes.MeanMs == nil ||
		*row.Changes.MeanMs != 1.5 || row.Changes.TotalPercent == nil || *row.Changes.TotalPercent != 100 ||
		row.Changes.MeanPercent == nil || *row.Changes.MeanPercent != 100 ||
		row.Changes.MaxPercent == nil || *row.Changes.MaxPercent != 50 {
		t.Fatalf("row: %+v", row)
	}
}

func TestComparisonIncludesUnpositionedDurations(t *testing.T) {
	input := ComparisonInput{RPC: []span.Span{{Provider: "p", DurationMs: math.MaxUint32}}}
	got, err := Compare(input, input)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Sections[0].Rows[0]
	if row.Before.TotalMs != math.MaxUint32 || row.Before.MaxMs == nil || *row.Before.MaxMs != math.MaxUint32 {
		t.Fatalf("unpositioned duration: %+v", row.Before)
	}
}
