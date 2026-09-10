# Comparison JSON v1

Comparison JSON reports the observed differences between two complete profile
captures. It contains no generated timestamp, absolute path, raw body,
interned identifier, source observation array, or copied profile ranking list.
Schema version 1 makes no compatibility promise for future versions.

All fields are mandatory and use `snake_case`; field order is the order below.
Empty arrays and objects are `[]` and `{}`. Unavailable values are `null`; a
measured zero remains `0`. Durations and offsets are integer milliseconds.
Exported strings must be valid UTF-8, otherwise encoding fails with
`comparison JSON contains invalid UTF-8`.

## Root

The root has `schema_version` (`1`), `kind` (`"comparison"`), `tool_version`,
`duration_unit` (`"ms"`), `before`, `after`, `comparability`, `sections`, and
`qualifications`.

Each capture has `input`, `tiers`, `quality`, and `unnamed_ui`. `input`,
`tiers`, and `quality` use exactly the fields, nullability, and meanings of
[Profile JSON v1](profile-json-v1.md), including each capture's local
quality entry locations and reconstruction status. `unnamed_ui` is a summary
or `null` when the UI tier is unavailable. The shared capture objects retain
the same origins, quality summaries, rejected counts, context limitations, and
reconstruction snapshots as the source profile reports.

`comparability` has `logging_configuration` (always `"unknown"`),
`provider_identity_status`, `before_providers`, and `after_providers`.
Provider arrays are sorted unique nonempty observed RPC provider identities.
Identity status is `unknown` if either RPC tier is unavailable or either side
has an empty provider identifier; otherwise it is `same` or `different` based
on the identity sets. It does not establish provider-version equivalence.

## Sections and rows

`sections` always contains five sections in this order:

1. `rpc_providers` (`rpc`), keyed by `provider`.
2. `rpc_resource_types` (`rpc`), keyed by `resource_type`.
3. `rpc_methods` (`rpc`), keyed by `provider`, `resource_type`, `method`.
4. `ui_resource_types` (`ui`), keyed by `resource_type`.
5. `ui_operations` (`ui`), keyed by `address`, `action`.

Each section has `kind`, `tier`, `before_available`, `after_available`, and
`rows`. A tier is available when its admitted observation slice is nonempty,
including observations whose positions are invalid. Raw strings are retained,
including empty identifiers; keys are structured objects, so empty strings and
embedded separators cannot collide. UI `action` is the observed span action
(`Span.RPC`). An empty UI address contributes to UI resource-type totals and
the capture's `unnamed_ui` summary, but has no exact-address operation row.
Empty action remains an exact action on a known address. UI addresses are never
inferred from RPC attribution.

Each row has `key`, `state`, `before`, `after`, and `changes`. State is one of
`matched`, `added`, `removed`, or `unavailable`. `added` and `removed` describe
observation presence, not Terraform actions. If either whole tier is
unavailable, the row is `unavailable`, the unavailable side is `null`, and all
changes are `null`. When both tiers are available, absent groups have a zero
count and total, with null mean and maximum.

The side summary fields are `count`, `total_ms`, `mean_ms`, `max_ms`, and
`lower_bound`. A summary is null for an unavailable tier. A present summary
counts admitted observations, sums their durations, uses
`float64(total_ms)/float64(count)` for its mean, and records the maximum. A
lower-bound flag is true when any contributing UI duration was saturated.
Counts describe admitted evidence, not the number of operations the logging
could have missed.

`changes` has `count`, `total_ms`, `mean_ms`, `max_ms`, `count_percent`,
`total_percent`, `mean_percent`, and `max_percent`. Every change is
after-minus-before. Count, total, and maximum are exact signed JSON integers
(`null` when unavailable); means and percentages are finite JSON numbers or
`null`. Percentages are `100 * change / before`; a zero or absent baseline is
unavailable. Missing means or maxima make their corresponding delta and
percentage unavailable. If either side is lower-bounded, total, mean, and
maximum changes and percentages are all unavailable; exact admitted-count
changes remain available. Integer arithmetic is checked and never narrowed
through `int64` or rounded through `float64`; a duration sum overflow fails
with `comparison duration total overflows uint64`.

Rows preserve the complete deterministic model order. Within each section,
exact total-duration changes rank positive increases first, then zero, then
decreases nearest zero first. Rows without an exact total change are last and
sorted by their raw key fields in table order, bytewise ascending. Lower-bound
rows do not participate in exact timing-change ranking. JSON includes every
row, including unranked, unavailable, added, and removed rows; text views may
apply independent limits.

## Qualifications

`qualifications` is an array of string codes in this order:

`unmasked_identifiers`, `logging_affects_durations`,
`rpc_and_ui_measure_different_work`, `ui_duration_rounding`,
`observed_changes_are_not_causal`, `added_removed_are_observation_presence`,
`independent_scrub_aliases_may_differ`, `logging_configuration_unknown`, and
`lower_bounds_do_not_define_timing_deltas`.

The codes disclose that identifiers are unmasked; logging changes measured
durations; RPC and UI-hook durations measure different, overlapping work and
must not be added, subtracted, or treated as interchangeable; UI durations can
differ by up to one second due to rounding; observed changes do not prove
causality; added and removed rows indicate observation presence; independently
scrubbed captures may use different aliases; logging equivalence is unknown;
and lower bounds do not define timing deltas. Comparisons produce observations,
not pass/fail performance judgements.

Consumers must preserve large integer magnitudes with typed integer fields or
`json.Decoder.UseNumber`; JSON integer values may exceed JavaScript's exact
precision range:

```go
decoder := json.NewDecoder(reader)
decoder.UseNumber()
var comparison map[string]any
if err := decoder.Decode(&comparison); err != nil {
	return err
}
```
