# Capture Quality Facts and Presentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose consistent whole-capture evidence facts in the TUI and text profile, with source locations, honest attribution denominators and lazy reconstruction status.

**Architecture:** Assemble a focused capture-quality model from existing scan/build results and C1 admission evidence. Compute immutable scan facts once, add lazy reconstruction status separately, and let each output format render those facts without parsing another report. Source-line lookup is lazy and based on original byte offsets.

**Tech Stack:** Go 1.25+, standard testing, existing Bubble Tea/Lip Gloss/Bubbles viewport. No new tool or dependency.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), item 6, source-location support and boundary C. This is C2, dependent on [Timing evidence validity](2026-09-10-timing-evidence-validity.md). Dan approved the two-plan split on 10 September 2026. No application implementation is part of writing these plans.

## Global Constraints

- “Whole-log quality facts remain labelled as whole-log facts.”
- “Filtering cannot make an input anomaly disappear from the capture summary or turn an unavailable tier into an available one.”
- “Counters at different processing stages may overlap and must not be summed into a fabricated ‘bad lines’ total.”
- “Zero denominator means unavailable, not 0% or 100% coverage.”
- “Incomplete context means an observed start lacked a matching terminator. It is not proof the resource failed.”
- “Until requested, its quality state is ‘not checked’, not ‘valid’.”
- “Terminal rendering escapes untrusted controls.”
- “--diagnose continues to avoid resource names and captured field values.”
- Preserve the existing streaming diagnose path and strict reconstruction/scrub policy. Do not implement response recovery, JSON export, comparison or Resources here.
- Follow TDD, independent task/whole-branch review and separate test cleanup; sanitised fixtures and British/Australian prose.
- Use `/Users/dan/.codex/bin/codex-git` for all Git operations. Sign commits and preserve hooks; signing failure is an immediate hard stop. No merge/push without Dan's instruction.

---

## Prerequisites and ownership

Start this plan after C1 is implemented, reviewed and verified. Use its actual
integration commit as the execution base; do not reapply C1 or execute the two
plans in parallel. Verify clean status, pull with rebase and use a topic branch.

C1 supplies `logfmt.TimestampStatus`, `span.Span.HasPosition/PositionReasons`,
`span.IssueCount`, `span.TimingEvidence`, `model.SelectTiming`,
`model.PreferredTiming`, Log.RPCEvidence/UIEvidence/UIOrigin and position-aware
attribution. This plan uses those exact names. Changes to these interfaces must
be reconciled in both plans before C2 starts.

Capture facts are not active-selection evidence. Do not accept a TUI Filter in
the capture-quality builder. Confidence partitions cover all admitted RPCs,
including missing resource type and unavailable positions. No-context is a
capture property; it does not claim failed inference for each resource.

## File responsibilities

| Files | Responsibility |
| --- | --- |
| New `internal/model/location.go`, `location_test.go` | Lazy exact physical-line lookup from source byte ranges |
| New `internal/model/quality.go`, `quality_test.go`; `model/log.go` | Capture facts, denominator rules and lazy reconstruction state |
| `internal/attrib/context.go`, `context_test.go` | Expose syntax/schema/timestamp/incomplete context facts from existing collection |
| `internal/profile/profile.go`, `profile_test.go`; `internal/diagnose/diagnose.go`, `diagnose_test.go`; `cmd/tfli/main.go`, `main_test.go` | Shared facts with consumer-specific rendering and privacy |
| New `internal/tui/quality.go`, `quality_test.go`; `model.go`, `layout.go`, `workbench.go`, `help.go` and tests | Compact indicator, scrollable modal, modal precedence and frame sizing |
| `README.md`; TUI golden files changed by new indicator/help | User guidance and reviewed visual evidence |

### Task 1: Add exact source locations and context facts

**Files:** Create `model/location.go`, `location_test.go`; modify `model/log.go`, `attrib/context.go`, `context_test.go`.

**Interfaces produced:**

