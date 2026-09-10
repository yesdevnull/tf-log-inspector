# JSON Profile CLI Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose complete JSON profiles through `--profile --format json` while retaining text defaults and protected output handling.

**Architecture:** Validate format and explicit flag presence before dispatch. Load one log, pass F's complete report and basename/version metadata to G1's renderer, and keep the existing `writeReport` output policy. No comparison mode or parser change.

**Tech Stack:** Existing Go flag/testing packages, standard filepath/JSON decoding in tests, existing dependencies.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), item 7 CLI/profile contract. Requires reviewed [G1 schema and encoder](2026-09-11-json-profile-schema.md).

## Global Constraints

- “`--format` defaults to `text` and applies only to profile and comparison modes.” Comparison is not implemented in G; only profile accepts it here.
- “Explicit `--limit` with JSON is rejected because JSON always exports the complete data.”
- “Unknown formats, invalid limits and irrelevant options fail before output opens.”
- “Do not include current generation timestamps or absolute input paths by default.”
- “Terminal rendering escapes untrusted controls. Machine output uses valid JSON encoding and leaves identifier values intact.”
- Preserve existing help/version precedence, input arity and same-file/symlink/hard-link protection. Do not change unrelated output overwrite semantics.
- TDD, independent review, separate test cleanup, sanitised real fixtures and current toolchain only.
- Signed commits via `/Users/dan/.codex/bin/codex-git`; signing failure stops work. No push/merge implied.

---

## Status and exact CLI decisions

Dan authorised drafting G1/G2 on 11 September 2026, then approved both plans and subagent execution. G1 passed review and cleanup before G2 started.

```text
tfli --profile [--format text] [--limit N] [-o profile.txt] run.log
tfli --profile --format json [-o profile.json] run.log
```

Only exact lowercase `text` and `json` are accepted; empty or other values fail. Register `--format` as a string defaulting to `text`. `FlagSet.Visit` records `formatSet` separately from its value; explicit `--format=text` outside profile must fail too. Keep current parsing/help/version/arity/mode-conflict order, then existing scrub-values and limit mode/negative checks. Add checks in this order before dispatch:

1. Explicit format outside profile: `--format applies only to --profile`.
2. Unknown format: `--format must be text or json` (fixed error, never echoes raw value).
3. Profile JSON with any explicit limit, including 0, 20 or an earlier repeated flag: `--limit is not supported with --format json`.

Malformed/overflowing limits remain flag-parser errors before these checks. Existing negative-limit check wins for a negative explicit limit. Do not alter the current version/help short-circuit behaviour, flag parsing after a positional argument, or accepted one-file arity. No `--compare` flag or future-mode placeholder.

## Files and interfaces

| File | Responsibility |
| --- | --- |
| Modify `cmd/tfli/main.go` | Flag, validation, profile format dispatch and metadata |
| Modify `cmd/tfli/main_test.go` | Existing runProfile call migration, mode/help/validation and protected output regression tests |
| Create `cmd/tfli/profile_json_test.go` | Real CLI JSON decoding, completeness, source and parity tests |
| Modify `README.md` | Commands, disclosure, whole-data semantics and schema-reference link |

Use a CLI-local `profileOptions`, not a generic options framework or compatibility wrapper:

```go
type profileOptions struct {
    Format string
    Text profile.TextOptions
}
func runProfile(path, outPath string, stdout io.Writer, options profileOptions) error
```

Consume G1's exact `profile.JSONMetadata{ToolVersion, InputBasename}` and `profile.RenderJSON(io.Writer, profile.Report, profile.JSONMetadata) error`. Migrate all internal runProfile callers together. `profile.Render` retains its current text API; default text bytes must remain identical.

## Task 1: Validate and dispatch JSON profiles safely

**Files:** `cmd/tfli/main.go`, `main_test.go`, new `profile_json_test.go`.

**Interfaces:** Produce profileOptions/runProfile above; consume F/G1 APIs. Existing `writeReport` remains the single owner of output descriptor identity, truncation and close-error handling.

- [x] **Step 1: Add failing CLI behaviour tests.** Start with this validation table; use a temp existing sentinel output for each case and verify no modification, stdout or stderr:

