package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// FacetValue is one selectable value and how many spans carry it.
type FacetValue struct {
	Value string
	Count int
}

// Facet is one filterable dimension.
type Facet struct {
	Name   string
	Values []FacetValue
}

// Filter is a cumulative facet selection: an allow-list per dimension, all
// of which must hold.
//
// A NIL map is the caller having no opinion about that dimension, so a zero
// Filter passes everything. An EMPTY non-nil map is the opposite -- an
// allow-list that admits no value -- and matches nothing. The two are told
// apart rather than both read as "unconstrained" because the facet pane
// builds its allow-lists by removing the values the reader unticked, and a
// reader who unticks a dimension's last value hands down an empty one:
// reading that as "no opinion" would answer their last untick by putting
// the whole log back on screen.
type Filter struct {
	Providers map[string]bool
	RPCs      map[string]bool
	Types     map[string]bool
	Levels    map[logfmt.Level]bool
}

// selected reports whether a value passes one dimension. The value is
// normalised through FacetKey, so it is compared against the same key
// FacetsForSpans offered the user as a checkbox: a span with no value for
// the dimension passes when "(none)" is selected, and only then. A nil set
// is no opinion and passes everything; an empty one admits nothing (see
// Filter).
func selected(set map[string]bool, v string) bool {
	if set == nil {
		return true
	}
	return set[FacetKey(v)]
}

// MatchSpan applies every dimension: alternatives within one, conjunction
// across them, as the spec's cumulative facets require.
func (f Filter) MatchSpan(s span.Span) bool {
	return selected(f.Providers, s.Provider) &&
		selected(f.RPCs, s.RPC) &&
		selected(f.Types, s.ResourceType)
}

// MatchEntry applies the level dimension. Span dimensions do not apply to a
// raw entry: most entries belong to no span at all.
func (f Filter) MatchEntry(e logfmt.Entry) bool {
	if f.Levels == nil {
		return true
	}
	return f.Levels[e.Level]
}

// SpansMatching returns the matching spans in their input order, which is
// scan order, so a caller that wants a different order sorts explicitly.
func (f Filter) SpansMatching(spans []span.Span) []span.Span {
	out := make([]span.Span, 0, len(spans))
	for _, s := range spans {
		if f.MatchSpan(s) {
			out = append(out, s)
		}
	}
	return out
}

// sortFacetValues orders a facet's values by span count descending, ties
// broken by value ascending so the ordering is total.
func sortFacetValues(vs []FacetValue) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Count != vs[j].Count {
			return vs[i].Count > vs[j].Count
		}
		return vs[i].Value < vs[j].Value
	})
}

// FacetsForSpans builds the selectable dimensions, each ordered by span count
// descending then value ascending, so the ordering is total.
func FacetsForSpans(spans []span.Span) []Facet {
	dims := []struct {
		name string
		key  func(span.Span) string
	}{
		{"provider", func(s span.Span) string { return s.Provider }},
		{"rpc", func(s span.Span) string { return s.RPC }},
		{"resource type", func(s span.Span) string { return s.ResourceType }},
	}
	out := make([]Facet, 0, len(dims))
	for _, d := range dims {
		counts := map[string]int{}
		for _, s := range spans {
			counts[FacetKey(d.key(s))]++
		}
		f := Facet{Name: d.name, Values: make([]FacetValue, 0, len(counts))}
		for v, c := range counts {
			f.Values = append(f.Values, FacetValue{Value: v, Count: c})
		}
		sortFacetValues(f.Values)
		out = append(out, f)
	}
	return out
}
