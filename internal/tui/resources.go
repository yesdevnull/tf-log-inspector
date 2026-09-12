package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func (m *Model) resourceRows() []row {
	projection := m.selectedResources()
	rows := make([]row, len(projection.Rows))
	for i := range projection.Rows {
		r := &projection.Rows[i]
		var sources uint64
		for _, op := range r.Operations {
			sources |= 1 << m.log.UISpans[op.UIIndex].DurationSource
		}
		var labels []string
		for source, label := range []string{"UI", "refresh", "CLI"} {
			if sources&(1<<source) != 0 {
				labels = append(labels, label)
			}
		}
		rows[i] = row{
			identity: selectionIdentity{kind: "resource", value: r.Address},
			cells:    []string{logfmt.DisplayText(r.Address), strconv.FormatUint(r.UI.Count, 10), durationTotalText(r.UI), durationMaxText(r.UI), strings.Join(labels, "+"), strconv.FormatUint(r.NamedRPC.Count, 10), rpcEvidenceDuration(r.NamedRPC), strconv.FormatUint(r.OverlappingRPC.Count, 10), rpcEvidenceDuration(r.OverlappingRPC)},
			numeric:  []uint64{0, r.UI.Count, r.UI.TotalMs, uint64(r.UI.MaxMs), sources, r.NamedRPC.Count, r.NamedRPC.TotalMs, r.OverlappingRPC.Count, r.OverlappingRPC.TotalMs},
			spanIdx:  noSpanIdx,
			resource: r,
		}
	}
	return rows
}

func durationTotalText(d model.DurationTotal) string {
	prefix := ""
	if d.LowerBound {
		prefix = "≥"
	}
	return prefix + formatMs(d.TotalMs)
}

func rpcEvidenceDuration(d model.DurationTotal) string {
	if d.Count == 0 {
		return "n/a"
	}
	return durationTotalText(d)
}

func durationMaxText(d model.DurationTotal) string {
	prefix := ""
	if d.LowerBound {
		prefix = "≥"
	}
	return prefix + formatMs(uint64(d.MaxMs))
}

func (m *Model) renderResources(w, h int) string {
	if m.resourceOperations {
		return m.renderResourceOperations(w, h)
	}
	p := m.selectedResources()
	var preamble []string
	if p.UI.Count > 0 {
		preamble = append(preamble, typesPreamble(m.selectedUISpans())...)
		if p.UI.LowerBound {
			preamble = append(preamble, "Observed totals and maxima are lower bounds (≥).")
		}
		positioned := 0
		for _, i := range p.UIIndices {
			if m.log.UISpans[i].HasPosition() {
				positioned++
			}
		}
		if positioned < len(p.UIIndices) {
			preamble = append(preamble, fmt.Sprintf("Timeline position unavailable for %d of %d observed operations.", len(p.UIIndices)-positioned, len(p.UIIndices)))
		}
	}
	empty := noRowsNote
	switch {
	case len(p.Rows) > 0:
	case p.UnnamedUI.Count > 0:
		empty = "no exact address: observed resource operations are ungrouped."
	case len(m.log.UISpans) == 0 && m.log.UIEvidence.Records > 0:
		empty = "observed resource timing unavailable: completion records were rejected."
	case len(m.log.UISpans) == 0 && len(m.log.RPCSpans) > 0:
		empty = "no observed resource operations.\nUse 4 calls, 2 types, i quality, or e evidence."
	case m.filterActive():
		empty = m.noMatchTail()
	default:
		empty = "no observed resource operations."
	}
	rows := m.rows()
	if len(rows) == 0 {
		return renderResourceEmpty(empty, w, h)
	}
	preamble = fitTableGuidance(preamble, w, h)
	cols, rows := visibleResourceColumns(resourceColumns, rows, w)
	return renderTable(preamble, cols, m.activeSort(), rows, empty, m.selected, m.pane == PaneList, w, h)
}

func renderResourceEmpty(message string, w, h int) string {
	var lines []string
	for _, paragraph := range strings.Split(message, "\n") {
		lines = append(lines, wrapToWidth(paragraph, w)...)
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = styles.note.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

// visibleResourceColumns keeps the observed identity and measurements usable
// before admitting the supplementary inferred RPC pairs.
func visibleResourceColumns(cols []column, rows []row, w int) ([]column, []row) {
	cols = append([]column(nil), cols...)
	if w < 60 {
		cols[1].header, cols[2].header, cols[3].header = "ops", "total", "max"
	}
	visible := 5
	natural := columnWidths(headerCells(cols, 2), rows)
	for candidate := 7; candidate <= len(cols); candidate += 2 {
		reserved := 2 * (candidate - 1)
		for i := 1; i < candidate; i++ {
			reserved += natural[i]
		}
		if w-reserved < 12 {
			break
		}
		visible = candidate
	}
	trimmed := make([]row, len(rows))
	for i, r := range rows {
		trimmed[i] = r
		trimmed[i].cells = r.cells[:visible]
		trimmed[i].numeric = r.numeric[:visible]
	}
	return cols[:visible], trimmed
}

func resourceDetailSections(r *model.ResourceRow, w int) []paneSection {
	fields := []string{
		"address: " + logfmt.DisplayText(r.Address),
		fmt.Sprintf("operations: %d", r.UI.Count),
		"observed resource total: " + durationTotalText(r.UI),
		"observed resource max: " + durationMaxText(r.UI),
	}
	if r.NamedRPC.Count > 0 {
		fields = append(fields, fmt.Sprintf("inferred Contained/Likely RPCs: %d, %s", r.NamedRPC.Count, rpcEvidenceDuration(r.NamedRPC)))
	}
	if r.OverlappingRPC.Count > 0 {
		fields = append(fields, fmt.Sprintf("inferred Overlapping RPCs: %d, %s", r.OverlappingRPC.Count, rpcEvidenceDuration(r.OverlappingRPC)))
	}
	fields = append(fields, "Duration sources and qualifications: e evidence.")
	var lines paneSection
	for _, field := range fields {
		lines = append(lines, strings.Split(ansi.Wrap(field, max(1, w), ""), "\n")...)
	}
	return []paneSection{lines}
}

// captureSummary is separate from the filename so long paths cannot hide evidence.
func (m *Model) captureSummary() string {
	label := "resource operations"
	cliOnly := len(m.log.UISpans) > 0
	for _, s := range m.log.UISpans {
		cliOnly = cliOnly && s.DurationSource == span.SourceCLIElapsed
	}
	if cliOnly {
		label = "CLI operations"
	}
	count := strconv.Itoa(len(m.log.UISpans))
	if m.filterActive() {
		count = fmt.Sprintf("%d/%s", len(m.selectedResources().UIIndices), count)
	}
	text := count + " " + label
	if len(m.log.RPCSpans) == 0 {
		text += " · no RPC timings"
	} else {
		text += fmt.Sprintf(" · %d RPC timings", len(m.log.RPCSpans))
	}
	if cliOnly {
		text += " · no timestamps"
	}
	return text
}
