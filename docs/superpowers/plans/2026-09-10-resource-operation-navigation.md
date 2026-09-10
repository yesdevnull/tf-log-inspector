# Resource Operation Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Inspect each observed UI operation behind a resource row, open its source, and navigate to inferred RPC associations without silently widening the selection.

**Architecture:** Add an operation-list mode within Resources, backed by the existing projection's original UI indices. Extend E1 history snapshots with that mode and its sort state. Reuse Calls and its original RPC indices for associated evidence; do not invent operation-to-RPC attribution or a new numbered view.

**Tech Stack:** Go 1.25+, existing Bubble Tea, Bubbles and Lip Gloss dependencies; standard Go tests.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), shared rules and item 4, Boundary E2. Requires completed/reviewed [E1 navigation history](2026-09-10-investigation-navigation.md).

## Global Constraints

- “Use the existing Go toolchain and dependencies.”
- “Source bytes and scanner entry identities remain authoritative. Derived views, reconstructed text and filters never modify either.”
- “UI-hook duration measures a resource operation. RPC duration measures a provider call. They overlap and must never be added, subtracted to claim unexplained time, or represented as interchangeable measurements.”
- “An inferred address always retains its confidence.”
- “Whole-second UI measurements retain their rounding qualification.”
- “Explicit numbered view changes end the current drill-down chain”.
- “Do not rewrite these packages wholesale.”
- TDD with real parsing/rendering, independent task review and separate test-cleanup agent after each implementation task.
- Use `/Users/dan/.codex/bin/codex-git`, signed commits, and stop immediately on signing failure. No push or main merge without authorisation.

---

## Status, dependencies and behaviour

Planning scope approved by Dan on 10 September 2026; implementation awaits review of both E1/E2 plans. Read E1 and its final evidence before executing this plan. The base Resources projection exists at `85d58a7`; E1's new interfaces are requirements here, not claims about that commit.

Enter on a Resources aggregate snapshots the parent, replaces `resourceSelection.Addresses` with the exact clicked singleton, preserves all other dimensions and opens observed operations. The list shows action, observed duration and exact physical source line; full address and qualifications remain accessible in detail and the scrollable `e` panel. Repeated actions/addresses remain separate by original UI index. Source opening is available even when the observation has no timeline position.

The operation list represents the active selection. Its initial scope is the clicked resource; subsequent explicit facet edits may change it, as in other child views. Include the address column so deliberately selecting more addresses remains intelligible. Do not retain a hidden clicked-address constraint alongside visible filters. Empty selection stays empty. Esc restores the original Resources row and complete parent selection.

Within the operation list, `c` opens associated Calls and pushes another frame. It preserves every selection dimension, including an explicitly empty address map. Those calls are associated with selected addresses across the capture, not proven members of the selected individual UI operation. Facet edits are explicit user actions; neither `c` nor Enter on a source broadens timing selection. Number `4` remains manual navigation that ends history.

No named RPCs means “No named RPC associations in this selection”, accompanied by the existing evidence route; it does not mean the resource made no calls. `c` still opens an empty Calls list so its limitations and route back are visible. Confidence and partial-association wording stay visible in this context, even below the detail-pane width threshold.

If explicit child facet edits remove both address and module constraints, the
projection can also admit unresolved calls. In that state use neutral “Calls
for current selection” wording, show each actual confidence/no-context state,
and never label the entire list as named associations. `c` preserves that
intentional selection change instead of silently reapplying a resource filter.

## File responsibilities and interfaces

