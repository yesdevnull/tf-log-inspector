package qualitytext

import (
	"fmt"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// DurationSourceQualification describes the resolution and meaning of a resource duration.
func DurationSourceQualification(source span.DurationSource) string {
	switch source {
	case span.SourceUIElapsed:
		return "ui_elapsed: rounded to whole seconds, +/- 1s each."
	case span.SourceRefreshWindow:
		return "refresh_window: timestamp-derived hook window, not RPC."
	case span.SourceCLIElapsed:
		return "cli_elapsed: displayed duration; no timeline position."
	default:
		return "Duration source unavailable."
	}
}

// DurationSourceLines renders admitted counts and durations without resource identities.
func DurationSourceLines(summaries []model.DurationSourceSummary) []string {
	var lines []string
	for _, summary := range summaries {
		total := formatMs(summary.DurationMs)
		if summary.Source != span.SourceUIElapsed {
			total = ExactDuration(summary.DurationMs)
		}
		if summary.DurationLowerBound {
			total = "≥" + total
		}
		noun := "operations"
		if summary.Count == 1 {
			noun = "operation"
		}
		lines = append(lines, fmt.Sprintf("%s: %d %s, %s", summary.Source, summary.Count, noun, total))
	}
	return lines
}

// ExactDuration preserves the millisecond precision of timestamp and CLI durations.
func ExactDuration(ms uint64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	fraction := strings.TrimRight(fmt.Sprintf("%03d", ms%1000), "0")
	if fraction == "" {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%d.%ss", ms/1000, fraction)
}
