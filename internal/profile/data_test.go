package profile

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestBuildRetainsAllObservedOperations(t *testing.T) {
	l, err := model.Load("../../testdata/resources-modules.log")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.UI) != 2 || len(got.UIRanking) != 2 || len(got.RPC) != 0 {
		t.Fatalf("lost original observations: %+v", got)
	}
	if got.Timeline.Tier == nil || *got.Timeline.Tier != span.FidelityUIReported {
		t.Fatal("UI-only capture must select its UI tier")
	}
	for i, row := range got.UI {
		if row.Index != i || row.Source == nil || row.Source.StartLine == 0 {
			t.Fatalf("operation source identity missing: %+v", row)
		}
	}
	if got.Resources.UI.Count != 2 || len(got.Resources.Rows) != 1 || got.Resources.Rows[0].UI.TotalMs != 3000 {
		t.Fatalf("incomplete resource evidence: %+v", got.Resources)
	}
}

func TestBuildRanksCompleteTiersByRawIdentityAndOriginalIndex(t *testing.T) {
	const long = "provider.\x00" + "long-identifier-" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rpc := make([]span.Span, 25)
	attributions := make([]attrib.Attribution, len(rpc))
	entries := make([]logfmt.Entry, len(rpc))
	data := make([]byte, len(rpc))
	for i := range rpc {
		rpc[i] = span.Span{Entry: uint32(i), DurationMs: uint32(i + 1), Provider: "z", ResourceType: "z", RPC: "z"}
		entries[i] = logfmt.Entry{Off: uint64(i), Len: 1}
		data[i] = 'x'
	}
	rpc[2] = span.Span{Entry: 2, DurationMs: 100, Provider: long, ResourceType: "type-b", RPC: "read"}
	rpc[3] = span.Span{Entry: 3, DurationMs: 100, Provider: long, ResourceType: "type-a", RPC: "read"}
	rpc[4] = span.Span{Entry: 3, DurationMs: 100, Provider: long, ResourceType: "type-a", RPC: "read"}
	attributions[2] = attrib.Attribution{Address: "address-a", Confidence: attrib.Contained}
	attributions[3] = attrib.Attribution{Address: "address-z", Confidence: attrib.Likely}
	attributions[4] = attrib.Attribution{Address: "address-z", Confidence: attrib.Likely}
	ui := []span.Span{
		{Entry: 8, DurationMs: 100, Address: "b", RPC: "read", ResourceType: "type-a"},
		{Entry: 7, DurationMs: 100, Address: "a", RPC: "write", ResourceType: "type-a"},
		{Entry: 6, DurationMs: 100, Address: "a", RPC: "read", ResourceType: "type-b"},
		{Entry: 5, DurationMs: 100, Address: "a", RPC: "read", ResourceType: "type-a"},
	}
	l := &model.Log{Data: data, Entries: entries, RPCSpans: rpc, UISpans: ui, Attribs: attributions, Contexts: []attrib.Context{{}}}

	got, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RPC) != 25 || len(got.RPCRanking) != 25 {
		t.Fatalf("complete RPC tier lengths = %d, %d", len(got.RPC), len(got.RPCRanking))
	}
	if want := []int{3, 4, 2}; !reflect.DeepEqual(got.RPCRanking[:3], want) {
		t.Fatalf("tie ranking = %v, want %v", got.RPCRanking[:3], want)
	}
	if got.RPC[3].Attribution != attributions[3] || got.RPC[4].Source == got.RPC[3].Source || got.RPC[4].Source.Entry != 3 {
		t.Fatalf("parallel attribution or duplicate source identity lost: rpc[3]=%+v rpc[4]=%+v", got.RPC[3], got.RPC[4])
	}
	if got.RPC[2].Span.Provider != long {
		t.Fatal("raw control-bearing identifier was changed")
	}
	if want := []int{3, 2, 1, 0}; !reflect.DeepEqual(got.UIRanking, want) {
		t.Fatalf("UI tie ranking = %v, want %v", got.UIRanking, want)
	}
}

func TestBuildKeepsMissingSourcesAndAllAttributionConfidenceStates(t *testing.T) {
	l, err := model.Load("../tui/testdata/resource-association-confidence.log")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	want := map[attrib.Confidence]bool{attrib.Unattributed: false, attrib.Ambiguous: false, attrib.Overlapping: false, attrib.Likely: false, attrib.Contained: false}
	for _, observation := range got.RPC {
		want[observation.Attribution.Confidence] = true
		if (observation.Attribution.Confidence == attrib.Ambiguous || observation.Attribution.Confidence == attrib.Unattributed) && observation.Attribution.Address != "" {
			t.Fatalf("unsupported address fabricated: %+v", observation.Attribution)
		}
	}
	for confidence, found := range want {
		if !found {
			t.Errorf("missing confidence %s", confidence)
		}
	}

	missing := &model.Log{Data: []byte("x"), RPCSpans: []span.Span{{Entry: 7, DurationMs: 1}}}
	missingReport, err := Build(missing)
	if err != nil {
		t.Fatal(err)
	}
	if missingReport.RPC[0].Source != nil || missingReport.RPC[0].Index != 0 {
		t.Fatalf("missing source should remain explicit: %+v", missingReport.RPC[0])
	}
}

