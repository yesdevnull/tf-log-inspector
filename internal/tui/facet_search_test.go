package tui

import (
	"maps"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestResourceFacetNarrowsDisplayWithoutFilteringAndMapsOriginalChoice(t *testing.T) {
	l := &model.Log{
		UISpans: []span.Span{
			{Address: "aws_instance.a", ResourceType: "aws_instance"},
			{Address: "aws_instance.b", ResourceType: "aws_instance"},
		},
		Contexts: []attrib.Context{{Address: "aws_instance.c"}},
	}
	m := New(l, "synthetic.log")
	setFacetCursor(t, &m, dimResource, "aws_instance.a")
	m.pane = PaneFacets
	before := m.selectedResources()
	m.facetSearch.dimension = dimResource
	m.facetSearch.query = "aws_instance.b"
	indices := m.visibleFacetIndices(dimResource)
	if len(indices) != 1 || m.facetValues(dimResource)[indices[0]].Value != "aws_instance.b" {
		t.Fatalf("visible resource indices = %v, want only the original B index", indices)
	}
	if m.resourceSelection.Addresses != nil || m.resourceSelection.Modules != nil {
		t.Fatalf("chooser query changed named selection: %+v", m.resourceSelection)
	}
	m.invalidateRows()
	after := m.selectedResources()
	if !slices.Equal(before.RPCIndices, after.RPCIndices) || before.UI != after.UI {
		t.Fatal("chooser query changed the selected resource projection")
	}
	m.clampFacetCursor()
	m.toggleFacetValue()
	want := map[string]bool{"aws_instance.a": true, "aws_instance.c": true}
	if !maps.Equal(m.resourceSelection.Addresses, want) {
		t.Fatalf("address allowlist = %v, want hidden A and C retained while B is toggled", m.resourceSelection.Addresses)
	}
	m.facetSearch.query = ""
	for _, value := range []string{"aws_instance.a", "aws_instance.c"} {
		setFacetCursor(t, &m, dimResource, value)
		m.toggleFacetValue()
	}
	if m.resourceSelection.Addresses == nil || len(m.resourceSelection.Addresses) != 0 {
		t.Fatalf("unticking every address = %v, want explicit empty allowlist", m.resourceSelection.Addresses)
	}
	if projection := m.selectedResources(); len(projection.RPCIndices) != 0 || projection.UI.Count != 0 {
		t.Fatalf("empty address allowlist admitted timing evidence: %+v", projection)
	}
}

func TestResourceFacetSoloIncludesHiddenChoices(t *testing.T) {
	l := &model.Log{UISpans: []span.Span{
		{Address: "aws_instance.a", ResourceType: "aws_instance"},
		{Address: "aws_instance.b", ResourceType: "aws_instance"},
	}}
	m := New(l, "synthetic.log")
	setFacetCursor(t, &m, dimResource, "aws_instance.a")
	m.facetSearch = facetSearchState{dimension: dimResource, query: ".a"}
	m.soloFacetValue()
	if !maps.Equal(m.resourceSelection.Addresses, map[string]bool{"aws_instance.a": true}) {
		t.Fatalf("solo address allowlist = %v", m.resourceSelection.Addresses)
	}
	m.soloFacetValue()
	if m.resourceSelection.Addresses != nil {
		t.Fatalf("repeated solo did not restore unconstrained selection: %v", m.resourceSelection.Addresses)
	}
}

func TestResourceFacetSingleChoiceSoloIsExplicit(t *testing.T) {
	one := New(&model.Log{
		RPCSpans: []span.Span{{ResourceType: "aws_instance", DurationMs: 7}},
		UISpans:  []span.Span{{Address: "aws_instance.only", ResourceType: "aws_instance"}},
	}, "one.log")
	setFacetCursor(t, &one, dimResource, "aws_instance.only")
	one.soloFacetValue()
	if !maps.Equal(one.resourceSelection.Addresses, map[string]bool{"aws_instance.only": true}) {
		t.Fatalf("single-choice solo = %v, want explicit singleton", one.resourceSelection.Addresses)
	}
	projection := one.selectedResources()
	if len(projection.RPCIndices) != 0 || projection.Selection.Baseline.Count != 1 || projection.Selection.Unresolved.Count != 1 {
		t.Fatalf("single-choice projection = %+v, want unresolved RPC retained as evidence but excluded from selected indices", projection)
	}
}

func TestModuleFacetCountsDistinctAddressesInStructuralSubtrees(t *testing.T) {
	l := &model.Log{UISpans: []span.Span{
		{Address: "aws_instance.root"},
		{Address: "module.app.aws_instance.a"},
		{Address: "module.app.aws_instance.a"},
		{Address: "module.app.module.child.aws_instance.b"},
		{Address: "module.application.aws_instance.c"},
	}, Contexts: []attrib.Context{{Address: "module.app.module.child.aws_instance.context"}}}
	m := New(l, "synthetic.log")
	got := map[string]int{}
	for _, v := range m.facetValues(dimModule) {
		got[v.Value] = v.Count
	}
	want := map[string]int{"": 5, "module.app": 3, "module.app.module.child": 2, "module.application": 1}
	if !maps.Equal(got, want) {
		t.Fatalf("module distinct-address counts = %v, want %v", got, want)
	}
	if displayFacetValue(dimModule, "") != "(root subtree)" {
		t.Fatalf("root display label = %q", displayFacetValue(dimModule, ""))
	}
}

func TestModuleFacetNaturalWidthMeasuresTheRootDisplayLabel(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{{Address: "x.a"}}}, "synthetic.log")
	want := facetValueNaturalWidth("(root subtree)", facetCountWidth(m.facets))
	if m.facetPaneNatural < want {
		t.Fatalf("facet natural width = %d, want at least %d for the displayed root label", m.facetPaneNatural, want)
	}
}

