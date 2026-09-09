# Timing Caveats Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the profile report and README state that logging overhead varies between calls, rankings are approximate and absolute durations do not transfer to an unlogged run.

**Architecture:** Correct the existing profile rendering function and its supporting documentation in place. Exercise the public report renderer through real fixture loading, retaining the TUI's existing wording and separate qualifications for UI rounding, start clamping and saturation. No new package, dependency, CLI flag or timing calculation is needed.

**Tech Stack:** Go 1.25, standard `testing` package, existing Go formatting and repository CI checks; Markdown documentation.

**Spec:** [Investigation workflows design](../specs/2026-09-09-investigation-workflows-design.md), section 2 “Consistent qualification of timing results” and delivery boundary A “Timing qualifications”.

## Global Constraints

- “Use the existing Go toolchain and dependencies.”
- “Do not estimate unlogged durations or adjust observed rankings by log volume.”
- “Keep UI rounding, start clamping, saturation and logging overhead distinct.”
- “Fixtures are synthetic or sanitised.”
- “Test observable contracts and important failure boundaries, not copied implementation details or mocked behaviour.”
- “Capture and assert expected error output.”
- Repository conventions additionally require British/Australian prose, TDD, signed commits, intact hooks, independent review and a separate test-cleanup pass after implementation.
- Use `/Users/dan/.codex/bin/codex-git` for Git operations. Stop immediately if signing fails; never bypass signing or hooks.

---

## Scope and starting evidence

Dan requested this implementation plan on 9 September 2026. The broader spec
remains a draft; this plan covers the Timing caveats slice only. Creating this
plan does not execute it or approve the other feature designs.

At planning time, `internal/profile/profile.go` promises “Rankings hold, since
every span paid the same cost”. Its function comment repeats that reasoning.
README's “Durations are measured under logging” section says “every span paid
the same tax”. In contrast, `internal/tui/layout.go` already explains that a call
which logs heavily is inflated more than one which waits, so rankings are
approximate.

The existing `TestReportStatesDurationsAreMeasuredUnderLogging` checks only
“under logging” and the word “Rankings”, and its comment endorses the false
claim. `TestLoggingCaveatSurvivesTheNoSpansPath` checks only that a caveat exists.
Strengthen the first to reject the current report's false claim, and give the
second a distinct placement assertion protecting the early-return explanation.

Later JSON and comparison plans must carry the same meaning through their own
qualification codes and readable explanations. This task does not add unused
code for those outputs. Historical specs and earlier plans may quote the false
claim to explain its correction; those references are not live application copy.

## File map

| File | Role in this task |
| --- | --- |
| `internal/profile/profile.go` | Modify `writeLoggingCaveat` and its explanatory comment |
| `internal/profile/profile_test.go` | Strengthen existing report caveat tests using real fixtures |
| `README.md` | Correct the paragraph beneath the empirical logging-overhead example |
| `internal/tui/layout.go` | Read-only reference for existing full and compact qualifications |
| `internal/tui/layout_test.go` | Run existing caveat tests without altering their expectations |
| `internal/tui/help.go` | Verify help uses the existing full caveat |
| `cmd/tfli/main.go` | Inspect CLI help wording and exercise report dispatch |

No new source files or fixtures are required. No TUI goldens should change.

### Task 1: Correct the timing qualification across existing user-facing copy

**Files:**

- Modify: `internal/profile/profile.go` — `writeLoggingCaveat` near line 343.
- Modify: `internal/profile/profile_test.go` — the two logging-caveat tests near line 271.
- Modify: `README.md` — “Durations are measured under logging” near line 263.
- Test: `internal/profile/profile_test.go` and existing `internal/tui/layout_test.go`.

**Interfaces:**

- Consumes: existing `render(t *testing.T, path string) string` in `profile_test.go`, which calls `model.Load` and `profile.Render`.
- Retains: `Render(w io.Writer, l *model.Log) error` and `writeLoggingCaveat(b *strings.Builder)` unchanged in signature.
- Produces: corrected report/documentation wording and regression coverage. No new API.

- [x] **Step 1: Establish a clean topic branch and baseline.**

At execution time, use the worktree skill to determine whether isolation is
needed. Check status before editing; resolve any pre-existing changes with Dan.
Stay on an appropriate topic branch. Pull with rebase using the signing wrapper;
if the branch has no upstream, explicitly use `pull --rebase origin main`.

Run from the execution checkout's repository root:

```bash
go test ./internal/profile ./internal/tui
```

Expected: both packages pass. Inspect all output. Baseline failures must be
understood and resolved before starting the red/green cycle.

- [x] **Step 2: Strengthen the report tests before changing production copy.**

