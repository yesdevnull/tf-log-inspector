package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestComparisonJSONCompleteContract(t *testing.T) {
	before := comparisonReportFixture(t, "provider-rpc.log")
	after := comparisonReportFixture(t, "structured-ui.log")
	report, err := BuildComparison(before, after)
	if err != nil {
		t.Fatal(err)
	}
	original := deepCopyComparisonReport(report)
	metadata := ComparisonMetadata{ToolVersion: "test", BeforeBasename: "before.log", AfterBasename: "after.log"}
	var out bytes.Buffer
	if err := RenderComparisonJSON(&out, report, metadata); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(out.Bytes(), []byte("}\n")) || bytes.HasSuffix(out.Bytes(), []byte("}\n\n")) {
		t.Fatalf("ending: %q", out.Bytes()[len(out.Bytes())-4:])
	}
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.UseNumber()
	var root map[string]json.RawMessage
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("second decode: %v", err)
	}
	assertJSONKeys(t, "root", root, "schema_version", "kind", "tool_version", "duration_unit", "before", "after", "comparability", "sections", "qualifications")
	assertJSONLiteral(t, root, "schema_version", "1")
	assertJSONLiteral(t, root, "kind", `"comparison"`)
	for side, wantName := range map[string]string{"before": "before.log", "after": "after.log"} {
		capture := decodeJSONObject(t, root[side])
		assertComparisonCaptureKeys(t, side, capture)
		input := decodeJSONObject(t, capture["input"])
		assertJSONKeys(t, side+" input", input, "basename", "bytes")
		assertJSONLiteral(t, input, "basename", `"`+wantName+`"`)
		assertJSONKeys(t, side+" tiers", decodeJSONObject(t, capture["tiers"]), "rpc", "ui")
		assertJSONKeys(t, side+" quality", decodeJSONObject(t, capture["quality"]), "scope", "provider_entries", "structured_lines", "has_address_context", "issues", "attribution", "nameable_ms", "rpc_duration_ms", "nameable_share", "reconstruction", "duration_sources")
		reconstruction := decodeJSONObject(t, decodeJSONObject(t, capture["quality"])["reconstruction"])
		assertJSONKeys(t, side+" reconstruction", reconstruction, "state", "responses", "diagnostics", "code")
	}
	assertJSONLiteral(t, decodeJSONObject(t, decodeJSONObject(t, root["before"])["input"]), "bytes", "1152")
	assertJSONLiteral(t, decodeJSONObject(t, decodeJSONObject(t, root["after"])["input"]), "bytes", "4259")
	comparability := decodeJSONObject(t, root["comparability"])
	assertJSONKeys(t, "comparability", comparability, "logging_configuration", "provider_identity_status", "before_providers", "after_providers")
	sections := decodeJSONArray(t, root["sections"])
	wants := []struct {
		kind string
		keys []string
	}{{"rpc_providers", []string{"provider"}}, {"rpc_resource_types", []string{"resource_type"}}, {"rpc_methods", []string{"provider", "resource_type", "method"}}, {"ui_resource_types", []string{"resource_type", "duration_source"}}, {"ui_operations", []string{"address", "action", "duration_source"}}}
	if len(sections) != 5 {
		t.Fatalf("sections=%d", len(sections))
	}
	for i, want := range wants {
		section := sections[i]
		assertJSONKeys(t, "section", section, "kind", "tier", "before_available", "after_available", "rows")
		assertJSONLiteral(t, section, "kind", `"`+want.kind+`"`)
		rows := decodeJSONArray(t, section["rows"])
		for _, row := range rows {
			assertJSONKeys(t, "row", row, "key", "state", "before", "after", "changes")
			assertJSONKeys(t, "key", decodeJSONObject(t, row["key"]), want.keys...)
			for _, side := range []string{"before", "after"} {
				if string(row[side]) != "null" {
					assertJSONKeys(t, "summary", decodeJSONObject(t, row[side]), "count", "total_ms", "mean_ms", "max_ms", "lower_bound")
				}
			}
			assertJSONKeys(t, "changes", decodeJSONObject(t, row["changes"]), "count", "total_ms", "mean_ms", "max_ms", "count_percent", "total_percent", "mean_percent", "max_percent")
		}
	}
	firstChanges := decodeJSONObject(t, decodeJSONArray(t, sections[0]["rows"])[0]["changes"])
	for name, value := range firstChanges {
		assertJSONNull(t, "unavailable "+name, value)
	}
	var qualifications []string
	if err := json.Unmarshal(root["qualifications"], &qualifications); err != nil {
		t.Fatal(err)
	}
	wantQualifications := []string{"unmasked_identifiers", "logging_affects_durations", "rpc_and_ui_measure_different_work", "ui_elapsed_duration_rounding", "refresh_windows_are_hook_measurements", "cli_elapsed_displayed_resolution", "resource_duration_sources_not_interchangeable", "observed_changes_are_not_causal", "added_removed_are_observation_presence", "independent_scrub_aliases_may_differ", "logging_configuration_unknown", "lower_bounds_do_not_define_timing_deltas"}
	if !reflect.DeepEqual(qualifications, wantQualifications) {
		t.Fatalf("qualifications = %#v, want %#v", qualifications, wantQualifications)
	}
	if !reflect.DeepEqual(report, original) {
		t.Fatal("renderer mutated report")
	}
}

