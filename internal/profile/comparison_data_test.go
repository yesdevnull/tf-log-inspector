package profile

import (
	"reflect"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func comparisonReportFixture(t *testing.T, name string) Report {
	t.Helper()
	log, err := model.Load("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Build(log)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestBuildComparisonKeepsCaptureEvidence(t *testing.T) {
	log, err := model.Load("../../testdata/provider-rpc.log")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Build(log)
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildComparison(report, report)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Data.Sections) != 5 || len(got.Data.Sections[0].Rows) != 1 {
		t.Fatalf("sections: %+v", got.Data.Sections)
	}
	row := got.Data.Sections[0].Rows[0]
	if row.Before == nil || row.Before.Count != 2 || row.Before.TotalMs != 6 {
		t.Fatalf("before: %+v", row.Before)
	}
	if row.After == nil || row.After.Count != 2 || row.After.TotalMs != 6 {
		t.Fatalf("after: %+v", row.After)
	}
	if got.Before.Reconstruction.State != "not_checked" || log.ReconstructionQuality().State != "not_checked" {
		t.Fatal("comparison reconstructed responses")
	}
}

func TestBuildComparisonIncludesEverySectionAndUnavailableSides(t *testing.T) {
	before := comparisonReportFixture(t, "structured-ui.log")
	after := comparisonReportFixture(t, "core-only.log")
	got, err := BuildComparison(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Data.Sections) != 5 {
		t.Fatalf("section count = %d", len(got.Data.Sections))
	}
	for _, section := range got.Data.Sections {
		if section.Kind == "" || section.Tier == "" {
			t.Fatalf("incomplete section: %+v", section)
		}
		if section.Tier == "rpc" && section.BeforeAvailable {
			t.Fatalf("structured UI unexpectedly has RPC evidence: %+v", section)
		}
		if section.Tier == "ui" && section.AfterAvailable {
			t.Fatalf("core-only unexpectedly has UI evidence: %+v", section)
		}
		for _, row := range section.Rows {
			if section.BeforeAvailable && row.Before == nil || !section.BeforeAvailable && row.Before != nil {
				t.Fatalf("before availability mismatch: %+v", row)
			}
			if section.AfterAvailable && row.After == nil || !section.AfterAvailable && row.After != nil {
				t.Fatalf("after availability mismatch: %+v", row)
			}
		}
	}
}

func TestBuildComparisonPreservesLowerBoundsAndAdmittedInvalidPositions(t *testing.T) {
	report := comparisonReportFixture(t, "resources-long-lower-bound.log")
	got, err := BuildComparison(report, report)
	if err != nil {
		t.Fatal(err)
	}
	foundLowerBound := false
	for _, section := range got.Data.Sections {
		for _, row := range section.Rows {
			foundLowerBound = foundLowerBound || row.Before != nil && row.Before.LowerBound
			if row.Before != nil && row.Before.LowerBound && row.Changes.TotalMs != nil {
				t.Fatalf("lower-bound total change should be unavailable: %+v", row)
			}
		}
	}
	if !foundLowerBound {
		t.Fatal("lower-bound evidence was lost")
	}
	invalid := Report{RPC: []Observation{{Span: span.Span{Provider: "p", ResourceType: "r", RPC: "read", DurationMs: 4, TimestampStatus: logfmt.TimestampMissing}}}}
	invalidComparison, err := BuildComparison(invalid, invalid)
	if err != nil {
		t.Fatal(err)
	}
	if got := invalidComparison.Data.Sections[0].Rows[0].Before.Count; got != 1 {
		t.Fatalf("invalid-position observation was discarded, count=%d", got)
	}
}

func TestBuildComparisonDoesNotMutateReports(t *testing.T) {
	before := comparisonReportFixture(t, "provider-rpc.log")
	after := comparisonReportFixture(t, "resources-long-lower-bound.log")
	beforeRPC, beforeUI := append([]Observation(nil), before.RPC...), append([]Observation(nil), before.UI...)
	afterRPC, afterUI := append([]Observation(nil), after.RPC...), append([]Observation(nil), after.UI...)
	beforeRPCRanking, beforeUIRanking := append([]int(nil), before.RPCRanking...), append([]int(nil), before.UIRanking...)
	afterRPCRanking, afterUIRanking := append([]int(nil), after.RPCRanking...), append([]int(nil), after.UIRanking...)
	if _, err := BuildComparison(before, after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.RPC, beforeRPC) || !reflect.DeepEqual(before.UI, beforeUI) || !reflect.DeepEqual(before.RPCRanking, beforeRPCRanking) || !reflect.DeepEqual(before.UIRanking, beforeUIRanking) ||
		!reflect.DeepEqual(after.RPC, afterRPC) || !reflect.DeepEqual(after.UI, afterUI) || !reflect.DeepEqual(after.RPCRanking, afterRPCRanking) || !reflect.DeepEqual(after.UIRanking, afterUIRanking) {
		t.Fatal("comparison mutated report evidence or rankings")
	}
}
