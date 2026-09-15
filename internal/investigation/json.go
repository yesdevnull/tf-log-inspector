package investigation

import (
	"encoding/json"
	"io"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

type jsonReport struct {
	SchemaVersion uint8            `json:"schema_version"`
	Kind          string           `json:"kind"`
	ToolVersion   string           `json:"tool_version"`
	Input         jsonInput        `json:"input"`
	Scope         jsonScope        `json:"scope"`
	TimingScope   string           `json:"timing_scope"`
	EventScope    string           `json:"event_scope"`
	Timings       []jsonTiming     `json:"timings"`
	Events        []jsonEvent      `json:"events"`
	Incomplete    []jsonIncomplete `json:"incomplete_operations"`
	Diagnostics   []jsonDiagnostic `json:"diagnostics"`
	Milestones    []jsonMilestone  `json:"milestones"`
}
type jsonInput struct {
	Basename string `json:"basename"`
}
type jsonScope struct {
	Panel          string      `json:"panel"`
	Query          string      `json:"query"`
	Address        string      `json:"address"`
	Kind           string      `json:"kind"`
	Severity       string      `json:"severity"`
	SelectedSource *jsonSource `json:"selected_source"`
}
type jsonSource struct {
	Entry     uint32 `json:"entry"`
	StartLine uint64 `json:"start_line"`
	EndLine   uint64 `json:"end_line"`
	StartByte uint64 `json:"start_byte"`
	EndByte   uint64 `json:"end_byte"`
}
type jsonTiming struct {
	Tier          string      `json:"tier"`
	Address       string      `json:"address"`
	Action        string      `json:"action"`
	Source        string      `json:"source"`
	Qualification string      `json:"qualification"`
	DurationMs    uint32      `json:"duration_ms"`
	LowerBound    bool        `json:"lower_bound"`
	ClockOrigin   *string     `json:"clock_origin"`
	Location      *jsonSource `json:"location"`
}
type jsonEvent struct {
	Kind       string       `json:"kind"`
	Address    string       `json:"address"`
	Action     string       `json:"action"`
	Message    string       `json:"message"`
	Severity   string       `json:"severity"`
	Source     string       `json:"source"`
	DeposedKey string       `json:"deposed_key"`
	Timestamp  *string      `json:"timestamp"`
	Location   jsonSource   `json:"location"`
	Summary    *jsonSummary `json:"summary"`
}
type jsonSummary struct {
	Operation        string  `json:"operation"`
	Add              *uint64 `json:"add"`
	Change           *uint64 `json:"change"`
	Remove           *uint64 `json:"remove"`
	Import           *uint64 `json:"import"`
	ActionInvocation *uint64 `json:"action_invocation"`
}
type jsonIncomplete struct {
	Start        jsonEvent  `json:"start"`
	LastProgress *jsonEvent `json:"last_progress"`
	Ambiguous    bool       `json:"ambiguous"`
}
type jsonDiagnostic struct {
	Severity    string      `json:"severity"`
	Address     string      `json:"address"`
	Message     string      `json:"message"`
	Source      string      `json:"source"`
	Occurrences []jsonEvent `json:"occurrences"`
}
type jsonMilestone struct {
	Label string    `json:"label"`
	Event jsonEvent `json:"event"`
}

func RenderJSON(w io.Writer, report Report) error {
	d := jsonReport{SchemaVersion: 1, Kind: "investigation", ToolVersion: report.Metadata.ToolVersion, Input: jsonInput{report.Metadata.InputBasename}, Scope: jsonScope{report.Scope.Panel, report.Scope.Query, report.Scope.Address, string(report.Scope.Kind), report.Scope.Severity, sourceJSON(report.Scope.SelectedSource)}, TimingScope: report.TimingScope, EventScope: report.EventScope, Timings: make([]jsonTiming, 0, len(report.Timings)), Events: make([]jsonEvent, 0, len(report.Events)), Incomplete: make([]jsonIncomplete, 0, len(report.Incomplete)), Diagnostics: make([]jsonDiagnostic, 0, len(report.Diagnostics)), Milestones: make([]jsonMilestone, 0, len(report.Milestones))}
	for _, row := range report.Timings {
		d.Timings = append(d.Timings, timingJSON(row))
	}
	for _, event := range report.Events {
		d.Events = append(d.Events, eventJSON(event))
	}
	for _, operation := range report.Incomplete {
		row := jsonIncomplete{Start: eventJSON(operation.Start), Ambiguous: operation.Ambiguous}
		if operation.LastProgress != nil {
			value := eventJSON(*operation.LastProgress)
			row.LastProgress = &value
		}
		d.Incomplete = append(d.Incomplete, row)
	}
	for _, group := range report.Diagnostics {
		row := jsonDiagnostic{Severity: group.Severity, Address: group.Address, Message: group.Message, Source: group.Source, Occurrences: make([]jsonEvent, 0, len(group.Members))}
		for _, event := range group.Members {
			row.Occurrences = append(row.Occurrences, eventJSON(event))
		}
		d.Diagnostics = append(d.Diagnostics, row)
	}
	for _, milestone := range report.Milestones {
		d.Milestones = append(d.Milestones, jsonMilestone{milestone.Label, eventJSON(milestone.Event)})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(d)
}

func sourceJSON(source *model.SourceLocation) *jsonSource {
	if source == nil {
		return nil
	}
	return &jsonSource{source.Entry, source.StartLine, source.EndLine, source.StartByte, source.EndByte}
}
func eventJSON(event model.ResourceEvent) jsonEvent {
	var timestamp *string
	if !event.Timestamp.IsZero() {
		value := event.Timestamp.Format(time.RFC3339Nano)
		timestamp = &value
	}
	var summary *jsonSummary
	if event.Summary != nil {
		summary = &jsonSummary{event.Summary.Operation, event.Summary.Add, event.Summary.Change, event.Summary.Remove, event.Summary.Import, event.Summary.ActionInvocation}
	}
	location := sourceJSON(&event.Location)
	return jsonEvent{string(event.Kind), event.Address, event.Action, event.Message, event.Severity, event.Source, event.DeposedKey, timestamp, *location, summary}
}
func timingJSON(row Timing) jsonTiming {
	var clock *string
	if row.ClockOrigin != nil {
		value := row.ClockOrigin.Format(time.RFC3339Nano)
		clock = &value
	}
	return jsonTiming{row.Tier, row.Address, row.Action, row.Source, row.Qualification, row.DurationMs, row.LowerBound, clock, sourceJSON(row.Location)}
}
