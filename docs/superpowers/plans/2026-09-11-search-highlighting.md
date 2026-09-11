# Visible Search Occurrences Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Visibly identify the active literal search occurrence in Raw Log and reconstructed responses without changing search order, source identity or terminal layout.

**Architecture:** Extend the existing displayed-text match coordinates with one shared grapheme-aware styling function. Raw Log applies it while rendering the matching physical row. Response rendering styles only the visible rows in a temporary viewport, leaving the original plain response lines and navigation viewport unchanged. Preserve the existing match anchors after misses, but suppress their styling while `notFound` is true.

**Tech Stack:** Go 1.25, existing Bubble Tea, Bubbles viewport, Lip Gloss and `charmbracelet/x/ansi`; no dependency changes.

**Spec:** `docs/superpowers/specs/2026-09-11-investigation-usability-design.md`, shared invariants and boundary B only. Baseline `9c7c25b`; work on `feature/search-highlighting`.

## Global Constraints

- Original bytes, scanner entry ordinals and original per-tier observation indices remain stable through filtering, navigation and rendering.
- Source lines are one-based physical lines in the original file, including continuations. Entry-relative lines must be labelled as such if displayed.
- UI and RPC retain separate clocks, durations and qualifications. No combined total, automatic clock alignment or claim of causal blocking is introduced.
- Whole-log capture quality remains whole-log. Selected totals and displayed timeline windows must have their own explicit scope labels.
- Preserve unavailable values, measured zero, lower bounds, attribution confidence and observations excluded from temporal rendering.
- User-derived text is escaped before terminal display. Search highlighting must not reintroduce source control sequences.
- Strict scrubbing still refuses every reconstruction diagnostic. Viewing, checking or navigating a capture never changes scrub acceptance.
- New prompts are modal: typed command letters are text. Esc cancels without changing the underlying investigation; Ctrl+C retains the quit behaviour.
- Navigation frames own their mutable state. Returning restores filters, selection identity, searches, raw position and timeline state exactly, subject only to viewport clamping after terminal resize.
- Keep case sensitivity, non-overlapping occurrence order and non-wrapping next/previous behaviour. Highlight only the active occurrence, never count or decorate all matches.
- Expand only the visual style range to whole intersected grapheme clusters. Do not alter the recorded literal byte offset, query, column or occurrence ordering.
- Preserve severity styling outside the highlight; reverse video must remain visible with colour disabled. Do not introduce a new colour.
- Follow TDD and existing formatting. Every Git operation uses `/Users/dan/.codex/bin/codex-git`; commits must be signed. A signing failure stops work without a bypass. Scratch reports remain ignored and must never be force-added.

## File map and delivery boundaries

- `internal/tui/search_match.go`: shared styling helper beside existing literal coordinates and grapheme traversal.
- `internal/tui/rawlog.go`: style the active matching row after stripping source ANSI and escaping controls, before horizontal clipping.
- New `internal/tui/search_highlight_test.go`: focused rendered-style assertions and real Raw Log journeys, reusing `horizontalLog` and existing test input helpers.
- `internal/tui/response.go`: render active response styling from visible rows without mutating searchable text or scroll anchors.
- `internal/tui/response_test.go`, `internal/tui/response_recovery_journey_test.go`, `internal/tui/history_test.go`: extend existing journeys where useful; adapt existing plain-text assertions to strip application styling when they assert text rather than style.
- `README.md`: describe the active highlight beside existing search guidance.

Task 1 delivers the shared primitive and working Raw Log highlighting. Task 2 consumes that primitive for responses and completes cross-view lifecycle coverage. Independent cleanup follows each implementation; a whole-branch review and real-terminal verification follow both tasks.

## Existing lifecycle contracts

Raw matches are `{entry, line, text literalPosition}` and belong to `raw.lastQuery`, not the editable `raw.query`. Response matches belong to `response.query`, not its text input. The rendered line's byte offset is authoritative for slicing; `literalPosition.column` is only a navigation/display coordinate.

