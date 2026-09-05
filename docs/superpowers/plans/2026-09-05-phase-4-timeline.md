# Phase 4: Timeline (view 5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add view 5, a swimlane timeline that shows whether a slow plan was work or waiting, with a within-lane cursor that names any bar on screen.

**Architecture:** The timeline is the raw log's shape, not the table views': it renders from its own state rather than from `rows()`, because a lane is neither a rollup nor a single span and forcing it into `row` would break the invariant `renderDetail` dispatches on. A new `internal/tui/timeline.go` owns `timelineState` (selected lane, selected span within it), lane geometry and bar rendering. Lane packing and stall detection stay pure in `internal/model/lanes.go`, where `PackLanes` and `PeakConcurrency` already live. The detail pane, the Enter handler and the footer's open hint all reach the selected span through one new accessor, so they cannot disagree about what is selected.

**Tech Stack:** Go 1.25, bubbletea v1.3.10, lipgloss, `x/ansi`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-03-tf-log-inspector-design.md` — "Timeline (view 5)", "Views", "Width degradation", "The observer effect".

## Global Constraints

- **`internal/model`, `internal/span`, `internal/logfmt`, `internal/diagnose`, `internal/profile` stay terminal-unaware and dependency-free.** `internal/tui` is the only package permitted a third-party import. Task 2 adds a pure function to `internal/model`; it renders nothing and imports nothing new.
- **`--profile` output must not change.** Verify with `md5` of `tfli --profile` over a fixture before and after; on the real capture the expected digest is `eb10230cefb53cd63a978f3aa5759f73`, which is Dan's to check, not an agent's.
- **TDD.** Failing test first, run it, minimal implementation, run it, commit.
- **No fixture may contain content from a real capture.** Every new fixture carries a `# SYNTHESISED` header or a named public source URL. Never copy lines out of a log under `/Users/dan` that is not already committed.
- **Half-open intervals.** A span occupies `[StartMs, EndMs)`. `PackLanes`' lane count can legitimately exceed `PeakConcurrency`'s peak; that is inherent, not a discrepancy to surface.
- **Never mix fidelities on one timeline.** `PackLanes` returns `ErrMixedTimelines` for a mixed slice. The timeline draws exactly one tier.
- **Australian/British English** in prose and comments.
- **Commits are signed** via `/Users/dan/.claude/bin/claude-git`.
- **Every duration is a duration under logging.** The existing `loggingCaveat` block stays on screen in this view too.

## File Structure

- `internal/model/lanes.go` — **modify.** Add `Stall` and `Stalls`, beside `PackLanes` and `PeakConcurrency` that share their sweep.
- `internal/model/lanes_test.go` — **modify.** Tests for `Stalls`.
- `internal/tui/timeline.go` — **create.** `timelineState`, tier selection, lane geometry, bar rendering, stall annotation, cursor movement.
- `internal/tui/timeline_test.go` — **create.**
- `internal/tui/model.go` — **modify.** `ViewTimeline` in the enum and `views` table; `timeline timelineState` on `Model`; key routing.
- `internal/tui/layout.go` — **modify.** Two-line footer; `renderCentre` dispatch; `selectedDetail` for the timeline.
- `internal/tui/views.go` — **modify.** `rows()` case returning nil, as `ViewRawLog` does.
- `internal/tui/rawlog.go` — **modify.** `jumpToSpan` generalised to take the span slice it is jumping from.
- `internal/tui/testdata/golden/*` — **modify/create.** Regenerated for the two-line footer; new timeline goldens.
- `testdata/*.log` — **create.** One fixture with overlapping RPC spans and a deliberate idle window.

---

### Task 1: Two-line footer

The footer becomes two lines: view keys above, action keys below. This removes the all-or-nothing cliff where the view-key group vanished below 95 columns, and it makes room for `5 timeline` without pushing the composed line to ~107 columns. Every golden changes; that is expected and each one must be read before committing.

