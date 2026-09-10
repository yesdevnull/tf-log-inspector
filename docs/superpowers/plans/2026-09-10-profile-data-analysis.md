# Profile Data and Interval Analysis Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Build complete, presentation-independent profile data and reuse the timeline's calculation policies for report analysis.

**Architecture:** Keep temporal calculations in `internal/model` and report assembly in `internal/profile`. Reuse admitted observations, source locations, attribution and resource projections; never reparse a report or copy raw log bodies. F1 supplies F2's concrete data contract without adding JSON or comparison commands.

**Tech Stack:** Existing Go 1.25+ toolchain, standard testing package, current dependencies only.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), shared rules, item 5 and the common data portion of item 7. F2: [actionable text profiles](2026-09-10-actionable-text-profiles.md).

## Global Constraints

- “UI-hook duration measures a resource operation. RPC duration measures a provider call.”
- “Source bytes and scanner entry identities remain authoritative.”
- “An inferred address always retains its confidence.”
- “Whole-log quality facts remain labelled as whole-log facts.”
- “Keep presentation width, formatted duration strings and row truncation outside the calculation model.”
- Preserve existing dependencies, tier preference, zero-to-latest-end window and `max(window/20, 1000)` millisecond interval threshold.
- Use `/Users/dan/.codex/bin/codex-git`; every commit must be signed. Signing failure stops work. No push or merge is implied.
- TDD, independent task review and separate test-cleanup subagent after implementation; real model/parsing behaviour, no mocks.

---

## Status and scope

Dan authorised starting Boundary F and its two-plan split on 10 September 2026, after Boundary E merged locally at `e21f366`, then approved both plans and subagent implementation. F1 completed its task reviews before F2 began. The execution record below records actual validation. Exact JSON field names, schema/version metadata, CLI comparison and response recovery remain boundaries G/H/I.

The selected approach adds a small concrete report value rather than putting report calculations in the text renderer or building a generic report framework. The former would duplicate work in JSON/comparison; the latter adds unused abstractions. `profile.Build` computes the complete data once. F2 owns display limits and escaping.

## Contract decisions

1. Input is one whole loaded log, with no new CLI filters. Resource projection uses empty base filter and nil address/module selections. Keep its separate observed UI, named RPC, overlapping and unresolved evidence; never rank resources by inferred RPC totals.
2. Preferred tier means RPC if any RPC durations were admitted, otherwise UI. An RPC tier with no usable positions remains unavailable even if UI positions exist. No admitted observations means no tier.
3. Temporal metrics are absent when no observations are positioned. With positions but a zero window, peak/busy are numerical zero and the busy fraction is absent. Some excluded positions make results partial. Preserve full admitted duration totals, exclusion reasons and saturation flags separately.
4. All qualifying intervals are retained, including leading observed gaps. Keep `Stalls`' current merge/noise policy and half-open intervals. Do not infer time after the latest positioned endpoint or manufacture intervals for a zero-capacity run. Interval selection and busy union are different measurements.
5. `Stall.Blocking` remains an index into the positioned slice and means the longest observed active span, not a dependency blocker. The report stores the positioned-to-original mapping so text and later JSON resolve the correct observation after exclusions.
6. RPC/UI observation arrays retain original input order. Separate ranking-index arrays sort by duration descending, then raw domain identity, source entry and original index. Never derive identity from escaped text or slice position after sorting.
7. Source is a nullable `model.SourceLocation`: unavailable location is not line zero. Observed source entry and original index remain available. Attribution is copied by original RPC index; absent capture context is distinct from Unattributed. Ambiguous observations never get an address.
8. Reuse existing provider/type ordering. Attach UI lower-bound qualification to each type row containing a saturated UI duration; do not rely only on the whole-log warning. Report resource aggregates retain `DurationTotal.LowerBound` already provided by the model.
9. The report contains values and newly owned slices/maps, not a retained `*Log`, raw bytes or a reconstruction result. Calling Build must not trigger response reconstruction. Strings remain exact until the text boundary escapes them.

## File responsibilities

