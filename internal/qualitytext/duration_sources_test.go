package qualitytext

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestCaptureQualityShowsAdmittedDurationSources(t *testing.T) {
	q := model.BuildCaptureQuality(model.CaptureQualityInput{UISpans: []span.Span{{DurationSource: span.SourceRefreshWindow, DurationMs: 25}, {DurationSource: span.SourceCLIElapsed, DurationMs: 1000}}})
	var b strings.Builder
	WriteCaptureQuality(&b, q)
	for _, want := range []string{"refresh_window: 1 operation, 25ms", "cli_elapsed: 1 operation, 1s"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q: %s", want, b.String())
		}
	}
}
