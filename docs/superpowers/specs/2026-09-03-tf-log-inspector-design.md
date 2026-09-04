# tf-log-inspector (`tfli`) — design

**Date:** 2026-09-03
**Status:** Approved design, pending implementation plan
**Module path:** `github.com/yesdevnull/tf-log-inspector`

## Purpose

A terminal UI for loading a Terraform `TF_LOG` output file and finding where a
slow plan spent its time.

Two use cases, in priority order:

1. **Primary — profiling.** "This plan takes eight minutes. Which provider
   calls, resource types and providers ate the time, and was it work or
   waiting?"
2. **Secondary — spelunking.** "Something odd happened in this plan and I need
   to read a several-hundred-megabyte TRACE log without dying."

The design treats (1) as the core and (2) as a view onto the same data, joined
by the ability to jump from any measured span directly to the log lines that
produced it.

## Constraints

These shaped the design more than any feature request.

- **The target logs are unavailable.** The logs this tool exists to read are
  work artefacts that cannot leave the work environment, and the project is
  deliberately not developed on work hardware. No log from the environment this
  tool is built for will ever be present in this repository.
- **Public logs partially compensate.** Bug reports against
  `hashicorp/terraform` and the major providers routinely include real
  `TF_LOG=TRACE` excerpts. These are public, safe to commit, and are the basis
  of every fixture. They establish the *format* reliably; they cannot establish
  the *scale, concurrency or provider mix* of a real CI plan, which is what
  phase 1 exists to measure.
- **The feedback loop is: implement here → Dan pulls and runs on work hardware
  → Dan reports back.** Every design decision that could be validated by
  reading a real log must instead be validated by the tool reporting on itself.
- **Target log size is 1GB.** Typical logs are several hundred megabytes.
- **Distribution is via `go install`.** A user should get a working binary
  without a build toolchain beyond Go.

The blind-development constraint is why `--diagnose` (below) exists and is
built before the TUI.

## The input is an HCP Terraform raw run log

All target workspace runs execute in **HCP Terraform**, not locally. This is
the single most important fact about the input, and it was established late
enough to invalidate an earlier assumption in this document.

**A locally captured `TF_LOG_PATH` is useless for this tool's primary purpose.**
When a workspace uses a remote execution backend, the local CLI only
orchestrates a run over the HCP API; no provider plugin process starts locally,
so the local log contains no provider RPC entries and there is nothing to
profile. The sampled gist log has exactly this shape.

TRACE output must therefore be produced *on the runner* and retrieved from the
run:

- **Per-run** — on a new run, expand *Additional Planning Options* and toggle
  **Enable Debug Logging**. Applies to that run only. Available in all HCP
  Terraform tiers and Terraform Enterprise v202502-2 or later.
- **Persistent** — add a workspace **environment** variable `TF_LOG` = `TRACE`.
  Applies to all subsequent runs until removed. HCP Terraform `export`s
  workspace environment variables into the shell before running Terraform.

The resulting log is retrieved from the run UI via **View raw log**.

### What this implies for the parser

The retrieved artefact is the runner's combined output, not a clean hclog
stream. It contains at least three interleaved kinds of content:

1. hclog TRACE/DEBUG entries — the material this tool profiles.
2. Terraform's ordinary human-readable plan output — resource diffs, the
   `Plan: N to add…` summary.
3. HCP Terraform's own harness output around the Terraform invocation.

**The parser must treat arbitrary non-hclog content as ordinary, not
exceptional.** It is preserved and displayed in the raw view, ignored for span
extraction, and never causes a parse failure.

One known imprecision, recorded rather than over-engineered: an untimestamped
line is currently treated as a continuation of the preceding entry, which is
correct for wrapped diagnostics but will also absorb blocks of plan output into
whatever entry precedes them. This affects only how the raw view groups lines —
span extraction reads solely from timestamped `Received downstream response`
entries — so it is cosmetic, not a correctness risk. If it proves ugly in
practice, the fix is to break continuation runs longer than a threshold into
their own `(output)` block.

**Unconfirmed:** whether the raw log carries ANSI escape sequences. It likely
does, since it is captured terminal output. `--diagnose` reports whether any
are present, and the parser strips them if so. This is flagged rather than
assumed.

## Verified log format facts

These were read from the upstream source, not assumed. Everything else in this
document that touches log format is explicitly marked as an open question.

Terraform providers built on `terraform-plugin-go` (which includes everything
on `terraform-plugin-framework` and `terraform-plugin-sdk` v2) log every
protocol RPC through `internal/logging` in the `proto` subsystem:

- `DownstreamRequest` emits a TRACE log `"Sending request downstream"` and
  records a start time.
- `DownstreamResponse` emits a TRACE log `"Received downstream response"`
  including `tf_req_duration_ms`, the measured duration of the RPC in
  milliseconds.

The structured field keys are defined as constants and upstream explicitly
notes they are treated as a compatibility surface: *"Practitioners or tooling
reading logs may be depending on these keys, so be conscious of that when
changing them."*

Fields relevant to this tool:

| Key | Meaning |
|-----|---------|
| `tf_req_duration_ms` | Duration in milliseconds for the RPC request |
| `tf_req_id` | Unique ID for the RPC request |
| `tf_rpc` | The RPC being run, e.g. `ApplyResourceChange` |
| `tf_provider_addr` | Full provider address, e.g. `registry.terraform.io/hashicorp/random` |
| `tf_resource_type` | Resource **type**, e.g. `random_pet` |
| `tf_data_source_type` | Data source type, e.g. `archive_file` |
| `tf_proto_version` | Protocol version as a string |
| `diagnostic_severity`, `diagnostic_summary`, `diagnostic_detail` | Diagnostic content |

Sources:
- `hashicorp/terraform-plugin-go` — `internal/logging/keys.go`
- `hashicorp/terraform-plugin-go` — `tfprotov5/internal/tf5serverlogging/downstream_request.go`
  (and the identical `tfprotov6` variant)

### Real log lines

The following are genuine lines taken from public GitHub issues, not
reconstructions. They are the ground truth the parser is written against.

A provider RPC response, from `hashicorp/terraform-provider-aws#28364`
(wrapped here; one line in the file):

