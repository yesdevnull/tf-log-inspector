package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func qualityModel(t *testing.T, name string) *Model {
	t.Helper()
	const capture = `2026-09-10T00:00:00.000Z [TRACE] provider.terraform-provider-aws_v5.0.0_x5: Received downstream response: tf_req_id=one tf_resource_type=aws_instance tf_rpc=ReadResource tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=broken
2026-09-10T00:00:01.000Z [TRACE] provider.terraform-provider-aws_v5.0.0_x5: Received downstream response: tf_req_id=two tf_resource_type=aws_subnet tf_rpc=ReadResource tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=10
{"@level":"info","@message":"aws_instance.open: Creating...","@module":"terraform.ui","@timestamp":"2026-09-10T00:00:02Z","hook":{"resource":{"addr":"aws_instance.open","module":"","resource":"aws_instance.open","implied_provider":"aws","resource_type":"aws_instance","resource_name":"open","resource_key":null},"action":"create"},"type":"apply_start"}
`
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(capture), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, path)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m.setView(ViewRawLog)
	m.pane = PaneList
	return &m
}

func qualityKey(m *Model, key string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func TestQualityPanelPreservesTheRawInvestigationAndShowsWholeLogFacts(t *testing.T) {
	m := qualityModel(t, "capture.log")
	m.raw.lastQuery = "provider"
	if !m.searchFrom(0, true, true) {
		t.Fatal("fixture has no searchable provider occurrence")
	}
	wholeBeforeFilter := m.qualityText(m.log.CaptureQuality(), m.log.ReconstructionQuality())
	m.raw.scope = []int{1}
	m.excludedFacets = map[string]map[string]bool{"resource type": {"aws_instance": true, "aws_subnet": true}}
	if wholeAfterFilter := m.qualityText(m.log.CaptureQuality(), m.log.ReconstructionQuality()); wholeAfterFilter != wholeBeforeFilter {
		t.Fatal("active filter changed whole-log quality facts")
	}
	before := m.renderRawLog(60, 6)
	beforeTop, beforeLine, beforeColumn, beforeMatch := m.raw.top, m.raw.topLine, m.raw.column, *m.raw.match

	qualityKey(m, "i")
	frame := ansi.Strip(m.View())
	for _, want := range []string{"CAPTURE QUALITY (whole log)", "admitted 1, rejected 1", "Esc/i close", "q quit"} {
		if !strings.Contains(frame, want) {
			t.Errorf("quality panel missing %q:\n%s", want, frame)
		}
	}
	whole := ansi.Strip(m.renderQuality(60, 200))
	for _, want := range []string{"context_incomplete", "line 3", "RECONSTRUCTION", "not checked"} {
		if !strings.Contains(whole, want) {
			t.Errorf("quality content missing %q:\n%s", want, whole)
		}
	}
	initial := m.renderQuality(60, 4)
	qualityKey(m, "j")
	if got := m.renderQuality(60, 4); got == initial {
		t.Fatal("quality panel did not scroll")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	if got := m.renderQuality(30, 2); got == "" {
		t.Fatal("narrow quality panel rendered no content")
	}
	for _, key := range []string{"1", "f", "s", "\\", "/", "n", " "} {
		qualityKey(m, key)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewRawLog || m.pane != PaneList || m.raw.top != beforeTop || m.raw.topLine != beforeLine || m.raw.column != beforeColumn || *m.raw.match != beforeMatch || m.renderRawLog(60, 6) != before || m.raw.scope == nil || !m.filterActive() {
		t.Fatal("quality panel changed the underlying investigation")
	}
	qualityKey(m, "n")
	if m.raw.match == nil || m.raw.match.entry <= beforeMatch.entry {
		t.Fatal("raw repeat did not continue from the prior occurrence")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.filterActive() {
		t.Fatal("the next Escape did not retain the underlying clear-filter behaviour")
	}
}

func TestQualityLocatedAnomalyJumpsAndReturnsToExactSelection(t *testing.T) {
	m := qualityModel(t, "capture.log")
	m.raw.lastQuery = "provider"
	if !m.searchFrom(1, true, true) {
		t.Fatal("fixture has no raw match")
	}
	wantRaw := cloneRawState(m.raw)
	m.openQuality()
	m.renderQuality(40, 5)
	qualityKey(m, "down")
	wantQuality := m.captureQualityNavigation()
	if wantQuality.selected.kind != "anomaly" {
		t.Fatalf("selected = %+v", wantQuality.selected)
	}
	qualityKey(m, "enter")
	if m.view != ViewRawLog || m.quality.open || len(m.history) != 1 {
		t.Fatalf("jump state = view %v quality %v history %d", m.view, m.quality.open, len(m.history))
	}
	rows := m.rawLogRows(1)
	if len(rows) != 1 || rows[0].sourceLine != 3 {
		t.Fatalf("jump source = %+v, want line 3", rows)
	}
	m.raw.query = "child"
	m.raw.lastQuery = "changed"
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.quality.open || m.quality.selected != wantQuality.selected {
		t.Fatalf("quality navigation = %+v, want %+v", m.captureQualityNavigation(), wantQuality)
	}
	if m.raw.query != wantRaw.query || m.raw.lastQuery != wantRaw.lastQuery || m.raw.match == nil || *m.raw.match != *wantRaw.match {
		t.Fatal("raw parent search was not restored")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.quality.open || len(m.history) != 0 {
		t.Fatal("closing restored quality spent or retained history")
	}
}

func TestQualityPagingKeepsTallOverlappingActionSelected(t *testing.T) {
	m := qualityModel(t, "capture.log")
	m.openQuality()
	records := []qualityRecord{{id: qualityItemID{kind: "check"}, text: strings.Repeat("wrapped ", 30)}, {text: "prose"}}
	lines, actions := wrapQualityRecords(records, 12)
	m.quality.viewport = viewport.New(12, 3)
	m.quality.viewport.MouseWheelEnabled = false
	m.quality.viewport.SetContent(strings.Join(lines, "\n"))
	m.quality.selected = actions[0].id
	m.quality.viewport.PageDown()
	offset := m.quality.viewport.YOffset
	m.selectVisibleQualityAction(actions, true)
	if m.quality.selected != actions[0].id {
		t.Fatalf("overlapping action selection = %+v", m.quality.selected)
	}
	m.revealQualityAction(actions[0])
	if m.quality.viewport.YOffset != offset {
		t.Fatalf("tall action snapped from offset %d to %d", offset, m.quality.viewport.YOffset)
	}
}

func TestQualityArrowUpRevealsTallActionFirstRow(t *testing.T) {
	m := qualityModel(t, "capture.log")
	tall := qualityItemID{kind: "diagnostic", index: 1}
	next := qualityItemID{kind: "diagnostic", index: 2}
	actions := []qualityActionRow{{id: tall, start: 0, end: 7}, {id: next, start: 7, end: 8}}
	m.quality.viewport = viewport.New(20, 3)
	m.quality.viewport.SetContent(strings.Repeat("row\n", 8))
	m.quality.viewport.SetYOffset(5)
	m.quality.selected = next
	m.moveQualitySelection(actions, false)
	if m.quality.selected != tall || m.quality.viewport.YOffset != 0 {
		t.Fatalf("Up selected/offset = %+v/%d, want tall/0", m.quality.selected, m.quality.viewport.YOffset)
	}
}

func TestQualityProseOnlyPageClearsSelectionAndRecoversByDirection(t *testing.T) {
	records := []qualityRecord{
		{id: qualityItemID{kind: "check"}, text: "before"},
		{text: strings.Repeat("prose ", 24)},
		{id: qualityItemID{kind: "diagnostic", index: 1}, text: "after", sourceLine: 9},
	}
	lines, actions := wrapQualityRecords(records, 12)
	if len(actions) != 2 || actions[1].start-actions[0].end < 3 {
		t.Fatal("fixture has no prose-only page")
	}
	m := qualityModel(t, "capture.log")
	m.quality.open = true
	m.quality.viewport = viewport.New(12, 3)
	m.quality.viewport.SetContent(strings.Join(lines, "\n"))
	proseOffset := actions[0].end
	m.quality.viewport.SetYOffset(proseOffset)
	m.selectVisibleQualityAction(actions, true)
	if m.quality.selected != (qualityItemID{}) {
		t.Fatalf("prose page selection = %+v", m.quality.selected)
	}
	view, depth := m.view, len(m.history)
	m.handleQualityKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != view || len(m.history) != depth {
		t.Fatal("Enter acted on prose-only page")
	}
	m.moveQualitySelection(actions, true)
	if m.quality.selected != actions[1].id {
		t.Fatalf("Down recovered %+v, want next", m.quality.selected)
	}
	m.quality.selected = qualityItemID{}
	m.quality.viewport.SetYOffset(proseOffset)
	m.moveQualitySelection(actions, false)
	if m.quality.selected != actions[0].id {
		t.Fatalf("Up recovered %+v, want previous", m.quality.selected)
	}
}

func TestQualityPublicationPrecedesInspectionMessage(t *testing.T) {
	m := responseModel(t, `{"message":"available"}`)
	m.openQuality()
	_, cmd := m.handleQualityKey(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	if m.log.ReconstructionQuality().State != "complete" {
		t.Fatal("real command did not publish")
	}
	text := m.qualityText(m.log.CaptureQuality(), m.log.ReconstructionQuality())
	if strings.Contains(text, "checking responses") || !strings.Contains(text, "complete: 1 responses available") {
		t.Fatalf("published rendering remained transient:\n%s", text)
	}
	m.Update(msg)
}

func TestQualityUnlocatedActionDoesNotChangeInvestigationOrHistory(t *testing.T) {
	m := qualityModel(t, "capture.log")
	m.openQuality()
	view, pane, depth := m.view, m.pane, len(m.history)
	m.activateQualityAction(qualityActionRow{id: qualityItemID{kind: "anomaly", stage: "scan", code: "unlocated"}})
	if m.view != view || m.pane != pane || len(m.history) != depth || !m.quality.open {
		t.Fatal("unlocated Enter changed investigation")
	}
}

func TestQualityComposedWorkbenchAtSupportedWidthsAndShortHeight(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 60, Height: 8}, {Width: 100, Height: 12}, {Width: 160, Height: 24}, {Width: 60, Height: 4}} {
		m := qualityModel(t, "capture.log")
		m.Update(size)
		m.openQuality()
		frame := unstyled(m.workbenchView())
		wants := []string{"Esc/i close", "↑↓ select", "Enter open/check", "q quit"}
		if size.Height > 4 {
			wants = append(wants, "Check responses")
		}
		for _, want := range wants {
			if !strings.Contains(frame, want) {
				t.Errorf("%dx%d frame missing %q:\n%s", size.Width, size.Height, want, frame)
			}
		}
	}
}

func TestQualityModalPrecedenceAndQuitKeys(t *testing.T) {
	m := qualityModel(t, "capture.log")
	m.raw.searching = true
	m.raw.input = newSearchInput()
	qualityKey(m, "i")
	if m.raw.input.Value() != "i" || m.quality.open {
		t.Fatal("i did not remain search input")
	}
	m.raw.searching = false
	qualityKey(m, "?")
	qualityKey(m, "i")
	if !m.showHelp || m.quality.open {
		t.Fatal("i escaped through help")
	}
	qualityKey(m, "?")
	m.response.open = true
	m.response.searching = true
	m.response.input = newSearchInput()
	qualityKey(m, "i")
	if m.response.input.Value() != "i" || m.quality.open {
		t.Fatal("i did not remain response search input")
	}
	m.response.searching = false
	qualityKey(m, "i")
	if !m.response.open || m.quality.open {
		t.Fatal("i escaped through an open response")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'q'}}, {Type: tea.KeyCtrlC}} {
		fresh := qualityModel(t, "capture.log")
		qualityKey(fresh, "i")
		fresh.Update(key)
		if !fresh.quitting {
			t.Errorf("%q did not quit from quality", key.String())
		}
	}
}

func TestQualityIndicatorAndRenderingBoundaries(t *testing.T) {
	m := qualityModel(t, "unsafe\x1b[2J界.log")
	if got := m.qualityIndicator(); got != "i limitations" {
		t.Fatalf("indicator = %q", got)
	}
	qualityKey(m, "i")
	if m.quality.viewport.MouseWheelEnabled {
		t.Fatal("quality viewport enabled mouse-wheel navigation")
	}
	if got := m.renderQuality(0, 0); got != "" {
		t.Fatalf("zero-sized quality render = %q", got)
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 0, Height: 0}, {Width: 1, Height: 1}, {Width: 20, Height: 4}, {Width: 60, Height: 12}, {Width: 100, Height: 24}} {
		m.Update(size)
		frame := m.View()
		if strings.Contains(frame, "\x1b[2J") {
			t.Fatal("filename control reached the terminal")
		}
	}
	text := ansi.Strip(m.renderQuality(100, 200))
	for _, want := range []string{
		"ATTRIBUTION", "contained", "likely", "overlapping", "ambiguous", "unattributed",
		"CONTEXT LIMITATIONS", "Malformed hclog headers", "@level", "@timestamp", "independent RPC records",
		`unsafe\x1b[2J界.log, line 3`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("quality content missing %q:\n%s", want, text)
		}
	}
	clean := New(testLog(t, "structured-ui.log"), "clean.log")
	if got := clean.qualityIndicator(); got != "i quality" {
		t.Errorf("clean indicator = %q", got)
	}
	clean.openQuality()
	if got := clean.log.ReconstructionQuality().State; got != "not_checked" {
		t.Fatalf("opening quality triggered reconstruction: %q", got)
	}
}

