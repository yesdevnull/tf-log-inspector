# tf-log-inspector Phase 1 — Parser and `--diagnose` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a `tfli --diagnose <logfile>` binary that parses an HCP Terraform raw run log and reports its structure, so the log format is established by evidence before any UI is built on it.

**Architecture:** A single streaming pass over the file parses each physical line into a logical `Entry` (a timestamped line plus its continuation lines), pushing each to a set of `Sink`s. **Structured fields are parsed from the header line only** — never from continuation lines — and are never retained, so memory stays flat regardless of file size. One sink accumulates diagnostic counters; another builds spans from `tf_req_duration_ms`. No TUI, no mmap, no external dependencies.

**Tech Stack:** Go 1.25, standard library only. No third-party modules in phase 1.

**Spec:** `docs/superpowers/specs/2026-09-03-tf-log-inspector-design.md`

**Review:** This plan was revised after an adversarial review that compiled its code and ran it against real logs. Findings: `/tmp/tf-log-inspector-par-findings.md`.

## Global Constraints

- **Module path:** `github.com/yesdevnull/tf-log-inspector`. Binary name `tfli`, so the command package must be `cmd/tfli`.
- **Go version:** `go 1.25` in `go.mod`.
- **Zero external dependencies in phase 1.** Standard library only.
- **Australian/British English** in all prose, comments and user-facing output.
- **Test output must be pristine.** Expected error paths are asserted, not printed.
- **Fixtures state their provenance truthfully.** A fixture line either exists verbatim in the cited source, or the fixture is labelled synthesised. Never cite a real source for invented content.
- **Commit with `claude-git`**, not bare `git`, so commits are signed: `/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit ...`. Never `cd X && cmd`; use each tool's directory flag.

## The disclosure model (read before Task 3 or Task 8)

`--diagnose` output is the only artefact that leaves a machine holding confidential logs. Two different standards apply to its two halves, and conflating them is what produced the original leak:

- **Field keys are safe by construction.** A token is recorded as a field only if its key matches `^@?[A-Za-z_][A-Za-z0-9_.\-]*$`. Anything else is discarded outright, not recorded under a placeholder. Real secrets reached the report in review because arbitrary text preceding an `=` was accepted as a key.
- **Message prose is masked heuristically, and Dan reviews the output.** Masking replaces quoted strings, paths, resource addresses and long identifiers with placeholders. It is best-effort by nature, which is why the report retains its review footer. This is a deliberate trade-off: an allow-list of known message shapes would be safe by construction but would refuse to show unanticipated shapes, which is the entire purpose of `--diagnose`.

**Fields are parsed from the header line only.** hclog renders multi-line values as `key=` followed by `  | body` lines — a shape present in the real logs (`awslogs.txt` lines 292-295, an `http.response.body=` carrying a JSON body). Tokenising body lines is what let a JSON body's leading text become a printed "key". Body lines contribute to an entry's `Off`, `Len` and `Lines`; they never contribute fields or template text.

## Deliberate deviations from the spec

1. **No `mmap` in phase 1.** Phase 1 needs one sequential pass, so it uses a `bufio.Reader` and computes byte offsets as it goes — the same offsets the TUI will later use. Keeps phase 1 dependency-free with no build tags. Mmap arrives in phase 3.
2. **Package named `logfmt`, not `hclog`.** A package called `hclog` that is not `hashicorp/go-hclog` misleads the reader.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `go.mod` | Module definition |
| `internal/logfmt/level.go` | `Level` type and parsing |
| `internal/logfmt/intern.go` | String interning, saturating on overflow |
| `internal/logfmt/entry.go` | `Entry`, `Stats` |
| `internal/logfmt/header.go` | Parse one line's timestamp / level / component |
| `internal/logfmt/fields.go` | `key=value` parsing with a strict key charset |
| `internal/logfmt/scan.go` | The streaming pass: lines → entries → sinks |
| `internal/logfmt/ansi.go` | ANSI escape detection and stripping |
| `internal/span/span.go` | `Span`, `Fidelity` |
| `internal/span/reported.go` | Tier 1 builder |
| `internal/span/sniff.go` | Capability detection across all four tiers |
| `internal/diagnose/mask.go` | Prose and component masking |
| `internal/diagnose/diagnose.go` | Report accumulation and rendering |
| `cmd/tfli/main.go` | Flags, wiring, output |
| `testdata/*.log` | Fixtures, provenance stated truthfully |

---

### Task 1: Module scaffolding, fixtures, `Level` and `Interner`

**Files:**
- Create: `go.mod`, `internal/logfmt/level.go`, `internal/logfmt/intern.go`
- Create: `testdata/provider-rpc.log`, `testdata/core-only.log`, `testdata/mixed-hcp.log`
- Test: `internal/logfmt/level_test.go`, `internal/logfmt/intern_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `logfmt.Level` with `ParseLevel(string) Level` and `String()`; `logfmt.Interner` with `Intern(string) uint16`, `Lookup(uint16) string`, `Len() int`, `Overflowed() uint64`.

- [ ] **Step 1: Initialise the module**

```bash
go -C /Users/dan/Code/tf-log-inspector mod init github.com/yesdevnull/tf-log-inspector
```

- [ ] **Step 2: Create the fixtures**

`testdata/provider-rpc.log` — every line below exists verbatim in the cited issue:

```
# source: https://github.com/hashicorp/terraform-provider-aws/issues/28364 (verbatim)
2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=2634bc46-bb66-3d22-528d-d2eaf8165f52 tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange diagnostic_error_count=1 diagnostic_warning_count=0 tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5 @caller=github.com/hashicorp/terraform-plugin-go@v0.14.2/tfprotov5/internal/tf5serverlogging/downstream_request.go:37 @module=sdk.proto timestamp=2022-12-15T00:16:20.799Z
2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=1 tf_req_id=6bf5c123-55fb-d840-bbf4-d84f472bd996 tf_resource_type=aws_internet_gateway tf_rpc=ApplyResourceChange diagnostic_error_count=1 @module=sdk.proto diagnostic_warning_count=0 tf_proto_version=5.3 @caller=github.com/hashicorp/terraform-plugin-go@v0.14.2/tfprotov5/internal/tf5serverlogging/downstream_request.go:37 timestamp=2022-12-15T00:16:20.799Z
```

`testdata/core-only.log` — verbatim from the cited gist, including a real continuation pair (`Terraform v1.16.0` / `on linux_amd64` are genuine untimestamped CLI output following a timestamped line):

```
# source: https://gist.github.com/Nilsils/7c0e60d4d200f81f3f9a0a66a9fe37ee (verbatim)
2026-08-29T10:34:43.123+0200 [INFO]  Terraform version: 1.16.0
2026-08-29T10:34:43.124+0200 [DEBUG] using github.com/hashicorp/go-tfe v1.108.0
2026-08-29T10:34:43.124+0200 [INFO]  CLI command args: []string{"version"}
Terraform v1.16.0
on linux_amd64
2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete
2026-08-29T10:34:43.151+0200 [TRACE] building graph for terraform dependencies
2026-08-29T10:34:43.220+0200 [TRACE] statemgr.Filesystem: unlocking terraform.tfstate using fcntl flock
```

`testdata/multiline-body.log` — the hclog multi-line value shape, verbatim from a real issue. **This is the fixture the leak tests are built on.**

```
# source: https://github.com/hashicorp/terraform-provider-aws/issues/36974 (verbatim; values were redacted by the reporter, shape is real)
2024-02-13T12:11:28.330+0100 [DEBUG] provider.terraform-provider-aws_v5.5.0_x5: HTTP Response Received: @module=aws aws.operation=UpdateProject aws.sdk=aws-sdk-go
  http.response.body=
  | {"__type":"Inva*************tion","message":"Caller is an end user and not allowed to mutate system tags."}
   http.response.header.x_amzn_requestid=1d77924f-****3ca61 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_mux_provider="*schema.GRPCProviderServer" aws.service=CodeBuild http.response.header.date="Tue, 13 Feb 2024 11:11:17 GMT" tf_req_id=3544216***966 @caller=github.com/hashicorp/aws-sdk-go-base/v2/awsv1shim/v2@v2.0.0-beta.31/logger.go:144 aws.region=us-east-1 http.duration=10705 http.response.header.content_type=application/x-amz-json-1.1 http.response_content_length=107 http.status_code=400 tf_resource_type=aws_codebuild_project tf_rpc=ApplyResourceChange timestamp="2024-02-13T12:11:28.330+0100"
