package profile

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/qualitytext"
)

// RenderComparisonText writes a human-readable comparison report.
func RenderComparisonText(w io.Writer, report ComparisonReport, metadata ComparisonMetadata, options TextOptions) error {
	if options.Limit < 0 {
		return errors.New("comparison limit must be non-negative")
	}
	b := &strings.Builder{}
	fmt.Fprintln(b, "tfli comparison report\n======================")
	fmt.Fprintf(b, "\nBEFORE: %s\n  bytes: %d\nBEFORE CAPTURE QUALITY\n", logfmt.DisplayText(metadata.BeforeBasename), report.Before.Bytes)
	qualitytext.WriteCaptureQuality(b, report.Before.Quality)
	fmt.Fprintf(b, "AFTER: %s\n  bytes: %d\nAFTER CAPTURE QUALITY\n", logfmt.DisplayText(metadata.AfterBasename), report.After.Bytes)
	qualitytext.WriteCaptureQuality(b, report.After.Quality)
	writeComparisonQualifications(b, report.Data)
	writeUnnamedUI(b, report.Data.BeforeUnnamedUI, report.Data.AfterUnnamedUI)
	for _, section := range report.Data.Sections {
		writeComparisonSection(b, section, options.Limit)
	}
	return writeText(w, b.String())
}

func writeComparisonQualifications(b *strings.Builder, comparison model.Comparison) {
	fmt.Fprintln(b, "COMPARABILITY")
	fmt.Fprintln(b, "  identifiers are unmasked; this report is not safe to share.")
	fmt.Fprintln(b, "  logging changes observed timing; logging configuration equivalence is unknown.")
	fmt.Fprintf(b, "  Provider identity sets: %s.\n", comparison.ProviderIdentityStatus)
	if comparison.ProviderIdentityStatus != "same" {
		fmt.Fprintf(b, "    before: %s\n    after: %s\n", displaySet(comparison.BeforeProviders), displaySet(comparison.AfterProviders))
	}
	fmt.Fprintln(b, "  Provider identity strings do not establish matching provider versions.")
	fmt.Fprintln(b, "  RPC calls and UI resource operations measure different, potentially overlapping work; they are not combined.")
	fmt.Fprintln(b, "  UI durations are rounded by up to one second per observation.")
	fmt.Fprintln(b, "  added and removed mean observed evidence presence, not resource creation or destruction.")
	fmt.Fprintln(b, "  Observed changes do not establish causes or a performance verdict.")
	fmt.Fprintln(b, "  separately scrubbed aliases cannot be matched reliably.")
	fmt.Fprintln(b)
}

func displaySet(values []string) string {
	if len(values) == 0 {
		return "(none observed)"
	}
	displayed := make([]string, len(values))
	for i, value := range values {
		displayed[i] = logfmt.DisplayText(value)
	}
	return strings.Join(displayed, ", ")
}

func writeUnnamedUI(b *strings.Builder, before, after *model.ComparisonTotal) {
	fmt.Fprintln(b, "UNNAMED UI OBSERVATIONS\n  UI observations with missing addresses cannot be matched to exact operations.")
	fmt.Fprintf(b, "  before: %s\n  after: %s\n\n", compactTotal(before), compactTotal(after))
}
func compactTotal(total *model.ComparisonTotal) string {
	if total == nil {
		return "unavailable"
	}
	prefix := ""
	if total.LowerBound {
		prefix = ">="
	}
	return fmt.Sprintf("count %d, total ms %s%d", total.Count, prefix, total.TotalMs)
}

func writeComparisonSection(b *strings.Builder, section model.ComparisonSection, limit int) {
	title := comparisonSectionTitle(section.Kind)
	fmt.Fprintf(b, "%s AVAILABILITY\n  before: %s; after: %s\n", title, availability(section.BeforeAvailable), availability(section.AfterAvailable))
	exactAt := len(section.Rows)
	for i := range section.Rows {
		if section.Rows[i].Changes.TotalMs == nil {
			exactAt = i
			break
		}
	}
	exact, unranked := section.Rows[:exactAt], section.Rows[exactAt:]
	if len(exact) == 0 && len(unranked) == 0 {
		fmt.Fprintln(b, "  no rows")
		fmt.Fprintln(b)
		return
	}
	writeComparisonRows(b, title+" — EXACT TOTAL-DURATION CHANGES", section.Kind, exact, limit)
	writeComparisonRows(b, title+" — UNRANKED / UNAVAILABLE", section.Kind, unranked, limit)
}
func availability(ok bool) string {
	if ok {
		return "available"
	}
	return "unavailable"
}
func comparisonSectionTitle(kind string) string {
	return map[string]string{"rpc_providers": "RPC PROVIDERS", "rpc_resource_types": "RPC RESOURCE TYPES", "rpc_methods": "RPC METHODS", "ui_resource_types": "UI RESOURCE TYPES", "ui_operations": "UI OPERATIONS"}[kind]
}
func writeComparisonRows(b *strings.Builder, heading, kind string, rows []model.ComparisonRow, limit int) {
	if len(rows) == 0 {
		return
	}
	shown := limitedLength(len(rows), limit)
	fmt.Fprintln(b, heading)
	if shown < len(rows) {
		fmt.Fprintf(b, "  shown %d of %d\n", shown, len(rows))
	}
	for _, row := range rows[:shown] {
		writeComparisonRow(b, kind, row)
	}
	fmt.Fprintln(b)
}

