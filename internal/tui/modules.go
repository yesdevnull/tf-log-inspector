package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

var moduleColumns = []column{
	{header: "module instance", kind: tailIdentifierColumn},
	{header: "ops", kind: numericColumn},
	{header: "total", kind: numericColumn},
	{header: "mean", kind: numericColumn},
	{header: "max", kind: numericColumn},
	{header: "sources", kind: headIdentifierColumn},
}

func moduleLabel(module model.ResourceModule) string {
	if !module.Known {
		return "(module unavailable)"
	}
	if module.Path == "" {
		return "(root module)"
	}
	return module.Path
}

func (m *Model) moduleRows() []row {
	groups := model.GroupResourcesByModule(m.log, m.resourceIndex, m.selectedResources())
	rows := make([]row, len(groups))
	for i := range groups {
		group := &groups[i]
		var sources []string
		fields := []detailField{
			{label: "module instance", value: moduleLabel(group.Module), kind: tailIdentifierColumn},
			{label: "operations", value: strconv.FormatUint(group.UI.Count, 10), kind: numericColumn},
			{label: "observed total", value: durationTotalText(group.UI), kind: numericColumn},
			{label: "observed mean", value: durationMeanText(group.UI), kind: numericColumn},
			{label: "observed max", value: durationMaxText(group.UI), kind: numericColumn},
		}
		for _, source := range []span.DurationSource{span.SourceUIElapsed, span.SourceRefreshWindow, span.SourceCLIElapsed} {
			if d := group.Sources[source]; d.Count > 0 {
				sources = append(sources, fmt.Sprintf("%s:%d", []string{"UI", "refresh", "CLI"}[source], d.Count))
				fields = append(fields, detailField{label: source.String(), value: fmt.Sprintf("%d operations, %s total, %s mean", d.Count, durationTotalText(d), durationMeanText(d)), kind: headIdentifierColumn})
			}
		}
		rows[i] = rollupRow([]string{logfmt.DisplayText(moduleLabel(group.Module)), strconv.FormatUint(group.UI.Count, 10), durationTotalText(group.UI), durationMeanText(group.UI), durationMaxText(group.UI), strings.Join(sources, "+")}, []uint64{0, group.UI.Count, group.UI.TotalMs, uint64(group.UI.MeanMs() * 1000), uint64(group.UI.MaxMs), 0}, &rollupDetail{aggregate: fields})
		rows[i].module = group
		rows[i].identity = selectionIdentity{kind: "module", value: moduleLabel(group.Module)}
	}
	return rows
}

func (m *Model) renderModules(w, h int) string {
	preamble := []string{"Exact module instances · summed observed durations can overlap.", "m resources · Enter scoped resources · ≥ lower bound"}
	return renderTable(fitTableGuidance(preamble, w, h), moduleColumns, m.activeSort(), m.rows(), m.noMatchTail(), m.selected, m.pane == PaneList, w, h)
}

func (m *Model) openModuleResources() bool {
	if m.view != ViewResources || !m.moduleRanking {
		return false
	}
	r, ok := m.selectedRow()
	if !ok || r.module == nil {
		return false
	}
	parent := m.captureNavigation()
	m.resourceSelection.ExactModules = map[model.ResourceModule]bool{r.module.Module: true}
	m.moduleRanking = false
	m.history = append(m.history, parent)
	m.changeView(ViewResources)
	m.selected = 0
	return true
}

func selectTableColumns(cols []column, rows []row, indices []int) ([]column, []row) {
	selected := make([]column, len(indices))
	for i, index := range indices {
		selected[i] = cols[index]
	}
	trimmed := make([]row, len(rows))
	for i, r := range rows {
		trimmed[i] = r
		trimmed[i].cells = make([]string, len(indices))
		trimmed[i].numeric = make([]uint64, len(indices))
		for j, index := range indices {
			trimmed[i].cells[j] = r.cells[index]
			trimmed[i].numeric[j] = r.numeric[index]
		}
	}
	return selected, trimmed
}