```
2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5:
Received downstream response: tf_req_id=2634bc46-bb66-3d22-528d-d2eaf8165f52
tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange diagnostic_error_count=1
diagnostic_warning_count=0 tf_proto_version=5.3
tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5
@caller=github.com/hashicorp/terraform-plugin-go/... @module=sdk.proto
```

Terraform core lines, from `hashicorp/terraform#39094` and the same AWS issue:

```
2026-09-01T10:53:44.877+1000 [INFO]  Terraform version: 1.16.0
2026-09-01T10:53:44.877+1000 [TRACE] Stdout is a terminal of width 428
[TRACE] vertex "aws_codebuild_project.codebuild_name": visit complete
[TRACE] GRPCProvider: GetProviderSchema
[TRACE] NodeAbstractResouceInstance.writeResourceInstanceState: writing state
object for aws_codebuild_project.codebuild_name
[TRACE] evalApplyProvisioners: module.vpc["datacenter1"].aws_internet_gateway.this[0]
is tainted, so skipping provisioning
```

Format facts these establish, several of which contradict reasonable
assumptions and must be handled explicitly:

1. **Two timestamp offset formats occur.** `2022-12-15T00:16:20.800Z` (UTC)
   and `2026-09-01T10:53:44.877+1000` (numeric offset, **no colon**). The
   parser must accept both.
2. **The level is bracketed and space-padded to five characters** — `[TRACE]`,
   `[DEBUG]`, `[INFO] `, `[WARN] `, `[ERROR]`. Note the trailing spaces on the
   shorter names.
3. **The logger name is the plugin binary name, not the provider address** —
   `provider.terraform-provider-aws_v4.46.0_x5`, including version and protocol
   suffix. The canonical address is only available in the `tf_provider_addr`
   field. Rollups must key on the field; the binary name is useful as a lane
   identity because it distinguishes concurrent plugin processes.
4. **The message ends with a colon before the fields begin** — `Received
   downstream response: tf_req_id=…`.
5. **Field order is not stable.** The two sampled response lines list the same
   fields in different orders. Positional parsing is invalid; the parser must
   parse `key=value` pairs into a map.
6. **hclog metadata fields are `@`-prefixed** — `@module=sdk.proto`,
   `@caller=…`. `@module` usefully distinguishes SDK protocol logging from
   provider-authored logging.
7. **Core lines have no logger-name prefix at all** — the message follows the
   level directly. The parser must treat an absent subsystem as valid rather
   than a parse failure.

### Resource addresses

`tf_resource_type` is a type, not an address. A provider does not know it is
planning `aws_instance.web[0]`; it knows it is planning an `aws_instance`.

However, **Terraform core does log full addresses**, including module paths and
index keys, as the samples above show — `module.vpc["datacenter1"].aws_internet_gateway.this[0]`.
Core also logs its own side of each provider call as `[TRACE] GRPCProvider:
<RPCName>`, and brackets graph node work with `vertex "<address>": …` lines.

This makes address attribution feasible by correlation: a provider's timed RPC
span is matched to the core-side address context active at that moment, keyed
on RPC name, resource type and time. It is **inference, not observation**, and
is treated as such throughout — see Address attribution below.

## The observer effect: logging changes what is measured

**Measured 2026-09-04, and it is large.** Four captures of the same HCP
workspace, days apart but the same configuration:

| capture | wall clock |
|---|---|
| normal (structured output only) | 24.1s |
| debug toggle | 138.3s |
| `TF_LOG=TRACE` | 730.0s |
| debug toggle + `TF_LOG_PROVIDER`/`TF_LOG_SDK_PROTO` at TRACE | 522.2s |

A plan that takes 24 seconds unlogged takes around 20 times longer with debug
logging on. The tool cannot see an unlogged run — its only input is the log —
so **every absolute duration it reports is a duration under logging**.

**The mechanism is already documented above:** Terraform captures a provider's
stderr and re-logs it line by line through its own logger. Every line is
re-parsed and re-emitted, then captured again by HCP. Providers that dump HTTP
bodies at DEBUG therefore pay that cost per line of body.

**A worked example, because the magnitude is easy to disbelieve.** The
slowest single RPC in the dual-tier capture was a 116,591 ms `azurerm`
`Configure`. Inside that span the provider made exactly one Azure API call —
`GET /subscriptions/<id>/providers`, the resource-provider list — plus two
token requests. `EnsureRegistered` completed 2 ms before the span closed,
having found every required provider already registered, so no registration
work occurred. What the span contains instead is the API response: 3.1 MB of
azurerm log lines, inside a window of 14.8 MB — **49% of the entire 30 MB log
produced by one call**.

### What this means for the tool

- **Rankings within a capture remain valid.** Every span in a log paid the
  same logging tax, so "which resource type cost the most" and "which call was
  slowest" survive. The Microsoft Graph reads really are the slow ones.
- **Absolute durations do not transfer to an unlogged run**, and neither do
  ratios between a heavily-logging provider and a quiet one. A provider that
  dumps HTTP bodies is penalised against one that does not, so cross-provider
  comparison is the least trustworthy reading.
- **The TUI must not present these as wall-clock truth.** Phase 3 and phase 4
  render timelines and rollups from exactly these numbers; a timeline that
  says "116 seconds" invites a reader to go and optimise 116 seconds that will
  not exist without the log. Some standing caveat belongs in the interface,
  not only in this document.

### Honest limits of this measurement

The four captures are separate runs, and upstream API latency genuinely varies
between them — `azuread_service_principal` totalled 19.0s across 20 reads in
one capture and 247.0s across 17 in another, which is real variance and not
logging overhead. So the 20x figure is an observation about four runs, not a
controlled experiment. What is not in doubt is the direction, the rough
magnitude, and the mechanism.

**The controlled test, if it is ever worth running:** the structured-output
capture is already the control at 24.1s, since it needs nothing enabled. Two
normal runs, one with `resource_provider_registrations = "none"` and one
without, would isolate the resource-provider list call's true cost without any
logging tax at all.

## Architecture

```
cmd/tfli/main.go      flag parsing, wiring, terminal setup
internal/hclog/       TF_LOG TRACE/DEBUG line scanner
internal/tfjson/      `terraform plan -json` stream parser
internal/model/       Line/Span tables, rollups, filters, interning
internal/diagnose/    structural report over a parsed log
internal/tui/         bubbletea program and views
```

