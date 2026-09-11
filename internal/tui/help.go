package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The help screen: every key this interface binds, reached with
// '?' and dismissed with '?' or Esc.
//
// It exists because the footer cannot be the answer. The footer's action
// line reaches 70 columns and each hint is pared down to fit -- "␣ facet",
// "↔ span" -- so it can say WHICH keys act on what is on screen but not
// what any of them mean. And the keys it never names at all -- o, n and N,
// the page keys, the arrows -- are only here.

// helpEntry is one binding: the keys that trigger it, and what it does.
type helpEntry struct {
	keys string
	what string
}

// helpGroup is a titled block of bindings. Each group is a complete
// answer to a question a reader might have -- how do I move, how do I
// filter -- rather than an arbitrary slice of the key table. The viewport
// scrolls through them; bounded renderHelp callers drop groups whole.
type helpGroup struct {
	title   string
	entries []helpEntry
}

// helpTitle names the screen. It is inset into the pane row's top rule
// rather than drawn in the pane's own space, the same way every other pane
// is named, so it survives every height cut without costing the key table a
// line.
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
			{keys: "t", what: "switch RPC/UI timing with the timeline list focused"},
			{keys: "← → h l", what: "step within a timeline lane -- the footer's ↔"},
			{keys: "← → h l", what: "scroll horizontally in raw log or response"},
			{keys: "PgUp PgDn", what: "page through raw log or response"},
			{keys: "r", what: "open reconstructed response; r or Esc returns"},
			{keys: "g", what: "go to a physical source line"},
			{keys: "c", what: "open inferred calls from resource operations"},
			{keys: "↑ ↓ j k", what: "scroll the reconstructed response"},
			{keys: "↵ ⏎ Enter", what: "open selected provider/type or call"},
			{keys: "s", what: "sort by the next column, in the table views"},
			{keys: "\\", what: "show the whole log again, after opening a call"},
		}},
		{title: "FILTERING", entries: []helpEntry{
			{keys: "␣ Space", what: "toggle value; modules may inherit inclusion"},
			{keys: "o", what: "show only that value -- again undoes it"},
			{keys: "f", what: "show the facets and give them the keyboard"},
			{keys: "Esc", what: "return; then clear query or filters"},
			{keys: "/", what: "narrow resources/modules; search log/response"},
			{keys: "n N", what: "step to the next or previous match"},
		}},
		{title: "EVERYWHERE", entries: []helpEntry{
			{keys: "⇥ Tab", what: "move focus between panes"},
			{keys: "i", what: "open quality; select, Enter checks/jumps; i or Esc closes"},
			{keys: "e", what: "open resource evidence; e or Esc returns"},
			{keys: "?", what: "open or close this help (Esc closes it too)"},
			{keys: "↑ ↓ j k", what: "scroll help; PgUp/PgDn move a page"},
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
	for i, g := range groups {
		// A blank line above every group but the FIRST, setting them off
		// from each other. The first needs none: the pane row's top rule is
		// immediately above it and separates it already, and a blank there
		// is the whole of what a two-line pane can show -- an opening blank
		// and a cut mark, where the VIEWS heading would have stood.
		//
		// It is carried INSIDE the group rather than appended between them
		// because fitPaneSections drops a group whole: a separator left
		// outside would survive the group it separated and end the pane on
		// a blank line.
		var lines []string
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, styles.title.Render(clipWidth(g.title, w)))
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
	resources := paneSection{"", styles.title.Render(clipWidth("RESOURCES", w))}
	for _, line := range []string{
		"Resources ranks exact addresses by summed",
		"observed resource duration.",
		"Associated RPC time is inferred and partial.",
		"It cannot recover every call.",
		"UI scope: type/resource/module.",
		"RPC scope: provider/type/method/resource/module.",
		"Resource/module / narrows only the chooser.",
		"Space or o changes results.",
		"(root subtree) includes every known descendant.",
		"[+] module is included through a selected ancestor.",
		"Module instance keys remain exact.",
		"e shows selected-scope evidence; i shows whole-log quality.",
	} {
		for _, wrapped := range strings.Split(ansi.Wrap(line, max(1, w), ""), "\n") {
			resources = append(resources, styles.note.Render(clipWidth(wrapped, w)))
		}
	}
	sections = append(sections, resources)
	timing := paneSection{"", styles.title.Render(clipWidth("TIMING", w))}
	for _, line := range fullLoggingCaveat {
		for _, wrapped := range strings.Split(ansi.Wrap(line, max(1, w), ""), "\n") {
			timing = append(timing, styles.note.Render(clipWidth(wrapped, w)))
		}
	}
	sections = append(sections, timing)
	return strings.Join(fitPaneSections(sections, w, h), "\n")
}

// renderWorkbenchHelp keeps the complete guide reachable in a short pane.
func (m *Model) renderWorkbenchHelp(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if m.helpViewport.Width == 0 {
		m.helpViewport = viewport.New(w, h)
		m.helpViewport.MouseWheelEnabled = false
	}
	m.helpViewport.Width, m.helpViewport.Height = w, h
	m.helpViewport.SetContent(renderHelp(w, math.MaxInt))
	m.helpViewport.SetYOffset(m.helpViewport.YOffset)
	return m.helpViewport.View()
}
