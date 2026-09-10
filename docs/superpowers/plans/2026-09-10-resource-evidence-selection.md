# Resource Evidence and Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide observed resource operations, exact-address summaries and resource/module selection with reconciling RPC evidence.

**Architecture:** Preserve admitted UI and RPC spans as authoritative observations. Build a focused index retaining original slice indices and module identity, then calculate evidence before named selection removes unresolved calls. D2 supplies terminal consumers.

**Tech Stack:** Go 1.25+, standard library and existing test tooling; no new dependency.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), shared rules, item 3 and boundary D. D1 precedes [Resources TUI and filters](2026-09-10-resources-tui-filters.md). Dan approved this split and RPC-only provider/method filtering on 10 September 2026, then authorised sequential subagent implementation of both plans.

## Global Constraints

- “UI-hook duration measures a resource operation. RPC duration measures a provider call.”
- “Source bytes and scanner entry identities remain authoritative.”
- “An inferred address always retains its confidence.”
- “Ambiguous calls never acquire a chosen address, and their duration is never divided among candidates.”
- “Provider and RPC-method filters apply only to RPC evidence; they cannot infer a provider or method for a UI operation.”
- “Severity and request scope affect Raw Log only and do not change this baseline.”
- “Filtering cannot make an input anomaly disappear from the capture summary or turn an unavailable tier into an available one.”
- “Rank by observed UI total, then exact address.”
- Preserve C1/C2 duration admission, position validity, saturation and source locations. No attribution algorithm change, inferred resource ranking, response recovery, JSON, comparison or navigation history.
- Sanitised fixtures, TDD, independent reviews and a separate test-cleanup agent. British/Australian prose and existing formatting/toolchain only.
- Use `/Users/dan/.codex/bin/codex-git`; every commit must be signed and hooks must run. Signing failure is a hard stop. No push or merge without Dan's instruction.

---

## Execution and responsibilities

Start from reviewed C1/C2 (`8de5285` or a descendant). Check clean status, pull with rebase before branch work and use a topic branch/worktree. Read D1, D2 and item 3 together. Run tasks sequentially; D2 starts only after D1 review and verification. Record actual RED/GREEN commands and results here. New API compile failures do not replace demonstrating a behavioural assertion failing once declarations exist.

| Files | Responsibility |
| --- | --- |
| `internal/span/span.go`, `uihook.go`, `uihook_test.go` | Preserve observed module metadata without changing duration admission |
| `internal/attrib/context.go`, `context_test.go`, `correlate.go`, `correlate_test.go` | Preserve the same metadata state through context-derived named attribution |
| New `internal/model/resource_address.go`, `resource_address_test.go` | Structural module decomposition and subtree membership |
| New `internal/model/resources.go`, `resources_test.go` | Indexed operations, exact choices and duration totals |
| New `internal/model/resource_selection.go`, `resource_selection_test.go` | Typed selection, resource rows and disjoint evidence |
| New `internal/model/resources_benchmark_test.go` | Reproducible index/selection benchmark |

Do not modify existing `Filter` or TUI filtering in D1. It leaves a runnable application and tested model API; D2 migrates consumers with their presentation. Model packages remain terminal-unaware.

## Semantics and interface ledger

Addresses are exact, case-sensitive strings. A nonempty address stays selectable even when module decomposition is unsupported. Module identity consists of complete segments including instance keys: dots inside quoted keys are not separators, and an unindexed module differs from every indexed instance. Root is empty Path with Known true; unknown is Known false. Root subtree includes all known module paths, including descendants, not just root resources.

Prefer valid observed module metadata. When absent, conservatively derive the prefix from a complete resource address. A supplied malformed module or contradiction with a successfully decomposed address makes module membership unavailable, without invalidating exact address identity. Preserve spelling; do not normalise resource identities. Use `internal/attrib/testdata/context.log` and `testdata/structured-ui.log` as initial real-parser fixtures; support nested modules, numeric keys and quoted keys with escapes. Unsupported syntax becomes unknown, never guessed root.

These declarations belong to D1 and are consumed verbatim by D2:

