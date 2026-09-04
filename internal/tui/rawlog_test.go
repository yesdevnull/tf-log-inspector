package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// The whole point: from a slow call, land on the log lines that produced it.
func TestEnterJumpsFromACallToItsLogEntry(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	rows := m.rows()
	if len(rows) < 2 {
		t.Fatal("need at least two calls to prove the jump targets the selected one")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ActiveView() != ViewRawLog {
		t.Fatalf("enter did not switch to the raw log, view = %v", m.ActiveView())
	}
	want := int(m.log.RPCSpans[rows[1].spanIdx].Entry)
	if m.TopEntry() != want {
		t.Errorf("TopEntry = %d, want %d -- the jump must target the selected row's span", m.TopEntry(), want)
	}
}

// An entry's byte range covers its continuation lines, so a multi-line entry
// renders whole rather than as a fragment.
func TestRawLogRendersWholeMultiLineEntries(t *testing.T) {
	m := update(t, New(testLog(t, "multiline-body.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	out := m.renderRawLog(200, 40)
	if strings.Count(out, "\n") < 4 {
		t.Errorf("raw log rendered too few lines for a multi-line fixture:\n%s", out)
	}
}

// Facets apply to the raw log too, per the spec.
func TestRawLogHonoursFilters(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	before := strings.Count(m.renderRawLog(200, 100), "\n")
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := strings.Count(m.renderRawLog(200, 100), "\n"); got >= before {
		t.Errorf("raw log ignored an active filter: %d lines then %d", before, got)
	}
}

// A provider facet filters raw log entries by component, not by span
// ownership: an entry is that provider's traffic (and stays visible) as
// long as it shares a component with one of that provider's spans, whether
// or not the entry itself closes a span. two-providers.log's two spans sit
// on two different components, so selecting aws must keep its own line and
// drop google's.
func TestRawLogProviderFacetFiltersByComponent(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = focusFacets(t, m) // cursor starts on the provider dimension's first (aws) value
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	out := m.renderRawLog(200, 100)
	if !strings.Contains(out, "aws_subnet") {
		t.Errorf("aws's own entry missing after selecting the aws provider facet:\n%s", out)
	}
	if strings.Contains(out, "google_compute_instance") {
		t.Errorf("google's entry survived after selecting the aws provider facet:\n%s", out)
	}
}

// A core entry -- Terraform's own lines, plan output -- has no component
// that maps to any provider, so it has nothing to match once a provider
// facet narrows the view, and is hidden rather than shown by default.
func TestRawLogProviderFacetHidesCoreEntries(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	before := m.renderRawLog(200, 100)
	if !strings.Contains(before, "SYNTHESISED") {
		t.Fatal("fixture's core comment header is not present unfiltered -- test assumption is wrong")
	}
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.renderRawLog(200, 100); strings.Contains(got, "SYNTHESISED") {
		t.Errorf("core entry with no provider component survived an active provider filter:\n%s", got)
	}
}

// RPC and resource type are properties of a call, not of a log line: most
// of what surrounds a slow call (a provider's own DEBUG chatter, HTTP body
// dumps) carries neither field, so applying either dimension to raw
// entries would hide exactly the context this view exists to show.
// Selecting either must leave the raw log completely unchanged.
func TestRawLogIgnoresRPCAndResourceTypeFacets(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	before := m.renderRawLog(200, 100)

	m = focusFacets(t, m)
	for i := 0; i < 2; i++ { // move past both provider values onto the rpc dimension's only value
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.renderRawLog(200, 100); got != before {
		t.Errorf("selecting an rpc facet narrowed the raw log:\nbefore:\n%s\nafter:\n%s", before, got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace}) // deselect it again

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // move onto the resource type dimension's first value
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.renderRawLog(200, 100); got != before {
		t.Errorf("selecting a resource type facet narrowed the raw log:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

// Paging must not walk off either end of the entry index.
func TestRawLogPagingIsClamped(t *testing.T) {
	m := update(t, New(testLog(t, "core-only.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	for i := 0; i < 200; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if m.TopEntry() >= len(m.log.Entries) {
		t.Errorf("TopEntry = %d ran past %d entries", m.TopEntry(), len(m.log.Entries))
	}
	for i := 0; i < 200; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after paging up past the start, want 0", m.TopEntry())
	}
}

// Enter on a rollup row (spanIdx -1, as every ViewProviders row is) has no
// span to jump to, so it must be inert rather than jumping to entry 0.
func TestEnterOnARollupRowIsInert(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ActiveView() != ViewProviders {
		t.Errorf("enter on a rollup row changed the view to %v", m.ActiveView())
	}
}

// '/' opens a synchronous search over the raw log and jumps to the first
// entry at or after the current position whose bytes contain the query.
//
// The spec sizes /pattern with n/N as a cancellable goroutine because it was
// designed against a 1GB log. Real captures measured for this tool are
// 17-37MB, where a synchronous scan over m.log.Data costs a few tens of
// milliseconds -- well under a frame -- so the concurrent, cancellable
// version is deferred until a log turns up large enough to need it. This is
// the same reasoning phase 2 used to replace mmap with a whole-file read.
func TestSlashSearchJumpsToTheFirstMatch(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "aws_internet_gateway" {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	got := m.log.Entries[m.TopEntry()]
	if !strings.Contains(string(m.log.Bytes(got)), "aws_internet_gateway") {
		t.Errorf("TopEntry after search does not contain the query: %q", string(m.log.Bytes(got)))
	}
}

// A search match hidden by the active facet filter must not be jumped to --
// the raw log is filtered, and search must respect what is currently shown.
func TestSlashSearchHonoursActiveFilter(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = focusFacets(t, m) // cursor starts on the provider dimension's first (aws) value
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	before := m.TopEntry()
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "google_compute_instance" {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.TopEntry() != before {
		t.Errorf("search jumped to a match hidden by the active filter: TopEntry = %d, want unchanged %d", m.TopEntry(), before)
	}
}

// footerOf returns the composed view's last line, which is the footer: the
// key hints, or the search prompt while a search is open or has just
// failed.
func footerOf(view string) string {
	lines := strings.Split(view, "\n")
	return lines[len(lines)-1]
}

// rawLogView returns a model showing the raw log at a known terminal size.
func rawLogView(t *testing.T, fixture string) Model {
	t.Helper()
	m := update(t, New(testLog(t, fixture), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
}

// typeQuery sends each rune of q as its own key press, the way a user types
// into the search prompt.
func typeQuery(t *testing.T, m Model, q string) Model {
	t.Helper()
	for _, r := range q {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// '/' captures every subsequent key, so without a prompt the keyboard has
// silently stopped doing what it did a moment ago and the user is typing
// into a void.
func TestSearchPromptShowsTheQueryBeingTyped(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "aws")
	if got := footerOf(m.View()); got != "/aws" {
		t.Errorf("footer while searching = %q, want the prompt %q", got, "/aws")
	}
}

// A search that matches nothing leaves the position unchanged, which is
// indistinguishable from a search that matched the entry already on screen.
// The one has to be reported, and the report has to clear once a later
// search succeeds.
func TestFailedSearchIsReportedAndClearsOnTheNextMatch(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "no-such-text-anywhere")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Errorf("footer after a failed search = %q, want it to report the miss", got)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "aws_internet_gateway")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); got != clipWidth(footerKeys(), 100) {
		t.Errorf("footer after a successful search = %q, want the key hints back", got)
	}
}

// In the alt screen Ctrl+C arrives as a key rather than a signal, so a
// search prompt that swallows every key must still honour it: otherwise the
// only way out of a prompt opened by accident is to guess Esc.
func TestCtrlCQuitsFromTheSearchPrompt(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlC}); !m.Quitting() {
		t.Error("Ctrl+C while the search prompt is open did not quit")
	}
}

// The spec binds up/down and j/k to "move" in every view. In the raw log
// nothing reads the row selection -- the pane renders from TopEntry -- so
// moving has to move the pane's top entry, clamped the same way paging is.
func TestArrowKeysScrollTheRawLog(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}); m.TopEntry() != 1 {
		t.Errorf("TopEntry = %d after one 'j', want 1", m.TopEntry())
	}
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}); m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after 'k' back to the top, want 0", m.TopEntry())
	}
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}); m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after 'k' at the top, want it clamped to 0", m.TopEntry())
	}
	for i := 0; i < 500; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.TopEntry() >= len(m.log.Entries) {
		t.Errorf("TopEntry = %d ran past %d entries", m.TopEntry(), len(m.log.Entries))
	}
}

// Log.Bytes returns a line's ORIGINAL bytes, and Terraform's own plan
// output in a captured log is colourised. Those bytes are untrusted: an
// escape sequence that moves the cursor or clears the screen would corrupt
// the whole frame rather than just its own line, and a colour left unreset
// bleeds into the panes beside it.
func TestRawLogStripsANSIFromLogLines(t *testing.T) {
	data := []byte("2026-09-04T10:00:00.000+1000 [INFO] \x1b[1m\x1b[32m+ create\x1b[0m\x1b[2J aws_instance.example\n")
	l := &model.Log{Data: data, Entries: []logfmt.Entry{{Off: 0, Len: uint32(len(data))}}}
	m := New(l, "x.log")
	m.view = ViewRawLog
	out := m.renderRawLog(200, 10)
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("raw log pane emitted an escape sequence from the log's own bytes: %q", out)
	}
	if !strings.Contains(out, "+ create") {
		t.Errorf("stripping the escapes took the line's text with it: %q", out)
	}
}

