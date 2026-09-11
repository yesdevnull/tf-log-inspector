package tui

import (
	"os"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/scrub"
)

func TestResponseRecoveryJourneyPreservesRawInvestigationAndStrictScrubbing(t *testing.T) {
	const path = "../../testdata/response-recovery.log"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, path)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	responseKey(&m, "i")
	if got := l.ReconstructionQuality(); got.State != "not_checked" {
		t.Fatalf("drawing quality initiated reconstruction: %+v", got)
	}
	if got := m.renderQuality(100, 200); !strings.Contains(got, "not checked (response reconstruction is lazy)") {
		t.Fatalf("initial quality omitted lazy state:\n%s", got)
	}
	responseKey(&m, "i")

	responseKey(&m, "6")
	if m.view != ViewRawLog || m.pane != PaneList {
		t.Fatalf("raw selection = view %v, pane %v", m.view, m.pane)
	}
	responseKey(&m, "/")
	responseKey(&m, "recovered response")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.raw.match == nil || m.raw.top != 2 || m.raw.topLine != 0 {
		t.Fatalf("raw search position = top %d, line %d, match %+v", m.raw.top, m.raw.topLine, m.raw.match)
	}
	beforeHistory := []navigationFrame{m.captureNavigation()}
	m.history = append(m.history, m.captureNavigation())
	beforeTop, beforeLine, beforeColumn := m.raw.top, m.raw.topLine, m.raw.column
	beforeMatch := *m.raw.match
	beforeQuery, beforeLastQuery := m.raw.query, m.raw.lastQuery
	beforeRaw := m.renderRawLog(100, 10)
	if reversedText(beforeRaw) != "recovered response" {
		t.Fatalf("raw occurrence is not highlighted before opening response: %q", beforeRaw)
	}

	responseKey(&m, "r")
	if got := m.renderResponse(100, 10); !strings.Contains(got, "Partial reconstruction") || !strings.Contains(got, "Decoded @message:") || !strings.Contains(got, "recovered response") {
		t.Fatalf("recovered response view:\n%s", got)
	}
	responseKey(&m, "/")
	responseKey(&m, "needle third")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.renderResponse(100, 10); m.response.match == nil || !strings.Contains(unstyled(got), "needle third") || reversedText(got) != "needle third" {
		t.Fatalf("decoded response search did not select needle third: match %+v\n%s", m.response.match, m.renderResponse(100, 10))
	}
	responseKey(&m, "n")
	responseKey(&m, "n")
	if got := m.renderResponse(100, 10); reversedText(got) != "" || !m.response.notFound {
		t.Fatalf("failed response search retained styling: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.response.open || m.raw.top != beforeTop || m.raw.topLine != beforeLine || m.raw.column != beforeColumn || m.raw.match == nil || *m.raw.match != beforeMatch || m.raw.query != beforeQuery || m.raw.lastQuery != beforeLastQuery || !reflect.DeepEqual(m.history, beforeHistory) {
		t.Fatal("response return changed the exact raw occurrence or navigation history")
	}
	if got := m.renderRawLog(100, 10); got != beforeRaw || reversedText(got) != "recovered response" {
		t.Fatalf("response return did not restore the raw highlight: %q", got)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	responseKey(&m, "r")
	if got := m.renderResponse(100, 10); !strings.Contains(got, "This response is incomplete or invalid.") || !strings.Contains(got, "Source line 2.") || strings.Contains(got, `"broken"`) {
		t.Fatalf("invalid response status:\n%s", got)
	}
	responseKey(&m, "r")

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	responseKey(&m, "r")
	if got := m.renderResponse(100, 10); !strings.Contains(got, "This stream is unavailable after an earlier failure.") || !strings.Contains(got, "Source line 4.") || strings.Contains(got, "apparent_restart") {
		t.Fatalf("unavailable response status:\n%s", got)
	}
	responseKey(&m, "r")

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	responseKey(&m, "r")
	if got := m.renderResponse(100, 10); !strings.Contains(got, "No reconstructed response at this physical line.") || !strings.Contains(got, "Source line 5.") || strings.Contains(got, "ordinary entry") {
		t.Fatalf("ordinary entry response status:\n%s", got)
	}
	responseKey(&m, "r")

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	responseKey(&m, "r")
	if got := m.renderResponse(100, 10); !strings.Contains(got, "No reconstructed response at this physical line.") || !strings.Contains(got, "Source line 6.") || strings.Contains(got, "ordinary continuation") {
		t.Fatalf("ordinary continuation response status:\n%s", got)
	}
	responseKey(&m, "r")

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	responseKey(&m, "r")
	if got := m.renderResponse(100, 10); !strings.Contains(got, "This response is incomplete or invalid.") || !strings.Contains(got, "Source line 7.") || strings.Contains(got, "unfinished") {
		t.Fatalf("incomplete response status:\n%s", got)
	}
	responseKey(&m, "r")

	responseKey(&m, "i")
	wantQuality := model.ReconstructionQuality{State: "partial", Responses: 2, Diagnostics: 2, Code: "reconstruction_partial"}
	if got := l.ReconstructionQuality(); got != wantQuality {
		t.Fatalf("inspected quality = %+v, want %+v", got, wantQuality)
	}
	if got := m.renderQuality(100, 200); !strings.Contains(got, "partial: 2 responses available; 2 reconstruction diagnostics") || !strings.Contains(got, "Diagnostic counts can include ownership triggers and aborted messages.") {
		t.Fatalf("partial quality omitted exact counts or qualification:\n%s", got)
	}
	responseKey(&m, "i")

	selections := []struct {
		entry      uint32
		lineOffset int
		state      string
		sourceLine uint64
		body       string
	}{
		{0, 0, "complete", 1, "first response"},
		{1, 0, "invalid", 2, ""},
		{2, 0, "complete", 3, "recovered response"},
		{3, 0, "unavailable", 4, ""},
		{4, 0, "none", 5, ""},
		{4, 1, "none", 6, ""},
		{5, 0, "invalid", 7, ""},
	}
	for _, want := range selections {
		got := l.ProviderResponseAt(want.entry, want.lineOffset)
		if got.State != want.state || got.SourceLine != want.sourceLine || !got.HasDiagnostics || (want.body != "" && !strings.Contains(got.Response.Text, want.body)) {
			t.Errorf("selection (%d, %d) = state %q, source line %d, body %q, diagnostics %v; want state %q, source line %d, body containing %q, diagnostics true", want.entry, want.lineOffset, got.State, got.SourceLine, got.Response.Text, got.HasDiagnostics, want.state, want.sourceLine, want.body)
		}
	}

	result, err := scrub.Scrub(source, nil)
	if err == nil {
		t.Fatal("scrubbing inspected partial recovery succeeded")
	}
	if !reflect.DeepEqual(result, scrub.Result{}) {
		t.Fatalf("scrubbing inspected partial recovery returned output: %+v", result)
	}
}