func TestQualityReportsLazyReconstructionWithoutTriggeringIt(t *testing.T) {
	m := responseModel(t, `{"message":"available"}`)
	qualityKey(m, "i")
	if got := m.log.ReconstructionQuality().State; got != "not_checked" {
		t.Fatalf("opening quality triggered reconstruction: %q", got)
	}
	if !strings.Contains(m.renderQuality(100, 200), "not checked") {
		t.Fatal("quality did not report untouched reconstruction state")
	}
	qualityKey(m, "i")
	responseKeyAndDrain(t, m, "r")
	if got := m.log.ReconstructionQuality().State; got != "complete" {
		t.Fatalf("viewing response left reconstruction state %q", got)
	}
	responseKeyAndDrain(t, m, "r")
	qualityKey(m, "i")
	if got := m.renderQuality(100, 200); !strings.Contains(got, "complete: 1 responses available") {
		t.Fatalf("quality did not report completed reconstruction:\n%s", got)
	}
}

func TestQualityReportsFailedReconstruction(t *testing.T) {
	m := responseModel(t, `{"secret":`)
	responseKeyAndDrain(t, m, "r")
	if got := m.log.ReconstructionQuality().State; got != "failed" {
		t.Fatalf("malformed response left reconstruction state %q", got)
	}
	responseKeyAndDrain(t, m, "r")
	qualityKey(m, "i")
	if got := m.renderQuality(100, 200); !strings.Contains(got, "failed: 0 responses available; 1 reconstruction diagnostics") || !strings.Contains(got, "Diagnostic counts can include ownership triggers and aborted messages.") {
		t.Fatalf("quality did not report reconstruction failure:\n%s", got)
	}
}

