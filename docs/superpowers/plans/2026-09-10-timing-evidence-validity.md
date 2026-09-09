# Timing Evidence Validity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admit only explicit valid durations, retain usable durations independently of their timeline positions, and prevent unavailable positions from producing fabricated timeline or attribution results.

**Architecture:** Keep the scanner's entry identities and compact index intact. Deliver clock validity through an optional scanner sink callback; both span builders retain per-observation validity and stage-specific rejection facts. A model projection separates positioned observations from admitted durations for existing timeline, profile and attribution consumers.

**Tech Stack:** Go 1.25+, standard testing, existing Bubble Tea/Lip Gloss and standard-library JSON. No new dependency.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), shared duration admission/position validity and boundary C. This is C1; [Capture quality presentation](2026-09-10-capture-quality-presentation.md) is C2 and depends on this plan. Dan approved the two-plan split on 10 September 2026. These plans propose implementation; no application changes have been made.

## Global Constraints

- “Duration evidence and timeline position are independent properties of each observation.”
- “Source bytes and scanner entry identities remain authoritative.”
- “Missing or null elapsed_seconds is not zero.”
- “RPC durations require the existing unsigned integer millisecond representation; values outside its storage range are rejected and counted, not silently capped.”
- “Unpositioned RPC observations receive no temporal attribution.”
- “They remain in the RPC duration denominator.”
- “Do not silently switch tiers because the preferred tier lacks usable positions.”
- “Diagnostics use fixed reason codes, counts and locations; they do not embed source text or JSON parser snippets.”
- Preserve the streaming diagnose path, the 24-byte pointer-free Entry, and separate UI/RPC clocks. No new parser recovery, JSON export, Resources view or new interval-analysis feature.
- Follow TDD, independent task/whole-branch review and separate test cleanup. Use sanitised fixtures, British/Australian prose and the existing toolchain.
- All Git operations use `/Users/dan/.codex/bin/codex-git`. Sign every commit, preserve hooks and stop immediately if signing fails. Do not push or merge without Dan's instruction.

---

## Starting evidence and scope

Planning starts from clean local main at `6c3ae73` on `wip/capture-evidence`.
`pull --rebase origin main` was up to date; `go test ./...` passed.

Verified current behaviours:

- `UIHookBuilder` decodes elapsed seconds into a float64: absent, null and negative values can become admitted zero durations. Missing resource objects prevent admission even when duration exists.
- UI timestamp failures become zero offsets; before-origin timestamps clamp to zero; oversized offsets clamp to MaxUint32. Saturated durations have only a capture-level counter.
- `Scan` aborts on oversized hclog offsets and loses before-origin validity after clamping. `Entry` has an explicit 24-byte size regression.
- `ReportedBuilder` silently skips missing/invalid durations. `Caps.BestFidelity()` reflects markers, not successful duration admission.
- Temporal attribution and timeline consumers currently treat all stored offsets as usable. Profile concurrency derives its denominator/window directly from all RPC spans.

**Recognition boundary:** an RPC timing record remains a scanner-recognised header whose message begins with `Received downstream response`. A missing/invalid outer hclog timestamp currently makes a physical line continuation text, not a new RPC record. Do not promote such text, parse arbitrary response-marker substrings, or split entries here. The admission contract applies to recognised records; UI envelopes can be recognised independently of their timestamp. Report this detection limitation honestly in C2. If broader header recovery is wanted, it needs its own design and parser tests.

**Staging:** each task must leave a runnable application. Adding new evidence types does not immediately switch all consumers. Switch temporal consumers in Task 4 after both builders produce explicit status; include necessary synthetic-fixture migrations in that task. Task 3 switches attribution and its fixtures together. Do not introduce a compatibility mode that treats unknown status as valid.

## File responsibilities

