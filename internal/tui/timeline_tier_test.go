package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	m.switchTimelineTier()
	if got := m.selectedTimelineIdentity(); got != rpc {
		t.Fatalf("restored RPC identity = %#v, want %#v", got, rpc)
	}
	m.switchTimelineTier()
	if got := m.selectedTimelineIdentity(); got != ui {
		t.Fatalf("restored UI identity = %#v, want %#v", got, ui)
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
