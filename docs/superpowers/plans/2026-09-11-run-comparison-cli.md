# Run Comparison Renderers and CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose before/after raw-log comparison through deterministic text and complete JSON output while protecting both inputs.

**Architecture:** Consume H1's comparison report without recalculating deltas in renderers. Extend the existing report writer to check every input against the actual output descriptor before truncation. Keep flag validation, file loading and metadata in the CLI.

**Tech Stack:** Existing Go flag/JSON/testing packages, existing `logfmt.DisplayText` and `qualitytext`, current dependencies only.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), item 7 and Boundary H; requires reviewed [H1 model and schema](2026-09-11-run-comparison-model.md).

## Global Constraints

- “`--compare` is mutually exclusive with profile, diagnose and scrub, and requires exactly two raw-log paths.”
- “`--format` defaults to `text` and applies only to profile and comparison modes.”
- “Explicit `--limit` with JSON is rejected because JSON always exports the complete data.”
- “Unknown formats, invalid limits and irrelevant options fail before output opens.”
- “For comparison, protect both input files from output aliasing, including symbolic and hard links.”
- “Read and validate both inputs before rendering.”
- “Use the existing report writing policy for unrelated output files; do not introduce a hidden change to overwrite semantics in this feature.”
- “Terminal rendering escapes untrusted controls. Machine output uses valid JSON encoding and leaves identifier values intact.”
- “Exit status remains success for a rendered report, including unavailable sections; input/argument/render failures remain errors.”
- TDD, real sanitised fixtures, independent review and separate test cleanup. Use existing tools; no compatibility wrapper, new dependencies, TUI comparison or performance-threshold exit codes.
- Signed commits through `/Users/dan/.codex/bin/codex-git`; stop on signing failure. Do not push or merge without Dan's instruction.

---

## Status and fixed interfaces

Draft authorised on 11 September 2026 after G merged at `801a917`; Dan subsequently approved both plans and subagent implementation. H1 must pass review and cleanup first. Its exact JSON field contract, model declarations and semantics are binding inputs to every task below; give each implementing subagent both the task brief and H1 contract.

```go
// internal/profile — H1 provides these names and complete declarations:
func BuildComparison(before, after Report) (ComparisonReport, error)
type ComparisonMetadata struct { ToolVersion, BeforeBasename, AfterBasename string }

// New H2 renderers; existing TextOptions and DefaultLimit remain shared:
func RenderComparisonJSON(w io.Writer, report ComparisonReport, metadata ComparisonMetadata) error
func RenderComparisonText(w io.Writer, report ComparisonReport, metadata ComparisonMetadata, options TextOptions) error

// cmd/tfli — existing profileOptions remains profile-only:
type comparisonOptions struct { Format string; Text profile.TextOptions }
func runComparison(beforePath, afterPath, outPath string, stdout io.Writer, options comparisonOptions) error
func writeReport(stdout io.Writer, inputPaths []string, outPath string, render func(io.Writer) error) error
```

## File responsibilities

| File | Change |
| --- | --- |
| Create `internal/profile/comparison_json.go` and `comparison_json_test.go` | Explicit wire projection, deterministic buffered encoding and contract tests |
| Modify `internal/profile/json_data.go` and existing JSON tests only as needed | Share existing capture/tier/quality projection without changing profile v1 |
| Create `internal/profile/comparison_text.go` and `comparison_text_test.go` | Text rows, ranked/unranked limits, disclosure and safe strings |
| Modify `cmd/tfli/main.go`, `main_test.go` | Mode/arity/options, list-of-inputs writer and direct caller migration |
| Create `cmd/tfli/comparison.go`, `comparison_test.go` | Loading/assembly dispatch and comparison-specific CLI tests |
| Modify `README.md`, H1's `docs/comparison-json-v1.md` if clarification is needed | Commands, disclosure and schema reference |

## Text presentation contract

Print `tfli comparison report`, then explicitly labelled `BEFORE` and `AFTER` basenames/byte sizes and both labelled whole-capture quality summaries. Reuse `qualitytext.WriteCaptureQuality` for each capture, preceded by its side label. State that identifiers are unmasked, logging changes observed timing, logging equivalence is unknown, and provider identity sets are same/different/unknown without asserting matching versions. Print the sorted sets when different or unknown so the limitation is actionable. Always disclose that separately scrubbed aliases cannot be matched reliably.

