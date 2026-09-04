package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// TypeRow is one resource type as both tiers saw it.
//
// The two sides measure overlapping work at different granularities and must
// not be added together. A UI-hook figure times a whole resource, from
// Terraform's own hooks, quantised to whole seconds with up to a second of
// error either way. An RPC figure times one provider call exactly, in
// milliseconds, and one resource may make several. Reading them side by side
// is the point: a type whose UI total dwarfs its RPC total spent its time
// somewhere other than in provider calls.
type TypeRow struct {
	ResourceType string

	UIResources int
	UITotalMs   uint64
	UIMaxMs     uint32

	RPCCalls   int
	RPCTotalMs uint64
	RPCMaxMs   uint32
}

// JoinByResourceType puts each tier's view of a resource type on one row.
//
// It keys on ResourceType because that field means the same thing in both
// builders -- unlike RPC, which holds an RPC name on reported spans and a
// hook action on UI-hook spans. No address attribution is involved, which is
// why this works on the dual-tier capture even though that capture has no
// core graph output.
func JoinByResourceType(rpcSpans, uiSpans []span.Span) []TypeRow {
	rows := make(map[string]*TypeRow)
	get := func(k string) *TypeRow {
		k = FacetKey(k)
		r := rows[k]
		if r == nil {
			r = &TypeRow{ResourceType: k}
			rows[k] = r
		}
		return r
	}

	for _, s := range rpcSpans {
		r := get(s.ResourceType)
		r.RPCCalls++
		r.RPCTotalMs += uint64(s.DurationMs)
		if s.DurationMs > r.RPCMaxMs {
			r.RPCMaxMs = s.DurationMs
		}
	}
	for _, s := range uiSpans {
		r := get(s.ResourceType)
		r.UIResources++
		r.UITotalMs += uint64(s.DurationMs)
		if s.DurationMs > r.UIMaxMs {
			r.UIMaxMs = s.DurationMs
		}
	}

	out := make([]TypeRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UITotalMs != out[j].UITotalMs {
			return out[i].UITotalMs > out[j].UITotalMs
		}
		if out[i].RPCTotalMs != out[j].RPCTotalMs {
			return out[i].RPCTotalMs > out[j].RPCTotalMs
		}
		return out[i].ResourceType < out[j].ResourceType
	})
	return out
}