```go
type SourceLocation struct {
    Entry uint32
    StartByte, EndByte uint64 // half-open source byte range
    StartLine, EndLine uint64 // inclusive, one-based physical lines
}
func (l *Log) SourceLocation(entry uint32) (SourceLocation, bool)

// In attrib, reuse span.IssueCount's count/first ordinal representation.
type ContextEvidence struct {
    SyntaxErrors, SchemaErrors span.IssueCount
    MissingTimestamps, InvalidTimestamps span.IssueCount
    UnmatchedTerminators, IncompleteContexts span.IssueCount
}
func (c *ContextCollector) Evidence() ContextEvidence
```

- [ ] **Step 1: Write a location test whose source exceeds Entry.Lines storage.** Use real model.Load over a temporary fixture: a timestamped header, 65,536 continuation lines and another timestamped header. Assert the second entry's StartLine is 65,538; the first entry's EndLine is 65,537. Then test CRLF, no final newline, UTF-8 before a newline, a zero-length/invalid ordinal and an out-of-range source byte interval. The production failure this protects is deriving locations from saturating Entry.Lines or rescanning only the current rendered text.

```go
first, ok := l.SourceLocation(0)
if !ok || first.StartLine != 1 || first.EndLine != 65537 {
    t.Fatalf("first location=%+v, ok=%v", first, ok)
}
second, ok := l.SourceLocation(1)
if !ok || second.StartLine != 65538 { t.Fatalf("second location=%+v, ok=%v", second, ok) }
```

- [ ] **Step 2: Run focused RED, then implement one lazy line-start index.** Add `sourceLinesOnce sync.Once` and `sourceLineStarts []uint64` to Log. Build offsets once by scanning Data for newline bytes. Include zero as the first line start for nonempty data; omit a trailing empty line start at EOF. Validate entry ordinal and byte interval before lookup. Binary-search the line containing Off and the line containing Off+Len-1. Never change Data or Entries.

```go
start := sort.Search(len(starts), func(i int) bool { return starts[i] > e.Off })
end := sort.Search(len(starts), func(i int) bool { return starts[i] > e.Off+uint64(e.Len)-1 })
return SourceLocation{Entry: entry, StartByte: e.Off, EndByte: e.Off+uint64(e.Len),
    StartLine: uint64(start), EndLine: uint64(end)}, true
```

The searches return one-based containing-line numbers because starts includes zero. A zero-length entry or invalid interval returns false; no fabricated source location. Index allocation is O(physical lines), paid only for location consumers. Concurrent location reads must initialise once without races.

- [ ] **Step 3: Add failing context-fact tests before collector changes.** Feed real structured lines containing syntax errors, valid JSON with bad schema, missing/invalid timestamps, unmatched terminators and an unclosed start. Required counters are separate; valid JSON with `"@timestamp":7` is schema/timestamp evidence, not JSON syntax failure. A record with two defects may affect separate stages but is never counted twice in one reason bucket. Retain the scanner ordinal of the first example per counter. A first occurrence at ordinal0 is valid because Count indicates presence.
- [ ] **Step 4: Retain context diagnostics during existing collection.** Extend Context with `Entry uint32` for its observed start; populate it from Structured's ordinal. Count missing/invalid timestamps before the existing early return. Distinguish syntax (`!json.Valid`) from field-schema errors. Preserve the existing context window, duplicate-start and terminator matching rules. Compute IncompleteContexts by iterating final Contexts once and counting Unclosed, including duplicate-start close-outs; choose the smallest source ordinal for its first location. Calling Evidence/Contexts repeatedly must not increase counters or mutate completed results.

Decode envelope timestamp/type as RawMessages before validating their individual types, so a timestamp type error can be counted as invalid timestamp as well as a schema-stage failure. Coalesce multiple bad fields into one SchemaErrors event per line. Context creation still requires its existing valid timestamp/resource metadata; no duration or window is inferred from rejected context data.

```go
func noteIssue(dst *span.IssueCount, entry uint32) {
    if dst.Count == 0 || entry < dst.FirstEntry { dst.FirstEntry = entry }
    dst.Count++
}
```

