package investigation

import (
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func RenderMarkdown(w io.Writer, report Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Terraform investigation: %s\n\n", markdown(report.Metadata.InputBasename))
	fmt.Fprintf(&b, "- panel: %s\n- timing scope: %s\n- event scope: %s\n", markdown(value(report.Scope.Panel)), markdown(value(report.TimingScope)), markdown(value(report.EventScope)))
	if report.Scope.Query != "" {
		fmt.Fprintf(&b, "- query: %s\n", markdown(report.Scope.Query))
	}
	if report.Scope.Address != "" {
		fmt.Fprintf(&b, "- address: %s\n", markdown(report.Scope.Address))
	}
	if report.Scope.Kind != "" {
		fmt.Fprintf(&b, "- kind: %s\n", markdown(string(report.Scope.Kind)))
	}
	if report.Scope.Severity != "" {
		fmt.Fprintf(&b, "- severity: %s\n", markdown(report.Scope.Severity))
	}
	if report.Scope.SelectedSource != nil {
		fmt.Fprintf(&b, "- selected source: %s\n", source(*report.Scope.SelectedSource))
	}
	b.WriteString("\n## Timings\n\n")
	if len(report.Timings) == 0 {
		b.WriteString("No timing evidence in this scope.\n")
	}
	for _, row := range report.Timings {
		clock := "unavailable"
		if row.ClockOrigin != nil {
			clock = row.ClockOrigin.Format(time.RFC3339Nano)
		}
		location := "source unavailable"
		if row.Location != nil {
			location = source(*row.Location)
		}
		fmt.Fprintf(&b, "- %s %s %s: %d ms; %s; duration source: %s; %s; clock origin: %s\n", markdown(row.Tier), markdown(value(row.Address)), markdown(value(row.Action)), row.DurationMs, markdown(row.Qualification), markdown(evidenceValue(row.Source)), location, clock)
	}
	b.WriteString("\n## Events and outcomes\n\n")
	if len(report.Events) == 0 {
		b.WriteString("No event evidence in this scope.\n")
	}
	for _, event := range report.Events {
		clock := "unavailable"
		if !event.Timestamp.IsZero() {
			clock = event.Timestamp.Format(time.RFC3339Nano)
		}
		fmt.Fprintf(&b, "- %s; %s; timestamp: %s; %s", markdown(string(event.Kind)), source(event.Location), clock, eventIdentity(event))
		if event.Summary != nil {
			fmt.Fprintf(&b, "; add: %s; change: %s; remove: %s; import: %s; action invocation: %s", count(event.Summary.Add), count(event.Summary.Change), count(event.Summary.Remove), count(event.Summary.Import), count(event.Summary.ActionInvocation))
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n## Incomplete operations\n\n%d selected incomplete operation(s). Missing completion evidence does not prove a hang.\n", len(report.Incomplete))
	for _, operation := range report.Incomplete {
		fmt.Fprintf(&b, "- start: %s; %s", source(operation.Start.Location), eventIdentity(operation.Start))
		if operation.Ambiguous {
			b.WriteString("; ambiguous repeated starts")
		}
		b.WriteByte('\n')
		if operation.LastProgress != nil {
			fmt.Fprintf(&b, "  last progress: %s; %s\n", source(operation.LastProgress.Location), eventIdentity(*operation.LastProgress))
		} else {
			b.WriteString("  last progress: unavailable\n")
		}
	}
	fmt.Fprintf(&b, "\n## Diagnostics\n\n%d selected diagnostic group(s).\n", len(report.Diagnostics))
	for _, group := range report.Diagnostics {
		fmt.Fprintf(&b, "- %s %s: %s\n", markdown(group.Severity), markdown(group.Address), markdown(group.Message))
		for _, occurrence := range group.Members {
			fmt.Fprintf(&b, "  occurrence: %s\n", source(occurrence.Location))
		}
	}
	b.WriteString("\n## Observed milestones\n\nObserved activity can overlap; these are representative source observations, not inferred phases.\n")
	for _, milestone := range report.Milestones {
		fmt.Fprintf(&b, "- %s: %s\n", markdown(milestone.Label), source(milestone.Event.Location))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func source(location model.SourceLocation) string {
	return fmt.Sprintf("lines %d-%d (bytes %d-%d, entry %d)", location.StartLine, location.EndLine, location.StartByte, location.EndByte, location.Entry)
}
func count(value *uint64) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprint(*value)
}
func value(text string) string {
	if text == "" {
		return "whole capture"
	}
	return text
}
func evidenceValue(text string) string {
	if text == "" {
		return "unavailable"
	}
	return text
}
func eventIdentity(event model.ResourceEvent) string {
	return fmt.Sprintf("address: %s; action: %s; source: %s; severity: %s; deposed key: %s; message: %s",
		markdown(evidenceValue(event.Address)), markdown(evidenceValue(event.Action)), markdown(evidenceValue(event.Source)),
		markdown(evidenceValue(event.Severity)), markdown(evidenceValue(event.DeposedKey)), markdown(event.Message))
}
func markdown(text string) string {
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "#", "\\#", "!", "\\!")
	return html.EscapeString(r.Replace(logfmt.DisplayText(text)))
}
