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

func responseModel(t *testing.T, body string) *Model {
	t.Helper()
	const head = "2026-01-01T00:00:00.000Z [DEBUG] provider.example: "
	path := filepath.Join(t.TempDir(), "synthetic.log")
	text := head + body[:len(body)/2] + "\n" + head + body[len(body)/2:] + "\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, path)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 15})
	m.setView(ViewRawLog)
	m.pane = PaneList
	return &m
}

func responseKey(m *Model, key string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func TestResponseNavigationAndSearchPreservesRawPosition(t *testing.T) {
	message := "HTTP/1.1 200 OK\n" + strings.Repeat("padding\n", 30) + "needle first\nneedle second\n" + strings.Repeat("tail\n", 20)
	body, _ := json.Marshal(map[string]string{"@message": message})
	m := responseModel(t, string(body))
	m.raw.lastQuery = "provider.example"
	if !m.searchFrom(0, true, true) {
		t.Fatal("raw search fixture has no first occurrence")
	}
	m.raw.topLine = 1
	beforeTop, beforeTopLine, beforeColumn := m.raw.top, m.raw.topLine, m.raw.column
	beforeMatch := *m.raw.match
	before := m.renderRawLog(100, 8)
	responseKey(m, "r")
	if !strings.Contains(m.centreTitle(), "RECONSTRUCTED RESPONSE") || !strings.Contains(m.centreTitle(), "2 fragments") {
		t.Fatalf("title = %q", m.centreTitle())
	}
	initial := m.renderCentre(100, 8)
	responseKey(m, "j")
	if m.renderCentre(100, 8) == initial {
		t.Fatal("response did not scroll")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.response.viewport.YOffset <= 1 {
		t.Fatal("page did not advance")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.renderCentre(100, 8), "needle first") {
		t.Fatal("search did not reveal decoded message")
	}
	responseKey(m, "n")
	if !strings.HasPrefix(m.renderCentre(100, 8), "needle second") {
		t.Fatal("next match not shown")
	}
	responseKey(m, "N")
	if !strings.HasPrefix(m.renderCentre(100, 8), "needle first") {
		t.Fatal("previous match not shown")
	}
	for _, key := range []string{"1", "f", "s", "\\", "tab"} {
		responseKey(m, key)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewRawLog || m.pane != PaneList || m.raw.top != beforeTop || m.raw.topLine != beforeTopLine || m.raw.column != beforeColumn || *m.raw.match != beforeMatch || m.renderRawLog(100, 8) != before {
		t.Fatal("modal changed underlying position")
	}
	responseKey(m, "n")
	if m.raw.match == nil || m.raw.match.entry <= beforeMatch.entry {
		t.Fatal("raw repeat did not continue from its prior occurrence")
	}
	responseKey(m, "r")
	if !strings.Contains(m.footer(60), "q quit") {
		t.Fatal("narrow response footer hides quit")
	}
	responseKey(m, "q")
	if !m.quitting {
		t.Fatal("modal blocked quit")
	}
}

func TestResponseDecodedControlsAndUnavailableBody(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"@message": "HTTP\nunsafe\x1b[2J\r\t\a\u009b text"})
	m := responseModel(t, string(body))
	responseKey(m, "r")
	out := m.renderCentre(200, 20)
	if !strings.Contains(out, "Decoded @message") || !strings.Contains(out, `unsafe\x1b[2J\r\t\a\u009b text`) {
		t.Fatalf("unsafe or unreadable decoded display: %q", out)
	}
	responseKey(m, "/")
	responseKey(m, `unsafe\x1b`)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.response.notFound {
		t.Fatal("search did not match visible escaped controls")
	}
	responseKey(m, "/")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Fatal("search blocked Ctrl+C")
	}
	for _, body := range []string{"ordinary message", `{"secret":`} {
		m = responseModel(t, body)
		responseKey(m, "r")
		out = m.renderCentre(100, 10)
		if !strings.Contains(out, "response") || strings.Contains(out, "secret") {
			t.Fatalf("unavailable message = %q", out)
		}
		responseKey(m, "r")
		if m.centreTitle() != "RAW LOG" {
			t.Fatal("r did not close")
		}
	}
}

func TestResponseSearchRevealsWideMatchAndAdvancesNearBottom(t *testing.T) {
	m := responseModel(t, `{"a":"`+strings.Repeat("x", 150)+`needle","b":"needle"}`)
	responseKey(m, "r")
	m.renderCentre(50, 10)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.renderCentre(50, 10), "needle") {
		t.Fatal("matched text remains horizontally hidden")
	}
	responseKey(m, "n")
	responseKey(m, "n")
	if !strings.Contains(m.footer(100), "pattern not found") {
		t.Fatal("search repeats a bottom match indefinitely")
	}
}

func TestResponseSearchVisitsOccurrencesOnOneLine(t *testing.T) {
	m := responseModel(t, `{"message":"`+strings.Repeat("x", 100)+`needle first needle second`+strings.Repeat("z", 100)+`"}`)
	responseKey(m, "r")
	m.renderCentre(12, 4)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.renderCentre(12, 4), "needle first") {
		t.Fatal("first occurrence is hidden")
	}
	responseKey(m, "n")
	if !strings.Contains(m.renderCentre(12, 4), "needle secon") {
		t.Fatal("next occurrence on the same line is hidden")
	}
	responseKey(m, "N")
	if !strings.Contains(m.renderCentre(12, 4), "needle first") {
		t.Fatal("previous occurrence on the same line is hidden")
	}
}

