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
		m.ActiveView(), m.sortCol, m.Selected(), m.pane, m.excludedFacets, m.raw.top)
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
// soloableFacet gives the facet pane the keyboard and moves its cursor onto
// a dimension holding MORE than one value. Soloing a dimension that offers
// one value is a no-op by construction, and a key that changes nothing with
// the help shut cannot demonstrate that the help blocked it.
func soloableFacet(t *testing.T, m Model) Model {
	t.Helper()
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	for i := 0; i < 20; i++ {
		if dim, _, ok := m.cursorFacetValue(); ok && dim == dimRPC {
			return m
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	t.Fatalf("the facet cursor never reached the %q dimension", dimRPC)
	return m
}

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
		{what: "solo a facet value", prep: soloableFacet, key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}}},
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
	for _, g := range helpGroups {
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
	paneRow := []string{"PROVIDERS", "duration" + sortDescMark, detailIndent + "ApplyResourceChange"}

	shut := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	behind := unstyled(shut.View())
	for _, anchor := range paneRow {
		if !strings.Contains(behind, anchor) {
			t.Fatalf("the frame does not draw %q with the help shut, so its absence with the help open would prove nothing:\n%s", anchor, behind)
		}
	}

	open := update(t, shut, helpKey)
	out := unstyled(open.View())
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

// While the help is open only ?, Esc and q do anything, and the footer names
// two of them. Esc is left out deliberately: it works, but the meaning it is
// advertised under -- "Esc clear" -- describes clearing filters, which is
// not what it does here, and the key table above says what it does instead.
// Hints for ⏎, s, f and the number keys left standing would advertise keys
// that do nothing, over the very screen that says what each key does.
func TestTheFooterDropsEveryInertHintWhileTheHelpIsOpen(t *testing.T) {
	shut := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	open := update(t, shut, helpKey)
	footer := open.keyHints(100)
	inertHints := []string{"⇥ pane", "␣ facet", openHint, sortHint, "f facets", "/ search", "Esc clear", "1 providers", helpHint}
	// Asserted PRESENT with the help shut first. These are literals, and a
	// hint renamed in actionKeys would otherwise leave its row silently
	// asserting the absence of a string the footer never contained.
	for _, hint := range inertHints {
		if !strings.Contains(shut.keyHints(100), hint) {
			t.Fatalf("the shut footer does not offer %q, so its absence with the help open proves nothing: %q", hint, shut.keyHints(100))
		}
	}
	for _, inert := range inertHints {
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
// are locked. 100 columns is one of the three widths the spec names; 60 is
// below detailInlineWidth, the lower bound of the narrowest of those three,
// and is where the pane row is the whole frame.
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

// Nothing in the key table may be wider than 60 columns, which is below
// every width the spec names. A cut IS marked now (see
// TestAClippedHelpDescriptionIsMarked), so this is no longer the only thing
// standing between a reader and a silently broadened claim -- but the tail
// is where each qualifier lives, "in the table views" and "in a timeline
// lane", and a table that fits without cutting states them all.
func TestEveryHelpLineFitsTheNarrowestSupportedWidth(t *testing.T) {
	const narrowest = 60
	for _, line := range strings.Split(renderHelp(hugeWidth, 200), "\n") {
		if n := lipgloss.Width(line); n > narrowest {
			t.Errorf("help line is %d columns, more than the %d a narrow terminal gives it: %q", n, narrowest, line)
		}
	}
}

// The footer must always name a key that works. footer() replaces the whole
// hint block with the raw log's search report, and notFound survives into
// the help because only invalidateRows clears it -- so a reader who searched,
// missed, and then pressed ? was shown "pattern not found" where the only
// two working keys should be, over a view the help is not drawing.
//
// The short-height frame is the one that matters: there the key table is cut
// down to its first group or less, and renderHelp is allowed to cut without
// a mark precisely because "the footer carries the quit hint on every
// frame". This is what makes that true.
func TestTheFooterStillNamesAWorkingKeyOverAFailedSearch(t *testing.T) {
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "zzzz" {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.raw.notFound {
		t.Fatalf("the search found something, so this cannot reach the state it is about")
	}

	m = update(t, m, helpKey)
	for _, h := range []int{40, 12} {
		sized := update(t, m, tea.WindowSizeMsg{Width: 100, Height: h})
		frame := unstyled(sized.View())
		for _, want := range []string{helpCloseHint, quitHint} {
			if !strings.Contains(frame, want) {
				t.Errorf("at height %d the help frame does not name %q, so nothing on it names a working key:\n%s", h, want, frame)
			}
		}
		if strings.Contains(frame, "pattern not found") {
			t.Errorf("at height %d the help frame reports a search in a view it is not drawing:\n%s", h, frame)
		}
	}
}

// A frame short enough to cut the key table must still show some of it. The
// durations caveat takes five lines and qualifies figures the help pane does
// not show, so left up it spends a short frame on numbers that are not there
// and takes them from the only content that is.
func TestTheHelpKeepsItsTableOnAShortFrame(t *testing.T) {
	short := helpModel(t, 100, 12)
	frame := short.View()
	if !strings.Contains(frame, helpTitle) {
		t.Fatalf("the help title is gone at height 12:\n%s", frame)
	}
	if !strings.Contains(frame, "VIEWS") {
		t.Errorf("at height 12 the help is cut down to a heading with no keys under it, naming none at all:\n%s", frame)
	}
	if strings.Contains(frame, "measured under logging") {
		t.Errorf("the durations caveat is drawn over a pane showing no durations:\n%s", frame)
	}
}

// A description cut for width must SAY it was cut. The tail is where the
// qualifier lives, so "sort by the next column, in the table views" clipped
// silently reads as an unconditional binding -- the opposite of what s does
// in the timeline and the raw log, asserted on the one screen a reader
// consults to find out.
func TestAClippedHelpDescriptionIsMarked(t *testing.T) {
	const narrow = 45
	rendered := renderHelp(narrow, 200)
	full := renderHelp(hugeWidth, 200)
	if !strings.Contains(full, "in the table views") {
		t.Fatalf("no help entry carries a trailing qualifier, so this checks nothing:\n%s", full)
	}
	var clipped []string
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, "  ") && line != "" {
			clipped = append(clipped, line)
		}
	}
	if len(clipped) == 0 {
		t.Fatalf("no entry lines at %d columns:\n%s", narrow, rendered)
	}
	marked := 0
	for _, line := range clipped {
		// clipValueEnd's own marker: this description was too WIDE. A
		// height cut carries different words (see moreBelowMark).
		if strings.HasSuffix(line, "…") {
			marked++
		}
	}
	if marked == 0 {
		t.Errorf("at %d columns not one entry line is marked as cut, so every clipped description reads as complete:\n%s", narrow, rendered)
	}
}

// renderHelp is a pane entry point and every other one guards a non-positive
// height. The guard belongs in fitPaneSections, which is what actually
// slices, so both its callers get it. A panic here is a panic inside the alt
// screen, which leaves the user's terminal wrecked.
func TestTheHelpDoesNotPanicAtAnyHeightOrWidth(t *testing.T) {
	for _, h := range []int{-2, -1, 0, 1, 2, 3} {
		for _, w := range []int{-1, 0, 1, 60, 200} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("renderHelp(%d, %d) panicked: %v", w, h, r)
					}
				}()
				renderHelp(w, h)
			}()
		}
	}
}

