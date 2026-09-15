package investigation

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestReportPreservesScopeMissingValuesSourcesAndOriginalEvidence(t *testing.T) {
	zero := uint64(0)
	log := &model.Log{
		Data:    []byte("first\nsecond\nthird\n"),
		Entries: []logfmt.Entry{{Off: 0, Len: 6, Lines: 1}, {Off: 6, Len: 7, Lines: 1}, {Off: 13, Len: 6, Lines: 1}},
		UISpans: []span.Span{{Entry: 1, StartEntry: 0, HasStartEntry: true, Address: "aws_instance.example", RPC: "create", DurationMs: 0, DurationSource: span.SourceCLIElapsed}},
		Events: []model.ResourceEvent{
			{Kind: model.EventPlannedChange, Address: "aws_instance.example", Action: "create", Message: "`hostile` <b>x</b> [link](bad)\x1b[31m", Source: "cli", Location: model.SourceLocation{Entry: 0, StartByte: 0, EndByte: 6, StartLine: 1, EndLine: 1}},
			{Kind: model.EventChangeSummary, Message: "Plan: 1 to add.", Source: "cli", Location: model.SourceLocation{Entry: 1, StartByte: 6, EndByte: 13, StartLine: 2, EndLine: 2}, Summary: &model.ChangeSummary{Operation: "plan", Change: &zero}},
		},
	}
	log.Incomplete = []model.IncompleteOperation{{Start: log.Events[0]}}
	selection := Selection{Scope: Scope{Panel: "outcomes", Query: "literal", Address: "aws_instance.example", SelectedSource: &log.Events[0].Location}, UIIndices: []int{0}, EventIndices: []int{0, 1}, TimingScope: "resource and timing facets", EventScope: "event panel query and facets"}
	report := Build(log, Metadata{ToolVersion: "test", InputBasename: "capture.log"}, selection)

	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, report); err != nil {
		t.Fatal(err)
	}
	text := markdown.String()
	for _, want := range []string{"capture.log", "tool version: test", "lines 1-2", "clock origin: unavailable", "0 ms", "change: 0", "add: unavailable", "\\`hostile\\`", "&lt;b&gt;x&lt;/b&gt;", "\\[link\\]\\(bad\\)", "selected source: lines 1-1"} {
		if !strings.Contains(text, want) {
			t.Errorf("Markdown missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Fatalf("Markdown retained control sequence: %q", text)
	}

	var encoded bytes.Buffer
	if err := RenderJSON(&encoded, report); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document["schema_version"] != float64(1) || document["kind"] != "investigation" {
		t.Fatalf("JSON header = %#v", document)
	}
	events := document["events"].([]any)
	if events[0].(map[string]any)["message"] != log.Events[0].Message {
		t.Fatalf("original evidence changed: %#v", events[0])
	}
	summary := events[1].(map[string]any)["summary"].(map[string]any)
	if summary["change"] != float64(0) || summary["add"] != nil {
		t.Fatalf("missing and zero collapsed: %#v", summary)
	}
	if events[0].(map[string]any)["timestamp"] != nil {
		t.Fatalf("untimestamped evidence gained a clock: %#v", events[0])
	}
	if events[0].(map[string]any)["location"].(map[string]any)["start_line"] != float64(1) {
		t.Fatalf("source DTO is not snake_case: %#v", events[0])
	}
	timings := document["timings"].([]any)
	if timings[0].(map[string]any)["duration_ms"] != float64(0) || timings[0].(map[string]any)["clock_origin"] != nil {
		t.Fatalf("timing DTO lost zero or clock availability: %#v", timings[0])
	}
	if len(document["incomplete_operations"].([]any)) != 1 || len(document["milestones"].([]any)) != 2 {
		t.Fatalf("JSON omitted report sections: %#v", document)
	}
}

func TestMarkdownRetainsEvidenceIdentityProvenanceAndPreciseClocks(t *testing.T) {
	clockOrigin := time.Date(2026, 9, 15, 0, 0, 0, 123456789, time.UTC)
	eventTime := time.Date(2026, 9, 15, 0, 0, 0, 223456789, time.UTC)
	start := model.ResourceEvent{
		Kind: model.EventStart, Address: `module.example["quoted"].aws_instance.main`, Action: "create",
		Message: "owner's `message`", Severity: "info", Source: "ui", DeposedKey: "deadbeef", Timestamp: eventTime,
		Location: model.SourceLocation{Entry: 2, StartByte: 20, EndByte: 30, StartLine: 3, EndLine: 3},
	}
	progress := start
	progress.Kind = model.EventProgress
	progress.Location = model.SourceLocation{Entry: 3, StartByte: 30, EndByte: 40, StartLine: 4, EndLine: 4}
	report := Report{
		Metadata:   Metadata{InputBasename: "capture.log"},
		Timings:    []Timing{{Tier: "resource", Address: start.Address, Action: "create", Source: "ui_elapsed", Qualification: "observed duration", DurationMs: 100, ClockOrigin: &clockOrigin, Location: &start.Location}},
		Events:     []model.ResourceEvent{start},
		Incomplete: []model.IncompleteOperation{{Start: start, LastProgress: &progress}},
	}

	var rendered bytes.Buffer
	if err := RenderMarkdown(&rendered, report); err != nil {
		t.Fatal(err)
	}
	text := rendered.String()
	for _, want := range []string{
		"duration source: ui\\_elapsed",
		"clock origin: 2026-09-15T00:00:00.123456789Z",
		"timestamp: 2026-09-15T00:00:00.223456789Z",
		"address: module.example\\[&#34;quoted&#34;\\].aws\\_instance.main",
		"action: create", "source: ui", "severity: info", "deposed key: deadbeef",
		"owner&#39;s \\`message\\`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Markdown missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `&\#34;`) || strings.Contains(text, `&\#39;`) {
		t.Fatalf("Markdown damaged generated quote entities:\n%s", text)
	}
}

func TestRealSanitisedLogRetainsTimingOutcomeAndIncompleteEvidence(t *testing.T) {
	path := filepath.Join("testdata", "investigation.log")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	log, err := model.LoadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	report := Build(log, Metadata{InputBasename: filepath.Base(path)}, Selection{RPCIndices: makeTestIndices(len(log.RPCSpans)), UIIndices: makeTestIndices(len(log.UISpans)), EventIndices: makeTestIndices(len(log.Events))})
	if len(report.Timings) != 1 || report.Timings[0].DurationMs != 0 || len(report.Incomplete) != 1 || len(report.Diagnostics) != 1 {
		t.Fatalf("real report evidence = %#v", report)
	}
	if len(report.Milestones) != 3 {
		t.Fatalf("milestones = %#v", report.Milestones)
	}
	var encoded bytes.Buffer
	if err := RenderJSON(&encoded, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded.String(), `"change": 0`) || !strings.Contains(encoded.String(), `"add": null`) || !strings.Contains(encoded.String(), `"timestamp": null`) || !strings.Contains(encoded.String(), "2026-09-15T00:00:00.123456789Z") {
		t.Fatalf("JSON lost evidence semantics:\n%s", encoded.String())
	}
}

func makeTestIndices(length int) []int {
	values := make([]int, length)
	for i := range values {
		values[i] = i
	}
	return values
}

func TestRenderersPropagateWriterFailures(t *testing.T) {
	report := Report{Metadata: Metadata{InputBasename: "x.log"}}
	for name, render := range map[string]func() error{
		"markdown": func() error { return RenderMarkdown(failingWriter{}, report) },
		"json":     func() error { return RenderJSON(failingWriter{}, report) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := render(); err == nil {
				t.Fatal("writer failure ignored")
			}
		})
	}
}

