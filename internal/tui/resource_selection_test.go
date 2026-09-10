package tui

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourceSelectionKeepsUIOnlyTimelineUnderRPCFilters(t *testing.T) {
	l := &model.Log{UISpans: []span.Span{{
		Address:         "aws_instance.a",
		ResourceType:    "aws_instance",
		DurationMs:      1000,
		EndMs:           1000,
		TimestampStatus: logfmt.TimestampValid,
		Fidelity:        span.FidelityUIReported,
	}}}
	m := New(l, "synthetic.log")
	m.setFacetExclusions(dimProvider, map[string]bool{"p": true})
	m.setFacetExclusions(dimRPC, map[string]bool{"ReadResource": true})
	m.invalidateRows()

	tier, spans := m.timelineSpans()
	if tier != tierUI || len(spans) != 1 || spans[0].Address != "aws_instance.a" {
		t.Fatalf("timeline = tier %v, spans %+v; want the UI observation", tier, spans)
	}
}

func TestResourceSelectionRetainsOriginalRPCIndex(t *testing.T) {
	l := testLog(t, "two-tier.log")
	m := New(l, "two-tier.log")
	showOnly(t, &m, dimType, "aws_subnet")

	projection := m.selectedResources()
	if len(projection.RPCIndices) != 1 || projection.RPCIndices[0] != 2 {
		t.Fatalf("selected RPC indices = %v, want original index 2", projection.RPCIndices)
	}
	rows := m.rows()
	if len(rows) != 1 || rows[0].spanIdx != 2 {
		t.Fatalf("call rows = %+v, want one row carrying original index 2", rows)
	}
	spans, idx, ok := m.jumpTarget()
	if !ok || idx != 2 {
		t.Fatalf("jump target = index %d, ok %v; want original index 2", idx, ok)
	}
	want := l.RPCSpans[2]
	if spans[idx].Entry != want.Entry || spans[idx].ReqID != want.ReqID {
		t.Errorf("jump target = entry %d request %d, want entry %d request %d", spans[idx].Entry, spans[idx].ReqID, want.Entry, want.ReqID)
	}
	if got := l.AttributionForEntry(spans[idx].Entry); got.Confidence != attrib.Unattributed {
		t.Errorf("jump target attribution = %+v, want original index 2's Unattributed verdict", got)
	}
}

func TestResourceSelectionRanksUnpositionedUIOutsideTimeline(t *testing.T) {
	l := &model.Log{UISpans: []span.Span{{
		Address:      "aws_instance.a",
		ResourceType: "aws_instance",
		DurationMs:   1000,
		Fidelity:     span.FidelityUIReported,
	}}}
	m := New(l, "synthetic.log")

	projection := m.selectedResources()
	if len(projection.Rows) != 1 || projection.Rows[0].UI.Count != 1 || projection.Rows[0].UI.TotalMs != 1000 {
		t.Fatalf("resource projection = %+v, want the unpositioned UI observation ranked", projection)
	}
	if tier, spans := m.timelineSpans(); tier != tierUI || len(spans) != 0 {
		t.Fatalf("timeline = tier %v with %+v, want UI tier with the unpositioned observation excluded", tier, spans)
	}
	_, timing := m.timelineTiming()
	if timing.AdmittedCount != 1 || timing.ExcludedCount != 1 || timing.ExcludedMs != 1000 {
		t.Errorf("timeline timing = %+v, want one admitted but unpositioned 1000ms observation", timing)
	}
}

func TestResourceSelectionDoesNotChangeRawVisibility(t *testing.T) {
	l := testLog(t, "two-tier.log")
	m := New(l, "two-tier.log")
	before := m.rawLogVisible()
	m.resourceSelection = model.ResourceSelection{
		Addresses: map[string]bool{"local_file.config": true},
		Modules:   map[string]bool{"": true},
	}
	m.setFacetExclusions(dimType, map[string]bool{"aws_instance": true})
	m.setFacetExclusions(dimRPC, map[string]bool{"ApplyResourceChange": true})
	m.invalidateRows()
	after := m.rawLogVisible()

	for i := range l.Entries {
		if before(i) != after(i) {
			t.Errorf("raw entry %d visibility changed from %v to %v under type/method/resource/module selection", i, before(i), after(i))
		}
	}
}

func TestResourceSelectionClearsWithOtherFilters(t *testing.T) {
	l := testLog(t, "two-tier.log")
	m := New(l, "two-tier.log")
	m.resourceSelection = model.ResourceSelection{Addresses: map[string]bool{"local_file.config": true}}
	m.invalidateRows()
	if got := len(m.selectedUISpans()); got != 1 {
		t.Fatalf("selected UI spans = %d before clearing, want 1", got)
	}

	m.clearFilters()

	if m.resourceSelection.Addresses != nil || m.resourceSelection.Modules != nil {
		t.Fatalf("resource selection remained after clearing: %+v", m.resourceSelection)
	}
	if got := len(m.selectedUISpans()); got != len(l.UISpans) {
		t.Errorf("selected UI spans = %d after clearing, want all %d", got, len(l.UISpans))
	}
}

func TestModuleFacetSelectionUsesStructuralBoundaries(t *testing.T) {
	l := &model.Log{UISpans: []span.Span{
		{Address: `module.app["a.b"].module.db[0].aws_instance.one`},
		{Address: `module.app["other"].module.db[0].aws_instance.two`},
		{Address: `module.application.aws_instance.three`},
		{Address: "opaque", ModuleInvalid: true},
	}}
	m := New(l, "synthetic.log")
	setFacetCursor(t, &m, dimModule, `module.app["a.b"]`)
	m.soloFacetValue()
	projection := m.selectedResources()
	if len(projection.UIIndices) != 1 || projection.UIIndices[0] != 0 {
		t.Fatalf("indexed module selection admitted UI indices %v, want only original index 0", projection.UIIndices)
	}

	m.soloFacetValue()
	setFacetCursor(t, &m, dimModule, "")
	m.soloFacetValue()
	projection = m.selectedResources()
	if len(projection.UIIndices) != 3 {
		t.Fatalf("root subtree admitted UI indices %v, want three known module members and no unknown opaque address", projection.UIIndices)
	}
}
