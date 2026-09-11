package profile

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func writeTimeline(b *strings.Builder, report Report, limit int) error {
	if report.Timeline.Tier == nil {
		return nil
	}
	tier := *report.Timeline.Tier
	analysis := report.Timeline.Analysis
	tierName, origin, observations := "RPC", report.Quality.RPC.Origin, report.RPC
	if tier == span.FidelityUIReported {
		tierName, origin, observations = "resource", report.Quality.UI.Origin, report.UI
	}
	fmt.Fprintf(b, "CONCURRENCY (%s tier)\n", tierName)
	if origin == nil {
		fmt.Fprintf(b, "  clock origin: unavailable\n")
	} else {
		fmt.Fprintf(b, "  clock origin: %s\n", origin.UTC().Format(time.RFC3339Nano))
	}
	fmt.Fprintf(b, "  interval offsets are milliseconds from this tier origin\n")
	timing := analysis.Timing
	positionedCount := timing.AdmittedCount - timing.ExcludedCount
	fmt.Fprintf(b, "  positioned observations %d of %d admitted\n", positionedCount, timing.AdmittedCount)
	fmt.Fprintf(b, "  positioned duration   %s (%d excluded, %s)\n", formatMs(timing.PositionedMs), timing.ExcludedCount, formatLowerBoundMs(timing.ExcludedMs, timing.ExcludedLowerBound))
	writeExclusions(b, timing)
	if analysis.Metrics == nil {
		fmt.Fprintf(b, "  analysis: unavailable\n  temporal metrics: unavailable\n")
		writeTimelineQualification(b)
		return nil
	}
	if timing.ExcludedCount > 0 {
		fmt.Fprintf(b, "  analysis: partial; excluded observations are omitted from temporal metrics\n")
	} else {
		fmt.Fprintf(b, "  analysis: complete\n")
	}
	metrics := analysis.Metrics
	fmt.Fprintf(b, "  window (zero to latest positioned end) %s\n", formatMs(uint64(metrics.WindowMs)))
	fmt.Fprintf(b, "  peak concurrency %d\n  busy union %s\n", metrics.Peak, formatMs(uint64(metrics.BusyMs)))
	if metrics.BusyFraction == nil {
		fmt.Fprintf(b, "  busy / window unavailable\n")
	} else {
		fmt.Fprintf(b, "  busy / window %.1f%%\n", *metrics.BusyFraction*100)
	}
	fmt.Fprintf(b, "  positioned reported-duration sum %s\n", formatMs(timing.PositionedMs))
	if metrics.WindowMs == 0 {
		fmt.Fprintf(b, "  summed / window unavailable\n")
	} else {
		fmt.Fprintf(b, "  summed / window %.1fx\n", float64(timing.PositionedMs)/float64(metrics.WindowMs))
	}
	writeClampingNote(b, observations)
	if err := writeIntervals(b, report, tierName, observations, analysis, limit); err != nil {
		return err
	}
	writeTimelineQualification(b)
	return nil
}

func writeExclusions(b *strings.Builder, timing model.TimingSelection) {
	if timing.ExcludedCount == 0 {
		return
	}
	keys := make([]string, 0, len(timing.Exclusions))
	for reason := range timing.Exclusions {
		keys = append(keys, reason)
	}
	sort.Strings(keys)
	for _, reason := range keys {
		fmt.Fprintf(b, "    excluded: %s %d\n", logfmt.DisplayText(reason), timing.Exclusions[reason])
	}
}

func writeClampingNote(b *strings.Builder, observations []Observation) {
	for _, observation := range observations {
		if observation.Span.HasPosition() && observation.Span.StartClamped {
			fmt.Fprintf(b, "  Note: one or more spans have a clamped start, so timeline extents may be shorter than reported durations.\n")
			return
		}
	}
}

func writeIntervals(b *strings.Builder, report Report, tierName string, observations []Observation, analysis model.TimingAnalysis, limit int) error {
	intervals := append([]model.Stall(nil), analysis.Intervals...)
	sort.Slice(intervals, func(i, j int) bool {
		di, dj := intervals[i].EndMs-intervals[i].StartMs, intervals[j].EndMs-intervals[j].StartMs
		if di != dj {
			return di > dj
		}
		if intervals[i].StartMs != intervals[j].StartMs {
			return intervals[i].StartMs < intervals[j].StartMs
		}
		return intervals[i].EndMs < intervals[j].EndMs
	})
	shown := limitedLength(len(intervals), limit)
	if len(intervals) == 0 {
		fmt.Fprintf(b, "  no qualifying intervals at or above %s\n", formatMs(uint64(analysis.ThresholdMs)))
		return nil
	}
	writeHeading(b, "QUALIFYING INTERVALS", shown, len(intervals))
	for _, interval := range intervals[:shown] {
		fmt.Fprintf(b, "  %dms-%dms  ", interval.StartMs, interval.EndMs)
		if interval.MinRunning == interval.MaxRunning {
			fmt.Fprintf(b, "%d of %d running\n", interval.MinRunning, interval.Capacity)
		} else {
			fmt.Fprintf(b, "%d-%d of %d running\n", interval.MinRunning, interval.MaxRunning, interval.Capacity)
		}
		if interval.Blocking < 0 {
			fmt.Fprintf(b, "    no observed %s work\n", tierName)
			continue
		}
		observation, err := intervalObservation(report, observations, interval.Blocking)
		if err != nil {
			return err
		}
		writeActiveObservation(b, observation, report.HasContext, tierName == "resource")
	}
	return nil
}

func intervalObservation(report Report, observations []Observation, positionedIndex int) (Observation, error) {
	if positionedIndex >= len(report.Timeline.PositionedIndices) {
		return Observation{}, errors.New("profile interval observation index out of range")
	}
	original := report.Timeline.PositionedIndices[positionedIndex]
	if original < 0 || original >= len(observations) {
		return Observation{}, errors.New("profile interval observation index out of range")
	}
	return observations[original], nil
}

func writeActiveObservation(b *strings.Builder, observation Observation, hasContext, ui bool) {
	s := observation.Span
	if ui {
		fmt.Fprintf(b, "    longest observed active observation: %s %s\n", logfmt.DisplayText(s.RPC), logfmt.DisplayText(s.ResourceType))
		fmt.Fprintf(b, "    resource: %s (observed resource)\n", logfmt.DisplayText(s.Address))
		fmt.Fprintf(b, "    duration source: %s\n", s.DurationSource)
	} else {
		fmt.Fprintf(b, "    longest observed active observation: %s %s %s\n", logfmt.DisplayText(s.RPC), logfmt.DisplayText(s.ResourceType), logfmt.DisplayText(s.Provider))
		writeAttribution(b, observation.Attribution, hasContext)
	}
	writeSource(b, observation.Source)
}

func writeTimelineQualification(b *strings.Builder) {
	fmt.Fprintf(b, "  Observed gaps do not establish Terraform idleness, and a long active\n  observation does not prove it caused other work to wait.\n\n")
}