| Files | Responsibility |
| --- | --- |
| New `internal/logfmt/clock.go`, `clock_test.go`; existing `scan.go`, `scan_test.go` | Clock classification and optional sink delivery without growing Entry |
| New `internal/span/evidence.go`, `evidence_test.go`; `span.go`, `reported.go`, `uihook.go` and their tests | Per-observation status, explicit duration admission and builder facts |
| New `internal/model/timing.go`, `timing_test.go`; `log.go`, `lanes.go` and tests | Retained evidence/origins and positioned-subset projection |
| `internal/attrib/correlate.go`, `correlate_test.go` | Skip temporal inference for unavailable positions |
| `internal/tui/timeline.go`, `model.go`, `views.go`, `layout.go` and relevant tests | Positioned timeline with unavailable/partial/empty distinctions and honest detail |
| `internal/profile/profile.go`, `profile_test.go`; `internal/diagnose/diagnose.go`, `diagnose_test.go`; `cmd/tfli/main.go`, `main_test.go` | Existing report consumers honour admission and positional eligibility |

### Task 1: Preserve clock validity without changing entry identity

**Files:** Create `internal/logfmt/clock.go`, `clock_test.go`; modify `scan.go`, `scan_test.go` and Stats in `entry.go`. Keep Entry's layout unchanged; add one Stats counter for out-of-range timestamp offsets.

**Interfaces produced:**

```go
type TimestampStatus uint8
const (
    TimestampMissing TimestampStatus = iota
    TimestampValid
    TimestampInvalid
    TimestampBeforeOrigin
    TimestampOutOfRange
)
type ClockPosition struct {
    OffsetMs uint32
    Status TimestampStatus
}
type ClockSink interface {
    EntryClock(ord uint32, position ClockPosition)
}
func RelativePosition(at, origin time.Time) ClockPosition
```

`RelativePosition` takes parsed nonzero timestamps. Compare `at.Before(origin)` before rounding; an instant even one nanosecond before origin is unavailable. For nonnegative differences use milliseconds, preserving sub-millisecond rounding to zero. MaxUint32 milliseconds is valid; the next millisecond is unavailable. A zero `at` or origin returns TimestampMissing. Status-to-code rendering in Task 2 owns text; this layer stores no captured strings.

```go
func RelativePosition(at, origin time.Time) ClockPosition {
    if at.IsZero() || origin.IsZero() { return ClockPosition{Status: TimestampMissing} }
    if at.Before(origin) { return ClockPosition{Status: TimestampBeforeOrigin} }
    ms := at.Sub(origin).Milliseconds()
    if ms > math.MaxUint32 { return ClockPosition{Status: TimestampOutOfRange} }
    return ClockPosition{OffsetMs: uint32(ms), Status: TimestampValid}
}
```

- [x] **Step 1: Add a real scanner regression before production edits.** Extend the existing offset-boundary tests: a normal first header and a header at MaxUint32+1 must both be emitted, with no scan error. Keep the exact-boundary case and add a third normal header to prove scanning continues.

```go
func TestScanRetainsEntriesBeyondClockRange(t *testing.T) {
    base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
    later := base.Add((time.Duration(math.MaxUint32) + 1) * time.Millisecond)
    text := base.Format(tsLayout) + " [DEBUG] core: first\n" +
        later.Format(tsLayout) + " [DEBUG] core: later\n"
    var comps, ids Interner
    st, err := Scan(strings.NewReader(text), &comps, &ids)
    if err != nil || st.Entries != 2 {
        t.Fatalf("entries=%d, err=%v; want both entries retained", st.Entries, err)
    }
}
```

- [x] **Step 2: Run `go test ./internal/logfmt -run TestScanRetainsEntriesBeyondClockRange -count=1`.** Record the current offset-error RED. Then add a recording sink exercising the real callback, not mocked behaviour: every Entry call must have a matching preceding EntryClock ordinal; structured and untimestamped entries report TimestampMissing.
- [x] **Step 3: Implement the classifier and scanner callback.** Keep one `curClock ClockPosition` beside `cur`. Initialise it on every new entry, including structured and untimestamped branches. In `flush`, call the optional clock callback immediately before the same sink's Entry callback. Do not call it once per continuation.

