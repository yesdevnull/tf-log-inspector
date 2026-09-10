package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestJSONRendererProducesOneDeterministicCompleteDocument(t *testing.T) {
	l, err := model.Load("../../testdata/two-tier.log")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	originalRPC := append([]Observation(nil), report.RPC...)
	originalUI := append([]Observation(nil), report.UI...)
	metadata := JSONMetadata{ToolVersion: "test", InputBasename: "run.log"}

	var first, second bytes.Buffer
	if err := RenderJSON(&first, report, metadata); err != nil {
		t.Fatal(err)
	}
	if err := RenderJSON(&second, report, metadata); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("JSON output is not deterministic")
	}
	if !json.Valid(first.Bytes()) {
		t.Fatal("output is not valid JSON")
	}
	if !bytes.HasSuffix(first.Bytes(), []byte("}\n")) || bytes.HasSuffix(first.Bytes(), []byte("}\n\n")) {
		t.Fatalf("output does not end in exactly one newline: %q", first.Bytes()[len(first.Bytes())-4:])
	}
	decoder := json.NewDecoder(bytes.NewReader(first.Bytes()))
	decoder.UseNumber()
	var root map[string]json.RawMessage
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("second decode error = %v, want EOF", err)
	}
	assertJSONKeys(t, "root", root, "schema_version", "kind", "tool_version", "input", "duration_unit", "tiers", "quality", "rpc_observations", "ui_observations", "aggregates", "timeline", "qualifications")
	assertJSONLiteral(t, root, "schema_version", "1")
	assertJSONLiteral(t, root, "kind", `"profile"`)
	assertJSONLiteral(t, root, "tool_version", `"test"`)
	assertJSONLiteral(t, root, "duration_unit", `"ms"`)

	var input map[string]json.RawMessage
	decodeJSONField(t, root, "input", &input)
	assertJSONKeys(t, "input", input, "basename", "bytes")
	assertJSONLiteral(t, input, "basename", `"run.log"`)
	assertJSONLiteral(t, input, "bytes", "4995")

	var rpc, ui []map[string]json.RawMessage
	decodeJSONField(t, root, "rpc_observations", &rpc)
	decodeJSONField(t, root, "ui_observations", &ui)
	if len(rpc) != 3 || len(ui) != 3 {
		t.Fatalf("observation counts = rpc %d, ui %d", len(rpc), len(ui))
	}
	assertJSONKeys(t, "rpc observation", rpc[0], "index", "entry", "source", "method", "provider", "resource_type", "duration_ms", "position", "attribution")
	assertJSONKeys(t, "ui observation", ui[0], "index", "entry", "source", "address", "action", "resource_type", "duration_ms", "duration_lower_bound", "position")
	assertJSONLiteral(t, rpc[0], "duration_ms", "120")
	assertJSONLiteral(t, ui[0], "duration_ms", "1000")

	var tiers map[string]json.RawMessage
	decodeJSONField(t, root, "tiers", &tiers)
	var rpcTier map[string]json.RawMessage
	decodeJSONField(t, tiers, "rpc", &rpcTier)
	assertJSONKeys(t, "tier", rpcTier, "duration_available", "records", "admitted", "rejected", "positioned", "excluded", "duration_ms", "positioned_ms", "excluded_ms", "duration_lower_bound", "clock_origin", "exclusions")
	assertJSONLiteral(t, rpcTier, "admitted", "3")
	assertJSONLiteral(t, rpcTier, "duration_ms", "410")
	var uiTier map[string]json.RawMessage
	decodeJSONField(t, tiers, "ui", &uiTier)
	assertJSONLiteral(t, uiTier, "duration_ms", "6000")
	var textOutput strings.Builder
	if err := renderReport(&textOutput, report, TextOptions{Limit: 0}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"duration 410ms", "duration 6.0s"} {
		if !strings.Contains(textOutput.String(), want) {
			t.Fatalf("text report missing independently expected %q", want)
		}
	}

	if !reflect.DeepEqual(report.RPC, originalRPC) || !reflect.DeepEqual(report.UI, originalUI) {
		t.Fatal("RenderJSON mutated report observations")
	}
	if got := l.ReconstructionQuality().State; got != "not_checked" {
		t.Fatalf("RenderJSON triggered reconstruction: %q", got)
	}
}

