# Provider fragment reconstruction

## Approved behaviour

Reconstruct provider JSON messages split across timestamped physical records,
including interleaved records from different providers. Scrub the complete
message, including escaped JSON and HTTP bodies, using the existing shared
identity mapping. Preserve original record order, timestamps and genuine
`tf_req_id` metadata. Reject incomplete or ambiguous reconstruction without
publishing output. Dan approved this approach on 9 September 2026.

## Evidence and privacy

The three authorised lines contained 65,536-byte message fragments. Two
AzureAD fragments surrounded an AzureRM fragment; the first AzureAD fragment
ended inside a JSON escape. Only structural diagnostics were retained. Tests
must use synthetic data. A subsequent authorised inspection of lines 11360–11361
found a complete Terraform UI event inserted directly into an unfinished provider
JSON string in the same physical line. The event is 799 bytes, has
`@module=terraform.ui` and `type=apply_complete`, and ends that physical line.
An additional authorised inspection of lines 11438–11439 found an ordinary
AzureAD HTTP request header followed by an unprefixed request-ID line. Only
structural facts were retained; other private-line inspection remains restricted.
Authorised lines 11417–11418 also show a standalone UI event inserted between
object properties in an AzureRM response, after a fragment ending with a comma.

## Architecture

Add reconstruction beside the log format parser, returning complete JSON
messages with ordered source byte ranges and physical line numbers. Maintain
independent pending messages for exact provider components, including their
process suffixes. Preserve continuation payload bytes; strip only transport
headers and physical line endings. Do not change scanner ordinals or its
streaming metadata contract.

Unprefixed continuations follow the most recent timestamped entry, as in the
ordinary log scanner. If that entry has no pending provider JSON, its continuation
remains ordinary physical content even while a different provider is pending.
An unfinished provider stream still fails at end of file.

Use JSON string/escape and delimiter state to recognise completion, then
validate the completed JSON structurally. Reject malformed input, interrupted
messages and unfinished messages with content-free diagnostics. Leave ordinary
provider messages and bracketed diagnostic tags alone.

Reconstruction assumes an ordered byte stream within each exact provider
component. An unrelated same-component record inserted inside an unfinished
JSON string is indistinguishable from payload in this format. Reject observable
syntax and ownership ambiguity; do not claim to detect unobservable mixing.
Accept hclog's optional colon before trailing logger fields, preserving those
fields outside the reconstructed JSON ranges.

Complete inline Terraform UI envelopes can interrupt the provider stream without
a transport delimiter. Exclude their ranges from provider JSON only at positions
where an object cannot be valid provider data, retaining their original bytes for
normal physical-view scrubbing. Require a first root annotation key, valid JSON,
`@module=terraform.ui` and nonempty string level, message, timestamp and type
markers. An object opening or comma inside an object expects a property name;
an inserted object at that point is invalid payload syntax. Object and array
value positions still admit genuine nested objects. Legitimate nested objects
and escaped payload text remain in the provider
message; malformed or uncertain insertions still fail without output.

The scrubber masks reconstructed body ranges while parsing physical metadata,
and parses each joined message as a separate logical view. Render each logical
view once and map source fragment boundaries into the rewritten text. A quoted
value or alias crossing a boundary is emitted atomically in its first fragment;
later fragments can consequently be shorter or empty. Preserve all physical
headers and record ordering. Validate metadata with body ranges excluded, so
an arbitrary fragment starting inside a string cannot pose as hclog metadata.

Expose reconstruction independently of scrubbing so the inspector can display
complete messages without changing raw bytes, filters, span IDs or attribution.
Provide an optional reconstructed-response viewer from Raw Log, opened with
`r` for the current entry. Use a scrollable, searchable centre pane and return
to the exact raw position with Esc or `r`. Format JSON and make multiline
`@message` text readable. Escape controls before terminal rendering. Resolve
responses lazily so ordinary loading and profiling do not pay this cost.

## Validation

Use interleaved AzureAD/AzureRM synthetic records, including a 64 KiB split
inside an escape, cuts inside UTF-8/quoted values, pretty JSON, empty fragments,
nested JSON/HTTP, repeated identifiers and trailing metadata. Verify malformed,
interrupted and incomplete messages fail without source disclosure. Verify
rewritten fragments reassemble to valid JSON and preserve metadata, source
ordering and alias linkage. Run the complete suite, race detector, build and
vet, followed by independent review and test cleanup.

## Constraints

Go 1.25+, existing dependencies only. British/Australian prose. Signed commits.
Raw bytes and scanner entry IDs remain authoritative. No private fixtures.
