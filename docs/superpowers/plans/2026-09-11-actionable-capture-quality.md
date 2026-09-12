# Actionable Capture Quality Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make capture-quality findings navigable and let readers explicitly check responses without blocking the terminal or losing their investigation.

**Architecture:** Keep reconstruction and atomic publication in `model.Log` behind its existing `sync.Once`. Run inspection and response presentation in Bubble Tea commands that capture immutable inputs, then apply results through capture and request identifiers. Represent quality content as prose plus identified actionable records; store only value-owned selection and scroll state in navigation history.

**Tech Stack:** Existing Go 1.25 toolchain, Bubble Tea, Bubbles viewport, Lip Gloss, ANSI helpers and standard-library tests. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-11-investigation-usability-design.md`, shared invariants and boundary D. Baseline `66fff81`; topic branch `feature/actionable-capture-quality`. A–C are on main. Boundary D is implemented on this branch; the delivery record below describes validation and review.

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
- Running is a transient TUI state, not a new reconstruction state in JSON v1.
- Ordinary CLI reporting remains lazy. No new CLI validation mode is introduced.
- No export or scrub output is produced by checking responses.
- Use `/Users/dan/.codex/bin/codex-git` for Git operations and signed commits. Stop on signing failure. Use sanitised fixtures; never copy private captures.

## Existing contracts and file map

`internal/model/response.go` already reconstructs once, builds all message/diagnostic range indexes, then publishes `responseChecked` with an atomic store. `ReconstructionQuality` checks that flag before reading the result. Keep this publication boundary; neither a renderer nor a quality getter may enter `sync.Once.Do` and wait for an inspection.

`ProviderResponseAt(entry uint32, lineOffset int)` is a synchronous model operation used by tests, benchmarks and the TUI. It preserves original physical coordinates and detaches exposed fragments. Keep its semantics; change the TUI caller to run in a command. The present `openResponse()` performs both inspection and JSON formatting in the key handler. Move both out of that handler.

`quality.go` currently generates flat prose and scrolls a viewport. `navigationFrame` does not include quality state. `jumpToSourceLine(uint64) bool` in `rawlog.go` already validates the coordinate, captures history, clears obstructing raw restrictions and enters Raw Log. Reuse that primitive, capturing the still-open panel before hiding it.

- Modify `internal/model/response.go`: public explicit check entry point and detached, non-triggering diagnostic access.
- Modify `internal/model/response_benchmark_test.go`: rename the two direct private inspection calls.
- Extend `internal/model/provider_response_test.go` and `quality_test.go`: lazy access, detached metadata and publication races.
- Create `internal/tui/response_check.go`: shared check command, completion messages and generation guards.
- Modify `internal/tui/response.go`: pending requests and pure response presentation, retaining verified-body and escaping rules.
- Modify `internal/tui/model.go`: transient coordinator fields, completion-message dispatch and returned commands for `r`.
- Create `internal/tui/response_check_test.go`: real command execution, stale results and event-loop responsiveness.
- Modify existing response/search/recovery tests where helpers currently discard commands.
- Create `internal/tui/quality_records.go`: record identities, diagnostic ordering and wrapped-row/action mapping.
- Modify `internal/tui/quality.go`: selectable actions, safe diagnostics and existing explanatory content.
- Modify `internal/tui/history.go`: value-owned quality snapshot and restoration.
- Extend `internal/tui/quality_test.go`, `history_test.go` and existing recovery journeys; add focused quality record tests.
- Modify `internal/tui/help.go`, `workbench.go`, `layout.go`, `README.md` and relevant goldens for pending and quality navigation guidance.

## State and ownership decisions

There is one inspection command in flight per TUI capture. Repeated check actions join it rather than scheduling another command. The model's existing `sync.Once` also protects concurrent callers outside that TUI instance. Closing a view does not cancel the inspection, and no worker reads or writes `*tui.Model`.

Use two result stages: an inspection-complete message contains the capture and inspection generation; a response-ready message contains the capture and response request ID. On inspection completion, resolve whichever response request is currently waiting for inspection, using its saved original entry/line offset. Distinguish waiting for inspection from an already queued lookup/formatting command so one request cannot schedule presentation twice. The formatting command returns its result tagged with that request ID. Reject results for a different capture, a closed response, a newer request or a quitting model. Closing/reopening never reuses a request ID.

The quality panel initially selects a `Check responses` action at the top. Opening the panel or rendering it never starts work. Keep that action identifiable after completion so publication cannot shift selection onto an unrelated anomaly. Checked results are cached; activating the action again displays the existing outcome rather than retrying a failed immutable capture.

Quality selection uses stable IDs: check action, anomaly `(Stage, Code)`, or original reconstruction diagnostic index. Do not use wrapped-line numbers as identity. Page keys keep ordinary prose scrollable: choose a visible actionable record when one exists; a page containing only prose has no highlighted selection. Up/down from such a page selects the nearest action in that direction and reveals it. This explicitly handles prose-only pages without making prose selectable or skipping the guide.

## Task 1: Explicit inspection and safe diagnostic access

**Files:** `internal/model/response.go`, `provider_response_test.go`, `quality_test.go`, `response_benchmark_test.go`.

**Interfaces:**
- Consume existing `Log.responseOnce`, `responseChecked`, response slices and indexes.
- Produce `func (l *Log) InspectProviderResponses()` by exporting the existing private method and updating its callers, without changing its body or publication ordering.
- Produce `func (l *Log) ReconstructionDiagnostics() []logfmt.ProviderJSONDiagnostic`, a detached content-free snapshot that does not start or wait for inspection.
- Preserve `ProviderResponseAt` and `ReconstructionQuality` signatures and outcomes.

- [x] **Step 1: Add failing behavioural tests.**

Use the existing `loadResponseLog(t testing.TB, source string) *Log` helper in `provider_response_test.go`, which writes and loads a real temporary capture. Do not manufacture a checked flag or inject a fake reconstruction result.

```go
func TestExplicitInspectionWithoutSourceSelection(t *testing.T) {
    l := loadResponseLog(t, "ordinary log line\n")
    if got := l.ReconstructionDiagnostics(); len(got) != 0 {
        t.Fatalf("unchecked diagnostics = %#v", got)
    }
    if l.ReconstructionQuality().State != "not_checked" {
        t.Fatal("diagnostic access triggered inspection")
    }
    l.InspectProviderResponses()
    q := l.ReconstructionQuality()
    if q.State != "complete" || q.Responses != 0 || q.Diagnostics != 0 {
        t.Fatalf("zero-response inspection = %+v", q)
    }
}
```

Extend the existing real malformed/recovered/ambiguous cases to check complete, partial and failed counts. After inspecting, mutate a returned diagnostic's code/coordinates and append elements; another snapshot must retain the original values. Every returned `Ranges` and `Unavailable` must be nil. Preserve original diagnostic ordering for downstream identity.

- [x] **Step 2: Record RED.**

Run `go test ./internal/model -run 'TestExplicitInspection|TestReconstructionDiagnostics' -count=1`; missing exported methods must fail before implementation. Name the new metadata cases with the second prefix.

- [x] **Step 3: Expose the existing operation and add the getter.**

Rename the private method and all its actual callers, including benchmarks. Keep `responseChecked.Store(true)` after all five result/index assignments. The getter is:

```go
func (l *Log) ReconstructionDiagnostics() []logfmt.ProviderJSONDiagnostic {
    if !l.responseChecked.Load() {
        return nil
    }
    out := append([]logfmt.ProviderJSONDiagnostic(nil), l.responseDiagnostics...)
    for i := range out {
        out[i].Ranges = nil
        out[i].Unavailable = nil
    }
    return out
}
```

The getter exposes structural metadata only; do not add bodies, snippets, fragment enumeration or output operations. No new persistent state is needed in `Log`.

- [x] **Step 4: Verify concurrency and commit.**

Extend the existing atomic-publication test with concurrent real `InspectProviderResponses`, `ReconstructionDiagnostics` and `ReconstructionQuality` calls. Before publication, readers see `not_checked` and no diagnostics; after publication every diagnostic/range lookup is complete. Avoid comparing two independently timed getter calls as though they were one atomic snapshot. Keep concurrent first-selection tests and invalid-position laziness tests.

Run focused model tests, `go test -race ./internal/model`, `go test ./...`, formatting and diff checks. Commit explicit paths with signed subject `Expose explicit response inspection and safe diagnostics`. Request task review and a separate test-cleanup pass.

## Task 2: Non-blocking response checks and request-safe completion

**Files:** create `internal/tui/response_check.go` and `response_check_test.go`; modify `response.go`, `model.go`, `workbench.go` and existing response/quality/recovery/search test helpers.

**Interfaces:**
- Consume Task 1's model methods and existing `rawResponsePosition()`.
- Produce `func (m *Model) requestResponseCheck() tea.Cmd` for the shared check.
- Change `func (m *Model) openResponse() tea.Cmd`; the `r` dispatcher returns its command.
- Produce `func responseInspectionCmd(l *model.Log, generation uint64) tea.Cmd`, `func responseSelectionCmd(l *model.Log, request responseRequest) tea.Cmd` and `func presentResponse(selection model.ProviderResponseSelection) responseState`.
- Produce `func (m *Model) completeResponseInspection(msg responseInspectionDoneMsg) tea.Cmd` and `func (m *Model) completeResponseSelection(msg responseReadyMsg)`.
- Produce `func (m *Model) queueResponseSelection() tea.Cmd` as the single scheduling gate and `func (m *Model) responseNavigationHint() string` for pending-aware guidance in both the response footer and workbench navigation.

- [x] **Step 1: Add failing deferred-command journeys.**

Use the real `responseModel` helper and preserve commands rather than discarding them:

```go
func TestResponseFirstRequestIsPendingBeforeCommandRuns(t *testing.T) {
    m := responseModel(t, `{"message":"verified"}`)
    _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
    if cmd == nil || !m.response.open || !m.response.pending {
        t.Fatal("first response did not return a pending command")
    }
    if m.log.ReconstructionQuality().State != "not_checked" {
        t.Fatal("key handler performed reconstruction")
    }
    if !strings.Contains(unstyled(m.View()), "Checking responses") {
        t.Fatal("pending response not visible")
    }
    inspectionDone := cmd() // actual reconstruction, outside Update
    _, resolve := m.Update(inspectionDone)
    if resolve == nil { t.Fatal("pending source was not resolved") }
    m.Update(resolve()) // actual lookup and formatting
    if m.response.pending || !strings.Contains(m.renderResponse(100, 20), "verified") {
        t.Fatal("verified response was not published")
    }
}
```

Test repeated `requestResponseCheck` returns no second command while running. Close the response before executing the inspection command; its completion must not reopen it. Also close/reopen at a different physical position, then deliver the old response-ready result after the newer result; only the newer request may remain visible. Deliver a completion from a different `*model.Log` and after quit; neither may change the current view.

Cover publication before message delivery: start a check, execute its real command but hold the completion message, then open a response. The checked cache must schedule one response-selection command. Deliver the held inspection-complete message before that selection command finishes; it must return nil rather than queue another lookup/formatting pass. Execute the original selection command and verify the requested body appears. While waiting for inspection and while preparing the response, full `View()` output must advertise close/quit only, with no search, repeat-match or scroll hints. After completion those controls return.

Run `go test ./internal/tui -run 'TestResponseFirstRequest|TestResponseCheck|TestResponseCompletion' -count=1` and record RED.

- [x] **Step 2: Implement the command/message protocol.**

Add these value types and state fields; never reset the counters on modal close:

```go
type responseInspectionState struct {
    running bool
    generation uint64
}
type responseRequest struct {
    id uint64
    entry uint32
    lineOffset int
    valid bool
}
type responseInspectionDoneMsg struct {
    log *model.Log
    generation uint64
}
type responseReadyMsg struct {
    log *model.Log
    requestID uint64
    presentation responseState
}
// Model: inspection responseInspectionState; nextResponseRequestID uint64
// responseState: pending bool; resolving bool; request responseRequest

