# Resources TUI and Filters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose observed resource rankings, searchable resource/module choices and honest selected RPC evidence in the terminal interface.

**Architecture:** Consume D1's resource projection through one cached TUI selection. Extend current table/facet patterns while retaining original RPC indices for attribution and navigation. Expose complete scoped evidence in a scrollable panel so narrow layouts can retain its qualifications.

**Tech Stack:** Go 1.25+, existing Bubble Tea, Bubbles text input/viewport and Lip Gloss; existing test and golden tools only.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), item 3 and boundary D. Requires reviewed [Resource evidence and selection](2026-09-10-resource-evidence-selection.md), D1. Dan approved the split and RPC-only provider/method filter behaviour on 10 September 2026, then authorised sequential subagent implementation of both plans.

## Global Constraints

- “Use the reserved `3` key for Resources.”
- “Use ‘operations’, not ‘resources’, for repeated completions on one address.”
- “Supplement each resource with associated RPC count and total, separated into Contained/Likely and weaker Overlapping evidence.”
- “Label these as inferred and partial.”
- “The view does not substitute a misleading inferred ranking.”
- “Provider and RPC-method filters apply only to RPC evidence; they cannot infer a provider or method for a UI operation.”
- “Raw-log provider and severity filtering retains its existing meaning.”
- “Terminal rendering escapes untrusted controls.”
- “Whole-log quality facts remain labelled as whole-log facts.”
- Keep C1/C2 timing/source/quality semantics. No inferred address ranking, provider guessing, compatibility adapter, CLI flag, JSON, response recovery or general history implementation.
- TDD, sanitised real fixtures, independent reviews and separate test cleanup. Signed commits through `/Users/dan/.codex/bin/codex-git`; signing failure is a hard stop. Preserve hooks; no push/merge without instruction.

---

## Prerequisites and responsibilities

Start after D1's reviewed integration commit; consume its interface ledger verbatim. Check status, pull with rebase and use a topic branch/worktree. Do not execute D1 and D2 concurrently. Run tasks sequentially, recording actual RED/GREEN, review and signed commits. New API compile failures do not replace demonstrating failing behavioural assertions.

| Files | Responsibility |
| --- | --- |
| New `internal/tui/resource_selection.go`, `resource_selection_test.go` | D1 index/projection cache and original-index consumers |
| `internal/tui/model.go`, `facets.go`, `views.go`, `timeline.go`, `layout.go` and corresponding tests | Tier-specific filtering and view integration |
| New `internal/tui/resources.go`, `resources_test.go` | Resource table, observed details and scoped preamble |
| New `internal/tui/resource_evidence.go`, `resource_evidence_test.go` | Scrollable complete evidence and modal precedence |
| New `internal/tui/facet_search.go`, `facet_search_test.go`; `facets.go` | Literal narrowing with complete underlying choice universe |
| `internal/tui/navigation.go`, `help.go`, `workbench.go`, affected tests; `README.md` | Keys, scope explanations and user guidance |
| `internal/tui/testdata/golden/` | Reviewed layout updates and Resources fixtures |

## UI decisions

Resources groups exact addresses. Columns: address, operations, observed UI total, longest UI operation, inferred Contained/Likely RPC count/total and inferred Overlapping RPC count/total. Default order is UI total descending then exact address. Only address and observed UI columns participate in `s` sorting; inferred RPC ranking remains prohibited. Saturated observations qualify UI sum/max as lower bounds.

Selected-resource details show full escaped address, UI operation count/total/max, timing qualifications and separate inferred RPC groups. D1 preserves individual operation identities; boundary E owns a selectable operation list and multi-step drill-down. Enter on a resource aggregate must not jump to an arbitrary completion or RPC. Existing Calls/Timeline Enter remains intact.

The preamble names separate scopes: UI uses type/resource/module; RPC uses provider/type/method/resource/module. With named selection active, show selected/other/unresolved RPC totals against the provider/type/method baseline. `e evidence` opens a complete scrollable panel from Resources, Calls, Types, Providers and Timeline. It shows preselection resource evidence, selected partitions and exact baseline filters. `i` remains whole-log quality.

