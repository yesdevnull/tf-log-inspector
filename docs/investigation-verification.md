# Non-TRACE investigation verification

The investigation work branches from `f0ffd31`. Captures were read from the supplied scrubbed files without modifying them or copying their contents into the repository.

## Evidence reconciliation

An independent inventory of raw lifecycle and diagnostic headings was compared with the loaded event model. All captures have zero RPC spans. CLI events retain unavailable timestamps.

| Capture | Starts | Progress | Completions | Drift | Planned entries | Diagnostics | Progress groups |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Slow structured plan | 652 | 645 | 652 | 11 | 0 | 0 | 38 |
| Normal structured plan | 652 | 0 | 652 | 12 | 0 | 0 | 0 |
| Normal CLI plan | 863 | 0 | 350 | 0 | 53 | 11 | 0 |

The 38 slow-plan progress groups retain all 645 original observations. The CLI capture's 11 diagnostic events match 11 raw warning/error headings. Its 53 planned entries comprise 52 creates and one deferred read. Each capture retains its summary; neither absent optional counts nor absent clocks are replaced with zero. Milestones remain in physical source order.

## Terminal inspection

All five investigation panels were opened from Raw Log against all three captures at widths of 60, 100 and 160 columns. These 45 cases checked rendering, physical source jumps and return to the original panel. Source checks compare both the logical entry and the physical line offset within it.

Sanitised terminal frames also exercised compact progress, repeated diagnostics and milestones at the same widths. Representative evidence includes:

```text
Event history (whole capture)
7/7 matching evidence
start · aws_instance.web · create · cli · clock unavailable · line 1
progress · 2 progress observations · aws_instance.web · create · lines 2–3
latest: aws_instance.web: Still creating... [20s elapsed]
complete · aws_instance.web · create · cli · clock unavailable · line 4

Diagnostics (whole capture)
2/2 matching evidence
warning · aws_instance.web · 2 occurrences

Milestones (whole capture)
Observed activity can overlap; milestones do not infer sequential phases.
```

These are excerpts of inspected synthetic output. They contain no private capture text. Review identified a clipped quit hint on the narrow footer; its correction is included with export integration.

## Regression coverage

Real parsing tests cover addressless CLI diagnostics, provider-owned lookalikes, unfinished diagnostic boxes, adjacent boxed and plain diagnostics, exact case-sensitive resource identities, ambiguous progress attribution and repeated operations. Panel interaction tests cover literal searches, combined facets, empty selections, expansion, resize and source-return state.

Independent task reviews found and drove fixes for diagnostic admission/boundaries, exact-address comparison and diagnostic-only filter counts. Separate test-cleanup passes retained the distinct regressions and interaction coverage.

## Export reconciliation

The CLI JSON reports were generated from each supplied capture and checked independently against the raw files. Both structured captures exported 652 timings: 267 reported UI durations and 385 refresh windows. The CLI capture exported 350 reported durations. Event totals were 1,961, 1,317 and 1,278 respectively.

Every exported event's byte range matched its physical source lines. The verifier also checked snake_case field names, schema version 1, null CLI clocks, all 11 CLI diagnostic occurrences, empty incomplete-operation lists, and source-ordered milestones. Structured summaries retained explicit zeroes for all five counts; the CLI summary retained 52 additions with unavailable import and action-invocation counts.

TUI export was also exercised at 60 columns against the actual captures: a progress query retained all 645 slow-plan progress events, a milestone query retained the normal plan's summary, and a diagnostic query retained all 11 CLI diagnostics. Each report preserved the query, selected source reference and configured binary version. Generated reports remained outside the repository.

## Validation

At implementation commit `1ac0963`, `go test -race ./...`, `go build ./...` and `golangci-lint run --timeout=5m` all passed. Each implementation task received an independent review and a separate test-cleanup pass. The narrow footer was re-rendered after correction and retains complete Esc, help and quit hints.
