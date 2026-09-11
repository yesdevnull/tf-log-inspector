# Investigation navigation, scoped reports and responsive inspection

## Status and purpose

Draft for Dan's review. Requested on 11 September 2026 following a review of
the inspector at `33bcbc2`. This specifies proposed behaviour; no feature in
this document is implemented by the documentation change.

The next iteration makes evidence easier to reach, search and compare without
changing what the underlying observations mean. It covers six user-facing
features, report-file protections, a diagnostic wording correction and a
bounded performance investigation. Each delivery boundary gets its own
implementation plan later.

The [completed investigation design](2026-09-09-investigation-workflows-design.md)
remains the baseline. The [profile JSON v1](../../profile-json-v1.md) and
[comparison JSON v1](../../comparison-json-v1.md) contracts remain authoritative.
This draft proposes no schema change or compatibility adapter.

## Approach and scope

Use the existing model calculations and source identities with small additions
to navigation and report selection. Keep the current Go toolchain, dependencies,
terminal framework and package boundaries.

Three approaches were considered:

1. **Extend the existing investigation flow — recommended.** Add precise
   navigation, visible matches, explicit tier selection and scoped text reports.
   Reuse the evidence and history machinery already present.
2. **Introduce a general query and command system.** Flexible, but unnecessary
   for exact identifiers, source lines and a handful of existing views. It would
   add syntax and configuration without solving a demonstrated need.
3. **Build a persistent comparison workspace.** Useful for repeated sessions,
   but introduces paired logs, persistence and considerably more state. Defer it.

No major existing feature is removed. Remove the unconditional diagnose sharing
guarantee and consolidate duplicated rules only where these features touch them.
Live tailing, persistent sessions/bookmarks, JSON import, interactive comparison,
automatic performance verdicts and inferred-RPC resource ranking remain deferred.

## Shared invariants

- Original bytes, scanner entry ordinals and original per-tier observation
  indices remain stable through filtering, navigation and rendering.
- Source lines are one-based physical lines in the original file, including
  continuations. Entry-relative lines must be labelled as such if displayed.
- UI and RPC retain separate clocks, durations and qualifications. No combined
  total, automatic clock alignment or claim of causal blocking is introduced.
- Whole-log capture quality remains whole-log. Selected totals and displayed
  timeline windows must have their own explicit scope labels.
- Preserve unavailable values, measured zero, lower bounds, attribution
  confidence and observations excluded from temporal rendering.
- User-derived text is escaped before terminal display. Search highlighting
  must not reintroduce source control sequences.
- Strict scrubbing still refuses every reconstruction diagnostic. Viewing,
  checking or navigating a capture never changes scrub acceptance.
- New prompts are modal: typed command letters are text. Esc cancels without
  changing the underlying investigation; Ctrl+C retains the quit behaviour.
- Navigation frames own their mutable state. Returning restores filters,
  selection identity, searches, raw position and timeline state exactly, subject
  only to viewport clamping after terminal resize.

## A. Physical source navigation and diagnostic wording

### Behaviour

With Raw Log focused, `g` opens a “Go to source line” prompt. It accepts a
positive decimal integer without signs or embedded whitespace. Empty, zero,
overflowing and out-of-range input leave the prompt open with a short error.
No index is narrowed before range validation.

On Enter, place the requested physical line at the top of Raw Log. Blank lines,
continuations, LF, CRLF and a final line without a newline are valid targets.
The logical-entry lookup must use actual source offsets, including entries
whose stored continuation count has saturated.

A successful jump pushes the existing investigation onto history, clears the
call scope and provider/level filters, and sets horizontal position to zero.
Because provider filtering is shared, this temporarily widens that timing
dimension too; resource, module, type and method selections remain recorded.
It clears the active raw match while retaining the submitted query for
subsequent searches. The footer names
the resulting whole-log scope. Esc returns to the complete prior state; the
prompt's cancellation does not create a history frame.

The status bar reports the absolute physical line of the first line actually
drawn, rather than simply the stored cursor's entry-relative offset. Keep entry
ordinal as secondary information where width permits. An empty pane reports no
visible source line rather than a fabricated position.