Both search implementations intentionally retain the last match on failed `n`/`N`. It is an exclusive anchor for reversing direction, not a newly successful result: use `match != nil && !notFound` for styling. Do not clear this anchor to hide the highlight. Existing manual scroll, filter invalidation and jumps clear matches; response close preserves the underlying raw match; navigation frames deep-copy it; resize preserves it and clamps only view geometry. Keep these transitions, including existing query editing and cancellation behaviour.

### Task 1: Highlight the active Raw Log occurrence

**Files:** Modify `internal/tui/search_match.go` and `internal/tui/rawlog.go`; create `internal/tui/search_highlight_test.go`; extend `internal/tui/history_test.go` for restored styling; adjust existing raw search test assertions only where the new ANSI boundaries affect text comparisons.

**Interfaces:**

Consumes `literalPosition`, `ansi.FirstGraphemeCluster`, `styles.selected`, `semantic.forLevel`, `raw.match`, `raw.lastQuery` and `raw.notFound`.

Produces:

```go
func renderLiteralMatch(line, query string, position literalPosition, base lipgloss.Style) string
```

`line` is already safe displayed text with no source ANSI. Invalid, empty, mismatched or out-of-bounds ranges return `base.Render(line)` without highlighting. The helper does not change any model state.

- [ ] **Step 1: Write failing styling and Raw Log journey tests.**

Use real loaded bytes through `horizontalLog`. A representative repeated-occurrence journey is:

```go
m := horizontalLog(t, "needle first needle second")
m.raw.lastQuery = "needle"
if !m.searchFrom(0, true, true) {
	t.Fatal("first occurrence missing")
}
first := m.renderRawLog(80, 1)
m.searchAgain(1)
second := m.renderRawLog(80, 1)
m.searchAgain(1)
miss := m.renderRawLog(80, 1)
if !m.raw.notFound || m.raw.match == nil {
	t.Fatal("miss must retain the exclusive reversal anchor")
}
m.searchAgain(-1)
back := m.renderRawLog(80, 1)
```

Assert the styled byte ranges identify the first then second occurrence, that `miss` has no reversed text, and that `back` highlights the first again. Compare stripped text and display widths with their unhighlighted equivalents; do not assert whole rendered strings remain identical across a miss because the highlight intentionally disappears.

After a successful search, open `/`, type a different unsubmitted query and cancel. Assert the previously submitted occurrence stays highlighted while editing and after cancellation; the editable query must not determine its range.

Use a small test-only SGR reader, if needed, to track reverse on (`7`), reverse off (`27`) and reset (`0`) and report which displayed text is reversed. It must tolerate combined SGR parameters (for example reverse plus bold and red) rather than only matching a literal `ESC[7m`. Reuse existing `unstyled`, `sgrPrefix` and `lastSGR` where applicable. Assert resets prevent styling from leaking into adjacent text or subsequent rows.

Cover these distinct boundaries:

| Safe line/query | Visual expectation |
| --- | --- |
| `needle first needle second` / `needle` | Exactly the chosen occurrence reverses |
| `Café next` / combining acute accent | Entire `é` cluster reverses, literal byte offset unchanged |
| `界needle` / `needle` | Byte and display-column offsets remain distinct |
| `👩‍💻 next` / `💻` | Entire joined emoji reverses |
| `a\\tb` / `\\t` | Visible escaped tab reverses; no literal terminal tab introduced |
| Empty query, negative offset, offset beyond line, mismatched substring | Base style only, no panic |

For raw severity, use a loaded ERROR or WARN line with text before and after the query; preserve original severity attributes outside the active range, inherit them within the reversed range, and verify reverse video remains with `styles = newTheme(false)` and `semantic = newSemantics(false)`, restoring globals with `t.Cleanup`. Never run tests that change those globals in parallel.