func TestJSONRendererEncodesEverySchemaObjectKey(t *testing.T) {
	firstEntry := uint32(0)
	share, fraction := 0.5, 0.25
	tier := span.FidelityReported
	origin := time.Date(2026, 9, 11, 1, 2, 3, 0, time.UTC)
	report := Report{
		HasContext:     true,
		Reconstruction: model.ReconstructionQuality{State: "checked", Responses: 1},
		Quality: model.CaptureQuality{
			HasContext: true,
			RPC:        model.TierQuality{Admitted: 1, Positioned: 1, Origin: &origin, Exclusions: map[string]uint64{}},
			UI:         model.TierQuality{Exclusions: map[string]uint64{}},
			Issues: []model.QualityIssue{
				{Stage: "scan", Code: "example", Count: 1, FirstEntry: &firstEntry},
				{Stage: "scan", Code: "absent", Count: 1},
			},
			Attribution: attrib.Coverage{
				Spans: 1, TotalMs: 10, Candidates: map[uint32]int{1: 1},
			},
			NameableShare: &share,
		},
		RPC: []Observation{{
			Index: 0,
			Span: span.Span{Entry: 0, StartMs: 0, EndMs: 10, DurationMs: 10, TimestampStatus: logfmt.TimestampValid,
				RPC: "read", Provider: "provider", ResourceType: "type"},
			Source:      &model.SourceLocation{Entry: 0, StartLine: 1, EndLine: 1, StartByte: 0, EndByte: 10},
			Attribution: attrib.Attribution{Confidence: attrib.Contained, Address: "resource.a", Candidates: 1},
		}, {Index: 1, Attribution: attrib.Attribution{Confidence: attrib.Unattributed}}},
		UI:        []Observation{{Index: 0, Span: span.Span{Address: "resource.a", RPC: "read", ResourceType: "type"}}},
		Providers: []model.Bucket{{Key: "provider", Count: 1}},
		Types:     []TypeSummary{{TypeRow: model.TypeRow{ResourceType: "type", RPCCalls: 1, UIResources: 1}}},
		Resources: model.ResourceProjection{
			Rows: []model.ResourceRow{{Address: "resource.a", Operations: []model.ResourceOperation{{UIIndex: 0}}}},
		},
		Timeline: Timeline{
			Tier:              &tier,
			PositionedIndices: []int{0},
			Analysis: model.TimingAnalysis{
				Timing:      model.TimingSelection{AdmittedCount: 1, Exclusions: map[string]uint64{}},
				Metrics:     &model.TimingMetrics{WindowMs: 10, BusyFraction: &fraction},
				ThresholdMs: 1,
				Intervals:   []model.Stall{{StartMs: 0, EndMs: 10, Blocking: 0}, {StartMs: 10, EndMs: 11, Blocking: -1}},
			},
		},
	}
	var out bytes.Buffer
	if err := RenderJSON(&out, report, JSONMetadata{}); err != nil {
		t.Fatal(err)
	}
	root := decodeJSONObject(t, out.Bytes())
	tiers := decodeJSONObject(t, root["tiers"])
	assertJSONKeys(t, "tiers", tiers, "rpc", "ui")
	for name, raw := range tiers {
		assertJSONKeys(t, name+" tier", decodeJSONObject(t, raw), "duration_available", "records", "admitted", "rejected", "positioned", "excluded", "duration_ms", "positioned_ms", "excluded_ms", "duration_lower_bound", "clock_origin", "exclusions")
	}
	rpcRows := decodeJSONArray(t, root["rpc_observations"])
	rpc := rpcRows[0]
	assertJSONKeys(t, "source", decodeJSONObject(t, rpc["source"]), "entry", "start_line", "end_line", "start_byte", "end_byte")
	assertJSONKeys(t, "position", decodeJSONObject(t, rpc["position"]), "start_ms", "end_ms", "valid", "reasons", "start_clamped")
	assertJSONKeys(t, "attribution", decodeJSONObject(t, rpc["attribution"]), "confidence", "address", "candidates")

	quality := decodeJSONObject(t, root["quality"])
	assertJSONKeys(t, "quality", quality, "scope", "provider_entries", "structured_lines", "has_address_context", "issues", "attribution", "nameable_ms", "rpc_duration_ms", "nameable_share", "reconstruction")
	issues := decodeJSONArray(t, quality["issues"])
	assertJSONKeys(t, "issue", issues[0], "stage", "code", "count", "first_entry")
	assertJSONNull(t, "absent issue first entry", issues[1]["first_entry"])
	attributionQuality := decodeJSONObject(t, quality["attribution"])
	assertJSONKeys(t, "attribution quality", attributionQuality, "spans", "duration_ms", "by_confidence", "candidate_counts")
	assertJSONKeys(t, "confidence total", decodeJSONArray(t, attributionQuality["by_confidence"])[0], "confidence", "count", "duration_ms")
	assertJSONKeys(t, "candidate count", decodeJSONArray(t, attributionQuality["candidate_counts"])[0], "candidates", "count")
	assertJSONKeys(t, "reconstruction", decodeJSONObject(t, quality["reconstruction"]), "state", "responses", "code")
	assertJSONNull(t, "checked reconstruction code", decodeJSONObject(t, quality["reconstruction"])["code"])

	aggregates := decodeJSONObject(t, root["aggregates"])
	assertJSONKeys(t, "aggregates", aggregates, "providers", "resource_types", "resources", "ui", "unnamed_ui", "rpc_evidence")
	provider := decodeJSONArray(t, aggregates["providers"])[0]
	assertJSONKeys(t, "provider", provider, "provider", "rpc")
	assertJSONKeys(t, "total", decodeJSONObject(t, provider["rpc"]), "count", "total_ms", "max_ms", "lower_bound")
	assertJSONKeys(t, "resource type", decodeJSONArray(t, aggregates["resource_types"])[0], "resource_type", "rpc", "ui")
	assertJSONKeys(t, "resource", decodeJSONArray(t, aggregates["resources"])[0], "address", "ui", "named_rpc", "overlapping_rpc", "ui_observation_indices")
	assertJSONKeys(t, "RPC evidence", decodeJSONObject(t, aggregates["rpc_evidence"]), "baseline", "missing_type", "no_context", "contained", "likely", "overlapping", "ambiguous", "unattributed")

	timeline := decodeJSONObject(t, root["timeline"])
	assertJSONKeys(t, "timeline", timeline, "tier", "status", "clock_origin", "window_scope", "admitted", "positioned", "excluded", "admitted_ms", "admitted_lower_bound", "positioned_ms", "excluded_ms", "excluded_lower_bound", "exclusions", "metrics", "threshold_ms", "intervals")
	assertJSONKeys(t, "metrics", decodeJSONObject(t, timeline["metrics"]), "window_ms", "peak", "busy_ms", "busy_fraction", "summed_duration_ms", "summed_window_ratio")
	intervals := decodeJSONArray(t, timeline["intervals"])
	assertJSONKeys(t, "interval", intervals[0], "start_ms", "end_ms", "duration_ms", "min_running", "max_running", "observed_peak", "active_observation_index")
	assertJSONNull(t, "inactive interval observation", intervals[1]["active_observation_index"])
	rpcPosition := decodeJSONObject(t, rpc["position"])
	rpcAttribution := decodeJSONObject(t, rpc["attribution"])
	absentRPCAttribution := decodeJSONObject(t, rpcRows[1]["attribution"])
	rpcTier := decodeJSONObject(t, tiers["rpc"])
	uiTier := decodeJSONObject(t, tiers["ui"])
	reconstruction := decodeJSONObject(t, quality["reconstruction"])
	metrics := decodeJSONObject(t, timeline["metrics"])
	if string(rpc["source"]) == "null" || string(rpcPosition["start_ms"]) == "null" || string(rpcPosition["end_ms"]) == "null" ||
		string(rpcAttribution["address"]) == "null" || string(issues[0]["first_entry"]) == "null" ||
		string(quality["attribution"]) == "null" || string(quality["nameable_share"]) == "null" ||
		string(reconstruction["responses"]) == "null" || string(rpcTier["clock_origin"]) == "null" ||
		string(timeline["clock_origin"]) == "null" || string(metrics["busy_fraction"]) == "null" || string(metrics["summed_window_ratio"]) == "null" ||
		string(timeline["threshold_ms"]) == "null" || string(intervals[0]["active_observation_index"]) == "null" {
		t.Fatal("populated nullable field encoded as null")
	}
	assertJSONNull(t, "unattributed address", absentRPCAttribution["address"])
	assertJSONNull(t, "absent UI tier origin", uiTier["clock_origin"])

	failedReport := Report{
		Reconstruction: model.ReconstructionQuality{State: "failed", Code: "decode_failed"},
		Timeline: Timeline{Tier: &tier, Analysis: model.TimingAnalysis{
			Timing:  model.TimingSelection{Exclusions: map[string]uint64{}},
			Metrics: &model.TimingMetrics{},
		}},
	}
	var failed bytes.Buffer
	if err := RenderJSON(&failed, failedReport, JSONMetadata{}); err != nil {
		t.Fatal(err)
	}
	failedQuality := decodeJSONObject(t, decodeJSONObject(t, failed.Bytes())["quality"])
	failedReconstruction := decodeJSONObject(t, failedQuality["reconstruction"])
	assertJSONNull(t, "failed reconstruction responses", failedReconstruction["responses"])
	if string(failedReconstruction["code"]) == "null" {
		t.Fatal("failed reconstruction code encoded as null")
	}
	failedTimeline := decodeJSONObject(t, decodeJSONObject(t, failed.Bytes())["timeline"])
	failedMetrics := decodeJSONObject(t, failedTimeline["metrics"])
	assertJSONNull(t, "zero-window busy fraction", failedMetrics["busy_fraction"])
	assertJSONNull(t, "zero-window summed ratio", failedMetrics["summed_window_ratio"])

	var absent bytes.Buffer
	if err := RenderJSON(&absent, Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, JSONMetadata{}); err != nil {
		t.Fatal(err)
	}
	absentRoot := decodeJSONObject(t, absent.Bytes())
	absentQuality := decodeJSONObject(t, absentRoot["quality"])
	absentTimeline := decodeJSONObject(t, absentRoot["timeline"])
	for name, raw := range map[string]json.RawMessage{
		"quality attribution": absentQuality["attribution"], "nameable share": absentQuality["nameable_share"],
		"timeline tier": absentTimeline["tier"], "timeline origin": absentTimeline["clock_origin"],
		"timeline metrics": absentTimeline["metrics"], "timeline threshold": absentTimeline["threshold_ms"],
	} {
		assertJSONNull(t, name, raw)
	}
	absentReconstruction := decodeJSONObject(t, absentQuality["reconstruction"])
	assertJSONNull(t, "reconstruction responses", absentReconstruction["responses"])
	assertJSONNull(t, "reconstruction code", absentReconstruction["code"])
	absentRPC := decodeJSONArray(t, absentRoot["rpc_observations"])
	if len(absentRPC) != 0 {
		t.Fatal("absent RPC observations are not empty")
	}
	populatedUI := decodeJSONArray(t, root["ui_observations"])[0]
	assertJSONNull(t, "absent source", populatedUI["source"])
	uiPosition := decodeJSONObject(t, populatedUI["position"])
	assertJSONNull(t, "absent position start", uiPosition["start_ms"])
	assertJSONNull(t, "absent position end", uiPosition["end_ms"])
}

