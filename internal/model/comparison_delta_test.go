package model

import (
	"math"
	"testing"
)

func TestComparisonSignedArithmeticBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		before, after uint64
		want          SignedChange
	}{
		{"zero normalised", 7, 7, SignedChange{}},
		{"above float precision", 1 << 53, (1 << 53) + 1, SignedChange{Magnitude: 1}},
		{"above max int64", math.MaxInt64 + 1, math.MaxInt64, SignedChange{Negative: true, Magnitude: 1}},
		{"full unsigned range", math.MaxUint64, 0, SignedChange{Negative: true, Magnitude: math.MaxUint64}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := signedChange(test.before, test.after); got != test.want {
				t.Fatalf("signedChange(%d, %d) = %+v, want %+v", test.before, test.after, got, test.want)
			}
		})
	}
}

func TestComparisonAddDurationChecksOverflow(t *testing.T) {
	got, err := addDuration(math.MaxUint64-3, 3)
	if err != nil || got != math.MaxUint64 {
		t.Fatalf("boundary sum = %d, %v", got, err)
	}
	got, err = addDuration(math.MaxUint64-3, 4)
	if got != 0 || err == nil || err.Error() != "comparison duration total overflows uint64" {
		t.Fatalf("overflow = %d, %v", got, err)
	}
}

func TestComparisonSignedRanking(t *testing.T) {
	rows := []ComparisonRow{
		comparisonRankedRow("minus-far", SignedChange{Negative: true, Magnitude: math.MaxUint64}),
		comparisonRankedRow("zero", SignedChange{}),
		{Key: ComparisonKey{Provider: "unranked-b"}},
		comparisonRankedRow("plus-small", SignedChange{Magnitude: 1}),
		comparisonRankedRow("minus-near", SignedChange{Negative: true, Magnitude: 1}),
		{Key: ComparisonKey{Provider: "unranked-a"}},
		comparisonRankedRow("plus-large", SignedChange{Magnitude: math.MaxUint64}),
	}
	sortComparisonRows(rows)
	want := []string{"plus-large", "plus-small", "zero", "minus-near", "minus-far", "unranked-a", "unranked-b"}
	for index, name := range want {
		if rows[index].Key.Provider != name {
			t.Fatalf("row %d = %q, want %q", index, rows[index].Key.Provider, name)
		}
	}
}

func comparisonRankedRow(name string, change SignedChange) ComparisonRow {
	return ComparisonRow{Key: ComparisonKey{Provider: name}, Changes: ComparisonChanges{TotalMs: &change}}
}
