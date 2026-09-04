package tui

import (
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// styleRenderer is pinned to the ANSI colour profile rather than left to
// auto-detect the terminal. Auto-detection keys off os.Stdout being a real
// TTY, which it never is under `go test`, so an auto-detected renderer would
// silently strip every style -- making the selected-row highlight
// untestable, and invisible in the golden files this task also adds -- and
// would make those goldens vary with whatever colour capability the machine
// that generated them happened to report. A fixed profile keeps both
// deterministic.
var styleRenderer = func() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI)
	return r
}()

// selectedStyle marks the row or facet value the cursor is on. Reverse
// video, rather than an explicit colour, reads correctly against both light
// and dark terminal themes without this package having to guess either.
var selectedStyle = styleRenderer.NewStyle().Reverse(true)

// selectedStyleOverhead is how many runes selectedStyle.Render adds beyond
// its content: the escape sequences that switch reverse video on and off.
// highlightLine budgets for it so a styled line's rune count, escapes
// included, never exceeds the width it was given -- the width guarantee
// this package tests counts runes, not the terminal columns those runes
// will actually occupy.
var selectedStyleOverhead = len([]rune(selectedStyle.Render("")))

// highlightLine re-renders s in reverse video, right-padded to fill w runes
// so the highlight reads as a full-width bar, while keeping the escaped
// result itself within w runes. If w leaves no room for the escape
// overhead, s is returned clipped but unstyled rather than risking a line
// over its budget.
func highlightLine(s string, w int) string {
	if w <= selectedStyleOverhead {
		return clipWidth(s, w)
	}
	contentWidth := w - selectedStyleOverhead
	return selectedStyle.Render(padRight(clipWidth(s, contentWidth), contentWidth))
}

// padRight right-pads s with spaces to exactly w runes. Callers must clip s
// to at most w runes first -- this never truncates.
func padRight(s string, w int) string {
	if n := len([]rune(s)); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
