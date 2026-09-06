package attrib

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// collect runs the collector over a fixture and returns its contexts.
func collect(t *testing.T, path string) (*ContextCollector, []Context) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return collectLines(t, string(data))
}

// collectLines is collect's own scan step, over a log given inline rather
// than read from testdata/context.log -- for a case that needs one or two
// synthetic lines and would otherwise have to grow the shared fixture, whose
// contents several other tests already index into by position.
func collectLines(t *testing.T, content string) (*ContextCollector, []Context) {
	t.Helper()
	var c ContextCollector
	if _, err := logfmt.Scan(strings.NewReader(content), &logfmt.Interner{}, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return &c, c.Contexts()
}

func TestCollectorPairsStartWithComplete(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	if len(ctxs) != 5 {
		t.Fatalf("contexts = %d, want 5", len(ctxs))
	}
	got := ctxs[0]
	if got.Address != "data.local_file.a" {
		t.Errorf("Address = %q, want data.local_file.a", got.Address)
	}
	if got.ResourceType != "local_file" {
		t.Errorf("ResourceType = %q, want local_file", got.ResourceType)
	}
	if got.Action != "read" {
		t.Errorf("Action = %q, want read", got.Action)
	}
	if !got.IsData {
		t.Error("IsData = false, want true for data.local_file.a")
	}
	if got.Unclosed {
		t.Error("Unclosed = true, want false")
	}
	if d := got.End.Sub(got.Start); d != 2*time.Second {
		t.Errorf("window = %v, want 2s", d)
	}
}

// A refresh is apply_start/apply_complete with action:"read" -- there is no
// refresh_* hook type. Asserted here because an earlier design draft assumed
// otherwise and the assumption reached a committed spec.
func TestRefreshIsAnApplyPairWithReadAction(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	if ctxs[0].Action != "read" {
		t.Fatalf("first context action = %q, want read", ctxs[0].Action)
	}
	if !ctxs[0].End.After(ctxs[0].Start) {
		t.Error("refresh produced no window")
	}
}

func TestErroredTerminatesAContext(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	got := ctxs[1]
	if got.Address != `module.m["k"].aws_instance.web[0]` {
		t.Fatalf("Address = %q", got.Address)
	}
	if got.Unclosed {
		t.Error("apply_errored did not close the context")
	}
	// apply_start 09:15:06 -> apply_errored 09:15:09. apply_progress at
	// 09:15:07 must not have closed it.
	if d := got.End.Sub(got.Start); d != 3*time.Second {
		t.Errorf("window = %v, want 3s (apply_progress must not terminate)", d)
	}
}

func TestModuleNameAndKeyAreDecoded(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	got := ctxs[1]
	if got.Module != `module.m["k"]` {
		t.Errorf("Module = %q", got.Module)
	}
	if got.Name != "web" {
		t.Errorf("Name = %q, want web", got.Name)
	}
	if got.Key != "0" {
		t.Errorf("Key = %q, want 0 (a numeric resource_key renders as its literal)", got.Key)
	}
	if ctxs[0].Key != "" {
		t.Errorf("null resource_key = %q, want empty", ctxs[0].Key)
	}
}

// A for_each key and a count key decode to DIFFERENT bracket syntax, end to
// end from the raw structured-output line through to the string a caller
// would actually concatenate onto a resource's name -- module.m.web["mykey"]
// for a for_each key, module.m.db[0] for a count key. Terraform's own JSON
// distinguishes the two only by the resource_key value's JSON TYPE (a
// string versus a number), which is why this decodes real lines rather
// than constructing a Context directly: the distinction has to survive
// encoding/json's own unmarshalling, not just decodeKey in isolation.
func TestKeyDecodesToValidAddressBracketSyntaxForBothJSONKinds(t *testing.T) {
	const lines = `{"@level":"info","@message":"aws_instance.web: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:03.000000+10:00","hook":{"resource":{"addr":"aws_instance.web[\"mykey\"]","module":"","resource":"aws_instance.web[\"mykey\"]","implied_provider":"aws","resource_type":"aws_instance","resource_name":"web","resource_key":"mykey"},"action":"create"},"type":"apply_start"}
{"@level":"info","@message":"aws_instance.db: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:04.000000+10:00","hook":{"resource":{"addr":"aws_instance.db[0]","module":"","resource":"aws_instance.db[0]","implied_provider":"aws","resource_type":"aws_instance","resource_name":"db","resource_key":0},"action":"create"},"type":"apply_start"}
`
	_, ctxs := collectLines(t, lines)
	if len(ctxs) != 2 {
		t.Fatalf("contexts = %d, want 2", len(ctxs))
	}
	if got, want := ctxs[0].Name+"["+ctxs[0].Key+"]", `web["mykey"]`; got != want {
		t.Errorf("for_each (string) key built address suffix %q, want %q", got, want)
	}
	if got, want := ctxs[1].Name+"["+ctxs[1].Key+"]", "db[0]"; got != want {
		t.Errorf("count (number) key built address suffix %q, want %q", got, want)
	}
}

func TestUnclosedContextEndsAtLastTimestamp(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	// Find the final unclosed orphan context (second orphan, opened at 09:15:12,
	// unclosed at end-of-log which is 09:15:13 when aws_instance.final opens).
	var got Context
	for _, c := range ctxs {
		if c.Address == "aws_instance.orphan" && c.Start.Equal(c.End.Add(-time.Second)) {
			got = c
			break
		}
	}
	if got.Address == "" {
		t.Fatalf("no orphan context with End > Start found")
	}
	if !got.Unclosed {
		t.Error("Unclosed = false, want true")
	}
	// The fixture now continues after the second orphan start at 09:15:12, so
	// lastTS is 09:15:13. The unclosed orphan's End must equal that later timestamp.
	wantStart, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:12.000000+10:00")
	wantEnd, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:13.000000+10:00")
	if !got.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", got.Start, wantStart)
	}
	if !got.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v (strictly later than Start)", got.End, wantEnd)
	}
}