```go
position := RelativePosition(h.TS, baseTS)
curClock = position
// TSms remains bounded storage; only TimestampValid authorises timing use.
delta := position.OffsetMs
if position.Status == TimestampBeforeOrigin { st.BackwardsTimestamps++ }
if position.Status == TimestampOutOfRange { st.TimestampOffsetsOutOfRange++ }
// Within flush's existing sink loop, before s.Entry(...):
if cs, ok := s.(ClockSink); ok { cs.EntryClock(ord, curClock) }
```

Unavailable `OffsetMs` is zero; it is a sentinel guarded by status, not a measured endpoint. Preserve FirstTS/LastTS capture timestamps and bytes/ordinals, including the original source timestamp even when its relative offset is unusable. Existing I/O, byte-size and structural scan failures remain errors. Replace the former oversized-offset error expectation with status and entry-retention assertions; retain the rationale and boundary coverage.

- [x] **Step 4: Verify clock delivery, monotonic-origin policy and index invariants.** Cases: missing, genuine zero, before-origin by 1ns, MaxUint32, MaxUint32+1, very distant dates, structured lines between headers and multiple continuations. `go test ./internal/logfmt -count=1` and `go test ./...` must pass, including `TestEntryStaysTwentyFourBytes`.
- [x] **Step 5: Self-review, signed commit and independent task review.** Commit exact changed files with subject `Preserve per-entry clock validity during scanning`.

### Task 2: Admit explicit durations and retain observation evidence

**Files:** Create `internal/span/evidence.go`, `evidence_test.go`; modify `span.go`, `reported.go`, `uihook.go` and tests. Modify `model/log.go` to retain builder evidence/origin and the existing UI saturation count. Leave position-dependent consumers for Tasks 3–4.

**Consumes:** Task 1's ClockSink, ClockPosition and TimestampStatus.

**Produces:**

```go
// Add to Span. No validity is inferred from offset zero.
TimestampStatus logfmt.TimestampStatus
DurationSaturated bool

func (s Span) HasPosition() bool {
    return s.TimestampStatus == logfmt.TimestampValid && !s.DurationSaturated
}
func (s Span) PositionReasons() []string // fixed order: timestamp, saturation

type IssueCount struct { Count uint64; FirstEntry uint32 }
type TimingEvidence struct {
    Records uint64
    Rejected map[string]IssueCount
    SyntaxErrors IssueCount
    SchemaErrors IssueCount
    TimestampIssues map[string]IssueCount
}
func (b *ReportedBuilder) EntryClock(ord uint32, p logfmt.ClockPosition)
func (b *ReportedBuilder) Evidence() TimingEvidence
func (b *UIHookBuilder) Evidence() TimingEvidence
func (b *UIHookBuilder) Origin() (time.Time, bool)
// Add to model.Log:
RPCEvidence, UIEvidence span.TimingEvidence
UIOrigin time.Time
```

Timestamp reason codes: `timestamp_missing`, `timestamp_invalid`, `timestamp_before_origin`, `timestamp_out_of_range`; saturated duration adds `duration_saturated`. Rejected-duration codes are `duration_missing`, `duration_null`, `duration_invalid`, `duration_negative`, `duration_out_of_range`, `record_schema_invalid`. The latter is for a completion envelope/hook that cannot expose a valid duration field. RPC parsing does not accept JSON null; its literal text is `duration_invalid`. Store only codes/counts/first scanner ordinal; Count zero means no location, so ordinal zero remains valid.

`Records` counts recognised response markers or recognised completion-type UI envelopes. Each is admitted or rejected exactly once. JSON syntax failures for which no completion type can be established do not join this denominator. SchemaErrors/SyntaxErrors are stage facts and may overlap a recognised record's rejection; never add these stages into one bad-record total. TimestampIssues records fixed timestamp reason counts/first ordinals for every decoded UI envelope, including non-completion lines; RPC scanner-wide timestamp counters remain in Stats. Evidence getters return detached maps so later consumers cannot mutate builder state.

Within one record, accumulate schema failures into one local flag and increment SchemaErrors once, even when several metadata fields have bad types. A rejected duration has exactly one rejection reason. Separate timestamp/decoding stages may still overlap, with their denominators documented.

