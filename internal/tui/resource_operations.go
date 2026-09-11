package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/qualitytext"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func (m *Model) operationRows() []row {
	indices := append([]int(nil), m.selectedResources().UIIndices...)
	sort.SliceStable(indices, func(i, j int) bool {
		a, b := m.log.UISpans[indices[i]], m.log.UISpans[indices[j]]
		if a.DurationMs != b.DurationMs {
			return a.DurationMs > b.DurationMs
		}
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		if a.RPC != b.RPC {
			return a.RPC < b.RPC
		}
		if a.Entry != b.Entry {
			return a.Entry < b.Entry
		}
		return indices[i] < indices[j]
	})
	rows := make([]row, 0, len(indices))
	for _, index := range indices {
		s := m.log.UISpans[index]
		duration := operationDuration(s.DurationMs, s.DurationSaturated)
		action := logfmt.DisplayText(s.RPC)
		if action == "" {
			action = "unavailable"
		}
		source, sourceLine := "unavailable", uint64(0)
		if location, ok := m.log.ObservationSource(s); ok {
			sourceLine = location.StartLine
			source = fmt.Sprintf("%d", sourceLine)
			if location.EndLine != location.StartLine {
				source = fmt.Sprintf("%d-%d", location.StartLine, location.EndLine)
			}
		}
		rows = append(rows, row{
			identity: selectionIdentity{kind: "ui", index: index},
			cells:    []string{logfmt.DisplayText(s.Address), action, resourceOperationDuration(s), source},
			numeric:  []uint64{0, 0, duration.TotalMs, sourceLine},
			spanIdx:  noSpanIdx,
		})
	}
	return rows
}

func operationDuration(ms uint32, lowerBound bool) model.DurationTotal {
	return model.DurationTotal{Count: 1, TotalMs: uint64(ms), MaxMs: ms, LowerBound: lowerBound}
}

func resourceOperationDuration(s span.Span) string {
	if s.DurationSource == span.SourceUIElapsed {
		return durationTotalText(operationDuration(s.DurationMs, s.DurationSaturated))
	}
	text := qualitytext.ExactDuration(uint64(s.DurationMs))
	if s.DurationSaturated {
		return "≥" + text
	}
	return text
}

func (m *Model) selectedUIOperation() (int, bool) {
	if m.view != ViewResources || !m.resourceOperations {
		return 0, false
	}
	r, ok := m.selectedRow()
	if !ok || r.identity.kind != "ui" || r.identity.index < 0 || r.identity.index >= len(m.log.UISpans) {
		return 0, false
	}
	index := r.identity.index
	return index, true
}

func (m *Model) renderResourceOperations(w, h int) string {
	cols, rows, sortCol := visibleOperationColumns(operationColumns, m.rows(), m.activeSort(), w)
	empty := noRowsNote
	if m.filterActive() {
		empty = m.noMatchTail()
	}
	return renderTable(nil, cols, sortCol, rows, empty, m.selected, m.pane == PaneList, w, h)
}

func (m *Model) operationJumpContextLines() int {
	visibleLines := paneBodyHeight(workbenchPaneHeight(m.height))
	return min(jumpContextLines, max(0, visibleLines/2-1))
}

func visibleOperationColumns(cols []column, rows []row, sortCol, w int) ([]column, []row, int) {
	if w >= 60 {
		return cols, rows, sortCol
	}
	trimmed := make([]row, len(rows))
	for i, r := range rows {
		trimmed[i] = r
		trimmed[i].cells = append([]string(nil), r.cells[1:]...)
		trimmed[i].numeric = append([]uint64(nil), r.numeric[1:]...)
	}
	if sortCol == 0 {
		sortCol = -1
	} else {
		sortCol--
	}
	return cols[1:], trimmed, sortCol
}

func (m *Model) operationDetailSections(index, w int) []paneSection {
	if index < 0 || index >= len(m.log.UISpans) {
		return nil
	}
	s := m.log.UISpans[index]
	action := logfmt.DisplayText(s.RPC)
	if action == "" {
		action = "unavailable"
	}
	source := "unavailable"
	if location, ok := m.log.ObservationSource(s); ok {
		source = fmt.Sprintf("line %d", location.StartLine)
		if location.EndLine != location.StartLine {
			source = fmt.Sprintf("lines %d-%d", location.StartLine, location.EndLine)
		}
	}
	fields := []string{
		"address: " + logfmt.DisplayText(s.Address),
		"action: " + action,
		"source: " + source,
		"observed resource duration: " + resourceOperationDuration(s),
		qualitytext.DurationSourceQualification(s.DurationSource),
	}
	if s.DurationSaturated {
		fields = append(fields, "Observed resource duration is a lower bound (≥).")
	}
	if s.StartClamped {
		fields = append(fields, "Operation start was clamped to the capture origin.")
	}
	if !s.HasPosition() {
		fields = append(fields, "Timeline position unavailable.")
	}
	var lines paneSection
	for _, field := range fields {
		lines = append(lines, strings.Split(ansi.Wrap(field, max(1, w), ""), "\n")...)
	}
	return []paneSection{lines}
}