Replace the body and preceding explanatory comment of
`TestReportStatesDurationsAreMeasuredUnderLogging` with this table-driven test.
It keeps the existing test's purpose and extends it to both timing tiers and the
early-return path. The existing imports already provide everything it uses.

```go
// Logging overhead varies by call, so every report path must qualify both
// rankings and absolute durations, independently of line wrapping.
func TestReportStatesDurationsAreMeasuredUnderLogging(t *testing.T) {
	for _, fixture := range []string{
		"provider-rpc.log",
		"structured-ui.log",
		"core-only.log",
	} {
		t.Run(fixture, func(t *testing.T) {
			out := render(t, "../../testdata/"+fixture)
			text := strings.ToLower(strings.Join(strings.Fields(out), " "))
			for _, required := range []string{
				"under logging",
				"logs heavily",
				"inflated more than one that waits",
				"rankings are approximate",
				"absolute times do not transfer",
			} {
				if !strings.Contains(text, required) {
					t.Errorf("report omits timing qualification %q:\n%s", required, out)
				}
			}
			for _, forbidden := range []string{
				"rankings hold",
				"same cost",
				"same tax",
				"only rankings transfer",
			} {
				if strings.Contains(text, forbidden) {
					t.Errorf("report promises reliable rankings through %q:\n%s", forbidden, out)
				}
			}
		})
	}
}
```

Strengthen the existing no-spans test to check placement, rather than duplicate
the table's content assertions. Retain its explanation of the early return.

```go
func TestLoggingCaveatSurvivesTheNoSpansPath(t *testing.T) {
	out := render(t, "../../testdata/core-only.log")
	caveat := strings.Index(out, "under logging")
	noSpans := strings.Index(out, "NO SPANS")
	if caveat < 0 || noSpans < 0 || caveat >= noSpans {
		t.Errorf("logging caveat must precede the no-spans explanation:\n%s", out)
	}
}
```

- [x] **Step 3: Run the focused tests and observe the intended failure.**

```bash
go test ./internal/profile -run 'TestReportStatesDurationsAreMeasuredUnderLogging|TestLoggingCaveatSurvivesTheNoSpansPath' -count=1
```

Expected: the table-driven test fails on the current false guarantee and missing
approximation/differential-overhead explanations. The placement test should
already pass. A compilation or fixture-loading failure is not a valid red phase.

- [x] **Step 4: Correct the production caveat and its comment.**

Replace `writeLoggingCaveat` and its preceding comment with:

```go
// writeLoggingCaveat qualifies durations and rankings because logging overhead
// varies with each call's output. It precedes the no-spans early return so the
// qualification remains visible on every report path.
func writeLoggingCaveat(b *strings.Builder) {
	fmt.Fprintf(b, "Durations here are measured under logging, which is not\n")
	fmt.Fprintf(b, "free: one workspace planned in 24.1s unlogged and 522.2s\n")
	fmt.Fprintf(b, "with debug plus provider TRACE. A call that logs heavily\n")
	fmt.Fprintf(b, "is inflated more than one that waits, so rankings are\n")
	fmt.Fprintf(b, "approximate and absolute times do not transfer.\n\n")
}
```

This matches the meaning and current full TUI caveat. Keep it in the profile
package: importing the TUI for copy or adding a shared package for one paragraph
would create unnecessary coupling. Retain its call before the no-spans branch
and leave existing rounding, saturation and clamped-start warnings intact.

- [x] **Step 5: Correct the README's ranking paragraph.**

Replace the paragraph beginning “Rankings within a log therefore hold” with:

```markdown
Rankings within a log are approximate. A call that logs heavily is inflated
more than one that waits, so the logging overhead can change their order.
Absolute durations do not transfer to a run without logging, and comparisons
between a chatty provider and a quiet one are particularly unreliable.
```

Retain the preceding empirical example as an example, with no extrapolation or
correction factor. Do not change capture instructions or the disclosure guidance.

- [x] **Step 6: Format and confirm the green phase.**

```bash
gofmt -w internal/profile/profile.go internal/profile/profile_test.go
go test ./internal/profile -run 'TestReportStatesDurationsAreMeasuredUnderLogging|TestLoggingCaveatSurvivesTheNoSpansPath' -count=1
go test ./internal/profile ./internal/tui
```

Expected: all pass. Existing tests still protect UI-hook resolution, saturated
duration warnings, clamped starts, unmasked-output warnings and TUI caveat layout.
Inspect the diff to ensure gofmt did not reveal unrelated changes.

- [x] **Step 7: Review the actual copy on all existing surfaces.**

```bash
go run ./cmd/tfli --profile testdata/provider-rpc.log
go run ./cmd/tfli --profile testdata/structured-ui.log
go run ./cmd/tfli --profile testdata/core-only.log
go run ./cmd/tfli --help
rg -n 'rankings|Rankings|same cost|same tax|only rankings transfer' README.md cmd/tfli/main.go internal/profile/profile.go internal/tui/layout.go internal/tui/help.go
```