func writeComparisonRow(b *strings.Builder, kind string, row model.ComparisonRow) {
	fmt.Fprintf(b, "  %s: %s\n    metric        before       after        delta       change\n", row.State, comparisonKeyText(kind, row.Key))
	writeComparisonMetric(b, "count", totalCount(row.Before), totalCount(row.After), signedText(row.Changes.Count), percentText(row.Changes.CountPercent))
	writeComparisonMetric(b, "total ms", totalUint(row.Before), totalUint(row.After), signedText(row.Changes.TotalMs), percentText(row.Changes.TotalPercent))
	writeComparisonMetric(b, "mean ms", totalFloat(row.Before), totalFloat(row.After), floatChangeText(row.Changes.MeanMs), percentText(row.Changes.MeanPercent))
	writeComparisonMetric(b, "max ms", totalMax(row.Before), totalMax(row.After), signedText(row.Changes.MaxMs), percentText(row.Changes.MaxPercent))
	if (row.Before != nil && row.Before.LowerBound) || (row.After != nil && row.After.LowerBound) {
		fmt.Fprintln(b, "    timing deltas unavailable: lower bound")
	}
	if row.State == "unavailable" {
		fmt.Fprintf(b, "    before %s; after %s\n", totalAvailability(row.Before), totalAvailability(row.After))
	}
}
func comparisonKeyText(kind string, key model.ComparisonKey) string {
	d := logfmt.DisplayText
	switch kind {
	case "rpc_providers":
		return "provider=" + d(key.Provider)
	case "rpc_resource_types", "ui_resource_types":
		return "resource_type=" + d(key.ResourceType)
	case "rpc_methods":
		return "provider=" + d(key.Provider) + "  resource_type=" + d(key.ResourceType) + "  method=" + d(key.Method)
	case "ui_operations":
		return "address=" + d(key.Address) + "  action=" + d(key.Action)
	default:
		return "unknown"
	}
}
func writeComparisonMetric(b *strings.Builder, name, before, after, delta, percent string) {
	fmt.Fprintf(b, "    %-13s %-12s %-12s %-11s %s\n", name, before, after, delta, percent)
}
func totalAvailability(total *model.ComparisonTotal) string {
	if total == nil {
		return "unavailable"
	}
	return "available"
}
func totalCount(total *model.ComparisonTotal) string {
	if total == nil {
		return "n/a"
	}
	return strconv.FormatUint(total.Count, 10)
}
func totalUint(total *model.ComparisonTotal) string {
	if total == nil {
		return "n/a"
	}
	p := ""
	if total.LowerBound {
		p = ">="
	}
	return p + strconv.FormatUint(total.TotalMs, 10)
}
func totalFloat(total *model.ComparisonTotal) string {
	if total == nil || total.MeanMs == nil {
		return "n/a"
	}
	p := ""
	if total.LowerBound {
		p = ">="
	}
	return p + fmt.Sprintf("%.2f", normalZero(*total.MeanMs))
}
func totalMax(total *model.ComparisonTotal) string {
	if total == nil || total.MaxMs == nil {
		return "n/a"
	}
	p := ""
	if total.LowerBound {
		p = ">="
	}
	return p + strconv.FormatUint(uint64(*total.MaxMs), 10)
}
func signedText(change *model.SignedChange) string {
	if change == nil {
		return "n/a"
	}
	sign := "+"
	if change.Negative && change.Magnitude != 0 {
		sign = "-"
	}
	return sign + strconv.FormatUint(change.Magnitude, 10)
}
func floatChangeText(change *float64) string {
	if change == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+.2f", normalZero(*change))
}
func percentText(change *float64) string {
	if change == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+.2f%%", normalZero(*change))
}
func normalZero(value float64) float64 {
	if math.Abs(value) < 0.005 {
		return 0
	}
	return value
}