func TestJSONRendererPreservesIdentifiersAndNulls(t *testing.T) {
	identifier := "module.東京[\"quoted\"]\n\x1b[2J"
	report := Report{
		Reconstruction: model.ReconstructionQuality{State: "not_checked"},
		RPC: []Observation{{
			Span:        span.Span{RPC: identifier, Provider: identifier, ResourceType: identifier},
			Attribution: attrib.Attribution{},
		}},
	}
	var out bytes.Buffer
	if err := RenderJSON(&out, report, JSONMetadata{ToolVersion: identifier, InputBasename: identifier}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ToolVersion string `json:"tool_version"`
		Input       struct {
			Basename string `json:"basename"`
		} `json:"input"`
		RPC []struct {
			Method   string `json:"method"`
			Provider string `json:"provider"`
			Position struct {
				Start *uint32 `json:"start_ms"`
				End   *uint32 `json:"end_ms"`
			} `json:"position"`
			Attribution struct {
				Address *string `json:"address"`
			} `json:"attribution"`
		} `json:"rpc_observations"`
		UI []json.RawMessage `json:"ui_observations"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ToolVersion != identifier || doc.Input.Basename != identifier || doc.RPC[0].Method != identifier || doc.RPC[0].Provider != identifier {
		t.Fatalf("identifier changed during JSON encoding: %+v", doc)
	}
	if doc.RPC[0].Position.Start != nil || doc.RPC[0].Position.End != nil || doc.RPC[0].Attribution.Address != nil {
		t.Fatal("unavailable values were not encoded as null")
	}
	if doc.UI == nil || len(doc.UI) != 0 {
		t.Fatalf("empty UI observations = %#v, want []", doc.UI)
	}
	for _, forbidden := range []string{`"DisplayText"`, `"ReqID"`, `"body"`, `"absolute_path"`} {
		if bytes.Contains(out.Bytes(), []byte(forbidden)) {
			t.Fatalf("output contains forbidden field %s", forbidden)
		}
	}
}

func TestJSONRendererIncludesAllArraysAndSaturatedValues(t *testing.T) {
	report := Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}
	for i := 0; i < 25; i++ {
		report.RPC = append(report.RPC, Observation{Index: i, Span: span.Span{DurationMs: uint32(i)}})
		report.UI = append(report.UI, Observation{Index: i, Span: span.Span{DurationMs: uint32(i)}})
		report.Providers = append(report.Providers, model.Bucket{Key: fmt.Sprintf("provider-%02d", i)})
		report.Types = append(report.Types, TypeSummary{TypeRow: model.TypeRow{ResourceType: fmt.Sprintf("type-%02d", i)}})
		report.Resources.Rows = append(report.Resources.Rows, model.ResourceRow{Address: fmt.Sprintf("resource-%02d", i)})
	}
	report.UI[24].Span.DurationMs = math.MaxUint32
	report.UI[24].Span.DurationSaturated = true
	var out bytes.Buffer
	if err := RenderJSON(&out, report, JSONMetadata{}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RPC []struct {
			Index int `json:"index"`
		} `json:"rpc_observations"`
		UI []struct {
			Index              int    `json:"index"`
			DurationMs         uint32 `json:"duration_ms"`
			DurationLowerBound bool   `json:"duration_lower_bound"`
		} `json:"ui_observations"`
		Aggregates struct {
			Providers []struct {
				Provider string `json:"provider"`
			} `json:"providers"`
			Types []struct {
				ResourceType string `json:"resource_type"`
			} `json:"resource_types"`
			Resources []struct {
				Address string `json:"address"`
			} `json:"resources"`
		} `json:"aggregates"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RPC) != 25 || doc.RPC[24].Index != 24 {
		t.Fatalf("RPC observations were limited: len=%d last=%+v", len(doc.RPC), doc.RPC[len(doc.RPC)-1])
	}
	if len(doc.UI) != 25 || doc.UI[24].Index != 24 || doc.UI[24].DurationMs != math.MaxUint32 || !doc.UI[24].DurationLowerBound {
		t.Fatalf("UI observations were limited or saturation changed: len=%d last=%+v", len(doc.UI), doc.UI[24])
	}
	if len(doc.Aggregates.Providers) != 25 || doc.Aggregates.Providers[24].Provider != "provider-24" ||
		len(doc.Aggregates.Types) != 25 || doc.Aggregates.Types[24].ResourceType != "type-24" ||
		len(doc.Aggregates.Resources) != 25 || doc.Aggregates.Resources[24].Address != "resource-24" {
		t.Fatalf("aggregate arrays were limited: providers=%d types=%d resources=%d", len(doc.Aggregates.Providers), len(doc.Aggregates.Types), len(doc.Aggregates.Resources))
	}
}