Preserve Malformed's current aggregate accessor only for remaining callers while moving shared quality readers to the separated facts. Do not embed JSON parser errors, addresses or hook values in diagnostics. No inference window is created from an unusable timestamp.

- [ ] **Step 5: Run `go test ./internal/model ./internal/attrib -count=1`, then the full suite.** Confirm exact locations and context counters against raw input. Self-review, signed commit `Retain source locations and context evidence`, then independent task review.

### Task 2: Build the shared whole-capture quality model

**Files:** Create `model/quality.go`, `quality_test.go`; modify `model/log.go` and tests. No TUI/CLI flags in this layer.

**Interfaces consumed:** C1 TimingEvidence/SelectTiming and Task 1 ContextEvidence/SourceLocation.

**Interfaces produced:**

```go
type QualityIssue struct {
    Stage, Code string
    Count uint64
    FirstEntry *uint32 // nil means collector did not retain a location
}
type TierQuality struct {
    Records, Admitted, Rejected, Positioned uint64
    DurationMs, PositionedMs, ExcludedMs uint64
    DurationLowerBound bool
    Origin *time.Time
    Exclusions map[string]uint64
}
type CaptureQuality struct {
    RPC, UI TierQuality
    ProviderEntries, StructuredLines uint64
    Issues []QualityIssue // deterministic stage/code order, no captured values
    HasContext bool
    Attribution attrib.Coverage
    NameableMs, RPCDurationMs uint64
    NameableShare *float64
}
type CaptureQualityInput struct {
    Stats logfmt.Stats
    Caps span.Capabilities
    RPCSpans, UISpans []span.Span
    RPCEvidence, UIEvidence span.TimingEvidence
    UIOrigin time.Time
    Contexts []attrib.Context
    ContextEvidence attrib.ContextEvidence
    Attributions []attrib.Attribution
    ComponentOverflow, RequestIDOverflow uint64
}
func BuildCaptureQuality(in CaptureQualityInput) CaptureQuality
func (l *Log) CaptureQuality() CaptureQuality

type ReconstructionQuality struct {
    State string // "not_checked", "checked", "failed"
    Responses int
    Code string // empty unless failed: "reconstruction_failed"
}
func (l *Log) ReconstructionQuality() ReconstructionQuality
```

BuildCaptureQuality retains no source bytes, addresses or builder references. Fixed stage names: `scan`, `rpc_duration`, `ui_duration`, `ui_decode`, `context`, `interning`, `capability`. Count/first-entry helpers copy optional ordinals; never point to a changing loop variable. CaptureQuality getters return detached maps/slices (including Coverage.Candidates) and fresh copies of every pointer pointee (QualityIssue.FirstEntry, both TierQuality.Origin values and NameableShare), so consumers cannot mutate stored capture facts. IssueCount itself has a value ordinal and needs no pointer clone. Test mutation of each pointer as well as maps/slices. Store the completed immutable value once on Log; getter must not rescan data, rebuild attribution or respond to filters.

- [ ] **Step 1: Add failing reconciliation tests using model.Load.** A fixture with a 10ms named RPC, 20ms unavailable-position RPC, one rejected RPC record and one explicit-zero UI observation must show admitted RPC count2, rejected1, total30ms, positioned10ms, excluded20ms, nameable numerator10ms/denominator30ms. Add no context, zero denominator, missing resource type, all-unpositioned tiers and an incomplete context. Assert equality across repeated getter calls and that mutating a returned slice cannot affect later reads.

```go
q := l.CaptureQuality()
if q.RPC.Admitted != 2 || q.RPC.Rejected != 1 || q.RPC.DurationMs != 30 || q.RPC.ExcludedMs != 20 {
    t.Fatalf("RPC quality does not reconcile: %+v", q.RPC)
}
if q.NameableMs != 10 || q.RPCDurationMs != 30 || q.NameableShare == nil {
    t.Fatalf("nameable evidence=%+v", q)
}
```

