# Phase 2: Model Layer and `--profile` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the spans phase 1 already extracts into answers — which resource type cost the most, what its RPCs cost, and whether the plan was slow because of work or waiting — exposed through a headless `tfli --profile <log>`.

**Architecture:** A new `internal/model` package holds a loaded log (whole-file `[]byte`, retained entry index, both span slices) and pure functions over it: rollups, a cross-tier join on resource type, facet filtering, and greedy lane packing. A new `internal/profile` package renders that model as text, mirroring how `internal/diagnose` renders its report. `cmd/tfli` gains one flag.

**Tech Stack:** Go 1.25, standard library only. No TUI, no bubbletea, no third-party anything.

**Spec:** `docs/superpowers/specs/2026-09-03-tf-log-inspector-design.md` — read the *Data model*, *Span extraction*, *Views and interaction* and *Phasing* sections, and the *Open questions*, which carry measurements from four real HCP captures that overturned several of this document's original assumptions.

## Global Constraints

- **Go 1.25.** `go.mod` says `go 1.25`; do not raise it, and do not use a stdlib symbol added after 1.25.
- **Zero third-party dependencies.** `go.mod` has no `require` block and must not gain one.
- **Australian / British English** in all prose, comments and user-facing output ("behaviour", "summarise", "recognised").
- **Metric units.** Durations in ms/s.
- **TDD, always.** Write the failing test, watch it fail, implement, watch it pass, commit. Never write implementation first.
- **Shell conventions.** Use `go -C /Users/dan/Code/tf-log-inspector test ./...` — never `cd X && go test`. Commit with `/Users/dan/.claude/bin/claude-git -C <repo> commit -F <file>`, never `-m` with multi-line text.
- **`gofmt` clean and `go vet` clean** before every commit.
- **Comments say what and why, never what changed.** No "previously this did X".

## Critical context an implementer will not guess

**1. `--profile` output is NOT safe to share. `--diagnose` output is.**
This is the single most important distinction in the codebase. `--diagnose` exists so Dan can send a report about a confidential work log back to this project; it masks addresses, strips values, and gates on recurrence. `--profile` is for Dan's own eyes on his own machine and prints **real resource addresses unmasked**, because a profiler that hides which resource was slow is useless. Do not reuse `internal/diagnose`'s masking here, and do not let `--profile` output leak into `--diagnose`. The `--profile` report must carry a header line saying it contains unmasked addresses.

