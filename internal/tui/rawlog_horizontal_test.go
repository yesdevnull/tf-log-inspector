package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func horizontalLog(t *testing.T, text string) *Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "horizontal.log")
	if err := os.WriteFile(path, []byte(text+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, path)
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6")})
	return &m
}

func TestRawLogArrowsRevealClippedTextAndStopAtEdges(t *testing.T) {
	m := horizontalLog(t, "0123456789abcdefghijklmnopqrst")
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := unstyled(m.renderRawLog(20, 5)); got != "123456789abcdefghijk" {
		t.Fatalf("right arrow did not scroll one column: %q", got)
	}
	for i := 0; i < 40; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if got := unstyled(m.renderRawLog(20, 5)); got != "abcdefghijklmnopqrst" {
		t.Fatalf("right edge did not keep the end visible: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := unstyled(m.renderRawLog(20, 5)); got != "9abcdefghijklmnopqrs" {
		t.Fatalf("left did not move immediately from the right edge: %q", got)
	}
	for i := 0; i < 40; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	if got := unstyled(m.renderRawLog(20, 5)); got != "0123456789abcdefghij" {
		t.Fatalf("left edge did not restore the line start: %q", got)
	}
}

func TestRawLogHorizontalScrollRespectsFocusAndResize(t *testing.T) {
	m := horizontalLog(t, "START"+strings.Repeat("prefix", 30)+"TAIL")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	before := m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.View() != before {
		t.Fatal("right arrow scrolled the log while filters had focus")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	for i := 0; i < 200; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if !strings.Contains(unstyled(m.View()), "TAIL") {
		t.Fatal("wide layout did not scroll to the line end")
	}
	m.Update(tea.WindowSizeMsg{Width: 240, Height: 24})
	if !strings.Contains(unstyled(m.View()), "STARTprefix") {
		t.Fatal("resize left a now-fitting line scrolled out of view")
	}
}

func TestRawLogHorizontalScrollPreservesUnicodeAndEscapedControls(t *testing.T) {
	m := horizontalLog(t, "界e\u0301"+strings.Repeat("界", 20)+"\x1b]52;c;x\a")
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	first := unstyled(m.renderRawLog(20, 5))
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	second := unstyled(m.renderRawLog(20, 5))
	if first == second || !strings.HasPrefix(second, "e\u0301") {
		t.Fatalf("scrolling split or skipped a grapheme: %q, %q", first, second)
	}
	for i := 0; i < 60; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
		line := unstyled(m.renderRawLog(20, 5))
		if !utf8.ValidString(line) || lipgloss.Width(line) > 20 || strings.ContainsAny(line, "\x1b\a") {
			t.Fatalf("unsafe or overflowing scrolled text: %q", line)
		}
	}
}

func TestRawLogSearchArrowsEditQueryAndSearchRestoresLineStart(t *testing.T) {
	m := horizontalLog(t, "0123456789abcdefghijklmnopqrst")
	for i := 0; i < 8; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	before := m.renderRawLog(20, 5)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("012")})
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.renderRawLog(20, 5) != before {
		t.Fatal("search cursor movement scrolled the log")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := unstyled(m.renderRawLog(20, 5)); !strings.HasPrefix(got, "012") {
		t.Fatalf("successful search left its line start hidden: %q", got)
	}
}

func TestRawLogHorizontalBoundsFollowVerticalScrolling(t *testing.T) {
	m := horizontalLog(t, "0123456789abcdefghijklmnopqrst\nshort")
	for i := 0; i < 10; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	}
	if got := unstyled(m.renderRawLog(20, 5)); !strings.HasPrefix(got, "abcdefghijklmnopqrst") {
		t.Fatalf("l did not reveal the line end: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := unstyled(m.renderRawLog(20, 5)); got != "short" {
		t.Fatalf("vertical scrolling hid a shorter line: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := unstyled(m.renderRawLog(20, 5)); !strings.HasPrefix(got, "0123456789abcdefghij") {
		t.Fatalf("h did not clamp against the shorter line: %q", got)
	}
}

func TestOpeningACallRestoresTheLeftEdge(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	for i := 0; i < 100; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := unstyled(m.renderRawLog(60, 5)); !strings.HasPrefix(got, "2022-") {
		t.Fatalf("opening a call kept the line context off-screen: %q", got)
	}
}
