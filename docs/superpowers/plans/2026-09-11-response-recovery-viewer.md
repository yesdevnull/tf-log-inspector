# Response Recovery Viewer and Quality Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Open the verified response belonging to the selected raw physical line, explain failed or unavailable positions, and report lazy reconstruction quality accurately across the viewer and exported reports.

**Architecture:** Cache I1's structured outcome once per loaded log and index its original byte ranges. Resolve the top visible raw physical line through that index, keeping the existing modal renderer and navigation. Publish complete/partial/failed quality from the same cache and migrate both JSON report kinds to an explicit version 2 contract.

**Tech Stack:** Go 1.25, existing standard library and Bubble Tea/Lip Gloss dependencies, standard Go tests, existing golden and terminal workflows.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), item 8 and Boundary I. [I1 parser and strict scrubbing](2026-09-11-response-recovery-parser.md) defines the implemented outcome and range contract. [Provider fragment reconstruction](../specs/2026-09-09-provider-fragments-design.md) remains the authority for grammar and terminal safety.

## Global Constraints

- “Use one parser and one definition of message ownership.”
- “Preserve exact component isolation and source-fragment mapping.”
- “Source bytes and scanner entry identities remain authoritative. Derived views, reconstructed text and filters never modify either.”
- “Diagnostics use fixed reason codes, counts and locations; they do not embed source text or JSON parser snippets.”
- “The quality panel reports reconstruction as complete, partial or failed only after it has actually run.”
- “Scrubbing continues to reject any reconstruction diagnostic and create no output.”
- “Preserve scrolling, search and exact return position.”
- “Fixtures are synthetic or sanitised.”
- Use the existing dependencies and toolchain. No new runner, parser mode, background worker or capture mutation.
- Follow behavioural TDD, independent task review and separate test-cleanup subagents. Tests exercise real parsing/model/rendering/filesystem behaviour, without mocked application boundaries.
- Use `/Users/dan/.codex/bin/codex-git` and signed topic-branch commits. A signing failure stops work immediately. No merge or push without Dan's instruction.
- Use Australian/British prose. Validate Linux/macOS on amd64/arm64 with the existing CI commands.

---

## Status and decisions

Dan authorised this I2 plan after I1 merged into local main at `04d28da` on 11 September 2026. Work is on `wip/response-recovery-viewer-plan`. After both plan-review findings were fixed and verified in `433c460`, Dan approved the revised plan and subagent implementation on 11 September 2026. Implementation is in progress.

Dan approved JSON v2 with explicit recovery states for both profile and comparison output on 11 September 2026. Version 1 only defines `not_checked`, `checked` and `failed`; silently adding recovery states would change its documented contract. Version 2 exposes the actual outcome without maintaining a second writer or a version-selection flag. The existing v1 documents remain historical references, clearly labelled as such. Both peer-review findings are resolved: the approved plan specifies atomic temporary-error publication and scalar diagnostic selection with failure-tail benchmarks.

Raw Log has a viewport position, not a character cursor. For `r`, “selected physical position” means the first physical line the pane actually draws, identified by entry ordinal and zero-based line offset. Horizontal scrolling and a search occurrence do not become byte offsets. A physical line containing provider payload opens that message even when its logger header is at the left edge; an inline UI-only line or an empty fragment does not select a response. The model will intersect the original nonempty line byte range with original fragment ranges. It never searches a later physical line or another response in the same entry.

### Alternatives considered

1. **Cached ranges and explicit JSON v2 — recommended.** One parser result, logarithmic repeated range lookup, one canonical quality state and no lossy report projection. Status selection copies only fixed-size diagnostic presentation facts; complete selection additionally copies the selected message's fragments.
2. **Scan every message for each opening.** Less indexing code, but repeated opening in a large capture revisits every response and diagnostic. The existing spec explicitly asks for lazy indexes where repeated lookup needs them.
3. **Keep JSON v1 with conservative strict-quality projection.** This preserves old wire states but cannot expose recovered counts and partial quality directly; it introduces a separate consumer projection. Do not implement this alternative without Dan choosing it and revising this plan's exact mapping.

The implementation is synchronous on first `r`, as the current viewer is. Benchmark first and repeated access; do not add concurrency, progress UI or cancellation without a separate demonstrated need and design decision.

## Existing code and file map

At `04d28da`, `Log.ProviderResponse(e)` caches strict reconstruction and returns the first response overlapping any part of an entry. `openResponse` ignores `raw.topLine`. `ReconstructionQuality` snapshots `not_checked`, `checked` or `failed` using atomic publication. `profile.Report` carries that snapshot, and both JSON encoders share `reconstructionJSON`. `SourceLocation` already builds an exact lazy physical-line index independently of the saturated `Entry.Lines` count.

