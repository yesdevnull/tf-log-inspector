package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/investigation"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

type eventExportState struct {
	open           bool
	format         string
	input          textinput.Model
	notice         string
	selectedSource *model.SourceLocation
}

func (m *Model) beginEventExport() {
	state := eventExportState{open: true, input: newSearchInput()}
	if action, ok := m.selectedEventPanelAction(); ok && action.sourceLine != 0 {
		for _, event := range m.log.Events {
			if event.Location.StartLine == action.sourceLine {
				source := event.Location
				state.selectedSource = &source
				break
			}
		}
	}
	m.events.export = state
}

func (m *Model) handleEventExportKey(msg tea.KeyMsg) {
	if msg.Type == tea.KeyEsc {
		m.events.export.open = false
		return
	}
	if m.events.export.format == "" {
		switch msg.String() {
		case "m":
			m.events.export.format = "markdown"
		case "j":
			m.events.export.format = "json"
		default:
			return
		}
		m.events.export.input.Focus()
		return
	}
	if msg.Type == tea.KeyEnter {
		path := m.events.export.input.Value()
		if path == "" {
			m.events.export.notice = "Destination is required"
			return
		}
		report := m.currentInvestigationReport()
		err := writeNewExport(path, func(w io.Writer) error {
			if m.events.export.format == "json" {
				return investigation.RenderJSON(w, report)
			}
			return investigation.RenderMarkdown(w, report)
		})
		if err != nil {
			m.events.export.notice = err.Error()
			return
		}
		m.events.export.open = false
		m.events.export.notice = "Exported " + filepath.Base(path)
		return
	}
	m.events.export.input, _ = m.events.export.input.Update(msg)
}

func (m *Model) currentInvestigationReport() investigation.Report {
	filter := m.eventPanelFilter()
	scope := investigation.Scope{Panel: eventPanelName(m.events.mode), Query: filter.Query, Address: filter.Address, Kind: filter.Kind, Severity: filter.Severity}
	scope.SelectedSource = m.events.export.selectedSource
	projection := m.selectedResources()
	selection := investigation.Selection{Scope: scope, RPCIndices: projection.RPCIndices, UIIndices: projection.UIIndices, EventIndices: m.selectedEventIndices(filter), TimingScope: m.timingScopeDescription(), EventScope: "current event panel query and facets"}
	return investigation.Build(m.log, investigation.Metadata{ToolVersion: m.toolVersion, InputBasename: m.name}, selection)
}

func (m *Model) timingScopeDescription() string {
	base := m.filter()
	parts := []string{"current timing selection"}
	appendValues := func(name string, values map[string]bool) {
		if values == nil {
			return
		}
		keys := make([]string, 0, len(values))
		for value, selected := range values {
			if selected {
				keys = append(keys, value)
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			parts = append(parts, name+"=(none)")
			return
		}
		parts = append(parts, name+"="+strings.Join(keys, ","))
	}
	appendValues("provider", base.Providers)
	appendValues("method", base.RPCs)
	appendValues("resource type", base.Types)
	appendValues("address", m.resourceSelection.Addresses)
	appendValues("module subtree", m.resourceSelection.Modules)
	appendValues("duration source", intersectSelection(m.resourceSelection.Sources, m.allowedFacetValues(dimSource)))
	appendValues("lifecycle action", intersectSelection(m.resourceSelection.Actions, m.allowedFacetValues(dimAction)))
	if m.resourceSelection.ExactModules != nil {
		values := make(map[string]bool)
		for module, selected := range m.resourceSelection.ExactModules {
			if selected {
				values[module.Path] = true
			}
		}
		appendValues("exact module", values)
	}
	return strings.Join(parts, "; ")
}

func eventPanelName(mode string) string {
	switch mode {
	case "p":
		return "outcomes"
	case "u":
		return "incomplete"
	case "d":
		return "diagnostics"
	case "M":
		return "milestones"
	default:
		return "event history"
	}
}

func (m *Model) selectedEventIndices(filter model.EventFilter) []int {
	population := make(map[model.SourceLocation]bool)
	add := func(event model.ResourceEvent) {
		if len(model.FilterEvents([]model.ResourceEvent{event}, filter)) != 0 {
			population[event.Location] = true
		}
	}
	switch m.events.mode {
	case "p":
		for _, event := range m.log.Events {
			if event.Kind == model.EventPlannedChange || event.Kind == model.EventDrift || event.Kind == model.EventDiagnostic || event.Kind == model.EventChangeSummary {
				add(event)
			}
		}
	case "u":
		for _, operation := range m.log.Incomplete {
			add(operation.Start)
			if operation.LastProgress != nil {
				add(*operation.LastProgress)
			}
		}
	case "d":
		for _, event := range m.log.Events {
			if event.Kind == model.EventDiagnostic {
				add(event)
			}
		}
	case "M":
		for _, milestone := range model.InvestigationMilestones(m.log.Events) {
			add(milestone.Event)
		}
	default:
		for _, event := range m.log.Events {
			add(event)
		}
	}
	var indices []int
	for index, event := range m.log.Events {
		if population[event.Location] {
			indices = append(indices, index)
		}
	}
	return indices
}

func writeNewExport(destination string, render func(io.Writer) error) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".tfli-export-*")
	if err != nil {
		return fmt.Errorf("creating export: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := render(temporary); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("writing export: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing export: %w", err)
	}
	if err := os.Link(temporaryName, destination); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("destination %s already exists", destination)
		}
		return fmt.Errorf("publishing export: %w", err)
	}
	return nil
}