`internal/tui` is the only package permitted to import bubbletea or lipgloss.
Nothing below it knows a terminal exists.

**Decided 2026-09-04, at the start of phase 3.** The dependencies are taken.
`go.mod` gains its first `require` block: bubbletea v1.3.10 (8 direct and 9
indirect modules of its own) plus lipgloss. Everything outside `internal/tui`
stays dependency-free, and `internal/model` and `internal/profile` must not
gain a terminal import.

The alternative was hand-rolling with the standard library, which needs raw
terminal mode via termios syscalls with separate Windows console handling,
plus ANSI rendering, resize handling and escape-sequence decoding. That is the
same class of platform-specific code the `mmap` decision above rejects — and
the parts most likely to be subtly wrong are the ones that cannot be tested
without a terminal. Rejecting that complexity for a 37MB buffer and then
accepting it for a screen renderer would not be consistent.

**The risk this accepts,** recorded because it falls on the one machine this
project cannot test: distribution is via `go install` onto work hardware, and
a corporate module proxy or dependency vetting may not serve seventeen
modules as readily as it serves none. `--diagnose` and `--profile` are
unaffected either way; if the TUI proves uninstallable there, the headless
modes remain the whole product and the dependency was still confined to the
package that can be dropped. This keeps the parked features
(run comparison, headless output) cheap to add later, and it is what makes the
TUI testable as a pure function.

### Input handling

~~The log file is `mmap`ed once.~~ **Revised 2026-09-04:** the log file is
read into a single `[]byte`. Every parsed structure refers to bytes by offset
into that buffer, so "jump from a span to its log lines" is still a slice
expression rather than a search.

The `mmap` decision was sized against a 1GB log at 102 bytes per line. Four
real HCP captures measure **365 to 828 bytes per line** and **17 to 37MB**
total, so the largest observed log costs 37MB resident plus a ~1MB entry
index. `mmap` would buy constant residency for a log size that has not
appeared, at the cost of platform-specific code and build tags in a package
that has none. (That phrasing originally read "a project whose hard constraint
is zero third-party dependencies", which overstated it: this document has
always permitted `internal/tui` to import bubbletea. The argument against
`mmap` is about platform-specific code in `internal/model`, not about
dependencies.) Reading the file
whole is the simplest thing that works at the measured sizes; the accessor
is an interface boundary, so swapping in `ReadAt` or `mmap` later is
contained if a 1GB log ever turns up.

Parsing runs on a goroutine and reports progress into the bubbletea event loop,
so the UI is interactive and cancellable while a large file loads. The raw log
view is usable as soon as the file is mapped; ranked views populate when
parsing completes.

**Added 2026-09-04:** `--profile` (phase 2) reads the whole file into memory
via `model.Load`, the same whole-file read this section justifies above.
`--diagnose` (phase 1) streams instead, via `logfmt.Scan` over an
`os.Open`ed `io.Reader`, and never holds the file whole. The asymmetry is
deliberate, not an inconsistency to reconcile: `--diagnose` only ever needs
one entry at a time to build its structural report, while `--profile`'s
model layer needs random access into `Log.Data` for span-to-log jumping and
for holding both tiers' spans at once. Both are bounded by the same 17–37MB
observed captures above, so neither mode's memory cost is a concern at
today's measured sizes; this is worth re-checking if a much larger capture
ever turns up.

## Data model

The indexed unit is a **logical entry**, not a physical line. A single log
entry frequently spans several physical lines: in the sampled real log, **239
of 1,278 lines (19%) were continuation lines carrying no timestamp or level**.
Multi-line entries are therefore the normal case, not an edge case.

Indexing physical lines would break both halves of the tool — filtering by
level would strip continuation lines away from their parent entry, and jumping
from a span to its log lines would land on a fragment. An entry's byte range
covers all of its lines.

```go
// Entry is one logical log entry — a timestamped line plus any continuation
// lines that follow it — located in the mmap'd buffer.
type Entry struct {
    Off   uint64 // byte offset of the entry's first line
    Len   uint32 // bytes covering ALL lines of the entry
    TSms  uint32 // milliseconds since the first entry's timestamp
    Level uint8  // TRACE/DEBUG/INFO/WARN/ERROR
    Comp  uint16 // interned component (see below)
    Lines uint16 // physical line count, for rendering
}

// Span is one provider operation with a measured duration.
type Span struct {
    StartEntry, EndEntry uint32 // indices into []Entry
    StartMs, EndMs     uint32
    RPC, Provider      uint16 // interned
    ResourceType       uint32 // interned; observed, from tf_resource_type
    ResourceAddr       uint32 // interned; inferred by correlation, 0 if unknown
    Fidelity           uint8  // which SpanBuilder produced this
    AddrConfidence     uint8  // Exact | Likely | Ambiguous | Unknown
}
```

Neither struct contains a pointer or a string. Go's garbage collector therefore
never traces the line and span slices — they are two large pointer-free
allocations it walks past. Strings live in a small intern table; distinct
providers, RPC names and resource types number in the thousands at most.

**Component facets.** Terraform core writes messages that *begin* with a
component prefix — `terraform.contextPlugins:`, `statemgr.Filesystem:`,
`ProviderTransformer:` — which is textually indistinguishable from an hclog
named logger such as `provider.terraform-provider-aws_v4.46.0_x5:`. Core's root
logger is unnamed, so there is no way to tell them apart from the text, and no
reason to try: both identify the component that emitted the entry. The parser
treats the token before the first `:` as `Comp` in either case, and that is
what the facet pane lists. Provider entries remain identifiable by their
`provider.` prefix.

**Sizing.** The sampled real log averaged **102 bytes per line**, half my
earlier estimate. At that rate a 1GB log is roughly 10 million physical lines;
at 19% continuations, about 8 million logical entries. `Entry` is 24 bytes
padded, so the index costs roughly **190MB resident**, plus OS-managed mmap
pages that are evictable under memory pressure.

That is higher than the earlier 120MB figure and worth watching, though TRACE
logs containing large state dumps will skew the average line longer and the
entry count lower. If it proves too high, the lever is to drop `Lines` and
derive it at render time. This should be measured, not pre-optimised — phase 1
reports the real figures.

