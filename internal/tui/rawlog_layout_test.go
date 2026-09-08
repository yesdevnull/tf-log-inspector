package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestRawLogUsesTheDetailPanesSpace(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "capture.log")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.pane != PaneDetail {
		t.Fatal("test must enter Raw Log from the detail pane")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6")})
	if m.pane == PaneDetail {
		t.Error("switching to Raw Log left focus on the hidden detail pane")
	}
	for _, w := range []int{60, 70, 99, 100, 160} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		frame := unstyled(m.View())
		if strings.Contains(frame, "Detail") || strings.Contains(frame, "(nothing selected)") {
			t.Errorf("width %d still shows detail:\n%s", w, frame)
		}
		rule := strings.Split(frame, "\n")[3]
		wantSeparators := 0
		if w >= 100 {
			wantSeparators = 1
		}
		if lipgloss.Width(rule) != w || strings.Count(rule, "╮ ╭") != wantSeparators {
			t.Errorf("width %d does not allocate the whole row to visible panes: %q", w, rule)
		}
		for i := 0; i < 4; i++ {
			m.Update(tea.KeyMsg{Type: tea.KeyTab})
			if m.pane == PaneDetail {
				t.Errorf("width %d lets Tab focus the hidden detail pane", w)
			}
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if !strings.Contains(unstyled(m.View()), "Span detail") {
		t.Fatal("returning to Calls did not restore the detail pane")
	}
}
