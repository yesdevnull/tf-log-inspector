package model

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestBuildCaptureQualityReconcilesIndependentEvidence(t *testing.T) {
	first := uint32(7)
	in := CaptureQualityInput{
		Stats: logfmt.Stats{StructuredLines: 4, BackwardsTimestamps: 2, TimestampOffsetsOutOfRange: 3, LinesSaturated: 5},
		Caps:  span.Capabilities{ProviderEntries: 6, ReqIDTrackingFull: true},
		RPCSpans: []span.Span{
			{DurationMs: 10, TimestampStatus: logfmt.TimestampValid},
			{Entry: 4, DurationMs: 20, TimestampStatus: logfmt.TimestampMissing, StartClamped: true},
		},
		UISpans:           []span.Span{{Entry: 3, DurationMs: math.MaxUint32, DurationSaturated: true, TimestampStatus: logfmt.TimestampValid}},
		RPCEvidence:       span.TimingEvidence{Records: 3, Rejected: map[string]span.IssueCount{"duration_invalid": {Count: 1, FirstEntry: first}}},
		UIEvidence:        span.TimingEvidence{Records: 1, SchemaErrors: span.IssueCount{Count: 1, FirstEntry: 9}, TimestampIssues: map[string]span.IssueCount{"timestamp_before_origin": {Count: 4, FirstEntry: 10}}},
		UIOrigin:          time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Contexts:          []attrib.Context{{}},
		ContextEvidence:   attrib.ContextEvidence{IncompleteContexts: span.IssueCount{Count: 1, FirstEntry: 11}},
		Attributions:      []attrib.Attribution{{Confidence: attrib.Contained, Candidates: 1}, {Confidence: attrib.Unattributed}},
		ComponentOverflow: 12, RequestIDOverflow: 13,
	}

	q := BuildCaptureQuality(in)
	if q.RPC.Records != 3 || q.RPC.Admitted != 2 || q.RPC.Rejected != 1 || q.RPC.DurationMs != 30 || q.RPC.PositionedMs != 10 || q.RPC.ExcludedMs != 20 {
		t.Fatalf("RPC quality does not reconcile: %+v", q.RPC)
	}
	if q.NameableMs != 10 || q.RPCDurationMs != 30 || q.NameableShare == nil || *q.NameableShare != 1.0/3.0 {
		t.Fatalf("nameable evidence = %+v", q)
	}
	if q.UI.DurationMs != math.MaxUint32 || !q.UI.DurationLowerBound || q.UI.Rejected != 0 {
		t.Fatalf("UI quality = %+v", q.UI)
	}
	for _, want := range []QualityIssue{{"rpc_duration", "duration_invalid", 1, &first}, {"scan", "line_count_saturated", 5, nil}, {"interning", "component_overflow", 12, nil}, {"interning", "request_id_overflow", 13, nil}, {"capability", "request_tracking_capped", 1, nil}} {
		if !containsIssue(q.Issues, want) {
			t.Errorf("missing issue %+v in %+v", want, q.Issues)
		}
	}
	if !containsIssueCode(q.Issues, "ui_decode", "schema_invalid") {
		t.Fatalf("decode issues assigned to wrong stages: %+v", q.Issues)
	}
	if !containsIssue(q.Issues, QualityIssue{"rpc_duration", "start_clamped", 1, uint32Pointer(4)}) || !containsIssue(q.Issues, QualityIssue{"ui_duration", "duration_saturated", 1, uint32Pointer(3)}) {
		t.Fatalf("span issue locations unavailable: %+v", q.Issues)
	}
}

func TestBuildCaptureQualityDistinguishesUnknownAndGenuineZeroShare(t *testing.T) {
	span10 := []span.Span{{DurationMs: 10}}
	cases := []struct {
		name string
		in   CaptureQualityInput
		want *float64
	}{
		{"no context", CaptureQualityInput{RPCSpans: span10}, nil},
		{"zero denominator", CaptureQualityInput{Contexts: []attrib.Context{{}}, Attributions: nil}, nil},
		{"genuine zero", CaptureQualityInput{RPCSpans: span10, Contexts: []attrib.Context{{}}, Attributions: []attrib.Attribution{{Confidence: attrib.Unattributed}}}, floatPointer(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildCaptureQuality(tc.in)
			if !reflect.DeepEqual(got.NameableShare, tc.want) {
				t.Fatalf("share = %v, want %v", got.NameableShare, tc.want)
			}
		})
	}
}

