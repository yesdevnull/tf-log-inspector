# Investigation JSON v1

This alpha contract is produced by `tfli --investigate --format json` and by
JSON export from an investigation panel. `schema_version` is `1` and `kind` is
`"investigation"`. The report contains unmasked identifiers and original event
messages; review it before sharing.

All field names use `snake_case`. Arrays preserve source or report order and
empty collections are `[]`. An unavailable clock, count, source, selected
source, summary or last-progress observation is `null`; an observed zero stays
`0`. Timestamps and clock origins use RFC3339Nano. Source line ranges are
one-based and inclusive, while byte ranges are half-open.

The root fields are `schema_version`, `kind`, `tool_version`, `input`, `scope`,
`timing_scope`, `event_scope`, `timings`, `events`, `incomplete_operations`,
`diagnostics`, and `milestones`. `input.basename` contains no directory.

`scope` records `panel`, literal `query`, exact `address`, `kind`, `severity`,
and nullable `selected_source`. Event-panel filters select event evidence only;
they never silently filter timing rows. `timing_scope` separately describes the
active provider, method, resource type, exact address, module subtree, exact
module, duration source and lifecycle-action values. `event_scope` states how
the event selection was formed. A command-line report uses whole-capture scope.

A source object has `entry`, `start_line`, `end_line`, `start_byte`, and
`end_byte`. Each timing has `tier`, `address`, `action`, `source`,
`qualification`, `duration_ms`, `lower_bound`, nullable `clock_origin`, and
nullable `location`. RPC and resource timing tiers measure different work and
must not be added. CLI elapsed evidence has no clock merely because it has an
observed duration. A zero duration remains a measured zero.

Each event has `kind`, `address`, `action`, `message`, `severity`, `source`,
`deposed_key`, nullable `timestamp`, `location`, and nullable `summary`.
Messages retain original evidence, including control characters safely encoded
by JSON. A summary has `operation`, `add`, `change`, `remove`, `import`, and
`action_invocation`; each count is independently nullable.

An incomplete operation has `start`, nullable `last_progress`, and `ambiguous`.
It retains its start context when only its progress matched the panel filter.
Missing completion is evidence of an incomplete capture or lifecycle and does
not prove a hang. A diagnostic group has `severity`, `address`, `message`,
`source`, and every original event in `occurrences`.

Each milestone has `label` and its representative `event`. Milestones are
chosen from the full capture before applying an export selection, so a later
matching observation is never relabelled as the capture's first observation.
Observed milestone activity can overlap and does not imply sequential phases.