func TestQualityReportsPartialReconstructionAndFlagsItAsALimitation(t *testing.T) {
	reconstruction := model.ReconstructionQuality{State: "partial", Responses: 2, Diagnostics: 1, Code: "reconstruction_partial"}
	m := New(&model.Log{}, "capture.log")
	completeCapture := model.CaptureQuality{HasContext: true}
	text := m.qualityText(completeCapture, reconstruction)
	for _, want := range []string{
		"partial: 2 responses available; 1 reconstruction diagnostics",
		"Diagnostic counts can include ownership triggers and aborted messages.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("quality text missing %q:\n%s", want, text)
		}
	}
	if qualityHasLimitations(completeCapture, model.ReconstructionQuality{State: "complete"}) {
		t.Fatal("fixture has an unrelated capture limitation")
	}
	if !qualityHasLimitations(completeCapture, reconstruction) {
		t.Fatal("partial reconstruction was not flagged as a limitation")
	}
	if !qualityHasLimitations(completeCapture, model.ReconstructionQuality{State: "failed", Diagnostics: 1, Code: "reconstruction_failed"}) {
		t.Fatal("failed reconstruction was not flagged as a limitation")
	}
}

func TestQualityAnomalyFallbackAndAttributionUnitsAreVisible(t *testing.T) {
	first := uint32(99)
	m := New(&model.Log{}, "capture.log")
	q := model.CaptureQuality{
		HasContext: true,
		Issues:     []model.QualityIssue{{Stage: "rpc_duration", Code: "duration_invalid", Count: 1, FirstEntry: &first}},
	}
	q.Attribution.Spans = 3
	q.Attribution.ByConfidence[attrib.Overlapping] = 2
	q.Attribution.MsByConfidence[attrib.Overlapping] = 17
	text := m.qualityText(q, model.ReconstructionQuality{State: "not_checked"})
	for _, want := range []string{"location unavailable", "overlapping  2 spans, 17ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("quality text missing %q:\n%s", want, text)
		}
	}
}