func TestComparisonJSONUsesProfileReconstructionStates(t *testing.T) {
	tests := []struct {
		name          string
		before, after model.ReconstructionQuality
		wantBefore    []string
		wantAfter     []string
	}{
		{"unchecked and complete", model.ReconstructionQuality{State: "not_checked"}, model.ReconstructionQuality{State: "complete"}, []string{`"not_checked"`, "null", "null", "null"}, []string{`"complete"`, "0", "0", "null"}},
		{"partial and failed", model.ReconstructionQuality{State: "partial", Responses: 2, Diagnostics: 1, Code: "reconstruction_partial"}, model.ReconstructionQuality{State: "failed", Diagnostics: 3, Code: "reconstruction_failed"}, []string{`"partial"`, "2", "1", `"reconstruction_partial"`}, []string{`"failed"`, "0", "3", `"reconstruction_failed"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := ComparisonReport{Before: Report{Reconstruction: tc.before}, After: Report{Reconstruction: tc.after}, Data: model.Comparison{Sections: []model.ComparisonSection{}}}
			var out bytes.Buffer
			if err := RenderComparisonJSON(&out, report, ComparisonMetadata{}); err != nil {
				t.Fatal(err)
			}
			root := decodeJSONObject(t, out.Bytes())
			assertJSONLiteral(t, root, "schema_version", "1")
			for side, want := range map[string][]string{"before": tc.wantBefore, "after": tc.wantAfter} {
				reconstruction := decodeJSONObject(t, decodeJSONObject(t, decodeJSONObject(t, root[side])["quality"])["reconstruction"])
				assertJSONKeys(t, side+" reconstruction", reconstruction, "state", "responses", "diagnostics", "code")
				for i, key := range []string{"state", "responses", "diagnostics", "code"} {
					assertJSONLiteral(t, reconstruction, key, want[i])
				}
			}
		})
	}
}

func deepCopyComparisonReport(report ComparisonReport) ComparisonReport {
	return cloneReflect(reflect.ValueOf(report)).Interface().(ComparisonReport)
}

func TestComparisonJSONRetainsCompleteSectionsAndNullSemantics(t *testing.T) {
	sections := make([]model.ComparisonSection, 0, 5)
	kinds := []string{"rpc_providers", "rpc_resource_types", "rpc_methods", "ui_resource_types", "ui_operations"}
	for _, kind := range kinds {
		section := model.ComparisonSection{Kind: kind, Tier: "rpc", BeforeAvailable: true, AfterAvailable: true, Rows: []model.ComparisonRow{}}
		for i := 0; i < 21; i++ {
			section.Rows = append(section.Rows, model.ComparisonRow{State: "matched", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{LowerBound: true}})
		}
		sections = append(sections, section)
	}
	report := ComparisonReport{Before: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, After: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, Data: model.Comparison{Sections: sections}}
	var out bytes.Buffer
	if err := RenderComparisonJSON(&out, report, ComparisonMetadata{}); err != nil {
		t.Fatal(err)
	}
	root := decodeJSONObject(t, out.Bytes())
	for _, section := range decodeJSONArray(t, root["sections"]) {
		rows := decodeJSONArray(t, section["rows"])
		if len(rows) != 21 {
			t.Fatalf("rows=%d, want 21", len(rows))
		}
		after := decodeJSONObject(t, rows[0]["after"])
		assertJSONLiteral(t, after, "lower_bound", "true")
		assertJSONNull(t, "empty mean", after["mean_ms"])
		assertJSONNull(t, "empty max", after["max_ms"])
	}
	before := decodeJSONObject(t, root["before"])
	assertJSONNull(t, "unavailable unnamed UI", before["unnamed_ui"])
	comparability := decodeJSONObject(t, root["comparability"])
	if len(decodeJSONArray(t, comparability["before_providers"])) != 0 || len(decodeJSONArray(t, comparability["after_providers"])) != 0 {
		t.Fatal("provider arrays not empty")
	}
}

func TestComparisonJSONSignedInteger(t *testing.T) {
	value := model.SignedChange{Negative: true, Magnitude: math.MaxUint64}
	got := comparisonInteger(value)
	if string(got) != "-18446744073709551615" {
		t.Fatalf("integer: %s", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "-18446744073709551615" {
		t.Fatalf("token: %s", encoded)
	}
}

func TestComparisonJSONValidationAndWriterFailures(t *testing.T) {
	valid := ComparisonReport{Before: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, After: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, Data: model.Comparison{Sections: []model.ComparisonSection{}}}
	tests := []struct {
		name   string
		mutate func(*ComparisonReport, *ComparisonMetadata)
		want   string
	}{
		{"metadata utf8", func(_ *ComparisonReport, m *ComparisonMetadata) { m.ToolVersion = "\xff" }, "comparison JSON contains invalid UTF-8"},
		{"exclusion key utf8", func(r *ComparisonReport, _ *ComparisonMetadata) {
			r.Before.Quality.RPC.Exclusions = map[string]uint64{"\xff": 1}
		}, "comparison JSON contains invalid UTF-8"},
		{"reconstruction snapshot", func(r *ComparisonReport, _ *ComparisonMetadata) {
			r.Before.Reconstruction = model.ReconstructionQuality{State: "partial", Diagnostics: 1, Code: "reconstruction_partial"}
		}, "profile JSON has invalid reconstruction snapshot"},
		{"section kind", func(r *ComparisonReport, _ *ComparisonMetadata) {
			r.Data.Sections = []model.ComparisonSection{{Kind: "other"}}
		}, "comparison JSON has invalid section kind"},
		{"row state", func(r *ComparisonReport, _ *ComparisonMetadata) {
			r.Data.Sections = []model.ComparisonSection{{Kind: "rpc_providers", Tier: "rpc", Rows: []model.ComparisonRow{{State: "other"}}}}
		}, "comparison JSON has invalid row state"},
		{"row state utf8", func(r *ComparisonReport, _ *ComparisonMetadata) {
			r.Data.Sections = []model.ComparisonSection{{Kind: "rpc_providers", Tier: "rpc", Rows: []model.ComparisonRow{{State: "\xff"}}}}
		}, "comparison JSON contains invalid UTF-8"},
		{"nonfinite", func(r *ComparisonReport, _ *ComparisonMetadata) {
			v := math.Inf(1)
			r.Data.Sections = []model.ComparisonSection{{
				Kind: "rpc_providers", Tier: "rpc",
				Rows: []model.ComparisonRow{{State: "matched", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{}, Changes: model.ComparisonChanges{MeanMs: &v}}},
			}}
		}, "encoding comparison JSON failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, m := valid, ComparisonMetadata{}
			tt.mutate(&r, &m)
			var out bytes.Buffer
			err := RenderComparisonJSON(&out, r, m)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err=%v want %q", err, tt.want)
			}
			if out.Len() != 0 {
				t.Fatalf("wrote %d bytes", out.Len())
			}
		})
	}
	want := errors.New("write failed")
	if err := RenderComparisonJSON(failingWriter{err: want}, valid, ComparisonMetadata{}); !errors.Is(err, want) {
		t.Fatalf("writer error=%v", err)
	}
	if err := RenderComparisonJSON(shortWriter{}, valid, ComparisonMetadata{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write=%v", err)
	}
}

func TestComparisonJSONEncodesCalculatedChangesAndLowerBounds(t *testing.T) {
	comparison, err := model.Compare(
		model.ComparisonInput{
			RPC: []span.Span{{Provider: "matched", DurationMs: 10}, {Provider: "matched", DurationMs: 30}, {Provider: "removed", DurationMs: 5}},
			UI:  []span.Span{{Address: "resource.lower", RPC: "apply", ResourceType: "type", DurationMs: 10, DurationSaturated: true}},
		},
		model.ComparisonInput{
			RPC: []span.Span{{Provider: "matched", DurationMs: 20}, {Provider: "matched", DurationMs: 30}, {Provider: "matched", DurationMs: 40}, {Provider: "added", DurationMs: 7}},
			UI:  []span.Span{{Address: "resource.lower", RPC: "apply", ResourceType: "type", DurationMs: 20}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	report := ComparisonReport{Before: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, After: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, Data: comparison}
	var out bytes.Buffer
	if err := RenderComparisonJSON(&out, report, ComparisonMetadata{}); err != nil {
		t.Fatal(err)
	}
	sections := decodeJSONArray(t, decodeJSONObject(t, out.Bytes())["sections"])
	rpcRows := decodeJSONArray(t, sections[0]["rows"])
	matched := comparisonJSONRowByKey(t, rpcRows, "provider", "matched")
	assertComparisonSummary(t, matched["before"], "2", "40", "20", "30", "false")
	assertComparisonSummary(t, matched["after"], "3", "90", "30", "40", "false")
	changes := decodeJSONObject(t, matched["changes"])
	for key, want := range map[string]string{"count": "1", "total_ms": "50", "mean_ms": "10", "max_ms": "10", "count_percent": "50", "total_percent": "125", "mean_percent": "50"} {
		assertJSONLiteral(t, changes, key, want)
	}
	var maxPercent float64
	if err := json.Unmarshal(changes["max_percent"], &maxPercent); err != nil {
		t.Fatal(err)
	}
	if math.Abs(maxPercent-100.0/3.0) > 1e-12 {
		t.Fatalf("max percent = %v", maxPercent)
	}

	added := comparisonJSONRowByKey(t, rpcRows, "provider", "added")
	assertComparisonSummary(t, added["before"], "0", "0", "null", "null", "false")
	addedChanges := decodeJSONObject(t, added["changes"])
	assertJSONLiteral(t, addedChanges, "count", "1")
	assertJSONLiteral(t, addedChanges, "total_ms", "7")
	for _, key := range []string{"mean_ms", "max_ms", "count_percent", "total_percent", "mean_percent", "max_percent"} {
		assertJSONNull(t, "added "+key, addedChanges[key])
	}

	uiRows := decodeJSONArray(t, sections[4]["rows"])
	lower := comparisonJSONRowByKey(t, uiRows, "address", "resource.lower")
	lowerChanges := decodeJSONObject(t, lower["changes"])
	assertJSONLiteral(t, lowerChanges, "count", "0")
	assertJSONLiteral(t, lowerChanges, "count_percent", "0")
	for _, key := range []string{"total_ms", "mean_ms", "max_ms", "total_percent", "mean_percent", "max_percent"} {
		assertJSONNull(t, "lower bound "+key, lowerChanges[key])
	}
}

func comparisonJSONRowByKey(t *testing.T, rows []map[string]json.RawMessage, field, value string) map[string]json.RawMessage {
	t.Helper()
	for _, row := range rows {
		var got string
		if err := json.Unmarshal(decodeJSONObject(t, row["key"])[field], &got); err != nil {
			t.Fatal(err)
		}
		if got == value {
			return row
		}
	}
	t.Fatalf("row %s=%q not found", field, value)
	return nil
}

func assertComparisonSummary(t *testing.T, raw json.RawMessage, count, total, mean, max, lower string) {
	t.Helper()
	summary := decodeJSONObject(t, raw)
	for key, want := range map[string]string{"count": count, "total_ms": total, "mean_ms": mean, "max_ms": max, "lower_bound": lower} {
		assertJSONLiteral(t, summary, key, want)
	}
}

func TestComparisonJSONPreservesStringsCollectionsAndLargeIntegers(t *testing.T) {
	identifier := "東京\n\x1b[2J\""
	large := model.SignedChange{Magnitude: 1<<53 + 1}
	report := ComparisonReport{Before: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, After: Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, Data: model.Comparison{BeforeProviders: []string{}, AfterProviders: []string{}, Sections: []model.ComparisonSection{{Kind: "rpc_providers", Tier: "rpc", BeforeAvailable: true, AfterAvailable: true, Rows: []model.ComparisonRow{{Key: model.ComparisonKey{Provider: identifier}, State: "matched", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{}, Changes: model.ComparisonChanges{Count: &large}}}}}}}
	var first, second bytes.Buffer
	metadata := ComparisonMetadata{ToolVersion: identifier, BeforeBasename: identifier, AfterBasename: identifier}
	if err := RenderComparisonJSON(&first, report, metadata); err != nil {
		t.Fatal(err)
	}
	if err := RenderComparisonJSON(&second, report, metadata); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("nondeterministic output")
	}
	decoder := json.NewDecoder(bytes.NewReader(first.Bytes()))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	sections := doc["sections"].([]any)
	rows := sections[0].(map[string]any)["rows"].([]any)
	row := rows[0].(map[string]any)
	if row["key"].(map[string]any)["provider"] != identifier {
		t.Fatal("identifier changed")
	}
	if row["changes"].(map[string]any)["count"].(json.Number).String() != "9007199254740993" {
		t.Fatal("large integer lost")
	}
	if strings.Contains(first.String(), "raw_body") || strings.Contains(first.String(), "rpc_observations") || strings.Contains(first.String(), "ReqID") {
		t.Fatal("internal data leaked")
	}
}

func assertComparisonCaptureKeys(t *testing.T, name string, capture map[string]json.RawMessage) {
	t.Helper()
	assertJSONKeys(t, name, capture, "input", "tiers", "quality", "unnamed_ui")
	tiers := decodeJSONObject(t, capture["tiers"])
	assertJSONKeys(t, name+" tiers", tiers, "rpc", "ui")
	for tierName, raw := range tiers {
		assertJSONKeys(t, name+" "+tierName+" tier", decodeJSONObject(t, raw), "duration_available", "records", "admitted", "rejected", "positioned", "excluded", "duration_ms", "positioned_ms", "excluded_ms", "duration_lower_bound", "clock_origin", "exclusions")
	}
	quality := decodeJSONObject(t, capture["quality"])
	assertJSONKeys(t, name+" quality", quality, "scope", "provider_entries", "structured_lines", "has_address_context", "issues", "attribution", "nameable_ms", "rpc_duration_ms", "nameable_share", "reconstruction", "duration_sources")
	for _, issue := range decodeJSONArray(t, quality["issues"]) {
		assertJSONKeys(t, name+" issue", issue, "stage", "code", "count", "first_entry")
	}
	if string(quality["attribution"]) != "null" {
		attribution := decodeJSONObject(t, quality["attribution"])
		assertJSONKeys(t, name+" attribution", attribution, "spans", "duration_ms", "by_confidence", "candidate_counts")
		for _, row := range decodeJSONArray(t, attribution["by_confidence"]) {
			assertJSONKeys(t, name+" confidence", row, "confidence", "count", "duration_ms")
		}
		for _, row := range decodeJSONArray(t, attribution["candidate_counts"]) {
			assertJSONKeys(t, name+" candidates", row, "candidates", "count")
		}
	}
	assertJSONKeys(t, name+" reconstruction", decodeJSONObject(t, quality["reconstruction"]), "state", "responses", "diagnostics", "code")
	if string(capture["unnamed_ui"]) != "null" {
		assertJSONKeys(t, name+" unnamed UI", decodeJSONObject(t, capture["unnamed_ui"]), "count", "total_ms", "mean_ms", "max_ms", "lower_bound")
	}
}
