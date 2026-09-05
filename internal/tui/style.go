package tui

import (
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// styleRenderer is pinned to the ANSI colour profile rather than left to
// auto-detect the terminal. Auto-detection keys off os.Stdout being a real
// TTY, which it never is under `go test`, so an auto-detected renderer would
// silently strip every style -- making the selected-row highlight
// untestable, and invisible in this package's golden files -- and would make
// those goldens vary with whatever colour capability the machine that
// generated them happened to report. A fixed profile keeps both
// deterministic.
var styleRenderer = func() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI)
	return r
}()

// selectedStyle marks the row or facet value the cursor is on in the pane
// that HAS keyboard focus. Reverse video, rather than an explicit colour,
// reads correctly against both light and dark terminal themes without this
// package having to guess either.
var selectedStyle = styleRenderer.NewStyle().Reverse(true)

// unfocusedCursorStyle marks the cursor of a pane that does not have focus.
// Both panes carry a cursor at all times -- Tab moves the keyboard between
// them without moving either cursor -- so drawing both the same way leaves
// the user no way to see what Tab did. It stays a reverse-video bar, dimmed
// rather than dropped: a terminal that ignores faint then shows a cursor
// that merely looks focused, where a marker-only treatment would show no
// cursor at all.
var unfocusedCursorStyle = styleRenderer.NewStyle().Reverse(true).Faint(true)

// cursorBar re-renders s as the cursor's full-width bar, right-padded to
// fill w terminal columns, in the style that says whether its pane has
// focus. The escape sequences the styles add occupy no columns of their
// own, so the content gets the whole of w: a styled row is exactly as wide
// on screen as the unstyled rows above and below it, and the pane
// separators to its right stay in the same column.
func cursorBar(s string, w int, focused bool) string {
	style := unfocusedCursorStyle
	if focused {
		style = selectedStyle
	}
	return style.Render(padRight(clipWidth(s, w), w))
}

// padRight right-pads s with spaces to exactly w terminal columns. Callers
// must clip s to at most w columns first -- this never truncates. It
// measures display width rather than runes so that a line carrying ANSI
// escapes, which occupy no columns, is padded to the same visible width as
// a plain one.
func padRight(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// clipValueFront shortens s to at most w terminal columns by dropping
// characters from the front and marking the cut with a leading ellipsis, so
// s's tail survives instead of its head.
//
// This is the one clipping rule for identifier-like values anywhere in this
// package -- a provider address, a resource type, a resource address, a
// component name -- values distinguished from their siblings by their
// TAIL, not their head: "registry.terraform.io/hashicorp/azuread" and
// "…/azurerm" share a 37-character prefix and differ only in the last 2,
// so an end-clip (clipValueEnd) collapses them to the same text while a
// leading ellipsis keeps them apart. Prose and message text is the
// opposite -- distinguished by its head -- and end-clips, as does the one
// identifier that is also distinguished by its head: an RPC name, which
// names one of a closed set of plugin-protocol methods sharing long
// suffixes (see columnKind). Which of the two rules a value gets follows
// from the kind of value it is, not from a choice made at each render site:
// clipValueForKind is where a kind becomes a direction. See
// clipIdentifierField for the "label plus identifier" line shape several of
// those sites share.
func clipValueFront(s string, w int) string {
	width := lipgloss.Width(s)
	if width <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	if w == 1 {
		return "…"
	}
	// TruncateLeft cuts on grapheme boundaries and keeps whichever grapheme
	// straddles the cut, so a double-width one can leave a tail a column
	// wider than was asked for: drop further cells until it fits beside the
	// ellipsis. An ASCII identifier settles on the first try.
	for drop := width - (w - 1); drop <= width; drop++ {
		if tail := ansi.TruncateLeft(s, drop, ""); lipgloss.Width(tail) <= w-1 {
			return "…" + tail
		}
	}
	return "…"
}

// clipValueEnd shortens s to at most w terminal columns by dropping
// characters from the end and marking the cut with a trailing ellipsis, so
// s's head survives instead of its tail.
//
// It is clipValueFront's mirror, for values distinguished by their HEAD --
// an RPC name (see columnKind), a column header. The ellipsis matters for
// the same reason it does on the front-clip: a clipped value that carries
// no marker reads as a complete one, and a facet value or a table cell is
// something the user acts on. clipWidth, which cuts without a marker, is
// the safety net for already-composed lines rather than for bare values.
func clipValueEnd(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// clipValueForKind clips s to at most w terminal columns from whichever end its kind
// says may give way: a tail-distinguished value keeps its tail
// (clipValueFront), a head-distinguished one keeps its head
// (clipValueEnd), and a NUMBER gives way at neither -- it is returned whole,
// however narrow w is. Both clips mark the cut with an ellipsis at the end
// they cut from.
//
// This is the single place the taxonomy in columnKind is turned into a clip
// direction, so a table cell, a facet value and a detail field all agree on
// what happens to a given kind of value -- which means it has to answer for
// every kind, numericColumn included. Left to fall through, a number would
// be cut from the FRONT, the one end that carries its magnitude: "742.4s"
// rendered as "…2.4s" reads as a smaller number rather than as a cut one.
// Callers keep a number inside its space by other means -- the tables
// reserve numeric columns at their full natural width (fitColumnWidths),
// and clipWidth is the last line of defence beneath every composed line.
//
// The switch names every kind columnKind has. Front-clipping is left as the
// default rather than spelled as its own case because tailIdentifierColumn
// is columnKind's ZERO VALUE: a kind added later and forgotten here then
// gets the conservative treatment -- clipped, marked, tail preserved --
// rather than escaping the clip altogether, which is the same reasoning
// that makes it the zero value.
func clipValueForKind(s string, w int, kind columnKind) string {
	switch kind {
	case headIdentifierColumn:
		return clipValueEnd(s, w)
	case numericColumn:
		return s
	default:
		return clipValueFront(s, w)
	}
}

// clipIdentifierField formats prefix + a value, clipped via
// clipValueForKind to leave room for prefix and suffix, + suffix, at most w
// terminal columns total. Shared by every render site that shows one labelled
// value on its own line -- a facet value's checkbox and count, every field
// of the detail pane -- so the budgeting arithmetic exists in one place
// rather than being reimplemented at each. Which end of the value gives way,
// or whether it gives way at all, is the caller's kind and not this
// function's choice: a numeric field passes through untouched and is held to
// w by the closing clipWidth alone.
//
// Width is display columns throughout, the same measure clipWidth and
// padRight use: a taxonomy that clipped by rune count beneath a safety net
// that cuts by column count would leave neither able to promise that a
// value fits the space it was clipped for.
func clipIdentifierField(prefix, value, suffix string, w int, kind columnKind) string {
	avail := w - lipgloss.Width(prefix) - lipgloss.Width(suffix)
	line := prefix + clipValueForKind(value, avail, kind) + suffix
	return clipWidth(line, w)
}
