# Consistent log scrubbing

## Purpose and agreed direction

Add a local CLI mode that produces a shareable candidate log with identifying
values replaced by consistent fake values. The output must remain useful in
the inspector: distinct resources remain distinct, repeated values remain
linkable, and call timings and resource attribution survive.

Dan approved a two-pass scrubber on 2026-09-08. Preserve `tf_req_id` fields;
there is no blanket exemption for other `tf_*_id` fields or GUIDs. This is
heuristic pseudonymisation, not a guarantee that every identifier is removed.

## User interface

```text
tfli --scrub -o sanitised.log original.log
tfli --scrub --scrub-values private-values.txt -o sanitised.log original.log
```

- Require `-o` for scrubbing and reject combinations with `--diagnose` or
  `--profile`. Reject `--scrub-values` outside scrub mode.
- Accept a UTF-8 values file containing one additional literal identifying
  value per line. Remove line terminators, ignore empty lines, preserve other
  whitespace, and reject embedded control characters. There is no comment
  syntax, regex syntax, or user-supplied replacement syntax.
- Keep the input intact. Refuse an existing output path, including symbolic
  links, rather than overwrite it. Write a new output with mode `0600`.
- Complete discovery and transformation before creating the output. Create
  the output exclusively; remove a newly created partial output on a write
  or close failure, reporting cleanup failure if removal also fails.
- Print aggregate replacement counts by category and a short reminder to
  review the result to stderr. Do not print source values, replacement pairs,
  log excerpts, or values-file contents. Scrub mode emits no log to stdout.

## Architecture and data flow

Add `internal/scrub`, independent of the TUI and CLI flags. Its input is the
original bytes and optional explicit values; its output is transformed bytes,
aggregate counts, or an error. File ownership and argument handling remain in
`cmd/tfli`. No new dependencies, external services, or model downloads.

Read the input once, then make two logical passes over the same immutable
buffer. This follows the existing model's whole-file loading approach and
avoids inconsistent passes if the source file changes on disk. Memory grows
with input, output, and the number of distinct candidates; do not silently
stop discovering values after an arbitrary count.

1. Discover candidates in structured fields, Terraform resource addresses,
   recognised identifier patterns, and the explicit values list. Record
   structural spans that must be preserved. Decode supported quoted strings
   for discovery, including escaped JSON within a log field.
2. Allocate one mapping for the whole file and apply replacements to original
   occurrences. Re-escape replacements for their enclosing syntax. Never
   search already-replaced output, which could cause cascading substitutions.

Before returning transformed bytes, validate parser-visible metadata against
the original buffer as described under Inspector invariants. These validation
scans are additional to the two discovery/replacement passes; they do not
reread the source file or require changes to the scanner or model.

Use the existing log-format helpers where they supply the necessary context.
Their `Fields` values do not retain source offsets, so rewriting needs a
small lexical layer in the scrub package. Do not change the scanner or model
just to support rewriting.

## Discovery coverage

The first implementation covers hclog key/value fields, Terraform UI JSON,
interleaved plan text, and HTTP body continuations. JSON string traversal must
retain enclosing field context; a key name inside arbitrary message text is
not automatically a trusted metadata field.

| Category | Discovery and replacement |
| --- | --- |
| GUIDs | Recognise canonical hyphenated GUIDs, including surrounding braces and case variations. Use the context-dependent identity rules below before assigning fresh valid GUIDs. Preserve wrapper and letter case where applicable; whole-secret classification overrides this format rule. |
| Names | Discover Terraform module/resource labels and string instance keys; identifying field values such as names, usernames, display names, organisations, workspaces and projects; explicit values. Generate valid identifier aliases such as `name_0001`. |
| Email | Replace recognised complete addresses with distinct aliases under `example.invalid`. |
| Network | Recognise IPv4/IPv6 literals, URL hosts and hostname fields. Generate syntactically valid, distinct fake addresses or hostnames; retain URL scheme and port. |
| Cloud identifiers | Recognise AWS ARN structure and account IDs, Azure resource-ID paths, and GCP project/resource paths. Replace identifying segments using the shared mapping while retaining service/resource-type structure. |
| Local paths | Replace identifying segments in recognised absolute POSIX and Windows paths; retain separators and file extensions. |
| Credentials | Recognise sensitive fields and HTTP headers, including password, secret, token, API/access keys, Authorization and cookies. Replace complete sensitive values with distinct opaque aliases; detect and replace PEM private-key payloads as a whole. |

