package model

import (
	"errors"
	"math"
	"sort"
)

func signedChange(before, after uint64) SignedChange {
	if after < before {
		return SignedChange{Negative: true, Magnitude: before - after}
	}
	return SignedChange{Magnitude: after - before}
}
func addDuration(total uint64, duration uint32) (uint64, error) {
	if math.MaxUint64-total < uint64(duration) {
		return 0, errors.New("comparison duration total overflows uint64")
	}
	return total + uint64(duration), nil
}
func comparisonChanges(before, after ComparisonTotal) ComparisonChanges {
	count := signedChange(before.Count, after.Count)
	total := signedChange(before.TotalMs, after.TotalMs)
	changes := ComparisonChanges{
		Count: &count, TotalMs: &total,
		CountPercent: percentageChange(count, before.Count),
		TotalPercent: percentageChange(total, before.TotalMs),
	}
	if before.LowerBound || after.LowerBound {
		changes.TotalMs = nil
		changes.TotalPercent = nil
		return changes
	}
	if before.MeanMs != nil && after.MeanMs != nil {
		change := *after.MeanMs - *before.MeanMs
		changes.MeanMs = float64Pointer(change)
		if *before.MeanMs != 0 {
			changes.MeanPercent = float64Pointer(100 * change / *before.MeanMs)
		}
	}
	if before.MaxMs != nil && after.MaxMs != nil {
		change := signedChange(uint64(*before.MaxMs), uint64(*after.MaxMs))
		changes.MaxMs = &change
		changes.MaxPercent = percentageChange(change, uint64(*before.MaxMs))
	}
	return changes
}
func percentageChange(change SignedChange, baseline uint64) *float64 {
	if baseline == 0 {
		return nil
	}
	value := float64(change.Magnitude)
	if change.Negative {
		value = -value
	}
	value = 100 * value / float64(baseline)
	return &value
}
func sortComparisonRows(rows []ComparisonRow) {
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if left.Changes.TotalMs == nil || right.Changes.TotalMs == nil {
			if left.Changes.TotalMs != nil {
				return true
			}
			if right.Changes.TotalMs != nil {
				return false
			}
			return comparisonKeyLess(left.Key, right.Key)
		}
		if comparisonSignedEqual(*left.Changes.TotalMs, *right.Changes.TotalMs) {
			return comparisonKeyLess(left.Key, right.Key)
		}
		return signedChangeRanksBefore(*left.Changes.TotalMs, *right.Changes.TotalMs)
	})
}
func signedChangeRanksBefore(left, right SignedChange) bool {
	if left.Negative != right.Negative {
		return !left.Negative
	}
	if left.Negative {
		return left.Magnitude < right.Magnitude
	}
	return left.Magnitude > right.Magnitude
}
func comparisonSignedEqual(left, right SignedChange) bool {
	return left.Negative == right.Negative && left.Magnitude == right.Magnitude
}
func comparisonKeyLess(left, right ComparisonKey) bool {
	leftFields := [...]string{left.Provider, left.ResourceType, left.Method, left.Address, left.Action}
	rightFields := [...]string{right.Provider, right.ResourceType, right.Method, right.Address, right.Action}
	for index := range leftFields {
		if leftFields[index] != rightFields[index] {
			return leftFields[index] < rightFields[index]
		}
	}
	return left.DurationSource < right.DurationSource
}
