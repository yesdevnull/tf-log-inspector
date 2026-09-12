package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestComparisonTextRendersQualifiedMetrics(t *testing.T) {
	before := []span.Span{{Provider: "p\x1b", ResourceType: "r\n", RPC: "Read\t", DurationMs: 10}, {Provider: "p\x1b", ResourceType: "r\n", RPC: "Read\t", DurationMs: 10}}
	after := []span.Span{{Provider: "p\x1b", ResourceType: "r\n", RPC: "Read\t", DurationMs: 20}}
	report := comparisonTextReport(t, model.ComparisonInput{RPC: before}, model.ComparisonInput{RPC: after})
	var out bytes.Buffer
	metadata := ComparisonMetadata{BeforeBasename: "before\n.log", AfterBasename: "after\x1b.log"}
	if err := RenderComparisonText(&out, report, metadata, TextOptions{Limit: 20}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"tfli comparison report", "BEFORE: before\\n.log", "AFTER: after\\x1b.log", "BEFORE CAPTURE QUALITY", "AFTER CAPTURE QUALITY",
		"RPC METHODS — EXACT TOTAL-DURATION CHANGES", "matched: provider=p\\x1b  resource_type=r\\n  method=Read\\t",
		"count         2            1            -1          -50.00%",
		"total ms      20           20           +0          +0.00%",
		"mean ms       10.00        20.00        +10.00      +100.00%",
		"max ms        10           20           +10         +100.00%",
		"identifiers are unmasked", "logging changes observed timing", "logging configuration equivalence is unknown",
		"separately scrubbed aliases cannot be matched reliably", "added and removed mean observed evidence presence", "changes do not establish causes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestComparisonTextLowerBoundsNullsAndUnavailable(t *testing.T) {
	report := comparisonTextReport(t,
		model.ComparisonInput{UI: []span.Span{{Address: "addr\r", RPC: "apply\b", ResourceType: "type", DurationMs: 1, DurationSaturated: true}}},
		model.ComparisonInput{UI: []span.Span{{Address: "addr\r", RPC: "apply\b", ResourceType: "type", DurationMs: 1}, {Address: "addr\r", RPC: "apply\b", ResourceType: "type", DurationMs: 1}}},
	)
	var out bytes.Buffer
	if err := RenderComparisonText(&out, report, ComparisonMetadata{}, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"UNRANKED / UNAVAILABLE", "address=addr\\r  action=apply\\b", ">=1", ">=1.00", "timing deltas unavailable: lower bound", "count         1            2            +1          +100.00%", "total ms      >=1          2            n/a         n/a", "ui_elapsed durations are rounded by up to one second per observation", "missing addresses cannot be matched"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestComparisonTextLimitsListsIndependently(t *testing.T) {
	beforeUI := make([]span.Span, 0, 42)
	afterUI := make([]span.Span, 0, 42)
	for i := 0; i < 21; i++ {
		address := "exact" + string(rune('A'+i))
		beforeUI = append(beforeUI, span.Span{Address: address, RPC: "apply", ResourceType: "type", DurationMs: 1})
		afterUI = append(afterUI, span.Span{Address: address, RPC: "apply", ResourceType: "type", DurationMs: uint32(22 - i)})
	}
	for i := 0; i < 21; i++ {
		address := "lower" + string(rune('A'+i))
		beforeUI = append(beforeUI, span.Span{Address: address, RPC: "apply", ResourceType: "type", DurationMs: 1, DurationSaturated: true})
		afterUI = append(afterUI, span.Span{Address: address, RPC: "apply", ResourceType: "type", DurationMs: 2})
	}
	report := comparisonTextReport(t, model.ComparisonInput{UI: beforeUI}, model.ComparisonInput{UI: afterUI})
	unavailableRPC := make([]span.Span, 21)
	for i := range unavailableRPC {
		unavailableRPC[i] = span.Span{Provider: "unavailable" + string(rune('A'+i)), DurationMs: 1}
	}
	unavailableReport := comparisonTextReport(t, model.ComparisonInput{RPC: unavailableRPC}, model.ComparisonInput{})
	for _, tc := range []struct {
		limit           int
		exact, unranked int
	}{{1, 1, 1}, {20, 20, 20}, {0, 21, 21}} {
		var out bytes.Buffer
		if err := RenderComparisonText(&out, report, ComparisonMetadata{}, TextOptions{Limit: tc.limit}); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if strings.Count(got, "matched: address=exact") != tc.exact || strings.Count(got, "matched: address=lower") != tc.unranked {
			t.Errorf("limit %d counts wrong", tc.limit)
		}
		if tc.limit > 0 && !strings.Contains(got, "shown "+strconv.Itoa(tc.limit)+" of 21") {
			t.Errorf("limit label absent in:\n%s", got)
		}
		if strings.Count(got, "RPC timing records     99: admitted 88, rejected 11") != 2 {
			t.Errorf("quality totals were absent or duplicated at limit %d", tc.limit)
		}
		out.Reset()
		if err := RenderComparisonText(&out, unavailableReport, ComparisonMetadata{}, TextOptions{Limit: tc.limit}); err != nil {
			t.Fatal(err)
		}
		got = out.String()
		providerSection := strings.SplitN(got, "RPC RESOURCE TYPES AVAILABILITY", 2)[0]
		providerSection = strings.SplitN(providerSection, "RPC PROVIDERS AVAILABILITY", 2)[1]
		if count := strings.Count(providerSection, "unavailable: provider=unavailable"); count != tc.unranked {
			t.Errorf("unavailable RPC limit %d count=%d want %d", tc.limit, count, tc.unranked)
		}
	}
}

func TestComparisonTextValidationAndWriterFailures(t *testing.T) {
	report := comparisonTextReport(t, model.ComparisonInput{}, model.ComparisonInput{})
	var out bytes.Buffer
	if err := RenderComparisonText(&out, report, ComparisonMetadata{}, TextOptions{Limit: -1}); err == nil || err.Error() != "comparison limit must be non-negative" || out.Len() != 0 {
		t.Fatalf("negative limit: %v, output %q", err, out.String())
	}
	want := errors.New("write failed")
	if err := RenderComparisonText(failingWriter{err: want}, report, ComparisonMetadata{}, TextOptions{}); !errors.Is(err, want) {
		t.Fatalf("writer error=%v", err)
	}
	if err := RenderComparisonText(shortWriter{}, report, ComparisonMetadata{}, TextOptions{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write=%v", err)
	}
}

func TestComparisonTextEscapesEveryKeyAndPreservesJSONValues(t *testing.T) {
	large := model.SignedChange{Magnitude: math.MaxUint64}
	rounded := -0.001
	longProvider := strings.Repeat("very-long-provider-", 12) + "end"
	before := model.ComparisonInput{
		RPC: []span.Span{{Provider: "provider\a" + longProvider, ResourceType: "rpc-type\v", RPC: "method\r", DurationMs: 1}},
		UI:  []span.Span{{ResourceType: "ui-type\f", Address: "address\x00", RPC: "action\x7f", DurationMs: 1}},
	}
	after := model.ComparisonInput{
		RPC: []span.Span{{Provider: "other-set\t", ResourceType: "rpc-type\v", RPC: "method\r", DurationMs: 2}},
		UI:  []span.Span{{ResourceType: "ui-type\f", Address: "address\x00", RPC: "action\x7f", DurationMs: 2}},
	}
	report := comparisonTextReport(t, before, after)
	// Maximal signed magnitudes and sub-cent rounding cannot be produced by
	// span.DurationMs values, so exercise those formatter boundaries directly.
	report.Data.Sections[0].Rows[0].Changes.TotalMs = &large
	report.Data.Sections[0].Rows[0].Changes.MeanMs = &rounded
	wantReport := deepCopyComparisonReport(report)
	var textOut, jsonOut bytes.Buffer
	if err := RenderComparisonText(&textOut, report, ComparisonMetadata{}, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := RenderComparisonJSON(&jsonOut, report, ComparisonMetadata{}); err != nil {
		t.Fatal(err)
	}
	got := textOut.String()
	for _, want := range []string{"provider=provider\\a" + longProvider, "resource_type=rpc-type\\v", "method=method\\r", "resource_type=ui-type\\f", "address=address\\x00  action=action\\x7f", "provider\\a" + longProvider, "other-set\\t", "+18446744073709551615", "+0.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !reflect.DeepEqual(report, wantReport) {
		t.Fatal("renderer mutated report")
	}
	if strings.Contains(got, "-0.00") {
		t.Errorf("rounded negative zero in:\n%s", got)
	}
	var decoded struct {
		Sections []struct {
			Rows []struct {
				Changes struct {
					TotalMs json.Number `json:"total_ms"`
					MeanMs  float64     `json:"mean_ms"`
				} `json:"changes"`
			} `json:"rows"`
		} `json:"sections"`
	}
	decoder := json.NewDecoder(bytes.NewReader(jsonOut.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Sections[0].Rows[0].Changes.TotalMs.String(); got != "18446744073709551615" {
		t.Fatalf("JSON total delta=%s", got)
	}
	if got := decoded.Sections[0].Rows[0].Changes.MeanMs; got != -0.001 {
		t.Fatalf("JSON mean delta=%v", got)
	}
}

func comparisonTextReport(t *testing.T, before, after model.ComparisonInput) ComparisonReport {
	t.Helper()
	comparison, err := model.Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	quality := model.CaptureQuality{RPC: model.TierQuality{Records: 99, Admitted: 88, Rejected: 11}}
	return ComparisonReport{Before: Report{Bytes: 12, Quality: quality, Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, After: Report{Bytes: 34, Quality: quality, Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, Data: comparison}
}
