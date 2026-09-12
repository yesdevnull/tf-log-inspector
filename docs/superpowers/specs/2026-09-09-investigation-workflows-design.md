# Investigation workflows, evidence quality and run comparison

## Status and purpose

Approved and implemented. All eight improvements and delivery boundaries A–I
are merged into local main through `c51588e` on 11 September 2026. This document
records the delivered behaviour, acceptance criteria and scope decisions.

The investigation flow connects provider/resource-type summaries to resources,
operations, associated calls and original log text. Capture-quality facts and
timing qualifications remain visible through that flow. Text profiles, JSON
exports and raw-log comparisons use the same model calculations.

Dan approved comparison of two raw logs with text or JSON output. JSON-profile
imports and interactive comparison remain outside scope. Both JSON report kinds
use alpha version 1 with explicit reconstruction states; the published
[profile](../../profile-json-v1.md) and [comparison](../../comparison-json-v1.md)
contracts define their exact fields and nullability.

The implementation plans are retired after delivery. Their task history,
approvals, review fixes and detailed validation records remain in Git at
`c51588e` under `docs/superpowers/plans/`. The acceptance evidence below identifies
the maintained tests, and the [release notes](../../release-notes.md) summarise
the user-visible changes. No remote publication is implied by local completion.

## Evidence and relationship to existing designs

The assessment inspected the CLI, model, profiling, attribution, response
reconstruction and TUI code, exercised the application, and ran the full Go test
suite and build successfully at commit `524e757`. Those checks establish the
starting point, not validation of the proposed behaviour.

Two defects identified by that assessment are now resolved:

- Raw-log search now lands on the matching physical line and occurrence,
  including repeated matches inside one logical entry.
- The profile, README and TUI now consistently qualify rankings as
  approximate because logging overhead varies between calls.

The original [application design](2026-09-03-tf-log-inspector-design.md) records
an important constraint: an inferred address-ranking view was withheld after
only 15.9% of RPC time in the measured capture resolved to Contained or Likely,
against its 50% gate. This delivery preserves that gate. The new
Resources view is grounded in observed Terraform UI resource timings; inferred
RPC associations are supplementary evidence, visibly separated from those
timings. Ranking resources by inferred RPC totals remains outside the delivered scope.

The [provider reconstruction design](2026-09-09-provider-fragments-design.md)
requires complete reconstruction before scrubbing can publish output. That
requirement remains. The viewer retains verified messages around failures
without relaxing the scrubber's acceptance rules.

Dan approved the expansion to JSON profile export and CLI run comparison.
The original application design remains a historical record; this document and
the current JSON v1 contracts describe the investigation behaviour.

## Approaches considered

1. **Shared investigation data with separate consumers — implemented.** Add
   focused model functions for resource summaries, capture quality and report
   data. The TUI, text reports and JSON use the same calculations. Navigation,
   search positions and response recovery remain local to their domains. This
   reduces inconsistent results while allowing independent delivery.
2. **Eight independent enhancements.** This makes individual changes easier to
   start but encourages different definitions of coverage, missing values and
   timing totals in each output. Repeated calculations would need reconciliation
   before reliable comparison could ship.
3. **A unified interactive comparison workspace.** This could support side by
   side investigation, but introduces two active logs, paired selection state,
   separate clocks and considerably more navigation. Its additional design
   cost is unnecessary for the first useful comparison report.

Use the existing Go toolchain and dependencies. Avoid a new query language,
storage layer, plugin system or general report framework.

## Shared rules

### Measurements and identity

- UI-hook duration measures a resource operation. RPC duration measures a
  provider call. They overlap and must never be added, subtracted to claim
  unexplained time, or represented as interchangeable measurements.
- UI and RPC timeline offsets have different origins. Every exported timing
  identifies its tier and clock origin when known. Comparisons use durations
  and aggregates, not alignment of raw offsets between captures.
- Whole-second UI measurements retain their rounding qualification. Clamped
  starts and saturated durations retain their flags; saturated totals are
  lower bounds, not exact rankings.
- Source bytes and scanner entry identities remain authoritative. Derived
  views, reconstructed text and filters never modify either.
- An inferred address always retains its confidence. Ambiguous calls never
  acquire a chosen address, and their duration is never divided among candidates.
- Distinguish observed zero, absent observations and unavailable measurement
  tiers. A missing tier cannot become a zero-duration run.

### Duration admission and position validity

Duration evidence and timeline position are independent properties of each
observation. Apply the following contract before building rankings, resource
summaries, comparisons or timeline analysis:

| Input evidence | Duration statistics | Timeline and temporal attribution |
| --- | --- | --- |
| Explicit valid duration, including zero, and a usable timestamp | Include the duration and count | Include the positioned observation |
| Missing/null/invalid/negative duration, with any timestamp | Reject as a timing observation; retain a rejection count | Exclude; do not synthesise a duration |
| Valid duration with a missing or invalid timestamp | Include the duration and count | Position unavailable; exclude |
| Valid duration with a timestamp before its tier's origin or beyond the representable offset range | Include the duration and count | Position unavailable; exclude rather than using a clamped endpoint |
| Valid duration extending before the capture origin, with a usable end timestamp | Include the full duration and count | Retain the existing start-at-zero clamp and its qualification |
| Valid UI duration exceeding duration storage capacity | Include the saturated lower bound and count, flagged on the observation | Position unavailable because the capped duration cannot establish the start; exclude |

RPC durations require the existing unsigned integer millisecond representation;
values outside its storage range are rejected and counted, not silently capped.
UI durations require an explicit finite non-negative JSON number of seconds and
use the existing millisecond rounding. A positive duration that rounds to zero
remains an admitted observation. Missing or null elapsed_seconds is not zero.

Retain per-observation duration saturation and position-validity reasons. A
whole-log counter alone cannot qualify an individual JSON row. For an
unavailable position, both start/end offsets are null; retain its source location
and usable duration. A genuine position at offset zero remains numerical zero.
A tier clock origin is established only from parseable timestamps, and is null
if none exists. It never supplies a missing observation's timestamp.

Duration count, sum, mean and maximum use admitted duration observations,
including those without a usable position. Means divide by that same count;
rejected timing records are counted separately and never dilute a mean. Resource
metadata absence may prevent a named grouping without invalidating a duration.
Duration-based tier availability means at least one admitted observation, even
if all its positions are unavailable.

Timeline selection continues to prefer the RPC tier when it has admitted
durations, otherwise UI. Within that chosen tier and active selection, analyse
only observations with usable positions. Do not silently switch tiers because
the preferred tier lacks usable positions. Report positioned and excluded
observation counts and duration totals, with exclusion reasons and lower-bound
flags. Some excluded observations make the analysis partial; none positioned
makes the timeline metrics and window unavailable, not measured zero or “idle”.
An empty active selection is separately labelled as having no matches.

For a positioned subset, retain the current zero-to-latest-end window and
threshold policy, explicitly scoped to that subset. A zero-length window has an
unavailable busy fraction; zero-duration positioned observations contribute no
busy extent. Start-clamped observations retain their shortened timeline extent.
For RPC attribution, retain the existing end-point rule for clamped starts, with
confidence capped at Overlapping. Unpositioned RPC observations receive no
temporal attribution; if context exists, they remain Unattributed with a
position-unavailable reason. They remain in the RPC duration denominator.
No-context remains a distinct capture-level condition. Observed UI addresses
remain available even when their timing positions are unavailable.

### Disclosure and diagnostics

Text profiles, JSON exports and comparisons contain unmasked resource addresses
and are for local investigation. JSON contains structured timing and source
references, not raw log bodies or credential fields. This limits its content but
does not make it an anonymised artefact.

Terminal rendering escapes untrusted controls. Machine output uses valid JSON
encoding and leaves identifier values intact. Diagnostics use fixed reason codes,
counts and locations; they do not embed source text or JSON parser snippets.
`--diagnose` continues to avoid resource names and captured field values.

### Scope and reproducibility

Whole-log quality facts remain labelled as whole-log facts. Filtered tables and
timeline summaries report their selection scope. Filtering cannot make an input
anomaly disappear from the capture summary or turn an unavailable tier into an
available one.

Every ranking has a deterministic tie-break: domain identity, then source entry
and occurrence where needed. Stable ordering is a presentation guarantee, not
an assertion that nearly equal durations differ meaningfully.

## 1. Search that lands on the matching text

### Behaviour

Keep literal, case-sensitive search with `/`, `Enter`, `Esc`, `n` and `N`.
Do not introduce regular expressions, wraparound or a new search language.

Represent a raw-log match by entry, physical line within the entry and position
within that line. Search the same safe display text the pane renders: remove
ANSI sequences, then apply the existing visible escaping of controls. Translate
the match's text position into display columns before setting horizontal scroll.
Byte offsets must not be mistaken for terminal cell widths.

A submitted query includes the current top line from the current horizontal
position. If it has no match there, continue forward through visible lines.
Successful search brings the matched line and the start of the match into view.
An over-wide match need not fit in full. A failed search leaves the viewport
unchanged and reports failure.

`n` and `N` advance between non-overlapping occurrences, including multiple
occurrences on the same line and inside the same logical entry. Search stays
within active filters and request scope. It stops at the boundary without
wrapping. Manual scrolling invalidates the previous match anchor; the next
search begins at the new visible position rather than an abandoned match.

