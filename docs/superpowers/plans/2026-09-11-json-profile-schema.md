# JSON Profile Schema and Encoder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Export complete, reproducible profile evidence as versioned JSON without coupling the wire format to internal model types.

**Architecture:** Project F's complete `profile.Report` into private, explicitly tagged JSON structs. Marshal the complete document before writing. Add a non-triggering reconstruction-status snapshot to `Report`; retain existing model calculations and source identities. G2 adds the CLI selection.

**Tech Stack:** Existing Go toolchain, standard `encoding/json`, `testing`, `io`, `time` and `unicode/utf8`; no new dependencies.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), item 7 profile schema, shared evidence/disclosure rules and Boundary G. Requires completed [Boundary F](2026-09-10-profile-data-analysis.md). Followed by [G2 CLI integration](2026-09-11-json-profile-cli.md).

## Global Constraints

- “UI-hook duration measures a resource operation. RPC duration measures a provider call.”
- “An inferred address always retains its confidence.”
- “Machine output uses valid JSON encoding and leaves identifier values intact.”
- “Unavailable per-observation offsets and timeline metrics are null, never zero placeholders.”
- “Observation arrays contain admitted durations; rejected timing records contribute to quality counters only.”
- “Do not include current generation timestamps or absolute input paths by default.”
- “JSON contains structured timing and source references, not raw log bodies or credential fields.”
- Complete arrays and aggregates; no limit, filtering, reconstruction trigger, JSON import, comparison or compatibility adapter.
- TDD with real parsing/model behaviour; independent review and separate test cleanup. Use existing tools and sanitised fixtures.
- Use `/Users/dan/.codex/bin/codex-git`; every commit must be signed. Signing failure stops work. No push or merge implied.

---

## Status and proposed approach

Dan authorised drafting G1/G2 on 11 September 2026, after F merged locally at `529a6fd`. These plans propose exact contracts for approval; application implementation is not yet authorised.

Use dedicated wire structs. Directly marshalling `Report` would expose Go names, enum ordinals, cached selection details and invalid zero offsets. A generic export framework or second parser would add unnecessary machinery. A small explicit projection keeps F's calculations authoritative and makes schema changes reviewable.

The schema below is the complete v1 field contract. No field uses `omitempty`. All arrays encode as `[]` when empty; all reason maps encode as `{}`. Nullable fields are pointers, not numeric sentinels. Strings are original UTF-8 identifiers, not `DisplayText` output. Reject invalid UTF-8 in any exported string with a fixed error (no raw value in diagnostics), rather than silently replacing bytes. This proposed error behaviour preserves the spec's identifier contract; it does not affect text rendering.

## Wire contract

All object fields below are mandatory, including null-valued fields. Names are literal snake_case. Counts and duration/offset/byte/line values are JSON integers, retaining Go's uint64 precision; decoder tests use `UseNumber` or typed integers. Consumers must not assume IEEE-754 doubles preserve arbitrary integer precision. Fractions are numbers in [0,1], or null. Origins are UTC RFC3339Nano strings or null. Durations and offsets use milliseconds throughout.

### Root and shared objects

| Object | Exact fields and meaning |
| --- | --- |
| Root | `schema_version: 1`, `kind: "profile"`, `tool_version: string`, `input: Input`, `duration_unit: "ms"`, `tiers: {rpc: Tier, ui: Tier}`, `quality: Quality`, `rpc_observations: RPC[]`, `ui_observations: UI[]`, `aggregates: Aggregates`, `timeline: Timeline`, `qualifications: string[]` |
| Input | `basename: string`, `bytes: uint64`. CLI supplies `filepath.Base(path)`; no absolute path. |
| Source | `entry: uint32` (zero-based logical entry), `start_line`, `end_line` (uint64, one-based inclusive), `start_byte`, `end_byte` (uint64, half-open). Source object is null when unavailable. |
| Total | `count: uint64`, `total_ms: uint64`, `max_ms: uint32`, `lower_bound: bool`. Lower bound qualifies the total and potentially the maximum. |
| Position | `start_ms: uint32|null`, `end_ms: uint32|null`, `valid: bool`, `reasons: string[]`, `start_clamped: bool`. Both offsets null unless `Span.HasPosition()`; valid zero stays 0. Reasons come from `Span.PositionReasons()` in its existing order. |
| Tier | `duration_available: bool` (`Admitted > 0`, including zero durations), `records`, `admitted`, `rejected`, `positioned`, `excluded` (uint64), `duration_ms`, `positioned_ms`, `excluded_ms` (uint64), `duration_lower_bound: bool`, `clock_origin: string|null`, `exclusions: object<string,uint64>`. Excluded = admitted − positioned; reason counts may overlap and must not be summed to obtain excluded. Tier objects always exist, including absent tiers. |

