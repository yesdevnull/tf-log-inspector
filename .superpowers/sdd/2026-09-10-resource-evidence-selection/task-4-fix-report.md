# Task 4 benchmark review fix

Added independent `ProviderOnly`, `ResourceOnly` and `ModuleOnly` SelectResources benchmark cases to the existing synthetic fixture. The combined provider-and-resource/module cases remain for comparison. No production code or dependency changed.

The cases assert these fixture results on every iteration:

- provider only: 50,000 RPCs and all 30,000 UI operations
- exact resource only: 10 RPCs and 3 UI operations
- module only: 1,000 RPCs and 300 UI operations

Measurement command:

`go test ./internal/model -run '^$' -bench 'BenchmarkResource' -benchmem -benchtime=200ms -count=3`

Result: passed. The complete three-run measurements and medians are recorded in `task-4-report.md`; every result reported 10,000 fixture addresses, 30,000 UI operations and 100,000 RPCs.

Verification commands:

- `go test ./internal/model -count=1`
- `go test ./...`
- `/Users/dan/.codex/bin/codex-git diff --check`

The fix is committed as `Measure independent resource filters` with the Codex git wrapper and mandatory signing. Controller-owned scoped review and cleanup remain pending.
