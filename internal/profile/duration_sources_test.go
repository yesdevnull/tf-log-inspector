package profile

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestTextProfileQualifiesResourceDurationSources(t *testing.T) {
	for _, source := range []span.DurationSource{span.SourceRefreshWindow, span.SourceCLIElapsed} {
		l := &model.Log{UISpans: []span.Span{{Address: "aws_instance.a", ResourceType: "aws_instance", RPC: "refresh", DurationMs: 1250, Fidelity: span.FidelityUIReported, DurationSource: source}}}
		var out strings.Builder
		if err := Render(&out, l, TextOptions{}); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if !strings.Contains(text, source.String()) || !strings.Contains(text, "1.25s") || strings.Contains(text, "whole seconds") {
			t.Errorf("%s report lost duration provenance:\n%s", source, text)
		}
	}
}