Include source ANSI splitting a literal, C0/C1 controls and invalid UTF-8, comparing against the existing `StripANSI` then `DisplayText` result. Exercise horizontal clipping through `renderRawLog`, including a match wider than the viewport and a wide/combining character at a clipping edge. Manual scrolling, a real facet change, and a source jump must remove the highlight; Esc must restore a valid parent match from history. Extend an existing history test rather than duplicating its state-only assertions.

- [ ] **Step 2: Run the focused tests and record RED.**

Run `go test ./internal/tui -run 'Test(RawSearchHighlight|LiteralMatchHighlight)' -count=1`. The missing helper or absent reverse styling must explain the failure; correct test-fixture mistakes separately.

- [ ] **Step 3: Implement the bounded shared helper.**

Use the existing grapheme API, avoiding a new Unicode dependency:

```go
func renderLiteralMatch(line, query string, position literalPosition, base lipgloss.Style) string {
	start := position.byteOffset
	if query == "" || start < 0 || start > len(line) || len(query) > len(line)-start || line[start:start+len(query)] != query {
		return base.Render(line)
	}
	end := start + len(query)
	first, last := start, end
	remaining, offset, state := line, 0, -1
	for remaining != "" {
		cluster, rest, _, nextState := ansi.FirstGraphemeCluster(remaining, state)
		clusterEnd := offset + len(cluster)
		if offset <= start && start < clusterEnd {
			first = offset
		}
		if offset < end && end <= clusterEnd {
			last = clusterEnd
			break
		}
		remaining, offset, state = rest, clusterEnd, nextState
	}
	return base.Render(line[:first]) + styles.selected.Inherit(base).Render(line[first:last]) + base.Render(line[last:])
}
```

In `rawLogRows`, compute `entryLine` before styling. After the existing source stripping and `DisplayText`, style only the selected row:

```go
entryLine := firstEntryLine + j
if match := m.raw.match; match != nil && !m.raw.notFound && match.entry == i && match.line == entryLine {
	line = renderLiteralMatch(line, m.raw.lastQuery, match.text, style)
} else if marked {
	line = style.Render(line)
}
```

Keep `renderRawLog`'s existing `ansi.Cut` after this step. Preserve every source coordinate in `rawLogLine`; no change to `findLiteral`, search order or scope traversal is needed.

- [ ] **Step 4: Verify GREEN, inspect styling and commit.**

Run the focused tests, then `go test ./internal/tui`, and the full `go test ./...` before committing. Inspect actual SGR spans, clipping resets and both colour settings. If old tests compare prose split by the new style boundaries, change only those assertions to compare `unstyled` text and retain their original positional checks. Run `gofmt` on changed Go files and `/Users/dan/.codex/bin/codex-git diff --check`. Stage only explicit changed paths and make a signed commit with subject `Highlight active raw log search matches`. The controller then requests review and separate test cleanup.

### Task 2: Highlight responses and verify the complete search lifecycle

**Files:** Modify `internal/tui/response.go`, `internal/tui/response_test.go`, `internal/tui/response_recovery_journey_test.go` and `README.md`; reuse or extend `internal/tui/search_highlight_test.go` for shared styled-output assertions.

**Interfaces:** Consume Task 1's `renderLiteralMatch` and test-only style assertions. Keep `responseState`, `r.lines`, original `viewport` content, `responseMatch`, query ownership and search routines as the authoritative existing state. No persistent highlight cache or replacement search engine.

- [ ] **Step 1: Write failing response/lifecycle tests.**

Extend existing real `responseModel` journeys with actual ANSI assertions. Start with `{"message":"needle first needle second"}` reconstructed from split provider fragments; submit `/needle`, then `n`, a failing `n`, and `N`. Assert exactly one visible occurrence highlights on success, none on the miss, the retained anchor still permits reversal, and original `r.lines` and query/match offsets do not change during rendering.

Adapt the existing `TestResponseSearchMissAndEmptyInputPreserveOccurrence` to compare stripped text for its no-movement assertion; separately check that failed search suppresses styling. Other existing `strings.Contains(..., "needle first")` assertions may need `unstyled` because a reset now separates `needle` from the following space. Do not weaken their scroll/position assertions.

