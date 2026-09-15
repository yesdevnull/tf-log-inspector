package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func eventRecord(e model.ResourceEvent, index int) qualityRecord {
	clock := "clock unavailable"
	if !e.Timestamp.IsZero() {
		clock = e.Timestamp.Format(time.RFC3339Nano)
	}
	text := fmt.Sprintf("%s · %s · %s · %s · %s · line %d", logfmt.DisplayText(string(e.Kind)), logfmt.DisplayText(e.Address), logfmt.DisplayText(e.Action), logfmt.DisplayText(e.Source), clock, e.Location.StartLine)
	if e.DeposedKey != "" {
		text += "\ndeposed object: " + logfmt.DisplayText(e.DeposedKey)
	}
	if e.Severity != "" {
		text += "\nseverity: " + logfmt.DisplayText(e.Severity)
	}
	if e.Message != "" {
		text += "\n" + logfmt.DisplayText(e.Message)
	}
	return qualityRecord{id: qualityItemID{kind: "event", index: index}, text: text, sourceLine: e.Location.StartLine}
}

func eventRecords(l *model.Log, address string) []qualityRecord {
	var records []qualityRecord
	for i, e := range l.Events {
		if address == "" || e.Address == address {
			records = append(records, eventRecord(e, i))
		}
	}
	return records
}

func searchableEventRecords(events []model.ResourceEvent, filter model.EventFilter, expanded map[string]bool) []qualityRecord {
	groups := model.GroupProgress(events)
	groupAt := make(map[int]int)
	for groupIndex, group := range groups {
		for _, eventIndex := range group.Indices {
			groupAt[eventIndex] = groupIndex
		}
	}
	seen := make(map[int]bool)
	var records []qualityRecord
	for index, event := range events {
		if groupIndex, grouped := groupAt[index]; grouped {
			if seen[groupIndex] {
				continue
			}
			seen[groupIndex] = true
			group := groups[groupIndex]
			members := model.FilterEvents(group.Members, filter)
			if len(members) == 0 {
				continue
			}
			key := fmt.Sprintf("progress-group:%d", groupIndex)
			latest := members[len(members)-1]
			text := fmt.Sprintf("progress · %d progress observations · %s · %s · lines %d–%d\nlatest: %s", len(members), logfmt.DisplayText(group.Address), logfmt.DisplayText(group.Action), members[0].Location.StartLine, latest.Location.StartLine, logfmt.DisplayText(latest.Message))
			records = append(records, qualityRecord{id: qualityItemID{kind: key}, text: text, sourceLine: latest.Location.StartLine})
			if expanded[key] {
				for _, member := range members {
					record := eventRecord(member, index)
					record.id.kind = "progress-member"
					records = append(records, record)
				}
			}
			continue
		}
		if event.Kind == model.EventProgress || len(model.FilterEvents([]model.ResourceEvent{event}, filter)) == 0 {
			continue
		}
		records = append(records, eventRecord(event, index))
	}
	return records
}

func diagnosticGroupRecords(events []model.ResourceEvent, filter model.EventFilter, expanded map[string]bool) []qualityRecord {
	var records []qualityRecord
	for index, group := range model.GroupDiagnostics(events) {
		members := model.FilterEvents(group.Members, filter)
		if len(members) == 0 {
			continue
		}
		key := fmt.Sprintf("diagnostic-group:%d", index)
		text := fmt.Sprintf("%s · %s · %d occurrences\n%s", logfmt.DisplayText(group.Severity), logfmt.DisplayText(group.Address), len(members), logfmt.DisplayText(group.Message))
		records = append(records, qualityRecord{id: qualityItemID{kind: key}, text: text, sourceLine: members[0].Location.StartLine})
		if expanded[key] {
			for _, member := range members {
				records = append(records, qualityRecord{id: qualityItemID{kind: "diagnostic-occurrence"}, text: fmt.Sprintf("occurrence at line %d · %s", member.Location.StartLine, logfmt.DisplayText(member.Source)), sourceLine: member.Location.StartLine})
			}
		}
	}
	return records
}

func milestoneRecords(events []model.ResourceEvent, filter model.EventFilter) []qualityRecord {
	records := []qualityRecord{{text: "Observed activity can overlap; milestones do not infer sequential phases."}}
	for index, milestone := range model.InvestigationMilestones(events) {
		if len(model.FilterEvents([]model.ResourceEvent{milestone.Event}, filter)) == 0 {
			continue
		}
		record := eventRecord(milestone.Event, index)
		record.text = logfmt.DisplayText(milestone.Label) + "\n" + record.text
		records = append(records, record)
	}
	return records
}

