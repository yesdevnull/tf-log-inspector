package tui

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

type renderedGlyph struct {
	text           string
	reversed, bold bool
	hasForeground  bool
}

func renderedGlyphs(s string) []renderedGlyph {
	var glyphs []renderedGlyph
	reversed, bold, foreground := false, false, false
	for len(s) > 0 {
		if strings.HasPrefix(s, "\x1b[") {
			end := strings.IndexByte(s, 'm')
			if end >= 0 {
				params := strings.Split(s[2:end], ";")
				if len(params) == 1 && params[0] == "" {
					params[0] = "0"
				}
				for i := 0; i < len(params); i++ {
					p, err := strconv.Atoi(params[i])
					if err != nil {
						continue
					}
					switch p {
					case 0:
						reversed, bold, foreground = false, false, false
					case 1:
						bold = true
					case 22:
						bold = false
					case 7:
						reversed = true
					case 27:
						reversed = false
					case 30, 31, 32, 33, 34, 35, 36, 37, 38, 90, 91, 92, 93, 94, 95, 96, 97:
						foreground = true
					case 39:
						foreground = false
					}
				}
				s = s[end+1:]
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s)
		glyphs = append(glyphs, renderedGlyph{text: string(r), reversed: reversed, bold: bold, hasForeground: foreground})
		s = s[size:]
	}
	return glyphs
}

func reversedText(s string) string {
	var b strings.Builder
	for _, glyph := range renderedGlyphs(s) {
		if glyph.reversed {
			b.WriteString(glyph.text)
		}
	}
	return b.String()
}

func firstReversedGlyph(s string) int {
	for i, glyph := range renderedGlyphs(s) {
		if glyph.reversed {
			return i
		}
	}
	return -1
}

func TestLiteralMatchHighlightExpandsToDisplayedGraphemes(t *testing.T) {
	for _, tc := range []struct {
		name, line, query, reversed string
		offset                      int
	}{
		{"chosen repeated occurrence", "needle first needle second", "needle", "needle", len("needle first ")},
		{"combining mark", "Cafe\u0301 next", "\u0301", "e\u0301", len("Cafe")},
		{"wide prefix", "界needle", "needle", "needle", len("界")},
		{"joined emoji", "👩‍💻 next", "💻", "👩‍💻", strings.Index("👩‍💻 next", "💻")},
		{"visible escaped tab", `a\tb`, `\t`, `\t`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := styleRenderer.NewStyle().Bold(true)
			got := renderLiteralMatch(tc.line, tc.query, literalPosition{byteOffset: tc.offset}, base)
			if text := reversedText(got); text != tc.reversed {
				t.Errorf("reversed text = %q, want %q; render %q", text, tc.reversed, got)
			}
			if text := unstyled(got); text != tc.line {
				t.Errorf("unstyled text = %q, want %q", text, tc.line)
			}
			if gotWidth, wantWidth := lipgloss.Width(got), lipgloss.Width(base.Render(tc.line)); gotWidth != wantWidth {
				t.Errorf("display width = %d, want %d", gotWidth, wantWidth)
			}
			for _, glyph := range renderedGlyphs(got) {
				if !glyph.bold {
					t.Fatalf("base bold style was lost at %q in %q", glyph.text, got)
				}
			}
			if last := lastSGR(got); last != "\x1b[0m" {
				t.Errorf("final SGR = %q, want reset", last)
			}
		})
	}
}

func TestLiteralMatchHighlightRejectsInvalidRanges(t *testing.T) {
	base := styleRenderer.NewStyle().Bold(true)
	for _, tc := range []struct {
		name, query string
		offset      int
	}{
		{"empty query", "", 0},
		{"negative offset", "needle", -1},
		{"offset beyond line", "needle", 7},
		{"range beyond line", "needle!", 0},
		{"mismatched substring", "needlx", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderLiteralMatch("needle", tc.query, literalPosition{byteOffset: tc.offset}, base)
			if want := base.Render("needle"); got != want {
				t.Errorf("render = %q, want base-only %q", got, want)
			}
			if text := reversedText(got); text != "" {
				t.Errorf("invalid range reversed %q", text)
			}
		})
	}
}