Replace “safe to share” in diagnose CLI help and related source comments with
“masked; review before sharing”. Audit user-facing report text and documentation
for the same implication, including the profile report's “Unlike --diagnose”
comparison. Retain the README's heuristic masking guidance. This changes sharing
qualifications only, not detection, measurements or report data fields.

### Acceptance

- Jump to the first, last, blank and continuation lines of sanitised fixtures;
  the displayed line and reported location agree with the original file.
- Invalid input and cancellation leave history and investigation state intact.
- Jump out of a filtered call scope, then return to its exact state with Esc.
- Narrow layouts still expose the physical line or a clearly marked truncation.
- Actual `--help` and profile text output contain appropriate review
  qualifications and no direct or implied assurance that diagnose is safe to
  share. Related maintained prose uses the same rule.

## B. Visible search occurrences

### Behaviour

Highlight the current successful literal occurrence in Raw Log and reconstructed
response text. Highlight only the active occurrence; do not scan the entire
capture to count or decorate every match. Keep current case sensitivity,
occurrence ordering and non-wrapping next/previous behaviour.

Use the existing match's displayed-text position. Apply styling after source
ANSI removal/control escaping and before viewport clipping. Preserve display
columns for tabs, wide characters, combining characters and escaped controls.
Highlighting must not change searchable text, layout width or copied source
identity. If a match starts or ends inside a grapheme cluster, style the whole
intersected cluster without changing the literal match's byte range or order.
Retain severity styling outside the active range and use an attribute
such as reverse video so colour alone is not the distinguishing signal.

Scrolling and filter changes continue to invalidate matches under the existing
rules. A failed next/previous search preserves the cursor, reports the miss and
does not decorate an old occurrence as a newly successful result. Returning
from a response restores any valid raw match held by that investigation.

### Acceptance

- Repeated identical strings on one line visibly identify different matches.
- Matches partly outside the horizontal viewport are clipped correctly.
- Sanitised ANSI, tabs, Unicode and visible control escapes retain their exact
  search positions and terminal widths.
- Search miss, manual movement, filtering, response return and resize do not
  leave a misleading highlight.
- Review representative styled terminal output, not just stripped strings.

## C. Explicit timing-tier selection

### Behaviour

The timeline continues to open with the current preference: RPC when admitted
RPC observations exist, otherwise UI. With the timeline list focused, `t`
switches between RPC and UI when both exist. A single available tier remains
selected with an explanatory status; no synthetic empty tier is introduced.

Availability comes from the whole capture, before filters. An explicitly
selected tier with no matching or no positioned observations stays selected
and explains that condition. It never silently falls back to the other clock.

Preserve a separate selected observation and, after F is delivered, display
window for each tier. Tier changes invalidate every tier-dependent cache,
including lanes, labels, analysis and detail sizing. History saves the explicit
tier and both tier states. Filters reconcile selection by original identity,
then use a deterministic first available observation if it is no longer shown.

Titles and axis labels identify the active tier; UI keeps its whole-second
qualification. Enter uses the active tier's original source identity. This
feature changes the TUI only: existing profile and JSON timeline preference
remains unchanged.

Use `model.PreferredTiming` as the default preference rule rather than retaining
a second equivalent rule in TUI code. Keep explicit user choice in TUI state.

### Acceptance

- Mixed captures can display and drill into both timelines independently.
- RPC-only, UI-only and no-timing captures explain available behaviour.
- Filtering every observation from a tier does not switch clocks.
- Switch tiers, drill down, return and resize without changing source identity
  or leaking one tier's cursor/window into the other.

## D. Actionable capture quality and explicit reconstruction checking

### Behaviour

Keep the quality panel scrollable, with selectable anomaly records and an
explicit “Check responses” action. Up/down moves between actions/records and
keeps the selection visible; page keys scroll the guide while reconciling the
selection to a visible actionable row. Explanatory prose is not selectable.

