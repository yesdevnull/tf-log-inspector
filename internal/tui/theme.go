package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// accent is the one colour this interface uses. Everything the eye should
// find first -- a pane's title, the key in a footer hint, the marker on the
// sorted column -- wears it, and nothing else does. A second colour would
// have to mean something, and there is no second thing to mean: the
// interface's other distinctions are already carried by position and by
// weight.
//
// It is cyan rather than red, green or yellow because terminals and their
// readers give those three status meanings. An accent in one of them would
// read as a verdict on the row it marks -- a red column header saying
// something is wrong with that column -- where cyan says only "here".
//
// Naming an ANSI colour, rather than a 256-colour index or a hex value, is
// what makes the accent safe on a terminal this package never sees: the
// sixteen ANSI slots are remapped by the reader's own scheme, so the accent
// arrives in a hue that already harmonises with everything else on their
// screen. See TestTheAccentIsAnANSIColourTheTerminalCanRemap for why
// lipgloss.AdaptiveColor is not the answer here.
const accent = lipgloss.Color("6")

// theme is the interface's whole visual vocabulary. Every render site takes
// its style from here rather than building one, so what a treatment MEANS is
// decided once: bold-and-accent is a title wherever it appears, faint is
// chrome, reverse is the cursor.
//
// The styles set colour and attributes only. None sets width, padding or a
// border, and none may: text reaches a style already composed, measured and
// padded to the space it has to fill (see the package's clipping and padding
// helpers), so a style that resized its argument would put the layout
// arithmetic and the screen out of step. TestEveryThemedStyleLeavesTheText-
// WidthUnchanged holds that line for every style, including ones added later.
type theme struct {
	// title marks a pane's name and the header line naming the open file.
	title lipgloss.Style
	// columnHeader marks a table's column names. Weight without accent: a
	// table has several headers at once and they are scaffolding for the
	// figures beneath, not the thing to look at.
	columnHeader lipgloss.Style
	// sortedColumn marks the header of the column the table is ordered by --
	// the whole cell, name and marker together, rather than the marker
	// alone. It is accented where its sibling headers are not, because it
	// answers a question the reader just asked by pressing s.
	//
	// Styling the cell whole rather than the glyph inside it is what keeps
	// the name and its marker contiguous in the rendered frame: a style
	// wrapped around the glyph alone would put escape sequences between
	// "duration" and its "▾", so nothing reading the frame could still find
	// the two together.
	sortedColumn lipgloss.Style
	// chrome marks what separates content from content -- pane separators,
	// facet checkboxes. Dimming is what turns a structural character into
	// something the eye can skip rather than read.
	chrome lipgloss.Style
	// key marks the keystroke in a footer hint, leaving the words describing
	// it plain. The reader scanning the footer is looking for which key to
	// press, not for the sentence around it.
	key lipgloss.Style
	// note marks text that qualifies rather than reports: the observer-effect
	// caveat, the placeholder in an empty detail pane.
	note lipgloss.Style
	// alert marks a report the reader has to see because it answers a
	// keystroke that appeared to do nothing -- a search that found no match,
	// a jump refused by the active filter.
	alert lipgloss.Style
	// selected marks the row or facet value the cursor is on in the pane
	// that HAS keyboard focus. Reverse video, rather than an explicit colour,
	// reads correctly against both light and dark terminal themes without
	// this package having to guess either -- the same reasoning that keeps
	// the accent on an ANSI slot.
	selected lipgloss.Style
	// unfocusedCursor marks the cursor of a pane that does not have focus.
	// Both panes carry a cursor at all times -- Tab moves the keyboard
	// between them without moving either cursor -- so drawing both the same
	// way leaves the user no way to see what Tab did. It stays a reverse-
	// video bar, dimmed rather than dropped: a terminal that ignores faint
	// then shows a cursor that merely looks focused, where a marker-only
	// treatment would show no cursor at all.
	unfocusedCursor lipgloss.Style
}

// all names every style in the theme. It exists so the invariants that bind
// the vocabulary as a whole -- width neutrality, one accent and no other --
// are checked against whatever the theme currently holds rather than against
// a list written once and outgrown. A style added to the struct and not to
// this map is the case those tests cannot see, so keep the two in step.
func (t theme) all() map[string]lipgloss.Style {
	return map[string]lipgloss.Style{
		"title":           t.title,
		"columnHeader":    t.columnHeader,
		"sortedColumn":    t.sortedColumn,
		"chrome":          t.chrome,
		"key":             t.key,
		"note":            t.note,
		"alert":           t.alert,
		"selected":        t.selected,
		"unfocusedCursor": t.unfocusedCursor,
	}
}

// newTheme builds the vocabulary, with the accent applied only if colour is
// wanted. Withholding colour is done by not setting a foreground, which
// leaves every attribute in place: a reader who asked for no colour still
// sees which row the cursor is on and which lines are titles.
//
// The tempting alternative -- switching styleRenderer to termenv.Ascii --
// does NOT do this. termenv's Style.Styled returns its argument untouched
// under that profile, so it drops reverse video and both weights along with
// the colour, leaving a frame with no visible cursor.
func newTheme(colour bool) theme {
	base := styleRenderer.NewStyle()
	accented := func(s lipgloss.Style) lipgloss.Style {
		if !colour {
			return s
		}
		return s.Foreground(accent)
	}
	return theme{
		title:           accented(base.Bold(true)),
		columnHeader:    base.Bold(true),
		sortedColumn:    accented(base.Bold(true)),
		chrome:          base.Faint(true),
		key:             accented(base),
		note:            base.Faint(true),
		alert:           accented(base.Bold(true)),
		selected:        base.Reverse(true),
		unfocusedCursor: base.Reverse(true).Faint(true),
	}
}

// colourWanted reports whether the reader will accept colour, per the
// NO_COLOR convention (no-color.org): colour is withheld when the variable
// is present and non-empty.
//
// The emptiness clause is the part worth spelling out. `NO_COLOR=` is what a
// shell leaves behind when the variable is cleared rather than unset, and
// reading that as a request would strip colour from readers who never made
// one.
func colourWanted() bool {
	v, ok := os.LookupEnv("NO_COLOR")
	return !ok || v == ""
}

// styles is the theme every render site draws from. It carries colour until
// Run withholds it, which Run must do before the program starts drawing:
// every render site reads this value, so replacing it mid-run would change
// the frame under the reader.
var styles = newTheme(true)
