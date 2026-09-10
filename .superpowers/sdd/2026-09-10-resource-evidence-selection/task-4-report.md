# Task 4 benchmark and verification report

Added `internal/model/resources_benchmark_test.go` with one synthetic fixture built entirely outside timed sections. It contains 10,000 module-qualified exact addresses, three UI operations per address and 100,000 RPCs. RPC attribution cycles evenly through Contained, Likely, Overlapping, Ambiguous and Unattributed states, while two providers permit selective base filtering.

The benchmarks consume and validate each result. BuildResourceIndex retains all 30,000 UI operations, 100,000 parallel RPC module slots and 10,000 choices. SelectResources is measured unconstrained, with provider plus one exact resource, and with provider plus one module subtree. The exact resource case retains three UI observations and ten RPCs; the module case retains 300 UI observations and 1,000 RPCs.

## Measurement

Command:

`go test ./internal/model -run '^$' -bench 'BenchmarkResource' -benchmem -benchtime=200ms -count=3`

Environment: Go 1.27.1; Darwin 24.6.0; arm64; Apple M4. The benchmark reports `fixture_addresses=10000`, `fixture_ui=30000` and `fixture_rpcs=100000` on every result. There is deliberately no machine-specific pass threshold.

| Benchmark | Three timings | Median bytes/op | Median allocations/op |
| --- | --- | ---: | ---: |
| BuildResourceIndex | 44.23ms, 45.09ms, 45.39ms | 20,173,152 | 450,098 |
| SelectResources / unconstrained | 5.98ms, 5.96ms, 6.07ms | 12,897,010 | 30,152 |
| SelectResources / provider and exact resource | 2.13ms, 2.26ms, 2.16ms | 664 | 13 |
| SelectResources / provider and module | 12.41ms, 12.46ms, 12.15ms | 3,935,206 | 120,342 |

Inspection confirmed that neither benchmark path rescans source bytes or performs a nested lookup whose iterations grow with the full address collection. ModuleContains structurally parses the selected parent and observed child for every module match. That bounded parsing accounts for the module case's allocations and its cost relative to the unconstrained case. D2 owns reusing a projection between relevant result-filter changes; no D1 cache, parallel loading or storage change is justified by this baseline.

## Verification

The first `golangci-lint run --timeout=5m` exposed an existing Task 2 test loop that copied a `Log`, including its `sync.Once`. This was a pristine copylocks failure, not a production bug:

```text
internal/model/resources_test.go:130:9: copylocks: range var tc copies lock: struct{name string; log github.com/yesdevnull/tf-log-inspector/internal/model.Log; wantOperationKnown bool} contains github.com/yesdevnull/tf-log-inspector/internal/model.Log contains sync.Once contains sync.noCopy (govet)
1 issues:
* govet: 1
```

`resources_test.go` now iterates that table by pointer. `go test ./internal/model -run TestResourceIndexSuppliedUnavailableModuleEvidenceIsSticky -count=1` passed, and the focused lint rerun reported `0 issues.`.

The complete post-fix local gate passed:

- `go test -race -count=1 ./...` — all 11 packages passed.
- `go build ./...` — exited 0 with no output.
- `golangci-lint run --timeout=5m` — reported zero issues.
- `gofmt -d .` — exited 0 with no diff.
- `go mod tidy -diff` — exited 0 with no diff.
- `go mod verify` — reported all modules verified.

These checks ran locally on Darwin/arm64. The repository's existing CI matrix remains responsible for linux/darwin amd64/arm64 builds.

## Handoff state

Task 3 independent behavioural and quality review approved signed commit `0d3076c`. Its separate cleanup classified all six added test functions and retained every case without edits. The durable plan now reconciles the minor file-placement discrepancy: all selection/projection regressions belong in `resource_selection_test.go`; `resources_test.go` retains index tests.

Task 4's separate cleanup/review, the whole-D1 review and the D2 interface dependency review remain pending with the controller. This benchmark and verification pass does not claim that the D2 resource screen exists.
