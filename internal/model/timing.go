package model

import "github.com/yesdevnull/tf-log-inspector/internal/span"

// TimingSelection keeps the duration evidence a report may total while
// separating spans whose positions are safe for temporal rendering.
type TimingSelection struct {
	Positioned []span.Span

	AdmittedCount, ExcludedCount           int
	AdmittedMs, PositionedMs, ExcludedMs   uint64
	AdmittedLowerBound, ExcludedLowerBound bool
	Exclusions                             map[string]uint64
}

// SelectTiming projects admitted spans for timeline consumers without
// discarding their durations from report totals.
func SelectTiming(spans []span.Span) TimingSelection {
	out := TimingSelection{Exclusions: make(map[string]uint64)}
	for _, s := range spans {
		out.AdmittedCount++
		out.AdmittedMs += uint64(s.DurationMs)
		out.AdmittedLowerBound = out.AdmittedLowerBound || s.DurationSaturated
		if s.HasPosition() {
			out.Positioned = append(out.Positioned, s)
			out.PositionedMs += uint64(s.DurationMs)
			continue
		}

		out.ExcludedCount++
		out.ExcludedMs += uint64(s.DurationMs)
		out.ExcludedLowerBound = out.ExcludedLowerBound || s.DurationSaturated
		for _, code := range s.PositionReasons() {
			out.Exclusions[code]++
		}
	}
	return out
}

// PreferredTiming identifies the highest-fidelity admitted timing tier on a
// log. The tiers stay separate because their clocks have different origins.
func PreferredTiming(l *Log) (span.Fidelity, bool) {
	if len(l.RPCSpans) > 0 {
		return span.FidelityReported, true
	}
	if len(l.UISpans) > 0 {
		return span.FidelityUIReported, true
	}
	return 0, false
}
