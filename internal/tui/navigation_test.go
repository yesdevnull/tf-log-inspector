package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestNavigationShowsTheActiveViewAfterSwitching(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "plan.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	for _, key := range []string{"1", "2", "4", "5", "6"} {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		line := strings.Split(m.footer(100), "\n")[0]
		if !strings.Contains(unstyled(line), key+" ") || !strings.Contains(line, "\x1b[7m") {
			t.Errorf("view %s lacks an active tab: %q", key, line)
		}
		for _, other := range []string{"1 providers", "2 types", "4 calls", "5 timeline", "6 raw log", "? help"} {
			if !strings.Contains(unstyled(line), other) {
				t.Errorf("navigation omits %q: %q", other, line)
			}
		}
	}
}

func TestCompactNavigationKeepsActiveViewAndHelp(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "plan.log")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6")})
	for _, w := range []int{24, 40, 60} {
		line := strings.Split(m.footer(w), "\n")[0]
		if lipgloss.Width(line) > w || !strings.Contains(unstyled(line), "6 ") || !strings.Contains(unstyled(line), "? help") {
			t.Errorf("width %d loses navigation or overflows: %q", w, line)
		}
	}
}

func TestCompactActionHelpKeepsQuitWhole(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "plan.log")
	for _, w := range []int{24, 40, 60} {
		line := strings.Split(m.footer(w), "\n")[1]
		if lipgloss.Width(line) > w || !strings.HasSuffix(unstyled(line), "q quit") {
			t.Errorf("width %d clips the quit action: %q", w, line)
		}
	}
}

func TestPaneFocusHasATextMarker(t *testing.T) {
	for _, focused := range []bool{false, true} {
		line := titledRule(pane{title: "CALLS", width: 30, focused: focused})
		if strings.Contains(unstyled(line), "▶") != focused || lipgloss.Width(line) != 30 {
			t.Errorf("focus %v has incorrect marker or width: %q", focused, line)
		}
	}
}

func TestFocusMarkerFollowsTheKeyboardAcrossPanes(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "plan.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	for i := 0; i < 3; i++ {
		out := unstyled(m.View())
		if strings.Count(out, "▶") != 1 {
			t.Errorf("pane %v lacks a unique focus marker:\n%s", m.pane, out)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
}

func TestFilterIndicatorTracksExclusions(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "plan.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !strings.Contains(unstyled(m.View()), "FILTERS (1)") {
		t.Fatalf("excluded value is not counted in the filter title:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(unstyled(m.View()), "FILTERS (") {
		t.Fatal("clearing filters left an active filter indicator")
	}
}