| File | Planned responsibility |
| --- | --- |
| New `internal/model/response.go` | Lazy I1 outcome, detached selection result, source-line lookup and three range indexes |
| Modify `internal/model/log.go` | Store cache/index fields; route the existing entry API through the shared cache during migration |
| Modify `internal/model/provider_response_test.go` | Exact physical selection, recovery, detachment and concurrency tests |
| New `internal/model/response_benchmark_test.go` | First inspection and repeated line selection on large synthetic captures |
| Modify `internal/model/quality.go`, `quality_test.go` | Canonical non-triggering outcome status and safe counts |
| Modify `internal/tui/response.go`, `response_test.go` | Resolve visible physical position, display status/notices, retain modal behaviour |
| Modify `internal/tui/quality.go`, `quality_test.go` | Complete/partial/failed text and limitations indicator |
| Modify `internal/profile/json_data.go`, relevant `json*_test.go` | Version 2 reconstruction mapping and exact field/null validation |
| Modify `internal/profile/comparison_json.go`, `comparison_json_test.go` | Version 2 comparison root and shared capture-quality contract |
| Modify `internal/profile/data_test.go`, `comparison_data_test.go` | Lazy snapshots before and after actual inspection, including detached prior reports |
| Modify `cmd/tfli/profile_json_test.go`, `comparison_test.go` | Real CLI JSON version/schema assertions and publication checks |
| Modify `README.md`, `internal/tui/help.go` if its response guidance needs correction | Explain physical-line selection, notices and quality states |
| New `docs/profile-json-v2.md`, `docs/comparison-json-v2.md`; annotate v1 docs | Complete current wire contract with historical versions retained as documents only |
| New `testdata/response-recovery.log` | Sanitised interactive and integration fixture |
| Update this plan and investigation spec after execution | Validation, reviews and Boundary I completion evidence |

Do not change I1 grammar or the scrubber's strict gate. Do not change capture-quality timing/attribution calculations, filters, source IDs or raw history. Keep any code movement limited to the response cache's responsibility.

## Model interfaces and selection contract

These are proposed new interfaces; the I1 types already exist.

```go
// internal/model/response.go
type ProviderResponseSelection struct {
    State          string // "complete", "invalid", "unavailable", "none"
    SourceLine     uint64 // one-based physical line; zero for an invalid request
    Response       logfmt.ProviderJSON
    Diagnostic     *logfmt.ProviderJSONDiagnostic // presentation facts only; both range slices nil
    HasDiagnostics bool // any diagnostic in the whole-capture outcome
}

func (l *Log) ProviderResponseAt(entry uint32, lineOffset int) ProviderResponseSelection
func (l *Log) inspectProviderResponses()
func (l *Log) responseSourceLine(entry uint32, lineOffset int) (start, end, line uint64, ok bool)

type responseRange struct {
    start, end uint64
    index      int // message index or diagnostic index, according to the containing list
}

func responseRangeMatch(ranges []responseRange, start, end uint64) (int, bool)
```

Keep three sorted private slices on `Log`: `responseMessageRanges`, `responseFailedRanges`, `responseUnavailableRanges`. Keep `responses []logfmt.ProviderJSON`, add `responseDiagnostics []logfmt.ProviderJSONDiagnostic`, and retain `responseOnce sync.Once` / `responseChecked atomic.Bool`. The cache is immutable after publication. No exported pointer refers to cached range storage.

`inspectProviderResponses` invokes `logfmt.InspectProviderJSON(string(l.Data))` exactly once, stores both result slices and builds the indexes. Flatten complete `Fragments`, diagnostic `Ranges`, and diagnostic `Unavailable` separately. Skip `Start == End`; preserve indexes into the original message/diagnostic slices; sort each flattened slice by start, end, then original index. The I1 ownership contract makes nonempty ranges within each class disjoint, so ends are monotonic after sorting. Binary-search the first `end > selectedStart`, then require `start < selectedEnd`. No interval tree or all-capture scan per lookup.

Set `responseChecked.Store(true)` only after the messages, diagnostics and all indexes are complete. During Tasks 1–2, the same `responseOnce.Do` closure must also assign the temporary `responseErr` from the first diagnostic before that final store; neither public response method may assign it afterwards. Quality reads must load the flag before accessing any published field, including `responseErr`. Concurrent `ProviderResponseAt` calls synchronise through the same `sync.Once`; callers never mutate cached results. Task 3 removes the temporary error assignment together with its field and consumer. Do not expose a cache-reset API.

`responseSourceLine` calls the existing `SourceLocation(entry)` to validate the entry and initialise `sourceLineStarts`. Reject negative offsets and offsets exceeding `EndLine-StartLine` before addition. Use the indexed global line start and next start (or `len(Data)`), clipped to the entry's validated byte interval. Exclude one final LF and then one immediately preceding CR; also exclude a final CR without LF, matching I1. Do not sum `Entry.Lines`, split the entire capture again, or reinterpret display text. Return the real global physical line even for an empty byte range.

Selection order is exact:

1. An invalid entry/line request returns `State:"none"`, zero source line and empty details without triggering inspection.
2. A valid physical line triggers the one inspection, including a blank line. Set `SourceLine` and `HasDiagnostics` from that result.
3. A nonempty intersection with complete-message fragments returns `complete`, a copy of that message and a detached fragment slice; `Diagnostic` is nil.
4. Otherwise an intersection with unsuccessful-message `Ranges` returns `invalid`, an empty response and a scalar copy of the owning diagnostic with `Ranges` and `Unavailable` both nil. This includes a pending response aborted by a global ownership failure; its reason remains `ambiguous_ownership`.
5. Otherwise an intersection with `Unavailable` returns `unavailable` and the same scalar diagnostic projection with both range slices nil. A global trigger owns its whole trigger line and tail, as defined by I1.
6. Otherwise return `none`. Do not substitute the first response in the entry, first diagnostic in the capture, or a response on a later line. An empty range never matches.

Diagnostic ranges remain private in the cache and indexes; never copy or return them during status selection. Preserve every scalar field, including code, source locations, counts and syntax offset, so `Diagnostic.Error()` retains exactly the cached diagnostic's safe text. The returned pointer owns an independent value. Status selection must not allocate or copy in proportion to the failed message's ranges or unavailable tail.