Enter on a located anomaly opens its first source sample in Raw Log through A's
navigation primitive. Returning restores the quality panel's selection and
scroll position as well as its parent investigation. Records without a location
state that fact and do not perform a jump. Samples remain labelled as first
examples rather than exhaustive lists of affected lines.

Enter on “Check responses” explicitly starts the same whole-capture inspection
used by the response viewer. Show an indeterminate running status, then publish
complete, partial or failed with the existing counts. No fabricated percentage
is shown. Expose content-free reconstruction diagnostics with a single primary
source coordinate per row: use nonzero `SyntaxLine`, otherwise nonzero `Line`,
otherwise nonzero `StartLine`. Order by that coordinate, with unlocated records
last and original diagnostic index as the tie-break. Enter jumps to the primary
coordinate. Show a distinct nonzero start line as secondary context, labelled
“response starts at”; it is not another selectable target. Do not enumerate
every retained fragment or unavailable range. Bodies are only accessible through
the existing verified-response rules.

Every first inspection trigger, including ordinary `r` in Raw Log, starts or
joins the check outside the Bubble Tea update handler and returns a pending
view immediately. There is at most one
inspection per loaded capture; repeated requests join the same work. Publication
is atomic after all indexes are ready. Closing the panel leaves the check running
for the immutable loaded log. Esc does not claim to cancel it. Quit remains
responsive. Opening a response during inspection shows a pending state and
resolves the requested source position when the result arrives. Apply completion
only to the currently open request/generation; late completion must neither
reopen a modal the user closed nor replace a newer response selection.

Running is a transient TUI state, not a new reconstruction state in JSON v1.
Ordinary CLI reporting remains lazy. No new CLI validation mode is introduced.
No export or scrub output is produced by checking responses.

### Acceptance

- Diagnostic jumps and return restore both the panel and parent investigation.
- No-location records, empty issue lists and reconstruction with zero messages
  are distinguishable from a failed check.
- Repeated activation, panel closure, response opening and quit during a real
  inspection do not duplicate work, block the event loop or publish partial
  indexes. Race checks cover result publication and quality reads.
- Malformed and ambiguous fixtures expose only structural diagnostics; recovered
  responses remain available, and strict scrub still rejects those captures.

## E. Scoped text profiles and comparisons

### CLI contract

Introduce repeatable `--provider`, `--rpc`, `--type`, `--resource` and `--module`
options for text `--profile` and `--compare` only. These are proposed flags, not
existing CLI options. Values are exact, case-sensitive strings with no regex,
glob, comma splitting or implicit trimming. Repetition means OR within a
dimension; different dimensions combine with AND. Duplicate values are harmless.

An omitted dimension is unconstrained. Explicit empty values are rejected except
`--module ''`, which deliberately selects the root subtree including all known
descendants. Module paths and resource addresses use the existing Terraform
address parser; malformed syntax is rejected. Module instance keys stay exact.
Syntactically valid resource addresses and module paths, like provider, method
and type names, need not exist in the capture: an unmatched selection is a
valid empty result. Expose no special `(none)` selector in this
first CLI delivery; literal names must not accidentally become missing-value
sentinels.

Provider and RPC-method filters affect only RPC observations. Type, resource
and module filters follow the existing shared resource projection for both
tiers. Unresolved identity is retained in evidence partitions, not assigned to
a selected address. Preserve overlapping versus contained/likely associations.

Reject these options with JSON, diagnose, scrub or bare TUI invocation before
creating output. Filtered JSON would need an explicit schema design describing
scope and denominators; that is deferred. No v2 payload is silently redefined.

### Reporting semantics

Print a deterministic selection description before ranked data. Mark selected
counts and durations separately from whole-log capture facts. With named
selection active, show the base-filtered RPC denominator and its selected,
other and unresolved partitions. Row limits still apply independently after
selection and ranking, and do not affect totals.

Apply exactly the same selection to before and after captures. Retain each
capture's full-tier availability facts separately from selected observations.
“No observations match this selection” is different from “this capture has no
admitted tier”. A selected empty side must not acquire a measured-zero mean or
percentage; preserve missing/new row treatment and unavailable baselines.
Provider identity qualifications are based on the selected comparison evidence,
with the unchanged whole-capture quality alongside it. Never infer equivalent
capture configurations from matching selections.

