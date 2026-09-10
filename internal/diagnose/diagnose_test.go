package diagnose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCaptureQualitySummaryPreservesWriterErrors(t *testing.T) {
	want := errors.New("writer failed")
	if err := (Report{}).Render(failingWriter{err: want}); !errors.Is(err, want) {
		t.Fatalf("Render error = %v, want %v", err, want)
	}
}

// fixture mirrors internal/model/log_test.go's helper of the same name --
// this package has its own testdata-relative path from its own directory,
// so the two cannot share one definition.
func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", name)
}

func build(t *testing.T, in string) Report {
	t.Helper()
	var comps, reqIDs logfmt.Interner
	c := NewCollector(&comps)
	sn := span.NewSniffer(&comps)
	var b span.ReportedBuilder
	var ui span.UIHookBuilder
	var cc attrib.ContextCollector
	st, err := logfmt.Scan(strings.NewReader(in), &comps, &reqIDs, c, sn, &b, &ui, &cc)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	quality := qualityFromScan(st, sn.Report(), &b, &ui, &cc, &comps, &reqIDs)
	return Build(st, sn.Report(), b.Spans(), ui.Spans(), quality,
		ui.Malformed(), ui.BackwardsTimestamps(), ui.Saturated(), &cc,
		c, &comps, 5*time.Millisecond)
}

func render(t *testing.T, r Report) string {
	t.Helper()
	var sb strings.Builder
	if err := r.Render(&sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return sb.String()
}

// renderFixture loads path through the same sinks runDiagnose wires
// together and returns the rendered report, for tests that need a real
// fixture file rather than an inline literal.
func renderFixture(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})

	var comps, reqIDs logfmt.Interner
	c := NewCollector(&comps)
	sn := span.NewSniffer(&comps)
	var b span.ReportedBuilder
	var ui span.UIHookBuilder
	var cc attrib.ContextCollector
	st, err := logfmt.Scan(f, &comps, &reqIDs, c, sn, &b, &ui, &cc)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	quality := qualityFromScan(st, sn.Report(), &b, &ui, &cc, &comps, &reqIDs)
	r := Build(st, sn.Report(), b.Spans(), ui.Spans(), quality,
		ui.Malformed(), ui.BackwardsTimestamps(), ui.Saturated(), &cc,
		c, &comps, 5*time.Millisecond)
	return render(t, r)
}