The source-line selector is intentionally independent of active TUI filters; the TUI admits the opening position first. Once a verified fragment is selected, its complete message can contain other physical fragments hidden by a current raw scope/filter. This is the existing complete-body modal behaviour, not a change to the raw selection or filter.

```go
func responseRangeMatch(ranges []responseRange, start, end uint64) (int, bool) {
    if start >= end { return 0, false }
    i := sort.Search(len(ranges), func(i int) bool { return ranges[i].end > start })
    if i == len(ranges) || ranges[i].start >= end { return 0, false }
    return ranges[i].index, true
}
```

No user-visible promise of byte-column selection is introduced. Different payload/inline-UI segments on one selected physical line still select that line's verified provider message when a nonempty provider fragment exists. An inline UI event occupying its own line does not select a neighbouring response.

## Quality and JSON v2 contract

The final model type remains a comparable value snapshot:

```go
type ReconstructionQuality struct {
    State       string
    Responses   int
    Diagnostics int
    Code        string
}
```

| State | Condition | Responses | Diagnostics | Code |
| --- | --- | --- | --- | --- |
| `not_checked` | Inspection not yet published | `0` | `0` | empty |
| `complete` | No diagnostics, including a checked capture with no responses | verified count, possibly `0` | `0` | empty |
| `partial` | At least one verified message and at least one diagnostic | positive | positive | `reconstruction_partial` |
| `failed` | Diagnostics and no verified messages | `0` | positive | `reconstruction_failed` |

`Diagnostics` counts diagnostic records, not failed messages or bad lines. A global failure can produce a trigger diagnostic plus several aborted-message diagnostics. Do not add these to static `CaptureQuality.Issues` or claim distinct damaged-response totals. The aggregate code is fixed; the selected response view uses the specific I1 diagnostic for location/reason detail.

Both JSON roots emit `schema_version: 2`; `kind` remains `profile` or `comparison`. The shared `quality.reconstruction` field order is `state`, `responses`, `diagnostics`, `code`. All four keys are mandatory:

| Model state | JSON responses | JSON diagnostics | JSON code |
| --- | --- | --- | --- |
| `not_checked` | `null` | `null` | `null` |
| `complete` | measured count, including `0` | `0` | `null` |
| `partial` | positive count | positive count | `"reconstruction_partial"` |
| `failed` | `0` | positive count | `"reconstruction_failed"` |

All other fields, ordering, nullability, qualification arrays, timing calculations and large-integer behaviour retain the existing documented meanings. No response bodies, per-response ranges or source-derived diagnostic text enter JSON. Export and comparison continue to snapshot quality without initiating reconstruction. A fresh CLI profile/comparison therefore normally reports `not_checked`; an in-process report built after inspection represents the observed state. A previously built report does not change when inspection later completes.

The encoder rejects an unknown state with the existing fixed `profile JSON has invalid reconstruction state` error. It rejects negative counts or any state/count/code combination outside this table with `profile JSON has invalid reconstruction snapshot`, before any bytes are written. The `checked` state is not accepted by v2. No v1 writer, reader, compatibility adapter, version flag or dual emission is added. Historical v1 documents are not advertised as the current CLI format.

## Viewer behaviour and copy

`r` retains its existing Raw Log/list-focus binding. Add `rawResponsePosition() (entry, lineOffset int, ok bool)` beside `openResponse`: use `nextRawEntry` and `rawLogVisible`; honour `raw.topLine` only for the current top entry, using zero for a later first-visible entry. Skip a top entry with an out-of-range positive topLine just as `rawLogLines` does. Use `entryLines` only to mirror the existing raw pane's displayed-line bounds. Return false for an empty scope/filter/capture; no inspection then runs. Do not scan past the first displayed line looking for a response.

`openResponse` calls `ProviderResponseAt` once for that position and keeps all raw state untouched. Existing pretty JSON, decoded multiline `@message`, safe `DisplayText`, search, horizontal scroll, page keys, resize handling and Esc/r return remain in place.

| Selection | Body / notice |
| --- | --- |
| `complete`, no diagnostics | Existing complete-body rendering |
| `complete`, diagnostics elsewhere | Same body; persistent notice `Partial reconstruction: other responses could not be reconstructed. Raw Log remains available.` |
| `invalid` | `This response is incomplete or invalid.` then selected source line and `Diagnostic.Error()`, then `Raw Log remains available. Press Esc or r to return.` |
| `unavailable`, local failure | `This stream is unavailable after an earlier failure.` then selected source line and `Diagnostic.Error()`, then the same Raw Log return sentence |
| `unavailable`, global ownership | `Response ownership is unavailable at this position.` then selected source line and `Diagnostic.Error()`, then the same return sentence |
| `none`, valid source line | `No reconstructed response at this physical line.` then `Source line N.` and the same return sentence |
| No displayed physical line | `No reconstructed response at this position.` and the same return sentence |

Use `Source line N.` for the selected line and the diagnostic's existing safe formatter for start/detection facts. Never display a failed body's bytes or fall back to a complete message from another position. Failure/status panes use title `RESPONSE STATUS`; complete bodies keep `RECONSTRUCTED RESPONSE (N fragments)`, with ` — partial capture` appended when a notice is present.

