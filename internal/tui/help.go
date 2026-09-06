package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The help screen: every key this interface binds, on one page, reached with
// '?' and dismissed with '?' or Esc.
//
// It exists because the footer cannot be the answer. The footer's action
// line is clipped from its end at 70 columns and each hint is pared down to
// fit -- "␣ facet", "↔ span" -- so it can say which keys act on what is on
// screen but not what any of them mean, and it drops the ones that would
// not fit. This is where those are spelled out.

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
func helpGroups() []helpGroup {
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
			{keys: "␣ Space", what: "toggle the facet value under the cursor"},
			{keys: "f", what: "show the facets and give them the keyboard"},
			{keys: "Esc", what: "clear every active filter"},
			{keys: "/", what: "search the raw log for a pattern"},
			{keys: "n N", what: "step to the next or previous match"},
		}},
		{title: "EVERYWHERE", entries: []helpEntry{
			{keys: "⇥ Tab", what: "move focus between panes"},
			{keys: "?", what: "open or close this help"},
			{keys: "q", what: "quit"},
		}},
	}
}

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
	groups := helpGroups()
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
		lines := []string{"", clipWidth(g.title, w)}
		for _, e := range g.entries {
			pad := strings.Repeat(" ", keyWidth-lipgloss.Width(e.keys))
			lines = append(lines, clipWidth("  "+e.keys+pad+helpKeyGap+e.what, w))
		}
		sections = append(sections, lines)
	}
	return strings.Join(fitPaneSections(clipWidth(helpTitle, w), sections, w, h), "\n")
}
