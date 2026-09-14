# Non-TRACE inspection improvements

Dan approved all five opportunities after PR #12: source/action filtering,
resource event history, plan outcomes and drift, incomplete-operation evidence,
and mean durations with module rankings. Work is isolated from updated main.

## Behaviour and boundaries

Resource timing filters add duration source and lifecycle action to the shared
resource selection. They affect resource tables, Types, Timeline and resource
evidence consistently; RPC provider/method selection keeps its own semantics.
Drill-down and Esc restore every selected dimension.

An ordered event index retains resource starts, progress, completions, diagnostic
events and drift from structured UI records and anchored CLI lifecycle lines.
Events retain exact resource addresses and physical source links. CLI uses file
order and no invented clock. Event messages are display-escaped and are never
added to timing totals. A resource history action opens its events and Enter
opens the physical line; Esc returns to the investigation.

A capture outcome panel separates observed plan changes from drift and shows
diagnostics with source jumps. Unknown or missing outcome evidence is unavailable,
never a successful/empty plan inferred from missing data. Incomplete operations
show a start and last observed progress without claiming completion or a hang.
Repeated ambiguous starts must not be paired by guessing. Incomplete evidence is
kept outside completed-duration rankings. Start-to-completion association is by
exact address and action where known; completion-only captures stay usable.

Resources and Types show mean observed duration alongside count, total and max
where space permits. Module rankings group each operation by its exact module
instance, including a named root bucket and unavailable module bucket. Enter
opens scoped resources. Source breakdowns and lower-bound flags remain visible;
summed durations can overlap and never mean run elapsed time.

## Implementation tasks

1. Add the model event/outcome/incomplete index with synthetic TDD fixtures and
   real physical source navigation. Integrate model loading only; leave export
   contracts and scrub admission unchanged. Publish the event API for the TUI.
2. Add shared source/action filters, mean duration and module aggregation with
   TDD. Integrate facets, navigation history and Resources/Types/module rankings.
3. Add navigable event/outcome/incomplete panels using task 1's index, with TDD,
   discoverable key hints, escaped text, compact layouts and return navigation.

Review each task and the integrated changes independently. Run a separate test
cleanup pass after implementation. Verify all five workflows against the three
scrubbed Downloads captures without copying them into the repository. Run full
tests, race tests, build, lint and inspect changed terminal goldens. Sign every
commit with the Codex git wrapper. JSON schema_version remains 1; no new export
mode or external dependency is introduced.