func TestBuildCaptureQualityRejectsNonParallelAttribution(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("BuildCaptureQuality did not reject non-parallel attribution")
		}
	}()
	BuildCaptureQuality(CaptureQualityInput{RPCSpans: []span.Span{{}, {}}, Contexts: []attrib.Context{{}}, Attributions: []attrib.Attribution{{}}})
}

func TestBuildCaptureQualityRetainsAllUnpositionedDurations(t *testing.T) {
	q := BuildCaptureQuality(CaptureQualityInput{
		RPCSpans: []span.Span{{DurationMs: 12, TimestampStatus: logfmt.TimestampMissing}},
		UISpans:  []span.Span{{DurationMs: 34, TimestampStatus: logfmt.TimestampInvalid}},
	})
	if q.RPC.Admitted != 1 || q.RPC.Positioned != 0 || q.RPC.DurationMs != 12 || q.RPC.ExcludedMs != 12 || q.UI.Admitted != 1 || q.UI.Positioned != 0 || q.UI.DurationMs != 34 || q.UI.ExcludedMs != 34 {
		t.Fatalf("unpositioned durations were discarded: RPC=%+v UI=%+v", q.RPC, q.UI)
	}
}

func TestCaptureQualityReturnsDeeplyDetachedFacts(t *testing.T) {
	first := uint32(2)
	origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	share := 0.5
	l := &Log{quality: CaptureQuality{RPC: TierQuality{Origin: &origin, Exclusions: map[string]uint64{"x": 1}}, UI: TierQuality{Origin: &origin, Exclusions: map[string]uint64{"y": 2}}, Issues: []QualityIssue{{Stage: "x", Code: "y", Count: 1, FirstEntry: &first}}, Attribution: attrib.Coverage{Candidates: map[uint32]int{1: 2}}, NameableShare: &share}}
	got := l.CaptureQuality()
	got.RPC.Exclusions["x"] = 99
	got.UI.Exclusions["y"] = 99
	got.Issues[0].Code = "changed"
	*got.Issues[0].FirstEntry = 99
	*got.RPC.Origin = time.Time{}
	*got.UI.Origin = time.Time{}
	got.Attribution.Candidates[1] = 99
	*got.NameableShare = 99
	want := l.CaptureQuality()
	if want.RPC.Exclusions["x"] != 1 || want.UI.Exclusions["y"] != 2 || want.Issues[0].Code != "y" || *want.Issues[0].FirstEntry != 2 || want.RPC.Origin.IsZero() || want.UI.Origin.IsZero() || want.Attribution.Candidates[1] != 2 || *want.NameableShare != 0.5 {
		t.Fatalf("stored quality mutated: %+v", want)
	}
}

