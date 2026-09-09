package model

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", name)
}

// Load must retain one Entry per logical entry Scan counted -- the index is
// what phase 3's raw-log view pages through, and a count that disagrees with
// Stats means entries were dropped or double-counted.
func TestLoadRetainsOneEntryPerLogicalEntry(t *testing.T) {
	l, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if uint64(len(l.Entries)) != l.Stats.Entries {
		t.Errorf("len(Entries) = %d, Stats.Entries = %d", len(l.Entries), l.Stats.Entries)
	}
	if len(l.Entries) == 0 {
		t.Fatal("no entries retained")
	}
}

// Bytes must return every line of a multi-line entry, not just its header.
// Entry.Off/Len cover all of an entry's physical lines, which is what makes
// "jump from a span to its log lines" a slice expression.
func TestBytesCoversAllLinesOfAnEntry(t *testing.T) {
	l, err := Load(fixture(t, "multiline-body.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var multi logfmt.Entry
	for _, e := range l.Entries {
		if e.Lines > 1 {
			multi = e
			break
		}
	}
	if multi.Lines <= 1 {
		t.Fatal("fixture has no multi-line entry")
	}
	got := string(l.Bytes(multi))
	if n := strings.Count(got, "\n"); n < int(multi.Lines)-1 {
		t.Errorf("Bytes returned %d newlines for a %d-line entry:\n%s", n, multi.Lines, got)
	}
}

func TestLoadBuildsBothSpanKinds(t *testing.T) {
	rpc, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rpc.RPCSpans) == 0 {
		t.Error("no RPC spans built from provider-rpc.log")
	}
	ui, err := Load(fixture(t, "structured-ui.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ui.UISpans) == 0 {
		t.Error("no UI-hook spans built from structured-ui.log")
	}
}

func TestLoadRetainsTimingEvidenceAndUIOrigin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.log")
	input := "{\"@level\":\"info\",\"@timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"version\"}\n" +
		"{\"@level\":\"info\",\"@timestamp\":\"2026-01-01T00:00:01Z\",\"type\":\"apply_complete\",\"hook\":{\"elapsed_seconds\":0}}\n" +
		"2026-01-01T00:00:02.000Z [TRACE] p: Received downstream response\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.UIOrigin.IsZero() || l.UIEvidence.Records != 1 || l.RPCEvidence.Records != 1 || l.RPCEvidence.Rejected["duration_missing"].Count != 1 {
		t.Fatalf("origin/evidence = %v, %+v, %+v", l.UIOrigin, l.UIEvidence, l.RPCEvidence)
	}
	if string(l.Data) != input || len(l.Entries) != 3 || l.UISaturatedDurations != 0 {
		t.Fatalf("load retention changed: entries=%d data=%q saturated=%d", len(l.Entries), l.Data, l.UISaturatedDurations)
	}
}

