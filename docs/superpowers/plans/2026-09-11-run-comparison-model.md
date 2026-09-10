# Run Comparison Model and Schema Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Calculate honest before/after changes from complete admitted observations and define the exact comparison JSON contract.

**Architecture:** Put comparison calculations in focused `internal/model` files. Assemble a comparison report from two existing `profile.Report` values, retaining each capture's quality separately. Renderers and CLI integration follow in H2; no new package or dependency is needed.

**Tech Stack:** Existing Go 1.25+ toolchain, standard testing/sort/math packages, existing model/profile data.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), item 7 comparison behaviour and Boundary H. Follow with [H2 renderers and CLI](2026-09-11-run-comparison-cli.md).

## Global Constraints

- “Load both raw logs with the same parser/model version.”
- “Counts and means use admitted duration observations, including those with unavailable positions; rejected timing records remain separate quality facts.”
- “Percentage change from zero is unavailable, not infinity.”
- “UI-hook duration measures a resource operation. RPC duration measures a provider call. They overlap and must never be added, subtracted to claim unexplained time, or represented as interchangeable measurements.”
- “Do not match by inferred RPC address, alias similarity or fuzzy resource names.”
- “If either side's timing is a lower bound, retain both observations and their flags but make the timing delta and percentage unavailable”.
- “Comparisons produce observations, not pass/fail performance judgements.”
- “Do not include current generation timestamps or absolute input paths by default”.
- Use real domain logic and sanitised fixtures; TDD, independent review and a separate test-cleanup pass for each implementation phase. No compatibility layer, parallel loading, new dependencies or unrelated refactoring.
- Use signed commits through `/Users/dan/.codex/bin/codex-git`; signing failure stops work. No push or merge is implied.

---

## Status and scope

Dan authorised drafting H1/H2 on 11 September 2026 after G merged locally at `801a917`, then approved both plans and subagent implementation. H1 yields a tested calculation/report API and public schema reference. H2 consumes those exact APIs to expose the feature. Boundary I is unaffected.

The spec permits a comparison package only if calculations outgrow focused model files. Keep calculations in model and assembly/rendering in profile. Parsing JSON profile exports, comparing request IDs, resource aliases or inferred RPC addresses, workload normalisation, threshold exits, timeline alignment and provider-version reconciliation are outside H.

## Exact comparison semantics

The five sections below are always present in this order. Each section compares one tier only:

| Kind | Tier | Exact key fields |
| --- | --- | --- |
| `rpc_providers` | `rpc` | provider |
| `rpc_resource_types` | `rpc` | resource_type |
| `rpc_methods` | `rpc` | provider, resource_type, method |
| `ui_resource_types` | `ui` | resource_type |
| `ui_operations` | `ui` | address, action |

Use raw strings as map keys, including empty strings. Do not use `FacetKey`, display escaping, delimiter concatenation or normalisation: an empty identifier must not collide with a literal `(none)`, and embedded separators must not collide. Use comparable Go structs for compound keys. UI action is the existing `span.Span.RPC` value. UI observations with an empty address contribute to UI resource-type totals and a separate `UnnamedUI` total for each capture; they do not become an exact-address operation row. Empty action remains an exact empty action on a known address. Never derive UI addresses from RPC attribution.

A tier is available when its admitted observation slice is nonempty, as in G. Available zero-duration observations remain available. For the union of keys:

| Condition | Row state | Side summaries | Changes |
| --- | --- | --- | --- |
| Both tiers available, key on both sides | `matched` | Measured totals | Defined per metric below |
| Both tiers available, key only after | `added` | Before count/total zero; mean/max null | Count and total deltas valid |
| Both tiers available, key only before | `removed` | After count/total zero; mean/max null | Count and total deltas valid |
| Either whole tier unavailable | `unavailable` | Unavailable side null; observed side measured | All changes null |

Both unavailable gives no rows and false availability flags. `added` and `removed` describe observed evidence, never Terraform create/destroy actions. Each available side summary has count, sum, mean, maximum and lower-bound flag. No observations means count/sum zero and mean/maximum null. Mean is `float64(sum)/float64(count)` when count is positive, without integer truncation. Retain admitted observations with invalid positions. Do not filter by capture quality or confidence.