| File | Responsibility |
| --- | --- |
| New `internal/tui/resource_operations.go` | Operation rows, sort/table binding, rendering and detail text |
| New `internal/tui/resource_operations_test.go` | Original UI identities, display qualifications and source navigation |
| New `internal/tui/resource_navigation_test.go` | Full resource → operation/source/Calls → back workflows |
| `internal/tui/history.go` | Save/restore operation mode, operation sort and associated-call context |
| `internal/tui/drilldown.go` | Resource singleton route and associated Calls action |
| `internal/tui/model.go`, `internal/tui/views.go` | Mode state, row routing, sorting and key handling |
| `internal/tui/layout.go`, `internal/tui/help.go`, `internal/tui/resource_evidence.go` | Titles, accessible detail/evidence and accurate action hints |
| `internal/tui/resources_test.go`, `internal/tui/resources_workflow_test.go` | Replace explicitly obsolete inert-Enter expectations with drill-down assertions |
| `README.md`, affected `internal/tui/testdata/golden/` files | Navigation instructions and terminal rendering coverage |

Use existing `model.ResourceProjection.UIIndices`, `.RPCIndices`, `model.ResourceRow.Operations`, `span.Span.RPC` (UI hook action), and `(*model.Log).SourceLocation(entry uint32) (model.SourceLocation, bool)`. Never derive physical lines from `Entry.Lines` or expose an ordinal as a physical line.

Task 1 introduces these fields on Model and `navigationFrame`:

```go
resourceOperations bool
operationSort int
```

Task 2 additionally introduces `associatedCalls bool` on both. They are presentation/navigation state, not extra filters. Manual `setView` clears both context booleans. Internal `changeView` does not clear history; callers explicitly set the child presentation mode. Restore all fields before rebuilding rows.

## Task 1: Inspect and open individual observed UI operations

**Files:** Create `internal/tui/resource_operations.go`, `internal/tui/resource_operations_test.go`; modify `internal/tui/history.go`, `internal/tui/drilldown.go`, `internal/tui/model.go`, `internal/tui/views.go`, `internal/tui/layout.go`, `internal/tui/resource_evidence.go`, `internal/tui/resources_test.go`, `internal/tui/resources_workflow_test.go`.

**Interfaces:** Consumes E1 `captureNavigation() navigationFrame`, `changeView(View)`, `returnFromHistory() bool`, `selectionIdentity`, `enterHint() string`. Produces:

```go
func (m *Model) openResourceOperations() bool
func (m *Model) operationRows() []row
func (m *Model) selectedUIOperation() (int, bool)
func (m *Model) renderResourceOperations(w, h int) string
func (m *Model) operationDetailSections(index, w int) []paneSection
func (m *Model) activeTable() (tableBinding, bool)
func (m *Model) activeSort() int
func (m *Model) setActiveSort(col int)
```

`activeTable` returns the operation binding only for `ViewResources && resourceOperations`, otherwise the existing `tables[m.view]`. `activeSort`/`setActiveSort` select `operationSort` in that same mode, otherwise `sortCol[m.view]`. Route row sorting, sort key, table rendering and sort hint through these helpers so header and behaviour cannot diverge.

`cycleSort` currently has a Resources-specific observed-column cycle. Limit
that branch to aggregate Resources (`!m.resourceOperations`); operations cycle
their own four columns. Preserve the aggregate restriction against ranking by
inferred RPC totals. Update `renderResources` to use the active sort helper
without changing its aggregate columns or default ranking.

- [ ] **Step 1: Write a failing original-identity regression.** Import Bubble Tea and use existing real fixture/helper functions:

```go
func TestResourceOperationsRetainRepeatedSourceIdentity(t *testing.T) {
    m := New(testLog(t, "resources-modules.log"), "resources-modules.log")
    m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
    pressRune(t, &m, '3')
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
    rows := m.rows()
    if len(rows) != 2 { t.Fatalf("operations = %d, want 2", len(rows)) }
    if rows[0].identity.kind != "ui" || rows[0].identity.index != 1 ||
        rows[1].identity.kind != "ui" || rows[1].identity.index != 0 {
        t.Fatal("duration-ranked operations lost original UI identity")
    }
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
    if m.view != ViewRawLog { t.Fatal("operation did not open source") }
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
    if m.view != ViewResources || len(m.rows()) != 2 ||
        m.rows()[m.selected].identity.index != 1 {
        t.Fatal("source return lost selected operation")
    }
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
    if m.view != ViewResources || len(m.rows()) != 1 || m.rows()[0].resource == nil {
        t.Fatal("second return did not restore aggregate")
    }
}
```

