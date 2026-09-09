# Search Positions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Raw Log and reconstructed-response searches reveal the actual matching text and visit each non-overlapping occurrence in either direction.

**Architecture:** Keep each viewer's state and traversal separate. Share a small literal occurrence selector over safe display text; retain a successful match independently of the viewport so clamping near the bottom cannot repeat it. Raw Log traverses its existing filtered/request-scoped entries and their physical lines.

**Tech Stack:** Go, standard `testing`, Bubble Tea, the existing Bubbles viewport, and `charmbracelet/x/ansi`. No new dependency.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), item 1 and delivery boundary B. Boundary A is complete. This plan does not implement the other draft features.

## Global Constraints

- “Keep literal, case-sensitive search with `/`, `Enter`, `Esc`, `n` and `N`.”
- “Do not introduce regular expressions, wraparound or a new search language.”
- “Search stays within active filters and request scope.”
- “A failed search leaves the viewport unchanged and reports failure.”
- “Manual scrolling invalidates the previous match anchor; the next search begins at the new visible position rather than an abandoned match.”
- “Fixtures are synthetic or sanitised.”
- “Test observable contracts and important failure boundaries, not copied implementation details or mocked behaviour.”
- “Capture and assert expected error output.”
- Use the existing Go toolchain, British/Australian prose, TDD, independent review and a separate test-cleanup subagent.
- Use `/Users/dan/.codex/bin/codex-git` for Git. Sign every commit and preserve hooks. Stop immediately on signing failure.

---

## Starting evidence and decisions

The execution branch is `wip/response-search-positions`, created from clean
local `main` at `b799381`. `pull --rebase origin main` reported it up to date;
local `main` includes the six previously integrated, unpushed commits.
`go test ./internal/tui` passes before changes.

`rawlog.go:searchFrom` currently searches an entire entry and resets both line
and column to zero. `searchAgain` excludes the entire current entry. Rendering
already has the correct physical-line traversal and uses `StripANSI` followed
by `DisplayText`, whereas search currently only strips ANSI.

`response.go:searchResponse` finds the first occurrence on a line and advances
by line. Its `matchLine` protects against vertical viewport clamping, but cannot
represent multiple occurrences on a line. The pinned Bubbles viewport exposes
`SetXOffset` but keeps `xOffset` private. Own a response column in response state
and update it at rendering and horizontal-navigation boundaries; do not infer
an integer position from its floating-point scroll percentage.

Interpret non-overlapping occurrences consistently from the beginning of each
line: `aaaaa` with query `aa` has starts 0 and 2 in both directions. Submitted
searches and repetitions after manual movement include the current visible
column. Repetitions with a valid anchor exclude that occurrence. Search operates
within physical display lines, not across newline separators. If a literal
starts inside a grapheme cluster, reveal that cluster's starting cell: a
combining accent or part of a joined emoji must not be scrolled off screen.

Keep existing empty-query and cancellation behaviour in Raw Log. Give responses
the same behaviour: empty submission does not move or replace the last non-empty
query; cancellation does not replace the prior query or anchor. Failed searches
retain the viewport and any valid anchor. Opening and closing a response never
changes Raw Log's position or anchor.

## File map

| File | Responsibility |
| --- | --- |
| `internal/tui/search_match.go` (new) | Literal occurrence selection and display-column positions |
| `internal/tui/search_match_test.go` (new) | Direction, non-overlap and column-boundary contracts |
| `internal/tui/rawlog.go` | Physical-line search, raw match anchor and scroll invalidation |
| `internal/tui/rawlog_test.go` | Raw search navigation and rendered visibility regressions |
| `internal/tui/model.go` | Invalidate raw anchor when filters/view/scope change |
| `internal/tui/response.go` | Response occurrence anchor and owned horizontal position |
| `internal/tui/response_test.go` | Response navigation parity and raw-position restoration |
| `internal/tui/search_input.go` | Existing raw submission entry point; change only if needed to reset an anchor |

No parser/model API changes, new persistent indices, asynchronous search or
unrelated layout refactoring. Existing TUI goldens should normally remain stable.

### Task 1: Land Raw Log searches on individual occurrences

**Files:** Create `internal/tui/search_match.go` and `search_match_test.go`; modify `internal/tui/rawlog.go`, `rawlog_test.go`, `model.go`; inspect `search_input.go`.

**Interfaces:** Preserve `searchFrom(start int, forward, includeStart bool) bool` and `searchAgain(direction int)` for existing callers. Add `literalPosition` and `findLiteral` below for Task 2. No exported interface.

