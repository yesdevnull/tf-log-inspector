# Task 2 implementation report

Implemented provider and resource-type aggregate drill-down on
`wip/investigation-navigation` from `c1c1786`.

## Behaviour

- Enter on a provider opens Calls with that provider as the only admitted
  provider and preserves every other selection.
- Enter on a type opens Calls when the active projection contains an RPC for
  that type, including explicit zero-duration observations. It opens Resources
  when only selected UI observations remain.
- Aggregate matching uses stable row identities and `model.FacetKey`, including
  `(none)` for unavailable provider or type metadata.
- Esc restores the parent snapshot. Invalid selections, empty tables, non-list
  focus and resource rows remain inert.
- The footer derives `↵ calls`, `↵ resources`, `⏎ open` or no Enter hint from
  the same route predicates used by the key handler.

## TDD evidence

RED, before production changes:

```text
$ go test ./internal/tui -run '^TestDrillDown' -count=1
--- FAIL: TestDrillDownProviderPreservesTypeAndRestoresParent (0.00s)
    drilldown_test.go:22: view = 0, want Calls
FAIL
```

GREEN after the minimal provider route:

```text
$ go test ./internal/tui -run '^TestDrillDownProviderPreservesTypeAndRestoresParent$' -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
```

The expanded route tests initially failed to compile because the required
`enterHint` interface did not exist. After implementing the type predicate and
shared hint route:

```text
$ go test ./internal/tui -run '^TestDrillDown' -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
```

The expanded tests use a parsed temporary A/B/C provider fixture with two
types and repeated provider calls, plus repository fixtures and direct model
observations for zero-duration and unavailable-metadata boundaries.

## Validation

```text
$ go test ./internal/tui -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
$ go test ./... -count=1
ok all packages
$ go vet ./...
(no output)
$ go build ./...
(no output)
$ gofmt -d .
(no output)
$ codex-git diff --check
(no output)
```

Regenerated only `help-60.txt`, `help-100.txt` and
`layout-100-providers.txt`. `scripts/read-golden.sh help-60.txt` and
`scripts/read-golden.sh layout-100-providers.txt` showed the intended updated
Enter/Esc help and the new provider `↵ calls` footer hint; the raw diffs contain
only those lines.

## Files

- Added `internal/tui/drilldown.go` and `internal/tui/drilldown_test.go`.
- Updated Enter dispatch, footer/help text and affected behavioural tests.
- Updated `README.md` and the three intentional goldens above.

## Handoff concerns

The controller still owns independent review, separate test cleanup, real PTY
acceptance, race/lint/module checks and the CI build matrix. Useful PTY routes
are `resources-accounting.log` provider → Calls and
`resources-modules.log` type `aws_instance` → Resources.

## Review fixes

Review identified two missing acceptance boundaries: the selected Types row was
hidden at 60×9, and no single test composed aggregate navigation with a real
reconstructed response. Both were reproduced before production changes:

```text
$ go test ./internal/tui -run '^(TestDrillDownShortTypesFrameShowsSelectedRouteTarget|TestDrillDownProviderCallRawResponseRoundTrip)$' -count=1
--- FAIL: TestDrillDownShortTypesFrameShowsSelectedRouteTarget
    selected type is hidden at 60x9
--- FAIL: TestDrillDownProviderCallRawResponseRoundTrip
    real reconstructed response did not open
FAIL
```

The short-pane root cause was the full Types preamble plus header consuming all
data height. Capping that preamble exposed the row, then the six columns still
front-clipped its identity. The final narrow layout retains the identity and
active sort column while removing trailing non-sort measurements until the
identity has 12 columns. Full qualifications remain in help and evidence, and
row ordering still uses the original selected sort before presentation columns
are trimmed.

The response fixture now parses a normal RPC completion and a real provider
JSON entry under the same request ID. The workflow opens Provider → Calls →
Raw, selects that scoped response entry, reconstructs and renders its body,
dismisses it without changing raw line/column/search state, and returns through
Calls to the exact Provider parent.

```text
$ go test ./internal/tui -run '^(TestDrillDownShortTypesFrameShowsSelectedRouteTarget|TestDrillDownProviderCallRawResponseRoundTrip)$' -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
$ go test ./internal/tui -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
```

No golden files changed in the review fix.

### Active-sort width correction

Scoped re-review found that the narrow Types fitter measured the default sorted
header even after another column became the active sort. A rendered boundary
case proved the active marker could consume two columns from the promised
12-column identity:

```text
$ go test ./internal/tui -run '^TestDrillDownShortTypesFrameKeepsTheActiveSortColumn$' -count=1
--- FAIL: TestDrillDownShortTypesFrameKeepsTheActiveSortColumn
    active sort marker consumed the promised 12-column identity:
    resource t…  UI res.▾              UI total
    …cdefghijkl         1  12345678901234567890
FAIL
```

The fitter now measures `headerCells` with the active sort column. The rendered
boundary case and the real 60×9 route both pass:

```text
$ go test ./internal/tui -run '^(TestDrillDownShortTypesFrameKeepsTheActiveSortColumn|TestDrillDownShortTypesFrameShowsSelectedRouteTarget)$' -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/tui
```

No golden files changed for this correction.