func filteredOutcomeRecords(l *model.Log, filter model.EventFilter) []qualityRecord {
	var records []qualityRecord
	for _, section := range []struct {
		title string
		kind  model.EventKind
	}{
		{"SUMMARIES", model.EventChangeSummary}, {"DRIFT", model.EventDrift},
		{"PLANNED CHANGES", model.EventPlannedChange}, {"DIAGNOSTICS", model.EventDiagnostic},
	} {
		sectionFilter := filter
		if sectionFilter.Kind != "" && sectionFilter.Kind != section.kind {
			continue
		}
		sectionFilter.Kind = section.kind
		events := model.FilterEvents(l.Events, sectionFilter)
		if len(events) == 0 {
			continue
		}
		records = append(records, qualityRecord{text: section.title})
		for _, event := range events {
			record := eventRecord(event, len(records))
			if event.Summary != nil {
				record.text += "\n" + summaryText(*event.Summary)
			}
			records = append(records, record)
		}
	}
	return records
}

func filteredIncompleteRecords(l *model.Log, filter model.EventFilter) []qualityRecord {
	records := []qualityRecord{{text: "Starts with no unambiguous ending observed in this capture."}}
	for _, operation := range l.Incomplete {
		for _, event := range []model.ResourceEvent{operation.Start} {
			if len(model.FilterEvents([]model.ResourceEvent{event}, filter)) != 0 {
				record := eventRecord(event, len(records))
				if operation.Ambiguous {
					record.text += "\nambiguous: repeated starts; later events cannot be assigned safely"
				}
				records = append(records, record)
			}
		}
		if operation.LastProgress != nil && len(model.FilterEvents([]model.ResourceEvent{*operation.LastProgress}, filter)) != 0 {
			records = append(records, eventRecord(*operation.LastProgress, len(records)))
		}
	}
	return records
}

func eventPanelEvidenceCounts(l *model.Log, mode string, filter model.EventFilter) (int, int) {
	events := l.Events
	switch mode {
	case "p":
		var outcomes []model.ResourceEvent
		for _, event := range events {
			if event.Kind == model.EventPlannedChange || event.Kind == model.EventDrift || event.Kind == model.EventDiagnostic || event.Kind == model.EventChangeSummary {
				outcomes = append(outcomes, event)
			}
		}
		events = outcomes
	case "u":
		events = nil
		for _, operation := range l.Incomplete {
			events = append(events, operation.Start)
			if operation.LastProgress != nil {
				events = append(events, *operation.LastProgress)
			}
		}
	case "d":
		var diagnostics []model.ResourceEvent
		for _, event := range events {
			if event.Kind == model.EventDiagnostic {
				diagnostics = append(diagnostics, event)
			}
		}
		events = diagnostics
	case "M":
		events = nil
		for _, milestone := range model.InvestigationMilestones(l.Events) {
			events = append(events, milestone.Event)
		}
	}
	base := model.EventFilter{Address: filter.Address}
	return len(model.FilterEvents(events, filter)), len(model.FilterEvents(events, base))
}

func outcomeRecords(l *model.Log) []qualityRecord {
	var records []qualityRecord
	for _, section := range []struct {
		title  string
		events []model.ResourceEvent
	}{
		{"SUMMARIES", l.Outcomes.Summaries}, {"DRIFT", l.Outcomes.Drift},
		{"PLANNED CHANGES", l.Outcomes.PlannedChanges}, {"DIAGNOSTICS", l.Outcomes.Diagnostics},
	} {
		records = append(records, qualityRecord{text: section.title})
		if len(section.events) == 0 {
			records = append(records, qualityRecord{text: "unavailable: no evidence observed"})
		}
		for _, e := range section.events {
			record := eventRecord(e, len(records))
			if e.Summary != nil {
				record.text += "\n" + summaryText(*e.Summary)
			}
			records = append(records, record)
		}
	}
	return records
}

func summaryText(s model.ChangeSummary) string {
	fields := []string{"operation: " + logfmt.DisplayText(s.Operation)}
	for _, f := range []struct {
		name  string
		count *uint64
	}{
		{"add", s.Add}, {"change", s.Change}, {"remove", s.Remove}, {"import", s.Import}, {"action invocation", s.ActionInvocation},
	} {
		value := "unavailable"
		if f.count != nil {
			value = fmt.Sprint(*f.count)
		}
		fields = append(fields, f.name+": "+value)
	}
	return strings.Join(fields, "; ")
}

func incompleteRecords(l *model.Log) []qualityRecord {
	records := []qualityRecord{{text: "Starts with no unambiguous ending observed in this capture."}}
	if len(l.Incomplete) == 0 {
		return append(records, qualityRecord{text: "No incomplete operations observed; capture completeness is unknown."})
	}
	for _, op := range l.Incomplete {
		record := eventRecord(op.Start, len(records))
		if op.Ambiguous {
			record.text += "\nambiguous: repeated starts; later events cannot be assigned safely"
		}
		records = append(records, record)
		if op.LastProgress != nil {
			record = eventRecord(*op.LastProgress, len(records))
			record.text = "last progress: " + record.text
			records = append(records, record)
		} else {
			records = append(records, qualityRecord{text: "last progress: unavailable"})
		}
	}
	return records
}