| File | Responsibility |
| --- | --- |
| New `internal/model/timing_analysis.go` | Complete positioned analysis and common window/threshold policy |
| New `internal/model/timing_analysis_test.go` | Temporal arithmetic, unavailable/partial/zero cases and errors |
| `internal/tui/timeline.go`, `timeline_test.go` | Delegate window and threshold policy while preserving TUI output |
| New `internal/profile/data.go`, `data_test.go` | Report assembly, original identities, full aggregates and ordering |
| `internal/profile/profile.go` | Remains the existing renderer until F2 consumes the report |

## Task 1: Share complete temporal analysis and policy

**Files:** Create `internal/model/timing_analysis.go` and its tests; modify `internal/tui/timeline.go` and relevant timeline tests.

**Interfaces:** Consume `SelectTiming([]span.Span) TimingSelection`, `PeakConcurrency`, `BusyMs`, and `Stalls`. Produce these exact APIs:

```go
type TimingMetrics struct {
    WindowMs uint32
    Peak int
    BusyMs uint32
    BusyFraction *float64
}

type TimingAnalysis struct {
    Timing TimingSelection
    Metrics *TimingMetrics
    ThresholdMs uint32
    Intervals []Stall // Blocking indexes Timing.Positioned
}

func TimingWindowMs(positioned []span.Span) uint32
func IntervalThresholdMs(window uint32) uint32
func AnalyseTiming(timing TimingSelection) (TimingAnalysis, error)
```

- [x] **Step 1: Write the failing arithmetic regression.** Use package `model`, standard testing, and the existing span/logfmt package imports:

```go
func TestAnalyseTimingSeparatesBusyExtentFromDurations(t *testing.T) {
    spans := []span.Span{
        {StartMs: 0, EndMs: 4000, DurationMs: 6000, StartClamped: true, TimestampStatus: logfmt.TimestampValid},
        {StartMs: 2000, EndMs: 6000, DurationMs: 4000, TimestampStatus: logfmt.TimestampValid},
    }
    got, err := AnalyseTiming(SelectTiming(spans))
    if err != nil { t.Fatal(err) }
    if got.Metrics == nil || got.Metrics.WindowMs != 6000 ||
        got.Metrics.BusyMs != 6000 || got.Metrics.Peak != 2 ||
        got.Timing.AdmittedMs != 10000 {
        t.Fatalf("incorrect duration/extent analysis: %+v", got)
    }
    if got.Metrics.BusyFraction == nil || *got.Metrics.BusyFraction != 1 {
        t.Fatal("busy fraction must use union extent")
    }
}
```

Add independent cases for nil input; all-unpositioned spans; mixed positioned/unpositioned durations and reasons; UI saturation; a positioned zero at offset zero; zeros at positive offsets; simultaneous handovers; mixed fidelity returning `ErrMixedTimelines`; leading gaps and multiple equal-length intervals. Use `TimestampStatus: logfmt.TimestampMissing` for missing timestamps and `DurationSaturated: true` for saturated UI observations, with `Fidelity: span.FidelityUIReported` for the UI tier. A nil metrics pointer distinguishes unavailable from a measured zero. Add a greater-than-three-interval case to prove this API is not the TUI's top-three presentation.

- [x] **Step 2: Run RED.** `go test ./internal/model -run '^TestAnalyseTiming' -count=1`. An initially absent API is expected; after adding declarations, confirm the arithmetic assertions fail with an empty implementation before implementing calculations.

- [x] **Step 3: Implement using existing primitives.** The algorithm is:

```go
func TimingWindowMs(positioned []span.Span) uint32 {
    var end uint32
    for _, s := range positioned { end = max(end, s.EndMs) }
    return end
}

func IntervalThresholdMs(window uint32) uint32 { return max(window/20, 1000) }

func AnalyseTiming(timing TimingSelection) (TimingAnalysis, error) {
    out := TimingAnalysis{Timing: timing}
    if len(timing.Positioned) == 0 { return out, nil }
    peak, err := PeakConcurrency(timing.Positioned)
    if err != nil { return TimingAnalysis{}, err }
    busy, err := BusyMs(timing.Positioned)
    if err != nil { return TimingAnalysis{}, err }
    window := TimingWindowMs(timing.Positioned)
    out.ThresholdMs = IntervalThresholdMs(window)
    out.Intervals, err = Stalls(timing.Positioned, out.ThresholdMs)
    if err != nil { return TimingAnalysis{}, err }
    out.Metrics = &TimingMetrics{WindowMs: window, Peak: peak, BusyMs: busy}
    if window > 0 {
        fraction := float64(busy) / float64(window)
        out.Metrics.BusyFraction = &fraction
    }
    return out, nil
}
```

