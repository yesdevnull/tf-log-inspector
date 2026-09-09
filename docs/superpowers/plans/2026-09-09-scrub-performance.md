# Scrubber performance regression

Dan reported that a roughly 30 MB capture increased from 5–10 seconds to
minutes after provider response reconstruction.

## Evidence and correction

A synthetic provider collection reproduced the regression without accessing
private log contents. The initial 10,000-object fixture took 7.35 seconds for
about 620 KB. Its CPU profile attributed 68% of samples to whitespace scanning
and 19% to region containment checks.

Reconstruction masks physical response bodies with spaces. Generic field
discovery searched the remaining whitespace suffix at every byte, producing
quadratic work. Consuming each run once reduced the benchmark to 2.36 seconds.
The whitespace regression test failed at 5.14 seconds before the correction
and passed afterwards, while checking that the following name was scrubbed.

Large logical JSON views also contain many protected delimiters. Rendering
scanned every protected region for each quoted child. A sorted index of starts
and maximum preceding ends preserves the original containment and overlap
predicates, including nested regions, without those repeated full scans.
The same benchmark then took 0.57 seconds. Explicit boundary tests cover the
index, including overlapping intervals whose union cannot imply containment.

The retained benchmark includes additional GUID, URL, boolean, null and numeric
fields. It took 0.21 seconds for 1,000 objects and 2.11 seconds for 10,000 objects;
its CPU profile was dominated by pattern detection. Reproduce with:

```
go test ./internal/scrub -run '^$' -bench BenchmarkProviderResponse -benchtime=1x
```

## Validation

Full tests, race tests, separate coverage, build and vet passed (scrub statement
coverage: 96.2%). Independent review found no
correctness or security regressions. The authorised capture scrub completed in
29.19 seconds; a repeat without concurrent tests took 27.70 seconds, including
`go run`, with all replacement counts and the 632
unsupported-input count unchanged. This improves the regression but does not
restore the reported 5–10-second baseline. No capture or output contents were
read during this investigation; CPU profiles used synthetic data only.
Separate test cleanup retained all 169 branch tests and both benchmarks, with
no removals or follow-up findings.