- [x] **Step 1: Add occurrence and raw rendering regression tests.**

Use the existing `update`, `typeQuery`, `rawLogBody` and synthetic `model.Log`
patterns in `rawlog_test.go`. Add this regression before production changes:

```go
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
```

Add the selector test after the renderer's behavioural RED has been observed:

```go
func TestLiteralOccurrencesAndColumns(t *testing.T) {
	first, ok := findLiteral("aaaaa", "aa", true, nil, 0)
	if !ok || first.byteOffset != 0 {
		t.Fatal("missing first non-overlapping occurrence")
	}
	second, ok := findLiteral("aaaaa", "aa", true, &first, 0)
	if !ok || second.byteOffset != 2 {
		t.Fatal("missing second non-overlapping occurrence")
	}
	if _, ok := findLiteral("aaaaa", "aa", true, &second, 0); ok {
		t.Fatal("search wrapped or admitted an overlapping occurrence")
	}
	previous, ok := findLiteral("aaaaa", "aa", false, &second, 0)
	if !ok || previous != first {
		t.Fatal("reverse traversal changed the occurrence set")
	}
	for _, tc := range []struct{ column, wantByte, wantColumn int }{
		{2, 3, 2},
		{3, 10, 9},
	} {
		p, ok := findLiteral("界needle needle", "needle", true, nil, tc.column)
		if !ok || p.byteOffset != tc.wantByte || p.column != tc.wantColumn {
			t.Errorf("column %d: got %+v, found=%v", tc.column, p, ok)
		}
	}
	if _, ok := findLiteral("text", "", true, nil, 0); ok {
		t.Fatal("empty query matched")
	}
}
```

The new test file uses `package tui` and imports `testing`.

- [x] **Step 2: Observe RED.**

Run `go test ./internal/tui -run TestRawSearchRevealsContinuationOccurrences -count=1`
before introducing the helper-dependent tests. Expect failure because the
current implementation opens line zero. Record that behavioural failure;
compilation failures from a new helper are not its substitute.

- [x] **Step 3: Add the small literal selector.**

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type literalPosition struct {
	byteOffset int
	column     int
}

