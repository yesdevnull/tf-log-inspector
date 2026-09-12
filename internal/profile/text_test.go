package profile

import (
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestTextProfileOpensExactObservationEvidence(t *testing.T) {
	l, err := model.Load("../../testdata/resources-accounting.log")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Render(&out, l, TextOptions{Limit: 0}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"aws_instance.a", "contained", "source: line 4", "source: line 5", "observed resource"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in report:\n%s", want, got)
		}
	}
}

func TestTextProfileRendersSourceConfidenceAndUntrustedIdentity(t *testing.T) {
	report := Report{
		HasContext: true,
		RPC: []Observation{
			{Span: span.Span{DurationMs: 12, RPC: "Read\nResource", ResourceType: "type\x1b[2J", Provider: "provider\nname"}, Source: &model.SourceLocation{StartLine: 12, EndLine: 14}, Attribution: attrib.Attribution{Address: "module.web.aws_instance.app[0]\x1b[2J", Confidence: attrib.Contained}},
			{Span: span.Span{DurationMs: 11, RPC: "ReadResource", ResourceType: "type", Provider: "provider"}, Source: &model.SourceLocation{StartLine: 18, EndLine: 18}, Attribution: attrib.Attribution{Confidence: attrib.Ambiguous, Candidates: 2}},
			{Span: span.Span{DurationMs: 10, RPC: "ReadResource", ResourceType: "type", Provider: "provider"}, Attribution: attrib.Attribution{Confidence: attrib.Unattributed}},
			{Span: span.Span{DurationMs: 9, RPC: "ReadResource", ResourceType: "type", Provider: "provider"}, Attribution: attrib.Attribution{Address: "likely.address", Confidence: attrib.Likely}},
			{Span: span.Span{DurationMs: 8, RPC: "ReadResource", ResourceType: "type", Provider: "provider"}, Attribution: attrib.Attribution{Address: "overlap.address", Confidence: attrib.Overlapping}},
		},
		RPCRanking: []int{0, 1, 2, 3, 4},
	}
	var out strings.Builder
	if err := renderReport(&out, report, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`Read\nResource`, `type\x1b[2J`, `provider\nname`, `module.web.aws_instance.app[0]\x1b[2J (contained)`,
		"source: lines 12-14", "source: line 18", "source: unavailable", "resource: ambiguous (2 candidates)",
		"resource: unattributed", "likely.address (likely)", "overlap.address (overlapping)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b") {
		t.Fatalf("report contains terminal controls: %q", got)
	}
}

func TestTextProfileRendersUILowerBoundsAndObservedIdentity(t *testing.T) {
	report := Report{
		UI:        []Observation{{Span: span.Span{DurationMs: math.MaxUint32, DurationSaturated: true, RPC: "apply", ResourceType: "aws_instance", Address: "aws_instance.example", Provider: "aws", Fidelity: span.FidelityUIReported}, Source: &model.SourceLocation{StartLine: 24, EndLine: 24}}},
		UIRanking: []int{0},
		Types:     []TypeSummary{{TypeRow: model.TypeRow{ResourceType: "aws_instance", UIResources: 1, UITotalMs: math.MaxUint32, UILowerBound: true}}},
	}
	var out strings.Builder
	if err := renderReport(&out, report, TextOptions{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"≥4294967.3s", "resource: aws_instance.example (observed resource)", "source: line 24"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestTextProfileLimitsListsIndependentlyWithoutChangingTotals(t *testing.T) {
	report := Report{Providers: []model.Bucket{{Key: "p1", Count: 2, TotalMs: 30}, {Key: "p2", Count: 1, TotalMs: 20}}, Types: []TypeSummary{{TypeRow: model.TypeRow{ResourceType: "t1", RPCCalls: 2, RPCTotalMs: 30}}, {TypeRow: model.TypeRow{ResourceType: "t2", RPCCalls: 1, RPCTotalMs: 20}}}}
	for i := 0; i < 21; i++ {
		report.RPC = append(report.RPC, Observation{Span: span.Span{DurationMs: uint32(30 - i), RPC: "rpc" + string(rune('A'+i)), ResourceType: "t1", Provider: "p1"}})
		report.RPCRanking = append(report.RPCRanking, i)
	}
	var limited, all strings.Builder
	if err := renderReport(&limited, report, TextOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if err := renderReport(&all, report, TextOptions{Limit: 0}); err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{"BY RESOURCE TYPE (top 1 of 2)", "BY PROVIDER (top 1 of 2)", "SLOWEST CALLS (top 1 of 21)"} {
		if !strings.Contains(limited.String(), heading) {
			t.Errorf("missing independent limit heading %q:\n%s", heading, limited.String())
		}
	}
	if strings.Contains(limited.String(), "rpcB") || !strings.Contains(all.String(), "rpcB") || strings.Contains(all.String(), "top ") {
		t.Fatalf("limit did not apply independently:\nLIMITED:\n%s\nALL:\n%s", limited.String(), all.String())
	}
	if !strings.Contains(limited.String(), "30ms") {
		t.Errorf("limited report lost full numerical summary:\n%s", limited.String())
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestTextProfileValidatesLimitsAndPropagatesWriteFailures(t *testing.T) {
	if err := Render(io.Discard, &model.Log{}, TextOptions{Limit: -1}); err == nil || err.Error() != "profile limit must be non-negative" {
		t.Fatalf("Render negative limit error = %v", err)
	}
	if err := renderReport(io.Discard, Report{}, TextOptions{Limit: -1}); err == nil || err.Error() != "profile limit must be non-negative" {
		t.Fatalf("renderReport negative limit error = %v", err)
	}
	if err := renderReport(shortWriter{}, Report{}, TextOptions{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v, want %v", err, io.ErrShortWrite)
	}
}