func TestResponseSearchMissAndEmptyInputPreserveOccurrence(t *testing.T) {
	m := responseModel(t, `{"message":"`+strings.Repeat("x", 100)+`needle first needle second`+strings.Repeat("z", 100)+`"}`)
	responseKey(m, "r")
	m.renderCentre(12, 4)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	first := m.renderCentre(12, 4)
	responseKey(m, "n")
	second := m.renderCentre(12, 4)
	responseKey(m, "n")
	if got := m.renderCentre(12, 4); got != second || !strings.Contains(m.footer(40), "pattern not found") {
		t.Fatalf("miss moved response or hid footer: %q", got)
	}
	responseKey(m, "N")
	if got := m.renderCentre(12, 4); got != first {
		t.Fatalf("miss discarded valid occurrence: %q", got)
	}
	responseKey(m, "/")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	responseKey(m, "n")
	if got := m.renderCentre(12, 4); got != second {
		t.Fatalf("empty query replaced prior search: %q", got)
	}
	responseKey(m, "/")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	responseKey(m, "N")
	if got := m.renderCentre(12, 4); got != first {
		t.Fatalf("cancelled query replaced prior search: %q", got)
	}
}

func TestResponseManualScrollingStartsRepeatFromVisiblePosition(t *testing.T) {
	body := `{"a":"` + strings.Repeat("x", 10) + `needle first needle second` + strings.Repeat("z", 300) + `"` + strings.Repeat(`,"tail":"padding"`, 30) + `}`
	m := responseModel(t, body)
	responseKey(m, "r")
	m.renderCentre(12, 4)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	responseKey(m, "l")
	responseKey(m, "n")
	if got := m.renderCentre(12, 4); !strings.Contains(got, "needle secon") {
		t.Fatalf("horizontal scroll reused the prior occurrence: %q", got)
	}

	m = responseModel(t, `{"a":"needle first","b":"needle second","wide":"`+strings.Repeat("z", 300)+`"`+strings.Repeat(`,"tail":"padding"`, 30)+`}`)
	responseKey(m, "r")
	m.renderCentre(20, 1)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	responseKey(m, "j")
	responseKey(m, "N")
	if got := m.renderCentre(20, 1); !strings.Contains(got, "needle second") {
		t.Fatalf("vertical scroll reused the prior occurrence: %q", got)
	}
}

func TestResponseResizeClampsViewWithoutDiscardingOccurrence(t *testing.T) {
	m := responseModel(t, `{"message":"`+strings.Repeat("x", 40)+`needle first `+strings.Repeat("y", 40)+`needle second"}`)
	responseKey(m, "r")
	m.renderCentre(12, 4)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.renderCentre(12, 4), "needle first") {
		t.Fatal("first occurrence is hidden")
	}
	m.renderCentre(200, 4)
	responseKey(m, "n")
	if got := m.renderCentre(12, 4); !strings.Contains(got, "needle secon") {
		t.Fatalf("resize discarded the successful occurrence: %q", got)
	}
}

func TestResponseSearchUsesDisplayColumnsAfterWideUnicode(t *testing.T) {
	m := responseModel(t, `{"message":"`+strings.Repeat("界", 30)+`needle first needle second"}`)
	responseKey(m, "r")
	m.renderCentre(14, 4)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.renderCentre(14, 4), "needle first") {
		t.Fatal("wide Unicode left the first occurrence hidden")
	}
	responseKey(m, "n")
	if !strings.Contains(m.renderCentre(14, 4), "needle second") {
		t.Fatal("wide Unicode left the next occurrence hidden")
	}
}

func TestResponseWorkbenchShowsModalGuidance(t *testing.T) {
	m := responseModel(t, `{"message":"synthetic"}`)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	responseKey(m, "r")
	out := unstyled(m.View())
	for _, hint := range []string{"Esc/r back", "scroll", "PgUp/PgDn", "q quit"} {
		if !strings.Contains(out, hint) {
			t.Errorf("modal frame missing %q", hint)
		}
	}
	if strings.Contains(out, "Entry 1/") {
		t.Fatal("response still displays raw entry status")
	}
}

func TestResponseDoesNotOpenEntriesOutsideRawFilterOrScope(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprint("empty scope=", scoped), func(t *testing.T) {
			m := responseModel(t, `{"message":"hidden-response"}`)
			if scoped {
				m.raw.scope = []int{}
			} else {
				m.excludedFacets = map[string]map[string]bool{dimLevel: {"DEBUG": true}}
			}
			m.invalidateRows()
			if out := m.renderRawLog(80, 10); strings.Contains(out, "hidden-response") {
				t.Fatalf("raw view did not hide entry: %q", out)
			}
			responseKey(m, "r")
			out := m.renderCentre(80, 10)
			if strings.Contains(out, "hidden-response") || !strings.Contains(out, "No reconstructed response") {
				t.Fatalf("empty raw pane opened response: %q", out)
			}
		})
	}
}
