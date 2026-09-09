# C1 final fix report

## Findings resolved

- Diagnose capture wall-clock availability now depends on parsed structured timestamp evidence. Reported UI durations cannot fabricate capture time. A parsed single timestamp retains the genuine numerical `0.0s` result.
- The entirely unavailable timeline branch now fits its supplied height through the existing pane fitter and ends a cut at height two or greater with `… more`.
- The `busyNote` zero-window comment now matches its fraction-unavailable output.

No timeline goldens changed. Capture wall-clock remains an observed timestamp range; admitted span durations remain duration evidence and are not used to infer that range.

## Behavioural RED

Command:

    go test ./internal/diagnose -run 'TestReportWallClock(UnavailableWhenUIDurationHasNoParsedTimestamp|PreservesCoincidentParsedEndpoints)$' -count=1

Relevant output:

    --- FAIL: TestReportWallClockUnavailableWhenUIDurationHasNoParsedTimestamp (0.00s)
        report inferred a capture clock from a duration with no parsed timestamp:
          log wall-clock       0.0s (derived from UI-hook resource timings)
    FAIL

The invalid timestamp fixture was scanned through the real scanner and builders. It admitted one 1000ms UI duration but produced no parsed timestamp evidence.

Command:

    go test ./internal/tui -run 'TestUnavailableTimelineMarksAHeightCut$' -count=1

Output:

    --- FAIL: TestUnavailableTimelineMarksAHeightCut (0.00s)
        timeline rendered 3 lines at height 2:
        Timeline positions unavailable: 1 admitted observations;
        20ms retained in duration totals.
        Excluded positions: timestamp_before_origin 1.
    FAIL

The existing `TestReportWallClockUnavailableWhenNeitherSourceExists` covers an absent capture clock. A trial fixture with an absent `@timestamp` and elapsed duration correctly built no UI span, so the admitted-duration regression uses the real invalid-timestamp admission path.

## GREEN and validation

Focused diagnose command:

    go test ./internal/diagnose -run 'TestReport.*WallClock' -count=1

Output:

    ok  github.com/yesdevnull/tf-log-inspector/internal/diagnose  0.279s

Focused timeline command:

    go test ./internal/tui -run 'Test(UnavailableTimelineMarksAHeightCut|TimelineExplainsExcludedPositionReasonsWithoutClippingTotals|TimelineKeepsRPCDurationsWhenTheirPositionsAreUnavailable)$' -count=1

Output:

    ok  github.com/yesdevnull/tf-log-inspector/internal/tui  0.244s

Full suite command:

    go test ./...

Output: all packages passed, including `cmd/tfli`, `internal/diagnose`, `internal/model`, `internal/profile`, `internal/span`, `internal/tui`, and `scripts`; the uncached diagnose result was `ok ... 0.308s`.

Diff validation:

    /Users/dan/.codex/bin/codex-git diff --check

Output: empty, exit zero.

## Files and self-review

- `internal/diagnose/diagnose.go`: adds explicit parsed-clock availability, removes span-end inference, and renders the observed structured timestamp range.
- `internal/diagnose/diagnose_test.go`: real scanner/report regressions for invalid clock evidence and coincident parsed endpoints.
- `internal/tui/timeline.go`: fits unavailable output to height and corrects the zero-window comment.
- `internal/tui/timeline_test.go`: verifies a short unavailable pane uses the established marked-cut convention.

Self-review found no changes outside the three findings. The explicit availability boolean is necessary because a zero millisecond value represents both coincident endpoints and the zero value of an unavailable measurement. The timeline uses the existing fitter without adding navigation or state. No concerns remain.
