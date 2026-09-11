# Investigation workflow delivery

Delivered to local main through `c51588e`, 11 September 2026. These notes describe
the completed feature set; they do not announce a published release.

## Investigation

- Raw Log search lands on the matching physical line and occurrence, including
  repeated matches inside an entry. Response search uses the same literal
  occurrence rules, and closing a response restores the exact raw position.
- Resources shows observed UI operations, resource/module filters and separately
  qualified RPC associations. Repeated operations retain their original source
  identities. Provider and RPC-method filters affect RPC evidence only.
- Aggregate drill-down connects providers/types, resources, operations, calls
  and raw evidence. Esc restores the parent investigation; numbered view keys
  start a new navigation chain.
- Capture quality remains a whole-log summary even when evidence is filtered.
  Timing reports distinguish missing measurements, measured zero, lower bounds
  and unusable timeline positions. Logging makes rankings approximate.
- Verified provider responses remain inspectable around independent stream
  failures. The viewer distinguishes invalid, unavailable and ordinary positions
  and keeps a partial-reconstruction notice visible. Scrubbing still refuses any
  reconstruction diagnostic and publishes no partial result.

## Reports and comparison

- Text profiles include physical source references, confidence-qualified
  attribution and observed concurrency intervals. `--limit` applies independently
  to each text list; `--limit 0` shows all rows without changing totals.
- `--profile --format json` exports complete structured evidence.
- `--compare` accepts two raw logs and emits text or JSON with separate UI/RPC
  measurements, before/after quality, exact grouping and qualified changes.
  Missing tiers and zero baselines remain unavailable where appropriate.

## JSON v2 migration

Both profile and comparison output now use `schema_version: 2`. Consumers that
require version 1 must update before using these outputs. There is no v1 output
switch or compatibility writer.

The reconstruction object has four mandatory fields in this order:
`state`, `responses`, `diagnostics`, `code`.

| State | Responses | Diagnostics | Code |
| --- | --- | --- | --- |
| `not_checked` | `null` | `null` | `null` |
| `complete` | Measured count, including zero | `0` | `null` |
| `partial` | Positive count | Positive count | `reconstruction_partial` |
| `failed` | `0` | Positive count | `reconstruction_failed` |

The old `checked` state is replaced by `complete`; `partial` represents retained
verified messages alongside reconstruction diagnostics. Diagnostic counts are
records, so ownership failures can produce more than one diagnostic. Ordinary
CLI report generation remains lazy and exports `not_checked`.

See the complete [profile JSON v2](profile-json-v2.md) and
[comparison JSON v2](comparison-json-v2.md) contracts. The v1 documents remain
historical references. Reports contain unmasked identifiers and source metadata;
they are not anonymised outputs.

## Scope

The [completed workflow specification](superpowers/specs/2026-09-09-investigation-workflows-design.md)
records the acceptance criteria. JSON import, interactive comparison, live
tailing, persistent bookmarks and automatic performance thresholds remain
outside this delivery.