Add `notice string` to `responseState`. Keep the notice separate from `lines`, so body searching only searches response content. At positive width/height, reserve one clipped notice row above the body when a notice exists. At height 1 show the notice only; keep the body viewport/search state and show it again after enlargement. At height >=2 set the body viewport height to `h-1`; without notice retain `h`. At nonpositive dimensions render an empty string. Display `Partial reconstruction` first so narrow panes retain the qualification. Do not bake the banner into scrollable content where it disappears after paging.

Quality panel copy:

```text
not checked (response reconstruction is lazy)
complete: N responses available
partial: N responses available; D reconstruction diagnostics
failed: 0 responses available; D reconstruction diagnostics
```

Append `Diagnostic counts can include ownership triggers and aborted messages.` for partial/failed outcomes. `qualityHasLimitations` must treat both partial and failed as limitations. Rendering the panel/indicator, loading, filtering and profile/comparison building must never invoke inspection.

### Task 1: Cache outcomes and select the exact physical line

**Files:** Create `internal/model/response.go`, `response_benchmark_test.go`; modify `internal/model/log.go`, `provider_response_test.go`. Read `location.go`, I1 outcome code and existing response/quality tests.

**Interfaces:** Consume `logfmt.InspectProviderJSON(string) logfmt.ProviderJSONResult`, `Log.SourceLocation(uint32) (SourceLocation, bool)` and the existing lazy publication fields. Produce `ProviderResponseSelection`, `Log.ProviderResponseAt`, `inspectProviderResponses`, `responseSourceLine`, `responseRangeMatch` exactly as above. Store responses/diagnostics and the three indexes. Existing quality wire states and the entry API remain until the coordinated final migration in Task 3.

- [ ] **Step 1: Add behavioural tests.** Use real `Load` from synthetic files for normal ownership/recovery. Include this direct model fixture to prove two complete bodies within one authoritative entry range are selected independently; it deliberately supplies a broad entry index, not a claim about ordinary scanner grouping:

```go
func TestProviderResponseAtSelectsPhysicalLineWithinEntry(t *testing.T) {
    const head = "2026-09-11T00:00:00.000Z [DEBUG] provider.a: "
    source := head + `{"first":1}` + "\n" + head + `{"second":2}` + "\n"
    l := &Log{Data: []byte(source), Entries: []logfmt.Entry{{Len: uint32(len(source)), Lines: 2}}}
    got := l.ProviderResponseAt(0, 1)
    if got.State != "complete" || got.SourceLine != 2 || got.Response.Text != `{"second":2}` {
        t.Fatalf("selected wrong physical response: %+v", got)
    }
    if string(l.Data) != source { t.Fatal("source changed") }
}
```

Add table cases for good A/bad B/good A; pending A/bad B/finish A; complete A/incomplete A at EOF; bad A/apparent A/good B; invalid UTF-8; inline UI-only line between A fragments; global trigger with earlier complete and pending streams; ordinary content within a multi-line entry; blank and CRLF/final-CR lines; invalid ordinals/negative and excessive line offsets; and an entry with more than 65,535 physical lines. Assert exact selected body/state/line/diagnostic code, no unrelated fallback and original-slice equality. On global failure, previous pending ranges select invalid and trigger/tail lines select unavailable. Include a quarantined payload followed by a different ordinary entry's continuation to prove its ordinary line is none.

After selecting a complete response, mutate the returned fragment slice and repeat the selection. For every invalid/unavailable selection, require a non-nil diagnostic with both range slices nil, and compare all scalar fields and `Error()` with the owning I1 diagnostic. Mutate the returned diagnostic's code/count/location fields and assign fresh slices to its range fields, then repeat selection; it must again return the original scalar facts and nil ranges. Check invalid-start, local unavailable-tail, global-trigger and global unavailable-tail positions. Cached diagnostics must retain their full original ranges, and later lookups must still select correctly. Internal results, source bytes and entries must remain equal to their baseline. Concurrent first lookups must return consistent independent values; existing quality accessors must not see partially published fields.

Add this interim regression before migrating quality in Task 3. It inspects through the new API first, without invoking the old entry API:

```go
func TestProviderResponseAtPublishesInterimFailedQuality(t *testing.T) {
    const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
    source := head + "a: {\"ok\":1}\n" + head + "b: {\"broken\":]}\n"
    l := &Log{Data: []byte(source), Entries: []logfmt.Entry{{Len: uint32(len(source)), Lines: 2}}}
    if got := l.ProviderResponseAt(0, 0); got.State != "complete" || !got.HasDiagnostics {
        t.Fatalf("selection = %+v", got)
    }
    want := ReconstructionQuality{State: "failed", Code: "reconstruction_failed"}
    if got := l.ReconstructionQuality(); got != want { t.Fatalf("quality = %+v", got) }
}
```

Run this case with malformed-only input as well (selection `invalid`, same interim quality). On fresh logs of both fixtures, release quality readers and first `ProviderResponseAt` calls from one start channel. Before publication, readers may see only the exact `not_checked` zero snapshot; afterwards, only the exact interim `failed` snapshot above. Join all workers and assert final `failed`. Then exercise the old entry API concurrently with quality readers on that inspected log to catch any post-publication assignment. Use finite iterations and a wait group, without sleeps; check worker errors in the test goroutine. In Task 3 migrate these tests to final `partial` with one response/one diagnostic for the mixed fixture and `failed` with zero responses/one diagnostic for malformed-only input; remove the old-API phase while retaining concurrent first-selection coverage.

