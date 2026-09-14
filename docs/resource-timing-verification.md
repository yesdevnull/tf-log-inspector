# Resource timing verification

The three scrubbed captures supplied for the September 2026 review were checked
without adding their contents to the repository. An independent parser reconciled
every exported observation against its original address, duration and physical
source lines. CLI positions remain unavailable; neither structured nor CLI
resource evidence is presented as provider RPC timing.

| Capture | Reported UI operations | Refresh windows | CLI completions |
| --- | ---: | ---: | ---: |
| Slow structured plan | 267 / 6,708,000 ms | 385 / 2,882,212 ms | 0 |
| Normal structured plan | 267 / 58,000 ms | 385 / 517,964 ms | 0 |
| Normal CLI plan | 0 | 0 | 350 / 204,000 ms |

Refresh durations are floored to milliseconds per window. The unquantised sums
are 2,882,410.174 ms and 518,157.929 ms. Operation totals can overlap and do not
represent run elapsed time.

Terminal checks covered initial Resources selection, operation details, physical
source navigation, Types, and Timeline. Refresh details retain both source
endpoints. CLI observations have no timeline lanes. Missing RPC durations display
as unavailable. Resource goldens were inspected at 60, 70, 100 and 160 columns.

Profile and comparison JSON remain at schema version 1 under the alpha policy.
Comparing structured and CLI captures keeps all three duration sources separate
and reports unavailable deltas where the other capture lacks a source.

Independent review found and drove regression fixes for malformed refresh pairing,
JSON core evidence without a module, missing resource-type source breakdowns, and
diagnostic lower-bound markers. Tests exercise real extraction and rendering with
small synthetic fixtures. A separate cleanup pass retained all 82 reviewed tests.

Validation uses `go test ./...`, `go test -race -count=1 ./...`, `go build ./...`,
`golangci-lint run --timeout=5m`, coverage, formatting checks and signed-commit
verification. Captures and generated reports remain outside the repository.

## Viewing follow-up

The September 12 viewing pass adds a capture summary independent of filename
length, per-resource source labels, collapsed empty inferred-RPC details, and
wrapped timing guidance. At short heights, evidence guidance preserves room for
the selected row. Calls explains captures without RPC timings; associated-call
views retain their selection-specific qualifications.

All three supplied captures were opened in the terminal to check the summary,
source labels and resource details. CLI Calls guidance was checked interactively.
Updated resource goldens were inspected at 60, 70 and 160 columns, together with
the raw styling diffs. Full tests, race tests, build, lint and coverage pass.
Independent review found no defects; separate cleanup retained all nine tests
added or modified in this pass. JSON contracts and extraction are unchanged.

## Event inspection and timing scope

The September 14 pass adds source/action timing filters, exact-module rankings,
observed means, resource event history, plan outcomes and incomplete-operation
evidence. Event source links retain physical file order; CLI events have no
invented clock. Incomplete operations never enter completed timing rankings.

An independent inventory reconciled the supplied captures with the event index:

| Capture | Starts | Progress | Completions | Drift | Planned resource entries | Incomplete |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Slow structured plan | 652 | 645 | 652 | 11 | 0 | 0 |
| Normal structured plan | 652 | 0 | 652 | 12 | 0 | 0 |
| Normal CLI plan | 863 | 0 | 350 | 0 | 53 | 0 |

The structured summaries explicitly report zero additions, changes and removals.
The CLI summary reports 52 additions and zero changes/removals; its 53 resource
entries include one deferred read. Missing event categories remain unavailable
evidence, rather than proving that no activity occurred. The CLI's 513 refresh
starts do not promise completion markers and are excluded from incomplete work.

Checks against all three captures covered the new panels at 60, 100 and 160
columns, initial Resources selection, exact-module drill-down and Escape return.
Synthetic navigation tests cover source jumps, nested resource histories,
ambiguous starts, missing counts, observed zeroes and lower-bound means.

The CLI capture also exposed missing closing ID brackets. Synthetic reproduction
confirmed two ANSI-related scrubber defects: a trailing colour reset could be
consumed with the ID, and a leading style could hide a resource label from
discovery. Regression tests preserve terminal syntax and alias linkage while
masking identifiers. Adjacent colour-separated credential fields are checked as
a separate privacy boundary. The event parser can read previously affected
refresh lines without interpreting their truncated identifier suffixes.
