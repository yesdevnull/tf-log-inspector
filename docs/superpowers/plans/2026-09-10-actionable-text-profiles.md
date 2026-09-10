# Actionable Text Profiles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Make text profiles lead to exact source observations and explain observed concurrency, with explicit list limits and unchanged whole-capture totals.

**Architecture:** Consume F1's complete `profile.Report`; text rendering alone formats, escapes and limits rows. Keep CLI validation in `cmd/tfli` and retain the existing protected output-file path. No JSON, comparison or new attribution behaviour is introduced.

**Tech Stack:** Existing Go 1.25+ standard flag/testing packages and current dependencies.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), item 5/shared rules. Requires completed [F1 profile data and analysis](2026-09-10-profile-data-analysis.md).

## Global Constraints

- “UI-hook duration measures a resource operation. RPC duration measures a provider call.”
- “An inferred address always retains its confidence.”
- “Terminal rendering escapes untrusted controls.”
- “Summary totals always cover all eligible observations; headings show truncation.”
- “Whole-log quality facts remain labelled as whole-log facts.”
- “Such a gap does not prove Terraform was idle, nor does a long active span prove it caused all other work to wait.”
- Use current dependencies/toolchain and `/Users/dan/.codex/bin/codex-git`; signed commits mandatory, signing failure is a hard stop. No push or merge implied.
- TDD with real parsing and output, independent review and separate test cleanup after implementation.

---

## Status and decisions

Dan approved both Boundary F plans and subagent implementation on 10 September 2026. F1 passed independent review before F2 execution.

1. `--limit N` applies only to text `--profile` in this boundary. Default 20; zero means all; negative, overflowing and malformed values fail before loading input or opening/truncating output. Explicit `--limit=20` or `--limit=0` with other modes also fails. Track presence using `FlagSet.Visit`; value alone cannot establish presence.
2. Apply the limit independently to provider/type rankings, slowest RPC/UI lists and the interval list. Size/quality/denominators/analysis use all admitted or eligible positioned observations. A truncated heading says `top N of M`; an untruncated heading remains plain. No new resource aggregate text table is required by item 5.
3. Preserve the existing privacy/logging/UI-rounding warnings and whole-capture quality output, including no-span reports and writer errors. Display each saturated UI duration and affected UI type total with `≥`, not just a global warning.
4. Every shown call/operation includes exact physical source lines when available. Show full escaped provider/type/method/address identifiers, using an indented continuation where necessary rather than dropping suffixes. Unknown source says unavailable. No input basename/path parameter is added to the text API; physical lines refer to the single input log.
5. Named RPC rows carry confidence; ambiguous rows carry candidate count without an address; unresolved rows say Unattributed when capture context exists, otherwise no address context. UI addresses are observed and must not receive RPC-confidence labels.
6. Report concurrency for the selected tier, including UI-only logs. State positioned/excluded counts, duration totals and reason codes; partial/unavailable are distinct from zero. Preserve clamped-start qualification. Label the window as zero to latest positioned end and print its tier clock origin when known; never imply it covers the whole Terraform run.
7. Present busy union/fraction, peak and full positioned reported-duration sum separately. For zero window print unavailable ratios. Rank complete qualifying intervals by extent descending, then start/end ascending. A zero-running interval says `no observed RPC work` or `no observed UI work`; others show running range versus observed peak and the longest observed active observation/source. Never print dependency/blocking claims.

## File responsibilities

| File | Responsibility |
| --- | --- |
| `internal/profile/profile.go`, `profile_test.go` | Complete-data renderer, full identities, limits and warnings |
| New `internal/profile/intervals.go`, `intervals_test.go` | Temporal summary/interval text and qualified source references |
| `cmd/tfli/main.go`, `main_test.go` | `--limit`, early mode validation and output handling |
| `internal/tui/timeline_test.go` | Update existing profile parity call for explicit options |
| `README.md` | Profile examples, limit semantics and interpretation |

## Task 1: Render actionable observations and complete temporal evidence

**Interfaces:** Consume all F1 report types and `Build`. Replace internal package call sites together; do not add a compatibility overload. Produce:

```go
const DefaultLimit = 20
type TextOptions struct { Limit int } // zero means all
func Render(w io.Writer, l *model.Log, options TextOptions) error
func renderReport(w io.Writer, report Report, options TextOptions) error
func writeTimeline(b *strings.Builder, report Report, limit int) error
```