Extend source assertions to prove the opened target is UI index 1's entry, allowing existing context lines above it. Test selection of the other operation, sorting then opening, equal durations/actions with separate entries, a missing action, zero duration, and `resources-long-lower-bound.log` (source works while position is unavailable). Start with multiple addresses selected and verify the child singleton then parent restoration; repeat from nil address selection. Verify module/type/provider/method/severity selections survive unchanged.

- [ ] **Step 2: Confirm RED.** Run `go test ./internal/tui -run '^TestResourceOperations' -count=1`; E1 still returns one aggregate row. Compile with E1's `row.identity` available; fail on behaviour rather than missing E2 fields.

- [ ] **Step 3: Implement the operation route and rows.**

```go
func (m *Model) openResourceOperations() bool {
    if m.view != ViewResources || m.resourceOperations { return false }
    r, ok := m.selectedRow()
    if !ok || r.resource == nil { return false }
    parent := m.captureNavigation()
    m.resourceSelection.Addresses = map[string]bool{r.resource.Address: true}
    m.resourceOperations = true
    m.operationSort = 2
    m.history = append(m.history, parent)
    m.changeView(ViewResources)
    m.selected = 0
    return true
}
```

Set operation columns to address, action, observed UI duration, source line (indices 0–3); default duration descending, then exact address, action, source entry and original UI index. Copy projection indices before sorting. Populate `row.identity` with `selectionIdentity{kind: "ui", index: index}` and keep `spanIdx: noSpanIdx`; never make a UI row satisfy `isCall`. Format action/address using `logfmt.DisplayText`. A missing action renders “unavailable”, not a guessed lifecycle action. Source failure renders “unavailable” and disables source opening instead of using a misleading line zero.

Use actual observation facts for qualification:

```go
s := m.log.UISpans[index]
duration := model.DurationTotal{
    Count: 1, TotalMs: uint64(s.DurationMs),
    MaxMs: s.DurationMs, LowerBound: s.DurationSaturated,
}
durationText := durationTotalText(duration)
location, hasLocation := m.log.SourceLocation(s.Entry)
```

Default rows remain duration-ranked observations, with `≥` visible for saturation. Show rounding, start clamp and position unavailability separately in full detail/evidence. Do not interpret saturated values as exact durations or use unavailable positions to reject valid duration rows.

Handle `selectedUIOperation` before `spanForRow` in detail rendering, and before RPC `jumpTarget` resolution. Validate original index and source entry. Pass `m.log.UISpans, index` to the existing validated raw-jump path; it retains raw severity/provider visibility policy and reports blocked targets without pushing history. No provider is inferred for UI spans and no timestamp is required for source opening. Preserve E1 history mode/identity when returning.

Render the operation list under Resources, title `OBSERVED UI OPERATIONS`, keeping number 3 highlighted. Use `renderTable` with the active table/sort helpers. At narrow width prioritise action/duration/source; the selected full address must remain reachable through `e`, including 60×9. Extend `resourceEvidenceText` with an explicit selected-operation section containing full escaped address, action, source range, duration and its qualifications, followed by the unchanged selected-scope baseline. Do not call it a resource aggregate or apply RPC attribution to the UI entry.

Use Enter dispatch order: aggregate provider/type route, resource-operation route, individual source jump. Generate the resource aggregate `↵ operations` and operation `↵ log` hints from the same availability checks. Change only the tests that deliberately asserted the old inert Enter; retain their original identity checks.

- [ ] **Step 4: Verify GREEN, accessibility and history.** Run `go test ./internal/tui -count=1`. Check rows from real parsed fixtures, operation detail/evidence at narrow widths, exact source identities after sorting, empty child after facet edits, quality unchanged, modal Esc before history, resize and manual number 3 resetting to aggregate mode. Include a selected address with controls/long Unicode text: display is escaped, selection identity stays exact. Independent review then separate test-cleanup agent must pass; repeat affected tests after changes.