- [ ] **Step 2: Implement counts and documented denominators.** Tier availability is Admitted>0; Records/ProviderEntries/StructuredLines independently express evidence presence. Positioned counts come from SelectTiming. Rejected totals sum only one mutually exclusive rejection stage. Attribution duration denominator is the sum over all admitted RPCs, independently of whether a context table exists. Nameable numerator uses Contained+Likely. With no context or zero denominator, NameableShare is nil; show actual numeric denominator separately. With context and positive denominator, a zero numerator is a real 0% result.

```go
q.RPCDurationMs = rpcTiming.AdmittedMs
q.NameableMs = q.Attribution.MsByConfidence[attrib.Contained] + q.Attribution.MsByConfidence[attrib.Likely]
if q.HasContext && q.RPCDurationMs > 0 {
    share := float64(q.NameableMs) / float64(q.RPCDurationMs)
    q.NameableShare = &share
}
```

Reject an inconsistent attribution input during development with an explicit invariant check: if HasContext, Attributions must be parallel to RPCSpans. Do not silently truncate the quality denominator through attrib.Summarise's shorter-slice behaviour. The builder's input comes from one scan; do not fabricate per-call attribution when there is no context.

Required quality facts and their meaning:

| Fact/code | Source | Count unit / denominator |
| --- | --- | --- |
| Admitted/rejected RPC duration, per rejection code | C1 ReportedBuilder evidence | Recognised response records; records = admitted + rejected |
| Admitted/rejected UI duration, per rejection code | C1 UIHookBuilder evidence | Recognised completion records; records = admitted + rejected |
| `json_syntax`, `schema_invalid` | C1 builder and Task 1 context facts, distinct stages | Decoding observations, not an additional rejected-timing total |
| Position exclusion reasons | SelectTiming | Admitted observations; one observation can have multiple reasons |
| `timestamp_before_origin`, `timestamp_out_of_range` | Scan Stats and UIEvidence.TimestampIssues | Timestamp-bearing records in their own clocks, including non-completion UI envelopes; do not combine clocks |
| `duration_saturated`, `start_clamped` | Admitted spans | Per-tier admitted observations; saturation means lower bound |
| `line_count_saturated` | Stats.LinesSaturated | Additional physical lines after an entry's line counter reaches its cap, not number of affected entries |
| `component_overflow`, `request_id_overflow` | Interner.Overflowed() separately | Overflowing Intern calls; not distinct strings, no invented percentage |
| `request_tracking_capped` | Caps.ReqIDTrackingFull | A tracking-cap flag, represented as 0/1, not a lost-record count |
| `context_incomplete`, `terminator_unmatched`, missing/invalid context timestamp | ContextEvidence | Contexts, terminator events, or decoded structured records respectively; keep stages separate |
| Attribution confidence counts/time and nameable numerator/denominator | Full admitted RPC slice/parallel attributions | All admitted RPC duration; never an inferred resource-eligible subset |

For counter-only facts with no retained first ordinal (existing scan/interner counters), FirstEntry stays nil and presentation says location unavailable if requested. Do not infer exact locations from saturated Entry.Lines or fabricate a source sample. C1 record/position and Task1 context facts have exact first entry support; document samples as first occurrences, not exhaustive lists.

- [ ] **Step 3: Wire model.Load once.** Collect contexts exactly once, correlate using C1 rules, assemble input including both interner overflow counters before locals are discarded, and store quality after those results exist. Keep UISaturatedDurations until its last renderer is migrated in Task4; then remove that redundant field and migrate all tests/callers in the same commit.
- [ ] **Step 4: Add lazy reconstruction status with a failing test.** Fresh load/CaptureQuality/ReconstructionQuality must remain not_checked. Calling ProviderResponse runs the existing whole-capture reconstruction once; success becomes checked with the actual response count, failure becomes failed with the fixed code. A checked capture containing no reconstructed responses is not evidence that every entry is a response. Do not change the strict parser's acceptance rules or expose its error text in quality output.

