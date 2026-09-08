package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestWorkbenchPanelsHaveIndependentFramesAndGutters(t *testing.T) {
	out := unstyled(framePanes(4,
		pane{title: "CALLS", content: "12ms", width: 16, focused: true},
		pane{title: "SPAN DETAIL", content: "ReadResource", width: 20},
	))
	lines := strings.Split(out, "\n")
	if len(lines) != 4 || !strings.Contains(lines[0], "Calls") || !strings.Contains(lines[0], "Span detail") {
		t.Fatalf("panels lack readable titles: %q", out)
	}
	if !strings.HasPrefix(lines[0], "╭") || !strings.Contains(lines[0], "╮ ╭") || !strings.HasSuffix(lines[3], "╯") {
		t.Fatalf("panels lack independent rounded frames: %q", out)
	}
	if !strings.HasPrefix(lines[1], "│ 12ms") || !strings.Contains(lines[1], "│ │ ReadResource") {
		t.Fatalf("panel content lacks inset spacing: %q", lines[1])
	}
	for _, line := range lines {
		if lipgloss.Width(line) != 37 {
			t.Errorf("panel row width = %d, want 37: %q", lipgloss.Width(line), line)
		}
	}
}