Resource/module facets use D1's complete choices, including context-only addresses and ancestor modules. Root displays `(root subtree)` but uses the typed empty path, never generic `(none)`. Root subtree includes known descendants, not just root resources. Unknown module never becomes root; exact address selection still works with unknown module membership.

With facet focus, `/` starts literal case-sensitive narrowing for its resource/module dimension. Enter finishes editing and retains the narrowed list; Esc during editing restores the previous chooser query. Outside editing, Esc clears a nonempty chooser query before existing filter-clearing behaviour. Queries never change result filters. Space toggles a choice; `o` solos it against the entire underlying dimension. `f`/Tab and narrow overlay keep their existing focus meaning. During editing, q/3/i/e insert text; Ctrl-C quits.

### Task 1: Connect selection and migrate tier-specific consumers

**Files:** Create `internal/tui/resource_selection.go`, `resource_selection_test.go`; modify `model.go`, `facets.go`, `views.go`, `timeline.go`, `layout.go`, `facets_test.go`, `timeline_test.go`, `views_test.go` under `internal/tui/`.

**Consumes:** D1 BuildResourceIndex/SelectResources/ResourceSelection/ResourceProjection and original Log spans.

**Produces:**

```go
// Additional Model fields:
resourceIndex model.ResourceIndex
resourceProjection model.ResourceProjection
resourceProjectionCached bool
resourceSelection model.ResourceSelection

func (m *Model) selectedResources() model.ResourceProjection
func (m *Model) selectedRPCSpans() []span.Span
func (m *Model) selectedUISpans() []span.Span
```

- [x] **Step 1: Write regressions before migration.** Added behavioural coverage for provider/method filters preserving UI, original RPC index 2 surviving projection into Calls/detail/jump data, unpositioned UI remaining ranked but absent from the temporal slice, named filtering retaining the whole log's RPC timeline tier, raw visibility ignoring type/method/resource/module selection and Esc clearing named selection. Integration cases load `two-tier.log` through the real parser and use existing facet helpers.

```go
func TestResourceSelectionKeepsUIUnderRPCFilters(t *testing.T) {
    l := &model.Log{
        RPCSpans: []span.Span{{Entry: 1, Provider: "p", RPC: "ReadResource", DurationMs: 10}},
        UISpans: []span.Span{{Entry: 2, Address: "aws_instance.a", DurationMs: 1000}},
    }
    m := New(l, "synthetic.log")
    m.setFacetExclusions(dimProvider, map[string]bool{"p": true})
    m.invalidateRows()
    if len(m.selectedRPCSpans()) != 0 || len(m.selectedUISpans()) != 1 {
        t.Fatal("RPC-only filter changed observed UI")
    }
}
```

Also filter to an RPC originally at index 2 and assert its attribution/request/raw entry remain index 2's. Unpositioned UI remains ranked but unavailable in temporal projection. Named filtering must not switch a capture's chosen timeline tier from RPC to UI.

- [x] **Step 2: Run RED.** `go test ./internal/tui -run TestResourceSelection -count=1` first failed to compile on the absent D2 declarations. After adding empty declared methods, the same command failed behaviourally on preserved UI, original indices, named timeline tier and unpositioned UI accounting. A later audit regression, `go test ./internal/tui -run TestResourceSelectionClearsWithOtherFilters -count=1`, failed because named-only selection survived Esc; it passed after the clear path was corrected.
- [x] **Step 3: Implement one selection cache.** New builds one index for its Log; selectedResources calls D1 with that original Log and caches the projection until invalidateRows clears it before row/timeline rebuild. Providers, Types, Calls, Timeline, headers and the Types preamble consume the projection. Calls sort D1's original RPC indices and retain them in callRow. Timeline chooses its tier from the whole Log, then passes only that projected tier through SelectTiming. D1 owns the approved type-only UI rule, so the obsolete provider translation and its now-unused TUI wrapper were removed without a compatibility path; raw entry/request filtering still uses the base filter only.

```go
func (m *Model) selectedResources() model.ResourceProjection {
    if !m.resourceProjectionCached {
        m.resourceProjection = model.SelectResources(m.log, m.resourceIndex, m.filter(), m.resourceSelection)
        m.resourceProjectionCached = true
    }
    return m.resourceProjection
}
func (m *Model) selectedUISpans() []span.Span {
    result := make([]span.Span, 0, len(m.selectedResources().UIIndices))
    for _, i := range m.selectedResources().UIIndices { result = append(result, m.log.UISpans[i]) }
    return result
}
```

