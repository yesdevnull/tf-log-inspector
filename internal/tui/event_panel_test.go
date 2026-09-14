package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestEventPanelResizeKeepsSelectionVisibleAndSourceReturn(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 60, Height: 24}, {Width: 160, Height: 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			var input strings.Builder
			for i := range 25 {
				fmt.Fprintf(&input, "aws_instance.resource_with_long_name_%d: Creating...\n", i)
			}
			m := New(eventCapture(t, input.String()), "events.log")
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
			for range 10 {
				m.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			m.View()
			m.Update(size)
			if got := ansi.Strip(m.View()); !strings.Contains(got, "> start") {
				t.Errorf("selected event hidden after resize:\n%s", got)
			}
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if m.view != ViewRawLog || m.raw.topLine != 10 {
				t.Fatalf("selected source = view %v line %d, want raw line 10", m.view, m.raw.topLine)
			}
			m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			if got := ansi.Strip(m.View()); !strings.Contains(got, "> start") || m.events.selected != 10 {
				t.Errorf("source return lost visible selection:\n%s", got)
			}
		})
	}
}

func TestEventPanelPagingWithinLongRecordPreservesOffset(t *testing.T) {
	m := New(eventCapture(t, "aws_instance."+strings.Repeat("long_name_", 100)+": Creating...\n"), "events.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	before := m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	offset := m.events.viewport.YOffset
	if offset == 0 {
		t.Fatal("page down did not scroll within the record")
	}
	if after := m.View(); after == before || m.events.viewport.YOffset != offset {
		t.Fatal("render reset paging within the selected record")
	}
}

func TestEventPanelWithoutTimingsJumpsAndReturnsToProgress(t *testing.T) {
	l := eventCapture(t, "heading\naws_instance.a: Creating...\naws_instance.a: Still creating... [10s elapsed]\n")
	m := New(l, "events.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if got := ansi.Strip(m.View()); !strings.Contains(got, "Event history (whole capture)") || !strings.Contains(got, "Still creating") {
		t.Fatalf("event panel missing: %s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || m.raw.topLine != 2 {
		t.Fatalf("source jump = view %v line %d", m.view, m.raw.topLine)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := ansi.Strip(m.View()); !strings.Contains(got, "> progress") {
		t.Fatalf("progress context lost: %s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(ansi.Strip(m.View()), "Event history") {
		t.Fatal("Esc did not close panel")
	}
}

func TestEventPanelSelectedResourceIsExactAndOutcomeResourceReturns(t *testing.T) {
	l := eventCapture(t, `{"@level":"info","type":"planned_change","change":{"resource":{"addr":"aws_instance.a"},"action":"create"}}
{"@level":"info","type":"planned_change","change":{"resource":{"addr":"aws_instance.ab"},"action":"delete"}}
`)
	m := New(l, "plan.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if !strings.Contains(ansi.Strip(m.View()), "Outcomes (whole capture)") {
		t.Fatal("outcome panel not opened")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	got := ansi.Strip(m.View())
	if !strings.Contains(got, "Event history: aws_instance.a") || strings.Contains(got, "aws_instance.ab") {
		t.Fatalf("resource history scope: %s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(ansi.Strip(m.View()), "Outcomes (whole capture)") {
		t.Fatal("resource jump lost outcome parent")
	}
}

func TestIncompletePanelKeyboardAndNarrowPaging(t *testing.T) {
	var input strings.Builder
	for range 12 {
		input.WriteString("aws_instance.a: Creating...\n")
	}
	m := New(eventCapture(t, input.String()), "incomplete.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	before := ansi.Strip(m.View())
	if !strings.Contains(before, "Incomplete (whole capture)") || !strings.Contains(before, "Esc") {
		t.Fatalf("panel missing: %s", before)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	after := ansi.Strip(m.View())
	if before == after {
		t.Fatal("page down did not reveal later evidence")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(ansi.Strip(m.View()), "Incomplete (whole capture)") {
		t.Fatal("Esc did not close incomplete panel")
	}
}

func TestEventInspectionHintsAndSelectedTimingResource(t *testing.T) {
	m := New(eventCapture(t, "aws_instance.a: Creating...\naws_instance.a: Creation complete after 1s\naws_instance.ab: Creating...\naws_instance.ab: Creation complete after 2s\n"), "events.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	for _, hint := range []string{"v events", "p outcomes", "u incomplete"} {
		if !strings.Contains(m.actionKeys(160), hint) {
			t.Errorf("missing hint %q", hint)
		}
	}
	m.setView(ViewResources)
	selected := m.selectedResourceRow().Address
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if m.events.address != selected {
		t.Fatalf("history scope %q != %q", m.events.address, selected)
	}
	for _, r := range m.eventPanelRecords() {
		if r.sourceLine > 0 && !strings.Contains(r.text, selected+" ·") {
			t.Fatalf("wrong resource event: %+v", r)
		}
	}
}

func TestRawLogEventHintsPreserveNavigationWithinWidth(t *testing.T) {
	for _, input := range []string{
		"aws_instance.a: Creating...\n",
		"aws_instance.a: Creating...\naws_instance.a: Creation complete after 1s\n",
	} {
		for _, width := range []int{60, 100, 160} {
			m := New(eventCapture(t, input), "events.log")
			m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m.setView(ViewRawLog)
			lines := strings.Split(m.footerText(width), "\n")
			actions := lines[len(lines)-1]
			for _, hint := range []string{"g line", "/ search", "Esc clear", "q quit"} {
				if !strings.Contains(actions, hint) {
					t.Errorf("width %d lost %q: %s", width, hint, actions)
				}
			}
			if width >= 100 {
				for _, hint := range []string{"v events", "p outcomes", "u incomplete"} {
					if !strings.Contains(actions, hint) {
						t.Errorf("width %d lost %q: %s", width, hint, actions)
					}
				}
			}
			if got := ansi.StringWidth(m.actionKeys(width)); got > width {
				t.Errorf("action hints occupy %d columns at width %d", got, width)
			}
		}
	}
}
