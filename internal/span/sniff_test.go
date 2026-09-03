package span

import (
	"fmt"
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

// TestSnifferReportedRequiresResponseMarker guards against DurationFields
// counting a tf_req_duration_ms field wherever it appears, rather than only
// on the response entries ReportedBuilder actually builds spans from. A
// non-response line carrying the field must not report reported-usable.
func TestSnifferReportedRequiresResponseMarker(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Some other message: tf_req_duration_ms=5` + "\n"
	c := sniff(t, in)
	if c.DurationFields != 0 {
		t.Errorf("DurationFields = %d, want 0 for a duration field on a non-response line", c.DurationFields)
	}
	if f, ok := c.BestFidelity(); ok {
		t.Errorf("BestFidelity = %v, %v; want no usable tier for a duration field on a non-response line", f, ok)
	}
}

// TestSnifferPairedRequiresMatchingReqID guards against the Paired gate
// firing on the mere presence of requests, responses and req-id fields
// without checking any id actually appears on both sides. Mismatched ids
// give nothing correlatable, so the log must not be reported paired-usable
// -- though it still has a request and a response, so it remains
// sequential-usable.
func TestSnifferPairedRequiresMatchingReqID(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_req_id=AAA tf_rpc=ReadResource\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_req_id=BBB tf_rpc=ReadResource\n"
	c := sniff(t, in)
	if c.CorrelatedReqIDs != 0 {
		t.Errorf("CorrelatedReqIDs = %d, want 0 for mismatched request/response ids", c.CorrelatedReqIDs)
	}
	if f, ok := c.BestFidelity(); !ok || f != FidelitySequential {
		t.Errorf("BestFidelity = %v, %v; want sequential, true for mismatched ids (not paired)", f, ok)
	}
}

// TestSnifferPairedAcceptsMatchingReqID is the positive counterpart: a
// request and response sharing the same tf_req_id is genuinely correlatable
// and must report paired-usable, with CorrelatedReqIDs counting it.
func TestSnifferPairedAcceptsMatchingReqID(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_req_id=AAA tf_rpc=ReadResource\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_req_id=AAA tf_rpc=ReadResource\n"
	c := sniff(t, in)
	if c.CorrelatedReqIDs != 1 {
		t.Errorf("CorrelatedReqIDs = %d, want 1 for matching request/response ids", c.CorrelatedReqIDs)
	}
	if f, ok := c.BestFidelity(); !ok || f != FidelityPaired {
		t.Errorf("BestFidelity = %v, %v; want paired, true for matching ids", f, ok)
	}
}

// TestSnifferSequentialRequiresRequestEntry guards against the Sequential
// gate firing on a response with no corresponding request line: pairing
// within one plugin stream needs both sides.
func TestSnifferSequentialRequiresRequestEntry(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_req_id=AAA tf_rpc=ReadResource\n"
	c := sniff(t, in)
	if f, ok := c.BestFidelity(); ok {
		t.Errorf("BestFidelity = %v, %v; want no usable tier for a response with no request", f, ok)
	}
}

// TestSnifferInferredRequiresMultipleProviderEntries guards against the
// Inferred gate firing on a single provider entry: a wall-clock gap needs at
// least two events to have an interval between them.
func TestSnifferInferredRequiresMultipleProviderEntries(t *testing.T) {
	one := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: some noise` + "\n"
	c := sniff(t, one)
	if f, ok := c.BestFidelity(); ok {
		t.Errorf("BestFidelity = %v, %v; want no usable tier for a single provider entry", f, ok)
	}

	two := one + `2022-12-15T00:16:20.900Z [TRACE] provider.aws: more noise` + "\n"
	c2 := sniff(t, two)
	if f, ok := c2.BestFidelity(); !ok || f != FidelityInferred {
		t.Errorf("BestFidelity = %v, %v; want inferred, true for two provider entries", f, ok)
	}
}

// TestSnifferCorrelationCapBounded checks that request-id correlation stays
// bounded on a log with more distinct ids than maxTrackedReqIDs: memory use
// must not grow without limit, and ids past the cap must simply not
// correlate rather than panicking or corrupting the count.
func TestSnifferCorrelationCapBounded(t *testing.T) {
	const n = maxTrackedReqIDs + 1
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_req_id=id%d tf_rpc=ReadResource\n", i)
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_req_id=id%d tf_rpc=ReadResource\n", i)
	}
	c := sniff(t, b.String())
	if c.CorrelatedReqIDs != maxTrackedReqIDs {
		t.Errorf("CorrelatedReqIDs = %d, want %d (capped)", c.CorrelatedReqIDs, maxTrackedReqIDs)
	}
	if f, ok := c.BestFidelity(); !ok || f != FidelityPaired {
		t.Errorf("BestFidelity = %v, %v; want paired, true", f, ok)
	}
}