Use an atomic completion flag beside existing responseOnce: store it only after responses/responseErr have been assigned; ReconstructionQuality loads it before reading those fields. This gives concurrent readers a race-free not_checked state during work, and a completed snapshot afterward. Do not trigger responseOnce from the status getter.

```go
if !l.responseChecked.Load() { return ReconstructionQuality{State: "not_checked"} }
if l.responseErr != nil { return ReconstructionQuality{State: "failed", Code: "reconstruction_failed"} }
return ReconstructionQuality{State: "checked", Responses: len(l.responses)}
```

- [ ] **Step 5: Verify privacy and laziness.** Use captured marker strings in addresses, keys and malformed JSON. Assert no marker appears in formatted quality facts; SourceLocation reports byte/line positions only. Concurrent ProviderResponse/status/SourceLocation calls must pass race tests. Compare CaptureQuality before and after reconstruction: static facts identical, reconstruction status separate.
- [ ] **Step 6: Run focused/model tests and full suite, self-review and signed commit `Build shared capture quality facts`.** Obtain independent task review before output consumers.

### Task 3: Present the same facts in profile and streaming diagnose

**Files:** Modify `profile/profile.go`, `profile_test.go`, `diagnose/diagnose.go`, `diagnose_test.go`, `cmd/tfli/main.go`, `main_test.go`.

**Consumes:** BuildCaptureQuality, CaptureQuality, ReconstructionQuality and C1 admission-aware guidance.

**Interfaces:** Keep profile.Render(io.Writer,*model.Log) error. Add Quality model.CaptureQuality to diagnose.Report. Add a CaptureQuality argument to diagnose.Build, replacing C1's separate timing-evidence arguments after moving their consumers to Quality; update every caller/test together. Existing specialised diagnose histograms remain local.

- [ ] **Step 1: Write writer-backed failing tests for actual output.** Use model.Load for profiles and the existing streaming runDiagnose path for diagnosis. Verify identical admitted/rejected/positioned totals and attribution numerator/denominator. Profile contains `CAPTURE QUALITY (whole log)` before its rankings, an unavailable share for zero denominator, and explicit rejection/position reasons even when no spans were built. Diagnose must contain no test address or captured field value.

```go
var out bytes.Buffer
if err := profile.Render(&out, l); err != nil { t.Fatal(err) }
text := out.String()
if !strings.Contains(text, "CAPTURE QUALITY (whole log)") ||
    !strings.Contains(text, "duration_missing") {
    t.Fatalf("missing capture evidence: %s", text)
}
if l.ReconstructionQuality().State != "not_checked" { t.Fatal("profile reconstructed bodies eagerly") }
```

- [ ] **Step 2: Implement a short profile summary.** Show each tier's admitted/rejected/positioned counts and duration total; show excluded counts/time with lower-bound qualification; show nameable milliseconds over total RPC milliseconds plus percentage only when available. Print nonzero issue rows grouped by stage using fixed codes; no fabricated total of bad lines. Include context-incomplete wording that does not assert operation failure. Retain resource names only in the existing local investigation report, not quality reason text. Ordinary profile rendering leaves reconstruction not checked.
- [ ] **Step 3: Reuse quality calculations in streaming diagnose.** In runDiagnose, construct CaptureQualityInput from the same builders, contexts, interner counters and Stats used by its current report. Do not call model.Load, os.ReadFile or reconstruct JSON bodies. Passing the data-only model builder is permitted; loading a full model is not. Replace duplicate duration/coverage totals and evidence-tier decisions with Quality values while retaining diagnose's masked histograms and capture-wide timestamp span where it explicitly reports that different metric.

```go
quality := model.BuildCaptureQuality(model.CaptureQualityInput{
    Stats: stats, Caps: sniffer.Report(), RPCSpans: builder.Spans(), UISpans: uiBuilder.Spans(),
    RPCEvidence: builder.Evidence(), UIEvidence: uiBuilder.Evidence(), UIOrigin: uiOrigin,
    Contexts: contexts, ContextEvidence: cc.Evidence(), Attributions: attributions,
    ComponentOverflow: comps.Overflowed(), RequestIDOverflow: reqIDs.Overflowed(),
})
```

