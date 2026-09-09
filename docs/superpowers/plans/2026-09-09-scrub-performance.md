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

## Follow-up: under ten seconds

Further profiling identified repeated pattern searches, decoded-string and
token allocations, and duplicate rendering as the remaining costs. Required
character checks and quote-delimited searches narrow regex inputs without
changing their match coordinates. Trailing provider-mask whitespace is excluded
from discovery searches. Plain quoted strings reuse their existing bytes, and
source-token reservation inserts maximal Unicode tokens directly into the set.

Address discovery locates dots and their preceding identifier before applying
the existing grammar. IP discovery scans maximal address-shaped runs, consuming
optional zones even when the preceding run is not an IP, and leaves validation
to `netip`. Differential fuzzing compared both searches with their original
regexes: 610,242 address inputs and 315,480 IP inputs passed.

The successful resource-collision validation pass now supplies final rendered
text and replacement counts to fragment assembly. Failed retries discard their
results; cuts remain paired with the successful rendering. Logical JSON and
final structural validation still run. Only enclosing addresses allocate edit
attribution maps, and output preallocation is capped for collapsing credentials.

The authorised 30 MB capture completed in **9.64 and 9.29 seconds**, including
`go run`, without profiling or concurrent tests. Counts remained unchanged:
197,844 names, 1,294 IDs, 25,457 GUIDs, 31,201 network values, 1,059 cloud values,
1,141 paths and 33,824 secrets. There were no unsupported inputs. Capture and
output contents were not read: temporary instrumentation around the authorised
scrub command collected function-level CPU samples only.

The richer 10,000-object synthetic benchmark improved from 1.98 to 0.51 seconds
per operation. Full tests, race tests, separate coverage, build and vet passed;
scrub statement coverage is 96.7%. Independent review checked the scanners,
render reuse and edit attribution. Timing results are local measurements, not
a guarantee for every capture or machine.

## Bounded allocation follow-up

The next pass retained four small optimisations: reject non-numeric first bytes
before JSON validation, reserve exact JSON delimiter storage, reuse bounded
regex results for short repeated values, and skip response-body assignment
searches when the required `body` marker is absent. Delimiter scanning shares
quote-boundary handling with string decoding but does not decode values.
Cache entries own their short strings and preserve occurrence-specific
discovery; their number and input length are bounded.

Independent review identified and resolved duplicate decoding of escaped
strings and retention of large backing strings through short cache keys.
Allocation regressions cover both cases. Warm-cache allocation checks compare
against cold discovery under the same race instrumentation.

Final capture runs took **8.67 and 8.81 seconds**, including `go run`, with the
same replacement counts and zero unsupported inputs. This is a modest gain
over 9.29–9.64 seconds; the pass did not reach five seconds. The synthetic
10,000-object benchmark improved from 0.516 to 0.423 seconds, from 215.5 to
178.3 MB allocated per operation, and from 1.51 to 1.06 million allocations.
These allocation figures describe the synthetic benchmark, not peak capture
memory. Full tests, race tests, separate coverage, build and vet passed;
scrub statement coverage is 96.8%.

Private input remained accessible only through authorised scrub commands.
Temporary instrumentation emitted function-level CPU profiles and aggregate
stage timings and allocation counters, with no log or memory contents read.