func TestBuildReportsLowerBoundsAndDoesNotMutateInput(t *testing.T) {
	rpc := []span.Span{{Entry: 0, ResourceType: "rpc_type", DurationMs: 7}}
	ui := []span.Span{{Entry: 1, Address: "resource.one", ResourceType: "ui_type", DurationMs: math.MaxUint32, DurationSaturated: true}}
	l := &model.Log{Data: []byte("xy"), Entries: []logfmt.Entry{{Off: 0, Len: 1}, {Off: 1, Len: 1}}, RPCSpans: rpc, UISpans: ui}
	rpcBefore := append([]span.Span(nil), l.RPCSpans...)
	uiBefore := append([]span.Span(nil), l.UISpans...)
	reconstructionBefore := l.ReconstructionQuality()

	got, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Types[0].UILowerBound || got.Resources.UI.TotalMs != math.MaxUint32 || !got.Resources.UI.LowerBound || !got.Resources.Rows[0].UI.LowerBound {
		t.Fatalf("lower-bound evidence lost: types=%+v resources=%+v", got.Types, got.Resources)
	}
	if !reflect.DeepEqual(l.RPCSpans, rpcBefore) || !reflect.DeepEqual(l.UISpans, uiBefore) {
		t.Fatal("Build reordered or changed input spans")
	}
	if after := l.ReconstructionQuality(); after != reconstructionBefore || after.State != "not_checked" {
		t.Fatalf("Build triggered reconstruction: before=%+v after=%+v", reconstructionBefore, after)
	}
}

func TestBuildSnapshotsReconstructionWithoutTriggeringIt(t *testing.T) {
	l, err := model.Load("../../testdata/resources-accounting.log")
	if err != nil {
		t.Fatal(err)
	}

	got, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reconstruction != (model.ReconstructionQuality{State: "not_checked"}) {
		t.Fatalf("reconstruction snapshot = %+v", got.Reconstruction)
	}
	if after := l.ReconstructionQuality(); after.State != "not_checked" {
		t.Fatalf("Build triggered reconstruction: %+v", after)
	}
}

func TestBuildMapsTimelinePositionsToOriginalTierIndicesWithoutFallback(t *testing.T) {
	valid := logfmt.TimestampValid
	l := &model.Log{
		RPCSpans: []span.Span{
			{Entry: 10, DurationMs: 9000, TimestampStatus: logfmt.TimestampMissing, Fidelity: span.FidelityReported},
			{Entry: 11, StartMs: 0, EndMs: 20000, DurationMs: 20000, TimestampStatus: valid, Fidelity: span.FidelityReported},
			{Entry: 12, StartMs: 3000, EndMs: 4000, DurationMs: 1000, TimestampStatus: valid, Fidelity: span.FidelityReported},
		},
		UISpans: []span.Span{{Entry: 13, StartMs: 0, EndMs: 2000, DurationMs: 2000, TimestampStatus: valid, Fidelity: span.FidelityUIReported}},
	}
	got, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}
	if got.Timeline.Tier == nil || *got.Timeline.Tier != span.FidelityReported || !reflect.DeepEqual(got.Timeline.PositionedIndices, []int{1, 2}) {
		t.Fatalf("timeline tier/mapping = %+v", got.Timeline)
	}
	if len(got.Timeline.Analysis.Intervals) == 0 {
		t.Fatal("expected complete temporal intervals")
	}
	for _, interval := range got.Timeline.Analysis.Intervals {
		if interval.Blocking >= 0 && got.Timeline.PositionedIndices[interval.Blocking] != 1 {
			t.Fatalf("blocking position %d maps to original %d, want 1", interval.Blocking, got.Timeline.PositionedIndices[interval.Blocking])
		}
	}
	noFallback := &model.Log{
		RPCSpans: []span.Span{{Entry: 20, DurationMs: 9000, TimestampStatus: logfmt.TimestampMissing, Fidelity: span.FidelityReported}},
		UISpans:  l.UISpans,
	}
	noFallbackReport, err := Build(noFallback)
	if err != nil {
		t.Fatal(err)
	}
	if noFallbackReport.Timeline.Tier == nil || *noFallbackReport.Timeline.Tier != span.FidelityReported || noFallbackReport.Timeline.Analysis.Metrics != nil || len(noFallbackReport.Timeline.PositionedIndices) != 0 {
		t.Fatalf("unpositioned preferred tier fell back to UI: %+v", noFallbackReport.Timeline)
	}

	rejectedOnly := &model.Log{RPCEvidence: span.TimingEvidence{Records: 1, Rejected: map[string]span.IssueCount{"duration_invalid": {Count: 1}}}, UISpans: l.UISpans}
	rejectedReport, err := Build(rejectedOnly)
	if err != nil {
		t.Fatal(err)
	}
	if rejectedReport.Timeline.Tier == nil || *rejectedReport.Timeline.Tier != span.FidelityUIReported {
		t.Fatalf("admitted tier selection = %+v", rejectedReport.Timeline.Tier)
	}
	emptyReport, err := Build(&model.Log{RPCEvidence: rejectedOnly.RPCEvidence})
	if err != nil {
		t.Fatal(err)
	}
	if emptyReport.Timeline.Tier != nil || emptyReport.Timeline.Analysis.Metrics != nil {
		t.Fatalf("rejected-only log admitted a timeline: %+v", emptyReport.Timeline)
	}
}

func TestBuildRejectsNilLogAndPreservesTemporalErrors(t *testing.T) {
	if _, err := Build(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("Build(nil) error = %v", err)
	}
	l := &model.Log{RPCSpans: []span.Span{
		{StartMs: 0, EndMs: 10, DurationMs: 10, TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityReported},
		{StartMs: 10, EndMs: 20, DurationMs: 10, TimestampStatus: logfmt.TimestampValid, Fidelity: span.FidelityUIReported},
	}}
	if _, err := Build(l); err == nil {
		t.Fatal("mixed timeline error was silently replaced by a zero summary")
	}
}