Build selected observations using original indices and source references. Do
not fabricate a smaller `model.Log` and recompute its quality as if it were the
whole file. No-filter text output retains its existing semantics.

### Acceptance

- For the same selection, CLI totals and evidence partitions equal the TUI
  projection over the same sanitised capture.
- OR/AND, exact module keys, root subtree, unknown identities, unmatched values
  and repeated operations preserve the current selection rules.
- Comparison distinguishes absent tiers from empty selections on either side.
- Lower bounds, attribution confidence, source identity and list limits survive
  selection; unfiltered quality is identical before and after selection.
- Invalid mode/format combinations leave stdout and output paths untouched.

## F. Timeline display windows

### Behaviour

With the timeline focused, `+`/`-` zoom in/out and `0` restores the full extent.
`[`/`]` pan by half the visible interval. Existing arrows continue to select
observations. Zoom halves/doubles the interval, centred on the selected
observation's midpoint and clamped to the full extent. Minimum width is one
millisecond; a zero-width full extent retains its existing unavailable-ratio
behaviour. Integer arithmetic must not overflow.

For integer widths, zoom-in uses ceiling-half, zoom-out doubles up to the full
extent, and pan uses floor-half with a minimum step of one millisecond. Compute
the observation midpoint as start plus floor-half of its duration in positioned
offsets. Place floor-half of the new window width before that midpoint, clamping
the window start to `[0, full_extent - new_width]`. With no selected observation,
use the current window midpoint by the same rule. Panning clamps the start to
the same bounds without changing width. Use widened arithmetic and saturating
boundary operations rather than overflowing intermediate sums/subtractions.

Use an inclusive-start, exclusive-end display window in the selected tier's
existing clock. Axis labels show original offsets, never a silently rebased
clock. Positive-duration bars intersecting the window are clipped; a zero-length
observation is shown when its position is inside the window, including the final
endpoint when the window reaches the full extent. Lane membership/order comes
from the full filtered tier so panning does not repack lanes.

Moving to an off-screen observation pans just enough to reveal its start while
preserving window width. Its detail retains full duration and source identity.
Changing filters retains and clamps the window to the new full extent; an empty
selection displays an explicit empty state. Tier changes preserve separate
windows as specified in C.

This is display-window selection only. Ranked tables, profile exports and
whole-selection timing analysis do not change. Label busy/gap annotations as
whole-selection measurements; do not imply they describe only the zoomed
window. No time-range CLI filter or window-specific performance statistic is
introduced in this boundary.

### Acceptance

- Zoom and pan on known fixtures produce the expected visible offsets and
  clipped bars without altering totals, lane assignment or source identities.
- Cover start/end boundaries, zero-length observations, maximum offsets,
  long spans crossing both edges, empty filters and repeated zoom at limits.
- Tier switching, history return and resize preserve valid windows.
- Review narrow and wide terminal output so window labels and whole-selection
  annotations cannot be mistaken for one another.

## G. Protective report-file publication

### Behaviour

For diagnose, profile and comparison `-o`, render to a temporary regular file in
the destination directory with owner-only read/write permissions (`0600`).
Publish only after rendering and closing succeed. Replace an existing regular
destination by atomic rename, or create the absent destination by the same
publication step. The resulting report has mode `0600`, including replacement
of a previously broader-mode report. Directory and ownership requirements can
therefore differ from the current in-place write and must be documented.

Reject an output path that is a symbolic link, directory or special file.
Stdout remains available for pipes and explicit shell redirection. For a
non-input hard-linked destination, replace only the named directory entry;
other links retain the old bytes. No automatic chmod or modification of other
links is permitted.

Retain descriptor-based protection of every loaded input and its current path.
Check destination identity before rendering and recheck before publication;
reject any alias of either input in comparison mode. Resolve ordinary output
errors without exposing log content. Clean up the temporary file on render,
close, validation or rename failure and report cleanup failures explicitly.