// The help and the facet overlay both take the whole pane row, so between
// detailInlineWidth and facetInlineWidth they compete. The help must win: it
// is modal, and a reader who pressed the documented help key over an open
// overlay would otherwise get the facets back with showHelp silently set and
// every other key inert.
func TestTheHelpTakesThePaneRowFromAnOpenFacetOverlay(t *testing.T) {
	const w = 80
	if w >= facetInlineWidth || w < detailInlineWidth {
		t.Fatalf("%d is outside the band where the facet overlay and the help compete", w)
	}
	m := update(t, New(testLog(t, "two-tier.log"), "x.log"), tea.WindowSizeMsg{Width: w, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if !m.facetOverlayShowing(w) || !strings.Contains(m.View(), "PROVIDERS") {
		t.Fatalf("the facet overlay is not open at %d columns, so this cannot show what the help displaces", w)
	}

	open := update(t, m, helpKey)
	frame := open.View()
	if !strings.Contains(frame, helpTitle) {
		t.Errorf("? over an open facet overlay drew no key table:\n%s", frame)
	}
	if strings.Contains(frame, "PROVIDERS") {
		t.Errorf("? over an open facet overlay left the facets drawn:\n%s", frame)
	}
	closed := update(t, open, helpKey)
	if back := closed.View(); !strings.Contains(back, "PROVIDERS") {
		t.Errorf("closing the help did not restore the facet overlay it displaced:\n%s", back)
	}
}

// The key table opens on its first heading, not on a blank. The blank above
// every group is there to set the groups off from each other; above the
// FIRST it separates nothing, the pane row's own top rule being immediately
// above it.
//
// What that costs is the shortest pane. Below a handful of lines the body
// is one or two, so a leading blank is the whole of what the reader gets --
// a blank and a cut mark, where the VIEWS heading would have stood. The
// number keys under it do not fit at that height either way.
func TestTheKeyTableOpensOnAHeadingRatherThanABlank(t *testing.T) {
	for _, h := range []int{40, 12, 4, 2, 1} {
		lines := strings.Split(unstyled(renderHelp(60, h)), "\n")
		// Spelled out rather than read back from helpGroups[0].title:
		// compared against the constant, emptying it leaves "" on both
		// sides and this passes over exactly the blank opening line it
		// exists to forbid.
		if got := strings.TrimSpace(lines[0]); got != "VIEWS" {
			t.Errorf("at height %d the key table opens on %q, want %q:\n%s",
				h, got, "VIEWS", strings.Join(lines, "\n"))
		}
	}
}
