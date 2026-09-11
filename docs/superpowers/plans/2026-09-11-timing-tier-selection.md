# Explicit Timing-Tier Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let readers independently inspect RPC and UI timelines without losing observation identity or silently changing clocks.

**Architecture:** Keep the default decision in `model.PreferredTiming` and store explicit tier choice plus per-tier observation identities in the TUI's value-owned timeline state. Reconcile the active cursor by original observation index when filtering or changing tiers. Refresh tier-dependent presentation through the existing cache invalidation path, and bind `t` only when the timeline list owns the keyboard.

**Tech Stack:** Existing Go toolchain, Bubble Tea, Lip Gloss and standard-library tests; no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-11-investigation-usability-design.md`, shared invariants and boundary C. Baseline `323a114`; topic branch `feature/timing-tier-selection`. Boundaries A/B and the AzureRM reconstruction fix are already on main.

## Global Constraints

- Original bytes, scanner entry ordinals and original per-tier observation indices remain stable through filtering, navigation and rendering.
- Source lines are one-based physical lines in the original file, including continuations. Entry-relative lines must be labelled as such if displayed.
- UI and RPC retain separate clocks, durations and qualifications. No combined total, automatic clock alignment or claim of causal blocking is introduced.
- Whole-log capture quality remains whole-log. Selected totals and displayed timeline windows must have their own explicit scope labels.
- Preserve unavailable values, measured zero, lower bounds, attribution confidence and observations excluded from temporal rendering.
- User-derived text is escaped before terminal display. Search highlighting must not reintroduce source control sequences.
- Strict scrubbing still refuses every reconstruction diagnostic. Viewing, checking or navigating a capture never changes scrub acceptance.
- New prompts are modal: typed command letters are text. Esc cancels without changing the underlying investigation; Ctrl+C retains the quit behaviour.
- Navigation frames own their mutable state. Returning restores filters, selection identity, searches, raw position and timeline state exactly, subject only to viewport clamping after terminal resize.
- Availability is based on admitted observations in the whole capture, before filtering or position projection. An unavailable position is not an unavailable tier.
- Profile and JSON timeline preference remain unchanged. No schema, compatibility adapter, zoom or pan implementation belongs to this boundary; display-window state arrives in F.
- Use signed commits through `/Users/dan/.codex/bin/codex-git`; stop on signing failure. Use synthetic or existing sanitised fixtures. Never copy private captures into the repository.

## Existing contracts and file map

`timelineSpans` memoises `filteredTimelineTiming`; the latter currently chooses `timelineTierFor(m.log)`. `timelineTierFor` duplicates the model's default rule. `timelineState` currently contains only lane/span ordinals. `selectedTimelineIdentity` and `restoreTimelineIdentity` already map positioned selections to original per-tier indices through `selectedResources`. The restore function currently replaces the entire timeline state: it must assign cursor fields individually once the state contains more than a cursor.

`invalidateRows` clears resource projection, spans/timing, lanes, lane labels and wall-clock caches. It packs/clamps timeline lanes only while the timeline is visible; preserve that boundary. `laneOrder` and `detailPaneNatural` are currently calculated once from the preferred tier in `New`, so both need to follow explicit tier changes. `detailNaturalWidth` must continue measuring RPC call details even while the UI timeline is selected, because the calls view still exists.

- Create `internal/tui/timeline_tier.go`: active/default tier resolution, switching, identity reconciliation and presentation refresh.
- Modify `internal/tui/timeline.go`: timeline state, filtered tier selection, cursor-memory updates, axis gutter and obsolete default-only comments.
- Modify `internal/tui/model.go`: presentation cache discriminator, initialisation/invalidation and key routing.
- Modify `internal/tui/history.go`: value-owned tier history and identity restoration without replacing the whole timeline state.
- Modify `internal/tui/layout.go`: tier-aware detail measurements, switch hint and unavailable-tier status.
- Modify `internal/tui/help.go` and `README.md`: explain `t`, independent clocks and whole-second UI timing.
- Create `internal/tui/timeline_tier_test.go`: real mixed-capture journeys and tier-specific rendering assertions. Extend relevant existing timeline/history/layout/help tests rather than duplicating their entire journeys.
- Create `testdata/timing-tiers.log`: the sanitised mixed-clock fixture below, shared by tests and terminal verification.

## State and transition design

Keep `lane` and `span` as the active derived cursor so the existing renderer and movement functions remain small. Add value-owned state:

```go
type timelineState struct {
	lane, span int
	tier       timelineTier // tierNone means use the model's default preference
	selections [3]selectionIdentity // indexed by tierNone, tierRPC, tierUI
	notice     string
}
```

The saved selections contain original indices and the existing `"rpc"`/`"ui"` kind. They contain no slices, maps or pointers, so copying `timelineState` into a navigation frame owns both tier selections. There is no second persistent lane cursor per tier: rebuild it from identity against current lanes when that tier becomes active.

`rememberTimelineSelection()` stores `selectedTimelineIdentity()` for the active tier. Call it after actual cursor movement, after restoring an identity, when capturing timeline navigation, and before leaving the timeline through `changeView`. Do not have the low-level clamp overwrite a remembered identity before reconciliation has attempted restoration.

`reconcileTimelineSelection()` tries the saved identity against the active tier's current filtered/positioned observations. If it is absent, set lane/span to zero, clamp, then remember the first displayed observation (deterministic lane order, then lane span order). An empty/unpositioned selection remembers no selected observation. Inactive tier identities are reconciled lazily on activation, so filtering a non-timeline view never packs its lanes. Clearing a filter after the active selection disappeared selects the first available observation; it does not resurrect an invalid cursor.

`switchTimelineTier()` preserves the outgoing identity and toggles only when both whole-capture tiers exist. A single tier stays selected and reports which other tier is unavailable. With neither tier, preserve `tierNone` and explain the absence. Successful switching clears the notice, sets the explicit tier, and uses `invalidateRows()` to rebuild all relevant derivatives before identity reconciliation. Switching is not a navigation-history push; an existing parent frame must remain available to Esc.

## Task 1: Tier selection, identity ownership and cache coherence

**Files:** `timeline_tier.go`, `timeline.go`, `model.go`, `history.go`, `layout.go`, `timeline_tier_test.go`; focused updates to existing timeline/history/layout tests.

**Interfaces:** Consume `model.PreferredTiming(*model.Log) (span.Fidelity, bool)`, `model.SelectTiming([]span.Span) model.TimingSelection`, `selectedTimelineIdentity() selectionIdentity`, `restoreTimelineIdentity(selectionIdentity) bool` and `invalidateRows()`. Produce methods `activeTimelineTier() timelineTier`, `switchTimelineTier()`, `rememberTimelineSelection()`, `reconcileTimelineSelection()` and `refreshTimelinePresentation()`.

- [x] **Step 1: Add failing tier and identity tests.**

Start with the existing real `loadedMixedPositionLog(t)` fixture, which includes admitted but unpositioned RPC evidence and a UI operation:

```go
func TestTimelineTierSwitchSelectsIndependentClock(t *testing.T) {
	m := update(t, New(loadedMixedPositionLog(t), "mixed.log"),
		tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if tier, _ := m.timelineSpans(); tier != tierRPC {
		t.Fatal("mixed capture must default to RPC")
	}
	m.switchTimelineTier()
	if tier, _ := m.timelineSpans(); tier != tierUI {
		t.Fatal("explicit switch did not select UI")
	}
	if id := m.selectedTimelineIdentity(); id.kind != "ui" || id.index != 0 {
		t.Fatalf("UI source identity = %#v", id)
	}
	m.switchTimelineTier()
	if id := m.selectedTimelineIdentity(); id.kind != "rpc" || id.index != 0 {
		t.Fatalf("RPC source identity = %#v", id)
	}
}
```

For multi-selection journeys, create `testdata/timing-tiers.log` with these literal records and load it using `testLog(t, "timing-tiers.log")`:

```text
2026-09-11T00:00:00.000Z [TRACE] terraform: origin
2025-09-11T00:00:00.010Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=10
2026-09-11T00:00:01.000Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=100
2026-09-11T00:00:02.000Z [TRACE] provider.azurerm: Received downstream response: tf_rpc=PlanResourceChange tf_req_duration_ms=200
{"@level":"info","@timestamp":"2026-09-11T01:00:03Z","type":"apply_complete","hook":{"action":"read","elapsed_seconds":1,"resource":{"addr":"google_compute_instance.first","resource_type":"google_compute_instance","implied_provider":"google"}}}
{"@level":"info","@timestamp":"2026-09-11T01:00:08Z","type":"apply_complete","hook":{"action":"read","elapsed_seconds":2,"resource":{"addr":"module.long_module_path_for_timing_details.aws_instance.second","resource_type":"aws_instance","implied_provider":"aws"}}}
```

This gives RPC original index 0 unavailable for positioning, selectable RPC indices 1/2 and UI indices 0/1 on a separate clock. Confirm fixture admission in the tests before driving keys. Exercise real `model.Load` and the existing `update` helper. Assert distinct original indices and source entries, not just lane ordinals. Cover:

| Journey | Expected result |
| --- | --- |
| Select a later RPC observation, switch, select a later UI observation, switch twice | Each tier returns to its own original observation |
| Apply a real facet filter that removes an earlier observation but retains the selected one | Selection follows original identity despite reindexing |
| Remove the selected observation while other observations remain | Select first displayed observation deterministically |
| Filter every observation from the explicit tier | Retain that tier and show no match; never switch clocks |
| Admitted observations all lack positions, with a positioned other tier | Default/explicit tier remains available, duration evidence retained, no selected bar |
| Filter while a different view is active, then open/switch timeline | Reconcile saved identity only on activation; no premature lane packing |
| UI Enter, child filtering and resize, Esc, then RPC switch | Exact source identity, explicit tier and both saved selections restored |
| RPC-only, UI-only and empty log | No manufactured empty tier; explanatory notice |

Warm spans, lanes, labels and wall-clock caches before switching. Use different providers, clocks and long UI addresses to prove returned labels, colours, notes, detail width and jump target belong to the new tier. Do not use cache booleans as the only evidence. Existing `TestAViewThatDrawsNoTimelineDoesNotPackItsLanes` must continue to pass.

- [x] **Step 2: Run focused tests and record RED.**

Run `go test ./internal/tui -run 'TestTimelineTier' -count=1`. Initially the switching interface is absent; after adding scaffolding, assertions must expose the missing behaviour. Keep fixtures independent of the implementation's index mapping.

- [x] **Step 3: Implement tier and identity transitions.**

Move `timelineTierFor` to `timeline_tier.go` and delegate the default decision:

```go
func timelineTierFor(l *model.Log) timelineTier {
	fidelity, ok := model.PreferredTiming(l)
	if !ok {
		return tierNone
	}
	if fidelity == span.FidelityUIReported {
		return tierUI
	}
	return tierRPC
}

func (m *Model) activeTimelineTier() timelineTier {
	if m.timeline.tier != tierNone {
		return m.timeline.tier
	}
	return timelineTierFor(m.log)
}

func (m *Model) rememberTimelineSelection() {
	m.timeline.selections[m.activeTimelineTier()] = m.selectedTimelineIdentity()
}

func (m *Model) reconcileTimelineSelection() {
	id := m.timeline.selections[m.activeTimelineTier()]
	if !m.restoreTimelineIdentity(id) {
		m.timeline.lane, m.timeline.span = 0, 0
		m.clampTimelineSelection()
	}
	m.rememberTimelineSelection()
}
```

In `restoreTimelineIdentity`, reject identities whose kind is neither `rpc` nor `ui` before looking up their index. On success assign `m.timeline.lane` and `m.timeline.span` separately, preserving the tier, both saved selections and notice. Ensure the final `restoreIdentity` path remembers the restored timeline selection. `reconcileTimelineSelection` may reuse this helper but neither it nor `rememberTimelineSelection` may call `invalidateRows`; avoid recursion.

Implement switching using whole-capture `len(m.log.RPCSpans)` and `len(m.log.UISpans)` only. For two available tiers, remember the outgoing identity, assign the opposite tier and invalidate. For one/zero tiers, leave the selection unchanged and set respectively `"RPC timing only; UI timing unavailable"`, `"UI timing only; RPC timing unavailable"` or `"No RPC or UI timing observations"`.

Change `filteredTimelineTiming` to switch on `m.activeTimelineTier()`. At the end of `invalidateRows`, replace timeline-only clamping with reconciliation. Persist the final cursor after both movement methods, including the nearest-time lane reseek; remember navigation snapshots before copying their value-owned state. Preserve modal and non-timeline packing boundaries.

- [x] **Step 4: Make presentation measurements tier-aware.**

Change the existing signatures and every caller/test without compatibility wrappers:

```go
func laneOrderFor(l *model.Log, tier timelineTier) map[string]int
func detailNaturalWidth(l *model.Log, tier timelineTier) int
```

`laneOrderFor` continues to use all observations of the requested tier, never its filtered subset. `detailNaturalWidth` always measures RPC spans and existing rollups; when `tier == tierUI`, additionally measure UI span details with empty attribution. This preserves reachable call details in mixed captures and covers UI resource addresses when the UI timeline is selected.

Add `timelinePresentationTier timelineTier` and `timelinePresentationCached bool` to `Model`. In `refreshTimelinePresentation`, return immediately when the cached tier equals `activeTimelineTier`; otherwise compute the palette and natural detail width with the new signatures and update both discriminator fields. Call it from `New` instead of the two default-only measurements, and from `invalidateRows` before selection clamping. This refreshes after switching/history restore without rescanning every span on a filter change or redraw. `timelineTimingCache` continues to hold the active tier's timing selection; analysis/notes derive from that tier's current spans and window.

Update inaccurate comments claiming the tier cannot change. Do not refactor unrelated layout or analysis code.

- [x] **Step 5: Verify and commit the core.**

Run focused tests, `go test ./internal/tui`, `go test ./...`, `golangci-lint run`, `gofmt` on changed Go files and `codex-git diff --check`. Review any intentional fixture/golden changes before accepting them. Commit explicit paths with a signed subject `Preserve independent timing-tier selections`. Request task review and separate test cleanup before Task 2.

## Task 2: Keyboard control, visible tier labels and end-to-end verification

**Files:** `model.go`, `layout.go`, `timeline.go`, `help.go`, `README.md`, `timeline_tier_test.go` and relevant existing help/layout/timeline tests and goldens.

**Interfaces:** Consume Task 1's `switchTimelineTier`, `activeTimelineTier`, `timeline.notice`, and tier-aware presentation. Produce `timelineAxisGutter(tier timelineTier, hidden, width int) string` for a tier-labelled axis gutter that preserves lane truncation evidence.

- [x] **Step 1: Add failing real key/render journeys.**

Route the Task 1 mixed-capture journey through the actual key dispatcher:

```go
m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
if tier, _ := m.timelineSpans(); tier != tierUI {
	t.Fatal("t did not switch the focused timeline")
}
if !strings.Contains(unstyled(m.timelineTitle()), "ui, whole seconds") {
	t.Fatal("UI timing qualification missing")
}
```

Test `t` with the list focused, with facets/detail focused, with a narrow facet overlay, and within raw/response/facet/source-line inputs, help and quality panels. Only the focused timeline list switches; modal text handlers retain their existing semantics. Cover singleton/empty status dismissal on the next key, no interference with quit, and no extra history frame on switching. Verify the footer advertises `t tier` only where it works.

At widths 160, 100 and 60, assert the title and axis identify the active tier, the UI title retains whole-second qualification, the lane-cut count survives limited height, and output stays within the terminal width. Include a UI-only provider colour and long UI address in a mixed capture so old default-tier caches cannot pass. Keep direct `timeAxis` numeric-label tests unchanged; update rendered-axis expectations for the new gutter.

- [x] **Step 2: Record RED, then wire key and status behaviour.**

Run `go test ./internal/tui -run 'TestTimelineTier' -count=1`. Add this case to the main key switch, after the existing modal handlers:

```go
case "t":
	if m.view == ViewTimeline && m.pane == PaneList {
		m.switchTimelineTier()
	}
```

Clear `m.timeline.notice` at the same start-of-key stage as `m.blockedJump`. In `footer`, after modal/footer overrides and before ordinary key hints, display a nonempty notice only for the visible timeline list, with clipped notice on the first line and the existing quit hint on the second. Keep the footer at two rows. Add `t tier` near the start of `actionKeys` only for that same focus/visibility condition, preserving quit/back hints at narrow widths.

- [x] **Step 3: Label the axis and document the control.**

Keep numeric `timeAxis` and its column alignment unchanged. Replace only its existing label gutter composition with:

```go
gutter := timelineAxisGutter(tier, len(lanes)-visible, labelW) + " "
lines = append(lines, clipWidth(gutter+timeAxis(wallClock, barW), w))
```

The helper uses `rpc`/`ui`, adds `laneCutMark(hidden)` after it when needed, and pads to exactly `width`. If the combined label does not fit, retain the lane-cut mark with the existing `clipValueEnd` treatment; the pane title still identifies the tier. Without a cut mark, clip the tier label normally. Never spend an additional lane/notes row for this label, and never move the numeric zero relative to the bars.

Add a help row: `t` — `switch RPC/UI timing with the timeline list focused`. README wording: “With the timeline focused, `t` switches between available RPC and UI timing. Each tier keeps its own selected observation. UI timing retains whole-second resolution; filtering does not switch clocks.” State that a single available tier stays selected and reports the unavailable alternative.

- [x] **Step 4: Verify the complete behaviour and commit.**

Run focused/TUI tests, full `go test ./...`, `go test -race -count=1 ./...`, `golangci-lint run`, `go build ./...`, formatting and diff checks. If snapshots change, regenerate with `go test ./internal/tui -update`, inspect the raw styling diff and `scripts/read-golden.sh` output, then rerun affected tests. Preserve existing profile/JSON tests as the boundary against changing their default preference.

Use a real PTY and sanitised mixed capture to select a later RPC observation, switch/select UI, drill to its source, return, resize and switch back. Repeat at 100 and 60 columns and with `NO_COLOR`; inspect actual terminal output, not just stripped strings. Include single-tier, no-timing and filtered-empty captures. Record source identities and visible tier labels independently. Commit explicit paths with signed subject `Expose timeline timing-tier controls`; request task review and separate test cleanup.

## Final review and handoff

Review the whole branch against boundary C, including lazy reconciliation while other views are active, history value ownership, cache refresh on return, modal routing, unavailable-position evidence and unchanged CLI/JSON preference. Fix verified findings and re-review the fix range. Record final validation and review outcomes in this plan. Keep the branch local until Dan requests integration; D and F remain separate boundaries.

## Planning self-review

Boundary C's default preference, whole-capture availability, independent selections, deterministic filter reconciliation, cache invalidation, history ownership and original-source drill-down are assigned to Task 1. Task 2 covers key scope, unavailable-tier status, tier/axis labels, UI qualification, help and terminal verification. The model preference remains authoritative for CLI/JSON consumers. Per-tier display windows are explicitly reserved for F. The interface names above are consistent across both tasks; there are no unresolved scope choices or placeholder steps.

## Delivery record

Implemented on `feature/timing-tier-selection` from `323a114`. Core selection and identity handling landed in `20bcc35`, with transition coverage and test diagnostics in `e6c3bac` and `bfb4f48`. Keyboard controls, axis labels, notices and documentation landed in `67b4749`; final test/comment clarifications landed in `73a61f3`. All branch commits have verified SSH signatures.

Both task reviews and the final whole-branch review approved the implementation. Missing transition coverage was added during core review. Real terminal verification exposed a workbench status composition bug that hid unavailable-tier notices; a full-view regression test reproduced it before the fix. Final scoped review confirmed both remaining test/comment findings resolved, with no open findings. Separate test-cleanup passes retained the meaningful cases and removed none.

Verification: focused and full TUI tests, `go test ./...`, `go test -race -count=1 ./...`, `golangci-lint run` (0 issues), `go build ./...`, formatting and diff checks passed. The full suite and build were checked again at `73a61f3`. Updated help and timeline goldens were inspected as rendered output and raw ANSI diffs.

Seven real PTY sessions passed 78 checkpoints: mixed RPC/UI at 100 and 60 columns with colour and `NO_COLOR`, plus RPC-only, UI-only and no-timing captures. Journeys verified independent selections, original source entries, raw-log return, resize, filtered-empty tiers, unavailable-tier notices and dismissal. ANSI attributes and selected-lane markers were inspected. The reproducible local harness is `/private/tmp/tfli-timing-tier-pty.py`; terminal evidence is in `/private/tmp/tfli-timing-tier-terminal`. These temporary artefacts use only sanitised fixtures.

Review decision: retain the independently measured help-rendering width test. Its output bound remains observable behaviour; if that judgement is wrong, the cost is one redundant test. No architectural or compatibility rulings were required.

Boundary C is complete locally. Profile/JSON preference is unchanged. Integration remains with Dan; boundaries D and F remain separate work.
