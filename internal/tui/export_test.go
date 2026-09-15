package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestEventPanelExportsCurrentFilteredSelection(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "selected.json")
	log := &model.Log{Events: []model.ResourceEvent{
		{Kind: model.EventDiagnostic, Severity: "warning", Message: "selected", Source: "cli", Location: model.SourceLocation{StartLine: 4, EndLine: 5}},
		{Kind: model.EventDiagnostic, Severity: "error", Message: "hidden", Source: "cli", Location: model.SourceLocation{StartLine: 9, EndLine: 9}},
	}}
	m := NewWithVersion(log, "input.log", "v9.8.7")
	m.resourceSelection = model.ResourceSelection{Addresses: map[string]bool{"aws_instance.example": true}, Modules: map[string]bool{"module.app": true}, ExactModules: map[model.ResourceModule]bool{{Path: "module.app", Known: true}: true}, Sources: map[string]bool{"cli_elapsed": true}, Actions: map[string]bool{"create": true}}
	m.openEventPanel("d")
	m.events.severity = "warning"
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(testKey("x"))
	m.Update(testKey("j"))
	for _, r := range destination {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		ToolVersion string `json:"tool_version"`
		Scope       struct {
			Panel, Severity string
			SelectedSource  *struct {
				StartLine uint64 `json:"start_line"`
			} `json:"selected_source"`
		}
		Events      []struct{ Message string }
		TimingScope string `json:"timing_scope"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Scope.Panel != "diagnostics" || document.Scope.Severity != "warning" || document.Scope.SelectedSource == nil || document.Scope.SelectedSource.StartLine != 4 || len(document.Events) != 1 || document.Events[0].Message != "selected" {
		t.Fatalf("exported wrong selection: %#v", document)
	}
	if document.ToolVersion != "v9.8.7" {
		t.Errorf("tool version = %q", document.ToolVersion)
	}
	for _, want := range []string{"address=aws_instance.example", "module subtree=module.app", "exact module=module.app", "duration source=cli_elapsed", "lifecycle action=create"} {
		if !strings.Contains(document.TimingScope, want) {
			t.Errorf("timing scope %q missing %q", document.TimingScope, want)
		}
	}
	if !strings.Contains(m.events.export.notice, "Exported") {
		t.Fatalf("success feedback = %q", m.events.export.notice)
	}
	if !strings.Contains(unstyled(m.View()), "Exported selected.json") {
		t.Fatalf("success feedback not visible:\n%s", unstyled(m.View()))
	}
}

func TestProgressExportMatchesFilteredPanelEvidence(t *testing.T) {
	m := New(eventCapture(t, "aws_instance.a: Creating...\naws_instance.a: Still creating... [10s elapsed]\naws_instance.a: Still creating... [20s elapsed]\n"), "events.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.openEventPanel("v")
	typeEventQuery(&m, "10s elapsed")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if frame := unstyled(m.View()); !strings.Contains(frame, "10s elapsed") || strings.Contains(frame, "20s elapsed") || !strings.Contains(frame, "1/3 matching evidence") {
		t.Fatalf("expanded group does not match query:\n%s", frame)
	}
	report := m.currentInvestigationReport()
	if report.Scope.Query != "10s elapsed" || len(report.Events) != 1 || report.Events[0].Location.StartLine != 2 {
		t.Fatalf("export does not match filtered panel: %+v", report)
	}
	m.Update(testKey("/"))
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if frame := unstyled(m.View()); !strings.Contains(frame, "10s elapsed") || !strings.Contains(frame, "20s elapsed") || !strings.Contains(frame, "3/3 matching evidence") {
		t.Fatalf("clearing query did not restore observations:\n%s", frame)
	}
	report = m.currentInvestigationReport()
	if report.Scope.Query != "" || len(report.Events) != 3 {
		t.Fatalf("clearing query did not restore exported evidence: %+v", report)
	}
}

func TestEventPanelExportCancelsAndRefusesExistingDestination(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "existing.md")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(&model.Log{}, "input.log")
	m.openEventPanel("v")
	m.Update(testKey("x"))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.events.export.open {
		t.Fatal("cancel left prompt open")
	}
	m.Update(testKey("x"))
	m.Update(testKey("m"))
	for _, r := range destination {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" || !strings.Contains(m.events.export.notice, "exists") {
		t.Fatalf("existing destination changed or missing feedback: %q, %q", data, m.events.export.notice)
	}
	if !strings.Contains(unstyled(m.View()), "already exists") {
		t.Fatalf("error feedback not visible:\n%s", unstyled(m.View()))
	}
}

func TestEventPanelFooterKeepsWholeEssentialHintsAtSixtyColumns(t *testing.T) {
	m := New(&model.Log{}, "input.log")
	m.openEventPanel("v")
	footer := m.footer(60)
	if !strings.Contains(footer, "Esc close") || !strings.Contains(footer, "? help") || !strings.Contains(footer, "q quit") || strings.Contains(footer, " r ") {
		t.Fatalf("unsafe clipped footer:\n%s", footer)
	}
}

func TestEventExportHintsFollowModalInput(t *testing.T) {
	m := New(&model.Log{}, "input.log")
	m.openEventPanel("v")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.Update(testKey("x"))
	for _, want := range []string{"m Markdown", "j JSON", "Esc cancel"} {
		if frame := unstyled(m.View()); !strings.Contains(frame, want) {
			t.Errorf("format prompt missing %q:\n%s", want, frame)
		}
	}
	for _, hint := range []string{"x export", "/ search", "f kind", "s severity", "Space expand", "↑↓ select", "Esc close", "? help", "q quit"} {
		if frame := unstyled(m.View()); strings.Contains(frame, hint) {
			t.Errorf("export format prompt advertises inactive %q:\n%s", hint, frame)
		}
	}
	m.Update(testKey("j"))
	m.Update(testKey("x/f?sq"))
	if got := m.events.export.input.Value(); got != "x/f?sq" {
		t.Fatalf("destination keys were treated as panel commands: %q", got)
	}
	for _, want := range []string{"Enter export", "Esc cancel"} {
		if frame := unstyled(m.View()); !strings.Contains(frame, want) {
			t.Errorf("destination prompt missing %q:\n%s", want, frame)
		}
	}
	for _, hint := range []string{"m Markdown", "j JSON", "x export", "/ search", "? help", "q quit"} {
		if frame := unstyled(m.View()); strings.Contains(frame, hint) {
			t.Errorf("destination prompt advertises inactive %q:\n%s", hint, frame)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if frame := unstyled(m.View()); !strings.Contains(frame, "x export") || !strings.Contains(frame, "Esc close") {
		t.Fatalf("cancel did not restore panel controls:\n%s", frame)
	}
}

func TestNarrowEventPanelHelpShowsOmittedActionsAndRestoresState(t *testing.T) {
	m := New(&model.Log{Events: []model.ResourceEvent{{Kind: model.EventStart, Address: "aws_instance.a", Location: model.SourceLocation{StartLine: 4}}}}, "input.log")
	m.openEventPanel("v")
	m.events.query = "instance"
	m.events.kind = model.EventStart
	m.events.selected = 0
	m.events.expanded["progress-group:2"] = true
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 50})
	before := m.events
	m.Update(testKey("?"))
	frame := unstyled(m.View())
	if !m.showHelp || !m.events.open || !strings.Contains(frame, "Keys") || !strings.Contains(frame, "Enter r") {
		t.Fatalf("event-panel help did not expose omitted actions:\n%s", frame)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.showHelp || !m.events.open || m.events.query != before.query || m.events.kind != before.kind || m.events.selected != before.selected || !m.events.expanded["progress-group:2"] {
		t.Fatalf("closing help did not restore event panel state: before=%+v after=%+v", before, m.events)
	}
}

func testKey(value string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)} }

func TestInvestigationExportDescribesEffectiveTimingFacets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions map[string]bool
		want    string
		count   int
	}{
		{"facet", nil, "lifecycle action=create", 1},
		{"intersection", map[string]bool{"create": true, "delete": true}, "lifecycle action=create", 1},
		{"empty intersection", map[string]bool{"delete": true}, "lifecycle action=(none)", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(eventCapture(t, "aws_instance.a: Creation complete after 1s\naws_instance.b: Destruction complete after 2s\n"), "events.log")
			m.excludedFacets = map[string]map[string]bool{dimAction: {"delete": true}, dimSource: {"refresh_window": true}}
			m.resourceSelection.Actions = tc.actions
			m.openEventPanel("v")
			report := m.currentInvestigationReport()
			if len(report.Timings) != tc.count || !strings.Contains(report.TimingScope, tc.want) || !strings.Contains(report.TimingScope, "duration source=cli_elapsed") {
				t.Fatalf("timings=%d scope=%q", len(report.Timings), report.TimingScope)
			}
		})
	}
}

func TestEventExportPreservesInvestigationPosition(t *testing.T) {
	for _, finish := range []string{"cancel", "export"} {
		t.Run(finish, func(t *testing.T) {
			var capture strings.Builder
			for i := range 20 {
				fmt.Fprintf(&capture, "aws_instance.r%d: Creating...\n", i)
			}
			m := New(eventCapture(t, capture.String()), "events.log")
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			m.openEventPanel("v")
			for range 10 {
				m.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			m.View()
			selected, offset := m.events.selected, m.events.viewport.YOffset
			_, actions := m.eventPanelContent(m.events.viewport.Width)
			selectedRow := actions[selected].start - offset
			m.Update(testKey("x"))
			m.View()
			if finish == "cancel" {
				m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			} else {
				m.Update(testKey("j"))
				m.Update(testKey(filepath.Join(t.TempDir(), "report.json")))
				m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			}
			m.View()
			_, actions = m.eventPanelContent(m.events.viewport.Width)
			if m.events.selected != selected || actions[selected].start-m.events.viewport.YOffset != selectedRow {
				t.Fatalf("selected event or screen row changed: selection=%d offset=%d", m.events.selected, m.events.viewport.YOffset)
			}
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if m.raw.topLine != 10 {
				t.Fatalf("source jump = %d, want 10", m.raw.topLine)
			}
		})
	}
}

func TestEventExportInterruptsBothStages(t *testing.T) {
	for _, format := range []string{"", "j"} {
		t.Run("format="+format, func(t *testing.T) {
			m := New(&model.Log{}, "events.log")
			m.openEventPanel("v")
			m.Update(testKey("x"))
			if format != "" {
				m.Update(testKey(format))
			}
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			if !m.quitting || cmd == nil {
				t.Fatal("Ctrl+C did not quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("missing quit message")
			}
		})
	}
}

func TestInvestigationInputsShowCursorAndLongTail(t *testing.T) {
	for _, mode := range []string{"search", "destination"} {
		t.Run(mode, func(t *testing.T) {
			m := New(&model.Log{}, "events.log")
			m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
			m.openEventPanel("v")
			if mode == "search" {
				m.Update(testKey("/"))
			} else {
				m.Update(testKey("x"))
				m.Update(testKey("j"))
			}
			m.Update(testKey(strings.Repeat("abc", 100) + "TAIL.json"))
			before := m.View()
			if !strings.Contains(unstyled(before), "TAIL.json") {
				t.Error("input tail is not visible")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyHome})
			if before == m.View() {
				t.Error("Home moves an invisible caret")
			}
			m.Update(testKey("START"))
			if !strings.Contains(unstyled(m.View()), "START") {
				t.Error("insertion at caret is not visible")
			}
		})
	}
}