## Span extraction

Pairing strategy is an interface, not a hardcoded assumption. The loader sniffs
the first few megabytes and selects the highest-fidelity builder the log
actually supports.

```go
// SpanBuilder turns a stream of parsed lines into spans.
type SpanBuilder interface {
    Name() string       // shown in the UI header
    Fidelity() Fidelity // Reported | Paired | Sequential | Inferred
    Feed(Line)
    Finish() []Span
}
```

| Tier | Mechanism | Accuracy |
|------|-----------|----------|
| **1 — Reported** | Read `tf_req_duration_ms` off the `"Received downstream response"` line. Start time is derived as `end − duration`. | Exact. The normal case for any modern provider. |
| **2 — Paired** | Correlate `"Sending request downstream"` to `"Received downstream response"` by `tf_req_id`. | Exact, and independent of the duration field. Also yields a true start timestamp rather than a derived one. |
| **3 — Sequential** | Pair within a single subsystem stream, assuming calls are serialised per plugin connection. | Correct only if that assumption holds. |
| **4 — Inferred** | No pairing. Attribute the wall-clock gap between consecutive lines to the most recently announced operation in that subsystem. | Approximate about *what* was slow; never wrong about *where in the file* time went. |

Tiers 1 and 2 are complementary rather than redundant: tier 1 is cheaper and
survives a missing request line, tier 2 gives a true observed start time. The
builder prefers tier 1 and uses tier 2 to supply start times where both are
present.

**A caveat on tier 2.** `"Received downstream response"` lines were found
readily in public issue logs; the matching `"Sending request downstream"` lines
were not present in the samples inspected. That is weak evidence — the samples
are excerpts pasted by bug reporters, not complete logs — but it means tier 1
should be treated as the workhorse and tier 2 as an enhancement, not the other
way round. Phase 1 settles this against real logs.

**The active tier is displayed in the UI header.** Durations produced by tier 4
are rendered with a `~` prefix. A number derived by gap attribution must never
be visually indistinguishable from one read off the wire.

### Address attribution

Provider spans carry a resource *type*; Terraform core knows the *address*.
Joining them is a separate, clearly-labelled inference step that runs after
span extraction.

Core's graph walk is tracked as a state machine over its own log lines:
`vertex "<address>"` lines open and close address context, and `[TRACE]
GRPCProvider: <RPCName>` marks core issuing a call. A provider span is then
attributed to an address when the RPC name and resource type match a core-side
context that was open across the span's time window.

Confidence is recorded per span and surfaced in the UI:

| Confidence | Condition |
|------------|-----------|
| **Exact** | Exactly one candidate address matched on RPC, type and time window |
| **Likely** | Multiple candidates, one materially better on time overlap |
| **Ambiguous** | Multiple equally plausible candidates; address shown with `?` |
| **Unknown** | No core-side context (e.g. `TF_LOG_CORE` not captured) |

**Attribution never changes a duration.** It only labels a span. If the whole
step fails — because core logs were not captured, or the log is provider-only —
every span is `Unknown` and the tool degrades to type-level ranking with no
loss of timing accuracy. This is why address ranking is a view, not a
foundation.

### Edge cases

- **Unclosed spans** (truncated log, killed plan): become open spans with
  duration measured to end-of-log, and are flagged as such.
- **Non-monotonic timestamps** across concurrent goroutines: clamped, never
  producing a negative duration. This is an expected condition and is counted
  in the diagnostic report rather than warned about per-occurrence.
- **Core-only logs.** A log may contain no provider RPC entries at all — the
  sampled gist log had zero, because it exercised the builtin `terraform_data`
  resource and launched no plugin process. Remote-execution backends produce
  the same shape locally. The tool must present this as a clear, explained
  state ("no provider RPC data in this log"), not as an empty ranked list that
  looks like a bug.

## `--diagnose`

A non-interactive mode that exists because of the blind-development constraint,
and the first thing built. It is the only channel through which facts about
real logs reach development, so it is a first-class feature rather than a
debugging aid.

It parses a log and prints **structural facts only**:

- Which tier the sniffer selected, and why.
- Counts of lines matching each known shape.
- Count of lines matching nothing.
- The most common unmatched line **templates**, with all values stripped:
  `<ts> [TRACE] provider.<name>: <msg> key1=<v> key2=<v>`.
- Which of the documented `tf_*` fields were observed, and how often.
- Whether core-side lines (`vertex "…"`, `GRPCProvider: …`) are present, which
  determines whether address attribution is possible at all.
- **The distribution of address-attribution confidence** — what proportion of
  spans resolve to Exact / Likely / Ambiguous / Unknown. This is the number
  that decides whether view `3` ships.
- Observed line count, mean line length, and parse throughput, to replace the
  capacity-planning guesses with measurements.
- The proportion of content that is **not** hclog — plan output and harness
  output — since an HCP raw run log is a mixed artefact.
- Whether **ANSI escape sequences** are present.

This output is what Dan pastes back to close the feedback loop without ever
transferring log content.

**Safety framing.** The tool minimises what appears in this output; it does not
certify it. Structured field *names* survive value stripping, and workplace
policy governs what may leave the machine. Dan reviews `--diagnose` output
before sharing it, every time.

**Sharing a redacted full log is explicitly out of scope.** TRACE logs contain
complete resource state including secret values. Reliable redaction of that is
not a guarantee this project will make.

## Views and interaction

Every duration these views render is a duration under logging — see *The
observer effect* above. The interface needs a standing caveat to that effect;
a timeline reading "116 seconds" otherwise invites a reader to optimise time
that will not exist without the log.

Layout: three panes. Facets left, list centre, detail right.