- [x] **Step 1: Add admission regressions using the real scanner and builders.** Pin the mutation each assertion detects: absent elapsed fields must cease producing zero spans, and a genuine explicit zero must remain admitted.

```go
func TestUIAdmissionDistinguishesMissingAndZero(t *testing.T) {
    input := "{\"@timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"apply_complete\",\"hook\":{\"resource\":{\"addr\":\"x.a\"}}}\n" +
        "{\"@timestamp\":\"2026-01-01T00:00:01Z\",\"type\":\"apply_complete\",\"hook\":{\"resource\":{\"addr\":\"x.b\"},\"elapsed_seconds\":0}}\n"
    var comps, ids logfmt.Interner
    var b UIHookBuilder
    if _, err := logfmt.Scan(strings.NewReader(input), &comps, &ids, &b); err != nil { t.Fatal(err) }
    if got := b.Spans(); len(got) != 1 || got[0].Entry != 1 || got[0].DurationMs != 0 {
        t.Fatalf("admitted %+v; want only explicit zero", got)
    }
}
```

- [x] **Step 2: Observe RED with `go test ./internal/span -run TestUIAdmissionDistinguishesMissingAndZero -count=1`.** Add cases before the corresponding production logic: null, negative, quoted number, object/array/bool, positive rounding to zero, malformed JSON, schema mismatch and missing resource metadata.
- [x] **Step 3: Implement explicit UI parsing.** Decode the envelope's timestamp/type/hook as RawMessages so a bad timestamp field cannot discard an otherwise usable duration. Distinguish syntax failure with `json.Valid` from schema failure. Parse the type independently, establishing Records only for existing completion types; progress/provision/refresh events remain non-duration events. A parseable timestamp from any valid envelope establishes the UI origin, as today. A valid timestamp in an envelope with a bad type can still establish that origin.

```go
func uiDuration(raw json.RawMessage) (uint32, bool, string) {
    if len(raw) == 0 { return 0, false, "duration_missing" }
    if string(bytes.TrimSpace(raw)) == "null" { return 0, false, "duration_null" }
    var value any
    dec := json.NewDecoder(bytes.NewReader(raw))
    dec.UseNumber()
    if err := dec.Decode(&value); err != nil { return 0, false, "duration_invalid" }
    number, ok := value.(json.Number)
    if !ok { return 0, false, "duration_invalid" }
    literal := string(number)
    mantissa := strings.SplitN(strings.ToLower(literal), "e", 2)[0]
    if strings.HasPrefix(mantissa, "-") && strings.ContainsAny(mantissa, "123456789") {
        return 0, false, "duration_negative"
    }
    seconds, err := number.Float64()
    if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) { return 0, false, "duration_invalid" }
    if seconds < 0 { return 0, false, "duration_negative" }
    scaled := math.Round(seconds * 1000)
    if scaled > math.MaxUint32 { return math.MaxUint32, true, "" }
    return uint32(scaled), false, ""
}
```

This helper receives one field extracted from a validated JSON object, so it cannot contain trailing JSON values. Negative zero equals zero and is admitted. Inspect the original negative mantissa before Float64 conversion so a negative nonzero value such as -1e-400 cannot underflow to admitted negative zero; add that regression. A finite float64 whose multiplication overflows still saturates. Values not representable as a finite float64 are invalid. Duration admission does not require resource/type/address metadata; preserve whatever valid metadata is available and leave missing values empty. Malformed metadata is a separate schema fact, not grounds to discard an independently valid duration. A missing/null hook means missing duration; a non-object hook is record_schema_invalid.

Classify timestamp independently: absent/null/empty string is missing, wrong type/unparseable text is invalid, parsed timestamps go through RelativePosition. Store offsets only when HasPosition; otherwise both are zero storage sentinels and StartClamped is false. With valid position, derive start from end and full duration, retaining the existing start-at-zero clamp. A saturated UI duration is admitted and flagged but has no usable position even with a valid end timestamp.

