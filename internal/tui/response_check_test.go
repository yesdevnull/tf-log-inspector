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
	newJourney := func(t *testing.T) (*Model, tea.Cmd, tea.Cmd) {
		t.Helper()
		source := responseRecoveryHead + `a: {"message":"old body"}` + "\n" +
			responseRecoveryHead + `b: {"message":"new body"}` + "\n"
		m := loadedResponseModel(t, source)
		_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		_, oldSelection := m.Update(inspect())
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m.raw.top = 1
		_, newSelection := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if oldSelection == nil || newSelection == nil || !m.response.pending || !m.response.resolving {
			t.Fatal("fixture did not produce an eligible newer request")
		}
		return &m, oldSelection, newSelection
	}

	t.Run("old request while newer request is eligible", func(t *testing.T) {
		m, oldSelection, newSelection := newJourney(t)
		m.Update(oldSelection())
		if got := m.renderResponse(100, 20); !m.response.pending || !strings.Contains(got, "Preparing response") || strings.Contains(got, "old body") {
			t.Fatalf("old request changed eligible newer response: %q", got)
		}
		m.Update(newSelection())
		newBody := m.renderResponse(100, 20)
		if !strings.Contains(newBody, "new body") || strings.Contains(newBody, "old body") {
			t.Fatalf("new request did not publish distinct body: %q", newBody)
		}
		m.Update(oldSelection())
		if got := m.renderResponse(100, 20); got != newBody {
			t.Fatal("old result replaced already-published newer response")
		}
	})

	t.Run("foreign capture while request is eligible", func(t *testing.T) {
		m, _, newSelection := newJourney(t)
		foreign := newSelection().(responseReadyMsg)
		foreign.log = responseModel(t, `{"message":"foreign"}`).log
		m.Update(foreign)
		if got := m.renderResponse(100, 20); !m.response.pending || !strings.Contains(got, "Preparing response") || strings.Contains(got, "new body") {
			t.Fatalf("foreign capture changed eligible response: %q", got)
		}
	})

	t.Run("completion after quit while request is eligible", func(t *testing.T) {
		m, _, newSelection := newJourney(t)
		m.quitting = true
		m.Update(newSelection())
		if got := m.renderResponse(100, 20); !m.response.pending || !strings.Contains(got, "Preparing response") || strings.Contains(got, "new body") {
			t.Fatalf("completion after quit changed eligible response: %q", got)
		}
	})
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
	assertPendingGuidance := func(phase string) {
		t.Helper()
		for _, frame := range []string{unstyled(m.View()), unstyled(m.workbenchView())} {
			for _, absent := range []string{"search", "next/previous", "scroll", "PgUp"} {
				if strings.Contains(frame, absent) {
					t.Fatalf("%s frame advertised %q:\n%s", phase, absent, frame)
				}
			}
			for _, want := range []string{"Esc/r back", "q quit"} {
				if !strings.Contains(frame, want) {
					t.Fatalf("%s frame omitted %q:\n%s", phase, want, frame)
				}
			}
		}
	}
	assertPendingGuidance("inspection")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.response.searching {
		t.Fatal("pending response accepted search")
	}
	_, selection := m.Update(inspect())
	if selection == nil || !strings.Contains(unstyled(m.View()), "Preparing response") {
		t.Fatal("selection preparation was not visible")
	}
	assertPendingGuidance("preparation")
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m.Update(selection())
	if frame := unstyled(m.View()); !strings.Contains(frame, "search") || !strings.Contains(frame, "scroll") {
		t.Fatalf("completed response controls did not return:\n%s", frame)
	}
}

func TestResponseCheckKeepsTerminalResponsiveWhileInspectionRuns(t *testing.T) {
	t.Run("quality check joins repeated activation and resolves response without reopening modal", func(t *testing.T) {
		m := responseModel(t, `{"message":"available"}`)
		m.openQuality()
		_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		_, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if inspect == nil || duplicate != nil {
			t.Fatal("quality activation did not schedule exactly one inspection")
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
		_, joined := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if joined != nil || !m.response.open {
			t.Fatal("response did not join quality inspection")
		}
		_, present := m.Update(inspect())
		if present == nil {
			t.Fatal("inspection did not resolve active response")
		}
		m.Update(present())
		if m.quality.open || !m.response.open || !strings.Contains(unstyled(m.View()), "available") {
			t.Fatal("completion reopened wrong modal or lost response")
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
		if !strings.Contains(unstyled(m.renderQuality(100, 200)), "complete: 1 responses available") {
			t.Fatal("quality did not reuse cached result")
		}
	})

	t.Run("close and reopen retains the real deferred request", func(t *testing.T) {
		m := responseModel(t, `{"message":"available"}`)
		_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		_, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if duplicate != nil || !m.response.open || !m.response.pending {
			t.Fatal("reopen did not join the in-flight inspection")
		}
		_, present := m.Update(inspect())
		if present == nil {
			t.Fatal("real inspection did not resolve reopened request")
		}
		m.Update(present())
		if frame := unstyled(m.View()); !strings.Contains(frame, "available") {
			t.Fatalf("reopened response was not presented:\n%s", frame)
		}
	})

	t.Run("mixed recovery renders quality and closes before completion delivery", func(t *testing.T) {
		source := responseRecoveryHead + `a: {"message":"recovered"}` + "\n" +
			responseRecoveryHead + `b: {"broken":]}` + "\n"
		m := loadedResponseModel(t, source)
		_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		done := make(chan tea.Msg, 1)
		go func() { done <- inspect() }()

		m.Update(tea.WindowSizeMsg{Width: 60, Height: 8})
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if m.response.open {
			t.Fatal("close was not responsive before completion delivery")
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
		if frame := unstyled(m.View()); !strings.Contains(frame, "CAPTURE QUALITY") {
			t.Fatalf("quality did not render while completion was outstanding:\n%s", frame)
		}
		m.Update(<-done)
		if got := m.log.ReconstructionQuality(); got.State != "partial" || got.Responses != 1 || got.Diagnostics != 1 {
			t.Fatalf("mixed inspection outcome = %+v", got)
		}
		if frame := unstyled(m.renderQuality(60, 200)); !strings.Contains(frame, "partial: 1 responses available; 1 reconstruction") || !strings.Contains(frame, "diagnostics") {
			t.Fatalf("published partial quality did not render:\n%s", frame)
		}
	})

	t.Run("malformed recovery renders quality and quits before completion delivery", func(t *testing.T) {
		m := responseModel(t, `{"secret":`)
		_, inspect := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		done := make(chan tea.Msg, 1)
		go func() { done <- inspect() }()

		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
		if frame := unstyled(m.View()); !strings.Contains(frame, "CAPTURE QUALITY") {
			t.Fatalf("quality did not render while malformed completion was outstanding:\n%s", frame)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		if !m.quitting {
			t.Fatal("quit was not responsive before completion delivery")
		}
		m.Update(<-done)
		if got := m.log.ReconstructionQuality(); got.State != "failed" || got.Responses != 0 || got.Diagnostics != 1 {
			t.Fatalf("malformed inspection outcome = %+v", got)
		}
	})
}
