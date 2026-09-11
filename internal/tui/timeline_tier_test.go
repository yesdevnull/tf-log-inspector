package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestTimelineTierSwitchSelectsIndependentClock(t *testing.T) {
	m := update(t, New(loadedMixedPositionLog(t), "mixed.log"),
		tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if tier, _ := m.timelineSpans(); tier != tierRPC {
		t.Fatal("mixed capture must default to RPC")
	}
	m.switchTimelineTier()
	if tier, _ := m.timelineSpans(); tier != tierUI {
		t.Fatal("explicit switch did not select UI")
	}
	if id := m.selectedTimelineIdentity(); id.kind != "ui" || id.index != 0 {
		t.Fatalf("UI source identity = %#v", id)
	}
	m.switchTimelineTier()
	if id := m.selectedTimelineIdentity(); id.kind != "rpc" || id.index != 0 {
		t.Fatalf("RPC source identity = %#v", id)
	}
}

func TestTimelineTierKeySwitchesOnlyTheFocusedVisibleTimeline(t *testing.T) {
	newTimeline := func(t *testing.T) Model {
		t.Helper()
		m := update(t, New(testLog(t, "timing-tiers.log"), "timing-tiers.log"),
			tea.WindowSizeMsg{Width: 160, Height: 40})
		return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	}
	pressT := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}}

	t.Run("list", func(t *testing.T) {
		m := update(t, newTimeline(t), pressT)
		if tier, _ := m.timelineSpans(); tier != tierUI {
			t.Fatal("t did not switch the focused timeline")
		}
		if !strings.Contains(unstyled(m.timelineTitle()), "ui, whole seconds") {
			t.Fatal("UI timing qualification missing")
		}
		if len(m.history) != 0 {
			t.Fatalf("switch added %d history frames", len(m.history))
		}
	})

	for _, tc := range []struct {
		name  string
		setup func(*Model)
	}{
		{"facets", func(m *Model) { m.pane = PaneFacets }},
		{"detail", func(m *Model) { m.pane = PaneDetail }},
		{"narrow facet overlay", func(m *Model) {
			m.width = 60
			m.showFacetOverlay = true
			m.pane = PaneFacets
		}},
		{"help", func(m *Model) { m.showHelp = true }},
		{"quality", func(m *Model) { m.quality.open = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTimeline(t)
			tc.setup(&m)
			m = update(t, m, pressT)
			if tier, _ := m.timelineSpans(); tier != tierRPC {
				t.Fatalf("t switched timeline while %s had focus", tc.name)
			}
		})
	}
}

func TestTimelineTierKeyRemainsTextInsideModalInputs(t *testing.T) {
	pressT := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}}
	for _, tc := range []struct {
		name  string
		setup func(*Model)
		text  func(Model) string
	}{
		{"raw search", func(m *Model) { m.raw.searching = true; m.raw.input = newSearchInput() }, func(m Model) string { return m.raw.input.Value() }},
		{"response search", func(m *Model) {
			m.response.open = true
			m.response.searching = true
			m.response.input = newSearchInput()
		}, func(m Model) string { return m.response.input.Value() }},
		{"facet search", func(m *Model) { m.facetSearch.editing = true; m.facetSearch.input = newSearchInput() }, func(m Model) string { return m.facetSearch.input.Value() }},
		{"source line", func(m *Model) { m.sourceLine.editing = true; m.sourceLine.input = newSourceLineInput() }, func(m Model) string { return m.sourceLine.input.Value() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := update(t, New(testLog(t, "timing-tiers.log"), "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			tc.setup(&m)
			m = update(t, m, pressT)
			if tier, _ := m.timelineSpans(); tier != tierRPC || tc.text(m) != "t" {
				t.Fatalf("modal t = tier %v text %q, want RPC and literal t", tier, tc.text(m))
			}
		})
	}
}

func TestTimelineTierNoticeClearsOnTheNextKeyWithoutInterceptingQuit(t *testing.T) {
	for _, tc := range []struct{ fixture, notice string }{
		{"provider-rpc.log", "RPC timing only; UI timing unavailable"},
		{"structured-ui.log", "UI timing only; RPC timing unavailable"},
		{"core-only.log", "No RPC or UI timing observations"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			m := update(t, New(testLog(t, tc.fixture), tc.fixture), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
			if !strings.Contains(unstyled(m.footer(100)), tc.notice) {
				t.Fatalf("footer did not show unavailable-tier notice: %q", unstyled(m.footer(100)))
			}
			if !strings.Contains(unstyled(m.View()), tc.notice) {
				t.Fatalf("frame did not show unavailable-tier notice: %q", unstyled(m.View()))
			}
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			if m.timeline.notice != "" {
				t.Fatalf("notice survived the next key: %q", m.timeline.notice)
			}
		})
	}

	m := update(t, New(testLog(t, "provider-rpc.log"), "provider-rpc.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil || !next.(*Model).Quitting() {
		t.Fatal("tier notice intercepted quit")
	}
}

func TestTimelineTierFooterAdvertisesSwitchOnlyWhereItWorks(t *testing.T) {
	m := update(t, New(testLog(t, "timing-tiers.log"), "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if got := m.actionKeys(160); !strings.Contains(got, "t tier") {
		t.Fatalf("focused timeline footer omits t tier: %q", got)
	}
	for _, pane := range []Pane{PaneFacets, PaneDetail} {
		m.pane = pane
		if got := m.actionKeys(160); strings.Contains(got, "t tier") {
			t.Fatalf("pane %v footer advertises inert t: %q", pane, got)
		}
	}
	m.pane = PaneList
	m.width = 60
	m.showFacetOverlay = true
	if got := m.actionKeys(60); strings.Contains(got, "t tier") {
		t.Fatalf("covered timeline footer advertises inert t: %q", got)
	}
}

func TestTimelineAxisGutterNamesTierAndPreservesLaneCut(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tier   timelineTier
		hidden int
		width  int
		want   string
	}{
		{"rpc", tierRPC, 0, 5, "rpc  "},
		{"ui", tierUI, 0, 5, "ui   "},
		{"tier and cut", tierRPC, 3, 7, "rpc +3 "},
		{"cut replaces tier when narrow", tierUI, 12, 3, "+12"},
		{"cut survives at minimum width", tierRPC, 4, 1, "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := timelineAxisGutter(tc.tier, tc.hidden, tc.width); got != tc.want {
				t.Fatalf("timelineAxisGutter(%v, %d, %d) = %q, want %q", tc.tier, tc.hidden, tc.width, got, tc.want)
			}
		})
	}
}

