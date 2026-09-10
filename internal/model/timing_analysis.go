package model

import "github.com/yesdevnull/tf-log-inspector/internal/span"

// TimingMetrics describes positioned work on one timeline clock.
type TimingMetrics struct {
	WindowMs     uint32
	Peak         int
	BusyMs       uint32
	BusyFraction *float64
}

// TimingAnalysis keeps admitted duration evidence alongside temporal metrics
// derived only from spans safe to position on one clock.
type TimingAnalysis struct {
	Timing      TimingSelection
	Metrics     *TimingMetrics
	ThresholdMs uint32
	Intervals   []Stall
}

// TimingWindowMs returns the latest positioned end offset from the log origin.
func TimingWindowMs(positioned []span.Span) uint32 {
	var end uint32
	for _, s := range positioned {
		end = max(end, s.EndMs)
	}
	return end
}

// IntervalThresholdMs returns the minimum interval worth reporting.
func IntervalThresholdMs(window uint32) uint32 {
	return max(window/20, 1000)
}

// AnalyseTiming derives temporal metrics and every qualifying interval while
// retaining the supplied selection's admitted and excluded evidence.
func AnalyseTiming(timing TimingSelection) (TimingAnalysis, error) {
	out := TimingAnalysis{Timing: timing}
	if len(timing.Positioned) == 0 {
		return out, nil
	}

	peak, err := PeakConcurrency(timing.Positioned)
	if err != nil {
		return TimingAnalysis{}, err
	}
	busy, err := BusyMs(timing.Positioned)
	if err != nil {
		return TimingAnalysis{}, err
	}
	window := TimingWindowMs(timing.Positioned)
	out.ThresholdMs = IntervalThresholdMs(window)
	out.Intervals, err = Stalls(timing.Positioned, out.ThresholdMs)
	if err != nil {
		return TimingAnalysis{}, err
	}
	out.Metrics = &TimingMetrics{WindowMs: window, Peak: peak, BusyMs: busy}
	if window > 0 {
		fraction := float64(busy) / float64(window)
		out.Metrics.BusyFraction = &fraction
	}
	return out, nil
}