- [ ] **Step 5: Commit.** Inspect diff/status, stage task files explicitly with the wrapper and signed commit `Open observed resource operations and their source logs`. Record RED/GREEN and review evidence here.

## Task 2: Navigate from operations to associated Calls

**Files:** Create `internal/tui/resource_navigation_test.go`; modify `internal/tui/drilldown.go`, `internal/tui/history.go`, `internal/tui/model.go`, `internal/tui/views.go`, `internal/tui/layout.go`, `internal/tui/help.go`, `README.md`, affected TUI goldens and this plan/spec completion records.

**Interfaces:** Consumes task 1's operation mode and E1's snapshot stack. Produces `func (m *Model) openAssociatedCalls() bool` and `associatedCalls bool` presentation context. The call list still uses `selectedResources().RPCIndices`, `callRowsForIndices` and existing attribution confidence; no new model association function.

- [ ] **Step 1: Write a failing real-key association workflow.**

```go
func TestResourceAssociatedCallsPreserveSelectionAndReturn(t *testing.T) {
    m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
    m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
    pressRune(t, &m, '3')
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
    before := maps.Clone(m.resourceSelection.Addresses)
    pressRune(t, &m, 'c')
    if m.view != ViewCalls || len(m.rows()) != 1 {
        t.Fatalf("associated Calls: view %v, rows %d", m.view, len(m.rows()))
    }
    if !reflect.DeepEqual(before, m.resourceSelection.Addresses) {
        t.Fatal("associated Calls changed resource selection")
    }
    i := m.rows()[0].spanIdx
    if m.log.AttributionForEntry(m.log.RPCSpans[i].Entry).Address != "aws_instance.a" {
        t.Fatal("associated Calls included another address")
    }
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
    if m.view != ViewResources || !m.resourceOperations {
        t.Fatal("associated Calls did not return to operations")
    }
}
```

Import `maps`, `reflect`, `testing` and Bubble Tea. Add an empty-association case using `resources-modules.log`, checking the qualified empty explanation and Esc restoration. Toggle the sole address off in the operation child and press `c`: zero RPC rows must remain zero; the route must not reinstate a singleton. Preserve type/module/provider/method selections too. Use a parsed mixed-confidence fixture to verify Contained/Likely/Overlapping remain distinguishable and Ambiguous/Unattributed never acquire a chosen address.

Use the actual facet `o` action to clear the singleton back to nil with no
module constraint, then press `c`. Assert the full selected RPC set is retained,
including unresolved observations, with neutral current-selection wording and
correct per-call confidence/no-context labels. This is distinct from an empty
allow-list. Repeat a nil-address but active-module selection to retain the named
module-selection semantics.

- [ ] **Step 2: Confirm RED.** Run `go test ./internal/tui -run '^TestResourceAssociated' -count=1`; `c` currently has no route.

- [ ] **Step 3: Implement the context-preserving transition.**

```go
func (m *Model) openAssociatedCalls() bool {
    if m.view != ViewResources || !m.resourceOperations { return false }
    parent := m.captureNavigation()
    m.history = append(m.history, parent)
    m.resourceOperations = false
    m.associatedCalls = true
    m.changeView(ViewCalls)
    m.selected = 0
    return true
}
```

Bind `c` only while the list pane has focus and operation mode is active; existing modal dispatch consumes it before this handler. Advertise `c calls` there, including the empty operation list. Snapshot/restore `associatedCalls`; manual numbered navigation clears it. With an active address/module constraint, show Calls title/preamble identifying inferred associations for the current selection and explicitly saying they are not assigned to an individual UI operation. When both constraints are nil, use the neutral current-selection wording described above; recompute that distinction after facet edits in Calls too.