Use the same occurrence semantics in the reconstructed-response viewer so its
search keys do not behave differently on repeated text. Reuse a small literal
match helper if useful; do not merge the two view-state implementations.

### Acceptance

- A query after hundreds of continuation lines opens the actual matching line.
- Forward and backward repetition visit multiple matches within an entry and
  within one line, then correctly report the end of the search.
- Horizontal positioning works with Unicode, wide characters and ANSI-coloured
  source text. Text rendered as visible control escapes is searchable as shown.
- Hidden entries and other request scopes never satisfy the active search.
- Empty queries, cancellation and failed searches preserve the appropriate
  position. Reconstructing and closing a response restores the raw position.

## 2. Consistent qualification of timing results

Replace the false equal-cost statement in the profile and README with the
existing TUI's meaning: logging can disproportionately inflate chatty calls;
rankings are approximate; absolute times do not transfer to an unlogged run.

Keep UI rounding, start clamping, saturation and logging overhead distinct.
They describe different limitations and must not collapse into a generic warning
whose cause the reader cannot identify.

Carry the same meaning into profile JSON and comparison output using explicit
qualification codes plus readable explanations. The empirical timing example
may remain as an example, never as a correction factor. Do not estimate unlogged
durations or adjust observed rankings by log volume.

### Acceptance

Inspect the README, CLI help, profile output, TUI help and compact caveat for
contradictions. Exercise report paths with and without spans. Tests verify that
qualifications survive relevant paths and that the false guarantee is absent;
they need not pin every incidental sentence.

## 3. Resources view and resource/module filtering

### Resource rows

Use the reserved `3` key for Resources. Its primary rows group observed UI-hook
operations by exact Terraform resource address. Show operation count, summed
observed UI duration and longest observed UI operation. Use “operations”, not
“resources”, for repeated completions on one address.

Rank by observed UI total, then exact address. Preserve operation-level detail
so repeated reads/applies of one address remain inspectable. Do not collapse
their distinct source locations or lifecycle actions.

Supplement each resource with associated RPC count and total, separated into
Contained/Likely and weaker Overlapping evidence. Label these as inferred and
partial. Do not imply that all calls made by that resource have been recovered.
Do not assign a provider to a UI resource from a type-name prefix.

An evidence summary outside the ranked rows accounts for all admitted RPC time.
Classify calls without resource type as “resource type unavailable” first. This
is a statement about metadata, not provider-level scope: ReadResource,
GetProviderSchema and an unknown RPC method all enter this bucket when their
type is absent. Do not infer non-resource work from that absence or introduce
a method-based scope classifier as part of this feature. Among remaining calls,
absence of collected address context means no-context; otherwise use Contained,
Likely, Overlapping, Ambiguous or Unattributed. These reporting buckets are
disjoint and reconcile with the RPC total. Preserve the existing full confidence
distribution alongside them, clearly labelled with its own denominator. Do not
treat either distribution as UI resource time.

When there are no observed UI timings, show an explanation and routes to Calls,
Types and capture quality. The view does not substitute a misleading inferred
ranking. An address present only in context or RPC attribution can still be
selected as a filter, but has no invented UI duration row.

### Filters and interaction

Add exact resource-address selection and module-subtree selection to the
existing structured filtering model. Alternatives within one dimension are OR;
different dimensions are AND. Root module is an explicit choice. Match module
segments and instance keys, not naive string prefixes: `module.app` must not
match `module.application`, and dots inside quoted keys are not separators.

Module selection means the selected module instance and its descendants. A
parameterised module instance remains distinct from another instance. Use
observed module metadata where available; validate any address decomposition
with Terraform address examples already represented in the repository.

High-cardinality resource choices need a literal narrowing input in the facet
pane. It narrows available choices, not the results until a choice is selected.
Do not add a second implicit filter by typing into the chooser.

For UI operations, resource/module filters use observed metadata. For RPC calls,
they use named attribution and display its confidence. Ambiguous and unattributed
calls do not silently match a named resource.

When a resource or module selection is active, show a selection-evidence summary
whose baseline is admitted RPC observations after provider/resource-type/method
filters, but before resource/module filters. Severity and request scope affect
Raw Log only and do not change this baseline. Partition its count and duration
into three disjoint groups:

- **Selected named associations:** the named address/module evidence satisfies
  every active resource/module dimension. Preserve the Contained/Likely versus
  Overlapping distinction; neither is an observed identity.
- **Other named associations:** named evidence definitively fails at least one
  active resource/module dimension. These calls were excluded by selection,
  not lost to attribution failure. Preserve their confidence too.
- **Unresolved evidence:** neither membership nor non-membership can be
  established. This includes ambiguous/unattributed calls, no-context calls,
  unavailable positions and insufficient address/module metadata. Label it as
  potentially relevant work, never time known to belong to the selected resource.