func TestRawSearchHighlightTracksSubmittedOccurrence(t *testing.T) {
	m := horizontalLog(t, "needle first needle second")
	m.raw.lastQuery = "needle"
	if !m.searchFrom(0, true, true) {
		t.Fatal("first occurrence missing")
	}
	first := m.renderRawLog(80, 1)
	m.searchAgain(1)
	second := m.renderRawLog(80, 1)
	m.searchAgain(1)
	miss := m.renderRawLog(80, 1)
	if !m.raw.notFound || m.raw.match == nil {
		t.Fatal("miss must retain the exclusive reversal anchor")
	}
	m.searchAgain(-1)
	back := m.renderRawLog(80, 1)

	for name, got := range map[string]string{"first": first, "second": second, "back": back} {
		if reversedText(got) != "needle" {
			t.Errorf("%s reversed text = %q, want needle", name, reversedText(got))
		}
		if unstyled(got) != "needle first needle second" {
			t.Errorf("%s plain text changed: %q", name, unstyled(got))
		}
	}
	if gotFirst, gotSecond := firstReversedGlyph(first), firstReversedGlyph(second); gotFirst != 0 || gotSecond != len("needle first ") {
		t.Errorf("second occurrence did not move the reverse span: first %q second %q", first, second)
	}
	if got := reversedText(miss); got != "" {
		t.Errorf("miss retained reversed text %q", got)
	}

	pressRune(t, m, '/')
	for _, r := range "different" {
		pressRune(t, m, r)
	}
	if got := reversedText(m.renderRawLog(80, 1)); got != "needle" {
		t.Errorf("editable query replaced submitted highlight with %q", got)
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := reversedText(m.renderRawLog(80, 1)); got != "needle" {
		t.Errorf("cancelled editor removed submitted highlight: %q", got)
	}
}

func TestRawSearchHighlightUsesSafeDisplayedTextAndClipsCleanly(t *testing.T) {
	text := "界Cafe\u0301 ne\x1b[31medle\t\x01\x9b\xff tail"
	m := horizontalLog(t, text)
	m.raw.lastQuery = "needle"
	if !m.searchFrom(0, true, true) {
		t.Fatal("visible word split by source ANSI was not found")
	}
	got := m.renderRawLog(80, 1)
	plain, _ := logfmt.StripANSI(text, nil)
	if want := logfmt.DisplayText(plain); unstyled(got) != want {
		t.Errorf("safe displayed text = %q, want %q", unstyled(got), want)
	}
	if reversedText(got) != "needle" || strings.Contains(unstyled(got), "\t") {
		t.Errorf("rendered highlight/control safety = %q", got)
	}

	wide := horizontalLog(t, "needle-wide tail")
	wide.raw.lastQuery = "needle-wide"
	if !wide.searchFrom(0, true, true) {
		t.Fatal("wide match missing")
	}
	clipped := wide.renderRawLog(5, 1)
	if reversedText(clipped) != "needl" || lipgloss.Width(clipped) != 5 || lastSGR(clipped) != "\x1b[0m" {
		t.Errorf("clipped reverse span leaked or moved: %q", clipped)
	}

	edge := horizontalLog(t, "e\u0301 tail")
	edge.raw.lastQuery = "\u0301"
	if !edge.searchFrom(0, true, true) {
		t.Fatal("combining-edge match missing")
	}
	if got := edge.renderRawLog(1, 1); reversedText(got) != "e\u0301" || lipgloss.Width(got) != 1 {
		t.Errorf("combining-edge clip = %q", got)
	}
}

func TestRawSearchHighlightInheritsSeverityWithAndWithoutColour(t *testing.T) {
	for _, colour := range []bool{true, false} {
		t.Run(strconv.FormatBool(colour), func(t *testing.T) {
			oldStyles, oldSemantic := styles, semantic
			styles, semantic = newTheme(colour), newSemantics(colour)
			t.Cleanup(func() { styles, semantic = oldStyles, oldSemantic })

			m := horizontalLog(t, "2026-09-11T00:00:00.000Z [ERROR] before needle after")
			m.raw.lastQuery = "needle"
			if !m.searchFrom(0, true, true) {
				t.Fatal("ERROR-line match missing")
			}
			got := m.renderRawLog(100, 1)
			glyphs := renderedGlyphs(got)
			if reversedText(got) != "needle" {
				t.Fatalf("reversed text = %q, want needle", reversedText(got))
			}
			for _, glyph := range glyphs {
				if !glyph.bold {
					t.Fatalf("ERROR weight was lost at %q in %q", glyph.text, got)
				}
				if glyph.hasForeground != colour {
					t.Fatalf("foreground at %q = %v, want %v", glyph.text, glyph.hasForeground, colour)
				}
			}
			if lastSGR(got) != "\x1b[0m" {
				t.Errorf("severity row did not end reset: %q", got)
			}
		})
	}
}

func TestRawSearchHighlightResetsBeforeTheNextSeverityRow(t *testing.T) {
	oldStyles, oldSemantic := styles, semantic
	styles, semantic = newTheme(true), newSemantics(true)
	t.Cleanup(func() { styles, semantic = oldStyles, oldSemantic })

	m := horizontalLog(t, strings.Join([]string{
		"2026-09-11T00:00:00.000Z [ERROR] before needle after",
		"2026-09-11T00:00:01.000Z [WARN] warning row",
	}, "\n"))
	m.raw.lastQuery = "needle"
	if !m.searchFrom(0, true, true) {
		t.Fatal("ERROR-line match missing")
	}
	rendered := m.renderRawLog(100, 2)
	glyphs := renderedGlyphs(rendered)
	newline := -1
	for i, glyph := range glyphs {
		if glyph.text == "\n" {
			newline = i
			break
		}
	}
	if newline < 0 || newline == len(glyphs)-1 {
		t.Fatalf("two-row render missing its second row: %q", rendered)
	}
	if got := reversedText(rendered); got != "needle" {
		t.Fatalf("joined two-row reverse span = %q, want only needle", got)
	}
	for _, glyph := range glyphs[newline+1:] {
		if glyph.reversed {
			t.Fatalf("reverse video leaked into the WARN row at %q: %q", glyph.text, rendered)
		}
		if glyph.bold || !glyph.hasForeground {
			t.Fatalf("WARN row lost its own severity style at %q: bold=%v foreground=%v", glyph.text, glyph.bold, glyph.hasForeground)
		}
	}
}

func TestRawSearchHighlightClearsWhenNavigationChangesItsDomain(t *testing.T) {
	m := horizontalLog(t, "needle\nsecond")
	find := func() {
		m.raw.lastQuery = "needle"
		if !m.searchFrom(0, true, true) {
			t.Fatal("fixture match missing")
		}
		if reversedText(m.renderRawLog(20, 2)) != "needle" {
			t.Fatal("fixture match was not highlighted")
		}
	}

	find()
	m.scrollRawLog(1)
	if m.raw.match != nil || reversedText(m.renderRawLog(20, 1)) != "" {
		t.Fatal("manual scrolling retained the highlight")
	}

	m.raw.top, m.raw.topLine = 0, 0
	find()
	m.setFacetExclusions(dimLevel, map[string]bool{"ERROR": true})
	m.invalidateRows()
	if m.raw.match != nil || reversedText(m.renderRawLog(20, 2)) != "" {
		t.Fatal("facet change retained the highlight")
	}
	m.setFacetExclusions(dimLevel, nil)

	find()
	if !m.jumpToSourceLine(1) {
		t.Fatal("source jump failed")
	}
	if m.raw.match != nil || reversedText(m.renderRawLog(20, 2)) != "" {
		t.Fatal("source jump retained the highlight")
	}
}