```
┌─ tfli ── plan.log ── 412MB · 8,441 spans · tier 1 (reported) ────────────────┐
│ PROVIDERS      │ DURATION  RESOURCE TYPE            │ SPAN DETAIL           │
│ ▸aws    181.3s │  42.1s  aws_instance          ◀   │ RPC   ReadResource    │
│  vault   54.0s │  18.7s  aws_subnet                │ Prov  aws             │
│  random   0.4s │   9.2s  aws_ami (data)            │ Start 04:11:02.104    │
│                │   6.1s  aws_security_group        │ Dur   42.09s          │
│ LEVELS         │   4.4s  aws_iam_role              │ Byte  148,203,991     │
│ ▸TRACE   2.0M  │   2.9s  aws_s3_bucket             │ Lines +34             │
├──────────────────────────────────────────────────────────────────────────────┤
│ ⇥ pane   ␣ toggle facet   ⏎ open log   / search   q quit                     │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Views

Views are groupings of the **middle pane**. The facet and detail panes persist,
so there is one filter state and many projections of it.

| Key | Middle pane |
|-----|-------------|
| `1` | **Providers** — rollup by `tf_provider_addr`, total time descending |
| `2` | **Resource types** — rollup by `tf_resource_type` / `tf_data_source_type`; observed, always exact |
| `3` | **Resource addresses** — rollup by inferred address; rows carry a confidence marker, and unattributed time is shown as an explicit `(unattributed)` row rather than silently dropped |
| `4` | **Calls** — individual spans, ranked by duration |
| `5` | **Timeline** — swimlanes (below) |
| `6` | **Raw log** — every line, facet-filtered |

View `3` is empty-but-honest when core logs were not captured: it shows a single
row explaining that address attribution needs `TF_LOG_CORE` output in the same
file, rather than appearing broken.

### Timeline (view 5)

Swimlanes, not a waterfall. Rows are execution lanes; spans are packed into
them; **idle time renders as visible blank space**.

Lanes are computed by greedy interval packing over each provider's spans, not
read from the log. There is no worker identifier in the log format, and none is
needed: packing depends only on start and end times, so the timeline behaves
identically at every extraction tier.

Below the lanes, a **stall annotation** identifies windows where concurrency
collapsed — for example `3 lanes idle 04:11:20–04:11:44 waiting on aws/1`.

This view exists to answer the one question a ranked list cannot: whether an
eight-minute plan was eight minutes of work, or ninety seconds of work and six
minutes of waiting. Those have different causes and different fixes.

**Added 2026-09-04:** lane packing and peak concurrency (phase 2's
`model.PackLanes` and `model.PeakConcurrency`, both consumed here) use
half-open interval semantics: a span occupies `[StartMs, EndMs)`. A
zero-duration span, or one whose start was clamped to zero because its
reported duration exceeded its offset from the log's first entry, has an
empty interval that overlaps nothing -- so it correctly contributes nothing
to peak concurrency, even nested inside spans that are genuinely running.
`PackLanes` still gives such a span its own lane, because this view needs a
row to draw every span in regardless of whether it overlaps anything. The
consequence: `PackLanes`' lane count can legitimately exceed
`PeakConcurrency`'s peak. That is not a bug for this view to reconcile or
surface as a discrepancy -- the two numbers answer different questions, and
`lanes >= peak` is inherent to the semantics above, not a signal that
something went wrong.

### Filtering and search

- **Facets** are the structured filter: providers, levels, RPC names, each with
  counts. `space` toggles. Filters are cumulative and apply to *every* view
  simultaneously, including the raw log.
- **Free-text search** is separate and behaves like `less`: `/pattern`, `n`/`N`
  to step matches, `Esc` cancels. It runs on a goroutine so it never blocks the
  render loop, reports progress, and supports cancellation — a regex miss over
  1GB is seconds of work that must be abortable. It searches the mapped bytes
  directly, respecting active facets.

### Keys

`⇥` cycle pane focus · `↑↓`/`jk` move · `space` toggle facet · `⏎` open selected
span in the raw log at its byte offset · `s` cycle sort · `/` search · `Esc`
clear filters · `?` help · `q` quit.

### Width degradation

The three-pane layout must never render as garbage in a small terminal:

- Below ~100 columns, the facet pane collapses to an overlay toggled with `f`.
- Below ~70 columns, the detail pane collapses too, leaving the middle list
  full-width.

These thresholds are locked by golden-file tests.

## Testing

TDD throughout. The design is testable without a terminal or a large file, by
two deliberate choices.

**Fixtures come from public GitHub issues.** This is the workaround for blind
development: bug reports against `hashicorp/terraform` and the major providers
routinely include real `TF_LOG=TRACE` excerpts, which are public data and safe
to commit. Every fixture file records its source issue URL in a header comment,
so any line can be traced back to its provenance and re-checked.

This materially de-risks the project — the parser is written against real
Terraform output from multiple provider versions and SDK generations rather
than against my reconstruction of it. Fixtures should deliberately span
protocol versions (5 and 6), both timestamp offset formats, core-only logs,
provider-only logs, and combined logs.

For size testing, `scripts/gen-fixture-log/`
synthesises a log of arbitrary size with a known-correct span set, giving
performance tests real input and correctness tests a ground truth. Per project
convention it has a name, help text, and documentation of when to use it.

**The TUI is tested as a pure function.** Bubbletea's `Update(msg) (Model, Cmd)`
is pure, so state transitions are plain table-driven tests — send a `KeyMsg`,
assert the model. `View()` returns a string, so rendering is covered by
golden-file tests at **70, 100 and 160 columns**, which locks the degradation
rules as tested behaviour. No PTY, no timing, no flakes, no library required.

| Layer | Proves |
|-------|--------|
| `hclog` parser | Lines → `Entry`, incl. multi-line entries, both offset formats, absent component |
| `SpanBuilder` ×4 | Each tier produces correct spans from its fixture |
| Tier sniffer | Selects the highest tier a given log supports |
| `diagnose` | Correct counts; value stripping leaks no values |
| `model` | Grouping, sorting, facet filtering, lane packing |
| `tui` | Key handling, pane focus, layout degradation |
| End-to-end | Real binary against a generated fixture log, no mocks |

Test output must be pristine. Truncated-log and non-monotonic-timestamp cases
are expected error paths; those tests capture and assert the specific
diagnostic rather than letting warnings leak into output.

## Phasing

Ordered so that the riskiest unknown is resolved by real-world feedback before
anything is built on top of it.

1. **Parser, sniffer, `--diagnose`. No TUI.** Built against public-issue
   fixtures, then run by Dan against real logs. Pins down the format, the tier
   distribution and the address-confidence distribution by evidence before
   anything is built on top of them.
2. **Span extraction and the model layer.** Rollups, facet filtering, lane
   packing, all as pure functions under test — plus `tfli --profile <log>`,
   a headless renderer over that model so the phase ships something runnable
   against real logs. See the revised out-of-scope entry. The cross-tier
   type join is the first deliverable: both builders populate
   `Span.ResourceType`, so it needs no address attribution and it is what
   answers "which resource type cost the most, and what did its RPCs cost".
3. **TUI: layout C with views 1, 2, 4 and 6** — planned in
   `docs/superpowers/plans/2026-09-04-phase-3-tui.md`. `s` (cycle sort) and
   `?` (help) from the key table are deliberately not in that plan: neither is
   named in this phase's scope line and both are cheap once the panes exist.
   Free-text search ships synchronous rather than as the cancellable
   goroutine described below, for the same reason the whole-file read replaced
   `mmap` — the design was sized against 1GB, and measured logs are 17-37MB — provider and type rollups, the
   ranked call list, and the raw log view including span-to-log jumping. This
   is the smallest thing that fully serves the primary use case.
4. **Timeline (view 5)** with swimlanes and stall annotation.
5. **Address attribution and view 3** — *conditional on phase 1's confidence
   numbers*. Cut it if ambiguity is high. Now conditional on a second thing
   as well: core addressing requires core at TRACE, which appears to suppress
   the `terraform.ui` stream, so shipping view 3 may mean the resource view
   and the address view can never appear in one session. See open question 8.
6. **`terraform plan -json` parser** as a second input format.

Phases 3 and 4 deliver the primary use case without depending on the one piece
of genuine inference in the design. That ordering is deliberate.

**Added 2026-09-04:** phase 2 ships three things with no caller yet, recorded
here so a later reviewer does not re-litigate them as YAGNI violations:
`model.PackLanes` and `ErrMixedTimelines` are consumed by phase 4's timeline
(view 5); `model.Filter` and `FacetsForSpans` are consumed by phase 3's facet
pane; and `TypeRow.UIMaxMs` is consumed by phase 3's `BY RESOURCE TYPE` view,
alongside the fields `--profile` already renders. Each is under test now
because pure functions over the model are the cheapest place to get them
right, even though the TUI that calls them does not exist yet.

## Out of scope

Deliberately excluded, recorded so they are not rediscovered as omissions:

- **Live tailing.** Logs come from CI artefacts, after the fact.
- ~~**Headless top-N and JSON/CSV export.**~~ **Revised 2026-09-04: headless
  top-N is now phase 2's deliverable**, as `tfli --profile <log>`. JSON/CSV
  export remains out of scope. Two things changed. Phase 2 as originally
  scoped shipped nothing runnable — a model layer behind a TUI that does not
  exist until phase 3 — which leaves it verifiable only against fixtures and
  not against the real logs that have corrected four assumptions in this
  document already. And the cross-tier type join now answers a live question
  (a 247 s `azuread_service_principal` total) that would otherwise wait for
  the TUI. `--diagnose` stays a structure report: profiling output belongs in
  its own mode rather than blurring the one whose purpose is safe disclosure.
- **Run comparison** (`--compare before.log after.log`). Agreed nice-to-have,
  not now.
- **Compressed input** (`.gz`, `.zst`). Likely wanted eventually since CI
  artefacts are often compressed; roughly twenty lines of decompressing reader.
  Nothing in this design should make it harder to add.
- **Redacted log sharing.** See `--diagnose` safety framing.

## Open questions

Every measurement dated 2026-09-04 below comes from three `--diagnose`
reports on real HCP Terraform runs — one structured-output, one
debug-toggle, one TRACE — all produced by `tfli` at commit `40c9a5f`, which
predates the nested-header peel. Re-running all three on `ce2b801` changed
almost nothing: the 2174-span figures are byte-identical, and the only
substantive difference is 1,152 message templates losing a masked `[DEBUG]`
prefix.

**A prediction recorded here was falsified, then explained.** This note
said the `INFO 901` count was Terraform's wrapper level and that a later
build would show most of those as DEBUG. It did not move. Grepping the
entries settled why: 19 of the 20 commonest shapes are

    [INFO] provider.terraform-provider-azuread_v3.9.0_x5: yyyy/mm/dd hh:ii:ss [DEBUG] ...

The nested timestamp is Go's standard `log` package format, not hclog's.
The peel scanned a single space-delimited token against hclog's layout, so a
two-token `2006/01/02 15:04:05` was never recognised and the entries kept
their wrapper level. The remaining shape,
`[INFO] provider: configuring client automatic mTLS`, is a genuine INFO line.

Nested Go-log timestamps are now peeled too, confined to nested headers:
accepting that format at the start of a line would promote continuation text
into an entry of its own. Confirmed on the same debug log at `d0c7afc`:

| level | debug `40c9a5f` | debug `d0c7afc` | TRACE `40c9a5f` | TRACE `d0c7afc` |
|---|---|---|---|---|
| UNKNOWN | 1314 | 1314 | 1 | 1 |
| TRACE | — | — | 73794 | 73794 |
| DEBUG | 11215 | 12050 | 11449 | 12284 |
| INFO | 901 | 66 | 901 | 66 |
| WARN | 115 | 115 | 114 | 114 |

Both logs move 835 entries from INFO to DEBUG and change nothing else. The
totals hold — 13,545 and 86,259 — so level attribution moved without
disturbing an entry boundary, and the identical split across two different
captures of the same workspace is what a deterministic provider should
produce. The 66 that remain are genuine Terraform INFO lines.

The `TRACE` count is untouched, which matters more than the correction
itself: the peel does not reach the protocol entries that tiers 1 and 2 are
built from, confirming those arrive un-nested. No figure other than that count is affected, and
the tier-1 result in particular is independent of the peel:
`40c9a5f` matched `"Received downstream response"` as a bare message prefix,
which is only possible because HCP delivers protocol lines un-nested.

1. **Whether an HCP Terraform raw run log actually contains provider RPC
   entries.** Execution location is now settled — runs happen on HCP Terraform
   runners, and TRACE is enabled there by workspace variable or the per-run
   debug toggle. Providers execute on the runner, so its log *should* contain
   `tf_req_duration_ms` entries. That is a strong expectation, not a verified
   fact: it depends on HCP capturing the Terraform process's stderr into the
   retrievable run log rather than discarding or separating it.

   **Result, 2026-09-04 (partial).** Dan ran a debug-enabled HCP run and
   `grep`ped the raw log: no `tf_req_duration_ms`, but `tf_req_id` is present.

   The cause is a logging *level*, not HCP discarding the data.
   `tf_req_duration_ms` is written in exactly two places in
   `terraform-plugin-go` — `tfprotov5/internal/tf5serverlogging/downstream_request.go`
   and its v6 twin — and every write goes through `logging.ProtocolTrace`,
   which is TRACE only. `"Sending request downstream"` is TRACE by the same
   route, so **tiers 1 and 2 both require TRACE**. `tf_req_id` survives at
   DEBUG because `RequestIdContext` sets it on three loggers, one of which is
   the provider's own `tflog` logger whose level the provider author chooses.

   **Result, 2026-09-04 (debug run report).** A real debug-enabled HCP run of
   13,545 entries: `response entries 0`, `request entries 0`,
   `response duration fields 0`. Not one protocol line survives at DEBUG. The
   1,270 `tf_req_id` occurrences are almost entirely one message — 1,260 of
   them are `"Value switched to prior value due to semantic equality logic"`,
   a terraform-plugin-framework DEBUG log — so the ID is present with nothing
   to correlate it against. `core vertex lines 0` and `core GRPC lines 0`
   likewise, so address attribution (open question 2, view 3) is TRACE-gated
   too.

   **The remaining check:** the proto subsystem takes its level from
   `TF_LOG_SDK_PROTO` and is built as its own `hclog.New` logger with its own
   `SetLevel`, so it does not inherit the root SDK level. Keep the debug
   toggle on, add `TF_LOG_SDK_PROTO=TRACE` as a workspace variable, and grep
   again. If that is empty, repeat with `TF_LOG=TRACE` to distinguish a
   downstream capture filter from the provider never emitting. Only if *both*
   are empty is the timing data genuinely unreachable from outside the runner.

   One false-negative trap was closed before that test runs. Provider
   protocol lines are provider stderr, and Terraform re-logs stderr through
   its own logger, so they can arrive with the provider's hclog header nested
   inside Terraform's message. The parser matched `"Received downstream
   response"` as a message prefix, which a nested header displaces, so a
   successful TRACE capture delivered that way would have reported
   `response entries 0` and read as a failure. Nested headers are now peeled;
   verified both ways against a nested TRACE line.
   **ANSWERED, 2026-09-04. The premise holds.** A TRACE-level HCP run:

   | | value |
   |---|---|
   | `response duration fields` | 2174 |
   | `request entries` | 2174 |
   | `correlated req ids` | 2174 |
   | selected tier | `reported` (tier 1) |
   | spans built | 2174 |
   | slowest span | 126,103 ms |
   | total span time | 1,739,913 ms over a 730 s wall clock |

   Every response pairs with a request, so tier 1 and tier 2 are both fully
   available and 29 minutes of summed RPC time sits inside a 12-minute plan.
   **Phase 2 is unblocked.**
