package attrib

import (
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
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
		Fidelity: span.FidelityReported, TimestampStatus: logfmt.TimestampValid,
	}
}

func TestUnavailablePositionCannotAcquireAnAddress(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := span.Span{DurationMs: 10, ResourceType: "x", RPC: "ReadResource",
		TimestampStatus: logfmt.TimestampInvalid}
	c := Context{Address: "x.a", Name: "x.a", ResourceType: "x", Action: "read", Start: base.Add(-time.Second), End: base.Add(time.Second)}
	got := Correlate([]span.Span{s}, base, []Context{c})[0]
	if got.Confidence != Unattributed || got.Address != "" || got.Candidates != 0 {
		t.Fatalf("unpositioned duration acquired attribution: %+v", got)
	}
	if !got.PositionUnavailable {
		t.Fatal("PositionUnavailable = false, want true")
	}
}

func TestUnknownTimestampCannotAcquireAnAddress(t *testing.T) {
	s := span.Span{DurationMs: 10, ResourceType: "x", RPC: "ReadResource"}
	c := Context{Address: "x.a", Name: "x.a", ResourceType: "x", Action: "read", Start: base.Add(-time.Second), End: base.Add(time.Second)}
	got := Correlate([]span.Span{s}, base, []Context{c})[0]
	if got.Confidence != Unattributed || got.Address != "" || got.Candidates != 0 || !got.PositionUnavailable {
		t.Fatalf("unknown timestamp acquired attribution: %+v", got)
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

// C3: a genuinely instantaneous span (StartMs == EndMs, a real
// tf_req_duration_ms == 0) is a real observation of an instant, not the
// truncation artefact a zero-extent CONTEXT is -- so it must be tested by
// point membership rather than excluded outright. A point plainly inside one
// context's window is Contained: there is no weaker "overlaps but does not
// contain" outcome for a single instant the way there is for an interval.
func TestZeroDurationSpanInsideOneContextIsAttributed(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 100)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained for a zero-duration span inside one context", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q, want aws_instance.a", got[0].Address)
	}
}

// pointOverlaps's start boundary must be inclusive, the point-membership
// analogue of TestContainmentIncludesASpanStartingExactlyAtTheContextStart.
// Every other degenerate-span case above places its instant well inside the
// context, never at c.Start itself, so this is the one test that would catch
// pointOverlaps's start clause being mutated to exclusive (t.After(c.Start)).
func TestZeroDurationSpanAtExactlyContextStartIsContained(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 100, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 100)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained for a zero-duration span exactly at the context's start", got[0].Confidence)
	}
}

// The half-open sibling: a zero-duration span at exactly the context's END is
// Unattributed -- the context's own end instant lies outside its window, the
// same rule TestContainmentIncludesASpanEndingExactlyAtTheContextEnd pins for
// an interval span.
func TestZeroDurationSpanAtExactlyContextEndIsUnattributed(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 100)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 100)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed for a zero-duration span exactly at the context's end", got[0].Confidence)
	}
}

// The sibling case: a zero-duration span whose instant falls inside TWO
// candidate contexts is Ambiguous, the same as an interval span overlapping
// several without uniquely containing the span.
func TestZeroDurationSpanInsideTwoContextsIsAmbiguous(t *testing.T) {
	ctxs := []Context{
		ctx("aws_instance.a", "aws_instance", "read", 0, 1000),
		ctx("aws_instance.b", "aws_instance", "read", 0, 1000),
	}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 100)}, base, ctxs)
	if got[0].Confidence != Ambiguous {
		t.Errorf("Confidence = %v, want Ambiguous for a zero-duration span inside two contexts", got[0].Confidence)
	}
	if got[0].Address != "" {
		t.Errorf("Address = %q, want empty -- Ambiguous must assert nothing", got[0].Address)
	}
}

func TestZeroExtentContextIsNeverACandidate(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 150, 150)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed for a zero-extent context", got[0].Confidence)
	}
}

// A zero-extent CONTEXT is still never a candidate for a zero-duration SPAN
// even when the two instants coincide -- the truncation-artefact exclusion
// and the real-instant admission are two different rules and neither must
// bleed into the other. pointOverlaps excludes this case with no separate
// guard: see its own doc comment for why.
func TestZeroExtentContextIsNeverACandidateForAZeroDurationSpanEither(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 100, 100)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 100)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed -- a zero-extent context must not match even a coincident zero-duration span", got[0].Confidence)
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

