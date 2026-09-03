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