```go
// resource_address.go
type ResourceModule struct { Path string; Known bool }
func ResolveResourceModule(address, observed string, observedKnown bool) ResourceModule
func ModuleContains(parent, child string) bool

// resources.go
type DurationTotal struct {
    Count uint64
    TotalMs uint64
    MaxMs uint32
    LowerBound bool
}
type ResourceOperation struct {
    UIIndex int // original Log.UISpans index
    Module ResourceModule
}
type ResourceRow struct {
    Address string
    Operations []ResourceOperation
    UI DurationTotal
    NamedRPC DurationTotal // Contained plus Likely
    OverlappingRPC DurationTotal
}
type ResourceChoice struct { Address string; Module ResourceModule }
type ResourceIndex struct {
    Operations []ResourceOperation // all admitted UI, including unnamed
    RPCModules []ResourceModule // parallel to original RPCSpans
    Choices []ResourceChoice // union of UI, context and named RPC addresses
    Modules []string // known paths and ancestors, including root
}
func BuildResourceIndex(l *Log) ResourceIndex

// resource_selection.go
type ResourceSelection struct {
    Addresses map[string]bool
    Modules map[string]bool
}
type Membership uint8
const (
    MembershipUnknown Membership = iota
    MembershipSelected
    MembershipOther
)
func (s ResourceSelection) Match(address string, module ResourceModule) Membership
type NamedEvidence struct { Contained, Likely, Overlapping DurationTotal }
type SelectionEvidence struct {
    Active bool
    Baseline DurationTotal
    Selected, Other NamedEvidence
    Unresolved DurationTotal
}
type ResourceEvidence struct {
    Baseline DurationTotal
    MissingType, NoContext DurationTotal
    Contained, Likely, Overlapping, Ambiguous, Unattributed DurationTotal
}
type ResourceProjection struct {
    RPCIndices, UIIndices []int // original indices in source order
    Rows []ResourceRow
    UI, UnnamedUI DurationTotal
    Evidence ResourceEvidence // base filters, before named selection
    Selection SelectionEvidence
}
func SelectResources(l *Log, index ResourceIndex, base Filter, named ResourceSelection) ResourceProjection
```

LowerBound is true when any included duration is saturated; qualify sum and maximum accordingly. Never derive duration from positioned start/end. Empty totals have count zero; capture tier availability remains independent of filtered rows. UI covers every selected admitted UI observation; UnnamedUI explains those excluded from named grouping. No pointer to an internal request-ID identity is added.

### Task 1: Retain module evidence and implement structural membership

**Files:** Modify `internal/span/span.go`, `internal/span/uihook.go`, `internal/span/uihook_test.go`, `internal/attrib/context.go`, `internal/attrib/context_test.go`, `internal/attrib/correlate.go`, `internal/attrib/correlate_test.go`; create `internal/model/resource_address.go`, `internal/model/resource_address_test.go`.

**Consumes:** Existing `parseUIHook`, `UIHookBuilder.Structured`, independent admission and coalesced schema counting.

**Produces:** Add `Module string`, `ModuleKnown bool`, `ModuleInvalid bool` to `span.Span`; ResourceModule and resolver/subtree functions from the ledger. ModuleInvalid retains a supplied field's schema failure so the index cannot mistake it for absent metadata and fall back to a guessed module.

Also add ModuleKnown/ModuleInvalid to `attrib.Context` and `attrib.Attribution`, preserving their existing Module string. This is metadata retention only: context windows, pairing, candidate selection and confidence calculation remain unchanged.

- [x] **Step 1: Write real parsing and membership tests.** Recognised structured fixtures include `@level` and `@timestamp`. Explicit empty-string module is known root; absent module permits address fallback. Null/invalid field shape sets ModuleInvalid, uses existing string-schema policy and increments schema once without rejecting valid duration. Check valid duration with invalid timestamp, source ordinal preservation and saturation flags.

```go
func TestResourceModuleBoundaries(t *testing.T) {
    cases := []struct { parent, child string; want bool }{
        {"", `module.app[0]`, true},
        {`module.app`, `module.app.module.db`, true},
        {`module.app`, `module.application`, false},
        {`module.app`, `module.app[0]`, false},
        {`module.app[0]`, `module.app[1]`, false},
        {`module.app["a.b"]`, `module.app["a.b"].module.db`, true},
        {`module.app["a"]`, `module.app["a.b"]`, false},
    }
    for _, tc := range cases {
        if got := ModuleContains(tc.parent, tc.child); got != tc.want {
            t.Errorf("%q contains %q = %v", tc.parent, tc.child, got)
        }
    }
    got := ResolveResourceModule(`module.m["k"].aws_instance.web[0]`, "", false)
    if !got.Known || got.Path != `module.m["k"]` { t.Fatalf("module=%+v", got) }
    if ResolveResourceModule(`module.m.aws_instance.web`, "", true).Known {
        t.Fatal("conflicting observed root accepted")
    }
}
```

