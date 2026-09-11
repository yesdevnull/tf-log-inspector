package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// DurationSourceSummary counts admitted resource operations by provenance.
type DurationSourceSummary struct {
	Source             span.DurationSource
	Count              uint64
	DurationMs         uint64
	MaxMs              uint32
	DurationLowerBound bool
}

// SummariseDurationSources returns only observed sources, in source enum order.
func SummariseDurationSources(spans []span.Span) []DurationSourceSummary {
	bySource := make(map[span.DurationSource]DurationSourceSummary)
	for _, s := range spans {
		summary := bySource[s.DurationSource]
		summary.Source = s.DurationSource
		summary.Count++
		summary.DurationMs += uint64(s.DurationMs)
		summary.MaxMs = max(summary.MaxMs, s.DurationMs)
		summary.DurationLowerBound = summary.DurationLowerBound || s.DurationSaturated
		bySource[s.DurationSource] = summary
	}
	var summaries []DurationSourceSummary
	for _, summary := range bySource {
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Source < summaries[j].Source })
	return summaries
}