```go
func TestProfileFormatValidationPrecedesFileAccess(t *testing.T) {
    cases := []struct { args []string; want string }{
        {[]string{"--format=text"}, "--format applies only to --profile"},
        {[]string{"--diagnose", "--format=json"}, "--format applies only to --profile"},
        {[]string{"--scrub", "--format=text"}, "--format applies only to --profile"},
        {[]string{"--profile", "--format=yaml"}, "--format must be text or json"},
        {[]string{"--profile", "--format="}, "--format must be text or json"},
        {[]string{"--profile", "--format=json", "--limit=0"}, "--limit is not supported with --format json"},
        {[]string{"--profile", "--format=json", "--limit=20"}, "--limit is not supported with --format json"},
    }
    for _, tc := range cases {
        dir := t.TempDir()
        output := filepath.Join(dir, "report.json")
        if err := os.WriteFile(output, []byte("keep\n"), 0600); err != nil { t.Fatal(err) }
        args := append(append([]string{}, tc.args...), "-o", output, filepath.Join(dir, "missing.log"))
        var stdout, stderr bytes.Buffer
        err := run(args, &stdout, &stderr)
        if err == nil || err.Error() != tc.want { t.Fatalf("%v: %v", args, err) }
        data, readErr := os.ReadFile(output)
        if readErr != nil || string(data) != "keep\n" || stdout.Len() != 0 || stderr.Len() != 0 { t.Fatal("validation touched output") }
    }
}
```

Add one positive real `run` invocation of `--profile --format json` decoded with the standard decoder; require one document and EOF, schema_version 1, kind profile, basename exactly the fixture filename, current tool version and independently checked source lines/quality totals. Use a fixture with >20 admitted observations and prove all survive without explicit limit. Add explicit text vs omitted format byte-equality and `--limit=1` text regression.

Cover uppercase/unknown/control-bearing format values, help/version with format flags, repeated format flags (last value wins; explicit presence retained), explicit repeated limit ending at default, and negative/malformed/overflow limits. Capture parser diagnostics using existing test conventions; no raw ESC/control injection or extra successful JSON output. Do not reimplement stdlib flag parsing.

- [x] **Step 2: Run RED.** `go test ./cmd/tfli -run 'Test.*(Format|JSON)' -count=1`. Confirm absent flag/dispatch/validation failures; API compilation failures alone are insufficient.
- [x] **Step 3: Implement the small dispatch change.** Register and visit the new flag, then apply the checks:

```go
format := fs.String("format", "text", "profile output format: text or json (--profile only)")
// In the existing Visit switch: case "format": formatSet = true.
if formatSet && !*doProfile { return errors.New("--format applies only to --profile") }
if *format != "text" && *format != "json" { return errors.New("--format must be text or json") }
if *format == "json" && limitSet { return errors.New("--limit is not supported with --format json") }
```

Pass `profileOptions{Format: *format, Text: profile.TextOptions{Limit: *limit}}`. In runProfile load once; JSON path builds the report before `writeReport`, propagating Build errors without opening output. Text path retains its existing behaviour. Use the following JSON branch:

```go
if options.Format == "json" {
    report, err := profile.Build(l)
    if err != nil { return err }
    metadata := profile.JSONMetadata{ToolVersion: version, InputBasename: filepath.Base(path)}
    return writeReport(stdout, path, outPath, func(w io.Writer) error {
        return profile.RenderJSON(w, report, metadata)
    })
}
return writeReport(stdout, path, outPath, func(w io.Writer) error {
    return profile.Render(w, l, options.Text)
})
```

Only validated CLI values reach runProfile; migrated direct tests explicitly supply `Format: "text"` or `"json"`. Do not duplicate output-file logic. G1 marshals fully before writing, but unrelated existing output files still follow writeReport's established truncation policy on render failure; no new atomic replacement promise.

- [x] **Step 4: Verify and review.** `go test ./cmd/tfli ./internal/profile -count=1`, then full suite. Independent review checks every explicit-presence combination and ordering before file access; separate cleanup retains error/sentinel coverage.
- [x] **Step 5: Commit.** Signed commit `Expose complete JSON profile output`; record RED/GREEN and review evidence.

## Task 2: Lock down file output and document the JSON contract

**Files:** `cmd/tfli/profile_json_test.go`, existing output tests in `main_test.go`, `README.md`.

**Interfaces:** Exercise `run` as the user entry point; no new production API. G1 schema reference is `docs/profile-json-v1.md`.

- [ ] **Step 1: Add real end-to-end output regressions.** Exercise JSON stdout and `-o` against sanitised RPC-only, UI-only, mixed, no-duration and saturated fixtures. Copy one fixture under two directories using the same basename: output bytes must match. Change only the basename: decoded input.basename changes, all other decoded data remains equal. Verify `-o` writes no stdout, produces exactly one complete document, and no absolute temp directory appears. Source locations and counts must match independent fixture values, not values obtained from the same JSON projection.

Add JSON to same-path, symlink and hard-link input/output refusal tests. Read the input bytes before and after each attempt; require an error and identical bytes. Retain platform handling from existing tests. Example success-path structure:

```go
var stdout, stderr bytes.Buffer
if err := run([]string{"--profile", "--format=json", "-o", output, input}, &stdout, &stderr); err != nil { t.Fatal(err) }
if stdout.Len() != 0 || stderr.Len() != 0 { t.Fatal("unexpected diagnostic output") }
data, err := os.ReadFile(output)
if err != nil { t.Fatal(err) }
if !json.Valid(data) { t.Fatal("output file is not JSON") }
```

Follow that structural check with decoded schema/source/count assertions. Cover missing input, output-open failure using a directory, failing stdout and short stdout using real writer-error boundary helpers already used in the repo. Preserve returned errors; no swallowed error may look like successful output. Test input identifiers with newline/ESC/quotes/non-ASCII round-trip through real parsing and JSON decoding. Do not place private logs or credentials into fixtures.

- [ ] **Step 2: Run the focused tests.** `go test ./cmd/tfli -run 'Test.*JSON' -count=1`. This task strengthens an existing feature; passing tests are legitimate coverage, not evidence of a new behavioural RED. Any production defect uncovered requires a failing regression before its smallest fix, followed by review.
- [ ] **Step 3: Document commands and disclosure.** Add these examples and link the schema reference:

```text
tfli --profile --format json run.log
tfli --profile --format json -o profile.json run.log
```

Explain complete data/no limit, versioned schema, explicit nulls vs zero, integer milliseconds, separate RPC/UI clocks and durations, full source references, confidence/lower bounds and local-only entry identities. JSON contains unmasked identifiers but excludes raw response/log bodies; review before sharing. Default text remains unchanged. No JSON import, compare command or compatibility commitment is advertised.
- [ ] **Step 4: Verify and review.** Run the full checks below; independently inspect representative JSON documents and decode them. Independent whole-G review checks G1/G2 together; separate cleanup covers these tests. Address all actionable findings, then update both plans and only Boundary G's spec status.
- [ ] **Step 5: Commit.** Signed commit `Verify and document JSON profile workflows`; record actual local/remote evidence without claiming H or I completion.

## Final validation

- [ ] `go test -race -count=1 ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, `gofmt -d .`, `go mod tidy -diff`, `go mod verify`.
- [ ] Existing CI matrix: Linux/macOS × amd64/arm64 with `CGO_ENABLED=0`, `GOTOOLCHAIN=local`, `GOFLAGS=-mod=readonly`; build all packages and CLI with `-trimpath` into task-specific `/tmp` paths. Distinguish local checks from remote CI.
- [ ] Text default output unchanged, JSON complete and deterministic, no schema leak of raw data/internal IDs; all errors propagate and input alias tests pass.
- [ ] Independent combined review and separate cleanup complete; signed history, whitespace and clean-worktree checks. Finish with Dan's integration decision; do not infer permission to push or merge.

## Planning self-review

Task 1 owns option semantics, exact validation order, the API migration and a
usable JSON command; task 2 owns broader file/identity/error integration coverage
and public usage. Every option check precedes input loading/output handling.
Explicit default-valued flags are tracked by presence. The plans share exact
`JSONMetadata` and `RenderJSON` signatures. Existing writer ownership is retained;
encoding failure is not misrepresented as atomic file replacement. Comparison
and JSON imports remain outside G. Documentation checks do not establish passing
application tests; implementation and independent reviews are future work.

## Execution record

Task 1 completed in signed commit `fd9c261`, following behavioural flag/dispatch
RED and focused GREEN. Independent spec/quality review approved with no findings.
Separate cleanup retained all twenty behavioural cases across six tests, with
94.7% CLI package coverage and no concerns. Full/race tests, build and vet pass.
Manual compiled-command checks decoded RPC/UI/mixed/no-duration/saturated/partial
JSON and confirmed reconstruction remains not checked. The mixed-tier default
text output is byte-identical to the pre-G binary. Broader file-output tests and
README work completed in task 2.

Task 2 completed in signed commit `3681ad6`. Real stdout/file regressions cover
five fixture classes, basename identity, control/Unicode round-tripping, output
errors and same-path/symlink/hard-link input preservation. No production defect
was found. Independent task review approved with no findings; separate cleanup
retained all cases with no concerns and measured 95.1% CLI package coverage.
Focused and full tests pass. Controller race checks passed all eleven packages,
lint reported zero issues, and formatting/build/module checks passed. Linux and
macOS amd64/arm64 package and trimmed CLI builds passed locally with CGO disabled,
local toolchain and readonly modules; remote CI has not run. Combined review
remains before Boundary G completion.
