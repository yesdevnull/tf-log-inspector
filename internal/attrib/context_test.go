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
	var c ContextCollector
	if _, err := logfmt.Scan(strings.NewReader(string(data)), &logfmt.Interner{}, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return &c, c.Contexts()
}

func TestCollectorPairsStartWithComplete(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	if len(ctxs) != 3 {
		t.Fatalf("contexts = %d, want 3", len(ctxs))
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

func TestUnclosedContextEndsAtLastTimestamp(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	got := ctxs[2]
	if got.Address != "aws_instance.orphan" {
		t.Fatalf("Address = %q", got.Address)
	}
	if !got.Unclosed {
		t.Error("Unclosed = false, want true")
	}
	if !got.End.Equal(got.Start) {
		// The orphan's start IS the last timestamp in the fixture, so its
		// window is zero-extent. That is the honest answer: nothing in the
		// log says it ran for any measurable time.
		t.Errorf("End = %v, want equal to Start %v", got.End, got.Start)
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
		"apply_start":    3,
		"apply_complete": 1,
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