Implement selectedRPCSpans analogously using original RPCIndices. Providers/Types use the selected slices. Timeline passes only the already-chosen tier through existing SelectTiming. Calls construct rows from original RPCIndices and carry that original index into callRow; never pass a reindexed filtered slice to callRows. Headers, no-match guidance and counts use the same projection. Audit every direct SpansMatching/filterActive/timelineNarrowed consumer for named selection. Raw entryVisible/request scope keep base filters only.

Apply the approved type-only UI rule through the shared D1 projection; remove uiProviderTypes and any TUI wrapper once no consumer uses it. Update obsolete tests to the new contract while preserving type/nil/empty/raw-provider coverage. Do not add compatibility behaviour or remove recorded UI provider metadata.

- [x] **Step 4: GREEN and independent review.** GREEN passed with `go test ./internal/tui ./internal/model -count=1`. A focused run covered all ResourceSelection regressions plus existing unavailable/partial-position timeline cases. Final `go test ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, `gofmt -d .`, `go mod tidy -diff` and `go mod verify` passed; no golden changed. Independent review approved commit `3951d14`.
- [x] **Step 5: Signed commit.** Task 1 was committed as `3951d14`. Separate cleanup removed one redundant provider-translation case in `0a96d0f`; TUI statement coverage remained 96.4% and the before/after profiles were identical.

### Task 2: Add observed Resources and complete evidence access

**Files:** Create `internal/tui/resources.go`, `resources_test.go`, `resource_evidence.go`, `resource_evidence_test.go`; modify `model.go`, `views.go`, `layout.go`, `navigation.go`, `help.go`, `workbench.go` and affected tests under `internal/tui/`.

**Consumes:** selectedResources, D1 ResourceRow/ResourceEvidence/SelectionEvidence, existing table/detail escaping and quality viewport patterns.

**Produces:**

```go
// Add ViewResources before viewCount and key 3 in views.
func (m *Model) resourceRows() []row
func (m *Model) renderResources(w, h int) string
func (m *Model) openResourceEvidence()
func (m *Model) renderResourceEvidence(w, h int) string
// Model fields:
showResourceEvidence bool
resourceEvidenceViewport viewport.Model
// Additional optional row field:
resource *model.ResourceRow
```

- [x] **Step 1: Write rendering/interaction tests.** Added behavioural coverage for key 3, grouped UI operations, inert Enter, observed-only sorting, distinct empty states, scope/qualification wording, safe identities, ordinary-width address visibility and full wrapped detail identity. Evidence tests cover the 10/20/5 partition, RPC-method-only filtering, UI operation nouns, zero observations versus measured zero, modal state preservation and 60-column scrolling.

```go
func TestResourcesKeyAndEvidence(t *testing.T) {
    m := New(&model.Log{}, "empty.log")
    m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
    m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
    if m.view != ViewResources { t.Fatalf("view=%v", m.view) }
    m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
    if !m.showResourceEvidence { t.Fatal("evidence did not open") }
    m.Update(tea.KeyMsg{Type: tea.KeyEsc})
    if m.showResourceEvidence || m.view != ViewResources { t.Fatal("return state lost") }
}
```

Render D1's 10/20/5 fixture: baseline 35, selected 10, other 20, unresolved 5; method filter changes baseline to 15 without changing UI. Remove unresolved call and assert excluded B is not called association failure. Cover missing-type priority across the three methods, no-context and unavailable denominator. Full C2 quality remains unchanged.

- [x] **Step 2: Run RED.** `go test ./internal/tui -run 'Test(Resources|ResourceEvidence)' -count=1` failed behaviourally after declarations compiled: key 3 remained on the old view, Resources rows reached the unhandled-view panic, and evidence did not open or render. A dedicated sorting RED then reported inferred column 4 instead of wrapping to address column 0; ordinary-width and UI-noun regressions separately exposed a missing address column and `call` wording.
- [x] **Step 3: Register and render.** Registered Resources and its table/detail dispatch. Rows retain the cached projected `ResourceRow`, use `noSpanIdx`, rank only by observed columns and keep address/observed measurements ahead of supplementary inferred pairs when width is constrained. Enter remains inert.

```go
rows = append(rows, row{
    cells: cells, numeric: numeric,
    spanIdx: noSpanIdx,
    resource: &projection.Rows[i],
})
```

Give Resources cycleSort an explicit observed-only column list; retain existing sorts elsewhere. Address ties remain deterministic. Expand navigation short labels for six views; remove stale unbound-3 comments/tests/help. Keep active view and quit hint available at narrow widths. Enter on resource aggregate is inert and unadvertised until boundary E.

- [x] **Step 4: Implement evidence modal.** Bound `e` on timing views with response/help/quality precedence, a dedicated rewrapping viewport, scrolling and modal key swallowing. The panel renders escaped sorted baseline filters, selected/other/unresolved partitions, UI operations and every preselection bucket with duration lower bounds and denominator guidance.

Render sorted escaped provider/type/method baseline filters (all/none/exact values), named selections, selected/other confidence subdivisions, unresolved potentially relevant work, and complete preselection resource buckets with counts/durations. Explain that these buckets and whole-log C2 confidence use different denominators; keep the latter in `i`. UI selected/unnamed totals remain separate. Zero denominator is unavailable, while zero duration with observations remains measured zero. Lower bounds remain flagged. Rewrap on resize; retain visible close/scroll/quit footer at short heights.

- [x] **Step 5: GREEN, inspection and independent review.** Initial implementation checks passed with focused Resources/evidence tests, `go test ./internal/tui -count=1` and `go test ./...` (all 11 packages). Goldens were regenerated with `go test ./internal/tui -update -count=1`; `scripts/read-golden.sh help-60.txt` and `scripts/read-golden.sh layout-100.txt` plus the raw diff confirmed only key 3/evidence navigation and responsive hint changes. Independent review found three reachability/presentation issues. Round-one behavioural regressions reproduced the missing selected-row evidence at 60x30 and for a long address, lost empty guidance at 60x9, and a blank explicit root-module label. The selected row is now separately available in scrollable evidence with complete escaped details and qualifications; empty explanations precede optional preamble/table content; root renders `(root subtree)` while retaining its empty typed key. Scoped re-review confirmed those fixes, then found the newly exposed selected-row RPC evidence did not state its partial coverage. A viewport regression failed on absent `inferred and partial` and `does not recover every RPC call` wording; explicit adjacent qualification lines made it pass. Final re-review approved the complete Task 2 range at `fce6e76`; separate cleanup made no edits.
- [x] **Step 6: Signed commit.** Task 2 is represented by signed commits `830b379`, `aaf5b24` and `fce6e76`; independent cleanup made no edits.

### Task 3: Add resource/module choices and literal narrowing

**Files:** Create `internal/tui/facet_search.go`, `facet_search_test.go`; modify `facets.go`, `model.go`, `layout.go`, `help.go`, `facets_test.go`, `resource_selection_test.go` under `internal/tui/`.

**Consumes:** D1 complete choices/modules, typed ResourceSelection, existing facet overlay/focus and `newSearchInput() textinput.Model`.

**Produces:**

```go
const dimResource = "resource"
const dimModule = "module subtree"
type facetSearchState struct {
    editing bool
    dimension string
    query, previous string
    input textinput.Model
}
// Model field: facetSearch facetSearchState
func (m *Model) visibleFacetIndices(dim string) []int
func (m *Model) beginFacetSearch()
func displayFacetValue(dim, value string) string
```

- [x] **Step 1: Write state and key regressions.** Added A/B plus context-only C coverage for display-only narrowing and original-index cursor mapping. Hidden-choice solo, repeated solo, explicit empty and single-choice allow-lists exercise nil versus none; the single-choice case proves unresolved RPC evidence remains in the partition while its RPC index is excluded. Module coverage deduplicates a repeated address, includes a context-only descendant, exercises indexed siblings, module-name boundaries, root and unknown membership. Update-driven editor coverage includes literal command keys, Unicode/backspace, Enter/Esc/Ctrl-C, no-match inert actions, clear ordering, raw search, focus, overlay and resize persistence.

```go
before := m.selectedResources()
m.facetSearch.dimension = dimResource
m.facetSearch.query = "aws_instance.a"
if len(m.visibleFacetIndices(dimResource)) != 1 { t.Fatal("chooser did not narrow") }
after := m.selectedResources()
if len(before.RPCIndices) != len(after.RPCIndices) || before.UI != after.UI {
    t.Fatal("chooser query applied a result filter")
}
```

Drive Update too: cancel restores previous query; no matches means no selectable cursor; q/number/i/e insert while editing, Ctrl-C quits; Unicode/backspace, f/Tab, narrow overlay and resize preserve selection. Raw `/` on focused raw list remains unchanged. Match safe untruncated display text and map to exact original identities.

- [x] **Step 2: Run RED.** After the declaration-only compile failure, `go test ./internal/tui -run 'Test(FacetSearch|ResourceFacet|ModuleFacet|FacetTypes|RawSlash)' -count=1` failed behaviourally because resource/module choices were absent, UI-only type was absent and the editor did not start. After choice construction, focused hidden-choice and single-choice tests reached the legacy exclusion path and failed with empty address allow-lists. A later display regression failed because the retained query was absent from its facet heading; the root-label width regression failed at 19 columns against the 21-column displayed value.
- [x] **Step 3: Add typed named dimensions.** New builds exact resource and structural module facets from the cached D1 index. Resource counts are one per distinct address; exact known-module counts aggregate through nearest known structural parents validated by `ModuleContains`, independently of lexical adjacency and without a module-by-address nested scan. Resource/module checkboxes write explicit typed allow-lists; legacy provider/method/type/level exclusions retain their existing semantics. Resource types merge RPC and UI counts, while provider and method remain RPC-only.

Build complete choices from D1 index, not filtered projection. Root label is separate from typed empty key. Resource/module counts mean distinct addresses: one per address and known addresses within each module subtree. Document this unit. Merge type choices from both admitted tiers so UI-only types remain selectable; provider/method choices come only from RPCs.

- [x] **Step 4: Implement display-only cursor mapping.** Literal case-sensitive narrowing maps visible positions to original facet indices and clamps after text edits. Space and solo read the complete underlying dimension; a no-match cursor is inert. The dedicated `textinput.Model` editor precedes global shortcuts and leaves projection caches untouched until a typed selection changes. `(root subtree)` is a display-only label shared by the facet, natural-width measurement and evidence output. A retained query is visible in its facet heading with an `Esc query` hint; starting another named dimension replaces the remembered narrowing.

```go
func (m *Model) visibleFacetIndices(dim string) []int {
    var result []int
    for i, value := range m.facetValues(dim) {
        shown := displayFacetValue(dim, value.Value)
        if m.facetSearch.dimension != dim || strings.Contains(shown, m.facetSearch.query) {
            result = append(result, i)
        }
    }
    return result
}
```

displayFacetValue uses `(root subtree)` for module empty path, otherwise existing safe identifier escaping without width truncation. Initialise the editor with newSearchInput and use textinput.Model's SetValue, Value, Update and Blur as existing search_input.go does; do not introduce another editor or borrow raw search state. Editing precedes global shortcuts. Named changes invalidate projection/row/timeline caches; query-only changes do not alter raw search anchors or result filters. Leaving facets retains its one remembered dimension/query; starting search in another dimension replaces that narrowing explicitly.

- [x] **Step 5: GREEN and independent review.** Focused Task 3 tests and `go test ./internal/tui -count=1`, including raw search and response coverage, passed. `go test ./internal/tui -update -count=1` passed; all seven changed help/layout/timeline goldens were inspected with `scripts/read-golden.sh` and their raw ANSI diff. Final `go test ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, `gofmt -d .`, `go mod tidy -diff`, `go mod verify` and `git diff --check` passed. The first lint run had found one obsolete unreferenced cursor inverse, which was removed before the clean final gate. Independent review then reproduced a lexical sibling separating a structural parent from its child and a no-match chooser losing its heading/navigation anchor. Behavioural regressions failed with `module.a` count zero and a hidden `RESOURCES /nomatch` heading; the fixes derive parents independently of lexical adjacency and treat an empty narrowed dimension as a nonselectable heading anchor whose Up/Down movements select adjacent visible choices. Scoped re-review approved the fixes at `7625e50`; both separate cleanup passes retained the Task 3 tests without edits.
- [x] **Step 6: Signed commit.** Signed commits `91f5c78` and `7625e50` contain the implementation, review fixes, tests, reviewed goldens and execution record.