func TestCompletedPairsCountsOnlyClosedContexts(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	if got := c.CompletedPairs(); got != 2 {
		t.Errorf("CompletedPairs = %d, want 2", got)
	}
}

func TestTypeCountsHistogram(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	want := map[string]uint64{
		"version":        1,
		"apply_start":    5,
		"apply_complete": 2,
		"apply_progress": 1,
		"apply_errored":  1,
	}
	got := c.TypeCounts()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("TypeCounts[%q] = %d, want %d", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("TypeCounts has %d keys, want %d: %v", len(got), len(want), got)
	}
}

// The collector must never materialise id_value, which carries real resource
// ids. This mirrors span.uiLine's deliberate omission and keeps the
// disclosure guarantee a property of the struct's shape.
func TestIDValueIsNeverDecoded(t *testing.T) {
	data, err := os.ReadFile("testdata/context.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "SECRETVALUE") {
		t.Fatal("fixture no longer carries id_value; this test proves nothing")
	}
	_, ctxs := collect(t, "testdata/context.log")
	for _, c := range ctxs {
		for _, f := range []string{c.Address, c.Module, c.Name, c.Key, c.ResourceType, c.Action} {
			if strings.Contains(f, "SECRETVALUE") {
				t.Fatalf("id_value reached a Context field: %q", f)
			}
		}
	}
}

func TestFirstTSIsTheFirstParseableTimestamp(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	want, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:02.000000+10:00")
	if !c.FirstTS().Equal(want) {
		t.Errorf("FirstTS = %v, want %v", c.FirstTS(), want)
	}
}

// A duplicate-start for the same address+action closes the prior context
// unclosed at the new start's timestamp, then opens a fresh one.
func TestDuplicateStartClosesAndReopens(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	// Find both orphan contexts (same address and action, different times)
	var orphans []Context
	for _, c := range ctxs {
		if c.Address == "aws_instance.orphan" && c.Action == "create" {
			orphans = append(orphans, c)
		}
	}
	if len(orphans) != 2 {
		t.Fatalf("found %d orphan contexts, want 2", len(orphans))
	}

	// First orphan: opened at 09:15:10, closed by duplicate-start at 09:15:12
	first := orphans[0]
	wantStart1, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:10.000000+10:00")
	if !first.Start.Equal(wantStart1) {
		t.Errorf("first orphan Start = %v, want %v", first.Start, wantStart1)
	}
	if !first.Unclosed {
		t.Error("first orphan Unclosed = false, want true (closed by duplicate-start, not by terminator)")
	}
	wantEnd1, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:12.000000+10:00")
	if !first.End.Equal(wantEnd1) {
		t.Errorf("first orphan End = %v, want %v (closed at second start)", first.End, wantEnd1)
	}

	// Second orphan: opened at 09:15:12, unclosed at end-of-log (09:15:13)
	second := orphans[1]
	if !second.Unclosed {
		t.Error("second orphan Unclosed = false, want true")
	}
	if second.End.Before(second.Start) {
		t.Errorf("second orphan End < Start, which violates interval semantics")
	}
}

// An apply_complete with no prior apply_start is an unmatched terminator: it is
// counted rather than creating a spurious context.
func TestUnmatchedTerminatorCountedNotCreated(t *testing.T) {
	c, ctxs := collect(t, "testdata/context.log")

	// aws_instance.unmatched has an apply_complete but no apply_start.
	// It should NOT appear in contexts.
	for _, ctx := range ctxs {
		if ctx.Address == "aws_instance.unmatched" {
			t.Fatalf("unmatched terminator should not create a context, but found one")
		}
	}

	// The unmatched terminator should be counted.
	if got := c.UnmatchedTerminators(); got != 1 {
		t.Errorf("UnmatchedTerminators = %d, want 1", got)
	}
}

// decodeKey handles null (empty string), JSON numbers (rendered as their
// bare literal), and JSON strings (kept quoted -- see decodeKey's own doc
// comment for why a string key must not be unquoted: "0" the JSON string
// and 0 the JSON number bracket differently in Terraform's own address
// syntax, and a decoded value with the quotes already stripped cannot be
// told apart from the other case any more).
func TestDecodeKey(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"null", "null", ""},
		{"empty", "", ""},
		{"number zero", "0", "0"},
		{"number positive", "42", "42"},
		{"string key", `"mykey"`, `"mykey"`},
		{"string key that looks numeric", `"0"`, `"0"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeKey([]byte(tt.raw))
			if got != tt.want {
				t.Errorf("decodeKey(%s) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
