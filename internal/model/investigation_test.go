package model

import (
	"reflect"
	"testing"
)

func TestInvestigationFilterCombinesCriteriaAndSearchesLiteralFields(t *testing.T) {
	events := []ResourceEvent{
		{Kind: EventProgress, Address: "aws_instance.Alpha", Action: "create", Message: "Still creating... [10s elapsed]", Severity: "INFO", Source: "CLI"},
		{Kind: EventDiagnostic, Address: "aws_instance.Alpha", Message: "Could not READ value", Severity: "warning", Source: "ui"},
		{Kind: EventDiagnostic, Address: "aws_instance.beta", Message: "Could not read value", Severity: "warning", Source: "ui"},
	}

	got := FilterEvents(events, EventFilter{Query: "read", Address: "aws_instance.Alpha", Kind: EventDiagnostic, Severity: "WARNING"})
	if !reflect.DeepEqual(got, events[1:2]) {
		t.Fatalf("FilterEvents() = %#v, want %#v", got, events[1:2])
	}
	if got := FilterEvents(events, EventFilter{Query: "progress"}); !reflect.DeepEqual(got, events[:1]) {
		t.Fatalf("kind query = %#v, want %#v", got, events[:1])
	}
}

func TestProgressGroupsDoNotCrossOperationBoundaries(t *testing.T) {
	events := []ResourceEvent{
		eventAt(EventStart, "aws_instance.a", "Creating...", 1),
		eventAt(EventProgress, "aws_instance.a", "Still creating... [10s elapsed]", 2),
		eventAt(EventProgress, "aws_instance.b", "Still creating... [10s elapsed]", 3),
		eventAt(EventProgress, "aws_instance.a", "Still creating... [20s elapsed]", 4),
		eventAt(EventComplete, "aws_instance.a", "Creation complete after 21s", 5),
		eventAt(EventStart, "aws_instance.a", "Creating...", 6),
		eventAt(EventProgress, "aws_instance.a", "Still creating... [30s elapsed]", 7),
	}

	got := GroupProgress(events)
	if len(got) != 3 || len(got[0].Members) != 2 || len(got[1].Members) != 1 || len(got[2].Members) != 1 {
		t.Fatalf("GroupProgress() = %#v", got)
	}
	if got[0].First.Location.StartLine != 2 || got[0].Last.Location.StartLine != 4 || got[0].LastElapsed != "20s elapsed" {
		t.Fatalf("first group evidence = %#v", got[0])
	}
	if got[2].First.Location.StartLine != 7 || got[2].Last.Location.StartLine != 7 || got[2].LastElapsed != "30s elapsed" {
		t.Fatalf("second operation group = %#v", got[2])
	}
	filtered := FilterProgressGroups(got, EventFilter{Query: "10s elapsed", Address: "aws_instance.a"})
	if len(filtered) != 1 || !reflect.DeepEqual(filtered[0].Members, []ResourceEvent{events[1], events[3]}) {
		t.Fatalf("hidden-member filter = %#v", filtered)
	}
}

func TestProgressGroupsLeaveAmbiguousOperationsUngrouped(t *testing.T) {
	repeated := []ResourceEvent{
		eventAt(EventStart, "aws_instance.a", "Creating...", 1),
		eventAt(EventStart, "aws_instance.a", "Creating...", 2),
		eventAt(EventProgress, "aws_instance.a", "Still creating... [10s elapsed]", 3),
		eventAt(EventProgress, "aws_instance.a", "Still creating... [20s elapsed]", 4),
	}
	if got := GroupProgress(repeated); len(got) != 2 || len(got[0].Members) != 1 || len(got[1].Members) != 1 {
		t.Fatalf("repeated starts grouped = %#v", got)
	}

	deposed := []ResourceEvent{
		{Kind: EventStart, Address: "aws_instance.a", Action: "create", Source: "cli", DeposedKey: "deadbeef"},
		{Kind: EventProgress, Address: "aws_instance.a", Action: "create", Source: "cli", Message: "Still creating... [10s elapsed]"},
		{Kind: EventProgress, Address: "aws_instance.a", Action: "create", Source: "cli", Message: "Still creating... [20s elapsed]"},
	}
	if got := GroupProgress(deposed); len(got) != 2 || len(got[0].Members) != 1 || len(got[1].Members) != 1 {
		t.Fatalf("deposed ambiguity grouped = %#v", got)
	}
}

func TestDiagnosticGroupsRetainEveryExactOccurrence(t *testing.T) {
	events := []ResourceEvent{
		{Kind: EventDiagnostic, Severity: "warning", Address: "aws_instance.a", Message: "same", Source: "cli", Location: SourceLocation{StartLine: 2}},
		{Kind: EventDiagnostic, Severity: "warning", Address: "aws_instance.a", Message: "same", Source: "cli", Location: SourceLocation{StartLine: 9}},
		{Kind: EventDiagnostic, Severity: "error", Address: "aws_instance.a", Message: "same", Source: "cli", Location: SourceLocation{StartLine: 12}},
	}
	got := GroupDiagnostics(events)
	if len(got) != 2 || len(got[0].Members) != 2 || got[0].Members[1].Location.StartLine != 9 || len(got[1].Members) != 1 {
		t.Fatalf("GroupDiagnostics() = %#v", got)
	}
}

func TestMilestonesRetainEvidenceOrderAndEverySummary(t *testing.T) {
	events := []ResourceEvent{
		eventAt(EventStart, "aws_instance.a", "create", 1),
		{Kind: EventStart, Address: "data.aws_ami.a", Action: "read", Location: SourceLocation{StartLine: 2}},
		{Kind: EventDrift, Address: "aws_instance.a", Location: SourceLocation{StartLine: 3}},
		{Kind: EventPlannedChange, Address: "aws_instance.a", Location: SourceLocation{StartLine: 4}},
		{Kind: EventDiagnostic, Severity: "warning", Location: SourceLocation{StartLine: 5}},
		{Kind: EventDrift, Address: "aws_instance.b", Location: SourceLocation{StartLine: 6}},
		{Kind: EventPlannedChange, Address: "aws_instance.b", Location: SourceLocation{StartLine: 7}},
		{Kind: EventDiagnostic, Severity: "error", Location: SourceLocation{StartLine: 8}},
		{Kind: EventChangeSummary, Message: "first", Location: SourceLocation{StartLine: 9}},
		{Kind: EventChangeSummary, Message: "second", Location: SourceLocation{StartLine: 10}},
		{Kind: EventStart, Address: "aws_instance.b", Action: "refresh", Location: SourceLocation{StartLine: 11}},
	}
	got := InvestigationMilestones(events)
	wantLabels := []string{"First refresh/read activity", "Drift", "Planned changes", "Diagnostic", "Summary", "Summary"}
	wantLines := []uint64{2, 3, 4, 5, 9, 10}
	if len(got) != len(wantLabels) {
		t.Fatalf("milestones = %#v", got)
	}
	for i, want := range wantLabels {
		if got[i].Label != want || got[i].Event.Location.StartLine != wantLines[i] {
			t.Errorf("milestone %d = %#v, want label %q on line %d", i, got[i], want, wantLines[i])
		}
	}
}

func eventAt(kind EventKind, address, message string, line uint64) ResourceEvent {
	return ResourceEvent{Kind: kind, Address: address, Action: "create", Message: message, Source: "cli", Location: SourceLocation{StartLine: line}}
}
