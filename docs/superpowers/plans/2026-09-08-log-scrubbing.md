# Log Scrubbing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce consistently pseudonymised Terraform logs that retain inspector relationships.

**Architecture:** A byte-buffer scrub package discovers values before allocating aliases and rewriting original occurrences. Syntax-aware discovery preserves metadata positions; validation runs the real scanner on both buffers. The CLI owns input/output files and prints aggregate counts only.

**Tech Stack:** Go 1.25, standard library, existing `internal/logfmt` and model test APIs.

**Spec:** `docs/superpowers/specs/2026-09-08-log-scrubbing-design.md`

## Global Constraints

- Preserve `tf_req_id` fields; there is no blanket exemption for other `tf_*_id` fields or GUIDs.
- No new dependencies, external services, or model downloads.
- Mappings exist only in memory. There is no mapping export, stable cross-file identity, seed flag, or reversible mode.
- Refuse an existing output path, including symbolic links, rather than overwrite it. Write a new output with mode `0600`.
- Preserve valid input JSON and quoted-field syntax, including escaped quotes, backslashes and Unicode.
- Use TDD with Go's existing testing tools and entirely synthetic identifying values.
- Every commit must be signed through `/Users/dan/.codex/bin/codex-git`. Stop on signing failure. Do not push or merge.
- Full tests create Git commits: run them with `env PATH=/tmp/tfli-pr-review-bin.o2IMPc:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin go test ./...`.

## File responsibilities

- `internal/scrub/scrub.go`: public byte-buffer API and discovery/allocation/rewrite orchestration.
- `internal/scrub/syntax.go`: source spans, decoded quoted/JSON values and enclosing contexts, without reserialising entire logs.
- `internal/scrub/fields.go`: the spec's exact field/context classification table and metadata protection.
- `internal/scrub/aliases.go`: shared aliases, context-dependent GUID identity, collisions, secret precedence and rejection.
- `internal/scrub/validate.go`: real-scanner metadata comparison before returning output.
- `internal/scrub/patterns.go`: identifiers found independently of field keys, with composite component discovery.
- `internal/scrub/*_test.go`: public transformation contracts plus focused parser/detector edge cases.
- `internal/scrub/integration_test.go`: real `model.Load` comparisons using synthetic temporary logs.
- `cmd/tfli/scrub.go`, `cmd/tfli/scrub_test.go`: values-file parsing, scrub dispatch, exclusive output lifecycle and aggregate diagnostics.
- `cmd/tfli/main.go`, `cmd/tfli/main_test.go`: mode flags, validation and help.
- `README.md`: command, actual coverage and limitations.

### Task 1: Consistent structured values and preservation checks

**Files:** Create `internal/scrub/{scrub,syntax,fields,aliases,validate}.go` and matching tests. Create `internal/scrub/integration_test.go`.

**Interfaces:** Produces the following public API, used unchanged by Tasks 2 and 3:

```go
package scrub

type Result struct {
    Data []byte
    Replacements map[string]int // replaced occurrences by category; no original values
    Unsupported int // malformed structured/quoted inputs handled as text
}

func Scrub(data []byte, extra []string) (Result, error)
```

The zero-result accompanies every failure. Error messages contain category/location only. Categories are `name`, `id`, `guid`, `email`, `network`, `cloud`, `path`, `secret`, `explicit`. Counts describe applied occurrences, with an overlapping replacement counted once under its winning category.

- [x] Write the first public test before implementation. It must catch loss of repeated-value linkage and accidental request-ID rotation:

```go
func TestRepeatedNameAndRequestID(t *testing.T) {
    in := "Earlier customer_prod\n2026-09-08T00:00:00.000Z [TRACE] provider.aws: Sending request downstream: name=customer_prod tf_req_id=12345678-1234-4234-8234-123456789abc\n"
    got, err := Scrub([]byte(in), nil)
    if err != nil { t.Fatal(err) }
    out := string(got.Data)
    if strings.Contains(out, "customer_prod") { t.Fatal("name remains") }
    fields := strings.Fields(out)
    alias := fields[1]
    if !strings.Contains(out, "name="+alias) { t.Fatal("repeated value lost linkage") }
    if !strings.Contains(out, "tf_req_id=12345678-1234-4234-8234-123456789abc") { t.Fatal("request ID changed") }
}
```