**Files:**
- Modify: `internal/tui/layout.go` — `frameFixedLines`, `footer`, `keyHints`
- Test: `internal/tui/layout_test.go`
- Modify: `internal/tui/testdata/golden/layout-70.txt`, `layout-100.txt`, `layout-160.txt`, `layout-100-providers.txt`

**Interfaces:**
- Consumes: `viewKeyHints(v View) string`, `(*Model).actionKeys() string`, both unchanged.
- Produces: `(*Model).footer(w int) string` now returns a string containing exactly one `"\n"` in the ordinary case, and no newline in the search/blocked-jump cases. `frameFixedLines` becomes 5.

- [ ] **Step 1: Write the failing test**

```go
func TestFooterKeepsViewKeysAtEveryWidth(t *testing.T) {
	m := update(t, New(testLog(t, "two-providers.log"), "x.log"), tea.WindowSizeMsg{Width: 100, Height: 40})
	for _, w := range []int{60, 70, 100, 160} {
		got := m.footer(w)
		lines := strings.Split(got, "\n")
		if len(lines) != 2 {
			t.Fatalf("footer(%d) = %d lines, want 2:\n%s", w, len(lines), got)
		}
		// The view-key line names every view except the one showing.
		if !strings.Contains(lines[0], "2 types") {
			t.Errorf("footer(%d) view line lost its hints: %q", w, lines[0])
		}
		if !strings.Contains(lines[1], "q quit") {
			t.Errorf("footer(%d) action line lost q quit: %q", w, lines[1])
		}
	}
}
```

The helpers are the existing `New(testLog(t, "<file>.log"), "<name>")` and `update(t, m, msg)` from `layout_test.go` — read them rather than assuming their signatures. There is no `newTestModel`.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/tui/ -run TestFooterKeepsViewKeysAtEveryWidth -v`
Expected: FAIL — one line, not two.

- [ ] **Step 3: Implement**

`footer` composes the two groups. The blocked-jump and search cases stay one line — they are a message, not a hint line — so `View` must not assume two.

```go
func (m *Model) footer(w int) string {
	if m.blockedJump {
		return jumpBlockedNote
	}
	if m.view == ViewRawLog {
		switch {
		case m.raw.searching:
			return "/" + m.raw.query
		case m.raw.notFound:
			return "/" + m.raw.lastQuery + "  pattern not found"
		}
	}
	return m.keyHints(w)
}

// keyHints is the footer's two hint lines: which number keys switch views,
// then which keys act on what is on screen.
//
// They are two lines rather than one because a single composed line ran to
// 107 display columns once the timeline joined it, and the line is clipped
// from its END -- so the tail it lost was "q quit", the one key a user must
// never lose sight of. Splitting them lets both groups keep their full names
// at every width this interface renders at, and costs one line of pane
// height. Each line is still clipped independently, because a 60-column
// terminal cannot show 62 columns of action keys however they are arranged.
func (m *Model) keyHints(w int) string {
	return clipWidth(viewKeyHints(m.view), w) + "\n" + clipWidth(m.actionKeys(), w)
}
```

Update `frameFixedLines` from 4 to 5 and rewrite its comment to say what the five lines are: the header, the blank line beneath it, the blank line above the footer, and the footer's **two** lines.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/tui/`
Expected: the new test passes; the golden tests FAIL with a one-line shift.

- [ ] **Step 5: Regenerate and READ the goldens**

Run: `go test ./internal/tui/ -update`
Then read every changed golden with `git diff -- internal/tui/testdata/golden`. Confirm, for each width: two hint lines are present, the pane row lost exactly one line, and nothing else moved. A golden that changed in any other way means the implementation is wrong — do not commit it.

- [ ] **Step 6: Commit**

```bash
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector add internal/tui/layout.go internal/tui/layout_test.go internal/tui/testdata/golden
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -F /tmp/commit-msg.md
```

---

### Task 2: `model.Stalls` — idle windows

The stall annotation needs windows where concurrency collapsed. This is a pure sweep over the same events `PeakConcurrency` already builds, so it belongs beside it.

**Files:**
- Modify: `internal/model/lanes.go`
- Test: `internal/model/lanes_test.go`

