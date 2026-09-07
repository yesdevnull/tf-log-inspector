package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	helpKey = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	escKey  = tea.KeyMsg{Type: tea.KeyEsc}
)

// helpModel is a model with the help open, for the tests whose subject is
// what help does to the frame rather than how it is reached.
func helpModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: w, Height: h})
	m = update(t, m, helpKey)
	if !m.showHelp {
		t.Fatalf("? did not open the help")
	}
	return m
}

// ? both opens and closes, so a reader who has opened it by accident closes
// it with the key they already pressed rather than guessing.
func TestHelpOpensAndClosesOnQuestionMark(t *testing.T) {
	m := helpModel(t, 100, 40)
	if m = update(t, m, helpKey); m.showHelp {
		t.Error("a second ? did not close the help")
	}
}

// Esc closes the help rather than clearing the filters underneath it. While
// help is on screen there is no filter in view to clear, and a key that
// silently emptied the facet selection behind an overlay would be a change
// the reader cannot see happening.
func TestHelpClosesOnEscWithoutClearingTheFilterBehindIt(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}) // focus the facets
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})                     // select a facet value
	if !m.filterActive() {
		t.Fatalf("no filter is active, so this cannot tell Esc closing the help from Esc clearing the filter")
	}

	m = update(t, m, helpKey)
	m = update(t, m, escKey)
	if m.showHelp {
		t.Error("Esc did not close the help")
	}
	if !m.filterActive() {
		t.Error("Esc closed the help and cleared the filter behind it as well")
	}
}

// q still quits. Help is the screen a reader who is lost opens, and being
// unable to leave the program from it is the worst place to strand them.
func TestHelpLeavesQuitWorking(t *testing.T) {
	if m := update(t, helpModel(t, 100, 40), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); !m.Quitting() {
		t.Error("q while the help is open did not quit")
	}
}

// modalState is everything a key could move that the help is supposed to
// hold still: which view is on, how each table is sorted, where the cursors
// are, which pane has focus, and what is filtered.
//
// It is one fingerprint rather than a list of comparisons because the set
// has to grow with the bindings. Observing only the view, the sort and the
// selection let 'f' and Space through this test silently -- 'f' moves the
// focus and Space the facet selection, and neither was being looked at.
func modalState(m Model) string {
	return fmt.Sprintf("view=%v sort=%v selected=%d pane=%v facets=%v rawtop=%d",
		m.ActiveView(), m.sortCol, m.Selected(), m.pane, m.selectedFacets, m.raw.top)
}

// Every other key is inert while the help is open -- the same modal
// treatment a search in progress already gets, where each key is text for
// the query rather than a command. A number key that switched the view
// behind the help would leave the reader looking at a key list over a view
// they cannot see they have moved to.
//
// Each key is first pressed with the help SHUT and required to move
// modalState. Without that guard a key that had stopped doing anything --
// or a fingerprint that had stopped watching what it does -- would pass
// this test by being inert in both states, which is the failure it exists
// to catch. It is the guard, not the assertion, that makes the row worth
// having.
func TestHelpSwallowsTheKeysThatWouldChangeWhatIsBehindIt(t *testing.T) {
	focusFacets := func(t *testing.T, m Model) Model {
		t.Helper()
		return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	}
	for _, tc := range []struct {
		what string
		prep func(*testing.T, Model) Model
		key  tea.KeyMsg
	}{
		{what: "switch to a rollup view", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}},
		{what: "switch to the timeline", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}}},
		{what: "cycle the sort", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}},
		{what: "move the cursor", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}},
		{what: "focus the facets", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}},
		{what: "toggle a facet value", prep: focusFacets, key: tea.KeyMsg{Type: tea.KeySpace}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			base := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
			if tc.prep != nil {
				base = tc.prep(t, base)
			}
			before := modalState(base)
			if got := modalState(update(t, base, tc.key)); got == before {
				t.Fatalf("%q does not %s with the help shut, so this cannot tell the modal block from a key that does nothing: %s", tc.key.String(), tc.what, before)
			}

			open := update(t, base, helpKey)
			next := update(t, open, tc.key)
			if !next.showHelp {
				t.Errorf("%q closed the help", tc.key.String())
			}
			if got := modalState(next); got != before {
				t.Errorf("%q reached through the help and changed what is behind it:\n before %s\n after  %s", tc.key.String(), before, got)
			}
		})
	}
}

