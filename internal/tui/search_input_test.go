package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Ignoring editor navigation would submit a different query and miss the real log entry.
func TestSearchCursorEditsSubmittedQuery(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = typeQuery(t, m, "/aws_internet_gatewaZ")
	for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyDelete} {
		m = update(t, m, tea.KeyMsg{Type: key})
	}
	m = typeQuery(t, m, "y")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyHome})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDelete})
	m = typeQuery(t, m, "w")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	m = typeQuery(t, m, "!")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.raw.lastQuery != "aws_internet_gateway" || m.raw.notFound {
		t.Fatalf("edited search = %q, not found = %v", m.raw.lastQuery, m.raw.notFound)
	}
	if !strings.Contains(string(m.log.Bytes(m.log.Entries[m.TopEntry()])), "aws_internet_gateway") {
		t.Fatal("edited query did not open its matching log entry")
	}
}

func TestSearchPromptKeepsCursorVisible(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = typeQuery(t, m, "/abcdefghijklmnop")
	for _, width := range []int{8, 20, 8} {
		out := m.footer(width)
		if !strings.Contains(out, "\x1b[7m") || lipgloss.Width(out) > width || !strings.Contains(unstyled(out), "nop") {
			t.Fatalf("search prompt loses its cursor or end at width %d: %q", width, out)
		}
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyHome})
	if out := unstyled(m.footer(8)); !strings.HasPrefix(out, "/abc") {
		t.Fatalf("search prompt did not scroll back to the cursor: %q", out)
	}
}

func TestSearchPasteEscapesControlsBeforeEditing(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = typeQuery(t, m, "/")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("a\x1b]52;c;x\a\n")})
	if m.raw.query != `a\x1b]52;c;x\a\n` {
		t.Fatalf("pasted controls were not escaped: %q", m.raw.query)
	}
}
