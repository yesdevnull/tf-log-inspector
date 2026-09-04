# tf-log-inspector (`tfli`)

Find where a slow Terraform plan spent its time.

## Install

Requires Go 1.25 or later. No third-party dependencies.

    go install github.com/yesdevnull/tf-log-inspector/cmd/tfli@latest

## Getting a log

`tfli` reads two kinds of timing, which need different capture settings.

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
yields `tf_req_id` — set on the provider's own logger — but no durations. Set
a workspace variable to raise the protocol subsystem specifically:

    TF_LOG_SDK_PROTO=TRACE

That subsystem gets its own logger and its own level, so this does not require
turning all of Terraform up to TRACE. Whether HCP's log capture passes the
resulting lines through is not yet confirmed; if the log comes back without
`tf_req_duration_ms`, try `TF_LOG=TRACE` before concluding the data is
unreachable.

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
