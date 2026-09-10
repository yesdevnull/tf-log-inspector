# Investigation Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Open provider/type investigations with Enter and restore each parent investigation exactly with Esc.

**Architecture:** Replace the TUI's single return destination with explicit navigation snapshots. Keep manual numbered navigation separate from history-preserving transitions; reuse the current filtering and timing projections. Capture stable domain identities and original observation indices, never cached row pointers or filtered-slice indices.

**Tech Stack:** Go 1.25+, existing Bubble Tea, Bubbles and Lip Gloss dependencies; standard Go tests.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), shared rules and item 4, Boundary E1. E2 is [resource operation navigation](2026-09-10-resource-operation-navigation.md).

## Global Constraints

- “Use the existing Go toolchain and dependencies.”
- “Source bytes and scanner entry identities remain authoritative. Derived views, reconstructed text and filters never modify either.”
- “Whole-log quality facts remain labelled as whole-log facts.”
- “Maintain navigation history only for explicit drill-down actions.”
- “Modal search/help/response dismissal takes precedence over history.”
- “Explicit numbered view changes end the current drill-down chain”.
- “Do not rewrite these packages wholesale.”
- TDD with real model/rendering behaviour; independent review and a separate test-cleanup agent after each implementation task.
- Use `/Users/dan/.codex/bin/codex-git` for Git. Signed commits are mandatory; stop immediately if signing fails. No push or main merge without authorisation.

---

## Status and scope

Dan approved this plan and authorised subagent implementation on 10 September 2026. Both tasks are implemented and individually reviewed; combined E1/E2 review is complete. Work started from Boundary D at `85d58a7`. The execution record below distinguishes implementation evidence from the original planning notes.

Deliver E1 before E2. E1 leaves Resources aggregate Enter inert, but type → Resources works. Response reconstruction remains modal and retains its current parser/policy. No new CLI flags, dependencies, attribution rules or measurement calculations.

## Behaviour decisions

1. Enter acts only in the list pane. Providers open Calls with exactly that provider; types open Calls if the clicked row has any selected RPC observations, otherwise Resources if it has selected UI observations. Zero-duration calls count as observations. Use the active projection to choose the route, not whole-log type availability or non-zero duration.
2. Replace only the clicked dimension. Preserve type/provider/method/severity/resource/module selection in all other dimensions, including nil versus empty selections. Legacy provider/type facets continue using exclusions; do not migrate every facet to a new representation.
3. Esc order remains modal dispatch first (response and its search, raw/facet search input, help, quality, resource evidence), then one history frame, then committed chooser-query clearing, then filters. An open facet overlay is existing focus/layout state, not a new modal history layer.
4. Number keys end history even when selecting the current view. They preserve active structured filters, clear request scope as today, and do not restore a parent. Backslash expands raw request scope without changing history.
5. Child filter edits and sorting are local to the child snapshot. On return, restore the parent's filters, sort, selected identity, raw position/search/scope and focus. Retain the current terminal dimensions; clamp restored positions/focus to the current layout.
6. Table scroll is currently derived from selection by `renderTable`; there is no independent table viewport offset to save. Raw physical line and horizontal column are explicit. Timeline selection must resolve against rebuilt lanes using the original tier/index identity.
7. A refused raw jump (hidden target or invalid source entry) pushes nothing. History is session-local, contains no log copies and has no arbitrary depth cap. Repeated manual view changes release its snapshots.

## File responsibilities and shared interfaces

