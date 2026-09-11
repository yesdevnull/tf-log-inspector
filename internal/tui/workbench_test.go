package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestEmptyRawLogStatusOmitsPosition(t *testing.T) {
	m := New(&model.Log{}, "empty.log")
	m.width, m.height = 100, 24
	m.setView(ViewRawLog)
	status := strings.Split(ansi.Strip(m.View()), "\n")[22]
	if !strings.Contains(status, "No visible source line") || !strings.Contains(status, "whole log") {
		t.Fatalf("empty raw log loses count or scope: %q", status)
	}
	if strings.Contains(status, "line 1") || strings.Contains(status, "column") {
		t.Fatalf("empty raw log claims a position: %q", status)
	}
}

func TestWorkbenchHeaderPreservesFilenameSeparators(t *testing.T) {
	m := New(&model.Log{}, "prod · trace.log")
	m.width, m.height = 100, 24
	head := strings.Split(ansi.Strip(m.View()), "\n")[0]
	if !strings.Contains(head, "prod · trace.log") {
		t.Fatalf("header split the filename: %q", head)
	}
}

func TestWorkbenchKeepsNavigationAboveContentAndActionsLast(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "capture.log")
	m.width, m.height = 100, 24
	lines := strings.Split(ansi.Strip(m.workbenchView()), "\n")
	if !strings.Contains(lines[0], "capture.log") || !strings.Contains(lines[0], "RPC spans") {
		t.Fatalf("header lost file or counts: %q", lines[0])
	}
	if !strings.Contains(lines[1], "calls") || !strings.Contains(lines[1], "help") {
		t.Fatalf("navigation missing above content: %q", lines[1])
	}
	if strings.Count(strings.Join(lines, "\n"), "under logging") != 1 || !strings.Contains(lines[22], "under logging") {
		t.Fatalf("timing warning must appear once just above actions: %q", lines)
	}
	if !strings.Contains(lines[23], "q quit") {
		t.Fatalf("quit action lost: %q", lines[23])
	}
}

func TestWorkbenchFitsSmallTerminalsWithoutLosingActions(t *testing.T) {
	for _, w := range []int{1, 20, 60, 100, 160} {
		for _, h := range []int{1, 2, 3, 4, 5, 7, 8, 12, 24} {
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				m := New(testLog(t, "provider-rpc.log"), "capture.log")
				m.width, m.height = w, h
				lines := strings.Split(m.workbenchView(), "\n")
				if len(lines) != h {
					t.Fatalf("frame has %d rows, want %d", len(lines), h)
				}
				for _, line := range lines {
					if lipgloss.Width(line) > w {
						t.Fatalf("line exceeds width: %q", line)
					}
				}
				if h > 1 && w >= 20 && !strings.Contains(ansi.Strip(lines[h-1]), "q quit") {
					t.Fatalf("actions lost in short frame: %q", lines[h-1])
				}
			})
		}
	}
}

func TestWorkbenchRawStatusAndSearchRemainVisible(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "capture.log")
	m.width, m.height = 240, 24
	m.setView(ViewRawLog)
	m.raw.column = 7
	m.raw.scope = []int{0}
	m.raw.notFound, m.raw.lastQuery = true, "needle"
	lines := strings.Split(ansi.Strip(m.workbenchView()), "\n")
	if strings.Contains(strings.Join(lines, "\n"), "under logging") {
		t.Fatal("raw log shows a timing warning with no durations")
	}
	if !strings.Contains(lines[22], "column 1") || !strings.Contains(lines[22], "call scope") {
		t.Fatalf("raw status loses position or scope: %q", lines[22])
	}
	if !strings.Contains(lines[23], "needle") || !strings.Contains(lines[23], "pattern not found") {
		t.Fatalf("search result lost: %q", lines[23])
	}
	m.showHelp = true
	view := ansi.Strip(m.workbenchView())
	if strings.Contains(view, "pattern not found") || strings.Contains(view, "under logging") || !strings.Contains(view, "close help") || !strings.Contains(strings.Split(view, "\n")[22], "scroll") {
		t.Fatalf("help chrome leaks underlying state: %s", view)
	}
}

func TestWorkbenchRawStatusTracksFacetOverlayVisibility(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	pressRune(t, &m, 'f')
	if !m.facetOverlayShowing(m.width) {
		t.Fatal("facet overlay did not replace the Raw Log pane")
	}
	if got := rawLogStatus(&m); !strings.Contains(got, "No visible source line") || strings.Contains(got, "Line 1") {
		t.Fatalf("hidden Raw Log pane has source position status: %q", got)
	}

	m = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 24})
	if m.facetOverlayShowing(m.width) {
		t.Fatal("wide layout still replaces the Raw Log pane")
	}
	if got := rawLogStatus(&m); !strings.Contains(got, "Line 1 · entry 1/") {
		t.Fatalf("visible Raw Log pane lacks source position status: %q", got)
	}
}