**Interfaces:**
- Produces:

```go
// Stall is a window during which fewer lanes were busy than the timeline
// has, named by the span that was still running when the others were not.
type Stall struct {
	StartMs, EndMs uint32
	Idle           int // lanes with nothing to do in this window
	Blocking       int // index into the spans slice: the longest span running throughout
}

func Stalls(spans []span.Span, lanes int, minMs uint32) ([]Stall, error)
```

- `minMs` suppresses noise: windows shorter than it are not reported. The caller passes a threshold; this function invents none.
- Returns `ErrMixedTimelines` for a mixed slice, exactly as its neighbours do.
- Windows are merged: two adjacent sweep segments with the same idle count and the same blocking span are one stall, not two.

- [ ] **Step 1: Write the failing tests**

```go
func TestStallsFindsTheWindowWhereOnlyOneSpanRan(t *testing.T) {
	// Three spans. Two finish early; one runs long past them, so from
	// 100ms to 500ms two of the three lanes are idle.
	spans := []span.Span{
		{StartMs: 0, EndMs: 500, DurationMs: 500, RPC: "Configure"},
		{StartMs: 0, EndMs: 100, DurationMs: 100, RPC: "ReadResource"},
		{StartMs: 0, EndMs: 80, DurationMs: 80, RPC: "ReadResource"},
	}
	got, err := Stalls(spans, 3, 50)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	want := []Stall{{StartMs: 100, EndMs: 500, Idle: 2, Blocking: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Stalls = %+v, want %+v", got, want)
	}
}

func TestStallsIgnoresWindowsBelowTheThreshold(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, DurationMs: 100},
		{StartMs: 0, EndMs: 90, DurationMs: 90},
	}
	got, err := Stalls(spans, 2, 50)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stalls = %+v, want none: the 10ms window is below the 50ms threshold", got)
	}
}

func TestStallsRefusesMixedTimelines(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 100, Fidelity: span.FidelityReported},
		{StartMs: 0, EndMs: 100, Fidelity: span.FidelityUIReported},
	}
	if _, err := Stalls(spans, 2, 0); !errors.Is(err, ErrMixedTimelines) {
		t.Errorf("Stalls over mixed fidelities = %v, want ErrMixedTimelines", err)
	}
}

func TestStallsReportsNoStallWhenEverySpanRunsThroughout(t *testing.T) {
	spans := []span.Span{
		{StartMs: 0, EndMs: 500, DurationMs: 500},
		{StartMs: 0, EndMs: 500, DurationMs: 500},
	}
	got, err := Stalls(spans, 2, 0)
	if err != nil {
		t.Fatalf("Stalls: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stalls = %+v, want none: both lanes are busy for the whole window", got)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/model/ -run TestStalls -v`
Expected: FAIL — `undefined: Stalls`.

- [ ] **Step 3: Implement**

Sweep the same `event{ms, delta}` list `PeakConcurrency` builds, sorted by ms with ends before starts at equal ms (half-open intervals). Between consecutive event times, the running count is constant; `Idle = lanes - running`. Emit a window when `Idle > 0 && running > 0` — a window where *nothing* runs is not a stall, it is the gap between two phases, and calling it a stall would blame a span that had already finished. `Blocking` is the longest-running span (by `DurationMs`) live throughout the window. Merge adjacent windows with equal `Idle` and `Blocking`, then drop those shorter than `minMs`. Drop *after* merging, so two 30ms halves of one 60ms stall are reported.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/model/`
Expected: PASS, whole package.

- [ ] **Step 5: Commit**

---

### Task 3: The tier the timeline draws

`PackLanes` refuses a mixed slice, so the timeline draws exactly one tier: RPC spans where the log has them, otherwise UI-hook spans. The pane title names which, because a UI-tier timeline is whole-second resolution and a reader must not take it for RPC precision.

**Files:**
- Create: `internal/tui/timeline.go`
- Test: `internal/tui/timeline_test.go`
- Create: `testdata/timeline.log`

**Interfaces:**
- Produces:

```go
// timelineTier is which of the log's two span sets the timeline draws.
type timelineTier uint8