- [x] Run `go test ./internal/scrub -run TestRepeatedNameAndRequestID`; observe missing feature, introduce only the API shell if necessary to obtain a behavioural RED, then implement discovery and shared replacement until GREEN.
- [x] Repeat RED/GREEN cycles for the complete initial field/context table, nested/escaped JSON, hclog field continuations versus body decoys, numeric scalar preservation, explicit values and malformed-input counts. Parse string escapes into a view whose edits can be encoded back into the original quoted span. Classify actual keys/positions; never globally exempt values discovered in metadata.
- [x] Build candidate records before allocating values. Keep exact decoded keys for ordinary names/secrets; use GUID equivalence groups only after all case-sensitive resource/name contexts are known. Replace longest applicable original spans once; a complete secret wins over a composite's edits. On a mandatory-syntax/secret conflict return a category/location error and an empty result.
- [x] Add failing tests for Terraform address labels, string versus numeric indices, resource declarations and JSON lifecycle resource fields. Discover labels from every address representation and use the same mappings in `addr`, `module`, `resource`, `resource_name`, `resource_key` and prose. Preserve resource types and count indices. Add PAR-1 case-only GUID resource keys and collision checks on rendered addresses. Use random valid GUIDs, reserve source/generated identities, and keep fixed environment suffixes from the spec only when syntax permits.
- [x] Add RED cases for invalid UTF-8, NUL-containing input, empty input, CRLF and absent final newline; preserve line count/order and delimiters. Validate `extra` values consistently with the values-file contract (no empty/control-containing replacements that could rewrite every boundary). Handle long lines without Scanner's default token limit.
- [x] Implement real-scanner comparison using a sink that records entry metadata and `Fields.Get` results for the exact six fields listed in the spec. Keep provider/component comparisons consistent with alias mappings. Do not duplicate the scanner's truncation arithmetic. Add these boundary tests before validation code:

```go
func TestShrinkingHeaderRejectsNewSpan(t *testing.T) {
    in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Received downstream response: password=\"" + strings.Repeat("z", 70000) + "\" tf_req_id=abc tf_req_duration_ms=5\n"
    got, err := Scrub([]byte(in), nil)
    if err == nil || len(got.Data) != 0 { t.Fatal("published changed parser metadata") }
    if strings.Contains(err.Error(), "zzz") { t.Fatal("error disclosed input") }
}
```

- [x] Add expansion-across-boundary and accepted-long-header cases. Use `model.Load` on synthetic originals/output files to assert preserved scopes, spans, durations, lifecycle relationships and distinct addresses. Expected identities and relationship counts come from hand-written fixtures, not scrubber helpers.
- [x] Run `go test ./internal/scrub`, `gofmt` on created Go files, then rerun the focused suite. Commit signed as `Add consistent structured log scrubbing`.
- [x] Complete independent task review (spec coverage for this task and code quality) and a separate test-cleanup pass. Task 2 owns the network/cloud/path/PEM detector categories below, so those are not yet a complete feature.

### Task 2: Composite identifiers and full detection coverage

**Files:** Create `internal/scrub/patterns.go`, `internal/scrub/patterns_test.go`; extend `aliases.go`, `syntax.go`, `scrub_test.go` and `integration_test.go` only where detectors need shared mapping/rewrite support.

**Interfaces:** Consumes and retains `Scrub([]byte, []string) (Result, error)` and `Result` from Task 1. Detectors feed the same candidate/mapping system; they must not allocate separate aliases or rewrite already-transformed output.

- [x] First address measured matching cost: the synthetic scaling probe recorded 842 ms for 1 MiB/500 distinct names and 13.7 s for 4 MiB/2,000 names. Add a benchmark with generated names and prose repetitions and RED/GREEN contract tests for prefix overlaps, shorter secret precedence, protected spans and Unicode boundaries. Index candidates by their byte prefixes so rendering examines actual matches at a position rather than the entire candidate list. One small private trie is sufficient:

```go
type candidateIndex struct {
    next map[byte]*candidateIndex
    terminal *candidate
}
```

Build the index after discovery and retain candidate pointers so allocation retries update aliases without stale copies. Enumerate matching terminals longest-first, preserving the existing secret priority and boundary/protection checks. Run the focused suite and the same scratch scaling probe before/after; record observed timings without machine-dependent timing assertions in unit tests. Keep public output contracts unchanged.

- [x] Add failing public-contract cases, one category at a time, for email, IPv4/IPv6, URL hosts/userinfo/query identifying values, hostname fields, AWS ARNs/account IDs, Azure resource paths, GCP resource paths, POSIX/Windows paths, and PEM private-key payloads. This example catches an independent ARN mapping:

```go
func TestSecretARNUsesOneAlias(t *testing.T) {
    in := `token="arn:aws:iam::123456789012:user/alice" resource_arn="arn:aws:iam::123456789012:user/alice"`
    got, err := Scrub([]byte(in), nil)
    if err != nil { t.Fatal(err) }
    fields := logfmt.ParseFields(string(got.Data), nil)
    token, _ := fields.Get("token")
    arn, _ := fields.Get("resource_arn")
    if token == "" || token != arn || strings.Contains(token, "alice") || strings.Contains(token, "arn:") { t.Fatal("inconsistent secret alias") }
}
```

