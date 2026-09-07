# Request-Id Scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pressing `⏎` on a call in the CALLS or TIMELINE view scopes the raw log to that call's own log lines, matched by Terraform's `tf_req_id`, so "open this call" shows the call rather than the whole log parked on one line of it.

**Architecture:** `logfmt.Scan` interns each entry's `tf_req_id` into a second `Interner` and records it as `Entry.ReqID uint16`, placed in the struct's existing tail padding so the entry index stays 24 bytes. `span.Span` copies that id verbatim — never re-interning, since ids compare only within one interner. The TUI materialises a scope as an ascending slice of entry indices when `⏎` creates one, and render, scroll and search iterate that slice rather than the whole log; the facet filter still applies per member, so the two stack.

**Tech Stack:** Go 1.25, bubbletea/lipgloss (TUI), no new dependencies. `scripts/mutate.sh` for mutation testing, `scripts/read-golden.sh` for reading golden frames.

**Spec:** `docs/superpowers/specs/2026-09-03-tf-log-inspector-design.md`, section **"### Scoping the raw log to one call"**. Read it in full before Task 1 — every number in this plan is measured there, and the section records which design alternatives were rejected and why.

## Global Constraints

- **Never invent technical details.** Every measured claim in a comment must be verified against the code or a `--diagnose` figure before it is written.
- **Comments state WHAT and WHY, never what changed.** No "this replaced", "used to", "now", "before".
- **Australian/British spelling** in all prose and comments.
- **TDD**: write the failing test, run it, see it fail for the right reason, then implement.
- **Mutation testing is the standard of evidence.** A mutation "CAUGHT" by a **build failure** proves nothing — re-probe with one that compiles. A mutation caught only by a golden (`TestGolden*`) is weakly held, because a golden's failure is answered by regenerating it: always attribute catches to a named non-golden test by re-running with `-skip 'TestGolden'`.
- **Every fixture under `testdata/` is synthesised or quoted from a named public source, with a header saying which.** Never copy lines from any real capture.
- **Git:** use `/Users/dan/.claude/bin/claude-git` for every git command. Commit messages via `-F <file>`, never multi-line `-m`. Do not push.
- **Verify before every commit:** `gofmt -l .` silent, `go build ./...`, `go vet ./...`, `go test ./...` green across all 8 packages.
- `Entry` **must stay 24 bytes.** Task 1 adds the assertion; no later task may break it.
- The raw log's action line budget is **70 columns** (`detailInlineWidth`). The scoped line is measured at 66 in Task 8.

---

### Task 1: `Entry.ReqID`, interned by `Scan`