### Task 4: Validate the workflow and document scope

**Files:** Update README, TUI help/tests and affected goldens; add `internal/tui/testdata/golden/resources-60.txt`, `resources-70.txt`, `resources-100.txt`, `resources-160.txt` through the existing golden harness. Update both D plans' execution evidence.

**Consumes:** D1/D2 behaviour and existing CI/golden/terminal workflow.

**Produces:** Reviewed terminal evidence, final regression validation, user documentation and signed integration result.

- [x] **Step 1: Add integration assertions before updating outputs.** A sanitised accounting workflow now drives real keys through Resources, literal narrowing, exact selection, evidence, quality, Calls and the scoped raw request. It asserts the 35/10/20/5 ms partition and unchanged whole-log quality. While Raw Log retains the active request, `i` preserves its one-entry scope, exact entry 4 (zero-based index 3), line 1, horizontal query anchor and match. Returning to Calls intentionally clears request scope; `e` then preserves the retained raw position/query/match state, and Enter still reaches the exact request target afterwards. A second sanitised fixture proves two repeated UI operations retain original indices 0 and 1, then moves focus from facets to the Resources list before asserting aggregate Enter remains inert. The same parsed fixture exposed a named attribution with authoritative `Address` and absent optional `Name`; the behavioural RED rendered `(none)`, and the smallest GREEN display fallback now shows that exact address without parsing or changing attribution.

