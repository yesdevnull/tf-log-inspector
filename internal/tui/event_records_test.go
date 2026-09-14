package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func eventCapture(t *testing.T, input string) *model.Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.log")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestEventRecordsKeepExactAddressAndPhysicalOrder(t *testing.T) {
	l := eventCapture(t, "heading\naws_instance.a: Creating...\naws_instance.ab: Creating...\naws_instance.a: Still creating... [10s elapsed]\naws_instance.a: Creation complete after 12s\n")
	records := eventRecords(l, "aws_instance.a")
	if len(records) != 3 {
		t.Fatalf("records = %+v", records)
	}
	for i, line := range []uint64{2, 4, 5} {
		if records[i].sourceLine != line {
			t.Fatalf("record %d = %+v", i, records[i])
		}
	}
	for i, kind := range []string{"start", "progress", "complete"} {
		if !strings.Contains(records[i].text, kind) || !strings.Contains(records[i].text, "clock unavailable") {
			t.Fatalf("record %d = %+v", i, records[i])
		}
	}
	if len(eventRecords(l, "")) != 4 {
		t.Fatal("whole-capture history lost events")
	}
}

func TestEventRecordNamesDeposedObject(t *testing.T) {
	record := eventRecord(model.ResourceEvent{Kind: model.EventStart, Address: "aws_instance.a", DeposedKey: "old\x1b[2J"}, 0)
	if !strings.Contains(record.text, `deposed object: old\x1b[2J`) || strings.Contains(record.text, "\x1b") {
		t.Fatalf("deposed identity missing or unsafe: %s", record.text)
	}
}

func TestOutcomeRecordsSeparateEvidenceAndMissingSummaryFields(t *testing.T) {
	l := eventCapture(t, `{"@level":"info","type":"resource_drift","change":{"resource":{"addr":"aws_instance.a"},"action":"update"}}
{"@level":"info","type":"planned_change","change":{"resource":{"addr":"aws_instance.a"},"action":"delete"}}
{"@level":"info","type":"change_summary","changes":{"add":0,"operation":"plan"}}
{"@level":"warn","type":"diagnostic","diagnostic":{"summary":"unsafe\u001b[2J","detail":"detail","severity":"warning"}}
`)
	records := outcomeRecords(l)
	if records[0].text != "SUMMARIES" {
		t.Errorf("reported counts must lead outcome evidence, got %q", records[0].text)
	}
	var texts []string
	for _, r := range records {
		texts = append(texts, r.text)
	}
	text := strings.Join(texts, "\n")
	for _, want := range []string{"DRIFT", "PLANNED CHANGES", "SUMMARIES", "DIAGNOSTICS", "add: 0", "change: unavailable", "remove: unavailable", "import: unavailable", "action invocation: unavailable", `unsafe\x1b[2J`, "detail"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Fatal("unescaped log control")
	}
	empty := outcomeRecords(&model.Log{})
	if len(empty) == 0 || !strings.Contains(empty[1].text, "unavailable") {
		t.Fatalf("missing evidence: %+v", empty)
	}
}

func TestIncompleteRecordsExposeProgressAndAmbiguityWithoutDurationClaims(t *testing.T) {
	l := eventCapture(t, "aws_instance.a: Creating...\naws_instance.a: Still creating... [10s elapsed]\naws_instance.b: Creating...\naws_instance.b: Creating...\n")
	records := incompleteRecords(l)
	var texts []string
	var lines []uint64
	for _, r := range records {
		texts = append(texts, r.text)
		if r.sourceLine > 0 {
			lines = append(lines, r.sourceLine)
		}
	}
	text := strings.Join(texts, "\n")
	for _, want := range []string{"no unambiguous ending observed", "last progress", "ambiguous", "start"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if len(lines) != 4 || lines[1] != 2 {
		t.Fatalf("source actions = %v", lines)
	}
	if strings.Contains(text, "hung") || strings.Contains(text, "duration:") {
		t.Fatalf("invented claim: %s", text)
	}
}
