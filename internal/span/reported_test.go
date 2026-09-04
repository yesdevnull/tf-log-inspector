package span

import (
	"fmt"
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

// Retained strings must not pin the scanner's per-line buffers: a repeated
// value should be deduplicated into a single cache entry rather than cloned
// once per span.
func TestReportedBuilderDedupsRepeatedValues(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5` + "\n" +
		`2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=6` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2", len(got))
	}
	if got[0].RPC != "ReadResource" || got[1].RPC != "ReadResource" {
		t.Fatalf("RPC = %q, %q, want both %q", got[0].RPC, got[1].RPC, "ReadResource")
	}
	if n := len(b.kept); n != 1 {
		t.Errorf("dedup cache holds %d entries, want 1", n)
	}
}

// A retained string must survive the scanner reusing its per-line buffers for
// later lines. Without cloning, an earlier span's Provider would end up
// reading back as a later line's value once the scan completes.
func TestReportedBuilderRetainsAcrossScan(t *testing.T) {
	var sb strings.Builder
	var want []string
	for i := 0; i < 50; i++ {
		v := fmt.Sprintf("registry.terraform.io/hashicorp/provider-%03d-abcdefghijklmnopqrstuvwxyz", i)
		want = append(want, v)
		fmt.Fprintf(&sb, "2022-12-15T00:16:%02d.000Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_provider_addr=%s tf_req_duration_ms=%d\n", i, v, i)
	}
	var b ReportedBuilder
	scanInto(t, sb.String(), &b)
	got := b.Spans()
	if len(got) != len(want) {
		t.Fatalf("got %d spans, want %d", len(got), len(want))
	}
	for i, s := range got {
		if s.Provider != want[i] {
			t.Errorf("span %d Provider = %q, want %q", i, s.Provider, want[i])
		}
	}
}

// Past the dedup cap, values are still cloned individually and returned
// intact: correctness must not depend on the cache holding every value.
func TestReportedBuilderPastCapStillCorrect(t *testing.T) {
	var sb strings.Builder
	n := maxDistinctValues + 5
	want := make([]string, n)
	for i := 0; i < n; i++ {
		v := fmt.Sprintf("registry.terraform.io/hashicorp/provider-%05d", i)
		want[i] = v
		fmt.Fprintf(&sb, "2022-12-15T00:16:20.000Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_provider_addr=%s tf_req_duration_ms=1\n", v)
	}
	var b ReportedBuilder
	scanInto(t, sb.String(), &b)
	got := b.Spans()
	if len(got) != n {
		t.Fatalf("got %d spans, want %d", len(got), n)
	}
	for i, s := range got {
		if s.Provider != want[i] {
			t.Fatalf("span %d Provider = %q, want %q", i, s.Provider, want[i])
		}
	}
	if len(b.kept) != maxDistinctValues {
		t.Errorf("dedup cache holds %d entries, want %d (capped)", len(b.kept), maxDistinctValues)
	}
}

// A span whose duration exactly equals its offset from the base started at
// the base, so its zero start is arithmetic rather than a clamp. Reporting
// it as clamped inflates an anomaly counter with a non-anomaly, and an
// anomaly signal that cries wolf is worse than none.
func TestReportedBuilderExactBaseStartIsNotClamped(t *testing.T) {
	in := "2022-12-15T00:16:20.000Z [TRACE] provider.aws: first\n" +
		`2022-12-15T00:16:30.000Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=10000` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].StartMs != 0 {
		t.Errorf("StartMs = %d, want 0", got[0].StartMs)
	}
	if got[0].StartClamped {
		t.Error("StartClamped = true, want false: the start is exactly the base, not clamped")
	}
}

// scanIntoWithComps is scanInto plus the interner the builder needs to resolve
// a component, which is what the provider-address fallback reads.
func scanIntoWithComps(t *testing.T, in string, b *ReportedBuilder) {
	t.Helper()
	var comps logfmt.Interner
	b.Comps = &comps
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, b); err != nil {
		t.Fatalf("Scan: %v", err)
	}
}

// An SDKv2 provider served without ProviderAddr reports the bare string
// "provider" as its tf_provider_addr -- measured on
// terraform-provider-github v6.3.1 across 1,104 lines of a real HCP log, and
// fixed upstream only in v6.13.0. Left alone, two such providers in one plan
// would collapse into a single rollup row and attribute one's time to the
// other. The component names the plugin binary unambiguously, so it stands in.
func TestReportedBuilderFallsBackToComponentForBareProviderAddr(t *testing.T) {
	in := `2026-09-04T12:53:23.000+1000 [TRACE] provider.terraform-provider-github_v6.3.1: Received downstream response: tf_rpc=ReadResource tf_provider_addr=provider tf_req_duration_ms=1500` + "\n"
	var b ReportedBuilder
	scanIntoWithComps(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].Provider != "provider.terraform-provider-github_v6.3.1" {
		t.Errorf("Provider = %q, want the component to stand in for the bare address", got[0].Provider)
	}
}

// A real registry address must never be replaced. The fallback exists for the
// one useless value, not as a general preference for the component.
func TestReportedBuilderKeepsRealProviderAddr(t *testing.T) {
	in := `2026-09-04T12:53:23.000+1000 [TRACE] provider.terraform-provider-azurerm_v4.81.0_x5: Received downstream response: tf_rpc=ReadResource tf_provider_addr=registry.terraform.io/hashicorp/azurerm tf_req_duration_ms=1500` + "\n"
	var b ReportedBuilder
	scanIntoWithComps(t, in, &b)
	if got := b.Spans(); got[0].Provider != "registry.terraform.io/hashicorp/azurerm" {
		t.Errorf("Provider = %q, want the address unchanged", got[0].Provider)
	}
}

// The zero-value builder must keep working: Comps is optional, and without it
// the fallback is simply unavailable rather than a nil dereference.
func TestReportedBuilderWithoutInternerLeavesBareAddrAlone(t *testing.T) {
	in := `2026-09-04T12:53:23.000+1000 [TRACE] provider.terraform-provider-github_v6.3.1: Received downstream response: tf_rpc=ReadResource tf_provider_addr=provider tf_req_duration_ms=1500` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	if got := b.Spans(); got[0].Provider != "provider" {
		t.Errorf("Provider = %q, want the raw value when no interner is set", got[0].Provider)
	}
}
