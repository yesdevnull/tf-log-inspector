# Resource Timing Sources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Extract and explain refresh windows and CLI completion durations throughout tfli.

**Architecture:** Extend the resource-operation tier with duration provenance independent of clock fidelity. Reuse the scanner/model/report flows; retain separate RPC clocks and unpositioned CLI measurements.

**Tech Stack:** Go 1.25+, existing Bubble Tea/Lip Gloss and standard testing tooling.

**Spec:** docs/superpowers/specs/2026-09-11-resource-timing-sources-design.md

## Global Constraints

- Small, straightforward changes; no new dependencies or backwards-compatibility modes.
- British/Australian spelling; sanitised fixtures; never commit Downloads captures.
- TDD for behaviour changes; preserve truthful source and timing qualifications.
- All commits signed with `/Users/dan/.codex/bin/codex-git`; signing failure stops work immediately. No pushes or merges.
- Continue on `wip/verify-non-rpc-logs`; it already holds the presentation fixes.

### Task 1: Extract resource duration sources and integrate admission

**Files:** `internal/span/span.go`, `uihook.go`, new CLI parser/builder files and tests; `internal/logfmt/scan.go` and scanner tests as needed; `internal/model/log.go`, `location.go`, quality integration and tests; `cmd/tfli/main.go` diagnose collection; `internal/diagnose/diagnose.go`; small `testdata` fixtures.

**Interfaces:** Implement the DurationSource constants and Span fields from the spec exactly. Consumers continue reading `Log.UISpans`, `UIEvidence`, `UIOrigin`. Expose a model helper for observation source ranges if needed, and document its exact signature in the task report. Do not change JSON or TUI in this task.

- [ ] Add failing builder and model/diagnose tests for matched refresh pairs and CLI completions. Tests must assert literal source, duration, admission and position results, e.g. a pair from `2026-09-11T00:00:01Z` to `2026-09-11T00:00:03.250Z` yields `2250` ms and `SourceRefreshWindow`; `aws_instance.example: Creation complete after 2m16s` yields `136000` ms and no position.

```go
if got.DurationMs != 2250 || got.DurationSource != span.SourceRefreshWindow || !got.HasPosition() {
    t.Fatalf("refresh observation = %+v", got)
}
```

- [ ] Run focused tests and record expected RED failures.
- [ ] Implement pairing, CLI fallback admission, exact entry identity/source ranges, source evidence and shared diagnose/model admission. Avoid interpreting provider payloads as lifecycle messages. Count malformed/unmatched/backwards/suppressed evidence explicitly.
- [ ] Add and run boundary regressions from the spec; keep existing suites passing where output contracts have not intentionally changed. Record any downstream presentation expectations needing Task 2/3 updates.
- [ ] Validate the three real captures using the counts in the spec; run `go test ./internal/span ./internal/logfmt ./internal/model ./internal/diagnose ./cmd/tfli`, `go build ./...`, and signed commit.

### Task 2: Present all sources in terminal and text reports

**Files:** `internal/tui/resources.go`, `resource_operations.go`, `resource_evidence.go`, `timeline.go`, `layout.go`, `views.go`, help/quality renderers and tests; `internal/profile/profile.go`, `data.go`, source mapping and tests; `internal/diagnose/diagnose.go`; README and reviewed goldens.

**Interfaces:** Consume `Span.DurationSource`, `StartEntry`/`HasStartEntry`, and Task 1 source-location helper. Use existing resource selection/projection and timing exclusion flows. JSON/comparison formatting belongs to Task 3.

- [ ] Add failing rendered-output and navigation tests covering each source. A CLI operation must show its source and duration, open its physical line, and produce no timeline lanes. A refresh detail must show both source endpoints and timestamp-derived qualification; a mixed resource table must not label every duration rounded.

```go
if strings.Contains(text, "all durations are rounded") || !strings.Contains(text, "refresh") {
    t.Fatalf("missing source qualification: %s", text)
}
```

- [ ] Run focused tests to observe RED; implement source-aware labels and totals/quality breakdowns. Retain empty RPC handling and neutral activity wording. Replace the temporary unsupported-CLI guidance.
- [ ] Run TUI/profile/diagnose tests, regenerate affected goldens intentionally, inspect them with `scripts/read-golden.sh` and inspect raw diffs.
- [ ] Open the three captures and check Resources, operation/source drill-down, Types and Timeline. Run `go test ./...`, build and signed commit.

### Task 3: Preserve provenance in JSON and comparisons

**Files:** `internal/profile/json_data.go`, JSON data/contract tests, comparison JSON/text rendering and tests; `internal/model/comparison.go`, delta ordering and tests; schema documentation under `docs`; relevant CLI JSON contract tests.

**Interfaces:** Consume Task 1 provenance and source range helpers. Schema version is `3` for profile and comparison. Retain RPC/UI top-level tier keys. Resource observation `duration_source` is one of the three values in the spec. Source-aware totals expose source counts and durations. Resource comparison keys include duration source.

- [ ] Add failing contract tests requiring version 3, source provenance, refresh source range and null CLI positions. Add comparison tests proving identical addresses/actions with different duration sources stay separate.

```go
if doc["schema_version"] != float64(3) {
    t.Fatalf("schema_version = %v", doc["schema_version"])
}
```

- [ ] Implement serializers and comparison grouping/order/qualification changes; update exact JSON contracts and document every new field. Do not silently match unlike duration sources.
- [ ] Run focused JSON/comparison tests followed by `go test -race -count=1 ./...`, `go build ./...`, `golangci-lint run --timeout=5m`, formatting/diff checks and signed commit.
- [ ] Reconcile all three captures independently, run task/final reviews and independent test cleanup, resolve findings, and record final validation.
