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
)

const responseRecoveryHead = "2026-09-11T00:00:00.000Z [DEBUG] provider."

func loadedResponseModel(t *testing.T, source string) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "response-recovery.log")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(l, path)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.setView(ViewRawLog)
	m.pane = PaneList
	return m
}

func TestResponsePhysicalPositionSelectsLineWithinBroadEntry(t *testing.T) {
	source := responseRecoveryHead + `a: {"selected":"first"}` + "\n" +
		responseRecoveryHead + `a: {"selected":"second"}` + "\n"
	m := loadedResponseModel(t, source)
	m.log.Entries = []logfmt.Entry{{
		Len:         uint32(len(source)),
		Lines:       2,
		Timestamped: true,
		Comp:        m.log.Entries[0].Comp,
	}}
	m.raw.topLine = 1
	m.raw.column = 37
	m.raw.match = &rawMatch{entry: 0, line: 1, text: literalPosition{byteOffset: 7, column: 7}}

	responseKeyAndDrain(t, &m, "r")
	out := m.renderResponse(80, 8)
	if !strings.Contains(out, `"selected": "second"`) || strings.Contains(out, `"selected": "first"`) {
		t.Fatalf("response at the second physical line = %q", out)
	}
}

func TestResponseRecoveryOpensCompleteAndInvalidPositions(t *testing.T) {
	source := responseRecoveryHead + `a: {"selected":"first"}` + "\n" +
		responseRecoveryHead + `b: {"source-sentinel":]}` + "\n" +
		responseRecoveryHead + `a: {"selected":"second"}` + "\n"
	m := loadedResponseModel(t, source)

	m.raw.top = 2
	m.raw.topLine = 0
	m.raw.column = 9
	m.raw.query = "raw query"
	m.raw.lastQuery = "raw repeat"
	m.raw.scope = []int{0, 1, 2}
	m.raw.match = &rawMatch{entry: 2, line: 0, text: literalPosition{byteOffset: 4, column: 4}}
	m.excludedFacets = map[string]map[string]bool{dimLevel: {"INFO": true}}
	m.history = append(m.history, m.captureNavigation())
	beforeRaw := m.raw
	beforeFilter := cloneExclusions(m.excludedFacets)
	beforeHistory := slices.Clone(m.history)

	responseKeyAndDrain(t, &m, "r")
	if !strings.Contains(m.response.notice, "Partial reconstruction") {
		t.Fatal("recovered body lacks the capture qualification")
	}
	if got := m.responseTitle(); !strings.Contains(got, "RECONSTRUCTED RESPONSE (1 fragments)") || !strings.Contains(got, "partial capture") {
		t.Fatalf("recovered response title = %q", got)
	}
	out := m.renderResponse(60, 3)
	if !strings.Contains(out, `"selected": "second"`) || strings.Contains(out, `"selected": "first"`) {
		t.Fatalf("second recovered body = %q", out)
	}
	responseKey(&m, "j")
	if !strings.Contains(m.renderResponse(60, 3), "Partial reconstruction") {
		t.Fatal("qualification scrolled out of view")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !reflect.DeepEqual(m.raw, beforeRaw) || !reflect.DeepEqual(m.excludedFacets, beforeFilter) || !reflect.DeepEqual(m.history, beforeHistory) {
		t.Fatal("response changed raw return state, filters or navigation history")
	}

	m.raw.top = 0
	responseKeyAndDrain(t, &m, "r")
	out = m.renderResponse(80, 8)
	if !strings.Contains(out, `"selected": "first"`) || strings.Contains(out, `"selected": "second"`) {
		t.Fatalf("first recovered body = %q", out)
	}
	responseKeyAndDrain(t, &m, "r")

	m.raw.top = 1
	responseKeyAndDrain(t, &m, "r")
	out = m.renderResponse(100, 8)
	for _, want := range []string{
		"This response is incomplete or invalid.",
		"Source line 2.",
		"provider JSON at line 2:",
		"Raw Log remains available. Press Esc or r to return.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("invalid response omitted %q: %q", want, out)
		}
	}
	if strings.Contains(out, "source-sentinel") || m.responseTitle() != "RESPONSE STATUS" {
		t.Fatalf("invalid response disclosed source or used body title: %q / %q", out, m.responseTitle())
	}
}

