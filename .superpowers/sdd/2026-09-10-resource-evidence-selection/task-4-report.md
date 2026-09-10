# Task 4 benchmark and verification report

Added `internal/model/resources_benchmark_test.go` with one synthetic fixture built entirely outside timed sections. It contains 10,000 module-qualified exact addresses, three UI operations per address and 100,000 RPCs. RPC attribution cycles evenly through Contained, Likely, Overlapping, Ambiguous and Unattributed states, while two providers permit selective base filtering.

The benchmarks consume and validate each result. BuildResourceIndex retains all 30,000 UI operations, 100,000 parallel RPC module slots and 10,000 choices. SelectResources is measured unconstrained; independently by provider, exact resource and module subtree; and with provider combined with resource or module selection. Provider-only retains all 30,000 UI observations and 50,000 RPCs. Exact resource cases retain three UI observations and ten RPCs; module cases retain 300 UI observations and 1,000 RPCs.

## Measurement

Command:

`go test ./internal/model -run '^$' -bench 'BenchmarkResource' -benchmem -benchtime=200ms -count=3`

Environment: Go 1.27.1; Darwin 24.6.0; arm64; Apple M4. The benchmark reports `fixture_addresses=10000`, `fixture_ui=30000` and `fixture_rpcs=100000` on every result. There is deliberately no machine-specific pass threshold.

| Benchmark | Three timings | Median bytes/op | Median allocations/op |
| --- | --- | ---: | ---: |
| BuildResourceIndex | 41.69ms, 43.05ms, 43.02ms | 20,173,152 | 450,098 |
| SelectResources / unconstrained | 5.84ms, 6.15ms, 5.85ms | 12,897,010 | 30,152 |
| SelectResources / provider only | 5.33ms, 5.31ms, 5.39ms | 10,750,706 | 30,149 |
| SelectResources / exact resource only | 2.50ms, 2.49ms, 2.52ms | 664 | 13 |
| SelectResources / module only | 17.57ms, 16.87ms, 17.40ms | 5,855,200 | 180,342 |
| SelectResources / provider and exact resource | 2.12ms, 2.12ms, 2.10ms | 664 | 13 |
| SelectResources / provider and module | 12.11ms, 12.24ms, 12.14ms | 3,935,200 | 120,342 |

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

The final benchmark review fix added the previously missing independent provider-only, resource-only and module-only measurements without changing production code or dependencies. The required benchmark command passed three times with every fixture/result count assertion intact; targeted and full test commands are recorded in `task-4-fix-report.md`. The fix is committed with the subject `Measure independent resource filters` using mandatory signing.