Here uiOrigin is Origin()'s time or zero; contexts and attributions are the one post-scan collection/correlation, not a second pass. Build receives this quality value in place of duplicate admission inputs. Diagnose does not require model.SourceLocation or retain a physical-line index; original diagnostic counters without locations remain counts only.

- [ ] **Step 4: Preserve error propagation and disclosure.** Existing failing-writer tests remain authoritative; add the new summary to the path those writers exercise. Assert expected errors, no uncontrolled stderr/log output, and no parser snippets in either quality summary. Test malformed-only UI, provider-traffic-without-admitted-duration, UI-only zero-duration and mixed tiers. Do not add JSON flags or new CLI options here.
- [ ] **Step 5: Run profile/diagnose/CLI focused tests, full suite, self-review and signed commit `Report capture quality consistently`.** Obtain independent review, including streaming-path and disclosure checks.

### Task 4: Add the TUI quality indicator and scrollable panel

**Files:** Create `tui/quality.go`, `quality_test.go`; modify `tui/model.go`, `layout.go`, `workbench.go`, `help.go`, related layout/help/security tests and intentional goldens; update README.

**Consumes:** Log.CaptureQuality/ReconstructionQuality/SourceLocation. Rendering must never receive the active filter as input to capture facts.

**Interfaces produced:**

```go
type qualityState struct { open bool; viewport viewport.Model }
func (m *Model) openQuality()
func (m *Model) handleQualityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd)
func (m *Model) renderQuality(w, h int) string
func (m *Model) qualityIndicator() string
```

- [ ] **Step 1: Add a failing real-model keyboard test.** Load a fixture with a rejected duration and incomplete context, record raw position/filter/scope/query, send i, scroll through the panel, resize narrow, and close with Esc. The panel must be reachable without changing the investigation, show whole-log facts even when a filter hides affected entries, and restore the original raw occurrence so n still repeats correctly. Capture rendered panel content and footer, not only state fields.

```go
before := m.renderRawLog(60, 6)
m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
if !strings.Contains(m.View(), "CAPTURE QUALITY") { t.Fatal("i did not open quality") }
m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
if got := m.renderRawLog(60, 6); got != before { t.Fatalf("quality changed raw position: %q", got) }
```

- [ ] **Step 2: Implement the modal using the existing viewport pattern.** In Update, existing response/search/help modes retain their key precedence. Handle an already-open quality modal before normal navigation; bind i to open only when no other modal/editor is active. Inside quality: Esc/i close, up/down/j/k scroll, PgUp/PgDn page, q/Ctrl-C quit; other keys are swallowed. Set MouseWheelEnabled=false. Numbered keys, filters, search and request-scope keys must not modify the underlying model while quality is open. Avoid nesting a generic modal framework.

```go
switch msg.String() {
case "esc", "i": m.quality.open = false
case "q", "ctrl+c": m.quitting = true; return m, tea.Quit
case "up", "down", "j", "k", "pgup", "pgdown":
    m.View() // refresh viewport dimensions
    m.quality.viewport, _ = m.quality.viewport.Update(msg)
}
```

Render through the workbench's existing full-width help/response panel paths with title `CAPTURE QUALITY (whole log)`. Build content from facts only; wrap lines to width before setting viewport content and preserve/clamp YOffset on resize. Keep close/scroll/quit hints visible in the footer. Full content stays reachable in a short terminal. Escape all filename/source text with DisplayText before styling.

