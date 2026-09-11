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
)

func TestComparisonTextRendersQualifiedMetrics(t *testing.T) {
	meanBefore, meanAfter, meanDelta := 10.0, 20.0, 10.0
	maxBefore, maxAfter := uint32(10), uint32(20)
	minusOne := model.SignedChange{Negative: true, Magnitude: 1}
	zero := model.SignedChange{}
	plusTen := model.SignedChange{Magnitude: 10}
	minus50, plus100, roundedZero := -50.0, 100.0, -0.001
	report := comparisonTextReport([]model.ComparisonSection{{
		Kind: "rpc_methods", Tier: "rpc", BeforeAvailable: true, AfterAvailable: true,
		Rows: []model.ComparisonRow{{
			Key: model.ComparisonKey{Provider: "p\x1b", ResourceType: "r\n", Method: "Read\t"}, State: "matched",
			Before: &model.ComparisonTotal{Count: 2, TotalMs: 20, MeanMs: &meanBefore, MaxMs: &maxBefore},
			After:  &model.ComparisonTotal{Count: 1, TotalMs: 20, MeanMs: &meanAfter, MaxMs: &maxAfter},
			Changes: model.ComparisonChanges{Count: &minusOne, TotalMs: &zero, MeanMs: &meanDelta, MaxMs: &plusTen,
				CountPercent: &minus50, TotalPercent: &roundedZero, MeanPercent: &plus100, MaxPercent: &plus100},
		}},
	}})
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
	if strings.Contains(got, "-0.00") {
		t.Errorf("rounded negative zero in:\n%s", got)
	}
}

