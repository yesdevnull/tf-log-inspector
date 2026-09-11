# Task 4 implementation report

## What I implemented

- Added the exact seven-physical-line sanitised fixture at `testdata/response-recovery.log`, including its final newline.
- Added one fixture-backed integration journey through the real `tui.Model.Update`, `model.Log.ProviderResponseAt`, `model.Log.ReconstructionQuality`, and `scrub.Scrub` APIs. It checks the lazy initial state, raw search and exact return position, decoded response search, retained navigation history, every physical-line selection state, exact partial counts, safe failure views, and a zero scrub result on strict rejection after inspection.
- Inspected the existing README guidance before editing it. Lines 188–206 already describe top physical-line selection, complete recovered bodies, the persistent partial-capture notice, lazy quality states and counts, and strict scrub refusal, so no duplicate wording was added.
- Did not change application code: the finished Tasks 1–3 implementation passed the complete journey on its first focused run. No production defect was exposed.
- Did not change the implementation plan, investigation-workflows specification, or Boundary I status. Those remain with the controller until the separate cleanup, task review, and whole-branch review finish.

## TDD and regression evidence

The task brief explicitly allows the finished journey to pass after Tasks 1–3 and directs that outcome to be recorded as regression evidence. The new test passed on its first run before any application change:

```text
go test ./internal/tui -run '^TestResponseRecoveryJourneyPreservesRawInvestigationAndStrictScrubbing$' -count=1
ok  github.com/yesdevnull/tf-log-inspector/internal/tui  0.315s
```

There is no behavioural RED because there was no missing production behaviour to implement or defect to fix. The test would fail if raw search selected the wrong physical entry, a modal changed the raw occurrence or history, decoded search stopped working, any state/source-line mapping changed, status views disclosed failed source bodies, quality published early or with the wrong counts, or `scrub.Scrub` returned partial output.

## Actual PTY journey

The programme was run twice as required through a Python standard-library `pty` harness:

```text
python3 /tmp/tfli-i2-terminal/run_journey.py
PASS 100x30: raw=/tmp/tfli-i2-terminal/100x30.raw; transcript=/tmp/tfli-i2-terminal/100x30.txt
PASS 60x9: raw=/tmp/tfli-i2-terminal/60x9.raw; transcript=/tmp/tfli-i2-terminal/60x9.txt
```

Each child process executed this actual command in its PTY:

```text
go run ./cmd/tfli testdata/response-recovery.log
```

For both 100×30 and 60×9, I inspected these actions and screens:

1. Opened quality with `i`, scrolled until `RECONSTRUCTION`, and observed `not checked (response reconstruction is lazy)` before response inspection.
2. Closed quality, selected Raw Log with `6`, and confirmed list focus.
3. Searched for `recovered response`; the footer moved to Entry 3/6, line 1, retaining the exact horizontal search occurrence.
4. Opened `r`; the complete decoded `recovered response`/`needle third` body appeared with the persistent partial-reconstruction notice and partial-capture title.
5. Searched the decoded modal for `needle third`; the narrow 60×9 screen displayed that decoded match while keeping the notice visible.
6. Resized the 100×30 session to 60×9 and back while the response was open, and resized the 60×9 session to 100×30 and back. The body, search position, title, and persistent notice survived both directions.
7. Closed with Esc; both screens returned to Entry 3/6, line 1 at the same horizontal occurrence.
8. Moved to physical line 2 and observed `This response is incomplete or invalid.`; the 100×30 screen showed `Source line 2.` and a safe delimiter diagnostic without the failed body.
9. Moved to physical line 4 and observed `This stream is unavailable after an earlier failure.`; the 100×30 screen showed `Source line 4.` and the safe earlier-failure diagnostic.
10. Moved to physical lines 5 and 6 and observed `No reconstructed response at this physical line.` for the ordinary entry and its continuation. The wide screens showed the exact source line; the narrow return footers proved Entry 5/6 line 1 then line 2.
11. Moved to physical line 7 and observed `This response is incomplete or invalid.` with `Source line 7.` and the safe incomplete diagnostic on the wide screen.
12. Opened quality and scrolled until the actual screen showed `partial: 2 responses available; 2 reconstruction diagnostics`; the 60×9 rendering wrapped after `reconstruction` without losing the two counts.