### Observations

`RPC` fields: `index: int`, `entry: uint32`, `source: Source|null`, `method: string`, `provider: string`, `resource_type: string`, `duration_ms: uint32`, `position: Position`, `attribution: Attribution`.

`UI` fields: `index: int`, `entry: uint32`, `source: Source|null`, `address: string`, `action: string`, `resource_type: string`, `duration_ms: uint32`, `duration_lower_bound: bool`, `position: Position`.

`index` is the original zero-based index within its tier array, not a ranking index or request ID. Observations remain in original input order (`Report.RPC`/`.UI`), preserving F's mapping. Do not export `ReqID`, raw bodies, arbitrary entry fields or interned component identifiers. Missing observed strings remain empty; do not replace them with display labels.

`Attribution` fields: `confidence: string`, `address: string|null`, `candidates: uint32`. Confidence is `no_context` when `!Report.HasContext`; otherwise one of `unattributed`, `ambiguous`, `overlapping`, `likely`, `contained`. Only the last three can carry a non-null address, and only when the model supplied a nonempty address. Candidate count is copied for contextual observations; no-context exports 0. Never manufacture an address or convert UI identity to RPC attribution.

### Quality and qualifications

`Quality` fields:

- `scope: "whole_log"`, `provider_entries: uint64`, `structured_lines: uint64`, `has_address_context: bool`.
- `issues: Issue[]`, each `{stage: string, code: string, count: uint64, first_entry: uint32|null}` in F's stage/code order. Preserve null first-entry versus entry 0.
- `attribution: AttributionQuality|null`; null when no context. Otherwise fields are `spans: int`, `duration_ms: uint64`, `by_confidence: ConfidenceTotal[]`, `candidate_counts: CandidateCount[]`. Emit all five confidence rows in fixed order `unattributed, ambiguous, overlapping, likely, contained`; each `{confidence: string, count: int, duration_ms: uint64}`. Candidate rows `{candidates: uint32, count: int}` sort numerically ascending, copying the existing histogram without reinterpretation.
- `nameable_ms: uint64`, `rpc_duration_ms: uint64`, `nameable_share: number|null`, directly from `Report.Quality` (do not recompute or turn unavailable into zero).
- `reconstruction: {state: string, responses: int|null, code: string|null}`. Snapshot `Log.ReconstructionQuality()`: `not_checked` gives null responses/code, `checked` gives measured response count and null code, `failed` gives null responses and its fixed code. Export does not initiate reconstruction.

Root `qualifications` is the following fixed ordered array of reason codes:

```json
["unmasked_identifiers","logging_affects_durations","rpc_and_ui_measure_different_work","ui_duration_rounding","observed_gaps_do_not_prove_idleness","active_observation_does_not_prove_blocking"]
```

Document each code's meaning in `docs/profile-json-v1.md`; they qualify interpretation, not measured issue counts. UI rounding is up to one second either way per observation. Per-observation saturation, clamping and positioning reasons remain separately encoded, not hidden in this list.

### Aggregates

`Aggregates` fields:

- `providers: Provider[]`: `{provider: string, rpc: Total}` from `Report.Providers`; lower_bound false. Keep F's duration-descending/key-ascending order and `(none)` aggregate key normalisation.
- `resource_types: ResourceType[]`: `{resource_type: string, rpc: Total, ui: Total}` from `Report.Types`, preserving its existing order. UI count maps `UIResources` (operation count, not distinct addresses), max maps `UIMaxMs`, lower_bound maps `UILowerBound`. RPC count/max map `RPCCalls`/`RPCMaxMs`, lower_bound false. Do not add the two totals together.
- `resources: Resource[]`: `{address: string, ui: Total, named_rpc: Total, overlapping_rpc: Total, ui_observation_indices: int[]}` from `Report.Resources.Rows` and each operation's `UIIndex`, preserving row/operation order. Named RPC means contained plus likely; overlapping remains separate. These are observed UI resource rows, not an invented ranking of inferred RPC addresses.
- `ui: Total`, `unnamed_ui: Total` from the resource projection.
- `rpc_evidence: {baseline: Total, missing_type: Total, no_context: Total, contained: Total, likely: Total, overlapping: Total, ambiguous: Total, unattributed: Total}` from `ResourceProjection.Evidence`. These are the model's disjoint accounting buckets. They can differ from quality's confidence-only distribution because missing type/no-context take priority.