| File | Responsibility |
| --- | --- |
| New `internal/tui/history.go` | Snapshot/restore, deep copies of mutable selection state, stable selection resolution |
| New `internal/tui/history_test.go` | Parent restoration and precedence regressions |
| New `internal/tui/drilldown.go` | Aggregate target selection and singleton filtering |
| New `internal/tui/drilldown_test.go` | Provider/type routes and filter preservation |
| `internal/tui/model.go` | Model history field, manual/internal transitions, Enter/Esc dispatch |
| `internal/tui/rawlog.go` | Commit successful raw jumps to history after validating target visibility |
| `internal/tui/views.go` | Raw domain identity on aggregate rows, history-aware empty guidance |
| `internal/tui/layout.go`, `internal/tui/help.go` | Action-specific hints, return hints and instructions |
| `internal/tui/rawlog_test.go`, `internal/tui/resources_workflow_test.go` | Migrate assertions from the old single return marker without dropping behavioural coverage |
| `README.md`, affected `internal/tui/testdata/golden/` files | Public navigation documentation and reviewed terminal output |

New internal interfaces, defined in task 1 and consumed by task 2/E2:

```go
type selectionIdentity struct {
    kind string // "provider", "type", "resource", "rpc", "ui", or empty
    value string // unescaped facet key for provider/type; exact address for resource
    index int // original Log.RPCSpans or Log.UISpans index for observations
}

func (m *Model) captureNavigation() navigationFrame
func (m *Model) restoreNavigation(frame navigationFrame)
func (m *Model) returnFromHistory() bool
func (m *Model) changeView(v View) // transition without clearing history
func (m *Model) selectedIdentity() selectionIdentity
func (m *Model) restoreIdentity(id selectionIdentity, fallback int)
```

`setView(View)` remains the manual navigation entry point: clear history and request scope, then call `changeView`. `changeView` retains existing per-view cursor bookkeeping, focus clamping and cache invalidation. A caller captures its parent BEFORE changing filters, then appends that snapshot immediately before a known-valid transition. There is no callback-based generic navigation framework.

## Task 1: Restore complete parent state for existing raw jumps

**Files:** Create `internal/tui/history.go`, `internal/tui/history_test.go`; modify `internal/tui/model.go`, `internal/tui/rawlog.go`, `internal/tui/views.go`, `internal/tui/layout.go`, `internal/tui/rawlog_test.go`, `internal/tui/resources_workflow_test.go` and `internal/tui/resources_test.go`.

**Interfaces:** Consumes `Model`, `rawLogState`, `timelineState`, `model.ResourceSelection`, `selectedTimelineSpan()`, `timelineSpans()`. Produces all navigation interfaces above and `Model.history []navigationFrame`. Replace `returnTo`/`hasReturn` and `returnFromJump` rather than retaining two sources of truth.

Add `identity selectionIdentity` to `row` in this task: provider/type builders
populate their existing `model.FacetKey` aggregate keys, Resources uses its exact address, and Calls
uses its original RPC index. This is required for task 1 restoration, before
aggregate drill-down is enabled by task 2.

- [x] **Step 1: Add a failing public-key regression for child edits.** Use existing `testLog`, `pressKey` and `pressRune` helpers, with `reflect` and Bubble Tea imports. This must fail because the current return path does not restore child-edited filters.

```go
func TestHistoryRestoresParentAfterRawChildFilterEdit(t *testing.T) {
    m := New(testLog(t, "resources-accounting.log"), "resources-accounting.log")
    m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
    before := m.filter()
    selected := m.rows()[m.selected].spanIdx
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
    if m.view != ViewRawLog { t.Fatal("fixture did not open raw log") }
    setFacetCursor(t, &m, dimType, "aws_instance")
    m.pane = PaneFacets
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeySpace})
    pressKey(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
    if m.view != ViewCalls || !reflect.DeepEqual(m.filter(), before) {
        t.Fatalf("parent view/filter not restored: %v %+v", m.view, m.filter())
    }
    if m.rows()[m.selected].spanIdx != selected {
        t.Fatal("returned to another original RPC observation")
    }
}
```

Add separate cases exercising resource/module allow-list map edits (including empty versus nil), parent sorting, timeline selection after child filtering, request expansion, modal Esc precedence, manual same-view key clearing history, and refused jumps leaving history unchanged. Drive real key handlers; directly set only fixture preconditions that existing tests already set.