func responseInspectionCmd(l *model.Log, generation uint64) tea.Cmd {
    return func() tea.Msg {
        l.InspectProviderResponses()
        return responseInspectionDoneMsg{log: l, generation: generation}
    }
}

func (m *Model) requestResponseCheck() tea.Cmd {
    if m.log.ReconstructionQuality().State != "not_checked" || m.inspection.running {
        return nil
    }
    m.inspection.generation++
    m.inspection.running = true
    return responseInspectionCmd(m.log, m.inspection.generation)
}
```

On `openResponse`, increment `nextResponseRequestID`, snapshot the original raw entry and line offset, and initialise an open status view containing `Checking responses…` plus `Closing this view leaves the check running.` Set `pending=true` and `resolving=false`. If already checked, return `queueResponseSelection`; otherwise call `requestResponseCheck`. Return its command if nonnil. If it returns nil, recheck the published outcome: a checked result queues selection, while a still-running check is joined without scheduling another command. This also handles publication between the two quality reads. Empty raw captures still permit the explicit whole-capture check; their invalid source request resolves to the existing no-response status.

Inspection completion is handled as its own top-level message case, before key/modal routing. Reject a different capture, mismatched generation, duplicate completion when not running, or a quitting model. Mark the matching check no longer running and return `queueResponseSelection()`. That gate returns nil for a closed response or one already being prepared. Quality results become visible through the model's published getters without reopening the quality panel.

```go
func (m *Model) queueResponseSelection() tea.Cmd {
    r := &m.response
    if m.quitting || !r.open || !r.pending || r.resolving {
        return nil
    }
    if m.log.ReconstructionQuality().State == "not_checked" {
        return nil
    }
    r.resolving = true // set before returning the command
    r.lines = []string{"Preparing response…"}
    r.viewport.SetContent(strings.Join(r.lines, "\n"))
    return responseSelectionCmd(m.log, r.request)
}