Do not export F's inactive filter/selection state, internal module parser fields or duplicate cached indices. Every provider/type/resource row survives export, regardless of text limits.

### Timeline

`Timeline` always exists. Fields: `tier: "rpc"|"ui"|null`, `status: "complete"|"partial"|"unavailable"`, `clock_origin: string|null`, `window_scope: "zero_to_latest_positioned_end"`, `admitted: uint64`, `positioned: uint64`, `excluded: uint64`, `admitted_ms: uint64`, `admitted_lower_bound: bool`, `positioned_ms: uint64`, `excluded_ms: uint64`, `excluded_lower_bound: bool`, `exclusions: object<string,uint64>`, `metrics: Metrics|null`, `threshold_ms: uint32|null`, `intervals: Interval[]`.

Use F's chosen tier and `TimingAnalysis`; never fall back from unavailable RPC positions to UI. No tier: null tier/origin/metrics/threshold, unavailable status, zero counts/totals, false lower bounds, empty reasons/intervals. Chosen tier with nil metrics is also unavailable, but retains its admitted/excluded evidence and matching origin. With metrics, status is partial iff excluded > 0, otherwise complete. Zero-window metrics remain a non-null object.

`Metrics` fields: `window_ms: uint32`, `peak: int`, `busy_ms: uint32`, `busy_fraction: number|null`, `summed_duration_ms: uint64`, `summed_window_ratio: number|null`. Copy F's values; sum is `Timing.PositionedMs`; ratio is null for zero window, otherwise sum/window (it can exceed 1).

`Interval` fields: `start_ms`, `end_ms`, `duration_ms` (uint32), `min_running`, `max_running`, `observed_peak` (int), `active_observation_index: int|null`. Preserve F's chronological order, including all intervals (no text extent-ranking or limit). For `Blocking == -1` use null; otherwise resolve `PositionedIndices[Blocking]` into the chosen original tier array. Validate both bounds before producing a document. This reference identifies the longest observed active observation in the interval, not a causal blocker or a request identifier. Offsets use the selected tier origin.

## File responsibilities and interfaces

| File | Responsibility |
| --- | --- |
| Modify `internal/profile/data.go`, `data_test.go` | Non-triggering reconstruction snapshot in Report |
| Create `internal/profile/json_data.go`, `json_data_test.go` | Private wire types, explicit complete projection and mapping validation |
| Create `internal/profile/json.go`, `json_test.go` | Public metadata/renderer, complete marshal then single write |
| Create `internal/profile/json_benchmark_test.go` | Reproducible projection/allocation measurement on generated sanitised data |
| Create `docs/profile-json-v1.md` | Full v1 keys, null/units/order/reference/disclosure semantics, runnable example |

Public API supplied to G2:

```go
type JSONMetadata struct {
    ToolVersion string
    InputBasename string
}
func RenderJSON(w io.Writer, report Report, metadata JSONMetadata) error
```

Private API between these tasks: `func buildJSONProfile(report Report, metadata JSONMetadata) (jsonProfile, error)`. Root `jsonProfile` has tagged fields exactly as the tables above. Define `JSONMetadata` in task 1 so its API exists when that task compiles; task 2 adds `RenderJSON`. Wire structs are private, do not embed model structs, and do not attach JSON tags to F's domain types.

## Task 1: Project the complete report into the v1 schema

**Files:** `data.go`, `data_test.go`, new `json_data.go`, `json_data_test.go`, `json.go` (metadata only), `json_benchmark_test.go`, `docs/profile-json-v1.md`.

**Interfaces:** Consume `Build(*model.Log) (Report,error)` and the F report types. Add `Reconstruction model.ReconstructionQuality` to Report, populated by the non-triggering accessor. Produce `JSONMetadata` and `buildJSONProfile` with the complete schema above.

- [ ] **Step 1: Add behaviour tests.** Start with real `resources-accounting.log`, `two-tier.log`, `resources-long-lower-bound.log`, `core-only.log` and the association-confidence fixture. A representative source assertion must use independently counted lines:

```go
func TestJSONProjectionRetainsPhysicalSources(t *testing.T) {
    l, err := model.Load("../../testdata/resources-accounting.log")
    if err != nil { t.Fatal(err) }
    report, err := Build(l)
    if err != nil { t.Fatal(err) }
    doc, err := buildJSONProfile(report, JSONMetadata{ToolVersion: "test", InputBasename: "run.log"})
    if err != nil { t.Fatal(err) }
    if len(doc.RPCObservations) != len(report.RPC) { t.Fatal("lost observations") }
    found := false
    for _, row := range doc.RPCObservations {
        if row.Source != nil && row.Source.StartLine == 4 {
            found = true
            if row.Attribution.Address == nil || *row.Attribution.Address != "aws_instance.a" || row.Attribution.Confidence != "contained" {
                t.Fatalf("wrong source attribution: %+v", row)
            }
        }
    }
    if !found { t.Fatal("missing physical line 4") }
}
```

