# Physical Source-Line Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a reader jump from the focused Raw Log pane to any valid one-based physical source line, report the first line actually drawn, return to the exact prior investigation, and remove assurances that a diagnose report is safe to share.

**Architecture:** Extend `model.Log`'s existing lazy original-byte line index with a validated physical-line-to-entry lookup; this remains the sole authority for source coordinates and does not depend on `Entry.Lines`, which can saturate. Add one modal numeric prompt and one source-jump primitive to the TUI. Raw rendering will retain source coordinates beside each rendered row so the status describes the first row actually emitted under the active scope and filters.

**Tech Stack:** Go 1.25+, standard library (`sort`, `strconv`), Bubble Tea/Bubbles text input, existing model/TUI test helpers.

**Spec:** `docs/superpowers/specs/2026-09-11-investigation-usability-design.md`, boundary A only (“Physical source navigation and diagnostic wording”); the shared invariants in that spec also apply.

## Global Constraints

- Preserve original bytes, scanner entry ordinals and original per-tier observation indices.
- Source lines are one-based physical lines in the original file, including continuations; entry-relative positions must be labelled as such.
- Keep UI and RPC clocks, durations and qualifications separate.
- Keep whole-log capture quality distinct from selected totals and displayed timeline windows.
- Preserve unavailable values, measured zero, lower bounds, attribution confidence and observations excluded from temporal rendering.
- Escape user-derived text before terminal display.
- Viewing, checking or navigating a capture must not change strict-scrub acceptance.
- The new prompt is modal: command letters become text, Esc cancels without changing the investigation, and Ctrl+C quits.
- Navigation history owns the mutable investigation state; returning restores filters, selected identities, searches, raw position and timeline state, subject only to viewport clamping after resize.
- Do not change report schemas, timing calculations, detection, masking, measurements or report data fields.
- Use Australian/British English in prose and comments, make the smallest reasonable change, and add no dependency or compatibility layer.
- Follow TDD for every production change. Run the test-cleanup skill as a separate worker after implementation, then run final verification.
- Use `/Users/dan/.codex/bin/codex-git` for every Git operation. Every commit must be signed; if signing fails, stop without bypassing signing.

## File Map

- Modify `internal/model/location.go`: share the lazy physical-line index and map a validated one-based physical line to its original entry and entry-relative line.
- Modify `internal/model/location_test.go`: cover LF, CRLF, blank lines, final lines without newline, invalid ranges and saturated continuation counts.
- Modify `internal/tui/model.go`: give the source-line prompt modal precedence and bind `g` only when Raw Log's list pane is focused.
- Modify `internal/tui/rawlog.go`: hold prompt state, perform a history-producing whole-log jump, and retain source identity on rendered rows.
- Create `internal/tui/source_line_input.go`: own numeric prompt creation, rendering, validation, submission and cancellation.
- Modify `internal/tui/rawlog_test.go`: cover prompt validation, exact physical targets, filter widening, search retention and whole-log jump state.
- Modify `internal/tui/history_test.go`: prove cancellation is inert and Esc restores the complete filtered parent investigation.
- Modify `internal/tui/workbench.go`: describe the first Raw Log row actually drawn with absolute physical line as the primary status field.
- Modify `internal/tui/layout.go`: render the modal prompt and advertise the focused Raw Log `g` action.
- Modify `internal/tui/layout_test.go`: cover status agreement, empty/zero-height panes, a filtered-out stored cursor and narrow truncation.
- Modify `internal/tui/help.go`, `internal/tui/help_test.go` and affected help goldens under `internal/tui/testdata/golden/`: document `g` and review the rendered key guide.
- Modify `cmd/tfli/main.go` and `cmd/tfli/main_test.go`: qualify actual `--help` output and correct adjacent source comments.
- Modify `internal/profile/profile.go` and `internal/profile/profile_test.go`: remove the comparison that implies diagnose output is safe to share.
- Modify `internal/diagnose/diagnose_test.go`: correct the source comment that describes diagnose as an output Dan shares.
- Modify `README.md`: retain the heuristic-masking explanation while applying the same “masked; review before sharing” rule.