func TestLoadBuildsStableQualityFromRealScan(t *testing.T) {
	const marker = "CAPTURE_SECRET_MARKER"
	source := "{\"@level\":\"info\",\"@timestamp\":\"2025-12-31T23:59:59Z\",\"type\":\"apply_start\",\"hook\":{\"action\":\"read\",\"resource\":{\"addr\":\"" + marker + "\",\"resource_type\":\"x\"}}}\n" +
		"2026-01-01T00:00:00.000Z [TRACE] core: baseline\n" +
		"2026-01-01T00:00:00.020Z [TRACE] provider.x: Received downstream response tf_req_duration_ms=10 tf_rpc=ReadResource tf_resource_type=x tf_provider_addr=" + marker + "\n" +
		"2027-01-01T00:00:01.000Z [TRACE] provider.x: Received downstream response tf_req_duration_ms=20 tf_rpc=ReadResource tf_provider_addr=x\n" +
		"2027-01-01T00:00:02.000Z [TRACE] provider.x: Received downstream response tf_req_duration_ms=bad\n" +
		"{\"@level\":\"info\",\"@timestamp\":\"2026-01-01T00:00:01Z\",\"type\":\"apply_complete\",\"hook\":{\"elapsed_seconds\":0,\"action\":\"read\",\"resource\":{\"addr\":\"" + marker + "\",\"resource_type\":\"x\"}}}\n" +
		"{\"@level\":\"info\",\"@timestamp\":null,\"marker\":\"" + marker + "\"\n" +
		"{\"@level\":\"info\",\"@timestamp\":\"2026-01-01T00:00:03Z\",\"type\":\"apply_start\",\"hook\":{\"resource\":{\"addr\":\"" + marker + "-incomplete\",\"resource_type\":\"y\"}}}\n"
	path := filepath.Join(t.TempDir(), "quality.log")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	q1, q2 := l.CaptureQuality(), l.CaptureQuality()
	if !reflect.DeepEqual(q1, q2) || q1.RPC.Admitted != 2 || q1.RPC.Rejected != 1 || q1.RPC.DurationMs != 30 || q1.RPC.PositionedMs != 10 || q1.RPC.ExcludedMs != 20 || q1.UI.Admitted != 1 || q1.UI.DurationMs != 0 {
		t.Fatalf("quality = %+v", q1)
	}
	if strings.Contains(fmt.Sprintf("%+v", q1), marker) {
		t.Fatalf("quality leaked marker: %+v", q1)
	}
	if q1.NameableShare == nil || *q1.NameableShare != 1.0/3.0 || q1.NameableMs != 10 || q1.RPCDurationMs != 30 {
		t.Fatalf("nameable denominator = %+v", q1)
	}
	if !containsIssueCode(q1.Issues, "context", "context_incomplete") {
		t.Fatalf("incomplete context absent: %+v", q1.Issues)
	}
}

func TestReconstructionQualityIsLazyAndSeparate(t *testing.T) {
	for _, tc := range []struct {
		name, source, state, code string
		responses                 int
	}{
		{"success", "2026-01-01T00:00:00.000Z [DEBUG] provider.x: {\"ok\":true}\n", "checked", "", 1},
		{"zero responses", "2026-01-01T00:00:00.000Z [INFO] core: ordinary log line\n", "checked", "", 0},
		{"failure", "2026-01-01T00:00:00.000Z [DEBUG] provider.x: {\"secret\":\n", "failed", "reconstruction_failed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "response.log")
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			l, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			before := l.CaptureQuality()
			if got := l.ReconstructionQuality(); got != (ReconstructionQuality{State: "not_checked"}) {
				t.Fatalf("initial = %+v", got)
			}
			_, _ = l.ProviderResponse(l.Entries[0])
			if got := l.ReconstructionQuality(); got.State != tc.state || got.Responses != tc.responses || got.Code != tc.code || strings.Contains(fmt.Sprintf("%+v", got), "secret") {
				t.Fatalf("completed = %+v", got)
			}
			if after := l.CaptureQuality(); !reflect.DeepEqual(before, after) {
				t.Fatalf("static quality changed: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestQualityAccessorsAreConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "response.log")
	if err := os.WriteFile(path, []byte("2026-01-01T00:00:00.000Z [DEBUG] provider.x: {\"ok\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _ = l.ProviderResponse(l.Entries[0])
			_ = l.ReconstructionQuality()
			_ = l.CaptureQuality()
			_, _ = l.SourceLocation(0)
		})
	}
	wg.Wait()
}

func containsIssue(issues []QualityIssue, want QualityIssue) bool {
	for _, got := range issues {
		if got.Stage == want.Stage && got.Code == want.Code && got.Count == want.Count && reflect.DeepEqual(got.FirstEntry, want.FirstEntry) {
			return true
		}
	}
	return false
}
func containsIssueCode(issues []QualityIssue, stage, code string) bool {
	for _, got := range issues {
		if got.Stage == stage && got.Code == code {
			return true
		}
	}
	return false
}
func floatPointer(v float64) *float64 { return &v }
func uint32Pointer(v uint32) *uint32  { return &v }