func responseSelectionCmd(l *model.Log, request responseRequest) tea.Cmd {
    return func() tea.Msg {
        selection := model.ProviderResponseSelection{State: "none"}
        if request.valid {
            selection = l.ProviderResponseAt(request.entry, request.lineOffset)
        }
        return responseReadyMsg{log: l, requestID: request.id,
            presentation: presentResponse(selection)}
    }
}
```

Extract the existing formatting/escaping body of `openResponse` into `presentResponse`: initialise its own viewport and return a complete `responseState`, without reading a TUI model. Preserve fragment counts, decoded messages, incomplete/unavailable statuses and the partial-capture notice. `responseSelectionCmd` runs only after inspection is checked, keeping lookup and potentially large formatting outside Update.

On response-ready, require the same log, an open pending and resolving response, matching `request.id` and `!m.quitting`; transfer the presentation and retain the current request value. Set `pending=false` and `resolving=false`. Closing and quit keep their current meanings; while pending, accept close/quit but swallow search and scroll keys.

Pending guidance is `Esc/r back` in the navigation row and `q quit` in the action row. Add the shared helper below and use it in both `responseFooter` and the response branch of `workbenchView`; the latter currently hard-codes `responseNavigation`, so a footer-only change is insufficient. In `responseFooter`, check pending before search/not-found branches and return those two pending rows. Completed views retain their existing navigation and search hints.

```go
func (m *Model) responseNavigationHint() string {
    if m.response.pending { return "Esc/r back" }
    return responseNavigation
}
```

- [x] **Step 3: Adapt tests to execute actual commands and verify responsiveness.**

Existing `responseKey` and `qualityKey` helpers currently throw away commands. Use the following helper explicitly after response-opening/check commands in existing completed-response journeys; pending/race tests control delivery themselves. Leave key-only helpers unchanged where they handle quit, search or scrolling. Do not silently change the generic `update` helper to execute every command, since callers use it to inspect transient states and quit messages.

```go
func drainResponseCommands(t *testing.T, m *Model, cmd tea.Cmd) {
    t.Helper()
    for i := 0; cmd != nil; i++ {
        if i == 8 { t.Fatal("response command cycle") }
        _, cmd = m.Update(cmd())
    }
}
```

For race coverage, execute a real inspection command in a goroutine, send its message through a buffered channel, and continue rendering quality, resizing, closing/reopening and issuing quit on the test's single UI goroutine. Never run concurrent `Update`/`View` calls on `*Model`. Use real malformed and mixed recovery fixtures, not fake parsers, sleeps, global counters or production test hooks. Deferred command execution proves Update does not inspect even on machines where reconstruction completes immediately.

Keep tests for verified JSON only, escaped controls, physical continuation lines, ambiguous tails and raw search restoration. Run focused tests, full TUI tests and `go test -race ./internal/tui ./internal/model`.

- [x] **Step 4: Verify and commit.**

Run `go test ./...`, `go build ./...`, `golangci-lint run`, formatting and diff checks. Commit explicit paths with signed subject `Keep response inspection outside terminal updates`. Request task review and separate test cleanup before Task 3.

## Task 3: Selectable quality records, diagnostics and exact return

**Files:** create `internal/tui/quality_records.go` and `quality_records_test.go`; modify `quality.go`, `history.go`, `quality_test.go`, `history_test.go`, recovery journeys, help/layout/workbench and README.

**Interfaces:**
- Consume `requestResponseCheck() tea.Cmd`, `ReconstructionDiagnostics`, `ReconstructionQuality`, `SourceLocation`, `jumpToSourceLine(uint64) bool` and existing navigation history.
- Produce `qualityItemID`, `qualityRecord`, `qualityActionRow`, `qualityNavigationState` below.
- Produce `func (m *Model) qualityRecords(q model.CaptureQuality, reconstruction model.ReconstructionQuality) []qualityRecord`, `func wrapQualityRecords(records []qualityRecord, width int) ([]string, []qualityActionRow)` and `func diagnosticSourceLine(d logfmt.ProviderJSONDiagnostic) uint64`.
- Produce `func (m *Model) captureQualityNavigation() qualityNavigationState` and `func (m *Model) restoreQualityNavigation(state qualityNavigationState)`; add one value field of that type to `navigationFrame`.

- [x] **Step 1: Add failing record and source-return tests.**

Use these exact primary-coordinate expectations:

```go
func TestQualityDiagnosticPrimarySource(t *testing.T) {
    for _, tc := range []struct {
        d logfmt.ProviderJSONDiagnostic
        want uint64
    }{
        {logfmt.ProviderJSONDiagnostic{SyntaxLine: 9, Line: 7, StartLine: 2}, 9},
        {logfmt.ProviderJSONDiagnostic{Line: 7, StartLine: 2}, 7},
        {logfmt.ProviderJSONDiagnostic{StartLine: 2}, 2},
        {logfmt.ProviderJSONDiagnostic{}, 0},
    } {
        if got := diagnosticSourceLine(tc.d); got != tc.want {
            t.Fatalf("primary coordinate = %d, want %d", got, tc.want)
        }
    }
}
```

Use `qualityModel` for real anomaly navigation: open `i`, move from Check responses to a located anomaly, save the panel ID/offset and `captureNavigation`, press Enter, verify Raw Log's exact physical source line, alter child filters/search, resize, then Esc. Assert panel ID/offset (clamped only for size), original filters, raw query/match, original selection and timeline state. Close quality once more and verify the parent investigation remains intact. Include a timeline parent with explicit UI selection from `timing-tiers.log`.

Also test nil/invalid `FirstEntry`, zero issues, empty captures and nonzero count filtering; no-location rows are selectable but Enter never pushes history or changes investigation. Record tests should check mixed located/unlocated diagnostics, ties by original index, one primary target and a distinct `response starts at` secondary label. A diagnostic with a syntax coordinate must not expose its other trigger coordinate as another target.

Run `go test ./internal/tui -run 'TestQuality' -count=1` and record RED.

- [x] **Step 2: Build records without duplicating the explanatory guide.**

```go
type qualityItemID struct {
    kind string // "check", "anomaly", "diagnostic"; empty for prose
    stage, code string
    index int // original diagnostic index
}
type qualityRecord struct {
    id qualityItemID
    text string
    sourceLine uint64
}
type qualityActionRow struct {
    id qualityItemID
    start, end int // wrapped row interval, end exclusive
    sourceLine uint64
}
type qualityNavigationState struct {
    open bool
    selected qualityItemID
    offset int
}