const (
	tierNone timelineTier = iota
	tierRPC
	tierUI
)

// timelineSpans reports which tier the timeline draws under the active
// filter, and the filtered spans of that tier.
func (m *Model) timelineSpans() (timelineTier, []span.Span)
```

- Falls back to the UI tier only when the RPC tier is empty **before** filtering. A filter that hides every RPC span must not silently switch tiers under the user — that would redraw the whole view on a facet toggle and change what the numbers mean without saying so.

- [ ] **Step 1: Write the fixture**

`testdata/timeline.log`, with a `# SYNTHESISED` header naming what it is for: overlapping RPC spans across two providers, plus a deliberate window where one long span runs alone. Model its line shape on `testdata/two-providers.log` — read that file and match its format exactly rather than inventing hclog syntax.

- [ ] **Step 2: Write the failing tests**

```go
func TestTimelineDrawsTheRPCTierWhenTheLogHasOne(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	tier, spans := m.timelineSpans()
	if tier != tierRPC {
		t.Fatalf("tier = %v, want tierRPC", tier)
	}
	if len(spans) == 0 {
		t.Error("no spans: the RPC tier was chosen but nothing was returned")
	}
}

func TestTimelineFallsBackToTheUITierWhenThereAreNoRPCSpans(t *testing.T) {
	// structured-ui.log carries the UI-hook tier and no RPC spans at all.
	m := New(testLog(t, "structured-ui.log"), "x.log")
	tier, spans := m.timelineSpans()
	if tier != tierUI {
		t.Fatalf("tier = %v, want tierUI", tier)
	}
	for _, s := range spans {
		if s.Fidelity != span.FidelityUIReported {
			t.Fatalf("span %+v is not UI fidelity; PackLanes will refuse this slice", s)
		}
	}
}

func TestFilteringOutEveryRPCSpanDoesNotSwitchTiers(t *testing.T) {
	m := New(testLog(t, "timeline.log"), "x.log")
	// Narrow the provider dimension to a value no span carries. The facet
	// maps are unexported and toggleSelectedFacetValue works off the facet
	// pane's cursor, so this test is in package tui and sets them directly,
	// as facets_test.go does.
	m.selectedFacets = map[string]map[string]bool{dimProvider: {"registry.terraform.io/hashicorp/nothing": true}}
	m.invalidateRows()
	tier, spans := m.timelineSpans()
	if tier != tierRPC {
		t.Errorf("tier = %v, want tierRPC: a filter must not change which tier is drawn", tier)
	}
	if len(spans) != 0 {
		t.Errorf("spans = %d, want 0: the filter matches nothing", len(spans))
	}
}
```

`testdata/structured-ui.log` is the UI-only fixture; `two-tier.log` carries both tiers, so it is not the one. Confirm `dimProvider` is the real constant name in `facets.go` before using it.

- [ ] **Step 3: Run and watch fail. Step 4: Implement. Step 5: Run. Step 6: Commit.**

---

### Task 4: Lane geometry and bars

Pure geometry, tested without a terminal: given spans, a lane packing and a pane width, produce the bar cells for each lane.

**Files:**
- Modify: `internal/tui/timeline.go`
- Test: `internal/tui/timeline_test.go`

**Interfaces:**
- Produces:

```go
// laneBar renders one lane's spans as a bar row barW columns wide covering
// [0, spanMs). Each span occupies the columns its interval maps to, with a
// minimum of one column so a short span is visible rather than rounded away.
func laneBar(spans []span.Span, lane model.Lane, spanMs uint32, barW int) string

// timeAxis renders the axis line beneath the lanes: 0s at the left, the
// total at the right.
func timeAxis(spanMs uint32, barW int) string
```

