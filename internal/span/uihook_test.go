package span

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// scanUIInto scans in through a UIHookBuilder, exactly as
// span.ReportedBuilder's tests scan through a ReportedBuilder.
func scanUIInto(t *testing.T, in string, b *UIHookBuilder) {
	t.Helper()
	var comps logfmt.Interner
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, b); err != nil {
		t.Fatalf("Scan: %v", err)
	}
}

const uiRefreshComplete = `{"@level":"info","@message":"module.m[\"key\"].data.local_file.thing: Refresh complete after 0s [id=abc]","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.601000+10:00","hook":{"resource":{"addr":"module.m[\"key\"].data.local_file.thing","module":"module.m[\"key\"]","resource":"data.local_file.thing","implied_provider":"local","resource_type":"local_file","resource_name":"thing","resource_key":null},"action":"read","id_key":"id","id_value":"abc","elapsed_seconds":0},"type":"apply_complete"}`

// TestUIHookBuilderElapsedSecondsZero is the trap: elapsed_seconds:0 must
// still yield a span (a genuinely fast operation), not be treated as absent.
func TestUIHookBuilderElapsedSecondsZero(t *testing.T) {
	var b UIHookBuilder
	scanUIInto(t, uiRefreshComplete+"\n", &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].DurationMs != 0 {
		t.Errorf("DurationMs = %d, want 0", got[0].DurationMs)
	}
}

func uiLineWith(ts, typ string, elapsed float64, includeElapsed bool) string {
	elapsedField := ""
	if includeElapsed {
		elapsedField = fmt.Sprintf(`,"elapsed_seconds":%v`, elapsed)
	}
	return fmt.Sprintf(`{"@level":"info","@message":"aws_instance.example: done","@module":"terraform.ui","@timestamp":"%s","hook":{"resource":{"addr":"aws_instance.example","module":"","resource":"aws_instance.example","implied_provider":"aws","resource_type":"aws_instance","resource_name":"example","resource_key":null},"action":"create"%s},"type":"%s"}`,
		ts, elapsedField, typ)
}

// apply_progress carries a partial "still working" elapsed_seconds and must
// never produce a span -- treating it as a completion would double-count and
// inflate durations. This is the trap the spec calls out explicitly.
func TestUIHookBuilderApplyProgressProducesNoSpan(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:03.000000+10:00", "apply_progress", 1.2, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("got %d spans from apply_progress, want 0", len(got))
	}
}

// apply_errored carries a real total elapsed_seconds and must produce a
// span, or slow failures go missing from the report.
func TestUIHookBuilderApplyErroredProducesSpan(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:05.500000+10:00", "apply_errored", 2.5, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].DurationMs != 2500 {
		t.Errorf("DurationMs = %d, want 2500", got[0].DurationMs)
	}
}

// A fractional elapsed_seconds must round, not truncate.
func TestUIHookBuilderElapsedSecondsRounds(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:05.500000+10:00", "apply_complete", 0.0006, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].DurationMs != 1 {
		t.Errorf("DurationMs = %d, want 1 (0.6ms rounds up)", got[0].DurationMs)
	}
}

// A multi-second fractional duration must convert precisely.
func TestUIHookBuilderMultiSecondFractionalDuration(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:05.500000+10:00", "apply_complete", 2.5, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].DurationMs != 2500 {
		t.Errorf("DurationMs = %d, want 2500", got[0].DurationMs)
	}
}