Check each rendered report has the approximation warning; the no-span report
places it before “NO SPANS”. UI-only output still states whole-second resolution.
CLI help makes no guarantee that conflicts with the corrected copy. Check the
TUI full/compact constants and the help path that renders them; existing TUI
tests cover these unchanged surfaces. Historical quotations in TUI comments are
expected search results, not justification to rewrite the file or its goldens.

- [x] **Step 8: Run independent review and the separate test-cleanup pass.**

Request an independent reviewer to inspect the three-file diff against this
task and spec section 2. Require checks that the false guarantee is removed,
all existing caveat paths remain intact and no timing calculations changed.

After implementation, run the test-cleanup skill in a separate subagent, as
required by the repository. Give it the changed test diff and the behavioural
requirements: all three report paths need the qualification, and the no-span
warning precedes the early-return explanation. It may remove redundant tests
only while preserving that coverage. The implementer must not clean up its own
tests. Resolve substantive findings before final validation.

- [x] **Step 9: Run the repository checks after review changes.**

```bash
gofmt -d .
go mod tidy -diff
go mod verify
golangci-lint run --timeout=5m
go test -race -count=1 ./...
go build ./...
```

Expected: no formatting/module diff, successful checksum verification, no lint
diagnostics, passing race tests and successful build. Use the configured linter
version (CI currently uses 2.13.2); do not install a new tool without discussion.
Inspect all output and report any blocked check explicitly. Existing GitHub CI
also cross-compiles Linux/macOS amd64/arm64; confirm those jobs before integration.

- [x] **Step 10: Commit the completed task with signing and intact hooks.**

```bash
/Users/dan/.codex/bin/codex-git status --short --branch
/Users/dan/.codex/bin/codex-git diff --check
/Users/dan/.codex/bin/codex-git add internal/profile/profile.go internal/profile/profile_test.go README.md
/Users/dan/.codex/bin/codex-git commit -S -m "Qualify timing rankings under variable logging overhead"
/Users/dan/.codex/bin/codex-git log -1 '--format=%h %G? %s'
/Users/dan/.codex/bin/codex-git status --short --branch
```

Expected: signed commit with `G` verification status and no unexpected changes.
If task tracking changes this plan's checkboxes, review and stage that exact plan
path too before committing. Stop immediately on signing failure. If additional
review rounds require commits, sign each one. Commit the red/green/docs change
as one coherent task; there is no value in publishing an isolated failing test
commit for this small change.

## Completion evidence and handoff

Report the signed commit, red-phase failure observed, green validation results,
independent review/test-cleanup outcome and any remaining blocked CI checks.
The deliverable is corrected existing copy, not implementation of the other
investigation features. No JSON qualification API is introduced by this task;
the JSON and comparison plans must encode the same three claims when their
output contracts are designed.

## Plan self-review

This section records the review at planning time; execution evidence follows.

- Scope: section 2's existing-profile/README correction maps to steps 2–7;
  unchanged TUI/help and separate timing limitations map to steps 6–7.
- Future JSON/comparison qualifications belong to boundaries G/H and are
  explicitly excluded from this independently shippable boundary A.
- Tests use the existing fixture-backed render helper, with no new interface,
  fixture or dependency. No exact wrapping or full-report snapshot is pinned.
- Review, test cleanup, full verification and signed commits map to steps 8–10.
- No application code has been changed or tests added merely by writing this plan.

## Execution evidence — 9 September 2026

- Implemented on `wip/app-gap-review` in signed commit `b5b3cba`
  (`G` signature verification). The initial clean branch was rebased against
  `origin/main`, which was already up to date.
- TDD: the strengthened qualification test failed on all three fixtures with
  the old equal-cost guarantee; the no-span placement test passed. After the
  copy correction, the focused tests and full suite passed.
- Inspected RPC, UI-only and no-span CLI reports plus CLI help. The no-span
  caveat precedes `NO SPANS`; TUI full/compact caveats and help remain consistent.
- Independent task review and final whole-branch review found no issues.
  A separate test-cleanup subagent kept all four touched cases: each protects
  a distinct report path or warning placement. No tests were removed.
- Final checks passed: `gofmt -d .`, `go mod tidy -diff`, `go mod verify`,
  `golangci-lint run --timeout=5m` (2.13.2), `go test -race -count=1 ./...`
  and `go build ./...`. Formatting/module checks produced no diff; lint
  reported zero issues. No code changed after these checks.
- GitHub CI, including its Linux/macOS amd64/arm64 build matrix, remains
  pending a push/PR and must pass before integration. No push or merge was
  performed as part of this implementation.