- [ ] **Step 2: Observe behavioural RED.** Add compiling declarations only if needed, then run `go test ./internal/model -run 'TestProviderResponseAt' -count=1`. Record wrong/empty selected response assertions, not only undefined symbols.
- [ ] **Step 3: Implement the shared cache and range selection.** Follow the exact contracts above. Copy fragments only for a selected complete message; project failure diagnostics to scalar facts:

```go
func (l *Log) ProviderResponseAt(entry uint32, lineOffset int) ProviderResponseSelection {
    start, end, line, ok := l.responseSourceLine(entry, lineOffset)
    selection := ProviderResponseSelection{State: "none"}
    if !ok { return selection }
    l.inspectProviderResponses()
    selection.SourceLine = line
    selection.HasDiagnostics = len(l.responseDiagnostics) != 0
    if i, ok := responseRangeMatch(l.responseMessageRanges, start, end); ok {
        selection.State, selection.Response = "complete", l.responses[i]
        selection.Response.Fragments = append([]logfmt.JSONFragment(nil), selection.Response.Fragments...)
        return selection
    }
    for _, candidate := range []struct { state string; ranges []responseRange }{
        {"invalid", l.responseFailedRanges}, {"unavailable", l.responseUnavailableRanges},
    } {
        if i, ok := responseRangeMatch(candidate.ranges, start, end); ok {
            d := l.responseDiagnostics[i]
            d.Ranges = nil
            d.Unavailable = nil
            selection.State, selection.Diagnostic = candidate.state, &d
            return selection
        }
    }
    return selection
}
```

Until Task 3, route the current `ProviderResponse(e)` through `inspectProviderResponses`; the old method only calls the helper and reads the published error/messages. Replace its old `responseOnce.Do` block with that helper call; never nest a call to the same `sync.Once` inside itself. Inside the helper's `responseOnce.Do` closure, after storing the outcome and building all three indexes, finish publication with exactly this ordering:

```go
if len(l.responseDiagnostics) != 0 {
    d := l.responseDiagnostics[0]
    d.Ranges, d.Unavailable = nil, nil
    l.responseErr = d
}
l.responseChecked.Store(true)
```

The zero-value error remains nil for diagnostic-free input. Clear the temporary error's slice fields so returning that error from the old method cannot expose cached range storage; its safe error text remains identical. Extend Task 1's old-API phase to assert the returned diagnostic error has nil ranges. Neither response method writes the error outside this closure, including on repeated calls. This keeps current TUI/quality consumers correct even when `ProviderResponseAt` performs the first inspection. Do not add a second strict reconstruction pass. Task 3 removes this temporary assignment, its field and the old method together; it is not an additional supported API or compatibility feature.

- [ ] **Step 4: Verify and measure.** Run `go test ./internal/model ./internal/tui ./internal/profile -count=1` and `go test -race ./internal/model -run 'TestProviderResponseAt' -count=1`. Add `BenchmarkProviderResponseAtFirst` and `BenchmarkProviderResponseAtRepeated`, with 1,000 and 10,000 synthetic groups of good A / bad unique B / apparent B restart; alternate lookups of a complete, invalid and unavailable last-group line. Build input and entries outside measurement. First-access iterations construct a fresh `Log` with immutable fixture Data/Entries and no copied sync primitives; repeated access uses one inspected log. Validate expected selection before timing, `ReportAllocs`, and assign the returned selection to a package benchmark sink.

