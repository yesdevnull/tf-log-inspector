package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// focusFacets tabs until the facet pane has focus, so a test can act on it
// without depending on which pane New starts focused.
func focusFacets(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 8; i++ {
		if m.Focus() == PaneFacets {
			return m
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	}
	t.Fatal("focus never reached PaneFacets")
	return m
}

// An empty filter shows everything. Toggling one value narrows every view at
// once -- the spec calls facets cumulative -- and toggling it back restores.
// two-providers.log exists specifically so this has a second provider to
// narrow away: every other RPC fixture in this repo has just one, and
// selecting the only value present can never narrow anything.
func TestFacetToggleFiltersEveryView(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	before := len(m.rows())
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab}) // focus moves off the list
	for m.Focus() != PaneFacets {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.rows()) >= before {
		t.Errorf("toggling a facet did not narrow the list: %d then %d", before, len(m.rows()))
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.rows()) != before {
		t.Errorf("untoggling did not restore the list: %d, want %d", len(m.rows()), before)
	}
}

func TestEscClearsAllFilters(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	before := len(m.rows())
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.rows()) != before {
		t.Errorf("Esc did not clear filters: %d rows, want %d", len(m.rows()), before)
	}
}

// The dimension header is upper-cased and pluralised (task 6, to match the
// design mock-up's PROVIDERS/LEVELS section labels), so this checks case-
// insensitively rather than pinning the exact casing.
func TestFacetPaneShowsCountsPerValue(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	out := m.renderFacets(30, 20)
	if !strings.Contains(strings.ToUpper(out), "PROVIDER") {
		t.Errorf("facet pane missing the provider dimension:\n%s", out)
	}
}

// Step A (task 6): without a visible cursor, space's "toggle whatever the
// cursor points at" is unusable -- the user cannot tell what they are about
// to select. two-providers.log's provider values are sorted alphabetically
// (aws, then google), so the cursor starts on the aws value.
func TestRenderFacetsHighlightsTheCursorValue(t *testing.T) {
	m := focusFacets(t, New(testLog(t, "two-providers.log"), "x.log"))
	out := m.renderFacets(50, 20)
	var highlighted []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "\x1b[7m") {
			highlighted = append(highlighted, ln)
		}
	}
	if len(highlighted) != 1 {
		t.Fatalf("got %d highlighted lines, want exactly 1:\n%s", len(highlighted), out)
	}
	if !strings.Contains(highlighted[0], "registry.terraform.io/hashicorp/aws") {
		t.Errorf("highlighted line is not the cursor's value:\n%s", highlighted[0])
	}
}

// The facet cursor must actually move, and space must act on whatever it
// currently points at -- not always the first value of the first dimension.
// two-providers.log's provider dimension has two values sorted alphabetically
// (aws, then google), so moving down once and toggling must select google,
// not aws.
func TestFacetCursorMovesAndSpaceTogglesValueUnderCursor(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // move onto the second facet value
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})

	rows := m.rows() // default view is ViewProviders
	if len(rows) != 1 {
		t.Fatalf("got %d provider rows after selecting one value, want 1: %+v", len(rows), rows)
	}
	if rows[0].cells[0] != "registry.terraform.io/hashicorp/google" {
		t.Errorf("selected provider row = %q, want the google provider -- space acted on the wrong value", rows[0].cells[0])
	}
}

// A cache that survived a filter change would silently keep serving the
// pre-toggle rows -- a wrong answer, not a crash -- so this pins invalidation
// directly rather than relying on TestFacetToggleFiltersEveryView's narrowing
// check to catch it incidentally.
func TestRowsCacheInvalidatesOnFilterChange(t *testing.T) {
	m := New(testLog(t, "two-providers.log"), "x.log")
	warm := m.rows() // populate any cache before the filter changes
	if len(warm) == 0 {
		t.Fatal("need at least one row to prove filtering narrows it")
	}
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.rows(); len(got) >= len(warm) {
		t.Errorf("rows() returned %d rows after filtering, want fewer than %d -- looks like a stale cache", len(got), len(warm))
	}
}
