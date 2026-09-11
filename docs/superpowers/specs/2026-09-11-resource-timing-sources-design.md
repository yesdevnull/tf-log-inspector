# Resource timing from refresh windows and CLI completions

Dan requested extraction of refresh timestamp windows and plain-text CLI completion durations, together with the presentation recommendations from the three-capture review. The existing presentation fixes remain in place.

## Behaviour

Resources and Types rank resource operations from three explicitly labelled duration sources: `ui_elapsed`, `refresh_window`, and `cli_elapsed`. RPC evidence stays separate. Structured completion durations keep their whole-second qualification. Refresh duration is the difference between matched structured start and completion timestamps; it is a hook window, not a provider RPC measurement. CLI completion durations keep their displayed resolution and never acquire invented timestamps.

Structured refresh windows share the existing structured UI clock. CLI operations have unavailable positions and can be ranked, filtered, inspected and compared by duration, but cannot contribute timeline lanes, concurrency or capture wall-clock duration. Zero-duration observations remain valid observations. Resource totals count admitted operations and can overlap; they are not run elapsed time.

## Extraction and integrity

- Match `refresh_start` and `refresh_complete` by exact resource address. Retain start and completion source locations. Require valid timestamps, a non-negative elapsed interval and unambiguous pairing. Unmatched, repeated-open, incomplete, malformed and backwards pairs are diagnosed, never fabricated.
- Keep structured `apply_complete`/error and ephemeral completion behaviour. Do not count progress messages as completions. Refresh records carrying no `elapsed_seconds` are no longer mistaken for missing reported durations.
- Parse anchored Terraform CLI completion lines after ANSI removal, including Read, Creation, Modifications, Destruction and Refresh completions and compound durations such as `2m16s`. Preserve exact module/instance addresses and physical source links. Reject malformed duration/address inputs; do not parse nested provider response text or arbitrary message substrings.
- CLI extraction is a fallback for captures without structured lifecycle hooks or timestamped provider/core entries. In mixed captures prefer structured lifecycle evidence and report suppressed CLI candidates, avoiding double-counting when the same operation appears in both renderings. Do not suppress independent repeated operations within the accepted CLI stream.
- Bound arithmetic using existing span saturation/lower-bound conventions. Bound open-pair tracking and report any overflow. Source files and raw entry content remain unchanged.
- Both `--diagnose` and model loading use the same extraction and admission rules. Quality reports retain admitted/rejected/positioned counts and source-specific extraction issues.

## Model and presentation

Keep the existing resource-operation collection (`Log.UISpans`) and structured clock boundary rather than adding competing Resources tabs. Add duration provenance to `span.Span`; provenance is independent of clock fidelity. Retain `FidelityUIReported` as the resource-clock discriminator so existing timeline guards still exclude mixing RPC and structured clocks. Update its documentation accordingly.

Public internal interfaces:

```go
type DurationSource uint8
const (
    SourceUIElapsed DurationSource = iota
    SourceRefreshWindow
    SourceCLIElapsed
)
func (s DurationSource) String() string // ui_elapsed, refresh_window, cli_elapsed
// Span gains DurationSource DurationSource, StartEntry uint32,
// and HasStartEntry bool. Entry remains the completion entry.
```

New resource sources must appear correctly in Resources, operation detail, Types, timeline headings/detail, evidence, capture quality, text profile and masked diagnose. Replace unconditional claims that all resource durations are rounded UI measurements with source-aware qualifications. Sources and their counts/durations must be visible in aggregate evidence; individual operations name their source. Missing RPC evidence stays unavailable, and no activity annotation implies idleness or causation. UI-only or CLI-only captures open Resources.

Source links for refresh show the start-to-completion physical range. CLI completions link to their own physical line. Do not make one giant untimestamped entry the identity of all CLI observations; scanner indexing must support distinct completion entries while preserving raw navigation.

## Export and comparison

Profile and comparison JSON advance together to schema version 3 because the resource timing contract is broader. Keep existing RPC/UI collection keys to avoid a needless global rename; document `ui` as the resource-operation tier. Add `duration_source` to resource observations and source breakdowns to resource totals/capture evidence. No v2 compatibility output mode is required.

Comparisons separate source kinds in resource operation and type keys. A refresh-window measurement must not be compared with a reported completion merely because the address/type and action match. Explain changes in observed source coverage. Preserve unavailable values, lower-bound handling and independent scrub-alias qualifications.

## Verification

Use small synthetic sanitised fixtures for automated tests, not copies of Downloads captures. Cover concurrent and repeated addresses; incomplete, unmatched and backwards refresh pairs; ANSI CLI lines; compound and zero durations; malformed and overflowing durations; nested-message rejection; mixed-stream suppression; source navigation and filtering; absent RPC evidence; JSON provenance and comparison source separation; and narrow terminal layouts.

The two supplied structured captures each contain 267 reported completions and 385 refresh pairs: expect 652 resource operations in each. Reported-completion totals remain 6,708 s and 58 s respectively. The CLI capture contains 350 completion durations, summing to 204 s, with zero positioned observations. Independently reconcile extracted records with source lines and timestamps, including millisecond quantisation, rather than deriving expectations through production helpers.

Run TDD, full Go tests with race detection, build, configured lint, reviewed terminal goldens and all three real captures. Require independent code review and a separate test-cleanup pass. All commits are signed using the Codex git wrapper.