func TestBuildDoesNotRelabelFilteredEventsAsCaptureFirstMilestones(t *testing.T) {
	log := &model.Log{Events: []model.ResourceEvent{
		{Kind: model.EventPlannedChange, Address: "first", Location: model.SourceLocation{StartLine: 1}},
		{Kind: model.EventPlannedChange, Address: "selected", Location: model.SourceLocation{StartLine: 2}},
		{Kind: model.EventStart, Address: "selected", Location: model.SourceLocation{StartLine: 3}},
		{Kind: model.EventProgress, Address: "selected", Location: model.SourceLocation{StartLine: 4}},
	}}
	log.Incomplete = []model.IncompleteOperation{{Start: log.Events[2], LastProgress: &log.Events[3]}}
	report := Build(log, Metadata{}, Selection{EventIndices: []int{1, 3}})
	if len(report.Milestones) != 0 {
		t.Fatalf("filtered event relabelled capture-first milestone: %#v", report.Milestones)
	}
	if len(report.Incomplete) != 1 || report.Incomplete[0].Start.Location.StartLine != 3 {
		t.Fatalf("matching progress lost operation context: %#v", report.Incomplete)
	}
	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "start: lines 3-") || !strings.Contains(markdown.String(), "last progress: lines 4-") {
		t.Fatalf("Markdown lost expanded operation evidence:\n%s", markdown.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

var errWrite = &writeError{}

type writeError struct{}

func (*writeError) Error() string { return "write failed" }