For each of H1's five sections, show tier availability and split the complete rows into **EXACT TOTAL-DURATION CHANGES** and **UNRANKED / UNAVAILABLE** lists. Omit an empty sublist with an explicit `no rows` section message if both are empty. Exact rows retain H1 ordering; unranked rows retain raw-key ordering. `TextOptions.Limit` applies independently to each of these lists, default 20, zero means all, negative returns `comparison limit must be non-negative` before writing. Print shown/total counts when limited. Quality, availability, provider sets and unnamed-UI totals are never limited. JSON has no limit parameter.

Use a multiline row layout so long exact keys remain readable without truncation or terminal-width logic. Escape each key and basename with `logfmt.DisplayText` exactly once. Include row state, full key fields and these four metrics, each with before, after, signed delta and percentage:

```text
RPC METHODS — EXACT TOTAL-DURATION CHANGES
  matched: provider=p  resource_type=r  method=Read
    metric        before       after        delta       change
    count         2            1            -1          -50.00%
    total ms      20           20           +0          +0.00%
    mean ms       10.00        20.00        +10.00      +100.00%
    max ms        10           20           +10         +100.00%
```

Integers display exactly in base 10; means and percentages use two decimals for text only. Defined nonnegative deltas include `+`, zero is `+0`/`+0.00`, and unavailable metrics/deltas are `n/a` with section/row explanations. Lower-bound measured timing values use `>=` (including mean/max), with `timing deltas unavailable: lower bound` on the row; do not prefix counts. Roundoff should not print a negative zero in rounded text. RPC and UI timings remain separate; no combined total or percentage.

Show before/after unnamed-UI totals outside exact-address rows and state that missing addresses cannot be matched. Qualify UI durations as rounded by up to one second per observation. Explain that added/removed means evidence presence, not creation/destruction, and changes do not establish causes. No pass/fail/regression verdict and no attempt to certify workload or logging equivalence.

## CLI validation contract

```text
tfli --compare [--format text] [--limit N] [-o comparison.txt] before.log after.log
tfli --compare --format json [-o comparison.json] before.log after.log
```

Retain stdlib flag parsing, last-value behaviour, parsing stopping at the first positional argument, escaped parser diagnostics and help/version short circuits. Track `formatSet`, `limitSet` and `valuesSet` with `FlagSet.Visit`. After parse/help/version, validate in this order:

1. Arity: when effective `--compare` is true require 2 paths, otherwise require 1. Error `expected exactly two log file arguments for --compare`, or existing one-file error. Print existing trusted usage once for arity failures.
2. Mode exclusivity over diagnose/profile/scrub/compare: `pass only one of --diagnose, --profile, --scrub, or --compare`.
3. Existing scrub-values mode check.
4. Explicit limit outside profile/compare: `--limit applies only to --profile or --compare`.
5. Existing negative limit check.
6. Explicit format outside profile/compare: `--format applies only to --profile or --compare`.
7. Existing exact lowercase text/json format check; fixed message, no echoed raw value.
8. Existing explicit JSON-limit rejection, including 0, 20 and repeated limit ending in default.
9. Dispatch, retaining scrub `-o` requirement and updating no-mode `-o` diagnostic to include compare.

This retains the previous arity-before-mode ordering. For combined comparison/mode-conflict tests provide two positional inputs to reach the mode error. Update old exact-message expectations for expanded mode lists without weakening assertions. `--compare=false` is not selected and retains one-file arity. Same file as before and after is allowed for identity comparisons; only output aliasing is prohibited.

## Task 1: Encode complete comparison JSON

**Files:** `internal/profile/comparison_json.go`, `comparison_json_test.go`; focused shared capture projection changes in `json_data.go` and existing JSON contract tests if required.

**Interfaces:** Consume H1's `ComparisonReport`, `ComparisonMetadata`, `model.Comparison` and full exact schema contract. Produce `RenderComparisonJSON(io.Writer, ComparisonReport, ComparisonMetadata) error`. H2 task 3 calls it directly.

- [x] **Step 1: Write failing decoder/contract tests.** Build reports from real `provider-rpc.log` and `structured-ui.log`; validate one root document followed by EOF, five sections, before/after metadata and independent fixture counts. Use this exact integer boundary assertion against the private formatter introduced in step 3:

```go
func TestComparisonJSONSignedInteger(t *testing.T) {
    value := model.SignedChange{Negative: true, Magnitude: math.MaxUint64}
    got := comparisonInteger(value)
    if string(got) != "-18446744073709551615" { t.Fatalf("integer: %s", got) }
    encoded, err := json.Marshal(got)
    if err != nil { t.Fatal(err) }
    if string(encoded) != "-18446744073709551615" { t.Fatalf("token: %s", encoded) }
}
```