- [x] **Step 4: Implement RPC admission/evidence.** Remember the latest callback ordinal/position; consume it once in Entry, including non-response entries, so stale state cannot leak. A direct Entry call without the matching callback has TimestampMissing. Update direct builder tests to deliver explicit clock evidence rather than adding a fallback that invents it. Count recognised markers before reading duration. Use ParseUint(raw,10,32); absent, negative, invalid and overflow are distinct rejections, with negative recognised before unsigned parsing. Only valid unsigned decimal values, including zero, produce spans. Preserve existing metadata/provider/ReqID handling.

```go
// For every admitted span in either builder:
s.TimestampStatus = position.Status
s.DurationSaturated = saturated
if s.HasPosition() {
    s.EndMs = position.OffsetMs
    s.StartClamped = s.DurationMs > s.EndMs
    if !s.StartClamped { s.StartMs = s.EndMs - s.DurationMs }
}
```

- [x] **Step 5: Store evidence in model.Load and test the full admission matrix.** Set RPCEvidence/UIEvidence from the builders and UIOrigin from Origin(); zero time means no origin. Keep UISaturatedDurations populated for existing consumers until C2 replaces its readers. This retained field is an existing interface, not a new compatibility feature.

Required assertions: record count equals admitted+rejected; invalid timestamp does not dilute duration means; genuine zero is positioned; before-origin and beyond-range records retain full durations; UI saturation is a per-span lower bound; missing metadata leaves durations in totals; invalid timestamp is not a JSON syntax error; raw bytes/entry identities remain unchanged. Use a RPC 10ms valid + 20ms before-origin pair and a rejected negative record: admitted count2, total30ms, mean15ms. UI explicit 0 plus missing/null/negative values admits only the zero. Add combined timestamp-invalid and duration-saturated evidence and retain both reasons.

- [x] **Step 6: Run focused tests, `go test ./internal/span ./internal/model ./internal/diagnose ./cmd/tfli`, then `go test ./...`.** Migrate legacy tests that intentionally asserted missing elapsed as zero; retain the zero-extent rendering contract with explicit zero fixtures. Update misleading comments about absent elapsed values.
- [x] **Step 7: Self-review, signed commit and independent review.** Subject `Separate admitted durations from timing positions`.

### Task 3: Project positioned evidence and protect temporal attribution

**Files:** Create `internal/model/timing.go`, `timing_test.go`; modify `attrib/correlate.go`, `correlate_test.go` and existing model/diagnose attribution fixtures that construct spans directly.

**Consumes:** Span.HasPosition/PositionReasons and Log's admitted span sets/evidence.

**Produces:**

```go
type TimingSelection struct {
    Positioned []span.Span
    AdmittedCount, ExcludedCount int
    AdmittedMs, PositionedMs, ExcludedMs uint64
    AdmittedLowerBound, ExcludedLowerBound bool
    Exclusions map[string]uint64
}
func SelectTiming(spans []span.Span) TimingSelection
func PreferredTiming(l *Log) (span.Fidelity, bool)
// Add to attrib.Attribution:
PositionUnavailable bool
```

PreferredTiming selects RPC if any RPC duration is admitted, then UI, else unavailable. SelectTiming preserves input order/Entry identities, retains all admitted durations in totals, and counts excluded observations once even when Exclusions records two reasons. It does not mix fidelities or calculate new report intervals.

- [x] **Step 1: Add a failing temporal-attribution test before changing consumers.** Build an invalid-timestamp span with a duration and offset-zero sentinels, plus an otherwise matching context spanning origin. Correlate must return Unattributed, zero candidates/no address and PositionUnavailable=true. Use direct HasPosition tests only for the domain primitive; end-to-end checks must load real text.

```go
func TestUnavailablePositionCannotAcquireAnAddress(t *testing.T) {
    base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
    s := span.Span{DurationMs: 10, ResourceType: "x", RPC: "ReadResource",
        TimestampStatus: logfmt.TimestampInvalid}
    c := Context{Address: "x.a", ResourceType: "x", Start: base.Add(-time.Second), End: base.Add(time.Second)}
    got := Correlate([]span.Span{s}, base, []Context{c})[0]
    if got.Confidence != Unattributed || got.Address != "" || got.Candidates != 0 {
        t.Fatalf("unpositioned duration acquired attribution: %+v", got)
    }
}
```