---

### Task 1: Resolve Physical Lines from Original Byte Offsets

**Files:**
- Modify: `internal/model/location.go`
- Test: `internal/model/location_test.go`

**Interfaces:**
- Consumes: `Log.Data`, `Log.Entries`, the existing `sourceLinesOnce`/`sourceLineStarts` cache, and each entry's exact `Off`/`Len` half-open byte range.
- Produces:

```go
type SourcePosition struct {
	Entry     uint32
	EntryLine uint64 // zero-based physical-line offset within Entry
}

func (l *Log) PhysicalLineCount() uint64
func (l *Log) SourcePosition(line uint64) (SourcePosition, bool)
```

`SourcePosition` accepts only one-based physical lines. It returns false for zero, a line beyond `PhysicalLineCount`, an empty log, or malformed hand-built entry ranges. It must locate the line by its byte start and the entry's `Off`/`Len`; it must never derive the answer from saturating `Entry.Lines` or narrow `line` before validation.

- [ ] **Step 1: Write failing lookup tests over real loaded bytes**

Add table-driven tests that write and load this exact source (and an LF variant) so scanning and lookup are exercised together:

```go
const source = "2026-09-10T00:00:00.000Z [INFO] first\r\n\r\ncontinuation\r\n2026-09-10T00:00:01.000Z [INFO] last"

tests := []struct {
	line      uint64
	wantEntry uint32
	wantLine  uint64
}{
	{line: 1, wantEntry: 0, wantLine: 0},
	{line: 2, wantEntry: 0, wantLine: 1}, // blank continuation
	{line: 3, wantEntry: 0, wantLine: 2},
	{line: 4, wantEntry: 1, wantLine: 0}, // final line has no newline
}
```

Assert `PhysicalLineCount() == 4`, each lookup equals `SourcePosition{Entry: wantEntry, EntryLine: wantLine}`, and `SourceLocation(position.Entry).StartLine+position.EntryLine == line`. Add rejection cases for `0`, `5`, and `math.MaxUint64`, asserting `(SourcePosition{}, false)`.

- [ ] **Step 2: Run the focused tests and observe the missing API failure**

Run: `go test ./internal/model -run 'Test(PhysicalLineCount|SourcePosition)'`

Expected: FAIL to compile because `PhysicalLineCount`, `SourcePosition`, or both do not exist.

- [ ] **Step 3: Factor the existing line-start initialisation and implement the lookup**

Keep the existing lazy `sync.Once` cache, but initialise it through one helper used by `SourceLocation`, `PhysicalLineCount`, and `SourcePosition`:

```go
func (l *Log) indexSourceLines() []uint64 {
	l.sourceLinesOnce.Do(func() {
		if len(l.Data) == 0 {
			return
		}
		l.sourceLineStarts = append(l.sourceLineStarts, 0)
		for i, b := range l.Data {
			if b == '\n' && i+1 < len(l.Data) {
				l.sourceLineStarts = append(l.sourceLineStarts, uint64(i+1))
			}
		}
	})
	return l.sourceLineStarts
}

func (l *Log) PhysicalLineCount() uint64 {
	return uint64(len(l.indexSourceLines()))
}

func (l *Log) SourcePosition(line uint64) (SourcePosition, bool) {
	starts := l.indexSourceLines()
	if line == 0 || line > uint64(len(starts)) {
		return SourcePosition{}, false
	}
	offset := starts[line-1]
	i := sort.Search(len(l.Entries), func(i int) bool {
		return l.Entries[i].Off > offset
	}) - 1
	if i < 0 || uint64(i) > uint64(^uint32(0)) {
		return SourcePosition{}, false
	}
	e := l.Entries[i]
	end := e.Off + uint64(e.Len)
	if e.Len == 0 || end < e.Off || offset < e.Off || offset >= end || end > uint64(len(l.Data)) {
		return SourcePosition{}, false
	}
	location, ok := l.SourceLocation(uint32(i))
	if !ok || line < location.StartLine {
		return SourcePosition{}, false
	}
	return SourcePosition{Entry: uint32(i), EntryLine: line - location.StartLine}, true
}
```

