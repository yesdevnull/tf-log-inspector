package profile

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func render(t *testing.T, path string) string {
	t.Helper()
	l, err := model.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var sb strings.Builder
	if err := Render(&sb, l); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return sb.String()
}

// Unlike --diagnose, this report shows real addresses. A reader who assumes
// otherwise could paste a confidential resource address somewhere it does not
// belong, so the warning is part of the contract, not decoration.
func TestReportWarnsThatOutputIsUnmasked(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "not masked") {
		t.Errorf("report does not warn that it is unmasked:\n%s", out)
	}
}

func TestReportShowsResourceTypeJoin(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "BY RESOURCE TYPE") {
		t.Errorf("report missing the resource-type join:\n%s", out)
	}
	if !strings.Contains(out, "aws_instance") {
		t.Errorf("report does not name a resource type present in the fixture:\n%s", out)
	}
}

// UI-hook figures are whole seconds carrying up to a second of error each, so
// a report that ranks them must say so -- the same caveat --diagnose carries.
func TestReportStatesUIHookResolution(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "whole seconds") {
		t.Errorf("report does not state the UI-hook timing resolution:\n%s", out)
	}
}

func TestReportShowsProviderRollup(t *testing.T) {
	out := render(t, "../../testdata/provider-rpc.log")
	if !strings.Contains(out, "BY PROVIDER") {
		t.Errorf("report missing the provider rollup:\n%s", out)
	}
}

func TestReportShowsConcurrencyForRPCSpans(t *testing.T) {
	out := render(t, "../../testdata/provider-rpc.log")
	if !strings.Contains(out, "CONCURRENCY") {
		t.Errorf("report missing the concurrency section:\n%s", out)
	}
	if !strings.Contains(out, "peak") {
		t.Errorf("concurrency section does not report a peak:\n%s", out)
	}
}

// A log with neither tier must say so rather than printing empty headings.
func TestReportSaysSoWhenThereAreNoSpans(t *testing.T) {
	out := render(t, "../../testdata/core-only.log")
	if !strings.Contains(out, "no spans") {
		t.Errorf("report does not explain an empty result:\n%s", out)
	}
}

func TestTruncateLeavesShortStringsAlone(t *testing.T) {
	if got := truncate("aws_instance", 24); got != "aws_instance" {
		t.Errorf("truncate = %q, want unchanged", got)
	}
}

func TestTruncateEllipsizesLongStrings(t *testing.T) {
	long := "azuread_application_federated_identity_credential"
	got := truncate(long, 24)
	if len(got) != 24 {
		t.Errorf("truncate(%q, 24) = %q (len %d), want len 24", long, got, len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncate(%q, 24) = %q, want it to end with an ellipsis", long, got)
	}
}

// A resource type wider than its column must not shunt the columns that
// follow it out of alignment: every rendered row of the table, including the
// header, must come out the same length.
func TestReportKeepsColumnsAlignedWithLongResourceType(t *testing.T) {
	l := &model.Log{
		RPCSpans: []span.Span{
			{
				DurationMs:   100,
				RPC:          "PlanResourceChange",
				Provider:     "registry.terraform.io/hashicorp/azuread",
				ResourceType: "azuread_application_federated_identity_credential",
			},
			{
				DurationMs:   50,
				RPC:          "ApplyResourceChange",
				Provider:     "registry.terraform.io/hashicorp/aws",
				ResourceType: "aws_instance",
			},
		},
	}
	var sb strings.Builder
	if err := Render(&sb, l); err != nil {
		t.Fatalf("Render: %v", err)
	}
	rows := tableRows(t, sb.String(), "BY RESOURCE TYPE")
	want := len(rows[0])
	for i, row := range rows {
		if len(row) != want {
			t.Errorf("row %d length = %d, want %d (header's length):\n%s", i, len(row), want, sb.String())
		}
	}
}

// Two resource types sharing a long common prefix must not collide into one
// truncated label -- BY RESOURCE TYPE's resource-type column is the only
// thing distinguishing its rows, unlike SLOWEST CALLS/RESOURCES, which still
// have an address or provider column to fall back on.
func TestReportDistinguishesResourceTypesWithLongCommonPrefix(t *testing.T) {
	l := &model.Log{
		RPCSpans: []span.Span{
			{DurationMs: 10, RPC: "PlanResourceChange", Provider: "registry.terraform.io/hashicorp/azuread", ResourceType: "azuread_service_principal_password"},
			{DurationMs: 20, RPC: "PlanResourceChange", Provider: "registry.terraform.io/hashicorp/azuread", ResourceType: "azuread_service_principal_certificate"},
		},
	}
	var sb strings.Builder
	if err := Render(&sb, l); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := sb.String()
	for _, want := range []string{"azuread_service_principal_password", "azuread_service_principal_certificate"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not render %q as a distinguishable label:\n%s", want, out)
		}
	}
}

// A span whose start is clamped contributes duration but no measurable
// concurrency (see writeConcurrency), which can make peak concurrency read
// as unexpectedly low next to a nonzero span count. The report must explain
// that, but only when it is actually true.
func TestReportNotesClampedStartsUnderConcurrency(t *testing.T) {
	l := &model.Log{
		RPCSpans: []span.Span{
			{DurationMs: 12, StartMs: 0, EndMs: 0, StartClamped: true, RPC: "PlanResourceChange", Provider: "registry.terraform.io/hashicorp/aws", ResourceType: "aws_subnet"},
		},
	}
	var sb strings.Builder
	if err := Render(&sb, l); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(sb.String(), "clamped") {
		t.Errorf("report does not explain a clamped start:\n%s", sb.String())
	}
}

func TestReportOmitsClampedNoteWhenNoSpanIsClamped(t *testing.T) {
	l := &model.Log{
		RPCSpans: []span.Span{
			{DurationMs: 12, StartMs: 0, EndMs: 12, StartClamped: false, RPC: "PlanResourceChange", Provider: "registry.terraform.io/hashicorp/aws", ResourceType: "aws_subnet"},
		},
	}
	var sb strings.Builder
	if err := Render(&sb, l); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(sb.String(), "clamped") {
		t.Errorf("report explains a clamped start when none is clamped:\n%s", sb.String())
	}
}

// tableRows returns the lines directly under a section header, up to the
// first blank line -- the column header row plus every data row.
func tableRows(t *testing.T, out, header string) []string {
	t.Helper()
	idx := strings.Index(out, header+"\n")
	if idx < 0 {
		t.Fatalf("section %q not found in:\n%s", header, out)
	}
	rest := out[idx+len(header)+1:]
	end := strings.Index(rest, "\n\n")
	if end < 0 {
		end = len(rest)
	}
	return strings.Split(strings.TrimRight(rest[:end], "\n"), "\n")
}
