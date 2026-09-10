# Task 1 report: complete JSON schema projection

## Files and decisions

- `internal/profile/data.go` snapshots `Log.ReconstructionQuality()` during `Build` without triggering lazy reconstruction.
- `internal/profile/json.go` defines the public `JSONMetadata` input required by the later renderer task.
- `internal/profile/json_data.go` defines private, explicitly tagged v1 wire structs and projects every report field. It preserves source identity, original observation and aggregate order, nullable values, wide integers, separate UI/RPC totals, attribution confidence, reconstruction state, and original-array timeline references. It copies mutable maps, slices, pointers and source objects.
- `internal/profile/json_data_test.go` covers real loaded logs plus synthetic states unavailable from convenient fixtures: complete counts above 20, zero and unavailable positions, large `uint64` values, confidence and reconstruction states, numerical candidate order, detached ownership, invalid UTF-8, unsupported enums, and invalid interval references.
- `internal/profile/json_benchmark_test.go` generates fixed sanitised reports outside the timed loop and measures 1,000 and 10,000 observation projections with allocations.
- `docs/profile-json-v1.md` documents every v1 field, units, ordering, nulls, references, disclosures, qualifications, and integer-preserving decoder use.

The projection copies authoritative report totals and correlations. It does not recalculate rollups, sort report slices, trigger reconstruction, add a renderer, or add CLI behaviour.

## TDD evidence

Initial scaffold RED:

```text
$ go test ./internal/profile -run 'TestJSONProjection|TestBuild.*Reconstruction' -count=1
internal/profile/data_test.go:151:9: got.Reconstruction undefined
internal/profile/json_data_test.go:18:14: undefined: buildJSONProfile
internal/profile/json_data_test.go:18:39: undefined: JSONMetadata
FAIL
```

Behavioural RED after the minimal API returned an empty projection:

```text
$ go test ./internal/profile -run 'TestJSONProjection|TestBuild.*Reconstruction' -count=1
--- FAIL: TestJSONProjectionRetainsPhysicalSources
    json_data_test.go:23: lost observations
FAIL
```

Focused GREEN after projection:

```text
$ go test ./internal/profile -run 'TestJSONProjection|TestBuild.*Reconstruction' -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/profile
```

## Verification and benchmark

```text
$ go test ./internal/model ./internal/profile -count=1
ok github.com/yesdevnull/tf-log-inspector/internal/model
ok github.com/yesdevnull/tf-log-inspector/internal/profile

$ go test ./...
ok github.com/yesdevnull/tf-log-inspector/cmd/tfli 0.982s
ok github.com/yesdevnull/tf-log-inspector/internal/attrib (cached)
ok github.com/yesdevnull/tf-log-inspector/internal/diagnose (cached)
ok github.com/yesdevnull/tf-log-inspector/internal/logfmt (cached)
ok github.com/yesdevnull/tf-log-inspector/internal/model (cached)
ok github.com/yesdevnull/tf-log-inspector/internal/profile 0.366s
ok github.com/yesdevnull/tf-log-inspector/internal/qualitytext (cached)
ok github.com/yesdevnull/tf-log-inspector/internal/scrub (cached)
ok github.com/yesdevnull/tf-log-inspector/internal/span (cached)
ok github.com/yesdevnull/tf-log-inspector/internal/tui 1.130s
ok github.com/yesdevnull/tf-log-inspector/scripts (cached)

$ go vet ./...
(no output; passed)

$ go test ./internal/profile -run '^$' -bench BenchmarkJSONProjection -benchmem -count=1
BenchmarkJSONProjection/observations-1000-10       128465 ns/op   327039 B/op    3012 allocs/op
BenchmarkJSONProjection/observations-10000-10     1118756 ns/op  3216656 B/op   30012 allocs/op
PASS
```

## Self-review

I compared each struct and mapping against the contract tables and checked the complete diff for accidental renderer or CLI scope. All fields have explicit JSON tags and none use `omitempty`. Empty collections are initialised, optional numeric/string fields use pointers, origins are copied and formatted in UTC RFC3339Nano, and invalid strings return the fixed error without including input. Timeline blocking values accept only `-1` as the null sentinel and otherwise use the existing checked `intervalObservation` mapping.

The benchmark is intentionally descriptive and has no threshold. Rendering and the single-write guarantee belong to Task 2, as directed. No implementation concerns remain for Task 1.
