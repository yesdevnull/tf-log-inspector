package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func sp(resourceType, provider, rpc string, durationMs uint32) span.Span {
	return span.Span{
		ResourceType: resourceType,
		Provider:     provider,
		RPC:          rpc,
		DurationMs:   durationMs,
		Fidelity:     span.FidelityReported,
	}
}

func TestRollupBySumsCountsAndMaxima(t *testing.T) {
	spans := []span.Span{
		sp("azuread_service_principal", "azuread", "ReadDataSource", 1000),
		sp("azuread_service_principal", "azuread", "ReadDataSource", 3000),
		sp("azurerm_key_vault", "azurerm", "ReadDataSource", 500),
	}
	got := RollupBy(spans, func(s span.Span) string { return s.ResourceType })
	if len(got) != 2 {
		t.Fatalf("got %d buckets, want 2: %+v", len(got), got)
	}
	if got[0].Key != "azuread_service_principal" {
		t.Errorf("first bucket = %q, want the largest total first", got[0].Key)
	}
	if got[0].TotalMs != 4000 {
		t.Errorf("TotalMs = %d, want 4000", got[0].TotalMs)
	}
	if got[0].MaxMs != 3000 {
		t.Errorf("MaxMs = %d, want 3000", got[0].MaxMs)
	}
	if got[0].Count != 2 {
		t.Errorf("Count = %d, want 2", got[0].Count)
	}
}

// Ordering must be total, or two runs over the same log disagree and any
// golden-file test downstream flakes.
func TestRollupByBreaksTiesByKey(t *testing.T) {
	spans := []span.Span{
		sp("zebra", "p", "r", 1000),
		sp("alpha", "p", "r", 1000),
		sp("mike", "p", "r", 1000),
	}
	got := RollupBy(spans, func(s span.Span) string { return s.ResourceType })
	want := []string{"alpha", "mike", "zebra"}
	for i, w := range want {
		if got[i].Key != w {
			t.Errorf("bucket %d = %q, want %q (equal totals must sort by key)", i, got[i].Key, w)
		}
	}
}

// A span with no value for the chosen key still holds real time, and dropping
// it would make the totals quietly disagree with the summed span time.
func TestRollupByKeepsEmptyKeysUnderAnExplicitLabel(t *testing.T) {
	spans := []span.Span{
		sp("aws_instance", "aws", "r", 1000),
		sp("", "aws", "r", 250),
	}
	got := RollupBy(spans, func(s span.Span) string { return s.ResourceType })
	var total uint64
	var found bool
	for _, b := range got {
		total += b.TotalMs
		if b.Key == "(none)" {
			found = true
		}
	}
	if !found {
		t.Errorf("empty key not labelled (none): %+v", got)
	}
	if total != 1250 {
		t.Errorf("summed TotalMs = %d, want 1250 -- no span may be dropped", total)
	}
}

func TestRollupByEmptyInput(t *testing.T) {
	if got := RollupBy(nil, func(s span.Span) string { return s.RPC }); len(got) != 0 {
		t.Errorf("got %d buckets from no spans, want 0", len(got))
	}
}