**2. The two span builders sit on different clocks.**
`span.Span.StartMs`/`EndMs` are offsets from a zero point that is *per-builder*, not per-log. `ReportedBuilder` anchors to `Entry.TSms` (the hclog stream's first timestamped entry). `UIHookBuilder` tracks its own baseline from the first `@timestamp` it parses, because structured lines are never `Timestamped` and their `Entry.TSms` is always 0. Read the doc comment on `span.Span` in `internal/span/span.go` before writing anything that touches `StartMs` or `EndMs`.

The consequences are absolute:
- **Never sort, compare or pack spans of mixed `Fidelity` by `StartMs`/`EndMs`.** It produces a silently wrong ordering where every individual number looks plausible.
- **`DurationMs` is safe to compare across builders** — it is a duration, not a point in time.
- Lane packing therefore runs per-`Fidelity`, and Task 5 enforces that with a guard rather than a comment.

**3. UI-hook durations are quantised to whole seconds, ±1s each.**
Terraform rounds *both* endpoints to the nearest second before subtracting (`internal/command/views/hook_json.go`), so `elapsed_seconds` is always integral and each figure carries up to a second of error. Aggregates over many resources are sound; ranking two individual resources a second apart is not. Any `--profile` section that ranks individual UI-hook spans must say so, the way `internal/diagnose` already does in `SLOWEST RESOURCES`.

**4. What a real log actually contains.** From the confirmed dual-tier capture (`TF_LOG=DEBUG` + `TF_LOG_PROVIDER=TRACE` + `TF_LOG_SDK_PROTO=TRACE`), 30MB, 38,379 entries:

| | |
|---|---|
| RPC spans (`FidelityReported`) | 2,174, all correlated |
| UI-hook spans (`FidelityUIReported`) | 264, all `read` |
| slowest single RPC | 116,591 ms |
| summed RPC time | 1,503,968 ms |
| wall clock | 522.3 s |

So summed RPC time is ~2.9× wall clock — that ratio is what Task 5 exists to explain. The headline finding the join must surface: `azuread_service_principal` totalled 247.0 s across 17 reads, 47% of the plan.

**5. Existing API you build on.** These exist and are tested; do not modify them.

```go
// internal/logfmt
type Entry struct {
    Off         uint64 // byte offset of the entry's first line
    Len         uint32 // bytes covering all lines of the entry
    TSms        uint32 // ms since the first timestamped entry
    Level       Level
    Comp        uint16 // interned component; 0 means none
    Lines       uint16
    Timestamped bool
}
type Level uint8 // LevelUnknown, LevelTrace, LevelDebug, LevelInfo, LevelWarn, LevelError
func (l Level) String() string

type Sink interface {
    Entry(ord uint32, e Entry, msg string, f Fields)
}
func Scan(r io.Reader, comps *Interner, sinks ...Sink) (Stats, error)

type Interner struct{ /* ... */ }
func (i *Interner) Intern(s string) uint16
func (i *Interner) Lookup(id uint16) string

// internal/span
type Fidelity uint8 // FidelityReported, FidelityUIReported, FidelityPaired, FidelitySequential, FidelityInferred
type Span struct {
    Entry        uint32
    StartMs      uint32
    EndMs        uint32
    DurationMs   uint32
    StartClamped bool
    RPC          string // for UI-hook spans this holds the hook action: read/create/update
    Provider     string
    ResourceType string
    Address      string // populated only for FidelityUIReported
    Fidelity     Fidelity
}
func NewSniffer(comps *logfmt.Interner) *Sniffer
func (s *Sniffer) Report() Capabilities
func (b *ReportedBuilder) Spans() []Span
func (b *UIHookBuilder) Spans() []Span
```

Note `RPC` carries the *hook action* on UI-hook spans and the *RPC name* on reported spans. That is deliberate and is why the join keys on `ResourceType`, which means the same thing in both.

## File structure

| File | Responsibility |
|---|---|
| `internal/model/log.go` | `Log` — whole-file bytes, entry index, both span slices; `Load` |
| `internal/model/log_test.go` | |
| `internal/model/rollup.go` | `Bucket`, `RollupBy` — one generic rollup, keyed by a caller-supplied function |
| `internal/model/rollup_test.go` | |
| `internal/model/join.go` | `TypeRow`, `JoinByResourceType` — the cross-tier join |
| `internal/model/join_test.go` | |
| `internal/model/filter.go` | `Filter`, `Facet`, `FacetValue`, `FacetsForSpans` |
| `internal/model/filter_test.go` | |
| `internal/model/lanes.go` | `PackLanes`, `PeakConcurrency`, `IdleWindow` |
| `internal/model/lanes_test.go` | |
| `internal/profile/profile.go` | `Render` — the headless text report |
| `internal/profile/profile_test.go` | |
| `cmd/tfli/main.go` | add `--profile`; wire `model.Load` + `profile.Render` |

Six tasks, one per row group. Each ends with something independently testable.

---

### Task 1: `model.Log` — load a log once, keep its bytes and its entries

**Files:**
- Create: `internal/model/log.go`
- Create: `internal/model/log_test.go`

**Interfaces:**
- Consumes: `logfmt.Scan`, `logfmt.Entry`, `logfmt.Interner`, `logfmt.Stats`, `span.NewSniffer`, `span.ReportedBuilder`, `span.UIHookBuilder`, `span.Capabilities`
- Produces:
  ```go
  type Log struct {
      Data     []byte
      Entries  []logfmt.Entry
      Comps    *logfmt.Interner
      Stats    logfmt.Stats
      RPCSpans []span.Span
      UISpans  []span.Span
      Caps     span.Capabilities
  }
  func Load(path string) (*Log, error)
  func (l *Log) Bytes(e logfmt.Entry) []byte
  ```

- [ ] **Step 1: Write the failing test**

```go
package model

import (
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", name)
}

// Load must retain one Entry per logical entry Scan counted -- the index is
// what phase 3's raw-log view pages through, and a count that disagrees with
// Stats means entries were dropped or double-counted.
func TestLoadRetainsOneEntryPerLogicalEntry(t *testing.T) {
	l, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if uint64(len(l.Entries)) != l.Stats.Entries {
		t.Errorf("len(Entries) = %d, Stats.Entries = %d", len(l.Entries), l.Stats.Entries)
	}
	if len(l.Entries) == 0 {
		t.Fatal("no entries retained")
	}
}

// Bytes must return every line of a multi-line entry, not just its header.
// Entry.Off/Len cover all of an entry's physical lines, which is what makes
// "jump from a span to its log lines" a slice expression.
func TestBytesCoversAllLinesOfAnEntry(t *testing.T) {
	l, err := Load(fixture(t, "multiline-body.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var multi logfmt.Entry
	for _, e := range l.Entries {
		if e.Lines > 1 {
			multi = e
			break
		}
	}
	if multi.Lines <= 1 {
		t.Fatal("fixture has no multi-line entry")
	}
	got := string(l.Bytes(multi))
	if n := strings.Count(got, "\n"); n < int(multi.Lines)-1 {
		t.Errorf("Bytes returned %d newlines for a %d-line entry:\n%s", n, multi.Lines, got)
	}
}

func TestLoadBuildsBothSpanKinds(t *testing.T) {
	rpc, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rpc.RPCSpans) == 0 {
		t.Error("no RPC spans built from provider-rpc.log")
	}
	ui, err := Load(fixture(t, "structured-ui.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ui.UISpans) == 0 {
		t.Error("no UI-hook spans built from structured-ui.log")
	}
}

func TestLoadNamesTheFileOnError(t *testing.T) {
	_, err := Load("no-such-file.log")
	if err == nil {
		t.Fatal("Load returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -count=1`
Expected: FAIL to build — `undefined: Load`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package model holds a loaded log and pure functions over it. Nothing here
// renders or reads flags; internal/profile and cmd/tfli do that.
package model

import (
	"bytes"
	"fmt"
	"os"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Log is one loaded log file: its bytes, an index of its logical entries, and
// the spans both builders extracted from it.
//
// The whole file is held in Data. Four real HCP captures measure 17 to 37MB,
// so this costs less than the entry index would have under the 102 bytes/line
// estimate the design was originally sized against. Bytes() is the only
// accessor, so swapping in ReadAt or mmap later is contained to this file if a
// log ever turns up large enough to need it.
type Log struct {
	Data    []byte
	Entries []logfmt.Entry
	Comps   *logfmt.Interner
	Stats   logfmt.Stats

	// RPCSpans and UISpans are kept apart rather than concatenated. Their
	// StartMs/EndMs sit on different zero points -- see the doc comment on
	// span.Span -- so a single merged slice would invite exactly the
	// cross-timeline comparison that produces silently wrong orderings.
	RPCSpans []span.Span
	UISpans  []span.Span
	Caps     span.Capabilities
}

// entryIndex retains every entry Scan emits. It deliberately ignores msg and
// fields: the model indexes structure, and any consumer wanting an entry's
// text reads it back out of Data via Bytes, which keeps this slice free of
// pointers for the garbage collector to trace.
type entryIndex struct{ entries []logfmt.Entry }

func (x *entryIndex) Entry(_ uint32, e logfmt.Entry, _ string, _ logfmt.Fields) {
	x.entries = append(x.entries, e)
}

// Load reads a log file whole and extracts everything phase 2 needs in a
// single pass.
func Load(path string) (*Log, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	comps := &logfmt.Interner{}
	idx := &entryIndex{}
	sniffer := span.NewSniffer(comps)
	var rb span.ReportedBuilder
	var ub span.UIHookBuilder

	stats, err := logfmt.Scan(bytes.NewReader(data), comps, idx, sniffer, &rb, &ub)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}

	return &Log{
		Data:     data,
		Entries:  idx.entries,
		Comps:    comps,
		Stats:    stats,
		RPCSpans: rb.Spans(),
		UISpans:  ub.Spans(),
		Caps:     sniffer.Report(),
	}, nil
}

// Bytes returns every line of an entry, including its continuations.
func (l *Log) Bytes(e logfmt.Entry) []byte {
	return l.Data[e.Off : e.Off+uint64(e.Len)]
}
```

Add `"github.com/yesdevnull/tf-log-inspector/internal/logfmt"` to the test file's imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -count=1 && go -C /Users/dan/Code/tf-log-inspector vet ./...`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

Write the message to `/tmp/tfli-commit.md`, then:
```
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector add internal/model/
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -F /tmp/tfli-commit.md
```

---

### Task 2: `RollupBy` — one generic rollup, many keys

**Files:**
- Create: `internal/model/rollup.go`
- Create: `internal/model/rollup_test.go`

**Interfaces:**
- Consumes: `span.Span`
- Produces:
  ```go
  type Bucket struct {
      Key     string
      TotalMs uint64
      MaxMs   uint32
      Count   int
  }
  func RollupBy(spans []span.Span, key func(span.Span) string) []Bucket
  ```

- [ ] **Step 1: Write the failing test**

```go
package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func sp(resourceType, provider, rpc string, durationMs uint32) span.Span {
	return span.Span{
		ResourceType: resourceType,
		Provider:     provider,
		RPC:          rpc,
		DurationMs:   durationMs,
		Fidelity:     span.FidelityReported,
	}
}

func TestRollupBySumsCountsAndMaxima(t *testing.T) {
	spans := []span.Span{
		sp("azuread_service_principal", "azuread", "ReadDataSource", 1000),
		sp("azuread_service_principal", "azuread", "ReadDataSource", 3000),
		sp("azurerm_key_vault", "azurerm", "ReadDataSource", 500),
	}
	got := RollupBy(spans, func(s span.Span) string { return s.ResourceType })
	if len(got) != 2 {
		t.Fatalf("got %d buckets, want 2: %+v", len(got), got)
	}
	if got[0].Key != "azuread_service_principal" {
		t.Errorf("first bucket = %q, want the largest total first", got[0].Key)
	}
	if got[0].TotalMs != 4000 {
		t.Errorf("TotalMs = %d, want 4000", got[0].TotalMs)
	}
	if got[0].MaxMs != 3000 {
		t.Errorf("MaxMs = %d, want 3000", got[0].MaxMs)
	}
	if got[0].Count != 2 {
		t.Errorf("Count = %d, want 2", got[0].Count)
	}
}

// Ordering must be total, or two runs over the same log disagree and any
// golden-file test downstream flakes.
func TestRollupByBreaksTiesByKey(t *testing.T) {
	spans := []span.Span{
		sp("zebra", "p", "r", 1000),
		sp("alpha", "p", "r", 1000),
		sp("mike", "p", "r", 1000),
	}
	got := RollupBy(spans, func(s span.Span) string { return s.ResourceType })
	want := []string{"alpha", "mike", "zebra"}
	for i, w := range want {
		if got[i].Key != w {
			t.Errorf("bucket %d = %q, want %q (equal totals must sort by key)", i, got[i].Key, w)
		}
	}
}

// A span with no value for the chosen key still holds real time, and dropping
// it would make the totals quietly disagree with the summed span time.
func TestRollupByKeepsEmptyKeysUnderAnExplicitLabel(t *testing.T) {
	spans := []span.Span{
		sp("aws_instance", "aws", "r", 1000),
		sp("", "aws", "r", 250),
	}
	got := RollupBy(spans, func(s span.Span) string { return s.ResourceType })
	var total uint64
	var found bool
	for _, b := range got {
		total += b.TotalMs
		if b.Key == "(none)" {
			found = true
		}
	}
	if !found {
		t.Errorf("empty key not labelled (none): %+v", got)
	}
	if total != 1250 {
		t.Errorf("summed TotalMs = %d, want 1250 -- no span may be dropped", total)
	}
}

func TestRollupByEmptyInput(t *testing.T) {
	if got := RollupBy(nil, func(s span.Span) string { return s.RPC }); len(got) != 0 {
		t.Errorf("got %d buckets from no spans, want 0", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -run Rollup -count=1`
Expected: FAIL to build — `undefined: RollupBy`.

- [ ] **Step 3: Write minimal implementation**

```go
package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// noKey labels spans with no value for the rollup's key. They are kept rather
// than dropped so a rollup's totals always reconcile with the summed span
// time -- a missing row is far harder to notice than an explicit one.
const noKey = "(none)"

// Bucket is one row of a rollup.
type Bucket struct {
	Key     string
	TotalMs uint64 // widened: 2174 spans summing to 1.5e6 ms is comfortable in
	MaxMs   uint32 // uint32, but a long apply is not, and overflow here would
	Count   int    // silently invert the ordering
}

// RollupBy groups spans by a caller-supplied key and returns buckets ordered
// by total time descending, ties broken by key ascending so the ordering is
// total and two runs over one log cannot disagree.
//
// Callers must pass spans of a single Fidelity. DurationMs is comparable
// across builders, but mixing them conflates two different measurements of
// overlapping work: an RPC span times one call, a UI-hook span times a whole
// resource. JoinByResourceType is the supported way to show both.
func RollupBy(spans []span.Span, key func(span.Span) string) []Bucket {
	if len(spans) == 0 {
		return nil
	}
	byKey := make(map[string]*Bucket)
	for _, s := range spans {
		k := key(s)
		if k == "" {
			k = noKey
		}
		b := byKey[k]
		if b == nil {
			b = &Bucket{Key: k}
			byKey[k] = b
		}
		b.TotalMs += uint64(s.DurationMs)
		b.Count++
		if s.DurationMs > b.MaxMs {
			b.MaxMs = s.DurationMs
		}
	}
	out := make([]Bucket, 0, len(byKey))
	for _, b := range byKey {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMs != out[j].TotalMs {
			return out[i].TotalMs > out[j].TotalMs
		}
		return out[i].Key < out[j].Key
	})
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit** (same command shape as Task 1)

---

### Task 3: `JoinByResourceType` — the cross-tier join

This is phase 2's headline deliverable. Both builders populate `ResourceType`, so the two tiers join on it with no address attribution at all — which is what makes view 3 optional rather than necessary.

**Files:**
- Create: `internal/model/join.go`
- Create: `internal/model/join_test.go`

**Interfaces:**
- Consumes: `span.Span`, `RollupBy`
- Produces:
  ```go
  type TypeRow struct {
      ResourceType string
      UIResources  int
      UITotalMs    uint64
      UIMaxMs      uint32
      RPCCalls     int
      RPCTotalMs   uint64
      RPCMaxMs     uint32
  }
  func JoinByResourceType(rpcSpans, uiSpans []span.Span) []TypeRow
  ```

- [ ] **Step 1: Write the failing test**

```go
package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func uiSp(resourceType string, durationMs uint32) span.Span {
	return span.Span{
		ResourceType: resourceType,
		RPC:          "read",
		DurationMs:   durationMs,
		Fidelity:     span.FidelityUIReported,
	}
}

// The join is what answers "this type cost 247s overall, and here is what its
// RPCs cost" -- the two tiers measure overlapping work at different
// granularities, so both columns must survive on one row.
func TestJoinByResourceTypeCombinesBothTiers(t *testing.T) {
	rpc := []span.Span{
		sp("azuread_service_principal", "azuread", "ReadDataSource", 40000),
		sp("azuread_service_principal", "azuread", "ReadDataSource", 30000),
	}
	ui := []span.Span{
		uiSp("azuread_service_principal", 64000),
		uiSp("azuread_service_principal", 63000),
	}
	got := JoinByResourceType(rpc, ui)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.UIResources != 2 || r.UITotalMs != 127000 || r.UIMaxMs != 64000 {
		t.Errorf("UI columns wrong: %+v", r)
	}
	if r.RPCCalls != 2 || r.RPCTotalMs != 70000 || r.RPCMaxMs != 40000 {
		t.Errorf("RPC columns wrong: %+v", r)
	}
}

// A TF_LOG=TRACE capture has no terraform.ui stream at all, and a plain debug
// capture has no protocol lines. Both are real, measured captures, so a row
// present in only one tier must still appear with the other side zeroed.
func TestJoinByResourceTypeKeepsSingleTierRows(t *testing.T) {
	got := JoinByResourceType(
		[]span.Span{sp("aws_instance", "aws", "ApplyResourceChange", 5000)},
		[]span.Span{uiSp("local_file", 1000)},
	)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	byType := map[string]TypeRow{}
	for _, r := range got {
		byType[r.ResourceType] = r
	}
	if r := byType["aws_instance"]; r.RPCTotalMs != 5000 || r.UIResources != 0 {
		t.Errorf("RPC-only row wrong: %+v", r)
	}
	if r := byType["local_file"]; r.UITotalMs != 1000 || r.RPCCalls != 0 {
		t.Errorf("UI-only row wrong: %+v", r)
	}
}

// Ordering leads with the UI-hook total because that is the closest thing to
// "how long did this type actually take", and falls back to RPC time so a
// capture with no terraform.ui stream still ranks sensibly rather than
// collapsing to an arbitrary order.
func TestJoinByResourceTypeOrdersByUIThenRPC(t *testing.T) {
	got := JoinByResourceType(
		[]span.Span{
			sp("big_rpc", "p", "r", 90000),
			sp("small_rpc", "p", "r", 10000),
		},
		[]span.Span{uiSp("has_ui", 5000)},
	)
	if got[0].ResourceType != "has_ui" {
		t.Errorf("first row = %q, want has_ui: a UI total outranks any RPC total", got[0].ResourceType)
	}
	if got[1].ResourceType != "big_rpc" {
		t.Errorf("second row = %q, want big_rpc", got[1].ResourceType)
	}
}

func TestJoinByResourceTypeEmptyInput(t *testing.T) {
	if got := JoinByResourceType(nil, nil); len(got) != 0 {
		t.Errorf("got %d rows from no spans, want 0", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -run Join -count=1`
Expected: FAIL to build — `undefined: JoinByResourceType`.

- [ ] **Step 3: Write minimal implementation**

```go
package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// TypeRow is one resource type as both tiers saw it.
//
// The two sides measure overlapping work at different granularities and must
// not be added together. A UI-hook figure times a whole resource, from
// Terraform's own hooks, quantised to whole seconds with up to a second of
// error either way. An RPC figure times one provider call exactly, in
// milliseconds, and one resource may make several. Reading them side by side
// is the point: a type whose UI total dwarfs its RPC total spent its time
// somewhere other than in provider calls.
type TypeRow struct {
	ResourceType string

	UIResources int
	UITotalMs   uint64
	UIMaxMs     uint32

	RPCCalls   int
	RPCTotalMs uint64
	RPCMaxMs   uint32
}

// JoinByResourceType puts each tier's view of a resource type on one row.
//
// It keys on ResourceType because that field means the same thing in both
// builders -- unlike RPC, which holds an RPC name on reported spans and a
// hook action on UI-hook spans. No address attribution is involved, which is
// why this works on the dual-tier capture even though that capture has no
// core graph output.
func JoinByResourceType(rpcSpans, uiSpans []span.Span) []TypeRow {
	rows := make(map[string]*TypeRow)
	get := func(k string) *TypeRow {
		if k == "" {
			k = noKey
		}
		r := rows[k]
		if r == nil {
			r = &TypeRow{ResourceType: k}
			rows[k] = r
		}
		return r
	}

	for _, s := range rpcSpans {
		r := get(s.ResourceType)
		r.RPCCalls++
		r.RPCTotalMs += uint64(s.DurationMs)
		if s.DurationMs > r.RPCMaxMs {
			r.RPCMaxMs = s.DurationMs
		}
	}
	for _, s := range uiSpans {
		r := get(s.ResourceType)
		r.UIResources++
		r.UITotalMs += uint64(s.DurationMs)
		if s.DurationMs > r.UIMaxMs {
			r.UIMaxMs = s.DurationMs
		}
	}

	out := make([]TypeRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UITotalMs != out[j].UITotalMs {
			return out[i].UITotalMs > out[j].UITotalMs
		}
		if out[i].RPCTotalMs != out[j].RPCTotalMs {
			return out[i].RPCTotalMs > out[j].RPCTotalMs
		}
		return out[i].ResourceType < out[j].ResourceType
	})
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

---

### Task 4: `Filter` and facets

**Files:**
- Create: `internal/model/filter.go`
- Create: `internal/model/filter_test.go`

**Interfaces:**
- Consumes: `span.Span`, `logfmt.Entry`, `logfmt.Level`, `logfmt.Interner`, `RollupBy`
- Produces:
  ```go
  type FacetValue struct {
      Value string
      Count int
  }
  type Facet struct {
      Name   string
      Values []FacetValue
  }
  func FacetsForSpans(spans []span.Span) []Facet

  type Filter struct {
      Providers map[string]bool
      RPCs      map[string]bool
      Types     map[string]bool
      Levels    map[logfmt.Level]bool
  }
  func (f Filter) MatchSpan(s span.Span) bool
  func (f Filter) MatchEntry(e logfmt.Entry) bool
  func (f Filter) SpansMatching(spans []span.Span) []span.Span
  ```

- [ ] **Step 1: Write the failing test**

```go
package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// An empty dimension means "no opinion", not "match nothing". Getting this
// backwards makes a fresh filter hide the whole log, which reads as the tool
// being broken rather than as a filter being active.
func TestEmptyFilterMatchesEverything(t *testing.T) {
	var f Filter
	if !f.MatchSpan(sp("aws_instance", "aws", "ReadResource", 1)) {
		t.Error("empty filter rejected a span")
	}
	if !f.MatchEntry(logfmt.Entry{Level: logfmt.LevelTrace}) {
		t.Error("empty filter rejected an entry")
	}
}

// Within a dimension the selected values are alternatives; across dimensions
// every constraint must hold. Facets are described in the spec as cumulative.
func TestFilterIsOrWithinAndAndAcross(t *testing.T) {
	f := Filter{
		Providers: map[string]bool{"aws": true, "azurerm": true},
		RPCs:      map[string]bool{"ReadResource": true},
	}
	if !f.MatchSpan(sp("t", "aws", "ReadResource", 1)) {
		t.Error("rejected a span matching both dimensions")
	}
	if !f.MatchSpan(sp("t", "azurerm", "ReadResource", 1)) {
		t.Error("rejected the second alternative within a dimension")
	}
	if f.MatchSpan(sp("t", "aws", "ApplyResourceChange", 1)) {
		t.Error("accepted a span failing the RPC dimension")
	}
	if f.MatchSpan(sp("t", "google", "ReadResource", 1)) {
		t.Error("accepted a span failing the provider dimension")
	}
}

func TestSpansMatchingPreservesOrder(t *testing.T) {
	spans := []span.Span{
		sp("a", "aws", "r", 3),
		sp("b", "google", "r", 2),
		sp("c", "aws", "r", 1),
	}
	f := Filter{Providers: map[string]bool{"aws": true}}
	got := f.SpansMatching(spans)
	if len(got) != 2 || got[0].ResourceType != "a" || got[1].ResourceType != "c" {
		t.Errorf("SpansMatching = %+v, want a then c in input order", got)
	}
}

// Facet counts drive the counts shown beside each value in the TUI's facet
// pane, so they count spans, not distinct values.
func TestFacetsForSpansCountsAndOrders(t *testing.T) {
	spans := []span.Span{
		sp("t1", "aws", "ReadResource", 1),
		sp("t2", "aws", "ReadResource", 1),
		sp("t3", "google", "ApplyResourceChange", 1),
	}
	facets := FacetsForSpans(spans)
	byName := map[string]Facet{}
	for _, f := range facets {
		byName[f.Name] = f
	}
	prov, ok := byName["provider"]
	if !ok {
		t.Fatalf("no provider facet: %+v", facets)
	}
	if prov.Values[0].Value != "aws" || prov.Values[0].Count != 2 {
		t.Errorf("provider facet = %+v, want aws with count 2 first", prov.Values)
	}
	if _, ok := byName["rpc"]; !ok {
		t.Error("no rpc facet")
	}
	if _, ok := byName["resource type"]; !ok {
		t.Error("no resource type facet")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -run 'Filter|Facet|SpansMatching' -count=1`
Expected: FAIL to build — `undefined: Filter`.

- [ ] **Step 3: Write minimal implementation**

```go
package model

import (
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// FacetValue is one selectable value and how many spans carry it.
type FacetValue struct {
	Value string
	Count int
}

// Facet is one filterable dimension.
type Facet struct {
	Name   string
	Values []FacetValue
}

// Filter is a cumulative facet selection. An empty or nil map means the
// dimension is unconstrained -- "no opinion", never "match nothing", so a
// zero Filter passes everything.
type Filter struct {
	Providers map[string]bool
	RPCs      map[string]bool
	Types     map[string]bool
	Levels    map[logfmt.Level]bool
}

// selected reports whether a value passes one dimension.
func selected(set map[string]bool, v string) bool {
	if len(set) == 0 {
		return true
	}
	return set[v]
}

// MatchSpan applies every dimension: alternatives within one, conjunction
// across them, as the spec's cumulative facets require.
func (f Filter) MatchSpan(s span.Span) bool {
	return selected(f.Providers, s.Provider) &&
		selected(f.RPCs, s.RPC) &&
		selected(f.Types, s.ResourceType)
}

// MatchEntry applies the level dimension. Span dimensions do not apply to a
// raw entry: most entries belong to no span at all.
func (f Filter) MatchEntry(e logfmt.Entry) bool {
	if len(f.Levels) == 0 {
		return true
	}
	return f.Levels[e.Level]
}

// SpansMatching returns the matching spans in their input order, which is
// scan order, so a caller that wants a different order sorts explicitly.
func (f Filter) SpansMatching(spans []span.Span) []span.Span {
	out := make([]span.Span, 0, len(spans))
	for _, s := range spans {
		if f.MatchSpan(s) {
			out = append(out, s)
		}
	}
	return out
}

// FacetsForSpans builds the selectable dimensions, each ordered by span count
// descending then value ascending, so the ordering is total.
func FacetsForSpans(spans []span.Span) []Facet {
	dims := []struct {
		name string
		key  func(span.Span) string
	}{
		{"provider", func(s span.Span) string { return s.Provider }},
		{"rpc", func(s span.Span) string { return s.RPC }},
		{"resource type", func(s span.Span) string { return s.ResourceType }},
	}
	out := make([]Facet, 0, len(dims))
	for _, d := range dims {
		counts := map[string]int{}
		for _, s := range spans {
			k := d.key(s)
			if k == "" {
				k = noKey
			}
			counts[k]++
		}
		f := Facet{Name: d.name, Values: make([]FacetValue, 0, len(counts))}
		for v, c := range counts {
			f.Values = append(f.Values, FacetValue{Value: v, Count: c})
		}
		sortFacetValues(f.Values)
		out = append(out, f)
	}
	return out
}
```

Add the helper it calls:

```go
// sortFacetValues orders a facet's values by span count descending, ties
// broken by value ascending so the ordering is total.
func sortFacetValues(vs []FacetValue) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Count != vs[j].Count {
			return vs[i].Count > vs[j].Count
		}
		return vs[i].Value < vs[j].Value
	})
}
```

and add `"sort"` to the file's imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

---

### Task 5: Lane packing and concurrency

This answers the question a ranked list cannot: was a 522-second plan 522 seconds of work, or 25 minutes of work overlapped 2.9 ways with gaps in between?

**Files:**
- Create: `internal/model/lanes.go`
- Create: `internal/model/lanes_test.go`

**Interfaces:**
- Consumes: `span.Span`, `span.Fidelity`
- Produces:
  ```go
  type Lane struct{ Spans []int } // indices into the slice passed to PackLanes
  func PackLanes(spans []span.Span) ([]Lane, error)
  func PeakConcurrency(spans []span.Span) int
  ```

- [ ] **Step 1: Write the failing test**

```go
package model

import (
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func timed(start, end uint32, f span.Fidelity) span.Span {
	return span.Span{StartMs: start, EndMs: end, DurationMs: end - start, Fidelity: f}
}

func TestPackLanesPutsOverlappingSpansInSeparateLanes(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(500, 1500, span.FidelityReported),
		timed(2000, 2500, span.FidelityReported),
	}
	lanes, err := PackLanes(spans)
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 2 {
		t.Fatalf("got %d lanes, want 2: %+v", len(lanes), lanes)
	}
	// The third span starts after the first ends, so it reuses lane 0.
	if len(lanes[0].Spans) != 2 {
		t.Errorf("lane 0 holds %d spans, want 2 -- a non-overlapping span must reuse a free lane", len(lanes[0].Spans))
	}
}

func TestPackLanesKeepsEverySpan(t *testing.T) {
	spans := []span.Span{
		timed(0, 100, span.FidelityReported),
		timed(10, 200, span.FidelityReported),
		timed(20, 300, span.FidelityReported),
	}
	lanes, err := PackLanes(spans)
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	var n int
	seen := map[int]bool{}
	for _, l := range lanes {
		for _, i := range l.Spans {
			if seen[i] {
				t.Errorf("span %d appears in more than one lane", i)
			}
			seen[i] = true
			n++
		}
	}
	if n != len(spans) {
		t.Errorf("packed %d spans, want %d", n, len(spans))
	}
}

// The two builders anchor StartMs/EndMs to different zero points, so packing
// a mixed slice would interleave two unrelated timelines and produce lanes
// that look plausible and mean nothing. This must fail loudly, not silently.
func TestPackLanesRejectsMixedFidelity(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(0, 1000, span.FidelityUIReported),
	}
	if _, err := PackLanes(spans); err == nil {
		t.Fatal("PackLanes accepted spans from two different timelines")
	}
}

func TestPeakConcurrency(t *testing.T) {
	spans := []span.Span{
		timed(0, 1000, span.FidelityReported),
		timed(100, 900, span.FidelityReported),
		timed(200, 800, span.FidelityReported),
		timed(5000, 6000, span.FidelityReported),
	}
	if got := PeakConcurrency(spans); got != 3 {
		t.Errorf("PeakConcurrency = %d, want 3", got)
	}
}

func TestPackLanesEmptyInput(t *testing.T) {
	lanes, err := PackLanes(nil)
	if err != nil {
		t.Fatalf("PackLanes: %v", err)
	}
	if len(lanes) != 0 {
		t.Errorf("got %d lanes from no spans, want 0", len(lanes))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -run 'Lane|Concurrency' -count=1`
Expected: FAIL to build — `undefined: PackLanes`.

- [ ] **Step 3: Write minimal implementation**

```go
package model

import (
	"errors"
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Lane holds indices into the slice passed to PackLanes, in start order.
// Indices rather than copies so a caller can look up the original span
// without the lanes duplicating span data.
type Lane struct{ Spans []int }

// ErrMixedTimelines is returned when PackLanes is handed spans from more than
// one builder. See the doc comment on span.Span: the builders anchor
// StartMs/EndMs to different zero points, so packing a mixed slice
// interleaves two unrelated timelines into lanes that look plausible and mean
// nothing. Refusing is the only safe behaviour, because there is no signal in
// the output that would let a reader notice.
var ErrMixedTimelines = errors.New("model: cannot pack lanes across spans of different fidelity")

// PackLanes assigns spans to execution lanes by greedy interval packing: each
// span goes into the first lane whose last span has already finished.
//
// Lanes are computed, not read from the log. Terraform's log carries no worker
// identifier and none is needed -- packing depends only on start and end
// times, so this behaves identically at every extraction tier.
func PackLanes(spans []span.Span) ([]Lane, error) {
	if len(spans) == 0 {
		return nil, nil
	}
	for _, s := range spans[1:] {
		if s.Fidelity != spans[0].Fidelity {
			return nil, ErrMixedTimelines
		}
	}

	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		x, y := spans[order[a]], spans[order[b]]
		if x.StartMs != y.StartMs {
			return x.StartMs < y.StartMs
		}
		return x.EndMs < y.EndMs
	})

	var lanes []Lane
	var laneEnd []uint32
	for _, i := range order {
		s := spans[i]
		placed := false
		for l := range lanes {
			if laneEnd[l] <= s.StartMs {
				lanes[l].Spans = append(lanes[l].Spans, i)
				laneEnd[l] = s.EndMs
				placed = true
				break
			}
		}
		if !placed {
			lanes = append(lanes, Lane{Spans: []int{i}})
			laneEnd = append(laneEnd, s.EndMs)
		}
	}
	return lanes, nil
}

// PeakConcurrency is the largest number of spans in flight at once, computed
// by sweeping start and end events. It is the headline number for whether a
// plan was slow because of work or because of waiting: summed span time
// divided by wall clock gives the average, and this gives the ceiling.
func PeakConcurrency(spans []span.Span) int {
	type event struct {
		at    uint32
		delta int
	}
	events := make([]event, 0, len(spans)*2)
	for _, s := range spans {
		events = append(events, event{s.StartMs, 1}, event{s.EndMs, -1})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		// Ends before starts at the same instant: a span ending exactly as
		// another begins is a handover, not overlap.
		return events[i].delta < events[j].delta
	})
	var cur, peak int
	for _, e := range events {
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/model/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

---

### Task 6: `--profile` — render the model, wire the flag

**Files:**
- Create: `internal/profile/profile.go`
- Create: `internal/profile/profile_test.go`
- Modify: `cmd/tfli/main.go` (the flag block and the branch after argument validation)
- Modify: `cmd/tfli/main_test.go` (add cases)
- Modify: `README.md` (document `--profile` and its disclosure warning)

**Interfaces:**
- Consumes: `model.Log`, `model.JoinByResourceType`, `model.RollupBy`, `model.PeakConcurrency`, `model.PackLanes`
- Produces: `func Render(w io.Writer, l *model.Log) error`

- [ ] **Step 1: Write the failing test**

```go
package profile

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func render(t *testing.T, path string) string {
	t.Helper()
	l, err := model.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var sb strings.Builder
	if err := Render(&sb, l); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return sb.String()
}

// Unlike --diagnose, this report shows real addresses. A reader who assumes
// otherwise could paste a confidential resource address somewhere it does not
// belong, so the warning is part of the contract, not decoration.
func TestReportWarnsThatOutputIsUnmasked(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "not masked") {
		t.Errorf("report does not warn that it is unmasked:\n%s", out)
	}
}

func TestReportShowsResourceTypeJoin(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "BY RESOURCE TYPE") {
		t.Errorf("report missing the resource-type join:\n%s", out)
	}
	if !strings.Contains(out, "aws_instance") {
		t.Errorf("report does not name a resource type present in the fixture:\n%s", out)
	}
}

// UI-hook figures are whole seconds carrying up to a second of error each, so
// a report that ranks them must say so -- the same caveat --diagnose carries.
func TestReportStatesUIHookResolution(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "whole seconds") {
		t.Errorf("report does not state the UI-hook timing resolution:\n%s", out)
	}
}

func TestReportShowsProviderRollup(t *testing.T) {
	out := render(t, "../../testdata/provider-rpc.log")
	if !strings.Contains(out, "BY PROVIDER") {
		t.Errorf("report missing the provider rollup:\n%s", out)
	}
}

func TestReportShowsConcurrencyForRPCSpans(t *testing.T) {
	out := render(t, "../../testdata/provider-rpc.log")
	if !strings.Contains(out, "CONCURRENCY") {
		t.Errorf("report missing the concurrency section:\n%s", out)
	}
	if !strings.Contains(out, "peak") {
		t.Errorf("concurrency section does not report a peak:\n%s", out)
	}
}

// A log with neither tier must say so rather than printing empty headings.
func TestReportSaysSoWhenThereAreNoSpans(t *testing.T) {
	out := render(t, "../../testdata/core-only.log")
	if !strings.Contains(out, "no spans") {
		t.Errorf("report does not explain an empty result:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/profile/ -count=1`
Expected: FAIL to build — `undefined: Render`.

- [ ] **Step 3: Write minimal implementation**

Write `Render` to emit, in order:

1. A header naming the file's size and span counts, then the warning line — it must contain the exact substring `not masked`, e.g. `Resource addresses below are NOT masked. Unlike --diagnose, this report is not safe to share.`
2. `BY RESOURCE TYPE` — `model.JoinByResourceType(l.RPCSpans, l.UISpans)`, columns: resource type, UI resources, UI total, RPC calls, RPC total, RPC max. Include the whole-seconds caveat (must contain `whole seconds`) whenever any UI column is non-zero.
3. `BY PROVIDER` — `model.RollupBy(l.RPCSpans, func(s span.Span) string { return s.Provider })`, columns: total, calls, max, provider. This is the spec's view 1. Skip the section when there are no RPC spans.
4. `SLOWEST CALLS` — RPC spans sorted by `DurationMs` descending, top 20: duration, RPC, resource type, provider.
5. `SLOWEST RESOURCES` — UI-hook spans sorted by `DurationMs` descending, top 20: duration, action, resource type, **unmasked** `Address`.
6. `CONCURRENCY` — for RPC spans only: `model.PeakConcurrency`, the lane count from `model.PackLanes`, summed span time, and the ratio of summed time to wall clock. Call `PackLanes` on `l.RPCSpans` and `l.UISpans` separately, never on a concatenation; surface `model.ErrMixedTimelines` as a returned error rather than ignoring it.
7. When `len(l.RPCSpans) == 0 && len(l.UISpans) == 0`, print a single explanatory line containing `no spans` naming the likely cause — no protocol lines and no `terraform.ui` stream — and return without the other sections.

Reuse the millisecond formatting shape from `internal/diagnose/diagnose.go`'s `formatMs` (sub-second as `842ms`, otherwise one decimal second). Copy it rather than exporting it: the two reports are free to diverge, and a shared formatter would couple a safety-critical renderer to a convenience one.

Then in `cmd/tfli/main.go`, add alongside the existing flags:

```go
doProfile = fs.Bool("profile", false, "rank resource types and calls by time (output is NOT masked)")
```

and after the existing argument-count check, replace the `--diagnose`-only guard with a branch that runs `model.Load` + `profile.Render` when `*doProfile` is set, keeps the existing `--diagnose` path when `*doDiagnose` is set, rejects both being set at once, and errors when neither is. Reuse the existing `-o` handling for both modes — including the `Close` error check, which is the reason a truncated report cannot pass silently.

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./... -count=1 && go -C /Users/dan/Code/tf-log-inspector vet ./...`
Expected: PASS, vet clean.

Then run it for real and read the output:
```
go -C /Users/dan/Code/tf-log-inspector run ./cmd/tfli --profile testdata/mixed-hcp.log
```

- [ ] **Step 5: Add CLI tests**

Add to `cmd/tfli/main_test.go`: `--profile` on a fixture succeeds and prints `BY RESOURCE TYPE`; `--diagnose --profile` together returns an error naming both; neither flag returns the existing error.

- [ ] **Step 6: Update the README**

Under `## Usage`, document `tfli --profile plan.log` and state plainly that its output contains unmasked resource addresses and is not safe to share, in contrast to `--diagnose`.

- [ ] **Step 7: Commit**

---

## Self-review

**1. Spec coverage.** Phase 2 in the spec is "Span extraction and the model layer. Rollups, facet filtering, lane packing, all as pure functions under test", plus the revised `--profile` deliverable. Rollups → Task 2. Facet filtering → Task 4. Lane packing → Task 5. `--profile` → Task 6. The cross-tier join, added to the spec today, → Task 3. Span extraction itself already exists from phase 1; Task 1 is what retains it.

Deliberately **not** in this plan, and why:
- **Address attribution / view 3.** Spec phase 5, and doubly conditional now: it needs core-TRACE output, which the recommended capture does not contain, and Task 3's type join answers most of the question without it.
- **Free-text search.** Spec places it with the TUI; it has no meaning in a headless report.
- **Stall annotation on the timeline.** Spec view 5, phase 4. Task 5 delivers the packing that view needs; the annotation is a rendering concern.
- **Width degradation, keys, panes.** All TUI, phase 3.

**2. Placeholder scan.** No TBDs. Task 6's step 3 is prose rather than a full code block — deliberate, because it is a renderer whose exact column layout should be settled by looking at real output, and its behaviour is pinned by five tests written in step 1. Every other step carries the code it needs.

**3. Type consistency.** `Bucket`, `TypeRow`, `Filter`, `Facet`, `FacetValue`, `Lane` are each defined once. `noKey` is defined in Task 2 and reused in Tasks 3 and 4. `RollupBy` takes `key func(span.Span) string` everywhere it appears. `PackLanes` returns `([]Lane, error)` in both its interface block and its test.

**4. Ordering.** Tasks 2–5 are independent of one another and all depend only on Task 1. Task 6 depends on 2, 3 and 5. Task 4 is not consumed by Task 6 — facets have no headless surface — but the spec places it in phase 2 and the TUI needs it in phase 3.
