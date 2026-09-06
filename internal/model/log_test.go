package model

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	var rb span.ReportedBuilder
	rb.Comps = comps
	if _, err := logfmt.Scan(bytes.NewReader(data), comps, &rb); err != nil {
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
