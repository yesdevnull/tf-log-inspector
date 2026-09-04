package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
