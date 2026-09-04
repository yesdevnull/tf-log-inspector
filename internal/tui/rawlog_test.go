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
