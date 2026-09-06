# Phase 5: Address Attribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Name the Terraform resource a provider RPC call belongs to, by correlating RPC spans against address context taken from the `terraform.ui` structured-output stream, and surface it in the Calls detail pane.

**Architecture:** A new `internal/attrib` package collects address context windows from UI-hook `_start`/terminator pairs during the existing single scan, then correlates RPC spans against those windows on the absolute wall clock. Correlation produces a table parallel to `model.Log.RPCSpans`, never a field on `span.Span` — `Span` records what the log stated, `Attribution` records what was inferred from it. The TUI reads that table; `--diagnose` publishes the coverage statistic that gates view 3.

**Tech Stack:** Go (module root `/Users/dan/Code/tf-log-inspector`), `encoding/json`, `bubbletea`/`lipgloss` for the TUI, golden-file tests at 70/100/160 columns.

**Spec:** `docs/superpowers/specs/2026-09-03-tf-log-inspector-design.md` — read the `### Address attribution` section in full before starting. This plan argues from it; where they disagree, the spec wins and the disagreement is a defect to report.

## Global Constraints

Copied from the spec and the project's conventions. Every task's requirements implicitly include this section.

- **Never invent technical details.** A field, flag, message form or JSON key you have not read in this repo or in a cited upstream source is a lie, not a placeholder. If you need one and cannot verify it, stop and say so.
- **Inference must never be indistinguishable from observation.** This is the design's central discipline. An inferred address must be marked as inferred everywhere it appears.
- **An `Ambiguous` span never asserts an address.** It reports a candidate count and lists candidates as candidates.
- **Attribution never changes a duration or a start time.** `Span.StartMs`/`EndMs` are never rewritten; correlation runs on locally computed absolute times.
- **Interval convention is half-open `[start, end)`**, matching `model.PackLanes` and `PeakConcurrency`. An empty interval occupies no instant and overlaps nothing.
- **Every fixture under `testdata/` must be synthesised or quoted from a named public source, with a header comment saying which.** New fixtures in this phase are synthesised and carry a `# SYNTHESISED` header. Never copy lines from any log outside the repository.
- **No address is ever printed by `--diagnose`.** Its output is masked and shareable; the TUI and `--profile` are not.
- Build with `go build ./...`, test with `go test ./...` from the module root. Format with `gofmt`.
- Commit with `/Users/dan/.claude/bin/claude-git`, never bare `git`, and pass multi-line messages via `-F <file>`. Do not merge or push.
- Australian / British English in prose and comments.

## Deliverables and the one gate

- **Tasks 1–6 are unconditional.** They deliver the phase's acceptance criterion: the Calls detail pane names a call's resource (name, plus module and index key where they exist).
- **Task 7 (view 3) is gated.** It ships as a view only if at least half of attributable RPC span *time* resolves to `Contained` or `Likely`, measured by Task 5's coverage statistic against a real capture. Task 7 begins with a human gate; do not build it on a synthesised number.

## File structure

| File | Responsibility |
|---|---|
| `internal/attrib/context.go` (create) | `Context`, `ContextCollector` — decodes UI-hook lines into address context windows on the absolute clock. One `logfmt.StructuredSink`. |
| `internal/attrib/correlate.go` (create) | `Attribution`, `Confidence`, `Correlate` — the interval predicates and the confidence model. Pure functions over the two inputs. |
| `internal/attrib/coverage.go` (create) | `Coverage`, `Summarise` — the published statistic, including the share of time that is nameable. |
| `internal/model/log.go` (modify) | Wires `ContextCollector` into the existing single scan; adds `Log.Contexts` and `Log.Attribs`. |
| `internal/span/sniff.go` (modify) | Counts UI-hook `type` values for the histogram; adds the "context source present" capability. |
| `internal/diagnose/diagnose.go` (modify) | Renders the histogram, coverage statistic, confidence distribution and baseline offset. |
| `internal/tui/layout.go` (modify) | `spanDetailLines` gains the resource fields and closes the standing `ResourceType` gap. |
| `internal/tui/views.go`, `internal/tui/model.go` (modify) | View 3 — gated, Task 7 only. |

---

### Task 1: Address context collection

Builds the input half of attribution: a `logfmt.StructuredSink` that turns UI-hook lines into address context windows with absolute timestamps.

**Files:**
- Create: `internal/attrib/context.go`
- Create: `internal/attrib/context_test.go`
- Create: `internal/attrib/testdata/context.log`

**Interfaces:**
- Consumes: `logfmt.Entry`, `logfmt.Fields`, `logfmt.Sink`, `logfmt.StructuredSink` from `internal/logfmt/scan.go`. `logfmt.Scan(r io.Reader, comps *Interner, sinks ...Sink) (Stats, error)`.
- Produces, for Tasks 2–5:
  - `type Context struct { Address, Module, Name, Key, ResourceType, Action string; IsData bool; Start, End time.Time; Unclosed bool }`
  - `type ContextCollector struct{ ... }` with `Entry(uint32, logfmt.Entry, string, logfmt.Fields)`, `Structured(uint32, logfmt.Entry, string)`, `Contexts() []Context`, `FirstTS() time.Time`, `TypeCounts() map[string]uint64`, `Malformed() uint64`, `UnmatchedTerminators() uint64`, `CompletedPairs() int`

**Background the implementer needs.**

Terraform's structured-output stream emits one JSON object per line with `@module":"terraform.ui"`. `logfmt.Scan` detects those lines and delivers their raw text to any sink implementing `StructuredSink`; a sink that does not implement it never sees the text, which is how the diagnostic report's disclosure guarantee stays true by construction. Your collector deliberately decodes only the keys listed below.

~~**A refresh is `apply_start`/`apply_complete` with `action:"read"`.** There is no `refresh_*` hook type. This is confirmed against a real run and recorded in `testdata/structured-ui.log`'s header — read that header before writing the decoder.~~

**Corrected 2026-09-07: this was a false generalisation, caught by a later PR review.** `apply_start`/`apply_complete` with `action:"read"` is what a **data-source read** looks like (`PreApply` with `plans.Read`), confirmed against `testdata/structured-ui.log`'s header as before. A **managed-resource refresh** is a different hook pair, `refresh_start`/`refresh_complete`, verified against `hashicorp/terraform` tag `v1.14.9`'s `internal/command/views/json/message_types.go` and `hook_json.go`; it carries no `action` field at all. Both are real. See the spec's `### Address attribution` section for the full correction, and `internal/attrib/context.go`'s `opensContext`/`closesContext` for the implementation.

- [ ] **Step 1: Write the fixture**

Create `internal/attrib/testdata/context.log`. The header must be one line beginning `# SYNTHESISED`.

```
# SYNTHESISED to exercise attrib.ContextCollector. Values are invented; no line here is from a real run. The object shape -- hook.resource carrying addr, module, resource, implied_provider, resource_type, resource_name and resource_key, and apply_start/apply_complete with action:"read" for a refresh -- matches testdata/structured-ui.log, whose header records it as confirmed against a real run.
{"@level":"info","@message":"Terraform 1.14.9","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.000000+10:00","terraform":"1.14.9","type":"version","ui":"1.2"}
{"@level":"info","@message":"data.local_file.a: Refreshing...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:03.000000+10:00","hook":{"resource":{"addr":"data.local_file.a","module":"","resource":"data.local_file.a","implied_provider":"local","resource_type":"local_file","resource_name":"a","resource_key":null},"action":"read"},"type":"apply_start"}
{"@level":"info","@message":"data.local_file.a: Refresh complete after 2s","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:05.000000+10:00","hook":{"resource":{"addr":"data.local_file.a","module":"","resource":"data.local_file.a","implied_provider":"local","resource_type":"local_file","resource_name":"a","resource_key":null},"action":"read","id_key":"id","id_value":"SECRETVALUE"},"type":"apply_complete"}
{"@level":"info","@message":"module.m[\"k\"].aws_instance.web[0]: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:06.000000+10:00","hook":{"resource":{"addr":"module.m[\"k\"].aws_instance.web[0]","module":"module.m[\"k\"]","resource":"aws_instance.web[0]","implied_provider":"aws","resource_type":"aws_instance","resource_name":"web","resource_key":0},"action":"create"},"type":"apply_start"}
{"@level":"info","@message":"module.m[\"k\"].aws_instance.web[0]: Still creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:07.000000+10:00","hook":{"resource":{"addr":"module.m[\"k\"].aws_instance.web[0]","module":"module.m[\"k\"]","resource":"aws_instance.web[0]","implied_provider":"aws","resource_type":"aws_instance","resource_name":"web","resource_key":0},"action":"create","elapsed_seconds":1.0},"type":"apply_progress"}
{"@level":"error","@message":"module.m[\"k\"].aws_instance.web[0]: Creation errored after 3s","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:09.000000+10:00","hook":{"resource":{"addr":"module.m[\"k\"].aws_instance.web[0]","module":"module.m[\"k\"]","resource":"aws_instance.web[0]","implied_provider":"aws","resource_type":"aws_instance","resource_name":"web","resource_key":0},"action":"create","elapsed_seconds":3.0},"type":"apply_errored"}
{"@level":"info","@message":"aws_instance.orphan: Creating...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:10.000000+10:00","hook":{"resource":{"addr":"aws_instance.orphan","module":"","resource":"aws_instance.orphan","implied_provider":"aws","resource_type":"aws_instance","resource_name":"orphan","resource_key":null},"action":"create"},"type":"apply_start"}
```

The last line opens a context that is never closed — the unclosed case. `SECRETVALUE` is there to prove `id_value` is never decoded.