- The bar glyph is `█` (U+2588 FULL BLOCK). **Measured against unicodedata 16.0.0: it is East Asian class Ambiguous** — the same class as `│` (U+2502) and `─` (U+2500), which this package already renders and whose goldens are locked, so it costs one display column here exactly as they do. Do **not** cap bars with `▐`/`▌`: `▐` (U+2590) is Narrow while `▌` (U+258C) is Ambiguous, and mixing width classes inside one bar makes its rendered width depend on the class the terminal resolves Ambiguous to. One glyph for the bar body, nothing else. Measure with `lipgloss.Width`, never `len` or a rune count — counting runes as columns was a Critical defect in phase 3.
- A zero-duration span still gets one column: it is a real call that the log recorded, and rounding it to nothing makes the lane look emptier than it was.
- `spanMs` of 0 (every span zero-duration, or one span) must not divide by zero.

- [ ] **Step 1: Write the failing tests** — at minimum: a span covering the whole window fills every column; two spans in one lane leave the gap between them blank; a 1ms span in a 10s window still renders one column; `lipgloss.Width` of every produced row equals `barW`.

- [ ] **Step 2–6: fail, implement, pass, commit.**

---

### Task 5: Wire view 5 in

**Files:**
- Modify: `internal/tui/model.go`, `internal/tui/views.go`, `internal/tui/layout.go`

**Interfaces:**
- `ViewTimeline` is added to the `View` enum **between `ViewCalls` and `ViewRawLog`**, so the enum reads in key order. `viewCount` stays last and grows by itself.
- `views` gains `{key: "5", view: ViewTimeline, title: "TIMELINE", name: "timeline"}`. The title is overridden at render time to name the tier — `TIMELINE (rpc)` / `TIMELINE (ui, whole seconds)`.
- `rows()` gains a `ViewTimeline` case returning nil with a comment saying why, as `ViewRawLog` has: the timeline renders from `m.timeline`, and a lane is neither a rollup nor a single span, so forcing it into `row` would break the invariant `renderDetail` dispatches on.
- `renderCentre` dispatches to `m.renderTimeline(w, h-1)`.
- `Model` gains `timeline timelineState`.

- [ ] **Step 1: Write the failing test** — `TestEveryViewHasABinding` already exists and will fail the moment `ViewTimeline` joins the enum without a `views` entry. Run it first and confirm it catches the gap; that is this task's proof the enum sweep works.

- [ ] **Step 2: Add the enum value alone, run the suite, and record which tests fail.** Every failure is a sweep that works. A view added with no test failing means a sweep is missing and must be added before the view is wired up.

- [ ] **Step 3: Wire the binding, `rows()` case and `renderCentre` dispatch.**

- [ ] **Step 4: Empty state.** A log with neither tier renders one honest line in the centre pane, in the shape view 3's empty state is specified with: what is missing and what to capture to get it — `no timed spans in this log; RPC timings need TF_LOG_SDK_PROTO=TRACE and TF_LOG_PROVIDER=TRACE`. Not a blank pane.

- [ ] **Step 5: Run the suite. Step 6: Commit.**

---

### Task 6: The cursor — lanes and spans within a lane

`↑`/`↓` (and `k`/`j`) move between lanes; `←`/`→` (and `h`/`l`) step through the spans packed into the selected lane, in start order. The detail pane shows the selected span. Enter opens it in the raw log.

**Files:**
- Modify: `internal/tui/timeline.go`, `internal/tui/model.go`, `internal/tui/layout.go`, `internal/tui/rawlog.go`

**Interfaces:**
- Produces:

```go
// timelineState is the timeline's own selection: which lane the cursor is
// on, and which of that lane's spans is selected within it. It is kept as
// its own struct, as rawLogState is, so the view's concerns stay with the
// file that owns them.
type timelineState struct {
	lane int
	span int // index into the selected lane's Spans, not into the span slice
}

// selectedTimelineSpan resolves the timeline's cursor to one span and its
// index in the tier's span slice.
func (m *Model) selectedTimelineSpan() (idx int, ok bool)
```

