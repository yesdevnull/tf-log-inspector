package profile

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestJSONResourceDurationProvenanceAndBreakdowns(t *testing.T) {
	r := Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"},
		UI: []Observation{
			{Index: 0, Span: span.Span{Entry: 3, Address: "resource.a", ResourceType: "resource", RPC: "read", DurationSource: span.SourceCLIElapsed, DurationMs: 2000, TimestampStatus: logfmt.TimestampMissing}, Source: &model.SourceLocation{Entry: 3, StartLine: 4, EndLine: 4, StartByte: 100, EndByte: 150}},
			{Index: 1, Span: span.Span{Entry: 2, Address: "resource.a", ResourceType: "resource", RPC: "read", DurationSource: span.SourceRefreshWindow, DurationMs: 1250, StartMs: 0, EndMs: 1250, TimestampStatus: logfmt.TimestampValid}, Source: &model.SourceLocation{Entry: 2, StartLine: 1, EndLine: 3, StartByte: 0, EndByte: 100}},
			{Index: 2, Span: span.Span{Address: "resource.b", ResourceType: "other", DurationMs: 0}},
		},
		Types:     []TypeSummary{{TypeRow: model.TypeRow{ResourceType: "resource"}}},
		Resources: model.ResourceProjection{Rows: []model.ResourceRow{{Address: "resource.a"}}},
	}
	var out bytes.Buffer
	if err := RenderJSON(&out, r, JSONMetadata{}); err != nil {
		t.Fatal(err)
	}
	doc := decodeJSONObject(t, out.Bytes())
	assertJSONLiteral(t, doc, "schema_version", "1")
	observations := decodeJSONArray(t, doc["ui_observations"])
	assertJSONLiteral(t, observations[0], "duration_source", `"cli_elapsed"`)
	assertJSONLiteral(t, observations[1], "duration_source", `"refresh_window"`)
	assertJSONLiteral(t, observations[2], "duration_source", `"ui_elapsed"`)
	position := decodeJSONObject(t, observations[0]["position"])
	assertJSONLiteral(t, position, "valid", "false")
	assertJSONNull(t, "CLI start", position["start_ms"])
	assertJSONNull(t, "CLI end", position["end_ms"])
	source := decodeJSONObject(t, observations[1]["source"])
	assertJSONLiteral(t, source, "start_line", "1")
	assertJSONLiteral(t, source, "end_line", "3")
	quality := decodeJSONObject(t, doc["quality"])
	aggregates := decodeJSONObject(t, doc["aggregates"])
	for _, scope := range []map[string]json.RawMessage{quality, aggregates} {
		summaries := decodeJSONArray(t, scope["duration_sources"])
		if len(summaries) != 3 {
			t.Fatalf("capture sources = %s", scope["duration_sources"])
		}
		assertJSONLiteral(t, summaries[0], "duration_source", `"ui_elapsed"`)
		assertJSONLiteral(t, summaries[0], "count", "1")
		assertJSONLiteral(t, summaries[0], "duration_ms", "0")
	}
	for _, scope := range []map[string]json.RawMessage{decodeJSONArray(t, aggregates["resources"])[0], decodeJSONArray(t, aggregates["resource_types"])[0]} {
		summaries := decodeJSONArray(t, scope["duration_sources"])
		if len(summaries) != 2 {
			t.Fatalf("group sources = %s", scope["duration_sources"])
		}
		for i, want := range []struct{ source, ms string }{{`"refresh_window"`, "1250"}, {`"cli_elapsed"`, "2000"}} {
			assertJSONKeys(t, "duration source", summaries[i], "duration_source", "count", "duration_ms", "max_ms", "duration_lower_bound")
			assertJSONLiteral(t, summaries[i], "duration_source", want.source)
			assertJSONLiteral(t, summaries[i], "count", "1")
			assertJSONLiteral(t, summaries[i], "duration_ms", want.ms)
			assertJSONLiteral(t, summaries[i], "max_ms", want.ms)
			assertJSONLiteral(t, summaries[i], "duration_lower_bound", "false")
		}
	}
}

func TestComparisonExportsUnavailableSourceChanges(t *testing.T) {
	before := Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}, UI: []Observation{{Span: span.Span{Address: "resource.a", ResourceType: "resource", RPC: "read", DurationMs: 1000}}}}
	after := Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}, UI: []Observation{{Span: span.Span{Address: "resource.a", ResourceType: "resource", RPC: "read", DurationMs: 25, DurationSource: span.SourceRefreshWindow}}}}
	r, err := BuildComparison(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RenderComparisonJSON(&out, r, ComparisonMetadata{}); err != nil {
		t.Fatal(err)
	}
	doc := decodeJSONObject(t, out.Bytes())
	assertJSONLiteral(t, doc, "schema_version", "1")
	for _, section := range decodeJSONArray(t, doc["sections"])[3:] {
		rows := decodeJSONArray(t, section["rows"])
		if len(rows) != 2 {
			t.Fatalf("source rows = %s", section["rows"])
		}
		for i, want := range []string{`"ui_elapsed"`, `"refresh_window"`} {
			assertJSONLiteral(t, decodeJSONObject(t, rows[i]["key"]), "duration_source", want)
			assertJSONLiteral(t, rows[i], "state", `"unavailable"`)
			for name, value := range decodeJSONObject(t, rows[i]["changes"]) {
				assertJSONNull(t, name, value)
			}
		}
		assertJSONNull(t, "reported after", rows[0]["after"])
		assertJSONNull(t, "refresh before", rows[1]["before"])
	}
	for _, side := range []string{"before", "after"} {
		quality := decodeJSONObject(t, decodeJSONObject(t, doc[side])["quality"])
		if len(decodeJSONArray(t, quality["duration_sources"])) != 1 {
			t.Fatalf("%s sources = %s", side, quality["duration_sources"])
		}
	}
	out.Reset()
	if err := RenderComparisonText(&out, r, ComparisonMetadata{}, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"duration_source=ui_elapsed", "duration_source=refresh_window", "source absent from a capture is unavailable", "refresh_window measures a hook window", "cli_elapsed retains displayed resolution"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("comparison missing %q", want)
		}
	}
	if strings.Contains(out.String(), "EXACT TOTAL-DURATION CHANGES") {
		t.Fatal("incompatible sources ranked as deltas")
	}
}
