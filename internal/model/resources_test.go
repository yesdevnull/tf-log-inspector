package model

import (
	"math"
	"reflect"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourceIndexRetainsOccurrences(t *testing.T) {
	l := &Log{UISpans: []span.Span{
		{Entry: 2, Address: "aws_instance.a", RPC: "create", DurationMs: 1000, ModuleKnown: true},
		{Entry: 7, Address: "aws_instance.a", RPC: "update", DurationMs: 0, ModuleKnown: true},
		{Entry: 9, DurationMs: 20, DurationSaturated: true},
	}}
	got := BuildResourceIndex(l)
	if len(got.Operations) != 3 || len(got.Choices) != 1 {
		t.Fatalf("index=%+v", got)
	}
	for i, op := range got.Operations {
		if op.UIIndex != i {
			t.Fatalf("operation %d index=%d", i, op.UIIndex)
		}
	}
	first, second, third := l.UISpans[got.Operations[0].UIIndex], l.UISpans[got.Operations[1].UIIndex], l.UISpans[got.Operations[2].UIIndex]
	if first.Entry != 2 || first.RPC != "create" || second.Entry != 7 || second.RPC != "update" || second.DurationMs != 0 {
		t.Errorf("indexed occurrences lost source facts: first=%+v second=%+v", first, second)
	}
	if third.Entry != 9 || third.HasPosition() || !third.DurationSaturated {
		t.Errorf("unnamed saturated operation=%+v, want retained and unpositioned", third)
	}
	if got.Choices[0] != (ResourceChoice{Address: "aws_instance.a", Module: ResourceModule{Known: true}}) {
		t.Errorf("choice=%+v, want known root aws_instance.a", got.Choices[0])
	}
	if !reflect.DeepEqual(got.Modules, []string{""}) {
		t.Errorf("modules=%q, want root", got.Modules)
	}
}

func TestResourceIndexIncludesCompleteEvidenceWithoutInventingAmbiguousChoices(t *testing.T) {
	l := &Log{
		UISpans: []span.Span{
			{Address: `module.app[0].module.db.aws_instance.ui`, Module: `module.app[0].module.db`, ModuleKnown: true},
			{Address: "aws_instance.invalid", ModuleInvalid: true},
			{Module: `module.unnamed`, ModuleKnown: true},
		},
		Contexts: []attrib.Context{
			{Address: `module.context.aws_instance.only`, Module: `module.context`, ModuleKnown: true},
			{Address: "aws_instance.context_invalid", ModuleInvalid: true},
			{Address: "opaque_conflict", Module: `module.first`, ModuleKnown: true},
		},
		RPCSpans: make([]span.Span, 6),
		Attribs: []attrib.Attribution{
			{Address: `module.rpc.aws_instance.contained`, Module: `module.rpc`, ModuleKnown: true, Confidence: attrib.Contained},
			{Address: "aws_instance.likely", ModuleInvalid: true, Confidence: attrib.Likely},
			{Address: "opaque_conflict", Module: `module.second`, ModuleKnown: true, Confidence: attrib.Overlapping},
			{Address: "aws_instance.invented", ModuleKnown: true, Confidence: attrib.Ambiguous},
			{Address: `module.derived.aws_instance.rpc`, Module: `module.ignored`, Confidence: attrib.Contained},
		},
	}

	got := BuildResourceIndex(l)
	wantChoices := []ResourceChoice{
		{Address: "aws_instance.context_invalid"},
		{Address: "aws_instance.invalid"},
		{Address: "aws_instance.likely"},
		{Address: `module.app[0].module.db.aws_instance.ui`, Module: ResourceModule{Path: `module.app[0].module.db`, Known: true}},
		{Address: `module.context.aws_instance.only`, Module: ResourceModule{Path: `module.context`, Known: true}},
		{Address: `module.derived.aws_instance.rpc`, Module: ResourceModule{Path: `module.derived`, Known: true}},
		{Address: `module.rpc.aws_instance.contained`, Module: ResourceModule{Path: `module.rpc`, Known: true}},
		{Address: "opaque_conflict"},
	}
	if !reflect.DeepEqual(got.Choices, wantChoices) {
		t.Errorf("choices=\n%+v\nwant=\n%+v", got.Choices, wantChoices)
	}
	wantModules := []string{"", `module.app[0]`, `module.app[0].module.db`, `module.context`, `module.derived`, `module.first`, `module.rpc`, `module.second`, `module.unnamed`}
	if !reflect.DeepEqual(got.Modules, wantModules) {
		t.Errorf("modules=%q, want %q", got.Modules, wantModules)
	}
	if len(got.RPCModules) != len(l.RPCSpans) {
		t.Fatalf("RPC modules=%d, want %d", len(got.RPCModules), len(l.RPCSpans))
	}
	if got.RPCModules[0] != (ResourceModule{Path: `module.rpc`, Known: true}) || got.RPCModules[1].Known || got.RPCModules[3].Known || got.RPCModules[4] != (ResourceModule{Path: `module.derived`, Known: true}) || got.RPCModules[5].Known {
		t.Errorf("RPC modules=%+v", got.RPCModules)
	}
}

func TestResourceIndexKnownEvidenceSupplementsUnknownWithoutErasingConflicts(t *testing.T) {
	l := &Log{
		UISpans: []span.Span{{Address: "opaque_supplement"}, {Address: "opaque_conflict", Module: `module.a`, ModuleKnown: true}},
		Contexts: []attrib.Context{
			{Address: "opaque_supplement", Module: `module.good`, ModuleKnown: true},
			{Address: "opaque_conflict", Module: `module.b`, ModuleKnown: true},
			{Address: "opaque_conflict"},
		},
	}
	got := BuildResourceIndex(l)
	if len(got.Choices) != 2 {
		t.Fatalf("choices=%+v, want two exact addresses", got.Choices)
	}
	if got.Choices[0] != (ResourceChoice{Address: "opaque_conflict"}) {
		t.Errorf("conflicting choice=%+v, want unknown", got.Choices[0])
	}
	if got.Choices[1] != (ResourceChoice{Address: "opaque_supplement", Module: ResourceModule{Path: `module.good`, Known: true}}) {
		t.Errorf("supplemented choice=%+v", got.Choices[1])
	}
}

func TestResourceIndexSuppliedUnavailableModuleEvidenceIsSticky(t *testing.T) {
	const address = `module.a.aws_instance.x`
	knownUI := span.Span{Address: address, Module: `module.a`, ModuleKnown: true}
	knownContext := attrib.Context{Address: address, Module: `module.a`, ModuleKnown: true}
	cases := []struct {
		name               string
		log                Log
		wantOperationKnown bool
	}{
		{"UI contradiction before known context", Log{UISpans: []span.Span{{Address: address, Module: `module.b`, ModuleKnown: true}}, Contexts: []attrib.Context{knownContext}}, false},
		{"UI invalid before known context", Log{UISpans: []span.Span{{Address: address, ModuleInvalid: true}}, Contexts: []attrib.Context{knownContext}}, false},
		{"UI malformed before known context", Log{UISpans: []span.Span{{Address: address, Module: `module.`, ModuleKnown: true}}, Contexts: []attrib.Context{knownContext}}, false},
		{"context contradiction after known UI", Log{UISpans: []span.Span{knownUI}, Contexts: []attrib.Context{{Address: address, Module: `module.b`, ModuleKnown: true}}}, true},
		{"context invalid after known UI", Log{UISpans: []span.Span{knownUI}, Contexts: []attrib.Context{{Address: address, ModuleInvalid: true}}}, true},
		{"context malformed after known UI", Log{UISpans: []span.Span{knownUI}, Contexts: []attrib.Context{{Address: address, Module: `module.`, ModuleKnown: true}}}, true},
		{"named RPC contradiction after known UI", Log{UISpans: []span.Span{knownUI}, RPCSpans: []span.Span{{}}, Attribs: []attrib.Attribution{{Address: address, Module: `module.b`, ModuleKnown: true, Confidence: attrib.Contained}}}, true},
		{"named RPC invalid after known UI", Log{UISpans: []span.Span{knownUI}, RPCSpans: []span.Span{{}}, Attribs: []attrib.Attribution{{Address: address, ModuleInvalid: true, Confidence: attrib.Contained}}}, true},
		{"named RPC malformed after known UI", Log{UISpans: []span.Span{knownUI}, RPCSpans: []span.Span{{}}, Attribs: []attrib.Attribution{{Address: address, Module: `module.`, ModuleKnown: true, Confidence: attrib.Contained}}}, true},
	}
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			got := BuildResourceIndex(&tc.log)
			if len(got.Choices) != 1 {
				t.Fatalf("choices=%+v, want one exact address", got.Choices)
			}
			if got.Choices[0] != (ResourceChoice{Address: address}) {
				t.Errorf("choice=%+v, want sticky unknown module", got.Choices[0])
			}
			if got.Operations[0].Module.Known != tc.wantOperationKnown {
				t.Errorf("operation module=%+v, want Known=%v independently of aggregate choice", got.Operations[0].Module, tc.wantOperationKnown)
			}
			if len(got.RPCModules) > 0 && got.RPCModules[0].Known {
				t.Errorf("RPC module=%+v, want its independently unresolved fact", got.RPCModules[0])
			}
		})
	}
}