func TestResponseUnavailableAndGlobalOwnershipStatuses(t *testing.T) {
	t.Run("local stream", func(t *testing.T) {
		source := responseRecoveryHead + `a: {"private-sentinel":]}` + "\n" +
			responseRecoveryHead + "a: ordinary continuation\n"
		m := loadedResponseModel(t, source)
		m.raw.top = 1
		responseKeyAndDrain(t, &m, "r")
		out := m.renderResponse(100, 8)
		for _, want := range []string{
			"This stream is unavailable after an earlier failure.",
			"Source line 2.",
			"provider JSON at line 1:",
			"Raw Log remains available. Press Esc or r to return.",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("unavailable response omitted %q: %q", want, out)
			}
		}
		if strings.Contains(out, "private-sentinel") || m.responseTitle() != "RESPONSE STATUS" {
			t.Fatalf("unavailable response disclosed source or used body title: %q / %q", out, m.responseTitle())
		}
	})

	t.Run("global ownership", func(t *testing.T) {
		source := responseRecoveryHead + `a: {"pending":` + "\n" +
			"2026-09-11T00:00:01.000Z [DEBUG] provider.: {\"global-sentinel\":1}\n" +
			responseRecoveryHead + `b: {"later-sentinel":1}` + "\n"
		m := loadedResponseModel(t, source)
		m.raw.top = 1
		responseKeyAndDrain(t, &m, "r")
		out := m.renderResponse(100, 8)
		for _, want := range []string{
			"Response ownership is unavailable at this position.",
			"Source line 2.",
			"provider JSON at line 2: ambiguous ownership",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("ownership response omitted %q: %q", want, out)
			}
		}
		if strings.Contains(out, "global-sentinel") || strings.Contains(out, "later-sentinel") {
			t.Fatalf("ownership response disclosed source: %q", out)
		}
	})
}

func TestResponsePositionUsesFirstDisplayedPhysicalLine(t *testing.T) {
	t.Run("filtered top entry", func(t *testing.T) {
		source := "2026-09-11T00:00:00.000Z [INFO] terraform: filtered\n" +
			responseRecoveryHead + `a: {"selected":"visible"}` + "\n"
		m := loadedResponseModel(t, source)
		m.excludedFacets = map[string]map[string]bool{dimLevel: {"INFO": true}}
		responseKeyAndDrain(t, &m, "r")
		if out := m.renderResponse(80, 8); !strings.Contains(out, `"selected": "visible"`) {
			t.Fatalf("response did not follow the first displayed entry: %q", out)
		}
	})

	for _, tc := range []struct {
		name   string
		scope  []int
		filter bool
	}{
		{name: "empty scope", scope: []int{}},
		{name: "empty filter", filter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := loadedResponseModel(t, responseRecoveryHead+`a: {"hidden":1}`+"\n")
			m.raw.scope = tc.scope
			if tc.filter {
				m.excludedFacets = map[string]map[string]bool{dimLevel: {"DEBUG": true}}
			}
			responseKeyAndDrain(t, &m, "r")
			out := m.renderResponse(80, 8)
			if !strings.Contains(out, "No reconstructed response at this position.") || strings.Contains(out, "hidden") {
				t.Fatalf("empty raw pane response = %q", out)
			}
			if got := m.log.ReconstructionQuality(); got.State != "complete" {
				t.Fatalf("empty raw pane did not complete the explicit whole-capture check: %+v", got)
			}
		})
	}

	t.Run("invalid top line skips top entry", func(t *testing.T) {
		source := responseRecoveryHead + `a: {"selected":"skipped"}` + "\n" +
			responseRecoveryHead + `b: {"selected":"next"}` + "\n"
		m := loadedResponseModel(t, source)
		m.raw.topLine = 99
		responseKeyAndDrain(t, &m, "r")
		out := m.renderResponse(80, 8)
		if !strings.Contains(out, `"selected": "next"`) || strings.Contains(out, `"selected": "skipped"`) {
			t.Fatalf("invalid top line response = %q", out)
		}
	})
}