```go
snapshot := l.CaptureQuality()
m.resourceSelection = model.ResourceSelection{Addresses: map[string]bool{"aws_instance.a": true}}
m.invalidateRows()
if m.selectedResources().Selection.Baseline.TotalMs != 35 { t.Fatal("baseline drift") }
if !reflect.DeepEqual(snapshot, l.CaptureQuality()) { t.Fatal("whole-log facts changed") }
```

- [x] **Step 2: RED then intentional output changes.** Before golden generation, `go test ./internal/tui -count=1` failed for an overlong new help line and the absent Resources golden. The help text was wrapped within the existing width contract before intentional generation. Four committed synthetic fixtures cover mixed RPC/UI evidence, repeated nested quoted modules and a context-only choice, RPC-only input, and a long address with saturated lower-bound timing. README and scrollable help document summed observed UI ranking, partial inferred RPC evidence, exact scopes, chooser-only narrowing, root/module semantics, inert aggregate Enter and the different `e`/`i` denominators.
- [x] **Step 3: Inspect goldens.** `go test ./internal/tui -update -count=1` passed. All fourteen goldens changed since D2 base `9836d2e` were inspected through `scripts/read-golden.sh`, and every inherited and new file was checked in the raw ANSI diff. The four new Resources frames cover widths 60/70/100/160; they preserve exact identity priority, repeated UI totals, short no-UI routes, long-address wrapping, lower-bound markers, qualifications and close/quit actions. The inherited diffs are explained by Resources navigation, resource/module facets and evidence controls.
- [x] **Step 4: Real terminal validation.** The controller built `/tmp/tfli-resources` from `7625e50` plus the stable Task 4 Calls-address correction and exercised the sanitised fixtures in real `zsh -f` PTY 8810. At 100×30 the workflow preserved UI under provider exclusion; showed 35/10/20/5 ms partitions and method-scoped 15/10 ms; kept whole-log 35/1000 ms quality unchanged; displayed the corrected exact Calls address; and preserved raw entry 4, line 1 and column 97 across quality. At 60×15 quoted modules, explicit root and context-only selection behaved correctly. At 60×9 no-UI routes, all nineteen evidence pages, close and quit were reachable. The PTY exited zero with no remaining terminal finding. The tested Resources, filtering and navigation code matches the final implementation; help prose was tightened afterwards and validated separately by the help-width and golden tests. After the final evidence correction, PTY 4229 rebuilt signed `69d1fb9` and verified Calls → `e` at 100×30 plus all aggregate qualifications through the final page at 60×9; close and quit returned zero.
- [x] **Step 5: Separate cleanup and final checks.** Separate cleanup retained the initial Task 4 tests, both test/report corrections and the final aggregate-qualification regression without edits. At signed `69d1fb9`, the controller's fresh `go test -race -count=1 ./...` passed all 11 packages (TUI 8.061s, scripts 12.032s); build and binary build passed, lint reported zero issues, formatting and module tidy had no diff, all modules verified and the diff check was clean. These are local Darwin/arm64 results; the existing CI matrix remains responsible for linux/darwin amd64/arm64 builds. The three-run result-checked model benchmarks at `7625e50` remained consistent with D1. Module-only selection remained the highest-allocation projection, while same-Log index/projection reuse already owns the appropriate cache boundary, so no new cache was added.
- [x] **Step 6: Whole-boundary independent review and signed evidence commit.** Independent review covered `0528049..c825b90` across totals, original identity, missing-type priority, unknown membership, raw semantics, tier-specific filtering and unchanged whole-log quality. Its sole finding D-1 was fixed in signed `69d1fb9`; scoped rereview approved specification and quality with no new or outstanding finding. It confirmed the selected-row and aggregate qualification scopes remain distinct and that the final fix changed no model accounting, attribution, filtering, cache or navigation behaviour. This closing evidence commit is signed as required. No remote CI run is claimed, and Dan's instruction remains required for integration.