Assert exact root/section/row/key/summary/change key sets, including each key shape. Recursively check both shared capture objects against G's key contract and no raw-body/internal-ID fields. Include null and populated forms, empty maps/arrays, >20 rows in all five sections, zero baseline and lower bounds. Numbers above 2^53 must survive `UseNumber`; tests must not decode exact integers through float64.
- [x] **Step 2: Run RED.** `go test ./internal/profile -run 'TestComparisonJSON' -count=1`. Add minimal declarations to reach behavioural failures before production implementation. Do not label missing-symbol errors as evidence the encoder satisfies the contract.
- [x] **Step 3: Implement private JSON projection and full-buffer encoding.** Reuse G's `jsonInput`, `jsonTiers`, `jsonQuality`, `tierJSON`, `qualityJSON`, `validStrings` and integer/null meanings. Extract the small shared metadata/tier-reason validation into a private helper if needed so both paths reject invalid UTF-8; keep the exact profile diagnostic unchanged and translate it to the comparison diagnostic at the comparison boundary. Do not render whole profiles to JSON and decode them, nor project/copy their observation arrays merely to get capture summaries. Use explicit private wire structs, including separate typed key objects for the five section kinds. Unknown synthetic kind/state values return fixed errors before writing, not a guessed key shape.

```go
func comparisonInteger(v model.SignedChange) json.Number {
    value := strconv.FormatUint(v.Magnitude, 10)
    if v.Negative && v.Magnitude != 0 { value = "-" + value }
    return json.Number(value)
}
```

Projection prepares all fields, marshals with `json.MarshalIndent`, appends exactly one newline and calls existing `writeText` once. Fixed marshal error: `encoding comparison JSON failed`. Unknown section kinds return `comparison JSON has invalid section kind`; unknown row states return `comparison JSON has invalid row state`. Propagate writer errors and `io.ErrShortWrite`. Validate exported string values and map keys; invalid UTF-8 must fail before any bytes are passed to the writer. Non-finite synthetic means/percentages also fail before writer invocation without echoing values.
- [x] **Step 4: Run GREEN, parity and failure tests.** Require deterministic output from equivalent inputs regardless of map iteration, valid newline/ESC/quotes/Unicode round-trip, no report mutation, exact signed extremes, and correct nulls. Reuse the existing failing/short writer helpers. Ensure profile JSON byte/schema tests still pass after helper extraction. Run `go test ./internal/profile -count=1`. Independently decode one comparison document and inspect both side labels, quality and lower-bound semantics.
- [x] **Step 5: Review, clean up and commit.** Independent task review plus separate test cleanup; fix actionable findings. Signed commit: `Encode complete comparison reports`.

## Task 2: Render qualified text comparisons

**Files:** `internal/profile/comparison_text.go`, `comparison_text_test.go`.

**Interfaces:** Consume H1 model values and metadata without recalculating aggregates or deltas. Produce `RenderComparisonText(io.Writer, ComparisonReport, ComparisonMetadata, TextOptions) error`. Reuse existing `writeText`, `limitedLength`, `qualitytext.WriteCaptureQuality` and safe display helpers where applicable.

- [x] **Step 1: Add failing text tests.** Construct comparison reports via H1 from synthetic Span inputs or real loaded reports; assert the two-call/one-call example above, labelled before/after quality, count/mean separation, nulls, lower bounds and all caveats. To test limits, create at least 21 exact rows and 21 lower-bound/unavailable rows within a section, then require independent shown/total labels and full quality totals at limit 1, default 20 and zero. For sections where lower bounds cannot occur (RPC), use unavailable-tier rows in a separate report. Use a small literal output expectation for one row plus targeted contract assertions; no giant golden copied blindly from the renderer.

```go
var output bytes.Buffer
err := RenderComparisonText(&output, report, metadata, TextOptions{Limit: -1})
if err == nil || err.Error() != "comparison limit must be non-negative" || output.Len() != 0 {
    t.Fatalf("negative limit: %v, output %q", err, output.String())
}
```