func TestJSONRendererLeavesOutputUntouchedOnProjectionOrEncodingFailure(t *testing.T) {
	badUTF8 := string([]byte{0xff})
	nan := math.NaN()
	for _, tc := range []struct {
		name   string
		report Report
		meta   JSONMetadata
		want   string
	}{
		{"invalid mapping", Report{Reconstruction: model.ReconstructionQuality{State: "invalid"}}, JSONMetadata{}, "profile JSON has invalid reconstruction state"},
		{"invalid UTF-8", Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, JSONMetadata{ToolVersion: badUTF8}, "profile JSON contains invalid UTF-8"},
		{"non-finite fraction", Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}, Quality: model.CaptureQuality{NameableShare: &nan}}, JSONMetadata{}, "encoding profile JSON failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := RenderJSON(&out, tc.report, tc.meta)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("failure wrote %q", out.Bytes())
			}
			if strings.Contains(err.Error(), badUTF8) {
				t.Fatal("error exposed invalid input")
			}
		})
	}
}

func TestJSONRendererPropagatesWriterFailures(t *testing.T) {
	report := Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}
	want := errors.New("writer failed")
	if err := RenderJSON(failingWriter{err: want}, report, JSONMetadata{}); !errors.Is(err, want) {
		t.Fatalf("writer error = %v, want %v", err, want)
	}
	if err := RenderJSON(shortWriter{}, report, JSONMetadata{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v, want %v", err, io.ErrShortWrite)
	}
}

func assertJSONKeys(t *testing.T, name string, object map[string]json.RawMessage, keys ...string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	want := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		want[key] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("%s keys = %v, want exactly %v", name, got, keys)
	}
	for _, key := range got {
		if _, ok := want[key]; !ok {
			t.Fatalf("%s has unexpected key %q", name, key)
		}
	}
}

func assertJSONLiteral(t *testing.T, object map[string]json.RawMessage, key, want string) {
	t.Helper()
	if got := string(object[key]); got != want {
		t.Fatalf("%s = %s, want %s", key, got, want)
	}
}

func decodeJSONField(t *testing.T, object map[string]json.RawMessage, key string, target any) {
	t.Helper()
	if err := json.Unmarshal(object[key], target); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
}

func decodeJSONObject(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	return object
}

func decodeJSONArray(t *testing.T, data []byte) []map[string]json.RawMessage {
	t.Helper()
	var array []map[string]json.RawMessage
	if err := json.Unmarshal(data, &array); err != nil {
		t.Fatalf("decode array: %v", err)
	}
	return array
}

func assertJSONNull(t *testing.T, name string, raw json.RawMessage) {
	t.Helper()
	if string(raw) != "null" {
		t.Fatalf("%s = %s, want null", name, raw)
	}
}
