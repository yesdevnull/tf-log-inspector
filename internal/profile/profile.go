// Package profile renders a loaded model.Log as a plain-text performance
// report for the user who captured it.
//
// This is the opposite safety posture from internal/diagnose: that package
// exists so a report can be sent back to this project about a confidential
// work log, so it masks addresses and withholds content. This package exists
// so the user can see which of their own resources were slow, so it prints
// real, unmasked resource addresses. Never reuse diagnose's masking here, and
// never let this report's content leak into a diagnose report -- an
// unmasked address is the one thing that must never appear in the other
// package's output.
package profile

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// maxRows caps SLOWEST CALLS and SLOWEST RESOURCES at their top N: enough to
// see shape and rank, not a full listing.
const maxRows = 20

// typeColWidth and actionColWidth are the fixed widths given to a resource
// type / RPC name column and a UI-hook action column respectively. Both are
// truncated to these widths before formatting -- see truncate -- so a name
// wider than its column can never shunt every column after it out of
// alignment.
const (
	typeColWidth   = 24
	actionColWidth = 8
)

// truncate ellipsizes s to at most n bytes so a fixed-width column can never
// overflow into the columns that follow it. A %-Ns verb only pads a short
// string; it never shortens a long one, and real provider type names run
// well past any width this report could reasonably use -- for example
// azuread_application_federated_identity_credential.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// Render writes a profile report for l to w.
func Render(w io.Writer, l *model.Log) error {
	b := &strings.Builder{}

	fmt.Fprintf(b, "tfli profile report\n====================\n\n")
	fmt.Fprintf(b, "SIZE\n")
	fmt.Fprintf(b, "  bytes                %d\n", l.Stats.Bytes)
	fmt.Fprintf(b, "  RPC spans            %d\n", len(l.RPCSpans))
	fmt.Fprintf(b, "  UI-hook spans        %d\n", len(l.UISpans))
	fmt.Fprintf(b, "\n")
	fmt.Fprintf(b, "Resource addresses in this report are not masked. Unlike\n")
	fmt.Fprintf(b, "--diagnose, this report is not safe to share.\n\n")

	if len(l.RPCSpans) == 0 && len(l.UISpans) == 0 {
		fmt.Fprintf(b, "NO SPANS\n")
		fmt.Fprintf(b, "  This log has no spans to profile: no provider RPC\n")
		fmt.Fprintf(b, "  entries, and no terraform.ui structured-output stream\n")
		fmt.Fprintf(b, "  to read UI-hook spans from.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	writeResourceTypeJoin(b, l.RPCSpans, l.UISpans)
	writeProviderRollup(b, l.RPCSpans)
	writeSlowestCalls(b, l.RPCSpans)
	writeSlowestResources(b, l.UISpans)
	writeConcurrency(b, l.RPCSpans)

	_, err := io.WriteString(w, b.String())
	return err
}

// writeResourceTypeJoin renders BY RESOURCE TYPE from model.JoinByResourceType.
func writeResourceTypeJoin(b *strings.Builder, rpcSpans, uiSpans []span.Span) {
	rows := model.JoinByResourceType(rpcSpans, uiSpans)
	if len(rows) == 0 {
		return
	}

	fmt.Fprintf(b, "BY RESOURCE TYPE\n")
	uiPresent := false
	for _, r := range rows {
		if r.UIResources > 0 {
			uiPresent = true
			break
		}
	}
	if uiPresent {
		// See writeSlowestResources for why UI-hook figures carry this
		// caveat: it applies here too, since these UI totals are sums of the
		// same whole-second, +/-1s measurements.
		fmt.Fprintf(b, "  UI-hook figures are sums of measurements rounded to\n")
		fmt.Fprintf(b, "  whole seconds, +/- 1s each.\n")
	}
	// The numeric columns are 9 wide, not 8: "RPC calls" and "RPC total" are
	// themselves 9-character labels, and a %9s header narrower than its own
	// label would overrun into the columns after it -- the same hazard
	// truncate guards the resource-type column against, just on the header
	// row instead of the data.
	fmt.Fprintf(b, "  %-*s %9s %9s %9s %9s %9s\n",
		typeColWidth, "resource type", "UI res.", "UI total", "RPC calls", "RPC total", "RPC max")
	for _, r := range rows {
		fmt.Fprintf(b, "  %-*s %9d %9s %9d %9s %9s\n",
			typeColWidth, truncate(r.ResourceType, typeColWidth), r.UIResources, formatMs(r.UITotalMs),
			r.RPCCalls, formatMs(r.RPCTotalMs), formatMs(uint64(r.RPCMaxMs)))
	}
	fmt.Fprintf(b, "\n")
}

// writeProviderRollup renders BY PROVIDER from model.RollupBy. It is skipped
// when there are no RPC spans: a provider rollup has no meaning over
// UI-hook spans, which carry no provider name.
func writeProviderRollup(b *strings.Builder, rpcSpans []span.Span) {
	if len(rpcSpans) == 0 {
		return
	}
	buckets := model.RollupBy(rpcSpans, func(s span.Span) string { return s.Provider })

	fmt.Fprintf(b, "BY PROVIDER\n")
	fmt.Fprintf(b, "  %8s %8s %8s  %s\n", "total", "calls", "max", "provider")
	for _, bkt := range buckets {
		fmt.Fprintf(b, "  %8s %8d %8s  %s\n",
			formatMs(bkt.TotalMs), bkt.Count, formatMs(uint64(bkt.MaxMs)), bkt.Key)
	}
	fmt.Fprintf(b, "\n")
}

// writeSlowestCalls renders SLOWEST CALLS: RPC spans sorted by duration
// descending, top maxRows.
func writeSlowestCalls(b *strings.Builder, rpcSpans []span.Span) {
	if len(rpcSpans) == 0 {
		return
	}
	rows := append([]span.Span(nil), rpcSpans...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DurationMs != rows[j].DurationMs {
			return rows[i].DurationMs > rows[j].DurationMs
		}
		return rows[i].RPC < rows[j].RPC
	})
	if len(rows) > maxRows {
		fmt.Fprintf(b, "SLOWEST CALLS (top %d)\n", maxRows)
		rows = rows[:maxRows]
	} else {
		fmt.Fprintf(b, "SLOWEST CALLS\n")
	}
	for _, s := range rows {
		fmt.Fprintf(b, "  %8s  %-*s %-*s %s\n",
			formatMs(uint64(s.DurationMs)), typeColWidth, truncate(s.RPC, typeColWidth),
			typeColWidth, truncate(s.ResourceType, typeColWidth), s.Provider)
	}
	fmt.Fprintf(b, "\n")
}

// writeSlowestResources renders SLOWEST RESOURCES: UI-hook spans sorted by
// duration descending, top maxRows, with real, unmasked addresses.
func writeSlowestResources(b *strings.Builder, uiSpans []span.Span) {
	if len(uiSpans) == 0 {
		return
	}
	rows := append([]span.Span(nil), uiSpans...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DurationMs != rows[j].DurationMs {
			return rows[i].DurationMs > rows[j].DurationMs
		}
		return rows[i].Address < rows[j].Address
	})
	if len(rows) > maxRows {
		fmt.Fprintf(b, "SLOWEST RESOURCES (top %d)\n", maxRows)
		rows = rows[:maxRows]
	} else {
		fmt.Fprintf(b, "SLOWEST RESOURCES\n")
	}
	// Terraform rounds a resource's start and end to the nearest second
	// before subtracting them, so these figures are whole seconds carrying
	// up to a second of error each -- see internal/diagnose's identical
	// caveat on its own SLOWEST RESOURCES section.
	fmt.Fprintf(b, "  Terraform reports these in whole seconds, +/- 1s each, so\n")
	fmt.Fprintf(b, "  neighbouring rows are not reliably ordered.\n")
	for _, s := range rows {
		fmt.Fprintf(b, "  %8s  %-*s %-*s %s\n",
			formatMs(uint64(s.DurationMs)), actionColWidth, truncate(s.RPC, actionColWidth),
			typeColWidth, truncate(s.ResourceType, typeColWidth), s.Address)
	}
	fmt.Fprintf(b, "\n")
}