// C1: a refresh context carries no action at all (refresh_start/
// refresh_complete have no action field -- see opensContext), which must
// not exclude it as a candidate for a mapped RPC the way a real action
// mismatch would. This is the mirror of TestUnmappedRPCMatchesAnyAction
// below: that one declines to constrain on an unmapped RPC, this one
// declines to constrain on an absent context action.
func TestRefreshContextWithNoActionMatchesAnyRPC(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained -- an empty context action must not exclude a candidate", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q, want aws_instance.a", got[0].Address)
	}
}

// TestRefreshContextWithNoActionMatchesAnyRPC above exercises only
// ReadResource, whose allowed set is {"read"} -- mutating actionMatches'
// empty-action branch to `return rpc == "ReadResource"` (precisely the false
// "refresh means read" mapping the spec was corrected against) leaves that
// test green. This pins the same rule for an RPC mapped to a DIFFERENT
// allowed set, so a mapping that special-cased ReadResource cannot pass here.
func TestRefreshContextWithNoActionMatchesAnRPCMappedToOtherActions(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ApplyResourceChange", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained -- an empty context action must not exclude ApplyResourceChange either", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q, want aws_instance.a", got[0].Address)
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
	c.ModuleKnown, c.ModuleInvalid = true, false
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, []Context{c})
	if got[0].Module != `module.m["k"]` || got[0].Name != "web" || got[0].Key != "0" {
		t.Errorf("got Module=%q Name=%q Key=%q", got[0].Module, got[0].Name, got[0].Key)
	}
	if !got[0].ModuleKnown || got[0].ModuleInvalid {
		t.Errorf("module evidence = known %v invalid %v, want true false", got[0].ModuleKnown, got[0].ModuleInvalid)
	}
}

func TestInvalidModuleEvidenceTravelsWithNamedAttribution(t *testing.T) {
	c := ctx("module.m.aws_instance.web", "aws_instance", "read", 0, 1000)
	c.ModuleInvalid = true
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, []Context{c})
	if got[0].Confidence != Contained || !got[0].ModuleInvalid || got[0].ModuleKnown {
		t.Errorf("attribution = %+v, want unchanged confidence with invalid module evidence", got[0])
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

// I2: replacing actionMatches' loop body with `return allowed[0] == ctxAction`
// passed the whole suite before this test existed, because every existing
// case exercised only "create", which is allowed[0] for both
// PlanResourceChange and ApplyResourceChange. This asserts a match for
// EVERY action each RPC's list allows, and a non-match for one that is not
// in any list.
func TestActionMatchesEveryAllowedActionForEveryMappedRPC(t *testing.T) {
	for rpc, allowed := range rpcActions {
		for _, action := range allowed {
			if !actionMatches(rpc, action) {
				t.Errorf("actionMatches(%q, %q) = false, want true", rpc, action)
			}
		}
	}
	// "destroy" names no action any RPC in rpcActions allows.
	for rpc := range rpcActions {
		if actionMatches(rpc, "destroy") {
			t.Errorf("actionMatches(%q, \"destroy\") = true, want false", rpc)
		}
	}
}

// I5: IsData travels from Context through to Attribution the same way
// Module, Name and Key already do -- named() must not drop it.
func TestIsDataTravelsWithTheAttribution(t *testing.T) {
	c := ctx("data.local_file.a", "local_file", "read", 0, 1000)
	c.IsData = true
	got := Correlate([]span.Span{rpc("local_file", "ReadDataSource", 100, 200)}, base, []Context{c})
	if !got[0].IsData {
		t.Error("IsData = false, want true")
	}

	managed := ctx("local_file.b", "local_file", "create", 0, 1000)
	got = Correlate([]span.Span{rpc("local_file", "ApplyResourceChange", 100, 200)}, base, []Context{managed})
	if got[0].IsData {
		t.Error("IsData = true, want false for a managed-resource context")
	}
}

// M3: the clamped-span probe is [end-1ms, end) -- the LAST instant the span
// occupies -- not a window built from EndMs itself, since a half-open
// context can never contain its own EndMs. Pinned here so a plausible
// off-by-one (probing from EndMs rather than up to it) would be caught: a
// context ending exactly at EndMs must still match, and one starting
// exactly at EndMs must not.
func TestClampedSpanProbesTheLastInstantItOccupies(t *testing.T) {
	s := rpc("aws_instance", "ReadResource", 0, 200)
	s.DurationMs = 900
	s.StartClamped = true

	endsAtEndMs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 200)}
	got := Correlate([]span.Span{s}, base, endsAtEndMs)
	if got[0].Confidence != Overlapping {
		t.Errorf("Confidence = %v, want Overlapping -- a context ending exactly at EndMs must still match the probe", got[0].Confidence)
	}

	startsAtEndMs := []Context{ctx("aws_instance.a", "aws_instance", "read", 200, 1000)}
	got = Correlate([]span.Span{s}, base, startsAtEndMs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed -- a context starting exactly at EndMs must not match the probe", got[0].Confidence)
	}
}

