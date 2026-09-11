// Package profile renders a loaded model.Log as a plain-text performance
// report for the user who captured it. It prints real, unmasked resource
// addresses and requires review before sharing.
package profile

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/qualitytext"
)

// DefaultLimit is the default maximum number of rows in each ranked list.
const DefaultLimit = 20

// TextOptions controls plain-text report presentation. A zero Limit prints
// every row.
type TextOptions struct{ Limit int }

const (
	typeColWidth            = 24
	maxResourceTypeColWidth = 49
)

// Render builds and writes a profile report for l.
func Render(w io.Writer, l *model.Log, options TextOptions) error {
	if options.Limit < 0 {
		return errors.New("profile limit must be non-negative")
	}
	report, err := Build(l)
	if err != nil {
		return err
	}
	return renderReport(w, report, options)
}

func renderReport(w io.Writer, report Report, options TextOptions) error {
	if options.Limit < 0 {
		return errors.New("profile limit must be non-negative")
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "tfli profile report\n====================\n\n")
	fmt.Fprintf(b, "SIZE\n  bytes                %d\n  RPC spans            %d\n  UI-hook spans        %d\n\n", report.Bytes, len(report.RPC), len(report.UI))
	fmt.Fprintf(b, "Resource addresses in this report are not masked. Review the report before sharing it.\n\n")
	writeLoggingCaveat(b)
	qualitytext.WriteCaptureQuality(b, report.Quality)
	writeSaturationWarning(b, report.Quality)
	if len(report.RPC) == 0 && len(report.UI) == 0 {
		fmt.Fprintf(b, "NO SPANS\n  This log has no spans to profile: no admitted timing observations.\n")
		if report.Quality.ProviderEntries > 0 || report.Quality.RPC.Records > 0 || report.Quality.StructuredLines > 0 {
			fmt.Fprintf(b, "  Provider or structured-output evidence was observed, but\n  it did not yield an admitted duration.\n")
		} else {
			fmt.Fprintf(b, "  Capture provider TRACE logs or terraform.ui completion hooks.\n")
		}
		return writeText(w, b.String())
	}
	writeResourceTypeJoin(b, report.Types, options.Limit)
	writeProviderRollup(b, report.Providers, options.Limit)
	if err := writeObservations(b, "SLOWEST CALLS", report.RPC, report.RPCRanking, options.Limit, report.HasContext, false); err != nil {
		return err
	}
	if err := writeObservations(b, "SLOWEST RESOURCES", report.UI, report.UIRanking, options.Limit, report.HasContext, true); err != nil {
		return err
	}
	if err := writeTimeline(b, report, options.Limit); err != nil {
		return err
	}
	return writeText(w, b.String())
}

func writeText(w io.Writer, value string) error {
	n, err := io.WriteString(w, value)
	if err != nil {
		return err
	}
	if n != len(value) {
		return io.ErrShortWrite
	}
	return nil
}

func writeSaturationWarning(b *strings.Builder, quality model.CaptureQuality) {
	if !quality.UI.DurationLowerBound {
		return
	}
	var saturated uint64
	for _, issue := range quality.Issues {
		if issue.Stage == "ui_duration" && issue.Code == "duration_saturated" {
			saturated += issue.Count
		}
	}
	fmt.Fprintf(b, "WARNING: %d UI-hook duration(s) exceeded the storage limit.\n", saturated)
	fmt.Fprintf(b, "Affected timings and totals are lower bounds; their rankings\nand timeline positions may be inaccurate.\n\n")
}

func limitedLength(length, limit int) int {
	if limit > 0 && limit < length {
		return limit
	}
	return length
}

func writeHeading(b *strings.Builder, name string, shown, total int) {
	if shown < total {
		fmt.Fprintf(b, "%s (top %d of %d)\n", name, shown, total)
	} else {
		fmt.Fprintf(b, "%s\n", name)
	}
}

func resourceTypeColWidth(types []string) int {
	width := typeColWidth
	for _, value := range types {
		width = max(width, len(logfmt.DisplayText(value)))
	}
	return min(width, maxResourceTypeColWidth)
}

func truncate(value string, width int) string {
	value = logfmt.DisplayText(value)
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}