- [x] **Step 2: Run RED.** `go test ./internal/profile -run TestComparisonText -count=1`. Require behavioural evidence for missing content/limits after declarations compile.
- [x] **Step 3: Implement the text contract.** Build the complete report into a `strings.Builder`, label capture sides, write shared quality summaries, then split each already-ordered section on `row.Changes.TotalMs != nil` into exact and unranked views. Apply `limitedLength` independently, format stored metric/delta values and finish through `writeText`. Use `strconv.FormatUint` for signed magnitudes and `fmt.Sprintf("%+.2f", value)` for defined decimal changes, normalising rounded zero. Keep all raw identifiers escaped and untruncated; do not share JSON-escaped strings with text.
- [x] **Step 4: Run GREEN and inspect output.** Test safe controls in both basenames and every key position, unchanged model values, added/removed meaning, missing-tier text, count-only lower-bound changes, empty sections, independent limits, writer errors and short writes. Cross-check the same report's decoded JSON numbers against text values before text rounding. Run `go test ./internal/profile -count=1`. Inspect actual representative text for comprehensible labels/rows, including very long names.
- [x] **Step 5: Review, cleanup and commit.** Independent task review and separate cleanup. Signed commit: `Render qualified before and after comparisons`.

## Task 3: Add two-input dispatch and output protection

**Files:** `cmd/tfli/main.go`, `main_test.go`, new `comparison.go`, `comparison_test.go`.

**Interfaces:** Consume both H2 renderer signatures and H1 `BuildComparison`. Produce `runComparison`, `comparisonOptions` and the list-based `writeReport` signature above. Migrate every existing `writeReport` caller/test in this task; no legacy signature or compatibility wrapper.

- [x] **Step 1: Add failing option and input-safety tests.** Table-test compare arity 0/1/2/3, every competing mode, irrelevant scrub-values, empty/uppercase/control-bearing formats, default/explicit/repeated format and limit, negative/malformed/overflow limit, help/version and `--compare=false`. Use sentinel output and nonexistent inputs so validation must return the expected fixed error without changing output. Capture and assert parser/usage diagnostics using current conventions. Tests for profile/diagnose/scrub/TUI must retain their meaningful exact-message checks.

```go
var stdout, stderr bytes.Buffer
err := run([]string{"--compare", "--format=json", "--limit=0", "-o", output, before, after}, &stdout, &stderr)
if err == nil || err.Error() != "--limit is not supported with --format json" { t.Fatalf("error: %v", err) }
data, err := os.ReadFile(output)
if err != nil || string(data) != "keep\n" || stdout.Len() != 0 || stderr.Len() != 0 { t.Fatal("validation touched output") }
```

Test both input roles × same path/symlink/hard link × text/JSON. Read each input's bytes before and after every attempt, require an error, unchanged input bytes and no successful report. Retain existing platform skip/error handling for unavailable symlink creation. Also test identical before/after paths with a distinct output: success and zero defined changes.
- [x] **Step 2: Run RED.** `go test ./cmd/tfli -run 'TestCompare|TestComparison' -count=1`. Run existing writer tests during the signature migration so preserving the one-input modes is a behavioural requirement.
- [x] **Step 3: Implement validation and the shared writer extension.** Add `--compare` and the exact validation order above. Change only the writer's input identity phase: stat every input before opening output, open output without truncation, stat the output descriptor, compare it with every input's FileInfo, then perform the existing regular-file truncation/render/close logic. Keep render-error precedence and nonregular-file handling. Single-input callers pass `[]string{path}`. Do not duplicate writer logic in comparison or alter scrub's separate publication path.

```go
inputInfos := make([]os.FileInfo, len(inputPaths))
for i, path := range inputPaths {
    info, err := os.Stat(path)
    if err != nil { return fmt.Errorf("checking %s: %w", path, err) }
    inputInfos[i] = info
}
// After OpenFile without O_TRUNC and out.Stat succeed:
for i, info := range inputInfos {
    if os.SameFile(info, outputInfo) {
        _ = out.Close()
        return fmt.Errorf("input %s and output %s are the same file", inputPaths[i], outPath)
    }
}
```

`runComparison` loads before then after exactly once each with `model.Load`, builds each with `profile.Build`, then calls `profile.BuildComparison`. Wrap failures by role using `%w` (`loading before`, `loading after`, `profiling before`, `profiling after`, `comparing captures`) without raw log contents. Only after both inputs and model assembly succeed call `writeReport` with both paths and the selected renderer. Metadata uses `version` and each `filepath.Base`. Do not parallelise loads. Keep JSON rendering inside the existing writer policy: unrelated output may be truncated before an encoding failure, with no atomic replacement claim.
- [x] **Step 4: Run GREEN.** Require two missing/unreadable input role cases, second-input failure preserving an existing output, directory output error, failing/short stdout, no stdout with `-o`, alias checks, exact one JSON document and EOF, default-text equality to explicit text, and no stale accepted `--format`/`--limit` outside allowed modes. Run full CLI package and `go test ./...` to detect writer regressions.
- [x] **Step 5: Review, cleanup and commit.** Independent task review and separate cleanup. Signed commit: `Expose raw-log comparison with two-input protection`.

