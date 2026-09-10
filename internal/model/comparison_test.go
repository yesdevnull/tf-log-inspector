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
	assertComparisonChanges(t, row.Changes, ComparisonChanges{
		Count:        changePointer(SignedChange{Negative: true, Magnitude: 1}),
		TotalMs:      changePointer(SignedChange{}),
		MaxMs:        changePointer(SignedChange{Magnitude: 10}),
		MeanMs:       float64Pointer(10),
		CountPercent: float64Pointer(-50),
		TotalPercent: float64Pointer(0),
		MeanPercent:  float64Pointer(100),
		MaxPercent:   float64Pointer(100),
	})
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
	if same.State != "matched" {
		t.Fatalf("zero baseline row: %+v", same)
	}
	assertComparisonChanges(t, same.Changes, ComparisonChanges{
		Count: changePointer(SignedChange{}), TotalMs: changePointer(SignedChange{Magnitude: 10}),
		MaxMs: changePointer(SignedChange{Magnitude: 10}), MeanMs: float64Pointer(10),
		CountPercent: float64Pointer(0),
	})
	added := byKey["added"]
	if added.State != "added" || added.Before.Count != 0 || added.Before.TotalMs != 0 ||
		added.Before.MeanMs != nil || added.Before.MaxMs != nil {
		t.Fatalf("added row: %+v", added)
	}
	assertComparisonChanges(t, added.Changes, ComparisonChanges{
		Count: changePointer(SignedChange{Magnitude: 1}), TotalMs: changePointer(SignedChange{Magnitude: 7}),
	})
	removed := byKey["removed"]
	if removed.State != "removed" || removed.After.Count != 0 || removed.After.TotalMs != 0 ||
		removed.After.MeanMs != nil || removed.After.MaxMs != nil {
		t.Fatalf("removed row: %+v", removed)
	}
	assertComparisonChanges(t, removed.Changes, ComparisonChanges{
		Count:        changePointer(SignedChange{Negative: true, Magnitude: 1}),
		TotalMs:      changePointer(SignedChange{Negative: true, Magnitude: 5}),
		CountPercent: float64Pointer(-100), TotalPercent: float64Pointer(-100),
	})
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
		row.After.Count != 1 || row.After.TotalMs != 0 || row.After.MaxMs == nil || *row.After.MaxMs != 0 {
		t.Fatalf("unavailable row: %+v", row)
	}
	assertComparisonChanges(t, row.Changes, ComparisonChanges{})
	if got.Sections[3].BeforeAvailable || got.Sections[3].AfterAvailable || len(got.Sections[3].Rows) != 0 {
		t.Fatalf("disjoint UI tier: %+v", got.Sections[3])
	}
}

func TestComparisonChangeFieldsByRowState(t *testing.T) {
	tests := []struct {
		name   string
		before ComparisonInput
		after  ComparisonInput
		key    string
		state  string
		want   ComparisonChanges
	}{
		{
			name:   "identical",
			before: ComparisonInput{RPC: []span.Span{{ResourceType: "target", DurationMs: 10}}},
			after:  ComparisonInput{RPC: []span.Span{{ResourceType: "target", DurationMs: 10}}},
			key:    "target", state: "matched",
			want: ComparisonChanges{
				Count: changePointer(SignedChange{}), TotalMs: changePointer(SignedChange{}),
				MaxMs: changePointer(SignedChange{}), MeanMs: float64Pointer(0),
				CountPercent: float64Pointer(0), TotalPercent: float64Pointer(0),
				MeanPercent: float64Pointer(0), MaxPercent: float64Pointer(0),
			},
		},
		{
			name: "matched",
			before: ComparisonInput{RPC: []span.Span{
				{ResourceType: "target", DurationMs: 10}, {ResourceType: "target", DurationMs: 10},
			}},
			after: ComparisonInput{RPC: []span.Span{{ResourceType: "target", DurationMs: 20}}},
			key:   "target", state: "matched",
			want: ComparisonChanges{
				Count:   changePointer(SignedChange{Negative: true, Magnitude: 1}),
				TotalMs: changePointer(SignedChange{}), MaxMs: changePointer(SignedChange{Magnitude: 10}),
				MeanMs: float64Pointer(10), CountPercent: float64Pointer(-50),
				TotalPercent: float64Pointer(0), MeanPercent: float64Pointer(100),
				MaxPercent: float64Pointer(100),
			},
		},
		{
			name:   "added",
			before: ComparisonInput{RPC: []span.Span{{ResourceType: "sentinel", DurationMs: 1}}},
			after: ComparisonInput{RPC: []span.Span{
				{ResourceType: "sentinel", DurationMs: 1}, {ResourceType: "target", DurationMs: 7},
			}},
			key: "target", state: "added",
			want: ComparisonChanges{
				Count:   changePointer(SignedChange{Magnitude: 1}),
				TotalMs: changePointer(SignedChange{Magnitude: 7}),
			},
		},
		{
			name: "removed",
			before: ComparisonInput{RPC: []span.Span{
				{ResourceType: "sentinel", DurationMs: 1}, {ResourceType: "target", DurationMs: 5},
			}},
			after: ComparisonInput{RPC: []span.Span{{ResourceType: "sentinel", DurationMs: 1}}},
			key:   "target", state: "removed",
			want: ComparisonChanges{
				Count:        changePointer(SignedChange{Negative: true, Magnitude: 1}),
				TotalMs:      changePointer(SignedChange{Negative: true, Magnitude: 5}),
				CountPercent: float64Pointer(-100), TotalPercent: float64Pointer(-100),
			},
		},
		{
			name:   "unavailable",
			before: ComparisonInput{},
			after:  ComparisonInput{RPC: []span.Span{{ResourceType: "target", DurationMs: 5}}},
			key:    "target", state: "unavailable",
			want: ComparisonChanges{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Compare(test.before, test.after)
			if err != nil {
				t.Fatal(err)
			}
			var row *ComparisonRow
			for index := range got.Sections[1].Rows {
				if got.Sections[1].Rows[index].Key.ResourceType == test.key {
					row = &got.Sections[1].Rows[index]
					break
				}
			}
			if row == nil || row.State != test.state {
				t.Fatalf("row for %q: %+v", test.key, row)
			}
			assertComparisonChanges(t, row.Changes, test.want)
		})
	}
}

