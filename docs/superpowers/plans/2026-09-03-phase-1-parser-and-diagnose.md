# tf-log-inspector Phase 1 — Parser and `--diagnose` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a `tfli --diagnose <logfile>` binary that parses an HCP Terraform raw run log and reports its structure, so the log format is established by evidence before any UI is built on it.

**Architecture:** A single streaming pass over the file parses each physical line into a logical `Entry` (a timestamped line plus its continuation lines), pushing each entry to a set of `Sink`s. Structured `key=value` fields are parsed transiently and never retained, so memory stays flat regardless of file size. One sink accumulates diagnostic counters; another builds spans from `tf_req_duration_ms`. No TUI, no mmap, no external dependencies.

**Tech Stack:** Go 1.27.1, standard library only. No third-party modules in phase 1.

**Spec:** `docs/superpowers/specs/2026-09-03-tf-log-inspector-design.md`

## Global Constraints

- **Module path:** `github.com/yesdevnull/tf-log-inspector`. Binary name `tfli`, so the command package must be `cmd/tfli`.
- **Go version:** `go 1.27` in `go.mod`.
- **Zero external dependencies in phase 1.** Standard library only. Do not add bubbletea, lipgloss, or any mmap package yet.
- **Australian/British English** in all prose, comments and user-facing output (`analyse`, `behaviour`, `summarise`).
- **Test output must be pristine.** Expected error paths are asserted, not printed.
- **Every fixture file records its source URL** in a leading comment line so any line can be traced to its provenance.
- **`--diagnose` output must never contain field values** — only keys, counts and shapes. This is a hard requirement, not a preference: it is the only artefact leaving work hardware.

## Two deliberate deviations from the spec

Both are refinements discovered while planning. They are recorded here rather than applied silently.

1. **No `mmap` in phase 1.** The spec specifies mmap'd input. Phase 1 needs only a single sequential pass, so it uses a `bufio.Reader` and computes byte offsets as it goes. Those offsets are exactly the ones the TUI will later use, so nothing is wasted and mmap arrives in phase 3 when random access is genuinely needed. This keeps phase 1 dependency-free and portable with no build tags.
2. **Package named `logfmt`, not `hclog`.** The spec says `internal/hclog`. A package called `hclog` that is not `hashicorp/go-hclog` is misleading to a reader. `logfmt` names what it does.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `go.mod` | Module definition |
| `internal/logfmt/level.go` | `Level` type and parsing |
| `internal/logfmt/intern.go` | String interning for component names |
| `internal/logfmt/entry.go` | `Entry` struct, `Log` container |
| `internal/logfmt/header.go` | Parse one line's timestamp / level / component |
| `internal/logfmt/fields.go` | `key=value` field parsing, including quoted values |
| `internal/logfmt/scan.go` | The streaming pass: lines → entries → sinks |
| `internal/logfmt/ansi.go` | ANSI escape detection and stripping |
| `internal/span/span.go` | `Span`, `Fidelity` |
| `internal/span/reported.go` | Tier 1 builder, reading `tf_req_duration_ms` |
| `internal/span/sniff.go` | Capability detection across all four tiers |
| `internal/diagnose/diagnose.go` | Structural report accumulation and rendering |
| `cmd/tfli/main.go` | Flags, wiring, output |
| `testdata/*.log` | Fixtures from public GitHub issues |

---

### Task 1: Module scaffolding, fixtures, `Level` and `Interner`

**Files:**
- Create: `go.mod`, `internal/logfmt/level.go`, `internal/logfmt/intern.go`
- Create: `testdata/provider-rpc.log`, `testdata/core-only.log`, `testdata/mixed-hcp.log`
- Test: `internal/logfmt/level_test.go`, `internal/logfmt/intern_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `logfmt.Level` (`LevelUnknown|LevelTrace|LevelDebug|LevelInfo|LevelWarn|LevelError`), `logfmt.ParseLevel(string) Level`, `Level.String() string`; `logfmt.Interner` with `Intern(string) uint16` and `Lookup(uint16) string`.

- [ ] **Step 1: Initialise the module**

```bash
cd /Users/dan/Code/tf-log-inspector
go mod init github.com/yesdevnull/tf-log-inspector
```

- [ ] **Step 2: Create the fixtures**

These are real lines from public sources. `@caller` values are shortened for width only; nothing else is altered.

`testdata/provider-rpc.log` — provider RPC entries with durations:

```
# source: https://github.com/hashicorp/terraform-provider-aws/issues/28364
2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=2634bc46-bb66-3d22-528d-d2eaf8165f52 tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange diagnostic_error_count=1 diagnostic_warning_count=0 tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5 @caller=logging/keys.go @module=sdk.proto timestamp=2022-12-15T00:16:20.799Z
2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=1 tf_req_id=6bf5c123-55fb-d840-bbf4-d84f472bd996 tf_resource_type=aws_internet_gateway tf_rpc=ApplyResourceChange diagnostic_error_count=1 @module=sdk.proto diagnostic_warning_count=0 tf_proto_version=5.3 @caller=logging/keys.go timestamp=2022-12-15T00:16:20.799Z
2022-12-15T00:16:25.900Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=aaaa1111-bb22-cc33-dd44-ee5555555555 tf_data_source_type=aws_ami tf_rpc=ReadDataSource tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=4200 @module=sdk.proto timestamp=2022-12-15T00:16:25.899Z
```

`testdata/core-only.log` — core entries, no provider, with a multi-line entry:

```
# source: https://gist.github.com/Nilsils/7c0e60d4d200f81f3f9a0a66a9fe37ee
2026-08-29T10:34:43.123+0200 [INFO]  Terraform version: 1.16.0
2026-08-29T10:34:43.124+0200 [DEBUG] using github.com/hashicorp/go-tfe v1.108.0
2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete
2026-08-29T10:34:43.151+0200 [TRACE] building graph for terraform dependencies
2026-08-29T10:34:43.219+0200 [ERROR] Provider produced inconsistent result: on
terraform_data.r1. Only destroying should always produce a null value, so
this is always a bug in the provider and should be reported.
2026-08-29T10:34:43.220+0200 [TRACE] statemgr.Filesystem: unlocking terraform.tfstate using fcntl flock
```

`testdata/mixed-hcp.log` — the shape expected from an HCP raw run log: hclog interleaved with plan output. The non-hclog block is representative, not copied from a real run.

```
# source: synthesised to match HCP Terraform raw run log shape (hclog + plan output interleaved)
2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=2634bc46-bb66-3d22-528d-d2eaf8165f52 tf_resource_type=aws_subnet tf_rpc=PlanResourceChange tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=12 @module=sdk.proto
Terraform used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
  + create

Terraform will perform the following actions:

  # aws_subnet.example will be created
  + resource "aws_subnet" "example" {
      + id = (known after apply)
    }

Plan: 1 to add, 0 to change, 0 to destroy.
2022-12-15T00:16:21.000Z [TRACE] statemgr.Filesystem: unlocking terraform.tfstate using fcntl flock
```

- [ ] **Step 3: Write the failing tests**

`internal/logfmt/level_test.go`:

```go
package logfmt