**Whole-boundary review correction:** Review of `0528049..c825b90` found one remaining evidence-presentation defect: outside Resources, the aggregate observed UI section retained a saturated `≥` total but omitted its rounding, lower-bound and unavailable-position explanations because those lines belonged only to a selected Resources row. A real Calls → `e` regression at 100×70 failed for all three absent phrases on `resources-long-lower-bound.log`. Aggregate qualifications now use the projected original UI indices independently of row selection, explicitly cover unnamed operations, and remain separate from selected-row details. A second assertion proves a positioned selected row cannot mask an unpositioned operation elsewhere in the projected UI scope. Focused evidence tests and `go test ./internal/tui -count=1` passed. Scoped rereview approved D-1 at `69d1fb9`; cleanup retained the regression, and the final affected gates and real PTY route passed with no outstanding finding.

## Coverage and remaining boundaries

Task 1 migrates timing consumers without losing source identity. Task 2 provides observed rankings, empty states and complete scoped evidence. Task 3 supplies exact/module selection and literal narrowing. Task 4 verifies full interaction and documentation. D1 owns calculations; TUI never recomputes attribution or whole-log quality. E owns operation navigation/history, F profile data/text, G JSON, H comparison and I partial recovery.

## Approved peer-review follow-up

Dan authorised both findings from `/tmp/tf-log-inspector-par-findings.md` on 10 September 2026. These are bounded corrections to the completed design: preserve subtree OR semantics and all evidence contracts. The reviewed baseline is `c1b62bc`.