- [x] **Step 2: Confirm RED.** Run `go test ./internal/tui -run '^TestHistory' -count=1`; confirm the filter-restoration assertion fails for the current implementation before changing production behaviour.

- [x] **Step 3: Implement snapshot storage and restoration.** Use this data shape, with existing package types/imports:

```go
type navigationFrame struct {
    view View
    pane Pane
    selected int
    identity selectionIdentity
    sortCol [viewCount]int
    viewSelected [viewCount]int
    excludedFacets map[string]map[string]bool
    resourceSelection model.ResourceSelection
    facetCursor facetCursor
    facetDimension, facetQuery string
    showFacetOverlay bool
    raw rawLogState
    timeline timelineState
}

func cloneExclusions(src map[string]map[string]bool) map[string]map[string]bool {
    if src == nil { return nil }
    dst := make(map[string]map[string]bool, len(src))
    for key, values := range src { dst[key] = maps.Clone(values) }
    return dst
}

func (m *Model) returnFromHistory() bool {
    if len(m.history) == 0 { return false }
    last := len(m.history)-1
    frame := m.history[last]
    m.history[last] = navigationFrame{}
    m.history = m.history[:last]
    m.restoreNavigation(frame)
    return true
}
```

Capture exclusions deeply; clone both `ResourceSelection` maps with `maps.Clone`, preserving nil. Copy `raw.scope` with `slices.Clone`, and copy the pointed-to `raw.match`. Reconstruct inactive text inputs using `newSearchInput()` and saved query/cursor values, instead of sharing internal textinput buffers. Save committed facet dimension/query/cursor; input editing is modal and cannot initiate drill-down. Do not snapshot log/index/caches, window dimensions, history itself or modal viewport objects.

Restore selections and sort first, switch via `changeView`, rebuild derived rows/lanes, resolve identity, then restore raw search/position so `invalidateRows` cannot erase the saved match/not-found state. For table rows resolve raw aggregate key or original observation index; fall back to `min(max(0, savedRow), max(0, rowCount-1))` and never index an empty result. For timeline, translate the selected positioned span to its original index using `selectedResources().RPCIndices` or `.UIIndices`, skipping spans without positions; find that original observation in rebuilt lanes. Do not use span-value equality, since identical observations can repeat. Clamp lane/span only if identity disappeared. Retain current window dimensions and call existing focus/position clamps.

Existing `jumpToSpan` must finish all source/visibility checks before snapshot append. Replace its `setView`/single-return assignment with:

```go
parent := m.captureNavigation()
m.history = append(m.history, parent)
m.changeView(ViewRawLog)
```

Remove the `v != m.view` guard from numbered-key dispatch so an explicit
same-view key also executes `setView` and ends the history chain. Keep modal
dispatch ahead of numbered keys.

Then initialise the new raw scope/position as today. Clear a previous raw match/search-failure anchor on entry to a different jump; restore the saved parent raw state on return. E1 deliberately restores the complete parent snapshot, so migrate old assertions that relied on child raw state leaking into the parent to assert exact parent restoration instead.

- [x] **Step 4: Verify GREEN and review navigation semantics.** Run `go test ./internal/tui -count=1`. Replace old field assertions in tests with history behaviour/depth only where needed. Update `noMatchTail`, footer and comments to consult history; check Resources empty-state messages as well as Calls. Confirm quality/evidence/help dismissal leaves depth unchanged and backslash retains the return frame. Run independent task review, then a separate test-cleanup agent; preserve behavioural coverage and repeat affected tests after edits.

- [x] **Step 5: Commit the task.** Inspect `codex-git diff --check` and status, stage only task files with the wrapper, then signed commit subject `Restore investigation state through navigation history`. Record RED/GREEN and review evidence in this plan.

## Task 2: Open provider and type investigations with singleton scope

**Files:** Create `internal/tui/drilldown.go`, `internal/tui/drilldown_test.go`; modify `internal/tui/views.go`, `internal/tui/model.go`, `internal/tui/layout.go`, `internal/tui/help.go`, `internal/tui/resources.go`, `README.md` and affected TUI tests/goldens.