func TestQualityTimingShowsPositionedAndExcludedDurations(t *testing.T) {
	m := New(&model.Log{}, "capture.log")
	q := model.CaptureQuality{RPC: model.TierQuality{
		Records: 3, Admitted: 2, Rejected: 1, Positioned: 1,
		DurationMs: 30, PositionedMs: 10, ExcludedMs: 20,
	}}
	text := m.qualityText(q, model.ReconstructionQuality{State: "not_checked"})
	for _, want := range []string{"duration 30ms", "positioned 1 / 10ms", "excluded 1 / 20ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("quality timing missing %q:\n%s", want, text)
		}
	}
}

func TestQualityNoContextRetainsTheAdmittedRPCDurationDenominator(t *testing.T) {
	m := New(&model.Log{}, "capture.log")
	q := model.CaptureQuality{RPCDurationMs: 30}
	text := m.qualityText(q, model.ReconstructionQuality{State: "not_checked"})
	if !strings.Contains(text, "nameable duration: unavailable / 30ms (no address context)") {
		t.Fatalf("no-context attribution hid or fabricated the denominator:\n%s", text)
	}

	q.RPCDurationMs = 0
	text = m.qualityText(q, model.ReconstructionQuality{State: "not_checked"})
	if !strings.Contains(text, "nameable duration: unavailable / 0ms (no address context)") {
		t.Fatalf("zero-duration no-context state is not distinct:\n%s", text)
	}
}