- [x] **Step 2: Observe RED; implement exclusion and model projection.** Add the HasPosition guard before correlateOne converts offsets to absolute time. Do not filter the parallel Attribs slice; retain one result per admitted RPC. Preserve no-context as a log-level state and the endpoint rule/confidence cap for valid start-clamped spans.

```go
if !s.HasPosition() {
    return Attribution{Confidence: Unattributed, PositionUnavailable: true}
}
// SelectTiming's per-span accumulation:
// Initialise out.Exclusions = make(map[string]uint64) before the loop.
out.AdmittedCount++
out.AdmittedMs += uint64(s.DurationMs)
out.AdmittedLowerBound = out.AdmittedLowerBound || s.DurationSaturated
if s.HasPosition() {
    out.Positioned = append(out.Positioned, s)
    out.PositionedMs += uint64(s.DurationMs)
} else {
    out.ExcludedCount++
    out.ExcludedMs += uint64(s.DurationMs)
    out.ExcludedLowerBound = out.ExcludedLowerBound || s.DurationSaturated
    for _, code := range s.PositionReasons() { out.Exclusions[code]++ }
}
```

- [x] **Step 3: Verify projection and attribution together.** Use valid 10ms and unpositioned 20ms spans: count2, admitted30ms, positioned10ms, excluded20ms, one excluded observation. Add an excluded saturated span with a bad timestamp: it counts once in ExcludedCount and once under each reason. Check all-unpositioned and empty selections and mixed-tier preference without merging clocks. Migrate direct attribution fixtures to explicit TimestampValid; unknown status must never acquire a name. Run `go test ./internal/model ./internal/attrib ./internal/diagnose`, then the full suite.
- [x] **Step 4: Self-review, signed commit `Separate positioned evidence from duration totals`, and independent task review.**

### Task 4: Switch existing timeline and report consumers together

**Files:** Modify `model/lanes.go`, `tui/timeline.go`, `tui/model.go`, `tui/layout.go`, `tui/views.go`, `profile/profile.go`, `diagnose/diagnose.go`, `cmd/tfli/main.go` and affected tests.

**Consumes:** Tasks 2–3's explicit span status, SelectTiming and PreferredTiming.

**Produces:** `model.ErrUnavailablePosition`; an admission-aware existing TUI/profile/diagnose experience. Keep profile.Render and existing temporal function signatures; change diagnose.Build's evidence parameters and every caller together.

- [x] **Step 1: Write a failing real-model timeline regression.** Load a log with admitted but entirely unpositioned RPC durations and valid UI positions. `renderTimeline` must show positions unavailable while Calls retains the RPC durations; it must neither draw an offset-zero RPC lane nor fall back to UI. Add partial and filtered-empty cases before switching the renderer. Run the focused test and record behavioural RED.
- [x] **Step 2: Make low-level temporal functions reject invalid input explicitly.** Add `ErrUnavailablePosition` alongside ErrMixedTimelines; PackLanes, PeakConcurrency, BusyMs and Stalls validate input before computing. They must not silently drop values and corrupt returned indices. Their consumers pass SelectTiming.Positioned. Preserve mixed-fidelity rejection and all existing half-open/zero-extent semantics. Update synthetic temporal fixtures to state TimestampValid explicitly; do not let zero-valued status mean valid for old tests.
Put the position check in a shared validation helper used by the four temporal functions. Preserve mixed-fidelity validation before position validation so mixed-clock misuse remains explicit.

```go
for _, s := range spans {
    if !s.HasPosition() { return ErrUnavailablePosition }
}
```

- [x] **Step 3: Switch TUI timeline to the selected positioned subset.** Cache TimingSelection alongside the existing timeline slice; invalidate it in invalidateRows. Choose tier from whole-log admitted durations before applying the existing filter. SelectTiming runs on that filtered tier; lanes, selection indices, detail lookup, busy/window/stall calculations all use the same Positioned slice. The original Entry remains the raw-jump/attribution identity. `timelineNarrowed` compares admitted selection counts, not excluded counts, so invalid positions alone do not imply a user filter.

