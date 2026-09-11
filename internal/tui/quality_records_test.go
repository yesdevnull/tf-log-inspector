package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestQualityDiagnosticPrimarySource(t *testing.T) {
	for _, tc := range []struct {
		d    logfmt.ProviderJSONDiagnostic
		want uint64
	}{
		{logfmt.ProviderJSONDiagnostic{SyntaxLine: 9, Line: 7, StartLine: 2}, 9},
		{logfmt.ProviderJSONDiagnostic{Line: 7, StartLine: 2}, 7},
		{logfmt.ProviderJSONDiagnostic{StartLine: 2}, 2},
		{logfmt.ProviderJSONDiagnostic{}, 0},
	} {
		if got := diagnosticSourceLine(tc.d); got != tc.want {
			t.Fatalf("primary coordinate = %d, want %d", got, tc.want)
		}
	}
}

func TestWrapQualityRecordsClampsPrefixAtTinyWidths(t *testing.T) {
	for _, width := range []int{0, 1, 2} {
		lines, actions := wrapQualityRecords([]qualityRecord{{id: qualityItemID{kind: "check"}, text: "check"}}, width)
		if len(lines) == 0 || len(actions) != 1 {
			t.Fatalf("width %d lost record/action", width)
		}
		for _, line := range lines {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d produced %d-column row %q", width, got, line)
			}
		}
	}
}

func TestQualityRecordsKeepDiagnosticIdentityOrderAndContentPrivate(t *testing.T) {
	m := New(&model.Log{}, "capture.log")
	m.log.InspectProviderResponses()
	diagnostics := []logfmt.ProviderJSONDiagnostic{
		{Code: "unlocated\x1b[2J", StartLine: 0},
		{Code: "later", Line: 8, StartLine: 3},
		{Code: "syntax", SyntaxLine: 4, Line: 2, StartLine: 1},
		{Code: "tie", Line: 8},
	}
	records := diagnosticQualityRecords(diagnostics)
	if len(records) != 4 {
		t.Fatalf("diagnostic records = %d", len(records))
	}
	for i, want := range []int{2, 1, 3, 0} {
		if records[i].id.index != want {
			t.Fatalf("record %d original index = %d, want %d", i, records[i].id.index, want)
		}
	}
	if strings.Contains(records[0].text, "line 2") || strings.Contains(records[0].text, "line 1") {
		t.Fatalf("syntax diagnostic exposed competing coordinate: %q", records[0].text)
	}
	if !strings.Contains(records[1].text, "source line 8") || !strings.Contains(records[1].text, "response starts at line 3") {
		t.Fatalf("distinct start coordinate missing: %q", records[1].text)
	}
	if strings.Contains(records[3].text, "\x1b") || !strings.Contains(records[3].text, `\x1b`) || !strings.Contains(records[3].text, "location unavailable") {
		t.Fatalf("unlocated diagnostic was unsafe or unclear: %q", records[3].text)
	}
}

func TestWrapQualityRecordsMapsWholeActionIntervals(t *testing.T) {
	records := []qualityRecord{
		{id: qualityItemID{kind: "check"}, text: "Check responses"},
		{text: "prose prose prose"},
		{id: qualityItemID{kind: "diagnostic", index: 2}, text: "diagnostic wraps", sourceLine: 9},
	}
	lines, actions := wrapQualityRecords(records, 10)
	if len(lines) < 5 || len(actions) != 2 {
		t.Fatalf("wrapped lines/actions = %d/%d", len(lines), len(actions))
	}
	if actions[0].start != 0 || actions[0].end <= actions[0].start || actions[1].start <= actions[0].end || actions[1].end <= actions[1].start {
		t.Fatalf("action intervals = %+v", actions)
	}
}
