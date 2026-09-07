package tui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// accent is the one colour this interface uses. Everything the eye should
// find first -- a pane's name in the top rule, the key in a footer hint, the
// header of the sorted column, the label heading a value in a detail pane,
// and the report the footer raises when a keystroke found nothing -- wears
// it, and nothing outside that list does. A second colour would
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
// arithmetic and the screen out of step. That line is held for every style,
// including ones added later, by
// TestEveryThemedStyleLeavesTheTextWidthUnchanged.
type theme struct {
	// title marks the name of a thing on the frame: the header line naming
	// the open file, every pane's title, the facet pane's dimension
	// headings, and the help screen's own title and group headings.
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
	// chrome marks what is scaffolding for content rather than content
	// itself: the pane row's separators, and the rules that close it top
	// and bottom. Dimming is what turns a structural character into
	// something the eye can skip rather than read, and the frame around the
	// panes is the only thing on screen that is purely structural.
	//
	// The names inset into the top rule are NOT chrome. They take the title
	// style there, or the cursor bar where the pane has the keyboard (see
	// titledRule). What the rule is drawn in says nothing about what it
	// carries.
	//
	// Nothing a cursor bar can wrap is chrome, however much it looks like
	// scaffolding. A facet's checkbox is the tempting case and the
	// forbidden one: its line becomes the bar, and reverse video ends at
	// the first reset inside what it wraps.
	chrome lipgloss.Style
	// fieldLabel marks the label heading a value in a detail pane, where the
	// label sits on its own line above what it names (detailFieldLines).
	// Accent over the dim weight, and it needs BOTH. Colour is withheld by
	// not setting a foreground (see newTheme), so an accent-only label
	// carries no treatment of its own under NO_COLOR -- exactly as its
	// value does -- leaving those readers a column of lines distinguished
	// by two columns of indentation alone. The dim weight is the signal
	// that survives.
	//
	// It is quieter than title on purpose: a pane holds one title and as
	// many as eight labels, and labels that shouted as loudly as the
	// heading above them would leave the pane with no hierarchy at all.
	//
	// With colour withheld it renders identically to chrome, which is drawn
	// directly above and below it -- the pane row's rules. That is accepted
	// rather than overlooked: a full-width horizontal rule and a label are
	// told apart by shape and position, not by weight, and there is no third
	// attribute to spend -- bold is what title and columnHeader are told
	// apart by.
	fieldLabel lipgloss.Style
	// excludedValue marks a facet value the filter is currently hiding.
	// Every value starts ticked, so unticking is the filtering action, and
	// what the pane has to answer afterwards is what is still admitted --
	// which a column of identical lines told apart by one character inside
	// a bracket does not. Dimming the whole line lets the exclusions recede
	// and leaves the survivors standing.
	//
	// Faint rather than a colour, for the same reason levelError carries
	// weight: the distinction has to survive NO_COLOR, and this one is read
	// on every frame the reader has filtered anything at all.
	//
	// It renders identically to chrome and to note, and means neither. What
	// keeps them legible apart is shape rather than weight: chrome here is
	// the rule closing the pane row, drawn across its whole width, and a
	// note is prose about the frame, where this is one value's line inside
	// the facet pane.
	excludedValue lipgloss.Style
	// key marks a keystroke where one is offered -- in a footer hint and in
	// the help screen's key column -- leaving the words describing it plain.
	// The reader scanning either is looking for which key to press, not for
	// the sentence around it.
	key lipgloss.Style
	// note marks text ABOUT the frame rather than anything the log said: the
	// observer-effect caveat, the placeholder in an empty detail pane, and
	// the line that explains why a pane is empty wherever one can be.
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
		"fieldLabel":      t.fieldLabel,
		"excludedValue":   t.excludedValue,
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
		fieldLabel:      accented(base.Faint(true)),
		excludedValue:   base.Faint(true),
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

// styles is the theme every render site draws from, and semantic is the
// palette beside it. Both carry colour until applyColourPreference decides
// otherwise.
//
// They are built WITH colour at init rather than from the environment, so
// that what this package renders under `go test` does not depend on whose
// machine it runs on: the goldens are committed styled, and a developer with
// NO_COLOR set in their shell would otherwise fail every golden.
var (
	styles   = newTheme(true)
	semantic = newSemantics(true)
)

// applyColourPreference settles both vocabularies from the environment.
//
// It is a function rather than two lines inside Run because Run cannot be
// called from a test -- it blocks on a terminal -- and two lines that
// nothing can reach are two lines nothing can check. Deleting them left the
// whole suite green: colourWanted was tested thoroughly in isolation, which
// says nothing about whether anything acts on the answer.
//
// Run calls it BEFORE the first frame, and it must: every render site reads
// these values, so replacing them mid-run would change the frame under the
// reader.
func applyColourPreference() {
	c := colourWanted()
	styles, semantic = newTheme(c), newSemantics(c)
}

// The semantic colours. These are the interface's SECOND vocabulary, and it
// is kept apart from the theme deliberately.
//
// A theme style says how important something is on the frame -- this is a
// title, this is chrome -- and there is exactly one accent for it, because
// there is only one thing "look here" can mean. A semantic colour says what
// something IS: red is an error and nothing else, and a lane's hue is that
// provider's and no other's.
//
// Two types rather than a longer theme, because the two rules are applied
// differently as well as meaning differently. newTheme's helper takes a
// style and applies THE colour; newSemantics' takes a colour and applies it
// to a base -- one constructor carrying both would carry both shapes. And a
// palette has levels and lanes to answer for (forLevel, lane), which a
// vocabulary of titles and chrome has no business holding. What each type
// buys most is at the point somebody extends it: a field added lands inside
// a struct whose doc states the one rule that struct obeys.
//
// None of them is the accent. A lane drawn in the colour that elsewhere
// means "this is a title" would be making a claim about importance it does
// not have, and the reader has no way to tell the two uses apart.
const (
	// errorColour and warnColour are the two the terminal's own conventions
	// already settle: red is a failure and amber is a caution, in this
	// interface as everywhere else the reader has been.
	errorColour = lipgloss.Color("1")
	warnColour  = lipgloss.Color("3")
)

// laneColours are the hues a timeline lane is drawn in, one per provider.
// They stand for identity rather than for degree, so they are chosen to be
// told APART rather than ordered: no two adjacent entries are the normal and
// bright forms of one hue, since a terminal theme is free to render those
// close together.
//
// Red and amber are absent: they mean error and warning three lines up in
// the raw log, and a provider is not a failure. Cyan is absent because it is
// the accent. What is left is enough for the handful of providers a plan
// runs -- and when there are more, the list cycles and two providers share a
// hue. The lane's own label still names them, so that costs the reader a
// shortcut, not the answer.
var laneColours = []lipgloss.Color{
	lipgloss.Color("2"),  // green
	lipgloss.Color("4"),  // blue
	lipgloss.Color("5"),  // magenta
	lipgloss.Color("10"), // bright green
	lipgloss.Color("12"), // bright blue
	lipgloss.Color("13"), // bright magenta
}

// semanticStyles is the rendered form of those colours.
type semanticStyles struct {
	// levelError marks a raw-log line the reader must not scroll past. It
	// carries WEIGHT as well as colour, and it is the only semantic style
	// that does: weight survives NO_COLOR, so the one distinction that
	// matters most is the one that does not depend on colour being wanted.
	levelError lipgloss.Style
	// levelWarn marks a raw-log line worth stopping on.
	levelWarn lipgloss.Style
	// lanes are the per-provider timeline hues, in laneColours' order.
	lanes []lipgloss.Style
}

// colours names every style in the palette, the same way theme.all() does
// and for the same reason: the invariants below are checked against what the
// palette holds rather than against a list written once.
func (s semanticStyles) colours() map[string]lipgloss.Style {
	m := map[string]lipgloss.Style{
		"levelError": s.levelError,
		"levelWarn":  s.levelWarn,
	}
	for i, style := range s.lanes {
		m[fmt.Sprintf("lane%d", i)] = style
	}
	return m
}

// newSemantics builds the palette, with the colours applied only if colour
// is wanted -- withheld the same way the theme withholds the accent, by not
// setting a foreground, so levelError keeps its weight either way.
func newSemantics(colour bool) semanticStyles {
	base := styleRenderer.NewStyle()
	fg := func(c lipgloss.Color) lipgloss.Style {
		if !colour {
			return base
		}
		return base.Foreground(c)
	}
	lanes := make([]lipgloss.Style, len(laneColours))
	for i, c := range laneColours {
		lanes[i] = fg(c)
	}
	return semanticStyles{
		levelError: fg(errorColour).Bold(true),
		levelWarn:  fg(warnColour),
		lanes:      lanes,
	}
}

// forLevel reports how a raw-log line of level l should be drawn, and
// whether it should be drawn differently at all. The unmarked answer is a
// style from this package's own pinned renderer rather than a zero
// lipgloss.Style, which would carry lipgloss's global one: both render their
// argument untouched today, and only one of them would still do so if the
// unmarked case ever gained an attribute.
//
// Only ERROR and WARN answer yes. Everything else is left exactly as the log
// wrote it, because these captures are taken at TRACE with provider TRACE --
// that is what the tool is for -- so TRACE is not the exception in this pane,
// it is nearly the whole of it. Dimming it, the obvious reading of "quieter
// levels recede", would dim almost every line on screen: no line would stand
// out against another and the pane would only be harder to read. Marking the
// two levels that ARE rare is the same judgement from the other end.
//
// It reads the same on a STRUCTURED capture, whose lines spell their levels
// in lower case: logfmt.StructuredLevel takes each line's severity off it,
// so an error in Terraform's JSON stream is marked exactly as one in an
// hclog capture is. What the two formats share is the level, which is the
// only thing this decides on.
func (s semanticStyles) forLevel(l logfmt.Level) (lipgloss.Style, bool) {
	switch l {
	case logfmt.LevelError:
		return s.levelError, true
	case logfmt.LevelWarn:
		return s.levelWarn, true
	}
	return styleRenderer.NewStyle(), false
}

// lane returns the hue for the provider at index i of the log's provider
// ordering, cycling when there are more providers than colours.
func (s semanticStyles) lane(i int) lipgloss.Style {
	return s.lanes[i%len(s.lanes)]
}