// A search must match the same text the pane renders, not the entry's
// original bytes: an escape sequence sitting in the middle of a colourised
// phrase is invisible on screen, so a query for that phrase has to find it
// even though the escape splits it in the underlying bytes.
func TestSlashSearchMatchesAPhraseAnEscapeSequenceSplits(t *testing.T) {
	data := []byte("2026-09-04T10:00:00.000+1000 [INFO] aws\x1b[0m_instance.example: Creation complete\n")
	l := &model.Log{Data: data, Entries: []logfmt.Entry{{Off: 0, Len: uint32(len(data))}}}
	m := New(l, "x.log")
	m.view = ViewRawLog
	m.raw.lastQuery = "aws_instance"
	if !m.searchFrom(0, true, true) {
		t.Errorf("search did not find %q, split only by an escape sequence the screen does not show:\n%s", m.raw.lastQuery, m.renderRawLog(200, 10))
	}
}

// Span.Entry indexes the same log's Entries, but nothing revalidates it when
// a Log is assembled, so jumpToSpan must guard the index itself rather than
// trust it: an out-of-range Entry must leave the model exactly where it was
// rather than index past the end of Entries.
func TestJumpToSpanIgnoresAnOutOfRangeEntryIndex(t *testing.T) {
	l := &model.Log{
		Entries:  []logfmt.Entry{{}},
		RPCSpans: []span.Span{{RPC: "ApplyResourceChange", Provider: "aws", Entry: 99}},
	}
	m := New(l, "x.log")
	m.jumpToSpan(0)
	if m.ActiveView() != ViewProviders {
		t.Errorf("view = %v after a jump to an out-of-range entry, want it left alone", m.ActiveView())
	}
	if m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after a jump to an out-of-range entry, want 0", m.TopEntry())
	}
}

