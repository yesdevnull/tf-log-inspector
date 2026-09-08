package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestWorkbenchHeaderPreservesFilenameSeparators(t *testing.T) {
	for _, capped := range []uint64{0, 1} {
		m := New(&model.Log{UISaturatedDurations: capped}, "prod · trace.log")
		m.width, m.height = 100, 24
		head := strings.Split(ansi.Strip(m.View()), "\n")[0]
		if !strings.Contains(head, "prod · trace.log") {
			t.Fatalf("header split the filename: %q", head)
		}
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
