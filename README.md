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

## Development checks

GitHub CI runs on pull requests and pushes to `main`, using the Go version
declared in `go.mod`. All jobs run on Ubuntu. CI runs the full test suite with
race detection, checks formatting and module consistency, verifies dependency
checksums, and runs golangci-lint 2.13.2 with the repository configuration.

Run the same checks locally from the repository root:

    gofmt -d .
    go mod tidy -diff
    go mod verify
    golangci-lint run --timeout=5m
    go test -race -count=1 ./...
    go build ./...

Formatting is clean when `gofmt -d .` prints nothing; CI fails if it finds a
diff. Fix formatting with `gofmt -w` on the affected files. The linter includes
`govet`, `staticcheck`, `errcheck`, `ineffassign` and `unused`; optional
Staticcheck quick-fix style suggestions are disabled.

CI also cross-compiles `tfli` for Linux and macOS on amd64 and arm64 with cgo
disabled. Each workflow artefact contains a `tfli.tar.gz` archive that preserves
the binary's executable permission. Artefacts are retained for seven days.
These are development builds; version-tag publishing is not configured.

## Usage

    tfli plan.log
    tfli --diagnose plan.log
    tfli --diagnose -o report.txt plan.log
    tfli --profile plan.log
    tfli --profile -o profile.txt plan.log
    tfli --scrub -o sanitised.log plan.log
    tfli --scrub --scrub-values private-values.txt -o sanitised.log plan.log

With no mode flag, `tfli` opens the full-screen interface: facet checkboxes
on the left, a ranked table in the centre, the selected call's detail on the
right, and the raw log with `/` search. `q` quits. Read
[What each mode discloses](#what-each-mode-discloses) before you share a
session — the interface shows more of your log than either report does.

The top tab bar highlights the active view; its number keys switch views.
`Tab` moves the arrow and accented panel border to the pane receiving keyboard
input. The filter title counts hidden values. Raw Log uses the available width
without a Detail panel and reports its position in the status bar.

`?` opens the complete key guide. Scroll it with arrows, `j`/`k` or
`PgUp`/`PgDn`; the timing explanation follows the shortcuts. The main views
keep a compact timing qualification beside their action hints.

In Raw Log, `←`/`→` (or `h`/`l`) scroll horizontally one display column at a
time. While searching with `/`, use `←`/`→`, `Home`/`End`, `Backspace` and `Delete`
to edit the query. Long queries scroll with the cursor. `Enter` searches,
`Esc` cancels, and `n`/`N` repeat the submitted search forwards/backwards.

With the Raw Log list focused, `r` opens the complete provider JSON response
containing the entry at the top of the pane, joining timestamped fragments
even when different providers are interleaved. The centre pane shows the
fragment count, decoded multiline `@message` text and indented JSON. Terminal
controls are displayed as visible escapes. Use arrows or `h`/`j`/`k`/`l` to
scroll, `PgUp`/`PgDn` to page, `/` to search the displayed text and `n`/`N` for
the next/previous matching line. `Esc` or `r` restores the exact raw position.

Reconstruction runs lazily and leaves source bytes and entry identities intact.
Malformed, incomplete or observably ambiguous provider JSON anywhere in the file
makes reconstructed responses unavailable; ordinary loading and Raw Log still
work. Ordered fragments from each exact provider component are required:
unrelated same-component text inserted inside an unfinished JSON string cannot
always be distinguished from payload. Scrubbing uses this same reconstruction
and refuses to publish output on reconstruction failure.

Key `5` swaps the centre table for a timeline: one bar per lane of concurrent
work, shaded by how busy each column of it was, with the idle time between
calls left as the blank space it is. Beneath it sit how much of the run had
anything running at all, and the longest waits. `↑`/`↓` move between lanes,
and `←`/`→` (or `h`/`l`) step along the selected one call by call — the
detail pane follows the step, and `⏎` opens that call's own log lines,
scoped to it by its `tf_req_id`; `\` returns to the whole log.

`-o` writes a report for `--diagnose` and `--profile`, and is required for
`--scrub`. Passing it without a mode flag is an error. Select only one mode.

`--diagnose` reports the log's structure: size, levels, which extraction tier
applies, which fields are present, and the most common message shapes.

### Scrubbing a log

`--scrub` writes a candidate log for sharing, replacing identifying values
with consistent fake values throughout the file. Repeated values remain
linkable and distinct resources remain distinct. The source stays unchanged;
the output must be a new path, including when an existing path is a symbolic
or hard link. Output is created exclusively with owner-only read/write
permissions (`0600`) after transformation and validation succeed. Write or
close failures remove the newly created partial output; a failed removal is
reported as an error.

Detection covers hclog fields, Terraform UI JSON, plan text and supported
HTTP/JSON bodies, including escaped JSON strings:

- Terraform module/resource labels and string instance keys; name, user,
  organisation, workspace and project fields. Application display names,
  including `app_displayname`, share aliases with consent descriptions,
  service-principal URLs and encoded HTTP request queries.
- Consent descriptions discover SP names from the complete sentence
  `Allow the application to access <NAME> on behalf of the signed in user`
  (with an optional final full stop). Other `adminConsentDescription` and
  `userConsentDescription` values receive opaque whole-field replacements;
  identical descriptions share an alias. Snake-case field names are supported.
- GUIDs and identifying ID fields, including opaque request/resource IDs
  and Terraform UI `hook.id_value`. Azure AD IDs composed of three
  hyphen-separated GUIDs retain their component structure and share each
  GUID's replacement with standalone occurrences.
- Email addresses, IPv4/IPv6 literals, URL hosts and hostname fields,
  including `publisherDomain` and bare Azure `*.onmicrosoft.com` tenant domains.
- AWS ARNs and account IDs, Azure resource-ID paths and GCP resource paths.
- HCP Terraform OIDC subjects in `organization:…:project:…:workspace:…:run_phase:…`
  format, sharing organisation/project/workspace aliases while preserving run
  phases and trust-policy wildcards.
- Recognisable GitHub tokens (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`,
  `github_pat_`, including dotted installation tokens) and HCP Terraform
  `*.atlasv1.*` API tokens, including occurrences in prose.