## Task 4: Verify complete workflows and document interpretation

**Files:** `cmd/tfli/comparison_test.go`, `README.md`, `docs/comparison-json-v1.md`; completion evidence in both H plans and the spec status only after all gates pass.

**Interfaces:** Exercise user-facing `run`; no further production API planned.

- [x] **Step 1: Add real workflow regressions.** Use `provider-rpc.log`, `two-tier.log`, `structured-ui.log`, `core-only.log`, `resources-long-lower-bound.log` and temporary synthetic empty/admitted-unpositioned captures. Assert identical inputs, added/removed groups, changed count with equal total, zero baseline, repeated actions, disjoint tiers and saturated deltas through text and decoded JSON. Source counts and totals must come from independently inspected fixture values. Copy identical capture bytes into different directories under identical basenames and require byte-identical output; rename either side independently and require only that side's `input.basename` to change. No absolute temp directory may appear. Differently renamed UI addresses remain separate observed keys, with the scrub-alias qualification intact. Use valid control/Unicode identifiers through real parsing, then assert JSON round-trip and escaped text.
- [x] **Step 2: Run focused checks.** `go test ./cmd/tfli -run 'TestCompare|TestComparison' -count=1`. Passing regression additions are legitimate coverage, not a new feature's RED evidence. If they reveal a production bug, capture its behavioural failure before the smallest fix and review that fix.
- [x] **Step 3: Document actual commands and limits.** Add both CLI examples above to README and link `docs/comparison-json-v1.md`. Explain before/after direction, admitted counts versus mean changes, zero/missing baselines, unavailable tiers, separate clocks/work, lower-bound unranked rows, text's independent list limits, complete JSON, unmasked identifiers, no raw bodies, independent scrub aliases, unknown logging equivalence and observational success exit status. Do not advertise threshold exits, JSON import or fuzzy matching. Show `--limit 0` only for text.
- [x] **Step 4: Execute final checks.** Run the commands below once on the final application tree; inspect every result. Decode representative JSON with the standard decoder, inspect representative text, and compare pre-H versus post-H profile text/JSON on identical fixtures and version metadata. Run independent combined H review and separate cleanup; resolve actionable findings. Update both plans and **only Boundary H**'s completion status after this evidence exists.
- [x] **Step 5: Commit verified documentation.** Signed commit `Verify and document raw-log comparison workflows`. Record local versus remote verification honestly, preserve review findings/resolutions in durable plan evidence, verify all branch signatures and a clean worktree. Integration remains Dan's choice.

## Final validation

```text
go test -race -count=1 ./...
go build ./...
golangci-lint run --timeout=5m
gofmt -d .
go mod tidy -diff
go mod verify
```

Match existing `.github/workflows/ci.yml`: Linux/macOS × amd64/arm64, `CGO_ENABLED=0`, `GOTOOLCHAIN=local`, `GOFLAGS=-mod=readonly`. For each pair run both `go build ./...` and `go build -trimpath -o /tmp/tfli-h-OS-ARCH ./cmd/tfli` with literal environment values (for example `env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=amd64 go build ./...`). Local cross-builds do not establish remote CI success or runtime execution on other platforms.

## Planning self-review

| Requirement / dependency | Owner and verification |
| --- | --- |
| Exact H1 data and schema consumed unchanged | Tasks 1/2 contract checks; task 3 typed calls |
| Complete JSON, explicit nulls and exact integers | Task 1 decoder/key/large-number tests |
| Honest text, unranked lower bounds and independent limits | Task 2 layout and metric parity tests |
| Options rejected before file access | Task 3 sentinel tests, including explicit defaults |
| Both inputs protected through one writer | Task 3 alias matrix and old-mode regressions |
| Input identities, failure propagation and reproducibility | Tasks 3/4 real filesystem and decoder cases |
| Both quality summaries, limits of comparability, no threshold exits | Tasks 1/2/4 contract and workflow cases |
| No reconstruction, profile schema/output regression | H1 adapter assertion; tasks 1/4 parity |

Task 1 owns JSON projection; task 2 consumes the same report independently; task 3 owns all CLI/signature changes; task 4 adds wider integration evidence and documentation. The proposed writer migration is a list-of-inputs extension, not a compatibility layer. No application code or tests have been changed by this planning task.