```go
selected := m.filter().SpansMatching(m.log.RPCSpans)
timing := model.SelectTiming(selected)
// Cache timing and return timing.Positioned for RPC lanes/cursor/rendering.
// UI uses m.uiFilter().SpansMatching(m.log.UISpans) under the same rule.
```

Render states in this order: no admitted tier (capture guidance); admitted selected count0 (no matches); admitted selected count>0 and Positioned empty (positions unavailable); otherwise draw positioned lanes and qualify partial analysis. Required text: `Timeline positions unavailable: N admitted observations; Mms retained in duration totals.` For partial data: `Positioned X of N observations; excluded Y (Mms).` Expose fixed exclusion reasons in notes/detail and retain lower-bound wording. Do not switch an all-unpositioned RPC tier to available UI positions. No busy percentage for a zero-length window; a genuine positioned zero remains numerical zero and can still be selected/jumped to.

- [x] **Step 4: Update existing profile and diagnose paths.** Profile duration tables keep all admitted spans. Its existing RPC concurrency block uses the positioned subset for peak/window and names both the selected positioned duration sum and excluded count/time; all-unpositioned is unavailable. Extending profile to UI busy/interval lists remains boundary F. Add a concise admission line before rankings and no-data guidance based on recognised/rejected evidence, not `len(spans)==0` alone. Calls/Types/TUI guidance must not claim no provider traffic when Caps.ProviderEntries or response markers exist, nor no structured stream when Stats.StructuredLines>0.

Pass RPCEvidence/UIEvidence to diagnose.Build as two explicit parameters and update all callers/tests in this task. Its advertised selected usable timing tier is based on admitted observations; capability hints may still describe potential paired/sequential evidence but never claim those unimplemented tiers produced measurements. Diagnose keeps streaming Scan, and uses the same HasPosition exclusion for temporal attribution. Keep capture wall-clock (parsed timestamps) distinct from a span-analysis window. Codes/counts replace raw parse errors in admission output; retain writer error propagation.

- [x] **Step 5: Verify integration with real mixed fixtures.** Cover: valid RPC+unpositioned RPC+valid UI (RPC still chosen); all-unpositioned RPC with valid UI (no fallback); filter selecting only unpositioned data versus no matches; zero denominator coverage unavailable; unpositioned RPC retained in coverage denominator; valid start clamp; missing metadata; scan past oversized timestamps; no admitted durations despite observed markers. Assert profile/TUI totals agree, attribution stays parallel and duration means exclude rejections. Run `go test ./...`, inspect intentional golden changes, and independently review before proceeding.
- [x] **Step 6: Signed commit.** Subject `Keep unavailable positions out of temporal analysis`.

## Final validation and handoff

- [x] Separate test-cleanup subagent after implementation; retain every admission/position/denominator boundary. Review any cleanup diff and rerun affected tests.
- [x] Real terminal checks with mixed and entirely unpositioned fixtures: Calls retains durations; Timeline states partial/unavailable; filter changes distinguish no matches; raw jumps still land on original entries.
- [x] Run `gofmt -d .`, `go mod tidy -diff`, `go mod verify`, `golangci-lint run --timeout=5m`, `go test -race -count=1 ./...`, `go build ./...`. Require pristine output. Inspect goldens using `scripts/read-golden.sh` and raw styling diffs if updated.
- [x] Whole-branch review, resolve findings, record RED/GREEN/terminal evidence here, verify all signatures and leave integration to Dan. Remote integration still requires existing CI.
- [x] C2 consumes the exact evidence/projection interfaces above. Its quality panel, complete anomaly aggregation, source-line lookup and lazy reconstruction status are deliberately not claimed complete by C1. JSON encoding remains boundary G.

## Execution evidence — 10 September 2026

C1 is implemented and verified through signed commit `f5b4165` on `wip/capture-evidence`. C2 remains the next implementation phase. The checked steps above record completed behaviour and verification; the chronology qualifications below are part of that record.