func qualityFromScan(st logfmt.Stats, caps span.Capabilities, b *span.ReportedBuilder, ui *span.UIHookBuilder, cc *attrib.ContextCollector, comps, reqIDs *logfmt.Interner) model.CaptureQuality {
	rpcSpans := b.Spans()
	contexts := cc.Contexts()
	var attributions []attrib.Attribution
	if len(contexts) > 0 {
		attributions = attrib.Correlate(rpcSpans, st.FirstTS, contexts)
	}
	var uiOrigin time.Time
	if origin, ok := ui.Origin(); ok {
		uiOrigin = origin
	}
	return model.BuildCaptureQuality(model.CaptureQualityInput{
		Stats: st, Caps: caps, RPCSpans: rpcSpans, UISpans: ui.Spans(),
		RPCEvidence: b.Evidence(), UIEvidence: ui.Evidence(), UIOrigin: uiOrigin,
		Contexts: contexts, ContextEvidence: cc.Evidence(), Attributions: attributions,
		ComponentOverflow: comps.Overflowed(), RequestIDOverflow: reqIDs.Overflowed(),
	})
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
	if got := build(t, in).fieldKeys["tf_req_duration_ms"]; got != 2 {
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
	if r.Quality.RPC.DurationMs != 5000 {
		t.Errorf("Quality.RPC.DurationMs = %d, want 5000", r.Quality.RPC.DurationMs)
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
	if got := r.fieldKeys["tf_rpc"]; got != 1 {
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

// --- Structured-output (terraform.ui JSON) detection and reporting. ---

const structuredVersionLine = `{"@level":"info","@message":"Terraform 1.14.9","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.113402+10:00","terraform":"1.14.9","type":"version","ui":"1.2"}`

const structuredHookLine = `{"@level":"info","@message":"module.module_name[\"key\"].data.local_file.thing: Refreshing...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.556000+10:00","hook":{"resource":{"addr":"module.module_name[\"key\"].data.local_file.thing","module":"module.module_name[\"key\"]","resource":"data.local_file.thing","implied_provider":"local","resource_type":"local_file","resource_name":"thing","resource_key":null},"action":"read"},"type":"apply_start"}`

func TestReportShowsStructuredLineCountInSize(t *testing.T) {
	in := structuredVersionLine + "\n" + structuredHookLine + "\n"
	out := render(t, build(t, in))
	if !strings.Contains(out, "structured lines     2 (100.0% of lines)") {
		t.Errorf("report missing structured line count in SIZE:\n%s", out)
	}
}

// The test that matters most: a structured line's resource address must
// appear nowhere in the rendered report -- not in a field, not in a
// template, not anywhere else.
func TestReportNeverLeaksStructuredLineContent(t *testing.T) {
	in := structuredVersionLine + "\n" + structuredHookLine + "\n"
	out := render(t, build(t, in))
	const addr = `module.module_name["key"].data.local_file.thing`
	if strings.Contains(out, addr) {
		t.Fatalf("report leaked the structured-line resource address:\n%s", out)
	}
	if strings.Contains(out, "Refreshing") || strings.Contains(out, "local_file") {
		t.Fatalf("report leaked structured-line content:\n%s", out)
	}
}

// completionHookLine is a completion-bearing UI-hook line -- the kind
// span.UIHookBuilder turns into a span -- carrying the address
// module.m["key"].data.local_file.thing, distinct from structuredHookLine's
// address so the two tests cannot pass on stale state from each other.
const completionHookLine = `{"@level":"info","@message":"module.m[\"key\"].data.local_file.thing: Refresh complete after 0s [id=abc]","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.601000+10:00","hook":{"resource":{"addr":"module.m[\"key\"].data.local_file.thing","module":"module.m[\"key\"]","resource":"data.local_file.thing","implied_provider":"local","resource_type":"local_file","resource_name":"thing","resource_key":null},"action":"read","id_key":"id","id_value":"abc","elapsed_seconds":0},"type":"apply_complete"}`

// The property that matters most for this task: adding span.UIHookBuilder as
// a StructuredSink alongside the Collector must not let a structured line's
// content reach the Collector by some other path. The Collector deliberately
// does not implement logfmt.StructuredSink, so this checks its internal
// maps directly, not just the rendered text, and specifically the address
// that span.UIHookBuilder itself now reads from the very same line.
func TestUIHookBuilderPresenceDoesNotLeakIntoCollector(t *testing.T) {
	const addr = `module.m["key"].data.local_file.thing`
	in := completionHookLine + "\n"

	var comps, reqIDs logfmt.Interner
	c := NewCollector(&comps)
	var ui span.UIHookBuilder
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, &reqIDs, c, &ui); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Sanity check: the builder this test is guarding against did receive
	// the line and did build a span from it, so the negative assertions
	// below are not vacuous.
	if got := ui.Spans(); len(got) != 1 || got[0].Address != addr {
		t.Fatalf("UIHookBuilder did not build the expected span: %+v", got)
	}

	for key := range c.fieldKeys {
		if strings.Contains(key, addr) {
			t.Errorf("address reached a field key: %q", key)
		}
	}
	for tmpl := range c.templates {
		if strings.Contains(tmpl, addr) {
			t.Errorf("address reached a template: %q", tmpl)
		}
	}
	for comp := range c.compCount {
		if strings.Contains(comp, addr) {
			t.Errorf("address reached a component: %q", comp)
		}
	}
}

// A predominantly structured log has no hclog provider entries to profile,
// but the reason and the remedy differ from an ordinary core-only log, so
// EXTRACTION's guidance must say so specifically.
func TestReportExplainsPredominantlyStructuredLog(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 9; i++ {
		sb.WriteString(structuredVersionLine)
		sb.WriteString("\n")
	}
	sb.WriteString("2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n")
	r := build(t, sb.String())
	if r.TierUsable {
		t.Fatal("TierUsable = true for a structured-output log")
	}
	out := render(t, r)
	if !strings.Contains(out, "structured output (terraform.ui JSON)") {
		t.Errorf("report does not explain the structured-output log:\n%s", out)
	}
	if strings.Contains(out, "no provider RPC entries") {
		t.Errorf("report still shows the generic no-provider-entries guidance for a structured log:\n%s", out)
	}
}

// A core-only hclog log with no structured lines at all must keep the
// original guidance -- the new wording is specific to structured output.
func TestReportKeepsOriginalGuidanceForNonStructuredCoreOnlyLog(t *testing.T) {
	in := "2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n"
	r := build(t, in)
	if r.TierUsable {
		t.Fatal("TierUsable = true for a core-only log")
	}
	out := render(t, r)
	if !strings.Contains(out, "no provider RPC") {
		t.Errorf("report does not give the original no-provider-entries guidance:\n%s", out)
	}
	if strings.Contains(out, "structured-output (terraform.ui JSON) log") {
		t.Errorf("report wrongly shows structured-output guidance for a non-structured log:\n%s", out)
	}
}

// --- Final fix wave, Fix 1: COMPONENTS obeys both disclosure invariants. ---
//
// splitComponent (internal/logfmt/header.go) accepts any whitespace-free
// byte sequence before the first colon, so a component name carries no
// charset guarantee of its own the way a field key does. Two things must
// hold: components are disclosed only when they recur (the same rule field
// keys and templates already follow), and MaskComponent masks anything that
// is too long or carries a character no real component name uses.

func TestReportWithholdsComponentSeenOnce(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] diagnose_test_component: a message\n"
	r := build(t, in)
	out := render(t, r)
	if strings.Contains(out, "diagnose_test_component") {
		t.Errorf("component seen once was printed:\n%s", out)
	}
	if r.WithheldComponents != 1 {
		t.Errorf("WithheldComponents = %d, want 1", r.WithheldComponents)
	}
	if !strings.Contains(out, "1 component shapes seen only once (withheld)") {
		t.Errorf("report missing withheld component count:\n%s", out)
	}
}

func TestReportPrintsComponentSeenOnTwoEntries(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] backend/local: doing work\n" +
		"2022-12-15T00:16:20.900Z [TRACE] backend/local: doing more work\n" +
		"2022-12-15T00:16:21.000Z [TRACE] dag/walk: visiting a vertex\n" +
		"2022-12-15T00:16:21.100Z [TRACE] dag/walk: visiting another vertex\n"
	out := render(t, build(t, in))
	// backend/local and dag/walk are genuine Terraform component names, and
	// "/" is deliberately kept unmasked -- they must survive verbatim.
	if !strings.Contains(out, "backend/local") {
		t.Errorf("recurring component backend/local missing from report:\n%s", out)
	}
	if !strings.Contains(out, "dag/walk") {
		t.Errorf("recurring component dag/walk missing from report:\n%s", out)
	}
}

func TestReportMasksRecurringOverlongComponent(t *testing.T) {
	// Two distinct secrets, both longer than logfmt.MaxKeyLen, so masking
	// (not withholding) is what must catch this -- both mask to the same
	// "<other>" bucket, which is what lets it recur and print.
	in := "2026-01-01T00:00:00.000Z [ERROR] /home/tfc-agent/.tfc-agent/component/terraform/runs/run-SECRET123/config: plugin crashed\n" +
		"2026-01-01T00:00:01.000Z [ERROR] /home/tfc-agent/.tfc-agent/component/terraform/runs/run-DIFFERENTSECRET999/config: plugin crashed\n"
	out := render(t, build(t, in))
	if strings.Contains(out, "SECRET123") || strings.Contains(out, "DIFFERENTSECRET999") || strings.Contains(out, "tfc-agent") {
		t.Fatalf("report leaked an overlong component:\n%s", out)
	}
	if !strings.Contains(out, "<other>") {
		t.Errorf("report does not show the masked bucket for a recurring overlong component:\n%s", out)
	}
}

func TestReportExactLeakLineYieldsNothingResemblingThePath(t *testing.T) {
	// The exact reproduction from the review report.
	in := "2026-01-01T00:00:00.000Z [ERROR] /home/tfc-agent/.tfc-agent/component/terraform/runs/run-SECRET123/config: plugin crashed\n"
	out := render(t, build(t, in))
	for _, leak := range []string{"SECRET123", "tfc-agent", "run-", "component/terraform"} {
		if strings.Contains(out, leak) {
			t.Errorf("report leaked %q:\n%s", leak, out)
		}
	}
}

// --- Final fix wave, Fix 2: the byte count is honestly labelled. ---

func TestReportLabelsContinuationBytesHonestly(t *testing.T) {
	// testdata/multiline-body.log is 100% well-formed hclog, yet almost 40%
	// of it is continuation lines -- calling that "non-hclog" was the bug.
	in := "2024-02-13T12:11:28.330+0100 [DEBUG] provider.aws: HTTP Response Received: @module=aws\n" +
		"  http.response.body=\n" +
		`  | {"a":"b"}` + "\n" +
		"2024-02-13T12:11:28.331+0100 [TRACE] provider.aws: Received downstream response: tf_rpc=ApplyResourceChange tf_req_duration_ms=10710\n"
	out := render(t, build(t, in))
	if !strings.Contains(out, "continuation bytes") {
		t.Errorf("report does not label the figure as continuation bytes:\n%s", out)
	}
	if strings.Contains(out, "non-hclog bytes") {
		t.Errorf("report still uses the inaccurate non-hclog bytes label:\n%s", out)
	}
}

// --- Final fix wave, Fix 3: EXTRACTION discloses the header-lines-only caveat. ---

func TestReportNotesUnparsedContinuationLines(t *testing.T) {
	// tf_req_id sits on a continuation line here and so is never counted --
	// the report must say so and give the count, not stay silent about it.
	in := "2022-12-15T00:16:20.800Z [TRACE] a: multiline start\n" +
		"  continuation carrying tf_req_id=abc123, never parsed\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n"
	r := build(t, in)
	if r.Stats.ContinuationLines != 1 {
		t.Fatalf("ContinuationLines = %d, want 1", r.Stats.ContinuationLines)
	}
	out := render(t, r)
	if !strings.Contains(out, "continuation lines not parsed for fields") {
		t.Errorf("report missing the continuation-lines caveat:\n%s", out)
	}
	if !strings.Contains(out, "1 (fields are read from header lines only") {
		t.Errorf("report does not give the unparsed continuation line count:\n%s", out)
	}
}

// --- Final fix wave, Fix 5: DistinctComps does not overcount the pre-seeded "" slot. ---

func TestReportDistinctCompsExcludesUnusedNoneSlot(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: msg one\n" +
		"2022-12-15T00:16:20.900Z [TRACE] b: msg two\n"
	r := build(t, in)
	if r.DistinctComps != 2 {
		t.Errorf("DistinctComps = %d, want 2 (no entry actually lacked a component)", r.DistinctComps)
	}
}

func TestReportDistinctCompsCountsAGenuineNoneComponent(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: msg one\n" +
		"2022-12-15T00:16:20.900Z [TRACE] no component prefix on this line at all\n"
	r := build(t, in)
	if r.DistinctComps != 2 {
		t.Errorf("DistinctComps = %d, want 2 (component a, plus a genuine (none))", r.DistinctComps)
	}
}

func TestReportUsesCaptureQualityForAdmittedDuration(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5000` + "\n"
	out := render(t, build(t, in))
	if !strings.Contains(out, "admitted 1, rejected 0; duration 5.0s") || !strings.Contains(out, "RPC positioning        1 observations, 5.0s") {
		t.Errorf("report does not show the admitted capture duration:\n%s", out)
	}
	if strings.Contains(out, "total span time") {
		t.Errorf("report duplicates capture duration in SPANS:\n%s", out)
	}
}

// --- Final fix wave, Fix 7: an empty section reads as "none", not silence. ---

func TestReportRendersNoneForEmptySections(t *testing.T) {
	r := build(t, "")
	out := render(t, r)
	for _, section := range []string{"LEVELS", "HOOK TYPES", "COMPONENTS", "FIELD KEYS", "MESSAGE TEMPLATES"} {
		if !strings.Contains(out, section) {
			t.Fatalf("report missing %s section:\n%s", section, out)
		}
	}
	if got := strings.Count(out, "\n  none\n"); got != 5 {
		t.Errorf("report renders %d empty sections as \"none\", want 5:\n%s", got, out)
	}
}

// --- Review round 1: field keys must not pin their source message. ---
//
// bufio.Reader.ReadString (internal/logfmt/scan.go) builds its result via a
// fresh strings.Builder on every call, and StripANSI's returned string goes
// through a `string(scratch)` conversion, which always copies -- so in this
// codebase msg is never a slice of a buffer any later entry can overwrite.
// A test that drives many entries with distinct long values and checks
// their content survives (the style TestReportedBuilderRetainsAcrossScan in
// internal/span uses) therefore cannot distinguish cloned from uncloned
// keys here: it passes either way, and was confirmed to do so against the
// unfixed fieldKeys map before this test was written. The actual hazard is
// span, not corruption: retaining any substring of msg keeps msg's whole
// backing array -- up to 64KB, MaxHeaderMsg -- reachable for as long as the
// key lives, and that is what a stored key's address must be checked
// against directly.

// withinBackingArray reports whether sub's data pointer falls inside whole's
// backing array, i.e. whether retaining sub would keep the whole of whole
// reachable. This mirrors the pointer comparison the reviewer used to find
// the original bug.
func withinBackingArray(sub, whole string) bool {
	if len(whole) == 0 {
		return false
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(whole)))
	end := start + uintptr(len(whole))
	p := uintptr(unsafe.Pointer(unsafe.StringData(sub)))
	return p >= start && p < end
}

// TestFieldKeyDoesNotPinItsSourceMessage proves a key entering fieldKeys
// does not retain any part of the message it was parsed from. msg is built
// large (over 4KB) so the failure, if reintroduced, is unambiguous: a key
// sharing memory with it would otherwise pin all 4KB+ for a 6-byte key.
func TestFieldKeyDoesNotPinItsSourceMessage(t *testing.T) {
	msg := "Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5 padding=" + strings.Repeat("x", 4000)
	f := logfmt.ParseFields(msg, nil)

	var comps logfmt.Interner
	c := NewCollector(&comps)
	c.Entry(0, logfmt.Entry{}, msg, f)

	for key := range c.fieldKeys {
		if withinBackingArray(key, msg) {
			t.Errorf("fieldKeys key %q shares backing memory with its source message -- retaining it pins the whole message alive", key)
		}
	}
}

// --- Task 2: SLOWEST RESOURCES, BY RESOURCE TYPE, actions, and the SPANS/
// EXTRACTION/wall-clock updates that come with wiring span.UIHookBuilder
// into the report. ---

const (
	uiAddrLocalFile   = `module.m["key"].data.local_file.thing`
	uiAddrInstanceOne = `aws_instance.example`
	uiAddrInstanceTwo = `aws_instance.other`
)

// uiHookLine builds a minimal completion-bearing UI-hook line, mirroring
// span.uiLineWith in internal/span/uihook_test.go -- duplicated rather than
// exported, since a test-only helper is not worth widening span's public
// surface for.
func uiHookLine(ts, addr, resourceType, provider, action string, elapsedSeconds float64) string {
	return fmt.Sprintf(`{"@level":"info","@timestamp":%q,"hook":{"resource":{"addr":%q,"module":"","resource":%q,"implied_provider":%q,"resource_type":%q,"resource_name":"x","resource_key":null},"action":%q,"elapsed_seconds":%v},"type":"apply_complete"}`,
		ts, addr, addr, provider, resourceType, action, elapsedSeconds)
}

// threeResourceUIHookLog carries one local_file read (0ms -- must still
// count) and two aws_instance creates of different durations, so ranking,
// per-type rollup and action counting all have something real to check.
func threeResourceUIHookLog() string {
	return uiHookLine("2026-09-04T09:15:00.000000+10:00", uiAddrLocalFile, "local_file", "local", "read", 0) + "\n" +
		uiHookLine("2026-09-04T09:15:01.000000+10:00", uiAddrInstanceOne, "aws_instance", "aws", "create", 2.5) + "\n" +
		uiHookLine("2026-09-04T09:15:02.000000+10:00", uiAddrInstanceTwo, "aws_instance", "aws", "create", 8.2) + "\n"
}

func TestReportSlowestResourcesRankedWithMaskedAddresses(t *testing.T) {
	r := build(t, threeResourceUIHookLog())
	if len(r.SlowestResources) != 3 {
		t.Fatalf("SlowestResources has %d rows, want 3", len(r.SlowestResources))
	}
	if r.SlowestResources[0].DurationMs != 8200 || r.SlowestResources[1].DurationMs != 2500 || r.SlowestResources[2].DurationMs != 0 {
		t.Fatalf("SlowestResources not ranked by duration descending: %+v", r.SlowestResources)
	}
	if r.SlowestResources[0].ResourceType != "aws_instance" {
		t.Errorf("ResourceType = %q, want aws_instance", r.SlowestResources[0].ResourceType)
	}
	out := render(t, r)
	if !strings.Contains(out, "SLOWEST RESOURCES") {
		t.Fatalf("report missing SLOWEST RESOURCES section:\n%s", out)
	}
	if !strings.Contains(out, "aws_instance") || !strings.Contains(out, "local_file") {
		t.Errorf("report does not show unmasked resource types:\n%s", out)
	}
	for _, addr := range []string{uiAddrLocalFile, uiAddrInstanceOne, uiAddrInstanceTwo} {
		if strings.Contains(out, addr) {
			t.Errorf("report leaked an unmasked resource address %q:\n%s", addr, out)
		}
	}
}

func TestReportByResourceTypeTotals(t *testing.T) {
	r := build(t, threeResourceUIHookLog())
	byType := map[string]ResourceTypeTotal{}
	for _, row := range r.ByResourceType {
		byType[row.ResourceType] = row
	}
	if got := byType["aws_instance"]; got.TotalMs != 10700 || got.Count != 2 {
		t.Errorf("aws_instance rollup = %+v, want TotalMs 10700, Count 2", got)
	}
	if got := byType["local_file"]; got.TotalMs != 0 || got.Count != 1 {
		t.Errorf("local_file rollup = %+v, want TotalMs 0, Count 1", got)
	}
	out := render(t, r)
	if !strings.Contains(out, "BY RESOURCE TYPE") {
		t.Fatalf("report missing BY RESOURCE TYPE section:\n%s", out)
	}
	for _, addr := range []string{uiAddrLocalFile, uiAddrInstanceOne, uiAddrInstanceTwo} {
		if strings.Contains(out, addr) {
			t.Errorf("BY RESOURCE TYPE leaked an address %q:\n%s", addr, out)
		}
	}
}

func TestReportUIHookActionsLine(t *testing.T) {
	out := render(t, build(t, threeResourceUIHookLog()))
	if !strings.Contains(out, "read 1") || !strings.Contains(out, "create 2") {
		t.Errorf("report missing action counts:\n%s", out)
	}
}

func TestReportHclogLogHasNoUIHookSections(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5000` + "\n"
	out := render(t, build(t, in))
	for _, section := range []string{"SLOWEST RESOURCES", "BY RESOURCE TYPE"} {
		if strings.Contains(out, section) {
			t.Errorf("hclog-only report wrongly renders %s:\n%s", section, out)
		}
	}
}

// A pure UI-hook log has no RPC-level (tier 1) spans at all: the two span
// sets are never merged onto one timeline, so the RPC "spans built" figure
// must stay zero even though UI-hook spans exist.
func TestReportStructuredLogHasNoRPCSpansBuilt(t *testing.T) {
	r := build(t, threeResourceUIHookLog())
	if r.SpanCount != 0 {
		t.Errorf("SpanCount (RPC spans) = %d, want 0 for a pure UI-hook log", r.SpanCount)
	}
	if r.UISpanCount != 3 {
		t.Errorf("UISpanCount = %d, want 3", r.UISpanCount)
	}
}

func TestReportExtractionGuidanceReflectsThatUIHooksAreParsed(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 9; i++ {
		sb.WriteString(structuredVersionLine)
		sb.WriteString("\n")
	}
	sb.WriteString("2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n")
	out := render(t, build(t, sb.String()))
	if strings.Contains(out, "does not yet parse") {
		t.Errorf("report still uses the stale does-not-yet-parse wording:\n%s", out)
	}
	if !strings.Contains(out, "per-resource timings") {
		t.Errorf("report does not mention per-resource timings:\n%s", out)
	}
	// This assertion used to require the report to recommend debug logging.
	// A real debug-enabled HCP run then measured zero RPC entries, so the
	// recommendation was wrong and the assertion went with it: what the
	// report must now name is the level that actually carries them.
	if !strings.Contains(out, "TRACE") {
		t.Errorf("report does not tell the reader which level carries RPC entries:\n%s", out)
	}
}

func TestReportDerivesWallClockFromUIHookSpansWhenNoHclogTimestamps(t *testing.T) {
	out := render(t, build(t, threeResourceUIHookLog()))
	if !strings.Contains(out, "log wall-clock") {
		t.Fatalf("report missing log wall-clock line:\n%s", out)
	}
	if strings.Contains(out, "log wall-clock       unavailable") {
		t.Errorf("report says wall-clock unavailable when UI-hook spans can derive it:\n%s", out)
	}
}

// Fix wave: provision_*/refresh_* completions build no UI-hook span (see
// span.isCompletionType), so a wall clock derived only from UI-hook spans'
// own EndMs understates the log whenever one of those two is the LAST event
// in a pure structured-output capture. attrib.ContextCollector parses every
// structured line's timestamp unconditionally, so its FirstTS/LastTS span
// covers the trailing completion even though no span was built for it.
//
// completionHookLine ("2026-09-04T09:15:02.601000+10:00") is this log's only
// span-building line; the trailing refresh_complete below sits at
// "2026-09-04T09:20:00.000000+10:00", 297399ms later -- a gap the old
// span-only figure would drop entirely.
func TestReportWallClockCoversATrailingRefreshCompleteWithNoSpan(t *testing.T) {
	const trailingRefreshComplete = `{"@level":"info","@message":"aws_instance.tracked: Refresh complete","@module":"terraform.ui","@timestamp":"2026-09-04T09:20:00.000000+10:00","hook":{"resource":{"addr":"aws_instance.tracked","module":"","resource":"aws_instance.tracked","implied_provider":"aws","resource_type":"aws_instance","resource_name":"tracked","resource_key":null}},"type":"refresh_complete"}`
	r := build(t, completionHookLine+"\n"+trailingRefreshComplete+"\n")
	if r.UISpanCount == 0 {
		t.Fatal("fixture built no UI-hook spans; this test proves nothing")
	}
	if r.UIWallClockMs != 297399 {
		t.Errorf("UIWallClockMs = %d, want 297399 (must cover the trailing refresh_complete, which built no span of its own)", r.UIWallClockMs)
	}
	out := render(t, r)
	if strings.Contains(out, "log wall-clock       unavailable") {
		t.Errorf("report says wall-clock unavailable despite UI-hook spans:\n%s", out)
	}
	if !strings.Contains(out, "297.4s") {
		t.Errorf("report does not render the wall clock covering the trailing refresh_complete:\n%s", out)
	}
}

func TestReportWallClockUnavailableWhenNeitherSourceExists(t *testing.T) {
	out := render(t, build(t, ""))
	if !strings.Contains(out, "log wall-clock") {
		t.Fatalf("report missing log wall-clock line:\n%s", out)
	}
	if !strings.Contains(out, "unavailable") {
		t.Errorf("report does not plainly state wall-clock is unavailable:\n%s", out)
	}
}

func TestReportWallClockUnavailableWhenUIDurationHasNoParsedTimestamp(t *testing.T) {
	const in = `{"@level":"info","@timestamp":"bad","type":"apply_complete","hook":{"elapsed_seconds":1}}`
	r := build(t, in+"\n")
	if r.UISpanCount != 1 || r.Quality.UI.DurationMs != 1000 {
		t.Fatalf("UI timing = %d spans/%dms, want one admitted 1000ms duration", r.UISpanCount, r.Quality.UI.DurationMs)
	}
	out := render(t, r)
	if !strings.Contains(out, "log wall-clock       unavailable") {
		t.Errorf("report inferred a capture clock from a duration with no parsed timestamp:\n%s", out)
	}
}

func TestReportWallClockPreservesCoincidentParsedEndpoints(t *testing.T) {
	const in = `{"@level":"info","@timestamp":"2026-09-04T09:15:03Z","type":"apply_complete","hook":{"elapsed_seconds":1}}`
	r := build(t, in+"\n")
	if r.UISpanCount != 1 {
		t.Fatalf("UISpanCount = %d, want one admitted duration", r.UISpanCount)
	}
	out := render(t, r)
	if !strings.Contains(out, "log wall-clock       0.0s") {
		t.Errorf("report did not preserve the genuine zero between coincident parsed endpoints:\n%s", out)
	}
	if strings.Contains(out, "log wall-clock       unavailable") {
		t.Errorf("report treated parsed coincident endpoints as unavailable:\n%s", out)
	}
}

// --- Task 2 fix round 1. ---

// Finding 1: SLOWEST RESOURCES' address column must show real, differing
// structure per row (module depth, indexing, whether it's a data source),
// not the same decorative "<addr>" for every row -- that defeats the whole
// point of choosing a ranked list over aggregates-only.
func TestReportSlowestResourcesShowsAddressStructureNotJustAddrPlaceholder(t *testing.T) {
	out := render(t, build(t, threeResourceUIHookLog()))
	if !strings.Contains(out, `module.<m>["<k>"].data.local_file.<name>`) {
		t.Errorf("report does not show the masked-but-structured local_file address:\n%s", out)
	}
	if !strings.Contains(out, "aws_instance.<name>") {
		t.Errorf("report does not show the masked-but-structured aws_instance address:\n%s", out)
	}
	if strings.Count(out, "<addr>") > 0 {
		t.Errorf("report still uses the decorative <addr> placeholder for a UI-hook address:\n%s", out)
	}
}

// Finding 2: a predominantly structured log must explain why its RPC-related
// counters read zero even when a tier was selected (ui-reported, because it
// had UI-hook completions) -- not only in the NONE USABLE case.
func TestReportExplainsStructuredLogEvenWhenTierUsable(t *testing.T) {
	r := build(t, threeResourceUIHookLog())
	if !r.TierUsable {
		t.Fatal("TierUsable = false, want true for a log with UI-hook completions")
	}
	out := render(t, r)
	if !strings.Contains(out, "structured output (terraform.ui JSON)") {
		t.Errorf("report does not explain the structured-output log even though a tier was selected:\n%s", out)
	}
	if !strings.Contains(out, "provider RPC") {
		t.Errorf("report does not explain why the provider RPC counters read zero:\n%s", out)
	}
}

// Finding 3: a structured entry always has an empty message by design (Scan
// never gives Entry a structured line's content), so that empty shape must
// never be counted or printed as a message template -- on a structured-only
// log it would otherwise be the only row MESSAGE TEMPLATES ever shows.
func TestReportDoesNotCountEmptyMessageTemplate(t *testing.T) {
	r := build(t, threeResourceUIHookLog())
	if _, ok := r.templateCount[""]; ok {
		t.Error("an empty message template was counted")
	}
	out := render(t, r)
	if !strings.Contains(out, "MESSAGE TEMPLATES (content masked, recurring only, top 0)\n  none\n") {
		t.Errorf("report does not render an empty MESSAGE TEMPLATES section for an all-structured log:\n%s", out)
	}
}

// Finding 4: COMPONENTS' header count must match the rows actually shown
// beneath it. A purely structured log never calls Interner.Intern at all (no
// entry has a component to intern), so comps.Len() alone is 0 even though
// every entry's masked component was genuinely "(none)" and is shown as
// such below the header.
func TestReportDistinctCompsConsistentForPureStructuredLog(t *testing.T) {
	r := build(t, threeResourceUIHookLog())
	if r.DistinctComps != 1 {
		t.Errorf("DistinctComps = %d, want 1 (one genuine (none) component)", r.DistinctComps)
	}
	out := render(t, r)
	if !strings.Contains(out, "COMPONENTS (1 distinct, recurring only, top 1)\n         3  (none)\n") {
		t.Errorf("report's COMPONENTS header is inconsistent with the row shown beneath it:\n%s", out)
	}
}

// --- Whole-branch review, fix wave: eight findings. ---

// Finding 1: a log that is majority structured-output but also carries real
// provider RPC entries (e.g. hclog output interleaved with a
// structured-output capture) must not claim those counters "are expected to
// read zero" directly above a nonzero response entries count.
func TestReportMixedStructuredAndRPCLogGetsMixedWording(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 3; i++ {
		sb.WriteString(structuredVersionLine)
		sb.WriteString("\n")
	}
	sb.WriteString(`2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5` + "\n")
	r := build(t, sb.String())
	if r.Caps.ResponseEntries == 0 {
		t.Fatal("test fixture has no response entries -- the case under test is not exercised")
	}
	out := render(t, r)
	if strings.Contains(out, "expected to read zero") {
		t.Errorf("report claims RPC counters read zero on a log that has real RPC entries:\n%s", out)
	}
	if !strings.Contains(out, "structured output (terraform.ui JSON)") {
		t.Errorf("report drops the structured-output explanation entirely on a mixed log:\n%s", out)
	}
}

// Finding 2: ResourceType and Action come straight from a structured-output
// line's JSON with nothing upstream constraining their shape, unlike a
// field key. A hostile value (a newline, 300 characters, upper-case,
// punctuation) must never reach the report, and must not be able to break
// the column layout of every row after it.
func TestReportMasksHostileResourceTypeAndAction(t *testing.T) {
	hostileType := "AWS_INSTANCE\ninjected;" + strings.Repeat("x", 300)
	hostileAction := "CREATE\x00drop"
	uiSpans := []span.Span{{
		DurationMs:   1500,
		RPC:          hostileAction,
		ResourceType: hostileType,
		Address:      "aws_instance.example",
		Fidelity:     span.FidelityUIReported,
	}}
	var comps logfmt.Interner
	c := NewCollector(&comps)
	var cc attrib.ContextCollector
	quality := model.BuildCaptureQuality(model.CaptureQualityInput{UISpans: uiSpans})
	r := Build(logfmt.Stats{}, span.Capabilities{}, nil, uiSpans, quality, 0, 0, 0, &cc, c, &comps, 0)

	if len(r.SlowestResources) != 1 {
		t.Fatalf("SlowestResources has %d rows, want 1", len(r.SlowestResources))
	}
	if r.SlowestResources[0].ResourceType != "<other>" {
		t.Errorf("ResourceType = %q, want <other>", r.SlowestResources[0].ResourceType)
	}
	if r.SlowestResources[0].Action != "<other>" {
		t.Errorf("Action = %q, want <other>", r.SlowestResources[0].Action)
	}
	if len(r.ByResourceType) != 1 || r.ByResourceType[0].ResourceType != "<other>" {
		t.Errorf("ByResourceType = %+v, want a single <other> row", r.ByResourceType)
	}

	out := render(t, r)
	for _, leak := range []string{"AWS_INSTANCE", "injected", "CREATE", "drop"} {
		if strings.Contains(out, leak) {
			t.Errorf("hostile resource type/action reached the report: %q leaked:\n%s", leak, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 200 {
			t.Errorf("report line is %d bytes -- hostile input may have broken the column layout: %q", len(line), line)
		}
	}
}

// C1: a structured-output line's "type" value has nothing upstream
// constraining its shape either, the same hazard
// TestReportMasksHostileResourceTypeAndAction pins for ResourceType/Action.
// Demonstrated against the real binary: an unmasked, uncapped hook-type
// histogram put a crafted "type" value straight into --diagnose, the one
// artefact that leaves the machine. A hostile value must collapse into
// "<other>" the same way, singleton or not -- HookTypes is deliberately
// never withheld for rarity (see its doc comment), so masking, not
// withholding, is what has to catch this one.
func TestReportMasksHostileHookType(t *testing.T) {
	hostileType := "INJECTED_SECRET;" + strings.Repeat("x", 300)
	line := fmt.Sprintf(`{"@level":"info","@timestamp":"2026-09-04T09:15:02.556000+10:00","type":%q}`, hostileType)
	r := build(t, line+"\n")

	if len(r.HookTypes) != 1 || r.HookTypes[0].Text != "<other>" {
		t.Fatalf("HookTypes = %+v, want a single <other> row", r.HookTypes)
	}

	out := render(t, r)
	for _, leak := range []string{"INJECTED_SECRET", hostileType} {
		if strings.Contains(out, leak) {
			t.Errorf("hostile hook type reached the report: %q leaked:\n%s", leak, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 200 {
			t.Errorf("report line is %d bytes -- hostile input may have broken the column layout: %q", len(line), line)
		}
	}
}

// maskedTypeCounts sums with += rather than overwriting with =, deliberately,
// so that two DISTINCT hostile "type" values which collapse onto the same
// masked key ("<other>") both survive in the merged bucket's count. A single
// hostile value (as TestReportMasksHostileHookType feeds) cannot distinguish
// += from =, since there is nothing else in the bucket to lose: a lone value
// mapped with either operator yields the same count of 1. Changing the += to
// = makes this test fail (bucket count drops to 1, the count of whichever
// key iteration visited last), confirmed and reverted.
func TestMaskedTypeCountsSumsCollidingHostileValues(t *testing.T) {
	hostileA := "INJECTED_SECRET_A;" + strings.Repeat("x", 300)
	hostileB := "INJECTED_SECRET_B;" + strings.Repeat("y", 300)
	got := maskedTypeCounts(map[string]uint64{hostileA: 1, hostileB: 1})

	if len(got) != 1 {
		t.Fatalf("maskedTypeCounts = %v, want a single merged bucket", got)
	}
	if got["<other>"] != 2 {
		t.Errorf("maskedTypeCounts[<other>] = %d, want 2 (both hostile values merged)", got["<other>"])
	}
}

// Finding 3: a malformed structured line must be counted and surfaced in
// ANOMALIES, not silently absorbed. If HCP's schema drifts or a log is
// truncated, this is the only trace of it, on the one machine holding the
// original log.
func TestReportSurfacesUIHookMalformedLinesInAnomalies(t *testing.T) {
	good := uiHookLine("2026-09-04T09:15:00.000000+10:00", uiAddrLocalFile, "local_file", "local", "read", 0)
	malformed := `{"@level":"info","@timestamp":"2026-09-04T09:15:01.000000+10:00", not valid json`
	r := build(t, good+"\n"+malformed+"\n")
	if r.UIMalformedLines != 1 {
		t.Fatalf("UIMalformedLines = %d, want 1", r.UIMalformedLines)
	}
	out := render(t, r)
	if !strings.Contains(out, "ANOMALIES") {
		t.Fatalf("report missing ANOMALIES section for a log with a malformed UI-hook line:\n%s", out)
	}
	if !strings.Contains(out, "UI-hook lines malformed 1") {
		t.Errorf("report does not surface the malformed UI-hook line count:\n%s", out)
	}
	if !strings.Contains(out, "ui_decode issues") || !strings.Contains(out, "json_syntax") {
		t.Errorf("capture quality omits the malformed-only UI reason:\n%s", out)
	}
}

// Finding 5: a duration that saturates uint32 milliseconds must be counted
// and surfaced in ANOMALIES -- unmarked, it is indistinguishable from a real
// value and poisons UI-hook total time and the type rollup.
func TestReportSurfacesUIHookSaturatedDurationsInAnomalies(t *testing.T) {
	in := uiHookLine("2026-09-04T09:15:00.000000+10:00", uiAddrLocalFile, "local_file", "local", "read", 1e300) + "\n"
	r := build(t, in)
	if r.UISaturatedDurations != 1 {
		t.Fatalf("UISaturatedDurations = %d, want 1", r.UISaturatedDurations)
	}
	out := render(t, r)
	if !strings.Contains(out, "UI-hook durations saturated 1") {
		t.Errorf("report does not surface the saturated-duration count:\n%s", out)
	}
}

// Finding 6: a UI-hook timestamp earlier than the builder's base must be
// counted, mirroring logfmt.Scan's own BackwardsTimestamps -- a silent
// clamp shortens the derived UI-hook wall-clock with no visible trace.
func TestReportSurfacesUIHookBackwardsTimestampsInAnomalies(t *testing.T) {
	in := uiHookLine("2026-09-04T09:15:10.000000+10:00", uiAddrLocalFile, "local_file", "local", "read", 1) + "\n" +
		uiHookLine("2026-09-04T09:15:00.000000+10:00", uiAddrInstanceOne, "aws_instance", "aws", "create", 1) + "\n"
	r := build(t, in)
	if r.UIBackwardsTimestamps != 1 {
		t.Fatalf("UIBackwardsTimestamps = %d, want 1", r.UIBackwardsTimestamps)
	}
	out := render(t, r)
	if !strings.Contains(out, "UI-hook backwards timestamps 1") {
		t.Errorf("report does not surface the backwards UI-hook timestamp count:\n%s", out)
	}
}

// Finding 4: a sub-second duration must render as something other than a
// flat "0.0s" -- elapsed_seconds: 0 is a genuinely common figure for a fast
// data-source refresh, and a fixed one-decimal-second format collapses
// anything under 100ms to the same zero, making the ranking convey nothing.
func TestReportSubSecondResourceShowsDistinguishableDuration(t *testing.T) {
	in := uiHookLine("2026-09-04T09:15:00.000000+10:00", uiAddrLocalFile, "local_file", "local", "read", 0.045) + "\n"
	r := build(t, in)
	if len(r.SlowestResources) != 1 || r.SlowestResources[0].DurationMs != 45 {
		t.Fatalf("SlowestResources = %+v, want one 45ms row", r.SlowestResources)
	}
	out := render(t, r)
	// Scoped to the resource row itself (identified by the "read" action),
	// not the report as a whole: the log wall-clock line legitimately
	// renders "0.0s" for an unrelated reason and must not make this test
	// pass vacuously.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "read") && strings.Contains(line, "local_file") {
			if strings.Contains(line, "0.0s") {
				t.Errorf("SLOWEST RESOURCES row renders a sub-second duration as an undifferentiated 0.0s: %q", line)
			}
			if !strings.Contains(line, "45ms") {
				t.Errorf("SLOWEST RESOURCES row does not show a distinguishable sub-second duration: %q", line)
			}
		}
	}
}

// Finding 7: UISlowestMs is computed but must actually be rendered, or it is
// a trap for the next reader -- a value nobody reads is not a feature.
func TestReportRendersUISlowestSpan(t *testing.T) {
	out := render(t, build(t, threeResourceUIHookLog()))
	if !strings.Contains(out, "UI-hook slowest span 8200 ms") {
		t.Errorf("report does not render UI-hook slowest span:\n%s", out)
	}
}

// Finding 8: "top N" must reflect actual truncation, not just the printed
// row count -- two rows out of two total must not read as "top 2", which
// implies more exist and were cut.
func TestReportTopNOmittedWhenNotTruncated(t *testing.T) {
	out := render(t, build(t, threeResourceUIHookLog()))
	if strings.Contains(out, "SLOWEST RESOURCES (addresses masked, top") {
		t.Errorf("report claims truncation for a SLOWEST RESOURCES list that was not truncated:\n%s", out)
	}
	if !strings.Contains(out, "SLOWEST RESOURCES (addresses masked)\n") {
		t.Errorf("report does not render the untruncated SLOWEST RESOURCES header:\n%s", out)
	}
	if strings.Contains(out, "BY RESOURCE TYPE (top") {
		t.Errorf("report claims truncation for a BY RESOURCE TYPE rollup that was not truncated:\n%s", out)
	}
	if !strings.Contains(out, "BY RESOURCE TYPE\n") {
		t.Errorf("report does not render the untruncated BY RESOURCE TYPE header:\n%s", out)
	}
}

func TestReportTopNShownWhenTruncated(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxResourceRows+3; i++ {
		sb.WriteString(uiHookLine(fmt.Sprintf("2026-09-04T09:%02d:00.000000+10:00", i),
			fmt.Sprintf("aws_instance.i%d", i), "aws_instance", "aws", "create", float64(i)))
		sb.WriteString("\n")
	}
	out := render(t, build(t, sb.String()))
	if !strings.Contains(out, fmt.Sprintf("SLOWEST RESOURCES (addresses masked, top %d)", maxResourceRows)) {
		t.Errorf("report does not show truncation when the SLOWEST RESOURCES list was actually cut:\n%s", out)
	}
}

// A retained template must not alias its source message: a string header
// keeps the whole backing array alive, so one template would pin an entire
// log line for the life of the report. template() routes prose through
// MaskProse, whose maskQuotedSpans step always builds its result via
// strings.Builder even when there is nothing to mask, which is what breaks
// the aliasing. The field-count cases are checked separately because zero
// fields is the one where prose is the entire return value, and
// strings.Join on a single-element slice can return that element unchanged.
func TestTemplateDoesNotPinItsSourceMessage(t *testing.T) {
	long := strings.Repeat("x", 4000)
	cases := []string{
		"a plain message with no fields at all " + long,
		"a message with one field padding=" + long,
		"a message with two fields tf_rpc=ReadResource padding=" + long,
	}
	for _, msg := range cases {
		f := logfmt.ParseFields(msg, nil)
		got := template(msg, f)
		if withinBackingArray(got, msg) {
			t.Errorf("template(%q, ...) = %q shares backing memory with msg -- it would pin the whole message alive", msg, got)
		}
	}
}

// Terraform rounds both ends of a resource's timing to the nearest second
// before subtracting (internal/command/views/hook_json.go), so every
// elapsed_seconds carries up to a second of error. Two rows a second apart
// are therefore not reliably ordered, and the section must say so rather
// than presenting a ranking the data cannot support.
func TestReportStatesStructuredTimingResolution(t *testing.T) {
	out := render(t, build(t, threeResourceUIHookLog()))
	idx := strings.Index(out, "SLOWEST RESOURCES")
	if idx < 0 {
		t.Fatalf("report missing SLOWEST RESOURCES section:\n%s", out)
	}
	if !strings.Contains(out[idx:], "whole seconds") {
		t.Errorf("SLOWEST RESOURCES does not state the whole-second resolution of UI-hook timings:\n%s", out)
	}
}

// The caveat belongs to UI-hook timings, so a log with no UI-hook spans
// must not carry it -- an RPC-tier log's durations are exact milliseconds.
func TestResolutionCaveatAbsentWithoutUIHookSpans(t *testing.T) {
	out := render(t, build(t, "2024-02-13T12:11:28.331+0100 [TRACE] provider.aws: Received downstream response: tf_rpc=ApplyResourceChange tf_req_duration_ms=10710\n"))
	if strings.Contains(out, "whole seconds") {
		t.Errorf("report states the UI-hook resolution caveat on a log with no UI-hook spans:\n%s", out)
	}
}

// A real debug-enabled HCP run measured zero response entries, zero request
// entries and zero duration fields, because terraform-plugin-go emits every
// one of those through logging.ProtocolTrace. The same run's raw log still
// carried 1311 terraform.ui lines. So guidance that sends a user to debug
// logging for RPC detail, or that claims debug replaces the JSON with text,
// is wrong on both counts and must not be printed.
func TestGuidanceDoesNotPromiseRPCDetailFromDebugLogging(t *testing.T) {
	for name, in := range map[string]string{
		"structured": threeResourceUIHookLog(),
		"no tier":    "2024-02-13T12:11:28.331+0100 [INFO] backend/local: nothing to profile here\n",
	} {
		t.Run(name, func(t *testing.T) {
			out := render(t, build(t, in))
			for _, wrong := range []string{
				"rather than JSON",
				"enable debug logging on the run and use its raw log",
				"provider RPC detail, enable debug logging",
			} {
				if strings.Contains(out, wrong) {
					t.Errorf("guidance still claims %q, which the debug-run measurement disproved:\n%s", wrong, out)
				}
			}
			// Two gates sit between the provider and the log file:
			// TF_LOG_SDK_PROTO governs what the provider writes, and
			// TF_LOG_PROVIDER (falling back to TF_LOG) governs what
			// Terraform keeps when re-emitting plugin output. Raising
			// only the first was measured to produce a log with no TRACE
			// entries at all, so guidance naming one without the other
			// sends the reader on a wasted run.
			for _, v := range []string{"TF_LOG_SDK_PROTO", "TF_LOG_PROVIDER"} {
				if !strings.Contains(out, v) {
					t.Errorf("guidance does not name %s, one of the two gates that must be open:\n%s", v, out)
				}
			}
		})
	}
}

// --- Task 5: --diagnose publishes the address-attribution measurement. ---

func TestReportRendersHookTypeHistogram(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	if !strings.Contains(out, "HOOK TYPES") {
		t.Errorf("report has no hook-type histogram:\n%s", out)
	}
	if !strings.Contains(out, "apply_start") {
		t.Errorf("histogram does not name apply_start:\n%s", out)
	}
}

// The spec requires --diagnose to report "what the candidate-count
// distribution is across them" (the spans that fell inside at least one
// candidate window) -- Coverage.Candidates was computed and read by nothing.
// two-tier.log carries one Contained span (1 candidate) and one Ambiguous
// span (2 candidates), so the breakdown must show both rows.
func TestReportRendersCandidateCountBreakdown(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	section := out[strings.Index(out, "ADDRESS ATTRIBUTION"):]
	section = section[:strings.Index(section, "\n\n")]
	if !strings.Contains(section, "candidate counts") {
		t.Fatalf("report has no candidate-count breakdown:\n%s", section)
	}
	for _, want := range []string{"1 candidate: 1 span", "2 candidates: 1 span"} {
		if !strings.Contains(section, want) {
			t.Errorf("candidate-count breakdown missing %q:\n%s", want, section)
		}
	}
}

// The breakdown must sit outside the confidence list, as its own group,
// rather than nested under the ambiguous row -- a reader skimming the
// confidence rows top to bottom would otherwise read an indented block
// hanging off ambiguous as ambiguous's own detail, when it in fact covers
// every confidence with at least one candidate (Contained included).
func TestCandidateCountsFollowTheWholeConfidenceListNotJustAmbiguous(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	section := out[strings.Index(out, "ADDRESS ATTRIBUTION"):]
	section = section[:strings.Index(section, "\n\n")]

	ambiguous := strings.Index(section, "ambiguous")
	unattributed := strings.Index(section, "unattributed")
	candidates := strings.Index(section, "candidate counts")
	if ambiguous < 0 || unattributed < 0 || candidates < 0 {
		t.Fatalf("section is missing one of ambiguous/unattributed/candidate counts:\n%s", section)
	}
	if !(ambiguous < unattributed && unattributed < candidates) {
		t.Errorf("candidate counts must follow the whole confidence list (ambiguous, then unattributed, then candidate counts):\n%s", section)
	}
}

// The breakdown's row order must not depend on Go's randomised map
// iteration: two distinct candidate counts seen by the SAME number of spans
// must still print in the same order every time (candidate count ascending),
// not whichever order ranging over the map happened to visit them.
// candidateBreakdown must exclude the 0-candidate bucket: Unattributed's own
// row in the confidence list already covers spans with no candidate at all,
// and this breakdown is documented (and titled, in Render) as "spans with a
// candidate" -- an extra "0 candidates" row would contradict that heading.
// Removing the `if k == 0 { continue }` guard changes real rendered output
// but leaves every other candidateBreakdown/rendering test green, so this
// asserts the exclusion directly rather than relying on an incidental miss.
func TestCandidateBreakdownExcludesZeroCandidates(t *testing.T) {
	got := candidateBreakdown(map[uint32]int{0: 5, 1: 2})
	for _, cc := range got {
		if cc.Candidates == 0 {
			t.Fatalf("candidateBreakdown = %+v, want no 0-candidate row", got)
		}
	}
	if len(got) != 1 {
		t.Errorf("candidateBreakdown = %+v, want exactly the 1-candidate row", got)
	}
}

func TestCandidateBreakdownOrderIsDeterministic(t *testing.T) {
	m := map[uint32]int{3: 2, 1: 2, 2: 2}
	want := []CandidateCount{{Candidates: 1, Spans: 2}, {Candidates: 2, Spans: 2}, {Candidates: 3, Spans: 2}}
	for i := 0; i < 20; i++ {
		got := candidateBreakdown(m)
		if len(got) != len(want) {
			t.Fatalf("candidateBreakdown = %+v, want %+v", got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("candidateBreakdown = %+v, want %+v", got, want)
			}
		}
	}
}

func TestReportRendersCoverageAndConfidence(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	for _, want := range []string{"nameable duration", "contained", "likely", "ambiguous", "unattributed"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

func TestBuildKeepsUnavailablePositionsInAttributionCoverageDenominator(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=10\n" +
		"2021-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=20\n" +
		structuredHookLine + "\n" + structuredCompleteLine + "\n"
	r := build(t, in)
	if !r.HasSpans {
		t.Fatal("HasSpans = false, want admitted RPC evidence measured")
	}
	if r.Quality.Attribution.Spans != 2 || r.Quality.Attribution.TotalMs != 30 {
		t.Fatalf("coverage = %+v, want 2 spans and 30ms; unavailable positions stay in the denominator", r.Quality.Attribution)
	}
}

func TestReportRendersTimingEvidenceReasons(t *testing.T) {
	first := uint32(7)
	r := Report{Quality: model.CaptureQuality{
		RPC: model.TierQuality{Records: 2, Admitted: 1, Rejected: 1, Positioned: 1},
		Issues: []model.QualityIssue{
			{Stage: "rpc_duration", Code: "duration_missing", Count: 1, FirstEntry: &first},
			{Stage: "rpc_duration", Code: "timestamp_before_origin", Count: 1, FirstEntry: &first},
			{Stage: "rpc_duration", Code: "json_syntax", Count: 1, FirstEntry: &first},
			{Stage: "rpc_duration", Code: "schema_invalid", Count: 1, FirstEntry: &first},
		},
	}}
	out := render(t, r)
	for _, want := range []string{"duration_missing", "timestamp_before_origin", "json_syntax", "schema_invalid"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestReportRendersBaselineOffsetAsAConstantNotACheck(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	if !strings.Contains(out, "stream offset") {
		t.Errorf("report has no baseline offset:\n%s", out)
	}
	// The offset is the re-basing constant, not a verification of clock
	// agreement -- it cannot be one, because the two baselines mark
	// different events. Wording that claims otherwise is the defect.
	for _, forbidden := range []string{"clocks agree", "clock check", "verified"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("report claims the offset checks clock agreement (%q):\n%s", forbidden, out)
		}
	}
}

// The disclosure guarantee: --diagnose is the one output Dan shares, and no
// resource address may appear in it.
func TestReportNeverPrintsAnAddress(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	l, err := model.Load(fixture(t, "two-tier.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Contexts) == 0 {
		t.Fatal("fixture carries no contexts; this test proves nothing")
	}
	for _, c := range l.Contexts {
		if c.Address != "" && strings.Contains(out, c.Address) {
			t.Errorf("report printed the address %q", c.Address)
		}
	}
}

// --- Task 5 fix round 1: a log with terraform.ui context but zero RPC spans
// must not present an unqualified 0.0% share or 0-span/0ms confidence lines
// as though attribution ran and failed -- there was nothing to attribute.
// structured-ui.log is exactly this shape: INFO-level terraform.ui with no
// TRACE-level provider RPC entries.

// structuredCompleteLine closes the context structuredHookLine opens -- same
// address and action -- so cc.CompletedPairs() > 0 without any RPC span
// existing anywhere in the input: UI-hook completions and RPC spans come
// from entirely different sinks (span.UIHookBuilder vs span.ReportedBuilder),
// so this line alone cannot manufacture the RPC span this test must have
// none of.
const structuredCompleteLine = `{"@level":"info","@message":"module.module_name[\"key\"].data.local_file.thing: Refresh complete after 1s","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:03.556000+10:00","hook":{"resource":{"addr":"module.module_name[\"key\"].data.local_file.thing","module":"module.module_name[\"key\"]","resource":"data.local_file.thing","implied_provider":"local","resource_type":"local_file","resource_name":"thing","resource_key":null},"action":"read","elapsed_seconds":1,"id_key":"id","id_value":"deadbeef"},"type":"apply_complete"}`

func TestReportDoesNotClaimMeasuredCoverageWithoutRPCSpans(t *testing.T) {
	r := build(t, structuredVersionLine+"\n"+structuredHookLine+"\n"+structuredCompleteLine+"\n")
	if r.SpanCount != 0 {
		t.Fatalf("fixture carries %d RPC spans; this test proves nothing", r.SpanCount)
	}
	if !r.HasContext {
		t.Fatal("fixture carries no address context; this test proves nothing")
	}
	if r.HasSpans {
		t.Fatal("HasSpans = true with zero RPC spans")
	}
	out := render(t, r)
	// Scoped to the ADDRESS ATTRIBUTION section itself: SIZE legitimately
	// prints an unrelated "0.0%" for a fixture with no continuation bytes,
	// and that must not make this test pass vacuously.
	section := out[strings.Index(out, "ADDRESS ATTRIBUTION"):]
	section = section[:strings.Index(section, "\n\n")]
	if strings.Contains(section, "nameable share") {
		t.Errorf("report presents a nameable share with no RPC spans to measure it from:\n%s", section)
	}
	for _, unqualified := range []string{"0 spans, 0 ms", "0.0%"} {
		if strings.Contains(section, unqualified) {
			t.Errorf("report contains an unqualified %q, indistinguishable from a real measurement:\n%s", unqualified, section)
		}
	}
	if !strings.Contains(section, "no provider RPC spans to attribute") {
		t.Errorf("report does not explain that nothing was measured:\n%s", section)
	}
}

// The real fixture this shape was found on: terraform.ui context throughout,
// no hclog-timestamped lines at all, so the stream offset is also never
// derived and must say so rather than rendering a genuine-looking 0ms.
func TestReportOnPureStructuredFixtureDoesNotClaimMeasuredOffset(t *testing.T) {
	out := renderFixture(t, fixture(t, "structured-ui.log"))
	if strings.Contains(out, "stream offset             0 ms") {
		t.Errorf("report renders an unmeasured stream offset as a genuine 0ms:\n%s", out)
	}
	if !strings.Contains(out, "not derived") {
		t.Errorf("report does not say the stream offset was never derived:\n%s", out)
	}
}

// I6: the no-context guidance must attach the debug-logging toggle to what
// produces terraform.ui, and TF_LOG_PROVIDER/TF_LOG_SDK_PROTO to the
// provider RPC entries the log may already carry -- provider-rpc.log has
// exactly that shape: real RPC spans (so SPANS above is non-zero) and no
// terraform.ui stream at all. Telling this reader to set the two RPC
// variables would send them to set what SPANS already proves they set.
func TestNoContextGuidanceAttachesTheDebugToggleToTheStreamItProduces(t *testing.T) {
	out := renderFixture(t, fixture(t, "provider-rpc.log"))
	idx := strings.Index(out, "ADDRESS ATTRIBUTION")
	if idx < 0 {
		t.Fatal("report missing ADDRESS ATTRIBUTION section")
	}
	section := out[idx:]
	section = section[:strings.Index(section, "\n\n")]
	if strings.Contains(section, "contexts") {
		t.Fatalf("fixture carries address context; this test proves nothing:\n%s", section)
	}
	if !strings.Contains(section, "debug") {
		t.Errorf("no-context guidance does not name the debug-logging toggle:\n%s", section)
	}
	for _, v := range []string{"TF_LOG_PROVIDER", "TF_LOG_SDK_PROTO"} {
		if !strings.Contains(section, v) {
			t.Errorf("no-context guidance does not name %s:\n%s", v, section)
		}
	}
}

// --- Fix wave: I3, the counts attribution loses silently must be reported. ---

// contextsWithUnclosedAndZeroExtent builds a log carrying one completed
// context pair (so HasContext is true) plus one context still open at
// end-of-log, whose own End lands on the log's last timestamp -- Start
// equal to End, the exact zero-extent shape a cut-off capture produces.
func contextsWithUnclosedAndZeroExtent() string {
	const stillRunning = `{"@level":"info","@message":"aws_instance.b: Still creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:04.000000+10:00","hook":{"resource":{"addr":"aws_instance.b","module":"","resource":"aws_instance.b","implied_provider":"aws","resource_type":"aws_instance","resource_name":"b","resource_key":null},"action":"create"},"type":"apply_start"}`
	return structuredVersionLine + "\n" + structuredHookLine + "\n" + structuredCompleteLine + "\n" + stillRunning + "\n"
}

func TestReportCountsUnclosedAndZeroExtentContexts(t *testing.T) {
	r := build(t, contextsWithUnclosedAndZeroExtent())
	if !r.HasContext {
		t.Fatal("fixture carries no address context; this test proves nothing")
	}
	if r.UnclosedContexts != 1 {
		t.Errorf("UnclosedContexts = %d, want 1", r.UnclosedContexts)
	}
	if r.ZeroExtentContexts != 1 {
		t.Errorf("ZeroExtentContexts = %d, want 1", r.ZeroExtentContexts)
	}

	out := render(t, r)
	for _, want := range []string{"unclosed contexts         1", "zero-extent contexts      1"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not render %q:\n%s", want, out)
		}
	}
}

// Substance first, caveats after: once a log carries address context, how
// much of it there is (contexts, unclosed, zero-extent) must lead, with the
// data-quality counts (malformed lines, unmatched terminators) following --
// not the reverse, which is what a reader with real context would see if
// the section opened with a wall of zeros before saying anything was found
// at all.
func TestHasContextBranchLeadsWithContextCountsBeforeDataQualityCounts(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	section := out[strings.Index(out, "ADDRESS ATTRIBUTION"):]
	section = section[:strings.Index(section, "\n\n")]

	contexts := strings.Index(section, "contexts")
	malformed := strings.Index(section, "context lines malformed")
	unmatched := strings.Index(section, "unmatched terminators")
	if contexts < 0 || malformed < 0 || unmatched < 0 {
		t.Fatalf("section is missing contexts/malformed/unmatched:\n%s", section)
	}
	if !(contexts < malformed && malformed < unmatched) {
		t.Errorf("has-context branch must lead with contexts, then the data-quality counts:\n%s", section)
	}
}

// C2: a capture killed mid-run has every resource STARTED and none
// COMPLETED, so CompletedPairs() is 0 even though real context was
// collected. Gating on CompletedPairs() reported "no address context in
// this log" here and told the reader to enable a stream their log already
// carries -- structuredHookLine alone (apply_start, never closed) is
// exactly this shape.
func TestReportRecognisesContextFromATruncatedCaptureWithNoCompletedPairs(t *testing.T) {
	r := build(t, structuredVersionLine+"\n"+structuredHookLine+"\n")
	if r.Contexts == 0 {
		t.Fatal("fixture carries no contexts; this test proves nothing")
	}
	if !r.HasContext {
		t.Errorf("HasContext = false, want true -- a truncated capture still carries context")
	}

	out := render(t, r)
	if strings.Contains(out, "no address context in this log") {
		t.Errorf("report claims no address context for a truncated capture that plainly has some:\n%s", out)
	}
}

// I1: NameableShare's TotalMs == 0 guard returns a plain 0, which prints
// identically to a genuine "nothing was nameable" 0.0% measurement. The
// !r.HasSpans branch already distinguishes "no spans exist"; this is the
// sibling case, "spans exist and every one of them measured 0ms".
func TestReportDoesNotPrintAnUnmarkedZeroNameableShareWhenEverySpanIsZeroMs(t *testing.T) {
	in := structuredVersionLine + "\n" + structuredHookLine + "\n" + completionHookLine + "\n" +
		"2022-12-15T00:16:20.800Z [TRACE] provider.local: Received downstream response: tf_rpc=ReadDataSource tf_req_duration_ms=0\n"
	r := build(t, in)
	if !r.HasContext {
		t.Fatal("fixture carries no address context; this test proves nothing")
	}
	if !r.HasSpans {
		t.Fatal("HasSpans = false; this test proves nothing (needs at least one RPC span)")
	}
	if r.Quality.Attribution.TotalMs != 0 {
		t.Fatalf("Quality.Attribution.TotalMs = %d, want 0 -- test premise requires every span to measure 0ms", r.Quality.Attribution.TotalMs)
	}

	out := render(t, r)
	if strings.Contains(out, "nameable duration      0.0%") {
		t.Errorf("report renders an unmarked 0.0%% nameable share from a guarded 0/0:\n%s", out)
	}
	if !strings.Contains(out, "unavailable (total RPC duration is 0ms)") {
		t.Errorf("report does not explain that every span measured 0ms:\n%s", out)
	}
}

// A malformed structured line and an unmatched terminator are counted by
// attrib.ContextCollector regardless of whether any pair ever completes, so
// Build must read them unconditionally rather than only inside the
// CompletedPairs() > 0 gate the other new counts sit behind.
func TestReportCountsMalformedStructuredLinesAndUnmatchedTerminators(t *testing.T) {
	const malformed = `{"@level":"info","@timestamp":"2026-09-04T09:15:05.000000+10:00","type":"apply_start","hook":`
	const unmatchedTerminator = `{"@level":"info","@message":"aws_instance.c: Creation complete","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:06.000000+10:00","hook":{"resource":{"addr":"aws_instance.c","module":"","resource":"aws_instance.c","implied_provider":"aws","resource_type":"aws_instance","resource_name":"c","resource_key":null},"action":"create"},"type":"apply_complete"}`
	r := build(t, structuredVersionLine+"\n"+malformed+"\n"+unmatchedTerminator+"\n")

	if r.MalformedStructuredLines != 1 {
		t.Errorf("MalformedStructuredLines = %d, want 1", r.MalformedStructuredLines)
	}
	if r.UnmatchedTerminators != 1 {
		t.Errorf("UnmatchedTerminators = %d, want 1", r.UnmatchedTerminators)
	}
}

// This fixture's malformed line and unmatched terminator never open a
// context (the malformed one fails to decode at all; the terminator has no
// matching start), so HasContext is false here -- exactly the "collected and
// read by nothing" shape: both counts must still reach the rendered report,
// not just the Report struct, even though the no-context branch is what
// Render takes.
func TestReportRendersMalformedAndUnmatchedCountsWithNoAddressContext(t *testing.T) {
	const malformed = `{"@level":"info","@timestamp":"2026-09-04T09:15:05.000000+10:00","type":"apply_start","hook":`
	const unmatchedTerminator = `{"@level":"info","@message":"aws_instance.c: Creation complete","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:06.000000+10:00","hook":{"resource":{"addr":"aws_instance.c","module":"","resource":"aws_instance.c","implied_provider":"aws","resource_type":"aws_instance","resource_name":"c","resource_key":null},"action":"create"},"type":"apply_complete"}`
	r := build(t, structuredVersionLine+"\n"+malformed+"\n"+unmatchedTerminator+"\n")
	if r.HasContext {
		t.Fatal("fixture carries address context; this test proves nothing (needs HasContext = false)")
	}

	out := render(t, r)
	section := out[strings.Index(out, "ADDRESS ATTRIBUTION"):]
	section = section[:strings.Index(section, "\n\n")]
	if !strings.Contains(section, "context lines malformed   1") {
		t.Errorf("report drops the malformed-line count with no address context:\n%s", section)
	}
	if !strings.Contains(section, "unmatched terminators     1") {
		t.Errorf("report drops the unmatched-terminator count with no address context:\n%s", section)
	}
}

// The report carries the three figures that size a request-id scope: how
// many distinct calls the log names, how much traffic each wears, and how
// much of that traffic hides its id on a continuation line the scan does not
// parse for fields.
//
// They are reported together because they are only useful together. A mean
// scope size says nothing without the spread; a spread says nothing about
// what a header-only reader would miss.
func TestReportSizesTheRequestIDScope(t *testing.T) {
	const ts = "2022-12-15T00:16:20.800Z [TRACE] provider.aws: "
	in := ts + "Sending request downstream: tf_req_id=abc123\n" +
		ts + "Called provider defined validator.String: tf_req_id=abc123\n" +
		ts + "Received downstream response: tf_req_id=abc123 tf_rpc=ReadResource tf_req_duration_ms=5\n" +
		ts + "HTTP Response Received: @module=aws\n" +
		"  http.duration=10705 tf_req_id=def456\n"

	// The fixture names two ids but only ONE of them on a header. abc123 is
	// on three header lines; def456 is on a continuation, so the sniffer --
	// which reads fields from headers, like everything else downstream --
	// never sees it. That is the point of reporting the three together: the
	// spread describes the calls a header-only reader can find, and the
	// continuation figure is the only sign the others exist.
	out := render(t, build(t, in))
	for _, want := range []string{
		"distinct req ids          1",
		"entries per req id        min 3, median 3, max 3",
		"of those, carrying an id 1 line(s), costing 1 entry their only id",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

// A log with no request ids reports no spread at all, rather than a median
// over an empty set -- which would read as a measurement of calls carrying
// no entries instead of a log carrying no calls.
func TestReportOmitsTheRequestIDSpreadWhenThereAreNone(t *testing.T) {
	out := render(t, build(t, "2022-12-15T00:16:20.800Z [TRACE] a: nothing structured here\n"))
	for _, unwanted := range []string{"distinct req ids", "entries per req id"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("report carries %q for a log with no request ids:\n%s", unwanted, out)
		}
	}
}

// The report states how many responses carried an id against how many there
// were, because their difference is the number of spans that would have no
// id to scope by. The value column aligns with the req id fields row above
// it, because this line is a breakdown of that figure, not a separate counter.
func TestReportStatesResponsesCarryingARequestId(t *testing.T) {
	const ts = "2022-12-15T00:16:20.800Z [TRACE] provider.aws: "
	out := render(t, build(t, ts+"Received downstream response: tf_req_id=abc tf_req_duration_ms=5\n"+
		ts+"Received downstream response: tf_req_duration_ms=9\n"))
	if want := "on a response           1 of 2 responses"; !strings.Contains(out, want) {
		t.Errorf("report missing %q:\n%s", want, out)
	}
}