Missing-type observations participate if the provider/type/method filters admit
them, including an unconstrained type filter or explicit unavailable-type choice.
Their missing metadata is not evidence that they are provider-level calls. All
three partitions must sum to the filtered baseline; lower-bound flags propagate.
Show the baseline filters and distinguish this selection summary from whole-log
capture quality. Do not label other named work as incomplete association.

Provider and RPC-method filters apply only to RPC evidence; they cannot infer a
provider or method for a UI operation. Resource/type/module filters apply where
their metadata exists. The Resources view labels those separate scopes; RPC-only
filters narrow its supplementary RPC figures without altering observed UI rows.

Raw-log provider and severity filtering retains its existing meaning. Resource,
type and RPC dimensions do not suddenly discard surrounding raw text. Entering
a particular RPC still uses its request scope. Opening a UI operation jumps to
its observed completion entry with surrounding context; it does not present
inferred related text as a complete resource-specific raw log.

### Acceptance

Cover UI-only, RPC-only and mixed logs; repeated operations; root and nested
modules; numeric and string instance keys; low and zero attribution coverage;
ambiguous calls; and resource selections with no matching RPCs. UI totals and
RPC totals reconcile independently. Review terminal layouts at existing golden
widths, including availability of evidence qualifications on narrow screens.

Check missing-type ReadResource, GetProviderSchema and unknown-method calls
against ordinary typed resource calls: only their metadata determines the
missing-type bucket, and no missing-type duration vanishes from the RPC total.
For selection evidence, use resource A with 10ms named time, resource B with 20ms
named time and 5ms unresolved time: selecting A shows 10ms selected, 20ms other
named work and 5ms unresolved against a 35ms baseline. A separate provider/method
filter that removes B changes that baseline to 15ms, while whole-log quality
stays unchanged. Without the unresolved call, selecting A must not imply an
attribution failure merely because B is excluded. Also cover missing-type and
no-context observations admitted to the baseline, and combined resource/module
selections with incomplete module metadata.

## 4. Drill down from aggregates and return predictably

Enter on a provider or resource-type row opens Calls with the clicked dimension
restricted to a singleton containing that row's value. Replace that dimension's
existing allow-list, including an unconstrained one; never union the clicked
value into it. Preserve all other active dimensions and save the parent's full
selection in history. A type with only UI operations opens its Resources view
instead with the same singleton type restriction; the footer names the action.

Enter on a resource similarly restricts the resource-address dimension to that
one address, preserves other dimensions and opens its observed operation list.
From there, the reader can open an operation's source entry or switch to the
associated RPC call list without broadening that selection.
Association confidence remains visible. A resource with no named RPCs states
that fact rather than implying that it made no provider calls.

Maintain navigation history only for explicit drill-down actions. Each frame
captures the prior view, selection, sort, filters and viewport state. Esc returns
one frame and restores it. Once there is no frame, existing Esc filter clearing
applies. Modal search/help/response dismissal takes precedence over history.