Searching by the monotonic entry start offsets avoids adding `Off+Len` in the search predicate. Keep the exact checked-end guards already used by `SourceLocation` when validating the selected entry. Update `SourceLocation` to call `indexSourceLines()` without changing its public result.

- [ ] **Step 4: Add the saturation regression to the existing large-entry test**

Extend `TestSourceLocationUsesOriginalByteOffsetsBeyondEntryLineSaturation` to call `SourcePosition(65_537)` and `SourcePosition(65_538)`. Assert the first maps to entry 0 at offset 65,536 and the second maps to entry 1 at offset 0 even though `Entries[0].Lines == math.MaxUint16`.

- [ ] **Step 5: Run model location tests**

Run: `go test ./internal/model -run 'Test(SourceLocation|PhysicalLineCount|SourcePosition)'`

Expected: PASS with no output besides the package result.

- [ ] **Step 6: Commit the model primitive**

```bash
gofmt -w internal/model/location.go internal/model/location_test.go
/Users/dan/.codex/bin/codex-git status --short
/Users/dan/.codex/bin/codex-git add internal/model/location.go internal/model/location_test.go
/Users/dan/.codex/bin/codex-git commit -m "Resolve physical source lines"
```

Expected: the commit succeeds with a valid signature. Stop immediately if signing fails.

### Task 2: Add the Modal Source-Line Jump and Exact Return

**Files:**
- Create: `internal/tui/source_line_input.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/rawlog.go`
- Modify: `internal/tui/workbench.go`
- Modify: `internal/tui/layout.go`
- Test: `internal/tui/rawlog_test.go`
- Test: `internal/tui/history_test.go`
- Test: `internal/tui/layout_test.go`

**Interfaces:**
- Consumes: `model.Log.PhysicalLineCount()`, `model.Log.SourcePosition(uint64)`, `captureNavigation()`, `returnFromHistory()`, `setFacetExclusions`, `invalidateRows`, and existing `rawLogState` fields.
- Produces:

```go
type sourceLineInputState struct {
	editing bool
	value   string
	err     string
	input   textinput.Model
}

func newSourceLineInput() textinput.Model
func parseSourceLine(string) (uint64, error)
func (m *Model) beginSourceLineInput()
func (m *Model) handleSourceLineInputKey(tea.KeyMsg)
func (m Model) sourceLinePrompt(width int) string
func (m *Model) jumpToSourceLine(line uint64) bool
```

The prompt state is transient modal state on `Model`, outside `navigationFrame`: cancellation cannot mutate or snapshot the underlying investigation. `jumpToSourceLine` receives an already parsed `uint64`, asks the model to validate and resolve it before any conversion to `int`, and mutates nothing on failure.

Add `sourceLine sourceLineInputState` to `Model`, beside `raw`, as transient modal editor state. Do not add it to `navigationFrame` or `rawLogState`; opening and cancelling the editor must not become part of the investigation snapshot.

- [ ] **Step 1: Write failing prompt validation and modal-key tests**

In `rawlog_test.go`, create a Raw Log model, press `g`, and verify a prompt containing `Go to source line` is visible. Table-test exact invalid submissions:

```go
tests := []struct {
	name  string
	value string
	want  string
}{
	{name: "empty", value: "", want: "enter a source line"},
	{name: "zero", value: "0", want: "source line must be positive"},
	{name: "sign", value: "+1", want: "use decimal digits only"},
	{name: "space", value: "1 2", want: "use decimal digits only"},
	{name: "overflow", value: "18446744073709551616", want: "source line is too large"},
	{name: "past end", value: "999", want: "source line is outside this log"},
}
```