// findLiteral selects from the same non-overlapping occurrences in either
// direction. An anchor excludes itself; without one, column is inclusive.
// A negative column admits the whole line.
func findLiteral(line, query string, forward bool, anchor *literalPosition, column int) (literalPosition, bool) {
	if query == "" {
		return literalPosition{}, false
	}
	var chosen literalPosition
	found := false
	remaining, clusterByte, clusterColumn, state := line, 0, 0, -1
	for offset := 0; offset <= len(line)-len(query); {
		i := strings.Index(line[offset:], query)
		if i < 0 {
			break
		}
		i += offset
		admit := true
		if anchor != nil {
			admit = (forward && i > anchor.byteOffset) || (!forward && i < anchor.byteOffset)
		}
		if admit {
			for clusterByte < i && remaining != "" {
				cluster, rest, width, nextState := ansi.FirstGraphemeCluster(remaining, state)
				if i < clusterByte+len(cluster) {
					break
				}
				clusterByte += len(cluster)
				clusterColumn += width
				remaining, state = rest, nextState
			}
			p := literalPosition{byteOffset: i, column: clusterColumn}
			if anchor == nil && column >= 0 {
				admit = (forward && p.column >= column) || (!forward && p.column <= column)
			}
			if admit {
				chosen, found = p, true
				if forward {
					return chosen, true
				}
			}
		}
		offset = i + len(query)
	}
	return chosen, found
}
```

Do not materialise all matches in a capture. This selector walks one line and
keeps at most one candidate. Grapheme traversal advances with the increasing
byte positions; it does not measure the whole prefix for every occurrence.
The four-result `ansi.FirstGraphemeCluster` signature above is verified in the
repository's pinned `x/ansi v0.10.1`; do not copy the newer incompatible API.
Check long repeated lines before adding caching or
optimisation; any optimisation must preserve the forward/reverse occurrence set.

- [x] **Step 4: Make raw search traverse positions rather than entries.**

Add the raw-specific position and anchor:

```go
type rawMatch struct {
	entry int
	line  int
	text  literalPosition
}
// In rawLogState:
match *rawMatch
```

For submission (`includeStart == true`), clear the old anchor and begin at
`start`, `raw.topLine`, and the effective displayed horizontal column. Compute
that column using the same clamping as `renderRawLog`: visible body lines,
`rawLogMaxColumn`, and `rawLogViewportWidth`. For a repeat with `raw.match != nil`,
begin at its entry/line and pass its literal position as the exclusive anchor.
For a repeat without an anchor, begin inclusively at the visible position.

Walk `nextRawEntry`/`prevRawEntry` with `rawLogVisible`, then `entryLines` in the
requested direction. On subsequent entries start at line zero or the last line;
on subsequent lines pass no anchor and column -1. On each candidate line:

```go
plain, scratch := logfmt.StripANSI(line, scratch)
display := logfmt.DisplayText(plain)
p, ok := findLiteral(display, m.raw.lastQuery, forward, anchor, column)
if ok {
	m.raw.top, m.raw.topLine, m.raw.column = entryIndex, lineIndex, p.column
	m.raw.match = &rawMatch{entry: entryIndex, line: lineIndex, text: p}
	return true
}
```

Here `entryIndex` and `lineIndex` are the traversal indices, `line` is that
physical source line, and `scratch` is reused across the walk. Do not mutate the
viewport while scanning. Return false on exhaustion. Preserve the existing
callers' `notFound` handling. Replace comments that describe entry-sized search
with the actual line/occurrence contract.

Clear the raw anchor on manual vertical/horizontal scrolling and in
`invalidateRows` (covering filter and view changes):

```go
m.raw.match = nil
```

Horizontal scrolling must also clear `notFound`, as vertical scrolling already
does. Confirm scope removal and `jumpToSpan` pass through these invalidation
paths. Response opening/closing must not invalidate the raw anchor.

- [x] **Step 5: Cover search boundaries through real rendering and key handling.**

Extend the fixture-backed search tests, retaining existing scope/filter tests.
Use distinct assertions for these cases:

| Input/action | Required observable result |
| --- | --- |
| Two occurrences on a line, another on a continuation, then another entry | `/`, `n`, `n`, `n`, `N` traverse the ordered occurrences without skipping or wrapping |
| `界` before a far-right needle, ANSI inserted inside the word | Actual rendered needle is visible; horizontal scroll uses terminal cells |
| A query beginning inside a combining-character or joined-emoji grapheme | The containing grapheme remains visible at its starting cell |
| A tab/control rendered by `DisplayText` | Searching the visible escape finds it; searching stripped ANSI bytes does not |
| Scroll vertically or horizontally after a match, then `n`/`N` | Search begins at the new visible line/column |
| Empty submission, cancelled query, missing query | Appropriate prior position is unchanged; only a real miss reports failure |
| Facet change or request-scope removal after a match | Old anchor is discarded; hidden entries cannot satisfy search |
| One-entry, empty-log, and empty-filter results | Search terminates safely with no out-of-range traversal |

For a parser-backed case, write sanitised multi-line text to `t.TempDir`, load
it using `model.Load`, and send search keys via `update`; verify the continuation
line shown by `renderRawLog`. Do not test mocked loading or internal helper call
counts. Adjust old assertions that intentionally pinned line/column zero only
where they conflict with the newly specified behaviour.

Specifically, `TestASearchOpensTheMatchedEntryAtItsFirstLine` currently searches
forward for `tall head` after scrolling 24 lines below that header. Replace its
tall-entry query with `tall body line 030`, retaining `last head` for the short
entry case. Rename it to describe revealing the matched physical line and
rewrite its preceding comment accordingly. Add the complementary check that
forward submission of `tall head` from inside the entry reports failure without
moving, then `N` finds the header. This preserves the old stale-offset regression
while enforcing the new forward-only starting boundary.

Add this rendered grapheme regression, using the existing imports:

```go
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
```

- [x] **Step 6: Verify, review and commit Task 1.**

Run `gofmt -w` on the changed Go files, the new focused tests with `-count=1`,
then `go test ./internal/tui`. Inspect the diff and obtain independent task
review before proceeding. Commit exact changed paths with a signed commit,
subject `Search raw logs by visible text occurrence`. Preserve RED/GREEN evidence.

### Task 2: Give reconstructed responses the same occurrence navigation

**Files:** Modify `internal/tui/response.go` and `response_test.go`.

**Interfaces:** Consume `literalPosition` and `findLiteral(line, query string, forward bool, anchor *literalPosition, column int) (literalPosition, bool)` from Task 1. Keep `searchResponse(direction int, includeCurrent bool)` and all existing viewer entry points.

- [x] **Step 1: Add a repeated-response regression and observe RED.**

Use the existing `responseModel` helper, which loads real reconstructed provider
fragments. A body containing one long string keeps both matches on one line:

```go
func TestResponseSearchVisitsOccurrencesOnOneLine(t *testing.T) {
	m := responseModel(t, `{"message":"`+strings.Repeat("x", 100)+`needle first needle second`+strings.Repeat("z", 100)+`"}`)
	responseKey(m, "r")
	m.renderCentre(12, 4)
	responseKey(m, "/")
	responseKey(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.renderCentre(12, 4), "needle first") {
		t.Fatal("first occurrence is hidden")
	}
	responseKey(m, "n")
	if !strings.Contains(m.renderCentre(12, 4), "needle secon") {
		t.Fatal("next occurrence on the same line is hidden")
	}
	responseKey(m, "N")
	if !strings.Contains(m.renderCentre(12, 4), "needle first") {
		t.Fatal("previous occurrence on the same line is hidden")
	}
}
```

Run `go test ./internal/tui -run TestResponseSearchVisitsOccurrencesOnOneLine -count=1`.
Record failure caused by line-sized repetition before production changes.

- [x] **Step 2: Track the response occurrence separately from its viewport.**

Replace `matchLine` with an optional response-specific anchor and an owned
horizontal column:

```go
type responseMatch struct {
	line int
	text literalPosition
}
// In responseState:
match  *responseMatch
column int
```

Retain `r.lines` as the already-safe display text. Do not strip its visible
escaped controls. In `renderResponse`, after updating Width and Height,
synchronise the effective horizontal column and viewport:

```go
r.column = min(r.column, rawLogMaxColumn(r.lines, r.viewport.Width))
r.viewport.SetXOffset(r.column)
```

Use a local `r := &m.response` for that function. Keep its existing vertical
clamping and rendering. Treat horizontal keys separately from viewport Update:
`left`/`h` subtract four cells, `right`/`l` add four; clamp through
`rawLogMaxColumn(r.lines, r.viewport.Width)` and `max(0, ...)`, set the viewport
offset, clear the anchor and clear `notFound`. Refresh dimensions via `m.View()`
before these navigation actions, as the existing handler does. Retain viewport
Update for vertical/page keys, clearing only the response anchor and miss flag.

- [x] **Step 3: Select occurrences using the shared helper.**

For a submitted non-empty query, replace `r.query`, clear the response anchor and
search inclusively from `YOffset` and the effective owned column. Empty Enter
and Esc preserve the last non-empty query and anchor without moving. A repeat
uses the stored occurrence if present; otherwise it starts inclusively from the
visible position. Later lines have no anchor or column restriction.

On success:

```go
r.viewport.SetYOffset(lineIndex)
r.column = p.column
r.viewport.SetXOffset(r.column)
r.match = &responseMatch{line: lineIndex, text: p}
r.notFound = false
```

Keep `lineIndex` independent of the resulting `YOffset`: Bubbles clamps near
the bottom. Set `notFound` on exhaustion without changing offsets or the valid
anchor. A search after manual scrolling must not reuse a clamped previous
match. Re-rendering may clamp horizontal position, but must not replace the
byte position of a successful occurrence.

- [x] **Step 4: Verify response parity and regression boundaries.**

Extend `TestResponseNavigationAndSearchPreservesRawPosition` to preserve raw
`topLine` as well as entry/column, and verify a raw `n` after closing continues
from its prior occurrence. Retain decoded-control and near-bottom regressions.
Add cases for same-line non-overlap in both directions, failure without moving,
empty/cancelled queries, manual horizontal and vertical scrolling followed by
repetition, resize clamping, and matches after wide Unicode characters. Assert
rendered text and footer behaviour, not merely the stored match fields.

Run focused new tests, then `go test ./internal/tui`. Obtain independent review
of Task 2 against response parity and raw restoration. Commit exact changed
paths with a signed commit, subject `Search reconstructed responses by occurrence`.

## Final validation and delivery

- [x] Run a separate test-cleanup subagent after implementation; preserve every
  distinct occurrence, display-width, filter/scope and restoration boundary.
  Re-run affected tests after any edits and review the cleanup diff.
- [x] Inspect real terminal interaction using the existing CLI: open Raw Log,
  search a continuation and repeated text, use `n`/`N`, scroll and search again,
  open/close a reconstructed response, and repeat at a narrow terminal width.
  Use sanitised fixtures only. Record exact inputs and results.
- [x] Run `go test ./internal/tui` without updating goldens first. If output
  changes intentionally, use `go test ./internal/tui -update`, inspect with
  `scripts/read-golden.sh`, and review raw styling diffs. Do not regenerate
  snapshots solely to make a test pass.
- [x] Run `gofmt -d .`, `go mod tidy -diff`, `go mod verify`,
  `golangci-lint run --timeout=5m`, `go test -race -count=1 ./...`, and
  `go build ./...`. Require pristine passing output and no module/format diff.
- [x] Obtain final whole-branch review, resolve substantive findings, and
  record execution evidence in this plan. Verify signatures with
  `codex-git log main..HEAD '--format=%h %G? %s'` using the full wrapper path.
- [x] Leave the completed branch ready for Dan's integration choice. No push
  or merge is authorised merely by drafting this plan. GitHub CI's existing
  Linux/macOS amd64/arm64 matrix remains required before remote integration.

## Plan self-review

- Item 1's physical-line positioning, Unicode/control display, filters/scopes,
  no wraparound and manual anchor invalidation map to Task 1.
- Response occurrence parity, bottom clamping and raw restoration map to Task 2.
- The shared helper owns occurrence selection only; each viewer retains its own
  position and traversal. No shared view-state subsystem is introduced.
- Existing raw query APIs and response entry points are retained; helper types
  consumed by Task 2 are defined in Task 1.
- The original planning commit contained no application changes; execution is recorded below.

## Plan review evidence

- Independent review checked occurrence order, scoped traversal, viewport
  ownership, bottom clamping and the concrete regression expectations against
  the existing code.
- Review identified prefix-width measurement hiding matches inside a grapheme.
  The selector now tracks complete graphemes with the pinned ANSI API, with a
  rendered combining-accent/joined-emoji regression. Scoped re-review confirmed
  the finding addressed with no new substantive findings.
- The obsolete forward-search-above-cursor expectation has an explicit test
  migration preserving its stale-line-offset coverage.
- Planning baseline: `go test ./internal/tui` passed. Implementation and delivery
  checks were subsequently executed; evidence follows.

## Implementation evidence

- Task 1: signed commit `4a902f9`, `Search raw logs by visible text occurrence`.
  Behavioural RED showed the continuation query opening line zero (`header`).
  Focused GREEN covers occurrence order, physical continuations, real parser
  loading, Unicode/graphemes, ANSI and visible controls, manual scrolling,
  filters/scope changes and empty domains. The empty-log reverse regression
  exposed an out-of-range panic; its guard was added after reproducing it.
  The existing horizontal-search regression was also updated in
  `rawlog_horizontal_test.go` to assert the visible starting-column contract.
- Task 2: signed commit `4e2a035`, `Search reconstructed responses by occurrence`.
  Behavioural RED showed the second same-line occurrence remaining hidden.
  Focused GREEN covers repetition, miss/empty/cancel stability, manual scrolling,
  resize, wide Unicode and restoration of raw position and occurrence state.
- Independent task reviews approved both tasks with no findings. Controller
  checks confirmed unchanged raw input/cancellation and decoded-control coverage.
- A separate test-cleanup subagent classified all 17 touched tests (34 distinct
  scenarios) and retained them all. No redundant tests, edits or empty commit.
- Whole-branch review of `b799381..4e2a035` found no critical, important or minor
  issues, substantive plan deviations or deferred findings.

### Terminal verification

Used a sanitised six-line, three-entry fixture with raw continuation matches,
wide Unicode, a provider response and a later raw entry. Ran the actual CLI in
an interactive PTY at 80 columns × 18 rows and 40 columns × 14 rows.

- Raw keys: `6`, `/`, `needle`, Enter, `n`, `N`, `j`, `n`.
  Search revealed physical continuation line 3 at column 4; repetition revealed
  the second occurrence at column 17 and reversed correctly. After scrolling,
  repetition advanced from the new visible position to the provider entry.
- At narrow width, advanced to the provider entry and used `r`, `/`, `needle`,
  Enter, `n`, `N`, `r`, `n`. Both response occurrences were visible in order.
  Closing restored raw entry 2, line 1, column 103; raw repetition continued to
  its second occurrence at column 125.
- At normal width, searched raw `response-first`, opened the response, searched
  `needle` and repeated both ways. Occurrences remained visible with a vertically
  clamped viewport. Closing restored raw column 88 and its original query;
  raw `n` reported exhaustion without moving. Each CLI run exited cleanly.

### Final validation

At `4e2a035`, `gofmt -d .` produced no diff;
`golangci-lint run --timeout=5m` (2.13.2) reported zero issues;
`go test -race -count=1 ./...` passed all ten packages with pristine output;
and `go build ./...` passed. `go mod verify` and `go mod tidy -diff` also
passed, with no dependency changes. Existing TUI goldens passed unchanged.
All feature commits have verified signatures. No merge or push was performed;
remote CI remains a requirement for later remote integration.