- [ ] **Step 2: Write the failing test**

Create `internal/attrib/context_test.go`:

```go
package attrib

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// collect runs the collector over a fixture and returns its contexts.
func collect(t *testing.T, path string) (*ContextCollector, []Context) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	var c ContextCollector
	if _, err := logfmt.Scan(strings.NewReader(string(data)), &logfmt.Interner{}, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return &c, c.Contexts()
}

func TestCollectorPairsStartWithComplete(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	if len(ctxs) != 3 {
		t.Fatalf("contexts = %d, want 3", len(ctxs))
	}
	got := ctxs[0]
	if got.Address != "data.local_file.a" {
		t.Errorf("Address = %q, want data.local_file.a", got.Address)
	}
	if got.ResourceType != "local_file" {
		t.Errorf("ResourceType = %q, want local_file", got.ResourceType)
	}
	if got.Action != "read" {
		t.Errorf("Action = %q, want read", got.Action)
	}
	if !got.IsData {
		t.Error("IsData = false, want true for data.local_file.a")
	}
	if got.Unclosed {
		t.Error("Unclosed = true, want false")
	}
	if d := got.End.Sub(got.Start); d != 2*time.Second {
		t.Errorf("window = %v, want 2s", d)
	}
}

// A refresh is apply_start/apply_complete with action:"read" -- there is no
// refresh_* hook type. Asserted here because an earlier design draft assumed
// otherwise and the assumption reached a committed spec.
func TestRefreshIsAnApplyPairWithReadAction(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	if ctxs[0].Action != "read" {
		t.Fatalf("first context action = %q, want read", ctxs[0].Action)
	}
	if !ctxs[0].End.After(ctxs[0].Start) {
		t.Error("refresh produced no window")
	}
}

func TestErroredTerminatesAContext(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	got := ctxs[1]
	if got.Address != `module.m["k"].aws_instance.web[0]` {
		t.Fatalf("Address = %q", got.Address)
	}
	if got.Unclosed {
		t.Error("apply_errored did not close the context")
	}
	// apply_start 09:15:06 -> apply_errored 09:15:09. apply_progress at
	// 09:15:07 must not have closed it.
	if d := got.End.Sub(got.Start); d != 3*time.Second {
		t.Errorf("window = %v, want 3s (apply_progress must not terminate)", d)
	}
}

func TestModuleNameAndKeyAreDecoded(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	got := ctxs[1]
	if got.Module != `module.m["k"]` {
		t.Errorf("Module = %q", got.Module)
	}
	if got.Name != "web" {
		t.Errorf("Name = %q, want web", got.Name)
	}
	if got.Key != "0" {
		t.Errorf("Key = %q, want 0 (a numeric resource_key renders as its literal)", got.Key)
	}
	if ctxs[0].Key != "" {
		t.Errorf("null resource_key = %q, want empty", ctxs[0].Key)
	}
}

func TestUnclosedContextEndsAtLastTimestamp(t *testing.T) {
	_, ctxs := collect(t, "testdata/context.log")
	got := ctxs[2]
	if got.Address != "aws_instance.orphan" {
		t.Fatalf("Address = %q", got.Address)
	}
	if !got.Unclosed {
		t.Error("Unclosed = false, want true")
	}
	if !got.End.Equal(got.Start) {
		// The orphan's start IS the last timestamp in the fixture, so its
		// window is zero-extent. That is the honest answer: nothing in the
		// log says it ran for any measurable time.
		t.Errorf("End = %v, want equal to Start %v", got.End, got.Start)
	}
}

func TestCompletedPairsCountsOnlyClosedContexts(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	if got := c.CompletedPairs(); got != 2 {
		t.Errorf("CompletedPairs = %d, want 2", got)
	}
}

func TestTypeCountsHistogram(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	want := map[string]uint64{
		"version":        1,
		"apply_start":    3,
		"apply_complete": 1,
		"apply_progress": 1,
		"apply_errored":  1,
	}
	got := c.TypeCounts()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("TypeCounts[%q] = %d, want %d", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("TypeCounts has %d keys, want %d: %v", len(got), len(want), got)
	}
}

// The collector must never materialise id_value, which carries real resource
// ids. This mirrors span.uiLine's deliberate omission and keeps the
// disclosure guarantee a property of the struct's shape.
func TestIDValueIsNeverDecoded(t *testing.T) {
	data, err := os.ReadFile("testdata/context.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "SECRETVALUE") {
		t.Fatal("fixture no longer carries id_value; this test proves nothing")
	}
	_, ctxs := collect(t, "testdata/context.log")
	for _, c := range ctxs {
		for _, f := range []string{c.Address, c.Module, c.Name, c.Key, c.ResourceType, c.Action} {
			if strings.Contains(f, "SECRETVALUE") {
				t.Fatalf("id_value reached a Context field: %q", f)
			}
		}
	}
}

func TestFirstTSIsTheFirstParseableTimestamp(t *testing.T) {
	c, _ := collect(t, "testdata/context.log")
	want, _ := time.Parse(time.RFC3339Nano, "2026-09-04T09:15:02.000000+10:00")
	if !c.FirstTS().Equal(want) {
		t.Errorf("FirstTS = %v, want %v", c.FirstTS(), want)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/attrib/`
Expected: FAIL — `undefined: ContextCollector`.

- [ ] **Step 4: Write the implementation**

Create `internal/attrib/context.go`:

```go
// Package attrib attributes provider RPC spans to the Terraform resource
// addresses they belong to. This is inference, not observation: a provider
// RPC line carries a resource TYPE and never an address, so the address is
// correlated from Terraform's own structured-output stream, which does carry
// one. Everything this package produces is labelled with the confidence it
// was produced at, and nothing it produces is ever written back onto a
// span.Span -- Span records what the log stated.
package attrib

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// ctxLine is the subset of Terraform's structured-output line schema this
// package needs. Like span.uiLine it deliberately has no field for
// "@message", "id_key" or "id_value": encoding/json ignores unknown keys
// rather than erroring on them, so those three -- which between them carry a
// resource's real id value -- are never materialised at all. The disclosure
// guarantee is a property of this struct's shape, not of code elsewhere
// remembering not to read them.
type ctxLine struct {
	Timestamp string `json:"@timestamp"`
	Type      string `json:"type"`
	Hook      *struct {
		Resource *struct {
			Addr         string          `json:"addr"`
			Module       string          `json:"module"`
			Resource     string          `json:"resource"`
			ResourceType string          `json:"resource_type"`
			ResourceName string          `json:"resource_name"`
			ResourceKey  json.RawMessage `json:"resource_key"`
		} `json:"resource"`
		Action string `json:"action"`
	} `json:"hook"`
}

// Context is one window during which Terraform was working on one resource
// address. Start and End are ABSOLUTE times, taken from the lines' own
// RFC3339Nano timestamps rather than from elapsed_seconds -- which is
// quantised to whole seconds (see the spec's open question 7) and would blur
// every boundary by up to a second in each direction.
//
// The window is half-open, [Start, End), matching model.PackLanes. A
// zero-extent window occupies no instant and is never a candidate for
// anything.
type Context struct {
	Address      string
	Module       string // "" when the resource is not in a module
	Name         string
	Key          string // "" when the resource has no index key
	ResourceType string
	Action       string // hook.action: read, create, update, delete
	IsData       bool   // the address names a data source, not a managed resource
	Start, End   time.Time
	Unclosed     bool // no terminator before end-of-log; End is the log's last timestamp
}

// opensContext and closesContext name the hook types literally rather than
// by glob. A glob over "apply_*" would admit apply_progress, which carries a
// PARTIAL elapsed_seconds and must never terminate a context -- the same
// guard span.isCompletionType applies when building durations.
func opensContext(t string) bool {
	switch t {
	case "apply_start", "ephemeral_op_start", "provision_start":
		return true
	}
	return false
}

func closesContext(t string) bool {
	switch t {
	case "apply_complete", "apply_errored",
		"ephemeral_op_complete", "ephemeral_op_errored",
		"provision_complete", "provision_errored":
		return true
	}
	return false
}

// ContextCollector accumulates address contexts during a scan. It satisfies
// logfmt.StructuredSink, and Entry (a no-op) so it also satisfies
// logfmt.Sink and can be passed to logfmt.Scan directly. The zero value is
// ready to use.
type ContextCollector struct {
	ctxs   []Context
	open   map[string]int // address+action -> index into ctxs
	counts map[string]uint64 // hook type -> occurrences

	firstTS, lastTS time.Time
	haveTS          bool

	malformed  uint64
	unmatched  uint64
	closedPairs int
}

// Entry implements logfmt.Sink as a no-op: this collector only cares about
// structured lines, delivered via Structured.
func (c *ContextCollector) Entry(uint32, logfmt.Entry, string, logfmt.Fields) {}

// Structured implements logfmt.StructuredSink.
func (c *ContextCollector) Structured(_ uint32, _ logfmt.Entry, line string) {
	var cl ctxLine
	if err := json.Unmarshal([]byte(line), &cl); err != nil {
		c.malformed++
		return
	}

	if c.counts == nil {
		c.counts = make(map[string]uint64)
	}
	c.counts[cl.Type]++

	ts, err := time.Parse(time.RFC3339Nano, cl.Timestamp)
	if err != nil {
		// A line with no usable timestamp cannot bound a window. It is
		// still counted in the histogram above, so its absence from the
		// contexts is visible rather than silent.
		return
	}
	if !c.haveTS {
		c.firstTS, c.haveTS = ts, true
	}
	if ts.After(c.lastTS) {
		c.lastTS = ts
	}

	if cl.Hook == nil || cl.Hook.Resource == nil {
		return
	}
	r := cl.Hook.Resource
	key := r.Addr + "\x00" + cl.Hook.Action

	switch {
	case opensContext(cl.Type):
		if c.open == nil {
			c.open = make(map[string]int)
		}
		// A second start for a key already open closes the first here
		// rather than dropping either. Two windows on one address stay two
		// contexts: merging them would span the gap between them and
		// attribute calls made in that gap to a resource that was not
		// being worked on.
		if prev, ok := c.open[key]; ok {
			c.ctxs[prev].End = ts
			c.ctxs[prev].Unclosed = true
		}
		c.ctxs = append(c.ctxs, Context{
			Address:      r.Addr,
			Module:       r.Module,
			Name:         r.ResourceName,
			Key:          decodeKey(r.ResourceKey),
			ResourceType: r.ResourceType,
			Action:       cl.Hook.Action,
			IsData:       strings.HasPrefix(r.Resource, "data."),
			Start:        ts,
			End:          ts,
			Unclosed:     true,
		})
		c.open[key] = len(c.ctxs) - 1

	case closesContext(cl.Type):
		i, ok := c.open[key]
		if !ok {
			// A terminator with no start -- a log that begins mid-run.
			// Counted rather than synthesising a window whose start
			// nothing in the log states.
			c.unmatched++
			return
		}
		c.ctxs[i].End = ts
		c.ctxs[i].Unclosed = false
		delete(c.open, key)
		c.closedPairs++
	}
}

// decodeKey renders hook.resource.resource_key, which is null for a resource
// with no index, a JSON string for a for_each key and a JSON number for a
// count index. The literal is used for a number and the quotes are stripped
// from a string, so both read the way they do inside an address.
func decodeKey(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if unquoted, err := strconv.Unquote(s); err == nil {
		return unquoted
	}
	return s
}

// Contexts returns the collected contexts in the order their start lines
// appeared. A context still open at end-of-log has its End set to the last
// timestamp seen and stays marked Unclosed, matching how an unclosed SPAN is
// handled -- measured to end-of-log and flagged, never discarded.
func (c *ContextCollector) Contexts() []Context {
	for key, i := range c.open {
		c.ctxs[i].End = c.lastTS
		delete(c.open, key)
	}
	return c.ctxs
}

// FirstTS is the first parseable @timestamp on any structured line, and is
// this stream's baseline. It is reported by --diagnose against
// logfmt.Stats.FirstTS as the re-basing constant between the two streams --
// NOT as a check on clock agreement, which it cannot be: the two mark
// different events (the hclog stream brackets the whole Terraform process,
// terraform.ui only the plan phase within it), so their difference is real
// elapsed time plus any skew and the two are not separable.
func (c *ContextCollector) FirstTS() time.Time { return c.firstTS }

// TypeCounts is the histogram of structured-output "type" values. Type names
// and counts only: it is rendered by --diagnose, which is masked for sharing.
func (c *ContextCollector) TypeCounts() map[string]uint64 { return c.counts }

// Malformed reports structured lines that failed to decode as JSON.
func (c *ContextCollector) Malformed() uint64 { return c.malformed }

// UnmatchedTerminators reports terminators seen with no matching start,
// which is what a log captured from partway through a run produces.
func (c *ContextCollector) UnmatchedTerminators() uint64 { return c.unmatched }

// CompletedPairs reports how many contexts were both opened and closed. It
// is attribution's precondition: below one completed pair there is no
// address context in this log at all.
func (c *ContextCollector) CompletedPairs() int { return c.closedPairs }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/attrib/ -v`
Expected: PASS, all nine tests.