Explicit numbered view changes end the current drill-down chain, matching the
existing distinction between opening a call and independently changing views.
`\` expands a request-scoped raw log to the whole log without consuming its
return destination. Facet edits inside a drill-down affect that child view;
returning restores the parent snapshot.

Store identities rather than assuming a row index survives a rebuild. If a
restored selection is unavailable, select the nearest valid row and preserve
the rest of the snapshot. Never dereference stale filtered-slice positions.

### Acceptance

Exercise provider → calls → raw log → response → back, type → resources →
operation → raw log, and resource → associated calls. Cover child filter edits,
sorting, empty results, terminal resizing, manual view changes and every Esc
precedence. Returning must restore the investigation the reader actually left.
Start with providers A and B selected and a resource-type filter active: entering
A admits only A with that same type filter, and Esc restores A+B. Repeat for
multiple selected resource types and resources, and an initially unconstrained
clicked dimension. Other dimensions must remain unchanged in each child.

## 5. Actionable text profiles

Retain current provider/type rankings and the separate UI/RPC measurements.
Extend slowest RPC rows with source location and named attribution where
available, always qualified by confidence. Ambiguous calls show a candidate
count, never a guessed address. UI operation rows include source locations too.

For text output, long addresses may occupy a following indented line rather
than becoming indistinguishable truncated labels. Source references identify
one-based physical lines in the original log. Keep scanner entry ordinals as
internal identifiers; compute exact physical locations from source offsets,
not the saturating `Entry.Lines` counter.

Add observed busy time and longest low-concurrency/idle intervals, using the
model calculations already used by the timeline. Apply the shared position
eligibility and partial/unavailable analysis rules before calculating the window
or intervals. Use the same tier selection, window definition and thresholds in
the TUI and report. A UI-only log gets UI-tier analysis when positions are usable,
otherwise an explicit unavailable section. Never mix the two clocks.

Say “no observed RPC work” or “no observed UI work”, as appropriate, for gaps.
Such a gap does not prove Terraform was idle, nor does a long active span prove
it caused all other work to wait. Distinguish longest observed active work from
a dependency-critical path, which these logs have not established.

`--limit N` controls text ranking and interval lists: default 20,
`0` means all, negative values are errors. Summary totals always cover all
eligible observations; headings show truncation. This flag applies to text
profile and comparison output only.

### Acceptance

Check untruncated source/address identity, deterministic ties, UI-only analysis,
clamped starts, zero-duration observations, lower bounds, full versus limited
lists, and propagation of writer errors. Terminal controls in addresses and
paths must not create additional diagnostic rows or execute terminal sequences.

## 6. Capture-quality summary

### Shared facts

Introduce a focused model value retaining quality facts that current collectors
already expose, plus narrowly scoped rejection counters where an input can
currently be skipped without explanation. Collect facts during the existing
scan; do not load a second diagnose report just to display its text.

Include available measurement tiers and counts; RPC response observations versus
successfully built spans; missing or invalid duration values; malformed UI data;
backwards timestamps; saturated durations/line counts; clamped span starts;
request-ID/component interning overflow; capped capability tracking; incomplete
resource contexts; unmatched terminators; and attribution counts and time totals.

Tier availability follows the shared duration-admission contract independently
of timeline availability. Retain distinct admitted-duration, positioned and
rejected-record counts. A separate evidence state records relevant markers or
rejected observations when no duration was admitted. A missing tier therefore
cannot falsely claim the log contains no provider traffic or no structured stream.

Keep missing timestamps or schema fields separate from JSON syntax errors.
Counters at different processing stages may overlap and must not be summed into
a fabricated “bad lines” total. Rejection categories within one stage should be
mutually exclusive where a total is presented. Document each denominator.

Nameable RPC share retains the existing definition, Contained plus Likely over
all admitted RPC span time, and also shows the actual numerator and denominator.
Missing resource type and unavailable positions never remove an admitted
duration from this denominator. Do not add an “eligible-resource-call” percentage
whose denominator assumes that unknown metadata establishes provider-level scope.
Zero denominator means unavailable, not 0% or 100% coverage. The separate
selection-evidence summary uses its explicitly filtered baseline.

Incomplete context means an observed start lacked a matching terminator. It is
not proof the resource failed: capture truncation, cancellation and unsupported
events may look similar. Do not manufacture completion durations for these
contexts. End-of-capture context bounds remain attribution aids only.

### Presentation

The TUI header exposes a compact quality indicator and `i` opens a scrollable
capture-quality panel. Keep limitations visible even when filters hide affected
spans. The panel distinguishes unavailable timing, degraded observations and
attribution uncertainty; it does not assign an arbitrary health score.

Text profiles include a short summary before rankings. JSON carries the facts
and qualification codes. Diagnose keeps its content restrictions and can consume
the shared definitions without requiring the whole-file model-loading path.
Introduce common calculations incrementally, preserving its streaming scan.

Response reconstruction is lazy. Until requested, its quality state is “not
checked”, not “valid”. Viewing a response can add reconstruction diagnostics to
the TUI panel; ordinary profile generation does not reconstruct every body.

### Acceptance

Use controlled malformed, truncated and mixed fixtures to verify each fact and
its denominator. Check that normal TUI loading and profile output disclose
rejected timing evidence, that no-data guidance names the actual limitation,
and that diagnose remains free of addresses and captured values.
Exercise the admission matrix: explicit zero versus missing/null/negative/invalid
duration; valid duration with invalid timestamp; genuine offset zero; mixed and
entirely unpositioned tiers; backwards and saturated offsets; duration saturation;
and a legitimate clamped start. The TUI, text and JSON must agree on which
observations contribute to duration counts/means and which can establish a
timeline position. Rejection counters must not replace per-observation validity.

## 7. JSON profiles and CLI run comparison

### CLI contract — profile and comparison modes

Both profile and comparison forms are implemented:

```text
tfli --profile --format json -o profile.json run.log
tfli --profile --limit 0 run.log
tfli --compare [--format text|json] [--limit N] before.log after.log
tfli --compare --format json -o comparison.json before.log after.log
```

`--format` defaults to `text` and applies only to profile and comparison modes.
`--compare` is mutually exclusive with profile, diagnose and scrub, and requires
exactly two raw-log paths. Other modes retain their current arity. Explicit
`--limit` with JSON is rejected because JSON always exports the complete data.
Unknown formats, invalid limits and irrelevant options fail before output opens.

For comparison, protect both input files from output aliasing, including symbolic
and hard links. Read and validate both inputs before rendering. Use the existing
report writing policy for unrelated output files; do not introduce a hidden
change to overwrite semantics in this feature.

### Profile data contract

Add a concrete profile data structure assembled from model calculations. Text
and JSON renderers consume that structure; neither parses the other's output.
Keep presentation width, formatted duration strings and row truncation outside
the calculation model.

The current profile JSON v1 schema includes:

| Field group | Required meaning |
| --- | --- |
| Identity | `schema_version: 1`, `kind: "profile"`, tool version, input basename and byte size |
| Tiers | Duration availability, admitted/positioned/rejected counts, duration unit `ms`, clock origins when known |
| Quality | Measured counters, attribution distribution, reconstruction status and qualifications |
| RPC observations | Local source entry identity, physical line/byte reference, method, provider, type, admitted duration, nullable offsets, position-validity reason, clamping and attribution |
| UI observations | Local source identity, exact resource address, action, admitted duration, nullable offsets, position-validity reason, clamping and per-observation saturation |
| Aggregates | Complete provider/type/resource rows with separate UI and RPC measurements |
| Timeline summary | Chosen tier, partial/unavailable status, positioned/excluded counts and duration totals with reasons, nullable window/metrics, and complete qualifying interval list |

Use source-entry identifiers local to each capture. Do not export internal
interned request-ID numbers as portable identities or invent a request-ID string
the model has not retained. Exact source byte offsets remain available even if
displayed physical line counters exceed existing compact index fields.

All schema fields are mandatory. Unavailable values use explicit nulls; tier
objects remain present. Unavailable per-observation offsets and timeline metrics
are null, never zero placeholders. Observation arrays contain admitted durations; rejected timing
records contribute to quality counters only. Present numerical observations,
including zero, are numbers. Array ordering is deterministic. Do not include
current generation timestamps or
absolute input paths by default, so repeated exports of the same input with the
same tool version are reproducible.

Schema versioning identifies the format; it does not authorise compatibility
adapters or promises to preserve old schemas indefinitely. No JSON import or
backward-compatibility layer is included. The published
[profile JSON v1](../../profile-json-v1.md) and
[comparison JSON v1](../../comparison-json-v1.md) documents define the exact
fields, ordering and validation, with matching encoder tests.

### Comparison behaviour

Load both raw logs with the same parser/model version. Label them before and
after in all text and JSON. Compare separately by provider, resource type and
provider/resource-type/RPC-method tuple. These stable domain keys do not depend
on request IDs or source line numbers changing between runs.

For each group show before/after observation count, summed duration, mean
duration and maximum duration, plus signed absolute and percentage deltas where
defined. Counts and means use admitted duration observations, including those
with unavailable positions; rejected timing records remain separate quality
facts. Mean is unavailable for zero observations. Percentage change from zero
is unavailable, not infinity. Sort duration changes deterministically, with
increases first and explicit added/removed rows.

Added/removed means present in only one capture's observed data. It does not
assert that a Terraform resource was created or destroyed. If both tiers are
available, absence from a group may use zero observations for that side; if a
whole tier is unavailable, comparisons for that tier are unavailable instead.

Offer exact-address UI comparison as a separate section, matching address and
action and aggregating repeated observations. Do not match by inferred RPC
address, alias similarity or fuzzy resource names. Independent scrub runs may
produce different aliases; the tool cannot infer cross-file identity. Explain
that limitation without attempting to deanonymise either input.

The report separates call-volume changes from per-call mean changes. It does
not claim a causal explanation for either. UI rounding and saturation survive
into deltas. If either side's timing is a lower bound, retain both observations
and their flags but make the timing delta and percentage unavailable; subtracting
two lower bounds does not establish a bound on their difference. Keep such rows
outside the exact timing-change ranking. Their observation-count deltas remain
available when the counts themselves are complete.

Both captures' quality summaries accompany the comparison. Missing tiers,
incomplete contexts and differing observable provider identities are explicit.
Logging configuration equivalence is “unknown” unless directly established by
captured evidence; similarity of line counts cannot certify comparability.

Comparisons produce observations, not pass/fail performance judgements. Exit
status remains success for a rendered report, including unavailable sections;
input/argument/render failures remain errors. CI regression thresholds, workload
normalisation, provider-version reconciliation and an interactive comparison
workspace are separate future proposals.

### Acceptance

Verify JSON round trips through the standard decoder, all rows survive export,
text and JSON agree on underlying totals, output is deterministic, and failures
never leave a successful-looking JSON stream. Test identical logs, added and
removed groups, changed call counts, equal totals with different counts, zero
baselines, empty logs, disjoint tiers, repeated UI actions, lower bounds and
independently renamed inputs. Protect both comparison inputs from `-o` aliasing.

## 8. Partial response recovery for inspection

### Reconstruction outcome

Separate structural reconstruction results from consumer policy. A result can
contain verified complete messages and content-free diagnostics with affected
source ranges. The viewer consumes usable messages; the scrubber requires a
diagnostic-free complete reconstruction before transformation and publication.

Use one parser and one definition of message ownership. Do not fork a lenient
parser for viewing or add a permissive switch that the scrubber could enable
accidentally. Preserve exact component isolation and source-fragment mapping.

After malformed JSON, retain completed messages already verified. Continue
independently tracked components where ownership remains provable. Once a
component's stream loses a trustworthy boundary, mark that component unavailable
for the remainder of the capture; do not guess that a later `{` begins a new
message. A valid-looking prefix can be payload inside an unfinished string.

If ownership failure cannot be confined to one component, stop recovery at that
point and retain only earlier verified messages. Incomplete messages at EOF get
diagnostics; they do not invalidate independently verified completed messages.
This intentionally recovers fewer responses than speculative resynchronisation.
The existing limitation still applies: unrelated same-component text inserted
inside an unfinished JSON string can be indistinguishable from genuine payload.
Recovery does not claim to detect that unobservable mixing.

### Viewer behaviour

Resolve the first raw physical line actually drawn to its original fragment
range, using its entry ordinal and line offset. Horizontal scrolling and the
character position of a search match do not select a different byte range. Do
not search later lines or return another response from the same logical entry. Opening a fragment of a recovered response shows its full verified body
and a notice that other responses could not be reconstructed.

Distinguish “no response at this position”, “this response is incomplete or
invalid” and “this stream is unavailable after an earlier failure”. Show safe
source locations and reasons, with Raw Log always available. Preserve scrolling,
search and exact return position. The quality panel reports reconstruction as
complete, partial or failed only after it has actually run.

Scrubbing continues to reject any reconstruction diagnostic and create no output.
Neither the existence of some valid messages nor successful viewer inspection
changes that condition. Existing scrub privacy and metadata-preservation tests
must remain intact.

### Acceptance

Exercise good A / malformed B / good A, malformed A / later apparent A start,
completed A / incomplete A at EOF, multiple responses associated with one entry,
interleaved providers, inline UI envelopes, invalid UTF-8 and ambiguous ownership.
Verify retained source mapping, deterministic diagnostic ordering, no unsafe
same-stream resynchronisation, and rejection without output by the scrubber for
every partially recoverable input.

## Architecture and delivery boundaries

Keep parsing in `internal/logfmt`, timing extraction in `internal/span`, address
evidence in `internal/attrib`, and reusable investigation calculations in
`internal/model`. `internal/profile` owns report assembly/rendering and the CLI
owns option validation and input/output handling. A small comparison package is
justified only if its domain calculations outgrow a focused model file.

Do not rewrite these packages wholesale. Introduce the minimum shared values
needed by the next consumer, migrate duplicate calculations with parity tests,
and preserve the streaming diagnose path. Index source lines and response ranges
only when needed; avoid rescanning all source bytes for every exported row or
TUI keypress. Benchmark new projections on sanitised large fixtures before
introducing parallel loading or a new storage strategy.

All of the following delivery boundaries are complete:

| Boundary | Includes | Depends on |
| --- | --- | --- |
| A. Timing qualifications | Item 2, report/README consistency | None |
| B. Search positions | Item 1, response occurrence parity | None |
| C. Capture evidence | Shared duration/position admission, per-observation validity, item 6 facts, quality presentation and location support | A for wording |
| D. Resources and filters | Item 3, observed operation projection, typed selection and reconciling selection-evidence partitions | C for coverage presentation |
| E. Investigation navigation | Item 4, history and resource operation drill-down | D for resource paths; aggregate-to-call path can ship earlier |
| F. Profile data and text | Item 5, common report data, complete interval analysis | C; D for shared resource aggregates |
| G. JSON export | Item 7 schema and profile encoding | F |
| H. Run comparison | Item 7 comparison model, CLI and encoders | G; shares F's data |
| I. Response recovery | Item 8 parser outcomes and strict scrub policy | C only for quality-panel integration |

The dependency column records the dependencies between delivered areas. Every boundary left
a runnable application with its own task reviews and acceptance evidence.

## Validation and review requirements

Every later feature or fix follows TDD using real parsing, model and rendering
behaviour. Fixtures are synthetic or sanitised. Test observable contracts and
important failure boundaries, not copied implementation details or mocked
behaviour. Capture and assert expected error output.

Each implementation plan includes focused regression cases from its section,
the full Go suite and build, the repository's required CI checks, independent
review and the separate test-cleanup pass required by project conventions.
TUI changes include intentional golden updates, inspection through the existing
golden-reading script, raw styling diffs and real terminal interaction.

Cross-feature review must verify that filtered resource evidence never changes
whole-log quality, that text and JSON calculations agree, that history restores
selection scope, that repeated operation identities survive drill-down/export,
and that partial response recovery cannot weaken scrubbing.

The close-out check covers all eight sections against their original requests,
including the unchanged attribution gate and strict reconstruction policy.

## Scope reserved for later proposals

No live tailing, remote log fetching, database, persistent bookmarks, interactive
run comparison, JSON import, CSV export, fuzzy identity matching, automatic
performance thresholds, inferred critical path, new attribution mechanism or
inferred resource-time ranking is included. These are possible extensions, not
dependencies concealed inside the eight agreed areas for exploration.

## Approved decisions

1. Resources ranks observed UI operations. Inferred RPC associations remain
   separate and confidence-qualified; they do not become resource-time rankings.
2. Provider and RPC-method filters apply only to RPC evidence. Resource, type
   and module filters use the metadata available in each measurement tier.
3. Explicit drill-down saves the parent selection and position for Esc.
   Numbered view changes end the history chain.
4. JSON exports complete structured results; text alone has explicit list
   limits. Both JSON report kinds use v1 throughout the alpha, without a compatibility writer or version flag.
5. Comparison accepts two raw logs and emits text or JSON. It does not produce
   a performance pass/fail verdict or assume equal logging configurations.
6. Response recovery quarantines damaged streams without guessing a restart.
   Scrubbing rejects every reconstruction diagnostic, including after viewing.

## Delivery acceptance evidence

The maintained tests below exercise real parsing, model, rendering and CLI
boundaries. They carry the cross-feature acceptance criteria after the task
plans are retired.

| Contract | Maintained evidence |
| --- | --- |
| Search lands on physical occurrences and preserves return state | `internal/tui/rawlog_test.go`, `response_test.go` and `response_recovery_test.go` cover repeated matches, safe display columns, filtering, scope and modal return. |
| Timing qualifications agree across outputs | `internal/profile/profile_test.go`, `internal/tui/help_test.go` and CLI tests retain logging, rounding, clamping and saturation qualifications. |
| Resource filtering preserves whole-log quality | `TestResourcesInvestigationWorkflowPreservesEvidenceQualityAndRawState` checks the 35ms baseline partition and quality through resource selection, evidence/quality modals and scoped raw search. |
| History composes across types, resources, operations, calls and responses | `TestResourceOperationAssociatedCallResponseHistoryComposition` follows the entire drill-down and Esc chain; `history_test.go` covers parent filters, sorts, focus and resized restoration. |
| Repeated operations retain source identities through selection and export | `TestRepeatedResourceOperationsKeepOriginalIdentitiesThroughSelection`, `TestBuildRetainsAllObservedOperations` and JSON source-contract tests retain distinct original operations. |
| Text and JSON share complete qualified calculations | `internal/profile/data_test.go`, `text_test.go`, `json_contract_test.go`, `comparison_data_test.go` and both comparison renderer tests exercise the shared reports, limits, nulls and lower bounds. |
| CLI profile/comparison agree with parsed evidence and protect inputs | `cmd/tfli/profile_json_test.go`, `comparison_test.go` and `report_identity_test.go` cover complete output, determinism, loaded-file identity, both input aliases and failure paths. |
| Viewing recovered bodies cannot weaken scrubbing | `TestResponseRecoveryJourneyPreservesRawInvestigationAndStrictScrubbing` checks the same capture after inspection; `TestRunScrubRejectsPartialProviderJSONRecoveryBeforePublication` verifies no publication. |

The final I2 whole-branch and peer reviews through `c51588e` found no outstanding
issues. Earlier boundary reviews and local build/race/lint/platform/PTY evidence
are recorded in the retired plans at that commit. Cross-builds are local checks,
not evidence of remote CI execution.

Close-out validation on 11 September 2026 passed `go test -race -count=1 ./...`
across all eleven packages and `go build ./...`. No application code or tests
changed during documentation consolidation. The tests above therefore exercise
the application delivered at `c51588e`.

Actual CLI checks used `resources-accounting.log` and `resources-modules.log`:
text and JSON retained the 35ms RPC baseline and respective 1000ms/3000ms UI
totals; comparison capture objects matched independently generated profiles.
Repeated UI operations retained distinct source entries and physical lines.
The missing after-side RPC tier stayed unavailable, while the UI type total
change was 2000ms. Both report kinds retained lazy reconstruction snapshots.
The current alpha contract uses version 1. Scrubbing `response-recovery.log` failed with only a
structural diagnostic and left no output file. Complete CLI streams are retained
locally under `/tmp/tfli-closeout-validation`.