Cover >20 observations/providers/types/resources without truncation, original array order, equal-duration identities, zero-valued durations/offsets, missing source, uint64 totals/offsets above 2^53 in synthetic Report values, all confidence states, candidate histogram numerical ordering, separate UI/RPC aggregate totals, unnamed UI, saturation and detached ownership. Use synthetic Report values only for states parser fixtures cannot conveniently represent; do not mock model functions.

Exercise no tier, UI only, differing origins, preferred RPC with unusable positions, partial positions, zero window, clamping, all intervals, and an excluded observation preceding the referenced active observation. Verify an active index identifies the original RPC/UI array element, not the compact positioned slot. Invalid mapping returns `errors.New("profile interval observation index out of range")`; no negative mapping is indexed. Reuse existing `intervalObservation` for nonnegative indices; accept only -1 as the null gap sentinel and reject other negative indices. Verify reconstruction remains `not_checked` after Build/projection on a fresh loaded log; separately map checked-zero and failed snapshots. Invalid UTF-8 in metadata and an identifier must return a fixed `profile JSON contains invalid UTF-8` error, without echoing the value.

- [ ] **Step 2: Run RED.** `go test ./internal/profile -run 'TestJSONProjection|TestBuild.*Reconstruction' -count=1`. Initial missing API compilation establishes scaffolding only; after minimal empty projection exists, record failing value/null/source/complete-count assertions before implementing mappings.

- [ ] **Step 3: Implement private wire projection.** Define each schema object with explicit tags and pointer nulls, for example:

```go
type jsonPosition struct {
    StartMs *uint32 `json:"start_ms"`
    EndMs *uint32 `json:"end_ms"`
    Valid bool `json:"valid"`
    Reasons []string `json:"reasons"`
    StartClamped bool `json:"start_clamped"`
}
func positionJSON(s span.Span) jsonPosition {
    p := jsonPosition{Valid: s.HasPosition(), Reasons: append([]string{}, s.PositionReasons()...), StartClamped: s.StartClamped}
    if p.Valid { start, end := s.StartMs, s.EndMs; p.StartMs, p.EndMs = &start, &end }
    return p
}
```

Copy fields using the mapping tables; initialise output slices/maps even when empty. Copy sources rather than sharing pointers. Format copied origin timestamps with UTC/RFC3339Nano. Map confidence enums by explicit cases, never numeric casts. Use small helpers for Total, Tier, Source and Position; do not recalculate rollups or correlations. Validate exported untrusted strings with `utf8.ValidString` as they enter wire fields, returning the fixed error above. Sort only copied histogram data; never sort Report slices in place. Map `Report.Reconstruction` exactly as specified; unexpected states return `errors.New("profile JSON has invalid reconstruction state")` rather than claim success. Unknown confidence enums return `errors.New("profile JSON has invalid attribution confidence")`; an unsupported non-null timeline tier returns `errors.New("profile JSON has invalid timing tier")`. Loaded reports cannot produce those states, but synthetic test reports must not cause invented evidence.

- [ ] **Step 4: Verify, document and measure.** Run profile/model tests and full suite. Write the schema reference with every field above and a decoder example preserving integers; specify JSON spelling/order conventions and no compatibility promise. Add `BenchmarkJSONProjection` with `b.ReportAllocs`, a fixed generated sanitised capture outside the timed loop, and `b.ResetTimer` before projection iterations. Use 1,000 and 10,000 observations; record `go test ./internal/profile -run '^$' -bench BenchmarkJSONProjection -benchmem -count=1`. Do not set arbitrary timing thresholds or add parallel loading. Independent review checks the full wire contract; separate cleanup checks tests.
- [ ] **Step 5: Commit.** Signed commit `Define complete JSON profile schema`; record actual RED/GREEN, benchmark and review evidence here. This task leaves an internally tested projection, with no new CLI option.

## Task 2: Encode reproducible complete JSON and propagate failures

**Files:** `internal/profile/json.go`, new `json_test.go`, schema reference examples.

**Interfaces:** Consume `buildJSONProfile(Report,JSONMetadata) (jsonProfile,error)`. Produce `RenderJSON(io.Writer,Report,JSONMetadata) error` for G2. Existing package `writeText` owns single-write/short-write checks.

