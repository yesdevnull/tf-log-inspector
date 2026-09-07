package tui

import (
	"os"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// themeUnderTest is the theme these tests exercise, built fresh rather than
// read from the package-level `styles`: Run may have rebuilt that one for a
// NO_COLOR terminal, and an invariant that held only when colour happened to
// be on would be no invariant at all. Both settings get their own theme here
// for the same reason.
func themeUnderTest(colour bool) theme {
	return newTheme(colour)
}

// A themed style may set colour and attributes. It may NOT set width,
// padding, margins or a border: every render site in this package composes,
// measures and pads its text BEFORE handing it to a style, so a style that
// resized its argument would silently desynchronise the layout arithmetic
// from what reaches the screen -- panes overlapping their separators, a
// table's columns no longer starting where its header says they do.
//
// The check is by display width rather than by asking each style what
// properties it sets, because width is the property the layout actually
// depends on: it fails for a border, a padding, a Width() and for any later
// lipgloss addition that happens to make text wider, without this test
// having to enumerate them.
func TestEveryThemedStyleLeavesTheTextWidthUnchanged(t *testing.T) {
	// An ASCII identifier, a double-width run, and the empty string: the
	// last is what a blank scaffolding line hands the chrome style, and a
	// style that padded it would fill blank space with colour.
	samples := []string{"PROVIDERS", "duration▾", "日本語テスト", ""}
	for _, colour := range []bool{true, false} {
		for name, style := range themeUnderTest(colour).all() {
			for _, s := range samples {
				got, want := lipgloss.Width(style.Render(s)), lipgloss.Width(s)
				if got != want {
					t.Errorf("colour=%v style %s rendered %q at width %d, want the unstyled width %d",
						colour, name, s, got, want)
				}
			}
		}
	}
}

// One accent, used everywhere something wants the eye. The interface has no
// second colour to mean anything else, so a style carrying one would be
// saying something this vocabulary has not defined.
//
// lipgloss.NoColor is the other permitted foreground -- an unaccented style
// (bold, faint or reverse alone) takes the terminal's own foreground rather
// than naming one, which is what lets those styles read correctly against a
// theme this package never sees.
func TestTheThemeUsesOneAccentColourAndNoOther(t *testing.T) {
	for name, style := range themeUnderTest(true).all() {
		switch fg := style.GetForeground(); fg {
		case lipgloss.NoColor{}, lipgloss.TerminalColor(accent):
		default:
			t.Errorf("style %s has foreground %#v, want either the accent %#v or no colour", name, fg, accent)
		}
	}
}

// The accent is one of the sixteen ANSI colours, and that is the whole
// light/dark story: a terminal remaps those sixteen to its own scheme, so
// the accent arrives as whatever the reader's theme calls that slot instead
// of as a fixed hue this package guessed. A 256-colour or hex value would
// name an absolute colour that no theme adjusts -- legible on the terminal
// it was chosen on and possibly invisible on the next.
//
// lipgloss.AdaptiveColor, which would otherwise be the answer, cannot work
// here: it picks its variant from HasDarkBackground(), which queries the
// renderer's output, and styleRenderer's output is io.Discard. It would
// silently return the dark variant on every terminal.
func TestTheAccentIsAnANSIColourTheTerminalCanRemap(t *testing.T) {
	switch accent {
	case "0", "1", "2", "3", "4", "5", "6", "7",
		"8", "9", "10", "11", "12", "13", "14", "15":
	default:
		t.Errorf("accent is %q, want one of the sixteen ANSI colours \"0\"-\"15\"", accent)
	}
}

// NO_COLOR asks for no colour, not for no interface. The attributes are how
// this interface says "the cursor is here" and "this is a title", and they
// are not colour: dropping them would leave a reader who set NO_COLOR unable
// to see which row is selected at all.
//
// This is the trap worth naming, because the obvious implementation walks
// into it: switching the renderer to termenv.Ascii makes termenv's
// Style.Styled return its argument untouched (termenv/style.go), which drops
// reverse video and both weights along with the colour. Colour is withheld
// here by not setting a foreground, which leaves every attribute intact.
func TestColourOffKeepsTheAttributesThatCarryMeaning(t *testing.T) {
	plain := themeUnderTest(false)
	for name, style := range plain.all() {
		if fg := style.GetForeground(); fg != lipgloss.TerminalColor(lipgloss.NoColor{}) {
			t.Errorf("with colour off, style %s still sets foreground %#v", name, fg)
		}
	}

	// The three attributes the interface's meaning rests on, each checked on
	// the style that carries it.
	for _, tc := range []struct {
		what  string
		style lipgloss.Style
		seq   string
	}{
		{"the selected row's reverse video", plain.selected, "\x1b[7m"},
		{"a title's weight", plain.title, "\x1b[1m"},
		{"the chrome's dimming", plain.chrome, "\x1b[2m"},
	} {
		if got := tc.style.Render("x"); !strings.Contains(got, tc.seq) {
			t.Errorf("with colour off, %s is gone: rendered %q, want it to contain %q", tc.what, got, tc.seq)
		}
	}
}

// NO_COLOR is honoured per no-color.org: the variable disables colour when
// it is present AND non-empty. An empty NO_COLOR is not a request for
// anything -- it is what a shell leaves behind after `NO_COLOR=` -- and
// treating it as one would silently strip colour from readers who never
// asked.
func TestNoColourIsHonouredOnlyWhenItIsActuallySet(t *testing.T) {
	for _, tc := range []struct {
		what   string
		set    bool
		value  string
		colour bool
	}{
		{"unset", false, "", true},
		{"set to 1", true, "1", false},
		{"set to anything at all", true, "please", false},
		{"present but empty", true, "", true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if tc.set {
				t.Setenv("NO_COLOR", tc.value)
			} else {
				t.Setenv("NO_COLOR", "")
				os.Unsetenv("NO_COLOR")
			}
			if got := colourWanted(); got != tc.colour {
				t.Errorf("colourWanted() = %v with NO_COLOR %s, want %v", got, tc.what, tc.colour)
			}
		})
	}
}

