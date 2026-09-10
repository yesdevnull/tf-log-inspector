package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func (m *Model) resourceRows() []row {
	projection := m.selectedResources()
	rows := make([]row, len(projection.Rows))
	for i := range projection.Rows {
		r := &projection.Rows[i]
		rows[i] = row{
			cells:    []string{logfmt.DisplayText(r.Address), strconv.FormatUint(r.UI.Count, 10), durationTotalText(r.UI), durationMaxText(r.UI), strconv.FormatUint(r.NamedRPC.Count, 10), durationTotalText(r.NamedRPC), strconv.FormatUint(r.OverlappingRPC.Count, 10), durationTotalText(r.OverlappingRPC)},
			numeric:  []uint64{0, r.UI.Count, r.UI.TotalMs, uint64(r.UI.MaxMs), r.NamedRPC.Count, r.NamedRPC.TotalMs, r.OverlappingRPC.Count, r.OverlappingRPC.TotalMs},
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

func durationMaxText(d model.DurationTotal) string {
	prefix := ""
	if d.LowerBound {
		prefix = "≥"
	}
	return prefix + formatMs(uint64(d.MaxMs))
}

func (m *Model) renderResources(w, h int) string {
	p := m.selectedResources()
	preamble := []string{
		"Scopes: UI type/resource/module; RPC provider/type/method/resource/module.",
		"Inferred RPC evidence is partial; observed UI ranks rows.",
	}
	if p.UI.Count > 0 {
		preamble = append(preamble, "Observed UI operations; measurements rounded to whole seconds, +/- 1s each.")
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
		empty = "no exact address: observed UI operations are ungrouped."
	case len(m.log.UISpans) == 0 && m.log.UIEvidence.Records > 0:
		empty = "observed UI timing unavailable: completion records were rejected."
	case len(m.log.UISpans) == 0 && len(m.log.RPCSpans) > 0:
		empty = "no observed UI resource operations.\nUse 4 calls, 2 types, i quality, or e evidence."
	case m.filterActive():
		empty = noMatchNote
	default:
		empty = "no observed UI resource operations."
	}
	rows := m.rows()
	if len(rows) == 0 {
		return renderResourceEmpty(empty, w, h)
	}
	if len(rows) > 0 && len(preamble) > max(0, h-2) {
		preamble = preamble[:max(0, h-2)]
	}
	cols, rows := visibleResourceColumns(resourceColumns, rows, w)
	return renderTable(preamble, cols, m.sortCol[ViewResources], rows, empty, m.selected, m.pane == PaneList, w, h)
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
	visible := 4
	natural := columnWidths(headerCells(cols, 2), rows)
	for candidate := 6; candidate <= len(cols); candidate += 2 {
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
		"observed UI total: " + durationTotalText(r.UI),
		"observed UI max: " + durationMaxText(r.UI),
		fmt.Sprintf("inferred Contained/Likely RPCs: %d, %s", r.NamedRPC.Count, durationTotalText(r.NamedRPC)),
		fmt.Sprintf("inferred Overlapping RPCs: %d, %s", r.OverlappingRPC.Count, durationTotalText(r.OverlappingRPC)),
		"UI timings are rounded to whole seconds, +/- 1s each.",
	}
	var lines paneSection
	for _, field := range fields {
		lines = append(lines, strings.Split(ansi.Wrap(field, max(1, w), ""), "\n")...)
	}
	return []paneSection{lines}
}