For every row, assert the prompt remains open, its short error is rendered through `logfmt.DisplayText`, history length and `captureNavigation()` remain equal to their pre-prompt values, and the raw position/filter/search state has not moved. Type `q`, `j`, `/`, and `g` while editing in a separate case and assert they are inserted/validated as text rather than dispatched as commands. Assert Ctrl+C still sets quitting.

Add a cancellation case that opens the prompt over a model with nondefault filters, scope, selection, search match and timeline state, types a valid line, then presses Esc. Assert `sourceLine.editing == false`, history depth is unchanged and `captureNavigation()` still equals the pre-prompt frame. A following `j` must scroll Raw Log, proving command dispatch resumed.

- [ ] **Step 2: Write failing jump-state tests for first, last, blank and continuation lines**

Load an LF fixture assembled with a timestamped first entry, a blank continuation, a nonblank continuation, and a final timestamped line without a newline. For each physical line, submit its decimal number and assert:

```go
position, ok := m.log.SourcePosition(line)
if !ok {
	t.Fatal("fixture source position missing")
}
if m.raw.top != int(position.Entry) || m.raw.topLine != int(position.EntryLine) {
	t.Fatalf("raw position = %d+%d, want %d+%d", m.raw.top, m.raw.topLine, position.Entry, position.EntryLine)
}
```

Also assert the target text is the first rendered row, `raw.scope == nil`, `raw.column == 0`, `raw.match == nil`, `raw.lastQuery` is unchanged, `raw.notFound == false`, and exactly one history frame was pushed. Repeat the target assertions with CRLF input.

- [ ] **Step 3: Write the failing filtered-scope return test**

Start from a call-scoped Raw Log reached through Enter. Apply nonempty provider, level, RPC-method and resource-type exclusions plus non-nil resource and module selections; set a raw query, `lastQuery`, match, horizontal column, and exact raw position. Clone the full parent with `captureNavigation()`.

Submit a source line outside the call scope and assert the successful child:

- has `raw.scope == nil`, no provider exclusions and no level exclusions;
- retains the RPC-method/type exclusions and resource/module selections exactly;
- preserves `raw.lastQuery`, clears only `raw.match`/`raw.notFound`, sets column zero, and renders the requested line first;
- makes the timing projection wider when the cleared provider filter had excluded observations; and
- has one additional history frame.

Press Esc once and compare the restored model to the captured frame, including view/pane, selection identity, every filter dimension, nil-versus-empty resource selections, facet search, raw scope/top/topLine/column/query/lastQuery/match, and timeline state. This is the acceptance test for “jump out of a filtered call scope, then return to its exact state”.

- [ ] **Step 4: Implement numeric input without permissive parsing**

In `source_line_input.go`, build a static-cursor `textinput.Model` like `newSearchInput`, with a trusted `Go to source line: ` prompt. Handle keys in this order:

```go
switch msg.Type {
case tea.KeyCtrlC:
	m.sourceLine.editing = false
	m.sourceLine.input.Blur()
	m.quitting = true
case tea.KeyEsc:
	m.sourceLine = sourceLineInputState{}
case tea.KeyEnter:
	line, err := parseSourceLine(m.sourceLine.value)
	if err != nil {
		m.sourceLine.err = err.Error()
		return
	}
	if !m.jumpToSourceLine(line) {
		m.sourceLine.err = "source line is outside this log"
		return
	}
	m.sourceLine = sourceLineInputState{}
default:
	// Convert KeySpace to one rune and pass runes through DisplayText,
	// matching the existing search prompt's terminal-safety rule.
}
```

