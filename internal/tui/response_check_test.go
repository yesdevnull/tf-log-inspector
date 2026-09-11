package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func drainResponseCommands(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	for i := 0; cmd != nil; i++ {
		if i == 8 {
			t.Fatal("response command cycle")
		}
		_, cmd = m.Update(cmd())
	}
}

func responseKeyAndDrain(t *testing.T, m *Model, key string) {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	drainResponseCommands(t, m, cmd)
}

func TestResponseFirstRequestIsPendingBeforeCommandRuns(t *testing.T) {
	m := responseModel(t, `{"message":"verified"}`)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil || !m.response.open || !m.response.pending {
		t.Fatal("first response did not return a pending command")
	}
	if m.log.ReconstructionQuality().State != "not_checked" {
		t.Fatal("key handler performed reconstruction")
	}
	if !strings.Contains(unstyled(m.View()), "Checking responses") {
		t.Fatal("pending response not visible")
	}
	inspectionDone := cmd()
	_, resolve := m.Update(inspectionDone)
	if resolve == nil {
		t.Fatal("pending source was not resolved")
	}
	m.Update(resolve())
	if m.response.pending || !strings.Contains(m.renderResponse(100, 20), "verified") {
		t.Fatal("verified response was not published")
	}
}

func TestResponseCheckJoinsRunningInspectionAndCloseDoesNotReopen(t *testing.T) {
	m := responseModel(t, `{"message":"verified"}`)
	first := m.requestResponseCheck()
	if first == nil || m.requestResponseCheck() != nil {
		t.Fatal("running inspection scheduled more than one command")
	}
	m.response.open = true
	m.response.pending = true
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd := func() tea.Cmd { _, cmd := m.Update(first()); return cmd }(); cmd != nil {
		t.Fatal("closed response queued a selection")
	}
	if m.response.open {
		t.Fatal("inspection completion reopened response")
	}
}

func TestResponseCompletionRejectsStaleCaptureRequestAndQuit(t *testing.T) {
	m := responseModel(t, `{"message":"first"}`)
	_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	_, selectFirst := m.Update(inspect())
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.raw.topLine = 1
	_, selectSecond := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if selectSecond == nil {
		t.Fatal("checked capture did not queue a new selection")
	}
	m.Update(selectSecond())
	want := m.renderResponse(100, 20)
	m.Update(selectFirst())
	if got := m.renderResponse(100, 20); got != want {
		t.Fatal("old request replaced newer response")
	}

	other := responseModel(t, `{"message":"other"}`)
	m.Update(responseReadyMsg{log: other.log, requestID: m.response.request.id, presentation: responseState{lines: []string{"wrong capture"}}})
	if got := m.renderResponse(100, 20); got != want {
		t.Fatal("different capture replaced response")
	}
	m.quitting = true
	m.Update(responseReadyMsg{log: m.log, requestID: m.response.request.id, presentation: responseState{lines: []string{"after quit"}}})
	if got := m.renderResponse(100, 20); got != want {
		t.Fatal("completion changed response after quit")
	}
}

func TestResponseCompletionQueuesSelectionOnlyOnceAfterPublication(t *testing.T) {
	m := responseModel(t, `{"message":"published"}`)
	inspection := m.requestResponseCheck()
	done := inspection()
	_, selection := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if selection == nil || !m.response.resolving {
		t.Fatal("published cache did not queue response selection")
	}
	if _, duplicate := m.Update(done); duplicate != nil {
		t.Fatal("inspection completion queued duplicate selection")
	}
	m.Update(selection())
	if !strings.Contains(m.renderResponse(100, 20), "published") {
		t.Fatal("original selection was not delivered")
	}
}

func TestResponsePendingGuidanceAndKeys(t *testing.T) {
	m := responseModel(t, `{"message":"verified"}`)
	_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	for _, frame := range []string{unstyled(m.View()), unstyled(m.workbenchView())} {
		for _, absent := range []string{"search", "next/previous", "scroll", "PgUp"} {
			if strings.Contains(frame, absent) {
				t.Fatalf("pending frame advertised %q:\n%s", absent, frame)
			}
		}
		for _, want := range []string{"Esc/r back", "q quit"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("pending frame omitted %q:\n%s", want, frame)
			}
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.response.searching {
		t.Fatal("pending response accepted search")
	}
	_, selection := m.Update(inspect())
	if selection == nil || !strings.Contains(unstyled(m.View()), "Preparing response") {
		t.Fatal("selection preparation was not visible")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m.Update(selection())
	if frame := unstyled(m.View()); !strings.Contains(frame, "search") || !strings.Contains(frame, "scroll") {
		t.Fatalf("completed response controls did not return:\n%s", frame)
	}
}

func TestResponseCheckKeepsTerminalResponsiveWhileInspectionRuns(t *testing.T) {
	m := responseModel(t, `{"message":"verified"}`)
	_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	done := make(chan tea.Msg, 1)
	go func() { done <- inspect() }()

	if frame := unstyled(m.View()); !strings.Contains(frame, "Checking responses") {
		t.Fatalf("inspection was not visible while command ran:\n%s", frame)
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 8})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.response.open {
		t.Fatal("close was not responsive while inspection ran")
	}
	_, joined := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	_, completed := m.Update(<-done)
	if joined != nil && completed != nil {
		t.Fatal("publication and completion queued duplicate selections")
	}
	if joined == nil {
		joined = completed
	}
	if joined == nil {
		t.Fatal("reopened response did not queue a selection")
	}
	if !m.response.resolving {
		t.Fatal("reopened response did not begin resolving")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !m.quitting {
		t.Fatal("quit was not responsive while response was resolving")
	}
}