// The VIEWS group is GENERATED from views, the same list the number keys
// and the footer are built from, so a view added to the interface documents
// itself. A hand-kept second list is how a key comes to be bound, offered
// in the footer, and absent from the one screen a reader consults to find
// out what the keys are.
func TestHelpListsEveryBoundViewKeyByItsOwnName(t *testing.T) {
	rendered := renderHelp(100, 200)
	for _, b := range views {
		if !strings.Contains(rendered, b.key) || !strings.Contains(rendered, b.name) {
			t.Errorf("help does not document view key %q (%s):\n%s", b.key, b.name, rendered)
		}
	}
}

// Every key the footer can advertise must be documented here. The footer is
// terse by necessity -- its action line is clipped from the end at 70
// columns -- so help is where those abbreviations are spelled out, and a
// hint naming a key that help never mentions leaves a reader who noticed it
// with nowhere to look it up.
//
// The sweep runs one way only, and says so rather than implying more: it
// catches a hint added to the footer and not to the help. It cannot catch
// the reverse -- a key Update binds that neither names -- because the
// bindings live in a switch rather than a table, and that direction rests
// on review.
//
// A hint's key is its first field. The span hint's ↔ is the one token
// matched against a description rather than a key column: it is the
// footer's compression of ← and →, not a key anyone presses, so help
// decodes it there instead of listing it as something to press.
func TestHelpDocumentsEveryKeyTheFooterAdvertises(t *testing.T) {
	documented := map[string]bool{}
	for _, g := range helpGroups() {
		for _, e := range g.entries {
			for _, f := range strings.Fields(e.keys) {
				documented[f] = true
			}
			for _, f := range strings.Fields(e.what) {
				documented[f] = true
			}
		}
	}

	// Collected from the real footer, in every view, at three widths and
	// with the help both shut and open, so what is swept is what a reader
	// can actually be shown rather than a list of hints kept beside it.
	seen := map[string]bool{}
	for _, b := range views {
		base := update(t, New(testLog(t, "timeline.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
		base = update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(b.key[0])}})
		for _, m := range []Model{base, update(t, base, helpKey)} {
			for _, w := range []int{60, 100, 160} {
				for _, line := range strings.Split(m.keyHints(w), "\n") {
					for _, hint := range strings.Split(line, "  ") {
						if f := strings.Fields(hint); len(f) > 0 {
							seen[f[0]] = true
						}
					}
				}
			}
		}
	}
	if len(seen) < 10 {
		t.Fatalf("only %d distinct footer hints were collected, too few for this to be a sweep: %v", len(seen), seen)
	}

	for key := range seen {
		if !documented[key] {
			t.Errorf("the footer advertises %q, which the help does not mention", key)
		}
	}
}

// The help replaces the PANE ROW and nothing else: the header still names
// the file being read, the footer still carries a way out, and every pane
// between them is gone.
//
// The pane-row anchors are asserted PRESENT with the help shut before they
// are asserted absent with it open. Written the other way round the test
// passes on any string the frame never contained -- an earlier version of
// it looked for a column header the calls view renders clipped and one that
// belongs to a different view, so it could not fail whatever renderPanes
// did.
func TestHelpReplacesThePaneRowAndKeepsTheHeaderAndFooter(t *testing.T) {
	// One anchor from each side pane and one from the centre, so a help
	// that covered only part of the row would still be caught. Each is a
	// string the 100-column calls frame renders whole; see
	// testdata/golden/layout-100.txt.
	paneRow := []string{"PROVIDERS", "duration" + sortDescMark, "RPC   ApplyResourceChange"}

	shut := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	behind := shut.View()
	for _, anchor := range paneRow {
		if !strings.Contains(behind, anchor) {
			t.Fatalf("the frame does not draw %q with the help shut, so its absence with the help open would prove nothing:\n%s", anchor, behind)
		}
	}

	open := update(t, shut, helpKey)
	out := open.View()
	if !strings.Contains(out, "x.log") {
		t.Errorf("the help hid the header:\n%s", out)
	}
	if !strings.Contains(out, quitHint) {
		t.Errorf("the help hid the footer's quit hint:\n%s", out)
	}
	for _, anchor := range paneRow {
		if strings.Contains(out, anchor) {
			t.Errorf("the pane row behind the help is still drawn -- %q survived:\n%s", anchor, out)
		}
	}
}

