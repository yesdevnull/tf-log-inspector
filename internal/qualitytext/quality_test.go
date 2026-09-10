package qualitytext

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestWriteCaptureQualityRendersValueFreeSummary(t *testing.T) {
	first := uint32(7)
	share := 0.25
	q := model.CaptureQuality{
		RPC: model.TierQuality{
			Records: 3, Admitted: 2, Rejected: 1, Positioned: 1,
			DurationMs: 40, PositionedMs: 10, ExcludedMs: 30,
			DurationLowerBound: true,
		},
		UI:            model.TierQuality{},
		HasContext:    true,
		NameableMs:    10,
		RPCDurationMs: 40,
		NameableShare: &share,
		Issues: []model.QualityIssue{
			{Stage: "context", Code: "context_incomplete", Count: 1, FirstEntry: &first},
			{Stage: "rpc_duration", Code: "duration_invalid", Count: 1},
		},
	}

	var out strings.Builder
	WriteCaptureQuality(&out, q)
	text := out.String()
	for _, want := range []string{
		"CAPTURE QUALITY (whole log)",
		"RPC timing records     3: admitted 2, rejected 1; duration 40ms (lower bound)",
		"excluded 1 observations, 30ms (lower bound)",
		"nameable duration      10ms / 40ms (25.0%)",
		"does not establish that an operation failed",
		"context_incomplete       1 (first entry 7)",
		"duration_invalid         1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
}