**Files:**
- Modify: `internal/logfmt/entry.go` (the `Entry` struct, lines 19-27)
- Modify: `internal/logfmt/scan.go` (`Scan`'s signature and the header branch)
- Modify: `cmd/tfli/main.go:150`, `internal/model/log.go:79` (the two production call sites)
- Modify: every `Scan(` call in `internal/logfmt/*_test.go`, `internal/span/*_test.go`, `internal/diagnose/*_test.go` (31 test call sites)
- Test: `internal/logfmt/entry_test.go`, `internal/logfmt/scan_test.go`

**Interfaces:**
- Produces: `Entry.ReqID uint16` — the interned `tf_req_id`, 0 meaning none.
- Produces: `func Scan(r io.Reader, comps, reqIDs *Interner, sinks ...Sink) (Stats, error)`.

- [ ] **Step 1: Write the failing size test**

In `internal/logfmt/entry_test.go`:

```go
// Entry must stay 24 bytes. model.Log holds one per logical entry for the
// whole session, so its width is the index's resident cost: the spec's
// Sizing paragraph budgets ~190MB for a 1GB log at 24 bytes, and 32 would
// make that ~254MB. The field order is what keeps it there -- Timestamped
// sits in the byte after Level, which leaves ReqID the tail padding -- and
// Go does not reorder fields, so the order is load-bearing rather than
// stylistic.
func TestEntryStaysTwentyFourBytes(t *testing.T) {
	if got := unsafe.Sizeof(Entry{}); got != 24 {
		t.Errorf("unsafe.Sizeof(Entry{}) = %d, want 24 -- check the field order before widening a field", got)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/logfmt/ -run TestEntryStaysTwentyFourBytes -v`
Expected: PASS (Entry is 24 bytes today). This test is written FIRST so it
guards the reorder in Step 3 rather than being written to fit the result.

- [ ] **Step 3: Reorder the struct and add the field**

In `internal/logfmt/entry.go`, replace the struct with:

```go
type Entry struct {
	Off         uint64 // byte offset of the entry's first line
	Len         uint32 // bytes covering all lines of the entry
	TSms        uint32 // milliseconds since the first timestamped entry
	Level       Level
	Timestamped bool   // false for interleaved non-hclog content
	Comp        uint16 // interned component; 0 means none
	Lines       uint16 // physical line count, saturating
	// ReqID is the interned tf_req_id of the call this entry belongs to,
	// 0 meaning none -- the empty string interns to 0, so the absent case
	// needs no separate flag.
	//
	// It is interned in a DIFFERENT Interner from Comp. Ids compare only
	// within one interner, and the two vocabularies cannot share: request
	// ids are per-call where components are a handful, so one would exhaust
	// the 65534-id space the other needs, and Lookup could not say which
	// vocabulary an id belonged to.
	//
	// Its position is load-bearing. Timestamped sits in the byte after
	// Level, which leaves this the struct's tail padding and keeps Entry at
	// 24 bytes; appended after Timestamped instead it rounds the struct to
	// 32. TestEntryStaysTwentyFourBytes holds that.
	ReqID uint16
}
```

- [ ] **Step 4: Run the size test again**

Run: `go test ./internal/logfmt/ -run TestEntryStaysTwentyFourBytes -v`
Expected: PASS. If it reports 32, the field order was not applied as written.

- [ ] **Step 5: Write the failing interning test**

In `internal/logfmt/scan_test.go`:

```go
// An entry's tf_req_id is interned and resolvable, and it is interned in the
// request-id interner rather than the component one -- the two are separate
// vocabularies whose ids would otherwise be silently comparable.
func TestScanInternsRequestIds(t *testing.T) {
	const ts = "2026-08-29T10:34:43.124+0200 [TRACE] provider.aws: "
	in := ts + "Sending request downstream: tf_req_id=abc123\n" +
		ts + "Received downstream response: tf_req_id=abc123\n" +
		ts + "an entry with no request id at all\n"

	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(c.entries))
	}
	if a, b := c.entries[0].ReqID, c.entries[1].ReqID; a != b {
		t.Errorf("two entries of one call interned to %d and %d", a, b)
	}
	if got := reqIDs.Lookup(c.entries[0].ReqID); got != "abc123" {
		t.Errorf("Lookup(%d) = %q, want %q", c.entries[0].ReqID, got, "abc123")
	}
	if got := c.entries[2].ReqID; got != 0 {
		t.Errorf("an entry with no tf_req_id has ReqID %d, want 0", got)
	}
	// The component interner must not have been used for it: "abc123" is
	// not a component, and an id resolving there would mean the two spaces
	// were shared.
	if got := comps.Lookup(c.entries[0].ReqID); got == "abc123" {
		t.Errorf("the request id was interned into the component interner")
	}
}
```

- [ ] **Step 6: Run it to see it fail**

Run: `go test ./internal/logfmt/ -run TestScanInternsRequestIds -v`
Expected: FAIL to build — `Scan` takes one interner, not two.

- [ ] **Step 7: Change `Scan`'s signature and intern the id**

In `internal/logfmt/scan.go`, change the signature:

```go
func Scan(r io.Reader, comps, reqIDs *Interner, sinks ...Sink) (Stats, error) {
```

Update `Scan`'s doc comment to name the second interner and say why it is
separate (quote the reasoning from `Entry.ReqID`'s comment, do not repeat it
verbatim — say that ids compare only within one interner and the two
vocabularies have different cardinalities).

In `flush()`, after `fieldBuf = ParseFields(curMsg, fieldBuf[:0])` and before
the sinks are called, set the id on the entry being flushed:

```go
		if id, ok := fieldBuf.Get(reqIDKey); ok {
			cur.ReqID = reqIDs.Intern(id)
		}
```

- [ ] **Step 8: Update the two production call sites**

`cmd/tfli/main.go:150`:

```go
	stats, err := logfmt.Scan(f, &comps, &reqIDs, collector, sniffer, &builder, &uiBuilder, &cc)
```

Declare `var reqIDs logfmt.Interner` beside the existing `var comps logfmt.Interner`.

`internal/model/log.go:79`:

```go
	stats, err := logfmt.Scan(bytes.NewReader(data), comps, reqIDs, idx, sniffer, &rb, &ub, &cc)
```

`model.Load` constructs the interner LOCALLY and does not store it on the
`Log`:

```go
	reqIDs := &logfmt.Interner{}
```

Nothing in this plan resolves a request id back to its string — the pane
title carries the scope's COUNT, not its id, and `ScopeFor` compares ids
without looking them up. A `Log.ReqIDs` field would be a stored value with no
reader, so it is not added. Add it in the change that first needs it.

- [ ] **Step 9: Update the 31 test call sites**

Find them with:

```bash
grep -rn "Scan(" internal/ cmd/ --include='*_test.go' | grep -v "func "
```

Each gains a second interner. The in-package `internal/logfmt` tests use
`var comps, reqIDs Interner` and pass `&comps, &reqIDs`; the qualified callers
in `internal/span` and `internal/diagnose` use `logfmt.Interner`.

- [ ] **Step 10: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all 8 packages ok.

- [ ] **Step 11: Mutation-test the interning**

Create `/tmp/t1.json`:

```json
[
 {"file": "internal/logfmt/scan.go",
  "find": "\t\t\tcur.ReqID = reqIDs.Intern(id)",
  "replace": "\t\t\tcur.ReqID = comps.Intern(id)",
  "what": "the request id is interned in its own interner, not the component one"},
 {"file": "internal/logfmt/scan.go",
  "find": "\t\tif id, ok := fieldBuf.Get(reqIDKey); ok {\n\t\t\tcur.ReqID = reqIDs.Intern(id)\n\t\t}",
  "replace": "\t\t_ = reqIDs",
  "what": "an entry carrying a tf_req_id gets one"}
]
```

Run: `scripts/mutate.sh /tmp/t1.json ./... -skip 'TestGolden'`
Expected: `2 mutations: 2 caught, 0 survived, 0 refused.`

- [ ] **Step 12: Commit**

```bash
/Users/dan/.claude/bin/claude-git add -u
/Users/dan/.claude/bin/claude-git commit -F /tmp/t1-msg.txt
```

Message: state that `Entry` carries the interned request id, that the field
order is what keeps the struct at 24 bytes and is asserted, and that the
request ids get an interner of their own because ids compare only within one.

---

### Task 2: `Span.ReqID`, copied never re-interned

**Files:**
- Modify: `internal/span/span.go` (the `Span` struct)
- Modify: `internal/span/reported.go` (the span construction around line 125)
- Test: `internal/span/reported_test.go`, `internal/span/uihook_test.go`

**Interfaces:**
- Consumes: `logfmt.Entry.ReqID uint16` from Task 1.
- Produces: `Span.ReqID uint16` — copied from the closing entry, 0 when absent or overflowed.

- [ ] **Step 1: Write the failing test**

In `internal/span/reported_test.go`:

```go
// A span carries the request id of the entry that closed it, COPIED from
// Entry.ReqID rather than interned again. ReportedBuilder holds a Comps
// interner of its own, so re-interning here would produce an id from a
// second space that matches no entry -- a scope built on it would find
// nothing, or worse, the wrong entries.
func TestReportedSpanCopiesTheEntryRequestId(t *testing.T) {
	var comps, reqIDs logfmt.Interner
	b := NewReportedBuilder(&comps)
	e := logfmt.Entry{TSms: 100, ReqID: reqIDs.Intern("abc123")}
	b.Entry(0, e, "Received downstream response",
		logfmt.ParseFields("tf_req_duration_ms=5 tf_rpc=ReadResource tf_provider_addr=p tf_resource_type=t", nil))

	spans := b.Spans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if got, want := spans[0].ReqID, e.ReqID; got != want {
		t.Errorf("span ReqID = %d, want the entry's %d", got, want)
	}
}

// An id that overflowed the interner is treated as no id at all. Past 65534
// distinct strings every further one interns to OverflowID, deliberately, so
// no two are silently made equal -- which means every overflowed call would
// share one id, and a scope built on it would show a large set of unrelated
// calls looking exactly like a working scope. A visible absence is the safe
// failure; a plausible wrong answer is not.
func TestReportedSpanTreatsAnOverflowedIdAsAbsent(t *testing.T) {
	var comps logfmt.Interner
	b := NewReportedBuilder(&comps)
	b.Entry(0, logfmt.Entry{TSms: 100, ReqID: logfmt.OverflowID}, "Received downstream response",
		logfmt.ParseFields("tf_req_duration_ms=5 tf_rpc=ReadResource tf_provider_addr=p tf_resource_type=t", nil))

	if got := b.Spans()[0].ReqID; got != 0 {
		t.Errorf("span ReqID = %d for an overflowed id, want 0", got)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/span/ -run 'TestReportedSpanCopies|TestReportedSpanTreatsAnOverflowed' -v`
Expected: FAIL to build — `Span` has no `ReqID`.

- [ ] **Step 3: Add the field**

In `internal/span/span.go`, after `Entry uint32`:

```go
	// ReqID is the interned tf_req_id of this call, 0 when the log carried
	// none for it. It is COPIED from the closing entry's Entry.ReqID and
	// never interned here: ids compare only within one Interner, and
	// ReportedBuilder holds a component interner that would produce an id
	// from the wrong space.
	//
	// logfmt.OverflowID is recorded as 0. Past the interner's ceiling every
	// further string interns to that one id, so every overflowed call would
	// share it -- and a scope built on it would show a large set of
	// unrelated calls looking exactly like a working scope.
	ReqID uint16
```

- [ ] **Step 4: Copy it in the builder**

In `internal/span/reported.go`, inside the `b.spans = append(b.spans, Span{...})` literal:

```go
		ReqID:        reqIDOrNone(e.ReqID),
```

And beside the builder:

```go
// reqIDOrNone maps an interner id onto Span.ReqID, turning the overflow id
// into "none". See Span.ReqID for why an overflowed id must not be kept.
func reqIDOrNone(id uint16) uint16 {
	if id == logfmt.OverflowID {
		return 0
	}
	return id
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/span/ -run 'TestReportedSpanCopies|TestReportedSpanTreatsAnOverflowed' -v`
Expected: PASS.

- [ ] **Step 6: Pin the UI tier at zero**

In `internal/span/uihook_test.go`:

```go
// A UI-tier span carries no request id. UIHookBuilder reads Terraform's
// structured output stream, which is core's own JSON and carries no
// tf_req_id at all -- so a scope cannot be built from one, and the jump
// falls back to its unscoped form.
func TestUIHookSpansCarryNoRequestId(t *testing.T) {
	l := testUILog(t) // the package's existing UI-tier fixture helper
	for i, s := range l {
		if s.ReqID != 0 {
			t.Errorf("UI-hook span %d carries ReqID %d, want 0", i, s.ReqID)
		}
	}
}
```

Adapt the fixture call to whatever helper `uihook_test.go` already uses to
build UI spans; do not add a new one.

- [ ] **Step 7: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all 8 packages ok.

- [ ] **Step 8: Mutation-test**

`/tmp/t2.json`:

```json
[
 {"file": "internal/span/reported.go",
  "find": "\t\tReqID:        reqIDOrNone(e.ReqID),",
  "replace": "\t\tReqID:        e.ReqID,",
  "what": "an overflowed id is recorded as none"},
 {"file": "internal/span/reported.go",
  "find": "\t\tReqID:        reqIDOrNone(e.ReqID),",
  "replace": "\t\tReqID:        0,",
  "what": "a span carries its closing entry's request id"}
]
```

Run: `scripts/mutate.sh /tmp/t2.json ./... -skip 'TestGolden'`
Expected: `2 mutations: 2 caught, 0 survived, 0 refused.`

- [ ] **Step 9: Commit**

Message: a span carries its closing entry's request id, copied rather than
re-interned because ids compare only within one interner; an overflowed id is
recorded as absent because every overflowed call would otherwise share one.

---

### Task 3: The interleaved fixture

**Files:**
- Create: `testdata/interleaved-calls.log`
- Test: `internal/logfmt/scan_test.go`

**Interfaces:**
- Produces: `testdata/interleaved-calls.log` — two calls whose entries interleave, one with a continuation-borne id.

- [ ] **Step 1: Write the fixture**

No fixture in `testdata/` puts an id on more than two entries and none
interleaves two calls, so a scope that merely took a contiguous run would
pass against every one of them. Create `testdata/interleaved-calls.log`:

```
# SYNTHESISED. Two provider calls whose log lines interleave, in the shape a
# real capture takes when Terraform walks the graph concurrently. Written for
# this repository; no line is copied from any capture.
#
# Call A: tf_req_id=aaaaaaaa-0001-4a1b-8c2d-000000000001, five entries.
# Call B: tf_req_id=bbbbbbbb-0002-4a1b-8c2d-000000000002, three entries.
# The two are INTERLEAVED, so a scope built by taking a contiguous run of
# entries cannot pass. One entry of call A carries its id on a CONTINUATION
# line only, so the header-only limit is visible in a test.
2026-09-04T09:15:00.000+1000 [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Sending request downstream: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_id=aaaaaaaa-0001-4a1b-8c2d-000000000001 tf_resource_type=aws_subnet tf_rpc=ReadResource
2026-09-04T09:15:00.100+1000 [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Sending request downstream: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_id=bbbbbbbb-0002-4a1b-8c2d-000000000002 tf_resource_type=aws_vpc tf_rpc=ReadResource
2026-09-04T09:15:00.200+1000 [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Calling provider defined validator.String: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_id=aaaaaaaa-0001-4a1b-8c2d-000000000001 tf_resource_type=aws_subnet tf_rpc=ReadResource
2026-09-04T09:15:00.300+1000 [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Calling provider defined validator.String: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_id=bbbbbbbb-0002-4a1b-8c2d-000000000002 tf_resource_type=aws_vpc tf_rpc=ReadResource
2026-09-04T09:15:00.400+1000 [DEBUG] provider.terraform-provider-aws_v4.46.0_x5: HTTP Response Received: @module=aws aws.operation=DescribeSubnets
  http.response.body= {"subnets":[]}
  http.duration=140 tf_req_id=aaaaaaaa-0001-4a1b-8c2d-000000000001
2026-09-04T09:15:00.500+1000 [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Found resource type: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_id=aaaaaaaa-0001-4a1b-8c2d-000000000001 tf_resource_type=aws_subnet tf_rpc=ReadResource
2026-09-04T09:15:00.600+1000 [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=600 tf_req_id=aaaaaaaa-0001-4a1b-8c2d-000000000001 tf_resource_type=aws_subnet tf_rpc=ReadResource
2026-09-04T09:15:00.700+1000 [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=600 tf_req_id=bbbbbbbb-0002-4a1b-8c2d-000000000002 tf_resource_type=aws_vpc tf_rpc=ReadResource
```

- [ ] **Step 2: Write the test that pins its shape**

In `internal/logfmt/scan_test.go`:

```go
// The interleaved fixture is what makes a scope testable, and this pins the
// three properties later tasks rely on: two calls, their entries
// interleaved, and one id readable only from a continuation.
//
// A fixture whose calls did not overlap would let a scope that took a
// contiguous RUN of entries pass, which is the defect most likely to be
// written by accident.
func TestInterleavedFixtureHasTwoOverlappingCalls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "interleaved-calls.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(bytes.NewReader(data), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	const a = "aaaaaaaa-0001-4a1b-8c2d-000000000001"
	const b = "bbbbbbbb-0002-4a1b-8c2d-000000000002"
	var seq []string
	for _, e := range c.entries {
		seq = append(seq, reqIDs.Lookup(e.ReqID))
	}
	// Header-borne ids only: the HTTP entry's id is on a continuation and so
	// reads as "" here, which is the limit the scope ships with.
	want := []string{a, b, a, b, "", a, a, b}
	if !slices.Equal(seq, want) {
		t.Errorf("entry request ids = %v,\nwant                  %v", seq, want)
	}
	if got := st.ContinuationOnlyReqIDEntries; got != 1 {
		t.Errorf("ContinuationOnlyReqIDEntries = %d, want 1 -- the fixture must carry one id on a continuation", got)
	}
}
```

- [ ] **Step 3: Run it**

Run: `go test ./internal/logfmt/ -run TestInterleavedFixtureHasTwoOverlappingCalls -v`
Expected: PASS. If the id sequence differs, the fixture's lines are in the
wrong order or a header lost its id — fix the fixture, not the test.

- [ ] **Step 4: Check the counters agree**

Run: `go run ./cmd/tfli --diagnose testdata/interleaved-calls.log | grep -E "req id|entries per|of those"`
Expected: `distinct req ids 2`, `entries per req id min 3, max 4`, and
`of those, carrying an id 1 line(s), costing 1 entry their only id`.

- [ ] **Step 5: Commit**

Message: the fixture that makes a scope testable — two calls interleaved so a
contiguous-run scope cannot pass, and one id on a continuation so the
header-only limit is visible.

---

### Task 4: `--diagnose` gains `ResponseReqIDFields` and marks a saturated set

**Files:**
- Modify: `internal/span/sniff.go` (`Capabilities`, `Sniffer.Entry`)
- Modify: `internal/diagnose/diagnose.go` (the EXTRACTION block)
- Test: `internal/span/sniff_test.go`, `internal/diagnose/diagnose_test.go`

**Interfaces:**
- Produces: `Capabilities.ResponseReqIDFields uint64` — response entries carrying `tf_req_id`.

- [ ] **Step 1: Write the failing test**

In `internal/span/sniff_test.go`:

```go
// ResponseReqIDFields counts RESPONSE entries carrying tf_req_id, gated the
// way DurationFields directly above it is gated and for the same reason.
// ReqIDFields counts every entry with the field, which is right for sizing a
// scope and useless for the question this answers: how many spans would fall
// back to an unscoped jump because their response carried no id.
func TestSnifferCountsRequestIdsOnResponsesSeparately(t *testing.T) {
	const ts = "2022-12-15T00:16:20.800Z [TRACE] provider.aws: "
	c := sniff(t, ts+"Sending request downstream: tf_req_id=abc\n"+
		ts+"Received downstream response: tf_req_id=abc tf_req_duration_ms=5\n"+
		ts+"Received downstream response: tf_req_duration_ms=9\n")

	if got, want := c.ReqIDFields, uint64(2); got != want {
		t.Errorf("ReqIDFields = %d, want %d -- every entry carrying the field", got, want)
	}
	if got, want := c.ResponseReqIDFields, uint64(1); got != want {
		t.Errorf("ResponseReqIDFields = %d, want %d -- responses only", got, want)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/span/ -run TestSnifferCountsRequestIdsOnResponsesSeparately -v`
Expected: FAIL to build — no such field.

- [ ] **Step 3: Add the counter**

In `internal/span/sniff.go`, in `Capabilities` after `ReqIDFields`:

```go
	// ResponseReqIDFields is response entries carrying tf_req_id, gated the
	// way DurationFields is. ReqIDFields counts every entry with the field,
	// which sizes a scope and cannot answer how many spans would have no id
	// to scope BY: that question is about responses, since a span is built
	// from one.
	ResponseReqIDFields uint64
```

In `Sniffer.Entry`, inside the existing `if hasReqID` block:

```go
		if isResponse {
			s.caps.ResponseReqIDFields++
		}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/span/ -run TestSnifferCountsRequestIdsOnResponsesSeparately -v`
Expected: PASS.

- [ ] **Step 5: Render both new facts**

In `internal/diagnose/diagnose.go`, after the `req id fields` line:

```go
	fmt.Fprintf(b, "  %-25s %d of %d responses\n", "  on a response", r.Caps.ResponseReqIDFields, r.Caps.ResponseEntries)
```

`writeReqIDSpread` already marks a saturated set; verify it does by reading
it, and add nothing if so.

- [ ] **Step 6: Test the report line**

In `internal/diagnose/diagnose_test.go`:

```go
// The report states how many responses carried an id against how many there
// were, because their difference is the number of spans that would have no
// id to scope by.
func TestReportStatesResponsesCarryingARequestId(t *testing.T) {
	const ts = "2022-12-15T00:16:20.800Z [TRACE] provider.aws: "
	out := render(t, build(t, ts+"Received downstream response: tf_req_id=abc tf_req_duration_ms=5\n"+
		ts+"Received downstream response: tf_req_duration_ms=9\n"))
	if want := "on a response             1 of 2 responses"; !strings.Contains(out, want) {
		t.Errorf("report missing %q:\n%s", want, out)
	}
}
```

- [ ] **Step 7: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all 8 packages ok.

- [ ] **Step 8: Mutation-test**

`/tmp/t4.json`:

```json
[
 {"file": "internal/span/sniff.go",
  "find": "\t\tif isResponse {\n\t\t\ts.caps.ResponseReqIDFields++\n\t\t}",
  "replace": "\t\ts.caps.ResponseReqIDFields++",
  "what": "only RESPONSE entries count towards ResponseReqIDFields"}
]
```

Run: `scripts/mutate.sh /tmp/t4.json ./... -skip 'TestGolden'`
Expected: `1 mutations: 1 caught, 0 survived, 0 refused.`

- [ ] **Step 9: Commit**

Message: `--diagnose` reports responses carrying a request id against
responses, since their difference is the fallback rate, and `req id fields`
is ungated and cannot answer it.

---

### Task 5: `model.Log` materialises a scope

**Files:**
- Modify: `internal/model/log.go`
- Test: `internal/model/log_test.go`

**Interfaces:**
- Consumes: `logfmt.Entry.ReqID`, `Log.ReqIDs *logfmt.Interner` from Task 1.
- Produces: `func (l *Log) ScopeFor(reqID uint16) []int` — ascending entry indices, nil for id 0.

- [ ] **Step 1: Write the failing test**

In `internal/model/log_test.go`:

```go
// A scope is the ascending indices of every entry carrying one call's request
// id, which is what lets the raw log show a call rather than the log around
// it. The fixture's two calls INTERLEAVE, so a scope built by taking a
// contiguous run of entries fails here -- which is the defect most likely to
// be written by accident.
func TestScopeForCollectsOneCallsEntriesAcrossAnInterleavedLog(t *testing.T) {
	l := testLog(t, "interleaved-calls.log")
	var a uint16
	for _, s := range l.RPCSpans {
		if s.ResourceType == "aws_subnet" {
			a = s.ReqID
		}
	}
	if a == 0 {
		t.Fatal("the aws_subnet span carries no request id")
	}
	got := l.ScopeFor(a)
	if want := []int{0, 2, 5, 6}; !slices.Equal(got, want) {
		t.Errorf("ScopeFor = %v, want %v -- the call's entries, not a contiguous run", got, want)
	}
}

// Id 0 is "no request id", not a call whose id happens to be zero, so it
// scopes to nothing rather than to every entry that carries no id.
func TestScopeForReturnsNothingForTheAbsentId(t *testing.T) {
	l := testLog(t, "interleaved-calls.log")
	if got := l.ScopeFor(0); got != nil {
		t.Errorf("ScopeFor(0) = %v, want nil", got)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/model/ -run 'TestScopeFor' -v`
Expected: FAIL to build — no `ScopeFor`.

- [ ] **Step 3: Implement it**

In `internal/model/log.go`:

```go
// ScopeFor returns the indices of every entry carrying request id reqID, in
// ascending order -- one call's own log lines, which is what the raw log
// shows when a call is opened.
//
// It MATERIALISES rather than returning a predicate. A scope is small (a
// measured min of 4, median 7 and max 1934 entries on the standardised
// capture) and the pane iterating it must not walk the log to find its
// members: renderRawLog's only early exit is a full pane, so a scope that
// never fills one would scan every remaining entry on every keystroke --
// 38,379 of them on that capture, and about 8 million at the 1GB target.
//
// Id 0 means "no request id" rather than a call whose id is zero, so it
// scopes to nothing. Returning every entry that carries no id instead would
// answer a jump with a pane full of unrelated traffic.
func (l *Log) ScopeFor(reqID uint16) []int {
	if reqID == 0 {
		return nil
	}
	var scope []int
	for i, e := range l.Entries {
		if e.ReqID == reqID {
			scope = append(scope, i)
		}
	}
	return scope
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/model/ -run 'TestScopeFor' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite and mutation-test**

`/tmp/t5.json`:

```json
[
 {"file": "internal/model/log.go",
  "find": "\tif reqID == 0 {\n\t\treturn nil\n\t}",
  "replace": "\tif false {\n\t\treturn nil\n\t}",
  "what": "the absent id scopes to nothing"},
 {"file": "internal/model/log.go",
  "find": "\t\tif e.ReqID == reqID {",
  "replace": "\t\tif e.ReqID != reqID {",
  "what": "a scope holds the entries carrying the id"}
]
```

Run: `scripts/mutate.sh /tmp/t5.json ./... -skip 'TestGolden'`
Expected: `2 mutations: 2 caught, 0 survived, 0 refused.`

- [ ] **Step 6: Commit**

Message: `Log.ScopeFor` materialises one call's entry indices; why it
materialises rather than filtering, and why id 0 scopes to nothing.

---

### Task 6: The raw log draws a scope

**Files:**
- Modify: `internal/tui/rawlog.go` (`rawLogState`, `renderRawLog`, `stepRawLogDown`, `stepRawLogUp`, `searchFrom`, `jumpToSpan`)
- Test: `internal/tui/rawlog_test.go`

**Interfaces:**
- Consumes: `Log.ScopeFor(uint16) []int` from Task 5, `Span.ReqID` from Task 2.
- Produces: `rawLogState.scope []int`; `func (m Model) nextRawEntry(i int) (int, bool)`; `func (m Model) prevRawEntry(i int) (int, bool)`.

- [ ] **Step 1: Write the failing test**

In `internal/tui/rawlog_test.go`:

```go
// Opening a call draws that call's lines and nothing else. The fixture's two
// calls interleave, so a pane showing a contiguous run of entries fails here.
func TestOpeningACallDrawsOnlyThatCallsEntries(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Fatalf("Enter left the view at %v", m.view)
	}
	body := rawLogBody(m, 200, 20)
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(line, "aaaaaaaa-0001") && !strings.Contains(line, "DescribeSubnets") && !strings.Contains(line, "http.") {
			t.Errorf("the pane draws a line outside the call's scope: %q", line)
		}
	}
	if len(body) < 4 {
		t.Errorf("the pane drew %d lines, want the call's four entries at least", len(body))
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/tui/ -run TestOpeningACallDrawsOnlyThatCallsEntries -v`
Expected: FAIL — the pane draws the whole log from the call's entry down,
including call B's lines.

- [ ] **Step 3: Add the scope to the raw log's state**

In `internal/tui/rawlog.go`, in `rawLogState`:

```go
	// scope, when non-nil, is the ascending entry indices of ONE call's
	// lines (model.Log.ScopeFor). Render, scroll and search walk it instead
	// of the whole log, so the pane shows the call rather than the log
	// around it.
	//
	// nil is "no scope", which is not the same as an empty one: a scope is
	// built from a span's own id and always holds at least that span's
	// entry, so an empty scoped pane is the facet filter's doing and says so
	// (see renderRawLog).
	scope []int
```

- [ ] **Step 4: Add the two walkers**

```go
// nextRawEntry returns the entry index at or after i that the pane may draw,
// and whether there is one: the next member of the scope when one is live,
// and i itself otherwise. prevRawEntry is its mirror.
//
// The pair is what lets render, scroll and search share one notion of "the
// entries this pane is showing", so a scope cannot apply to one of them and
// not the others -- which would leave the position pointing at an entry the
// pane skips.
func (m Model) nextRawEntry(i int) (int, bool) {
	if m.raw.scope == nil {
		if i < 0 {
			i = 0
		}
		return i, i < len(m.log.Entries)
	}
	p, _ := slices.BinarySearch(m.raw.scope, i)
	if p >= len(m.raw.scope) {
		return 0, false
	}
	return m.raw.scope[p], true
}

func (m Model) prevRawEntry(i int) (int, bool) {
	if m.raw.scope == nil {
		return i, i >= 0
	}
	p, found := slices.BinarySearch(m.raw.scope, i)
	if !found {
		p--
	}
	if p < 0 || p >= len(m.raw.scope) {
		return 0, false
	}
	return m.raw.scope[p], true
}
```

- [ ] **Step 5: Route render, scroll and search through them**

In `renderRawLog`, replace the loop head:

```go
	for i, ok := m.nextRawEntry(m.TopEntry()); ok && len(lines) < h; i, ok = m.nextRawEntry(i + 1) {
```

In `stepRawLogDown`, replace the forward search for the next visible entry:

```go
	for i, ok := m.nextRawEntry(m.raw.top + 1); ok; i, ok = m.nextRawEntry(i + 1) {
		if visible(i) {
			m.raw.top, m.raw.topLine = i, 0
			return true
		}
	}
	return false
```

In `stepRawLogUp`, the mirror:

```go
	for i, ok := m.prevRawEntry(m.raw.top - 1); ok; i, ok = m.prevRawEntry(i - 1) {
		if visible(i) {
			m.raw.top, m.raw.topLine = i, len(m.entryLines(m.log.Entries[i]))-1
			return true
		}
	}
	return false
```

In `searchFrom`, replace its own entry walk so a search inside a scope
searches the scope. Its forward loop becomes:

```go
	for i, ok := m.nextRawEntry(start); ok; i, ok = m.nextRawEntry(i + 1) {
```

and its backward loop:

```go
	for i, ok := m.prevRawEntry(start); ok; i, ok = m.prevRawEntry(i - 1) {
```

Add to its doc comment: a search and a scope are both "narrow what I am
reading" gestures, and the scope is the narrower — searching outside it would
report a match the pane cannot show, leaving `top` on an entry `renderRawLog`
skips.

- [ ] **Step 6: Build the scope on the jump**

In `jumpToSpan`, after `m.setView(ViewRawLog)` and the return mark:

```go
	// The scope is what makes this "open the call" rather than "park the
	// whole log on one of its lines". Its first member is the earliest entry
	// in the FILE carrying the id, which is where the call's own traffic
	// begins -- so jumpContextLines is NOT applied here: the scope already
	// supplies what led to the call, and backing up three lines would open
	// the pane on lines outside it.
	if scope := m.log.ScopeFor(spans[idx].ReqID); len(scope) > 0 {
		m.raw.scope = scope
		m.raw.top, m.raw.topLine = scope[0], 0
		return
	}
	m.raw.scope = nil
	m.raw.top, m.raw.topLine = entry, 0
	m.scrollRawLog(-jumpContextLines)
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/tui/ -run TestOpeningACallDrawsOnlyThatCallsEntries -v`
Expected: PASS.

- [ ] **Step 8: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all 8 packages ok. Several existing raw-log tests will need their
expectations revisited — a jump now opens on the scope's first entry rather
than `jumpContextLines` above the closing one. Where a test's CLAIM still
holds, update the expectation; where the claim itself has changed, rewrite
the doc comment to say what it now pins. Do not delete a test to make the
suite pass.

- [ ] **Step 9: Mutation-test**

`/tmp/t6.json`:

```json
[
 {"file": "internal/tui/rawlog.go",
  "find": "\t\tm.raw.top, m.raw.topLine = scope[0], 0",
  "replace": "\t\tm.raw.top, m.raw.topLine = entry, 0",
  "what": "a scoped jump opens on the scope's first member"},
 {"file": "internal/tui/rawlog.go",
  "find": "\tif scope := m.log.ScopeFor(spans[idx].ReqID); len(scope) > 0 {\n\t\tm.raw.scope = scope",
  "replace": "\tif scope := m.log.ScopeFor(spans[idx].ReqID); false {\n\t\tm.raw.scope = scope",
  "what": "a jump builds a scope when the call has an id"}
]
```

Run: `scripts/mutate.sh /tmp/t6.json ./internal/tui/ -skip 'TestGolden'`
Expected: `2 mutations: 2 caught, 0 survived, 0 refused.`

- [ ] **Step 10: Commit**

---

### Task 7: `\` drops the scope; `Esc` and `setView` spend it

**Files:**
- Modify: `internal/tui/model.go` (`Update`'s key switch, `setView`, `returnFromJump`)
- Test: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `rawLogState.scope` from Task 6.

- [ ] **Step 1: Write the failing tests**

In `internal/tui/model_test.go`:

```go
// Backslash drops the scope and leaves the position, so the entry on the
// pane's first line stays there and the rest of the log resumes beneath it.
// What PRECEDED it does not appear: renderRawLog draws downward only, so
// unscoping reveals what follows and never what comes before, which is one
// scroll up away.
func TestBackslashDropsTheScopeAndKeepsThePosition(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.raw.scope == nil {
		t.Fatal("Enter built no scope")
	}
	top, topLine := m.TopEntry(), m.TopLine()

	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
	if m.raw.scope != nil {
		t.Errorf("the scope survived a backslash")
	}
	if m.TopEntry() != top || m.TopLine() != topLine {
		t.Errorf("position moved from %d+%d to %d+%d", top, topLine, m.TopEntry(), m.TopLine())
	}
}

// Backslash is inert with no scope live, and inert outside the raw log.
// Advertising a key that does nothing is the defect this package removes
// wherever it finds it; doing something invisible is worse.
func TestBackslashIsInertWithoutAScope(t *testing.T) {
	base := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	for _, c := range []struct {
		name string
		m    Model
	}{
		{"calls view", base},
		{"raw log reached by its own key", update(t, base, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})},
	} {
		before := c.m.View()
		after := update(t, c.m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
		if after.View() != before {
			t.Errorf("%s: backslash changed the frame", c.name)
		}
	}
}

// setView drops the scope, exactly as it drops the return. Reaching a view by
// its own key is the reader saying where they want to be, and the scope is
// part of the jump that took them elsewhere. Left standing, Enter then 1 then
// 6 gives a scoped raw log with no return behind it: Esc takes the
// clearFilters branch and does not drop it, and only backslash gets the
// reader out -- a key they may never have pressed.
func TestAViewChangeDropsTheScope(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	if m.raw.scope != nil {
		t.Errorf("the scope survived a view change of the reader's own")
	}
}

// Esc returns and drops the scope together. The scope belongs to the jump
// that made it, so this is not a third meaning for Esc -- it is part of the
// first.
func TestEscDropsTheScopeWithTheReturn(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != ViewCalls {
		t.Errorf("Esc left the view at %v", m.view)
	}
	if m.raw.scope != nil {
		t.Errorf("the scope survived the return")
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/tui/ -run 'TestBackslash|TestAViewChangeDropsTheScope|TestEscDropsTheScope' -v`
Expected: FAIL — backslash is unbound and nothing clears `scope`.

- [ ] **Step 3: Bind backslash**

In `internal/tui/model.go`, in `Update`'s `msg.String()` switch:

```go
		case "\\":
			// Backslash drops the scope and leaves the position, so the
			// entry on the pane's first line stays there and the rest of
			// the log resumes beneath it. Bound in the raw log only:
			// nowhere else has a scope to drop, and a key that acts
			// invisibly elsewhere is worse than one that does nothing.
			if m.view == ViewRawLog {
				m.raw.scope = nil
			}
```

- [ ] **Step 4: Spend it in `setView`**

In `setView`, beside `m.hasReturn = false`:

```go
	m.raw.scope = nil
```

Extend `setView`'s doc comment: the scope goes the way the return does, and
for the same reason.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/tui/ -run 'TestBackslash|TestAViewChangeDropsTheScope|TestEscDropsTheScope' -v`
Expected: PASS. `Esc` needs no separate change — `returnFromJump` calls
`setView`, which now drops the scope.

- [ ] **Step 6: Mutation-test**

`/tmp/t7.json`:

```json
[
 {"file": "internal/tui/model.go",
  "find": "\t\t\tif m.view == ViewRawLog {\n\t\t\t\tm.raw.scope = nil\n\t\t\t}",
  "replace": "\t\t\tif m.view != ViewRawLog {\n\t\t\t\tm.raw.scope = nil\n\t\t\t}",
  "what": "backslash drops the scope in the raw log"},
 {"file": "internal/tui/model.go",
  "find": "\tm.hasReturn = false\n\tm.raw.scope = nil",
  "replace": "\tm.hasReturn = false",
  "what": "a view change spends the scope"}
]
```

Run: `scripts/mutate.sh /tmp/t7.json ./internal/tui/ -skip 'TestGolden'`
Expected: `2 mutations: 2 caught, 0 survived, 0 refused.`

- [ ] **Step 7: Commit**

---

### Task 8: The frame says a scope is live

**Files:**
- Modify: `internal/tui/layout.go` (`centreTitle`, `actionKeys`)
- Modify: `internal/tui/rawlog.go` (`renderRawLog`'s empty-pane branch)
- Modify: `internal/tui/help.go` (`helpGroups`)
- Modify: `internal/tui/views.go` (`noMatchNote`'s tail)
- Test: `internal/tui/layout_test.go`, `internal/tui/help_test.go`

**Interfaces:**
- Consumes: `rawLogState.scope` from Task 6.
- Produces: `scopeHint = "\\ whole log"`, `scopedEmptyNote`.

- [ ] **Step 1: Write the failing tests**

In `internal/tui/layout_test.go`:

```go
// The pane title carries the scope's SIZE, not merely that there is one. The
// measured spread is min 4, median 7, max 1934 entries, so "one call" would
// be the same words over a pane the reader can read in full and over one
// holding eight per cent of the log's id-bearing lines.
func TestAScopedRawLogNamesItsSize(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got, want := m.centreTitle(), "RAW LOG (4 entries)"; got != want {
		t.Errorf("centreTitle = %q, want %q", got, want)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
	if got, want := m.centreTitle(), "RAW LOG"; got != want {
		t.Errorf("centreTitle = %q after unscoping, want %q", got, want)
	}
}

// The action line offers the key that widens, and only while there is
// something to widen.
func TestTheFooterOffersTheWideningKeyOnlyWhileScoped(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	if got := unstyled(m.footer(200)); strings.Contains(got, scopeHint) {
		t.Errorf("the calls view offers %q:\n%s", scopeHint, got)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := unstyled(m.footer(200)); !strings.Contains(got, scopeHint) {
		t.Errorf("a scoped raw log does not offer %q:\n%s", scopeHint, got)
	}
}

// The scoped action line fits its budget. The existing sweep reaches each
// view by pressing its number key, which goes through setView and spends the
// scope, so it measures the unscoped line and cannot see this one.
func TestTheScopedActionLineFitsTheNarrowestThreePaneWidth(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	line := strings.Split(unstyled(m.footer(200)), "\n")
	action := line[len(line)-1]
	if got := lipgloss.Width(action); got > detailInlineWidth {
		t.Errorf("the scoped action line is %d columns, over the %d budget: %q", got, detailInlineWidth, action)
	}
}
```

In `internal/tui/rawlog_test.go`:

```go
// A scope drawing nothing says so in its own words, naming the key that
// widens. A scope is never empty of MEMBERS -- it is built from a span's own
// id and holds at least that span's entry -- so an empty scoped pane is
// always the filter's doing, and backslash is a key the reader has and one
// that acts. "this log has no entries" would be false, and "Esc clears it"
// names a key that returns before it clears.
func TestAnEmptyScopedPaneNamesTheKeyThatWidensIt(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// Exclude every level, so the scope's members are all hidden.
	m.setFacetExclusions(dimLevel, map[string]bool{"TRACE": true, "DEBUG": true, "UNKNOWN": true})
	m.invalidateRows()

	got := unstyled(m.renderRawLog(200, 10))
	if !strings.Contains(got, scopedEmptyNote) {
		t.Errorf("an empty scoped pane says %q, want %q", got, scopedEmptyNote)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/tui/ -run 'TestAScopedRawLogNamesItsSize|TestTheFooterOffersTheWidening|TestTheScopedActionLine|TestAnEmptyScopedPane' -v`
Expected: FAIL to build — `scopeHint` and `scopedEmptyNote` do not exist.

- [ ] **Step 3: Name the scope in the title**

In `internal/tui/layout.go`, in `centreTitle`:

```go
	if m.view == ViewRawLog && m.raw.scope != nil {
		// The COUNT, not merely the fact. The measured spread is 4 to 1934
		// entries, so a word like "one call" would read the same over a
		// pane the reader can take in whole and over one holding a
		// twentieth of the log. It is free: the scope is a slice.
		return fmt.Sprintf("%s (%d %s)", viewTitle(ViewRawLog), len(m.raw.scope),
			plural(uint64(len(m.raw.scope)), "entry", "entries"))
	}
```

`plural` already exists in `internal/diagnose`; add a local one to
`internal/tui/layout.go` rather than importing across packages, with a doc
comment saying why the duplication is deliberate (the two packages share no
utility layer, and one function is not a layer).

- [ ] **Step 4: Offer the key**

In `actionKeys`, before the `esc` selection:

```go
	if m.raw.scope != nil {
		keys = append(keys, scopeHint)
	}
```

And beside `escClearHint`:

```go
// scopeHint offers the key that drops a raw-log scope. It is shown only
// while a scope is live: a key advertised with nothing to act on is the
// defect this package removes wherever it finds it.
const scopeHint = "\\ whole log"
```

- [ ] **Step 5: Add the third empty-pane answer**

In `internal/tui/rawlog.go`, in `renderRawLog`'s `len(lines) == 0` branch,
before the existing two:

```go
		if m.raw.scope != nil {
			return styles.note.Render(clipWidth(scopedEmptyNote, w))
		}
```

In `internal/tui/views.go`, beside `noMatchNote`:

```go
// scopedEmptyNote is what a scoped pane says when the filter hides every one
// of the call's entries. A scope always HAS members -- it is built from a
// span's own id and holds at least that span's entry -- so an empty scoped
// pane is the filter's doing, never an empty log. It names backslash rather
// than Esc because Esc returns before it clears, so the frame would
// otherwise advertise a key against what its own footer says about it.
const scopedEmptyNote = "nothing in this call matches the filter -- \\ shows the whole log"
```

- [ ] **Step 6: Fix `noMatchNote`'s tail for a standing return**

`noMatchNote`'s "Esc clears it" is false whenever `hasReturn` stands, which
is every raw log reached by `⏎`. Make the tail conditional at its render
sites in `renderRawLog` and `renderTimeline`, asking the same `hasReturn` the
footer asks:

```go
// noMatchTail names whichever thing Esc will actually do, so a note and the
// footer above it cannot advertise one key two ways.
func (m Model) noMatchTail() string {
	if m.hasReturn {
		return "nothing matches the filter -- Esc goes back"
	}
	return noMatchNote
}
```

- [ ] **Step 7: Document the key**

In `internal/tui/help.go`, in `helpGroups`' THE LIST group:

```go
			{keys: "\\", what: "show the whole log again, after opening a call"},
```

Check the line fits: `TestEveryHelpLineFitsTheNarrowestSupportedWidth`
enforces 60 columns.

- [ ] **Step 8: Run the tests and the full suite**

Run: `go test ./internal/tui/ && go build ./... && go vet ./... && go test ./...`
Expected: all green. Regenerate goldens with `go test ./internal/tui/ -update`
and READ each changed one with `scripts/read-golden.sh` before committing —
never pass `-update` to make a failure go away.

- [ ] **Step 9: Mutation-test**

`/tmp/t8.json`:

```json
[
 {"file": "internal/tui/layout.go",
  "find": "\tif m.raw.scope != nil {\n\t\tkeys = append(keys, scopeHint)\n\t}",
  "replace": "\tif m.raw.scope == nil {\n\t\tkeys = append(keys, scopeHint)\n\t}",
  "what": "the widening key is offered while a scope is live"},
 {"file": "internal/tui/rawlog.go",
  "find": "\t\tif m.raw.scope != nil {\n\t\t\treturn styles.note.Render(clipWidth(scopedEmptyNote, w))\n\t\t}",
  "replace": "\t\tif false {\n\t\t\treturn styles.note.Render(clipWidth(scopedEmptyNote, w))\n\t\t}",
  "what": "an empty scoped pane gets its own note"}
]
```

Run: `scripts/mutate.sh /tmp/t8.json ./internal/tui/ -skip 'TestGolden'`
Expected: `2 mutations: 2 caught, 0 survived, 0 refused.`

- [ ] **Step 10: Commit**

---

### Task 9: The jump's refusal is asked of the scope

**Files:**
- Modify: `internal/tui/rawlog.go` (`jumpToSpan`)
- Test: `internal/tui/rawlog_test.go`

**Interfaces:**
- Consumes: everything from Tasks 6-8.

- [ ] **Step 1: Write the failing tests**

```go
// Scoped, the refusal asks whether the filter admits ANY member of the call,
// not whether it admits the response entry. The pane no longer opens on the
// response, so that entry being hidden is not decisive -- and refusing a jump
// whose other lines are perfectly visible would deny the reader a pane that
// would have worked.
func TestAJumpProceedsWhenTheFilterAdmitsSomeOfTheCall(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	// Hide DEBUG, which is the call's HTTP entry but not its TRACE lines.
	m.setFacetExclusions(dimLevel, map[string]bool{"DEBUG": true})
	m.invalidateRows()

	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != ViewRawLog {
		t.Errorf("the jump was refused though the filter admits the call's TRACE lines")
	}
	if m.blockedJump {
		t.Errorf("blockedJump set for a call the filter partly admits")
	}
}

// It refuses when the filter admits NONE of them, reported in the footer over
// the view the reader pressed Enter in -- landing in an empty pane is
// indistinguishable from a jump that worked.
func TestAJumpIsRefusedWhenTheFilterHidesTheWholeCall(t *testing.T) {
	m := update(t, New(testLog(t, "interleaved-calls.log"), "x.log"), tea.WindowSizeMsg{Width: 200, Height: 40})
	m.setFacetExclusions(dimLevel, map[string]bool{"TRACE": true, "DEBUG": true, "UNKNOWN": true})
	m.invalidateRows()

	before := m.view
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != before {
		t.Errorf("the jump proceeded with every one of the call's entries hidden")
	}
	if !m.blockedJump {
		t.Errorf("blockedJump not set for a wholly hidden call")
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/tui/ -run 'TestAJumpProceedsWhen|TestAJumpIsRefusedWhen' -v`
Expected: the first fails — the refusal still tests the response entry alone.

- [ ] **Step 3: Ask the scope**

In `jumpToSpan`, replace the single-entry `entryVisible` check with:

Compute the scope BEFORE `setView`, since a refusal must leave the view
alone. Replace `jumpToSpan`'s single-entry visibility check with:

```go
	f, compProviders := m.filter(), componentProviders(m.log.RPCSpans, m.log.Entries)
	scope := m.log.ScopeFor(spans[idx].ReqID)

	// Scoped, the question is whether the filter admits any member at all.
	// The pane opens on the first ADMITTED member, so the response entry
	// being hidden is not decisive: refusing on it would deny a pane whose
	// other lines are perfectly visible. Unscoped, the question is the old
	// one, asked of the single entry the pane will open on.
	open := -1
	for _, i := range scope {
		if entryVisible(f, compProviders, m.log.Entries[i]) {
			open = i
			break
		}
	}
	if len(scope) == 0 && entryVisible(f, compProviders, m.log.Entries[entry]) {
		open = entry
	}
	if open < 0 {
		// Landing on a pane the filter has emptied is indistinguishable
		// from a jump that worked, so the refusal is reported in the footer
		// over the view the reader pressed Enter in.
		m.blockedJump = true
		return
	}

	from := m.view
	m.setView(ViewRawLog)
	m.returnTo, m.hasReturn = from, true
	if len(scope) > 0 {
		m.raw.scope = scope
		m.raw.top, m.raw.topLine = open, 0
		return
	}
	m.raw.scope = nil
	m.raw.top, m.raw.topLine = open, 0
	m.scrollRawLog(-jumpContextLines)
```

This supersedes Task 6 Step 6, which set the position without consulting the
filter. Task 6's version is the simpler one written first; this is the same
code with the refusal folded in, and it is the form that ships.

- [ ] **Step 4: Run the tests and the full suite**

Run: `go test ./internal/tui/ && go build ./... && go vet ./... && go test ./...`
Expected: all green.

- [ ] **Step 5: Mutation-test, then commit**

Probe that the refusal is asked of the whole scope rather than of its first
member alone, and that a partly-hidden call still opens.

---

## Verification before the branch is done

- [ ] `gofmt -l .` silent; `go build ./...`, `go vet ./...`, `go test ./...` green across all 8 packages.
- [ ] Every golden changed by this branch has been read with `scripts/read-golden.sh`, not merely regenerated.
- [ ] Every mutation table in this plan re-run against the finished branch: 0 survived, 0 refused, and every catch attributed to a NAMED non-golden test.
- [ ] `go run ./cmd/tfli --diagnose testdata/interleaved-calls.log` reports `distinct req ids 2` and one continuation-borne id.
- [ ] The spec section's "Designed 2026-09-07, not yet built" line is updated to say it shipped, with the date.