Changes are **after minus before** for count, total, mean and maximum. Percent is `100 * change / before`, null if the baseline metric is zero or absent. A missing mean/maximum on either side makes its delta/percent null. Count and total deltas for an absent group remain available when both tiers are available. Any contributing saturated UI duration sets the side's lower-bound flag; if either side is lower-bounded, suppress total/mean/maximum deltas and their percentages together, while retaining observed values and exact admitted-count changes. Counts refer to admitted observations, not the number of real-world operations the logging missed.

Integer differences must not overflow or round through float64. Use signed magnitude internally; normalise zero to nonnegative. Compare operands before subtraction. Export them as JSON integer tokens, not quoted strings. Means/percentages are finite float64 approximations; documented aggregate sums and integer deltas retain exact integer values. Accumulate totals with checked uint64 addition; return fixed `comparison duration total overflows uint64` on overflow, with no partial report. Count is bounded by the input slice length. No arbitrary precision arithmetic dependency or signed-int64 narrowing.

Order each section by exact total-duration delta descending: positive increases, zero, then decreases nearest zero first. Rows without an exact total delta form a final unranked group sorted by raw key. Equal deltas tie-break by raw key in the table's field order, bytewise ascending. Signed-magnitude ordering must also work beyond MaxInt64. A lower-bound row never participates in the exact timing-change ranking. Preserve this complete order in JSON; H2 text has independently limited ranked and unranked lists for each section.

Capture comparison metadata includes sorted unique nonempty observed RPC provider identities from each capture. `provider_identity_status` is `unknown` if either RPC tier is unavailable or either has an empty provider identifier; otherwise `same` for equal sets, `different` for unequal sets. This describes observable identity strings, not provider version equivalence. `logging_configuration` is always `unknown`: current data does not establish equivalence. Keep both quality summaries, origins, rejected counts, context limitations and reconstruction snapshots; never reconstruct responses for comparison.

## Files and interfaces

| File | Responsibility |
| --- | --- |
| Create `internal/model/comparison.go` | Types, grouping, union, availability, side summaries, provider identity sets |
| Create `internal/model/comparison_delta.go` | Checked sums, exact signed differences, percentages and ranking |
| Create `internal/model/comparison_test.go` | Behavioural comparison cases |
| Create `internal/model/comparison_delta_test.go` | Arithmetic boundary and ordering cases |
| Create `internal/model/comparison_benchmark_test.go` | Sanitised repeated-key and many-key scale measurements |
| Create `internal/profile/comparison_data.go` | Adapter from complete F reports to model comparison |
| Create `internal/profile/comparison_data_test.go` | Real loaded-capture assembly, quality retention and immutability |
| Create `docs/comparison-json-v1.md` | Exact schema reference specified below |

The following exported declarations are the H1/H2 interface. They have no JSON tags; H2 owns private wire types. String kind/state values are the closed sets defined above, not user-provided labels.

```go
// internal/model/comparison.go
type ComparisonInput struct { RPC, UI []span.Span }
type SignedChange struct { Negative bool; Magnitude uint64 }
type ComparisonTotal struct {
    Count, TotalMs uint64
    MeanMs *float64
    MaxMs *uint32
    LowerBound bool
}
type ComparisonChanges struct {
    Count, TotalMs, MaxMs *SignedChange
    MeanMs *float64
    CountPercent, TotalPercent, MeanPercent, MaxPercent *float64
}
type ComparisonKey struct { Provider, ResourceType, Method, Address, Action string }
type ComparisonRow struct {
    Key ComparisonKey
    State string
    Before, After *ComparisonTotal
    Changes ComparisonChanges
}
type ComparisonSection struct {
    Kind, Tier string
    BeforeAvailable, AfterAvailable bool
    Rows []ComparisonRow
}
type Comparison struct {
    Sections []ComparisonSection
    BeforeUnnamedUI, AfterUnnamedUI *ComparisonTotal
    BeforeProviders, AfterProviders []string
    ProviderIdentityStatus string
}
func Compare(before, after ComparisonInput) (Comparison, error)

// internal/profile/comparison_data.go
type ComparisonReport struct {
    Before, After Report
    Data model.Comparison
}
func BuildComparison(before, after Report) (ComparisonReport, error)
type ComparisonMetadata struct {
    ToolVersion, BeforeBasename, AfterBasename string
}
```

