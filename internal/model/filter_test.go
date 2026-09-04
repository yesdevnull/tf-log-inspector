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

// Every value FacetsForSpans offers must be a value MatchSpan can match,
// and it must select exactly the spans the offered count promised. The two
// sides are separate code, so a dimension where one normalises an empty
// value and the other does not puts a checkbox with a real, positive count
// in front of the user that selects nothing at all.
//
// A provider-level RPC (GetProviderSchema, ConfigureProvider,
// ValidateProviderConfig) belongs to no resource type, so an empty value is
// ordinary rather than exotic, and it is what this fixture carries.
func TestEveryOfferedFacetValueSelectsItsAdvertisedCount(t *testing.T) {
	spans := []span.Span{
		sp("", "aws", "GetProviderSchema", 12),
		sp("aws_subnet", "aws", "ApplyResourceChange", 5),
		sp("aws_subnet", "google", "ApplyResourceChange", 3),
	}
	dims := map[string]func(map[string]bool) Filter{
		"provider":      func(sel map[string]bool) Filter { return Filter{Providers: sel} },
		"rpc":           func(sel map[string]bool) Filter { return Filter{RPCs: sel} },
		"resource type": func(sel map[string]bool) Filter { return Filter{Types: sel} },
	}
	var sawNone bool
	for _, f := range FacetsForSpans(spans) {
		build, ok := dims[f.Name]
		if !ok {
			t.Fatalf("dimension %q has no filter field, so ticking it cannot be tested", f.Name)
		}
		for _, v := range f.Values {
			if v.Value == FacetKey("") {
				sawNone = true
			}
			got := build(map[string]bool{v.Value: true}).SpansMatching(spans)
			if len(got) != v.Count {
				t.Errorf("%s=%q advertises %d spans, selecting it matches %d", f.Name, v.Value, v.Count, len(got))
			}
		}
	}
	if !sawNone {
		t.Fatalf("no %q value was offered, so the case that broke is not covered", FacetKey(""))
	}
}

// The rollup, the join and the facet pane all label an empty key the same
// way, so a bucket, a joined row and a checkbox for the same spans carry the
// same name and the user can move between the three views without one of
// them appearing to lose a row.
func TestFacetKeyLabelsAnEmptyValueTheSameWayEverywhere(t *testing.T) {
	spans := []span.Span{sp("", "aws", "GetProviderSchema", 12)}
	none := FacetKey("")
	if got := RollupBy(spans, func(s span.Span) string { return s.ResourceType }); got[0].Key != none {
		t.Errorf("RollupBy labelled an empty key %q, want %q", got[0].Key, none)
	}
	if got := JoinByResourceType(spans, nil); got[0].ResourceType != none {
		t.Errorf("JoinByResourceType labelled an empty key %q, want %q", got[0].ResourceType, none)
	}
	if !(Filter{Types: map[string]bool{none: true}}).MatchSpan(spans[0]) {
		t.Errorf("MatchSpan rejected a span with an empty resource type against %q", none)
	}
}
