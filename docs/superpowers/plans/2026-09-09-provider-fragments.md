# Provider Fragment Reconstruction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Scrub complete interleaved provider JSON messages while preserving their physical records.

**Architecture:** Shared reconstruction returns logical JSON plus source ranges. Scrubbing separates body discovery from physical metadata, then maps rendered bodies back to their fragments. Inspector consumers can use the same reconstruction without changing scanner ordinals.

**Tech Stack:** Go standard library and the existing logfmt/scrub/model/TUI packages.

**Spec:** `docs/superpowers/specs/2026-09-09-provider-fragments-design.md`

## Global Constraints

- Go 1.25+, existing dependencies only. British/Australian prose. Signed commits.
- Raw bytes and scanner entry IDs remain authoritative. No private fixtures.

### Task 1: Shared reconstruction

Files: `internal/logfmt/fragments.go`, `internal/logfmt/fragments_test.go`.

Interface: `ReconstructProviderJSON(text string) ([]ProviderJSON, error)`;
`ProviderJSON` contains `Text string` and `Fragments []JSONFragment`;
`JSONFragment` contains `Start, End, Line int` source coordinates.

- [x] Write tests building two synthetic JSON messages, slicing each into
  fragments and alternating provider-prefixed records. Assert reconstructed
  text equals the independent original messages and ranges recover every byte.
- [x] Run `go test ./internal/logfmt -run 'TestProviderJSON' -count=1` and
  observe failure before implementation.
- [x] Implement per-component pending JSON, source ranges, exact payload
  boundaries and JSON completion checking. Complete messages must pass
  `json.Valid`; unfinished streams return line-number-only errors.
- [x] Add boundary tests for escaped strings, diagnostic tags, plain
  continuations, malformed JSON, interruption, CRLF and missing final newline.
- [x] Run package tests and commit with signing.

### Task 2: Scrub reconstructed messages and preserve records

Files: `internal/scrub/fragments.go`, `internal/scrub/fragments_test.go`,
`internal/scrub/scrub.go`, `internal/scrub/syntax.go`,
`internal/scrub/aliases.go`.

Consumes the shared interface. A source-boundary list on each view records
output offsets during rendering. Replacements crossing a cut are atomic.

- [x] Write a failing end-to-end `Scrub` test with interleaved responses,
  a credential split across fragments, repeated names and `tf_req_id=abc`
  outside the final JSON body. Require zero leaks and valid reconstructed JSON.
- [x] Mask body ranges with spaces of identical byte length; parse metadata
  from this input and discover bodies in separate response JSON views.
- [x] Render logical views once, tracking fragment cuts through replacements;
  insert their pieces into rendered physical views at their mapped boundaries.
- [x] Validate metadata against masked physical input/output and validate
  resource identity across both physical and reconstructed views.
- [x] Run scrub tests, including all existing regressions, then signed commit.

### Task 3: Inspector consumption and completion

Files: `internal/model/log.go`, focused model/TUI tests and documentation as
needed for Dan's selected UI scope.

- [x] Expose complete responses by source entry while preserving `Bytes` and
  `Entries`; test cross-fragment retrieval using an actual temporary log.
- [x] Implement the selected inspector presentation with tests for navigation
  and search using the same displayed text, preserving the raw view.
- [x] Document reconstruction and its fail-closed limits in README.
- [x] Run full tests with the signing-safe git shim, `go test -race ./...`,
  `go build ./...`, `go vet ./...`, and `codex-git diff --check`.
- [x] Obtain independent code review, address reproduced findings with TDD,
  and run the separate test-cleanup pass. Verify all commits are signed.

## Verification outcome

The complete suite, race detector, coverage checks, build and vet passed.
Independent task and final reviews are clean after fixing footer guidance,
filtered response lookup and metadata visibility across the scanner window.
Separate test cleanup retained the security and boundary regressions. Manual terminal
checks used synthetic interleaved responses only.

Authorised inspection of lines 11360–11361 identified a complete inline Terraform
UI event inside the provider JSON string. Reconstruction now excludes that event
from the provider join while retaining it for scrubbing. Synthetic roundtrip
tests and separate full-suite, race and coverage checks pass.

Authorised inspection of lines 11438–11439 identified an ordinary AzureAD request
dump continuation. Reconstruction now follows its existing scanner entry ownership
even while another provider is pending. A synthetic regression also identified
and fixed UI insertion immediately after a JSON escape backslash.

Authorised inspection of lines 11417–11418 identified a standalone Terraform UI
event between provider object properties. Reconstruction now excludes validated
UI envelopes at object-key positions while preserving legitimate objects at
value positions. Independent review and separate test cleanup found no further
issues; full tests, race, coverage, build and vet passed.

The permitted actual-capture scrub command completed successfully on 9 September
2026 and wrote its output to a temporary file. It reported 33,824 secret
replacements and 632 unsupported structured or quoted inputs. Successful
reconstruction does not establish complete sensitive-data removal; the output
was not opened or inspected. Inspector checks remain synthetic-data-only.
Direct inspection stayed within the authorised lines 11360–11361, 11417–11418,
11438–11439 and 11711–11713.