func writeResourceTypeJoin(b *strings.Builder, rows []TypeSummary, limit int) {
	if len(rows) == 0 {
		return
	}
	shown := limitedLength(len(rows), limit)
	writeHeading(b, "BY RESOURCE TYPE", shown, len(rows))
	types := make([]string, shown)
	uiPresent := false
	for i, row := range rows[:shown] {
		types[i] = row.ResourceType
		uiPresent = uiPresent || row.UIResources > 0
	}
	if uiPresent {
		fmt.Fprintf(b, "  UI-hook figures are sums of measurements rounded to\n  whole seconds, +/- 1s each.\n")
	}
	width := resourceTypeColWidth(types)
	fmt.Fprintf(b, "  %-*s %9s %9s %9s %9s %9s\n", width, "resource type", "UI res.", "UI total", "RPC calls", "RPC total", "RPC max")
	for _, row := range rows[:shown] {
		name := truncate(row.ResourceType, width)
		fmt.Fprintf(b, "  %-*s %9d %9s %9d %9s %9s\n", width, name, row.UIResources, formatLowerBoundMs(row.UITotalMs, row.UILowerBound), row.RPCCalls, formatMs(row.RPCTotalMs), formatMs(uint64(row.RPCMaxMs)))
		if name != logfmt.DisplayText(row.ResourceType) {
			fmt.Fprintf(b, "    resource type: %s\n", logfmt.DisplayText(row.ResourceType))
		}
	}
	fmt.Fprintf(b, "\n")
}

func writeProviderRollup(b *strings.Builder, buckets []model.Bucket, limit int) {
	if len(buckets) == 0 {
		return
	}
	shown := limitedLength(len(buckets), limit)
	writeHeading(b, "BY PROVIDER", shown, len(buckets))
	fmt.Fprintf(b, "  %8s %8s %8s  %s\n", "total", "calls", "max", "provider")
	for _, bucket := range buckets[:shown] {
		fmt.Fprintf(b, "  %8s %8d %8s  %s\n", formatMs(bucket.TotalMs), bucket.Count, formatMs(uint64(bucket.MaxMs)), logfmt.DisplayText(bucket.Key))
	}
	fmt.Fprintf(b, "\n")
}

func writeObservations(b *strings.Builder, heading string, observations []Observation, ranking []int, limit int, hasContext, ui bool) error {
	if len(observations) == 0 {
		return nil
	}
	shown := limitedLength(len(ranking), limit)
	writeHeading(b, heading, shown, len(ranking))
	if ui {
		fmt.Fprintf(b, "  Terraform reports these in whole seconds, +/- 1s each, so\n  neighbouring rows are not reliably ordered.\n")
	}
	for _, index := range ranking[:shown] {
		if index < 0 || index >= len(observations) {
			return errors.New("profile observation index out of range")
		}
		observation := observations[index]
		s := observation.Span
		duration := formatLowerBoundMs(uint64(s.DurationMs), ui && s.DurationSaturated)
		if ui {
			fmt.Fprintf(b, "  %8s  %s  %s\n    resource: %s (observed UI)\n", duration, logfmt.DisplayText(s.RPC), logfmt.DisplayText(s.ResourceType), logfmt.DisplayText(s.Address))
		} else {
			fmt.Fprintf(b, "  %8s  %s  %s  %s\n", duration, logfmt.DisplayText(s.RPC), logfmt.DisplayText(s.ResourceType), logfmt.DisplayText(s.Provider))
			writeAttribution(b, observation.Attribution, hasContext)
		}
		writeSource(b, observation.Source)
	}
	fmt.Fprintf(b, "\n")
	return nil
}

func writeAttribution(b *strings.Builder, attribution attrib.Attribution, hasContext bool) {
	if !hasContext {
		fmt.Fprintf(b, "    resource: unavailable (no address context)\n")
		return
	}
	switch attribution.Confidence {
	case attrib.Ambiguous:
		fmt.Fprintf(b, "    resource: ambiguous (%d candidates)\n", attribution.Candidates)
	case attrib.Unattributed:
		fmt.Fprintf(b, "    resource: unattributed\n")
	default:
		fmt.Fprintf(b, "    resource: %s (%s)\n", logfmt.DisplayText(attribution.Address), attribution.Confidence)
	}
}

func writeSource(b *strings.Builder, source *model.SourceLocation) {
	if source == nil || source.StartLine == 0 {
		fmt.Fprintf(b, "    source: unavailable\n")
	} else if source.StartLine == source.EndLine {
		fmt.Fprintf(b, "    source: line %d\n", source.StartLine)
	} else {
		fmt.Fprintf(b, "    source: lines %d-%d\n", source.StartLine, source.EndLine)
	}
}

func formatLowerBoundMs(ms uint64, lowerBound bool) string {
	if lowerBound {
		return "≥" + formatMs(ms)
	}
	return formatMs(ms)
}

func formatMs(ms uint64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func writeLoggingCaveat(b *strings.Builder) {
	fmt.Fprintf(b, "Durations here are measured under logging, which is not\nfree: one workspace planned in 24.1s unlogged and 522.2s\nwith debug plus provider TRACE. A call that logs heavily\nis inflated more than one that waits, so rankings are\napproximate and absolute times do not transfer.\n\n")
}
