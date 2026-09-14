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