Also cover root managed/data/ephemeral shapes, escaped quote/backslash/`]` keys, Unicode identifiers, unclosed keys, trailing separators, partial resource addresses and malformed supplied modules. Exact address spelling survives decomposition failure.

- [x] **Step 2: Run RED.** `go test ./internal/span ./internal/model -run 'Test(UI.*Module|ResourceModule)' -count=1`. After declarations compiled, the command failed on the expected semantic assertions: UI module evidence remained zero-valued and every resolver/membership result remained unknown or false.
- [x] **Step 3: Implement retention.** Extend both anonymous resource structs in `uiHook` and `parseUIHook` identically. Decode presence explicitly, reuse schema boolean, clone retained module and assign all three Span fields in the admitted construction.

```go
// Additional fields in both resource structs:
Module string `json:"module"`
ModuleKnown bool `json:"-"`
ModuleInvalid bool `json:"-"`
// In parseUIHook:
if value, present := resourceFields["module"]; present {
    resource.ModuleKnown = decodeJSONString(value, &resource.Module)
    resource.ModuleInvalid = !resource.ModuleKnown
    if resource.ModuleInvalid { schema = true }
}
```

Apply the same field declarations to both resource structs in `ctxHook`/`parseContextHook`. Remove module from that function's generic string loop and decode it once explicitly with `decodeContextString`, setting Known/Invalid by the same presence rule. Copy the flags in Context construction and `named` attribution construction. Extend real collector/correlation tests with absent, explicit-root, null, object-valued and conflicting module metadata on an otherwise valid named context. RPC duration, confidence and context pairing remain unchanged; membership in the index must remain unknown for invalid metadata.

```go
// In Context construction and then in named's Attribution construction:
ModuleKnown: r.ModuleKnown, // use c.ModuleKnown in named
ModuleInvalid: r.ModuleInvalid, // use c.ModuleInvalid in named
```

- [x] **Step 4: Implement conservative decomposition.** In private helpers, scan with bracket-depth, quoted-string and backslash-escape state; split only on dots outside brackets. Reject unbalanced state, empty tokens, raw controls and multiple index suffixes. Consume module/name-with-optional-key pairs. Address remainder must be a complete managed resource pair or data/ephemeral plus a pair; module-only input must contain only complete module pairs. Accept digit-only numeric keys or properly terminated quoted keys; unsupported expressions remain unknown. This is structural recognition, not a complete Terraform validator. Compare whole retained module tokens.

```go
// Final prefix comparison after validating/splitting both module paths.
if len(parentSegments) > len(childSegments) { return false }
for i := range parentSegments {
    if parentSegments[i] != childSegments[i] { return false }
}
return true
```

Resolver rules: validate supplied module before use; if observedKnown and malformed, return unknown. If observedKnown and a known address decomposition disagrees, return unknown. Otherwise valid observed module wins. With no observed module return address decomposition. Callers first reject ModuleInvalid; otherwise pass the retained ModuleKnown bit for spans, contexts and attributions alike, including explicit root. Never infer presence from nonempty strings.

- [x] **Step 5: Run GREEN and independent review.** Focused GREEN passed with `go test ./internal/span ./internal/model -run 'Test(UI.*Module|ResourceModule)' -count=1`; affected packages passed with `go test ./internal/span ./internal/model ./internal/attrib -count=1`; formatting and `go test ./...` passed. Round-one review found over-permissive identifiers, rejection of `[` inside a quoted key, and ambiguous module-field comments. Behavioural RED `go test ./internal/model -run 'TestResourceModule(Rejects|Accepts)' -count=1` failed on those identifier and key cases; the same command passed after the fixes. Scoped rereview then found the Unicode predicates omitted `Nl` and the `Other_ID_Start`/`Other_ID_Continue` properties. Behavioural RED `go test ./internal/model -run TestResourceModuleAcceptsConservativeTerraformIdentifiers -count=1` failed for representative ℘, Ⅰ and middle-dot cases; focused GREEN including malformed-input rejection passed after matching HCL's identifier classes. Both independent review verdicts were clean after the two fix rounds. Separate cleanup retained all 54 cases across the affected tests without edits.
- [x] **Step 6: Signed commit.** Staged the nine named files and execution record, then committed `Retain resource module evidence` as 5c096bb, followed by signed review-fix commits 1847149 and d3f2885. Signature verification reported G for all three commits.