In associated Calls mode, include a confidence column at all supported widths, shortening address/type/provider columns first. Reuse `AttributionForEntry` and the existing confidence formatter; keep original RPC indices unchanged. Detail and `e` retain full explanations, confidence partitions, unresolved potentially relevant work, and whole-log quality remains separate. The normal Calls mode need not gain new columns.

Extend `activeTable` to return the associated-call table binding and build rows with matching cell/numeric lengths. Keep existing Calls columns in their original order and append confidence, so ordinary Calls sort columns remain valid; store an `associatedCallSort int` in Model/history for the extended table so its extra sort column cannot leak into ordinary Calls. Extend `activeSort`/`setActiveSort` accordingly. Default uses the current Calls duration order. Treat any shared table selection predicate consistently in the sort key, footer and rendering.

When no selected RPC observations remain under an address/module constraint, render “No named RPC associations in this selection” and “This does not establish that no provider calls occurred.” Without either constraint, use the ordinary Calls no-matches/no-data distinction instead. Keep `e evidence` and accurate Esc-back guidance available; do not fabricate zero UI durations or broaden filters to make a call appear. If user edits facets to deliberately widen the child selection, update context wording to the current selection and continue using the existing model projection.

- [ ] **Step 4: Verify integration, docs and real terminal behaviour.** Run focused tests and `go test ./internal/tui -count=1`. Complete type → Resources → operation → Raw → Esc → operations → `c` Calls → Raw → response → back through every parent. Verify parent sort/filters/identity after child edits; empty results, no named RPCs, lower bounds, modal precedence, request expansion, number-key reset and 100×30 → 60×9 resize. Confirm operation selection is not presented as temporal RPC ownership. Update README/help with `c`, distinct numbered navigation and return semantics. Regenerate only intentional goldens using the existing update mechanism, inspect each rendered golden via `scripts/read-golden.sh` and its raw styling diff. Independent review then separate test cleanup must pass.

- [ ] **Step 5: Run final validation and commit.** Run the checklist below, then signed commit `Navigate from observed operations to inferred calls`. Record evidence in E1/E2 and mark Boundary E complete only after the combined implementation review passes. No remote merge/push is implied.

## Final validation

- [ ] `go test -race -count=1 ./...`, `go build ./...`, `gofmt -d .`, `go mod tidy -diff`, `go mod verify`, `golangci-lint run --timeout=5m`.
- [ ] Match existing CI build matrix (linux/darwin × amd64/arm64, `CGO_ENABLED=0`, packages and `-trimpath` CLI), without introducing a tool or dependency. Report local versus remote checks accurately.
- [ ] Real PTY evidence at 100×30 and 60×9, including operation source, associated empty Calls, response modal, history restoration and filters edited in a child. Do not substitute state assertions for terminal inspection.
- [ ] Full E1/E2 independent review checks singleton replacement, nil/empty selections, repeated original identities, map isolation, responsive qualifications, confidence visibility, unchanged capture quality and no source/filter scope widening by navigation actions.
- [ ] Check signed commits, diff whitespace and clean worktree; write completion evidence with no unsupported claims.

## Planning self-review

Item 4's resource singleton/operation/source requirements map to task 1; associated Calls, confidence and no-named-RPC wording map to task 2. E1 supplies parent restoration and aggregate routing; both tasks extend rather than bypass its snapshots. Active table/sort bindings handle the two contextual list variants without adding a numbered view. UI `RPC` contains the recorded hook action, and physical lines use the existing source-location index. This document specifies proposed code and tests; it does not claim they are implemented or passing.

Independent plan review on 10 September 2026 identified misleading association
wording after explicit removal of both named constraints. The plan now requires
neutral current-selection wording in that state, including live edits in Calls,
and a regression using the real facet action. Re-review confirmed all E1/E2
findings resolved and readiness for Dan's plan review. Documentation link,
code-fence, placeholder and staged whitespace checks passed; application tests
were not run for this documentation-only change.
