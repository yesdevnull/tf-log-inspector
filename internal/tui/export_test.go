package tui

import (
	"encoding/json"
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
	footer := eventPanelFooter(60)
	if !strings.Contains(footer, "Esc close") || !strings.Contains(footer, "? help") || !strings.Contains(footer, "q quit") || strings.Contains(footer, " r ") {
		t.Fatalf("unsafe clipped footer:\n%s", footer)
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