2024-02-13T12:11:28.331+0100 [TRACE] provider.terraform-provider-aws_v5.5.0_x5: Called downstream: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_id=3544***7966 @caller=github.com/hashicorp/terraform-plugin-sdk/v2@v2.26.1/helper/schema/resource.go:848 @module=sdk.helper_schema tf_mux_provider="*schema.GRPCProviderServer" tf_resource_type=aws_codebuild_project tf_rpc=ApplyResourceChange timestamp="2024-02-13T12:11:28.330+0100"
2024-02-13T12:11:28.331+0100 [TRACE] provider.terraform-provider-aws_v5.5.0_x5: Received downstream response: diagnostic_warning_count=0 tf_proto_version=5.3 @caller=github.com/hashicorp/terraform-plugin-go@v0.15.0/tfprotov5/internal/tf5serverlogging/downstream_request.go:37 tf_req_duration_ms=10710 tf_resource_type=aws_codebuild_project diagnostic_error_count=1 tf_rpc=ApplyResourceChange tf_provider_addr=registry.terraform.io/hashicorp/aws @module=sdk.proto tf_req_id=35442***66 timestamp="2024-02-13T12:11:28.331+0100"
```

`testdata/mixed-hcp.log` — **synthesised**, and labelled as such, because no public HCP raw run log was available. It models hclog interleaved with plan output.

```
# SYNTHESISED to model an HCP Terraform raw run log (hclog interleaved with plan output). Not from a real run.
2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=2634bc46-bb66-3d22-528d-d2eaf8165f52 tf_resource_type=aws_subnet tf_rpc=PlanResourceChange tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=12 @module=sdk.proto
Terraform used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
  + create

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

import (
	"strconv"
	"testing"
)