**Interfaces:** Consumes task 1 snapshots/transitions/row identities and `selectedResources() model.ResourceProjection`. Produces `func (m *Model) openAggregate() bool`, `func (m *Model) aggregateTarget() (View, string, string, bool)`, `func (m *Model) restrictFacet(dim, value string)` and `func (m *Model) enterHint() string`. `aggregateTarget` returns destination, dimension, normalised unescaped facet key and availability; hints and execution consume the same predicate.

- [x] **Step 1: Add a failing real-key route test.**

```go
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
    if m.view != ViewCalls { t.Fatalf("view = %v, want Calls", m.view) }
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
```

Use an additional synthetic parsed fixture with providers A/B/C, two types and multiple calls per provider: initialise A+B selected, one type selected; open A and assert every returned original RPC index belongs to A and that type. Assert Esc restores both A+B. Repeat from nil provider selection and multiple type selections. For the single-known-value case the exclusion representation may derive nil; assert the admitted set, not a fake representation change.

Add UI-only type → Resources using `resources-modules.log`, RPC type with explicit zero duration → Calls, and a mixed type whose RPCs are filtered out → Resources. Other dimension maps and source data/quality must be unchanged. Test empty tables/invalid selection and Enter outside the list as inert.

Also test a fresh model with no preceding facet edits (`excludedFacets == nil`)
and unavailable provider/type metadata. An unavailable type/provider row uses
the existing `(none)` facet key and must admit the missing-value observations.

- [x] **Step 2: Confirm RED.** Run `go test ./internal/tui -run '^TestDrillDown' -count=1`; current aggregate Enter must fail the destination assertion.

- [x] **Step 3: Implement the shared route predicate and singleton selection.**

```go
func (m *Model) restrictFacet(dim, value string) {
    excluded := make(map[string]bool)
    for _, candidate := range m.facetValues(dim) {
        if candidate.Value != value { excluded[candidate.Value] = true }
    }
    m.setFacetExclusions(dim, excluded)
}

func (m *Model) openAggregate() bool {
    view, dim, value, ok := m.aggregateTarget()
    if !ok { return false }
    parent := m.captureNavigation()
    m.restrictFacet(dim, value)
    m.history = append(m.history, parent)
    m.changeView(view)
    m.selected = 0
    return true
}
```

`aggregateTarget` checks selected row/view and its unescaped identity. Providers route to Calls. Types scan the current projection's RPC indices for `model.FacetKey(span.ResourceType) == identity.value`; any matching index chooses Calls, else any matching UI index chooses Resources. No reparsing or projection per row. Return false for all other views in E1. Wire Enter to `openAggregate()` before the existing `jumpTarget` path. `enterHint` returns `↵ calls`, `↵ resources`, existing raw-open hint or empty from these same predicates; preserve narrow-width hint fitting and quit visibility.

The aggregate row identity must never come from rendered cell text, which may contain visible control escaping. Use bucket/join keys for provider/type identity, normalise raw span values with `model.FacetKey` when matching those keys, and never attempt to reverse `(none)` into an empty string. Preserve the existing facet equivalence, including unavailable metadata; resource addresses remain exact and never pass through `FacetKey`. Resource rows remain inert until E2. All history-aware empty messages must say Esc returns when it will return.

- [x] **Step 4: Verify complete E1 workflows and documentation.** Run focused tests then `go test ./internal/tui -count=1`. Document Enter, Esc, modal precedence and numbered navigation. Test provider → Calls → Raw → reconstructed response → close → Calls → provider using a sanitised parsed response fixture and real reconstruction; retain exact raw line/column/search position during modal round-trip. Test type → Resources → Esc and child filtering/sorting/resize before return. Exercise 100×30 and 60×9 in a real PTY, including an empty child and same-view number key. Inspect intentional golden changes with `scripts/read-golden.sh` and raw diffs. Run independent review and separate test cleanup, then the final validation below.