import "testing"

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want Level
	}{
		{"TRACE", LevelTrace},
		{"DEBUG", LevelDebug},
		{"INFO", LevelInfo},
		{"WARN", LevelWarn},
		{"ERROR", LevelError},
		{"", LevelUnknown},
		{"NOPE", LevelUnknown},
	}
	for _, c := range cases {
		if got := ParseLevel(c.in); got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLevelString(t *testing.T) {
	if got := LevelTrace.String(); got != "TRACE" {
		t.Errorf("LevelTrace.String() = %q, want %q", got, "TRACE")
	}
	if got := LevelUnknown.String(); got != "UNKNOWN" {
		t.Errorf("LevelUnknown.String() = %q, want %q", got, "UNKNOWN")
	}
}
```

`internal/logfmt/intern_test.go`:

```go
package logfmt

import "testing"

func TestInternerReturnsSameIDForSameString(t *testing.T) {
	var in Interner
	a := in.Intern("provider.aws")
	b := in.Intern("provider.aws")
	if a != b {
		t.Fatalf("Intern returned %d then %d for the same string", a, b)
	}
}

func TestInternerRoundTrips(t *testing.T) {
	var in Interner
	id := in.Intern("statemgr.Filesystem")
	if got := in.Lookup(id); got != "statemgr.Filesystem" {
		t.Errorf("Lookup(%d) = %q, want %q", id, got, "statemgr.Filesystem")
	}
}

func TestInternerEmptyStringIsZero(t *testing.T) {
	var in Interner
	if id := in.Intern(""); id != 0 {
		t.Errorf("Intern(\"\") = %d, want 0", id)
	}
	if got := in.Lookup(0); got != "" {
		t.Errorf("Lookup(0) = %q, want empty", got)
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/logfmt/ -run 'TestParseLevel|TestLevelString|TestInterner' -v`
Expected: FAIL — build error, `undefined: Level`, `undefined: Interner`.

- [ ] **Step 5: Implement `level.go`**

```go
// Package logfmt parses Terraform TF_LOG output into logical entries.
package logfmt

// Level is a log level. Ordering is by severity, so filters can compare.
type Level uint8

const (
	LevelUnknown Level = iota
	LevelTrace
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel converts a bare level name, as it appears between the brackets
// of a log line, into a Level. Unrecognised names give LevelUnknown.
func ParseLevel(s string) Level {
	switch s {
	case "TRACE":
		return LevelTrace
	case "DEBUG":
		return LevelDebug
	case "INFO":
		return LevelInfo
	case "WARN":
		return LevelWarn
	case "ERROR":
		return LevelError
	}
	return LevelUnknown
}

func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return "UNKNOWN"
}
```

- [ ] **Step 6: Implement `intern.go`**

```go
package logfmt

// Interner maps component names to small integer ids so entries can stay
// pointer-free. ID 0 is always the empty string, which represents an entry
// with no component prefix.
type Interner struct {
	ids  map[string]uint16
	strs []string
}

// Intern returns a stable id for s, allocating one if needed.
func (i *Interner) Intern(s string) uint16 {
	if i.strs == nil {
		i.ids = map[string]uint16{"": 0}
		i.strs = []string{""}
	}
	if id, ok := i.ids[s]; ok {
		return id
	}
	id := uint16(len(i.strs))
	i.strs = append(i.strs, s)
	i.ids[s] = id
	return id
}

// Lookup returns the string for an id, or "" if the id is unknown.
func (i *Interner) Lookup(id uint16) string {
	if int(id) >= len(i.strs) {
		return ""
	}
	return i.strs[id]
}

// Len reports how many distinct strings have been interned.
func (i *Interner) Len() int { return len(i.strs) }
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/logfmt/ -v`
Expected: PASS, all tests.

- [ ] **Step 8: Commit**

```bash
git add go.mod internal/logfmt testdata
git commit -m "Add module scaffolding, log levels, interning and real-log fixtures"
```

---

### Task 2: Line header parsing

**Files:**
- Create: `internal/logfmt/header.go`
- Test: `internal/logfmt/header_test.go`

**Interfaces:**
- Consumes: `Level`, `ParseLevel` from Task 1.
- Produces: `logfmt.Header` struct with fields `TS time.Time`, `HasTS bool`, `Level Level`, `Comp string`, `Msg string`; and `logfmt.ParseHeader(line string) Header`.

**Behaviour this task must get right**, all derived from real log lines:

- Two offset formats: `2022-12-15T00:16:20.800Z` and `2026-08-29T10:34:43.123+0200`.
- Levels are space-padded inside the brackets' trailing space: `[INFO]  Terraform version` has two spaces after `]`.
- A component is the token before the first `:` **only if that token contains no space**. `terraform.NewContext: complete` has component `terraform.NewContext`; `Terraform version: 1.16.0` has none, because `Terraform version` contains a space.
- A line with no timestamp is not a header at all — `HasTS` is false and the caller treats it as a continuation.

- [ ] **Step 1: Write the failing test**

```go
package logfmt

import "testing"

func TestParseHeaderProviderLine(t *testing.T) {
	line := `2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=abc tf_req_duration_ms=5`
	h := ParseHeader(line)
	if !h.HasTS {
		t.Fatal("HasTS = false, want true")
	}
	if h.Level != LevelTrace {
		t.Errorf("Level = %v, want TRACE", h.Level)
	}
	if h.Comp != "provider.terraform-provider-aws_v4.46.0_x5" {
		t.Errorf("Comp = %q", h.Comp)
	}
	if h.Msg != "Received downstream response: tf_req_id=abc tf_req_duration_ms=5" {
		t.Errorf("Msg = %q", h.Msg)
	}
	if h.TS.UnixMilli() != 1671063380800 {
		t.Errorf("TS.UnixMilli() = %d, want 1671063380800", h.TS.UnixMilli())
	}
}

func TestParseHeaderNumericOffset(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete`)
	if !h.HasTS {
		t.Fatal("HasTS = false, want true")
	}
	if h.Comp != "terraform.NewContext" {
		t.Errorf("Comp = %q, want terraform.NewContext", h.Comp)
	}
	if h.Msg != "complete" {
		t.Errorf("Msg = %q, want complete", h.Msg)
	}
	if _, off := h.TS.Zone(); off != 2*60*60 {
		t.Errorf("zone offset = %d, want 7200", off)
	}
}

func TestParseHeaderPaddedLevelAndNoComponent(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.123+0200 [INFO]  Terraform version: 1.16.0`)
	if h.Level != LevelInfo {
		t.Errorf("Level = %v, want INFO", h.Level)
	}
	// "Terraform version" contains a space, so it is not a component.
	if h.Comp != "" {
		t.Errorf("Comp = %q, want empty", h.Comp)
	}
	if h.Msg != "Terraform version: 1.16.0" {
		t.Errorf("Msg = %q", h.Msg)
	}
}

func TestParseHeaderNoColonMeansNoComponent(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.151+0200 [TRACE] building graph for terraform dependencies`)
	if h.Comp != "" {
		t.Errorf("Comp = %q, want empty", h.Comp)
	}
	if h.Msg != "building graph for terraform dependencies" {
		t.Errorf("Msg = %q", h.Msg)
	}
}

func TestParseHeaderContinuationLine(t *testing.T) {
	h := ParseHeader(`this is always a bug in the provider and should be reported.`)
	if h.HasTS {
		t.Error("HasTS = true, want false for a continuation line")
	}
}

func TestParseHeaderPlanOutputIsNotAHeader(t *testing.T) {
	h := ParseHeader(`  + resource "aws_subnet" "example" {`)
	if h.HasTS {
		t.Error("HasTS = true, want false for plan output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/logfmt/ -run TestParseHeader -v`
Expected: FAIL — `undefined: ParseHeader`.

- [ ] **Step 3: Implement `header.go`**

```go
package logfmt

import (
	"strings"
	"time"
)

// tsLayout matches both offset forms Terraform emits: a "Z" for UTC and a
// numeric "+0200" with no colon.
const tsLayout = "2006-01-02T15:04:05.000Z0700"

// Header is the parsed prefix of a single physical log line.
type Header struct {
	TS    time.Time
	HasTS bool
	Level Level
	Comp  string // component prefix, or "" if the line has none
	Msg   string // message with the component prefix removed
}

// ParseHeader parses one physical line. A line without a leading timestamp is
// not a log entry header — HasTS is false and the remaining fields are unset,
// which the scanner treats as continuation or interleaved non-hclog content.
func ParseHeader(line string) Header {
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return Header{}
	}
	ts, err := time.Parse(tsLayout, line[:sp])
	if err != nil {
		return Header{}
	}

	h := Header{TS: ts, HasTS: true}
	rest := strings.TrimLeft(line[sp+1:], " ")

	if strings.HasPrefix(rest, "[") {
		if end := strings.IndexByte(rest, ']'); end > 0 {
			h.Level = ParseLevel(rest[1:end])
			rest = strings.TrimLeft(rest[end+1:], " ")
		}
	}

	h.Comp, h.Msg = splitComponent(rest)
	return h
}

// splitComponent peels a leading "component: " prefix off a message. Terraform
// core writes messages that begin with a component name, and hclog renders a
// named logger the same way, so the two are indistinguishable and are treated
// alike. A candidate containing a space is prose, not a component.
func splitComponent(s string) (comp, msg string) {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", s
	}
	candidate := s[:colon]
	if strings.ContainsAny(candidate, " \t") {
		return "", s
	}
	return candidate, strings.TrimLeft(s[colon+1:], " ")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logfmt/ -run TestParseHeader -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/logfmt/header.go internal/logfmt/header_test.go
git commit -m "Parse log line headers: timestamps, levels and component prefixes"
```

---

### Task 3: Structured field parsing

**Files:**
- Create: `internal/logfmt/fields.go`
- Test: `internal/logfmt/fields_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `logfmt.Field` (`struct{ Key, Val string }`), `logfmt.Fields` (`[]Field`) with method `Get(key string) (string, bool)`, and `logfmt.ParseFields(s string, dst Fields) Fields` which appends into `dst` and returns it so the caller can reuse the backing array.

**Behaviour this task must get right:**

- Field order is not stable between lines, so lookup is by key.
- Values may be quoted when they contain spaces: `key="some value"`.
- Keys may be `@`-prefixed hclog metadata: `@module=sdk.proto`.
- A value may itself contain `=` (`tf_provider_addr=registry.terraform.io/hashicorp/aws` is fine, but URLs with query strings occur too). Split on the **first** `=` only.
- Text before the first `key=` pair is message text, not fields, and must be ignored.

- [ ] **Step 1: Write the failing test**

```go
package logfmt

import "testing"

func TestParseFieldsUnorderedLookup(t *testing.T) {
	s := `tf_req_id=abc tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange tf_req_duration_ms=5 @module=sdk.proto`
	f := ParseFields(s, nil)

	want := map[string]string{
		"tf_req_id":          "abc",
		"tf_resource_type":   "aws_subnet",
		"tf_rpc":             "ApplyResourceChange",
		"tf_req_duration_ms": "5",
		"@module":            "sdk.proto",
	}
	for k, v := range want {
		got, ok := f.Get(k)
		if !ok {
			t.Errorf("Get(%q): not found", k)
			continue
		}
		if got != v {
			t.Errorf("Get(%q) = %q, want %q", k, got, v)
		}
	}
}

func TestParseFieldsQuotedValue(t *testing.T) {
	f := ParseFields(`tf_rpc=ReadResource diagnostic_summary="something went wrong" tf_req_duration_ms=7`, nil)
	if got, _ := f.Get("diagnostic_summary"); got != "something went wrong" {
		t.Errorf("quoted value = %q", got)
	}
	if got, _ := f.Get("tf_req_duration_ms"); got != "7" {
		t.Errorf("field after quoted value = %q, want 7", got)
	}
}

func TestParseFieldsValueContainingEquals(t *testing.T) {
	f := ParseFields(`url=https://example.com/a?b=c tf_rpc=ReadResource`, nil)
	if got, _ := f.Get("url"); got != "https://example.com/a?b=c" {
		t.Errorf("value with = : %q", got)
	}
	if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
		t.Errorf("following field = %q", got)
	}
}

func TestParseFieldsIgnoresLeadingProse(t *testing.T) {
	f := ParseFields(`Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=3`, nil)
	if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
		t.Errorf("tf_rpc = %q", got)
	}
	if _, ok := f.Get("Received"); ok {
		t.Error("prose word parsed as a field key")
	}
}

func TestParseFieldsMissingKey(t *testing.T) {
	f := ParseFields(`tf_rpc=ReadResource`, nil)
	if _, ok := f.Get("tf_req_duration_ms"); ok {
		t.Error("Get reported a key that is not present")
	}
}

func TestParseFieldsReusesBuffer(t *testing.T) {
	buf := ParseFields(`a=1 b=2`, nil)
	buf = ParseFields(`c=3`, buf[:0])
	if len(buf) != 1 {
		t.Fatalf("len = %d, want 1", len(buf))
	}
	if got, _ := buf.Get("c"); got != "3" {
		t.Errorf("Get(c) = %q, want 3", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/logfmt/ -run TestParseFields -v`
Expected: FAIL — `undefined: ParseFields`.

- [ ] **Step 3: Implement `fields.go`**

```go
package logfmt

import "strings"

// Field is one structured key=value pair from a log line.
type Field struct {
	Key, Val string
}

// Fields is the set of structured pairs on a line. Order is not meaningful:
// Terraform emits the same fields in different orders on different lines.
type Fields []Field

// Get returns the value for key.
func (f Fields) Get(key string) (string, bool) {
	for _, fl := range f {
		if fl.Key == key {
			return fl.Val, true
		}
	}
	return "", false
}

// ParseFields extracts key=value pairs from s, appending to dst and returning
// the result. Pass dst[:0] to reuse a buffer across lines and avoid allocating
// per entry. Text before the first pair is message prose and is skipped.
func ParseFields(s string, dst Fields) Fields {
	for i := 0; i < len(s); {
		// Skip whitespace.
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		// A token runs to the next space, unless it contains a quoted value.
		key, val, next, ok := parsePair(s, i)
		if ok {
			dst = append(dst, Field{Key: key, Val: val})
		}
		i = next
	}
	return dst
}

// parsePair reads one whitespace-delimited token starting at i. It returns ok
// only when the token is a key=value pair; prose words are skipped.
func parsePair(s string, i int) (key, val string, next int, ok bool) {
	start := i
	eq := -1
	for ; i < len(s); i++ {
		if s[i] == '=' && eq < 0 {
			eq = i
			// A quoted value may contain spaces, so consume it wholesale.
			if i+1 < len(s) && s[i+1] == '"' {
				if end := strings.IndexByte(s[i+2:], '"'); end >= 0 {
					return s[start:eq], s[i+2 : i+2+end], i + 3 + end, true
				}
			}
			continue
		}
		if s[i] == ' ' || s[i] == '\t' {
			break
		}
	}
	if eq < 0 || eq == start {
		return "", "", i, false
	}
	return s[start:eq], s[eq+1 : i], i, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logfmt/ -run TestParseFields -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/logfmt/fields.go internal/logfmt/fields_test.go
git commit -m "Parse unordered key=value log fields, including quoted values"
```

---

### Task 4: ANSI escape handling

**Files:**
- Create: `internal/logfmt/ansi.go`
- Test: `internal/logfmt/ansi_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `logfmt.HasANSI(s string) bool` and `logfmt.StripANSI(s string, dst []byte) string`.

**Why this exists:** an HCP Terraform raw run log is captured terminal output and may contain colour escapes. Whether it does is unconfirmed, so the parser strips them defensively and `--diagnose` reports whether any were seen.

- [ ] **Step 1: Write the failing test**

```go
package logfmt

import "testing"

func TestHasANSI(t *testing.T) {
	if HasANSI("plain text") {
		t.Error("HasANSI reported true for plain text")
	}
	if !HasANSI("\x1b[31mred\x1b[0m") {
		t.Error("HasANSI reported false for coloured text")
	}
}

func TestStripANSI(t *testing.T) {
	got := StripANSI("\x1b[1m\x1b[32m+ create\x1b[0m", nil)
	if got != "+ create" {
		t.Errorf("StripANSI = %q, want %q", got, "+ create")
	}
}

func TestStripANSILeavesPlainTextUntouched(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: b"
	if got := StripANSI(in, nil); got != in {
		t.Errorf("StripANSI altered plain text: %q", got)
	}
}

func TestStripANSIUnterminatedSequence(t *testing.T) {
	// A truncated escape at end of line must not panic or hang.
	if got := StripANSI("text\x1b[", nil); got != "text" {
		t.Errorf("StripANSI = %q, want %q", got, "text")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/logfmt/ -run ANSI -v`
Expected: FAIL — `undefined: HasANSI`.

- [ ] **Step 3: Implement `ansi.go`**

```go
package logfmt

import "strings"

// HasANSI reports whether s contains an ANSI escape sequence.
func HasANSI(s string) bool { return strings.IndexByte(s, 0x1b) >= 0 }

// StripANSI removes ANSI escape sequences from s. dst is an optional scratch
// buffer to avoid allocating per line; pass dst[:0] to reuse it. Strings with
// no escapes are returned unchanged without copying.
func StripANSI(s string, dst []byte) string {
	if !HasANSI(s) {
		return s
	}
	dst = dst[:0]
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			dst = append(dst, s[i])
			i++
			continue
		}
		// Skip ESC, an optional '[', then parameter bytes, then one final
		// byte. An unterminated sequence consumes the rest of the string.
		i++
		if i < len(s) && s[i] == '[' {
			i++
		}
		for i < len(s) && (s[i] < '@' || s[i] > '~') {
			i++
		}
		if i < len(s) {
			i++
		}
	}
	return string(dst)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logfmt/ -run ANSI -v`
Expected: PASS, all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/logfmt/ansi.go internal/logfmt/ansi_test.go
git commit -m "Strip ANSI escape sequences from captured terminal output"
```

---

### Task 5: The streaming scanner

**Files:**
- Create: `internal/logfmt/entry.go`, `internal/logfmt/scan.go`
- Test: `internal/logfmt/scan_test.go`

**Interfaces:**
- Consumes: `ParseHeader`, `ParseFields`, `StripANSI`, `HasANSI`, `Interner`, `Level`.
- Produces:
  - `logfmt.Entry` — `struct{ Off uint64; Len uint32; TSms uint32; Level Level; Comp uint16; Lines uint16; Timestamped bool }`
  - `logfmt.Sink` — `interface{ Entry(e Entry, msg string, f Fields) }`
  - `logfmt.Scan(r io.Reader, comps *Interner, sinks ...Sink) (Stats, error)`
  - `logfmt.Stats` — `struct{ Entries, PhysicalLines, ContinuationLines, Untimestamped, ByLevel [6]uint64; Bytes uint64; SawANSI bool; FirstTS, LastTS time.Time }`

**Critical behaviour:** an entry is a timestamped line plus every untimestamped line that follows it. `Off`/`Len` cover all of them. Untimestamped lines appearing *before* any timestamped line form a leading unstructured entry with `Timestamped: false`. `msg` and `f` passed to a sink are only valid for the duration of the call — sinks must copy anything they retain.

- [ ] **Step 1: Write the failing test**

```go
package logfmt

import (
	"strings"
	"testing"
)

// collector records what a sink was given, copying anything retained.
type collector struct {
	entries []Entry
	msgs    []string
	rpcs    []string
}

func (c *collector) Entry(e Entry, msg string, f Fields) {
	c.entries = append(c.entries, e)
	c.msgs = append(c.msgs, msg)
	rpc, _ := f.Get("tf_rpc")
	c.rpcs = append(c.rpcs, rpc)
}

func TestScanGroupsContinuationLines(t *testing.T) {
	in := "2026-08-29T10:34:43.219+0200 [ERROR] Provider produced inconsistent result: on\n" +
		"terraform_data.r1. Only destroying should always produce a null value, so\n" +
		"this is always a bug in the provider and should be reported.\n" +
		"2026-08-29T10:34:43.220+0200 [TRACE] statemgr.Filesystem: unlocking\n"

	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.entries))
	}
	if c.entries[0].Lines != 3 {
		t.Errorf("entry 0 Lines = %d, want 3", c.entries[0].Lines)
	}
	if st.ContinuationLines != 2 {
		t.Errorf("ContinuationLines = %d, want 2", st.ContinuationLines)
	}
	if st.PhysicalLines != 4 {
		t.Errorf("PhysicalLines = %d, want 4", st.PhysicalLines)
	}
	if comps.Lookup(c.entries[1].Comp) != "statemgr.Filesystem" {
		t.Errorf("entry 1 component = %q", comps.Lookup(c.entries[1].Comp))
	}
}

func TestScanOffsetsCoverWholeEntry(t *testing.T) {
	first := "2026-08-29T10:34:43.219+0200 [ERROR] a: one\n"
	cont := "continued\n"
	in := first + cont + "2026-08-29T10:34:43.220+0200 [TRACE] b: two\n"

	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.entries[0].Off != 0 {
		t.Errorf("entry 0 Off = %d, want 0", c.entries[0].Off)
	}
	if want := uint32(len(first) + len(cont)); c.entries[0].Len != want {
		t.Errorf("entry 0 Len = %d, want %d", c.entries[0].Len, want)
	}
	if want := uint64(len(first) + len(cont)); c.entries[1].Off != want {
		t.Errorf("entry 1 Off = %d, want %d", c.entries[1].Off, want)
	}
}

func TestScanRelativeTimestamps(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: first\n" +
		"2022-12-15T00:16:25.900Z [TRACE] a: second\n"
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.entries[0].TSms != 0 {
		t.Errorf("first TSms = %d, want 0", c.entries[0].TSms)
	}
	if c.entries[1].TSms != 5100 {
		t.Errorf("second TSms = %d, want 5100", c.entries[1].TSms)
	}
}

func TestScanPassesFieldsToSink(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n"
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.rpcs[0] != "ReadResource" {
		t.Errorf("tf_rpc = %q, want ReadResource", c.rpcs[0])
	}
}

func TestScanLeadingUnstructuredContent(t *testing.T) {
	in := "Terraform will perform the following actions:\n" +
		"  + resource \"aws_subnet\" \"example\" {\n" +
		"2022-12-15T00:16:20.800Z [TRACE] a: real entry\n"
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.entries))
	}
	if c.entries[0].Timestamped {
		t.Error("leading block should not be marked timestamped")
	}
	if c.entries[0].Lines != 2 {
		t.Errorf("leading block Lines = %d, want 2", c.entries[0].Lines)
	}
	if st.Untimestamped != 1 {
		t.Errorf("Untimestamped = %d, want 1", st.Untimestamped)
	}
}

