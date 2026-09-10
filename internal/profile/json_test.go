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

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
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

func TestJSONRendererIncludesAllObservationsAndSaturatedValues(t *testing.T) {
	report := Report{Reconstruction: model.ReconstructionQuality{State: "not_checked"}}
	for i := 0; i < 25; i++ {
		report.RPC = append(report.RPC, Observation{Index: i, Span: span.Span{DurationMs: uint32(i)}})
	}
	report.UI = []Observation{{Index: 0, Span: span.Span{DurationMs: math.MaxUint32, DurationSaturated: true}}}
	var out bytes.Buffer
	if err := RenderJSON(&out, report, JSONMetadata{}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RPC []struct {
			Index int `json:"index"`
		} `json:"rpc_observations"`
		UI []struct {
			DurationMs         uint32 `json:"duration_ms"`
			DurationLowerBound bool   `json:"duration_lower_bound"`
		} `json:"ui_observations"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RPC) != 25 || doc.RPC[24].Index != 24 {
		t.Fatalf("RPC observations were limited: len=%d last=%+v", len(doc.RPC), doc.RPC[len(doc.RPC)-1])
	}
	if doc.UI[0].DurationMs != math.MaxUint32 || !doc.UI[0].DurationLowerBound {
		t.Fatalf("saturated UI duration = %+v", doc.UI[0])
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