func TestInternerReturnsSameIDForSameString(t *testing.T) {
	var in Interner
	if a, b := in.Intern("provider.aws"), in.Intern("provider.aws"); a != b {
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

// The id space is uint16. Overflow must saturate to a sentinel rather than
// wrap, which would silently alias one component onto another.
func TestInternerSaturatesOnOverflow(t *testing.T) {
	var in Interner
	first := in.Intern("first")
	for i := 0; i < 70000; i++ {
		in.Intern("s" + strconv.Itoa(i))
	}
	if got := in.Lookup(first); got != "first" {
		t.Errorf("Lookup(%d) = %q after overflow, want %q", first, got, "first")
	}
	if in.Overflowed() == 0 {
		t.Error("Overflowed() = 0, want a non-zero count")
	}
	if got := in.Lookup(OverflowID); got != "(overflow)" {
		t.Errorf("Lookup(OverflowID) = %q, want %q", got, "(overflow)")
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -v`
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

// ParseLevel converts a bare level name, as it appears between the brackets of
// a log line, into a Level. Unrecognised names give LevelUnknown.
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

// OverflowID is returned once the id space is exhausted. Component
// cardinality is driven by log content -- Terraform core uses resource
// addresses as message prefixes -- so exhaustion is reachable on a large plan
// and must not silently alias one string onto another.
const OverflowID uint16 = 65535

// Interner maps component names to small integer ids so entries stay
// pointer-free. ID 0 is always the empty string, meaning "no component".
type Interner struct {
	ids      map[string]uint16
	strs     []string
	overflow uint64
}

func (i *Interner) init() {
	if i.strs == nil {
		i.ids = map[string]uint16{"": 0}
		i.strs = []string{""}
	}
}

// Intern returns a stable id for s, allocating one if needed. Once the id
// space is full it returns OverflowID and counts the event.
func (i *Interner) Intern(s string) uint16 {
	i.init()
	if id, ok := i.ids[s]; ok {
		return id
	}
	if len(i.strs) >= int(OverflowID) {
		i.overflow++
		return OverflowID
	}
	id := uint16(len(i.strs))
	i.strs = append(i.strs, s)
	i.ids[s] = id
	return id
}

// Lookup returns the string for an id, "(overflow)" for OverflowID, or "" if
// the id is unknown.
func (i *Interner) Lookup(id uint16) string {
	if id == OverflowID {
		return "(overflow)"
	}
	if int(id) >= len(i.strs) {
		return ""
	}
	return i.strs[id]
}

// Len reports how many distinct strings have been interned.
func (i *Interner) Len() int { return len(i.strs) }

// Overflowed reports how many strings could not be interned.
func (i *Interner) Overflowed() uint64 { return i.overflow }
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -v`
Expected: PASS, all six tests.

- [ ] **Step 8: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add go.mod internal/logfmt testdata
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Add module scaffolding, log levels, interning and real-log fixtures"
```

---

### Task 2: Line header parsing

**Files:**
- Create: `internal/logfmt/header.go`
- Test: `internal/logfmt/header_test.go`

**Interfaces:**
- Consumes: `Level`, `ParseLevel`.
- Produces: `logfmt.Header{TS time.Time; HasTS bool; Level Level; Comp string; Msg string}`, `logfmt.ParseHeader(line string) Header`.

Rules, all derived from real lines: two offset formats (`...800Z`, `...123+0200`); levels are space-padded after `]`; a component is the token before the first `:` **only if it contains no space**; a line with no leading timestamp is not a header.

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
	if h.Comp != "" {
		t.Errorf("Comp = %q, want empty (\"Terraform version\" contains a space)", h.Comp)
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
	if ParseHeader(`on linux_amd64`).HasTS {
		t.Error("HasTS = true, want false for a continuation line")
	}
}

func TestParseHeaderPlanOutputIsNotAHeader(t *testing.T) {
	if ParseHeader(`  + resource "aws_subnet" "example" {`).HasTS {
		t.Error("HasTS = true, want false for plan output")
	}
}

func TestParseHeaderMultilineValueBodyIsNotAHeader(t *testing.T) {
	// The hclog multi-line value shape, verbatim from testdata/multiline-body.log.
	if ParseHeader(`  | {"__type":"Inva*************tion","message":"Caller is an end user."}`).HasTS {
		t.Error("HasTS = true, want false for an hclog multi-line value body")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -run TestParseHeader -v`
Expected: FAIL — `undefined: ParseHeader`.

- [ ] **Step 3: Implement `header.go`**

```go
package logfmt

import (
	"strings"
	"time"
)

// tsLayout matches both offset forms Terraform emits: "Z" for UTC and a
// numeric "+0200" with no colon. It is hclog's TimeFormat.
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
// not an entry header: HasTS is false and the caller treats it as a
// continuation or as interleaved non-hclog content.
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
// core writes messages beginning with a component name, and hclog renders a
// named logger identically, so the two are indistinguishable and are treated
// alike. A candidate containing whitespace is prose, not a component.
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

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -run TestParseHeader -v`
Expected: PASS, all seven tests.

- [ ] **Step 5: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/logfmt/header.go internal/logfmt/header_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Parse log line headers: timestamps, levels and component prefixes"
```

---

### Task 3: Structured field parsing with a strict key charset

**Files:**
- Create: `internal/logfmt/fields.go`
- Test: `internal/logfmt/fields_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `logfmt.Field{Key, Val string}`, `logfmt.Fields []Field` with `Get(key) (string, bool)`, `logfmt.ParseFields(s string, dst Fields) Fields`, `logfmt.ValidKey(s string) bool`.

**This task carries the disclosure guarantee for keys.** A token is recorded only when its key matches `^@?[A-Za-z_][A-Za-z0-9_.\-]*$`. Non-conforming tokens are discarded entirely — not recorded under a placeholder, because a placeholder still implies the token existed and invites reconstructing it.

Other rules: field order is unstable, so lookup is by key; values may be quoted and may contain `\"` escapes; values may contain `=` (split on the first only); `\n` and `\r` are delimiters; prose before the first valid pair is skipped.

- [ ] **Step 1: Write the failing test**

```go
package logfmt

import "testing"

func TestParseFieldsUnorderedLookup(t *testing.T) {
	f := ParseFields(`tf_req_id=abc tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange tf_req_duration_ms=5 @module=sdk.proto`, nil)
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

// hclog escapes embedded quotes as \". Terminating the value at the first
// quote re-tokenises the remainder and turns its contents into "keys".
func TestParseFieldsEscapedQuoteInsideValue(t *testing.T) {
	f := ParseFields(`diagnostic_summary="bad header \"Authorization=Bearer s3cr3t\" here" tf_rpc=ReadResource`, nil)
	if got, _ := f.Get("diagnostic_summary"); got != `bad header \"Authorization=Bearer s3cr3t\" here` {
		t.Errorf("escaped-quote value = %q", got)
	}
	if _, ok := f.Get("Authorization"); ok {
		t.Error("value contents were re-tokenised into a key")
	}
	if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
		t.Errorf("field after escaped-quote value = %q", got)
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

// The disclosure guarantee: a token whose key is not an identifier is not a
// field, and must not be recorded at all.
func TestParseFieldsRejectsNonIdentifierKeys(t *testing.T) {
	cases := []string{
		`{"UserData":"IyEvYmluL2Jhc2gK==" tf_rpc=ReadResource`,
		`"-input=false tf_rpc=ReadResource`,
		`[id=673ed14b tf_rpc=ReadResource`,
		`wJalrXUtnFEMI/K7MDENG=secret tf_rpc=ReadResource`,
	}
	for _, in := range cases {
		f := ParseFields(in, nil)
		for _, fl := range f {
			if !ValidKey(fl.Key) {
				t.Errorf("ParseFields(%q) recorded invalid key %q", in, fl.Key)
			}
		}
		if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
			t.Errorf("ParseFields(%q): real field lost, tf_rpc = %q", in, got)
		}
	}
}

func TestParseFieldsNewlineIsADelimiter(t *testing.T) {
	f := ParseFields("tf_req_duration_ms=12\nTerraform used the selected providers", nil)
	if got, _ := f.Get("tf_req_duration_ms"); got != "12" {
		t.Errorf("tf_req_duration_ms = %q, want 12 (newline must delimit)", got)
	}
}

func TestParseFieldsEmptyValue(t *testing.T) {
	f := ParseFields(`diagnostic_detail= tf_rpc=ReadResource`, nil)
	if got, ok := f.Get("diagnostic_detail"); !ok || got != "" {
		t.Errorf("Get(diagnostic_detail) = %q, %v; want \"\", true", got, ok)
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

func TestValidKey(t *testing.T) {
	valid := []string{"tf_rpc", "@module", "aws.operation", "http.response.header.x_amzn_requestid", "a-b"}
	invalid := []string{"", "@", "1abc", `{"UserData"`, `"-input`, "[id", "a/b", "a b", "a=b"}
	for _, s := range valid {
		if !ValidKey(s) {
			t.Errorf("ValidKey(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidKey(s) {
			t.Errorf("ValidKey(%q) = true, want false", s)
		}
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

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -run 'TestParseFields|TestValidKey' -v`
Expected: FAIL — `undefined: ParseFields`.

- [ ] **Step 3: Implement `fields.go`**

```go
package logfmt

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

// ValidKey reports whether s has the shape of an hclog field key:
// an optional "@", then an identifier of letters, digits, "_", "." and "-".
//
// This is the disclosure guarantee for field keys. Log content that merely
// happens to contain "=" -- a JSON body, a quoted CLI argument, a base64 blob
// -- must never be recorded as a key, because keys are printed verbatim.
func ValidKey(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '@' {
		s = s[1:]
		if s == "" {
			return false
		}
	}
	if c := s[0]; !isAlpha(c) && c != '_' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isAlpha(c) && !isDigit(c) && c != '_' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// ParseFields extracts key=value pairs from s, appending to dst and returning
// the result. Pass dst[:0] to reuse a buffer across lines. Tokens whose key
// fails ValidKey are discarded entirely.
func ParseFields(s string, dst Fields) Fields {
	for i := 0; i < len(s); {
		if isSpace(s[i]) {
			i++
			continue
		}
		key, val, next, ok := parsePair(s, i)
		if ok && ValidKey(key) {
			dst = append(dst, Field{Key: key, Val: val})
		}
		i = next
	}
	return dst
}

// parsePair reads one whitespace-delimited token starting at i, returning ok
// only when the token has the k=v shape. Validity of the key is the caller's
// concern.
func parsePair(s string, i int) (key, val string, next int, ok bool) {
	start := i
	eq := -1
	for ; i < len(s); i++ {
		if s[i] == '=' && eq < 0 {
			eq = i
			if i+1 < len(s) && s[i+1] == '"' {
				v, end := readQuoted(s, i+1)
				return s[start:eq], v, end, true
			}
			continue
		}
		if isSpace(s[i]) {
			break
		}
	}
	if eq < 0 || eq == start {
		return "", "", i, false
	}
	return s[start:eq], s[eq+1 : i], i, true
}

// readQuoted consumes a quoted value beginning at the opening quote, honouring
// backslash escapes as hclog writes them. It returns the value without the
// surrounding quotes and the index just past the closing quote. An
// unterminated quote consumes the remainder of the string.
func readQuoted(s string, open int) (val string, next int) {
	for i := open + 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return s[open+1 : i], i + 1
		}
	}
	return s[open+1:], len(s)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -run 'TestParseFields|TestValidKey' -v`
Expected: PASS, all ten tests.

- [ ] **Step 5: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/logfmt/fields.go internal/logfmt/fields_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Parse log fields with a strict key charset so content cannot pose as a key"
```

---

### Task 4: ANSI escape handling

**Files:**
- Create: `internal/logfmt/ansi.go`
- Test: `internal/logfmt/ansi_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `logfmt.HasANSI(s string) bool`, `logfmt.StripANSI(s string, scratch []byte) (string, []byte)`.

`StripANSI` returns the grown scratch buffer so the caller can genuinely reuse it. An HCP raw run log is captured terminal output and may carry colour escapes; whether it does is unconfirmed, so this is defensive and `--diagnose` reports what it saw.

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
	got, _ := StripANSI("\x1b[1m\x1b[32m+ create\x1b[0m", nil)
	if got != "+ create" {
		t.Errorf("StripANSI = %q, want %q", got, "+ create")
	}
}

func TestStripANSILeavesPlainTextUntouched(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: b"
	if got, _ := StripANSI(in, nil); got != in {
		t.Errorf("StripANSI altered plain text: %q", got)
	}
}

func TestStripANSIUnterminatedSequence(t *testing.T) {
	if got, _ := StripANSI("text\x1b[", nil); got != "text" {
		t.Errorf("StripANSI = %q, want %q", got, "text")
	}
}

func TestStripANSIReusesScratch(t *testing.T) {
	_, scratch := StripANSI("\x1b[31mred\x1b[0m", nil)
	if cap(scratch) == 0 {
		t.Fatal("StripANSI returned an empty scratch buffer")
	}
	got, scratch2 := StripANSI("\x1b[32mgreen\x1b[0m", scratch)
	if got != "green" {
		t.Errorf("StripANSI = %q, want green", got)
	}
	if cap(scratch2) < cap(scratch) {
		t.Error("scratch buffer shrank between calls")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -run ANSI -v`
Expected: FAIL — `undefined: HasANSI`.

- [ ] **Step 3: Implement `ansi.go`**

```go
package logfmt

import "strings"

// HasANSI reports whether s contains an ANSI escape sequence.
func HasANSI(s string) bool { return strings.IndexByte(s, 0x1b) >= 0 }

// StripANSI removes ANSI escape sequences from s. scratch is reused as the
// working buffer and the grown buffer is returned so the caller can pass it
// back on the next call. Strings with no escapes are returned unchanged.
func StripANSI(s string, scratch []byte) (string, []byte) {
	if !HasANSI(s) {
		return s, scratch
	}
	scratch = scratch[:0]
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			scratch = append(scratch, s[i])
			i++
			continue
		}
		// Skip ESC, an optional '[', parameter bytes, then one final byte.
		// An unterminated sequence consumes the rest of the string.
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
	return string(scratch), scratch
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -run ANSI -v`
Expected: PASS, all five tests.

- [ ] **Step 5: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/logfmt/ansi.go internal/logfmt/ansi_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Strip ANSI escape sequences from captured terminal output"
```

---

### Task 5: The streaming scanner

**Files:**
- Create: `internal/logfmt/entry.go`, `internal/logfmt/scan.go`
- Test: `internal/logfmt/scan_test.go`

**Interfaces:**
- Consumes: `ParseHeader`, `ParseFields`, `StripANSI`, `HasANSI`, `Interner`, `Level`.
- Produces:
  - `logfmt.Entry{Off uint64; Len uint32; TSms uint32; Level Level; Comp uint16; Lines uint16; Timestamped bool}`
  - `logfmt.Sink` — `interface{ Entry(ord uint32, e Entry, msg string, f Fields) }`
  - `logfmt.Scan(r io.Reader, comps *Interner, sinks ...Sink) (Stats, error)`
  - `logfmt.Stats` with `Entries, PhysicalLines, ContinuationLines, ContinuationBytes, UntimestampedLines, LongContinuationRuns, BackwardsTimestamps uint64`, `ByLevel [6]uint64`, `Bytes uint64`, `SawANSI bool`, `LinesSaturated uint64`, `FirstTS, LastTS time.Time`.

**Three behaviours this task must get right, each of which was a review finding:**

1. **Only the header line's message reaches sinks.** Continuation lines contribute to `Off`, `Len` and `Lines`, and are counted, but never to `msg` or fields. This is what keeps the disclosure guarantee intact, keeps memory flat, and stops a continuation line's first token fusing with the header's last field.
2. **The sink ordinal is supplied by the scanner**, not counted independently by each sink, so all sinks agree on entry numbering by construction.
3. **Backwards timestamps clamp to zero and are counted.** `uint32(negative.Milliseconds())` wraps to ~4.29 × 10⁹ and produces spans at day 49.

- [ ] **Step 1: Write the failing test**

```go
package logfmt

import (
	"os"
	"strings"
	"testing"
)

type collector struct {
	ords    []uint32
	entries []Entry
	msgs    []string
	rpcs    []string
}

func (c *collector) Entry(ord uint32, e Entry, msg string, f Fields) {
	c.ords = append(c.ords, ord)
	c.entries = append(c.entries, e)
	c.msgs = append(c.msgs, msg)
	rpc, _ := f.Get("tf_rpc")
	c.rpcs = append(c.rpcs, rpc)
}

func TestScanGroupsContinuationLines(t *testing.T) {
	in := "2026-08-29T10:34:43.124+0200 [INFO]  CLI command args: []string{\"version\"}\n" +
		"Terraform v1.16.0\n" +
		"on linux_amd64\n" +
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

// Continuation text must not reach the sink. This is the disclosure guarantee
// and the reason field parsing cannot fuse across a line boundary.
func TestScanSinkSeesOnlyHeaderLineMessage(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=12\n" +
		"Terraform used the selected providers to generate the following\n" +
		"  + resource \"aws_subnet\" \"example\" {\n"

	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if strings.Contains(c.msgs[0], "Terraform used") || strings.Contains(c.msgs[0], "aws_subnet") {
		t.Errorf("continuation text reached the sink: %q", c.msgs[0])
	}
	if c.rpcs[0] != "ReadResource" {
		t.Errorf("tf_rpc = %q, want ReadResource", c.rpcs[0])
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

func TestScanOrdinalsAreSequential(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		"2022-12-15T00:16:20.801Z [TRACE] a: two\n" +
		"2022-12-15T00:16:20.802Z [TRACE] a: three\n"
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for i, ord := range c.ords {
		if ord != uint32(i) {
			t.Errorf("ordinal %d = %d, want %d", i, ord, i)
		}
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

// A timestamp earlier than the first entry must clamp, not wrap to ~4.29e9.
func TestScanBackwardsTimestampClamps(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: first\n" +
		"2022-12-15T00:16:12.800Z [TRACE] a: earlier\n"
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.entries[1].TSms != 0 {
		t.Errorf("backwards TSms = %d, want 0", c.entries[1].TSms)
	}
	if st.BackwardsTimestamps != 1 {
		t.Errorf("BackwardsTimestamps = %d, want 1", st.BackwardsTimestamps)
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
	if st.UntimestampedLines != 2 {
		t.Errorf("UntimestampedLines = %d, want 2", st.UntimestampedLines)
	}
}

// UntimestampedLines must count every untimestamped physical line in the file,
// not merely a leading block, or it cannot measure an HCP log's non-hclog
// proportion -- the thing phase 1 exists to measure.
func TestScanCountsUntimestampedLinesThroughoutFile(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		"plan output line 1\n" +
		"plan output line 2\n" +
		"2022-12-15T00:16:20.900Z [TRACE] a: two\n" +
		"plan output line 3\n"
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.UntimestampedLines != 3 {
		t.Errorf("UntimestampedLines = %d, want 3", st.UntimestampedLines)
	}
	if st.ContinuationBytes == 0 {
		t.Error("ContinuationBytes = 0, want non-zero")
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
	if !st.SawANSI {
		t.Error("SawANSI = false, want true")
	}
	if c.msgs[2] != "three" {
		t.Errorf("message = %q, want ANSI stripped to %q", c.msgs[2], "three")
	}
}

func TestScanCRLFAndNoTrailingNewline(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\r\n" +
		"2022-12-15T00:16:20.801Z [TRACE] a: two"
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.entries))
	}
	if c.msgs[1] != "two" {
		t.Errorf("last message = %q, want two", c.msgs[1])
	}
}

func TestScanRealFixtures(t *testing.T) {
	cases := []struct {
		file           string
		wantMinEntries int
		wantANSI       bool
	}{
		{"../../testdata/provider-rpc.log", 2, false},
		{"../../testdata/core-only.log", 6, false},
		{"../../testdata/multiline-body.log", 2, false},
		{"../../testdata/mixed-hcp.log", 2, false},
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

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -run TestScan -v`
Expected: FAIL — `undefined: Scan`, `undefined: Entry`.

- [ ] **Step 3: Implement `entry.go`**

```go
package logfmt

import "time"

// maxHeaderMsg caps the retained header message, bounding memory against a
// pathologically long single line.
const maxHeaderMsg = 64 << 10

// longRun is the continuation-run length above which a run is counted as a
// block of interleaved non-hclog output rather than a wrapped message.
const longRun = 5

// Entry is one logical log entry: a timestamped line plus any continuation
// lines that follow it. Off and Len cover every line of the entry, so a
// consumer can seek to Off and read Len bytes to render it whole.
//
// The struct holds no pointers and no strings, so a large slice of them costs
// the garbage collector nothing to scan.
type Entry struct {
	Off         uint64 // byte offset of the entry's first line
	Len         uint32 // bytes covering all lines of the entry
	TSms        uint32 // milliseconds since the first timestamped entry
	Level       Level
	Comp        uint16 // interned component; 0 means none
	Lines       uint16 // physical line count, saturating
	Timestamped bool   // false for interleaved non-hclog content
}

// Stats summarises a scan. It is the raw material of the diagnostic report.
type Stats struct {
	Entries              uint64
	PhysicalLines        uint64
	ContinuationLines    uint64
	ContinuationBytes    uint64
	UntimestampedLines   uint64 // every physical line with no timestamp
	LongContinuationRuns uint64 // runs longer than longRun lines
	BackwardsTimestamps  uint64
	LinesSaturated       uint64
	ByLevel              [6]uint64
	Bytes                uint64
	SawANSI              bool
	FirstTS, LastTS      time.Time
}
```

- [ ] **Step 4: Implement `scan.go`**

```go
package logfmt

import (
	"bufio"
	"io"
	"math"
	"strings"
	"time"
)

// Sink receives each entry as it is parsed. ord is the entry's zero-based
// ordinal, supplied by the scanner so every sink agrees on numbering. msg is
// the header line's message only -- never continuation text -- and f are the
// fields parsed from it. Both are valid only for the duration of the call.
type Sink interface {
	Entry(ord uint32, e Entry, msg string, f Fields)
}

// Scan reads r in a single pass, assembling logical entries and pushing each
// to every sink. Memory use is independent of input size: only the header
// line's message is retained, and only until the entry is flushed.
func Scan(r io.Reader, comps *Interner, sinks ...Sink) (Stats, error) {
	var st Stats
	br := bufio.NewReaderSize(r, 256*1024)

	var (
		fieldBuf Fields
		scratch  []byte
		off      uint64

		open     bool
		cur      Entry
		curMsg   string
		runLen   uint64
		baseTS   time.Time
		haveBase bool
		ord      uint32
	)

	flush := func() {
		if !open {
			return
		}
		if runLen > longRun {
			st.LongContinuationRuns++
		}
		fieldBuf = ParseFields(curMsg, fieldBuf[:0])
		st.Entries++
		st.ByLevel[cur.Level]++
		for _, s := range sinks {
			s.Entry(ord, cur, curMsg, fieldBuf)
		}
		ord++
		open = false
		curMsg = ""
		runLen = 0
	}

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			raw := uint32(len(line))
			text := strings.TrimRight(line, "\r\n")
			if HasANSI(text) {
				st.SawANSI = true
				text, scratch = StripANSI(text, scratch)
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

				delta := h.TS.Sub(baseTS).Milliseconds()
				if delta < 0 {
					// Concurrent goroutines can emit out of order. Clamp
					// rather than wrapping the unsigned field.
					st.BackwardsTimestamps++
					delta = 0
				}
				cur = Entry{
					Off:         off,
					Len:         raw,
					TSms:        uint32(delta),
					Level:       h.Level,
					Comp:        comps.Intern(h.Comp),
					Lines:       1,
					Timestamped: true,
				}
				if len(h.Msg) > maxHeaderMsg {
					curMsg = h.Msg[:maxHeaderMsg]
				} else {
					curMsg = h.Msg
				}
				open = true

			case open:
				// Continuation: counted and covered by Off/Len, but its text
				// never reaches a sink.
				st.ContinuationLines++
				st.ContinuationBytes += uint64(raw)
				st.UntimestampedLines++
				runLen++
				cur.Len += raw
				if cur.Lines == math.MaxUint16 {
					st.LinesSaturated++
				} else {
					cur.Lines++
				}

			default:
				// Non-hclog content before any entry.
				st.UntimestampedLines++
				cur = Entry{Off: off, Len: raw, Lines: 1}
				curMsg = ""
				open = true
			}
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

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/logfmt/ -v`
Expected: PASS, every test.

- [ ] **Step 6: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/logfmt/entry.go internal/logfmt/scan.go internal/logfmt/scan_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Add streaming scanner assembling logical entries from log lines"
```

---

### Task 6: Tier 1 span extraction

**Files:**
- Create: `internal/span/span.go`, `internal/span/reported.go`
- Test: `internal/span/reported_test.go`

**Interfaces:**
- Consumes: `logfmt.Entry`, `logfmt.Fields`, `logfmt.Sink`.
- Produces: `span.Fidelity` with `String()`; `span.Span{Entry uint32; StartMs, EndMs, DurationMs uint32; StartClamped bool; RPC, Provider, ResourceType string; Fidelity Fidelity}`; `span.ReportedBuilder` satisfying `logfmt.Sink`, with `Spans() []Span`.

**`DurationMs` is stored, never derived.** Deriving it from `EndMs − StartMs` after clamping `StartMs` at zero silently shrinks every span whose duration exceeds its offset from the first timestamp — which is exactly the early `GetProviderSchema` and `Configure` calls most likely to be slow. `StartMs` remains clamped for later timeline rendering, with `StartClamped` recording that it happened.

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
		t.Errorf("Fidelity = %v, want reported", s.Fidelity)
	}
	// The reported duration survives even though the span sits at the base
	// timestamp and its start is clamped.
	if s.DurationMs != 5000 {
		t.Errorf("DurationMs = %d, want 5000", s.DurationMs)
	}
	if !s.StartClamped {
		t.Error("StartClamped = false, want true")
	}
	if s.StartMs != 0 {
		t.Errorf("StartMs = %d, want 0", s.StartMs)
	}
}

func TestReportedBuilderUnclampedStart(t *testing.T) {
	in := "2022-12-15T00:16:20.000Z [TRACE] provider.aws: first\n" +
		`2022-12-15T00:16:30.000Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=4000` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].StartMs != 6000 {
		t.Errorf("StartMs = %d, want 6000", got[0].StartMs)
	}
	if got[0].EndMs != 10000 {
		t.Errorf("EndMs = %d, want 10000", got[0].EndMs)
	}
	if got[0].StartClamped {
		t.Error("StartClamped = true, want false")
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

// A response line followed by plan output must still yield its span: field
// parsing must not fuse the last field with the next line's first token.
func TestReportedBuilderSurvivesFollowingContinuation(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=12` + "\n" +
		"Terraform used the selected providers to generate the following\n" +
		"  + create\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].DurationMs != 12 {
		t.Errorf("DurationMs = %d, want 12", got[0].DurationMs)
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

func TestReportedBuilderIgnoresUnparseableDuration(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=notanumber` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("got %d spans, want 0", len(got))
	}
}

func TestReportedBuilderRecordsEntryOrdinal(t *testing.T) {
	in := "2022-12-15T00:16:20.700Z [TRACE] a: filler\n" +
		`2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5` + "\n"
	var b ReportedBuilder
	scanInto(t, in, &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].Entry != 1 {
		t.Errorf("Entry = %d, want 1", got[0].Entry)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/span/ -v`
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
//
// DurationMs is stored rather than derived from StartMs and EndMs. A span
// whose duration exceeds its offset from the first entry has its start clamped
// to zero, and deriving the duration from the clamped start would silently
// shrink it -- which would hit the early GetProviderSchema and Configure calls
// that are most often the slow ones.
type Span struct {
	Entry        uint32 // ordinal of the entry that closed this span
	StartMs      uint32 // clamped at zero; see StartClamped
	EndMs        uint32
	DurationMs   uint32 // as reported by the provider
	StartClamped bool
	RPC          string
	Provider     string
	ResourceType string
	Fidelity     Fidelity
}
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
// It satisfies logfmt.Sink.
type ReportedBuilder struct {
	spans []Span
}

// Entry implements logfmt.Sink.
func (b *ReportedBuilder) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {
	if !strings.HasPrefix(msg, responseMarker) {
		return
	}
	raw, ok := f.Get("tf_req_duration_ms")
	if !ok {
		return
	}
	ms64, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return
	}
	ms := uint32(ms64)

	start, clamped := uint32(0), true
	if e.TSms > ms {
		start, clamped = e.TSms-ms, false
	}

	rpc, _ := f.Get("tf_rpc")
	provider, _ := f.Get("tf_provider_addr")
	resType, ok := f.Get("tf_resource_type")
	if !ok {
		resType, _ = f.Get("tf_data_source_type")
	}

	b.spans = append(b.spans, Span{
		Entry:        ord,
		StartMs:      start,
		EndMs:        e.TSms,
		DurationMs:   ms,
		StartClamped: clamped,
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

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/span/ -v`
Expected: PASS, all seven tests.

- [ ] **Step 6: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/span
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Extract provider RPC spans from reported tf_req_duration_ms"
```

---

### Task 7: Capability sniffer

**Files:**
- Create: `internal/span/sniff.go`
- Test: `internal/span/sniff_test.go`

**Interfaces:**
- Consumes: `logfmt.Entry`, `logfmt.Fields`, `logfmt.Interner`, `Fidelity`.
- Produces: `span.NewSniffer(*logfmt.Interner) *Sniffer` satisfying `logfmt.Sink`, with `Report() Capabilities`; `span.Capabilities` with `BestFidelity() (Fidelity, bool)`.

`Sniffer` requires an interner, so it is constructed only via `NewSniffer` — a zero value would silently fail to count provider entries.

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
	s := NewSniffer(&comps)
	if _, err := logfmt.Scan(strings.NewReader(in), &comps, s); err != nil {
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
	if f, ok := c.BestFidelity(); !ok || f != FidelityReported {
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
	if f, ok := c.BestFidelity(); !ok || f != FidelityPaired {
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

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/span/ -run TestSniffer -v`
Expected: FAIL — `undefined: NewSniffer`.

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
// Construct it with NewSniffer: it needs the interner to resolve components.
type Sniffer struct {
	caps  Capabilities
	comps *logfmt.Interner
}

// NewSniffer returns a Sniffer resolving component ids via comps.
func NewSniffer(comps *logfmt.Interner) *Sniffer { return &Sniffer{comps: comps} }

// Entry implements logfmt.Sink.
func (s *Sniffer) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {
	comp := s.comps.Lookup(e.Comp)
	if strings.HasPrefix(comp, "provider.") {
		s.caps.ProviderEntries++
	}
	if comp == "GRPCProvider" {
		s.caps.CoreGRPCLines++
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
}

// Report returns the accumulated capabilities.
func (s *Sniffer) Report() Capabilities { return s.caps }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/span/ -v`
Expected: PASS, all ten tests.

- [ ] **Step 5: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/span/sniff.go internal/span/sniff_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Detect which span extraction tiers a log can support"
```

---

### Task 8: Prose masking

**Files:**
- Create: `internal/diagnose/mask.go`
- Test: `internal/diagnose/mask_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `diagnose.MaskProse(s string) string`, `diagnose.MaskComponent(s string) string`.

Masking is heuristic by design — see the disclosure model above. Token rules, applied in order:

| Condition | Replacement |
|-----------|-------------|
| Contains `"` | `<q>` |
| Contains `/` or begins `~` | `<path>` |
| Contains `[` or `]` | `<addr>` |
| Dotted identifier whose second segment begins lower-case | `<addr>` |
| Length > 12 and contains a digit | `<id>` |

The dotted rule distinguishes Terraform resource addresses (`terraform_data.r1`, `aws_instance.web`) from Go identifiers in core messages (`terraform.NewContext`, `statemgr.Filesystem`), which are safe and worth keeping. Core writes the latter in CamelCase after the dot. This is a heuristic and is documented as such.

- [ ] **Step 1: Write the failing test**

```go
package diagnose

import (
	"strings"
	"testing"
)

func TestMaskProseHidesAddressesAndPaths(t *testing.T) {
	cases := []struct{ in, mustNotContain string }{
		{`module.vpc["datacenter1"].aws_internet_gateway.this[0] encountered an error`, "datacenter1"},
		{`writeResourceInstanceState to workingState for aws_codebuild_project.codebuild_name`, "codebuild_name"},
		{`Attempting to open CLI config file: /home/lucas/.terraformrc`, "lucas"},
		{`applying the planned Create change for terraform_data.r1`, "terraform_data.r1"},
	}
	for _, c := range cases {
		got := MaskProse(c.in)
		if strings.Contains(got, c.mustNotContain) {
			t.Errorf("MaskProse(%q) = %q, still contains %q", c.in, got, c.mustNotContain)
		}
	}
}

func TestMaskProseKeepsCoreIdentifiers(t *testing.T) {
	// Go identifiers in core messages are safe and are what makes a template
	// legible. CamelCase after the dot distinguishes them from addresses.
	for _, in := range []string{"terraform.NewContext complete", "statemgr.Filesystem unlocking"} {
		got := MaskProse(in)
		if strings.Contains(got, "<addr>") {
			t.Errorf("MaskProse(%q) = %q, masked a core identifier", in, got)
		}
	}
}

func TestMaskProseHidesQuotedStrings(t *testing.T) {
	got := MaskProse(`vertex "aws_codebuild_project.codebuild_name": visit complete`)
	if strings.Contains(got, "codebuild_name") {
		t.Errorf("MaskProse = %q, leaked a quoted address", got)
	}
	if !strings.Contains(got, "vertex") {
		t.Errorf("MaskProse = %q, lost the message shape", got)
	}
}

func TestMaskProseHidesLongIdentifiers(t *testing.T) {
	got := MaskProse("request 2634bc46bb663d22528dd2eaf8165f52 complete")
	if strings.Contains(got, "2634bc46") {
		t.Errorf("MaskProse = %q, leaked a long identifier", got)
	}
}

func TestMaskComponent(t *testing.T) {
	if got := MaskComponent("terraform_data.r1"); got != "<addr>" {
		t.Errorf("MaskComponent(terraform_data.r1) = %q, want <addr>", got)
	}
	if got := MaskComponent("statemgr.Filesystem"); got != "statemgr.Filesystem" {
		t.Errorf("MaskComponent(statemgr.Filesystem) = %q, want it kept", got)
	}
	if got := MaskComponent("provider.terraform-provider-aws_v4.46.0_x5"); got != "provider.terraform-provider-aws_v4.46.0_x5" {
		t.Errorf("MaskComponent kept-case failed: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/diagnose/ -run Mask -v`
Expected: FAIL — `undefined: MaskProse`.

- [ ] **Step 3: Implement `mask.go`**

```go
// Package diagnose summarises a log's structure without disclosing its
// contents.
package diagnose

import "strings"

// MaskProse replaces the parts of a message that carry content -- quoted
// strings, filesystem paths, resource addresses and long identifiers -- with
// placeholders, leaving the message's shape legible.
//
// This is heuristic by design. Field keys are guarded by a strict charset and
// are safe by construction; prose cannot be, because the point of reporting it
// is to reveal message shapes nobody anticipated. The report therefore carries
// a review notice, and Dan reviews it before sharing.
func MaskProse(s string) string {
	fields := strings.Fields(s)
	for i, tok := range fields {
		fields[i] = maskToken(tok)
	}
	return strings.Join(fields, " ")
}

// MaskComponent masks a component name that looks like a resource address.
// Terraform core writes messages prefixed with the address of the resource
// being worked on, so component names are content too.
func MaskComponent(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "provider.") {
		// Plugin binary names carry no customer content.
		return s
	}
	if looksLikeAddress(s) {
		return "<addr>"
	}
	return s
}

func maskToken(t string) string {
	switch {
	case t == "":
		return t
	case strings.ContainsAny(t, `"'`):
		return "<q>"
	case strings.ContainsAny(t, "/\\") || strings.HasPrefix(t, "~"):
		return "<path>"
	case strings.ContainsAny(t, "[]"):
		return "<addr>"
	case looksLikeAddress(t):
		return "<addr>"
	case len(t) > 12 && hasDigit(t):
		return "<id>"
	}
	return t
}

// looksLikeAddress reports whether t has the shape of a Terraform resource
// address: a dotted identifier whose segment after the dot begins lower-case.
// Core's own Go identifiers -- terraform.NewContext, statemgr.Filesystem --
// are CamelCase after the dot and are kept, because they are what make a
// masked template legible.
func looksLikeAddress(t string) bool {
	dot := strings.IndexByte(t, '.')
	if dot <= 0 || dot == len(t)-1 {
		return false
	}
	head, tail := t[:dot], t[dot+1:]
	if !isLowerIdent(head) {
		return false
	}
	c := tail[0]
	return c >= 'a' && c <= 'z'
}

func isLowerIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '_' {
			return false
		}
	}
	return s != ""
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/diagnose/ -run Mask -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/diagnose/mask.go internal/diagnose/mask_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Mask addresses, paths and identifiers out of reported message shapes"
```

---

### Task 9: The diagnostic report

**Files:**
- Create: `internal/diagnose/diagnose.go`
- Test: `internal/diagnose/diagnose_test.go`

**Interfaces:**
- Consumes: `logfmt.Stats`, `logfmt.Interner`, `logfmt.Entry`, `logfmt.Fields`, `span.Capabilities`, `span.Span`, `MaskProse`, `MaskComponent`.
- Produces: `diagnose.NewCollector(*logfmt.Interner) *Collector` satisfying `logfmt.Sink`; `diagnose.Report`; `diagnose.Build(logfmt.Stats, span.Capabilities, []span.Span, *Collector, *logfmt.Interner, time.Duration) Report`; `Report.Render(io.Writer) error`.

Maps are capped at `maxDistinct` keys with overflow counted, so a high-cardinality log cannot grow them without bound.

- [ ] **Step 1: Write the failing test**

```go
package diagnose

import (
	"strings"
	"testing"
	"time"

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
	return Build(st, sn.Report(), b.Spans(), c, &comps, 5*time.Millisecond)
}

func render(t *testing.T, r Report) string {
	t.Helper()
	var sb strings.Builder
	if err := r.Render(&sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return sb.String()
}

func TestReportNeverLeaksWellFormedFieldValues(t *testing.T) {
	const secret = "SUPERSECRETVALUE"
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource api_token=` + secret + ` tf_req_duration_ms=5` + "\n"
	out := render(t, build(t, in))
	if strings.Contains(out, secret) {
		t.Fatal("report leaked a well-formed field value")
	}
	if !strings.Contains(out, "api_token") {
		t.Error("field key api_token missing from report")
	}
}

// The hclog multi-line value shape. Body lines must contribute no fields and
// no template text, so nothing inside the body can reach the report.
func TestReportNeverLeaksMultilineValueBody(t *testing.T) {
	const secret = "IyEvYmluL2Jhc2gKZWNobyBodW50ZXIy"
	in := "2024-02-13T12:11:28.330+0100 [DEBUG] provider.aws: HTTP Response Received: @module=aws aws.operation=UpdateProject\n" +
		`  http.response.body=` + "\n" +
		`  | {"UserData":"` + secret + `==","Token":"AQoDYXdzEPT//x=="}` + "\n" +
		"2024-02-13T12:11:28.331+0100 [TRACE] provider.aws: Received downstream response: tf_rpc=ApplyResourceChange tf_req_duration_ms=10710\n"
	out := render(t, build(t, in))
	if strings.Contains(out, secret) {
		t.Fatal("report leaked an hclog multi-line value body")
	}
	if strings.Contains(out, "UserData") || strings.Contains(out, "AQoDYXdz") {
		t.Fatalf("report leaked body content:\n%s", out)
	}
}

// hclog escapes embedded quotes; the value's remainder must not be
// re-tokenised into printed keys.
func TestReportNeverLeaksEscapedQuoteContents(t *testing.T) {
	in := `2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: diagnostic_summary="bad header \"Authorization=Bearer s3cr3t\" here" tf_rpc=ReadResource tf_req_duration_ms=5` + "\n"
	out := render(t, build(t, in))
	if strings.Contains(out, "s3cr3t") || strings.Contains(out, "Authorization") {
		t.Fatalf("report leaked escaped-quote value contents:\n%s", out)
	}
}

func TestReportNeverLeaksResourceAddresses(t *testing.T) {
	in := `2026-08-29T10:34:43.219+0200 [ERROR] module.vpc["datacenter1"].aws_internet_gateway.this[0] encountered an error` + "\n" +
		"2026-08-29T10:34:43.220+0200 [DEBUG] terraform_data.r1: applying the planned Create change\n"
	out := render(t, build(t, in))
	for _, leak := range []string{"datacenter1", "internet_gateway", "terraform_data.r1"} {
		if strings.Contains(out, leak) {
			t.Errorf("report leaked %q:\n%s", leak, out)
		}
	}
}

func TestReportCountsFieldKeys(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=5\n" +
		"2022-12-15T00:16:20.900Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=9\n"
	if got := build(t, in).FieldKeys["tf_req_duration_ms"]; got != 2 {
		t.Errorf("tf_req_duration_ms count = %d, want 2", got)
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
	// The reported duration must survive the start clamp.
	if r.SlowestMs != 5000 {
		t.Errorf("SlowestMs = %d, want 5000", r.SlowestMs)
	}
	if r.TotalSpanMs != 5000 {
		t.Errorf("TotalSpanMs = %d, want 5000", r.TotalSpanMs)
	}
}

func TestReportWarnsWhenNoProviderEntries(t *testing.T) {
	in := "2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete\n"
	r := build(t, in)
	if r.TierUsable {
		t.Error("TierUsable = true for a core-only log")
	}
	if out := render(t, r); !strings.Contains(out, "no provider RPC") {
		t.Errorf("report does not explain the absence of provider data:\n%s", out)
	}
}

func TestReportRendersThroughputAndWallClock(t *testing.T) {
	in := "2022-12-15T00:16:20.000Z [TRACE] a: one\n" +
		"2022-12-15T00:16:30.000Z [TRACE] a: two\n"
	out := render(t, build(t, in))
	for _, want := range []string{"log wall-clock", "parse throughput"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "10.0s") {
		t.Errorf("report does not show the 10s log span:\n%s", out)
	}
}

func TestReportCapsDistinctKeys(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxDistinct+500; i++ {
		sb.WriteString("2022-12-15T00:16:20.800Z [TRACE] a: message shape ")
		sb.WriteString(strings.Repeat("x", i%40+1))
		sb.WriteString("\n")
	}
	r := build(t, sb.String())
	if len(r.templateCount) > maxDistinct {
		t.Errorf("template map grew to %d, want at most %d", len(r.templateCount), maxDistinct)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/diagnose/ -v`
Expected: FAIL — `undefined: NewCollector`.

- [ ] **Step 3: Implement `diagnose.go`**

```go
package diagnose

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

const (
	// maxTemplates caps how many distinct shapes are printed.
	maxTemplates = 15
	// maxDistinct caps how many distinct keys any map retains, so a
	// high-cardinality log cannot grow them without bound.
	maxDistinct = 4096
)

// Template is a message shape with its content masked.
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
	dropped   uint64
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

// bump increments m[key], refusing new keys once the map is full.
func (c *Collector) bump(m map[string]uint64, key string) {
	if _, ok := m[key]; !ok && len(m) >= maxDistinct {
		c.dropped++
		return
	}
	m[key]++
}

// Entry implements logfmt.Sink.
func (c *Collector) Entry(ord uint32, e logfmt.Entry, msg string, f logfmt.Fields) {
	c.bump(c.compCount, MaskComponent(c.comps.Lookup(e.Comp)))
	for _, fl := range f {
		c.bump(c.fieldKeys, fl.Key)
	}
	c.bump(c.templates, template(msg, f))
}

// template renders a message with prose masked and every field value replaced
// by a placeholder, so a shape can be reported without disclosing content.
func template(msg string, f logfmt.Fields) string {
	prose := msg
	if len(f) > 0 {
		if i := strings.Index(msg, f[0].Key+"="); i >= 0 {
			prose = msg[:i]
		}
	}
	prose = MaskProse(prose)

	keys := make([]string, 0, len(f))
	for _, fl := range f {
		keys = append(keys, fl.Key+"=<v>")
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return prose
	}
	if prose == "" {
		return strings.Join(keys, " ")
	}
	return prose + " " + strings.Join(keys, " ")
}

// Report is the finished diagnostic summary.
type Report struct {
	Stats         logfmt.Stats
	Caps          span.Capabilities
	Tier          span.Fidelity
	TierUsable    bool
	SpanCount     int
	ClampedSpans  int
	SlowestMs     uint32
	TotalSpanMs   uint64
	Elapsed       time.Duration
	FieldKeys     map[string]uint64
	TopComponents []Template
	TopTemplates  []Template
	DistinctComps int
	InternOverflow uint64
	DroppedKeys   uint64

	templateCount map[string]uint64
}

// Build assembles a Report from a completed scan.
func Build(st logfmt.Stats, caps span.Capabilities, spans []span.Span, c *Collector, comps *logfmt.Interner, elapsed time.Duration) Report {
	tier, usable := caps.BestFidelity()
	r := Report{
		Stats:          st,
		Caps:           caps,
		Tier:           tier,
		TierUsable:     usable,
		SpanCount:      len(spans),
		Elapsed:        elapsed,
		FieldKeys:      c.fieldKeys,
		TopComponents:  topN(c.compCount, maxTemplates),
		TopTemplates:   topN(c.templates, maxTemplates),
		DistinctComps:  comps.Len(),
		InternOverflow: comps.Overflowed(),
		DroppedKeys:    c.dropped,
		templateCount:  c.templates,
	}
	for _, s := range spans {
		r.TotalSpanMs += uint64(s.DurationMs)
		if s.DurationMs > r.SlowestMs {
			r.SlowestMs = s.DurationMs
		}
		if s.StartClamped {
			r.ClampedSpans++
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

	fmt.Fprintf(b, "tfli diagnostic report\n======================\n\n")

	fmt.Fprintf(b, "SIZE\n")
	fmt.Fprintf(b, "  bytes                %d\n", r.Stats.Bytes)
	fmt.Fprintf(b, "  physical lines       %d\n", r.Stats.PhysicalLines)
	fmt.Fprintf(b, "  logical entries      %d\n", r.Stats.Entries)
	if r.Stats.PhysicalLines > 0 {
		fmt.Fprintf(b, "  mean line length     %d bytes\n", r.Stats.Bytes/r.Stats.PhysicalLines)
		fmt.Fprintf(b, "  untimestamped lines  %d (%.1f%% of lines)\n",
			r.Stats.UntimestampedLines,
			100*float64(r.Stats.UntimestampedLines)/float64(r.Stats.PhysicalLines))
	}
	if r.Stats.Bytes > 0 {
		fmt.Fprintf(b, "  non-hclog bytes      %d (%.1f%% of file)\n",
			r.Stats.ContinuationBytes,
			100*float64(r.Stats.ContinuationBytes)/float64(r.Stats.Bytes))
	}
	fmt.Fprintf(b, "  long output blocks   %d\n", r.Stats.LongContinuationRuns)
	fmt.Fprintf(b, "  ANSI escapes         %v\n", r.Stats.SawANSI)
	if !r.Stats.FirstTS.IsZero() {
		fmt.Fprintf(b, "  log wall-clock       %.1fs\n", r.Stats.LastTS.Sub(r.Stats.FirstTS).Seconds())
	}
	if r.Elapsed > 0 {
		fmt.Fprintf(b, "  parse throughput     %.0f MB/s (%s)\n",
			float64(r.Stats.Bytes)/(1<<20)/r.Elapsed.Seconds(), r.Elapsed.Round(time.Millisecond))
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "LEVELS\n")
	for l := logfmt.LevelUnknown; l <= logfmt.LevelError; l++ {
		if n := r.Stats.ByLevel[l]; n > 0 {
			fmt.Fprintf(b, "  %-8s %d\n", l, n)
		}
	}
	fmt.Fprintf(b, "\n")

	fmt.Fprintf(b, "EXTRACTION\n")
	if r.TierUsable {
		fmt.Fprintf(b, "  selected tier        %s\n", r.Tier)
	} else {
		fmt.Fprintf(b, "  selected tier        NONE USABLE\n")
		fmt.Fprintf(b, "  This log contains no provider RPC entries, so there is\n")
		fmt.Fprintf(b, "  nothing to profile. If the plan ran on HCP Terraform,\n")
		fmt.Fprintf(b, "  enable debug logging on the run and use its raw log.\n")
	}
	fmt.Fprintf(b, "  response entries     %d\n", r.Caps.ResponseEntries)
	fmt.Fprintf(b, "  request entries      %d\n", r.Caps.RequestEntries)
	fmt.Fprintf(b, "  duration fields      %d\n", r.Caps.DurationFields)
	fmt.Fprintf(b, "  req id fields        %d\n", r.Caps.ReqIDFields)
	fmt.Fprintf(b, "  provider entries     %d\n", r.Caps.ProviderEntries)
	fmt.Fprintf(b, "  core vertex lines    %d\n", r.Caps.CoreVertexLines)
	fmt.Fprintf(b, "  core GRPC lines      %d\n\n", r.Caps.CoreGRPCLines)

	fmt.Fprintf(b, "SPANS\n")
	fmt.Fprintf(b, "  spans built          %d\n", r.SpanCount)
	fmt.Fprintf(b, "  slowest span         %d ms\n", r.SlowestMs)
	fmt.Fprintf(b, "  total span time      %d ms\n", r.TotalSpanMs)
	fmt.Fprintf(b, "  starts clamped       %d\n\n", r.ClampedSpans)

	if r.Stats.BackwardsTimestamps > 0 || r.InternOverflow > 0 || r.DroppedKeys > 0 || r.Stats.LinesSaturated > 0 {
		fmt.Fprintf(b, "ANOMALIES\n")
		fmt.Fprintf(b, "  backwards timestamps %d\n", r.Stats.BackwardsTimestamps)
		fmt.Fprintf(b, "  intern overflow      %d\n", r.InternOverflow)
		fmt.Fprintf(b, "  dropped map keys     %d\n", r.DroppedKeys)
		fmt.Fprintf(b, "  line-count saturated %d\n\n", r.Stats.LinesSaturated)
	}

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

	fmt.Fprintf(b, "MESSAGE TEMPLATES (content masked, top %d)\n", len(r.TopTemplates))
	for _, t := range r.TopTemplates {
		fmt.Fprintf(b, "  %8d  %s\n", t.Count, t.Text)
	}
	fmt.Fprintf(b, "\n")
	fmt.Fprintf(b, "Field keys are reported verbatim and are restricted to an\n")
	fmt.Fprintf(b, "identifier charset. Message shapes are masked heuristically.\n")
	fmt.Fprintf(b, "Review this output before sharing it.\n")

	_, err := io.WriteString(w, b.String())
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./internal/diagnose/ -v`
Expected: PASS, all fifteen tests.

- [ ] **Step 5: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add internal/diagnose/diagnose.go internal/diagnose/diagnose_test.go
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Summarise log structure without disclosing field values"
```

---

### Task 10: CLI wiring and installability

**Files:**
- Create: `cmd/tfli/main.go`, `README.md`
- Test: `cmd/tfli/main_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: the `tfli` binary. Flags: `--diagnose`, `-o <file>`, `--version`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"io"
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
	for _, want := range []string{"tfli diagnostic report", "selected tier        reported", "spans built          2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// The mixed fixture's plan output must register as real non-hclog content,
// driven by the plan block rather than by the fixture's comment header.
func TestRunReportsNonHclogContent(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "mixed-hcp.log")}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	if strings.Contains(out, "untimestamped lines  0 ") || strings.Contains(out, "untimestamped lines  1 ") {
		t.Errorf("non-hclog content not measured:\n%s", out)
	}
	if !strings.Contains(out, "long output blocks   1") {
		t.Errorf("plan output block not detected:\n%s", out)
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
	out := filepath.Join(t.TempDir(), "report.txt")
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

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C /Users/dan/Code/tf-log-inspector test ./cmd/tfli/ -v`
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
	"time"

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

	started := time.Now()
	stats, err := logfmt.Scan(bufio.NewReaderSize(f, 1<<20), &comps, collector, sniffer, &builder)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", path, err)
	}
	elapsed := time.Since(started)

	report := diagnose.Build(stats, sniffer.Report(), builder.Spans(), collector, &comps, elapsed)

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

Run: `go -C /Users/dan/Code/tf-log-inspector test ./... -v`
Expected: PASS, every package.

- [ ] **Step 5: Verify the whole build**

```bash
go -C /Users/dan/Code/tf-log-inspector vet ./...
gofmt -l /Users/dan/Code/tf-log-inspector
go -C /Users/dan/Code/tf-log-inspector build ./...
go -C /Users/dan/Code/tf-log-inspector run ./cmd/tfli --diagnose testdata/provider-rpc.log
go -C /Users/dan/Code/tf-log-inspector run ./cmd/tfli --diagnose testdata/core-only.log
go -C /Users/dan/Code/tf-log-inspector run ./cmd/tfli --diagnose testdata/multiline-body.log
go -C /Users/dan/Code/tf-log-inspector run ./cmd/tfli --diagnose testdata/mixed-hcp.log
```

Expected: `gofmt -l` prints nothing. `provider-rpc.log` reports tier `reported`, 2 spans, slowest 5 ms. `core-only.log` reports `NONE USABLE` plus the HCP guidance. `multiline-body.log` reports 1 span of 10710 ms and **no JSON body content anywhere in the output**. `mixed-hcp.log` reports a non-zero untimestamped-line percentage and one long output block.

- [ ] **Step 6: Verify the disclosure guarantee against the real ground-truth log**

```bash
go -C /Users/dan/Code/tf-log-inspector run ./cmd/tfli --diagnose /private/tmp/claude-501/-Users-dan-Code-tf-log-inspector/b63ba3b2-5791-4e1b-a8af-38977800b3eb/scratchpad/trace.log
```

Read the output in full. Confirm no username, filesystem path, resource address or quoted string appears. This log previously produced the keys `"-input` and `[id`; neither should now appear. If anything content-bearing survives, that is a masking gap to fix before shipping — not a cosmetic issue.

- [ ] **Step 7: Write the README**

```markdown
# tf-log-inspector (`tfli`)

Find where a slow Terraform plan spent its time.

## Install

    go install github.com/yesdevnull/tf-log-inspector/cmd/tfli@latest

## Getting a log

Timing data comes from provider RPC entries, which exist only where the
providers actually run. For an HCP Terraform workspace the plan runs on
HashiCorp's runners, so a locally captured `TF_LOG` will not contain it.

Instead, on a new run expand **Additional Planning Options**, enable **Debug
Logging**, then download the run's raw log.

For a local plan:

    TF_LOG=TRACE TF_LOG_PATH=plan.log terraform plan

## Usage

    tfli --diagnose plan.log
    tfli --diagnose -o report.txt plan.log

`--diagnose` reports the log's structure: size, levels, which extraction tier
applies, which fields are present, and the most common message shapes.

### What the report discloses

Field **keys** are reported verbatim, restricted to an identifier charset so
log content cannot pose as a key. Field **values** are never reported. Message
shapes are reported with quoted strings, paths, resource addresses and long
identifiers masked — a heuristic, not a guarantee.

Review the report before sharing it.
```

- [ ] **Step 8: Commit**

```bash
git -C /Users/dan/Code/tf-log-inspector add cmd README.md
/Users/dan/.claude/bin/claude-git -C /Users/dan/Code/tf-log-inspector commit -m "Add tfli command with --diagnose and installation docs"
```

---

## Self-Review

**Review findings addressed.** All three criticals: C1 by the strict key charset plus header-line-only field parsing (Tasks 3 and 5); C2 by `MaskProse`/`MaskComponent` (Task 8); C3 by storing `DurationMs` rather than deriving it (Task 6). Majors M1-M9: newline delimiting and header-only parsing (Tasks 3, 5); timestamp clamping with a counter (Task 5); bounded memory via header-only retention and `maxHeaderMsg` (Task 5); map caps (Task 9); truthful fixture provenance (Task 1); untimestamped lines and bytes counted file-wide with long-run detection (Tasks 5, 9); interner and line-count saturation (Tasks 1, 5); throughput and wall-clock rendering (Tasks 9, 10). Minors N1-N6: `NewSniffer`-only construction (Task 7), `gofmt` verified in Task 10 Step 5, `StripANSI` returning its scratch buffer (Task 4), dead `curHdr` removed (Task 5), added tests for empty values, escaped quotes, CRLF, EOF without newline, backwards timestamps and ordinals, and `claude-git` plus directory flags throughout.

**Spec coverage.** Phase 1 is "Parser, sniffer, `--diagnose`. No TUI." Parser: Tasks 2-5. Sniffer: Task 7. `--diagnose`: Tasks 8-9. CLI: Task 10. Tier 1 extraction (Task 6) is included because the report must state how many spans a real log yields.

Every spec `--diagnose` requirement is now present: tier selected and why; counts per known shape; unmatched templates with content masked; observed `tf_*` fields; core-side line presence; line count and mean line length; non-hclog proportion by lines *and* bytes; ANSI presence; parse throughput.

**Deferred, consistent with spec phasing:** tier 2-4 builders; address attribution and its confidence distribution (spec phase 5, and it cannot exist before attribution does); retaining the entry index for random access (needs mmap, spec phase 3); the `plan -json` parser (spec phase 6).

**Type consistency.** `logfmt.Sink` is implemented by `diagnose.Collector`, `span.Sniffer` and `span.ReportedBuilder`, all with `Entry(uint32, logfmt.Entry, string, logfmt.Fields)`. `NewSniffer` and `NewCollector` both take `*logfmt.Interner`. `Build` takes a `time.Duration` supplied by `run`. `Span.DurationMs` is consumed by `Build`. `StripANSI` returns `(string, []byte)` and both call sites use both results.

**Known heuristic, stated rather than hidden.** `looksLikeAddress` separates Terraform addresses from Go identifiers by the case of the character after the dot. `terraform_data.r1` masks; `terraform.NewContext` does not. A resource type whose name begins with an upper-case letter, or a core identifier lower-case after the dot, would be classified wrongly. Task 10 Step 6 exists to catch such gaps against a real log before shipping.

---

## Execution Handoff

Plan revised and ready. Two execution options:

1. **Subagent-Driven (recommended)** — a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session with checkpoints for review.