func TestLoadNamesTheFileOnError(t *testing.T) {
	_, err := Load("no-such-file.log")
	if err == nil {
		t.Fatal("Load returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestLoadAttributesRPCSpansToAddresses(t *testing.T) {
	l, err := Load(fixture(t, "two-tier.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !l.HasAddressContext() {
		t.Fatal("HasAddressContext = false, want true for a log carrying terraform.ui hook pairs")
	}
	if len(l.Attribs) != len(l.RPCSpans) {
		t.Fatalf("len(Attribs) = %d, len(RPCSpans) = %d -- the table must stay parallel",
			len(l.Attribs), len(l.RPCSpans))
	}
}

// A log can carry address context with no RPC spans at all -- structured-ui.log
// is exactly this shape: completed apply_start/apply_complete (and
// apply_start/apply_errored) pairs, and no provider-RPC lines whatsoever.
// attrib.Correlate always allocates make([]Attribution, len(spans)), so with
// zero spans Attribs is a non-nil, zero-length slice rather than nil. Address
// context must still be reported true: HasAddressContext is a property of
// whether the log carries context at all, not of len(Attribs).
func TestLoadReportsAddressContextWithNoRPCSpans(t *testing.T) {
	l, err := Load(fixture(t, "structured-ui.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(l.RPCSpans) != 0 {
		t.Fatalf("fixture has %d RPC spans, want 0 -- test premise requires none", len(l.RPCSpans))
	}
	if len(l.Contexts) == 0 {
		t.Fatal("fixture produced no contexts -- test premise requires at least one completed pair")
	}
	if !l.HasAddressContext() {
		t.Error("HasAddressContext = false, want true: this log carries completed address context")
	}
	if len(l.Attribs) != 0 {
		t.Errorf("len(Attribs) = %d, want 0 -- there are no RPC spans to attribute", len(l.Attribs))
	}
}

// C2: a capture killed mid-run -- every resource started, none finished --
// has real context windows even though no pair ever completes. Gating on
// CompletedPairs() reported no address context at all for a log that
// plainly carries some.
func TestLoadReportsAddressContextWhenNoContextHasClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated.log")
	const line = `{"@level":"info","@message":"aws_instance.a: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:03.000000+10:00","hook":{"resource":{"addr":"aws_instance.a","module":"","resource":"aws_instance.a","implied_provider":"aws","resource_type":"aws_instance","resource_name":"a","resource_key":null},"action":"create"},"type":"apply_start"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(l.Contexts) == 0 {
		t.Fatal("fixture produced no contexts; test premise requires at least one open context")
	}
	for _, c := range l.Contexts {
		if !c.Unclosed {
			t.Fatal("fixture produced a closed context; test premise requires every context to remain open")
		}
	}
	if !l.HasAddressContext() {
		t.Error("HasAddressContext = false, want true: an open (never-closed) context is still address context")
	}
}

func TestLoadBuildsNoAttributionTableWithoutContext(t *testing.T) {
	// A log with no terraform.ui stream has no address context at all, which
	// is a property of the LOG. The table is not allocated rather than being
	// filled with Unattributed, so "this log cannot answer the question" and
	// "this log did not answer it for this span" stay distinguishable.
	l, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if l.HasAddressContext() {
		t.Error("HasAddressContext = true, want false")
	}
	if l.Attribs != nil {
		t.Errorf("Attribs = %v, want nil", l.Attribs)
	}
}

// AttributionForEntry is exercised elsewhere only indirectly, via
// internal/tui/timeline_test.go, left over from before it moved to this
// package. This pins its own contract directly: a lookup by the closing
// entry's ordinal, not by position, and a zero Attribution for an ordinal no
// RPC span closed.
func TestAttributionForEntryLooksUpByClosingEntryNotPosition(t *testing.T) {
	l := &Log{
		RPCSpans: []span.Span{{Entry: 5}, {Entry: 9}},
		Attribs: []attrib.Attribution{
			{Confidence: attrib.Contained, Address: "aws_instance.a"},
			{Confidence: attrib.Ambiguous, Candidates: 2},
		},
	}
	if got := l.AttributionForEntry(9); got.Confidence != attrib.Ambiguous || got.Candidates != 2 {
		t.Errorf("AttributionForEntry(9) = %+v, want the second span's Ambiguous attribution", got)
	}
	if got := l.AttributionForEntry(5); got.Address != "aws_instance.a" {
		t.Errorf("AttributionForEntry(5) = %+v, want the first span's attribution", got)
	}
	if got := l.AttributionForEntry(123); got != (attrib.Attribution{}) {
		t.Errorf("AttributionForEntry(123) = %+v, want the zero Attribution for an entry no span closed", got)
	}
}

// A scope is the ascending indices of every entry carrying one call's request
// id, which is what lets the raw log show a call rather than the log around
// it. The fixture's two calls INTERLEAVE, so a scope built by taking a
// contiguous run of entries fails here -- which is the defect most likely to
// be written by accident.
func TestScopeForCollectsOneCallsEntriesAcrossAnInterleavedLog(t *testing.T) {
	l, err := Load(fixture(t, "interleaved-calls.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var a uint16
	for _, s := range l.RPCSpans {
		if s.ResourceType == "aws_subnet" {
			a = s.ReqID
		}
	}
	if a == 0 {
		t.Fatal("the aws_subnet span carries no request id")
	}
	got := l.ScopeFor(a)
	// The fixture opens with a multi-line "#" header comment, which Scan
	// indexes as untimestamped entry 0 rather than discarding -- so the
	// eight log lines are entries 1 through 8, and the first index below is
	// 1, not 0.
	if want := []int{1, 3, 6, 7}; !slices.Equal(got, want) {
		t.Errorf("ScopeFor = %v, want %v -- the call's entries, not a contiguous run", got, want)
	}
}

// Id 0 is "no request id", not a call whose id happens to be zero, so it
// scopes to nothing rather than to every entry that carries no id.
func TestScopeForReturnsNothingForTheAbsentId(t *testing.T) {
	l, err := Load(fixture(t, "interleaved-calls.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := l.ScopeFor(0); got != nil {
		t.Errorf("ScopeFor(0) = %v, want nil", got)
	}
}

// Attribution must never rewrite a span's timeline. model.PackLanes refuses a
// mixed-fidelity slice on the premise that StartMs is not comparable across
// builders, and that premise is keyed on Fidelity -- so an in-place re-base
// would falsify it invisibly.
func TestLoadDoesNotRewriteSpanTimelines(t *testing.T) {
	path := fixture(t, "two-tier.log")

	// The control: what ReportedBuilder produces with no attribution in the
	// scan at all. Comparing Load against ITSELF would pass even if both
	// runs re-based identically, which is exactly the bug being excluded.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	comps := &logfmt.Interner{}
	reqIDs := &logfmt.Interner{}
	var rb span.ReportedBuilder
	rb.Comps = comps
	if _, err := logfmt.Scan(bytes.NewReader(data), comps, reqIDs, &rb); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := rb.Spans()

	l, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(l.RPCSpans) != len(want) {
		t.Fatalf("Load built %d spans, control built %d", len(l.RPCSpans), len(want))
	}
	for i, got := range l.RPCSpans {
		if got.StartMs != want[i].StartMs || got.EndMs != want[i].EndMs {
			t.Fatalf("span %d timeline = [%d,%d), want [%d,%d) -- attribution "+
				"must correlate on local copies, never rewrite the span",
				i, got.StartMs, got.EndMs, want[i].StartMs, want[i].EndMs)
		}
	}
}