Draft self-review on 11 September 2026 checked every item 7 comparison requirement,
H1/H2 signature consistency, CLI error ordering and all existing writer callers.
Placeholder scan, relative links and code-fence checks pass. Baseline
`go test ./...` and `go build ./...` pass on the unchanged application;
implementation, independent code review and cleanup remain future work.

## Execution record

Task 1 completed in signed commits `ff97904` and `9464f9e`. The initial RED
was compilation-only, not the planned behavioural failure. A later controlled
empty-object encoder mutation failed the intended root-key assertion; production
was restored byte-for-byte and focused/profile checks passed. This sensitivity
check does not retroactively establish the original TDD order.

Independent review found an invalid-UTF-8 row-state diagnostic and missing
populated/null/zero-baseline/lower-bound wire assertions. The fix recorded
behavioural RED before correction, then focused/profile GREEN and scoped review
approval. Separate cleanup retained all genuine cases and additions. The full
eleven-package suite passed before these focused comparison fixes. A minor
independent nonmutation-snapshot improvement is carried to task 4.

Controller decision: qualifications is an ordered array of the nine specified
codes, matching profile JSON. The draft did not explicitly identify its
container, and the implemented prose-valued object introduced unspecified wire
values. Schema, encoder and tests now agree on the smaller array representation.
If this choice is wrong, the schema/doc/tests require rework before consumer
adoption. The clarification is committed in `8810492`.

Task 2 completed in signed commits `a8df7f6`, `61aa676` and `5fcca16`.
Behavioural RED preceded implementation, then focused/profile/full tests passed.
Review required model-generated test reports and genuine exact/lower-bound UI
scale cases plus a separate unavailable RPC report. Corrected tests passed
scoped review with all findings addressed, including retained count-line output.
Separate cleanup removed one duplicated quality assertion block while preserving
all required coverage; profile package tests pass at 96.1% statement coverage.
No production correction was needed after review.

Task 3 completed in signed commits `3437edf` and `4110ea4`. New-flag behavioural
RED preceded implementation; focused/CLI/full-suite tests passed. Review required
arbitrary failing-stdout coverage in both formats; added real-log cases passed
scoped review and separate cleanup. No production correction was needed.

Controller checks on `3437edf` production verified compiled text/JSON against
independently inspected fixture counts and durations, unavailable UI semantics,
saturated lower-bound delta nulls and nine qualification codes. Existing profile
text and JSON are byte-identical to the pre-H binary on `two-tier.log`. All eight
local package/trimpath-CLI builds passed for Linux/macOS amd64/arm64 with CGO
disabled, local toolchain and readonly modules. Remote CI has not run. Final
workflow coverage, documentation and combined review remain in task 4.

Task 4 completed in signed commits `8e7b9f4`, `8e6fc0c` and `d04a8dd`.
Real-file workflows cover both formats, independent fixture totals, directory
and basename reproducibility, empty/unpositioned/disjoint tiers, repeated
operations, count-versus-mean changes, lower bounds and control/Unicode strings.
The two deferred review notes are resolved: exact key-object wording and an
independent JSON report snapshot. A temporary nested mutation failed that
snapshot assertion and was restored. Review added complete renamed-document
comparison and text empty/unpositioned assertions; scoped re-review approved
with no remaining findings. Cleanup retained the required cases and removed
two predicates subsumed by the stronger temporary-directory leakage check.

Controller race tests passed all eleven packages at `8e6fc0c`, with zero lint
issues and clean formatting. Subsequent cleanup removed only redundant test
predicates and passed its focused checks. Build, module consistency/checksums,
local platform builds and manual output checks passed on unchanged production.
Final combined review of `801a917..ac40ba0` found no production defects or
Critical/Important findings. Its sole Minor was a shallow text-renderer test
snapshot. Commit `64a1577` reuses the independent deep-copy helper; a temporary
nested mutation failed the assertion before restoration, and the focused test
passed. Scoped re-review approved the fix with no collateral findings. Separate
cleanup retained the meaningful test without edits. All review findings are
resolved.

The controller ran `go test ./...` on `64a1577`: all eleven packages passed.
Step 5's documentation commit is `8e7b9f4`; its signature and clean-worktree
checks passed. All H commits through `64a1577` have verified signatures; this
final bookkeeping commit is checked separately after creation.
Boundary H implementation is complete on
`wip/run-comparison-plans`; integration remains Dan’s decision. No merge, push
or remote CI execution is claimed.
