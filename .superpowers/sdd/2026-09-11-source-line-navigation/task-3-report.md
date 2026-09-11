# Task 3 report: Correct sharing qualifications and document the new key

## Implemented

- Qualified `--diagnose` help as masked output that must be reviewed before sharing.
- Reworded command and package prose so diagnose and profile outputs are described by their actual disclosure properties, without implying diagnose is safe or shareable.
- Changed profile output to say that resource addresses are unmasked and the report must be reviewed before sharing.
- Reworded the diagnose disclosure test comment around its structural invariant.
- Added the exact `g` help entry `go to a physical source line` and updated the intentional 60- and 100-column goldens.
- Documented `g` in the README, including physical line navigation, widening a filtered call view, horizontal reset, and exact-state return with `Esc`.

## TDD evidence

### RED

Command:

```text
go test ./cmd/tfli ./internal/profile ./internal/tui -run 'Test.*(Help|Warns|SourceLine)'
```

Result: failed as expected. `TestHelpQualifiesDiagnoseOutputBeforeSharing` found the old `output is masked, safe to share` flag text; `TestReportWarnsThatOutputIsUnmasked` found no review-before-sharing instruction; and `TestHelpDescribesPhysicalSourceLineNavigation` found no exact `{keys: "g", what: "go to a physical source line"}` entry. Existing source-line tests also ran in that command and exposed no unrelated failure.

### GREEN

Focused contract command after implementation:

```text
go test ./cmd/tfli ./internal/profile ./internal/diagnose ./internal/tui -run 'Test.*(Help|Warns|SourceLine)'
```

Result: `cmd/tfli`, `internal/profile`, `internal/diagnose`, and `internal/tui` passed with pristine output. The ordinary focused package command also passed:

```text
go test ./cmd/tfli ./internal/profile ./internal/diagnose ./internal/tui
```

The help goldens were regenerated with `go test ./internal/tui -run TestGoldenHelp -update`, then inspected with `scripts/read-golden.sh help-60.txt`, `scripts/read-golden.sh help-100.txt`, and the wrapper diff. Each golden changed only the `g` description.

## Verification

- `go test ./...`: passed.
- `go test -race -count=1 ./...`: passed.
- `go build ./...`: passed.
- `/Users/dan/.codex/bin/codex-git diff --check`: passed silently.
- `golangci-lint run`: failed on the pre-existing `internal/model/location_test.go:83` `copylocks` warning from copying `model.Log`; no Task 3 file is involved.
- Maintained-prose search found only the existing accurate comparison-report warning and the test assertions, plus the TUI package comment calling the full-screen mode the least shareable invocation. No diagnose safety assurance remains.

## Files changed

`README.md`, `cmd/tfli/main.go`, `cmd/tfli/main_test.go`, `internal/diagnose/diagnose_test.go`, `internal/profile/profile.go`, `internal/profile/profile_test.go`, `internal/tui/help.go`, `internal/tui/help_test.go`, `internal/tui/testdata/golden/help-60.txt`, and `internal/tui/testdata/golden/help-100.txt`.

## Self-review

The existing Task 2 tests already cover that `g` acts only with the Raw Log list focused, so no duplicate behaviour test was added. The wording tests exercise real CLI/profile/help output. The only unresolved verification concern is the unrelated existing `golangci-lint` `copylocks` finding described above.