// reverseOpen is how the two cursor styles begin: focused sets reverse
// alone, unfocused sets reverse and faint together.
var reverseOpen = []string{"\x1b[7m", "\x1b[7;2m"}

// A cursor bar must be reversed across its whole width. Reverse video is
// turned off by the first reset inside the string cursorBar wraps, so
// content that arrives carrying its own styling ends the highlight partway
// along the row -- marking a fragment of the selected line and leaving the
// rest looking unselected, which is precisely the "which row am I on"
// question the bar exists to answer.
//
// The check is that the first escape sequence after a bar opens is its own
// closing reset. That is what fails the moment any render site styles
// something that can also be drawn as a cursor -- a facet's checkbox, a
// table cell, a timeline lane label -- and it fails without the test having
// to know which sites those are.
func TestTheCursorBarIsReversedFromEndToEnd(t *testing.T) {
	bars := 0
	for _, c := range wholeFrameCases(t) {
		for _, w := range []int{70, 100, 160} {
			m := update(t, c.m, tea.WindowSizeMsg{Width: w, Height: 40})
			// Both panes carry a cursor at once, and Tab decides which is
			// drawn focused, so each frame is inspected under both.
			for _, focused := range []Model{m, focusFacets(t, m)} {
				for _, line := range strings.Split(focused.View(), "\n") {
					bars += countUnbrokenBars(t, line, c.name, w)
				}
			}
		}
	}
	// Without this the whole test passes over a frame that draws no cursor
	// at all -- which is the state it exists to rule out.
	if bars == 0 {
		t.Fatal("no cursor bar found in any frame, so nothing above was actually checked")
	}
}

// countUnbrokenBars reports how many cursor bars line opens, failing t for
// any whose reverse video is broken by a nested style before its own reset.
func countUnbrokenBars(t *testing.T, line, name string, w int) int {
	t.Helper()
	found := 0
	for _, open := range reverseOpen {
		for at := 0; ; {
			i := strings.Index(line[at:], open)
			if i < 0 {
				break
			}
			at += i + len(open)
			found++
			// The two openers cannot be confused for one another: they end
			// on different bytes, so "\x1b[7m" does not occur inside
			// "\x1b[7;2m" and each match is a bar of its own kind.
			rest := line[at:]
			if next := strings.Index(rest, "\x1b["); next >= 0 && !strings.HasPrefix(rest[next:], "\x1b[0m") {
				t.Errorf("%s at %d columns: a cursor bar is interrupted by a nested style before its own reset, so it stops being reversed partway along the row: %q",
					name, w, line)
			}
		}
	}
	return found
}

// A style the theme holds but all() does not name escapes every invariant
// the tests above hold -- it can resize its argument, or bring a second
// colour into a vocabulary that has exactly one, and no test here would
// see it. all() says to keep the two in step; this is what makes that
// more than a request.
//
// Counting fields is enough and needs no unsafe access to their values:
// the map is keyed by name, so a field added without a matching entry
// changes the count whatever the entry is called.
func TestEveryStyleTheThemeHoldsIsNamedInAll(t *testing.T) {
	got, want := len(newTheme(true).all()), reflect.TypeOf(theme{}).NumField()
	if got != want {
		t.Errorf("all() names %d styles but theme has %d fields -- a style missing from all() is checked by nothing above", got, want)
	}
}
