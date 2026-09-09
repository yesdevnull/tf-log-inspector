package tui

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// noForegroundSet fails t for any style still naming a colour. It is how
// both vocabularies withhold one: not by changing the renderer's profile,
// which would drop every attribute with it, but by leaving the foreground
// unset.
func noForegroundSet(t *testing.T, styles map[string]lipgloss.Style) {
	t.Helper()
	for name, style := range styles {
		if fg := style.GetForeground(); fg != lipgloss.TerminalColor(lipgloss.NoColor{}) {
			t.Errorf("with colour off, style %s still sets foreground %#v", name, fg)
		}
	}
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
// Every theme these tests exercise is built fresh rather than read from the
// package-level `styles`: applyColourPreference may have rebuilt that one
// for a NO_COLOR terminal, and an invariant that held only when colour
// happened to be wanted would be no invariant at all.
func TestEveryThemedStyleLeavesTheTextWidthUnchanged(t *testing.T) {
	// An ASCII identifier, a double-width run, and the empty string: the
	// last is what a blank scaffolding line hands the chrome style, and a
	// style that padded it would fill blank space with colour.
	samples := []string{"PROVIDERS", "duration▾", "日本語テスト", ""}
	for _, colour := range []bool{true, false} {
		for name, style := range newTheme(colour).all() {
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
	for name, style := range newTheme(true).all() {
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
	if !ansiSlot(accent) {
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
	plain := newTheme(false)
	noForegroundSet(t, plain.all())

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
				if err := os.Unsetenv("NO_COLOR"); err != nil {
					t.Fatal(err)
				}
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

// ansiSlot reports whether c is one of the sixteen colours a terminal theme
// remaps. Naming an absolute colour instead -- a 256-colour index, a hex
// value -- would fix a hue this package chose on a terminal it has never
// seen, legible there and possibly invisible on the reader's.
func ansiSlot(c lipgloss.Color) bool {
	switch c {
	case "0", "1", "2", "3", "4", "5", "6", "7",
		"8", "9", "10", "11", "12", "13", "14", "15":
		return true
	}
	return false
}

// The semantic palette obeys the theme's width rule for the same reason the
// theme does: its styles wrap text that has already been clipped and padded
// to the space it must fill, so one that resized its argument would put a
// raw-log line or a timeline bar out of step with the pane holding it.
func TestEverySemanticStyleLeavesTheTextWidthUnchanged(t *testing.T) {
	samples := []string{"[ERROR] provider crashed", "████░░░░", ""}
	for _, colour := range []bool{true, false} {
		for name, style := range newSemantics(colour).colours() {
			for _, s := range samples {
				if got, want := lipgloss.Width(style.Render(s)), lipgloss.Width(s); got != want {
					t.Errorf("colour=%v style %s rendered %q at width %d, want the unstyled width %d",
						colour, name, s, got, want)
				}
			}
		}
	}
}

// Every semantic colour is remappable, and none of them is the accent.
//
// The accent means "look here" everywhere the theme uses it. A lane drawn in
// it would be making a claim about importance that a provider's identity
// does not carry, and nothing on the frame would tell the two uses apart.
func TestSemanticColoursAreRemappableAndNoneIsTheAccent(t *testing.T) {
	for name, style := range newSemantics(true).colours() {
		fg, ok := style.GetForeground().(lipgloss.Color)
		if !ok {
			t.Errorf("style %s has foreground %#v, want one of the sixteen ANSI colours", name, style.GetForeground())
			continue
		}
		if !ansiSlot(fg) {
			t.Errorf("style %s uses colour %q, which no terminal theme remaps", name, fg)
		}
		if fg == accent {
			t.Errorf("style %s uses the accent %q, which already means \"look here\" rather than naming a thing", name, fg)
		}
	}
}

// A hue stands for a provider's identity, so two hues a terminal may render
// close together must not sit next to each other in the list: adjacent
// entries are what a plan with two providers actually gets.
func TestNoTwoAdjacentLaneColoursAreTheSameHue(t *testing.T) {
	slot := func(c lipgloss.Color) int {
		n, err := strconv.Atoi(string(c))
		if err != nil {
			t.Fatalf("lane colour %q is not an ANSI slot number: %v", c, err)
		}
		return n
	}
	for i := 1; i < len(laneColours); i++ {
		// The bright form of ANSI colour n is n+8.
		if prev, cur := slot(laneColours[i-1]), slot(laneColours[i]); cur == prev+8 || prev == cur+8 {
			t.Errorf("lane colours %d (%d) and %d (%d) are the normal and bright forms of one hue, which a terminal may draw alike",
				i-1, prev, i, cur)
		}
	}
}

// With colour withheld, ERROR keeps its weight. It is the one line in the
// pane a reader must not scroll past, and NO_COLOR asks for no colour rather
// than for no interface -- so the distinction that matters most is carried
// by the one property that does not depend on colour being wanted.
func TestErrorStaysMarkedWithColourWithheld(t *testing.T) {
	plain := newSemantics(false)
	noForegroundSet(t, plain.colours())
	if got := plain.levelError.Render("x"); !strings.Contains(got, "\x1b[1m") {
		t.Errorf("with colour off, an ERROR line is drawn exactly like every other: %q", got)
	}
}

// Only the two rare levels are marked. These captures are taken at TRACE
// with provider TRACE -- that is what the tool is for -- so a scheme that
// dimmed the quieter levels would dim nearly every line in the pane, leaving
// nothing standing out against anything and the log harder to read than
// before. TRACE and DEBUG are asserted plain for that reason, not by
// oversight.
func TestOnlyTheRareLevelsAreMarked(t *testing.T) {
	s := newSemantics(true)
	for _, tc := range []struct {
		level  logfmt.Level
		marked bool
	}{
		{logfmt.LevelError, true},
		{logfmt.LevelWarn, true},
		{logfmt.LevelInfo, false},
		{logfmt.LevelDebug, false},
		{logfmt.LevelTrace, false},
		{logfmt.LevelUnknown, false},
	} {
		if _, got := s.forLevel(tc.level); got != tc.marked {
			t.Errorf("level %v marked = %v, want %v", tc.level, got, tc.marked)
		}
	}
}

// More providers than colours is an ordinary log, not an edge case, so the
// list cycles rather than running out. Two providers then share a hue, which
// costs the reader a shortcut and not the answer: the lane's label names it
// either way.
func TestLaneHuesCycleOnceTheColoursRunOut(t *testing.T) {
	s := newSemantics(true)
	for i := range len(laneColours) * 2 {
		if got, want := s.lane(i), s.lanes[i%len(laneColours)]; got.GetForeground() != want.GetForeground() {
			t.Errorf("lane(%d) = %v, want the colour at %d", i, got.GetForeground(), i%len(laneColours))
		}
	}
	if a, b := s.lane(0), s.lane(1); a.GetForeground() == b.GetForeground() {
		t.Fatal("the first two lanes share a colour, so the cycling above compares a palette of one")
	}
}

// The same completeness rule the theme has: a style the palette holds but
// colours() does not name is checked by none of the invariants above.
func TestEveryStyleThePaletteHoldsIsNamedInColours(t *testing.T) {
	s := newSemantics(true)
	// One entry per field, except that the lanes field is a slice and
	// contributes one entry per colour rather than one for itself -- hence
	// dropping it from the field count and adding its length instead. A
	// second slice field would make this fail rather than pass quietly,
	// which is the direction to be wrong in.
	const sliceFields = 1
	want := reflect.TypeOf(semanticStyles{}).NumField() - sliceFields + len(s.lanes)
	if got := len(s.colours()); got != want {
		t.Errorf("colours() names %d styles but the palette holds %d -- one missing is checked by nothing above", got, want)
	}
}

// withColourPreference runs fn with the vocabularies settled from the
// environment, and puts them back afterwards.
//
// Restoring matters more than it looks: Go runs a package's tests in one
// process, and these two are package-level, so a test that left them
// colourless would strip the styling from every test that ran after it --
// including the goldens, which are committed styled.
func withColourPreference(t *testing.T, noColour string, fn func()) {
	t.Helper()
	if noColour == "" {
		t.Setenv("NO_COLOR", "")
		if err := os.Unsetenv("NO_COLOR"); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Setenv("NO_COLOR", noColour)
	}
	saveStyles, saveSemantic := styles, semantic
	t.Cleanup(func() { styles, semantic = saveStyles, saveSemantic })
	applyColourPreference()
	fn()
}

// The wiring, not the predicate. colourWanted is tested thoroughly above,
// and that says nothing about whether anything acts on its answer: deleting
// the two lines that do left the whole suite green, which is why they are a
// function rather than two lines inside a Run that no test can call.
func TestTheVocabulariesAreSettledFromTheEnvironment(t *testing.T) {
	withColourPreference(t, "1", func() {
		for name, style := range styles.all() {
			if fg := style.GetForeground(); fg != lipgloss.TerminalColor(lipgloss.NoColor{}) {
				t.Errorf("with NO_COLOR set, theme style %s still names colour %#v", name, fg)
			}
		}
		for name, style := range semantic.colours() {
			if fg := style.GetForeground(); fg != lipgloss.TerminalColor(lipgloss.NoColor{}) {
				t.Errorf("with NO_COLOR set, semantic style %s still names colour %#v", name, fg)
			}
		}
	})
	withColourPreference(t, "", func() {
		if got := styles.title.GetForeground(); got != lipgloss.TerminalColor(accent) {
			t.Errorf("with NO_COLOR unset, a title is drawn in %#v rather than the accent", got)
		}
		if got := semantic.levelError.GetForeground(); got != lipgloss.TerminalColor(errorColour) {
			t.Errorf("with NO_COLOR unset, an ERROR line is drawn in %#v rather than red", got)
		}
	})
}

// colourSGR matches the escape sequences that set a foreground: the ANSI
// eight (30-37), their bright forms (90-97), and the extended form (38).
var colourSGR = regexp.MustCompile(`\x1b\[[0-9;]*(3[0-8]|9[0-7])[;m]`)

// A reader who sets NO_COLOR must get a frame with no colour anywhere in it
// -- not merely styles that would withhold colour if a render site asked
// them for it. A site that builds its own style instead of drawing from the
// two vocabularies keeps its colour, and nothing else here would notice: the
// per-style tests inspect the vocabularies rather than the frame.
//
// Attributes are asserted PRESENT in the same sweep, because the failure
// this design exists to avoid is the opposite one -- withholding colour by
// switching the renderer's profile, which drops reverse video with it and
// leaves the reader no way to see which row the cursor is on.
func TestNoFrameCarriesColourWhenItIsWithheld(t *testing.T) {
	withColourPreference(t, "1", func() {
		var frames []string
		for _, c := range wholeFrameCases(t) {
			for _, w := range []int{70, 100, 160} {
				m := update(t, c.m, tea.WindowSizeMsg{Width: w, Height: 40})
				facets, help := focusFacets(t, m), update(t, m, helpKey)
				frames = append(frames, m.View(), facets.View(), help.View())
			}
		}
		raw := update(t, New(testLog(t, "severity-levels.log"), "x.log"), tea.WindowSizeMsg{Width: 120, Height: 30})
		raw = update(t, raw, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
		frames = append(frames, raw.View())

		attributes := 0
		for _, frame := range frames {
			for _, line := range strings.Split(frame, "\n") {
				if colourSGR.MatchString(line) {
					t.Errorf("a frame rendered with colour withheld still carries it: %q", line)
				}
				if strings.Contains(line, "\x1b[") {
					attributes++
				}
			}
		}
		if attributes == 0 {
			t.Error("no frame carries any styling at all, so withholding colour has taken the attributes with it")
		}
	})
}

// lastSGR is the final escape sequence on a line, or "" if it carries none.
func lastSGR(line string) string {
	at := strings.LastIndex(line, "\x1b[")
	if at < 0 {
		return ""
	}
	end := strings.IndexByte(line[at:], 'm')
	if end < 0 {
		return line[at:]
	}
	return line[at : at+end+1]
}

// No line may leave the terminal styled. This is the one failure that
// escapes the program: a frame ending with reverse video or a colour still
// switched on corrupts whatever the user's shell prints next, and nothing in
// this package -- or in bubbletea -- would report it.
//
// It is a real hazard rather than a theoretical one, because almost every
// styled line here is clipped after it is styled, and a clip that dropped a
// closing reset would do exactly this.
func TestNoRenderedLineLeavesTheTerminalStyled(t *testing.T) {
	styled := 0
	for _, c := range wholeFrameCases(t) {
		for _, w := range []int{1, 12, 40, 60, 70, 100, 160} {
			for _, h := range []int{1, 2, 5, 12, 40} {
				m := update(t, c.m, tea.WindowSizeMsg{Width: w, Height: h})
				facets, help := focusFacets(t, m), update(t, m, helpKey)
				for _, frame := range []string{m.View(), facets.View(), help.View()} {
					for _, line := range strings.Split(frame, "\n") {
						last := lastSGR(line)
						if last == "" {
							continue
						}
						styled++
						if last != "\x1b[0m" {
							t.Errorf("%s at %dx%d: a line ends with styling still on (%q): %q", c.name, w, h, last, line)
						}
					}
				}
			}
		}
	}
	if styled == 0 {
		t.Fatal("no rendered line carried styling at all, so nothing above was checked")
	}
}
