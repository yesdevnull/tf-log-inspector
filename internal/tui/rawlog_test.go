package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// The whole point: from a slow call, land on the log lines that produced it.
func TestEnterJumpsFromACallToItsLogEntry(t *testing.T) {
	m := callsModel(t, "provider-rpc.log", "x.log")
	rows := m.rows()
	if len(rows) < 2 {
		t.Fatal("need at least two calls to prove the jump targets the selected one")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ActiveView() != ViewRawLog {
		t.Fatalf("enter did not switch to the raw log, view = %v", m.ActiveView())
	}
	// The pane opens on the SCOPE's first member (see jumpToSpan), not
	// jumpContextLines above the call's own entry. provider-rpc.log's two
	// calls are each a single standalone "Received downstream response"
	// line, with no other traffic anywhere in the file sharing its id, so
	// the scope this call opens on holds only its own entry and the pane
	// lands exactly there. The assertion only needs "at or before" though --
	// what this test actually pins is that the call's own line is among the
	// lines actually drawn, which the check below this one covers.
	want := int(m.log.RPCSpans[rows[1].spanIdx].Entry)
	if m.TopEntry() > want {
		t.Errorf("TopEntry = %d, past the selected row's span at %d", m.TopEntry(), want)
	}
	// Matched on a PREFIX of the entry's first line. The drawn line is
	// clipped to the pane's width and has had its trailing carriage return
	// taken off, so it is not the entry's bytes and comparing whole would
	// be comparing the render against something the pane never draws.
	head := strings.SplitN(strings.TrimRight(string(m.log.Bytes(m.log.Entries[want])), "\n"), "\n", 2)[0]
	const prefix = 60
	if len(head) < prefix {
		t.Fatalf("fixture assumption changed: the call's line is only %d characters", len(head))
	}
	head = head[:prefix]
	drawn := rawLogBody(m, 200, 40)
	if !slices.ContainsFunc(drawn, func(ln string) bool { return strings.HasPrefix(ln, head) }) {
		t.Errorf("the pane does not draw the call's own line, which opens %q:\n%s", head, strings.Join(drawn, "\n"))
	}
}

// An entry's byte range covers its continuation lines, so a multi-line entry
// renders whole rather than as a fragment. multiline-body.log's HTTP
// response is four physical lines, and the response body sits on the third
// of them: the body is the content a user who jumped to a slow call came to
// read, so each line is named individually rather than counted. A count
// cannot tell a whole entry from one that lost some of its middle.
func TestRawLogRendersWholeMultiLineEntries(t *testing.T) {
	m := update(t, New(testLog(t, "multiline-body.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	out := m.renderRawLog(200, 40)
	for _, want := range []string{
		"HTTP Response Received",                 // the entry's own header line
		"http.response.body=",                    // its first continuation
		`| {"__type"`,                            // the body itself, two lines in
		"http.response.header.x_amzn_requestid=", // its last continuation
	} {
		if !strings.Contains(out, want) {
			t.Errorf("raw log dropped %q from a four-line entry:\n%s", want, out)
		}
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
// ownership: an entry is that provider's traffic (and disappears with it) as
// long as it shares a component with one of that provider's spans, whether
// or not the entry itself closes a span. two-providers.log's two spans sit
// on two different components, so unticking aws must drop its own line and
// keep google's.
func TestRawLogProviderFacetFiltersByComponent(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = focusFacets(t, m) // cursor starts on the provider dimension's first (aws) value
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	out := m.renderRawLog(200, 100)
	if strings.Contains(out, "aws_subnet") {
		t.Errorf("aws's own entry survived after unticking the aws provider facet:\n%s", out)
	}
	if !strings.Contains(out, "google_compute_instance") {
		t.Errorf("google's entry went missing after unticking the aws provider facet:\n%s", out)
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

	m = moveFacetCursorTo(t, m, dimRPC, "ApplyResourceChange")
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.renderRawLog(200, 100); got != before {
		t.Errorf("selecting an rpc facet narrowed the raw log:\nbefore:\n%s\nafter:\n%s", before, got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace}) // deselect it again

	m = moveFacetCursorTo(t, m, dimType, "aws_subnet")
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if got := m.renderRawLog(200, 100); got != before {
		t.Errorf("selecting a resource type facet narrowed the raw log:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

// manyEntryLog builds a log of n single-line entries. Every fixture in
// testdata is shorter than one page, so paging over one cannot be told from
// paging that does nothing: both leave the top entry clamped where it was.
func manyEntryLog(n int) *model.Log {
	var data []byte
	entries := make([]logfmt.Entry, n)
	for i := range entries {
		line := fmt.Sprintf("2026-09-04T10:00:00.000+1000 [INFO] entry-%03d\n", i)
		entries[i] = logfmt.Entry{Off: uint64(len(data)), Len: uint32(len(line)), Lines: 1, Timestamped: true}
		data = append(data, line...)
	}
	return &model.Log{Data: data, Entries: entries}
}

// PgDown and PgUp move the raw log by a whole screenful, which is what makes
// a long capture navigable at all: a real log runs to tens of thousands of
// entries, and one 'j' at a time will not cross it.
func TestRawLogPagesByAScreenful(t *testing.T) {
	m := update(t, New(manyEntryLog(3*rawLogPageSize), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if got := m.TopEntry(); got != rawLogPageSize {
		t.Errorf("TopEntry = %d after PgDown, want a whole page of %d", got, rawLogPageSize)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if got := m.TopEntry(); got != 2*rawLogPageSize {
		t.Errorf("TopEntry = %d after a second PgDown, want %d", got, 2*rawLogPageSize)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if got := m.TopEntry(); got != rawLogPageSize {
		t.Errorf("TopEntry = %d after PgUp, want it back at %d", got, rawLogPageSize)
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

// Enter on a rollup row (every ViewProviders row is one) has no span to jump
// to, so it must be inert rather than jumping to entry 0. Enter asks
// row.isCall, the same predicate the detail pane asks before reading a
// span's fields, so the two cannot disagree about which rows carry one.
func TestEnterOnARollupRowIsInert(t *testing.T) {
	m := update(t, New(testLog(t, "mixed-hcp.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ActiveView() != ViewProviders {
		t.Errorf("enter on a rollup row changed the view to %v", m.ActiveView())
	}
}

// '/' opens a synchronous search over the raw log and jumps to the first
// entry at or after the current position whose text contains the query. The
// entry AT that position counts, since it is the one on the pane's top line
// and a query for text the user can already see must find it. Only entries
// the active filter shows are candidates, and the match is against the same
// ANSI-stripped text the pane draws rather than the entry's raw bytes.
//
// The spec sizes /pattern with n/N as a cancellable goroutine because it was
// designed against a 1GB log. Real captures measured for this tool are
// 17-37MB, where a synchronous scan over m.log.Data costs a few tens of
// milliseconds -- well under a frame -- so the concurrent, cancellable
// version is deferred until a log turns up large enough to need it -- the
// same trade this project makes wherever a simpler synchronous read is fast
// enough for the log sizes that actually exist.
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

func TestRawSearchRevealsContinuationOccurrences(t *testing.T) {
	text := "header\n" + strings.Repeat("padding\n", 300) + "needle first needle second\ntail\n"
	l := &model.Log{Data: []byte(text), Entries: []logfmt.Entry{{Len: uint32(len(text))}}}
	m := New(l, "synthetic.log")
	m.setView(ViewRawLog)
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "needle")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.TopLine() != 301 || !strings.Contains(m.renderRawLog(13, 1), "needle first") {
		t.Fatalf("search did not reveal the continuation: line=%d, body=%q", m.TopLine(), m.renderRawLog(13, 1))
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.TopLine() != 301 || !strings.Contains(m.renderRawLog(13, 1), "needle second") {
		t.Fatalf("next occurrence was skipped: %q", m.renderRawLog(13, 1))
	}
	before := m.renderRawLog(13, 1)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if !m.raw.notFound || m.renderRawLog(13, 1) != before {
		t.Fatal("end of search moved or wrapped the viewport")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if !strings.Contains(m.renderRawLog(13, 1), "needle first") {
		t.Fatal("reverse search skipped the first occurrence")
	}
}

func TestRawSearchTraversesEveryVisibleOccurrence(t *testing.T) {
	first := "hit one xx hit two\ncontinuation hit three\n"
	second := "hit four\n"
	l := &model.Log{Data: []byte(first + second), Entries: []logfmt.Entry{
		{Len: uint32(len(first))},
		{Off: uint64(len(first)), Len: uint32(len(second))},
	}}
	m := update(t, New(l, "synthetic.log"), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setView(ViewRawLog)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "hit")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	for _, want := range []string{"hit one", "hit two", "hit three", "hit four"} {
		if got := m.renderRawLog(12, 1); !strings.Contains(got, want) {
			t.Fatalf("search rendered %q, want occurrence %q", got, want)
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	}
	if !m.raw.notFound {
		t.Fatal("search wrapped after the final occurrence")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if got := m.renderRawLog(12, 1); !strings.Contains(got, "hit three") {
		t.Fatalf("reverse search rendered %q, want the preceding occurrence", got)
	}
}

func TestRawSearchUsesRenderedCellsAndText(t *testing.T) {
	t.Run("wide prefix and embedded ANSI", func(t *testing.T) {
		text := "界" + strings.Repeat("x", 30) + "nee\x1b[31mdle\n"
		l := &model.Log{Data: []byte(text), Entries: []logfmt.Entry{{Len: uint32(len(text))}}}
		m := New(l, "synthetic.log")
		m.setView(ViewRawLog)
		m.raw.lastQuery = "needle"
		if !m.searchFrom(0, true, true) || !strings.Contains(m.renderRawLog(8, 1), "needle") {
			t.Fatalf("search did not reveal the rendered word: %q", m.renderRawLog(8, 1))
		}
		if got, want := m.raw.column, 32; got != want {
			t.Errorf("horizontal column = %d, want %d terminal cells", got, want)
		}
	})

	t.Run("visible control escape", func(t *testing.T) {
		text := "before\tafter\n"
		l := &model.Log{Data: []byte(text), Entries: []logfmt.Entry{{Len: uint32(len(text))}}}
		m := New(l, "synthetic.log")
		m.setView(ViewRawLog)
		m.raw.lastQuery = `\t`
		if !m.searchFrom(0, true, true) || !strings.Contains(m.renderRawLog(10, 1), `\tafter`) {
			t.Fatalf("search did not find the visible tab escape: %q", m.renderRawLog(10, 1))
		}
		m.raw.lastQuery = "\t"
		if m.searchFrom(0, true, true) {
			t.Fatal("search matched a raw control byte absent from rendered text")
		}
	})
}

func TestRawSearchRevealsTheContainingGrapheme(t *testing.T) {
	for _, tc := range []struct{ text, query string }{
		{"e\u0301", "\u0301"},
		{"👩‍💻", "💻"},
	} {
		data := []byte(tc.text + strings.Repeat("x", 100) + "\n")
		m := New(&model.Log{Data: data, Entries: []logfmt.Entry{{Len: uint32(len(data))}}}, "synthetic.log")
		m.setView(ViewRawLog)
		m.raw.lastQuery = tc.query
		if !m.searchFrom(0, true, true) || !strings.Contains(m.renderRawLog(10, 1), tc.text) {
			t.Errorf("query %q hid grapheme %q: %q", tc.query, tc.text, m.renderRawLog(10, 1))
		}
	}
}

func TestRawSearchLoadsAndRevealsAContinuationLine(t *testing.T) {
	path := t.TempDir() + "/continuation.log"
	text := "2026-09-09T10:00:00.000+1000 [INFO] header\ncontinuation needle\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	l, err := model.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := update(t, New(l, "continuation.log"), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setView(ViewRawLog)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "needle")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.TopLine() != 1 || !strings.Contains(m.renderRawLog(30, 1), "needle") {
		t.Fatalf("parser-backed search did not reveal continuation: line=%d, body=%q", m.TopLine(), m.renderRawLog(30, 1))
	}
}

func TestRawSearchRestartsFromTheViewportAfterScrolling(t *testing.T) {
	t.Run("vertical", func(t *testing.T) {
		text := "hit first\nhit second\n"
		l := &model.Log{Data: []byte(text), Entries: []logfmt.Entry{{Len: uint32(len(text))}}}
		m := New(l, "synthetic.log")
		m.setView(ViewRawLog)
		m.raw.lastQuery = "hit"
		if !m.searchFrom(0, true, true) {
			t.Fatal("initial search missed")
		}
		m.scrollRawLog(1)
		m.searchAgain(-1)
		if got := m.renderRawLog(12, 1); !strings.Contains(got, "hit second") {
			t.Fatalf("reverse search resumed from the stale match: %q", got)
		}
	})

	t.Run("horizontal", func(t *testing.T) {
		text := "hit early xxxxxxxxxx hit later\n"
		l := &model.Log{Data: []byte(text), Entries: []logfmt.Entry{{Len: uint32(len(text))}}}
		m := update(t, New(l, "synthetic.log"), tea.WindowSizeMsg{Width: 20, Height: 12})
		m.setView(ViewRawLog)
		m.raw.lastQuery = "hit"
		if !m.searchFrom(0, true, true) {
			t.Fatal("initial search missed")
		}
		m.scrollRawLogHorizontally(10)
		m.searchAgain(1)
		if got := m.renderRawLog(10, 1); !strings.Contains(got, "hit later") {
			t.Fatalf("forward search resumed from the stale match: %q", got)
		}
	})
}

func TestRawSearchHandlesEmptyDomains(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  *model.Log
	}{
		{"empty log", &model.Log{}},
		{"one entry", &model.Log{Data: []byte("no match\n"), Entries: []logfmt.Entry{{Len: 9}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(tc.log, "synthetic.log")
			m.setView(ViewRawLog)
			m.raw.lastQuery = "needle"
			for _, forward := range []bool{true, false} {
				if m.searchFrom(0, forward, true) {
					t.Fatalf("forward=%v search found text absent from the domain", forward)
				}
			}
		})
	}

	text := "needle\n"
	m := New(&model.Log{Data: []byte(text), Entries: []logfmt.Entry{{Len: uint32(len(text)), Level: logfmt.LevelInfo}}}, "synthetic.log")
	m.setView(ViewRawLog)
	m.setFacetExclusions(dimLevel, map[string]bool{"INFO": true})
	m.invalidateRows()
	m.raw.lastQuery = "needle"
	if m.searchFrom(0, true, true) {
		t.Fatal("search found an entry hidden by an empty filter result")
	}
}

func TestRawSearchDiscardsAnchorsWhenItsDomainChanges(t *testing.T) {
	t.Run("facet change", func(t *testing.T) {
		first, second := "needle info\n", "needle warning\n"
		l := &model.Log{Data: []byte(first + second), Entries: []logfmt.Entry{
			{Len: uint32(len(first)), Level: logfmt.LevelInfo},
			{Off: uint64(len(first)), Len: uint32(len(second)), Level: logfmt.LevelWarn},
		}}
		m := New(l, "synthetic.log")
		m.setView(ViewRawLog)
		m.raw.lastQuery = "needle"
		if !m.searchFrom(0, true, true) {
			t.Fatal("initial search missed")
		}
		m.setFacetExclusions(dimLevel, map[string]bool{"INFO": true})
		m.invalidateRows()
		m.searchAgain(-1)
		if m.raw.notFound || !strings.Contains(m.renderRawLog(20, 1), "needle warning") {
			t.Fatalf("search reused an anchor from before the facet change: %q", m.renderRawLog(20, 1))
		}
	})

	t.Run("scope removal", func(t *testing.T) {
		first, second := "needle first\n", "needle scoped\n"
		l := &model.Log{Data: []byte(first + second), Entries: []logfmt.Entry{
			{Len: uint32(len(first))},
			{Off: uint64(len(first)), Len: uint32(len(second))},
		}}
		m := New(l, "synthetic.log")
		m.setView(ViewRawLog)
		m.raw.scope, m.raw.top = []int{1}, 1
		m.raw.lastQuery = "needle"
		if !m.searchFrom(1, true, true) {
			t.Fatal("initial scoped search missed")
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
		m.searchAgain(-1)
		if m.raw.notFound || !strings.Contains(m.renderRawLog(20, 1), "needle scoped") {
			t.Fatalf("search reused an anchor from before scope removal: %q", m.renderRawLog(20, 1))
		}
	})
}

// A search match hidden by the active facet filter must not be jumped to --
// the raw log is filtered, and search must respect what is currently shown.
func TestSlashSearchHonoursActiveFilter(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = focusFacets(t, m) // widens the terminal so the facet pane is drawn
	showOnly(t, &m, dimProvider, "registry.terraform.io/hashicorp/aws")
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

// footerOf returns the composed view's footer: the key hints' two lines, or
// the search prompt's one line while a search is open or has just failed.
//
// View always places a blank line above the footer, so that blank line
// tells the two shapes apart: it survives as the line before last only when
// the footer beneath it is the one-line message, since a two-line footer's
// own first line -- never blank, views is never empty -- stands there
// instead. That assumes the view-key line itself is never clipped down to
// nothing, which holds at every width these tests render at.
func footerOf(view string) string {
	lines := strings.Split(unstyled(view), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(lines) > 2 && strings.HasSuffix(last, "q quit") {
		return lines[1] + "\n" + last
	}
	return last
}

// rawLogView returns a model showing the raw log at a known terminal size.
func rawLogView(t *testing.T, fixture string) Model {
	t.Helper()
	m := update(t, New(testLog(t, fixture), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
}

// typeQuery sends each rune of q as its own key press, the way a user types
// into the search prompt.
func typeQuery(t *testing.T, m Model, q string) Model {
	t.Helper()
	for _, r := range q {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// '/' captures every subsequent key, so without a prompt the keyboard has
// silently stopped doing what it did a moment ago and the user is typing
// into a void.
func TestSearchPromptShowsTheQueryBeingTyped(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "aws")
	if got := strings.TrimSpace(footerOf(m.View())); got != "/aws" {
		t.Errorf("footer while searching = %q, want the prompt %q", got, "/aws")
	}
}

// A search that matches nothing leaves the position unchanged, which is
// indistinguishable from a search that matched the entry already on screen.
// The one has to be reported, and the report has to clear once a later
// search succeeds.
func TestFailedSearchIsReportedAndClearsOnTheNextMatch(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "no-such-text-anywhere")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Errorf("footer after a failed search = %q, want it to report the miss", got)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "aws_internet_gateway")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); got != m.keyHints(100) {
		t.Errorf("footer after a successful search = %q, want the key hints back", got)
	}
}

// In the alt screen Ctrl+C arrives as a key rather than a signal, so a
// search prompt that swallows every key must still honour it: otherwise the
// only way out of a prompt opened by accident is to guess Esc.
func TestCtrlCQuitsFromTheSearchPrompt(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlC}); !m.Quitting() {
		t.Error("Ctrl+C while the search prompt is open did not quit")
	}
}

// The spec binds up/down and j/k to "move" in every view. In the raw log
// nothing reads the row selection -- the pane renders from TopEntry -- so
// moving has to move the pane's top entry, clamped the same way paging is.
func TestArrowKeysScrollTheRawLog(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}); m.TopEntry() != 1 {
		t.Errorf("TopEntry = %d after one 'j', want 1", m.TopEntry())
	}
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}); m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after 'k' back to the top, want 0", m.TopEntry())
	}
	if m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}); m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after 'k' at the top, want it clamped to 0", m.TopEntry())
	}
	for i := 0; i < 500; i++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.TopEntry() >= len(m.log.Entries) {
		t.Errorf("TopEntry = %d ran past %d entries", m.TopEntry(), len(m.log.Entries))
	}
}

// Log.Bytes returns a line's ORIGINAL bytes, and Terraform's own plan
// output in a captured log is colourised. Those bytes are untrusted: an
// escape sequence that moves the cursor or clears the screen would corrupt
// the whole frame rather than just its own line, and a colour left unreset
// bleeds into the panes beside it.
func TestRawLogStripsANSIFromLogLines(t *testing.T) {
	data := []byte("2026-09-04T10:00:00.000+1000 [INFO] \x1b[1m\x1b[32m+ create\x1b[0m\x1b[2J aws_instance.example\n")
	l := &model.Log{Data: data, Entries: []logfmt.Entry{{Off: 0, Len: uint32(len(data))}}}
	m := New(l, "x.log")
	m.view = ViewRawLog
	out := m.renderRawLog(200, 10)
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("raw log pane emitted an escape sequence from the log's own bytes: %q", out)
	}
	if !strings.Contains(out, "+ create") {
		t.Errorf("stripping the escapes took the line's text with it: %q", out)
	}
}

// A search must match the same text the pane renders, not the entry's
// original bytes: an escape sequence sitting in the middle of a colourised
// phrase is invisible on screen, so a query for that phrase has to find it
// even though the escape splits it in the underlying bytes.
func TestSlashSearchMatchesAPhraseAnEscapeSequenceSplits(t *testing.T) {
	data := []byte("2026-09-04T10:00:00.000+1000 [INFO] aws\x1b[0m_instance.example: Creation complete\n")
	l := &model.Log{Data: data, Entries: []logfmt.Entry{{Off: 0, Len: uint32(len(data))}}}
	m := New(l, "x.log")
	m.view = ViewRawLog
	m.raw.lastQuery = "aws_instance"
	if !m.searchFrom(0, true, true) {
		t.Errorf("search did not find %q, split only by an escape sequence the screen does not show:\n%s", m.raw.lastQuery, m.renderRawLog(200, 10))
	}
}

// n and N repeat the last submitted search forward and backward. Without
// them a search is a single jump: the second occurrence of a string is
// unreachable except by scrolling to it by hand. provider-rpc.log's two
// calls both name ApplyResourceChange, so there is a second match to reach
// and a first to come back to.
func TestNAndShiftNRepeatTheSearchForwardAndBack(t *testing.T) {
	const query = "tf_rpc=ApplyResourceChange"
	m := rawLogView(t, "provider-rpc.log")
	var matches []int
	for i, e := range m.log.Entries {
		if strings.Contains(string(m.log.Bytes(e)), query) {
			matches = append(matches, i)
		}
	}
	if len(matches) != 2 {
		t.Fatalf("fixture assumption changed: %q is in %d entries, want exactly 2 so a repeat has somewhere to go", query, len(matches))
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, query)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.TopEntry(); got != matches[0] {
		t.Fatalf("TopEntry = %d after the search, want the first match %d", got, matches[0])
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if got := m.TopEntry(); got != matches[1] {
		t.Errorf("TopEntry = %d after 'n', want the next match %d", got, matches[1])
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if got := m.TopEntry(); got != matches[0] {
		t.Errorf("TopEntry = %d after 'N', want the previous match %d", got, matches[0])
	}
}

// The search prompt is a line editor, not just a key sink: backspace removes
// the last rune typed, and space extends the query rather than being taken
// as the facet-toggle binding it is everywhere else. A prompt that cannot
// correct a typo or hold a phrase is a prompt the user has to abandon and
// reopen.
func TestSearchPromptEditsTheQuery(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "awz")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = typeQuery(t, m, "s")
	m = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = typeQuery(t, m, "subnet")
	if got, want := strings.TrimSpace(footerOf(m.View())), "/aws subnet"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
}

// Esc cancels the prompt without searching, and hands the keyboard back: the
// keys typed after it are commands again, not more of an abandoned query.
func TestEscCancelsTheSearchPrompt(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	before := m.TopEntry()
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "aws_internet_gateway")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := footerOf(m.View()); got != m.keyHints(100) {
		t.Errorf("footer = %q after Esc, want the key hints back", got)
	}
	if m.TopEntry() != before {
		t.Errorf("TopEntry = %d after cancelling the prompt, want the position left at %d", m.TopEntry(), before)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := m.TopEntry(); got != before+1 {
		t.Errorf("TopEntry = %d after 'j' following a cancelled prompt, want %d -- the prompt is still capturing keys", got, before+1)
	}
}

// Enter on an empty query closes the prompt without running a search. There
// is no pattern to look for, and reporting "pattern not found" for a search
// the user never made describes nothing -- while hiding the key hints in the
// one view where n and N matter most.
func TestEnterOnAnEmptyQueryClosesThePromptWithoutSearching(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); got != m.keyHints(100) {
		t.Errorf("footer = %q after Enter on an empty query, want the key hints", got)
	}
	if m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after Enter on an empty query, want the position left at 0", m.TopEntry())
	}
}

// Esc with nothing selected changes no filter, so the raw log's "pattern not
// found" still describes the search on screen and must stand. Clearing it
// would put the key hints back over a miss the user can still see the
// consequences of.
func TestEscWithNoFiltersLeavesTheMissReportStanding(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "no-such-text-anywhere")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Fatalf("footer = %q, want the miss reported before Esc is pressed", got)
	}
	if len(m.excludedFacets) != 0 {
		t.Fatalf("excludedFacets = %v, want nothing unticked so Esc has no filter to clear", m.excludedFacets)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Errorf("footer = %q after an Esc that cleared nothing, want the miss still reported", got)
	}
}

// '/' hands the keyboard to a text search over the raw log, and only there:
// the prompt captures every subsequent key, so opening it over a ranked
// table would take the keyboard away with nothing on screen for the search
// to run against.
//
// The test is that 'q' still quits, not that the footer shows no prompt:
// the footer only ever draws a prompt in the raw log (see Model.footer), so
// a prompt opened over a table would capture the keyboard with nothing
// whatsoever on screen to say so. What the user would see is a 'q' that
// stopped working.
func TestSlashOpensTheSearchPromptOnlyInTheRawLog(t *testing.T) {
	for _, key := range []rune{'1', '2', '4'} {
		m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		if m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); !m.Quitting() {
			t.Errorf("view %q: 'q' after '/' did not quit -- a prompt captured the keyboard outside the raw log", key)
		}
	}
}

// componentProviders reads Span.Entry, which nothing revalidates when a Log
// is assembled, so it must guard the index itself: a span pointing past the
// entries contributes nothing rather than indexing off the end.
func TestComponentProvidersSkipsASpanPointingPastTheLog(t *testing.T) {
	entries := []logfmt.Entry{{Comp: 7}}
	spans := []span.Span{
		{Provider: "registry.terraform.io/hashicorp/aws", Entry: 0},
		{Provider: "registry.terraform.io/hashicorp/google", Entry: 99},
	}
	got := componentProviders(spans, entries)
	if len(got) != 1 || got[7] != "registry.terraform.io/hashicorp/aws" {
		t.Errorf("componentProviders = %v, want only component 7 mapped to aws", got)
	}
}

// jumpToSpan takes a row's spanIdx, and must guard it at both ends: a row
// index past the last span has no span behind it, so the jump is a no-op
// rather than a read off the end of RPCSpans. -1 (every rollup row) is
// covered by TestEnterOnARollupRowIsInert.
func TestJumpToSpanIgnoresARowIndexPastTheLastSpan(t *testing.T) {
	m := New(testLog(t, "provider-rpc.log"), "x.log")
	// Any view but the raw log will do; naming one states what "left alone"
	// is measured against rather than leaning on whatever New defaults to.
	m.view = ViewProviders
	m.jumpToSpan(m.log.RPCSpans, len(m.log.RPCSpans))
	if m.ActiveView() != ViewProviders {
		t.Errorf("view = %v after a jump to a row index past the last span, want it left alone", m.ActiveView())
	}
	if m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after a jump to a row index past the last span, want 0", m.TopEntry())
	}
}

// Span.Entry indexes the same log's Entries, but nothing revalidates it when
// a Log is assembled, so jumpToSpan must guard the index itself rather than
// trust it: an out-of-range Entry must leave the model exactly where it was
// rather than index past the end of Entries.
func TestJumpToSpanIgnoresAnOutOfRangeEntryIndex(t *testing.T) {
	l := &model.Log{
		Entries:  []logfmt.Entry{{}},
		RPCSpans: []span.Span{{RPC: "ApplyResourceChange", Provider: "aws", Entry: 99}},
	}
	m := New(l, "x.log")
	m.view = ViewProviders
	m.jumpToSpan(l.RPCSpans, 0)
	if m.ActiveView() != ViewProviders {
		t.Errorf("view = %v after a jump to an out-of-range entry, want it left alone", m.ActiveView())
	}
	if m.TopEntry() != 0 {
		t.Errorf("TopEntry = %d after a jump to an out-of-range entry, want 0", m.TopEntry())
	}
}

// The search prompt and its miss describe the raw log, the only view '/'
// searches, so leaving that view puts the key hints back rather than
// reporting a search whose result is no longer on screen.
func TestSearchStateIsNotReportedOutsideTheRawLog(t *testing.T) {
	m := rawLogView(t, "provider-rpc.log")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "no-such-text-anywhere")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Fatalf("footer = %q, want the miss reported in the raw log", got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if got := footerOf(m.View()); got != m.keyHints(100) {
		t.Errorf("footer in the calls view = %q, want the key hints", got)
	}
}

// A newly submitted search must be able to match the entry at the top of
// the pane. Enter on a slow call puts the entry that closed its span on the
// pane's first line; a '/' for a string in that very entry has to find it
// rather than report "pattern not found" with the text on screen.
//
// n and N must still advance past it, or they would return the match
// already shown for ever instead of moving to the next one, so both halves
// are pinned here: the same query, from the same position, must match on
// submission and miss on the repeat.
func TestSlashSearchMatchesTheEntryAtTheTopOfThePane(t *testing.T) {
	const query = "aws_internet_gateway"
	m := rawLogView(t, "provider-rpc.log")
	target, matches := -1, 0
	for i, e := range m.log.Entries {
		if strings.Contains(string(m.log.Bytes(e)), query) {
			matches++
			if target < 0 {
				target = i
			}
		}
	}
	if matches != 1 || target <= 0 {
		t.Fatalf("fixture assumption changed: %q is in %d entries, first at %d -- want exactly one, below the first entry", query, matches, target)
	}
	for m.TopEntry() < target {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, query)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); strings.Contains(got, "not found") {
		t.Errorf("footer = %q -- the search missed text sitting on the pane's first line", got)
	}
	if m.TopEntry() != target {
		t.Errorf("TopEntry = %d after the search, want the matching entry %d", m.TopEntry(), target)
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Errorf("footer = %q after 'n' -- the repeat must advance past the match already shown, and there is no other", got)
	}
	if m.TopEntry() != target {
		t.Errorf("TopEntry = %d after a failed 'n', want it left at %d", m.TopEntry(), target)
	}
}

// "pattern not found" is a cached derivation of the last query, the
// position it searched from and the filter it searched under. Anything that
// moves one of those makes it a claim about a search that no longer
// describes what is on screen -- and while it stands, the footer's key
// hints are hidden in the one view where n and N matter most.
func TestFailedSearchReportClearsWhenItsPositionOrFilterMoves(t *testing.T) {
	for _, c := range []struct {
		name string
		act  func(t *testing.T, m Model) Model
	}{
		{"scrolling", func(t *testing.T, m Model) Model {
			return update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		}},
		{"paging", func(t *testing.T, m Model) Model {
			return update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
		}},
		{"toggling a facet", func(t *testing.T, m Model) Model {
			return update(t, focusFacets(t, m), tea.KeyMsg{Type: tea.KeySpace})
		}},
		{"clearing filters", func(t *testing.T, m Model) Model {
			m = update(t, focusFacets(t, m), tea.KeyMsg{Type: tea.KeySpace})
			return update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := rawLogView(t, "two-providers.log")
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			m = typeQuery(t, m, "no-such-text-anywhere")
			m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if got := footerOf(m.View()); !strings.Contains(got, "not found") {
				t.Fatalf("footer = %q, want the miss reported before %s moves it", got, c.name)
			}
			after := c.act(t, m)
			if got := footerOf(after.View()); strings.Contains(got, "not found") {
				t.Errorf("footer = %q after %s, want the key hints back -- the miss describes a search that has moved on", got, c.name)
			}
		})
	}
}

// An entry belonging to no provider -- Terraform's own core lines, plan
// output, or a provider whose address the log never named -- resolves to
// the empty provider, and the facet pane offers exactly that as "(none)".
// Matching the raw "" instead makes that checkbox select nothing, the same
// disagreement between what a dimension OFFERS and what it MATCHES that
// model.FacetKey exists to close.
func TestEntryVisibleMatchesAComponentlessEntryAgainstTheNoneFacet(t *testing.T) {
	f := model.Filter{Providers: map[string]bool{model.FacetKey(""): true}}
	core := logfmt.Entry{} // Comp 0: no component, so no provider
	if !entryVisible(f, map[uint16]string{}, core) {
		t.Errorf("an entry with no provider is hidden while %q is the selected provider facet", model.FacetKey(""))
	}
	named := model.Filter{Providers: map[string]bool{"registry.terraform.io/hashicorp/aws": true}}
	if entryVisible(named, map[uint16]string{}, core) {
		t.Errorf("an entry with no provider survived a filter asking for one provider's traffic")
	}
}

func TestTheRawLogSaysWhenAFilterHasHiddenEverything(t *testing.T) {
	m := update(t, New(testLog(t, "provider-rpc.log"), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m.setFacetExclusions(dimLevel, map[string]bool{"UNKNOWN": true, "TRACE": true})
	m.invalidateRows()

	out := m.renderRawLog(200, 40)
	for _, want := range []string{"nothing matches the filter", "Esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("an emptied raw log does not say %q, so it looks like a parse failure: %q", want, out)
		}
	}
}

func TestRawLogFilterMovesToAnAdmittedEntry(t *testing.T) {
	data := []byte("first\nsecond\nthird\n")
	l := &model.Log{Data: data, Entries: []logfmt.Entry{
		{Off: 0, Len: 6, Level: logfmt.LevelWarn},
		{Off: 6, Len: 7, Level: logfmt.LevelInfo},
		{Off: 13, Len: 6, Level: logfmt.LevelWarn},
	}}
	for _, tc := range []struct {
		name  string
		scope []int
		top   int
		level string
		want  string
	}{
		{name: "match before cursor", top: 2, level: "INFO", want: "second"},
		{name: "match after cursor", top: 0, level: "INFO", want: "second"},
		{name: "current entry still admitted", top: 2, level: "WARN", want: "third"},
		{name: "scoped earlier match", scope: []int{0, 1}, top: 1, level: "WARN", want: "first"},
		{name: "all scoped entries hidden", scope: []int{0, 2}, top: 2, level: "INFO", want: "no filter match in this call"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(l, "x.log")
			m.setView(ViewRawLog)
			m.raw.scope, m.raw.top = tc.scope, tc.top
			showOnly(t, &m, dimLevel, tc.level)
			if out := unstyled(m.renderRawLog(80, 1)); !strings.HasPrefix(out, tc.want) {
				t.Errorf("filtered raw log starts with %q, want %q", out, tc.want)
			}
		})
	}
}

func TestDroppingAnEmptyScopeRevealsEarlierFilterMatches(t *testing.T) {
	data := []byte("first\nsecond\nthird\n")
	l := &model.Log{Data: data, Entries: []logfmt.Entry{
		{Off: 0, Len: 6, Level: logfmt.LevelInfo},
		{Off: 6, Len: 7, Level: logfmt.LevelWarn},
		{Off: 13, Len: 6, Level: logfmt.LevelWarn},
	}}
	m := New(l, "x.log")
	m.setView(ViewRawLog)
	m.raw.scope, m.raw.top = []int{1, 2}, 2
	showOnly(t, &m, dimLevel, "INFO")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
	if out := unstyled(m.renderRawLog(80, 3)); out != "first" {
		t.Errorf("widened raw log = %q, want the earlier INFO entry", out)
	}
}

// The same emptiness with no filter to blame is a log with no entries at
// all -- the raw log's top entry is clamped inside the log, so there is no
// other way to reach it -- and saying "nothing matches the filter" there
// would send the user after a filter that was never set.
func TestAnEmptyRawLogWithNoFilterDoesNotBlameAFilter(t *testing.T) {
	m := New(&model.Log{}, "x.log")
	out := m.renderRawLog(80, 10)
	if !strings.Contains(out, "no entries") {
		t.Errorf("a log with no entries renders %q, which says nothing about why the pane is empty", out)
	}
	if strings.Contains(out, "filter") {
		t.Errorf("an unfiltered empty raw log blames a filter that was never set: %q", out)
	}
}

// Enter on the slow call the user came to investigate must not land them on
// a blank pane or on some other call's lines further down the log. The raw
// log renders from its top entry DOWNWARD through the active filter, so a
// jump to an entry the filter hides is indistinguishable from a jump that
// worked -- and the filter that hides it is invisible from the calls table.
//
// A level facet is what can do this: the level dimension narrows entries and
// not spans (see levelFacet), so the call stays in the table while its own
// log entry disappears. provider-rpc.log's calls close on TRACE entries and
// its comment header is UNKNOWN, so narrowing to UNKNOWN hides every call's
// entry while leaving both calls on screen to press Enter over.
func TestEnterRefusesAJumpTheFilterWouldHide(t *testing.T) {
	m := callsModel(t, "provider-rpc.log", "x.log")
	m = focusFacets(t, m) // widens the terminal so the facet pane is drawn
	showOnly(t, &m, dimLevel, "UNKNOWN")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}) // hand the keyboard back to the list
	if m.Focus() != PaneList {
		t.Fatalf("focus = %v, want the list so Enter is handled", m.Focus())
	}
	if len(m.rows()) == 0 {
		t.Fatal("fixture assumption changed: the level facet emptied the calls table, so Enter has no row to act on")
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ActiveView() != ViewCalls {
		t.Errorf("Enter jumped into a raw log the filter has emptied, view = %v", m.ActiveView())
	}
	if !strings.Contains(m.footerText(m.paneWidth()), "hidden by the active filter") {
		t.Errorf("footer = %q, want it to report the refused jump", m.footerText(m.paneWidth()))
	}

	// The report describes that one keypress under that one filter, so the
	// next key must clear it rather than leave it standing over a table the
	// user has since moved through.
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if strings.Contains(m.footerText(m.paneWidth()), "hidden by the active filter") {
		t.Errorf("footer = %q, want the key hints back once the selection has moved", m.footerText(m.paneWidth()))
	}
}

// The first visible entry is begun whatever its height: it is the one entry
// the user jumped to, and an entry taller than the pane must render its head
// rather than leave the pane blank. The pane row that composes it is what
// holds the result to h lines (framePanes, at every width), so this is the
// difference between showing something and showing nothing at all.
func TestTheRawLogRendersTheHeadOfAnEntryTallerThanThePane(t *testing.T) {
	m := update(t, New(tallEntryLog(40), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	out := m.renderRawLog(200, 5)
	if !strings.Contains(out, "HTTP Response Received") {
		t.Errorf("an entry taller than the pane rendered nothing at all: %q", out)
	}
}

// The raw log marks ERROR and WARN and leaves every other level exactly as
// the log wrote it.
//
// The multi-line ERROR entry is the case worth pinning: a level is a
// property of the ENTRY, and an error's detail block belongs to that error,
// so a marking that stopped at the header line would leave the explanation
// reading as unrelated traffic two lines below the thing it explains.
//
// TRACE and DEBUG are asserted UNMARKED deliberately. These captures are
// taken at TRACE with provider TRACE, so a scheme that also marked the
// quiet levels would mark nearly every line in the pane and distinguish
// nothing; see semanticStyles.forLevel.
func TestTheRawLogMarksOnlyTheRareLevels(t *testing.T) {
	m := update(t, New(testLog(t, "severity-levels.log"), "x.log"), tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	pane := m.renderRawLog(160, 40)

	lines := strings.Split(pane, "\n")
	marked := map[string]string{}
	for _, ln := range lines {
		plain := unstyled(ln)
		switch {
		case strings.Contains(plain, "[ERROR]"):
			marked["ERROR"] = ln
		case strings.Contains(plain, "[WARN]"):
			marked["WARN"] = ln
		case strings.Contains(plain, "[INFO]"):
			marked["INFO"] = ln
		case strings.Contains(plain, "[DEBUG]"):
			marked["DEBUG"] = ln
		case strings.Contains(plain, "[TRACE]"):
			marked["TRACE"] = ln
		case strings.Contains(plain, "Caller is not authorised"):
			marked["error detail"] = ln
		}
	}
	for _, want := range []string{"ERROR", "WARN", "INFO", "DEBUG", "TRACE", "error detail"} {
		if marked[want] == "" {
			t.Fatalf("the pane does not draw a %s line, so this asserts nothing about it:\n%s", want, pane)
		}
	}

	// The two rare levels carry their colour; the error's continuation
	// carries the SAME styling as its header, because it is the same entry.
	for _, name := range []string{"ERROR", "error detail"} {
		if got := marked[name]; !strings.HasPrefix(got, "\x1b[") {
			t.Errorf("the %s line is drawn unmarked: %q", name, got)
		}
	}
	if head, detail := marked["ERROR"], marked["error detail"]; sgrPrefix(head) != sgrPrefix(detail) {
		t.Errorf("an ERROR entry's continuation is drawn as %q and its header as %q -- the two are one entry", sgrPrefix(detail), sgrPrefix(head))
	}
	if got := marked["WARN"]; !strings.HasPrefix(got, "\x1b[") {
		t.Errorf("the WARN line is drawn unmarked: %q", got)
	}
	if sgrPrefix(marked["WARN"]) == sgrPrefix(marked["ERROR"]) {
		t.Errorf("WARN and ERROR are drawn alike (%q), so the pane cannot tell a caution from a failure", sgrPrefix(marked["WARN"]))
	}
	for _, name := range []string{"INFO", "DEBUG", "TRACE"} {
		if got := marked[name]; strings.Contains(got, "\x1b[") {
			t.Errorf("the %s line is marked (%q) -- these captures are almost entirely TRACE, so marking them distinguishes nothing", name, got)
		}
	}
}

// sgrPrefix is the escape sequence a line opens with, or "" if it opens with
// text. It is what says whether two lines are drawn the same way.
func sgrPrefix(line string) string {
	if !strings.HasPrefix(line, "\x1b[") {
		return ""
	}
	if end := strings.IndexByte(line, 'm'); end >= 0 {
		return line[:end+1]
	}
	return ""
}

// A structured capture reports its errors too. Terraform's JSON stream
// spells its levels in lower case and carries them in a field rather than in
// brackets, so nothing about the marking is shared with an hclog capture
// except the level itself -- and before that level was read, every entry of
// such a log arrived as UNKNOWN and an error was drawn exactly like the
// traffic around it.
func TestTheRawLogMarksAStructuredCapturesErrors(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 140, Height: 30})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})

	var errLine, infoLine string
	for _, ln := range strings.Split(m.renderRawLog(120, 20), "\n") {
		switch plain := unstyled(ln); {
		case strings.Contains(plain, `"@level":"error"`):
			errLine = ln
		case strings.Contains(plain, `"@level":"info"`) && infoLine == "":
			infoLine = ln
		}
	}
	if errLine == "" || infoLine == "" {
		t.Fatalf("the fixture does not draw both an error and an info line, so this compares nothing:\n%s", m.renderRawLog(120, 20))
	}
	if !strings.HasPrefix(errLine, "\x1b[") {
		t.Errorf("a structured capture's error is drawn unmarked: %q", errLine)
	}
	if strings.Contains(infoLine, "\x1b[") {
		t.Errorf("a structured capture's info line is marked (%q) -- these logs are almost entirely info", infoLine)
	}
}

// mixedHeightLog builds a log whose entries differ wildly in height: a
// one-line entry, then a tall body dump, then another one-line entry.
//
// That shape is what a real capture looks like around a provider's HTTP
// traffic -- one measured response accounted for 49% of a 30MB log -- and it
// is the shape every fixture in testdata lacks, each of them being a handful
// of one-line entries. Scrolling that moves by ENTRY looks correct on those
// and cannot be told from scrolling that moves by line.
func mixedHeightLog(tall int) *model.Log {
	var b strings.Builder
	var entries []logfmt.Entry
	add := func(n int, tag string) {
		start := b.Len()
		fmt.Fprintf(&b, "2026-09-04T10:00:00.000+1000 [TRACE] provider: %s head\n", tag)
		for i := 1; i < n; i++ {
			fmt.Fprintf(&b, "  %s body line %03d\n", tag, i)
		}
		entries = append(entries, logfmt.Entry{
			Off: uint64(start), Len: uint32(b.Len() - start), Lines: uint16(n), Timestamped: true,
		})
	}
	add(1, "first")
	add(tall, "tall")
	add(1, "last")
	return &model.Log{Data: []byte(b.String()), Entries: entries}
}

// rawLogBody is what the raw log pane actually SHOWS: its rendered lines,
// unstyled, truncated to the height it was given.
//
// The truncation is the pane row's (framePanes, via joinPanes), and it is
// applied here rather than trusted to renderRawLog's return, so a test
// asking what the reader can see is not answered with lines the frame
// discards. Reading the untruncated return instead makes an unreachable
// line look reachable -- which is one of the two defects here wearing the
// other as a disguise.
func rawLogBody(m Model, w, h int) []string {
	lines := unstyledLines(strings.Split(m.renderRawLog(w, h), "\n"))
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// The pane fills. An entry too tall to fit whole used to be dropped rather
// than begun, so a one-line entry above a forty-line one rendered ONE line
// into a twelve-line pane and left eleven blank -- with the content that
// would have filled them sitting immediately below, and nothing on screen
// saying why it was not shown.
func TestTheRawLogFillsThePaneAcrossATallEntry(t *testing.T) {
	m := update(t, New(mixedHeightLog(40), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	const h = 12
	if got := rawLogBody(m, 80, h); len(got) != h {
		t.Errorf("the pane drew %d of its %d lines from the top of the log:\n%s", len(got), h, strings.Join(got, "\n"))
	}
}

// renderRawLog honours the height it is given. It used to append its first
// entry whole whatever the budget -- forty lines for a pane of twelve -- and
// leave the pane row to truncate what would not fit, which cuts without a
// mark and hides the overrun from every caller.
func TestRenderRawLogHonoursItsHeight(t *testing.T) {
	m := update(t, New(mixedHeightLog(40), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	// Onto the tall entry: from the top of the log the first entry is one
	// line, and a budget is only overrun by an entry that exceeds it.
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	for _, h := range []int{1, 5, 12, 40, 100} {
		if got := strings.Split(m.renderRawLog(80, h), "\n"); len(got) > h {
			t.Errorf("asked for %d lines, rendered %d", h, len(got))
		}
	}
}

// Every line of a tall entry can be reached. Scrolling by ENTRY meant a
// forty-line body dump showed only its first paneful and the rest could not
// be reached by any key: the next press moved to the next ENTRY, taking the
// remaining lines with it. On a capture whose largest entry is half the log,
// that is half the log unreadable.
func TestEveryLineOfATallEntryCanBeReached(t *testing.T) {
	const tall = 40
	m := update(t, New(mixedHeightLog(tall), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	seen := map[string]bool{}
	// Enough presses to walk the whole log a line at a time, and then some.
	for range tall + 10 {
		for _, ln := range rawLogBody(m, 80, 12) {
			seen[strings.TrimSpace(ln)] = true
		}
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	for i := 1; i < tall; i++ {
		want := fmt.Sprintf("tall body line %03d", i)
		if !seen[want] {
			t.Errorf("no scroll position shows %q", want)
		}
	}
}

// Scrolling up is scrolling down's inverse, line for line. Moving by entry,
// a press up from inside a tall entry jumped to the head of the one before
// it -- a different place from where the press down had come.
func TestScrollingUpUndoesScrollingDown(t *testing.T) {
	m := update(t, New(mixedHeightLog(40), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	for range 25 { // down into the middle of the tall entry
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	middle := strings.Join(rawLogBody(m, 80, 12), "\n")

	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := strings.Join(rawLogBody(m, 80, 12), "\n"); got != middle {
		t.Errorf("down then up did not return to the same lines:\n--- want ---\n%s\n--- got ---\n%s", middle, got)
	}
}

// Opening a call opens on the SCOPE's first entry, not jumpContextLines
// above the call's own closing one -- the scope already supplies whatever
// of the call's own traffic came before it (see jumpToSpan). What is
// visible above the closing entry is whatever the scope holds earlier than
// it: multiline-body.log's closing entry is redacted differently from the
// entry before it, so the two carry different request ids and the scope
// holds only the closing entry itself. The pane opens exactly on it, with
// nothing above -- correctly, since the scope has nothing earlier to show.
func TestOpeningACallOpensOnItsScopesFirstEntry(t *testing.T) {
	m := update(t, New(testLog(t, "multiline-body.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	target := m.log.RPCSpans[0].Entry
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("Enter left the view at %v", m.view)
	}
	if got := m.TopEntry(); got != int(target) {
		t.Errorf("the pane opens at entry %d, want the call's own entry %d -- its scope holds nothing earlier", got, target)
	}
}

// Opening a call draws that call's lines and nothing else. The fixture's two
// calls interleave, so a pane showing a contiguous run of entries fails here.
//
// The lines are identified by TIMESTAMP rather than by request id: on the
// call's closing entry the id sits at column 209 of a 302-byte line, which a
// 200-column pane clips away, whereas a timestamp opens the line and survives
// any width. Call A's entries are .000, .200, .500 and .600. Every other
// timestamp in the fixture is a line this pane must NOT draw -- call B's
// .100, .300 and .700, and the HTTP entry's .400, which carries its id only
// on a continuation line and so belongs to no scope.
//
// Both calls report tf_req_duration_ms=600, and the table sorts with
// sort.SliceStable, so the file's order decides the tie and call A is row 0.
//
// The count below counts non-blank LINES and compares it against the
// number of ENTRIES in want, which only agrees because call A's four
// entries are each single-line -- the per-line timestamp match shares the
// same dependency, since a multi-line scope member's continuation lines
// carry no timestamp of their own. A fixture edit that gave one of these
// entries a continuation would fail this test loudly rather than pass it
// while asserting something else.
func TestOpeningACallDrawsOnlyThatCallsEntries(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("Enter left the view at %v", m.view)
	}
	want := []string{"09:15:00.000", "09:15:00.200", "09:15:00.500", "09:15:00.600"}
	drawn := 0
	for _, line := range rawLogBody(m, 200, 20) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		drawn++
		inScope := false
		for _, ts := range want {
			if strings.Contains(line, ts) {
				inScope = true
			}
		}
		if !inScope {
			t.Errorf("the pane draws a line outside the call's scope: %q", line)
		}
	}
	if drawn != len(want) {
		t.Errorf("the pane drew %d entries, want the call's %d", drawn, len(want))
	}
}

// A scroll must not walk past the scope: nextRawEntry and prevRawEntry are
// what stepRawLogDown and stepRawLogUp lean on to stop at its last and first
// member rather than spilling into the rest of the log once the scope runs
// out. Pressing past either end has to land on a member of the scope, and
// the pane must never draw a line belonging to call B or to the HTTP entry
// that carries no id.
func TestScrollingStaysInsideTheScope(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("Enter left the view at %v", m.view)
	}
	scope := map[int]bool{1: true, 3: true, 6: true, 7: true}
	outside := []string{"09:15:00.100", "09:15:00.300", "09:15:00.400", "09:15:00.700"}
	checkInsideScope := func(step string) {
		t.Helper()
		if !scope[m.TopEntry()] {
			t.Errorf("TopEntry = %d after scrolling %s past the scope, want one of its four members", m.TopEntry(), step)
		}
		for _, line := range rawLogBody(m, 200, 20) {
			for _, ts := range outside {
				if strings.Contains(line, ts) {
					t.Errorf("scrolling %s drew a line outside the scope: %q", step, line)
				}
			}
		}
	}

	for range len(scope) + 10 { // more presses than the scope has members
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	checkInsideScope("down")

	for range len(scope) + 10 {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	}
	checkInsideScope("up")
}

// A search inside a scope must not reach past it, in either direction:
// bbbbbbbb is call B's own id, and call B's entries lie outside call A's
// scope in both directions -- forward from the scope's first member, and
// backward from its last -- so neither a forward search for it nor a
// backward one may find a match.
//
// The backward search starts from the scope's LAST member, not its first.
// interleaved-calls.log's own header comment (entry 0) names call B's id in
// prose, so a whole-log backward walk starting any earlier in the scope
// would reach that header before it ever reached one of call B's real
// entries, and pass by matching a comment rather than by respecting the
// scope. Starting at the scope's last member puts call B's own entries (4,
// then 2, walked backward) between the start and the header, so a
// mis-routed walk is caught on real call traffic.
func TestSearchDoesNotReachPastTheScope(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("Enter left the view at %v", m.view)
	}
	before := m.TopEntry()

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeQuery(t, m, "bbbbbbbb")
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Errorf("footer = %q after a forward search for call B's id, want the miss reported", got)
	}
	if m.TopEntry() != before {
		t.Errorf("TopEntry = %d after a forward search that must miss, want it left at %d", m.TopEntry(), before)
	}

	for range 10 { // more presses than the scope has members
		m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	last := m.TopEntry()

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if got := footerOf(m.View()); !strings.Contains(got, "not found") {
		t.Errorf("footer = %q after a backward search from the scope's last member, want the miss reported", got)
	}
	if m.TopEntry() != last {
		t.Errorf("TopEntry = %d after a backward search that must miss, want it left at %d", m.TopEntry(), last)
	}
}

// A call carrying no request id -- Log.ScopeFor(0) is nil -- takes the
// unscoped jump path: the pane backs up jumpContextLines from the closing
// entry rather than a scope choosing its own lines. No fixture under
// testdata gives an RPC span a zero ReqID, so this builds one directly:
// manyEntryLog's entries carry no tf_req_id at all, and the span placed over
// it inherits ReqID's zero value.
func TestJumpWithNoRequestIDBacksUpFromTheClosingEntry(t *testing.T) {
	l := manyEntryLog(10)
	const entry = 9
	l.RPCSpans = []span.Span{{RPC: "ReadResource", Provider: "registry.terraform.io/hashicorp/aws", Entry: entry}}
	m := New(l, "x.log")
	m.jumpToSpan(l.RPCSpans, 0)
	if m.raw.scope != nil {
		t.Errorf("scope = %v after a jump with no request id, want nil -- there is no id to build one from", m.raw.scope)
	}
	if got, want := m.TopEntry(), entry-jumpContextLines; got != want {
		t.Errorf("TopEntry = %d after the jump, want %d lines of context above the closing entry %d", got, want, entry)
	}
}

// Scrolling up INTO a tall entry arrives at its end, not its head. Landing
// on the head instead skips everything between -- the same unreachable
// middle that scrolling by entry produced, reintroduced one boundary at a
// time -- and it is not the inverse of the press down that left it, so the
// pane does not come back to where it was.
//
// The boundary has to be crossed upward into an entry TALLER than one line:
// the last line of a one-line entry is also its first, so a fixture of
// short entries cannot tell the two apart.
func TestScrollingUpIntoATallEntryArrivesAtItsEnd(t *testing.T) {
	const tall = 40
	m := update(t, New(mixedHeightLog(tall), "x.log"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	// Down to the one-line entry after the tall one: 1 line of "first",
	// then all of "tall".
	for range 1 + tall {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := rawLogBody(m, 80, 12); len(got) != 1 || !strings.Contains(got[0], "last head") {
		t.Fatalf("expected to be on the last entry, showing one line; got %d:\n%s", len(got), strings.Join(got, "\n"))
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyUp})
	body := rawLogBody(m, 80, 12)
	if want := fmt.Sprintf("tall body line %03d", tall-1); !strings.Contains(body[0], want) {
		t.Errorf("scrolling up into the tall entry opens on %q, want its last line %q", strings.TrimSpace(body[0]), want)
	}
}

// Search begins at the visible physical line and reveals the matched line,
// including a continuation within the current entry. A forward search cannot
// reach a header above that position; the reverse search can.
func TestASearchRevealsTheMatchedPhysicalLine(t *testing.T) {
	const tall = 40
	// Down one line of "first", then 24 into "tall" -- far enough in to be
	// past the whole of the one-line entries the searches below match.
	const into = 25
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"an entry taller than the offset", "tall body line 030"},
		{"an entry shorter than the offset", "last head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := update(t, New(mixedHeightLog(tall), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
			for range into {
				m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
			}
			if got, want := rawLogBody(m, 80, 12), fmt.Sprintf("tall body line %03d", into-1); !strings.Contains(got[0], want) {
				t.Fatalf("the scroll did not land inside the tall entry, so nothing here is scrolled off: pane opens on %q, want %q", strings.TrimSpace(got[0]), want)
			}

			m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			m = typeQuery(t, m, tc.query)
			m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if got := footerOf(m.View()); strings.Contains(got, "not found") {
				t.Fatalf("the fixture does not contain %q at all, so this test compares nothing", tc.query)
			}
			body := rawLogBody(m, 80, 12)
			if !strings.Contains(body[0], tc.query) {
				t.Errorf("a search reported as found for %q opens the pane on %q:\n%s", tc.query, strings.TrimSpace(body[0]), strings.Join(body, "\n"))
			}
		})
	}

	m := update(t, New(mixedHeightLog(tall), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m.setView(ViewRawLog)
	for range into {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	beforeEntry, beforeLine := m.TopEntry(), m.TopLine()
	m.raw.lastQuery = "tall head"
	if m.searchFrom(m.raw.top, true, true) {
		t.Fatal("forward search found a header above the visible position")
	}
	if m.TopEntry() != beforeEntry || m.TopLine() != beforeLine {
		t.Fatal("failed forward search moved the viewport")
	}
	if !m.searchFrom(m.raw.top, false, false) || !strings.Contains(m.renderRawLog(20, 1), "tall head") {
		t.Fatalf("reverse search did not reveal the header: %q", m.renderRawLog(20, 1))
	}
}

// A scope drawing nothing says so in its own words, naming the key that
// widens. A scope is never empty of MEMBERS -- it is built from a span's own
// id and holds at least that span's entry -- so an empty scoped pane is
// always the filter's doing, and backslash is a key the reader has and one
// that acts. "this log has no entries" would be false, and "Esc clears it"
// names a key that returns before it clears.
//
// Asserted against the literal backslash and the ABSENCE of noMatchNote,
// rather than against scopedEmptyNote itself: a test that compares the
// rendered pane to the very constant it is meant to pin proves nothing about
// the constant's wording, only that renderRawLog returns whatever the
// constant currently says.
func TestAnEmptyScopedPaneNamesTheKeyThatWidensIt(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// Exclude every level, so the scope's members are all hidden.
	m.setFacetExclusions(dimLevel, map[string]bool{"TRACE": true, "DEBUG": true, "UNKNOWN": true})
	m.invalidateRows()

	got := unstyled(m.renderRawLog(200, 10))
	if !strings.Contains(got, "\\") {
		t.Errorf("an empty scoped pane does not name the backslash key: %q", got)
	}
	if strings.Contains(got, noMatchNote) {
		t.Errorf("an empty scoped pane says %q, which falsely claims %q", got, "Esc clears it")
	}
}

// Scoped, the refusal asks whether the filter admits ANY member of the call,
// not whether it admits the response entry. The pane opens on the first
// member the filter admits, so the response entry being hidden is not
// decisive -- and refusing a jump whose other lines are perfectly visible
// would deny the reader a pane that would have worked.
func TestAJumpProceedsWhenTheFilterAdmitsTheCall(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	// Hide DEBUG: its request id is continuation-borne (see ScopeFor), so
	// it is outside the call's scope -- every scope member is TRACE.
	m.setFacetExclusions(dimLevel, map[string]bool{"DEBUG": true})
	m.invalidateRows()

	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Errorf("the jump was refused though the filter admits the call's TRACE lines")
	}
	if m.blockedJump {
		t.Errorf("blockedJump set for a call the filter admits")
	}
}

// It refuses when the filter admits NONE of them, reported in the footer over
// the view the reader pressed Enter in -- landing in an empty pane is
// indistinguishable from a jump that worked.
func TestAJumpIsRefusedWhenTheFilterHidesTheWholeCall(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m.setFacetExclusions(dimLevel, map[string]bool{"TRACE": true, "DEBUG": true, "UNKNOWN": true})
	m.invalidateRows()

	before := m.view
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != before {
		t.Errorf("the jump proceeded with every one of the call's entries hidden")
	}
	if !m.blockedJump {
		t.Errorf("blockedJump not set for a wholly hidden call")
	}
}

// The pane opens on the first scope member the filter admits, which need
// not be the scope's own first member: a call whose OPENING line the filter
// hides, but whose later traffic it shows, still opens -- on the later
// line, not the hidden one. entryVisible weighs only level and provider
// (see its own doc comment), and every fixture's scope is level-uniform --
// for instance, testdata/interleaved-calls.log's two calls and
// testdata/severity-levels.log's pair -- so telling "checks the whole
// scope" apart from "checks the first member" takes a log built directly
// rather than a fixture file.
func TestAJumpOpensOnTheFirstAdmittedMemberEvenWhenAnEarlierOneIsHidden(t *testing.T) {
	l := manyEntryLog(3)
	l.Entries[0].Level, l.Entries[0].ReqID = logfmt.LevelTrace, 1
	l.Entries[1].Level, l.Entries[1].ReqID = logfmt.LevelDebug, 1
	l.Entries[2].Level, l.Entries[2].ReqID = logfmt.LevelTrace, 1
	l.RPCSpans = []span.Span{{Entry: 2, ReqID: 1}}

	m := New(l, "x.log")
	m.setFacetExclusions(dimLevel, map[string]bool{"TRACE": true})
	m.jumpToSpan(l.RPCSpans, 0)

	if m.blockedJump {
		t.Fatalf("blockedJump set though the scope's DEBUG member is visible")
	}
	if got, want := m.raw.top, 1; got != want {
		t.Errorf("top = %d, want %d -- the first scope member the filter admits, not the hidden opening one", got, want)
	}
}

// jumpToSpan sets top and topLine TOGETHER at a scoped jump (top, 0), not top
// alone: a stale topLine left over from an earlier visit to the raw log
// would otherwise survive, opening the scope's first member partway down
// its own text rather than at its own first line -- the entry index would
// be right and the line within it wrong, which looks like a working jump
// until the pane is read closely.
//
// Reached the way a reader reaches it: 6 opens the raw log directly, five
// downs scroll into the tall entry's body, 4 leaves it for the calls view
// without resetting m.raw, and Enter jumps into the scope that entry's call
// belongs to.
func TestAScopedJumpResetsTheLineOffset(t *testing.T) {
	const tall = 10
	l := mixedHeightLog(tall)
	// All three of mixedHeightLog's entries belong to one call, so Enter's
	// scope holds all three and opens on the first, entry 0.
	for i := range l.Entries {
		l.Entries[i].ReqID = 1
	}
	l.RPCSpans = []span.Span{{Entry: 2, ReqID: 1}}

	m := update(t, New(l, "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	for range 5 {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.TopLine() == 0 {
		t.Fatalf("scrolling did not leave a nonzero line offset, so this does not exercise the branch it means to")
	}

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.raw.scope == nil {
		t.Fatalf("Enter built no scope, so this does not reach the branch it means to pin")
	}
	if got := m.TopLine(); got != 0 {
		t.Errorf("TopLine = %d after a scoped jump, want 0 -- a line offset left over from scrolling before the jump", got)
	}
}

// A UI-hook span's ReqID is always 0 (span.uihook.go), and ScopeFor answers
// id 0 with nil (see its own doc comment), so jumping in from a UI-tier
// timeline span leaves hasReturn standing with no scope to name. That empty
// pane still owes its "Esc" claim the truth: Esc there goes back to the
// timeline rather than clearing the filter, so the pane must say "goes
// back" and not the ordinary noMatchNote's "clears it", which would be
// false over exactly this pane.
func TestAnEmptyUnscopedPaneReachedFromTheTimelineNamesEscsReturn(t *testing.T) {
	m := update(t, New(testLog(t, "structured-ui.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("view = %v after Enter from the timeline, want ViewRawLog", m.view)
	}
	if m.raw.scope != nil {
		t.Fatalf("the jumped-to span carries a scope, so this test does not reach the branch it names")
	}
	if !m.hasReturn {
		t.Fatalf("the jump left no return, so this test does not reach the branch it names")
	}
	showOnly(t, &m, dimLevel)

	got := unstyled(m.renderRawLog(200, 10))
	if !strings.Contains(got, "Esc goes back") {
		t.Errorf("an empty pane reached from the timeline says %q, want it to name %q", got, "Esc goes back")
	}
	if strings.Contains(got, noMatchNote) {
		t.Errorf("an empty pane reached from the timeline says %q, which falsely claims %q", got, "Esc clears it")
	}
}
