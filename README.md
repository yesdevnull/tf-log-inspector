# tf-log-inspector (`tfli`)

Find where a slow Terraform plan spent its time.

## Install

Requires Go 1.25 or later. No third-party dependencies.

    go install github.com/yesdevnull/tf-log-inspector/cmd/tfli@latest

## Getting a log

`tfli` reads two kinds of timing. One capture gives you both.

### The recommended capture

On the run, enable **Debug Logging**, and set these workspace variables:

    TF_LOG_PROVIDER=TRACE
    TF_LOG_SDK_PROTO=TRACE

Then download the raw log. This yields per-resource timings from Terraform's
own UI hooks *and* per-RPC timings from the providers, in one file. Measured
on a real HCP run: 30 MB, 2,174 correlated RPC spans and 264 resource spans.

Do not reach for `TF_LOG=TRACE` instead. It raises Terraform core as well,
and a core-TRACE run does not contain the `terraform.ui` stream — you gain
core's graph output and lose every per-resource timing. The sections below
explain why.

### Per-resource timing, from a normal run

A run's raw log already contains Terraform's structured output
(`terraform.ui` JSON), which carries an `elapsed_seconds` for each resource.
Nothing needs enabling — download the run's raw log as it is.

### Per-RPC timing, from a TRACE run

Finer-grained timing comes from provider RPC entries. These are emitted only
at TRACE: `terraform-plugin-go` logs `tf_req_duration_ms` and
`"Sending request downstream"` through `logging.ProtocolTrace`, so a
DEBUG-level log contains neither.

HCP Terraform's **Debug Logging** toggle alone is therefore not sufficient. It
yields `tf_req_id` — set on the provider's own logger — but no durations.

There are two gates between the provider and the log file, and both must be
open. `TF_LOG_SDK_PROTO` governs what the provider writes; Terraform then
re-emits plugin output through a logger whose level comes from
`TF_LOG_PROVIDER`, falling back to `TF_LOG`. Raising only the first is not
enough — measured: it produces a log byte-for-byte comparable to a plain
debug run, with no TRACE entries at all.

Set both as workspace variables:

    TF_LOG_PROVIDER=TRACE
    TF_LOG_SDK_PROTO=TRACE

`TF_LOG=TRACE` alone also works and is simpler, but it raises Terraform core
too, and a core-TRACE run has so far never contained the `terraform.ui`
stream.

A debug run's raw log contains **both**: the `terraform.ui` JSON and the
debug text, interleaved. One debug run therefore feeds every tier `tfli`
supports, so there is no need to choose between the two captures.

### A caveat on structured-output resolution

Terraform rounds both ends of a resource's timing to the nearest second
before subtracting them (`hook_json.go`: `h.timeNow().Round(time.Second)`),
so `elapsed_seconds` is always a whole number and each figure carries up to
a second of error. Type and provider rollups over many resources stay
meaningful; ranking two individual resources a second apart does not.

For a local plan:

    TF_LOG=TRACE TF_LOG_PATH=plan.log terraform plan

## Usage

    tfli plan.log
    tfli --diagnose plan.log
    tfli --diagnose -o report.txt plan.log
    tfli --profile plan.log
    tfli --profile -o profile.txt plan.log

With no mode flag, `tfli` opens the full-screen interface: facet checkboxes
on the left, a ranked table in the centre, the selected call's detail on the
right, and the raw log with `/` search. `q` quits. Read
[What each mode discloses](#what-each-mode-discloses) before you share a
session — the interface shows more of your log than either report does.

`-o` writes a report, so it applies to `--diagnose` and `--profile` only;
passing it without a mode flag is an error rather than a file that never
appears.

`--diagnose` reports the log's structure: size, levels, which extraction tier
applies, which fields are present, and the most common message shapes.

### Durations are measured under logging

Debug logging is not free, and on the runs measured here it is not close to
free: the same workspace planned in 24.1s with no logging enabled and 522.2s
with debug plus provider TRACE. Terraform re-logs each line of a provider's
stderr through its own logger, so a provider that dumps HTTP bodies at DEBUG
pays that cost per line — in one measured case a single API response
accounted for 49% of a 30MB log.

Rankings within a log therefore hold: every span paid the same tax, so the
slowest call really was the slowest. Absolute durations do not transfer to a
run without logging, and comparisons between a chatty provider and a quiet one
are the least reliable reading.

`--profile` reports where the plan spent its time: resource types and
providers ranked by total duration, the slowest individual calls and
resources, and concurrency. Use it to find the slow resource, not to share
the result.

### What each mode discloses

`--diagnose`'s field **keys** are reported verbatim, restricted to an
identifier charset so log content cannot pose as a key. Field **values** are
never reported. Message shapes are reported with quoted strings, paths,
resource addresses and long identifiers masked — a heuristic, not a
guarantee. Review a diagnose report before sharing it.

`--profile`'s output contains **real, unmasked resource addresses** — that is
the point of a profiler, which is useless if it cannot say which resource was
slow. It is for your own eyes on your own machine, and unlike `--diagnose`,
it is not safe to share.

The full-screen interface (`tfli plan.log`) discloses **more than
`--profile`**, and it sits behind the easiest invocation. Alongside the same
real resource addresses, provider addresses and resource types, its raw log
view renders your log's own lines **verbatim** — including whatever a
provider logged at DEBUG, such as request and response bodies, headers and
identifiers. Nothing in it is masked, and nothing is masked on the way to
your screen.

Treat a `tfli` session the way you would treat the log file itself: do not
screen-share, record or screenshot one against a log you would not hand over
whole.
