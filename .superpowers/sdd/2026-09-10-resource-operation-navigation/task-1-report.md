# Task 1 implementation report

## Scope

Implemented the observed UI operation list and source navigation within Resources. No RPC association behaviour from task 2 was added.

## TDD evidence

RED command:

```text
go test ./internal/tui -run '^TestResourceOperations' -count=1
--- FAIL: TestResourceOperationsRetainRepeatedSourceIdentity (0.00s)
    resource_operations_test.go:16: operations = 1, want 2
FAIL
```

Focused GREEN command:

```text
go test ./internal/tui -run '^TestResourceOperation' -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
```

Full verification commands:

```text
go test ./...
ok github.com/yesdevnull/tf-log-inspector/internal/tui 0.823s
ok (all other packages, cached or fresh)

go build ./...
exit 0

/Users/dan/.codex/bin/codex-git diff --check
exit 0
```

## Behaviour covered

- Preserves original UI indices for repeated operations through duration and source sorting.
- Opens the exact UI source entry and restores both operation and aggregate selections through Esc history.
- Keeps equal address/action/duration observations distinct.
- Renders missing action and source as unavailable, zero duration as `0s`, and saturated duration with `≥`.
- Allows source navigation without timeline position and refuses invalid source locations without pushing history.
- Preserves nil or multiple-address parent selections and all other selection dimensions.
- Reports rounding, lower-bound, start-clamp and position qualifications separately in detail/evidence.
- Keeps action, duration and source visible at 60×9; full escaped addresses remain in evidence.
- Manual numbered navigation resets operation mode; modal handling remains ahead of history.

## Render inspection

Regenerated `resources-60.txt`, `resources-70.txt`, and `resources-160.txt`. `scripts/read-golden.sh` and the raw diff showed only the new `↵ operations` aggregate footer hint.

## Self-review

Reviewed production and test diffs for index-domain mistakes, accidental RPC treatment of UI rows, filter widening, stale sort/header routing, unsafe display text, and unavailable-source handling. No outstanding task-1 concern found. Independent review, test cleanup, final PTY and CI remain controller-owned as directed.

## Test cleanup

Reviewed every test function or case changed in `b9f5818..1a3841a`: seven new operation tests and four modified existing tests. All eleven were kept because they cover distinct navigation, identity, sorting, unavailable-source, qualification, selection-restoration, escaping, hint, or workflow behaviour. No slop-taxonomy match was found, so no test code was changed and the suite was not repeated.

## Review fix round 1

RED command:

```text
go test ./internal/tui -run 'TestResourceOperationSourceTargetIsVisibleAtSixtyByNine|TestResourceFootersKeepEscapeAndQuitAtSixtyColumns' -count=1
--- FAIL: TestResourceOperationSourceTargetIsVisibleAtSixtyByNine
    narrow source frame hid the selected UI entry; context occupied both raw rows
--- FAIL: TestResourceFootersKeepEscapeAndQuitAtSixtyColumns
    aggregate footer lost clear or quit
FAIL
```

GREEN command:

```text
go test ./internal/tui -run 'TestResourceOperationSourceTargetIsVisibleAtSixtyByNine|TestResourceFootersKeepEscapeAndQuitAtSixtyColumns' -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
```

UI source jumps now cap context from the actual raw pane line budget and place the target at the top of a 60×9 frame. RPC source jumps retain their established three-line behaviour. Resources footers discard secondary sort and facet hints when required to retain Enter, Esc and quit. The singular selected-operation timing copy was corrected. The regenerated `resources-60.txt` was inspected with `scripts/read-golden.sh`; its only change from task 1 is replacement of the clipped sort/facet tail with `Esc clear  q quit`.
