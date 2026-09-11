# Profile JSON v2

Profile JSON exports the complete admitted evidence used by `tfli`'s profile report. It contains unmasked identifiers and source references, but no raw log bodies, credential fields, absolute input path, or generation timestamp. Version 2 makes no compatibility promise for a future schema version.

All object fields are mandatory and appear in the order documented below. Names use `snake_case`. Arrays preserve report order except `candidate_counts`, which is numerically ascending. Empty arrays and maps are `[]` and `{}`. Unavailable values are `null`; a measured zero remains `0`. Durations and offsets are integer milliseconds. Counts, byte offsets, and line numbers are JSON integers and may exceed JavaScript's exactly representable integer range. Invalid UTF-8 in any exported string makes rendering fail with the fixed diagnostic `profile JSON contains invalid UTF-8`.

The root fields are `schema_version` (always `2`), `kind` (`"profile"`), `tool_version`, `input`, `duration_unit` (`"ms"`), `tiers`, `quality`, `rpc_observations`, `ui_observations`, `aggregates`, `timeline`, and `qualifications`.

`input` has `basename` and `bytes`. A `source` object has `entry`, `start_line`, `end_line`, `start_byte`, and `end_byte`; lines are one-based inclusive and bytes are half-open. A missing source is `null`.

Each `tiers.rpc` and `tiers.ui` object has `duration_available`, `records`, `admitted`, `rejected`, `positioned`, `excluded`, `duration_ms`, `positioned_ms`, `excluded_ms`, `duration_lower_bound`, `clock_origin`, and `exclusions`. Origins use UTC RFC3339Nano. Reason maps count observations and can overlap.

Each RPC observation has `index`, `entry`, `source`, `method`, `provider`, `resource_type`, `duration_ms`, `position`, and `attribution`. Each UI observation has `index`, `entry`, `source`, `address`, `action`, `resource_type`, `duration_ms`, `duration_lower_bound`, and `position`. An index identifies the original element in its tier array. `position` has `start_ms`, `end_ms`, `valid`, `reasons`, and `start_clamped`; both offsets are null unless valid.

`attribution` has `confidence`, `address`, and `candidates`. Confidence is `no_context`, `unattributed`, `ambiguous`, `overlapping`, `likely`, or `contained`. No-context, unattributed, and ambiguous observations have a null address. An inferred address always retains its confidence.

`quality` has `scope` (`"whole_log"`), `provider_entries`, `structured_lines`, `has_address_context`, `issues`, `attribution`, `nameable_ms`, `rpc_duration_ms`, `nameable_share`, and `reconstruction`. Each issue has `stage`, `code`, `count`, and nullable `first_entry`. Attribution is null without address context; otherwise it has `spans`, `duration_ms`, `by_confidence`, and `candidate_counts`. The five confidence totals appear as unattributed, ambiguous, overlapping, likely, and contained, each with `confidence`, `count`, and `duration_ms`. Candidate rows have `candidates` and `count`.

`reconstruction` has `state`, `responses`, `diagnostics`, and `code`. `not_checked` has three null details. `complete` has a measured response count, including zero, zero diagnostics, and a null code. `partial` has positive response and diagnostic counts with code `reconstruction_partial`. `failed` has zero responses, a positive diagnostic count, and code `reconstruction_failed`. Diagnostic counts are diagnostic records; ownership failures can produce a trigger diagnostic and aborted-message diagnostics. Export never triggers reconstruction.

`aggregates` has `providers`, `resource_types`, `resources`, `ui`, `unnamed_ui`, and `rpc_evidence`. Provider rows have `provider` and `rpc`. Resource-type rows have `resource_type`, `rpc`, and `ui`. Resource rows have `address`, `ui`, `named_rpc`, `overlapping_rpc`, and `ui_observation_indices`. A total has `count`, `total_ms`, `max_ms`, and `lower_bound`. UI and RPC measure different work and must not be added. In `rpc_evidence`, `baseline` is the denominator represented by the seven disjoint partitions `missing_type`, `no_context`, `contained`, `likely`, `overlapping`, `ambiguous`, and `unattributed`; do not add `baseline` to those partitions.

`timeline` has `tier`, `status`, `clock_origin`, `window_scope` (`"zero_to_latest_positioned_end"`), `admitted`, `positioned`, `excluded`, `admitted_ms`, `admitted_lower_bound`, `positioned_ms`, `excluded_ms`, `excluded_lower_bound`, `exclusions`, `metrics`, `threshold_ms`, and `intervals`. Tier is `rpc`, `ui`, or null; status is `complete`, `partial`, or `unavailable`. No tier has null tier, origin, metrics, and threshold with zero totals and empty collections. The selected tier never falls back to another clock.

Metrics has `window_ms`, `peak`, `busy_ms`, nullable `busy_fraction`, `summed_duration_ms`, and nullable `summed_window_ratio`. A zero window keeps a metrics object but has null ratios. Every interval has `start_ms`, `end_ms`, `duration_ms`, `min_running`, `max_running`, `observed_peak`, and nullable `active_observation_index`. That index refers to the original selected-tier observation. It identifies the longest observed active operation, not a causal blocker.

`qualifications` contains, in order: `unmasked_identifiers` (identifiers are disclosed), `logging_affects_durations` (instrumentation changes timing), `rpc_and_ui_measure_different_work` (tiers are not additive), `ui_duration_rounding` (each UI duration can differ by up to one second), `observed_gaps_do_not_prove_idleness`, and `active_observation_does_not_prove_blocking`. Saturation, clamping, and positioning reasons stay on their measured evidence.

Go consumers should preserve large integers with typed fields or `json.Decoder.UseNumber`:

```go
decoder := json.NewDecoder(reader)
decoder.UseNumber()
var profile map[string]any
if err := decoder.Decode(&profile); err != nil {
	return err
}
```