`Render` validates `Limit`, builds once and calls `renderReport`. `renderReport` also validates options for direct package tests, builds output before writing, and returns writer errors unchanged. It must not mutate any report slices while sorting/limiting. Retain the existing `strings.Builder`/single-write structure; no streaming framework.

- [x] **Step 1: Write failing behaviour tests before replacing calculations.** Add a test using F1's report and explicit options:

```go
func TestTextProfileOpensExactObservationEvidence(t *testing.T) {
    l, err := model.Load("../../testdata/resources-accounting.log")
    if err != nil { t.Fatal(err) }
    var out strings.Builder
    if err := Render(&out, l, TextOptions{Limit: 0}); err != nil { t.Fatal(err) }
    got := out.String()
    for _, want := range []string{"aws_instance.a", "contained", "source: line 4", "source: line 5", "observed busy"} {
        if !strings.Contains(got, want) { t.Errorf("missing %q in report:\n%s", want, got) }
    }
}
```

Update old `Render` call sites to `TextOptions{Limit: DefaultLimit}` to compile, without relaxing assertions. Confirm a behavioural RED for absent source/attribution/busy output. Add focused tests for long identifiers that share prefixes, embedded newline/ESC in decoded fields, all confidence states, multiline source ranges, missing source, saturation marks, UI-only analysis, no admitted durations, preferred RPC with no positions, partial position exclusions and unavailable zero-window fractions. The source expectation must be an independently counted physical line in the fixture, not a value computed with the same helper as the renderer.

Add a parsed mixed-log case whose RPC and UI origins differ: the RPC interval section must print the RPC origin, never the UI origin. Pair it with UI-only and unknown-origin cases; assert the timestamp or explicit unavailable text against independently chosen fixture values. Add limit tests with more than 20 parsed observations and multiple providers/types: assert `Limit=1` restricts each applicable list, `Limit=0` includes all, totals are identical and heading denominators are full counts. Preserve writer-error tests using the existing failing writer. A writer returning a short count and nil error must be surfaced as `io.ErrShortWrite` rather than successful output.

- [x] **Step 2: Run RED.** `go test ./internal/profile -count=1`; record exact failed behaviours before implementation. Do not count the mechanical signature compilation failure as sufficient RED.

- [x] **Step 3: Render F1 data only.** Entry flow:

```go
func Render(w io.Writer, l *model.Log, options TextOptions) error {
    if options.Limit < 0 { return errors.New("profile limit must be non-negative") }
    report, err := Build(l)
    if err != nil { return err }
    return renderReport(w, report, options)
}
```

Replace renderer calls to rollup/join/sorting calculations with `Report.Providers`, `.Types`, `.RPCRanking`, `.UIRanking` and `.Timeline`. A local helper may return `min(limit, length)` for positive limits, otherwise length. Never slice original report fields in place. Emit heading `NAME (top N of M)` only for truncation. Keep numerical summary facts outside limited loops.

Use these observation continuation shapes, with each untrusted string passed through `logfmt.DisplayText`:

```text
  12ms  ReadResource  aws_instance  registry.terraform.io/hashicorp/aws
    source: lines 12-14
    resource: module.web.aws_instance.app[0] (contained)

  12ms  ReadResource  aws_instance  registry.terraform.io/hashicorp/aws
    source: line 18
    resource: ambiguous (2 candidates)

  ≥4294967.3s  apply  aws_instance
    resource: module.web.aws_instance.app[0] (observed UI)
    source: line 24
```

Use full escaped identifiers in observation rows; retain existing column formatting where it fits, but remove lossy truncation from identity output. For provider/type rankings, retain full identity in an indented continuation when the bounded column needs abbreviation. Update alignment tests to inspect each numeric row and its identity continuation; preserve their distinction guarantees. Do not remove diagnostic/privacy warnings.

For source references use `Observation.Source.StartLine`/`EndLine`; never `Entry.Lines`, entry ordinal plus one, or interned request IDs. Named attribution includes Contained/Likely/Overlapping exactly as supplied; ambiguous/unattributed paths cannot accidentally append `Attribution.Address`. UI saturation prints `≥` on observation and type total, with existing rounding caveats. Keep no-context separate using `Report.HasContext`.