### Task 2: Build indexed observations and complete filter choices

**Files:** Create `internal/model/resources.go`, `internal/model/resources_test.go`.

**Consumes:** Task 1 resolver; original Log UI/RPC spans, contexts and attributions.

**Produces:** DurationTotal, ResourceOperation, ResourceRow, ResourceChoice, ResourceIndex and BuildResourceIndex from the ledger.

- [x] **Step 1: Write index tests.** Repeated completions keep different actions, entries and original indices, including zero/unpositioned/saturated durations. Context-only and named-RPC-only addresses become choices without UI operations. Ambiguous candidates never become invented identities. Missing UI address still has an operation. Test real Load plus SourceLocation for distinct completions.

```go
func TestResourceIndexRetainsOccurrences(t *testing.T) {
    l := &Log{UISpans: []span.Span{
        {Entry: 2, Address: "aws_instance.a", RPC: "create", DurationMs: 1000},
        {Entry: 7, Address: "aws_instance.a", RPC: "update", DurationMs: 0},
        {Entry: 9, DurationMs: 20},
    }}
    got := BuildResourceIndex(l)
    if len(got.Operations) != 3 || len(got.Choices) != 1 { t.Fatalf("index=%+v", got) }
    for i, op := range got.Operations {
        if op.UIIndex != i { t.Fatalf("operation %d index=%d", i, op.UIIndex) }
    }
}
```

- [x] **Step 2: Run RED.** `go test ./internal/model -run TestResourceIndex -count=1` failed after declarations compiled: occurrence and real-load tests received no operations, complete-evidence tests received no choices/modules/RPC modules, conflict/supplement and mutation tests received no returned slices, and DurationTotal remained zero. Preconditions were tightened after the first run so missing slices fail diagnostically rather than causing follow-on panics; the behavioural failures remained.
- [x] **Step 3: Implement a source-order index.** Walk UI spans once, retaining unknown module for ModuleInvalid spans without resolver fallback. Apply the same invalid guard and retained presence bits to context choices and named RPC modules. Walk RPCSpans with original indices, resolve modules only for named Contained/Likely/Overlapping attribution and guard short/nil attribution slices. Union nonempty addresses from UI, context and named RPC evidence. Sort exact choices lexically; union known module paths and every ancestor, sorting root first. Conflicting known module facts for one choice yield unknown; absence may be supplemented by known evidence but cannot erase a contradiction. Preserve each operation's independent module fact. Returned slices must not alias mutable Log slices.

```go
func (d *DurationTotal) add(s span.Span) {
    d.Count++
    d.TotalMs += uint64(s.DurationMs)
    if s.DurationMs > d.MaxMs { d.MaxMs = s.DurationMs }
    d.LowerBound = d.LowerBound || s.DurationSaturated
}
```

Build once per capture; no source-byte rescans, response reconstruction or mutation of CaptureQuality. Index must not be reused with another Log.

- [ ] **Step 4: Run GREEN and review.** `go test ./internal/model -run TestResourceIndex -count=1` and `go test ./internal/model -count=1` passed. `go test ./...` passed all 11 packages before commit, and the diff check was clean. Self-review covered deterministic order, all ModuleInvalid guards, explicit ModuleKnown handling, conflict persistence, short attribution slices and returned-index mutation isolation. Independent review remains pending with the controller.
- [x] **Step 5: Signed commit.** Staged the two task files and this execution record, then committed `Index observed resource operations` with the wrapper and mandatory signing.

**Round-one review fix:** Review found that aggregate choices treated supplied invalid, malformed and address-contradictory module metadata like absent metadata, allowing a later known fact to erase the unavailable state. After cleanup commit e86bcca, nine regression cases covered all three unavailable states across UI, context and named-RPC evidence, with the contradiction tested before and after consistent evidence. `go test ./internal/model -run TestResourceIndexSuppliedUnavailableModuleEvidenceIsSticky -count=1` failed all nine cases before the fix and passed after a private sticky-unavailable bit was added to choice accumulation. The existing absent-unknown supplementation case still passes, and operation/RPC module facts remain independent. Focused index tests, the complete model package and `go test ./...` passed. The once-per-Log lifetime cannot be verified until D2 owns index construction. The signed fix commit uses subject `Preserve unavailable resource module evidence`; scoped rereview remains pending with the controller.