- [ ] **Step 6: Verify the whole repo still builds and passes**

Run: `go build ./... && go test ./...`
Expected: all seven existing packages plus `internal/attrib` pass.

- [ ] **Step 7: Commit**

Write the message to a file and commit both files plus the fixture:

```bash
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector add internal/attrib/
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -F /tmp/task1-msg.txt
```

---

### Task 2: The correlator and the confidence model

Correlates RPC spans against contexts and assigns one of five confidence values. This is the heart of the phase and the place where a wrong interval predicate produces silently plausible answers.

**Files:**
- Create: `internal/attrib/correlate.go`
- Create: `internal/attrib/correlate_test.go`

**Interfaces:**
- Consumes: `Context` from Task 1. `span.Span` from `internal/span/span.go` — the fields used here are `StartMs, EndMs, DurationMs uint32`, `StartClamped bool`, `RPC, ResourceType string`. `logfmt.Stats.FirstTS time.Time`.
- Produces, for Tasks 3–7:
  - `type Confidence uint8` with `Unattributed`, `Ambiguous`, `Overlapping`, `Likely`, `Contained` and a `String() string`
  - `type Attribution struct { Address, Module, Name, Key string; Candidates uint32; Confidence Confidence }`
  - `func Correlate(spans []span.Span, base time.Time, ctxs []Context) []Attribution`

**Background the implementer needs.**

`span.Span.StartMs`/`EndMs` are milliseconds from `logfmt.Stats.FirstTS`. Contexts carry absolute times. Correlation converts spans to absolute — `base.Add(ms)` — rather than converting contexts to relative, so the two meet on the wall clock and nothing is written back onto a span. **`Span.StartMs` and `Span.EndMs` must never be modified**: `model.PackLanes` and `PeakConcurrency` refuse a mixed-fidelity slice on the premise that those fields are not comparable across builders, a premise keyed on `Fidelity`, which in-place re-basing would silently falsify.

**`StartClamped` spans have a fabricated start.** `ReportedBuilder` sets it only when the entry's offset is less than the duration, so a clamped span always has `DurationMs >= 1` and its window is `[0, EndMs)` — which would overlap nearly every context in the log while appearing to contain several. A clamped span is therefore correlated on its end instant alone, as the one-millisecond probe interval `[End-1ms, End)`, and its confidence is capped at `Overlapping`.

- [ ] **Step 1: Write the failing test**

Create `internal/attrib/correlate_test.go`:

```go
package attrib

import (
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

var base = time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }

// ctx builds a managed-resource context of type typ over [startMs, endMs).
func ctx(addr, typ, action string, startMs, endMs int) Context {
	return Context{
		Address: addr, Name: addr, ResourceType: typ, Action: action,
		Start: at(startMs), End: at(endMs),
	}
}

// rpc builds an RPC span of type typ over [startMs, endMs).
func rpc(typ, rpcName string, startMs, endMs int) span.Span {
	return span.Span{
		StartMs: uint32(startMs), EndMs: uint32(endMs),
		DurationMs: uint32(endMs - startMs),
		RPC:        rpcName, ResourceType: typ,
		Fidelity: span.FidelityReported,
	}
}

func TestContainedWhenOneCandidateContainsTheSpan(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q", got[0].Address)
	}
	if got[0].Candidates != 1 {
		t.Errorf("Candidates = %d, want 1", got[0].Candidates)
	}
}

// The top label must require containment, not merely uniqueness. A span
// sharing one millisecond with a lone window is weaker evidence than one
// sitting wholly inside a window, and an earlier draft had it the other way
// round -- rewarding the SCARCITY of context rather than its strength.
func TestOverlappingWhenTheLoneCandidateDoesNotContain(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 150, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Overlapping {
		t.Errorf("Confidence = %v, want Overlapping", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q, want the address to still be named", got[0].Address)
	}
}

func TestLikelyWhenExactlyOneOfSeveralContains(t *testing.T) {
	ctxs := []Context{
		ctx("aws_instance.a", "aws_instance", "read", 0, 1000),
		ctx("aws_instance.b", "aws_instance", "read", 150, 1000),
	}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Likely {
		t.Fatalf("Confidence = %v, want Likely", got[0].Confidence)
	}
	if got[0].Address != "aws_instance.a" {
		t.Errorf("Address = %q, want the containing candidate", got[0].Address)
	}
	if got[0].Candidates != 2 {
		t.Errorf("Candidates = %d, want 2 (overlapping candidates considered)", got[0].Candidates)
	}
}

// An Ambiguous span never asserts an address. It reports how many candidates
// there were and names none of them.
func TestAmbiguousNamesNoAddress(t *testing.T) {
	ctxs := []Context{
		ctx("aws_instance.a", "aws_instance", "read", 0, 1000),
		ctx("aws_instance.b", "aws_instance", "read", 0, 1000),
	}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Ambiguous {
		t.Fatalf("Confidence = %v, want Ambiguous", got[0].Confidence)
	}
	if got[0].Address != "" {
		t.Errorf("Address = %q, want empty -- Ambiguous must assert nothing", got[0].Address)
	}
	if got[0].Candidates != 2 {
		t.Errorf("Candidates = %d, want 2", got[0].Candidates)
	}
}

func TestUnattributedWhenNothingMatches(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 5000, 6000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed", got[0].Confidence)
	}
	if got[0].Candidates != 0 {
		t.Errorf("Candidates = %d, want 0", got[0].Candidates)
	}
}

func TestResourceTypeMustMatch(t *testing.T) {
	ctxs := []Context{ctx("aws_subnet.a", "aws_subnet", "read", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed across differing types", got[0].Confidence)
	}
}

// A zero-extent interval occupies no instant under [start, end) and so
// overlaps nothing. Both directions are asserted: this is the boundary this
// project has already had to pin once, for PeakConcurrency.
func TestZeroExtentSpanOverlapsNothing(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 100)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed for a zero-extent span", got[0].Confidence)
	}
}

func TestZeroExtentContextIsNeverACandidate(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "read", 150, 150)}
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed for a zero-extent context", got[0].Confidence)
	}
}

// A clamped span's start is fabricated. Correlating on its [0, End) window
// would overlap nearly every context in the log and could appear to be
// CONTAINED by several -- on precisely the longest calls in a capture.
func TestClampedSpanCorrelatesOnItsEndInstantAndIsCappedAtOverlapping(t *testing.T) {
	s := rpc("aws_instance", "ReadResource", 0, 200)
	s.DurationMs = 900 // exceeds the offset, which is what clamping means
	s.StartClamped = true

	// A context covering the whole run would CONTAIN [0, 200) but does not
	// contain the end instant more tightly than any other; the cap applies
	// regardless.
	wide := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 1000)}
	got := Correlate([]span.Span{s}, base, wide)
	if got[0].Confidence != Overlapping {
		t.Errorf("Confidence = %v, want Overlapping (capped)", got[0].Confidence)
	}

	// A context that ended before the span's end instant must not match,
	// even though it overlaps the fabricated [0, 200) window.
	early := []Context{ctx("aws_instance.a", "aws_instance", "read", 0, 150)}
	got = Correlate([]span.Span{s}, base, early)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed -- a clamped span must not "+
			"match a context its fabricated start merely reaches", got[0].Confidence)
	}
}

// ReadDataSource draws only from data-source contexts and everything else
// only from managed-resource contexts. ReportedBuilder folds
// tf_data_source_type into ResourceType, so type alone cannot separate them.
func TestDataPrefixSeparatesCandidatePools(t *testing.T) {
	dataCtx := ctx("data.local_file.a", "local_file", "read", 0, 1000)
	dataCtx.IsData = true
	managedCtx := ctx("local_file.b", "local_file", "create", 0, 1000)
	ctxs := []Context{dataCtx, managedCtx}

	got := Correlate([]span.Span{rpc("local_file", "ReadDataSource", 100, 200)}, base, ctxs)
	if got[0].Address != "data.local_file.a" {
		t.Errorf("ReadDataSource attributed to %q, want the data-source context", got[0].Address)
	}

	got = Correlate([]span.Span{rpc("local_file", "ApplyResourceChange", 100, 200)}, base, ctxs)
	if got[0].Address != "local_file.b" {
		t.Errorf("ApplyResourceChange attributed to %q, want the managed context", got[0].Address)
	}
}

func TestOperationMustMatchWhereTheRPCIsMapped(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "create", 0, 1000)}
	// ReadResource maps to action "read" only, so a create context is not a
	// candidate for it.
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, ctxs)
	if got[0].Confidence != Unattributed {
		t.Errorf("Confidence = %v, want Unattributed across differing operations", got[0].Confidence)
	}
}

// An RPC name absent from the map matches any action. Attributing nothing
// because a name is unmapped would silently drop time, and the map cannot be
// complete for provider RPCs nobody has catalogued.
func TestUnmappedRPCMatchesAnyAction(t *testing.T) {
	ctxs := []Context{ctx("aws_instance.a", "aws_instance", "create", 0, 1000)}
	got := Correlate([]span.Span{rpc("aws_instance", "UpgradeResourceState", 100, 200)}, base, ctxs)
	if got[0].Confidence != Contained {
		t.Errorf("Confidence = %v, want Contained -- an unmapped RPC must not "+
			"be silently excluded", got[0].Confidence)
	}
}

func TestModuleNameAndKeyTravelWithTheAttribution(t *testing.T) {
	c := ctx(`module.m["k"].aws_instance.web[0]`, "aws_instance", "read", 0, 1000)
	c.Module, c.Name, c.Key = `module.m["k"]`, "web", "0"
	got := Correlate([]span.Span{rpc("aws_instance", "ReadResource", 100, 200)}, base, []Context{c})
	if got[0].Module != `module.m["k"]` || got[0].Name != "web" || got[0].Key != "0" {
		t.Errorf("got Module=%q Name=%q Key=%q", got[0].Module, got[0].Name, got[0].Key)
	}
}

func TestCorrelateReturnsOneAttributionPerSpan(t *testing.T) {
	spans := []span.Span{
		rpc("aws_instance", "ReadResource", 100, 200),
		rpc("aws_subnet", "ReadResource", 100, 200),
	}
	got := Correlate(spans, base, nil)
	if len(got) != len(spans) {
		t.Fatalf("len = %d, want %d -- the table must stay parallel to the span slice",
			len(got), len(spans))
	}
}

func TestConfidenceStrings(t *testing.T) {
	for c, want := range map[Confidence]string{
		Unattributed: "unattributed",
		Ambiguous:    "ambiguous",
		Overlapping:  "overlapping",
		Likely:       "likely",
		Contained:    "contained",
	} {
		if got := c.String(); got != want {
			t.Errorf("Confidence(%d).String() = %q, want %q", c, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/attrib/ -run Correlate -v`
Expected: FAIL — `undefined: Correlate`.

- [ ] **Step 3: Write the implementation**

Create `internal/attrib/correlate.go`:

```go
package attrib

import (
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Confidence records how well an address was established for a span. The
// zero value is Unattributed deliberately: an Attribution nobody filled in
// must claim nothing, not claim containment.
type Confidence uint8

const (
	// Unattributed means context was available in this log but no candidate
	// matched this span. It is distinct from "this log has no address
	// context at all", which is a property of the log rather than of a span
	// and is why no attribution table is built for such a log.
	Unattributed Confidence = iota
	// Ambiguous means several candidates overlapped and none uniquely
	// contained the span. It never carries an address.
	Ambiguous
	// Overlapping means exactly one candidate overlapped without containing
	// the span. Weaker than Likely: the geometry supports the association
	// but not the enclosure.
	Overlapping
	// Likely means several candidates overlapped and exactly one contained
	// the span. Containment is a QUALITATIVE difference between candidates,
	// which is why "materially better" needs no tuned ratio.
	Likely
	// Contained means exactly one candidate overlapped and it contained the
	// span entirely. This is the strongest verdict inference offers, and it
	// is deliberately not called "Exact" -- that is the word the extraction
	// tiers use for an OBSERVED duration, and inference does not get to
	// borrow observation's vocabulary.
	Contained
)

func (c Confidence) String() string {
	switch c {
	case Unattributed:
		return "unattributed"
	case Ambiguous:
		return "ambiguous"
	case Overlapping:
		return "overlapping"
	case Likely:
		return "likely"
	case Contained:
		return "contained"
	}
	return "unknown"
}

// Attribution labels one RPC span. It is held in a slice parallel to
// model.Log.RPCSpans specifically -- not to a concatenation of RPCSpans and
// UISpans, which model deliberately keeps apart because their StartMs sit on
// different zero points. UISpans need no attribution: their Address is
// observed.
type Attribution struct {
	// Address is "" for Ambiguous and Unattributed. An Ambiguous span
	// reports Candidates instead: naming one of several equally plausible
	// resources would assert something the evidence does not support.
	Address string
	Module  string // "" when the resource is not in a module
	Name    string
	Key     string // "" when the resource has no index key

	// Candidates is the number of overlapping candidates CONSIDERED, which
	// has a well-defined value in every state: 1 for Contained and
	// Overlapping, N for Likely and Ambiguous, 0 for Unattributed.
	Candidates uint32
	Confidence Confidence
}

// rpcActions maps a provider RPC name to the UI-hook actions it can belong
// to. An RPC absent from this map matches ANY action: this map cannot be
// complete for RPCs nobody has catalogued, and excluding an unmapped name
// would silently drop that span's time rather than reporting it as
// attributable.
var rpcActions = map[string][]string{
	"ReadResource":        {"read"},
	"ReadDataSource":      {"read"},
	"PlanResourceChange":  {"create", "update", "delete"},
	"ApplyResourceChange": {"create", "update", "delete"},
}

// actionMatches reports whether ctxAction is one this RPC can belong to.
func actionMatches(rpc, ctxAction string) bool {
	allowed, mapped := rpcActions[rpc]
	if !mapped {
		return true
	}
	for _, a := range allowed {
		if a == ctxAction {
			return true
		}
	}
	return false
}

// overlaps reports whether two half-open intervals share any instant. An
// EMPTY interval occupies no instant, so it overlaps nothing -- including
// another empty interval at the same point. Both emptiness checks are
// necessary: without them a zero-extent interval inside another would report
// an overlap it does not have.
func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	if !aStart.Before(aEnd) || !bStart.Before(bEnd) {
		return false
	}
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// contains reports whether the half-open interval [oStart, oEnd) encloses
// [iStart, iEnd). An empty interval neither contains nor is contained.
func contains(oStart, oEnd, iStart, iEnd time.Time) bool {
	if !oStart.Before(oEnd) || !iStart.Before(iEnd) {
		return false
	}
	return !iStart.Before(oStart) && !iEnd.After(oEnd)
}

// Correlate attributes each span to an address context, returning one
// Attribution per span in the same order. base is logfmt.Stats.FirstTS, the
// zero point of the spans' StartMs/EndMs; spans are converted to absolute
// times so the two inputs meet on the wall clock. No span is modified:
// model.PackLanes and PeakConcurrency refuse a mixed-fidelity slice on the
// premise that StartMs is not comparable across builders, and rewriting it
// here would falsify that premise while leaving Fidelity unchanged.
func Correlate(spans []span.Span, base time.Time, ctxs []Context) []Attribution {
	out := make([]Attribution, len(spans))
	for i, s := range spans {
		out[i] = correlateOne(s, base, ctxs)
	}
	return out
}

func correlateOne(s span.Span, base time.Time, ctxs []Context) Attribution {
	start := base.Add(time.Duration(s.StartMs) * time.Millisecond)
	end := base.Add(time.Duration(s.EndMs) * time.Millisecond)

	// A clamped span's start is fabricated -- ReportedBuilder set it to zero
	// because the duration exceeded the offset from the log's first entry --
	// so its [0, End) window would overlap nearly every context in the log
	// and could appear to be contained by several. It is correlated on its
	// end instant alone, as a one-millisecond probe, and capped at
	// Overlapping. A clamped span always has DurationMs >= 1, so the probe
	// is never empty.
	capped := s.StartClamped
	if capped {
		start = end.Add(-time.Millisecond)
	}

	var (
		n         uint32
		lone      *Context
		container *Context
		multiple  bool
	)
	for i := range ctxs {
		c := &ctxs[i]
		if c.ResourceType != s.ResourceType {
			continue
		}
		if c.IsData != (s.RPC == "ReadDataSource") {
			continue
		}
		if !actionMatches(s.RPC, c.Action) {
			continue
		}
		if !overlaps(start, end, c.Start, c.End) {
			continue
		}
		n++
		if lone == nil {
			lone = c
		}
		if contains(c.Start, c.End, start, end) {
			if container != nil {
				multiple = true
			}
			container = c
		}
	}

	switch {
	case n == 0:
		return Attribution{Confidence: Unattributed}
	case capped:
		// The cap applies whatever the geometry says: the start that would
		// justify a stronger verdict is not a fact the log states.
		if n > 1 {
			return Attribution{Candidates: n, Confidence: Ambiguous}
		}
		return named(*lone, n, Overlapping)
	case n == 1:
		if container != nil {
			return named(*lone, n, Contained)
		}
		return named(*lone, n, Overlapping)
	case container != nil && !multiple:
		return named(*container, n, Likely)
	default:
		return Attribution{Candidates: n, Confidence: Ambiguous}
	}
}

func named(c Context, n uint32, conf Confidence) Attribution {
	return Attribution{
		Address:    c.Address,
		Module:     c.Module,
		Name:       c.Name,
		Key:        c.Key,
		Candidates: n,
		Confidence: conf,
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/attrib/ -v`
Expected: PASS.