### Initial field and context rules

Match parsed keys, never substrings of prose. For detection, split keys on
`_`, `-`, `.` and camel-case/acronym boundaries, then compare ASCII lowercase
words (`tfResourceID` becomes `tf/resource/id`). The spellings below denote
these word sequences. Also recognise the compact spellings `username`,
`hostname`, `requestid`, `correlationid`, `apikey` and `accesskey`. This
normalisation is for detection only: preserve original key spelling and
require exact parser-recognised spelling for metadata exemptions.

| Context | Matching keys | Treatment |
| --- | --- | --- |
| Genuine hclog metadata, including field continuations outside bodies | Exact `tf_req_id` | Preserve verbatim, regardless of whether its value is a GUID. No other ID field is exempt. |
| Parsed hclog fields, JSON object members and plan assignments | `id`, `uuid`, `guid`, `requestid`, `correlationid`; any key ending in a separated/camel-case `id`, `uuid` or `guid` word | Scrub nonempty scalar identifiers, including opaque non-GUID values and non-GUID `tf_*_id` values. Use GUID/composite rules when applicable, otherwise a distinct opaque ID alias. |
| Terraform UI lifecycle `hook` object | Exact `id_value` | Scrub the resource ID even when it is not a GUID; this rule also covers its repetitions in `@message` and plan text. |
| Terraform UI lifecycle `hook` object | Exact `id_key` | Preserve the descriptive attribute key, such as `id`, rather than treat it as a resource ID. This is not an exemption for the corresponding `id_value`. |
| Parsed fields, JSON members and plan assignments | `name`, `user`, `username`, `user_name`, `display_name`, `first_name`, `last_name`, `full_name`, `organisation`, `organization`, `workspace`, `project`, `tenant`, `account`; keys ending in a `name` word | Discover names from nonempty scalar values. Terraform address labels and string indices also follow the address rules independently of this table. |
| Parsed fields and JSON members | `email`, `email_address`, `host`, `hostname`, `host_name`, `ip`, `ip_address`, `url`, `uri`, `endpoint` | Discover the corresponding email/network/URL values. Pattern discovery still covers these categories outside named fields. |
| Parsed fields, JSON members and plan assignments | `password`, `passwd`, `pwd`, `secret`, `token`, `api_key`, `apikey`, `access_key`, `accesskey`, `private_key`, `client_secret`, `authorization`, `proxy_authorization`, `cookie`, `cookies`, `set_cookie`; keys ending in the word sequences `password`, `secret`, `token`, `api_key`, `access_key`, `private_key` or `access_key_id` | Mark nonempty scalar values as whole secrets, taking precedence over ID/name/composite classification. |
| HTTP header lines and `http.request.header.*` / `http.response.header.*` fields | `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-API-Key` | Mark the complete header value as a secret; do not preserve a credential-bearing suffix. Header-name matching is case-insensitive. |
| HTTP header lines and the same dotted header fields | `X-Request-ID`, `X-Correlation-ID`, `X-Amzn-RequestId`, `X-Ms-Request-ID` | Scrub as opaque IDs, including non-GUID values. These are not Terraform's exempt `tf_req_id`. |

These generic rules also apply within supported decoded HTTP/JSON bodies;
body fields named `tf_req_id` are ordinary IDs, not genuine hclog metadata.
The lifecycle exceptions apply only to a recognised Terraform UI envelope,
not an arbitrary body's `hook` object. Nulls and empty values identify
nothing and remain unchanged. Scalar arrays under a matching key inherit
its classification; object members are traversed using their own keys.