func TestResponsePhysicalLinesDoNotSelectNeighbouringResponses(t *testing.T) {
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"event","@timestamp":"2026-09-11T00:00:00Z","type":"apply_complete"}`
	tests := []struct {
		name   string
		source string
		line   int
	}{
		{
			name: "ordinary continuation after complete response",
			source: responseRecoveryHead + `a: {"verified":1}` + "\n" +
				"ordinary continuation\n",
			line: 1,
		},
		{
			name: "inline UI-only physical line",
			source: responseRecoveryHead + `a: {"verified":"joined-` + "\n" + ui + "\n" +
				responseRecoveryHead + `a: body"}` + "\n",
			line: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := loadedResponseModel(t, tc.source)
			entry := 0
			line := tc.line
			for i := range m.log.Entries {
				location, ok := m.log.SourceLocation(uint32(i))
				if ok && uint64(tc.line+1) >= location.StartLine && uint64(tc.line+1) <= location.EndLine {
					entry, line = i, tc.line+1-int(location.StartLine)
					break
				}
			}
			m.raw.top, m.raw.topLine = entry, line
			responseKeyAndDrain(t, &m, "r")
			out := m.renderResponse(80, 8)
			if !strings.Contains(out, "No reconstructed response at this physical line.") || strings.Contains(out, "verified") {
				t.Fatalf("non-provider physical line selected a neighbour: %q", out)
			}
		})
	}
}

func TestResponseRecoveryIncludesFragmentsOutsideRawScope(t *testing.T) {
	source := responseRecoveryHead + `a: {"selected":"inside-` + "\n" +
		responseRecoveryHead + `b: {"damaged":]}` + "\n" +
		responseRecoveryHead + `a: outside"}` + "\n"
	m := loadedResponseModel(t, source)
	m.raw.scope = []int{0}
	before := slices.Clone(m.raw.scope)
	responseKeyAndDrain(t, &m, "r")
	out := m.renderResponse(100, 8)
	if !strings.Contains(out, `"selected": "inside-outside"`) {
		t.Fatalf("scoped response omitted verified fragments: %q", out)
	}
	responseKeyAndDrain(t, &m, "r")
	if !slices.Equal(m.raw.scope, before) {
		t.Fatalf("response changed scope: %v", m.raw.scope)
	}
}

func TestResponseNoticeLayoutPreservesBodyAndSearchState(t *testing.T) {
	source := responseRecoveryHead + `a: {"@message":"first needle\n界界界 needle second \u001b[2J needle third"}` + "\n" +
		responseRecoveryHead + `b: {"damaged":]}` + "\n"
	m := loadedResponseModel(t, source)
	responseKeyAndDrain(t, &m, "r")
	responseKey(&m, "/")
	responseKey(&m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.response.match == nil {
		t.Fatal("recovered response search found no first occurrence")
	}
	responseKey(&m, "n")
	if m.response.match == nil {
		t.Fatal("recovered response search found no second occurrence")
	}
	match := *m.response.match
	query := m.response.query
	column := m.response.column

	for _, width := range []int{20, 60, 100} {
		if got := m.renderResponse(width, 1); got != clipWidth(m.response.notice, width) {
			t.Errorf("width %d one-line notice = %q", width, got)
		}
	}
	for _, height := range []int{0, -1} {
		if got := m.renderResponse(60, height); got != "" {
			t.Errorf("height %d response = %q, want empty", height, got)
		}
	}
	if got := m.renderResponse(60, 2); !strings.HasPrefix(got, "Partial reconstruction") || strings.Count(got, "\n") != 1 {
		t.Fatalf("two-line response = %q", got)
	}
	if got := m.renderResponse(60, 8); !strings.Contains(unstyled(got), "needle second") || reversedText(got) != "needle" {
		t.Fatalf("enlarged response did not restore searched body: %q", got)
	}
	if got := m.renderResponse(60, 1); reversedText(got) != "" {
		t.Fatalf("notice-only response highlighted notice text: %q", got)
	}
	if m.response.match == nil || *m.response.match != match || m.response.query != query || m.response.column != column {
		t.Fatal("notice-only layout discarded response search state")
	}
	responseKey(&m, "n")
	if got := m.renderResponse(60, 8); !strings.Contains(unstyled(got), `\x1b[2J needle third`) || reversedText(got) != "needle" {
		t.Fatalf("search did not advance across decoded controls: %q", got)
	}
}

func TestResponseUnavailableSelectionWithoutDiagnosticIsSafe(t *testing.T) {
	selection := model.ProviderResponseSelection{State: "unavailable", SourceLine: 99}
	text, status := responsePresentation(selection)
	if !status || text != "Response details are unavailable.\n\nRaw Log remains available. Press Esc or r to return." {
		t.Fatalf("malformed unavailable selection = status %v, text %q", status, text)
	}
}