### Task 5: Make broad module selections efficient (PAR-001)

**Files:** `internal/model/resource_selection.go`, `resource_address.go` if structural ancestor reuse requires it, `resource_selection_test.go`, `resources_benchmark_test.go`; `internal/tui/model.go`, `resource_selection.go` and their tests only for projection invalidation.

**Interfaces:** Keep `ResourceSelection.Match(address string, module ResourceModule) Membership` and `SelectResources` public contracts. Subtrees are ORed; address and module dimensions are ANDed. Exact indexed/quoted module paths, invalid paths, unavailable membership, false map values and nil versus empty maps must retain their behaviour.

- [x] Establish a deterministic failing regression for repeated module parsing/allocation, using real matching and an allocation-growth bound rather than a wall-clock timeout. Retain behavioural assertions for broad selections, quoted keys and unknown membership. Add result-checked benchmarks for the normal state created by unticking root: all sibling module instances selected, at small and large cardinalities, alongside unconstrained and singleton selections. Record the slow baseline before changing production code.
- [x] Match structurally valid ancestor paths through selected-map lookups instead of scanning every selected parent for every observation. Reuse the existing parser and exact path spelling. Do not introduce a new dependency, generic cache framework or alternative address parser. Short-circuit definite address mismatches where safe. Keep projection caching across view/sort-only changes if this can be separated cleanly from filter invalidation; test cache lifetime through real behaviour and allocation evidence, not mocks.
- [x] Run the focused model/TUI tests, result-checked benchmarks and the saved review probe. Confirm all membership/evidence invariants. Run `go test ./...` before the signed commit; include actual RED/GREEN and measurements in the task report.
- [x] Independent task review and separate test cleanup; resolve findings before proceeding.