- [x] **Step 5: Commit.** Stage reviewed task files explicitly and signed commit `Open scoped investigations from provider and type rows`. Record review and validation evidence. Do not mark E2 or all of Boundary E complete.

## Final validation and handoff

- [x] `go test -race -count=1 ./...` and `go build ./...`.
- [x] `gofmt -d .`, `go mod tidy -diff`, `go mod verify`, `golangci-lint run --timeout=5m`.
- [x] Match `.github/workflows/ci.yml` build matrix: linux/darwin × amd64/arm64, `CGO_ENABLED=0`, build all packages and `-trimpath` CLI. Use the repository's existing CI environment settings; do not add a runner or dependency.
- [x] Review the full E1 diff, signed-commit status and real terminal evidence. Required workflows cover repeated frame pops, parent map isolation, stable identities, empty results and resize. Do not claim remote CI ran unless it did.
- [x] Update this plan with evidence; E2 consumes these interfaces only after E1 review passes.

## Planning self-review (before implementation)

Item 4 provider/type singleton selection maps to task 2; snapshots, identity fallback, modal precedence, backslash and manual navigation map to task 1. Resource operation and associated-call paths map explicitly to E2. Shared measurement/quality/source identity contracts remain unchanged. New interface names and existing package names were checked against `85d58a7`; no implementation or tests have been run for this proposed feature.

Independent plan review on 10 September 2026 found unsafe nil-map assignment
and inconsistent missing-metadata key normalisation in the proposed snippets.
Both were corrected using the existing facet setter and normalised facet keys,
with explicit regression cases. Re-review found no remaining findings in either
plan and approved them for Dan's review. Documentation link, code-fence,
placeholder and staged whitespace checks passed; no application tests were run
for this documentation-only change.

## Execution record

Dan authorised subagent implementation of both plans on 10 September 2026. E1 task 1 is complete in signed commits `31367fe`, `a0d0772` and `8e6ce58`. Required RED reproduced child type-filter leakage; focused tests and the full Go suite pass. Independent spec/quality review approved after additional real-key modal/selection coverage; separate cleanup retained all distinct boundary tests.

An empty named selection cannot initiate an E1 raw jump because no timing row remains. E1 verifies empty snapshot preservation directly and Enter inert through real handlers. E2 verified the newly reachable empty-operation parent → c → Calls → Esc path. This staging was confirmed in review; its risk was overlooking another reachable empty-parent navigation path.

E1 task 2 is complete in signed commits `f867107`, `1c15747` and `abf6e36`. Independent review approved singleton routes, missing metadata, full real-response navigation and the short-terminal fixes. Cleanup retained all distinct tests. At `abf6e36`, all 11 packages pass uncached race tests, lint reports zero issues, and package/trimpath CLI builds pass for linux/darwin × amd64/arm64. Module checks and formatting are clean. Real PTY at 100×30 verified provider → Calls → Raw, request expansion and nested return; 60×9 verified visible Types identity, non-default sort and type → Resources → back. All E1 commits have good signatures. Combined E1/E2 final review is complete; see final verification below.

## Final verification

The combined E1/E2 review found two omissions: saved keyboard focus was not restored, and filtered-empty operations claimed the log had no rows. Signed commit `c64c0f0` fixes both with real-key and rendered regressions. Independent scoped re-review marked both addressed with no new breakage. Separate cleanup retained all three new test cases.

At `c64c0f0`, all 11 packages pass `go test -race -count=1 ./...`. Build, formatting, module tidy/verification and lint pass. Local package and `-trimpath` CLI builds pass for linux/darwin × amd64/arm64 with CGO disabled; remote CI was not run. Real terminal checks cover 100×30 and 60×9 navigation, source and response views, current-selection wording, empty child returns and resize. Additional 160×30 verification confirms focus restoration and immediate Enter after returning. Commit signatures and whitespace checks pass. Implementation is complete on the topic branch; no merge or push was performed.