`writeTimeline` uses `Analysis.Metrics == nil` for unavailable and `Analysis.Timing.ExcludedCount > 0` for partial. Label the tier via `Timeline.Tier`; do not manufacture RPC analysis for UI-only captures. Select the matching `report.Quality.RPC.Origin` or `.UI.Origin`: print `clock origin: ` followed by `origin.UTC().Format(time.RFC3339Nano)` when present, otherwise `clock origin: unavailable`. Label interval offsets as milliseconds from that tier origin. A nil tier has no origin or interval section. This origin label also appears when a chosen tier has unavailable metrics. Show each exclusion reason in sorted key order. When metrics exist, print window, peak, busy union and fraction, positioned reported-duration sum, and summed/window ratio only when window is positive. Show clamping note if any positioned observation is clamped.

Copy and rank `Analysis.Intervals` by `(duration descending, StartMs ascending, EndMs ascending)`, then apply limit. Resolve nonnegative `Blocking` through `Timeline.PositionedIndices` and the chosen original observation array. `writeTimeline` checks both index bounds and returns `errors.New("profile interval observation index out of range")` for an invalid mapping; `renderReport` propagates that error before writing output. Zero-running intervals never index `-1`. State that observed gaps/active spans do not establish Terraform idleness or a dependency-critical path. No qualifying intervals is distinct from unavailable metrics.

After building text, propagate output failure including short writes:

```go
text := b.String()
n, err := io.WriteString(w, text)
if err != nil { return err }
if n != len(text) { return io.ErrShortWrite }
return nil
```

- [x] **Step 4: Verify and review.** `go test ./internal/profile ./internal/tui -count=1`; inspect actual reports from RPC, UI-only, saturated and no-span sanitised fixtures. Independent review checks totals/limit separation, physical locations, source identity after position exclusions, escaping and all unavailable/zero distinctions. Separate cleanup follows.
- [x] **Step 5: Commit.** Signed commit `Render actionable timing profiles with source evidence` and record RED/GREEN/review results.

## Task 2: Expose and validate the text-profile limit

**Files:** Modify `cmd/tfli/main.go`, `main_test.go`, `README.md`.

**Interfaces:** Change `runProfile` to `func runProfile(path, outPath string, stdout io.Writer, options profile.TextOptions) error`; consume task 1 `Render`. Existing `writeReport` owns same-file/symlink/hard-link protection and close-error handling.

- [x] **Step 1: Add failing CLI tests.** Use a real parsed fixture, capture both writers, and a path that would not exist if mode validation is correct:

```go
func TestLimitRejectedOutsideProfileBeforeOpeningInput(t *testing.T) {
    for _, args := range [][]string{
        {"--limit=0", "missing.log"},
        {"--diagnose", "--limit=20", "missing.log"},
        {"--scrub", "--limit=1", "-o", "unused.log", "missing.log"},
    } {
        var stdout, stderr bytes.Buffer
        err := run(args, &stdout, &stderr)
        if err == nil || !strings.Contains(err.Error(), "--limit applies only to --profile") {
            t.Fatalf("args %v: error %v", args, err)
        }
        if stdout.Len() != 0 || stderr.Len() != 0 { t.Fatal("unexpected output before mode validation") }
    }
}
```

Use `t.TempDir` for the scrub output sentinel instead of relying on the working directory. Add `--profile --limit=-1` with an existing sentinel output: no input load/truncation. Capture and assert malformed/overflow diagnostics and escaped controls. Test help documents default/zero/mode applicability; preserve existing `--version`/help precedence. Run real profiles with omitted limit, `--limit=1` and `--limit=0`, comparing output counts and unchanged totals. Retain same-file, hard-link and symbolic-link refusal tests for profile output; no TUI mock is needed for invalid flags because validation must precede dispatch.

- [x] **Step 2: Run RED.** `go test ./cmd/tfli -run 'Test.*Limit' -count=1`; confirm failures from the absent flag/semantics.

- [x] **Step 3: Add flag and early validation.** Register:

```go
limit := fs.Int("limit", profile.DefaultLimit, "maximum rows per text profile list (0 means all; --profile only)")
```

Extend the existing `fs.Visit` call to record `limitSet`. After existing mode exclusivity checks and before mode dispatch/input/output access:

