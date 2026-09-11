// Package qualitytext renders the shared value-free capture quality summary.
package qualitytext

import (
	"fmt"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// WriteCaptureQuality appends a whole-capture quality summary to b.
func WriteCaptureQuality(b *strings.Builder, q model.CaptureQuality) {
	fmt.Fprintln(b, "CAPTURE QUALITY (whole log)")
	writeTierQuality(b, "RPC", q.RPC)
	writeTierQuality(b, "resource", q.UI)
	for _, line := range DurationSourceLines(q.DurationSources) {
		fmt.Fprintf(b, "    %s\n", line)
	}
	switch {
	case !q.HasContext:
		fmt.Fprintf(b, "  %-22s unavailable / %s (no address context)\n", "nameable duration", formatMs(q.RPCDurationMs))
	case q.NameableShare == nil:
		fmt.Fprintf(b, "  %-22s unavailable (total RPC duration is 0ms)\n", "nameable duration")
	default:
		fmt.Fprintf(b, "  %-22s %s / %s (%.1f%%)\n", "nameable duration", formatMs(q.NameableMs), formatMs(q.RPCDurationMs), *q.NameableShare*100)
	}
	if hasIssue(q.Issues, "context", "context_incomplete") {
		fmt.Fprintln(b, "  Context evidence is incomplete; this limits attribution and does not establish that an operation failed.")
	}
	stage := ""
	for _, issue := range q.Issues {
		if issue.Count == 0 {
			continue
		}
		if issue.Stage != stage {
			stage = issue.Stage
			fmt.Fprintf(b, "  %s issues\n", stage)
		}
		fmt.Fprintf(b, "    %-24s %d", issue.Code, issue.Count)
		if issue.FirstEntry != nil {
			fmt.Fprintf(b, " (first entry %d)", *issue.FirstEntry)
		}
		fmt.Fprintln(b)
	}
	fmt.Fprintln(b)
}

func writeTierQuality(b *strings.Builder, name string, q model.TierQuality) {
	total := formatMs(q.DurationMs)
	if q.DurationLowerBound {
		total += " (lower bound)"
	}
	fmt.Fprintf(b, "  %s timing records     %d: admitted %d, rejected %d; duration %s\n",
		name, q.Records, q.Admitted, q.Rejected, total)
	excluded := formatMs(q.ExcludedMs)
	if q.DurationLowerBound && q.ExcludedMs > 0 {
		excluded += " (lower bound)"
	}
	fmt.Fprintf(b, "  %s positioning        %d observations, %s; excluded %d observations, %s\n",
		name, q.Positioned, formatMs(q.PositionedMs), q.Admitted-q.Positioned, excluded)
}

func hasIssue(issues []model.QualityIssue, stage, code string) bool {
	for _, issue := range issues {
		if issue.Stage == stage && issue.Code == code && issue.Count > 0 {
			return true
		}
	}
	return false
}

func formatMs(ms uint64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