An unsuccessful publication leaves the existing destination intact. Atomic
visibility does not promise crash durability or compare-and-swap protection
against another process replacing the destination concurrently. The plan must
require a destination directory trusted against concurrent replacement of the
temporary pathname or its ancestors; close-then-rename cannot guarantee temporary
file identity in a hostile writable directory. Support local filesystems with
atomic same-directory rename; unusual/network filesystem semantics are not a
durability guarantee. Preserve the input protection
under the existing pathname-replacement tests. Do not weaken scrub's distinct
exclusive-create policy or route scrub through report replacement.

### Acceptance

- New and replaced reports have `0600`; stdout semantics are unchanged.
- Real file tests cover input paths, symbolic links, hard links, replacement
  inputs, both comparison inputs, absent destinations and special files.
- Render/write/close/publication failures preserve old report bytes and clean
  up temporary files; expected errors are captured and asserted.
- Existing scrub output rejection and exclusive-create tests continue to pass.

## H. Performance investigation and focused maintenance

Before prescribing storage changes, measure sanitised captures with many small
entries and with very large multiline entries. Include 10, 100 and 500 MB input
sizes, stating actual bytes and line/entry counts rather than relying on labels.
Generate data through a documented benchmark helper; do not commit large blobs.

Measure load time and peak resident memory, steady-state raw scrolling and search
allocations/latency, first reconstruction cost and repeated response selection.
Separate reconstruction from pretty-printing and terminal rendering. Record the
machine, Go version, commands and repeated-run results. Existing 17–37 MB capture
assumptions do not establish performance at larger sizes.

Inspect the repeated whole-entry string conversion/splitting in `entryLines`
and repeated provider-map construction during rendering. Prefer reuse of the
existing source-line index or bounded caches if evidence shows meaningful cost.
Do not introduce mmap, an LRU framework, streaming storage or a new dependency
without a separate design decision.

The initial deliverable is benchmark evidence and a recommendation. Any chosen
optimisation gets a bounded plan with measured before/after criteria; no arbitrary
latency claim is required to declare the investigation complete. D's background
inspection is a specified responsiveness feature, not conditional on this probe.

Keep maintenance attached to relevant boundaries: C consolidates tier preference;
A–F update key handling and help together. A small shared binding description is
acceptable where it prevents drift, but a general action framework is outside
scope. Preserve comments explaining constraints and remove only demonstrably
stale or duplicated explanations in touched code.

## Delivery order and validation

| Boundary | Deliverable | Dependencies |
| --- | --- | --- |
| A | Source-line navigation and diagnose wording | Existing source index/history |
| B | Search occurrence highlighting | Existing occurrence tracking |
| C | Explicit timeline tier | Existing timing selection |
| D | Quality jumps and background response check | A |
| E | Scoped text profile, then scoped comparison | Shared selection; comparison follows profile |
| F | Timeline zoom and pan | C |
| G | Protective report publication | Independent; integrate with E's output validation |
| H | Performance measurements and recommendation | Independent; identify whether measured code includes D |

Recommended user-facing sequence is A, B, C, D, E, F. G and H can be planned
independently. Each implementation plan defines its concrete files, behavioural
tests and review points; E should use separate profile and comparison steps.

Use TDD for features/fixes, real model and parsing logic, sanitised fixtures,
independent review and a separate test-cleanup pass after implementation.
Exercise combined journeys, not just isolated key handlers: scoped report to
source line; search to response and back; diagnostic to source and back; tier
switch to zoom to drill-down and return. Review actual terminal output at wide,
narrow and short dimensions, including resize during a pending response check.

Run relevant focused tests during work and the repository's full suite/build
before submission. Use race detection for asynchronous inspection and the
existing lint/platform checks where required. Documentation-only approval of
this spec does not claim any of those future acceptance cases pass.

## Review decisions

This draft makes concrete proposals for key bindings, text-only CLI filtering,
background inspection semantics, display-only zoom and report replacement.
Dan's review of this spec is the decision point for those behaviours. In
particular, this draft does not treat permission/link policy changes or filtered
JSON as already approved implementation. No application changes, schema changes,
merge or push are authorised by creating this document.