```go
if limitSet && !*doProfile {
    return errors.New("--limit applies only to --profile")
}
if *limit < 0 { return errors.New("--limit must be non-negative") }
```

Pass `profile.TextOptions{Limit: *limit}` through `runProfile`. Keep errors escaped through the existing flag/main paths; do not print a second parser diagnostic. Update usage to separate diagnose from `--profile [--limit N] [-o report.txt] <logfile>`. README examples explain all-list scope, complete totals, physical source references, confidence, lower bounds and observed-gap limitations. Do not advertise comparison/JSON flags that are not implemented.

- [x] **Step 4: Verify and review.** Run `go test ./cmd/tfli ./internal/profile -count=1`, then final validation below. Independent reviewer checks all explicit flag combinations and no output mutation on validation errors. Separate cleanup preserves boundary/error coverage.
- [x] **Step 5: Commit.** Signed commit `Add explicit limits to text profile lists`. Record execution evidence and mark Boundary F implemented only after combined review.

## Final validation

- [x] `go test -race -count=1 ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, `gofmt -d .`, `go mod tidy -diff`, `go mod verify`.
- [x] Match existing CI matrix: linux/darwin × amd64/arm64, `CGO_ENABLED=0`, all packages and trimpath CLI. Report local versus remote checks accurately.
- [x] Inspect real CLI output for RPC-only, UI-only, mixed/partial, saturated, no-duration and long/control-bearing sanitised fixtures, including `-o`, default, limited and unlimited lists.
- [x] Full F1/F2 review and separate cleanup; no unresolved findings. Verify signed history, clean status and whitespace. Commit evidence in both plans/spec without claiming G/H/I completion.

## Planning self-review

Task 1 owns item 5's output and data qualifications; task 2 owns the explicit flag contract and safe dispatch. F1 supplies full observations/aggregates/intervals; rendering applies list limits only. JSON and comparison remain later boundaries. This plan records proposed work, not passing implementation tests.

Independent review on 10 September 2026 required explicit selected-tier clock
origins in text output. The contract and test requirements now include known,
unknown and differing RPC/UI origins. Scoped re-review found no remaining
issues and judged both F plans ready for Dan's review. No application code
changed; only documentation validation was run.

## Execution record

Task 1 completed in signed commits `7c3cb16`, `9d0dba3` and `3c1a43d`.
Behavioural RED preceded rendering changes. Review required full active-interval
identity, explicit analysis states and precise evidence wording. The fixes passed
scoped review. Separate cleanup detected lost assertions during the temporal file
split; those were restored and independently reviewed. No task 1 finding remains;
profile coverage is 94.6%, with all genuine regression tests retained.

Task 2 implemented profile-only limits in signed commit `f50dd08`, with behavioural
RED/GREEN and full-suite validation. Review required stronger real CLI coverage
for provider/type limits and complete totals. Test-only commit `8330be9` adds
independently expected row counts and totals for default, one-row and unlimited
output. Cleanup also required explicit interval and UI-operation limit coverage;
test-only commit `112fb6b` covers both with real fixtures. Scoped reviews approved
all corrections. Separate cleanup retained the genuine tests, with no remaining
concerns and CLI package coverage of 94.8%.

Final local validation: `go test -race -count=1 ./...` passes for all eleven
packages at `112fb6b`; lint reports zero issues and formatting is clean. Build,
module tidy diff and checksum verification passed. The configured Linux/macOS
amd64/arm64 matrix passed with `CGO_ENABLED=0`, `GOTOOLCHAIN=local` and
`GOFLAGS=-mod=readonly`, building all packages and each trimpath CLI. Those builds
used `f50dd08`; subsequent commits change tests only. Remote CI was not run.

Manual CLI inspection covered RPC-only, UI-only, mixed-tier, partial positioning,
saturated, no-duration and long/control-bearing sanitised input. Default, limited,
unlimited and output-file reports preserve whole-capture totals; source identities,
clock origins, lower bounds and timing qualifications remain visible. Existing TUI
goldens are unchanged.

Final combined review of `e21f366..112fb6b` approved specification, architecture
and code quality with no actionable findings. All task review and cleanup
findings are resolved; no deferred findings or controller rulings remain. Every
commit through `0940be8` has a verified signature. Boundary F is implemented and
ready for Dan's integration decision; nothing was pushed or merged.