- `jumpToSpan` is generalised to `func (m *Model) jumpToSpan(spans []span.Span, idx int)`, because the timeline may be drawing UI spans and the current signature indexes `m.log.RPCSpans` unconditionally. Update the two existing call sites. Keep the bounds check, the `Entry` revalidation and the blocked-jump behaviour exactly as they are — read the existing comments, they record why each exists.
- The footer's open hint (`actionKeys` → `selectedRowOpens`) must answer for the timeline too: in this view Enter opens the selected span, so the hint is shown. Route both the hint and the handler through one predicate so they cannot drift, the way `row.isCall` does for the table views.
- Both cursors clamp: changing lanes clamps the within-lane index to the new lane's length, and a filter change that shortens a lane must not leave the cursor past its end. Invalidate the timeline's selection wherever `invalidateRows` is called.

- [ ] **Step 1: Write the failing tests** — moving down past the last lane stays on the last lane; `→` past the last span in a lane stays put; changing lanes clamps the span index; a facet toggle that empties the timeline leaves `selectedTimelineSpan` returning `ok == false` rather than an out-of-range index; Enter from the timeline switches to `ViewRawLog` positioned at the selected span's entry.

- [ ] **Step 2–6: fail, implement, pass, commit.**

---

### Task 7: Stall annotation

Below the lanes, the windows `model.Stalls` found, in the spec's shape: `3 lanes idle 04:11:20–04:11:44 waiting on aws/1`.

**Files:**
- Modify: `internal/tui/timeline.go`
- Test: `internal/tui/timeline_test.go`

- Timestamps: spans carry milliseconds from a per-builder zero point, **not** wall-clock. The spec's example shows clock times; this interface has no clock to render, so annotate with offsets — `3 lanes idle 20.1s–44.3s waiting on azurerm ReadResource`. Do not invent a wall-clock time from a zero-based offset. If the log's first timestamp is available and trustworthy, rendering real times is a follow-up, not this task's licence to guess.
- The threshold passed as `minMs`: choose one, state it in a comment with the reasoning, and test it. A sensible starting point is 5% of the window or 1s, whichever is larger — but measure against the fixture rather than asserting it.
- At most the top few stalls by duration; the pane has finite height and a hundred 60ms stalls is noise.
- Where there are none, say so in one line rather than leaving blank space: silence and "no stalls" look identical otherwise.

- [ ] **Steps 1–6: test, fail, implement, pass, read, commit.**

---

### Task 8: Goldens, width degradation, and the parked residuals

**Files:**
- Modify: `internal/tui/layout_test.go`, `internal/tui/testdata/golden/*`
- Modify: `internal/tui/model.go` (`paneCount`)

- [ ] **Step 1: Timeline goldens at 70, 100 and 160 columns**, following the existing `TestLayoutDegradesByWidth` pattern. Read the existing goldens first and match their harness exactly. Confirm at 70 columns the detail pane is gone and the lanes take the full width; at 160 all three panes are present.

- [ ] **Step 2: Fix `paneCount`.** It is `PaneDetail + 1`, which a pane added after `PaneDetail` would slip past — the same latent flaw the `View` enum's sentinel fixed. Replace it with an iota sentinel at the end of the `Pane` enum. Prove it the way the view sentinel was proven: add a pane value to the enum, watch the sweep fail, remove it.

- [ ] **Step 3: Full verification.**

```bash
go build ./... && go vet ./... && gofmt -l . && go test -count=1 ./...
```

- [ ] **Step 4: Confirm `--profile` is byte-identical** over a committed fixture, before and after the branch.

- [ ] **Step 5: Fixture disclosure check.** Grep every file under `testdata/` for anything that could have come from a real capture. Each fixture is `# SYNTHESISED` or carries a public source URL.

- [ ] **Step 6: Commit.**

---

## Residuals deliberately NOT in this plan

- **The levels facet filters only the raw log**, which is documented in code but not on screen. It predates this phase and belongs with a facet-pane task, not a timeline one.
- **A UI-only resource type whose provider has no RPC spans is unreachable by a provider facet**, because the facet pane is built from the RPC tier. Same reason.
- **View 3 (resource addresses)** is phase 5 and conditional on address-attribution confidence. Key `3` stays unbound and unadvertised.
