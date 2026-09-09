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
	m.raw.top, m.raw.column = 1, 7
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
	if m.view != ViewRawLog || m.pane != PaneList || m.raw.top != 1 || m.raw.column != 7 || m.renderRawLog(100, 8) != before {
		t.Fatal("modal changed underlying position")
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