Keep intervals in their existing chronological order here; text ranking is F2. Replace the loop in the TUI's uncached wall-clock calculation with `model.TimingWindowMs(spans)` and the body of `stallThresholdMs` with `model.IntervalThresholdMs(wallClock)`. Preserve caching and selection. Existing TUI threshold/window tests must still pass; do not route per-frame rendering through a new full analysis or add duplicate sweeps.

- [x] **Step 4: Verify and review.** Run `go test ./internal/model ./internal/tui -count=1`. Existing timeline goldens should remain unchanged. Independent review checks clock separation, unavailable values, complete intervals, clamped/zero extents and unchanged TUI policy. Separate cleanup follows implementation; retain distinct boundary cases.
- [x] **Step 5: Commit.** Inspect status and whitespace, stage task paths explicitly, and make signed commit `Share complete timing analysis for profiles`.

## Task 2: Assemble complete profile data with original source identities

**Files:** Create `internal/profile/data.go`, `data_test.go`.

**Interfaces:** Consume task 1 and existing `CaptureQuality`, `SourceLocation`, `PreferredTiming`, `RollupBy`, `JoinByResourceType`, `BuildResourceIndex`, `SelectResources`. Produce:

```go
type Observation struct {
    Index int
    Span span.Span
    Source *model.SourceLocation
    Attribution attrib.Attribution // RPC only; UI address is observed
}

type TypeSummary struct {
    model.TypeRow
    UILowerBound bool
}

type Timeline struct {
    Tier *span.Fidelity // nil means no admitted tier
    Analysis model.TimingAnalysis
    PositionedIndices []int // index into the chosen original observation array
}

type Report struct {
    Bytes uint64
    Quality model.CaptureQuality
    HasContext bool
    RPC, UI []Observation
    RPCRanking, UIRanking []int
    Providers []model.Bucket
    Types []TypeSummary
    Resources model.ResourceProjection
    Timeline Timeline
}

func Build(l *model.Log) (Report, error)
```

`Build` accepts a non-nil loaded log. Return a clear error for nil rather than panic. It neither opens a file nor accepts formatting options. `Observation.Index` is original tier index, not a global cross-tier identity. F2 source text prints physical line ranges; G later chooses a wire schema rather than serialising this struct directly.

- [x] **Step 1: Add failing report tests.** Use the repository's real fixture and independent values:

```go
func TestBuildRetainsAllObservedOperations(t *testing.T) {
    l, err := model.Load("../../testdata/resources-modules.log")
    if err != nil { t.Fatal(err) }
    got, err := Build(l)
    if err != nil { t.Fatal(err) }
    if len(got.UI) != 2 || len(got.UIRanking) != 2 || len(got.RPC) != 0 {
        t.Fatalf("lost original observations: %+v", got)
    }
    if got.Timeline.Tier == nil || *got.Timeline.Tier != span.FidelityUIReported {
        t.Fatal("UI-only capture must select its UI tier")
    }
    for i, row := range got.UI {
        if row.Index != i || row.Source == nil || row.Source.StartLine == 0 {
            t.Fatalf("operation source identity missing: %+v", row)
        }
    }
}
```

Extend with: more than 20 observations; ties with different providers/types/addresses and duplicate source occurrences; all confidence states using the sanitised association fixture in `internal/tui/testdata`; long/control-bearing identifiers; missing source locations in deliberately assembled logs; RPC durations without positions alongside positioned UI; rejected-only logs; lower-bound UI/type/resource totals. Prove `Build` leaves input slices/order and reconstruction status unchanged. Add a leading unpositioned RPC before the active interval's chosen observation, and assert `PositionedIndices[interval.Blocking]` names its original source entry, not a shifted array index.

- [x] **Step 2: Run RED.** `go test ./internal/profile -run '^TestBuild' -count=1`; establish missing data/identity assertions fail before completing Build.

- [x] **Step 3: Assemble without new attribution rules.** Start from:

```go
report := Report{
    Bytes: l.Stats.Bytes, Quality: l.CaptureQuality(),
    HasContext: l.HasAddressContext(),
    Providers: model.RollupBy(l.RPCSpans, func(s span.Span) string { return s.Provider }),
    Resources: model.SelectResources(l, model.BuildResourceIndex(l), model.Filter{}, model.ResourceSelection{}),
}
```

For each tier build fresh observation/ranking slices. Copy each span; copy valid `SourceLocation` into a separate pointer. For RPC observations use `l.Attribs[i]` when present, otherwise the zero attribution, with `HasContext` retaining the capture distinction. Do not call the entry lookup in a loop: that would repeatedly scan RPC spans. Do not fabricate an address for Ambiguous/Unattributed; loaded-log attributions already enforce that invariant.

Build type rows from `JoinByResourceType`, attach `UILowerBound` by a single pass over UI spans keyed with `model.FacetKey`. Sort ranking indices, never the observation arrays: duration descending; RPC identity `(Provider, ResourceType, RPC, Attribution.Address)` or UI identity `(Address, RPC, ResourceType)` ascending; then source entry ascending; finally original index ascending. All comparisons use raw values.

Use `PreferredTiming(l)` once. For the chosen span slice call `SelectTiming` then `AnalyseTiming`; store its fidelity and append each positioned span's original index to `PositionedIndices` in input order. Do not switch tiers when analysis is unavailable. Preserve temporal errors rather than producing a zero summary. Keep `Report` complete: no `20`, no title, no escaped strings, no truncation or generation timestamps.

- [x] **Step 4: Verify and review.** Run `go test ./internal/model ./internal/profile ./internal/tui -count=1`, then full suite/build. Reviewer checks report completeness, deterministic ties, original source/attribution alignment, lower bounds, complete resource evidence and no raw-body/reconstruction side effect. Separate cleanup must retain behaviour coverage.
- [x] **Step 5: Commit.** Stage only task files and signed commit `Build complete profile data from observed evidence`. Record actual validation here before F2 starts.

## Final validation and handoff

- [x] `go test -race -count=1 ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, `gofmt -d .`, `go mod tidy -diff`, `go mod verify`.
- [x] Verify existing timeline goldens unchanged and compare TUI/profile temporal values on sanitised RPC and UI fixtures. No TUI behaviour change is intended; inspect a real terminal if output changes.
- [x] Independent review and separate test cleanup complete; all findings resolved before F2.
- [x] Signed commits, clean status, whitespace checks; record actual evidence without marking F2/G/H implemented.

## Planning self-review

The two tasks cover complete temporal analysis and report construction. Item 5's user-visible rendering and limit are explicitly assigned to F2. G owns JSON metadata/encoding, and H owns comparisons. The report retains full observations and original-index mappings; only the text layer may limit lists. No production changes or test results are claimed by this plan.

Independent plan review on 10 September 2026 checked the interfaces against
the repository and identified an explicit timestamp-validity precondition in
the sample arithmetic test and missing text clock-origin instructions in F2.
Both are corrected; scoped re-review found no remaining issues. Links, code
fences and whitespace checks pass. Application tests were not run for these
documentation-only changes.

## Execution record

Dan approved both plans and subagent execution on 10 September 2026. F1 task 1 completed in signed commit `1b5af25`: behavioural RED, focused GREEN and full suite pass. Independent task review approved specification and quality with no findings. Separate test cleanup retained all eight tests/ten cases; model coverage 98.1%. Task 2 completed in signed commit `1400957`; independent specification/quality review approved with no findings, and separate cleanup retained all six tests (profile coverage 93.4%). Full race tests, build, lint, formatting and module checks pass. Existing TUI goldens are unchanged.

F2 subsequently completed, including real RPC/UI output checks and the existing
TUI/profile admitted-versus-positioned parity regression. Final combined review
of `e21f366..112fb6b` approved both plans with no actionable findings. Race tests
passed for all eleven packages at `112fb6b`; lint and formatting also passed.
The F2 execution record contains the successful local four-target build matrix,
module checks and manual output evidence. Remote CI was not run. No findings or
controller rulings remain; Boundary F is ready for Dan's integration decision.