### Task 6: Explain inherited module inclusion (PAR-002)

**Files:** `internal/tui/facets.go`, `help.go`, their tests or `facet_search_test.go`; affected help/facet goldens and README filter guidance.

**Interfaces:** Keep module selection as an OR of explicit selected subtrees, including root. Unticking a child does not exclude observations still covered by a selected ancestor. Exact-address and legacy facet controls retain their behaviour.

- [x] Write a real-key regression starting from root plus sibling modules, untick a child while root remains selected, and assert retained observations with accurate guidance. Fail first on the misleading presentation. Cover genuinely excluded choices and narrow rendering without weakening existing assertions.
- [x] Qualify the generic Space help and show inherited inclusion in the module chooser or adjacent visible guidance. Prefer a small, clearly explained inherited marker for an unchecked child covered by a selected ancestor; do not dim it as excluded. Preserve checkbox toggle/solo behaviour, counts, query-only narrowing and escaped identifiers. Keep marker width equal to existing checkboxes and explain it in help. Avoid introducing a per-frame selected-module-by-choice scan that recreates PAR-001.
- [x] Run focused tests, regenerate only intentional golden changes using existing tooling, inspect changed terminal text with `scripts/read-golden.sh` and inspect raw ANSI diffs. Update README guidance if needed. Run `go test ./...` before the signed commit; record RED/GREEN and inspection evidence.
- [x] Independent task review and separate test cleanup. Finish with whole-branch review, fresh build/race/lint checks, narrow-terminal verification and read-only verification against both saved findings.

### Follow-up completion — 10 September 2026

PAR-001 was fixed in signed `8efd380`. The RED allocation regression measured 20 allocations with 10 sibling selections versus 2,000 with 1,000; known-module matching now parses the observed path once and checks exact structural ancestors in the selected map. Three-run result-checked broad benchmarks fell from 759–789 ms and approximately 10 million allocations to 3.76–3.82 ms and 30,120 allocations. The existing view/sort projection invalidation remains unchanged: the matcher root cause is fixed without a broader cache-lifetime change. Independent task review and final verification accepted this bounded scope.

PAR-002 was fixed in signed `9304d4f`. The real-key RED regression showed an inherited child incorrectly rendered as `[ ]`; GREEN preserves its observations and renders `[+]` without dimming. Removing ancestor coverage produces a genuinely excluded, dimmed `[ ]` state. Help and README explain inherited inclusion. The two intentional help goldens were inspected at 60 and 100 columns, including raw ANSI diffs. Each task passed independent spec/quality review and separate test cleanup; no tests were removed.

At `9304d4f`, the controller's fresh `go test -race -count=1 ./...` passed all 11 packages; build and CLI build passed; lint reported zero issues; formatting, module tidy and diff checks were clean; all module checksums verified. Real terminal checks at 100×30 and 60×9 confirmed inherited and excluded markers, retained versus filtered observations, readable help, and correct close/quit behaviour. The terminal session exited zero. These are local Darwin/arm64 results; no remote CI result is claimed.

Final whole-branch review against `main` and read-only verification of the original findings marked both PAR-001 and PAR-002 resolved, with no actionable collateral or integration finding. The final reviewer independently measured the broad saved probe at 3.72 ms for 10,000 observations across 1,000 modules, compared with the reviewed 785.54 ms sample. Timings are local observations, not portable latency guarantees. All 25 branch commits through the implementation head had valid signatures. No merge or push was performed.

## Planning review — 10 September 2026

Reviewed alongside D1 against item 3 and current TUI code. Self-review corrected the editor declaration to the existing textinput.Model/newSearchInput API. Independent review and the scoped D1 correction review found no remaining actionable issue. See D1's planning review for the existing-suite/build results. No application changes, new golden outputs or terminal implementation checks were performed in this planning task.
