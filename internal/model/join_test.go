package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func uiSp(resourceType string, durationMs uint32) span.Span {
	return span.Span{
		ResourceType: resourceType,
		RPC:          "read",
		DurationMs:   durationMs,
		Fidelity:     span.FidelityUIReported,
	}
}

// The join is what answers "this type cost 247s overall, and here is what its
// RPCs cost" -- the two tiers measure overlapping work at different
// granularities, so both columns must survive on one row.
func TestJoinByResourceTypeCombinesBothTiers(t *testing.T) {
	rpc := []span.Span{
		sp("azuread_service_principal", "azuread", "ReadDataSource", 40000),
		sp("azuread_service_principal", "azuread", "ReadDataSource", 30000),
	}
	ui := []span.Span{
		uiSp("azuread_service_principal", 64000),
		uiSp("azuread_service_principal", 63000),
	}
	got := JoinByResourceType(rpc, ui)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.UIResources != 2 || r.UITotalMs != 127000 || r.UIMaxMs != 64000 {
		t.Errorf("UI columns wrong: %+v", r)
	}
	if r.RPCCalls != 2 || r.RPCTotalMs != 70000 || r.RPCMaxMs != 40000 {
		t.Errorf("RPC columns wrong: %+v", r)
	}
}

// A TF_LOG=TRACE capture has no terraform.ui stream at all, and a plain debug
// capture has no protocol lines. Both are real, measured captures, so a row
// present in only one tier must still appear with the other side zeroed.
func TestJoinByResourceTypeKeepsSingleTierRows(t *testing.T) {
	got := JoinByResourceType(
		[]span.Span{sp("aws_instance", "aws", "ApplyResourceChange", 5000)},
		[]span.Span{uiSp("local_file", 1000)},
	)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	byType := map[string]TypeRow{}
	for _, r := range got {
		byType[r.ResourceType] = r
	}
	if r := byType["aws_instance"]; r.RPCTotalMs != 5000 || r.UIResources != 0 {
		t.Errorf("RPC-only row wrong: %+v", r)
	}
	if r := byType["local_file"]; r.UITotalMs != 1000 || r.RPCCalls != 0 {
		t.Errorf("UI-only row wrong: %+v", r)
	}
}

// Ordering leads with the UI-hook total because that is the closest thing to
// "how long did this type actually take", and falls back to RPC time so a
// capture with no terraform.ui stream still ranks sensibly rather than
// collapsing to an arbitrary order.
func TestJoinByResourceTypeOrdersByUIThenRPC(t *testing.T) {
	got := JoinByResourceType(
		[]span.Span{
			sp("big_rpc", "p", "r", 90000),
			sp("small_rpc", "p", "r", 10000),
		},
		[]span.Span{uiSp("has_ui", 5000)},
	)
	if got[0].ResourceType != "has_ui" {
		t.Errorf("first row = %q, want has_ui: a UI total outranks any RPC total", got[0].ResourceType)
	}
	if got[1].ResourceType != "big_rpc" {
		t.Errorf("second row = %q, want big_rpc", got[1].ResourceType)
	}
}

func TestJoinByResourceTypeEmptyInput(t *testing.T) {
	if got := JoinByResourceType(nil, nil); len(got) != 0 {
		t.Errorf("got %d rows from no spans, want 0", len(got))
	}
}
