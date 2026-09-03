package span

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func scanInto(t *testing.T, in string, b *ReportedBuilder) {
	t.Helper()
	var comps logfmt.Interner
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, b); err != nil {
		t.Fatalf("Scan: %v", err)
	}
}

func TestReportedBuilderReadsDuration(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=abc tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5000` + "\n"

	var b ReportedBuilder
	scanInto(t, in, &b)

	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	s := got[0]
	if s.RPC != "ApplyResourceChange" {
		t.Errorf("RPC = %q", s.RPC)
	}
	if s.ResourceType != "aws_subnet" {
		t.Errorf("ResourceType = %q", s.ResourceType)
	}
	if s.Provider != "registry.terraform.io/hashicorp/aws" {
		t.Errorf("Provider = %q", s.Provider)
	}
	if s.Fidelity != FidelityReported {
		t.Errorf("Fidelity = %v, want reported", s.Fidelity)
	}
	// The reported duration survives even though the span sits at the base
	// timestamp and its start is clamped.
	if s.DurationMs != 5000 {
		t.Errorf("DurationMs = %d, want 5000", s.DurationMs)
	}
	if !s.StartClamped {
		t.Error("StartClamped = false, want true")
	}
	if s.StartMs != 0 {
		t.Errorf("StartMs = %d, want 0", s.StartMs)
	}
}

func TestReportedBuilderUnclampedStart(t *testing.T) {
	in := "2022-12-15T00:16:20.000Z [TRACE] provider.aws: first\n" +
		`2022-12-15T00:16:30.000Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=4000` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].StartMs != 6000 {
		t.Errorf("StartMs = %d, want 6000", got[0].StartMs)
	}
	if got[0].EndMs != 10000 {
		t.Errorf("EndMs = %d, want 10000", got[0].EndMs)
	}
	if got[0].StartClamped {
		t.Error("StartClamped = true, want false")
	}
}

func TestReportedBuilderUsesDataSourceType(t *testing.T) {
	in := `2022-12-15T00:16:25.900Z [TRACE] provider.aws: Received downstream response: tf_data_source_type=aws_ami tf_rpc=ReadDataSource tf_req_duration_ms=4200` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].ResourceType != "aws_ami" {
		t.Errorf("ResourceType = %q, want aws_ami", got[0].ResourceType)
	}
}

// A response line followed by plan output must still yield its span: field
// parsing must not fuse the last field with the next line's first token.
func TestReportedBuilderSurvivesFollowingContinuation(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=12` + "\n" +
		"Terraform used the selected providers to generate the following\n" +
		"  + create\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].DurationMs != 12 {
		t.Errorf("DurationMs = %d, want 12", got[0].DurationMs)
	}
}

func TestReportedBuilderIgnoresEntriesWithoutDuration(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_rpc=ReadResource\n" +
		"2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("got %d spans, want 0", len(got))
	}
}

func TestReportedBuilderIgnoresUnparseableDuration(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=notanumber` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("got %d spans, want 0", len(got))
	}
}

func TestReportedBuilderRecordsEntryOrdinal(t *testing.T) {
	in := "2022-12-15T00:16:20.700Z [TRACE] a: filler\n" +
		`2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].Entry != 1 {
		t.Errorf("Entry = %d, want 1", got[0].Entry)
	}
}