Preserve numeric/boolean timing and count fields, and the structural metadata
listed under Inspector invariants, in their actual metadata positions. The
generic table must not reclassify those positions. Unknown fields have no
name-based detection beyond this table; pattern discovery and propagation
of already-discovered values still apply. Ordinary diagnostic prose stays
readable except for those patterns and propagated values.

Once discovered, a value is replaced even in earlier free-text occurrences.
Names match complete identifier tokens rather than arbitrary substrings:
discovering `ann` must not alter `planning`. Structured composite values
(addresses, URLs, paths and cloud IDs) are rebuilt from shared component
mappings so their standalone and embedded names agree, unless whole-secret
classification overrides reconstruction as specified below. Explicit multiword
values match literal spans with boundaries at their outer word characters.
Matching occurs in decoded supported strings as well as plain text.

Numeric identifiers are replaced in recognised identifying contexts and
matching composite identifiers, not in unrelated timing/count fields. String
and numeric JSON types remain unchanged. Unknown natural-language names,
unrecognised provider formats, and encoded/compressed payloads are outside
automatic detection; the README must say so explicitly.

## Mapping and precedence

- Identical identifying values use one mapping across field names and log
  formats. Distinct identities must not collapse into one replacement. Decide
  equivalence only after collecting all contexts. If any member of a GUID's
  case-equivalence group occurs as a case-sensitive name, resource label or
  string instance key, keep every distinct decoded spelling in that group
  separate everywhere. Assign distinct valid GUIDs to those spellings; do not
  rely on output letter casing to distinguish them. Only GUID groups with
  no case-sensitive identifying context may share a case-normalised mapping.
  Ordinary names and whole secrets use exact decoded-value identity.
- Use numbered readable aliases for names. Retain only these recognised
  environment suffixes when separated by `_` or `-`: `dev`, `test`, `stage`,
  `staging`, `prod`. For example, `specific_name_prod` can become
  `name_0001_prod`; do not retain arbitrary name fragments.
- Allocate fresh GUIDs from standard-library cryptographic randomness and
  fail on randomness errors. Mappings exist only in memory. There is no
  mapping export, stable cross-file identity, seed flag, or reversible mode.
- Reserve generated values against source identifiers and other generated
  values. Allocation order must be explicit rather than depend on Go map
  iteration. Allocate fake IPv4 addresses within `10.0.0.0/8` and fake IPv6
  addresses within `fd00::/8`, skipping source addresses. Detect exhaustion
  and fail rather than reuse an address.
  Also check rendered resource keys and complete addresses against one
  another and against source identities. If distinct resource identities
  collide, reallocate the offending aliases before producing output. Casing
  or escaping must never silently turn two resources into one.
- Collect all identifying contexts before choosing an alias. When a name is
  also a hostname label, choose an alias valid in both contexts, such as
  `name0001`, instead of allocating conflicting name and hostname aliases.
  Retaining an environment suffix is subordinate to syntax validity.
- Whole-secret classification propagates to every identifying occurrence of
  that exact decoded value, including earlier occurrences, normal fields
  and supported nested strings. Use one opaque alias everywhere and suppress
  GUID/composite reconstruction for that full value. Its discovered components
  can still receive their own aliases where they occur independently.
- If that whole-value alias would conflict with mandatory inspector syntax
  or preserved metadata anywhere, reject the input before creating output.
  Do not split one secret into inconsistent aliases or leave a conflicting
  occurrence untouched. Preserve JSON scalar types; a numeric secret uses
  a numeric opaque alias that can also be rendered inside a string. If no
  shared alias satisfies the required contexts, reject with a content-free
  category/location error.
- After classification and conflict checks, apply structural protection,
  then whole secrets, composites and individual identifiers. Resolve overlaps
  deterministically and never substitute a replacement twice. Non-secret
  composites must use the same component aliases as standalone values.