func TestLikelyOnlyAttributionIsALimitation(t *testing.T) {
	q := model.BuildCaptureQuality(model.CaptureQualityInput{
		RPCSpans:     []span.Span{{DurationMs: 10, EndMs: 10, TimestampStatus: logfmt.TimestampValid}},
		Contexts:     []attrib.Context{{}},
		Attributions: []attrib.Attribution{{Confidence: attrib.Likely}},
	})
	if q.Attribution.ByConfidence[attrib.Likely] != 1 || q.RPC.Admitted != q.RPC.Positioned || len(q.Issues) != 0 {
		t.Fatalf("fixture did not isolate Likely-only attribution: %+v", q)
	}
	if !qualityHasLimitations(q, model.ReconstructionQuality{State: "not_checked"}) {
		t.Fatal("Likely-only inferred attribution was labelled without limitations")
	}
}

func TestQualityExplainsSourceSamplesAndAnomalyCountUnits(t *testing.T) {
	m := qualityModel(t, "capture.log")
	q := m.log.CaptureQuality()
	q.Issues = append(q.Issues, model.QualityIssue{Stage: "scan", Code: "line_count_saturated", Count: 1})
	text := m.qualityText(q, m.log.ReconstructionQuality())
	for _, want := range []string{
		"First samples use original one-based physical source lines",
		"Counts can overlap across stages and must not be totalled as bad lines",
		"recognised RPC response records rejected at admission",
		"structured envelope and context events",
		"timestamp issues count timestamped hclog entries; line_count_saturated counts excess physical continuation lines after an entry line counter reaches its cap",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("quality explanation missing %q:\n%s", want, text)
		}
	}
}

func TestQualityIndicatorSurvivesBeforeTheFilenameAtSupportedWidths(t *testing.T) {
	m := qualityModel(t, strings.Repeat("long-name-", 20)+".log")
	for _, w := range []int{60, 70, 100, 160} {
		m.width, m.height = w, 24
		head := ansi.Strip(strings.Split(m.View(), "\n")[0])
		if !strings.Contains(head, "i limitations") {
			t.Errorf("width %d lost quality indicator before filename: %q", w, head)
		}
	}
}