`BuildComparison` extracts Span values from the two reports' complete RPC/UI observation slices into model inputs, calls `model.Compare` and stores both reports unchanged. It must not use ranking lists, positioned-only views, rendered JSON or inferred resource aggregates. Report inputs are immutable snapshots: retaining them is allowed, but no function may mutate them. Model output owns its maps/slices/pointers and does not alias input storage. Quality consistency comes from reports built by `profile.Build`; do not add a generic validator for arbitrary contradictory hand-built reports.

## Exact comparison JSON v1 contract

All fields below are mandatory. Empty collections are `[]` or `{}`; unavailable values are null; no `omitempty`. Define private structs in H2, never embed model types into JSON. Field order follows this description. No timestamps of generation, full paths, raw bodies, source observation arrays, interned IDs or copied profile ranking lists.

- Root: `schema_version: 1`, `kind: "comparison"`, `tool_version`, `duration_unit: "ms"`, `before`, `after`, `comparability`, `sections`, `qualifications`.
- Each capture: `input`, `tiers`, `quality`, `unnamed_ui`. `input`, `tiers` and `quality` use **exactly** G's field names, nulls and meanings from [Profile JSON v1](../../profile-json-v1.md), including reconstruction status; `unnamed_ui` is a summary or null when UI is unavailable. Entry locations in quality remain local to that capture.
- `comparability`: `logging_configuration: "unknown"`, `provider_identity_status`, `before_providers`, `after_providers`.
- Section: `kind`, `tier`, `before_available`, `after_available`, `rows`. Five sections in the fixed order above; no sections omitted.
- Row: `key`, `state`, `before`, `after`, `changes`. The key object contains **only** the applicable fields in that section's key table, in that order. Fields contain original strings (including empty identifiers).
- Side summary: `count`, `total_ms`, `mean_ms`, `max_ms`, `lower_bound`. Summary null means unavailable tier; measured empty group is the zero/null summary described above.
- `changes`: `count`, `total_ms`, `mean_ms`, `max_ms`, `count_percent`, `total_percent`, `mean_percent`, `max_percent`. The count/total/max changes are exact signed JSON integers or null. The mean and percentages are finite JSON numbers or null. Positive JSON integers have no `+` prefix.
- `qualifications`, in order: `unmasked_identifiers`, `logging_affects_durations`, `rpc_and_ui_measure_different_work`, `ui_duration_rounding`, `observed_changes_are_not_causal`, `added_removed_are_observation_presence`, `independent_scrub_aliases_may_differ`, `logging_configuration_unknown`, `lower_bounds_do_not_define_timing_deltas`.

Validate UTF-8 in every exported string; fixed error `comparison JSON contains invalid UTF-8`, never echo an invalid value. Preserve valid control and Unicode characters through JSON encoding. Keep profile v1 unchanged. Integer magnitudes may exceed JavaScript precision; consumers need typed integers or `Decoder.UseNumber`. Schema versioning is not a compatibility commitment. JSON includes all rows, including unranked/unavailable rows, regardless of text limits.

## Task 1: Implement comparison calculations

**Files:** All new model comparison files listed above.

**Interfaces:** Consume `ComparisonInput{RPC, UI []span.Span}`. Produce the exact model declarations and `Compare` API above for H1 task 2 and H2. Apply all semantics and ordering in this plan.

- [ ] **Step 1: Add a failing behavioural test.** Start with a volume-versus-mean example; import the existing span package and `testing`. Once the API declarations compile, record the behavioural failure before implementing grouping.

```go
func TestCompareSeparatesVolumeAndMean(t *testing.T) {
    before := ComparisonInput{RPC: []span.Span{
        {Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 10},
        {Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 10},
    }}
    after := ComparisonInput{RPC: []span.Span{
        {Provider: "p", ResourceType: "r", RPC: "Read", DurationMs: 20},
    }}
    got, err := Compare(before, after)
    if err != nil { t.Fatal(err) }
    if len(got.Sections) != 5 || len(got.Sections[2].Rows) != 1 { t.Fatalf("sections: %+v", got.Sections) }
    row := got.Sections[2].Rows[0]
    if row.State != "matched" || row.Before == nil || row.After == nil { t.Fatalf("row: %+v", row) }
    if row.Changes.Count == nil || *row.Changes.Count != (SignedChange{Negative: true, Magnitude: 1}) { t.Fatalf("count: %+v", row.Changes.Count) }
    if row.Changes.TotalMs == nil || row.Changes.TotalMs.Magnitude != 0 { t.Fatalf("total: %+v", row.Changes.TotalMs) }
    if row.Changes.MeanMs == nil || *row.Changes.MeanMs != 10 { t.Fatalf("mean: %+v", row.Changes.MeanMs) }
}
```