// M3: the capped && n > 1 branch had no test of its own.
func TestClampedSpanWithMultipleCandidatesIsAmbiguous(t *testing.T) {
	s := rpc("aws_instance", "ReadResource", 0, 200)
	s.DurationMs = 900
	s.StartClamped = true

	ctxs := []Context{
		ctx("aws_instance.a", "aws_instance", "read", 0, 1000),
		ctx("aws_instance.b", "aws_instance", "read", 0, 1000),
	}
	got := Correlate([]span.Span{s}, base, ctxs)
	if got[0].Confidence != Ambiguous {
		t.Errorf("Confidence = %v, want Ambiguous -- a capped span with more than one matching candidate must not assert an address", got[0].Confidence)
	}
	if got[0].Candidates != 2 {
		t.Errorf("Candidates = %d, want 2", got[0].Candidates)
	}
	if got[0].Address != "" {
		t.Errorf("Address = %q, want empty -- Ambiguous must assert nothing even when capped", got[0].Address)
	}
}

// named is the only path that can put an address on an Attribution, and
// correlateOne never reaches it with Ambiguous or Unattributed. This pins
// that invariant at named itself, so a future edit to correlateOne's switch
// that slips one of those two confidences past it fails loudly here rather
// than shipping a labelled-but-unsupported address to the UI.
func TestNamedPanicsOnAmbiguousOrUnattributed(t *testing.T) {
	c := ctx("aws_instance.a", "aws_instance", "create", 0, 100)
	for _, conf := range []Confidence{Ambiguous, Unattributed} {
		t.Run(conf.String(), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("named(%s) returned quietly instead of panicking", conf)
				}
			}()
			named(c, 1, conf)
		})
	}
}

// Every Correlate test above builds contexts with the synthetic ctx()
// helper, and collect(t, "testdata/context.log") -- used throughout
// context_test.go -- is never fed into Correlate. So "a refresh context
// opens" (TestRefreshHookOpensAndClosesAContext) and "an empty action
// matches" (TestRefreshContextWithNoActionMatchesAnyRPC) are each proven, but
// never joined, even though refresh windows are the largest source of RPC
// volume in a real plan. base and the span's offsets are derived from the
// fixture's own timestamps rather than hard-coded, so this cannot silently
// pass on a coincidence between an assumed base and the fixture's real one.
func TestCorrelateAttributesARealParsedRefreshWindow(t *testing.T) {
	cc, ctxs := collect(t, "testdata/context.log")
	realBase := cc.FirstTS()

	var refresh Context
	for _, c := range ctxs {
		if c.Address == "aws_instance.tracked" {
			refresh = c
			break
		}
	}
	if refresh.Address == "" {
		t.Fatal("fixture produced no aws_instance.tracked context")
	}

	startMs := refresh.Start.Sub(realBase).Milliseconds()
	endMs := refresh.End.Sub(realBase).Milliseconds()
	if endMs-startMs < 2 {
		t.Fatalf("refresh window [%d, %d) is too narrow to place a span strictly inside it", startMs, endMs)
	}

	s := span.Span{
		StartMs: uint32(startMs + 1), EndMs: uint32(endMs - 1),
		DurationMs: uint32(endMs - startMs - 2),
		RPC:        "ReadResource", ResourceType: "aws_instance",
		Fidelity: span.FidelityReported, TimestampStatus: logfmt.TimestampValid,
	}
	got := Correlate([]span.Span{s}, realBase, ctxs)
	if got[0].Confidence != Contained {
		t.Fatalf("Confidence = %v, want Contained", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.tracked" {
		t.Errorf("Address = %q, want aws_instance.tracked", got[0].Address)
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