// The search prompt and its miss describe the raw log, the only view '/'
// searches, so leaving that view puts the key hints back rather than
// reporting a search whose result is no longer on screen.
func TestSearchStateIsNotReportedOutsideTheRawLog(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "no-such-text-anywhere")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Fatalf("footer = %q, want the miss reported in the raw log", got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if got := footerOf(m.View()); got != clipWidth(footerKeys(), 100) {
		t.Errorf("footer in the calls view = %q, want the key hints", got)
	}
}

// A newly submitted search must be able to match the entry at the top of
// the pane. Enter on a slow call puts the entry that closed its span on the
// pane's first line; a '/' for a string in that very entry has to find it
// rather than report "pattern not found" with the text on screen.
//
// n and N must still advance past it, or they would return the match
// already shown for ever instead of moving to the next one, so both halves
// are pinned here: the same query, from the same position, must match on
// submission and miss on the repeat.
func TestSlashSearchMatchesTheEntryAtTheTopOfThePane(t *testing.T) {
	const query = "aws_internet_gateway"
	m := rawLogView(t, "provider-rpc.log")
	target, matches := -1, 0
	for i, e := range m.log.Entries {
		if strings.Contains(string(m.log.Bytes(e)), query) {
			matches++
			if target < 0 {
				target = i
			}
		}
	}
	if matches != 1 || target <= 0 {
		t.Fatalf("fixture assumption changed: %q is in %d entries, first at %d -- want exactly one, below the first entry", query, matches, target)
	}
	for m.TopEntry() < target {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, query)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); strings.Contains(got, "not found") {
		t.Errorf("footer = %q -- the search missed text sitting on the pane's first line", got)
	}
	if m.TopEntry() != target {
		t.Errorf("TopEntry = %d after the search, want the matching entry %d", m.TopEntry(), target)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Errorf("footer = %q after 'n' -- the repeat must advance past the match already shown, and there is no other", got)
	}
	if m.TopEntry() != target {
		t.Errorf("TopEntry = %d after a failed 'n', want it left at %d", m.TopEntry(), target)
	}
}