- [ ] **Step 5: Mutation-check the two interval predicates**

The confidence model is only as good as `overlaps` and `contains`. Prove the tests are load-bearing by breaking each one and confirming a test catches it. For each mutation: apply it, run `go test ./internal/attrib/`, confirm FAIL, then revert.

1. In `overlaps`, delete `if !aStart.Before(aEnd) || !bStart.Before(bEnd) { return false }`.
   Expected: `TestZeroExtentSpanOverlapsNothing` and `TestZeroExtentContextIsNeverACandidate` fail.
2. In `contains`, change `!iEnd.After(oEnd)` to `iEnd.Before(oEnd)`.
   Expected: a containment test fails (a span ending exactly at its context's end is contained).
3. In `correlateOne`, delete the `capped` branch from the `switch`.
   Expected: `TestClampedSpanCorrelatesOnItsEndInstantAndIsCappedAtOverlapping` fails.

If any mutation survives, the suite has a gap — add the test that catches it before continuing. Record which mutations you ran and what happened in your report.

- [ ] **Step 6: Run the whole suite and commit**

Run: `go build ./... && go test ./...`, then commit `internal/attrib/correlate.go` and `internal/attrib/correlate_test.go`.

---

### Task 3: The coverage statistic

Produces the number that gates view 3 and that `--diagnose` publishes.

**Files:**
- Create: `internal/attrib/coverage.go`
- Create: `internal/attrib/coverage_test.go`

**Interfaces:**
- Consumes: `Attribution`, `Confidence` from Task 2; `span.Span`.
- Produces, for Tasks 5 and 7:
  - `type Coverage struct { Spans int; TotalMs uint64; ByConfidence [5]int; MsByConfidence [5]uint64; Candidates map[uint32]int }`
  - `func Summarise(spans []span.Span, attribs []Attribution) Coverage`
  - `func (c Coverage) NameableShare() float64`

**Background.** The gate is expressed on **time, not span count**: time is what every view in this tool ranks by, and a distribution that resolves most short calls and none of the long ones has not earned a view. "Nameable" means `Contained` or `Likely` — the two verdicts that put a name on the screen.

- [ ] **Step 1: Write the failing test**

Create `internal/attrib/coverage_test.go`:

```go
package attrib

import (
	"math"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestSummariseCountsAndTimesByConfidence(t *testing.T) {
	spans := []span.Span{
		{DurationMs: 100}, {DurationMs: 200}, {DurationMs: 300},
	}
	attribs := []Attribution{
		{Confidence: Contained}, {Confidence: Ambiguous}, {Confidence: Likely},
	}
	got := Summarise(spans, attribs)

	if got.Spans != 3 {
		t.Errorf("Spans = %d, want 3", got.Spans)
	}
	if got.TotalMs != 600 {
		t.Errorf("TotalMs = %d, want 600", got.TotalMs)
	}
	if got.ByConfidence[Contained] != 1 || got.ByConfidence[Likely] != 1 || got.ByConfidence[Ambiguous] != 1 {
		t.Errorf("ByConfidence = %v", got.ByConfidence)
	}
	if got.MsByConfidence[Contained] != 100 {
		t.Errorf("MsByConfidence[Contained] = %d, want 100", got.MsByConfidence[Contained])
	}
	if got.MsByConfidence[Likely] != 300 {
		t.Errorf("MsByConfidence[Likely] = %d, want 300", got.MsByConfidence[Likely])
	}
}

// The gate is on TIME, not on span count. This fixture inverts the two: two
// of three spans are nameable, but they carry only a tenth of the time.
func TestNameableShareIsTimeWeightedNotCountWeighted(t *testing.T) {
	spans := []span.Span{
		{DurationMs: 50}, {DurationMs: 50}, {DurationMs: 900},
	}
	attribs := []Attribution{
		{Confidence: Contained}, {Confidence: Likely}, {Confidence: Ambiguous},
	}
	got := Summarise(spans, attribs).NameableShare()
	if math.Abs(got-0.1) > 1e-9 {
		t.Errorf("NameableShare = %v, want 0.1 -- a count-weighted share would be 0.666", got)
	}
}

func TestNameableShareExcludesOverlapping(t *testing.T) {
	spans := []span.Span{{DurationMs: 100}, {DurationMs: 100}}
	attribs := []Attribution{{Confidence: Contained}, {Confidence: Overlapping}}
	if got := Summarise(spans, attribs).NameableShare(); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("NameableShare = %v, want 0.5", got)
	}
}

func TestNameableShareOfNothingIsZeroNotNaN(t *testing.T) {
	if got := Summarise(nil, nil).NameableShare(); got != 0 {
		t.Errorf("NameableShare = %v, want 0", got)
	}
}

func TestCandidateHistogram(t *testing.T) {
	spans := []span.Span{{DurationMs: 1}, {DurationMs: 1}, {DurationMs: 1}}
	attribs := []Attribution{
		{Candidates: 1, Confidence: Contained},
		{Candidates: 4, Confidence: Ambiguous},
		{Candidates: 4, Confidence: Ambiguous},
	}
	got := Summarise(spans, attribs).Candidates
	if got[1] != 1 || got[4] != 2 {
		t.Errorf("Candidates = %v, want {1:1, 4:2}", got)
	}
}

// Summarise must not be handed mismatched slices, but if it is, it must not
// index out of range: the parallel-table invariant is the caller's to keep
// and a panic here would blame the wrong code.
func TestSummariseStopsAtTheShorterSlice(t *testing.T) {
	got := Summarise([]span.Span{{DurationMs: 5}, {DurationMs: 5}}, []Attribution{{Confidence: Contained}})
	if got.Spans != 1 {
		t.Errorf("Spans = %d, want 1", got.Spans)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/attrib/ -run 'Summarise|Nameable|Candidate' -v`
Expected: FAIL — `undefined: Summarise`.

- [ ] **Step 3: Write the implementation**

Create `internal/attrib/coverage.go`:

```go
package attrib

import "github.com/yesdevnull/tf-log-inspector/internal/span"

// confidenceCount is how many Confidence values there are. Kept as a named
// constant next to the arrays it sizes so adding a value forces this to be
// updated rather than silently truncating a distribution.
const confidenceCount = 5

// Coverage is the published distribution of attribution outcomes. It is what
// --diagnose reports and what gates view 3, and it carries both a count and
// a TIME total per confidence value -- the two answer different questions and
// a distribution that resolved every short call and no long one would look
// healthy by count alone.
type Coverage struct {
	Spans   int
	TotalMs uint64

	ByConfidence   [confidenceCount]int
	MsByConfidence [confidenceCount]uint64

	// Candidates maps a candidate count to how many spans saw that many.
	// It is counts of candidates, never the candidates themselves, so it is
	// safe for --diagnose's masked output.
	Candidates map[uint32]int
}

// Summarise builds the distribution over a span slice and its parallel
// attribution table. It stops at the shorter of the two: keeping them
// parallel is the caller's invariant, and panicking here would blame this
// code for a mistake made elsewhere.
func Summarise(spans []span.Span, attribs []Attribution) Coverage {
	c := Coverage{Candidates: make(map[uint32]int)}
	n := min(len(spans), len(attribs))
	for i := 0; i < n; i++ {
		s, a := spans[i], attribs[i]
		c.Spans++
		c.TotalMs += uint64(s.DurationMs)
		if int(a.Confidence) < confidenceCount {
			c.ByConfidence[a.Confidence]++
			c.MsByConfidence[a.Confidence] += uint64(s.DurationMs)
		}
		c.Candidates[a.Candidates]++
	}
	return c
}

// NameableShare is the share of span TIME that resolved to a verdict which
// puts a name on the screen -- Contained or Likely. Overlapping is excluded:
// it names an address, but on evidence too weak to build a ranked view on.
//
// This is the number phase 5's gate is expressed against: view 3 ships as a
// view when it is at least 0.5. That threshold is a judgement recorded in
// the spec, not a measurement.
func (c Coverage) NameableShare() float64 {
	if c.TotalMs == 0 {
		return 0
	}
	nameable := c.MsByConfidence[Contained] + c.MsByConfidence[Likely]
	return float64(nameable) / float64(c.TotalMs)
}
```

- [ ] **Step 4: Run to verify it passes, then run the whole suite**

Run: `go test ./internal/attrib/ -v && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

Commit `internal/attrib/coverage.go` and `internal/attrib/coverage_test.go`.

---

### Task 4: Wire attribution into the model

Runs the collector in the existing single scan and hangs the results off `model.Log`, so both `--profile`/TUI and `--diagnose` can reach them.

**Files:**
- Modify: `internal/model/log.go` — the `Log` struct and `Load`
- Modify: `internal/model/log_test.go`

**Interfaces:**
- Consumes: `attrib.ContextCollector`, `attrib.Context`, `attrib.Attribution`, `attrib.Correlate` from Tasks 1–2. Existing `logfmt.Scan(r, comps, sinks...)`.
- Produces, for Tasks 5–7: `Log.Contexts []attrib.Context`, `Log.Attribs []attrib.Attribution`, `Log.HasAddressContext() bool`.

**Background.** `Load` already runs four sinks in one pass (`idx`, `sniffer`, `&rb`, `&ub`). Adding a fifth costs one more interface call per line and no extra read. Read `internal/model/log.go:49-80` before editing.

**Attribution runs only when the log has address context.** Below one completed context pair there is nothing to correlate against, and `Log.Attribs` stays nil rather than being filled with `Unattributed` — that is the "no context" state, a property of the log rather than of any span.

- [ ] **Step 1: Write the failing test**

Add to `internal/model/log_test.go`:

```go
func TestLoadAttributesRPCSpansToAddresses(t *testing.T) {
	l, err := Load(fixture(t, "two-tier.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !l.HasAddressContext() {
		t.Fatal("HasAddressContext = false, want true for a log carrying terraform.ui hook pairs")
	}
	if len(l.Attribs) != len(l.RPCSpans) {
		t.Fatalf("len(Attribs) = %d, len(RPCSpans) = %d -- the table must stay parallel",
			len(l.Attribs), len(l.RPCSpans))
	}
}

func TestLoadBuildsNoAttributionTableWithoutContext(t *testing.T) {
	// A log with no terraform.ui stream has no address context at all, which
	// is a property of the LOG. The table is not allocated rather than being
	// filled with Unattributed, so "this log cannot answer the question" and
	// "this log did not answer it for this span" stay distinguishable.
	l, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if l.HasAddressContext() {
		t.Error("HasAddressContext = true, want false")
	}
	if l.Attribs != nil {
		t.Errorf("Attribs = %v, want nil", l.Attribs)
	}
}

// Attribution must never rewrite a span's timeline. model.PackLanes refuses a
// mixed-fidelity slice on the premise that StartMs is not comparable across
// builders, and that premise is keyed on Fidelity -- so an in-place re-base
// would falsify it invisibly.
func TestLoadDoesNotRewriteSpanTimelines(t *testing.T) {
	path := fixture(t, "two-tier.log")

	// The control: what ReportedBuilder produces with no attribution in the
	// scan at all. Comparing Load against ITSELF would pass even if both
	// runs re-based identically, which is exactly the bug being excluded.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	comps := &logfmt.Interner{}
	var rb span.ReportedBuilder
	rb.Comps = comps
	if _, err := logfmt.Scan(bytes.NewReader(data), comps, &rb); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := rb.Spans()

	l, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(l.RPCSpans) != len(want) {
		t.Fatalf("Load built %d spans, control built %d", len(l.RPCSpans), len(want))
	}
	for i, got := range l.RPCSpans {
		if got.StartMs != want[i].StartMs || got.EndMs != want[i].EndMs {
			t.Fatalf("span %d timeline = [%d,%d), want [%d,%d) -- attribution "+
				"must correlate on local copies, never rewrite the span",
				i, got.StartMs, got.EndMs, want[i].StartMs, want[i].EndMs)
		}
	}
}
```

**Before writing this test, confirm the two fixture names exist.** Run `ls testdata/*.log`. If `two-tier.log` or `reported.log` is named differently, use the actual names — a fixture that carries both streams for the first test, and one with no `terraform.ui` lines for the second. Do not invent a fixture name.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/model/ -run Attribut -v`
Expected: FAIL — `l.HasAddressContext undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/model/log.go`, add to the `Log` struct after `Caps`:

```go
	// Contexts and Attribs are the address-attribution layer. Attribs is
	// parallel to RPCSpans specifically -- never to a concatenation of
	// RPCSpans and UISpans, which are kept apart above. UISpans need no
	// attribution: their Address is observed, not inferred.
	//
	// Attribs is nil when the log carries no address context at all, which
	// is a different fact from a span that context failed to resolve. See
	// HasAddressContext.
	Contexts []attrib.Context
	Attribs  []attrib.Attribution
```

In `Load`, add the collector to the scan and correlate after it:

```go
	var cc attrib.ContextCollector

	stats, err := logfmt.Scan(bytes.NewReader(data), comps, idx, sniffer, &rb, &ub, &cc)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}

	rpcSpans := rb.Spans()
	// Attribution runs only where there is something to correlate against.
	// Below one completed context pair the answer for every span would be
	// the same, and recording it per span would make a property of the LOG
	// look like a property of each call.
	var (
		ctxs    []attrib.Context
		attribs []attrib.Attribution
	)
	if cc.CompletedPairs() > 0 {
		ctxs = cc.Contexts()
		attribs = attrib.Correlate(rpcSpans, stats.FirstTS, ctxs)
	}

	return &Log{
		Data:     data,
		Entries:  idx.entries,
		Comps:    comps,
		Stats:    stats,
		RPCSpans: rpcSpans,
		UISpans:  ub.Spans(),
		Caps:     sniffer.Report(),
		Contexts: ctxs,
		Attribs:  attribs,
	}, nil
```

Add the accessor below `Bytes`:

```go
// HasAddressContext reports whether this log carries any address context at
// all. It is the difference between "this log cannot answer which resource a
// call belongs to" and "this log can, and did not for this call" -- two facts
// the interface must not present in the same words.
func (l *Log) HasAddressContext() bool { return len(l.Attribs) > 0 }
```

Add `"github.com/yesdevnull/tf-log-inspector/internal/attrib"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/model/ -v`
Expected: PASS.

- [ ] **Step 5: Run the whole suite**

Run: `go build ./... && go test ./...`
Expected: all packages pass. If `--profile`'s digest tests fail, STOP — `Load` must not have changed any existing output. Report it rather than re-baselining.

- [ ] **Step 6: Commit**

---

### Task 5: `--diagnose` publishes the measurement

Adds the four outputs the spec requires, all masked and shareable.

**Files:**
- Modify: `internal/diagnose/diagnose.go` — `Report`, `Build`, the render function
- Modify: `internal/diagnose/diagnose_test.go`
- Modify: `cmd/tfli/main.go` — `runDiagnose` wires the collector in

**Interfaces:**
- Consumes: `attrib.ContextCollector`, `attrib.Coverage`, `attrib.Summarise`, `attrib.Correlate` from Tasks 1–3.
- Produces: nothing later tasks consume. Task 7's gate reads this output by eye.

**Background.** `--diagnose` output is the only channel through which facts about real logs reach development, and it is masked for sharing. **No address may appear in it.** The histogram is type names and counts; the coverage figures are counts and totals; the candidate distribution is counts of candidates, never the candidates.

**Where the counters live matters.** `logfmt.StructuredSink`'s doc comment states the disclosure guarantee as a property of construction: `diagnose.Collector` deliberately does not implement that interface, so no structured-line content can reach a template. Do **not** make `Collector` implement `StructuredSink`. The hook-type histogram comes from `attrib.ContextCollector`, which already does.

Read `internal/diagnose/diagnose.go`'s `Build` signature (line 235) and the render function around line 538 before editing.

- [ ] **Step 1: Write the failing test**

Add to `internal/diagnose/diagnose_test.go`:

```go
func TestReportRendersHookTypeHistogram(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	if !strings.Contains(out, "hook types") {
		t.Errorf("report has no hook-type histogram:\n%s", out)
	}
	if !strings.Contains(out, "apply_start") {
		t.Errorf("histogram does not name apply_start:\n%s", out)
	}
}

func TestReportRendersCoverageAndConfidence(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	for _, want := range []string{"nameable share", "contained", "likely", "ambiguous", "unattributed"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

func TestReportRendersBaselineOffsetAsAConstantNotACheck(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	if !strings.Contains(out, "stream offset") {
		t.Errorf("report has no baseline offset:\n%s", out)
	}
	// The offset is the re-basing constant, not a verification of clock
	// agreement -- it cannot be one, because the two baselines mark
	// different events. Wording that claims otherwise is the defect.
	for _, forbidden := range []string{"clocks agree", "clock check", "verified"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("report claims the offset checks clock agreement (%q):\n%s", forbidden, out)
		}
	}
}

// The disclosure guarantee: --diagnose is the one output Dan shares, and no
// resource address may appear in it.
func TestReportNeverPrintsAnAddress(t *testing.T) {
	out := renderFixture(t, fixture(t, "two-tier.log"))
	l, err := model.Load(fixture(t, "two-tier.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Contexts) == 0 {
		t.Fatal("fixture carries no contexts; this test proves nothing")
	}
	for _, c := range l.Contexts {
		if c.Address != "" && strings.Contains(out, c.Address) {
			t.Errorf("report printed the address %q", c.Address)
		}
	}
}
```

**`internal/diagnose/diagnose_test.go` has no fixture-path helper** — unlike `internal/model/log_test.go`, which defines `fixture(t, name) string` returning `filepath.Join("..", "..", "testdata", name)`. Add the same helper to the diagnose test file, and a `renderFixture(t, path) string` that loads the fixture through the same sinks `runDiagnose` uses and returns the rendered report. Check the file for an existing render helper first and reuse it rather than adding a second.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/diagnose/ -run 'Histogram|Coverage|Baseline|NeverPrints' -v`
Expected: FAIL — the strings are absent.

- [ ] **Step 3: Extend `Report` and `Build`**

Add to the `Report` struct, after the UI-hook figures:

```go
	// Address-attribution figures. Counts and totals only -- no address
	// appears in this report, which is the one output that leaves the
	// machine.
	HookTypes      []Template      // structured-output "type" values and counts, recurring only
	WithheldHookTypes uint64       // distinct types withheld for appearing once
	Coverage       attrib.Coverage
	Contexts       int  // address context windows collected
	HasContext     bool
	StreamOffsetMs int64 // logfmt.Stats.FirstTS to the structured stream's first timestamp
```

Extend `Build`'s signature with the collector and correlated table. Keep the parameter list ordered so the new arguments follow the existing UI-hook ones:

```go
func Build(st logfmt.Stats, caps span.Capabilities, spans []span.Span, uiSpans []span.Span,
	uiMalformed, uiBackwards, uiSaturated uint64, cc *attrib.ContextCollector,
	c *Collector, comps *logfmt.Interner, elapsed time.Duration) Report {
```

Inside `Build`, after the existing UI-hook figures:

```go
	hookTypes, withheldHookTypes := recurringTopN(cc.TypeCounts(), maxTemplates)
	r.HookTypes, r.WithheldHookTypes = hookTypes, withheldHookTypes
	if cc.CompletedPairs() > 0 {
		r.HasContext = true
		r.Contexts = len(cc.Contexts())
		r.Coverage = attrib.Summarise(spans, attrib.Correlate(spans, st.FirstTS, cc.Contexts()))
		if !st.FirstTS.IsZero() && !cc.FirstTS().IsZero() {
			r.StreamOffsetMs = cc.FirstTS().Sub(st.FirstTS).Milliseconds()
		}
	}
```

`recurringTopN(m map[string]uint64, n int) ([]Template, uint64)` is at `diagnose.go:417` and is already used at lines 237–239. `TypeCounts()` returns `map[string]uint64` so it feeds straight in. Note that `recurringTopN` withholds entries seen fewer than twice, which is the report's standing rule and applies here too.

- [ ] **Step 4: Render the new section**

In the render function, after the UI-hook block, add:

```go
	fmt.Fprintf(b, "\nADDRESS ATTRIBUTION\n")
	if !r.HasContext {
		fmt.Fprintf(b, "  no address context in this log -- attribution needs the\n")
		fmt.Fprintf(b, "  terraform.ui stream (debug toggle plus TF_LOG_PROVIDER and\n")
		fmt.Fprintf(b, "  TF_LOG_SDK_PROTO at TRACE)\n")
	} else {
		fmt.Fprintf(b, "  %-25s %d\n", "contexts", r.Contexts)
		fmt.Fprintf(b, "  %-25s %.1f%%\n", "nameable share", r.Coverage.NameableShare()*100)
		for _, c := range []attrib.Confidence{
			attrib.Contained, attrib.Likely, attrib.Overlapping,
			attrib.Ambiguous, attrib.Unattributed,
		} {
			fmt.Fprintf(b, "  %-25s %d spans, %d ms\n", c.String(),
				r.Coverage.ByConfidence[c], r.Coverage.MsByConfidence[c])
		}
		// The offset is the constant that re-bases the two streams onto one
		// clock. It is NOT a check on clock agreement: the two baselines
		// mark different events -- the hclog stream brackets the whole
		// Terraform process, terraform.ui only the plan phase within it --
		// so their difference is real elapsed time plus any skew, and the
		// two terms are not separable.
		fmt.Fprintf(b, "  %-25s %d ms\n", "stream offset", r.StreamOffsetMs)
	}
```

Render `HookTypes` in the same style the existing `TopTemplates` block uses — read that block and match it, including how it reports the withheld count.

- [ ] **Step 5: Wire `runDiagnose`**

In `cmd/tfli/main.go`, add `var cc attrib.ContextCollector`, pass `&cc` to `logfmt.Scan` alongside the existing sinks, and pass `&cc` to `diagnose.Build` in its new position.

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/diagnose/ ./cmd/tfli/ -v`
Expected: PASS. Existing `--diagnose` golden or digest tests will fail because the report gained a section — read the new output in full and confirm every line is correct and carries no address before updating any expectation.

- [ ] **Step 7: Read the actual output**

Run: `go run ./cmd/tfli --diagnose testdata/two-tier.log`

Read it. Confirm: the histogram names real hook types; the confidence lines sum to the span count; the nameable share is a plausible percentage; no address appears anywhere. A number that looks wrong is a finding, not a rounding artefact — report it.

- [ ] **Step 8: Run the whole suite and commit**

---

### Task 6: The Calls detail pane names the resource

The phase's acceptance criterion. Also closes the standing defect that `spanDetailLines` never renders `ResourceType` while `callColumns` has a resource-type column.

**Files:**
- Modify: `internal/tui/layout.go:926-939` — `spanDetailLines`
- Modify: `internal/tui/model.go` — pass the attribution to the detail pane
- Modify: `internal/tui/layout_test.go`
- Modify: `internal/tui/testdata/golden/*` — regenerate affected goldens

**Interfaces:**
- Consumes: `attrib.Attribution`, `attrib.Confidence` from Task 2; `Log.Attribs` from Task 4.
- Produces: nothing later tasks consume.

**Background — the pane is narrow.** `maxDetailPaneWidth = 40`, `minDetailPaneWidth = 19`, and below `detailInlineWidth = 70` the detail pane is not drawn at all. A real address (`module.vpc["datacenter1"].aws_internet_gateway.this[0]`) runs past 50 characters. This is why the resource **name** and the **module** are separate fields rather than one address line: the name is the identifying part and must survive clipping, while the module path is what gives way.

Field kinds route clipping: `tailIdentifierColumn` keeps the tail (use it for the module path, whose tail distinguishes siblings), `headIdentifierColumn` keeps the head (use it for the resource name).

Read `spanDetailLines` and `detailFieldLines` (`layout.go:1033`) before editing.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/layout_test.go`:

```go
func TestSpanDetailNamesTheResource(t *testing.T) {
	s := span.Span{RPC: "ReadResource", Provider: "registry.terraform.io/hashicorp/aws",
		ResourceType: "aws_instance", DurationMs: 1200, Fidelity: span.FidelityReported}
	a := attrib.Attribution{
		Address: `module.m["k"].aws_instance.web[0]`, Module: `module.m["k"]`,
		Name: "web", Key: "0", Candidates: 1, Confidence: attrib.Contained,
	}
	got := strings.Join(spanDetailLines(s, a, true, 40), "\n")

	for _, want := range []string{"web", `module.m["k"]`, "aws_instance"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail pane missing %q:\n%s", want, got)
		}
	}
}

// The standing defect this task closes: callColumns has a resource-type
// column and the detail pane never rendered it, so the pane showed LESS than
// the row it describes.
func TestSpanDetailRendersResourceType(t *testing.T) {
	s := span.Span{RPC: "ReadResource", ResourceType: "aws_instance", DurationMs: 5}
	got := strings.Join(spanDetailLines(s, attrib.Attribution{}, false, 40), "\n")
	if !strings.Contains(got, "aws_instance") {
		t.Errorf("detail pane omits ResourceType:\n%s", got)
	}
}

// An Ambiguous span states a count and names no resource.
func TestSpanDetailAmbiguousStatesACountAndNamesNothing(t *testing.T) {
	s := span.Span{RPC: "ReadResource", ResourceType: "azuread_service_principal", DurationMs: 5}
	a := attrib.Attribution{Candidates: 4, Confidence: attrib.Ambiguous}
	got := strings.Join(spanDetailLines(s, a, true, 40), "\n")
	if !strings.Contains(got, "4 candidates") {
		t.Errorf("detail pane does not state the candidate count:\n%s", got)
	}
	if strings.Contains(got, "azuread_service_principal.") {
		t.Errorf("detail pane named a resource for an Ambiguous span:\n%s", got)
	}
}

// Two facts the interface must not state in the same words.
func TestSpanDetailDistinguishesNoContextFromUnattributed(t *testing.T) {
	s := span.Span{RPC: "ReadResource", ResourceType: "aws_instance", DurationMs: 5}

	noContext := strings.Join(spanDetailLines(s, attrib.Attribution{}, false, 40), "\n")
	unattributed := strings.Join(
		spanDetailLines(s, attrib.Attribution{Confidence: attrib.Unattributed}, true, 40), "\n")

	if noContext == unattributed {
		t.Error("no-context and unattributed render identically; they are different facts")
	}
}

// The name is the identifying part and must survive a narrow pane; the
// module path is what gives way.
func TestSpanDetailKeepsTheNameWhenThePaneIsNarrow(t *testing.T) {
	a := attrib.Attribution{
		Address: `module.very_long_module_name_here.aws_instance.web`,
		Module:  "module.very_long_module_name_here",
		Name:    "web", Confidence: attrib.Contained,
	}
	s := span.Span{RPC: "ReadResource", ResourceType: "aws_instance", DurationMs: 5}
	got := strings.Join(spanDetailLines(s, a, true, 19), "\n")
	if !strings.Contains(got, "web") {
		t.Errorf("the resource name did not survive a %d-column pane:\n%s", 19, got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run SpanDetail -v`
Expected: FAIL — `spanDetailLines` takes two arguments, not four.

- [ ] **Step 3: Rewrite `spanDetailLines`**

Replace `internal/tui/layout.go:926-939`:

```go
// spanDetailLines is what the detail pane shows for one span. a is the
// span's attribution and hasContext says whether the LOG carries any address
// context at all -- two different facts, which must not render as the same
// sentence: "this log cannot answer which resource this call belongs to" is
// not "this log can, and did not for this call".
//
// The resource name and the module path are separate fields rather than one
// address line because the pane is at most maxDetailPaneWidth columns and an
// address routinely exceeds that. Splitting them lets the NAME -- the
// identifying part -- survive while the module path gives way.
func spanDetailLines(s span.Span, a attrib.Attribution, hasContext bool, w int) []string {
	fields := []detailField{
		{label: "RPC", value: s.RPC, kind: headIdentifierColumn},
		{label: "Type", value: s.ResourceType, kind: tailIdentifierColumn},
		{label: "Prov", value: s.Provider, kind: tailIdentifierColumn},
		{label: "Dur", value: formatMs(uint64(s.DurationMs)), kind: numericColumn},
	}
	if s.StartClamped {
		fields = append(fields, detailField{label: "Start", value: clampedStartValue, kind: headIdentifierColumn})
	}
	if s.Fidelity == span.FidelityUIReported {
		// An observed address, stated by the log rather than inferred from
		// it, so it carries no confidence marker.
		fields = append(fields, detailField{label: "Addr", value: s.Address, kind: tailIdentifierColumn})
		return detailFieldLines(fields, w)
	}
	return detailFieldLines(append(fields, attributionFields(a, hasContext)...), w)
}

// attributionFields renders the inferred half of a span's identity. Every
// value here is inference and is marked as such: an Ambiguous span reports
// how many candidates there were and names none of them, because naming one
// of several equally plausible resources asserts what the evidence does not
// support.
func attributionFields(a attrib.Attribution, hasContext bool) []detailField {
	if !hasContext {
		return []detailField{{label: "Res", value: noAddressContextValue, kind: headIdentifierColumn}}
	}
	switch a.Confidence {
	case attrib.Ambiguous:
		return []detailField{
			{label: "Res", value: fmt.Sprintf("%d candidates", a.Candidates), kind: headIdentifierColumn},
			{label: "Attr", value: a.Confidence.String(), kind: headIdentifierColumn},
		}
	case attrib.Unattributed:
		return []detailField{{label: "Res", value: unattributedValue, kind: headIdentifierColumn}}
	}

	name := a.Name
	if a.Key != "" {
		name += "[" + a.Key + "]"
	}
	fields := []detailField{
		{label: "Res", value: name, kind: headIdentifierColumn},
	}
	if a.Module != "" {
		fields = append(fields, detailField{label: "Mod", value: a.Module, kind: tailIdentifierColumn})
	}
	return append(fields, detailField{label: "Attr", value: a.Confidence.String(), kind: headIdentifierColumn})
}

// noAddressContextValue and unattributedValue say two different things. The
// first is a fact about the LOG -- it carries no terraform.ui stream, so no
// call in it can be attributed. The second is a fact about this CALL -- the
// log could answer the question and did not answer it here.
const (
	noAddressContextValue = "no address context in log"
	unattributedValue     = "not matched"
)
```

Add `"fmt"` and the `attrib` import if absent.

- [ ] **Step 4: Update the call site**

Find `spanDetailLines`'s caller in `internal/tui/layout.go` or `model.go` (`grep -n spanDetailLines internal/tui/`) and pass the attribution for the selected span. The row carries `spanIdx` into `m.log.RPCSpans`, and `m.log.Attribs` is parallel to that slice, so the attribution is `m.log.Attribs[spanIdx]` — **guarded**, because `Attribs` is nil when the log has no context:

```go
	var a attrib.Attribution
	if idx < len(m.log.Attribs) {
		a = m.log.Attribs[idx]
	}
	lines := spanDetailLines(m.log.RPCSpans[idx], a, m.log.HasAddressContext(), w)
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/tui/ -run SpanDetail -v`
Expected: PASS.

- [ ] **Step 6: Regenerate and READ the goldens**

Run: `go test ./internal/tui/ -update`

Then read every changed golden file in full — `git diff internal/tui/testdata/golden/`. The detail pane gained a `Type` row and up to three attribution rows, so panes get taller and the list pane may lose rows. Confirm at each width that: nothing is clipped mid-word into something that reads as a complete phrase; the list still shows rows; the 70-column golden still collapses the detail pane entirely.

**Do not commit a golden you have not read.** A golden that changed because behaviour changed needs a human deciding the change is right, which is why `-update` exists but is never run blind.

- [ ] **Step 7: Run the whole suite**

Run: `go build ./... && go test ./...`

- [ ] **Step 8: Render the real interface**

Run: `go run ./cmd/tfli testdata/two-tier.log`, press `4` for Calls, and move the selection through several rows at your terminal's width. Read the detail pane. Confirm a named resource reads as a name, an ambiguous one states a count, and nothing claims more than it should. Quit with `q`.

This step has caught user-visible defects in this project that no automated test and no agent review did. Do not skip it.

- [ ] **Step 9: Commit**

---

### Task 7: View 3 — GATED

**STOP. This task has a human gate. Do not begin it without clearing the gate.**

**The gate.** View 3 ships as a view only if at least half of attributable RPC span time resolves to `Contained` or `Likely`. That number comes from Task 5's `nameable share` line, measured against a **real capture**, not a fixture. Fixtures are synthesised and their coverage number means nothing about a real plan.

- [ ] **Step 1: Clear the gate**

Ask Dan to run `tfli --diagnose <his real capture>` and report the `ADDRESS ATTRIBUTION` block, in particular `nameable share`.

- **If `nameable share` >= 50%:** proceed to step 2.
- **If below 50%:** STOP. Do not build the view. Report the measured distribution, note that the spec's threshold was recorded as a judgement and may be worth revising against the real number, and let Dan decide. Record the outcome in the spec with a date, as the spec's phasing entry instructs.

Either outcome completes this task. A stop here is a result, not a failure — the phase's acceptance criterion shipped in Task 6.

- [ ] **Step 2: Only if the gate cleared — write the plan for the view**

The view's requirements are in the spec's `### Views` section: one row per address; an `obs`/`inf` column marking where the address came from, in a column of its own rather than as a confidence letter; `(ambiguous)` and `(unattributed)` rows for time that could not be attributed to one address, never divided across candidates; the sort key named in the view's header, because a structured-output-only capture has observed addresses and zero RPC spans; and the observed duration column labelled as whole-second quantised, with ranking never keyed on it while attributed RPC time exists.

Implementation notes for whoever builds it: `View` in `internal/tui/model.go` already reserves the slot — its comment says view 3 "belongs to a later phase and has no key bound to it yet", and `ViewCalls`/`ViewTimeline` take the keys either side. Add `ViewAddresses` between `ViewTypes` and `ViewCalls` so the enum order matches the key order, add its `viewBinding` with `key: "3"`, add its case to `rows()`, and add a column list beside `providerColumns`/`typeColumns`/`callColumns`. `TestEveryViewHasABinding` will fail until the binding exists, which is the sweep working as intended.

Write this as its own plan document before building it: it is a view's worth of work — rows, columns, detail pane, empty state, four goldens — and it deserves its own task decomposition rather than being crammed into one step here.

---

## Self-review

**Spec coverage.** Mechanism B context collection → Task 1. Confidence model, interval convention, zero-extent, clamped starts, `data.` pools, operation map → Task 2. Coverage statistic and the gate's number → Task 3. Parallel table, no span rewriting, `No context` as a log property → Task 4. Hook-type histogram, coverage, confidence distribution, baseline offset, disclosure → Task 5. Detail pane naming the resource, `ResourceType` gap, ambiguity display, narrow-pane behaviour → Task 6. View 3 → Task 7, gated as the spec requires. Mechanism A is deferred by the spec and has no task, which is correct.

**Not covered, deliberately:** the spec's view-3 requirements are enumerated in Task 7 but not decomposed into steps, because Task 7 may not run. If it does, it gets its own plan.

**Type consistency.** `Context` (Task 1) is consumed by `Correlate` (Task 2) and `Log.Contexts` (Task 4). `Attribution`/`Confidence` (Task 2) are consumed by `Summarise` (Task 3), `Log.Attribs` (Task 4), `Build` (Task 5) and `spanDetailLines` (Task 6). `Coverage.NameableShare()` (Task 3) is the gate's number in Tasks 5 and 7. `ContextCollector.CompletedPairs()` (Task 1) is attribution's precondition in Tasks 4 and 5. Names match across all of them.