- [ ] **Step 2: Run RED.** `go test ./internal/model -run TestCompareSeparatesVolumeAndMean -count=1`. Distinguish initial absent API compilation from a meaningful assertion failure. Add table-driven cases for identical inputs, added/removed keys while each tier has another admitted observation, zero baseline, zero-duration availability, empty inputs and disjoint tiers. Assert null versus zero and all metric deltas, not just row counts.
- [ ] **Step 3: Implement grouping and arithmetic.** Aggregate each input once per tier using typed keys, then form section key unions. Construct independent summaries, calculate deltas only when each metric is available, and sort complete rows. These are the required arithmetic kernels in `comparison_delta.go`:

```go
func signedChange(before, after uint64) SignedChange {
    if after < before { return SignedChange{Negative: true, Magnitude: before-after} }
    return SignedChange{Magnitude: after-before}
}
func addDuration(total uint64, duration uint32) (uint64, error) {
    if math.MaxUint64-total < uint64(duration) {
        return 0, errors.New("comparison duration total overflows uint64")
    }
    return total + uint64(duration), nil
}
```

For count/total percentage, convert the already-computed signed difference to float64 and divide by the positive baseline; never subtract two rounded float64 totals. For mean percentage use the two finite means. For integer ranking compare sign then magnitude (reverse magnitude ordering for negatives), with raw-key ties. Do not mutate the original observation slices.

- [ ] **Step 4: Extend GREEN coverage.** Add repeated UI address/action aggregation and different-action separation; missing-address UI accounting; raw missing identifiers versus literal `(none)`; compound identifiers containing separators; non-positioned durations; lower bound on either side suppressing every timing delta; count deltas retained; independent alias renames yielding added/removed, not fuzzy matches. Test provider identity `same`/`different`/`unknown`, empty provider, UI-only, input-order permutations and tied/unranked ordering. Exercise signed differences around 2^53, MaxInt64 and MaxUint64, `addDuration` overflow and zero normalisation directly, without enormous fixtures. Snapshot inputs to prove nonmutation and mutate returned summaries to prove independent pointer ownership. Run `go test ./internal/model -run 'TestCompare|TestComparison' -count=1`, then the whole model package.
- [ ] **Step 5: Benchmark, review, cleanup and commit.** Use this benchmark with inputs built outside the timed loop (imports: fmt, testing and the existing span package):

```go
func BenchmarkCompare(b *testing.B) {
    for _, size := range []int{1000, 10000} {
        for _, distinct := range []bool{false, true} {
            b.Run(fmt.Sprintf("n=%d/distinct=%t", size, distinct), func(b *testing.B) {
                input := ComparisonInput{RPC: make([]span.Span, size), UI: make([]span.Span, size)}
                for i := range size {
                    key := "shared"
                    if distinct { key = fmt.Sprintf("sanitised_%d", i) }
                    input.RPC[i] = span.Span{Provider: key, ResourceType: key, RPC: "Read", DurationMs: 10}
                    input.UI[i] = span.Span{Address: key, ResourceType: key, RPC: "read", DurationMs: 1000}
                }
                b.ReportAllocs()
                b.ResetTimer()
                for range b.N {
                    if _, err := Compare(input, input); err != nil { b.Fatal(err) }
                }
            })
        }
    }
}
```

Report allocations and timings without absolute performance thresholds; inspect grouping/sorting rather than quadratic key joins. Run `go test ./internal/model -run '^$' -bench BenchmarkCompare -benchmem`. Independent task review and separate cleanup follow; fix actionable issues. Signed commit: `Calculate evidence-qualified run comparisons`.

## Task 2: Assemble reports and publish the schema contract

**Files:** `internal/profile/comparison_data.go`, `comparison_data_test.go`, `docs/comparison-json-v1.md`.

**Interfaces:** Consume `model.Compare` and existing `profile.Report`; produce `ComparisonReport`, `ComparisonMetadata` and `BuildComparison` exactly as declared above. H2 consumes them without API renames.