- Signed implementation commits: `1c64820` scanner clock validity; `3bf9254` explicit duration admission; `6999b5e` null-string schema accounting; `b5f3b27` positioned projection and attribution; `d06d86f` temporal consumers; `bc2558a` evidence presentation and denominator fixes; `3844db3` real-log integration coverage; `f5b4165` capture-clock availability and short-pane disclosure.
- Behavioural regressions reproduced scanner range truncation, missing elapsed admitted as zero, invalid-position attribution, unavailable spans entering timelines, zero-window percentages, clipped summaries and fabricated capture-clock zero. The initial duration test failed to compile before exercising behaviour; the actual missing-duration failure was subsequently demonstrated against preserved baseline production. The diagnose denominator regression was added after its correction, then proved against the restored faulty projection (`1 span / 10ms`) before confirming the fixed `2 spans / 30ms`. These later checks are not claimed as original test-first chronology.
- Independent task reviews found and resolved null-string schema undercounting, absent reason/count output, missing integration boundaries, no-data guidance and a diagnose denominator that excluded unavailable positions. Whole-branch review found and resolved the UI-only capture-clock availability gate, short-height disclosure and stale documentation. Final scoped review found no new breakage or unresolved finding.
- Separate cleanup agents classified all changed tests, including review-fix additions, and retained them: no removals or coverage reduction. Final fix cleanup retained three distinct clock/height boundary tests.
- Fresh final checks passed at `f5b4165`: `gofmt -d .`, `go mod tidy -diff`, `go mod verify`, golangci-lint 2.13.2, `go test -race -count=1 ./...`, and `go build ./...`. All ten packages passed race tests; lint reported zero issues. All implementation signatures verified good. No golden files changed.
- Actual PTY checks at 110×34 showed Calls retaining 30ms, Timeline positioning 10ms and excluding 20ms with `timestamp_before_origin`, and diagnose reporting 10ms contained / 20ms unattributed (33.3%). Facet selection distinguished unavailable-only from no matches; clearing and opening the positioned call returned to original Entry 4/7. At 60×25 the unavailable message retained the duration and reason; at 60×9 the established `… more` marker disclosed omitted content. A real invalid-timestamp UI capture retained 1000ms while reporting wall-clock unavailable.
- Implementation decisions: migrate the scrubber's obsolete oversized-timestamp rejection test to supported retention plus real secret scrubbing (no scrub production change); add `@level` to the plan's scanner fixtures so they exercise recognised UI input. If either boundary were changed later, its admission/privacy fixtures would need revisiting.
- Existing recognition is preserved: structured lines require literal `@level` and `@timestamp` keys, although the timestamp value may be invalid, null or empty. An entirely absent key is outside scanner recognition. Missing/invalid outer hclog timestamps likewise do not create independent RPC records. C2 will disclose these limits without fabricated counts.

### Original plan self-review

Clock delivery precedes builder use; both builders precede consumer exclusion. Tasks 3–4 include synthetic-fixture migration and all existing temporal consumers, so adding status cannot silently fabricate zero positions. C1 retains diagnostics needed to distinguish recognised-but-rejected evidence; C2 supplies the full shared capture summary. Scanner entry layout/identity and the diagnose streaming path stay intact. Unknown hclog-header recovery is explicitly outside the recognised-record boundary.

## Planning review evidence

- Independent review inspected both plans against the binding spec and current scanner/builders/consumers. It found missing UI timestamp-stage retention, negative-number underflow in duration parsing, and pointer aliasing in C2's quality snapshots.
- Added TimestampIssues before completion/duration admission, lexical rejection of negative nonzero mantissas before floating-point conversion, and explicit pointer cloning/mutation regressions in C2.
- Scoped re-review confirmed all three findings addressed, with no new substantive issue. It also checked the Task 3/4 split keeps position guards paired with consumer/fixture migrations.
- Self-review checked scope coverage, type/signature consistency, stage denominators and task dependencies. No application code changed. The planning baseline `go test ./...` passed; implementation, terminal and final validation checkboxes remain unexecuted.