Also add `BenchmarkProviderResponseAtFailureTail` with separate local/global sub-benchmarks and tail lengths 1,000, 10,000 and 100,000. Construct these real-parser sources outside measurement (`tailLines` is the sub-benchmark's length):

```go
const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
localSource := head + "a: {\"broken\":]}\n" +
    strings.Repeat(head + "a: {\"apparent_restart\":1}\n", tailLines)
globalSource := head + "a: {\"pending\":\n" + head + ": {\"unknown\":1}\n" +
    strings.Repeat("ordinary tail\n", tailLines)
```

Use the real loaded entry index to resolve the requested physical lines before timing, including a continuation offset when the final global-tail line shares an entry. Pre-inspect each log. For local input, measure the invalid first line and unavailable final tail line separately; the owning diagnostic must have `tailLines` unavailable ranges. For global input, measure the aborted invalid first line, unavailable trigger line and unavailable final tail line separately; the trigger diagnostic must have `tailLines+1` unavailable ranges. Assert these fixture sizes and expected selection states before timing so the benchmark cannot silently measure an ordinary/no-match path. Each status result must have a diagnostic with nil range slices. Keep validation, fixture construction and first inspection outside repeated-lookup timing; use `ReportAllocs` and the same package sink. Run `go test ./internal/model -run '^$' -bench BenchmarkProviderResponseAt -benchmem -count=1`. Record ns/op, B/op and allocs/op for each case: repeated status-selection B/op and allocs/op must remain bounded independently of tail length; investigate any growth rather than accepting an aggregate number. Do not impose a wall-clock threshold. First inspection and selected complete-message fragment copying have separate costs.
- [ ] **Step 5: Review, cleanup and signed commit.** Independent task review and separate test cleanup; resolve findings. Commit `Resolve recovered responses by physical source line`.

### Task 2: Integrate recovered responses into the existing modal

**Files:** Modify `internal/tui/response.go`, `response_test.go`; add response integration tests in that test file or a focused `response_recovery_test.go`. Read `rawlog.go`, `model.go`, `history.go` and existing modal/search tests.

**Interfaces:** Consume `Log.ProviderResponseAt(uint32, int) ProviderResponseSelection` and the exact selection states above. Produce `rawResponsePosition() (entry, lineOffset int, ok bool)` and `responseState.notice string`; preserve `openResponse`, response key handlers and title/render interfaces used by the workbench.

- [ ] **Step 1: Add failing modal tests.** A synthetic broad-entry fixture with two timestamped complete responses and `raw.topLine=1` must open the second body. A real loaded good A/bad B/good A fixture must open both A bodies with a recovery notice and select B as invalid. Add these assertions through real `Model.Update`/`r`:

```go
before := m.raw
responseKey(&m, "r")
if !strings.Contains(m.response.notice, "Partial reconstruction") {
    t.Fatal("recovered body lacks the capture qualification")
}
responseKey(&m, "j")
if !strings.Contains(m.renderResponse(60, 3), "Partial reconstruction") {
    t.Fatal("qualification scrolled out of view")
}
m.Update(tea.KeyMsg{Type: tea.KeyEsc})
if !reflect.DeepEqual(m.raw, before) { t.Fatal("response changed raw return state") }
```

For this assertion body, write the real good A/bad B/good A fixture to `t.TempDir`, call `model.Load`, then construct a value with `m := New(l, path)`, set a 100×30 `tea.WindowSizeMsg`, `m.setView(ViewRawLog)`, `m.pane = PaneList` and `m.raw.top = 2`. The snippet uses a `Model` value, so `&m` is the existing helper's required pointer. Independently assert the selected pretty body excludes the other response. For the broad-entry test only, load two timestamped complete responses then replace the test model's entry index with one synthetic entry spanning the unchanged bytes; supply a valid component interner from Load. This deliberately exercises the model's entry-range contract, not an expected scanner grouping. Test first-visible entry after a filtered top entry; an empty filter result and empty scope; invalid topLine; ordinary continuation after a complete response; an inline UI-only physical line; invalid/unavailable/global-trigger status and safe numeric locations; and no source sentinel in any failure view. Verify the selected message can include verified fragments outside a raw scope without changing that scope.

Keep existing raw/response search and control-escaping regressions. Add recovered-body search across decoded multiline `@message`, multiple occurrences, wide Unicode and escaped controls; page/scroll/resize and return must preserve raw top, topLine, column, match, query, scope, filters and navigation history. Search and horizontal scroll select the same physical line rather than treating display columns as byte offsets. Test notice rows at widths 20/60/100 and heights 0/1/2/8; enlarging a one-line pane restores the body/search state.

- [ ] **Step 2: Observe RED.** `go test ./internal/tui -run 'TestResponse.*(Recovery|Physical|Notice|Unavailable|Position)' -count=1`; require wrong-body, missing-notice or wrong-status assertions against current modal behaviour.
- [ ] **Step 3: Implement position and presentation.** Replace the entry-wide query in `openResponse` with `rawResponsePosition` followed by one `ProviderResponseAt` call. Branch by the table above, then reuse the existing pretty/decoded body processing and terminal escaping. Add the persistent notice row without putting it in `response.lines` or changing raw/history state:

```go
if r.notice != "" {
    if h == 1 { return clipWidth(r.notice, w) }
    // Set the existing body viewport to w by h-1 before composing these rows.
    return clipWidth(r.notice, w) + "\n" + r.viewport.View()
}
return r.viewport.View()
```

Handle nonpositive dimensions before this block, and keep existing offset clamps/search anchors applied to the body viewport. Use `Diagnostic.Error()` only after checking the diagnostic pointer. A malformed internal selection with no diagnostic must display fixed `Response details are unavailable.` rather than panic or disclose source.
- [ ] **Step 4: Verify.** `go test ./internal/tui ./internal/model -count=1`. Review any intentional golden change using `go test ./internal/tui -update`, `scripts/read-golden.sh <changed-name>` and the raw diff; do not regenerate unrelated goldens. Existing golden output need not change if it does not open the response modal.
- [ ] **Step 5: Review, cleanup and signed commit.** Independent task review and separate cleanup; resolve findings. Commit `Show recovered responses and position-specific failures`.

### Task 3: Publish lazy quality and the explicit JSON v2 schema

**Files:** Modify model `quality.go`, `quality_test.go`, `log.go`, `provider_response_test.go`; TUI `quality.go`, `quality_test.go`; profile `json_data.go`, `json_data_test.go`, `json_contract_test.go`, `json_test.go`, `data_test.go`, `comparison_data_test.go`, `comparison_json.go`, `comparison_json_test.go`; `cmd/tfli/profile_json_test.go`, `cmd/tfli/comparison_test.go`; README and four JSON schema documents as described above.

**Interfaces:** Consume the immutable response/diagnostic cache and `responseChecked`; produce `ReconstructionQuality{State, Responses, Diagnostics, Code}` and the exact JSON v2 table above. Keep `Log.ReconstructionQuality()`, `profile.Build`, comparison building and encoder signatures unchanged. Remove `Log.ProviderResponse(e)` and `responseErr` after migrating all remaining test callers to `ProviderResponseAt`; no entry-wide response API remains in the final tree.

- [ ] **Step 1: Add state and lazy-snapshot tests.** Extend real model fixtures to cover not checked, complete with zero/positive messages, partial with independent recovery, and failed with no messages. Compare static `CaptureQuality`, Data and Entries before/after. Read quality concurrently with first inspection and require whole snapshots only. Mutating returned selections must not change later counts. Rendering/opening the quality panel and its indicator must leave fresh inspection not checked. Check partial/failed limitations using an otherwise complete synthetic quality summary so unrelated missing-context limitations cannot hide the branch under test.

```go
want := ReconstructionQuality{
    State: "partial", Responses: 2, Diagnostics: 1, Code: "reconstruction_partial",
}
if got := l.ReconstructionQuality(); got != want { t.Fatalf("quality = %+v", got) }
```

For both profile and comparison, build a report before actual `ProviderResponseAt`, then a report after; require the first snapshot to remain not checked and the second to be partial. Decoding both JSON kinds must show root version 2 and every mandatory reconstruction key with exact values/nulls from all four rows. Test complete-zero distinctly from not checked, and failed-zero distinctly from unknown. Reject bad state, negative counts, partial-with-zero, complete-with-diagnostics and mismatched codes before writing. Retain actual CLI output-file protection and invalid-UTF-8 tests.

- [ ] **Step 2: Observe RED.** Run `go test ./internal/model ./internal/tui ./internal/profile ./cmd/tfli -run 'Test.*(Reconstruction|Recovery|JSON)' -count=1`. Add the new `Diagnostics` declaration if required before measuring behavioural failure. Record wrong quality/state/version/null assertions.
- [ ] **Step 3: Implement model and quality text.** Use the same cache; no new inspection call:

```go
func (l *Log) ReconstructionQuality() ReconstructionQuality {
    if !l.responseChecked.Load() { return ReconstructionQuality{State: "not_checked"} }
    q := ReconstructionQuality{State: "complete", Responses: len(l.responses), Diagnostics: len(l.responseDiagnostics)}
    if q.Diagnostics > 0 {
        q.State, q.Code = "failed", "reconstruction_failed"
        if q.Responses > 0 { q.State, q.Code = "partial", "reconstruction_partial" }
    }
    return q
}
```

Update TUI copy and indicator per contract. Remove the temporary entry-wide method/error field and the error assignment inside `inspectProviderResponses` together; retain the final atomic store after all remaining cache fields are ready. Migrate retained regression tests to the intended physical line and the final quality table, including Task 1's mixed and malformed-only publication cases; keep their source-preservation, failure-safety and concurrency assertions.

- [ ] **Step 4: Implement coordinated JSON v2.** Add `Diagnostics *int` between Responses and Code in private `jsonReconstruction`, change both root constructors to version 2, and validate/map the state table in the shared helper before marshalling. Check not-checked zeros/empty code; complete nonnegative responses with zero diagnostics/empty code; partial positive counts/exact partial code; failed zero responses/positive diagnostics/exact failed code. Invalid state and invalid snapshot use the fixed distinct errors above. Update exact root, nested key, integer/null and CLI assertions together.

```go
type jsonReconstruction struct {
    State       string  `json:"state"`
    Responses   *int    `json:"responses"`
    Diagnostics *int    `json:"diagnostics"`
    Code        *string `json:"code"`
}
```

Write complete v2 documents from the current v1 contracts, changing only the version and reconstruction section specified here. Mark v1 documents historical and update README's current-format links to v2. Search source/tests/docs for active `checked`, `schema_version: 1` and v1 current-format claims; classify matches instead of blindly replacing historical plans or unrelated timeline `partial` states. In particular comparison capture quality must use exactly profile v2's fields/nulls.

- [ ] **Step 5: Verify, review, cleanup and commit.** Run `go test ./internal/model ./internal/tui ./internal/profile ./cmd/tfli -count=1`; independent task review and separate cleanup. Commit `Report partial reconstruction in quality and JSON v2`.

### Task 4: Verify Boundary I end to end and document delivery

**Files:** Create `testdata/response-recovery.log`; extend focused TUI integration tests if the prior tasks do not already cover the exact journey; update README response guidance, this plan and the investigation-workflows spec. Preserve strict scrub tests in `internal/scrub/fragments_test.go` and `cmd/tfli/scrub_test.go`.

**Interfaces:** Consume the finished model selection, viewer and quality contracts plus existing actual `scrub.Scrub` and CLI commands. No new application interface.

- [ ] **Step 1: Add the sanitised journey fixture.** Use these physical lines, including a final newline:

```text
2026-09-11T00:00:00.000Z [DEBUG] provider.a: {"@message":"first response\nneedle first\nneedle second"}
2026-09-11T00:00:00.001Z [DEBUG] provider.b: {"broken":]}
2026-09-11T00:00:00.002Z [DEBUG] provider.a: {"@message":"recovered response\nneedle third"}
2026-09-11T00:00:00.003Z [DEBUG] provider.b: {"apparent_restart":true}
2026-09-11T00:00:00.004Z [INFO] terraform: ordinary entry
ordinary continuation
2026-09-11T00:00:00.005Z [DEBUG] provider.a: {"unfinished":
```

The expected outcome is two verified responses, one delimiter diagnostic and one incomplete diagnostic; quality becomes partial with responses 2 and diagnostics 2 only after inspection. First/third physical lines select their own complete bodies, second is invalid, fourth unavailable, fifth/sixth none, seventh invalid.

- [ ] **Step 2: Exercise the complete journey through actual model and TUI APIs.** Check initial quality remains not checked after drawing the panel; search Raw Log for `recovered response`, open `r`, search the decoded response, close and verify exact raw occurrence plus history. Select invalid/unavailable/ordinary positions and verify distinct safe messages. Then open quality and assert exact partial counts. Call the actual `scrub.Scrub` on the same source after inspection and require an error plus exact zero result; this guards inspection never authorising partial output. Existing CLI partial-publication regressions remain mandatory. Add a real `Model.Update` integration test only for uncovered cross-component behaviour, not a duplicate of each task's cases. The finished journey may already pass after Tasks 1–3; record that as regression evidence. If it exposes a production defect, capture behavioural RED and fix the root cause before final validation.

- [ ] **Step 3: Inspect the real terminal.** Run `go run ./cmd/tfli testdata/response-recovery.log` in a PTY, select Raw Log with `6`, focus its list, and execute the journey above at 100×30 and 60×9. Inspect `r`/Esc return, persistent notice, decoded body search, error/ordinary views and quality. Resize while the response is open. Save only sanitised terminal evidence. Add README wording for physical-line selection, recovery notices, lazy quality and strict scrub rejection.

- [ ] **Step 4: Run final validation and independent whole-branch review.** Run the complete matrix below once on the final application tree; inspect all output, intentional goldens and decoded profile/comparison JSON. Run separate test cleanup after the last implementation/fix pass. Enumerate every spec requirement with evidence in the execution record. Do not mark Boundary I complete until all tasks, review findings and verification have been handled.

- [ ] **Step 5: Commit verified completion.** Commit `Complete response recovery viewer integration` with a signature. Record actual APIs, JSON decision, terminal evidence, test commands and review results; update the spec to mark Boundary I complete only when true. Integration remains Dan's decision.

## Final validation

Run separately from the repository root:

```text
go test -race -count=1 ./...
go build ./...
gofmt -d .
go mod tidy -diff
go mod verify
golangci-lint run --timeout=5m
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=amd64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/tfli-i2-linux-amd64 ./cmd/tfli
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=arm64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=arm64 go build -trimpath -o /tmp/tfli-i2-linux-arm64 ./cmd/tfli
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=amd64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=amd64 go build -trimpath -o /tmp/tfli-i2-darwin-amd64 ./cmd/tfli
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=arm64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=arm64 go build -trimpath -o /tmp/tfli-i2-darwin-arm64 ./cmd/tfli
```

Local cross-builds are not remote CI. Use the built host CLI to render and decode one complete profile and one comparison; both roots must be version 2, each fresh reconstruction snapshot not checked with three null details, and each stream exactly one JSON document followed by EOF. Retain source protection and all strict scrub publication regressions.

## Spec coverage and task dependencies

| Requirement | Owner/evidence |
| --- | --- |
| One parser; independent retained messages and safe diagnostic ranges | I1, consumed without grammar changes by Task 1 |
| Selected physical position, multiple bodies within one entry | Task 1 line/index tests; Task 2 raw-position parity |
| Invalid response vs unavailable stream vs ordinary position | Tasks 1–2 exact states, source lines and messages |
| Recovered full body plus notice | Task 2 complete rendering and persistent banner |
| Scrolling/search/controls/return/history | Existing modal regressions plus Tasks 2/4 recovery journeys |
| Lazy complete/partial/failed quality | Task 3 cache publication, panel/indicator and export tests |
| Profile/comparison contract remains explicit | Task 3 version 2 mapping, documents and exact decoded assertions |
| Strict scrub refusal even after viewer inspection | I1 regressions plus Task 4 same-source inspection/scrub journey |
| Performance, races, source immutability and detachment | Tasks 1/3 benchmarks, concurrent lookup/snapshot tests and final race suite |

Tasks are sequential: 1 supplies lookup; 2 switches the viewer; 3 coordinates canonical quality and both JSON writers while removing the temporary entry API; 4 validates the finished journey. Each intermediate commit must build and pass its current consumer expectations. No task may silently change another task's interface.

## Draft evidence and self-review

Baseline at `04d28da`: `go test ./...` passes all eleven packages (cached), and `go build ./...` passes. These checks validate the existing application, not the proposed I2 implementation. The draft inspected I1's outcome contract, model response/cache/location code, raw rendering/search/visibility, modal navigation, quality publication, shared JSON projection and both published v1 documents.

Self-review checked exact existing/new paths, type/field agreement, interim consumer expectations, the physical-line interpretation, empty-range handling, both JSON kinds and all spec-coverage rows. It made the temporary cache migration explicit to prevent recursive `sync.Once` use and supplied the precise TUI fixture setup. The document validator checked relative links, fences and unfinished-value markers; all ten Go examples parse with `gofmt`. Those checks do not type-check proposed APIs or validate future behaviour. Independent plan review and approval remain separate gates. No application code, new fixture, JSON v2 document or implementation test has been created by drafting this plan.

### Peer-review corrections, 11 September 2026

- `I2-PAR-1`: the cache contract and Task 1 publication example place temporary `responseErr` assignment inside the shared once closure before its atomic store. Task 1 requires mixed/malformed first-access and concurrent quality regressions; Task 3 removes the temporary assignment and migrates those assertions together with the consumer.
- `I2-PAR-2`: the selection contract, dispatch example and detachment tests return only scalar diagnostic facts with nil ranges. Full cached ranges remain available to the indexes. Separate local/global failure-tail benchmarks measure invalid and unavailable selections at 1,000, 10,000 and 100,000 tail lines, checking that repeated status allocation does not scale with the tail.

The corrected document passes link, fence and unfinished-value checks; all thirteen Go examples parse with `gofmt`. These are plan checks only. Runtime race, allocation and behaviour evidence belongs to the implementation tasks above.