- Protection applies to actual metadata positions, not all occurrences of a
  value. A GUID in a genuine `tf_req_id` field stays unchanged; its occurrence
  in an unrelated identifying field is scrubbed. A body string containing
  the text `tf_req_id=...` does not gain an exemption.
  This positional exemption does not bypass the whole-secret conflict check:
  a value also discovered as a secret requires rejection if it must remain
  visible in genuine `tf_req_id` metadata.
- If an explicitly supplied value overlaps mandatory preserved metadata,
  reject the request with a category/location-only error rather than silently
  claim it was scrubbed. Do not echo the value.

## Inspector invariants

Preserve line order, line endings, final-newline presence and hclog/JSON
framing. Replacement lengths may change subject to the validation below;
byte offsets are rebuilt when the inspector loads the new file. Preserve valid input JSON and quoted-field
syntax, including escaped quotes, backslashes and Unicode.

Preserve timestamps, severity levels, duration/count measurements, RPC
names, resource types, lifecycle event types/actions and recognised diagnostic
message prefixes such as `Received downstream response`. Keep Terraform
address grammar (`module`, `data`, separators, brackets and numeric count
indices), while replacing identifying labels and string keys.

Preserve built-in component/module labels and public provider identity
metadata required for readable grouping. The initial provider exemption set
is `registry.terraform.io/hashicorp/` followed by exactly one of `aws`,
`azurerm`, `azuread`, `google`, `local`, `null`, `random`, `time` or `tls`, plus
`registry.terraform.io/integrations/github`. Matching public plugin binary
labels retain their version/protocol suffixes. A public registry hostname
alone does not exempt its namespace: scrub unknown hostname/namespace
components consistently, including `registry.terraform.io/customer/custom`.
Keep the structural `provider.` component prefix and bare `provider` field
sentinel used by `ReportedBuilder.providerAddr`; unknown plugin names receive
consistent aliases. Provider groups must remain distinct. These exemptions
are positional and explicit, never a broad `tf_*` or JSON-key allowlist.

For supported fixtures, reloading the output must preserve entry order and
levels, request-ID scopes, span counts and timings, resource-type groupings,
resource lifecycle windows, attribution confidence, and candidate resource
relationships under the name mapping. Test relationships rather than compare
the inspector's independently interned numeric IDs directly.

### Parser-window validation

The existing scanner parses fields only from the first 65,536 bytes of a
header message (`internal/logfmt/entry.go`, `maxHeaderMsg`). A shorter alias
can expose a field outside that window; a longer alias can hide a field.
Textual preservation of its value is therefore insufficient.

Before returning output, scan both immutable buffers through `logfmt.Scan`
and compare entries by ordinal. Require unchanged entry count, timestamped
status, level, relative timestamps, component identity under its approved
mapping, request/response message classification, and first-field presence
and values for `tf_req_id`, `tf_req_duration_ms`, `tf_rpc`, `tf_resource_type`,
`tf_data_source_type` and `tf_provider_addr`. Compare provider values under
the approved mapping; all other listed field values remain verbatim. Resolve
interned IDs to their values rather than compare independent interner numbers.
Use the scanner's real parsing/truncation behaviour, including duplicate
field precedence; do not duplicate its window arithmetic in the scrubber.

Any mismatch rejects the transformation before output creation with a
content-free category and entry location. Scanner errors likewise produce
content-free failure, never a partially validated output. A long header is
not rejected solely for its length: accept it if this comparison passes.
Do not truncate the output, pad it with original sensitive bytes, relax the
invariants, or modify the scanner to make the comparison pass.

## Error handling and limits

Malformed input remains text for pattern-based discovery; do not discard it
or silently replace it with a reconstructed log. Report aggregate unsupported
structured/quoted-input counts without content. If a candidate cannot be
rewritten without damaging recognised syntax, fail before publishing output.
Plain-text logs are valid inputs and do not require a Terraform header.

Validate invalid UTF-8 and unsupported binary input explicitly and return an
error rather than corrupting bytes. Preserve empty files. Handle long lines
without the default `bufio.Scanner` token limit. Keep failures free of log
content, including JSON parser errors that might otherwise quote values.

