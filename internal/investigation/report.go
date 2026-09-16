// Package investigation builds investigation reports independently of their
// command-line and terminal entry points.
package investigation

import (
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

type Metadata struct {
	ToolVersion   string
	InputBasename string
}

type Scope struct {
	Panel          string
	Query          string
	Address        string
	Kind           model.EventKind
	Severity       string
	SelectedSource *model.SourceLocation
}

type Selection struct {
	Scope        Scope
	RPCIndices   []int
	UIIndices    []int
	EventIndices []int
	TimingScope  string
	EventScope   string
}

type Report struct {
	Metadata    Metadata
	Scope       Scope
	TimingScope string
	EventScope  string
	Timings     []Timing
	Events      []model.ResourceEvent
	Incomplete  []model.IncompleteOperation
	Diagnostics []model.DiagnosticGroup
	Milestones  []model.Milestone
}

type Timing struct {
	Tier, Address, Action, Source, Qualification string
	DurationMs                                   uint32
	LowerBound                                   bool
	ClockOrigin                                  *time.Time
	Location                                     *model.SourceLocation
}

func Build(log *model.Log, metadata Metadata, selection Selection) Report {
	report := Report{Metadata: metadata, Scope: selection.Scope, TimingScope: selection.TimingScope, EventScope: selection.EventScope}
	if log == nil {
		return report
	}
	for _, index := range selection.RPCIndices {
		if index >= 0 && index < len(log.RPCSpans) {
			report.Timings = append(report.Timings, timing(log, "rpc", log.RPCSpans[index]))
		}
	}
	for _, index := range selection.UIIndices {
		if index >= 0 && index < len(log.UISpans) {
			report.Timings = append(report.Timings, timing(log, "resource", log.UISpans[index]))
		}
	}
	for _, index := range selection.EventIndices {
		if index >= 0 && index < len(log.Events) {
			report.Events = append(report.Events, log.Events[index])
		}
	}
	report.Diagnostics = model.GroupDiagnostics(report.Events)
	selectedLocations := make(map[model.SourceLocation]bool, len(report.Events))
	for _, event := range report.Events {
		selectedLocations[event.Location] = true
	}
	for _, milestone := range model.InvestigationMilestones(log.Events) {
		if selectedLocations[milestone.Event.Location] {
			report.Milestones = append(report.Milestones, milestone)
		}
	}
	for _, incomplete := range log.Incomplete {
		selected := selectedLocations[incomplete.Start.Location]
		if incomplete.LastProgress != nil {
			selected = selected || selectedLocations[incomplete.LastProgress.Location]
		}
		if selected {
			report.Incomplete = append(report.Incomplete, incomplete)
		}
	}
	return report
}

func timing(log *model.Log, tier string, observation span.Span) Timing {
	row := Timing{Tier: tier, Address: observation.Address, Action: observation.RPC, Source: observation.DurationSource.String(), DurationMs: observation.DurationMs, LowerBound: observation.DurationSaturated}
	if tier == "rpc" {
		row.Address = ""
		row.Source = "reported RPC"
	}
	if source, ok := log.ObservationSource(observation); ok {
		row.Location = &source
	}
	if observation.HasPosition() {
		origin := log.UIOrigin
		if tier == "rpc" {
			origin = log.Stats.FirstTS
		}
		if !origin.IsZero() {
			row.ClockOrigin = &origin
		}
	}
	if observation.DurationSaturated {
		row.Qualification = "lower bound"
	} else {
		row.Qualification = "observed duration"
	}
	return row
}
