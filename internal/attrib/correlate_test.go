package attrib

import (
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

var base = time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }

// ctx builds a managed-resource context of type typ over [startMs, endMs).
func ctx(addr, typ, action string, startMs, endMs int) Context {
	return Context{
		Address: addr, Name: addr, ResourceType: typ, Action: action,
		Start: at(startMs), End: at(endMs),
	}
}

// rpc builds an RPC span of type typ over [startMs, endMs).
func rpc(typ, rpcName string, startMs, endMs int) span.Span {
	return span.Span{
		StartMs: uint32(startMs), EndMs: uint32(endMs),
		DurationMs: uint32(endMs - startMs),
		RPC:        rpcName, ResourceType: typ,
		Fidelity: span.FidelityReported,
	}
}

func TestContainedWhenOneCandidateContainsTheSpan(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q", got[0].Address)
	}
	if got[0].Candidates != 1 {
		t.Errorf("Candidates = %d, want 1", got[0].Candidates)
	}
}

// Containment is inclusive of the boundary: a span ending exactly where its
// context ends is still wholly inside it under the half-open convention,
// since the context's own end instant lies outside its own window too.
func TestContainmentIncludesASpanEndingExactlyAtTheContextEnd(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 200)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained for a span ending exactly at the context's end", got[0].Confidence)
	}
}

// Containment is inclusive on the START boundary too: a span starting
// exactly when its context starts is still wholly inside it, the sibling of
// the end-boundary case above.
func TestContainmentIncludesASpanStartingExactlyAtTheContextStart(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 100, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained for a span starting exactly at the context's start", got[0].Confidence)
	}
}

// The top label must require containment, not merely uniqueness. A span
// sharing one millisecond with a lone window is weaker evidence than one
// sitting wholly inside a window, and an earlier draft had it the other way
// round -- rewarding the SCARCITY of context rather than its strength.
func TestOverlappingWhenTheLoneCandidateDoesNotContain(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 150, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Overlapping {
		t.Errorf("Confidence = %v, want Overlapping", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q, want the address to still be named", got[0].Address)
	}
}

func TestLikelyWhenExactlyOneOfSeveralContains(t *testing.T) {
	ctxs := []Context{
		ctx("aws_instance.a", "aws_instance", "read", 0, 1000),
		ctx("aws_instance.b", "aws_instance", "read", 150, 1000),
	}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Likely {
		t.Fatalf("Confidence = %v, want Likely", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q, want the containing candidate", got[0].Address)
	}
	if got[0].Candidates != 2 {
		t.Errorf("Candidates = %d, want 2 (overlapping candidates considered)", got[0].Candidates)
	}
}

// An Ambiguous span never asserts an address. It reports how many candidates
// there were and names none of them.
func TestAmbiguousNamesNoAddress(t *testing.T) {
	ctxs := []Context{
		ctx("aws_instance.a", "aws_instance", "read", 0, 1000),
		ctx("aws_instance.b", "aws_instance", "read", 0, 1000),
	}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Ambiguous {
		t.Fatalf("Confidence = %v, want Ambiguous", got[0].Confidence)
	}
	if got[0].Address != "" {
		t.Errorf("Address = %q, want empty -- Ambiguous must assert nothing", got[0].Address)
	}
	if got[0].Candidates != 2 {
		t.Errorf("Candidates = %d, want 2", got[0].Candidates)
	}
}

func TestUnattributedWhenNothingMatches(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 5000, 6000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed", got[0].Confidence)
	}
	if got[0].Candidates != 0 {
		t.Errorf("Candidates = %d, want 0", got[0].Candidates)
	}
}

func TestResourceTypeMustMatch(t *testing.T) {
	ctxs := []Context{ctx("aws_subnet.a", "aws_subnet", "read", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed across differing types", got[0].Confidence)
	}
}

// A zero-extent interval occupies no instant under [start, end) and so
// overlaps nothing. Both directions are asserted: this is the boundary this
// project has already had to pin once, for PeakConcurrency.
func TestZeroExtentSpanOverlapsNothing(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 100)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed for a zero-extent span", got[0].Confidence)
	}
}

func TestZeroExtentContextIsNeverACandidate(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 150, 150)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed for a zero-extent context", got[0].Confidence)
	}
}