2. **How reliable address correlation is under real concurrency.** The
   mechanism is confirmed to exist — core logs addresses, and logs its own side
   of each RPC. What is unknown is the *ambiguity rate* when many resources of
   the same type are planned in parallel, which is exactly the case in a large
   plan. If most spans come back `Ambiguous`, view `3` is not worth its
   complexity and should be cut. **`--diagnose` must report the confidence
   distribution**, and that number decides whether the view ships.
3. **Whether tier 2 pairing is available. ANSWERED, 2026-09-04: yes.**
   Absent from the public samples because the line is emitted via
   `logging.ProtocolTrace` and those samples were captured below TRACE. A
   real TRACE log carries 2174 `"Sending request downstream"` entries against
   2174 responses, and all 2174 correlate by `tf_req_id`. Tier 2 is a real
   fallback, not a hypothesis.
4. **Which tier real logs actually support. ANSWERED, 2026-09-04: tier 1,
   entirely.** All 2174 spans in the TRACE log came from
   `tf_req_duration_ms`; the sniffer selected `reported` and never fell back.
   The four providers exercised — azurerm 4.81, tfe 0.59, azuread 3.9,
   github 6.3.1 — are all built on `terraform-plugin-go`, so this measures a
   real stack rather than the general case. A provider outside that family
   would still degrade to a lower tier, which is why the tiers stay.
