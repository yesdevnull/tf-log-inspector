package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestDrillDownProviderPreservesTypeAndRestoresParent(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	setFacetCursor(t, &m, dimType, "aws_instance")
	m.pane = PaneFacets
	pressRune(t, &m, 'o')
	pressRune(t, &m, '1')
	m.pane = PaneList
	before := m.filter()
	provider := m.selectedRPCSpans()[0].Provider
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewCalls {
		t.Fatalf("view = %v, want Calls", m.view)
	}
	for _, s := range m.selectedRPCSpans() {
		if s.Provider != provider || s.ResourceType != "aws_instance" {
			t.Fatalf("broadened drill-down: %+v", s)
		}
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewProviders || !reflect.DeepEqual(m.filter(), before) {
		t.Fatal("provider parent selection was not restored")
	}
}

func TestDrillDownProviderReplacesOnlyProviderSelection(t *testing.T) {
	m := New(parsedDrillDownLog(t), "drilldown.log")
	m.setFacetExclusions(dimProvider, map[string]bool{"provider-c": true})
	m.setFacetExclusions(dimType, map[string]bool{"type-two": true})
	m.changeView(ViewProviders)
	selectAggregate(t, &m, "provider", "provider-a")
	before := m.filter()

	if !m.openAggregate() {
		t.Fatal("provider row did not open")
	}
	if got := m.selectedResources().RPCIndices; !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("RPC indices = %v, want provider-a/type-one indices [0 1]", got)
	}
	m.returnFromHistory()
	if !reflect.DeepEqual(m.filter(), before) {
		t.Fatal("provider A+B selection was not restored")
	}
}

func TestDrillDownProviderFromUnconstrainedSelectionPreservesMultipleTypes(t *testing.T) {
	m := New(parsedDrillDownLog(t), "drilldown.log")
	m.changeView(ViewProviders)
	selectAggregate(t, &m, "provider", "provider-a")
	if m.excludedFacets != nil {
		t.Fatal("fresh model unexpectedly has facet exclusions")
	}

	m.openAggregate()
	if got := m.selectedResources().RPCIndices; !slices.Equal(got, []int{0, 1, 2}) {
		t.Fatalf("RPC indices = %v, want every provider-a type [0 1 2]", got)
	}
}

func TestDrillDownTypeRoutesBySelectedObservationTier(t *testing.T) {
	t.Run("UI type opens resources", func(t *testing.T) {
		m := New(testLog(t, "resources-modules.log"), "resources-modules.log")
		m.changeView(ViewTypes)
		selectAggregate(t, &m, "type", "aws_instance")
		if !m.openAggregate() || m.view != ViewResources {
			t.Fatalf("view = %v, want Resources", m.view)
		}
	})

	t.Run("zero duration RPC opens calls", func(t *testing.T) {
		m := New(&model.Log{RPCSpans: []span.Span{{Provider: "provider-a", ResourceType: "type-zero", DurationMs: 0, TimestampStatus: logfmt.TimestampValid}}}, "zero.log")
		m.changeView(ViewTypes)
		if !m.openAggregate() || m.view != ViewCalls {
			t.Fatalf("view = %v, want Calls", m.view)
		}
	})

	t.Run("filtered RPC leaves UI route", func(t *testing.T) {
		l := &model.Log{
			RPCSpans: []span.Span{{Provider: "provider-a", ResourceType: "shared", DurationMs: 10, TimestampStatus: logfmt.TimestampValid}},
			UISpans:  []span.Span{{ResourceType: "shared", Address: "shared.one", DurationMs: 1000, TimestampStatus: logfmt.TimestampValid}},
		}
		m := New(l, "mixed.log")
		m.setFacetExclusions(dimProvider, map[string]bool{"provider-a": true})
		m.changeView(ViewTypes)
		if !m.openAggregate() || m.view != ViewResources {
			t.Fatalf("view = %v, want Resources", m.view)
		}
	})
}

func TestDrillDownUnavailableMetadataUsesNoneFacetKey(t *testing.T) {
	for _, kind := range []string{"provider", "type"} {
		t.Run(kind, func(t *testing.T) {
			l := &model.Log{RPCSpans: []span.Span{{DurationMs: 1, TimestampStatus: logfmt.TimestampValid}}}
			m := New(l, "missing.log")
			if kind == "provider" {
				m.changeView(ViewProviders)
			} else {
				m.changeView(ViewTypes)
			}
			row, ok := m.selectedRow()
			if !ok || row.identity.value != "(none)" {
				t.Fatalf("identity = %+v, want existing (none) key", row.identity)
			}
			if !m.openAggregate() || len(m.selectedRPCSpans()) != 1 {
				t.Fatal("missing metadata observation was not admitted")
			}
		})
	}
}

