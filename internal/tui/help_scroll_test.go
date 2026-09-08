package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpScrollReachesTimingExplanation(t *testing.T) {
	m := helpModel(t, 60, 12)
	first := m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.View() == first {
		t.Fatal("down did not scroll the help")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.View() != first {
		t.Fatal("up did not restore the first help line")
	}
	for i := 0; i < 30; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if !strings.Contains(unstyled(m.View()), "absolute times do not transfer.") {
		t.Fatalf("help scrolling never reached the timing explanation:\n%s", m.View())
	}
	for i := 0; i < 30; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if m.View() != first {
		t.Fatal("page up did not stop at the first help line")
	}
}

func TestHelpScrollClampsOnResizeAndResetsOnReopen(t *testing.T) {
	m := helpModel(t, 60, 12)
	for i := 0; i < 200; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	if !strings.Contains(unstyled(m.View()), "absolute times do not transfer.") {
		t.Fatal("j did not reach the end of help")
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 100})
	if !strings.Contains(unstyled(m.View()), "VIEWS") {
		t.Fatal("enlarging the help left its first section scrolled away")
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	first := m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.View() != first {
		t.Fatal("k did not return after j")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m.Update(helpKey)
	m.Update(helpKey)
	if m.View() != first {
		t.Fatal("reopening help did not restore its first section")
	}
}