func TestResourceIndexLoadedCompletionsRetainDistinctSourceLocations(t *testing.T) {
	l, err := Load(fixture(t, "structured-ui.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := BuildResourceIndex(l)
	if len(got.Operations) != 2 {
		t.Fatalf("operations=%d, want two real completions", len(got.Operations))
	}
	first := l.UISpans[got.Operations[0].UIIndex]
	second := l.UISpans[got.Operations[1].UIIndex]
	firstLocation, firstOK := l.SourceLocation(first.Entry)
	secondLocation, secondOK := l.SourceLocation(second.Entry)
	if !firstOK || !secondOK || first.Entry == second.Entry || firstLocation.StartByte == secondLocation.StartByte {
		t.Fatalf("locations=(%+v,%v) (%+v,%v), entries=%d,%d", firstLocation, firstOK, secondLocation, secondOK, first.Entry, second.Entry)
	}
	if first.RPC != "read" || second.RPC != "create" {
		t.Errorf("completion actions=%q,%q, want read,create", first.RPC, second.RPC)
	}
}

func TestResourceIndexDurationTotalRetainsZeroAndSaturatedDurations(t *testing.T) {
	var got DurationTotal
	got.add(span.Span{DurationMs: 0})
	got.add(span.Span{DurationMs: math.MaxUint32, DurationSaturated: true})
	if got != (DurationTotal{Count: 2, TotalMs: math.MaxUint32, MaxMs: math.MaxUint32, LowerBound: true}) {
		t.Errorf("total=%+v", got)
	}
}