func TestDrillDownEnterIsInertWithoutAggregateListSelection(t *testing.T) {
	cases := []Model{
		New(&model.Log{}, "empty.log"),
		New(testLog(t, "resources-accounting.log"), "resources-accounting.log"),
	}
	cases[0].changeView(ViewProviders)
	cases[1].changeView(ViewProviders)
	cases[1].selected = len(cases[1].rows())
	for i := range cases {
		before := cases[i].captureNavigation()
		if cases[i].openAggregate() {
			t.Fatalf("case %d opened without a valid row", i)
		}
		if cases[i].view != before.view || len(cases[i].history) != 0 {
			t.Fatalf("case %d changed navigation state", i)
		}
	}

	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.changeView(ViewProviders)
	m.pane = PaneFacets
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewProviders || len(m.history) != 0 {
		t.Fatal("Enter outside the list opened an aggregate")
	}
}

func TestDrillDownEnterHintMatchesAggregateRoute(t *testing.T) {
	provider := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	provider.changeView(ViewProviders)
	if got := provider.enterHint(); got != "↵ calls" {
		t.Fatalf("provider hint = %q", got)
	}
	uiType := New(testLog(t, "resources-modules.log"), "resources-modules.log")
	uiType.changeView(ViewTypes)
	if got := uiType.enterHint(); got != "↵ resources" {
		t.Fatalf("UI type hint = %q", got)
	}
	call := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	if got := call.enterHint(); got != openHint {
		t.Fatalf("call hint = %q, want %q", got, openHint)
	}
	call.changeView(ViewResources)
	if got := call.enterHint(); got != "↵ operations" {
		t.Fatalf("resource hint = %q, want operations", got)
	}
	if got := provider.actionKeys(100); !strings.Contains(got, "↵ calls") {
		t.Fatalf("provider footer omits route: %q", got)
	}
}