- [ ] **Step 1: Add failing assembly tests from real loaded captures.** Follow this structure, using the existing `model.Load` and `Build` APIs:

```go
func TestBuildComparisonKeepsCaptureEvidence(t *testing.T) {
    log, err := model.Load("../../testdata/provider-rpc.log")
    if err != nil { t.Fatal(err) }
    report, err := Build(log)
    if err != nil { t.Fatal(err) }
    got, err := BuildComparison(report, report)
    if err != nil { t.Fatal(err) }
    if len(got.Data.Sections) != 5 || len(got.Data.Sections[0].Rows) != 1 { t.Fatalf("sections: %+v", got.Data.Sections) }
    row := got.Data.Sections[0].Rows[0]
    if row.Before == nil || row.Before.Count != 2 || row.Before.TotalMs != 6 { t.Fatalf("before: %+v", row.Before) }
    if row.After == nil || row.After.Count != 2 || row.After.TotalMs != 6 { t.Fatalf("after: %+v", row.After) }
    if got.Before.Reconstruction.State != "not_checked" || log.ReconstructionQuality().State != "not_checked" { t.Fatal("comparison reconstructed responses") }
}
```

Also compare `structured-ui.log` against `core-only.log` and `resources-long-lower-bound.log` against itself. Independently assert quality summaries unchanged, originals and ranks untouched, all sections present, saturated deltas null and unavailable sides null. Ensure invalid-position admitted observations survive adapter extraction. These fixtures already exist; no private captures.
- [ ] **Step 2: Run RED.** `go test ./internal/profile -run TestBuildComparison -count=1`; after declaring the interface, require behavioural failure for missing rows/evidence before implementation.
- [ ] **Step 3: Implement the thin adapter.** Extract span values with one loop per slice and call the model once:

```go
func comparisonInput(r Report) model.ComparisonInput {
    in := model.ComparisonInput{RPC: make([]span.Span, len(r.RPC)), UI: make([]span.Span, len(r.UI))}
    for i, observation := range r.RPC { in.RPC[i] = observation.Span }
    for i, observation := range r.UI { in.UI[i] = observation.Span }
    return in
}
func BuildComparison(before, after Report) (ComparisonReport, error) {
    data, err := model.Compare(comparisonInput(before), comparisonInput(after))
    if err != nil { return ComparisonReport{}, err }
    return ComparisonReport{Before: before, After: after, Data: data}, nil
}
```

- [ ] **Step 4: Write `docs/comparison-json-v1.md`.** Include every field, section, ordering, nullability, numeric rule and qualification from the exact contract above, plus before/after direction, lower-bound rules, admitted-count meaning, disclosure and decoder `UseNumber` guidance. Link G's unchanged shared capture objects. This task defines the contract; H2 implements its encoder.
- [ ] **Step 5: Verify and commit.** Run focused assembly tests, `go test ./...` and `go build ./...`. Independent task review and separate cleanup must pass before H2. Signed commit: `Assemble comparison reports and define JSON schema`. Record actual test/benchmark/review evidence in this plan; do not mark H complete yet.

## Acceptance mapping and self-review

| Spec requirement | Planned evidence |
| --- | --- |
| Stable group identity, independent RPC/UI | Task 1 typed keys, section order and collision/repeated-operation cases |
| Admitted duration counts, missing/zero/lower-bound distinctions | Task 1 state/delta tables and task 2 real captures |
| Deterministic increases-first ordering, exact changes | Task 1 signed arithmetic and permutation cases |
| Both quality summaries and honest comparability | Task 2 preservation; H2 exact encoded keys and text checks |
| No reconstruction or inferred address comparison | Task 2 accessor assertion; task 1 uses only span identity |
| Versioned complete machine contract | Task 2 schema; H2 encoder and CLI acceptance |

Types and field names in H2 must match this plan. H1 task 1 owns calculation semantics; task 2 owns only the adapter and schema reference. The complete model remains independently testable before renderers exist. Planning checks establish no passing implementation tests; execution evidence is recorded only after implementation.

Draft self-review on 11 September 2026 checked spec coverage, placeholder text,
shared types, task dependencies, null semantics and overflow behaviour against
the current source at `801a917`. All plan links resolve and code fences balance.
Baseline `go test ./...` and `go build ./...` pass on the unchanged application;
these results do not validate the proposed comparison implementation.