The final harness makes PASS conditional on every required screen state and return footer. Raw terminal streams and plain-text snapshots are retained only under `/tmp/tfli-i2-terminal`; searches found no private, secret, token, credential, customer, or account text in either transcript.

### PTY harness investigation

The first evidence attempt was rejected because slash/query/Enter and arrow/`r` sequences were each written to the PTY as one byte batch. Raw frames showed that Bubble Tea received these as grouped key events: search stayed on Entry 1 and subsequent labels did not match displayed states. The direct `Model.Update` journey remained green, isolating the problem to the harness boundary.

The harness now sends each logical key event separately, waits for the initial rendered footer instead of sleeping for programme readiness, and scrolls quality until the required text is present instead of assuming a fixed page count. It fails immediately when any expected body, status, source footer, return position, or quality count is absent. The corrected asserted runs above passed; no application change was made for this harness fault.

## Final validation

The complete required matrix ran once against the final application tree with full per-command output retained under `/tmp/tfli-i2-validation`:

```text
python3 .superpowers/sdd/2026-09-11-response-recovery-viewer/validate.py --repo /Users/dan/Code/tf-log-inspector --output /tmp/tfli-i2-validation
PASS format: exit=0
PASS tidy: exit=0
PASS modules: exit=0
PASS build: exit=0
PASS lint: exit=0
PASS linux-amd64-packages: exit=0
PASS linux-amd64-cli: exit=0
PASS linux-arm64-packages: exit=0
PASS linux-arm64-cli: exit=0
PASS darwin-amd64-packages: exit=0
PASS darwin-arm64-packages: exit=0
PASS darwin-amd64-cli: exit=0
PASS darwin-arm64-cli: exit=0
PASS race: exit=0
```

I inspected every retained log. `race.log` reports all eleven packages `ok`, including `internal/tui`, `internal/scrub`, and `cmd/tfli`; `modules.log` says `all modules verified`; `lint.log` says `0 issues.` Build, format, tidy, and all eight cross-build logs are empty, as expected for successful commands. The generated cross-platform CLI binaries are `/tmp/tfli-i2-{linux,darwin}-{amd64,arm64}`. These are local cross-builds, not remote CI.

The final race output was:

```text
ok  github.com/yesdevnull/tf-log-inspector/cmd/tfli  3.858s
ok  github.com/yesdevnull/tf-log-inspector/internal/attrib  1.594s
ok  github.com/yesdevnull/tf-log-inspector/internal/diagnose  2.740s
ok  github.com/yesdevnull/tf-log-inspector/internal/logfmt  1.861s
ok  github.com/yesdevnull/tf-log-inspector/internal/model  2.608s
ok  github.com/yesdevnull/tf-log-inspector/internal/profile  2.349s
ok  github.com/yesdevnull/tf-log-inspector/internal/qualitytext  1.979s
ok  github.com/yesdevnull/tf-log-inspector/internal/scrub  5.345s
ok  github.com/yesdevnull/tf-log-inspector/internal/span  3.181s
ok  github.com/yesdevnull/tf-log-inspector/internal/tui  7.726s
ok  github.com/yesdevnull/tf-log-inspector/scripts  12.471s
```

I built a host CLI and checked its complete output streams:

```text
go build -trimpath -o /tmp/tfli-i2-host ./cmd/tfli

python3 .superpowers/sdd/2026-09-11-response-recovery-viewer/check_cli_json.py --binary /tmp/tfli-i2-host --before testdata/provider-rpc.log --after testdata/two-rpcs.log --output /tmp/tfli-i2-validation/cli-json
PASS profile: one v2 document; 1 lazy snapshots with exact ordered keys and nulls
PASS comparison: one v2 document; 2 lazy snapshots with exact ordered keys and nulls
```

