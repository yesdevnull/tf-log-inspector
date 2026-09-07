package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The help screen: every key this interface binds, on one page, reached with
// '?' and dismissed with '?' or Esc.
//
// It exists because the footer cannot be the answer. The footer's action
// line reaches 70 columns and each hint is pared down to fit -- "␣ facet",
// "↔ span" -- so it can say WHICH keys act on what is on screen but not
// what any of them mean. And the keys it never names at all -- n and N, the
// page keys, the arrows -- are only here.

// helpEntry is one binding: the keys that trigger it, and what it does.
type helpEntry struct {
	keys string
	what string
}

// helpGroup is a titled block of bindings. Groups are the unit a short
// terminal drops whole (see fitPaneSections), so each one is a complete
// answer to a question a reader might have -- how do I move, how do I
// filter -- rather than an arbitrary slice of the key table.
type helpGroup struct {
	title   string
	entries []helpEntry
}

// helpTitle names the screen. It survives every height cut, the same way
// the detail pane's title does.
const helpTitle = "KEYS"

// helpGroups is the key table, in the order a reader meets the interface:
// which view to be in, then what to do in it, then how to narrow it, then
// the keys that work everywhere.
//
// The VIEWS group is GENERATED from views rather than written out, so a view
// added to the interface documents itself. That list already decides which
// number key is bound and what the footer calls it; a hand-kept copy here is
// how a key comes to be bound, advertised, and absent from the one screen a
// reader consults to find out what the keys are.
//
// The remaining groups are written out, because there is no table of action
// bindings to generate them from -- Update dispatches on a switch. That
// direction is not testable and rests on review; the sweep in
// TestHelpDocumentsEveryKeyTheFooterAdvertises covers the other one, which
// is that no key the footer names is missing from here.
var helpGroups = func() []helpGroup {
	viewEntries := make([]helpEntry, 0, len(views))
	for _, b := range views {
		viewEntries = append(viewEntries, helpEntry{keys: b.key, what: b.name})
	}
	return []helpGroup{
		{title: "VIEWS", entries: viewEntries},
		{title: "THE LIST", entries: []helpEntry{
			{keys: "↑ ↓ j k", what: "move the cursor in the focused pane"},
			{keys: "← → h l", what: "step within a timeline lane -- the footer's ↔"},
			{keys: "PgUp PgDn", what: "page through the raw log"},
			{keys: "⏎ Enter", what: "open the selected call in the raw log"},
			{keys: "s", what: "sort by the next column, in the table views"},
		}},
		{title: "FILTERING", entries: []helpEntry{
			{keys: "␣ Space", what: "untick the value under the cursor to hide it"},
			{keys: "f", what: "show the facets and give them the keyboard"},
			{keys: "Esc", what: "clear every active filter -- or close this help"},
			{keys: "/", what: "search the raw log for a pattern"},
			{keys: "n N", what: "step to the next or previous match"},
		}},
		{title: "EVERYWHERE", entries: []helpEntry{
			{keys: "⇥ Tab", what: "move focus between panes"},
			{keys: "?", what: "open or close this help (Esc closes it too)"},
			{keys: "q", what: "quit"},
		}},
	}
}()

// helpKeyGap is the space between the key column and its description.
const helpKeyGap = "  "

// renderHelp draws the key table into a pane w columns wide and h lines
// tall.
//
// The key column is measured across EVERY group rather than per group, so
// the descriptions line up down the whole page: a column that reset at each
// heading would put four description columns on one screen and read as four
// unrelated tables.
//
// Long lines are clipped rather than wrapped, and the height cut is
// fitPaneSections' -- groups dropped whole from the end, the cut marked --
// which is the same treatment the detail pane gets. A cut here is less
// costly than it looks: the footer carries the quit hint on every frame, so
// the one key a stranded reader needs is never the one clipped away.
func renderHelp(w, h int) string {
	groups := helpGroups
	keyWidth := 0
	for _, g := range groups {
		for _, e := range g.entries {
			keyWidth = max(keyWidth, lipgloss.Width(e.keys))
		}
	}

	sections := make([]paneSection, 0, len(groups))
	for _, g := range groups {
		// A blank line above every group, including the first, which sets
		// the groups off from the title as well as from each other. It is
		// carried INSIDE the group rather than appended between them
		// because fitPaneSections drops a group whole: a separator left
		// outside would survive the group it separated and end the pane on
		// a blank line.
		lines := []string{"", styles.title.Render(clipWidth(g.title, w))}
		for _, e := range g.entries {
			pad := strings.Repeat(" ", keyWidth-lipgloss.Width(e.keys))
			// clipValueEnd rather than clipWidth, because what a cut takes
			// off an entry is its QUALIFIER -- "in the table views", "in a
			// timeline lane" -- and clipWidth cuts without a mark. An
			// unmarked cut leaves "sort by the next column" reading as an
			// unconditional binding on the one screen a reader consults to
			// learn what a key does, which is the opposite of what s does
			// in the timeline and the raw log.
			// The keys are accented and their descriptions left plain, the
			// same division the footer makes: a reader on this screen is
			// looking down the key column for the one they want, not
			// reading the page as prose.
			//
			// The clip is applied to the styled line rather than to the
			// text before it, because what the width budget has to hold is
			// what reaches the screen -- and an escape sequence occupies
			// none of it, which clipValueEnd measures correctly and a
			// rune count would not.
			lines = append(lines, clipValueEnd("  "+styles.key.Render(e.keys)+pad+helpKeyGap+e.what, w))
		}
		sections = append(sections, lines)
	}
	return strings.Join(fitPaneSections(styles.title.Render(clipWidth(helpTitle, w)), sections, w, h), "\n")
}
