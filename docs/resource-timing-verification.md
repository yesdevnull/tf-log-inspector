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