// Two half-open intervals that are exactly adjacent -- one ending precisely
// where the other starts -- share no instant. Both directions are asserted:
// a span ending where a context starts, and a context ending where a span
// starts.
func TestAdjacentIntervalsDoNotOverlap(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 200, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed -- span ends exactly where context starts", got[0].Confidence)
	}
	if got[0].Candidates != 0 {
		t.Errorf("Candidates = %d, want 0", got[0].Candidates)
	}

	ctxs = []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 100)}
	got = Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed -- context ends exactly where span starts", got[0].Confidence)
	}
}

// A clamped span's start is fabricated. Correlating on its [0, End) window
// would overlap nearly every context in the log and could appear to be
// CONTAINED by several -- on precisely the longest calls in a capture.
func TestClampedSpanCorrelatesOnItsEndInstantAndIsCappedAtOverlapping(t *testing.T) {
	s := rpc("aws_instance", "ReadResource", 0, 200)
	s.DurationMs = 900 // exceeds the offset, which is what clamping means
	s.StartClamped = true

	// A context covering the whole run would CONTAIN [0, 200) but does not
	// contain the end instant more tightly than any other; the cap applies
	// regardless.
	wide := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 1000)}
	got := Correlate([]span.Span{s}, base, wide)
	if got[0].Confidence != Overlapping {
		t.Errorf("Confidence = %v, want Overlapping (capped)", got[0].Confidence)
	}

	// A context that ended before the span's end instant must not match,
	// even though it overlaps the fabricated [0, 200) window.
	early := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 150)}
	got = Correlate([]span.Span{s}, base, early)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed -- a clamped span must not "+
			"match a context its fabricated start merely reaches", got[0].Confidence)
	}
}

// ReadDataSource draws only from data-source contexts and everything else
// only from managed-resource contexts. ReportedBuilder folds
// tf_data_source_type into ResourceType, so type alone cannot separate them.
func TestDataPrefixSeparatesCandidatePools(t *testing.T) {
	dataCtx := ctx("data.local_file.a", "local_file", "read", 0, 1000)
	dataCtx.IsData = true
	managedCtx := ctx("local_file.b", "local_file", "create", 0, 1000)
	ctxs := []Context{dataCtx, managedCtx}

	got := Correlate([]span.Span{rpc("local_file", "ReadDataSource", 100, 200)}, base, ctxs)
	if got[0].Address != "data.local_file.a" {
		t.Errorf("ReadDataSource attributed to %q, want the data-source context", got[0].Address)
	}

	got = Correlate([]span.Span{rpc("local_file", "ApplyResourceChange", 100, 200)}, base, ctxs)
	if got[0].Address != "local_file.b" {
		t.Errorf("ApplyResourceChange attributed to %q, want the managed context", got[0].Address)
	}
}

func TestOperationMustMatchWhereTheRPCIsMapped(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "create", 0, 1000)}
	// ReadResource maps to action "read" only, so a create context is not a
	// candidate for it.
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed across differing operations", got[0].Confidence)
	}
}

// An RPC name absent from the map matches any action. Attributing nothing
// because a name is unmapped would silently drop time, and the map cannot be
// complete for provider RPCs nobody has catalogued.
func TestUnmappedRPCMatchesAnyAction(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "create", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "UpgradeResourceState", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained -- an unmapped RPC must not "+
			"be silently excluded", got[0].Confidence)
	}
}

func TestModuleNameAndKeyTravelWithTheAttribution(t *testing.T) {
	c := ctx(`module.m["k"].aws_instance.web[0]`, "aws_instance", "read", 0, 1000)
	c.Module, c.Name, c.Key = `module.m["k"]`, "web", "0"
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, []Context{c})
	if got[0].Module != `module.m["k"]` || got[0].Name != "web" || got[0].Key != "0" {
		t.Errorf("got Module=%q Name=%q Key=%q", got[0].Module, got[0].Name, got[0].Key)
	}
}

func TestCorrelateReturnsOneAttributionPerSpan(t *testing.T) {
	spans := []span.Span{
		rpc("aws_instance", "ReadResource", 100, 200),
		rpc("aws_subnet", "ReadResource", 100, 200),
	}
	got := Correlate(spans, base, nil)
	if len(got) != len(spans) {
		t.Fatalf("len = %d, want %d -- the table must stay parallel to the span slice",
			len(got), len(spans))
	}
}

func TestConfidenceStrings(t *testing.T) {
	for c, want := range map[Confidence]string{
		Unattributed: "unattributed",
		Ambiguous:    "ambiguous",
		Overlapping:  "overlapping",
		Likely:       "likely",
		Contained:    "contained",
	} {
		if got := c.String(); got != want {
			t.Errorf("Confidence(%d).String() = %q, want %q", c, got, want)
		}
	}
}
