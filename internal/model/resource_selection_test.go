package model

import (
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourceSelectionMembership(t *testing.T) {
	root := ResourceModule{Known: true}
	child := ResourceModule{Path: `module.app["a.b"].module.db[0]`, Known: true}
	for _, tc := range []struct {
		name      string
		selection ResourceSelection
		address   string
		module    ResourceModule
		want      Membership
	}{
		{"inactive unknown", ResourceSelection{}, "", ResourceModule{}, MembershipSelected},
		{"empty addresses", ResourceSelection{Addresses: map[string]bool{}}, "", ResourceModule{}, MembershipOther},
		{"false addresses", ResourceSelection{Addresses: map[string]bool{"a": false}}, "a", root, MembershipOther},
		{"false addresses missing identity", ResourceSelection{Addresses: map[string]bool{"a": false}}, "", root, MembershipOther},
		{"address alternatives", ResourceSelection{Addresses: map[string]bool{"a": true, "b": true}}, "b", ResourceModule{}, MembershipSelected},
		{"missing address", ResourceSelection{Addresses: map[string]bool{"a": true}}, "", root, MembershipUnknown},
		{"case sensitive address", ResourceSelection{Addresses: map[string]bool{"A": true}}, "a", root, MembershipOther},
		{"empty modules", ResourceSelection{Modules: map[string]bool{}}, "a", ResourceModule{}, MembershipOther},
		{"false modules", ResourceSelection{Modules: map[string]bool{"": false}}, "a", root, MembershipOther},
		{"false modules unknown identity", ResourceSelection{Modules: map[string]bool{"": false}}, "a", ResourceModule{}, MembershipOther},
		{"root includes root", ResourceSelection{Modules: map[string]bool{"": true}}, "a", root, MembershipSelected},
		{"root includes descendants", ResourceSelection{Modules: map[string]bool{"": true}}, "a", child, MembershipSelected},
		{"indexed ancestor", ResourceSelection{Modules: map[string]bool{`module.app["a.b"]`: true}}, "a", child, MembershipSelected},
		{"unindexed differs", ResourceSelection{Modules: map[string]bool{"module.app": true}}, "a", child, MembershipOther},
		{"module alternatives", ResourceSelection{Modules: map[string]bool{"module.no": true, `module.app["a.b"]`: true}}, "a", child, MembershipSelected},
		{"address matches module unknown", ResourceSelection{Addresses: map[string]bool{"a": true}, Modules: map[string]bool{"": true}}, "a", ResourceModule{}, MembershipUnknown},
		{"address fails module unknown", ResourceSelection{Addresses: map[string]bool{"b": true}, Modules: map[string]bool{"": true}}, "a", ResourceModule{}, MembershipOther},
		{"module fails address unknown", ResourceSelection{Addresses: map[string]bool{"a": true}, Modules: map[string]bool{"module.other": true}}, "", child, MembershipOther},
		{"dimensions intersect", ResourceSelection{Addresses: map[string]bool{"a": true}, Modules: map[string]bool{"module.other": true}}, "a", child, MembershipOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.selection.Match(tc.address, tc.module); got != tc.want {
				t.Fatalf("membership = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResourceSelectionReconciles(t *testing.T) {
	l := &Log{
		RPCSpans: []span.Span{
			{Entry: 1, ResourceType: "aws_instance", RPC: "ReadResource", DurationMs: 10, TimestampStatus: logfmt.TimestampValid},
			{Entry: 2, ResourceType: "aws_instance", RPC: "Other", DurationMs: 20, TimestampStatus: logfmt.TimestampValid},
			{Entry: 3, ResourceType: "aws_instance", RPC: "ReadResource", DurationMs: 5, TimestampStatus: logfmt.TimestampValid},
		},
		UISpans:  []span.Span{{Entry: 4, Address: "aws_instance.a", DurationMs: 1000}},
		Contexts: []attrib.Context{{Address: "aws_instance.a"}},
		Attribs: []attrib.Attribution{
			{Address: "aws_instance.a", Confidence: attrib.Contained},
			{Address: "aws_instance.b", Confidence: attrib.Likely},
			{Confidence: attrib.Unattributed},
		},
	}
	index := BuildResourceIndex(l)
	named := ResourceSelection{Addresses: map[string]bool{"aws_instance.a": true}}
	for _, tc := range []struct {
		name            string
		base            Filter
		baseline, other uint64
	}{
		{"all methods", Filter{}, 35, 20},
		{"method filter", Filter{RPCs: map[string]bool{"ReadResource": true}}, 15, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectResources(l, index, tc.base, named)
			e := got.Selection
			if !e.Active || e.Baseline.TotalMs != tc.baseline || e.Selected.Contained.TotalMs != 10 || e.Other.Likely.TotalMs != tc.other || e.Unresolved.TotalMs != 5 {
				t.Fatalf("selection = %+v", e)
			}
			if !slices.Equal(got.RPCIndices, []int{0}) || got.UI != (DurationTotal{Count: 1, TotalMs: 1000, MaxMs: 1000}) {
				t.Fatalf("projection = %+v", got)
			}
			assertResourcePartitions(t, got)
		})
	}
}

func TestResourceProjectionEvidencePriority(t *testing.T) {
	l := &Log{Contexts: []attrib.Context{{Address: "aws_instance.a"}}}
	// Missing type stays its own category for every method and confidence.
	for i, method := range []string{"ReadResource", "GetProviderSchema", "Unknown"} {
		l.RPCSpans = append(l.RPCSpans, span.Span{RPC: method, DurationMs: uint32(i + 1), TimestampStatus: logfmt.TimestampValid})
		l.Attribs = append(l.Attribs, attrib.Attribution{Confidence: attrib.Contained, Address: "aws_instance.a"})
	}
	for i, confidence := range []attrib.Confidence{attrib.Contained, attrib.Likely, attrib.Overlapping, attrib.Ambiguous, attrib.Unattributed} {
		l.RPCSpans = append(l.RPCSpans, span.Span{ResourceType: "aws_instance", DurationMs: uint32(i + 4), TimestampStatus: logfmt.TimestampValid})
		l.Attribs = append(l.Attribs, attrib.Attribution{Confidence: confidence, Address: "aws_instance.a"})
	}
	// Short attribution slices must leave admitted evidence unresolved.
	l.RPCSpans = append(l.RPCSpans, span.Span{ResourceType: "aws_instance", DurationMs: 9, TimestampStatus: logfmt.TimestampValid})
	got := SelectResources(l, BuildResourceIndex(l), Filter{}, ResourceSelection{})
	e := got.Evidence
	if e.Baseline != (DurationTotal{Count: 9, TotalMs: 45, MaxMs: 9}) || e.MissingType.TotalMs != 6 || e.MissingType.Count != 3 || e.NoContext.Count != 0 || e.Contained.TotalMs != 4 || e.Likely.TotalMs != 5 || e.Overlapping.TotalMs != 6 || e.Ambiguous.TotalMs != 7 || e.Unattributed.TotalMs != 17 {
		t.Fatalf("evidence = %+v", e)
	}
	if got.Selection.Active || len(got.RPCIndices) != 9 || got.Selection.Selected.Contained.TotalMs != 10 || got.Selection.Unresolved.TotalMs != 24 {
		t.Fatalf("selection = %+v, indices = %v", got.Selection, got.RPCIndices)
	}
	assertResourcePartitions(t, got)
	got = SelectResources(l, BuildResourceIndex(l), Filter{Types: map[string]bool{"(none)": true}}, ResourceSelection{})
	if got.Evidence.Baseline.TotalMs != 6 || !slices.Equal(got.RPCIndices, []int{0, 1, 2}) {
		t.Fatalf("missing-type filter = %+v", got)
	}
	assertResourcePartitions(t, got)
	l.Contexts = nil
	got = SelectResources(l, BuildResourceIndex(l), Filter{}, ResourceSelection{Addresses: map[string]bool{"aws_instance.b": true}})
	if got.Evidence.MissingType.TotalMs != 6 || got.Evidence.NoContext.TotalMs != 39 || got.Selection.Unresolved.TotalMs != 45 || len(got.RPCIndices) != 0 {
		t.Fatalf("no-context = %+v", got)
	}
	assertResourcePartitions(t, got)
}

func TestResourceSelectionRequiresUsableNamedEvidence(t *testing.T) {
	l := &Log{
		Contexts: []attrib.Context{{Address: "aws_instance.a"}},
		RPCSpans: []span.Span{
			{ResourceType: "aws_instance", DurationMs: 1, TimestampStatus: logfmt.TimestampValid},
			{ResourceType: "aws_instance", DurationMs: 2, TimestampStatus: logfmt.TimestampValid},
			{ResourceType: "aws_instance", DurationMs: 4, TimestampStatus: logfmt.TimestampValid},
			{ResourceType: "aws_instance", DurationMs: 8},
			{ResourceType: "aws_instance", DurationMs: math.MaxUint32, DurationSaturated: true, TimestampStatus: logfmt.TimestampValid},
			{ResourceType: "aws_instance", DurationMs: 16, TimestampStatus: logfmt.TimestampValid},
			{ResourceType: "aws_instance", DurationMs: 32, TimestampStatus: logfmt.TimestampValid},
		},
		Attribs: []attrib.Attribution{
			{Address: "aws_instance.a", ModuleInvalid: true, Confidence: attrib.Contained},
			{Address: "aws_instance.b", ModuleInvalid: true, Confidence: attrib.Likely},
			{Address: "aws_instance.a", Confidence: attrib.Overlapping},
			{Address: "aws_instance.b", Confidence: attrib.Contained},
			{Address: "aws_instance.a", Confidence: attrib.Contained},
			{Address: "aws_instance.b", Confidence: attrib.Ambiguous, Candidates: 2},
			{Confidence: attrib.Likely},
		},
		UISpans: []span.Span{{Address: "aws_instance.a", DurationMs: 100}},
	}
	index := BuildResourceIndex(l)
	got := SelectResources(l, index, Filter{}, ResourceSelection{Addresses: map[string]bool{"aws_instance.a": true}, Modules: map[string]bool{"": true}})
	if !slices.Equal(got.RPCIndices, []int{2}) || got.Selection.Selected.Overlapping.TotalMs != 4 || got.Selection.Other.Likely.TotalMs != 2 || got.Selection.Unresolved.TotalMs != uint64(math.MaxUint32)+57 || !got.Selection.Unresolved.LowerBound {
		t.Fatalf("active selection = %+v", got)
	}
	if len(got.Rows) != 1 || got.Rows[0].NamedRPC.Count != 0 || got.Rows[0].OverlappingRPC.TotalMs != 4 {
		t.Fatalf("row supplements = %+v", got.Rows)
	}
	assertResourcePartitions(t, got)
	got = SelectResources(l, index, Filter{}, ResourceSelection{})
	if len(got.RPCIndices) != 7 || len(got.Rows) != 1 || got.Rows[0].NamedRPC.TotalMs != 1 || got.Rows[0].NamedRPC.LowerBound || got.Selection.Unresolved.TotalMs != uint64(math.MaxUint32)+56 {
		t.Fatalf("inactive selection = %+v", got)
	}
	assertResourcePartitions(t, got)
}

func TestResourceProjectionGroupsOnlyObservedUI(t *testing.T) {
	l := &Log{
		UISpans: []span.Span{
			{Entry: 10, Address: "aws_instance.b", ResourceType: "aws_instance", RPC: "create", DurationMs: 10},
			{Entry: 12, Address: "aws_instance.a", ResourceType: "aws_instance", RPC: "refresh", DurationMs: 20},
			{Entry: 14, Address: "aws_instance.b", ResourceType: "aws_instance", RPC: "update", DurationMs: 10},
			{Entry: 16, Address: "aws_instance.b", ResourceType: "aws_instance", RPC: "delete", DurationMs: 0},
			{Entry: 18, DurationMs: 3},
			{Entry: 20, Address: "aws_bucket.c", ResourceType: "aws_bucket", DurationMs: math.MaxUint32, DurationSaturated: true},
		},
		Contexts: []attrib.Context{{Address: "aws_instance.context_only"}},
		RPCSpans: []span.Span{
			{Entry: 1, Provider: "p", ResourceType: "aws_instance", RPC: "ReadResource", DurationMs: 1, TimestampStatus: logfmt.TimestampValid},
			{Entry: 3, Provider: "q", ResourceType: "aws_instance", RPC: "ReadResource", DurationMs: 2, TimestampStatus: logfmt.TimestampValid},
			{Entry: 5, Provider: "p", ResourceType: "aws_instance", RPC: "Other", DurationMs: 4, TimestampStatus: logfmt.TimestampValid},
			{Entry: 7, Provider: "p", ResourceType: "aws_instance", RPC: "ReadResource", DurationMs: 8, TimestampStatus: logfmt.TimestampValid},
		},
		Attribs: []attrib.Attribution{
			{Address: "aws_instance.b", Confidence: attrib.Contained},
			{Address: "aws_instance.b", Confidence: attrib.Likely},
			{Address: "aws_instance.b", Confidence: attrib.Overlapping},
			{Address: "aws_instance.context_only", Confidence: attrib.Contained},
		},
	}
	index := BuildResourceIndex(l)
	got := SelectResources(l, index, Filter{}, ResourceSelection{})
	if len(got.Rows) != 3 {
		t.Fatalf("rows = %+v", got.Rows)
	}
	if got.Rows[0].Address != "aws_bucket.c" || !got.Rows[0].UI.LowerBound || got.Rows[1].Address != "aws_instance.a" || got.Rows[2].Address != "aws_instance.b" {
		t.Fatalf("ranked rows = %+v", got.Rows)
	}
	b := got.Rows[2]
	if b.UI != (DurationTotal{Count: 3, TotalMs: 20, MaxMs: 10}) || b.NamedRPC.TotalMs != 3 || b.NamedRPC.Count != 2 || b.OverlappingRPC.TotalMs != 4 || !reflect.DeepEqual(b.Operations, []ResourceOperation{{UIIndex: 0, Module: ResourceModule{Known: true}}, {UIIndex: 2, Module: ResourceModule{Known: true}}, {UIIndex: 3, Module: ResourceModule{Known: true}}}) {
		t.Fatalf("repeated operations = %+v", b)
	}
	if got.UI.Count != 6 || got.UI.TotalMs != uint64(math.MaxUint32)+43 || !got.UI.LowerBound || got.UnnamedUI != (DurationTotal{Count: 1, TotalMs: 3, MaxMs: 3}) || !slices.Equal(got.UIIndices, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("UI totals = %+v", got)
	}
	assertResourcePartitions(t, got)
	got = SelectResources(l, index, Filter{Providers: map[string]bool{"p": true}, RPCs: map[string]bool{"ReadResource": true}, Types: map[string]bool{"aws_instance": true}, Levels: map[logfmt.Level]bool{}}, ResourceSelection{})
	if !slices.Equal(got.RPCIndices, []int{0, 3}) || !slices.Equal(got.UIIndices, []int{0, 1, 2, 3}) || got.UI.TotalMs != 40 || len(got.Rows) != 2 || got.Rows[1].NamedRPC.TotalMs != 1 || got.Rows[1].OverlappingRPC.Count != 0 {
		t.Fatalf("filtered projection = %+v", got)
	}
	assertResourcePartitions(t, got)
}

func TestResourceProjectionTierAvailabilityAndQuality(t *testing.T) {
	for _, path := range []string{"../../testdata/structured-ui.log", "../../testdata/mixed-hcp.log", "../attrib/testdata/context.log"} {
		t.Run(path, func(t *testing.T) {
			l, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			before := l.CaptureQuality()
			index := BuildResourceIndex(l)
			all := SelectResources(l, index, Filter{}, ResourceSelection{})
			if len(all.RPCIndices) != len(l.RPCSpans) || len(all.UIIndices) != len(l.UISpans) {
				t.Fatalf("admitted tiers lost: %+v", all)
			}
			assertResourcePartitions(t, all)
			for _, selection := range []ResourceSelection{{Addresses: map[string]bool{}}, {Modules: map[string]bool{"": false}}} {
				got := SelectResources(l, index, Filter{}, selection)
				if !got.Selection.Active || len(got.RPCIndices) != 0 || len(got.UIIndices) != 0 || len(got.Rows) != 0 || got.Evidence != all.Evidence {
					t.Fatalf("empty selection = %+v", got)
				}
				assertResourcePartitions(t, got)
			}
			if after := l.CaptureQuality(); !reflect.DeepEqual(before, after) {
				t.Fatalf("quality changed: before=%+v after=%+v", before, after)
			}
		})
	}
	for _, l := range []*Log{nil, {}, {RPCSpans: []span.Span{{DurationMs: 0}}}, {UISpans: []span.Span{{Address: "aws_instance.zero", DurationMs: 0}}}} {
		got := SelectResources(l, BuildResourceIndex(l), Filter{}, ResourceSelection{})
		if l != nil && (got.UI.Count != uint64(len(l.UISpans)) || got.Evidence.Baseline.Count != uint64(len(l.RPCSpans))) {
			t.Fatalf("zero observation lost: %+v", got)
		}
		assertResourcePartitions(t, got)
	}
}

func assertResourcePartitions(t *testing.T, got ResourceProjection) {
	t.Helper()
	e, s := got.Evidence, got.Selection
	for name, parts := range map[string][]DurationTotal{
		"evidence":  {e.MissingType, e.NoContext, e.Contained, e.Likely, e.Overlapping, e.Ambiguous, e.Unattributed},
		"selection": {s.Selected.Contained, s.Selected.Likely, s.Selected.Overlapping, s.Other.Contained, s.Other.Likely, s.Other.Overlapping, s.Unresolved},
	} {
		var count, total uint64
		for _, part := range parts {
			count += part.Count
			total += part.TotalMs
		}
		if count != e.Baseline.Count || total != e.Baseline.TotalMs {
			t.Errorf("%s partition count=%d ms=%d, baseline=%+v", name, count, total, e.Baseline)
		}
	}
	if s.Baseline != e.Baseline {
		t.Errorf("selection baseline=%+v, evidence baseline=%+v", s.Baseline, e.Baseline)
	}
}