func diagnosticSourceLine(d logfmt.ProviderJSONDiagnostic) uint64 {
    for _, line := range []int{d.SyntaxLine, d.Line, d.StartLine} {
        if line > 0 { return uint64(line) }
    }
    return 0
}
```

Move the current guide construction into `qualityRecords`, retaining all timing, anomaly-count, attribution, context and reconstruction wording. Prose has an empty ID. `qualityText` remains a thin join of the resulting texts for the existing content tests; there must not be a second copy of the guide. Prepend the identified `Check responses` action; while running append `— checking responses…` and the prose `Closing this panel leaves the check running.` Never show a percentage.

Keep the existing complete/partial/failed count wording; distinguish `complete: 0 responses available` from failure. Running takes precedence over `not_checked` in the TUI, but after atomic publication use the checked outcome even if its completion message has not yet been delivered. The action must not start inspection when rendered.

Anomaly records retain `(Stage, Code)` identity and the original first-example wording. Obtain their source line through `SourceLocation(*FirstEntry).StartLine`; nil or unresolved locations explicitly say `location unavailable`.

In `renderQuality`, first read `ReconstructionQuality` and pass that same snapshot to `qualityRecords`; only fetch diagnostics if the supplied snapshot is checked. The checked outcome never changes, so this ordering keeps counts and rows consistent when publication occurs during rendering. A not-checked snapshot renders no diagnostics and can refresh on the next frame. Obtain the detached snapshot once per build, retain original indexes, then sort by nonzero primary coordinate ascending, unlocated last, original index as tie-break. Format only escaped diagnostic code, primary `source line N` or `location unavailable`, and a distinct positive start coordinate as `response starts at line N`. Do not use `diagnostic.Error()` here: it can name multiple competing coordinates. Do not enumerate range/fragment arrays or include source content.

`wrapQualityRecords` wraps escaped text using the existing ANSI-aware helper and maps each actionable record to its full wrapped interval. Reserve two columns for a prefix on every wrapped row, clamping gracefully below two columns. The renderer replaces that blank prefix with `> ` on the selected record's first row and applies the existing selected style; it does not wrap again. Selection therefore remains visible with `NO_COLOR` and does not change action coordinates.

- [x] **Step 3: Implement movement, paging and history ownership.**

Add `selected qualityItemID` and `notice string` to `qualityState`; the viewport remains an owned live object, not a history field. Initialise selection to `qualityItemID{kind: "check"}`. Build action intervals at the current viewport width before key movement. Up/down selects adjacent actions, with endpoint clamping, and minimally adjusts the offset so the record's first row is visible. A wrapped record taller than the viewport remains one action.

Page keys move by the viewport's existing page step, then select the first visible action for PgDown or last for PgUp. If only prose is visible, clear the ID; Enter is inert and up/down searches from the current visible interval. On resize, rewrap and preserve the ID, clamping the offset and revealing the selected record if necessary. At zero content height, retain ID and defer visibility adjustment. On result publication, preserve ID and offset; newly appended diagnostics must not steal selection or scroll position.

The quality key dispatcher handles Enter as follows:

```go
// action is the selected qualityActionRow, resolved against current records.
if action.id.kind == "check" {
    return m, m.requestResponseCheck()
}
if action.sourceLine != 0 && m.jumpToSourceLine(action.sourceLine) {
    m.quality.open = false // only after jump captured the open parent
}
return m, nil
```

No selected action means a no-op. Never close the panel before the navigation primitive captures history, and never append a second history frame. If a nonzero target is rejected by the jump primitive, set `m.quality.notice = "Source location unavailable."`, leave history untouched and display it in the quality workbench status row; clear that notice on the next quality key. A zero target already displays its unavailable label and does not attempt a jump. Keep `i`/Esc close and q/Ctrl+C quit behaviour.

`captureQualityNavigation` returns only open/selected/offset values. `restoreQualityNavigation` constructs a new viewport, disables mouse-wheel navigation, restores values, then rebuilds content at the current size before clamping the offset. Restore after the parent view, filters, raw and timeline state have been restored. Do not call `openQuality` on history return because that selects Check responses again. Do not snapshot, rewind or cancel the shared inspection or counters in history: new published results remain available when returning to a saved panel.

- [x] **Step 4: Cover combined asynchronous and privacy journeys.**

Exercise Check responses with a command held pending, repeated Enter, panel close, opening `r`, resizing, command completion, response close and reopening quality. Verify one inspection command, completion counts, active-request identity, no unexpected modal reopening and responsive quit. Execute the real commands; do not substitute fake results for the reconstruction integration cases. Deliver saved real messages out of order to test request guards.

Use `testdata/response-recovery.log` and existing ambiguous/malformed synthetic fixtures to verify content-free diagnostic rows and verified recovered bodies. Run existing strict-scrub rejection tests on those captures; checking must not make a diagnostic-bearing capture acceptable. Profile and JSON tests must retain their current lazy states and v1 contracts. Add no export action or CLI flag.

Update help and README to describe selecting quality actions/records, first-example jumps, pending response checks, cached results and Esc closing without cancellation. Make the quality navigation line `Esc/i close  ↑↓ select  PgUp/PgDn page`; put `Enter open/check  q quit` in the action row. Check the full composed workbench at 60/100/160 columns and short heights, not only `footer()` in isolation. Preserve current parent raw/timeline hints on return.

- [x] **Step 5: Verify, review terminal output and commit.**

Run focused tests, full `go test ./...`, `go test -race -count=1 ./...`, `go build ./...`, `golangci-lint run`, formatting and diff checks. Regenerate affected goldens only intentionally with `go test ./internal/tui -update`; inspect raw ANSI diffs and `scripts/read-golden.sh` output before accepting them.

Use real PTY sessions at 100 and 60 columns with colour and `NO_COLOR`: select a located anomaly, jump and return, check responses, open a recovered body, return to diagnostics, resize and close. Include a prose-only page, an unlocated record, an empty capture, and a sufficiently large sanitised capture for interactive pending/quit observation. Automated tests control command delivery for deterministic timing; PTY success must not depend on sleeping long enough to catch a transient frame. Record real source coordinates, selected markers and absence of body content in diagnostic views.

Commit explicit paths with signed subject `Navigate quality findings and check responses explicitly`. Request task review and separate test cleanup, then a whole-branch review of race/publication, stale requests, history ownership and diagnostic privacy. Fix verified findings and re-review their changes before handoff. Keep the branch local until Dan requests integration.

## Planning self-review

Task 1 covers explicit whole-capture inspection, zero-message outcomes, detached diagnostic access and publication. Task 2 covers ordinary `r`, pending presentation, shared work, closure, quit, capture/request guards and formatting outside Update. Task 3 covers selectable actions, paging, first-example and primary diagnostic navigation, owned panel history, content-free diagnostics, documentation and combined terminal verification. All tasks consume the same named interfaces; no JSON, scrub acceptance, CLI mode or display-window changes are proposed.

The prose-only paging rule is made explicit above so implementations cannot solve visible-selection reconciliation by skipping non-actionable guide sections. No unresolved implementation dependencies remain; review this plan before starting code.

## Plan review corrections

- A check can publish its cache before its completion message reaches Update. Opening a response in that interval queues presentation from the ready cache; the delayed completion must not queue it again. Task 2 now records the resolving phase before command dispatch and routes both triggers through one gate, with a deterministic real-command ordering test.
- Pending responses intentionally ignore search and scrolling, but the existing response footer and workbench advertise those keys independently. Task 2 now specifies shared pending-aware navigation, close/quit-only actions, full-view assertions and a separate preparing status once inspection has completed.

These corrections change the plan only. Re-tracing first inspection, joining, cached response opening, delayed completion, closed/replaced requests and final result delivery leaves one presentation command per request and no inactive controls advertised in a pending view.

## Delivery record — 11 September 2026

Boundary D is complete on `feature/actionable-capture-quality`, based on `66fff81`. All implementation commits are signed. The branch remains local pending integration.

- Model inspection and detached diagnostics: `1242a66`, with coverage refinement in `13da360`.
- Deferred response inspection and presentation: `e04eb48`, with request and concurrent-rendering regressions in `568f515`.
- Selectable quality records and source-return history: `12a12dd`, with exact offset restoration, publication wording, narrow layout and navigation corrections in `814599d`, `6a4c14d` and `09174e9`.
- Syntax diagnostics retain labelled response-start context while targeting only the primary syntax coordinate: `27ed743`.

Task reviews and independent test-cleanup passes completed for all three tasks and their fixes. The final whole-branch review identified one diagnostic-context mismatch; its fix passed scoped re-review. No outstanding, deferred or parked findings remain, and no design rulings were required.

Final verification at `27ed743`: `go test ./...`, `go test -race -count=1 ./...`, `go build ./...` and `golangci-lint run` all passed across the eleven packages; lint reported zero issues. Formatting and diff checks were clean. Existing strict-scrub and lazy reporting tests remain passing.

Real terminal journeys passed at 100 and 60 columns with colour and `NO_COLOR`: anomaly and diagnostic source jumps, exact panel return including a nonzero saved offset, invalid-body withholding, verified recovered bodies, resizing, unlocated and prose-only Enter behaviour, cached checks and an empty capture with zero responses. A large synthetic capture verified pending close/quit responsiveness; raw and response search regressions also passed. Unit tests additionally cover 160-column and short layouts, controlled command ordering, concurrent publication, explicit UI timeline history and arrow movement through tall records. Only sanitised fixtures were used.

Terminal evidence is retained locally under `/private/tmp/tfli-quality-terminal`, `/private/tmp/tfli-response-check-terminal` and `/private/tmp/tfli-quality-search-terminal`; the reproduced offset failure is archived under `/private/tmp/tfli-quality-offset-red`. Per-task scratch reports are removed after this delivery record is committed; signed Git history retains the implementation and review fixes.
