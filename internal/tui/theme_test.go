package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// themeUnderTest is the theme these tests exercise, built fresh rather than
// read from the package-level `theme`: Run may have rebuilt that one for a
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