Also verify opening `/`, editing an unsubmitted replacement and cancelling keeps the committed response occurrence highlighted; neither the input text nor the `searching` flag owns the active match.

Cover decoded multi-line `@message`, source controls displayed as escapes, combining/wide/emoji clusters, a query wider than the pane, and an active match partially clipped at the left edge after resize. Include a long offscreen line that sets the original horizontal maximum: styling a shorter visible line must not reset the real horizontal column to a smaller visible-only maximum.

Use existing response notice/resize tests to prove the partial-reconstruction notice retains its row and is never highlighted, including notice-only height one. The matching response line index must be relative to response content, not the notice. Manual horizontal/vertical movement removes response highlighting; closing the response restores the original raw highlight. A failed search does not mutate the raw investigation or scrub acceptance.

- [ ] **Step 2: Run the focused tests and record RED.**

Run `go test ./internal/tui -run 'Test(ResponseSearchHighlight|ResponseSearchMissAndEmptyInputPreserveOccurrence|ResponseRecoveryJourney)' -count=1`. Expected failure: no reverse styling on the response, while prior plain-text search behaviour remains intact.

- [ ] **Step 3: Render only the visible response slice through a temporary viewport.**

Keep the existing geometry/clamping at the start of `renderResponse`. After updating the original viewport dimensions and offsets, derive `body` from `r.viewport.View()` by default. When there is a successful active match on a visible content line, construct the styled visible slice instead:

```go
body := r.viewport.View()
if match := r.match; match != nil && !r.notFound {
	start := r.viewport.YOffset
	end := min(len(r.lines), start+bodyHeight)
	if match.line >= start && match.line < end {
		visible := make([]string, end-start)
		for i := start; i < end; i++ {
			line := r.lines[i]
			if i == match.line {
				line = renderLiteralMatch(line, r.query, match.text, styleRenderer.NewStyle())
			}
			visible[i-start] = ansi.Cut(line, r.column, r.column+w)
		}
		display := r.viewport
		display.SetContent(strings.Join(visible, "\n"))
		display.SetYOffset(0)
		display.SetXOffset(0)
		body = display.View()
	}
}
```

Return the existing notice plus `body`, or just `body`. Clipping the styled lines before putting them in the temporary viewport deliberately avoids clamping the authoritative horizontal offset against only the shorter visible subset. The copy exists only for padding/rendering: never assign it back to `r.viewport`. Leave original searchable response lines and full-content scroll bounds untouched. Only visible lines are joined and re-indexed; never rebuild the full response on every frame.

- [ ] **Step 4: Document and verify the delivered behaviour.**

Add one sentence beside README search guidance: the active match is highlighted in Raw Log and reconstructed responses; `n`/`N` visit individual occurrences without wrapping. Do not advertise counts, regex, multiple highlights or automatic wrapping.

Run focused response/highlight tests and `go test ./internal/tui`; then `go test ./...`, `go test -race -count=1 ./...`, `golangci-lint run`, `go build ./...`, formatting and diff checks. Inspect styled output rather than only stripped strings. Stage explicit changed paths and make a signed commit with subject `Highlight active response search matches`. The controller requests task review and separate test cleanup.

## Final controller verification

Run a real PTY journey with sanitised raw and fragmented-response input containing repeated literals, Unicode clusters and escaped controls. Capture original ANSI output and inspect active reverse spans at normal and narrow widths, including a clipped occurrence, miss/reverse recovery, scrolling, response return, resize and `NO_COLOR`. Validate rendered text/width and search position independently of the styling.

Review the complete branch against the shared invariants and boundary B; fix findings and re-review the fix diff. Retain the test-cleanup classification and TDD/verification evidence in the ignored per-plan workspace while work is active. Record final results in this plan before removing that scratch workspace. Keep the feature branch local until Dan requests integration; boundaries C–H remain separate work.