- [ ] **Step 1: Add failing renderer tests.** Encode a real loaded fixture twice and require byte equality. Decode once into explicitly tagged test structs or `map[string]json.RawMessage`, then require EOF on a second decode. Assert literal root values/complete key sets, explicit null vs zero, `[]` vs null and numeric values from independent fixture expectations, not only comparisons to the same projection helper. Representative call:

```go
var first, second bytes.Buffer
metadata := JSONMetadata{ToolVersion: "test", InputBasename: "run.log"}
if err := RenderJSON(&first, report, metadata); err != nil { t.Fatal(err) }
if err := RenderJSON(&second, report, metadata); err != nil { t.Fatal(err) }
if !bytes.Equal(first.Bytes(), second.Bytes()) { t.Fatal("non-deterministic JSON") }
if !json.Valid(first.Bytes()) { t.Fatal("invalid JSON document") }
```

Verify decoded newline/ESC/quotes/non-ASCII identifiers exactly match the original values, no `DisplayText` double escaping, no raw-body/ReqID/absolute-path fields, all arrays exceed 20 when appropriate, no mutation of Report slices, and text/JSON numerical parity on RPC/UI mixed and saturated fixtures. Exact output ends with one newline. Use existing failing/short writers: errors unchanged, short nil-error write becomes `io.ErrShortWrite`. Invalid mapping, invalid UTF-8 and non-finite synthetic fractions must fail with an untouched output buffer; encoder errors must not include input values. A failed writer may accept partial bytes, but the function must return failure, never claim atomic filesystem output.

- [ ] **Step 2: Run RED.** `go test ./internal/profile -run 'TestJSON' -count=1`; record behavioural failures once the signature compiles.
- [ ] **Step 3: Marshal before writing.** Follow this entry flow:

```go
func RenderJSON(w io.Writer, report Report, metadata JSONMetadata) error {
    doc, err := buildJSONProfile(report, metadata)
    if err != nil { return err }
    data, err := json.MarshalIndent(doc, "", "  ")
    if err != nil { return errors.New("encoding profile JSON failed") }
    return writeText(w, string(data)+"\n")
}
```

Standard JSON escaping is allowed; decoding must preserve identifiers. No warning banner/prose precedes or follows the document. Privacy and interpretation are encoded as qualifications and documented. Do not stream arrays before the whole document can be encoded, or add generation time. The public renderer accepts no limit.

- [ ] **Step 4: Verify and review.** Run profile tests, full tests/build and the checks below. Independent reviewer inspects schema coverage, nullability, source references, determinism, write errors and privacy; separate test-cleanup pass preserves boundary coverage.
- [ ] **Step 5: Commit.** Signed commit `Encode reproducible JSON profile reports`; record actual evidence before G2 starts.

## Final validation and G2 handoff

- [ ] `go test -race -count=1 ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, `gofmt -d .`, `go mod tidy -diff`, `go mod verify`.
- [ ] Decode examples with the standard library, verify deterministic bytes, complete schema keys and text parity; inspect no-duration and saturated documents.
- [ ] Independent review and separate cleanup complete; signed commits and clean worktree. Record benchmark results without claiming remote CI ran.
- [ ] G1 provides only the encoder/schema. G2 still owns mode selection, filename/version metadata, output protection tests and README usage.

## Planning self-review

| Requirement | Planned coverage |
| --- | --- |
| Version, basename, unit and tool identity | G1 root contract/projection; G2 dispatch metadata |
| Complete observations and aggregates | G1 task 1 complete-array, ordering and disjoint-accounting tests |
| Missing vs zero, lower bounds and independent clocks | G1 Position/Tier/Timeline rules and boundary tests |
| Source lines/bytes and capture-local identity | G1 Source and index mapping; G2 fixture assertions |
| Quality, attribution and non-triggering reconstruction | G1 Report snapshot and quality projection |
| Deterministic machine output and unmodified identifiers | G1 task 2 byte equality, decoder and UTF-8 tests |
| Render failure and partial writer failure | G1 task 2 encode-before-write and writer errors |
| Early flags/output protection | G2 tasks 1 and 2 |

The plan uses the inspected fields of `Report`, `CaptureQuality`,
`ResourceProjection`, `Attribution` and `ReconstructionQuality`. The sole domain
addition is the reconstruction snapshot; its accessor already exists. G1 and G2
use the same metadata/renderer signatures. No test execution is claimed for
these documentation-only plans. Exact schema and UTF-8 failure behaviour remain
proposed for Dan's approval, with no backward-compatibility commitment.