// "pattern not found" is a cached derivation of the last query, the
// position it searched from and the filter it searched under. Anything that
// moves one of those makes it a claim about a search that no longer
// describes what is on screen -- and while it stands, the footer's key
// hints are hidden in the one view where n and N matter most.
func TestFailedSearchReportClearsWhenItsPositionOrFilterMoves(t *testing.T) {
	for _, c := range []struct {
		name string
		act  func(t *testing.T, m Model) Model
	}{
		{"scrolling", func(t *testing.T, m Model) Model {
			return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		}},
		{"paging", func(t *testing.T, m Model) Model {
			return update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
		}},
		{"toggling a facet", func(t *testing.T, m Model) Model {
			return update(t, focusFacets(t, m), tea.KeyMsg{Type: tea.KeySpace})
		}},
		{"clearing filters", func(t *testing.T, m Model) Model {
			m = update(t, focusFacets(t, m), tea.KeyMsg{Type: tea.KeySpace})
			return update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := rawLogView(t, "two-providers.log")
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			m = typeQuery(t, m, "no-such-text-anywhere")
			m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if got := footerOf(m.View()); !strings.Contains(got, "not found") {
				t.Fatalf("footer = %q, want the miss reported before %s moves it", got, c.name)
			}
			after := c.act(t, m)
			if got := footerOf(after.View()); strings.Contains(got, "not found") {
				t.Errorf("footer = %q after %s, want the key hints back -- the miss describes a search that has moved on", got, c.name)
			}
		})
	}
}

// An entry belonging to no provider -- Terraform's own core lines, plan
// output, or a provider whose address the log never named -- resolves to
// the empty provider, and the facet pane offers exactly that as "(none)".
// Matching the raw "" instead makes that checkbox select nothing, the same
// disagreement between what a dimension OFFERS and what it MATCHES that
// model.FacetKey exists to close.
func TestEntryVisibleMatchesAComponentlessEntryAgainstTheNoneFacet(t *testing.T) {
	f := model.Filter{Providers: map[string]bool{model.FacetKey(""): true}}
	core := logfmt.Entry{} // Comp 0: no component, so no provider
	if !entryVisible(f, map[uint16]string{}, core) {
		t.Errorf("an entry with no provider is hidden while %q is the selected provider facet", model.FacetKey(""))
	}
	named := model.Filter{Providers: map[string]bool{"registry.terraform.io/hashicorp/aws": true}}
	if entryVisible(named, map[uint16]string{}, core) {
		t.Errorf("an entry with no provider survived a filter asking for one provider's traffic")
	}
}
