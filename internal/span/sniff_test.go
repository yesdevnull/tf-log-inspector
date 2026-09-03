package span

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func sniff(t *testing.T, in string) Capabilities {
	t.Helper()
	var comps logfmt.Interner
	s := NewSniffer(&comps)
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, s); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return s.Report()
}

func TestSnifferDetectsReportedTier(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_req_id=abc tf_rpc=ReadResource tf_req_duration_ms=5` + "\n"
	c := sniff(t, in)
	if c.ResponseEntries != 1 {
		t.Errorf("ResponseEntries = %d, want 1", c.ResponseEntries)
	}
	if c.DurationFields != 1 {
		t.Errorf("DurationFields = %d, want 1", c.DurationFields)
	}
	if c.ProviderEntries != 1 {
		t.Errorf("ProviderEntries = %d, want 1", c.ProviderEntries)
	}
	if f, ok := c.BestFidelity(); !ok || f != FidelityReported {
		t.Errorf("BestFidelity = %v, %v; want reported, true", f, ok)
	}
}

func TestSnifferFallsBackToPairedWhenNoDurations(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_req_id=abc tf_rpc=ReadResource\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_req_id=abc tf_rpc=ReadResource\n"
	c := sniff(t, in)
	if c.RequestEntries != 1 {
		t.Errorf("RequestEntries = %d, want 1", c.RequestEntries)
	}
	if f, ok := c.BestFidelity(); !ok || f != FidelityPaired {
		t.Errorf("BestFidelity = %v, %v; want paired, true", f, ok)
	}
}

func TestSnifferReportsNothingUsableForCoreOnlyLog(t *testing.T) {
	in := "2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n" +
		`2026-08-29T10:34:43.152+0200 [TRACE] vertex "aws_subnet.a": visit complete` + "\n" +
		"2026-08-29T10:34:43.153+0200 [TRACE] GRPCProvider: GetProviderSchema\n"
	c := sniff(t, in)
	if c.ProviderEntries != 0 {
		t.Errorf("ProviderEntries = %d, want 0", c.ProviderEntries)
	}
	if c.CoreVertexLines != 1 {
		t.Errorf("CoreVertexLines = %d, want 1", c.CoreVertexLines)
	}
	if c.CoreGRPCLines != 1 {
		t.Errorf("CoreGRPCLines = %d, want 1", c.CoreGRPCLines)
	}
	if _, ok := c.BestFidelity(); ok {
		t.Error("BestFidelity reported a usable tier for a core-only log")
	}
}
