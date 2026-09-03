package diagnose

import (
	"strings"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func build(t *testing.T, in string) Report {
	t.Helper()
	var comps logfmt.Interner
	c := NewCollector(&comps)
	sn := span.NewSniffer(&comps)
	var b span.ReportedBuilder
	st, err := logfmt.Scan(strings.NewReader(in), &comps, c, sn, &b)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return Build(st, sn.Report(), b.Spans(), c, &comps, 5*time.Millisecond)
}

func render(t *testing.T, r Report) string {
	t.Helper()
	var sb strings.Builder
	if err := r.Render(&sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return sb.String()
}

// Amendment 1 requires api_token to recur across entries before its key is
// printed, so the two entries below use distinct secret values -- neither
// may leak, and the recurring key must still be disclosed.
func TestReportNeverLeaksWellFormedFieldValues(t *testing.T) {
	const secret1 = "SUPERSECRETVALUE"
	const secret2 = "SUPERSECRETVALUE2"
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource api_token=` + secret1 + ` tf_req_duration_ms=5` + "\n" +
		`2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource api_token=` + secret2 + ` tf_req_duration_ms=9` + "\n"
	out := render(t, build(t, in))
	if strings.Contains(out, secret1) || strings.Contains(out, secret2) {
		t.Fatal("report leaked a well-formed field value")
	}
	if !strings.Contains(out, "api_token") {
		t.Error("field key api_token missing from report")
	}
}

// The hclog multi-line value shape. Body lines must contribute no fields and
// no template text, so nothing inside the body can reach the report.
func TestReportNeverLeaksMultilineValueBody(t *testing.T) {
	const secret = "IyEvYmluL2Jhc2gKZWNobyBodW50ZXIy"
	in := "2024-02-13T12:11:28.330+0100 [DEBUG] provider.aws: HTTP Response Received: @module=aws aws.operation=UpdateProject\n" +
		`  http.response.body=` + "\n" +
		`  | {"UserData":"` + secret + `==","Token":"AQoDYXdzEPT//x=="}` + "\n" +
		"2024-02-13T12:11:28.331+0100 [TRACE] provider.aws: Received downstream response: tf_rpc=ApplyResourceChange tf_req_duration_ms=10710\n"
	out := render(t, build(t, in))
	if strings.Contains(out, secret) {
		t.Fatal("report leaked an hclog multi-line value body")
	}
	if strings.Contains(out, "UserData") || strings.Contains(out, "AQoDYXdz") {
		t.Fatalf("report leaked body content:\n%s", out)
	}
}

// hclog escapes embedded quotes; the value's remainder must not be
// re-tokenised into printed keys.
func TestReportNeverLeaksEscapedQuoteContents(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: diagnostic_summary="bad header \"Authorization=Bearer s3cr3t\" here" tf_rpc=ReadResource tf_req_duration_ms=5` + "\n"
	out := render(t, build(t, in))
	if strings.Contains(out, "s3cr3t") || strings.Contains(out, "Authorization") {
		t.Fatalf("report leaked escaped-quote value contents:\n%s", out)
	}
}

func TestReportNeverLeaksResourceAddresses(t *testing.T) {
	in := `2026-08-29T10:34:43.219+0200 [ERROR] module.vpc["datacenter1"].aws_internet_gateway.this[0] encountered an error` + "\n" +
		"2026-08-29T10:34:43.220+0200 [DEBUG] terraform_data.r1: applying the planned Create change\n"
	out := render(t, build(t, in))
	for _, leak := range []string{"datacenter1", "internet_gateway", "terraform_data.r1"} {
		if strings.Contains(out, leak) {
			t.Errorf("report leaked %q:\n%s", leak, out)
		}
	}
}

func TestReportCountsFieldKeys(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=9\n"
	if got := build(t, in).FieldKeys["tf_req_duration_ms"]; got != 2 {
		t.Errorf("tf_req_duration_ms count = %d, want 2", got)
	}
}

func TestReportRecordsTierAndSpans(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5000` + "\n"
	r := build(t, in)
	if !r.TierUsable {
		t.Fatal("TierUsable = false, want true")
	}
	if r.Tier != span.FidelityReported {
		t.Errorf("Tier = %v, want reported", r.Tier)
	}
	if r.SpanCount != 1 {
		t.Errorf("SpanCount = %d, want 1", r.SpanCount)
	}
	// The reported duration must survive the start clamp.
	if r.SlowestMs != 5000 {
		t.Errorf("SlowestMs = %d, want 5000", r.SlowestMs)
	}
	if r.TotalSpanMs != 5000 {
		t.Errorf("TotalSpanMs = %d, want 5000", r.TotalSpanMs)
	}
}

func TestReportWarnsWhenNoProviderEntries(t *testing.T) {
	in := "2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n"
	r := build(t, in)
	if r.TierUsable {
		t.Error("TierUsable = true for a core-only log")
	}
	if out := render(t, r); !strings.Contains(out, "no provider RPC") {
		t.Errorf("report does not explain the absence of provider data:\n%s", out)
	}
}

func TestReportRendersThroughputAndWallClock(t *testing.T) {
	in := "2022-12-15T00:16:20.000Z [TRACE] a: one\n" +
		"2022-12-15T00:16:30.000Z [TRACE] a: two\n"
	out := render(t, build(t, in))
	for _, want := range []string{"log wall-clock", "parse throughput"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "10.0s") {
		t.Errorf("report does not show the 10s log span:\n%s", out)
	}
}

func TestReportCapsDistinctKeys(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxDistinct+500; i++ {
		sb.WriteString("2022-12-15T00:16:20.800Z [TRACE] a: message shape ")
		sb.WriteString(strings.Repeat("x", i%40+1))
		sb.WriteString("\n")
	}
	r := build(t, sb.String())
	if len(r.templateCount) > maxDistinct {
		t.Errorf("template map grew to %d, want at most %d", len(r.templateCount), maxDistinct)
	}
}

// --- Amendment 1: field keys are printed only when they recur. ---

func TestReportWithholdsFieldKeySeenOnce(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n"
	r := build(t, in)
	out := render(t, r)
	if strings.Contains(out, "tf_rpc") {
		t.Errorf("field key seen once was printed:\n%s", out)
	}
	if strings.Contains(out, "tf_req_duration_ms") {
		t.Errorf("field key seen once was printed:\n%s", out)
	}
	// tf_rpc and tf_req_duration_ms are each seen on exactly one entry.
	if r.WithheldFieldKeys != 2 {
		t.Errorf("WithheldFieldKeys = %d, want 2", r.WithheldFieldKeys)
	}
	if !strings.Contains(out, "2 key shapes seen only once (withheld)") {
		t.Errorf("report missing withheld field key count:\n%s", out)
	}
}

func TestReportPrintsFieldKeySeenOnTwoEntries(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=9\n"
	out := render(t, build(t, in))
	if !strings.Contains(out, "tf_rpc") {
		t.Errorf("field key seen on two entries missing from report:\n%s", out)
	}
	if !strings.Contains(out, "tf_req_duration_ms") {
		t.Errorf("field key seen on two entries missing from report:\n%s", out)
	}
}

func TestReportFieldKeyRepeatedWithinEntryNotPromoted(t *testing.T) {
	// tf_rpc appears twice on the same line. That must count as one
	// occurrence, not two, so a key repeated within a single entry cannot
	// promote itself into the printed set.
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_rpc=ReadResource tf_req_duration_ms=5\n"
	r := build(t, in)
	if got := r.FieldKeys["tf_rpc"]; got != 1 {
		t.Errorf("tf_rpc count = %d, want 1 (repetition within one entry must not double count)", got)
	}
	out := render(t, r)
	if strings.Contains(out, "tf_rpc") {
		t.Errorf("field key repeated within a single entry was wrongly promoted:\n%s", out)
	}
}

// --- Amendment 2: message templates are printed only when they recur. ---

func TestReportWithholdsTemplateSeenOnce(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: a one-off message shape\n"
	r := build(t, in)
	out := render(t, r)
	if r.WithheldTemplates != 1 {
		t.Errorf("WithheldTemplates = %d, want 1", r.WithheldTemplates)
	}
	if !strings.Contains(out, "1 message shapes seen only once (withheld)") {
		t.Errorf("report missing withheld template count:\n%s", out)
	}
	if strings.Contains(out, "one-off") {
		t.Errorf("singleton template text was printed:\n%s", out)
	}
}

func TestReportPrintsTemplateSeenOnTwoEntries(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: hello world\n" +
		"2022-12-15T00:16:20.900Z [TRACE] a: hello world\n"
	out := render(t, build(t, in))
	if !strings.Contains(out, "hello world") {
		t.Errorf("template seen on two entries missing from report:\n%s", out)
	}
}

func TestReportTemplateCountReflectsEntriesNotFields(t *testing.T) {
	// A single entry with several fields must contribute exactly one count
	// to its template, not one per field -- mirroring the field-key rule
	// that repetition inside one entry cannot inflate a shape's count.
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5 tf_provider_addr=x\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=9 tf_provider_addr=y\n"
	r := build(t, in)
	if len(r.templateCount) != 1 {
		t.Fatalf("templateCount has %d distinct shapes, want 1", len(r.templateCount))
	}
	for _, n := range r.templateCount {
		if n != 2 {
			t.Errorf("template count = %d, want 2 (one per entry, not one per field)", n)
		}
	}
}

// --- Amendment 3: CorrelatedReqIDs is rendered. ---

func TestReportRendersCorrelatedReqIDs(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_req_id=abc123` + "\n" +
		`2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_req_id=abc123 tf_rpc=ReadResource tf_req_duration_ms=5` + "\n"
	r := build(t, in)
	if r.Caps.CorrelatedReqIDs != 1 {
		t.Fatalf("CorrelatedReqIDs = %d, want 1", r.Caps.CorrelatedReqIDs)
	}
	out := render(t, r)
	if !strings.Contains(out, "correlated req ids") {
		t.Errorf("report missing correlated req ids line:\n%s", out)
	}
}

// --- Amendment 4: the duration fields label reflects the response-only counter. ---

func TestReportRelabelsDurationFieldsAsResponseOnly(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n"
	out := render(t, build(t, in))
	if !strings.Contains(out, "response duration fields") {
		t.Errorf("report does not label duration fields as response-only:\n%s", out)
	}
	if strings.Contains(out, "\n  duration fields") {
		t.Errorf("report still uses the old, inaccurate duration fields label:\n%s", out)
	}
}
