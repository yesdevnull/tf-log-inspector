package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestComparisonSeparatesResourceDurationSources(t *testing.T) {
	before := ComparisonInput{UI: []span.Span{{Address: "resource.a", ResourceType: "resource", RPC: "read", DurationMs: 1000, DurationSource: span.SourceUIElapsed}}}
	after := ComparisonInput{UI: []span.Span{{Address: "resource.a", ResourceType: "resource", RPC: "read", DurationMs: 10, DurationSource: span.SourceRefreshWindow}}}
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range got.Sections[3:] {
		if len(section.Rows) != 2 {
			t.Fatalf("%s rows = %+v, want separate source rows", section.Kind, section.Rows)
		}
		for _, row := range section.Rows {
			if row.State != "unavailable" || (row.Before == nil) == (row.After == nil) {
				t.Fatalf("cross-source row = %+v", row)
			}
			assertComparisonChanges(t, row.Changes, ComparisonChanges{})
		}
	}
}

func TestComparisonSourceAvailabilityPreservesGroupPresence(t *testing.T) {
	before := ComparisonInput{UI: []span.Span{
		{Address: "resource.a", ResourceType: "resource", RPC: "read", DurationMs: 10, DurationSource: span.SourceCLIElapsed},
		{Address: "resource.a", ResourceType: "resource", RPC: "read", DurationMs: 1, DurationSource: span.SourceRefreshWindow},
	}}
	after := ComparisonInput{UI: []span.Span{
		{Address: "resource.b", ResourceType: "other", RPC: "read", DurationMs: 5, DurationSource: span.SourceCLIElapsed},
		{Address: "resource.a", ResourceType: "resource", RPC: "read", DurationMs: 1, DurationSource: span.SourceRefreshWindow},
	}}
	got, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	rows := got.Sections[4].Rows
	if len(rows) != 3 || rows[0].State != "added" || rows[1].State != "matched" || rows[2].State != "removed" {
		t.Fatalf("same-source rows = %+v", rows)
	}
	if rows[2].After == nil || rows[2].After.Count != 0 || rows[2].Changes.TotalMs == nil || !rows[2].Changes.TotalMs.Negative || rows[2].Changes.TotalMs.Magnitude != 10 {
		t.Fatalf("available source missing group = %+v", rows[2])
	}
}