// writeConcurrency renders CONCURRENCY for the RPC tier: peak concurrency,
// summed span time, wall clock and their ratio.
//
// This deliberately does not call model.PackLanes or print a lane count.
// PackLanes' lane count is a rendering construct for the phase-4 timeline --
// every span, including a degenerate zero-duration one, needs a row to draw
// it in, whether or not it overlaps anything -- not a profiling metric. For
// a degenerate zero-duration span, PackLanes' lane count and
// model.PeakConcurrency's peak measure different things and can legitimately
// disagree (see the doc comment on PeakConcurrency), so printing both here
// invites exactly the "these two numbers should match" reading that is
// wrong.
func writeConcurrency(b *strings.Builder, rpcSpans []span.Span) {
	if len(rpcSpans) == 0 {
		return
	}

	var summed uint64
	var wallClock uint32
	for _, s := range rpcSpans {
		summed += uint64(s.DurationMs)
		if s.EndMs > wallClock {
			wallClock = s.EndMs
		}
	}

	fmt.Fprintf(b, "CONCURRENCY (RPC tier)\n")
	fmt.Fprintf(b, "  peak concurrency     %d\n", model.PeakConcurrency(rpcSpans))
	fmt.Fprintf(b, "  summed span time     %s\n", formatMs(summed))
	if wallClock > 0 {
		fmt.Fprintf(b, "  wall clock           %s\n", formatMs(uint64(wallClock)))
		fmt.Fprintf(b, "  summed / wall clock  %.1fx\n", float64(summed)/float64(wallClock))
	}
	fmt.Fprintf(b, "\n")
}

// formatMs renders a millisecond duration: whole milliseconds below one
// second, seconds with one decimal place at or above it. Copied from
// internal/diagnose rather than shared -- see the package doc comment on why
// this report and diagnose's are kept free to diverge.
func formatMs(ms uint64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
