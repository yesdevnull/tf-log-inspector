package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// An empty dimension means "no opinion", not "match nothing". Getting this
// backwards makes a fresh filter hide the whole log, which reads as the tool
// being broken rather than as a filter being active.
func TestEmptyFilterMatchesEverything(t *testing.T) {
	var f Filter
	if !f.MatchSpan(sp("aws_instance", "aws", "ReadResource", 1)) {
		t.Error("empty filter rejected a span")
	}
	if !f.MatchEntry(logfmt.Entry{Level: logfmt.LevelTrace}) {
		t.Error("empty filter rejected an entry")
	}
}

// Within a dimension the selected values are alternatives; across dimensions
// every constraint must hold. Facets are described in the spec as cumulative.
func TestFilterIsOrWithinAndAndAcross(t *testing.T) {
	f := Filter{
		Providers: map[string]bool{"aws": true, "azurerm": true},
		RPCs:      map[string]bool{"ReadResource": true},
	}
	if !f.MatchSpan(sp("t", "aws", "ReadResource", 1)) {
		t.Error("rejected a span matching both dimensions")
	}
	if !f.MatchSpan(sp("t", "azurerm", "ReadResource", 1)) {
		t.Error("rejected the second alternative within a dimension")
	}
	if f.MatchSpan(sp("t", "aws", "ApplyResourceChange", 1)) {
		t.Error("accepted a span failing the RPC dimension")
	}
	if f.MatchSpan(sp("t", "google", "ReadResource", 1)) {
		t.Error("accepted a span failing the provider dimension")
	}
}

// A populated Levels map is restrictive, not permissive -- unlike the empty
// case above, only the selected levels may pass.
func TestMatchEntryFiltersByLevel(t *testing.T) {
	f := Filter{Levels: map[logfmt.Level]bool{logfmt.LevelWarn: true}}
	if !f.MatchEntry(logfmt.Entry{Level: logfmt.LevelWarn}) {
		t.Error("rejected an entry at a selected level")
	}
	if f.MatchEntry(logfmt.Entry{Level: logfmt.LevelTrace}) {
		t.Error("accepted an entry at a level not selected")
	}
}

func TestSpansMatchingPreservesOrder(t *testing.T) {
	spans := []span.Span{
		sp("a", "aws", "r", 3),
		sp("b", "google", "r", 2),
		sp("c", "aws", "r", 1),
	}
	f := Filter{Providers: map[string]bool{"aws": true}}
	got := f.SpansMatching(spans)
	if len(got) != 2 || got[0].ResourceType != "a" || got[1].ResourceType != "c" {
		t.Errorf("SpansMatching = %+v, want a then c in input order", got)
	}
}

// Facet counts drive the counts shown beside each value in the TUI's facet
// pane, so they count spans, not distinct values.
func TestFacetsForSpansCountsAndOrders(t *testing.T) {
	spans := []span.Span{
		sp("t1", "aws", "ReadResource", 1),
		sp("t2", "aws", "ReadResource", 1),
		sp("t3", "google", "ApplyResourceChange", 1),
	}
	facets := FacetsForSpans(spans)
	byName := map[string]Facet{}
	for _, f := range facets {
		byName[f.Name] = f
	}
	prov, ok := byName["provider"]
	if !ok {
		t.Fatalf("no provider facet: %+v", facets)
	}
	if prov.Values[0].Value != "aws" || prov.Values[0].Count != 2 {
		t.Errorf("provider facet = %+v, want aws with count 2 first", prov.Values)
	}
	if _, ok := byName["rpc"]; !ok {
		t.Error("no rpc facet")
	}
	if _, ok := byName["resource type"]; !ok {
		t.Error("no resource type facet")
	}
}
