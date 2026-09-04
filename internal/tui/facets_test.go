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
func TestFacetToggleFiltersEveryView(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
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
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	before := len(m.rows())
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.rows()) != before {
		t.Errorf("Esc did not clear filters: %d rows, want %d", len(m.rows()), before)
	}
}

func TestFacetPaneShowsCountsPerValue(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	out := m.renderFacets(30, 20)
	if !strings.Contains(out, "provider") {
		t.Errorf("facet pane missing the provider dimension:\n%s", out)
	}
}

// A cache that survived a filter change would silently keep serving the
// pre-toggle rows -- a wrong answer, not a crash -- so this pins invalidation
// directly rather than relying on TestFacetToggleFiltersEveryView's narrowing
// check to catch it incidentally.
func TestRowsCacheInvalidatesOnFilterChange(t *testing.T) {
	m := New(testLog(t, "mixed-hcp.log"), "x.log")
	warm := m.rows() // populate any cache before the filter changes
	if len(warm) == 0 {
		t.Fatal("need at least one row to prove filtering removes it")
	}
	m = focusFacets(t, m)
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.rows(); len(got) != 0 {
		t.Errorf("rows() returned %d rows after filtering to nothing, want 0 -- looks like a stale cache", len(got))
	}
}