5. **`TF_LOG_CORE` versus `TF_LOG_PROVIDER` splitting. ANSWERED,
   2026-09-04: both land in one file.** The TRACE log carries 9,336 core
   vertex lines and 6,537 core GRPC lines alongside 34,356 provider entries,
   so core's addresses and the provider's RPCs are correlatable in principle.
   View 3 is reachable in the environment that matters; whether it is
   *reliable* is open question 2, still unmeasured because tier 3 is not
   built.

8. **What a TRACE capture costs in structured output. CONFIRMED,
   2026-09-04: the whole of it.** Checked directly against the raw logs:
   normal and debug runs carry `terraform.ui` markers, the TRACE run carries
   none. The mechanism is not established — only the effect.

   | capture | `terraform.ui` | provider RPC |
   |---|---|---|
   | normal | yes | no |
   | debug toggle | yes | no (TRACE only) |
   | `TF_LOG=TRACE` | **no** | yes |

   So no single capture yet yields both views, and they answer different
   questions: which *resources* were slow, versus which *calls* were slow.

   **`TF_LOG=DEBUG` plus `TF_LOG_SDK_PROTO=TRACE` was tried and produced
   nothing:** 17,496 KB against the plain debug run's 17,436 KB, and zero
   TRACE entries (UNKNOWN 1315, DEBUG 12283, INFO 66, WARN 115 — a delta of
   +233 DEBUG, which reads as run-to-run variation).

   **There are two gates, and that variable opens only one.**
   `TF_LOG_SDK_PROTO` governs what the *provider* writes. Terraform then
   re-emits plugin stderr through a logger of its own, whose level
   `internal/logging/logging.go` resolves as:

       providerEnvLevel := strings.ToUpper(os.Getenv(envLogProvider)) // TF_LOG_PROVIDER
       if providerEnvLevel == "" {
           providerEnvLevel = strings.ToUpper(os.Getenv(envLog))      // TF_LOG
       }

   With `TF_LOG=DEBUG` and `TF_LOG_PROVIDER` unset, that logger sits at
   DEBUG and discards the provider's TRACE lines on the way through. The
   provider very likely emitted them; Terraform dropped them.

   **The combination that addresses both gates is `TF_LOG=DEBUG` plus
   `TF_LOG_PROVIDER=TRACE` plus `TF_LOG_SDK_PROTO=TRACE`.** Core keeps
   falling back to `TF_LOG`, so it stays at DEBUG — which is the condition
   under which structured output has always survived. Note that Terraform's
   unsuffixed `TF_LOG_PROVIDER` and `terraform-plugin-go`'s
   `TF_LOG_PROVIDER_<NAME>` are different variables with confusingly similar
   names; the unsuffixed one is the gate that matters here.

   A risk flagged before that run did not materialise. `log wall-clock` is
   taken from the hclog stream whenever hclog timestamps exist, and a
   synthetic test showed it ignoring the UI-hook stream entirely. On the real
   combined capture it reads 522.3 s, which is right: the hclog stream
   brackets the whole Terraform process, while `terraform.ui` covers only the
   plan phase within it. The synthetic exaggerated the gap by separating the
   two streams artificially. No fix needed.

   **The trade-off is three-way, not two-way.** Terraform core logs through
   the global logger, whose level is `TF_LOG` (`globalLogLevel`), so
   `TF_LOG=TRACE` raises core as well as providers while
   `TF_LOG=DEBUG` + `TF_LOG_PROVIDER=TRACE` does not. Core at TRACE is what
   produces the 9,336 vertex lines and 6,537 GRPC lines — the raw material
   for attributing an RPC back to a resource address, which is view 3 and
   open question 2.

   | capture | `terraform.ui` | provider RPC | core addressing |
   |---|---|---|---|
   | debug toggle | yes (measured) | no (measured) | no (measured) |
   | `TF_LOG=TRACE` | no (measured) | yes (measured) | yes (measured) |
   | `DEBUG` + `TF_LOG_PROVIDER`/`TF_LOG_SDK_PROTO` at TRACE | **yes (measured)** | **yes (measured)** | **no (measured)** |

   **CONFIRMED, 2026-09-04.** The third row was run: 30 MB, 38,379 entries,
   `structured lines 1325`, `TRACE 24587`, `response duration fields 2174`,
   `correlated req ids 2174`, `spans built 2174`, `UI-hook spans built 264`,
   and `core vertex lines 0` / `core GRPC lines 0`. Core stayed at DEBUG and
   the `terraform.ui` stream survived, so **core at TRACE is what suppresses
   structured output**. This is the first log to produce spans from both
   builders at once.

   **This is the capture to standardise on.** It is the only one that answers
   both "which resources were slow" and "which calls were slow" from a single
   run, and at 30 MB it sits between the 17 MB debug and 37 MB full-TRACE
   captures. Only address attribution is out of reach, and open question 2
   never established that view 3 was worth its complexity in the first
   place.

   **The two tiers join on resource type, which weakens the case for view
   3.** Both builders populate `Span.ResourceType` — the reported builder
   from `tf_resource_type` (18,773 occurrences in this capture), the UI-hook
   builder from `hook.resource.resource_type`. So a rollup can put the
   UI-hook answer and the RPC answer side by side per type without any
   address attribution at all: *these reads took 247 s in total, and here are
   the RPC calls of that type and what each cost*. Address attribution is
   only needed to distinguish two instances **of the same type**, which is a
   narrower question than the design originally assumed and one this capture
   cannot answer anyway. Phase 2 should build the type-level join first and
   let it demonstrate whether the address-level view is still wanted.

   **Per-resource timings and address attribution are mutually exclusive.** Provider RPC timing can be added to
   either, but no capture yields all three. That is a hard constraint on the
   TUI: view 3 and the UI-hook resource view can never be rendered from the
   same log however the interface is arranged, and it gives phase 5 a second
   reason to be cut beyond the ambiguity rate it was already conditional on.

   **A precedence trap, worth recording because it is inverted between the
   two.** `globalLogLevel` reads `TF_LOG` first and falls back to
   `TF_LOG_CORE`; `providerLogLevel` reads `TF_LOG_PROVIDER` first and falls
   back to `TF_LOG`. So `TF_LOG=DEBUG TF_LOG_CORE=TRACE` does **not** raise
   core — `TF_LOG` wins. Holding providers down while raising core requires
   leaving `TF_LOG` unset and setting both subsystem variables.

   **If it does not work,** the TUI shows whichever tier its log supports and
   the capture is chosen by the question being asked. It must not merge two
   logs to synthesise both views: they come from different runs, so resource
   timings from one would be correlated against RPC timings from another —
   plausible-looking and wrong. Rejected explicitly so it is not
   rediscovered as an idea.