### Task 3: Select resources and reconcile admitted evidence

**Files:** Create `internal/model/resource_selection.go`, `internal/model/resource_selection_test.go`; extend `internal/model/resources_test.go`.

**Consumes:** Tasks 1/2, Filter.MatchSpan, HasAddressContext, confidence and Span.HasPosition.

**Produces:** Remaining selection/evidence/projection declarations and SelectResources from the ledger.

- [ ] **Step 1: Write the accounting regression.** A=10ms Contained, B=20ms Likely, unresolved=5ms; 1000ms UI for A. Selection A preserves B as other named work. A method filter removing B changes baseline from 35 to 15 without changing UI.

```go
func TestResourceSelectionReconciles(t *testing.T) {
    l := &Log{
        RPCSpans: []span.Span{
            {Entry: 1, ResourceType: "aws_instance", RPC: "ReadResource", DurationMs: 10, TimestampStatus: logfmt.TimestampValid},
            {Entry: 2, ResourceType: "aws_instance", RPC: "Other", DurationMs: 20, TimestampStatus: logfmt.TimestampValid},
            {Entry: 3, ResourceType: "aws_instance", RPC: "ReadResource", DurationMs: 5, TimestampStatus: logfmt.TimestampValid},
        },
        UISpans: []span.Span{{Entry: 4, Address: "aws_instance.a", DurationMs: 1000}},
        Contexts: []attrib.Context{{Address: "aws_instance.a"}},
        Attribs: []attrib.Attribution{
            {Address: "aws_instance.a", Confidence: attrib.Contained},
            {Address: "aws_instance.b", Confidence: attrib.Likely},
            {Confidence: attrib.Unattributed},
        },
    }
    index := BuildResourceIndex(l)
    named := ResourceSelection{Addresses: map[string]bool{"aws_instance.a": true}}
    got := SelectResources(l, index, Filter{}, named)
    e := got.Selection
    if e.Baseline.TotalMs != 35 || e.Selected.Contained.TotalMs != 10 ||
        e.Other.Likely.TotalMs != 20 || e.Unresolved.TotalMs != 5 { t.Fatalf("%+v", e) }
    if len(got.RPCIndices) != 1 || got.RPCIndices[0] != 0 || got.UI.TotalMs != 1000 { t.Fatal(got) }
    got = SelectResources(l, index, Filter{RPCs: map[string]bool{"ReadResource": true}}, named)
    if got.Selection.Baseline.TotalMs != 15 || got.UI.TotalMs != 1000 { t.Fatal(got) }
}
```

Add cases for nil/empty/false allow-lists; OR within/AND across dimensions; matching address with unknown module versus definitely failing address; no-context; missing-type ReadResource/GetProviderSchema/unknown method; `(none)` type selection; zero/missing tiers; explicit zero; unpositioned durations; short attribution slices; Overlapping; repeated actions; UI-only/RPC-only/mixed; unnamed UI; saturation; root/indexed descendants. Assert both count and duration partition sums. Compare whole-log quality before/after.

- [ ] **Step 2: Run RED.** `go test ./internal/model -run 'TestResource(Selection|Projection)' -count=1`; record behavioural accounting and membership failures.
- [ ] **Step 3: Implement three-valued matching.** Nil dimension is selected without testing metadata; empty non-nil dimension is other. Present exact identity decides address membership; missing identity is unknown. Module membership requires Known. Any definite dimension failure wins over unknown; otherwise unknown wins over selected. Only true map values are selected alternatives.

```go
func combineMembership(a, b Membership) Membership {
    if a == MembershipOther || b == MembershipOther { return MembershipOther }
    if a == MembershipUnknown || b == MembershipUnknown { return MembershipUnknown }
    return MembershipSelected
}
```

Inactive named selection passes missing metadata. For active RPC selection, no-context, unusable position and non-named attribution are unresolved, never an inferred Other from candidate names. Named attribution with a definite address mismatch remains Other even if module membership is unknown.

- [ ] **Step 4: Implement baseline and rows in linear passes.** Admit RPCs using base provider/type/method filters. Count every admitted RPC in baseline; classify missing type first, then no-context, then Contained/Likely/Overlapping/Ambiguous/Unattributed. This partition does not replace C2's whole-log full confidence distribution. Compute selected/other/unresolved before discarding nonselected original indices. Selected/Other retain each confidence; row supplements combine Contained/Likely and keep Overlapping separate.