- Azure Key Vault/Managed HSM and Blob, DFS, File, Queue and Table endpoints,
  including recognised private-link hosts and bare endpoint hostnames.
  Fixed Azure service suffixes and Key Vault `secrets`/`keys`/`certificates`
  paths remain recognisable; account/vault names, object names and versions
  are replaced consistently. Storage SAS signatures, policy identifiers
    and IP restrictions are scrubbed, as are delegation identities and Table
    partition/row keys. Custom domains use generic URL handling.
- Recognised absolute POSIX and Windows paths, preserving separators and
  file extensions.
- Credential fields and HTTP headers, including passwords, tokens, API/access
  keys, Authorization and cookies, plus PEM private-key payloads. Whole
  credentials receive one opaque alias even when also used in another field.
- JSON HTTP request bodies in `http.request.body` fields are inspected for
  credential fields, including quoted, unquoted and multiline bodies. Ordinary
  request `value` fields are preserved unless otherwise identified as sensitive.
- JSON HTTP response bodies treat scalar `value` fields as secrets. Arrays and
  objects retain their structure and are inspected recursively; null and empty
  values remain unchanged. This includes escaped and unquoted
  `http.response.body` dumps and JSON objects or arrays logged directly after
  a provider prefix. Timestamped provider JSON fragments are reassembled by
  exact provider component, including when different providers interleave.
  Rewritten values can move into an earlier fragment; physical record order
  and headers are preserved. Incomplete, malformed or observably ambiguous
  provider messages and JSON fragmented across HTTP chunks are rejected
  without output. Fragments from simultaneous messages within the same
  provider cannot be distinguished when the log supplies no message identity;
  such unobservable interleaving is unsupported.

For additional identifiers, use `--scrub-values private-values.txt` with one
literal UTF-8 value per line. Empty lines are ignored and line terminators
are removed; other whitespace is significant. Controls and invalid UTF-8
are rejected. There is no comment, regex or replacement syntax. This option
applies only to `--scrub`. Mappings stay in memory and are not exported or
stable across separate files.

The scrubber preserves line order/endings, timestamps, severity, durations,
counts, RPC names, resource types and lifecycle actions. Genuine hclog
`tf_req_id` metadata stays unchanged so request scopes survive; other ID
fields and body fields named `tf_req_id` are scrubbed. Terraform address
grammar and numeric indices remain intact. Built-in components and the
recognised public providers from HashiCorp (`aws`, `azurerm`, `azuread`,
`google`, `local`, `null`, `random`, `time`, `tls`) and `integrations/github`
retain their grouping labels; unknown provider identities are scrubbed.

Conflicts with preserved syntax or metadata, including changes to fields
visible through the inspector's header parsing window, reject the input
before output creation. Verifiable chunked HTTP bodies have their chunk sizes
updated after replacement. Provider dumps with already-invalid wire sizes,
such as pretty-printed JSON bodies, are treated as log text. Invalid UTF-8 and unsupported binary input also
fail. Malformed structured/quoted input is handled as text where possible
and counted. Stderr reports aggregate replacement and unsupported-input
counts, plus a review reminder; stdout contains no log content.

**Review the output before sharing it.** This is heuristic pseudonymisation:
unknown natural-language names, unrecognised provider formats and
encoded/compressed payloads are outside automatic detection. Replacement
counts do not measure completeness. Open or profile the candidate locally
to check its usefulness:

    tfli sanitised.log
    tfli --profile sanitised.log

### Durations are measured under logging

Debug logging is not free, and on the runs measured here it is not close to
free: the same workspace planned in 24.1s with no logging enabled and 522.2s
with debug plus provider TRACE. Terraform re-logs each line of a provider's
stderr through its own logger, so a provider that dumps HTTP bodies at DEBUG
pays that cost per line — in one measured case a single API response
accounted for 49% of a 30MB log.

Rankings within a log are approximate. A call that logs heavily is inflated
more than one that waits, so the logging overhead can change their order.
Absolute durations do not transfer to a run without logging, and comparisons
between a chatty provider and a quiet one are particularly unreliable.

`--profile` reports where the plan spent its time: resource types and
providers ranked by total duration, the slowest individual calls and
resources, and concurrency. Use it to find the slow resource, not to share
the result.

### What each mode discloses

`--scrub` writes the complete transformed log, including anything its
detectors did not recognise and preserved metadata such as `tf_req_id`.
Review the candidate using the coverage and limitations above before
sharing it or showing it in the interface.

The scrub summary reports the first 10 unsupported structured or quoted inputs
in source order, with a reason and a location in the **original input file**.
Lines and character columns are one-based; columns count Unicode characters,
not bytes or expanded tab widths. For decoded strings and reconstructed provider
responses, locations map back to the original physical text. Locations identify
the start of the unsupported structure or quote, not necessarily the exact
syntax error. The summary never prints captured content. These are parsing
limitations, not a count of confirmed PII leaks; any remaining instances are
reported as an omitted count.

JSON warnings apply to standalone structured records and identified bodies,
not ordinary string fields that happen to start with `[` or `{`. Valid JSON
embedded in strings is still inspected, and malformed quoted strings still
produce warnings. Malformed JSON hidden in an unmarked text field may therefore
receive no JSON warning; sensitive-value detection still runs on that text.

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