The output remains a review candidate. A count of replacements measures work
performed, not detection completeness. No TUI export action, general-purpose
PII recognition engine, provider plugin system, or changes to existing
diagnose/profile masking belong in this feature.

## Verification and delivery

Use TDD with Go's existing testing tools and entirely synthetic identifying
values. Cover each detector with representative positive and negative cases;
exercise repeated and overlapping values, collisions, GUID exceptions,
escaped/nested JSON, multiline bodies, address indices, explicit values,
line endings, long lines and malformed text. Verify file refusal, aliases of
the input path, read/write failures, permissions and content-free diagnostics.

Build integration fixtures mixing resource lifecycle JSON, hclog RPC entries,
HTTP bodies and plan text. Reload original and scrubbed fixtures through
`model.Load` and compare the invariants above. Assert that targeted synthetic
identifiers disappear from identifying occurrences, while their replacements
remain distinct and consistent. Include `tf_req_id` decoys in body text.

### Review acceptance cases

- **PAR-1:** Two otherwise identical resource addresses with string keys
  `aa000000-0000-4000-8000-000000000000` and
  `aA000000-0000-4000-8000-000000000000` receive distinct valid GUID keys.
  Their standalone identifying occurrences use those same respective GUIDs.
  Reloading retains two resources, their lifecycle windows and attribution
  relationships. Assert distinction regardless of random letter placement,
  and exercise rendered-address collision handling explicitly.
- **PAR-2:** A response header with a 70,000-character password before its
  `tf_req_id` and `tf_req_duration_ms` is rejected if shortening the password
  exposes those fields. Also reject expansion of a short identifying value
  that pushes previously visible fields across the scanner boundary. Both
  errors name only the category/location and leave no output file. Include
  an accepted long-header case whose parser-visible metadata is unchanged,
  and compare its request scopes, span count and durations after reloading.
- **PAR-3:** The full value `arn:aws:iam::123456789012:user/alice` appearing
  in both `token` and `resource_arn` becomes the same opaque alias at both
  occurrences; ARN structure is deliberately not retained for that secret.
  Apply the same rule to a URL used as both a password and an endpoint.
  If a secret's full value is also a lifecycle resource address, reject
  because an opaque alias cannot preserve the mandatory address structure.
  A secret also present as genuine `tf_req_id` likewise causes rejection.
- **PAR-4:** Scrub `request_id=opaque-reference`,
  `tf_resource_id=private-resource`, and UI `hook.id_value="deadbeef"`, plus
  their earlier free-text occurrences, with stable distinct aliases. Exercise
  the documented case/separator variants. Preserve genuine `tf_req_id`,
  UI `hook.id_key="id"`, durations, counts and structural metadata. In an
  HTTP body, both a `tf_req_id` JSON member and a textual `tf_req_id=...`
  decoy containing a GUID must have that identifying value scrubbed.
  Also scrub `{"authorization":"opaque-private-value"}` and ordinary
  hclog `cookie="opaque-cookie-value"` fields through credential-key
  discovery, even though neither value matches a GUID or network pattern.
  Exercise `proxy_authorization`, `cookies` and `set_cookie` similarly.

Run focused tests during development, then `go test ./...`,
`go test -race ./...`, `go build ./...` and `go vet ./...`. Full-suite runs must
put the Codex Git wrapper on PATH because repository script tests create
commits. Complete independent review and a separate test-cleanup subagent
pass before delivery. Update CLI help and README coverage/limitations with
the actual implemented detectors, and use signed topic-branch commits.

## Design review record

The design is ready for Dan's written-spec review. Implementation and its
detailed plan follow that review; no implementation is included in this
document change.

Self-review checked scope, replacement precedence, syntax conflicts and
placeholders. Independent review identified an ambiguous public-provider
exemption; the explicit identity set and unknown-namespace rule above resolve
it. Dan approved the four peer-review corrections: context-sensitive GUID
identity, rejection of parser-visible metadata changes, propagated secret
classification with conflict rejection, and explicit non-GUID field coverage.
The rules and acceptance cases above specify those corrections. This
documentation-only change does not require runtime tests.