Implement `parseSourceLine(string) (uint64, error)` by first rejecting empty input and any byte outside `'0'..'9'`, then calling `strconv.ParseUint(value, 10, 64)`, then rejecting zero. This deliberately rejects signs and embedded whitespace that `strconv` or trimming could otherwise accept. Render the error after the prompt, clipped to `width`; never render unescaped typed text.

- [ ] **Step 5: Give the prompt modal precedence and bind `g` at the stated focus**

In `Model.Update`, handle `m.sourceLine.editing` immediately after response-modal handling and before blocked-jump clearing, raw search, help, quality or ordinary commands. Return `tea.Quit` when its handler sets `m.quitting`.

Add this ordinary binding and expose the same predicate in footer help:

```go
case "g":
	if m.view == ViewRawLog && m.pane == PaneList {
		m.beginSourceLineInput()
	}
```

This matches “With Raw Log focused”: `g` is inert in other views and while the facet/detail pane owns the keyboard. In `layout.go`'s focused Raw Log action list, add `g line`; preserve it during narrow-width pruning alongside `/ search`, Esc and quit because it is the only discoverable route to this feature.

In `layout.go`'s `footer`, render `sourceLinePrompt(w)` before facet-search and raw-search states. Size the editable part from the trusted label and error widths, using the same text-input viewport behaviour as `searchPrompt`, so the current digits and short error remain readable at narrow widths. This hook is required for the modal state to be visible rather than merely consuming keys.

- [ ] **Step 6: Implement the successful jump as one atomic navigation transition**

Implement `jumpToSourceLine` in `rawlog.go`:

```go
func (m *Model) jumpToSourceLine(line uint64) bool {
	position, ok := m.log.SourcePosition(line)
	if !ok {
		return false
	}
	maxInt := uint64(^uint(0) >> 1)
	if uint64(position.Entry) > maxInt || position.EntryLine > maxInt {
		return false
	}
	entry, entryLine := int(position.Entry), int(position.EntryLine)
	parent := m.captureNavigation()
	m.history = append(m.history, parent)
	m.raw.scope = nil
	m.setFacetExclusions(dimProvider, nil)
	m.setFacetExclusions(dimLevel, nil)
	m.invalidateRows()
	m.changeView(ViewRawLog)
	m.pane = PaneList
	m.raw.top = entry
	m.raw.topLine = entryLine
	m.raw.column = 0
	m.raw.match = nil
	m.raw.notFound = false
	return true
}
```

Do not call `clearFilters`: method/type/resource/module state must survive in the child. Do not call `reconcileRawCursor` after assigning the target: provider and level are the only dimensions that hide raw entries and have just been cleared. Capture history only after complete range and architecture-safe conversion validation, so every failed submission is inert.

- [ ] **Step 7: Write failing status-agreement tests**

In `layout_test.go`, add a helper that extracts the workbench status row and table-test:

- a jump to a continuation line reports that absolute physical line first and the correct original entry ordinal second;
- a stored cursor on an entry excluded by the level filter reports the later admitted row that the renderer actually draws;
- an empty log reports `no visible source line` and never fabricates `line 1`;
- a filtered-empty pane reports `no visible source line`;
- a terminal height whose pane body is zero reports `no visible source line`; and
- widths 24, 40 and 60 keep `Line <absolute>` visible in full or with the existing explicit cut marker, never silently replace it with entry-relative `line 2`.

For nonempty cases, derive the expectation from `m.log.SourceLocation(uint32(first.entry)).StartLine + uint64(first.entryLine)` and assert the first rendered text belongs to the same row.

- [ ] **Step 8: Refactor the renderer to retain source coordinates**

Add these internal interfaces in `rawlog.go`:

```go
type rawLogLine struct {
	text       string
	entry      int
	entryLine int
	sourceLine uint64 // zero means unavailable for a malformed hand-built Log
}

func (m Model) rawLogRows(height int) []rawLogLine
func (m Model) rawLogLines(height int) []string
```