func TestTimelineTierRenderNamesActiveTierAndKeepsWidthAndLaneCut(t *testing.T) {
	for _, width := range []int{160, 100, 60} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := update(t, New(testLog(t, "timing-tiers.log"), "timing-tiers.log"), tea.WindowSizeMsg{Width: width, Height: 9})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
			view := unstyled(m.View())
			axis := unstyled(m.renderTimeline(m.paneWidth(), 3))
			if !strings.Contains(strings.ToLower(view), "timeline (ui, whole seconds)") || !strings.Contains(axis, "ui +") {
				t.Fatalf("%d-column UI frame lacks tier title or tier-labelled cut axis:\n%s\naxis:\n%s", width, view, axis)
			}
			for i, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("line %d is %d columns in a %d-column frame: %q", i, got, width, line)
				}
			}
		})
	}
}

func TestTimelineTierRemembersEachOriginalObservation(t *testing.T) {
	l := testLog(t, "timing-tiers.log")
	if len(l.RPCSpans) != 3 || len(l.UISpans) != 2 || l.RPCSpans[0].HasPosition() {
		t.Fatalf("fixture admission: RPC=%d UI=%d first positioned=%t", len(l.RPCSpans), len(l.UISpans), l.RPCSpans[0].HasPosition())
	}
	m := update(t, New(l, "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m.moveTimelineSpan(1)
	rpc := m.selectedTimelineIdentity()
	m.switchTimelineTier()
	ui := m.selectedTimelineIdentity()
	if rpc.kind != "rpc" || rpc.index != 2 || ui.kind != "ui" || ui.index != 1 {
		t.Fatalf("chosen identities: RPC=%#v UI=%#v", rpc, ui)
	}
	if l.RPCSpans[rpc.index].Entry == l.UISpans[ui.index].Entry {
		t.Fatalf("chosen identities share source entry %d", l.RPCSpans[rpc.index].Entry)
	}
	m.switchTimelineTier()
	if got := m.selectedTimelineIdentity(); got != rpc {
		t.Fatalf("restored RPC identity = %#v, want %#v", got, rpc)
	}
	m.switchTimelineTier()
	if got := m.selectedTimelineIdentity(); got != ui {
		t.Fatalf("restored UI identity = %#v, want %#v", got, ui)
	}
}

func TestTimelineTierSwitchRefreshesWarmedPresentationAndSource(t *testing.T) {
	l := testLog(t, "timing-tiers.log")
	m := update(t, New(l, "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	rpc := captureTimelineTierEvidence(t, &m)
	if rpc.tier != tierRPC || rpc.entry != l.RPCSpans[1].Entry || !strings.Contains(rpc.detail, "ReadResource") {
		t.Fatalf("warmed RPC evidence = %+v", rpc)
	}

	m.switchTimelineTier()
	ui := captureTimelineTierEvidence(t, &m)
	if ui.tier != tierUI || ui.entry != l.UISpans[1].Entry || !strings.Contains(ui.detail, "module.long_module_path_for_timing_details.aws_instance.second") {
		t.Fatalf("refreshed UI evidence = %+v", ui)
	}
	if reflect.DeepEqual(ui.labels, rpc.labels) || ui.hue == "" || ui.notes == rpc.notes || ui.width <= rpc.width {
		t.Fatalf("tier presentation did not change: RPC=%+v UI=%+v", rpc, ui)
	}

	m.switchTimelineTier()
	rpcAgain := captureTimelineTierEvidence(t, &m)
	if rpcAgain.entry != rpc.entry || !reflect.DeepEqual(rpcAgain.labels, rpc.labels) || rpcAgain.hue != rpc.hue || rpcAgain.notes != rpc.notes || rpcAgain.detail != rpc.detail || rpcAgain.width != rpc.width {
		t.Fatalf("RPC evidence after round trip = %+v, want %+v", rpcAgain, rpc)
	}
}

type timelineTierEvidence struct {
	tier   timelineTier
	labels []string
	hue    string
	notes  string
	detail string
	width  int
	entry  uint32
}

func captureTimelineTierEvidence(t *testing.T, m *Model) timelineTierEvidence {
	t.Helper()
	tier, tierSpans := m.timelineSpans()
	_, _ = m.timelineTiming()
	lanes := m.timelineLanes()
	labels, _ := m.timelineLaneLabels()
	_ = m.timelineWallClock()
	rows := timelineLaneRows(t, *m, lanes)
	if len(lanes) == 0 || len(rows) == 0 {
		t.Fatalf("tier %v has no lane", tier)
	}
	gotHue := barHueOf(t, rows[0])
	wantHue := barHueOf(t, hueOf(t, *m, tierSpans, lanes, 0).Render("x"))
	if gotHue != wantHue {
		t.Fatalf("tier %v first lane hue = %q, want %q for %q", tier, gotHue, wantHue, laneProvider(tierSpans, lanes[0]))
	}
	_, detailSections := m.selectedDetail(hugeWidth)
	spans, idx, ok := m.jumpTarget()
	if !ok {
		t.Fatalf("tier %v has no selected jump target", tier)
	}
	return timelineTierEvidence{
		tier: tier, labels: append([]string(nil), labels...), hue: gotHue,
		notes: strings.Join(m.timelineNotes(100), "\n"), detail: fmt.Sprint(detailSections),
		width: m.detailPaneNatural, entry: spans[idx].Entry,
	}
}

func TestTimelineTierReconcilesLazilyAfterFilteringAnotherView(t *testing.T) {
	l := testLog(t, "timing-tiers.log")
	m := update(t, New(l, "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m.moveTimelineSpan(1)
	m.changeView(ViewCalls)
	m.excludedFacets = map[string]map[string]bool{dimRPC: {"PlanResourceChange": true}}
	m.invalidateRows()
	if m.timelineLanesCached {
		t.Fatal("filtering the calls view packed timeline lanes")
	}

	m.changeView(ViewTimeline)
	if got := m.selectedTimelineIdentity(); got.kind != "rpc" || got.index != 1 {
		t.Fatalf("identity reconciled on activation = %#v, want RPC index 1", got)
	}
	spans, idx, ok := m.jumpTarget()
	if !ok || spans[idx].Entry != l.RPCSpans[1].Entry {
		t.Fatalf("reconciled source = ok %v entry %d, want %d", ok, spans[idx].Entry, l.RPCSpans[1].Entry)
	}
	m.switchTimelineTier()
	if got := m.selectedTimelineIdentity(); got.kind != "ui" || got.index != 1 {
		t.Fatalf("UI identity after lazy RPC reconciliation = %#v", got)
	}
}

func TestTimelineTierHistoryRestoresBothSelectionsAndSourceAfterChildChanges(t *testing.T) {
	l := testLog(t, "timing-tiers.log")
	m := update(t, New(l, "timing-tiers.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m.moveTimelineSpan(1)
	rpc := m.selectedTimelineIdentity()
	m.switchTimelineTier()
	ui := m.selectedTimelineIdentity()
	wantSelections := m.timeline.selections
	wantEntry := l.UISpans[ui.index].Entry

	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || len(m.history) != 1 {
		t.Fatalf("UI Enter child = view %v history %d", m.view, len(m.history))
	}
	m.excludedFacets = map[string]map[string]bool{dimRPC: {"ReadResource": true}}
	m.invalidateRows()
	m = update(t, m, tea.WindowSizeMsg{Width: 72, Height: 18})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewTimeline || m.activeTimelineTier() != tierUI || m.timeline.selections != wantSelections || m.selectedTimelineIdentity() != ui {
		t.Fatalf("restored timeline = view %v tier %v selections %#v identity %#v", m.view, m.activeTimelineTier(), m.timeline.selections, m.selectedTimelineIdentity())
	}
	spans, idx, ok := m.jumpTarget()
	if !ok || spans[idx].Entry != wantEntry {
		t.Fatalf("restored UI source = ok %v entry %d, want %d", ok, spans[idx].Entry, wantEntry)
	}
	m.switchTimelineTier()
	if got := m.selectedTimelineIdentity(); got != rpc || m.timeline.selections[tierUI] != ui {
		t.Fatalf("RPC switch restored %#v and UI memory %#v, want %#v/%#v", got, m.timeline.selections[tierUI], rpc, ui)
	}
	spans, idx, ok = m.jumpTarget()
	if !ok || spans[idx].Entry != l.RPCSpans[rpc.index].Entry {
		t.Fatalf("restored RPC source = ok %v entry %d, want %d", ok, spans[idx].Entry, l.RPCSpans[rpc.index].Entry)
	}
}

func TestTimelineTierSelectionFollowsOriginalIdentityThroughFiltering(t *testing.T) {
	m := update(t, New(testLog(t, "timing-tiers.log"), "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m.moveTimelineSpan(1)
	want := m.selectedTimelineIdentity()
	m.excludedFacets = map[string]map[string]bool{dimRPC: {"ReadResource": true}}
	m.invalidateRows()
	if got := m.selectedTimelineIdentity(); got != want {
		t.Fatalf("identity after earlier observation removed = %#v, want %#v", got, want)
	}

	m.excludedFacets = map[string]map[string]bool{dimRPC: {"PlanResourceChange": true}}
	m.invalidateRows()
	if got := m.selectedTimelineIdentity(); got.kind != "rpc" || got.index != 1 {
		t.Fatalf("fallback identity after selected observation removed = %#v, want RPC index 1", got)
	}

	m.excludedFacets = map[string]map[string]bool{dimRPC: {"ReadResource": true, "PlanResourceChange": true}}
	m.invalidateRows()
	if tier, spans := m.timelineSpans(); tier != tierRPC || len(spans) != 0 || m.selectedTimelineIdentity().kind != "" {
		t.Fatalf("empty explicit tier = tier %v, spans %d, identity %#v", tier, len(spans), m.selectedTimelineIdentity())
	}
}

func TestTimelineTierUnavailableNotice(t *testing.T) {
	cases := []struct {
		name, fixture, notice string
	}{
		{"rpc", "provider-rpc.log", "RPC timing only; UI timing unavailable"},
		{"ui", "structured-ui.log", "UI timing only; RPC timing unavailable"},
		{"empty", "core-only.log", "No RPC or UI timing observations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testLog(t, tc.fixture), tc.fixture)
			before := m.activeTimelineTier()
			m.switchTimelineTier()
			if m.activeTimelineTier() != before || m.timeline.notice != tc.notice {
				t.Fatalf("tier/notice = %v/%q, want %v/%q", m.activeTimelineTier(), m.timeline.notice, before, tc.notice)
			}
		})
	}
}

func TestTimelineTierPresentationFollowsActiveTier(t *testing.T) {
	m := update(t, New(testLog(t, "timing-tiers.log"), "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	rpcWidth := m.detailPaneNatural
	m.switchTimelineTier()
	_, sections := m.selectedDetail(hugeWidth)
	detail := fmt.Sprint(sections)
	if _, ok := m.laneOrder["google"]; !ok {
		t.Fatalf("UI lane palette = %#v", m.laneOrder)
	}
	if m.detailPaneNatural <= rpcWidth || !strings.Contains(detail, "module.long_module_path_for_timing_details.aws_instance.second") {
		t.Fatalf("UI presentation width/detail = %d/%q; RPC width %d", m.detailPaneNatural, detail, rpcWidth)
	}
}

func TestRestoreTimelineIdentityRejectsUnknownKind(t *testing.T) {
	m := update(t, New(testLog(t, "timing-tiers.log"), "timing-tiers.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if m.restoreTimelineIdentity(selectionIdentity{kind: "", index: 1}) {
		t.Fatal("empty identity restored a timeline observation")
	}
}