// A clamped start must still report the full DurationMs: clamping must never
// alter DurationMs, exactly as ReportedBuilder guarantees.
func TestUIHookBuilderClampedStartKeepsFullDuration(t *testing.T) {
	// This is the very first (and only) line the builder sees, so its own
	// timestamp becomes the base and EndMs is 0 -- a duration longer than
	// the offset from the base must clamp StartMs to 0 without touching
	// DurationMs.
	in := uiLineWith("2026-09-04T09:15:05.500000+10:00", "apply_complete", 5, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if !got[0].StartClamped {
		t.Fatal("StartClamped = false, want true")
	}
	if got[0].StartMs != 0 {
		t.Errorf("StartMs = %d, want 0", got[0].StartMs)
	}
	if got[0].DurationMs != 5000 {
		t.Errorf("DurationMs = %d, want 5000 even though clamped", got[0].DurationMs)
	}
}

// An unclamped start is EndMs - DurationMs, relative to the first timestamp
// this builder has seen.
func TestUIHookBuilderUnclampedStart(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:00.000000+10:00", "apply_start", 0, false) + "\n" +
		uiLineWith("2026-09-04T09:15:10.000000+10:00", "apply_complete", 4, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].EndMs != 10000 {
		t.Errorf("EndMs = %d, want 10000", got[0].EndMs)
	}
	if got[0].StartMs != 6000 {
		t.Errorf("StartMs = %d, want 6000", got[0].StartMs)
	}
	if got[0].StartClamped {
		t.Error("StartClamped = true, want false")
	}
}

// A malformed JSON line must be skipped silently, counted, and must not
// abort the scan -- a well-formed line before and after it must still yield
// spans.
func TestUIHookBuilderMalformedLineSkippedAndCounted(t *testing.T) {
	good := uiLineWith("2026-09-04T09:15:05.500000+10:00", "apply_complete", 1, true)
	// Not valid JSON, but still passes IsStructuredLine's cheap marker check
	// (it contains "@level": and "@timestamp": and starts with "{"), so it
	// reaches the builder rather than being filtered upstream.
	malformed := `{"@level":"info","@timestamp":"2026-09-04T09:15:05.600000+10:00", not valid json`
	in := good + "\n" + malformed + "\n" + good + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2 (malformed line must not abort the scan)", len(got))
	}
	if b.Malformed() != 1 {
		t.Errorf("Malformed() = %d, want 1", b.Malformed())
	}
}

// A structured line with no hook object (e.g. type:"version") must produce
// no span and no error -- it decodes fine, it just isn't a hook line.
func TestUIHookBuilderNoHookProducesNoSpanNoError(t *testing.T) {
	const versionLine = `{"@level":"info","@message":"Terraform 1.14.9","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.113402+10:00","terraform":"1.14.9","type":"version","ui":"1.2"}`
	var b UIHookBuilder
	scanUIInto(t, versionLine+"\n", &b)
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("got %d spans, want 0", len(got))
	}
	if b.Malformed() != 0 {
		t.Errorf("Malformed() = %d, want 0 -- a decodable line with no hook is not malformed", b.Malformed())
	}
}