- [ ] **Step 3: Add an indicator whose wording does not claim overall validity.** Normal header label is `i quality`; if any limitation is present use `i limitations`. Limitations include rejected records, position exclusions, saturation/clamping, parser/schema anomalies, capped tracking, overflow, incomplete contexts and attribution uncertainty/no context. A not_checked reconstruction state alone does not assert corruption; the panel always discloses it. Never print a score, percentage health or “all valid”. Retain the indicator at supported narrow widths before expendable filename text, using the existing header-fitting rules. Preserve explicit saturation warning until the panel/indicator replacement is visible at all existing golden widths; then remove the redundant UISaturatedDurations field and migrate its callers/tests to Quality.UI.DurationLowerBound.
- [ ] **Step 4: Render sections in order.** Timing availability and admitted/rejected/positioned counts; per-stage anomaly rows with first source location when available; attribution numerator/denominator/confidence distribution; context limitations; reconstruction status. Label positions as original one-based physical lines. First samples call SourceLocation with the stored ordinal; failed lookup yields `location unavailable`. Include the recognition limitation: malformed hclog headers are not recovered as independent RPC records. The panel does not invent a count of such records.
- [ ] **Step 5: Cover modal precedence and lazy status.** While raw/response search is active, i inserts text. While help or response is open it keeps that modal's existing behaviour. After viewing a response, close it and open quality: status reflects checked/failed without triggering another reconstruction. After filtering out every span, quality counts stay identical. Escape leaves filters intact; next Escape follows the existing underlying behaviour. Test Ctrl-C/q, zero height/width handling, Unicode/control-bearing filenames and short/narrow layouts.
- [ ] **Step 6: Document and verify visually.** Add i to help and README. Run TUI tests before updating goldens. For intended changes only, run `go test ./internal/tui -update`, inspect via `scripts/read-golden.sh` and review styling diffs. Verify live terminal interaction at existing normal/narrow widths and page through all sections, including first-location text and reconstruction status. Record exact fixture/keys/results.
- [ ] **Step 7: Self-review, signed commit `Expose capture quality in the terminal interface`, and independent review.** Confirm the renderer never recomputes scan facts or attribution on keypress.

## Final validation and delivery

- [ ] Separate test-cleanup subagent after implementation, preserving all denominator/privacy/location/modal boundaries; review edits and rerun affected tests.
- [ ] Verify TUI/profile/diagnose agree on the controlled capture's admission and attribution figures; filters affect selection figures only. Verify ordinary profile does not reconstruct bodies and diagnose still streams input.
- [ ] Run `gofmt -d .`, `go mod tidy -diff`, `go mod verify`, `golangci-lint run --timeout=5m`, `go test -race -count=1 ./...`, `go build ./...`. Require pristine output and inspect all intentional goldens.
- [ ] Measure model.Load and first/repeated SourceLocation calls with a sanitised large fixture using Go benchmarks. Record allocations and first-call versus repeated-call behaviour. Do not add caching, parallel loading or a storage change without measurement and a separate justified design.
- [ ] Whole-branch review, address findings, record execution evidence here and verify signed commits/clean status. Leave merge/push choice to Dan; remote integration requires existing CI.

## Plan self-review

Task 1 supplies location/context facts, Task 2 owns capture calculations, Task 3 consumes those values in reports and Task 4 in the TUI. All C1 interfaces are named consistently. Capture facts never accept active filters. Duration and positional validity stay per observation; counters do not replace them. Source locations use original bytes, and quality output does not disclose captured values. Response status remains lazy and strict reconstruction/recovery policy is unchanged. JSON encoding is intentionally left to boundary G.

## Planning review evidence

- Independently reviewed alongside C1 against item 6, shared admission rules and existing code. The missing UI timestamp evidence producer was added in C1, and C2 now consumes its named TimestampIssues map.
- Review identified shared pointer pointees in otherwise copied CaptureQuality values. The plan now requires deep copies and mutation tests for first-entry pointers, origins and the optional nameable share.
- Scoped re-review confirmed these findings and C1's negative-underflow fix addressed, with no new substantive issue. Self-review checked all cross-plan names and the source/context → quality → reports/TUI dependency chain.
- These are planning results, not implementation evidence. Application code is unchanged; all implementation and final validation checkboxes remain unexecuted.