- [x] Run the relevant focused test and observe RED before each detector. Add minimal detection that validates candidates with standard-library parsers where available (`net/netip`, `net/url`), then GREEN. Preserve URL scheme/port and cloud service/type structure; identifying components use shared aliases everywhere. Syntax validity outranks retaining environment suffixes.
- [x] Allocate fake IPv4 within `10.0.0.0/8`, IPv6 within `fd00::/8`, email/host aliases under `example.invalid`; reserve original and generated identities and fail without output on exhaustion. Test syntax, distinction and linkage, not a particular random GUID.
- [x] Exercise raw and escaped repeated composite values, an ARN/URL also used as a secret, source/generated alias collisions, substrings such as `ann` versus `planning`, multiline PEM preserving framing, and private provider namespaces. Preserve only the exact public provider identities in the spec, including their recognised plugin labels and suffixes; retain the structural `provider.` prefix and bare `provider` sentinel.
- [x] Add a mixed synthetic integration fixture combining all categories with UI lifecycle events and RPC timings; compare model relationships before/after and assert no targeted identifying occurrences remain. Empty/malformed and unsupported encoded payloads must follow the documented error/count policy.
- [x] Run `go test ./internal/scrub`, format the changed Go files and rerun. Commit signed as `Scrub network cloud and path identifiers consistently`.
- [x] Complete independent task review and a separate test-cleanup pass; resolve significant findings with regression tests before Task 3.

### Task 3: Safe CLI output and documentation

**Files:** Create `cmd/tfli/scrub.go`, `cmd/tfli/scrub_test.go`; modify `cmd/tfli/main.go`, `cmd/tfli/main_test.go`, `README.md`.

**Interfaces:** Consumes `scrub.Scrub` and `scrub.Result`. Add `runScrub(inputPath, outputPath, valuesPath string, stderr io.Writer) error`; mode dispatch returns its error through the existing terminal-safe error handling. Values-file parsing is local to the command, returning `[]string` for the public API.

- [ ] Write a CLI test with temporary input/output paths that invokes the existing `run` function using `--scrub -o`, confirms the input is unchanged, the new output is scrubbed and reloadable, stdout is empty, and stderr contains counts without source values. Run `go test ./cmd/tfli -run Scrub` and observe unsupported-flag RED.
- [ ] Add the flags and dispatch. Keep the existing diagnose/profile/TUI paths intact:

```go
doScrub := fs.Bool("scrub", false, "write a log with consistent fake identifying values")
valuesPath := fs.String("scrub-values", "", "additional literal identifying values, one per line")
```

- [ ] Test and implement rejection of combined modes, missing `-o`, and `--scrub-values` outside scrub mode. Read input and values before transforming; remove only line terminators, ignore empty lines, retain other whitespace and reject controls/invalid UTF-8 without echoing them.
- [ ] Test existing output files, input/output identity, symbolic/hard links, missing input/values, invalid output directory and permission mode. Create output only after successful scrubbing using `os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)`. Write all bytes, check short writes and close errors, remove only the newly created partial file on failure, and report cleanup failure without log content. Keep output ownership/lifecycle in the CLI rather than reuse the overwriting `writeReport` helper.
- [ ] Test failure and count diagnostics, including zero replacements and unsupported input counts. Print categories in stable order; do not expose the mapping. Add help/README examples, actual detector coverage, explicit-values use, unchanged source behaviour, preserved metadata, errors on invariant conflicts and the manual-review limitation.
- [ ] Run `go test ./cmd/tfli ./internal/scrub`, format changed Go files and commit signed as `Expose safe log scrubbing through the CLI`.
- [ ] Complete task review and separate test-cleanup. Run the full normal/race suites using the signing-safe PATH, `go build ./...`, and `go vet ./...`. Manually scrub a synthetic fixture and open/profile the output; inspect that the result is useful without exposing source identifiers.
- [ ] Complete broad independent whole-branch review against `main`, resolve findings with TDD and scoped re-review, and record validation in this plan. Keep the feature on its topic branch; do not push or merge without Dan's instruction.

## Plan self-review

Task 1 covers syntax, the field table, shared allocation, exceptions, error contracts and PAR-1/PAR-2. Task 2 completes the discovery table, shared composite reconstruction, public-provider rules and PAR-3. Task 3 covers the file/CLI contract and user documentation. PAR-4 is exercised by both package and command tests. Signatures and `Result` fields are shared unchanged across all tasks. No dependency or scanner/model change is required.
