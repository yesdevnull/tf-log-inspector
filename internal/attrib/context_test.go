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
	if len(ctxs) != 6 {
		t.Fatalf("contexts = %d, want 6", len(ctxs))
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

// A DATA SOURCE READ during apply is an apply_start/apply_complete pair with
// action:"read" (PreApply with plans.Read, in hashicorp/terraform's
// internal/command/views/hook_json.go) -- this is real, and distinct from a
// MANAGED RESOURCE refresh, which is refresh_start/refresh_complete and
// carries no action at all (see TestRefreshHookOpensAndClosesAContext). An
// earlier design draft conflated the two and the mistake reached a
// committed spec, corrected 2026-09-07.
func TestDataSourceReadIsAnApplyPairWithReadAction(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	if ctxs[0].Action != "read" {
		t.Fatalf("first context action = %q, want read", ctxs[0].Action)
	}
	if !ctxs[0].IsData {
		t.Fatal("first context IsData = false, want true -- this test is about a data source read")
	}
	if !ctxs[0].End.After(ctxs[0].Start) {
		t.Error("data source read produced no window")
	}
}

// C1: a MANAGED RESOURCE refresh -- Terraform's plan-time drift-detection
// walk -- is refresh_start/refresh_complete, verified against
// hashicorp/terraform tag v1.14.9's
// internal/command/views/json/message_types.go and hook_json.go. Unlike
// apply_start's operationStart, refreshStart/refreshComplete carry no
// action field at all, so Context.Action is "" for it.
func TestRefreshHookOpensAndClosesAContext(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	got := ctxs[5]
	if got.Address != "aws_instance.tracked" {
		t.Fatalf("Address = %q, want aws_instance.tracked", got.Address)
	}
	if got.ResourceType != "aws_instance" {
		t.Errorf("ResourceType = %q, want aws_instance", got.ResourceType)
	}
	if got.Action != "" {
		t.Errorf("Action = %q, want empty -- refresh_start/refresh_complete carry no action", got.Action)
	}
	if got.IsData {
		t.Error("IsData = true, want false -- this is a managed resource")
	}
	if got.Unclosed {
		t.Error("Unclosed = true, want false -- refresh_complete closed it")
	}
	if d := got.End.Sub(got.Start); d != 2*time.Second {
		t.Errorf("window = %v, want 2s", d)
	}
}

// I2: opensContext/closesContext's full vocabulary, asserted directly rather
// than only through fixture behaviour -- deleting ephemeral_op_* or
// provision_* from either switch left every package green before this
// existed, and the same was true of refresh_* before C1.
func TestOpensAndClosesContextRecogniseEveryLifecycleType(t *testing.T) {
	tests := []struct {
		typ    string
		opens  bool
		closes bool
	}{
		{"apply_start", true, false},
		{"apply_progress", false, false},
		{"apply_complete", false, true},
		{"apply_errored", false, true},
		{"refresh_start", true, false},
		{"refresh_complete", false, true},
		{"ephemeral_op_start", true, false},
		{"ephemeral_op_complete", false, true},
		{"ephemeral_op_errored", false, true},
		{"provision_start", true, false},
		{"provision_complete", false, true},
		{"provision_errored", false, true},
		{"version", false, false},
		{"diagnostic", false, false},
	}
	for _, tt := range tests {
		if got := opensContext(tt.typ); got != tt.opens {
			t.Errorf("opensContext(%q) = %v, want %v", tt.typ, got, tt.opens)
		}
		if got := closesContext(tt.typ); got != tt.closes {
			t.Errorf("closesContext(%q) = %v, want %v", tt.typ, got, tt.closes)
		}
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
	// Find the second orphan context, opened at 09:15:12 and never closed.
	wantStart, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:12.000000+10:00")
	var got Context
	for _, c := range ctxs {
		if c.Address == "aws_instance.orphan" && c.Start.Equal(wantStart) {
			got = c
			break
		}
	}
	if got.Address == "" {
		t.Fatalf("no orphan context starting at %v found", wantStart)
	}
	if !got.Unclosed {
		t.Error("Unclosed = false, want true")
	}
	// The fixture's last line is aws_instance.tracked's refresh_complete at
	// 09:15:16, so that is lastTS -- the unclosed orphan's End must equal it.
	wantEnd, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:16.000000+10:00")
	if !got.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v (the log's last timestamp)", got.End, wantEnd)
	}
}

// M2: a second call to Contexts must not re-run the close-out. Draining
// c.open is itself idempotent once it is already empty, so this pins the
// CONTRACT (a second call is a plain getter) rather than a behaviour a
// second drain would visibly break today -- see Contexts' own doc comment.
func TestContextsIsIdempotentAcrossRepeatedCalls(t *testing.T) {
	c, first := collect(t, "testdata/context.log")
	second := c.Contexts()
	if len(first) != len(second) {
		t.Fatalf("len(second call) = %d, want %d (same as the first)", len(second), len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("Contexts()[%d] changed between calls: %+v -> %+v", i, first[i], second[i])
		}
	}
}

// I3: a context still open at end-of-log whose own End lands on its own
// Start -- a resource still running when the capture was cut -- is
// zero-extent and must be counted so the loss is visible.
func TestZeroExtentContextsCountsAResourceStillRunningAtEndOfLog(t *testing.T) {
	const lines = `{"@level":"info","@message":"aws_instance.a: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:03.000000+10:00","hook":{"resource":{"addr":"aws_instance.a","module":"","resource":"aws_instance.a","implied_provider":"aws","resource_type":"aws_instance","resource_name":"a","resource_key":null},"action":"create"},"type":"apply_start"}
`
	c, ctxs := collectLines(t, lines)
	if len(ctxs) != 1 {
		t.Fatalf("contexts = %d, want 1", len(ctxs))
	}
	if !ctxs[0].Start.Equal(ctxs[0].End) {
		t.Fatalf("fixture assumption changed: Start = %v, End = %v, want equal", ctxs[0].Start, ctxs[0].End)
	}
	if got := c.ZeroExtentContexts(); got != 1 {
		t.Errorf("ZeroExtentContexts = %d, want 1", got)
	}
}

// A context closed normally, well before end-of-log, must not be counted as
// zero-extent -- the sibling case to
// TestZeroExtentContextsCountsAResourceStillRunningAtEndOfLog. A trailing
// line after the close keeps lastTS later than the closed context's own
// End, so this cannot pass by accident the way it would if the closed
// context's End happened to equal the log's last timestamp too.
func TestZeroExtentContextsExcludesNormallyClosedContexts(t *testing.T) {
	const lines = `{"@level":"info","@message":"aws_instance.a: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:03.000000+10:00","hook":{"resource":{"addr":"aws_instance.a","module":"","resource":"aws_instance.a","implied_provider":"aws","resource_type":"aws_instance","resource_name":"a","resource_key":null},"action":"create"},"type":"apply_start"}
{"@level":"info","@message":"aws_instance.a: Creation complete after 2s","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:05.000000+10:00","hook":{"resource":{"addr":"aws_instance.a","module":"","resource":"aws_instance.a","implied_provider":"aws","resource_type":"aws_instance","resource_name":"a","resource_key":null},"action":"create"},"type":"apply_complete"}
{"@level":"info","@message":"aws_instance.b: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:06.000000+10:00","hook":{"resource":{"addr":"aws_instance.b","module":"","resource":"aws_instance.b","implied_provider":"aws","resource_type":"aws_instance","resource_name":"b","resource_key":null},"action":"create"},"type":"apply_start"}
{"@level":"info","@message":"aws_instance.b: Creation complete after 1s","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:07.000000+10:00","hook":{"resource":{"addr":"aws_instance.b","module":"","resource":"aws_instance.b","implied_provider":"aws","resource_type":"aws_instance","resource_name":"b","resource_key":null},"action":"create"},"type":"apply_complete"}
`
	c, ctxs := collectLines(t, lines)
	if len(ctxs) != 2 {
		t.Fatalf("contexts = %d, want 2", len(ctxs))
	}
	if got := c.ZeroExtentContexts(); got != 0 {
		t.Errorf("ZeroExtentContexts = %d, want 0 for two normally-closed contexts", got)
	}
}

func TestCompletedPairsCountsOnlyClosedContexts(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	if got := c.CompletedPairs(); got != 3 {
		t.Errorf("CompletedPairs = %d, want 3", got)
	}
}

func TestTypeCountsHistogram(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	want := map[string]uint64{
		"version":          1,
		"apply_start":      5,
		"apply_complete":   2,
		"apply_progress":   1,
		"apply_errored":    1,
		"refresh_start":    1,
		"refresh_complete": 1,
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

	// Second orphan: opened at 09:15:12, unclosed at end-of-log (09:15:16)
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