func TestComparisonTextLowerBoundsNullsAndUnavailable(t *testing.T) {
	mean := 0.4
	max := uint32(1)
	report := comparisonTextReport([]model.ComparisonSection{{Kind: "ui_operations", Tier: "ui", BeforeAvailable: true, AfterAvailable: true, Rows: []model.ComparisonRow{{
		Key: model.ComparisonKey{Address: "addr\r", Action: "apply\b"}, State: "matched",
		Before: &model.ComparisonTotal{Count: 1, TotalMs: 0, MeanMs: &mean, MaxMs: &max, LowerBound: true}, After: &model.ComparisonTotal{Count: 2, TotalMs: 1, MeanMs: &mean, MaxMs: &max},
		Changes: model.ComparisonChanges{Count: &model.SignedChange{Magnitude: 1}},
	}}}, {Kind: "rpc_providers", Tier: "rpc", Rows: []model.ComparisonRow{{Key: model.ComparisonKey{Provider: "missing"}, State: "unavailable"}}}})
	var out bytes.Buffer
	if err := RenderComparisonText(&out, report, ComparisonMetadata{}, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"UNRANKED / UNAVAILABLE", "address=addr\\r  action=apply\\b", ">=0", ">=0.40", ">=1", "timing deltas unavailable: lower bound", "n/a", "before unavailable; after unavailable", "UI durations are rounded by up to one second per observation", "missing addresses cannot be matched"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestComparisonTextLimitsListsIndependently(t *testing.T) {
	rows := make([]model.ComparisonRow, 0, 42)
	for i := 0; i < 21; i++ {
		change := model.SignedChange{Magnitude: uint64(21 - i)}
		rows = append(rows, model.ComparisonRow{Key: model.ComparisonKey{Provider: "exact" + string(rune('A'+i))}, State: "matched", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{}, Changes: model.ComparisonChanges{TotalMs: &change}})
	}
	for i := 0; i < 21; i++ {
		rows = append(rows, model.ComparisonRow{Key: model.ComparisonKey{Provider: "unranked" + string(rune('A'+i))}, State: "unavailable"})
	}
	report := comparisonTextReport([]model.ComparisonSection{{Kind: "rpc_providers", Tier: "rpc", BeforeAvailable: true, AfterAvailable: true, Rows: rows}})
	for _, tc := range []struct {
		limit           int
		exact, unranked int
	}{{1, 1, 1}, {20, 20, 20}, {0, 21, 21}} {
		var out bytes.Buffer
		if err := RenderComparisonText(&out, report, ComparisonMetadata{}, TextOptions{Limit: tc.limit}); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if strings.Count(got, "matched: provider=exact") != tc.exact || strings.Count(got, "unavailable: provider=unranked") != tc.unranked {
			t.Errorf("limit %d counts wrong", tc.limit)
		}
		if tc.limit > 0 && !strings.Contains(got, "shown "+strconv.Itoa(tc.limit)+" of 21") {
			t.Errorf("limit label absent in:\n%s", got)
		}
		if strings.Count(got, "RPC timing records     99: admitted 88, rejected 11") != 2 {
			t.Errorf("quality totals were absent or duplicated at limit %d", tc.limit)
		}
	}
}

func TestComparisonTextValidationAndWriterFailures(t *testing.T) {
	report := comparisonTextReport(nil)
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
	mean := 1.234
	longProvider := strings.Repeat("very-long-provider-", 12) + "end"
	report := comparisonTextReport([]model.ComparisonSection{
		{Kind: "rpc_providers", Tier: "rpc", BeforeAvailable: true, AfterAvailable: true, Rows: []model.ComparisonRow{{Key: model.ComparisonKey{Provider: "provider\a" + longProvider}, State: "added", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{}, Changes: model.ComparisonChanges{TotalMs: &large, MeanMs: &mean}}}},
		{Kind: "rpc_resource_types", Tier: "rpc", BeforeAvailable: true, AfterAvailable: true, Rows: []model.ComparisonRow{{Key: model.ComparisonKey{ResourceType: "rpc-type\v"}, State: "removed", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{}, Changes: model.ComparisonChanges{TotalMs: &large}}}},
		{Kind: "ui_resource_types", Tier: "ui", BeforeAvailable: true, AfterAvailable: true, Rows: []model.ComparisonRow{{Key: model.ComparisonKey{ResourceType: "ui-type\f"}, State: "matched", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{}, Changes: model.ComparisonChanges{TotalMs: &large}}}},
		{Kind: "ui_operations", Tier: "ui", BeforeAvailable: true, AfterAvailable: true, Rows: []model.ComparisonRow{{Key: model.ComparisonKey{Address: "address\x00", Action: "action\x7f"}, State: "matched", Before: &model.ComparisonTotal{}, After: &model.ComparisonTotal{}, Changes: model.ComparisonChanges{TotalMs: &large}}}},
		{Kind: "rpc_methods", Tier: "rpc"},
	})
	report.Data.BeforeProviders = []string{"provider-set\n"}
	report.Data.AfterProviders = []string{"other-set\t"}
	wantReport := report
	var textOut, jsonOut bytes.Buffer
	if err := RenderComparisonText(&textOut, report, ComparisonMetadata{}, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := RenderComparisonJSON(&jsonOut, report, ComparisonMetadata{}); err != nil {
		t.Fatal(err)
	}
	got := textOut.String()
	for _, want := range []string{"provider=provider\\a" + longProvider, "resource_type=rpc-type\\v", "resource_type=ui-type\\f", "address=address\\x00  action=action\\x7f", "provider-set\\n", "other-set\\t", "+18446744073709551615", "+1.23", "RPC METHODS AVAILABILITY\n  before: unavailable; after: unavailable\n  no rows"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !reflect.DeepEqual(report, wantReport) {
		t.Fatal("renderer mutated report")
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
	if got := decoded.Sections[0].Rows[0].Changes.MeanMs; got != 1.234 {
		t.Fatalf("JSON mean delta=%v", got)
	}
}

func comparisonTextReport(sections []model.ComparisonSection) ComparisonReport {
	quality := model.CaptureQuality{RPC: model.TierQuality{Records: 99, Admitted: 88, Rejected: 11}}
	return ComparisonReport{Before: Report{Bytes: 12, Quality: quality, Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, After: Report{Bytes: 34, Quality: quality, Reconstruction: model.ReconstructionQuality{State: "not_checked"}}, Data: model.Comparison{Sections: sections, BeforeProviders: []string{"before-provider"}, AfterProviders: []string{"after-provider"}, ProviderIdentityStatus: "different"}}
}