func TestDrillDownTypeRestoresParentAfterChildEditsAndResize(t *testing.T) {
	m := New(parsedDrillDownLog(t), "drilldown.log")
	m.changeView(ViewTypes)
	m.sortCol[ViewTypes] = 0
	selectAggregate(t, &m, "type", "type-two")
	before := m.captureNavigation()
	beforeFilter := m.filter()
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewCalls {
		t.Fatalf("view = %v, want Calls", m.view)
	}
	m.cycleSort()
	m.setFacetExclusions(dimProvider, map[string]bool{"provider-a": true})
	m.invalidateRows()
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	if len(m.rows()) != 0 || !strings.Contains(unstyled(m.View()), "Esc goes back") {
		t.Fatal("empty child does not explain that Esc returns")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != before.view || m.sortCol != before.sortCol || m.selectedIdentity() != before.identity || !reflect.DeepEqual(m.filter(), beforeFilter) {
		t.Fatal("type parent state was not restored after child edits")
	}
	if m.width != 60 || m.height != 9 {
		t.Fatalf("restored stale dimensions %dx%d", m.width, m.height)
	}
}

func TestDrillDownShortTypesFrameShowsSelectedRouteTarget(t *testing.T) {
	m := New(testLog(t, "resources-modules.log"), "resources-modules.log")
	pressRune(t, &m, '2')
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	out := unstyled(m.View())
	if !strings.Contains(out, "aws_instance") {
		t.Fatalf("selected type is hidden at 60x9:\n%s", out)
	}
	if !strings.Contains(out, "↵ resources") {
		t.Fatalf("visible type has no Resources route hint at 60x9:\n%s", out)
	}
}

func TestDrillDownShortTypesFrameKeepsTheActiveSortColumn(t *testing.T) {
	m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
	m.changeView(ViewTypes)
	for sortCol := range typeColumns {
		m.sortCol[ViewTypes] = sortCol
		m.invalidateRows()
		cols, _, visibleSort := visibleTypeColumns(typeColumns, m.rows(), sortCol, 58)
		if visibleSort < 0 || visibleSort >= len(cols) || cols[visibleSort] != typeColumns[sortCol] {
			t.Fatalf("sort column %q was dropped from narrow table", typeColumns[sortCol].header)
		}
		if out := unstyled(m.renderList(58, 3)); !strings.Contains(out, "aws_instance") {
			t.Fatalf("sort column %q clipped the selected type below 12 columns:\n%s", typeColumns[sortCol].header, out)
		}
	}

	rows := []row{{
		cells:   []string{"abcdefghijkl", "1", "12345678901234567890", "1", "1", "1"},
		numeric: []uint64{0, 1, 1, 1, 1, 1},
	}}
	cols, visibleRows, visibleSort := visibleTypeColumns(typeColumns, rows, 1, 43)
	out := unstyled(renderTable(nil, cols, visibleSort, visibleRows, "", 0, true, 43, 2))
	if !strings.Contains(out, "abcdefghijkl") {
		t.Fatalf("active sort marker consumed the promised 12-column identity:\n%s", out)
	}
}

func TestDrillDownProviderCallRawResponseRoundTrip(t *testing.T) {
	m := New(parsedDrillDownResponseLog(t), "response-drilldown.log")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pressRune(t, &m, '1')
	providerParent := m.captureNavigation()
	providerFilter := m.filter()
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewCalls || len(m.history) != 1 {
		t.Fatalf("provider drill-down = view %v, depth %d", m.view, len(m.history))
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog || len(m.history) != 2 {
		t.Fatalf("call drill-down = view %v, depth %d", m.view, len(m.history))
	}
	responseEntry := -1
	for _, entry := range m.raw.scope {
		if strings.Contains(string(m.log.Bytes(m.log.Entries[entry])), `{"@message"`) {
			responseEntry = entry
			break
		}
	}
	if responseEntry < 0 {
		t.Fatal("scoped raw log does not contain the parsed response entry")
	}
	m.raw.top = responseEntry
	m.raw.topLine, m.raw.column = 0, 7
	m.raw.query, m.raw.lastQuery = "response", "provider"
	m.raw.match = &rawMatch{entry: m.raw.top, line: 0, text: literalPosition{byteOffset: 1, column: 1}}
	rawParent := cloneRawState(m.raw)
	pressRune(t, &m, 'r')
	if !m.response.open || !strings.Contains(m.renderCentre(100, 20), "composed response") {
		t.Fatalf("real reconstructed response did not open:\n%s", m.renderCentre(100, 20))
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.response.open || m.raw.top != rawParent.top || m.raw.topLine != rawParent.topLine || m.raw.column != rawParent.column || m.raw.query != rawParent.query || m.raw.lastQuery != rawParent.lastQuery || m.raw.match == nil || *m.raw.match != *rawParent.match {
		t.Fatal("response dismissal changed raw position or search state")
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewCalls || len(m.history) != 1 {
		t.Fatalf("raw return = view %v, depth %d", m.view, len(m.history))
	}
	pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewProviders || len(m.history) != 0 || m.selectedIdentity() != providerParent.identity || !reflect.DeepEqual(m.filter(), providerFilter) {
		t.Fatal("calls return did not restore provider parent")
	}
}

func parsedDrillDownLog(t *testing.T) *model.Log {
	t.Helper()
	const content = `# SYNTHETIC drill-down fixture; all values invented.
2026-09-10T00:00:00.000Z [TRACE] provider-a: Received downstream response: tf_req_id=aaaaaaaa-0000-4000-8000-000000000001 tf_resource_type=type-one tf_rpc=ReadResource tf_provider_addr=provider-a tf_req_duration_ms=11
2026-09-10T00:00:01.000Z [TRACE] provider-a: Received downstream response: tf_req_id=aaaaaaaa-0000-4000-8000-000000000002 tf_resource_type=type-one tf_rpc=PlanResourceChange tf_provider_addr=provider-a tf_req_duration_ms=12
2026-09-10T00:00:02.000Z [TRACE] provider-a: Received downstream response: tf_req_id=aaaaaaaa-0000-4000-8000-000000000003 tf_resource_type=type-two tf_rpc=ReadResource tf_provider_addr=provider-a tf_req_duration_ms=13
2026-09-10T00:00:03.000Z [TRACE] provider-b: Received downstream response: tf_req_id=bbbbbbbb-0000-4000-8000-000000000001 tf_resource_type=type-one tf_rpc=ReadResource tf_provider_addr=provider-b tf_req_duration_ms=21
2026-09-10T00:00:04.000Z [TRACE] provider-c: Received downstream response: tf_req_id=cccccccc-0000-4000-8000-000000000001 tf_resource_type=type-one tf_rpc=ReadResource tf_provider_addr=provider-c tf_req_duration_ms=31
`
	path := filepath.Join(t.TempDir(), "drilldown.log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func parsedDrillDownResponseLog(t *testing.T) *model.Log {
	t.Helper()
	const content = `# SYNTHETIC response drill-down fixture; all values invented.
2026-09-10T00:00:00.000Z [TRACE] provider.example: Received downstream response: tf_req_id=aaaaaaaa-0000-4000-8000-000000000001 tf_resource_type=example_item tf_rpc=ReadResource tf_provider_addr=provider-example tf_req_duration_ms=11
2026-09-10T00:00:00.100Z [DEBUG] provider.example: {"@message":"composed response"} tf_req_id=aaaaaaaa-0000-4000-8000-000000000001
`
	path := filepath.Join(t.TempDir(), "response-drilldown.log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func selectAggregate(t *testing.T, m *Model, kind, value string) {
	t.Helper()
	for i, row := range m.rows() {
		if row.identity.kind == kind && row.identity.value == value {
			m.selected = i
			return
		}
	}
	t.Fatalf("no %s row %q", kind, value)
}