func TestScanCountsLevelsAndDetectsANSI(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		"2022-12-15T00:16:20.801Z [DEBUG] a: two\n" +
		"2022-12-15T00:16:20.802Z [TRACE] a: \x1b[31mthree\x1b[0m\n"
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.ByLevel[LevelTrace] != 2 {
		t.Errorf("TRACE count = %d, want 2", st.ByLevel[LevelTrace])
	}
	if st.ByLevel[LevelDebug] != 1 {
		t.Errorf("DEBUG count = %d, want 1", st.ByLevel[LevelDebug])
	}
	if !st.SawANSI {
		t.Error("SawANSI = false, want true")
	}
	if c.msgs[2] != "three" {
		t.Errorf("message = %q, want ANSI stripped to %q", c.msgs[2], "three")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/logfmt/ -run TestScan -v`
Expected: FAIL — `undefined: Scan`, `undefined: Entry`.

- [ ] **Step 3: Implement `entry.go`**

```go
package logfmt

import "time"

// Entry is one logical log entry: a timestamped line plus any continuation
// lines that follow it. Off and Len cover every line of the entry, so a
// consumer can seek to Off and read Len bytes to render it whole.
//
// The struct holds no pointers and no strings so that a large slice of them
// costs the garbage collector nothing to scan.
type Entry struct {
	Off         uint64 // byte offset of the entry's first line
	Len         uint32 // bytes covering all lines of the entry
	TSms        uint32 // milliseconds since the first timestamped entry
	Level       Level
	Comp        uint16 // interned component; 0 means none
	Lines       uint16 // physical line count
	Timestamped bool   // false for interleaved non-hclog content
}

// Stats summarises a scan. It is the raw material of the diagnostic report.
type Stats struct {
	Entries           uint64
	PhysicalLines     uint64
	ContinuationLines uint64
	Untimestamped     uint64 // entries with no timestamp at all
	ByLevel           [6]uint64
	Bytes             uint64
	SawANSI           bool
	FirstTS, LastTS   time.Time
}
```

- [ ] **Step 4: Implement `scan.go`**

```go
package logfmt

import (
	"bufio"
	"io"
	"strings"
	"time"
)

// Sink receives each entry as it is parsed. The msg and f arguments are only
// valid for the duration of the call; a sink that retains them must copy.
type Sink interface {
	Entry(e Entry, msg string, f Fields)
}

// Scan reads r in a single pass, assembling logical entries and pushing each
// to every sink. Memory use is independent of input size: nothing is retained
// between entries.
func Scan(r io.Reader, comps *Interner, sinks ...Sink) (Stats, error) {
	var st Stats
	br := bufio.NewReaderSize(r, 256*1024)

	var (
		fieldBuf Fields
		ansiBuf  []byte
		off      uint64 // offset of the next line to be read

		// The entry currently being assembled.
		open     bool
		cur      Entry
		curMsg   strings.Builder
		curHdr   Header
		baseTS   time.Time
		haveBase bool
	)

	flush := func() {
		if !open {
			return
		}
		msg := curMsg.String()
		fieldBuf = ParseFields(msg, fieldBuf[:0])
		st.Entries++
		if !cur.Timestamped {
			st.Untimestamped++
		}
		st.ByLevel[cur.Level]++
		for _, s := range sinks {
			s.Entry(cur, msg, fieldBuf)
		}
		open = false
		curMsg.Reset()
	}

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			raw := uint32(len(line))
			text := strings.TrimRight(line, "\r\n")
			if HasANSI(text) {
				st.SawANSI = true
				text = StripANSI(text, ansiBuf)
			}

			st.PhysicalLines++
			st.Bytes += uint64(raw)

			h := ParseHeader(text)
			switch {
			case h.HasTS:
				flush()
				if !haveBase {
					baseTS, haveBase = h.TS, true
					st.FirstTS = h.TS
				}
				st.LastTS = h.TS
				cur = Entry{
					Off:         off,
					Len:         raw,
					TSms:        uint32(h.TS.Sub(baseTS).Milliseconds()),
					Level:       h.Level,
					Comp:        comps.Intern(h.Comp),
					Lines:       1,
					Timestamped: true,
				}
				curHdr = h
				curMsg.WriteString(h.Msg)
				open = true

			case open:
				// Continuation of the entry in progress.
				st.ContinuationLines++
				cur.Len += raw
				cur.Lines++
				curMsg.WriteByte('\n')
				curMsg.WriteString(text)

			default:
				// Interleaved non-hclog content before any entry, such as
				// plan output at the head of an HCP run log.
				cur = Entry{Off: off, Len: raw, Lines: 1}
				curMsg.WriteString(text)
				open = true
			}
			_ = curHdr
			off += uint64(raw)
		}

		if err != nil {
			flush()
			if err == io.EOF {
				return st, nil
			}
			return st, err
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/logfmt/ -v`
Expected: PASS, all tests including the six new scan tests.

- [ ] **Step 6: Verify against the real fixtures**

Run: `go test ./internal/logfmt/ -run TestScan -count=1`
Then add and run this fixture test in `scan_test.go`:

```go
func TestScanRealFixtures(t *testing.T) {
	cases := []struct {
		file           string
		wantMinEntries int
		wantANSI       bool
	}{
		{"../../testdata/provider-rpc.log", 3, false},
		{"../../testdata/core-only.log", 6, false},
		{"../../testdata/mixed-hcp.log", 3, false},
	}
	for _, c := range cases {
		f, err := os.Open(c.file)
		if err != nil {
			t.Fatalf("open %s: %v", c.file, err)
		}
		var comps Interner
		var col collector
		st, err := Scan(f, &comps, &col)
		f.Close()
		if err != nil {
			t.Fatalf("Scan %s: %v", c.file, err)
		}
		if st.Entries < uint64(c.wantMinEntries) {
			t.Errorf("%s: %d entries, want at least %d", c.file, st.Entries, c.wantMinEntries)
		}
		if st.SawANSI != c.wantANSI {
			t.Errorf("%s: SawANSI = %v, want %v", c.file, st.SawANSI, c.wantANSI)
		}
	}
}
```

Add `"os"` to the test file imports. Note the fixtures' leading `# source:` comment line is untimestamped and becomes a leading unstructured entry — that is correct behaviour and is why the assertions use a minimum rather than an exact count.

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/logfmt/entry.go internal/logfmt/scan.go internal/logfmt/scan_test.go
git commit -m "Add streaming scanner assembling logical entries from log lines"
```

---

### Task 6: Tier 1 span extraction

**Files:**
- Create: `internal/span/span.go`, `internal/span/reported.go`
- Test: `internal/span/reported_test.go`

**Interfaces:**
- Consumes: `logfmt.Entry`, `logfmt.Fields`, `logfmt.Sink`.
- Produces:
  - `span.Fidelity` (`FidelityReported|FidelityPaired|FidelitySequential|FidelityInferred`) with `String()`.
  - `span.Span` — `struct{ StartEntry, EndEntry uint32; StartMs, EndMs uint32; RPC, Provider, ResourceType string; Fidelity Fidelity }`
  - `span.ReportedBuilder` with `Entry(logfmt.Entry, string, logfmt.Fields)` (satisfying `logfmt.Sink`) and `Spans() []Span`.

**Note on the string fields:** phase 1 stores plain strings on `Span` because the span count is small (thousands) even for a large log — only response entries produce spans. Interning moves in when the TUI needs it.

**The marker:** an entry produces a span when its message begins with `Received downstream response` **and** it carries `tf_req_duration_ms`. Start time is derived as `end − duration`, per the spec.

- [ ] **Step 1: Write the failing test**

```go
package span

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func scanInto(t *testing.T, in string, b *ReportedBuilder) {
	t.Helper()
	var comps logfmt.Interner
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, b); err != nil {
		t.Fatalf("Scan: %v", err)
	}
}

func TestReportedBuilderReadsDuration(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=abc tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5000` + "\n"

	var b ReportedBuilder
	scanInto(t, in, &b)

	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	s := got[0]
	if s.RPC != "ApplyResourceChange" {
		t.Errorf("RPC = %q", s.RPC)
	}
	if s.ResourceType != "aws_subnet" {
		t.Errorf("ResourceType = %q", s.ResourceType)
	}
	if s.Provider != "registry.terraform.io/hashicorp/aws" {
		t.Errorf("Provider = %q", s.Provider)
	}
	if s.Fidelity != FidelityReported {
		t.Errorf("Fidelity = %v, want Reported", s.Fidelity)
	}
	if s.EndMs-s.StartMs != 5000 {
		t.Errorf("duration = %d ms, want 5000", s.EndMs-s.StartMs)
	}
}

func TestReportedBuilderUsesDataSourceType(t *testing.T) {
	in := `2022-12-15T00:16:25.900Z [TRACE] provider.aws: Received downstream response: tf_data_source_type=aws_ami tf_rpc=ReadDataSource tf_req_duration_ms=4200` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].ResourceType != "aws_ami" {
		t.Errorf("ResourceType = %q, want aws_ami", got[0].ResourceType)
	}
}

func TestReportedBuilderIgnoresEntriesWithoutDuration(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_rpc=ReadResource\n" +
		"2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("got %d spans, want 0", len(got))
	}
}

func TestReportedBuilderClampsStartAtZero(t *testing.T) {
	// A duration longer than the elapsed log time must not underflow.
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=99999` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].StartMs != 0 {
		t.Errorf("StartMs = %d, want 0 (clamped)", got[0].StartMs)
	}
}

func TestReportedBuilderIgnoresUnparseableDuration(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=notanumber` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("got %d spans, want 0", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/span/ -v`
Expected: FAIL — `undefined: ReportedBuilder`.

- [ ] **Step 3: Implement `span.go`**

```go
// Package span turns parsed log entries into timed provider operations.
package span

// Fidelity records how a span's duration was established. It is surfaced in
// the UI so an inferred number is never mistaken for a measured one.
type Fidelity uint8

const (
	// FidelityReported means the provider logged its own duration.
	FidelityReported Fidelity = iota
	// FidelityPaired means request and response lines were correlated by id.
	FidelityPaired
	// FidelitySequential means calls were paired within one plugin stream.
	FidelitySequential
	// FidelityInferred means the duration is a wall-clock gap attribution.
	FidelityInferred
)

func (f Fidelity) String() string {
	switch f {
	case FidelityReported:
		return "reported"
	case FidelityPaired:
		return "paired"
	case FidelitySequential:
		return "sequential"
	case FidelityInferred:
		return "inferred"
	}
	return "unknown"
}

// Span is one provider operation with a measured duration. Times are
// milliseconds relative to the first timestamped entry in the log.
type Span struct {
	StartEntry, EndEntry uint32
	StartMs, EndMs       uint32
	RPC                  string
	Provider             string
	ResourceType         string
	Fidelity             Fidelity
}

// DurationMs returns the span's length in milliseconds.
func (s Span) DurationMs() uint32 { return s.EndMs - s.StartMs }
```

- [ ] **Step 4: Implement `reported.go`**

```go
package span

import (
	"strconv"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// responseMarker is the message Terraform's plugin protocol layer logs when a
// provider RPC returns. It carries tf_req_duration_ms, the provider's own
// measurement of the call.
const responseMarker = "Received downstream response"

// ReportedBuilder is the tier 1 span builder. It reads durations directly off
// response entries rather than inferring them, so its spans are exact.
//
// It satisfies logfmt.Sink and is fed during the scan.
type ReportedBuilder struct {
	spans []Span
	seq   uint32 // entry ordinal, incremented per entry seen
}

// Entry implements logfmt.Sink.
func (b *ReportedBuilder) Entry(e logfmt.Entry, msg string, f logfmt.Fields) {
	idx := b.seq
	b.seq++

	if !strings.HasPrefix(msg, responseMarker) {
		return
	}
	raw, ok := f.Get("tf_req_duration_ms")
	if !ok {
		return
	}
	ms, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return
	}

	start := uint32(0)
	if uint64(e.TSms) > ms {
		start = e.TSms - uint32(ms)
	}

	rpc, _ := f.Get("tf_rpc")
	provider, _ := f.Get("tf_provider_addr")
	resType, ok := f.Get("tf_resource_type")
	if !ok {
		resType, _ = f.Get("tf_data_source_type")
	}

	b.spans = append(b.spans, Span{
		StartEntry:   idx,
		EndEntry:     idx,
		StartMs:      start,
		EndMs:        e.TSms,
		RPC:          rpc,
		Provider:     provider,
		ResourceType: resType,
		Fidelity:     FidelityReported,
	})
}

// Spans returns the spans built so far, in the order they were logged.
func (b *ReportedBuilder) Spans() []Span { return b.spans }
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/span/ -v`
Expected: PASS, all five tests.

- [ ] **Step 6: Commit**

```bash
git add internal/span
git commit -m "Extract provider RPC spans from reported tf_req_duration_ms"
```

---

### Task 7: Capability sniffer

**Files:**
- Create: `internal/span/sniff.go`
- Test: `internal/span/sniff_test.go`

**Interfaces:**
- Consumes: `logfmt.Entry`, `logfmt.Fields`, `Fidelity`.
- Produces: `span.Sniffer` (satisfying `logfmt.Sink`) with `Report() Capabilities`; `span.Capabilities` — `struct{ ResponseEntries, RequestEntries, DurationFields, ReqIDFields, ProviderEntries, CoreVertexLines, CoreGRPCLines uint64 }` with method `BestFidelity() (Fidelity, bool)`.

**Why a separate sink from the builder:** the builder answers "what spans exist"; the sniffer answers "what could this log support", including evidence for tiers not yet implemented. Tomorrow's real-log run needs the second question answered even though only tier 1 is built.

- [ ] **Step 1: Write the failing test**

```go
package span

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func sniff(t *testing.T, in string) Capabilities {
	t.Helper()
	var comps logfmt.Interner
	var s Sniffer
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, &s); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return s.Report()
}

func TestSnifferDetectsReportedTier(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_req_id=abc tf_rpc=ReadResource tf_req_duration_ms=5` + "\n"
	c := sniff(t, in)
	if c.ResponseEntries != 1 {
		t.Errorf("ResponseEntries = %d, want 1", c.ResponseEntries)
	}
	if c.DurationFields != 1 {
		t.Errorf("DurationFields = %d, want 1", c.DurationFields)
	}
	if c.ProviderEntries != 1 {
		t.Errorf("ProviderEntries = %d, want 1", c.ProviderEntries)
	}
	f, ok := c.BestFidelity()
	if !ok || f != FidelityReported {
		t.Errorf("BestFidelity = %v, %v; want reported, true", f, ok)
	}
}

func TestSnifferFallsBackToPairedWhenNoDurations(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Sending request downstream: tf_req_id=abc tf_rpc=ReadResource\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_req_id=abc tf_rpc=ReadResource\n"
	c := sniff(t, in)
	if c.RequestEntries != 1 {
		t.Errorf("RequestEntries = %d, want 1", c.RequestEntries)
	}
	f, ok := c.BestFidelity()
	if !ok || f != FidelityPaired {
		t.Errorf("BestFidelity = %v, %v; want paired, true", f, ok)
	}
}

func TestSnifferReportsNothingUsableForCoreOnlyLog(t *testing.T) {
	in := "2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n" +
		`2026-08-29T10:34:43.152+0200 [TRACE] vertex "aws_subnet.a": visit complete` + "\n" +
		"2026-08-29T10:34:43.153+0200 [TRACE] GRPCProvider: GetProviderSchema\n"
	c := sniff(t, in)
	if c.ProviderEntries != 0 {
		t.Errorf("ProviderEntries = %d, want 0", c.ProviderEntries)
	}
	if c.CoreVertexLines != 1 {
		t.Errorf("CoreVertexLines = %d, want 1", c.CoreVertexLines)
	}
	if c.CoreGRPCLines != 1 {
		t.Errorf("CoreGRPCLines = %d, want 1", c.CoreGRPCLines)
	}
	if _, ok := c.BestFidelity(); ok {
		t.Error("BestFidelity reported a usable tier for a core-only log")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/span/ -run TestSniffer -v`
Expected: FAIL — `undefined: Sniffer`.

- [ ] **Step 3: Implement `sniff.go`**

```go
package span

import (
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

const requestMarker = "Sending request downstream"

// Capabilities records what evidence a log contains for each extraction tier.
// It answers "what could this log support", which is a different question from
// "what spans were built", and is the core of the diagnostic report.
type Capabilities struct {
	ResponseEntries uint64 // "Received downstream response" entries
	RequestEntries  uint64 // "Sending request downstream" entries
	DurationFields  uint64 // entries carrying tf_req_duration_ms
	ReqIDFields     uint64 // entries carrying tf_req_id
	ProviderEntries uint64 // entries whose component starts with "provider."
	CoreVertexLines uint64 // core graph-walk lines naming a resource address
	CoreGRPCLines   uint64 // core "GRPCProvider: <RPC>" lines
}

// BestFidelity reports the highest-fidelity tier this log can support, and
// whether any tier is usable at all.
func (c Capabilities) BestFidelity() (Fidelity, bool) {
	switch {
	case c.DurationFields > 0:
		return FidelityReported, true
	case c.RequestEntries > 0 && c.ResponseEntries > 0 && c.ReqIDFields > 0:
		return FidelityPaired, true
	case c.ResponseEntries > 0:
		return FidelitySequential, true
	case c.ProviderEntries > 0:
		return FidelityInferred, true
	}
	return FidelityReported, false
}

// Sniffer accumulates Capabilities during a scan. It satisfies logfmt.Sink.
type Sniffer struct {
	caps  Capabilities
	comps *logfmt.Interner
}

// NewSniffer returns a Sniffer that resolves component ids via comps.
func NewSniffer(comps *logfmt.Interner) *Sniffer { return &Sniffer{comps: comps} }

// Entry implements logfmt.Sink.
func (s *Sniffer) Entry(e logfmt.Entry, msg string, f logfmt.Fields) {
	if s.comps != nil && strings.HasPrefix(s.comps.Lookup(e.Comp), "provider.") {
		s.caps.ProviderEntries++
	}
	switch {
	case strings.HasPrefix(msg, responseMarker):
		s.caps.ResponseEntries++
	case strings.HasPrefix(msg, requestMarker):
		s.caps.RequestEntries++
	}
	if _, ok := f.Get("tf_req_duration_ms"); ok {
		s.caps.DurationFields++
	}
	if _, ok := f.Get("tf_req_id"); ok {
		s.caps.ReqIDFields++
	}
	if strings.HasPrefix(msg, `vertex "`) {
		s.caps.CoreVertexLines++
	}
	if s.comps != nil && s.comps.Lookup(e.Comp) == "GRPCProvider" {
		s.caps.CoreGRPCLines++
	}
}

// Report returns the accumulated capabilities.
func (s *Sniffer) Report() Capabilities { return s.caps }
```

- [ ] **Step 4: Fix the test helper to pass the interner**

The `Sniffer` needs the interner to resolve component names. Update `sniff` in `sniff_test.go`:

```go
func sniff(t *testing.T, in string) Capabilities {
	t.Helper()
	var comps logfmt.Interner
	s := NewSniffer(&comps)
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, s); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return s.Report()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/span/ -v`
Expected: PASS, all eight tests.

- [ ] **Step 6: Commit**

```bash
git add internal/span/sniff.go internal/span/sniff_test.go
git commit -m "Detect which span extraction tiers a log can support"
```

---

### Task 8: The diagnostic report

**Files:**
- Create: `internal/diagnose/diagnose.go`
- Test: `internal/diagnose/diagnose_test.go`

**Interfaces:**
- Consumes: `logfmt.Stats`, `logfmt.Interner`, `logfmt.Entry`, `logfmt.Fields`, `span.Capabilities`, `span.Span`.
- Produces: `diagnose.Collector` (satisfying `logfmt.Sink`) with `NewCollector(*logfmt.Interner) *Collector`; `diagnose.Report` struct; `diagnose.Build(logfmt.Stats, span.Capabilities, []span.Span, *Collector, *logfmt.Interner) Report`; `Report.Render(io.Writer) error`.

**The hard requirement:** no field *values* in the output. The collector records field **keys** and their counts, top component names, and unmatched message **templates** with values stripped. It must never record a value.

**Template stripping rule:** take the message, drop everything from the first `key=` pair onward, replace it with the sorted key names rendered as `key=<v>`.

- [ ] **Step 1: Write the failing test**

```go
package diagnose

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func build(t *testing.T, in string) Report {
	t.Helper()
	var comps logfmt.Interner
	c := NewCollector(&comps)
	sn := span.NewSniffer(&comps)
	var b span.ReportedBuilder
	st, err := logfmt.Scan(strings.NewReader(in), &comps, c, sn, &b)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return Build(st, sn.Report(), b.Spans(), c, &comps)
}

func TestReportNeverLeaksFieldValues(t *testing.T) {
	const secret = "SUPERSECRETVALUE"
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource api_token=` + secret + ` tf_req_duration_ms=5` + "\n"

	var sb strings.Builder
	if err := build(t, in).Render(&sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(sb.String(), secret) {
		t.Fatal("diagnostic output leaked a field value")
	}
	if !strings.Contains(sb.String(), "api_token") {
		t.Error("field key api_token missing from report")
	}
}

func TestReportCountsFieldKeys(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=9\n"
	r := build(t, in)
	if r.FieldKeys["tf_req_duration_ms"] != 2 {
		t.Errorf("tf_req_duration_ms count = %d, want 2", r.FieldKeys["tf_req_duration_ms"])
	}
}

func TestReportRecordsTierAndSpans(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5000` + "\n"
	r := build(t, in)
	if !r.TierUsable {
		t.Fatal("TierUsable = false, want true")
	}
	if r.Tier != span.FidelityReported {
		t.Errorf("Tier = %v, want reported", r.Tier)
	}
	if r.SpanCount != 1 {
		t.Errorf("SpanCount = %d, want 1", r.SpanCount)
	}
	if r.SlowestMs != 5000 {
		t.Errorf("SlowestMs = %d, want 5000", r.SlowestMs)
	}
}

func TestReportWarnsWhenNoProviderEntries(t *testing.T) {
	in := "2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n"
	var sb strings.Builder
	r := build(t, in)
	if r.TierUsable {
		t.Error("TierUsable = true for a core-only log")
	}
	if err := r.Render(&sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(sb.String(), "no provider RPC") {
		t.Errorf("report does not explain the absence of provider data:\n%s", sb.String())
	}
}

func TestReportTemplatesStripValues(t *testing.T) {
	in := "2026-08-29T10:34:43.151+0200 [TRACE] someNewThing: a message we do not recognise with id=12345\n"
	r := build(t, in)
	found := false
	for _, tpl := range r.TopTemplates {
		if strings.Contains(tpl.Text, "id=<v>") {
			found = true
		}
		if strings.Contains(tpl.Text, "12345") {
			t.Errorf("template leaked a value: %q", tpl.Text)
		}
	}
	if !found {
		t.Errorf("no stripped template recorded, got %+v", r.TopTemplates)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/diagnose/ -v`
Expected: FAIL — `undefined: NewCollector`.

- [ ] **Step 3: Implement `diagnose.go`**

```go
// Package diagnose summarises a log's structure without disclosing its
// contents. Its output is the only artefact intended to leave the machine
// holding the log, so it reports keys, counts and shapes -- never values.
package diagnose

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// maxTemplates caps how many distinct message shapes are reported.
const maxTemplates = 15

// Template is a message shape with its field values removed.
type Template struct {
	Text  string
	Count uint64
}

// Collector accumulates shape information during a scan. It satisfies
// logfmt.Sink.
type Collector struct {
	comps     *logfmt.Interner
	fieldKeys map[string]uint64
	templates map[string]uint64
	compCount map[string]uint64
}

// NewCollector returns a Collector resolving component ids via comps.
func NewCollector(comps *logfmt.Interner) *Collector {
	return &Collector{
		comps:     comps,
		fieldKeys: map[string]uint64{},
		templates: map[string]uint64{},
		compCount: map[string]uint64{},
	}
}

// Entry implements logfmt.Sink.
func (c *Collector) Entry(e logfmt.Entry, msg string, f logfmt.Fields) {
	c.compCount[c.comps.Lookup(e.Comp)]++
	for _, fl := range f {
		c.fieldKeys[fl.Key]++
	}
	c.templates[template(msg, f)]++
}

// template renders a message with every field value replaced by a placeholder,
// so a shape can be reported without disclosing content.
func template(msg string, f logfmt.Fields) string {
	prose := msg
	if len(f) > 0 {
		// Field text begins at the first key= pair; everything before it is prose.
		if i := strings.Index(msg, f[0].Key+"="); i >= 0 {
			prose = msg[:i]
		}
	}
	prose = strings.TrimRight(strings.SplitN(prose, "\n", 2)[0], " ")

	keys := make([]string, 0, len(f))
	for _, fl := range f {
		keys = append(keys, fl.Key+"=<v>")
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return prose
	}
	return prose + " " + strings.Join(keys, " ")
}

// Report is the finished diagnostic summary.
type Report struct {
	Stats        logfmt.Stats
	Caps         span.Capabilities
	Tier         span.Fidelity
	TierUsable   bool
	SpanCount    int
	SlowestMs    uint32
	TotalSpanMs  uint64
	FieldKeys    map[string]uint64
	TopComponents []Template
	TopTemplates  []Template
	DistinctComps int
}

// Build assembles a Report from a completed scan.
func Build(st logfmt.Stats, caps span.Capabilities, spans []span.Span, c *Collector, comps *logfmt.Interner) Report {
	tier, usable := caps.BestFidelity()
	r := Report{
		Stats:         st,
		Caps:          caps,
		Tier:          tier,
		TierUsable:    usable,
		SpanCount:     len(spans),
		FieldKeys:     c.fieldKeys,
		TopComponents: topN(c.compCount, maxTemplates),
		TopTemplates:  topN(c.templates, maxTemplates),
		DistinctComps: comps.Len(),
	}
	for _, s := range spans {
		d := s.DurationMs()
		r.TotalSpanMs += uint64(d)
		if d > r.SlowestMs {
			r.SlowestMs = d
		}
	}
	return r
}

func topN(m map[string]uint64, n int) []Template {
	out := make([]Template, 0, len(m))
	for k, v := range m {
		out = append(out, Template{Text: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Render writes the report as plain text for pasting into a conversation.
func (r Report) Render(w io.Writer) error {
	b := &strings.Builder{}

	fmt.Fprintf(b, "tfli diagnostic report\n")
	fmt.Fprintf(b, "======================\n\n")

	fmt.Fprintf(b, "SIZE\n")
	fmt.Fprintf(b, "  bytes              %d\n", r.Stats.Bytes)
	fmt.Fprintf(b, "  physical lines     %d\n", r.Stats.PhysicalLines)
	fmt.Fprintf(b, "  logical entries    %d\n", r.Stats.Entries)
	fmt.Fprintf(b, "  continuation lines %d\n", r.Stats.ContinuationLines)
	fmt.Fprintf(b, "  non-hclog entries  %d\n", r.Stats.Untimestamped)
	if r.Stats.PhysicalLines > 0 {
		fmt.Fprintf(b, "  mean line length   %d bytes\n", r.Stats.Bytes/r.Stats.PhysicalLines)
	}
	fmt.Fprintf(b, "  ANSI escapes       %v\n\n", r.Stats.SawANSI)

	fmt.Fprintf(b, "LEVELS\n")
	for l := logfmt.LevelUnknown; l <= logfmt.LevelError; l++ {
		if n := r.Stats.ByLevel[l]; n > 0 {
			fmt.Fprintf(b, "  %-8s %d\n", l, n)
		}
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "EXTRACTION\n")
	if r.TierUsable {
		fmt.Fprintf(b, "  selected tier      %s\n", r.Tier)
	} else {
		fmt.Fprintf(b, "  selected tier      NONE USABLE\n")
		fmt.Fprintf(b, "  This log contains no provider RPC entries, so there is\n")
		fmt.Fprintf(b, "  nothing to profile. If the plan ran on HCP Terraform,\n")
		fmt.Fprintf(b, "  enable debug logging on the run and use its raw log.\n")
	}
	fmt.Fprintf(b, "  response entries   %d\n", r.Caps.ResponseEntries)
	fmt.Fprintf(b, "  request entries    %d\n", r.Caps.RequestEntries)
	fmt.Fprintf(b, "  duration fields    %d\n", r.Caps.DurationFields)
	fmt.Fprintf(b, "  req id fields      %d\n", r.Caps.ReqIDFields)
	fmt.Fprintf(b, "  provider entries   %d\n", r.Caps.ProviderEntries)
	fmt.Fprintf(b, "  core vertex lines  %d\n", r.Caps.CoreVertexLines)
	fmt.Fprintf(b, "  core GRPC lines    %d\n\n", r.Caps.CoreGRPCLines)

	fmt.Fprintf(b, "SPANS\n")
	fmt.Fprintf(b, "  spans built        %d\n", r.SpanCount)
	fmt.Fprintf(b, "  slowest span       %d ms\n", r.SlowestMs)
	fmt.Fprintf(b, "  total span time    %d ms\n\n", r.TotalSpanMs)

	fmt.Fprintf(b, "COMPONENTS (%d distinct, top %d)\n", r.DistinctComps, len(r.TopComponents))
	for _, t := range r.TopComponents {
		name := t.Text
		if name == "" {
			name = "(none)"
		}
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, name)
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "FIELD KEYS\n")
	for _, t := range topN(r.FieldKeys, 40) {
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, t.Text)
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "MESSAGE TEMPLATES (values stripped, top %d)\n", len(r.TopTemplates))
	for _, t := range r.TopTemplates {
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, t.Text)
	}
	fmt.Fprintf(b, "\n")
	fmt.Fprintf(b, "Review this output before sharing it. Field keys and message\n")
	fmt.Fprintf(b, "shapes are included; field values are not.\n")

	_, err := io.WriteString(w, b.String())
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/diagnose/ -v`
Expected: PASS, all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/diagnose
git commit -m "Summarise log structure without disclosing field values"
```

---

### Task 9: CLI wiring and installability

**Files:**
- Create: `cmd/tfli/main.go`, `README.md`
- Test: `cmd/tfli/main_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: the `tfli` binary. Flags: `--diagnose` (required in phase 1), `-o <file>` to write the report to a file instead of stdout, `--version`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDiagnoseOnFixture(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	for _, want := range []string{"tfli diagnostic report", "selected tier      reported", "spans built        3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunReportsMissingFileClearly(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", "no-such-file.log"}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestRunRequiresDiagnoseInPhase1(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"some.log"}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error when --diagnose was omitted")
	}
	if !strings.Contains(err.Error(), "--diagnose") {
		t.Errorf("error does not mention --diagnose: %v", err)
	}
}

func TestRunWritesToOutputFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "report.txt")
	var sb strings.Builder
	err := run([]string{"--diagnose", "-o", out, filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(data), "tfli diagnostic report") {
		t.Error("report file does not contain the report")
	}
}
```

Add `"io"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tfli/ -v`
Expected: FAIL — `undefined: run`.

- [ ] **Step 3: Implement `main.go`**

```go
// Command tfli inspects Terraform TF_LOG output.
//
// Phase 1 supports only --diagnose, which reports a log's structure so the
// format can be confirmed against real data before further features are built.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yesdevnull/tf-log-inspector/internal/diagnose"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// version is overridden at build time; the zero value is fine for go install.
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "tfli:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tfli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		doDiagnose = fs.Bool("diagnose", false, "report the log's structure and exit")
		outPath    = fs.String("o", "", "write the report to this file instead of standard output")
		showVer    = fs.Bool("version", false, "print the version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: tfli --diagnose [-o report.txt] <logfile>\n\n")
		fmt.Fprintf(stderr, "Analyse a Terraform TF_LOG file. For an HCP Terraform workspace,\n")
		fmt.Fprintf(stderr, "enable debug logging on a run and download its raw log.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVer {
		fmt.Fprintln(stdout, "tfli", version)
		return nil
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one log file argument")
	}
	if !*doDiagnose {
		return errors.New("phase 1 supports only --diagnose; pass --diagnose <logfile>")
	}

	path := fs.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	var comps logfmt.Interner
	collector := diagnose.NewCollector(&comps)
	sniffer := span.NewSniffer(&comps)
	var builder span.ReportedBuilder

	stats, err := logfmt.Scan(bufio.NewReaderSize(f, 1<<20), &comps, collector, sniffer, &builder)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", path, err)
	}

	report := diagnose.Build(stats, sniffer.Report(), builder.Spans(), collector, &comps)

	w := stdout
	if *outPath != "" {
		out, err := os.Create(*outPath)
		if err != nil {
			return fmt.Errorf("creating %s: %w", *outPath, err)
		}
		defer out.Close()
		w = out
	}
	return report.Render(w)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS, every package.

- [ ] **Step 5: Verify the whole build and run it for real**

```bash
go vet ./...
gofmt -l .
go build ./...
go run ./cmd/tfli --diagnose testdata/provider-rpc.log
go run ./cmd/tfli --diagnose testdata/core-only.log
go run ./cmd/tfli --diagnose testdata/mixed-hcp.log
```

Expected: `gofmt -l .` prints nothing. The provider fixture reports tier `reported` with 3 spans and a slowest span of 4200 ms. The core-only fixture reports `NONE USABLE` and the HCP guidance text. The mixed fixture reports a non-zero `non-hclog entries` count.

- [ ] **Step 6: Write the README**

```markdown
# tf-log-inspector (`tfli`)

Find where a slow Terraform plan spent its time.

## Install

```bash
go install github.com/yesdevnull/tf-log-inspector/cmd/tfli@latest
```

## Getting a log

Timing data comes from provider RPC entries, which only exist where the
providers actually run. For an HCP Terraform workspace the plan runs on
HashiCorp's runners, so a locally captured `TF_LOG` will not contain it.

Instead, on a new run expand **Additional Planning Options** and enable
**Debug Logging**, then download the run's raw log.

For a local plan:

```bash
TF_LOG=TRACE TF_LOG_PATH=plan.log terraform plan
```

## Usage

```bash
tfli --diagnose plan.log
tfli --diagnose -o report.txt plan.log
```

`--diagnose` reports the log's structure: size, levels, which extraction tier
applies, which fields are present, and the most common message shapes.

It reports field **keys**, counts and message shapes. It does not report field
**values**. Review its output before sharing it.
```

- [ ] **Step 7: Verify installability**

```bash
go install ./cmd/tfli
tfli --version
```

Expected: prints `tfli dev`. Confirms `go install` produces a working binary on the target path.

- [ ] **Step 8: Commit**

```bash
git add cmd README.md
git commit -m "Add tfli command with --diagnose and installation docs"
```

---

## Self-Review

**Spec coverage.** Phase 1 in the spec is "Parser, sniffer, `--diagnose`. No TUI." Every element is covered: the parser is Tasks 2–5, the sniffer Task 7, `--diagnose` Task 8, the CLI Task 9. Tier 1 extraction (Task 6) is included because the diagnostic report must state how many spans a real log actually yields — a tier claim with no span count would not settle the phase-2 gate.

The spec's `--diagnose` requirements are all present: tier selected and why (Task 7 `Capabilities` + Task 8 `EXTRACTION` block), counts per known shape (`TopTemplates`), unmatched templates with values stripped (`template()`), observed `tf_*` fields (`FieldKeys`), core-side line presence (`CoreVertexLines`/`CoreGRPCLines`), line count and mean line length (`SIZE`), non-hclog proportion (`Untimestamped`), and ANSI presence (`SawANSI`).

**Deliberately deferred to later phases**, consistent with the spec's phasing: tiers 2–4 builders, address attribution and confidence distribution (needs core-side correlation, spec phase 5), the entry index being retained for random access (needs mmap, spec phase 3), and the `plan -json` parser (spec phase 6). The confidence distribution is the one `--diagnose` item the spec lists that phase 1 does not produce; it cannot exist before address attribution does, and the spec makes that view conditional on this run's results anyway.

**Placeholder scan.** No TBDs, no "handle errors appropriately", no "similar to Task N". Every code step carries complete code.

**Type consistency.** `logfmt.Sink` is implemented by `diagnose.Collector`, `span.Sniffer` and `span.ReportedBuilder`, all with the identical signature `Entry(logfmt.Entry, string, logfmt.Fields)`. `Scan` accepts them variadically. `NewSniffer` and `NewCollector` both take `*logfmt.Interner`. `Fidelity` values are produced by `Capabilities.BestFidelity` and consumed by `Report.Tier`. `Span.DurationMs()` is used in `Build`.

One inconsistency found and fixed inline: Task 7's first draft of the test helper constructed `Sniffer` as a zero value, but the type needs an interner, so Step 4 of that task corrects the helper to use `NewSniffer`.

---

## Execution Handoff

Plan complete. Two execution options:

1. **Subagent-Driven (recommended)** — a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session with checkpoints for review.
