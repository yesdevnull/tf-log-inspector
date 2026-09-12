package tui

import (
	"math"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestTypeTotalsRetainSelectedDurationLowerBounds(t *testing.T) {
	for _, source := range []span.DurationSource{span.SourceUIElapsed, span.SourceRefreshWindow, span.SourceCLIElapsed} {
		m := New(&model.Log{UISpans: []span.Span{
			{Address: "aws_instance.large", ResourceType: "aws_instance", DurationMs: math.MaxUint32, DurationSaturated: true, DurationSource: source},
			{Address: "aws_instance.exact", ResourceType: "aws_instance", DurationMs: 1000, DurationSource: source},
		}}, "bounds.log")
		m.setView(ViewTypes)
		rows := m.rows()
		if !strings.HasPrefix(rows[0].cells[2], "≥") {
			t.Errorf("%s type total lacks lower bound: %v", source, rows[0].cells)
		}
		if !strings.Contains(detailBody(t, m, "GROUP DETAIL", 80, 30), "≥") {
			t.Errorf("%s detail total lacks lower bound", source)
		}
		m.resourceSelection.Addresses = map[string]bool{"aws_instance.exact": true}
		m.invalidateRows()
		if got := m.rows()[0].cells[2]; got != "1.0s" {
			t.Errorf("%s filtered exact total = %q", source, got)
		}
	}
}

func TestTimelineTitleDescribesAllSelectedSources(t *testing.T) {
	for _, sources := range [][]span.DurationSource{
		{span.SourceUIElapsed, span.SourceRefreshWindow},
		{span.SourceRefreshWindow, span.SourceUIElapsed},
		{span.SourceCLIElapsed, span.SourceRefreshWindow},
	} {
		l := &model.Log{}
		for i, source := range sources {
			l.UISpans = append(l.UISpans, span.Span{Address: []string{"aws_instance.a", "aws_instance.b"}[i], DurationSource: source})
		}
		m := New(l, "mixed.log")
		if got := m.timelineTitle(); got != "TIMELINE (resource, mixed sources)" {
			t.Errorf("sources %v title = %q", sources, got)
		}
		m.resourceSelection.Addresses = map[string]bool{"aws_instance.b": true}
		m.invalidateRows()
		if got := m.timelineTitle(); strings.Contains(got, "mixed") {
			t.Errorf("filtered title retained excluded source: %s", got)
		}
	}
}

func TestResourceDurationSourcesQualifyDetailsAndAggregates(t *testing.T) {
	m := newOperationTestModel()
	m.log.UISpans = m.log.UISpans[:3]
	m.log.UISpans[1].DurationSource = span.SourceRefreshWindow
	m.log.UISpans[1].StartEntry, m.log.UISpans[1].HasStartEntry = 0, true
	m.log.UISpans[2].DurationSource = span.SourceCLIElapsed
	m.log.UISpans[2].Entry = 2
	m.log.UISpans[2].DurationMs = 1250
	m = New(m.log, "synthetic.log")
	m.setView(ViewResources)
	for i, want := range []string{"ui_elapsed", "refresh_window", "cli_elapsed"} {
		detail := strings.Join(m.operationDetailSections(i, 160)[0], "\n")
		if !strings.Contains(detail, want) {
			t.Errorf("operation %d lost duration source %s:\n%s", i, want, detail)
		}
		if i != 0 && strings.Contains(detail, "rounded") {
			t.Errorf("operation %d inherited UI rounding:\n%s", i, detail)
		}
		if i == 1 && (!strings.Contains(detail, "lines 1-2") || !strings.Contains(detail, "timestamp")) {
			t.Errorf("refresh lost endpoints or qualification:\n%s", detail)
		}
		if i == 2 && !strings.Contains(detail, "1.25s") {
			t.Errorf("CLI duration lost displayed precision: %s", detail)
		}
	}
	evidence := m.resourceEvidenceText()
	for _, want := range []string{"ui_elapsed: 1", "refresh_window: 1", "cli_elapsed: 1"} {
		if !strings.Contains(evidence, want) {
			t.Errorf("source breakdown missing %q:\n%s", want, evidence)
		}
	}
	if got := strings.Join(typesPreamble(m.log.UISpans), "\n"); !strings.Contains(got, "refresh_window") || strings.Contains(got, "UI-hook figures are sums of measurements rounded") {
		t.Errorf("types qualify mixed durations incorrectly: %s", got)
	}
	q := model.BuildCaptureQuality(model.CaptureQualityInput{UISpans: m.log.UISpans})
	if got := m.qualityText(q, model.ReconstructionQuality{}); !strings.Contains(got, "cli_elapsed: 1") {
		t.Errorf("capture quality lost source counts: %s", got)
	}
}

func TestRefreshTimelineNamesActionAndSource(t *testing.T) {
	m := newOperationTestModel()
	m.log.UISpans = m.log.UISpans[:1]
	s := &m.log.UISpans[0]
	s.Fidelity, s.DurationSource = span.FidelityUIReported, span.SourceRefreshWindow
	s.RPC = "refresh"
	m = New(m.log, "synthetic.log")
	if title := m.timelineTitle(); strings.Contains(title, "whole seconds") || !strings.Contains(title, "refresh") {
		t.Errorf("refresh timeline title = %s", title)
	}
	detail := strings.Join(spanDetailLines(*s, attrib.Attribution{}, false, 100), "\n")
	if !strings.Contains(detail, "action") || !strings.Contains(detail, "refresh_window") || strings.Contains(detail, "RPC") {
		t.Errorf("resource timeline detail mislabels action or source:\n%s", detail)
	}
}

func TestCLIResourceSourceOpensOwnLineWithoutTimelineLanes(t *testing.T) {
	m := newOperationTestModel()
	m.log.UISpans = m.log.UISpans[1:2]
	m.log.UISpans[0].Fidelity = span.FidelityUIReported
	m.log.UISpans[0].DurationSource = span.SourceCLIElapsed
	m = New(m.log, "synthetic.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.openResourceOperations()
	if detail := strings.Join(m.operationDetailSections(0, 100)[0], "\n"); !strings.Contains(detail, "cli_elapsed") || !strings.Contains(detail, "position unavailable") {
		t.Errorf("CLI qualification missing: %s", detail)
	}
	_, timing := m.timelineSpans()
	if len(timing) != 0 {
		t.Fatal("CLI duration invented timeline positions")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || !strings.Contains(unstyled(strings.Join(m.rawLogLines(10), "\n")), "two") {
		t.Fatal("CLI operation did not open its source line")
	}
}

func TestResourceRPCEvidenceUnavailableWithoutObservations(t *testing.T) {
	m := New(testLog(t, "resource-cli.log"), "resource-cli.log")
	rows := m.resourceRows()
	if len(rows) == 0 {
		t.Fatal("missing CLI resource rows")
	}
	if rows[0].cells[6] != "n/a" || rows[0].cells[8] != "n/a" {
		t.Errorf("absent RPC evidence fabricated measured zero: %v", rows[0].cells)
	}
	detail := strings.Join(resourceDetailSections(rows[0].resource, 100)[0], "\n")
	if strings.Contains(detail, "inferred") {
		t.Errorf("empty inferred RPC sections were not collapsed: %s", detail)
	}
}