I decoded and inspected both retained documents with `jq .`. The profile root is `schema_version: 2`, `kind: profile`; the comparison root is `schema_version: 2`, `kind: comparison`. All three fresh reconstruction objects have the ordered keys `state`, `responses`, `diagnostics`, `code` with values `not_checked`, `null`, `null`, `null`. Python's single `json.loads` call consumed each complete stdout byte stream, so an extra document or non-whitespace trailing output would have failed.

## Specification evidence

| Requirement | Evidence |
| --- | --- |
| One parser and independent retained messages/safe diagnostics | The journey consumes `ProviderResponseAt` and the actual `scrub.Scrub` on the same source. No parser or grammar file changed. The final race suite includes the I1 reconstruction and strict scrub tests. |
| Selected physical position and multiple bodies | The test asserts all seven physical positions through literal entry/line-offset pairs. PTY raw search and exact return show Entry 3/6; Tasks 1–2 retain the broad-entry/index cases. |
| Invalid, unavailable, and ordinary states | Automated and PTY evidence distinguish line 2 invalid, line 4 unavailable, lines 5/6 none, and line 7 invalid with safe copy and exact wide-screen source lines. |
| Recovered full body and persistent notice | The fixture's line 3 opens `recovered response` plus decoded `needle third`; notice/title remain visible through search, scrolling layout, and both resize directions. |
| Scrolling, search, controls, return, and history | The new test drives raw and decoded searches through `Model.Update`, verifies the exact raw match/query/column and a detached history snapshot after Esc, and the PTY checks the same raw footer. Existing modal tests remain in the race suite. |
| Lazy complete/partial/failed quality | Before inspection, both automated and terminal views show not checked. After inspection the canonical snapshot and panel show partial with exactly two responses and two diagnostic records. Task 3's complete/failed variants remain in the race suite. |
| Explicit profile/comparison contract | Actual host CLI profile and comparison outputs are single v2 documents. All three fresh snapshots use the exact ordered four-key lazy/null representation. Full decoded JSON is retained and inspected. |
| Strict scrub refusal after inspection | The new same-source journey calls actual `scrub.Scrub` after viewer inspection and requires an error plus `scrub.Result{}`. `TestScrubRejectsPartialProviderJSONRecovery` and `TestRunScrubRejectsPartialProviderJSONRecoveryBeforePublication`, including absent/existing output cases, remain in the passing race suite. |
| Performance, races, source immutability, and detachment | Tasks 1 and 3 retain the response benchmarks and concurrent/detachment tests. The final `-race -count=1 ./...` suite passes. The journey verifies modal inspection preserves the exact raw occurrence/history and cannot alter strict scrub output. |

## Files changed

- `testdata/response-recovery.log`
- `internal/tui/response_recovery_journey_test.go`
- `.superpowers/sdd/2026-09-11-response-recovery-viewer/task-4-report.md`

## Self-review

- Confirmed the fixture has exactly seven physical lines, 466 bytes, and a final newline, and matches the task brief byte-for-byte by inspection.
- Confirmed the journey uses real model, TUI update, rendering, file loading, and scrub boundaries without mocks.
- Confirmed initial quality rendering does not trigger reconstruction; the first actual response inspection publishes partial 2/2.
- Confirmed line 1 and line 3 select their own complete bodies; lines 2, 4, 5, 6, and 7 map to invalid, unavailable, none, none, and invalid respectively.
- Confirmed invalid/unavailable/ordinary views do not contain the fixture's failed or raw source body text.
- Confirmed raw position, query, last query, match, horizontal column, and navigation history survive response search and Esc.
- Confirmed strict scrub refusal is checked after viewer inspection and the returned result is exactly the zero value.
- Confirmed the existing README already contains all requested delivery guidance and was not changed.
- Confirmed no application, plan, specification, status, dependency, or golden file changed.

## Concerns

None. Separate test cleanup, task review, broad whole-branch review, and Boundary I completion recording remain controller gates.
