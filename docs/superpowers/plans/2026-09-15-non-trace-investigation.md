# Non-TRACE Investigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Make non-TRACE evidence searchable, compact, navigable and exportable.

**Architecture:** Extend the existing event model and event panels. Pure model projections own filtering, grouping and milestones; TUI consumers retain source navigation and exports reuse the same projections. Preserve existing timing projections and source ownership checks.

**Tech Stack:** Go, standard testing, existing Bubble Tea and Lip Gloss dependencies.

**Spec:** Dan approved the five recommendations in this conversation: event search/filtering, collapsed progress, diagnostics, observed milestones, and investigation exports. Implement a CLI report plus a TUI export of the current investigation, as recommended in the optional preference question and subsequent stated assumption.

## Global Constraints

- Keep JSON `schema_version` at `1`; no compatibility modes.
- No new dependencies or tooling.
- Use British/Australian English, escape untrusted display text, and retain physical source locations.
- Missing timestamps and counts remain unavailable, not zero. Do not infer CLI clocks, exclusive phases, hangs or successful completion from missing events.
- Use real parsing/rendering tests and TDD. Do not commit private captures.
- Every commit must be signed using `/Users/dan/.codex/bin/codex-git`; stop immediately if signing fails.

### Task 1: Evidence projections and CLI diagnostics

**Files:** Modify `internal/model/events.go`; create `internal/model/investigation.go` and adjacent tests; extend `internal/model/events_test.go`.

**Interfaces:** Pure helpers accepting event slices, with exported types for an event filter (query, exact address, event kind, severity), progress groups (members and first/last source evidence), diagnostic groups (severity/address/message/source plus members), and milestones (label plus representative event). Final exact signatures are recorded in the task report for subsequent consumers.

- [ ] Write failing tests through real log loading: a boxed CLI warning with `with aws_instance.example,` and continuation detail must become one diagnostic with its source range; a provider-owned lookalike must not. Add an unboxed diagnostic and adjacent diagnostics, ensuring lifecycle/summary evidence is not swallowed. Expected source lines reference original physical lines including ANSI.
- [ ] Run `go test ./internal/model -run 'Test.*(Diagnostic|Investigation|ProgressGroup|Milestone)'` and capture the expected failures.
- [ ] Implement conservative CLI diagnostic block extraction using existing ownership checks; do not infer addresses without a recognised `with` line and a valid resource address.
- [ ] Add pure filter/group/milestone tests. Example input is start A, progress A, progress B, progress A, complete A, start A, progress A. The first two progress A events may group; the final progress A must remain in a different operation. Repeated ambiguous starts and deposed ambiguity must remain ungrouped. Filtering must find a group's hidden message; groups must retain every original member and last reported elapsed text.
- [ ] Implement projections. Case-insensitive literal search covers addresses/messages/actions/source/kind/severity. Filters combine with AND. Group only safely attributable progress; leave source event order unchanged. Diagnostics group exact severity/address/message/source with all occurrences. Milestones retain file order and identify first observed refresh/read activity, planned changes, drift, diagnostics and each summary; clocks come only from representative evidence.
- [ ] Run `go test ./internal/model` and commit the independently working model changes.

### Task 2: Searchable compact event panels, diagnostics and milestones

**Files:** Modify `internal/tui/event_panel.go`, `event_records.go`, `model.go`, `layout.go`, navigation state where necessary; create focused panel interaction tests.

**Interfaces:** Consume Task 1 helpers and retain `eventPanelState` in navigation snapshots. Existing `Enter` source and `r` resource-history navigation remain usable.

- [ ] Write failing interaction tests through `Model.Update` and rendered output. `/` edits a literal search; Enter applies and Esc cancels. Kind/severity facets combine with query and show matching/total evidence counts. Empty matches cannot jump to stale source lines.
- [ ] Run `go test ./internal/tui -run 'Test.*(EventSearch|EventFilter|ProgressCollapse|DiagnosticsPanel|MilestonePanel)'` and capture failures.
- [ ] Add search/filter controls to existing event panels, using established input patterns. Preserve query/filter/expansion/cursor state over source jumps and return. Footer/help must advertise controls at sensible widths.
- [ ] Collapse progress groups by default, with a toggle to expand selected groups and individual source jumps. Display count, first/last lines and latest progress message; do not invent elapsed duration. Filter original members before presentation so hidden matches remain discoverable.
- [ ] Add dedicated diagnostics and milestone panels accessible from all numbered views, including Raw Log. Diagnostics show grouped occurrences with expansion; milestones jump to representative source evidence and explain observed activity can overlap.
- [ ] Test source jumps and return after search, expansion and resize at 60/100/160 columns. Review any intentionally regenerated goldens with `scripts/read-golden.sh` and raw diff.
- [ ] Run `go test ./internal/tui` and commit the UI additions.

### Task 3: Investigation exports and documentation

**Files:** Create `internal/investigation/` report builder/renderers/tests; extend `cmd/tfli/main.go` and CLI tests; add TUI export integration if selected; update `README.md` and `docs/investigation-json-v1.md`.

**Interfaces:** Consume Task 1 projections and existing measured timing rows. Report metadata records input basename and explicit selected scope, query and facets. Event filters do not silently masquerade as timing filters. JSON uses explicit snake_case DTOs and `schema_version: 1`.

- [ ] Add `--investigate` for CLI reports; `--format markdown` (also the default text rendering for this mode) or `--format json`. Existing profile/comparison formats remain text/json. In the TUI, an export prompt selects Markdown or JSON and a destination; use a currently unbound key and expose it in help.
- [ ] Write failing tests with a real sanitised log containing timings, planned changes, a summary with missing counts, and an incomplete operation. Assert Markdown/JSON retain source references, observed zero versus missing, and untimestamped CLI evidence. Capture and assert output failures and invalid flags.
- [ ] Implement a shared report builder with scoped timings, events/outcomes, incomplete evidence, diagnostics and milestones. Markdown uses escaped literal log text; JSON retains original evidence safely encoded. Include the current investigation selection when exporting from TUI.
- [ ] Integrate CLI report dispatch using existing input/output protection. If TUI export is selected, provide a filename prompt, clear format selection and success/error feedback; prevent overwriting the input and existing files. Handle cancellation without writing.
- [ ] Test writer failures, hostile text, existing destinations, same-file aliases, CLI mode/format conflicts and TUI success/cancellation/error behaviour. Keep existing profile/comparison contracts unchanged.
- [ ] Document keys, scope semantics, export examples and the JSON v1 contract. Run `go test ./...`, `go build ./...`, configured lint and formatting checks, then commit.

### Task 4: Independent verification and test cleanup

**Files:** Review all changes from `f0ffd31`; retain only sanitised verification evidence in repository documentation.

- [ ] Run a separate test-cleanup agent pass after implementation; preserve meaningful coverage.
- [ ] Obtain task-scoped reviews after each implementation task and a final whole-branch review.
- [ ] Inspect all three supplied captures read-only through new views and exports, including 645 progress events in the slow capture. Verify retained evidence counts, source navigation, zero RPC assumptions and unavailable CLI clocks.
- [ ] Run final `go test -race ./...`, `go build ./...` and configured lint after any fixes. Confirm signed commits and clean worktree. Keep this branch local until Dan requests push/PR.