Move the existing `rawLogLines` traversal into `rawLogRows`. Track `firstEntryLine := 0`, setting it to `m.TopLine()` only for the top entry before slicing its lines. For every emitted physical line, append the styled text and original entry/entry-line coordinates. If `SourceLocation(uint32(i))` succeeds, set `sourceLine = location.StartLine + uint64(entryLine)`; if it fails for a malformed hand-built test log, still append and render the text with `sourceLine == 0` rather than silently dropping a row. Preserve the existing ANSI stripping, control escaping, severity styling, scope/filter order and height limit byte-for-byte. Implement `rawLogLines` as a projection over rows so search width calculations and rendered content remain unchanged.

- [ ] **Step 9: Render absolute physical line as the primary status field**

In the Raw Log branch of `workbenchView`, compute the pane body height exactly once, call `rawLogRows(bodyHeight)`, and format from its first row:

```go
scope := "whole log"
if m.raw.scope != nil {
	scope = "call scope"
}
switch {
case len(rows) == 0:
	status = "No visible source line · " + scope
case rows[0].sourceLine == 0:
	status = fmt.Sprintf("Source line unavailable · entry %d/%d · %s", rows[0].entry+1, len(m.log.Entries), scope)
default:
	first := rows[0]
	status = fmt.Sprintf("Line %d · entry %d/%d · column %d · %s",
		first.sourceLine, first.entry+1, len(m.log.Entries), column+1, scope)
}
```

Here `whole log` names the absence of a call scope. The header and Filters pane continue to disclose retained method/type/resource/module selections separately; do not describe the remaining timing selection as an unfiltered capture. Keep physical line first so right-edge clipping preserves it, and use the existing marked-clipping helper when even that first field cannot fit.

- [ ] **Step 10: Run focused prompt, jump, status and history tests**

Run: `go test ./internal/tui -run 'Test(SourceLine|GoToSource|RawLogStatus|History.*Source)'`

Expected: PASS with pristine output.

- [ ] **Step 11: Commit the complete navigation behaviour**

```bash
gofmt -w internal/tui/model.go internal/tui/rawlog.go internal/tui/source_line_input.go internal/tui/workbench.go internal/tui/layout.go internal/tui/rawlog_test.go internal/tui/history_test.go internal/tui/layout_test.go
/Users/dan/.codex/bin/codex-git status --short
/Users/dan/.codex/bin/codex-git add internal/tui/model.go internal/tui/rawlog.go internal/tui/source_line_input.go internal/tui/workbench.go internal/tui/layout.go internal/tui/rawlog_test.go internal/tui/history_test.go internal/tui/layout_test.go
/Users/dan/.codex/bin/codex-git commit -m "Navigate to physical source lines"
```

Expected: the commit succeeds with a valid signature. Stop immediately if signing fails.

### Task 3: Correct Sharing Qualifications and Document the New Key

**Files:**
- Modify: `cmd/tfli/main.go`
- Test: `cmd/tfli/main_test.go`
- Modify: `internal/profile/profile.go`
- Test: `internal/profile/profile_test.go`
- Modify: `internal/diagnose/diagnose_test.go`
- Modify: `README.md`
- Modify: `internal/tui/help.go`
- Test: `internal/tui/help_test.go`
- Modify if regenerated output changes: `internal/tui/testdata/golden/help-60.txt`
- Modify if regenerated output changes: `internal/tui/testdata/golden/help-100.txt`

**Interfaces:**
- Consumes: actual `run([]string{"--help"}, ...)`, `profile.Render`, `helpGroups`, and existing golden update/read workflow.
- Produces: diagnose help copy containing `masked; review before sharing`, profile copy that says its identifiers are unmasked and also requires review, and a help entry `g` → `go to a physical source line`.

- [ ] **Step 1: Write failing output-contract tests**

Add a CLI test that invokes real help and asserts:

```go
if err := run([]string{"--help"}, io.Discard, &stderr); err != nil {
	t.Fatalf("help: %v", err)
}
out := stderr.String()
if !strings.Contains(out, "output is masked; review before sharing") {
	t.Fatalf("diagnose help lacks review qualification:\n%s", out)
}
for _, forbidden := range []string{"safe to share", "shareable"} {
	if strings.Contains(strings.ToLower(out), forbidden) {
		t.Fatalf("help implies diagnose is %q:\n%s", forbidden, out)
	}
}
```

Strengthen `TestReportWarnsThatOutputIsUnmasked` to require a review-before-sharing instruction and reject `Unlike --diagnose`, `diagnose ... safe`, and `diagnose ... shareable` implications in the rendered profile text. Extend help tests to require `{keys: "g", what: "go to a physical source line"}` and verify `g` acts only in focused Raw Log.

- [ ] **Step 2: Run focused wording/help tests and observe failures**

Run: `go test ./cmd/tfli ./internal/profile ./internal/tui -run 'Test.*(Help|Warns|SourceLine)'`

Expected: FAIL on the old “safe to share”, “Unlike --diagnose”, and missing `g` copy.

- [ ] **Step 3: Replace the assurance consistently**

Apply these concrete copy rules:

- `--diagnose` flag help: `report the log's structure and exit (output is masked; review before sharing)`.
- `cmd/tfli/main.go` package prose: describe diagnose as masked structural output that must be reviewed before sharing; do not call it shareable or contrast other modes against diagnose safety.
- Profile report: `Resource addresses in this report are not masked. Review the report before sharing it.` Keep the existing details about what it exposes; remove “Unlike --diagnose”.
- `internal/profile/profile.go` package prose and related test comments: describe the actual unmasked content and review requirement without calling diagnose shareable.
- `internal/diagnose/diagnose_test.go` disclosure-test comment: state the structural invariant the test proves (resource addresses never appear), without saying Dan shares the output.
- README diagnose paragraph: retain verbatim in substance that masking is heuristic and not a guarantee, and that reports must be reviewed. Remove the later `unlike --diagnose` comparison from profile guidance; keep the profile/TUI warnings and all scrub guidance.

Search maintained prose after edits:

```bash
rg -n -i 'safe to share|safe.*sharing|shareable|Unlike --diagnose' README.md docs cmd internal --glob '!docs/superpowers/specs/**' --glob '!docs/superpowers/plans/**'
```

Expected: no diagnose safety assurance. Any remaining hit must describe another artefact accurately and must not imply diagnose is safe.

- [ ] **Step 4: Add `g` to help and inspect intentional golden changes**

Add the binding under `THE LIST`:

```go
{keys: "g", what: "go to a physical source line"},
```

Regenerate only if the ordinary golden test reports changed help output:

Run: `go test ./internal/tui -run TestGoldenHelp -update`

Then inspect both the semantic rendering and raw style diff:

```bash
scripts/read-golden.sh help-60.txt
scripts/read-golden.sh help-100.txt
/Users/dan/.codex/bin/codex-git diff -- internal/tui/testdata/golden/help-60.txt internal/tui/testdata/golden/help-100.txt
```

Expected: `g` appears with the physical-source-line description; no unrelated layout or styling changes are accepted.

- [ ] **Step 5: Run focused package tests**

Run: `go test ./cmd/tfli ./internal/profile ./internal/diagnose ./internal/tui`

Expected: PASS with pristine output.

- [ ] **Step 6: Commit wording and discoverability**