// The footer names ? on the line that no supported width clips -- the
// action line is already at its budget -- and says what the key will DO,
// which is not the same thing in both states: pressed over an open help it
// closes it, and a hint reading "help" there would offer to open a screen
// the reader is already looking at.
func TestTheFooterOffersHelpAndSaysWhichWayItGoes(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	if shut := m.keyHints(100); !strings.Contains(shut, helpHint) {
		t.Errorf("the footer does not offer help: %q", shut)
	}
	open := update(t, m, helpKey)
	if got := open.keyHints(100); !strings.Contains(got, helpCloseHint) {
		t.Errorf("with the help open the footer does not say ? closes it: %q", open.keyHints(100))
	}
}

// While the help is open every binding but ? and q is inert, so the footer
// must name those two and nothing else. Hints for ⏎, s, f, Esc and the
// number keys left standing there would advertise keys that do nothing --
// over the very screen that says what each key does.
func TestTheFooterDropsEveryInertHintWhileTheHelpIsOpen(t *testing.T) {
	open := helpModel(t, 100, 40)
	footer := open.keyHints(100)
	for _, inert := range []string{"⇥ pane", "␣ facet", openHint, sortHint, "f facets", "/ search", "Esc clear", "1 providers", helpHint} {
		if strings.Contains(footer, inert) {
			t.Errorf("the footer offers %q over an open help, where the key does nothing: %q", inert, footer)
		}
	}
	for _, want := range []string{helpCloseHint, quitHint} {
		if !strings.Contains(footer, want) {
			t.Errorf("the footer does not name %q, one of the two keys that still work: %q", want, footer)
		}
	}
}

// The view-key line is where ? went because it has the room: the action
// line was at 70 columns, the narrowest width that draws every pane, before
// ? was considered. This pins the line that took it inside the same budget.
func TestTheViewKeyLineFitsTheNarrowestThreePaneWidth(t *testing.T) {
	for _, b := range views {
		line := viewKeyHints(b.view)
		if n := lipgloss.Width(line); n > detailInlineWidth {
			t.Errorf("the %s view's key line is %d columns, more than the %d a %d-column terminal gives it: %q",
				b.name, n, detailInlineWidth, detailInlineWidth, line)
		}
	}
}

// Golden files lock the help screen at two widths, the same way the layouts
// are locked. 100 columns is the width this tool is run at; 60 is the
// narrowest the spec names, below detailInlineWidth, where the pane row is
// the whole frame.
//
// What 60 pins is that the table fits there WHOLE. clipWidth truncates
// prose without marking it (the footer's own "q qu" is the precedent), so a
// description one column too long is not reported as cut -- it just ends
// mid-word, and the golden is what makes that visible.
//
// Height 40 rather than a short frame, because what a short frame does to
// this pane is fitPaneSections' behaviour, already pinned against the detail
// pane; a golden of it here would lock the same rule twice.
func TestGoldenHelp(t *testing.T) {
	for _, w := range []int{60, 100} {
		m := helpModel(t, w, 40)
		compareGolden(t, fmt.Sprintf("help-%d.txt", w), m.View())
	}
}

// Nothing in the key table may be wider than the narrowest terminal the
// spec names. A description clipped there loses its tail silently, and the
// tail is where the qualifier lives -- "in the table views", "in the
// timeline" -- so what survives the cut reads as a broader claim than the
// binding makes.
func TestEveryHelpLineFitsTheNarrowestSupportedWidth(t *testing.T) {
	const narrowest = 60
	for _, line := range strings.Split(renderHelp(hugeWidth, 200), "\n") {
		if n := lipgloss.Width(line); n > narrowest {
			t.Errorf("help line is %d columns, more than the %d a narrow terminal gives it: %q", n, narrowest, line)
		}
	}
}
