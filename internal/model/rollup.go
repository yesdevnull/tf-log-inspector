package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// noKey labels spans with no value for the rollup's key. They are kept rather
// than dropped so a rollup's totals always reconcile with the summed span
// time -- a missing row is far harder to notice than an explicit one.
const noKey = "(none)"

// FacetKey normalises a span's value for one dimension into the key every
// consumer of that dimension agrees on: an empty value is always "(none)".
//
// It is one function rather than a substitution repeated per call site
// because the two sides have to agree exactly. FacetsForSpans OFFERS the
// key, RollupBy and JoinByResourceType GROUP by it, and MatchSpan MATCHES
// against it; a dimension where one of those four disagrees with the others
// puts a checkbox with a real, positive count in front of the user that
// selects a value no span carries. Empty values are ordinary -- a
// provider-level RPC (GetProviderSchema, ConfigureProvider,
// ValidateProviderConfig) belongs to no resource type at all.
//
// It is exported because internal/tui resolves a raw log entry's component
// to a provider the same way, and an entry belonging to no provider has to
// normalise to the same key the facet pane offers.
func FacetKey(v string) string {
	if v == "" {
		return noKey
	}
	return v
}

// Bucket is one row of a rollup.
//
// TotalMs is widened to uint64: 2174 spans summing to 1.5e6 ms is comfortable
// in uint32, but a long apply is not, and overflow here would silently
// invert the ordering.
type Bucket struct {
	Key     string
	TotalMs uint64
	MaxMs   uint32
	Count   int
}

// RollupBy groups spans by a caller-supplied key and returns buckets ordered
// by total time descending, ties broken by key ascending so the ordering is
// total and two runs over one log cannot disagree.
//
// Callers must pass spans of a single Fidelity. DurationMs is comparable
// across builders, but mixing them conflates two different measurements of
// overlapping work: an RPC span times one call, a UI-hook span times a whole
// resource. JoinByResourceType is the supported way to show both.
func RollupBy(spans []span.Span, key func(span.Span) string) []Bucket {
	if len(spans) == 0 {
		return nil
	}
	byKey := make(map[string]*Bucket)
	for _, s := range spans {
		k := FacetKey(key(s))
		b := byKey[k]
		if b == nil {
			b = &Bucket{Key: k}
			byKey[k] = b
		}
		b.TotalMs += uint64(s.DurationMs)
		b.Count++
		if s.DurationMs > b.MaxMs {
			b.MaxMs = s.DurationMs
		}
	}
	out := make([]Bucket, 0, len(byKey))
	for _, b := range byKey {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMs != out[j].TotalMs {
			return out[i].TotalMs > out[j].TotalMs
		}
		return out[i].Key < out[j].Key
	})
	return out
}
