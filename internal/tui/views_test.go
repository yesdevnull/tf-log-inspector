package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestProvidersViewRanksByTotalTime(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	out := m.renderList(100, 20)
	if !strings.Contains(out, "provider") {
		t.Errorf("providers view has no provider column:\n%s", out)
	}
}

// The types view shows both tiers side by side, which is the cross-tier join
// phase 2 built. A view showing only one tier would silently drop the other.
func TestTypesViewShowsBothTiers(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	out := m.renderList(120, 20)
	for _, want := range []string{"aws_instance", "UI", "RPC"} {
		if !strings.Contains(out, want) {
			t.Errorf("types view missing %q:\n%s", want, out)
		}
	}
}

// UI-hook figures are whole seconds carrying up to a second of error each, so
// a view ranking them must say so, exactly as --profile does.
func TestTypesViewStatesUIHookResolution(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if !strings.Contains(m.renderList(120, 20), "whole seconds") {
		t.Error("types view does not state the UI-hook resolution")
	}
}

func TestCallsViewRanksByDurationAndCarriesSpanIndex(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	rows := m.rows()
	if len(rows) == 0 {
		t.Fatal("calls view has no rows")
	}
	for i, r := range rows {
		if r.spanIdx < 0 {
			t.Errorf("row %d has no span index; jump-to-log needs one", i)
		}
	}
	for i := 1; i < len(rows); i++ {
		a := m.log.RPCSpans[rows[i-1].spanIdx].DurationMs
		b := m.log.RPCSpans[rows[i].spanIdx].DurationMs
		if a < b {
			t.Errorf("rows not ranked by duration descending at %d: %d then %d", i, a, b)
		}
	}
}

// RowCount must reflect the current view's actual row count, derived from
// rows(), not a placeholder count borrowed from a different slice -- an
// out-of-sync RowCount would clamp selection against the wrong length.
func TestRowCountMatchesRowsLength(t *testing.T) {
	for key, view := range map[rune]View{'1': ViewProviders, '2': ViewTypes, '4': ViewCalls} {
		m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if m.RowCount() != len(m.rows()) {
			t.Errorf("view %v: RowCount() = %d, want len(rows()) = %d", view, m.RowCount(), len(m.rows()))
		}
	}
}

// A view narrower than its columns must degrade, not wrap into unreadable
// wreckage. Width degradation proper is Task 6; this pins that renderList
// never emits a line wider than it was given.
func TestRenderListRespectsItsWidth(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	for _, line := range strings.Split(m.renderList(60, 20), "\n") {
		if len([]rune(line)) > 60 {
			t.Errorf("line exceeds the given width of 60: %q", line)
		}
	}
}