For UI apply only type and named filters. Count all selected UI in UI, unnamed selected UI in UnnamedUI; group others by exact address. Preserve operation indices in source order. RPC supplements attach only to existing named UI rows; context-only RPC evidence creates no ranked UI row. Sort rows descending UI total then exact address, retaining lower-bound flags.

```go
uiBase := Filter{Types: base.Types}
for _, op := range index.Operations {
    s := l.UISpans[op.UIIndex]
    if !uiBase.MatchSpan(s) || named.Match(s.Address, op.Module) != MembershipSelected { continue }
    result.UIIndices = append(result.UIIndices, op.UIIndex)
    result.UI.add(s)
    // Route empty Address to UnnamedUI; otherwise accumulate its exact-address row.
}
```

Use original indices for attributions, never repeated linear AttributionForEntry lookup. Selection.Active means a non-nil named dimension. Evidence always covers the base-filtered preselection RPCs; UI scope is separately represented. Severity/request scope do not enter either calculation. No timestamp alignment, inferred provider or position-derived duration.

- [ ] **Step 5: Run GREEN and review.** `go test ./internal/model ./internal/span ./internal/attrib -count=1`. Independently review each disjoint partition and missing-type priority, while retaining separate full confidence facts.
- [ ] **Step 6: Signed commit.** Stage task files and record; commit `Select resource evidence with reconciling totals`.

### Task 4: Validate cost, clean tests and hand off D1

**Files:** Create `internal/model/resources_benchmark_test.go`; update this plan's execution evidence.

**Consumes:** D1 API and existing model benchmark conventions.

**Produces:** Repeatable performance evidence and reviewed runnable integration commit.

- [ ] **Step 1: Add benchmarks.** Setup 10,000 addresses, three UI operations each and 100,000 RPCs across named/unresolved states outside timing. Benchmark BuildResourceIndex separately from repeated SelectResources, unconstrained and selective provider/resource/module cases. Consume results, report allocations and document fixture counts/Go/OS/CPU. No machine-specific pass threshold.

```go
// After building synthetic l/index outside the timed region:
b.ReportAllocs()
b.ResetTimer()
for b.Loop() {
    got := SelectResources(l, index, Filter{}, ResourceSelection{})
    if len(got.UIIndices) != len(l.UISpans) { b.Fatal("lost observations") }
}
```

- [ ] **Step 2: Measure.** `go test ./internal/model -run '^$' -bench 'BenchmarkResource' -benchmem -benchtime=200ms -count=3`. Investigate source rescans/quadratic lookups before adding caches; no new storage/parallel loading design.
- [ ] **Step 3: Separate test cleanup.** Dispatch a different agent with test-cleanup after implementation. Preserve original-index, tri-state and real parser coverage; review cleanup edits independently.
- [ ] **Step 4: Final checks.** Run `go test -race -count=1 ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, `gofmt -d .`, `go mod tidy -diff`, `go mod verify`. Require pristine expected output, zero lint issues and no format/module diff. Existing CI builds linux/darwin amd64/arm64; distinguish local from CI verification.
- [ ] **Step 5: Whole-D1 independent review and signed evidence commit.** Review item 3 and D2's interface dependencies, resolve findings and record actual checks/commit. Commit `Verify resource evidence and selection`. D2 starts from this reviewed result; do not claim the screen exists yet.

## Coverage review

Task 1 covers structural modules and unavailable metadata. Task 2 retains repeated identities and context-only choices. Task 3 covers grouping, tier scopes, missing-type priority, reconciling evidence and lower bounds. Task 4 verifies cost/integration. D2 owns controls, empty states, qualifications and terminal layouts. Item 4 owns operation drill-down/history; items 5–8 remain separate.

## Planning review — 10 September 2026

Self-review checked item 3 coverage, current code interfaces, task ordering and absence of unfinished steps. Independent review found module presence/invalid state also needed to survive context-derived attribution; Task 1 and the index contract now cover both paths. Scoped rereview found no remaining issue. D2's editor type was corrected to the existing textinput.Model during self-review.

The planning change contains documentation only. `go test ./...` passed all 11 packages (some cached); `go build ./...` passed. These validate the existing application, not the proposed APIs or future behaviour. Implementation checkboxes remain open; implementation review, test cleanup, benchmarks and terminal evidence are execution requirements.
