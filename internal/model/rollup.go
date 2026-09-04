package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// noKey labels spans with no value for the rollup's key. They are kept rather
// than dropped so a rollup's totals always reconcile with the summed span
// time -- a missing row is far harder to notice than an explicit one.
const noKey = "(none)"

// Bucket is one row of a rollup.
type Bucket struct {
	Key     string
	TotalMs uint64 // widened: 2174 spans summing to 1.5e6 ms is comfortable in
	MaxMs   uint32 // uint32, but a long apply is not, and overflow here would
	Count   int    // silently invert the ordering
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
		k := key(s)
		if k == "" {
			k = noKey
		}
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