func TestFacetTypesUnionUITierWithoutAddingUIProviders(t *testing.T) {
	l := &model.Log{
		RPCSpans: []span.Span{{Provider: "registry/rpc", RPC: "Read", ResourceType: "rpc_type"}},
		UISpans:  []span.Span{{Provider: "registry/ui", RPC: "Create", ResourceType: "ui_type", Address: "ui_type.a"}},
	}
	m := New(l, "synthetic.log")
	if got := facetValueNames(m.facetValues(dimType)); !slices.Equal(got, []string{"rpc_type", "ui_type"}) {
		t.Fatalf("type facet = %v, want RPC and UI types", got)
	}
	if got := facetValueNames(m.facetValues(dimProvider)); !slices.Equal(got, []string{"registry/rpc"}) {
		t.Fatalf("provider facet = %v, want RPC tier only", got)
	}
	if got := facetValueNames(m.facetValues(dimRPC)); !slices.Equal(got, []string{"Read"}) {
		t.Fatalf("RPC facet = %v, want RPC tier only", got)
	}
}

func TestFacetSearchEditorControlsAndNoMatchAreLocal(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{{Address: "aws_instance.a"}, {Address: "aws_instance.b"}}}, "synthetic.log")
	setFacetCursor(t, &m, dimResource, "aws_instance.a")
	m.pane = PaneFacets
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.facetSearch.editing {
		t.Fatal("slash did not start facet search")
	}
	for _, r := range []rune{'q', '3', 'i', 'e', ' ', '界'} {
		keyType := tea.KeyRunes
		if r == ' ' {
			keyType = tea.KeySpace
		}
		m.Update(tea.KeyMsg{Type: keyType, Runes: []rune{r}})
	}
	if got := m.facetSearch.query; got != "q3ie 界" {
		t.Fatalf("facet query = %q, want command keys inserted literally", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.facetSearch.query; got != "q3ie " {
		t.Fatalf("query after Unicode backspace = %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.facetSearch.editing || m.facetSearch.query != "q3ie " {
		t.Fatalf("Enter did not retain query: %+v", m.facetSearch)
	}
	before := m.resourceSelection
	m.beginFacetSearch()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.facetSearch.query != "q3ie " || !maps.Equal(before.Addresses, m.resourceSelection.Addresses) {
		t.Fatalf("cancel did not restore previous query without selection: %+v", m.facetSearch)
	}
	m.facetSearch.query = "no match"
	m.clampFacetCursor()
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if m.resourceSelection.Addresses != nil {
		t.Fatalf("no-match chooser changed selection: %v", m.resourceSelection.Addresses)
	}
	m.setFacetExclusions(dimType, map[string]bool{"aws_instance": true})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.facetSearch.query != "" || len(m.excludedFacets) == 0 {
		t.Fatalf("first Esc did not clear only the chooser query: query=%q filters=%v", m.facetSearch.query, m.excludedFacets)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.excludedFacets) != 0 {
		t.Fatalf("second Esc did not clear filters: %v", m.excludedFacets)
	}
	m.beginFacetSearch()
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Fatal("Ctrl-C did not quit from the facet editor")
	}
}

func TestFacetSearchSurvivesFocusOverlayAndResize(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{{Address: "aws_instance.a"}}}, "synthetic.log")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	setFacetCursor(t, &m, dimResource, "aws_instance.a")
	m.beginFacetSearch()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if m.facetSearch.query != "a" {
		t.Fatalf("focus or resize lost facet query: %+v", m.facetSearch)
	}
}

func TestFacetSearchRetainedQueryRemainsVisibleAndNamesItsEscapeAction(t *testing.T) {
	m := New(&model.Log{UISpans: []span.Span{{Address: "aws_instance.a"}, {Address: "aws_instance.b"}}}, "synthetic.log")
	setFacetCursor(t, &m, dimResource, "aws_instance.a")
	m.pane = PaneFacets
	m.beginFacetSearch()
	for _, r := range "aws_instance.a" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := unstyled(m.renderFacets(60, 20)); !strings.Contains(got, "RESOURCES /aws_instance.a") {
		t.Fatalf("retained chooser query is not visible in its facet heading:\n%s", got)
	}
	if got := m.actionKeys(100); !strings.Contains(got, "Esc query") {
		t.Fatalf("active chooser query has no specific Esc hint: %q", got)
	}
}

func TestRawSlashStillStartsRawSearch(t *testing.T) {
	m := New(&model.Log{}, "synthetic.log")
	m.setView(ViewRawLog)
	m.pane = PaneList
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.raw.searching || m.facetSearch.editing {
		t.Fatalf("raw slash state: raw=%v facet=%v", m.raw.searching, m.facetSearch.editing)
	}
}

func setFacetCursor(t *testing.T, m *Model, dim, value string) {
	t.Helper()
	for d, f := range m.facets {
		if f.Name != dim {
			continue
		}
		for v := range f.Values {
			if f.Values[v].Value == value {
				m.facetCursor = facetCursor{dim: d, val: v}
				return
			}
		}
	}
	t.Fatalf("facet value %q/%q not found", dim, value)
}

func facetValueNames(values []model.FacetValue) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = values[i].Value
	}
	return result
}