// Every field is mapped from the right JSON key.
func TestUIHookBuilderMapsFields(t *testing.T) {
	var b UIHookBuilder
	scanUIInto(t, uiRefreshComplete+"\n", &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	s := got[0]
	if s.Address != `module.m["key"].data.local_file.thing` {
		t.Errorf("Address = %q", s.Address)
	}
	if s.ResourceType != "local_file" {
		t.Errorf("ResourceType = %q", s.ResourceType)
	}
	if s.Provider != "local" {
		t.Errorf("Provider = %q", s.Provider)
	}
	if s.RPC != "read" {
		t.Errorf("RPC = %q, want the hook action", s.RPC)
	}
	if s.Fidelity != FidelityUIReported {
		t.Errorf("Fidelity = %v, want FidelityUIReported", s.Fidelity)
	}
}

// UIHookBuilder shares its dedup cache (dedupCache) with ReportedBuilder: a
// repeated RPC value across two spans must land in a single cache entry
// rather than being cloned per span.
func TestUIHookBuilderDedupsRepeatedValues(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:00.000000+10:00", "apply_complete", 1, true) + "\n" +
		uiLineWith("2026-09-04T09:15:01.000000+10:00", "apply_complete", 2, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2", len(got))
	}
	if got[0].RPC != "create" || got[1].RPC != "create" {
		t.Fatalf("RPC = %q, %q, want both %q", got[0].RPC, got[1].RPC, "create")
	}
	if n := len(b.kept); n != 3 { // RPC "create", Provider "aws", ResourceType "aws_instance"
		t.Errorf("dedup cache holds %d entries, want 3", n)
	}
}

// Retained ResourceType/Provider/RPC strings must not alias the scanner's
// per-line buffer, exactly as ReportedBuilder.retain guarantees, and must
// survive the scanner reusing its buffers for later lines.
func TestUIHookBuilderRetainsAcrossScan(t *testing.T) {
	var sb strings.Builder
	var want []string
	for i := 0; i < 50; i++ {
		v := fmt.Sprintf("provider-%03d-abcdefghijklmnopqrstuvwxyz", i)
		want = append(want, v)
		sb.WriteString(fmt.Sprintf(`{"@level":"info","@timestamp":"2026-09-04T09:15:%02d.000000+10:00","hook":{"resource":{"addr":"aws_instance.example[%d]","module":"","resource":"aws_instance.example","implied_provider":%q,"resource_type":"aws_instance","resource_name":"example","resource_key":%d},"action":"create","elapsed_seconds":1},"type":"apply_complete"}`, i, i, v, i))
		sb.WriteString("\n")
	}
	var b UIHookBuilder
	scanUIInto(t, sb.String(), &b)
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

// Address is high-cardinality and must be cloned directly rather than
// dedup-cached, but it still must not alias the scanner's per-line buffer:
// each of many distinct addresses seen within one scan must retain its own
// value once the scan has moved on and reused that buffer for later lines.
func TestUIHookBuilderAddressDoesNotAliasScannerBuffer(t *testing.T) {
	var sb strings.Builder
	var want []string
	for i := 0; i < 50; i++ {
		addr := fmt.Sprintf("aws_instance.example[%d]", i)
		want = append(want, addr)
		sb.WriteString(fmt.Sprintf(`{"@level":"info","@timestamp":"2026-09-04T09:15:%02d.000000+10:00","hook":{"resource":{"addr":%q,"module":"","resource":"aws_instance.example","implied_provider":"aws","resource_type":"aws_instance","resource_name":"example","resource_key":%d},"action":"create","elapsed_seconds":1},"type":"apply_complete"}`, i, addr, i))
		sb.WriteString("\n")
	}
	var b UIHookBuilder
	scanUIInto(t, sb.String(), &b)
	got := b.Spans()
	if len(got) != len(want) {
		t.Fatalf("got %d spans, want %d", len(got), len(want))
	}
	for i, s := range got {
		if s.Address != want[i] {
			t.Errorf("span %d Address = %q, want %q", i, s.Address, want[i])
		}
	}
}

// --- Whole-branch review, findings 5 and 6: saturated durations and
// backwards UI-hook timestamps must be counted, not just silently clamped.

// A UI-hook line whose timestamp is earlier than the builder's base must
// clamp to a 0 offset without wrapping, and must be counted -- mirroring
// logfmt.Scan's own BackwardsTimestamps counter -- since a silent clamp
// shortens the derived UI-hook wall-clock with no visible trace of why.
func TestUIHookBuilderCountsBackwardsTimestamps(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:10.000000+10:00", "apply_complete", 1, true) + "\n" +
		uiLineWith("2026-09-04T09:15:00.000000+10:00", "apply_complete", 1, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	if b.BackwardsTimestamps() != 1 {
		t.Errorf("BackwardsTimestamps() = %d, want 1", b.BackwardsTimestamps())
	}
}

// An elapsed_seconds absurd enough to overflow uint32 milliseconds must
// saturate DurationMs rather than wrap, and must be counted: a saturated
// duration is otherwise indistinguishable from a real one once folded into
// a sum such as UI-hook total time or the per-type rollup.
func TestUIHookBuilderCountsSaturatedDuration(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:05.500000+10:00", "apply_complete", 1e300, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].DurationMs != math.MaxUint32 {
		t.Errorf("DurationMs = %d, want math.MaxUint32", got[0].DurationMs)
	}
	if b.Saturated() != 1 {
		t.Errorf("Saturated() = %d, want 1", b.Saturated())
	}
}

// A normal duration must never be counted as saturated.
func TestUIHookBuilderDoesNotCountNormalDurationAsSaturated(t *testing.T) {
	in := uiLineWith("2026-09-04T09:15:05.500000+10:00", "apply_complete", 2.5, true) + "\n"
	var b UIHookBuilder
	scanUIInto(t, in, &b)
	if b.Saturated() != 0 {
		t.Errorf("Saturated() = %d, want 0", b.Saturated())
	}
}