```bash
gofmt -w cmd/tfli/main.go cmd/tfli/main_test.go internal/profile/profile.go internal/profile/profile_test.go internal/diagnose/diagnose_test.go internal/tui/help.go internal/tui/help_test.go
/Users/dan/.codex/bin/codex-git status --short
/Users/dan/.codex/bin/codex-git add cmd/tfli/main.go cmd/tfli/main_test.go internal/profile/profile.go internal/profile/profile_test.go internal/diagnose/diagnose_test.go README.md internal/tui/help.go internal/tui/help_test.go internal/tui/testdata/golden/help-60.txt internal/tui/testdata/golden/help-100.txt
/Users/dan/.codex/bin/codex-git commit -m "Qualify diagnostic sharing guidance"
```

Expected: the commit succeeds with a valid signature. If a named golden did not change, omit it from `add`. Stop immediately if signing fails.

## Final Controller Verification and Independent Test Cleanup

**Files:**
- Review: every file changed in Tasks 1–3
- Modify only if the independent cleanup finds a demonstrably low-value test: the corresponding `*_test.go` file

- [ ] **Step 1: Dispatch a separate test-cleanup worker**

Use the `test-cleanup` skill in a fresh worker that did not implement Tasks 1–3. Ask it to inspect only the tests added for boundary A, retain tests that prove public behaviour and the saturated-offset/filter/history regressions, and remove only tests that merely duplicate another assertion or mirror implementation details.

- [ ] **Step 2: Review and verify any cleanup edits**

If the cleanup changed tests, inspect the diff and run the affected packages:

Run: `go test ./internal/model ./internal/tui ./cmd/tfli ./internal/profile ./internal/diagnose`

Expected: PASS with pristine output, with all acceptance cases still represented explicitly. If no cleanup was warranted, record that result and make no empty commit.

- [ ] **Step 3: Commit warranted cleanup**

If the cleanup produced justified edits, run `/Users/dan/.codex/bin/codex-git status --short`, stage each changed test file by its explicit path, and commit with `/Users/dan/.codex/bin/codex-git commit -m "Trim source navigation tests"`. Do not create an empty commit. The commit must succeed with a valid signature; stop immediately if signing fails.

- [ ] **Step 4: Run formatting and full verification from a clean index-aware view**

Run:

```bash
gofmt -w internal/model/location.go internal/model/location_test.go internal/tui/model.go internal/tui/rawlog.go internal/tui/source_line_input.go internal/tui/rawlog_test.go internal/tui/history_test.go internal/tui/workbench.go internal/tui/layout.go internal/tui/layout_test.go internal/tui/help.go internal/tui/help_test.go cmd/tfli/main.go cmd/tfli/main_test.go internal/profile/profile.go internal/profile/profile_test.go internal/diagnose/diagnose_test.go
go test ./...
go test -race -count=1 ./...
golangci-lint run
go build ./...
/Users/dan/.codex/bin/codex-git diff --check
/Users/dan/.codex/bin/codex-git status --short --branch
```

Expected: formatting makes no uncommitted semantic change, all tests pass, all packages build, `diff --check` is silent, and status shows only intentional work. If `gofmt` changes tracked files, review, rerun affected tests, and commit those formatting changes with the relevant task rather than leaving them uncommitted.

- [ ] **Step 5: Manually exercise the sanitised fixture journey**

Run: `go run ./cmd/tfli testdata/multiline-body.log`

Verify in the terminal:

1. Focus Raw Log and use `g` to visit line 1, a blank/continuation line, and the final physical line.
2. Confirm the requested row is at the top and the status's absolute line agrees with the source file.
3. Enter invalid, overflowing and out-of-range values; confirm the prompt remains open and the underlying investigation does not move.
4. Cancel with Esc; confirm no history was created.
5. From a filtered call scope, jump outside it; confirm the status says `whole log`, provider/level are widened, horizontal position is zero, and the submitted raw search remains repeatable with `n`/`N`.
6. Press Esc once; confirm the exact call scope, filters, raw position, search and selection return.
7. Resize narrowly and confirm the physical line remains visible or explicitly marked as truncated.

Expected: all boundary-A acceptance journeys behave as specified; do not use private captures or screenshots.