6. **Real-world line rate.** ~~The 102 bytes/line figure comes from one small
   public log and is used for capacity planning.~~ **Answered, 2026-09-04:**
   828 bytes/line mean on a real 17.9 MB debug log (21,557 physical lines,
   13,545 logical entries), and 758 bytes/line on a structured-output log.
   Eight times the estimate. Parse throughput measured at 81 MB/s on the debug
   log; the streaming design absorbs the difference, so this revises capacity
   expectations rather than the architecture.

7. **Resolution of structured-output timings. Answered, 2026-09-04.**
   `elapsed_seconds` is quantised to whole seconds and carries up to a second
   of error. `internal/command/views/hook_json.go` rounds *both* endpoints
   independently before subtracting:

       start:   h.timeNow().Round(time.Second)
       elapsed: h.timeNow().Round(time.Second).Sub(progress.start)

   So a 0.6 s read reports 0 s or 1 s depending only on where it falls against
   the second boundary. Confirmed in the field: every one of 237 spans in a
   real structured log is a whole-second multiple, and 10 of 266 in the debug
   log report 0 ms.

   **Consequence for the design.** The `ui-reported` tier supports aggregate
   views — by type, by provider — where independent rounding errors are
   uncorrelated and wash out across many resources. It does not support
   ranking individual resources against each other, which is what
   `SLOWEST RESOURCES` currently invites. Per-resource ranking needs the RPC
   tiers, and therefore TRACE.
