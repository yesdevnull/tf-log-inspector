package model

import (
	"github.com/yesdevnull/tf-log-inspector/internal/span"
	"testing"
)

func TestResourceObservationSelectionAndModuleMeans(t *testing.T) {
	l := &Log{UISpans: []span.Span{
		{Address: "module.a[0].aws_instance.x", RPC: "create", DurationSource: span.SourceUIElapsed, DurationMs: 1000},
		{Address: "module.a[0].aws_instance.y", RPC: "create", DurationSource: span.SourceUIElapsed, DurationMs: 3000, DurationSaturated: true},
		{Address: "module.a[1].aws_instance.x", RPC: "create", DurationSource: span.SourceCLIElapsed, DurationMs: 9000},
		{Address: "aws_instance.root", RPC: "refresh", DurationSource: span.SourceRefreshWindow, DurationMs: 500},
		{Address: "invalid", RPC: "create", DurationSource: span.SourceUIElapsed, DurationMs: 700},
	}, RPCSpans: []span.Span{{RPC: "ReadResource", DurationMs: 10}}}
	idx := BuildResourceIndex(l)
	p := SelectResources(l, idx, Filter{}, ResourceSelection{Sources: map[string]bool{"ui_elapsed": true}, Actions: map[string]bool{"create": true}})
	if p.UI.Count != 3 || p.UI.TotalMs != 4700 || len(p.RPCIndices) != 1 {
		t.Fatalf("source/action selection = %+v", p)
	}
	groups := GroupResourcesByModule(l, idx, p)
	if len(groups) != 2 || groups[0].Module.Path != "module.a[0]" || groups[0].UI.MeanMs() != 2000 || !groups[0].UI.LowerBound || groups[0].Sources[span.SourceUIElapsed].Count != 2 {
		t.Fatalf("module ranking = %+v", groups)
	}
	all := GroupResourcesByModule(l, idx, SelectResources(l, idx, Filter{}, ResourceSelection{}))
	if len(all) != 4 {
		t.Fatalf("module instances/root/unavailable = %+v", all)
	}
	root := SelectResources(l, idx, Filter{}, ResourceSelection{ExactModules: map[ResourceModule]bool{{Known: true}: true}})
	if root.UI.Count != 1 || root.Rows[0].Address != "aws_instance.root" {
		t.Fatalf("exact root selected subtree: %+v", root)
	}
	unknown := SelectResources(l, idx, Filter{}, ResourceSelection{ExactModules: map[ResourceModule]bool{{}: true}})
	if unknown.UI.Count != 1 || unknown.Rows[0].Address != "invalid" {
		t.Fatalf("unavailable module selection = %+v", unknown)
	}
	if (DurationTotal{}).MeanMs() != 0 {
		t.Fatal("empty mean must be zero")
	}
}