func TestComparisonEmptyInputs(t *testing.T) {
	got, err := Compare(ComparisonInput{}, ComparisonInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sections) != 5 || got.BeforeUnnamedUI != nil || got.AfterUnnamedUI != nil ||
		got.ProviderIdentityStatus != "unknown" || len(got.BeforeProviders) != 0 || len(got.AfterProviders) != 0 {
		t.Fatalf("comparison: %+v", got)
	}
	for _, section := range got.Sections {
		if section.BeforeAvailable || section.AfterAvailable || len(section.Rows) != 0 {
			t.Fatalf("section: %+v", section)
		}
	}
}

func TestComparisonCrossedTierAvailability(t *testing.T) {
	got, err := Compare(
		ComparisonInput{RPC: []span.Span{{Provider: "p", DurationMs: 2}}},
		ComparisonInput{UI: []span.Span{{Address: "a", ResourceType: "r", RPC: "read", DurationMs: 3}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	rpc := got.Sections[0]
	if !rpc.BeforeAvailable || rpc.AfterAvailable || len(rpc.Rows) != 1 ||
		rpc.Rows[0].Before == nil || rpc.Rows[0].After != nil || rpc.Rows[0].State != "unavailable" {
		t.Fatalf("RPC section: %+v", rpc)
	}
	assertComparisonChanges(t, rpc.Rows[0].Changes, ComparisonChanges{})
	ui := got.Sections[4]
	if ui.BeforeAvailable || !ui.AfterAvailable || len(ui.Rows) != 1 ||
		ui.Rows[0].Before != nil || ui.Rows[0].After == nil || ui.Rows[0].State != "unavailable" {
		t.Fatalf("UI section: %+v", ui)
	}
	assertComparisonChanges(t, ui.Rows[0].Changes, ComparisonChanges{})
}

func TestComparisonUIOnlyKnownAddressWithEmptyAction(t *testing.T) {
	input := ComparisonInput{UI: []span.Span{{Address: "resource.a", ResourceType: "resource", RPC: "", DurationMs: 4}}}
	got, err := Compare(input, input)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if got.Sections[index].BeforeAvailable || got.Sections[index].AfterAvailable || len(got.Sections[index].Rows) != 0 {
			t.Fatalf("RPC section %d: %+v", index, got.Sections[index])
		}
	}
	operations := got.Sections[4]
	if !operations.BeforeAvailable || !operations.AfterAvailable || len(operations.Rows) != 1 ||
		operations.Rows[0].Key.Address != "resource.a" || operations.Rows[0].Key.Action != "" ||
		operations.Rows[0].State != "matched" {
		t.Fatalf("operations: %+v", operations)
	}
	assertComparisonChanges(t, operations.Rows[0].Changes, ComparisonChanges{
		Count: changePointer(SignedChange{}), TotalMs: changePointer(SignedChange{}),
		MaxMs: changePointer(SignedChange{}), MeanMs: float64Pointer(0),
		CountPercent: float64Pointer(0), TotalPercent: float64Pointer(0),
		MeanPercent: float64Pointer(0), MaxPercent: float64Pointer(0),
	})
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

func assertComparisonChanges(t *testing.T, got, want ComparisonChanges) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
}

func changePointer(value SignedChange) *SignedChange {
	return &value
}
