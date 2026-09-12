package tui

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourceCaptureSummarySurvivesLongFilename(t *testing.T) {
	m := New(testLog(t, "resource-cli.log"), strings.Repeat("long-name-", 20)+".log")
	m.width, m.height = 60, 24
	view := unstyled(m.View())
	for _, want := range []string{"CLI operations", "no RPC timings", "no timestamps"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	m.resourceSelection.Addresses = map[string]bool{"aws_instance.other": true}
	m.invalidateRows()
	if got := unstyled(m.View()); !strings.Contains(got, "2/3 CLI operations") {
		t.Errorf("summary lost selected/whole capture counts: %s", got)
	}
}

func TestNarrowTimingGuidanceWrapsAndKeepsSelectedRow(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{{Address: "aws_instance.a", DurationSource: span.SourceRefreshWindow}}}, "refresh.log")
	m.setView(ViewTypes)
	got := strings.Join(strings.Fields(unstyled(m.renderList(35, 20))), " ")
	if !strings.Contains(got, "timestamp-derived hook window, not RPC.") {
		t.Fatalf("narrow qualification clipped: %s", got)
	}
	m.setView(ViewResources)
	got = unstyled(m.renderResources(56, 3))
	if !strings.Contains(got, "e evidence") || !strings.Contains(got, "refresh") {
		t.Fatalf("short pane lost guidance or selected row: %s", got)
	}
}

func TestResourceTableShowsMixedSourcesAndWrapsGuidance(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{
		{Address: "aws_instance.a", DurationMs: 1000, DurationSource: span.SourceUIElapsed},
		{Address: "aws_instance.a", DurationMs: 2000, DurationSource: span.SourceRefreshWindow},
	}}, "mixed.log")
	view := unstyled(m.renderResources(56, 20))
	for _, want := range []string{"sources", "UI+refresh", "not RPC."} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.HasPrefix(line, "Scopes:") {
			t.Error("verbose scope guidance obscures resource rows")
		}
	}
}

func TestResourceDetailCollapsesOnlyEmptyRPCEvidence(t *testing.T) {
	r := &model.ResourceRow{Address: "aws_instance.a", UI: model.DurationTotal{Count: 1}}
	text := strings.Join(resourceDetailSections(r, 100)[0], "\n")
	if strings.Contains(text, "inferred") {
		t.Fatalf("empty RPC sections shown: %s", text)
	}
	r.NamedRPC.Count = 1
	text = strings.Join(resourceDetailSections(r, 100)[0], "\n")
	if !strings.Contains(text, "Contained/Likely RPCs: 1, 0s") || strings.Contains(text, "Overlapping RPCs") {
		t.Fatalf("measured zero or empty-section collapse lost: %s", text)
	}
}

func TestCallsExplainsResourceOnlyCapture(t *testing.T) {
	m := New(testLog(t, "resource-cli.log"), "cli.log")
	m.setView(ViewCalls)
	view := unstyled(m.renderList(35, 12))
	for _, want := range []string{"No RPC timings", "Resources", "TRACE"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q: %s", want, view)
		}
	}
}
