# Response Recovery Parser and Strict Scrubbing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retain independently verified provider responses after local reconstruction failures while rejecting every diagnostic-bearing capture for scrubbing.

**Architecture:** Extract a structured outcome from the existing provider-fragment parser, keeping one grammar and one ownership model. The strict reconstruction entry point consumes that outcome and returns no messages when any diagnostic exists. The future viewer plan will consume the outcome directly; this plan does not change viewer or quality-report behaviour.

**Tech Stack:** Go 1.25, standard library, existing `internal/logfmt` and `internal/scrub`, standard Go tests; no new dependency or runner.

**Spec:** [Investigation workflows](../specs/2026-09-09-investigation-workflows-design.md), item 8 and Boundary I. The existing [provider-fragments design](../specs/2026-09-09-provider-fragments-design.md) supplies the transport, suffix and inline-UI grammar that remains authoritative for complete messages.

## Global Constraints

- “Use one parser and one definition of message ownership.”
- “Do not fork a lenient parser for viewing or add a permissive switch that the scrubber could enable accidentally.”
- “Preserve exact component isolation and source-fragment mapping.”
- “Source bytes and scanner entry identities remain authoritative. Derived views, reconstructed text and filters never modify either.”
- “Diagnostics use fixed reason codes, counts and locations; they do not embed source text or JSON parser snippets.”
- “Scrubbing continues to reject any reconstruction diagnostic and create no output.”
- “Fixtures are synthetic or sanitised.”
- Follow TDD with behavioural RED after declarations compile, independent task review, and a separate test-cleanup subagent after each implementation/fix pass.
- Work on a topic branch. Use `/Users/dan/.codex/bin/codex-git` and signed commits; stop immediately on signing failure. No merge or push without Dan's instruction.
- Keep the existing Go toolchain and CI target matrix: Linux/macOS, amd64/arm64. Use Australian/British prose and comments.

---

## Status and delivery boundary

Draft authorised by Dan on 11 September 2026 after comparison and its input-identity fix merged locally at `f614493`. This is I1, the first of two sequential plans. It requires review and implementation approval; no application changes are authorised by the draft itself.

I1 supplies reconstruction outcomes and locks down strict publication. I2 will specify physical-position lookup, model caching, viewer notices/navigation and complete/partial/failed quality presentation. I1 leaves existing `model.ProviderResponse`, text/JSON quality schemas and TUI behaviour unchanged. Boundary I is not complete when I1 alone finishes.

Current code at `f614493`:

- `internal/logfmt/fragments.go` holds the single parser. `ReconstructProviderJSON` abandons all messages at the first error. Pending messages occupy start-ordered slots; exact component strings key the pending map.
- `providerJSONPending.consume` strips only verified inline UI events, preserves payload fragments, checks delimiters/suffixes, then the caller validates assembled UTF-8 and JSON.
- `internal/scrub/fragments.go:parseProviderFragments` calls the strict parser before constructing transformation views. `Scrub` returns `Result{}` on failure; `runScrub` opens output only after success.
- `internal/model/log.go:ProviderResponse` also calls the strict entry point and caches failure. Its migration belongs to I2.

The two entry points below express consumer policy, not separate parsing modes or a backwards-compatibility feature. Keep `ReconstructProviderJSON` as the actively used strict API for scrub. It delegates to the same outcome-producing parser that I2 will use.

## File map

| File | Responsibility |
| --- | --- |
| Modify `internal/logfmt/fragments.go` | Existing byte grammar, ownership state, component quarantine and complete-message assembly |
| Create `internal/logfmt/reconstruction.go` | Outcome/diagnostic types, fixed diagnostic formatting, strict consumer policy |
| Create `internal/logfmt/reconstruction_test.go` | Outcome retention, quarantine, global-stop, ranges and ordering tests |
| Retain/extend `internal/logfmt/fragments_test.go` | Existing strict grammar and detailed safe-error regressions |
| Create `internal/logfmt/reconstruction_benchmark_test.go` | Descriptive scaling for many complete/damaged streams |
| Extend `internal/scrub/fragments_test.go` | Diagnostic-bearing captures always yield no scrub result |
| Extend `cmd/tfli/scrub_test.go` | Real-file publication refusal, including preservation of existing output |
| Modify `internal/scrub/fragments.go` only if needed | Keep strict gate before any transformation; no second parser |
| Update this plan after execution | Evidence, review outcomes and I2 handover |

## Proposed outcome contract

All new names below are planned APIs, not claims about existing code.

```go
// internal/logfmt/reconstruction.go
type ProviderJSONResult struct {
    Messages    []ProviderJSON
    Diagnostics []ProviderJSONDiagnostic
}

type ProviderJSONDiagnostic struct {
    Code               string
    Line, StartLine     int
    FragmentCount      int
    JoinedBytes        int
    FirstFragmentBytes int
    LastFragmentBytes  int
    SyntaxOffset       int64
    SyntaxLine         int
    Ranges             []JSONFragment
    Unavailable        []JSONFragment
}

func InspectProviderJSON(text string) ProviderJSONResult
func (d ProviderJSONDiagnostic) Error() string
func ReconstructProviderJSON(text string) ([]ProviderJSON, error)
```

`Messages` contains only complete, UTF-8-valid, JSON-valid bodies satisfying the existing suffix grammar. Keep message-start order, not completion order; remove pending/failed slots during finalisation. Complete-message fragments keep their current half-open original byte offsets and one-based physical lines. Concatenating original slices must reproduce `Text` exactly, excluding transport bytes and recognised inline UI events. Do not copy raw component names, body text or parser snippets into diagnostics.

`Ranges` contains the known source payload portions of the unsuccessful message, including its failing physical payload through that line's end. For completed-but-invalid JSON/UTF-8, reuse its payload fragments. For a structural failure, preserve prior fragments and record the failing payload span; this diagnostic span may include unclassified trailing bytes and is not a reconstructed body. Never attribute another component's physical line to it. Empty fragments may retain line locations but must not match a byte position.

`Unavailable` contains subsequent physical payload ranges assigned to the quarantined component, including ordinary same-component messages and their continuations; these are unavailable stream positions, not attempted new messages. It must not include known transport headers or another component's lines. A global ownership failure instead marks the triggering physical line and every later physical line unavailable, including headers, because no response ownership can be asserted. Keep each range within one physical line, without line terminators: exclude LF and the CR immediately preceding LF; also exclude a final CR at EOF, matching the existing physical-line loop. I2 must prioritise complete-message fragments and respect these different range meanings; do not treat a diagnostic range as verified JSON.

`Line` is the physical line where failure is detected; at EOF use the final physical line processed, without inventing a line after a trailing newline. `StartLine` is the first attempted payload line, except for the trigger-only shape below. Counts and syntax coordinates retain the existing diagnostic meanings. `SyntaxOffset` is one-based joined-payload bytes, zero when unknown; `SyntaxLine` is its mapped source line or zero. Outcome diagnostics sort by `Line`, then `StartLine`, then the safe source-position key below, then `Code`. This retains detection order across ordinary failures and makes EOF ties deterministic. Within either range list, preserve source order. Empty outcomes have no messages or diagnostics; nil versus empty slices is not a public wire contract. The strict caller returns the first diagnostic in this declared order, not an arbitrary pending-map entry.

```go
func providerJSONDiagnosticStart(d ProviderJSONDiagnostic) int {
    if len(d.Ranges) != 0 { return d.Ranges[0].Start }
    if len(d.Unavailable) != 0 { return d.Unavailable[0].Start }
    return 0
}
```

The final fallback makes an empty hand-built diagnostic safe to sort; parser-produced trigger diagnostics always contain at least the triggering line in `Unavailable`. A blank unavailable line uses an empty half-open range with its real line-start offset, which never matches a byte. Do not index a range list without checking its length.

Global-stop diagnostics have two precise shapes:

| Field | Trigger-only diagnostic | Aborted pending-message diagnostic |
| --- | --- | --- |
| `Code` | `ambiguous_ownership` | `ambiguous_ownership` |
| `Line` | Trigger's physical line | Trigger's physical line |
| `StartLine` | Trigger's physical line | First attempted payload line |
| `FragmentCount` | `0` | Number of previously collected payload fragments, including empty fragments |
| `JoinedBytes` | `0` | Pending builder's byte length before the trigger, excluding stripped UI events |
| `FirstFragmentBytes` | `0` | First collected payload fragment's byte length |
| `LastFragmentBytes` | `0` | Saved consumed-byte count from the pending body's most recent contributing physical line, using the existing diagnostic convention |
| `SyntaxOffset`, `SyntaxLine` | Both `0` | Both `0`; do not infer syntax failure from ownership loss |
| `Ranges` | Empty | All previously collected payload ranges, unchanged; no trigger bytes |
| `Unavailable` | Complete trigger line, followed by every remaining physical line, with headers and without terminators | Empty; the trigger diagnostic owns the global unavailable tail |

`Error()` for both shapes uses `Line` in `provider JSON at line N: ambiguous ownership`. All fields are numeric facts, fixed codes or positions; the triggering header text and component strings never enter the diagnostic. Nil and empty range lists are equivalent; tests compare lengths or normalise empty lists before whole-struct comparisons.

| Code | Safe error reason / policy |
| --- | --- |
| `delimiter_mismatch` | Existing `delimiter mismatch` wording; quarantine exact component |
| `suffix_grammar` | Existing `suffix grammar`; quarantine exact component |
| `json_syntax` | Existing `JSON syntax`; quarantine exact component |
| `invalid_utf8` | Existing `UTF-8`; quarantine exact component |
| `invalid_inline_ui` | Existing `invalid inline UI envelope`; quarantine exact component |
| `incomplete` | Existing `provider JSON at line N: incomplete or invalid body`, where N is `StartLine`; pending message at EOF |
| `ambiguous_ownership` | Fixed `provider JSON at line N: ambiguous ownership`; global stop or pending message aborted by that stop |

For the five ordinary invalid-body codes, `Error()` uses `providerJSONFailure` with the existing numeric fields and appends `; syntax source line N` only when known. Preserve existing content-free diagnostic detail assertions. It must not call `json.SyntaxError.Error()`. A code outside the table formats as fixed `provider JSON: reconstruction failed`; callers never supply source-derived codes. Do not expose a mutable global code/label map.

The strict consumer is deliberately small:

```go
func ReconstructProviderJSON(text string) ([]ProviderJSON, error) {
    result := InspectProviderJSON(text)
    if len(result.Diagnostics) != 0 {
        return nil, result.Diagnostics[0]
    }
    return result.Messages, nil
}
```

## Ownership and failure transitions

1. Preserve `providerJSONOuter`, `providerJSONInitial`, nested-prefix recognition, suffix grammar and `terraformUIBytes` rules for valid input. A header's exact component identifies its stream. Headerless continuations belong only to the most recent physical entry, as today; a pending stream elsewhere does not acquire them.
2. On a component-local failure, retain earlier complete messages, discard only that pending body, store one diagnostic for that failure, and permanently quarantine its exact component key. Other known components can finish pending messages or begin new ones. Never call `providerJSONInitial` for a quarantined component, even if a later payload is valid-looking `{...}`.
3. A timestamped provider-looking header whose exact owner cannot be extracted triggers global failure. Precisely: after `splitTimestamp` and `splitLevel`, trim leading spaces/tabs from the remaining text; if it begins `provider.` but `providerJSONOuter` cannot supply a component with a nonempty suffix after `provider.` and a valid colon separator, ownership is ambiguous. This includes `provider.:`, `provider.a missing-colon`, and component text containing whitespace before the colon. Ordinary timestamped non-provider entries, standalone UI events and headerless payload bytes do not trigger this rule. This is a conservative proposed refinement of item 8, to review explicitly before implementation.
4. At global failure, retain only messages already complete. Emit the trigger-only `ambiguous_ownership` shape and one aborted-pending shape for each still-pending body, exactly as defined above. Stop body reconstruction at the trigger; the trigger diagnostic alone owns whole-line unavailable ranges beginning with the trigger itself. Never add trigger bytes to an aborted body's `Ranges`. No later apparent provider start, even from a different component, is recovered. Already quarantined local diagnostics keep their ranges accumulated before the stop.
5. At ordinary EOF, emit `incomplete` for each pending body, retain complete messages from all streams, and do not append duplicate incompleteness for already-quarantined streams. A split UTF-8 sequence may be completed across fragments; do not validate each physical fragment as UTF-8 independently.
6. Keep active-entry tracking even for ordinary and quarantined headers. A continuation after an ordinary entry remains ordinary; after a quarantined entry it is unavailable. An empty/headerless capture remains diagnostic-free unless it contains an attempted provider message under the existing grammar. Do not claim to detect same-component text inserted inside an unfinished JSON string when it is indistinguishable from payload.

Use the existing single physical-line loop. A map associates a component with pending state or a diagnostic index; diagnostic final sorting happens only after all unavailable ranges are attached. A stable start-ordered message slot list supplies final complete-message order. Release failed builders promptly; quarantine stores ranges and numeric facts, not a copy of discarded bodies. Avoid rescanning the entire capture per diagnostic or building one remaining-tail copy per damaged component.

### Task 1: Produce structured outcomes while retaining strict rejection

**Files:** `internal/logfmt/fragments.go`, new `internal/logfmt/reconstruction.go`, new `internal/logfmt/reconstruction_test.go`, existing `internal/logfmt/fragments_test.go`.

**Interfaces:** Consumes existing `ProviderJSON`, `JSONFragment`, `providerJSONPending.consume` and `providerJSONFailure`. Produces the three public signatures and diagnostic fields above. For this task, stop inspection at its first failure but return earlier verified messages plus that diagnostic; task 2 adds continued independent recovery and quarantine. Strict callers remain fail-closed throughout.

- [ ] **Step 1: Add outcome and strict-policy tests.** Include the following behavioural seed, imports `reflect`, `strings`, `testing`; use the existing `providerRecord` helper. Add declarations with a compiling empty-result body only when required, then observe the assertion failure before implementing retention.

```go
func TestInspectProviderJSONRetainsVerifiedPrefix(t *testing.T) {
    input := providerRecord("a", `{"ok":1}`) + "\n" +
        providerRecord("b", `{"private-token":]}`)
    result := InspectProviderJSON(input)
    if len(result.Messages) != 1 || result.Messages[0].Text != `{"ok":1}` {
        t.Fatalf("verified prefix lost: %#v", result.Messages)
    }
    if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "delimiter_mismatch" {
        t.Fatalf("diagnostics: %#v", result.Diagnostics)
    }
    if got, err := ReconstructProviderJSON(input); err == nil || got != nil {
        t.Fatalf("strict reconstruction accepted partial result: %#v, %v", got, err)
    }
    if strings.Contains(result.Diagnostics[0].Error(), "private-token") {
        t.Fatal("source text in diagnostic")
    }
}
```

For clean single/interleaved messages, assert `reflect.DeepEqual(result.Messages, strict)` and no diagnostics. For each existing failure family, assert the fixed code, start/detection lines, safe formatting and exact numeric/source mapping expected in `fragments_test.go`; retain those existing tests instead of replacing their assertions with weaker code-only checks. Include zero messages/zero diagnostics for empty/ordinary input. Assert each complete message's joined original fragments equals its text.

- [ ] **Step 2: Run behavioural RED.** `go test ./internal/logfmt -run 'TestInspectProviderJSON|TestProviderJSON' -count=1`. Record an actual missing-retained-message/diagnostic assertion, not merely an undefined symbol.
- [ ] **Step 3: Implement structured failure projection and the strict gate.** Move the public strict entry point to `reconstruction.go`; rename the single scanning implementation to `InspectProviderJSON`. Replace error exits with an outcome containing the completed slots and typed diagnostic. Make diagnostic construction precede discarding the failed builder. Finalise incomplete EOF slots safely even when their last fragment is empty. Keep grammar helpers in place and preserve source-syntax offset mapping. Implement the exact strict gate shown above and a closed switch mapping the table's codes to existing reasons. Do not add a second scanning pass in the strict entry point.
- [ ] **Step 4: Run GREEN and consumer parity.** `go test ./internal/logfmt ./internal/scrub ./internal/model ./internal/tui ./internal/profile ./cmd/tfli -count=1`. Confirm old strict rejection and safe diagnostic tests still pass. No viewer or quality expectation changes are allowed in I1.
- [ ] **Step 5: Review, cleanup and commit.** Obtain independent task review, address findings, run separate test cleanup. Signed commit: `Separate reconstruction outcomes from strict consumer policy`.

### Task 2: Recover independent streams and quarantine uncertain ownership

**Files:** `internal/logfmt/fragments.go`, `internal/logfmt/reconstruction.go`, `internal/logfmt/reconstruction_test.go`, new `internal/logfmt/reconstruction_benchmark_test.go`.

**Interfaces:** Consumes `InspectProviderJSON(text string) ProviderJSONResult`, `ProviderJSONDiagnostic` and strict `ReconstructProviderJSON` from task 1. Produces the same APIs with all ownership transitions and final ordering specified above; no public signature changes.

- [ ] **Step 1: Add the recovery matrix.** Use this table as concrete input/output cases inside `TestInspectProviderJSONRecovery`. The helper `join` below is test-local.

```go
join := func(lines ...string) string { return strings.Join(lines, "\n") }
cases := []struct {
    name, input string
    want []string
    codes []string
}{
    {"independent after failure", join(providerRecord("a", `{"a":1}`), providerRecord("b", `{"b":]}`), providerRecord("a", `{"a":2}`)), []string{`{"a":1}`, `{"a":2}`}, []string{"delimiter_mismatch"}},
    {"pending independent", join(providerRecord("a", `{"a":`), providerRecord("b", `{"b":]}`), providerRecord("a", `1}`)), []string{`{"a":1}`}, []string{"delimiter_mismatch"}},
    {"no restart", join(providerRecord("a", `{"a":]}`), providerRecord("a", `{"false-restart":1}`), providerRecord("b", `{"b":1}`)), []string{`{"b":1}`}, []string{"delimiter_mismatch"}},
    {"complete then EOF", join(providerRecord("a", `{"a":1}`), providerRecord("a", `{"a":`)), []string{`{"a":1}`}, []string{"incomplete"}},
    {"invalid UTF8", join(providerRecord("a", "{\"a\":\"\xff\"}"), providerRecord("b", `{"b":1}`)), []string{`{"b":1}`}, []string{"invalid_utf8"}},
    {"global stop", join(providerRecord("a", `{"a":1}`), providerRecord("", `{"unknown":1}`), providerRecord("b", `{"b":1}`)), []string{`{"a":1}`}, []string{"ambiguous_ownership"}},
}
for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
        got := InspectProviderJSON(tc.input)
        bodies, codes := []string{}, []string{}
        for _, m := range got.Messages {
            bodies = append(bodies, m.Text)
            var joined strings.Builder
            for _, r := range m.Fragments {
                if r.Start < 0 || r.End < r.Start || r.End > len(tc.input) {
                    t.Fatalf("invalid source range: %#v", r)
                }
                joined.WriteString(tc.input[r.Start:r.End])
            }
            if joined.String() != m.Text { t.Fatal("source mapping changed") }
        }
        for _, d := range got.Diagnostics { codes = append(codes, d.Code) }
        if !reflect.DeepEqual(bodies, tc.want) || !reflect.DeepEqual(codes, tc.codes) {
            t.Fatalf("bodies=%q codes=%q", bodies, codes)
        }
        if strict, err := ReconstructProviderJSON(tc.input); strict != nil || err == nil {
            t.Fatal("strict consumer accepted diagnostics")
        }
    })
}
```

Add `TestInspectProviderJSONRangesAndOrdering` with a pending A starting on line 1, complete B on line 2, local C failure on line 3, and A completing on line 4: require messages A then B, C diagnostic only, and exact original payload slices. Follow with C ordinary header plus continuation and a valid-looking C restart: all three payloads must appear in C's `Unavailable`, and none in complete-message fragments. Append a new ordinary non-provider entry and continuation; neither may enter C's unavailable ranges. Repeat inspection and require deep equality.

Add explicit EOF tests for two pending components with interleaved starts/continuations and an earlier local failure, a trailing newline, a blank final continuation and CRLF. Assert the declared diagnostic sort tuple and valid line-local ranges without map-order dependence. Add global-stop tests with pending A and B plus each malformed-header form in transition 3: one trigger diagnostic plus two aborted-pending diagnostics, no later completions, earlier verified messages preserved, only the trigger owns the remaining unavailable ranges.

Pin the special source boundaries with these two tests in `reconstruction_test.go`. They use the existing `providerRecord` timestamp, so its header plus `provider.a: ` is exactly 45 bytes. The literal offsets below are expectations from fixture bytes, not values calculated by the production range helpers.

```go
func TestInspectProviderJSONStructuralFailureRanges(t *testing.T) {
    cases := []struct {
        name, payload, code string
        end, consumed int
    }{
        {"delimiter", `{"a":]tail`, "delimiter_mismatch", 55, 6},
        {"suffix", `{"a":1}tail`, "suffix_grammar", 56, 7},
        {"inline UI", `{"a":"x{"@module":false}tail`, "invalid_inline_ui", 73, 8},
    }
    for _, tc := range cases {
        for _, ending := range []string{"\n", "\r\n"} {
            t.Run(tc.name+"/"+fmt.Sprintf("%q", ending), func(t *testing.T) {
                input := providerRecord("a", tc.payload) + ending
                result := InspectProviderJSON(input)
                if len(result.Messages) != 0 || len(result.Diagnostics) != 1 {
                    t.Fatalf("outcome: %#v", result)
                }
                d := result.Diagnostics[0]
                wantRanges := []JSONFragment{{Start: 45, End: tc.end, Line: 1}}
                if d.Code != tc.code || d.Line != 1 || d.StartLine != 1 ||
                    !reflect.DeepEqual(d.Ranges, wantRanges) || len(d.Unavailable) != 0 {
                    t.Fatalf("diagnostic boundaries: %#v", d)
                }
                if d.FragmentCount != 1 || d.JoinedBytes != tc.consumed ||
                    d.FirstFragmentBytes != tc.consumed || d.LastFragmentBytes != tc.consumed {
                    t.Fatalf("consumed counts: %#v", d)
                }
                if tc.end-45 <= d.LastFragmentBytes {
                    t.Fatal("fixture must include unconsumed trailing bytes")
                }
                if input[45:tc.end] != tc.payload {
                    t.Fatal("literal fixture boundary changed")
                }
            })
        }
    }
}
```

The malformed inline candidate is detected at its opening `{` after `{"a":"x`; the diagnostic span covers its full physical payload, while counts stop after that opening brace. This intentionally tests a broader diagnostic range than verified payload or consumed bytes. Add `fmt` to this test file's imports.

```go
func TestInspectProviderJSONGlobalDiagnosticShape(t *testing.T) {
    const head = "2026-09-08T00:00:00.000Z [DEBUG] "
    lines := []string{
        head + `provider.a: {"a":`,
        head + `provider.b: {"b":`,
        head + `provider.: {}`,
        head + `provider.c: {"ok":1}`,
        head + `terraform: ordinary`,
    }
    cases := []struct {
        name, input string
        want []ProviderJSONDiagnostic
    }{
        {"lone trigger EOF", lines[2], []ProviderJSONDiagnostic{
            {Code: "ambiguous_ownership", Line: 1, StartLine: 1,
                Unavailable: []JSONFragment{{Start: 0, End: 46, Line: 1}}},
        }},
        {"LF pending streams", strings.Join(lines, "\n") + "\n", []ProviderJSONDiagnostic{
            {Code: "ambiguous_ownership", Line: 3, StartLine: 1,
                FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5,
                Ranges: []JSONFragment{{Start: 45, End: 50, Line: 1}}},
            {Code: "ambiguous_ownership", Line: 3, StartLine: 2,
                FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5,
                Ranges: []JSONFragment{{Start: 96, End: 101, Line: 2}}},
            {Code: "ambiguous_ownership", Line: 3, StartLine: 3,
                Unavailable: []JSONFragment{{Start: 102, End: 148, Line: 3}, {Start: 149, End: 202, Line: 4}, {Start: 203, End: 255, Line: 5}}},
        }},
        {"CRLF pending streams", strings.Join(lines, "\r\n") + "\r\n", []ProviderJSONDiagnostic{
            {Code: "ambiguous_ownership", Line: 3, StartLine: 1,
                FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5,
                Ranges: []JSONFragment{{Start: 45, End: 50, Line: 1}}},
            {Code: "ambiguous_ownership", Line: 3, StartLine: 2,
                FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5,
                Ranges: []JSONFragment{{Start: 97, End: 102, Line: 2}}},
            {Code: "ambiguous_ownership", Line: 3, StartLine: 3,
                Unavailable: []JSONFragment{{Start: 104, End: 150, Line: 3}, {Start: 152, End: 205, Line: 4}, {Start: 207, End: 259, Line: 5}}},
        }},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            result := InspectProviderJSON(tc.input)
            if len(result.Messages) != 0 || len(result.Diagnostics) != len(tc.want) {
                t.Fatalf("outcome: %#v", result)
            }
            for i, d := range result.Diagnostics {
                // Empty-list representation is not part of this contract.
                if len(d.Ranges) == 0 { d.Ranges = nil }
                if len(d.Unavailable) == 0 { d.Unavailable = nil }
                if !reflect.DeepEqual(d, tc.want[i]) {
                    t.Fatalf("diagnostic %d: got %#v, want %#v", i, d, tc.want[i])
                }
            }
        })
    }
}
```

These full-struct expectations also pin all zero body/syntax fields on the trigger and both zero syntax fields on aborted bodies. They require aborted bodies before the trigger under the declared ordering, retain only their prior payload ranges, include the malformed header and later provider/non-provider headers in the trigger's unavailable list, and exclude both CR and LF. Extend the existing empty/blank-line cases to check the safe source-position helper with `ProviderJSONDiagnostic{}` and with an empty `Ranges` but nonempty `Unavailable`; neither may panic or read a nonexistent element.

Exercise every local failure code from the table followed by an independent good component. Reuse the valid inline-UI literal in `TestProviderJSONInlineUI`: place an event in a retained A body around a malformed B; assert A's original fragments exclude it. A malformed inline event quarantines only its known owner. Include split UTF-8 across A fragments with a B failure between them, multiple completed responses from one component, and the existing ordinary-continuation isolation cases. These additions must assert both retained bodies and diagnostic/unavailable positions, not just success.

- [ ] **Step 2: Run RED.** `go test ./internal/logfmt -run TestInspectProviderJSON -count=1`. Task 1's stop-at-first-failure implementation must fail the independent continuation and quarantine/unavailable assertions.
- [ ] **Step 3: Implement the six transitions.** Add a private component-to-diagnostic quarantine map, keep active-entry ownership updates before skipping quarantined payloads, and append line-local unavailable ranges without invoking body parsing. On local failure, collect numeric/range evidence, clear the failed message slot, delete its pending builder, and continue the physical-line loop. On global failure, abandon all pending builders with typed diagnostics and collect remaining line locations only. At EOF, finalise pending diagnostics; compact complete message slots; sort diagnostic values after attachment of ranges.

The local-failure control-flow change is:

```go
// diagnostic is constructed from the pending body and its source locations.
diagnostics = append(diagnostics, diagnostic)
quarantined[comp] = len(diagnostics) - 1
delete(pending, comp)
// The current failed slot is omitted from the final completed-message list.
// The next physical line still updates active-entry ownership.
```

Keep `consume`'s existing grammar and UTF-8-after-assembly rule. It may expose consumed-range evidence for failure construction, but it must not attempt to repair JSON, scan for a later `{`, or consume unrelated stream bytes. The global detector is a small header-ownership check using existing split helpers, not a second message parser:

```go
func providerJSONOwnershipUnknown(line string) bool {
    _, rest, ok := splitTimestamp(line)
    if !ok { return false }
    _, rest = splitLevel(rest)
    if !strings.HasPrefix(strings.TrimLeft(rest, " \t"), "provider.") {
        return false
    }
    comp, _, header := providerJSONOuter(line)
    return !header || !strings.HasPrefix(comp, "provider.") || len(comp) == len("provider.")
}
```

- [ ] **Step 4: Run GREEN and descriptive scaling.** Run all logfmt tests, then `go test ./...`. Add `BenchmarkInspectProviderJSON` using `b.Run` for 1,000 and 10,000 groups of complete A, damaged B and later apparent B restart with distinct component keys; build input before `b.ResetTimer`, call `b.ReportAllocs`, and require exactly the expected retained/diagnostic counts outside the measured loop. Also measure repeated healthy A with one quarantined B to expose per-line tail copying. Run `go test ./internal/logfmt -run '^$' -bench BenchmarkInspectProviderJSON -benchmem -count=1`; report measurements without thresholds. Inspect that memory is proportional to source/range count and no failed body is retained behind quarantine state.

```go
func BenchmarkInspectProviderJSON(b *testing.B) {
    for _, distinct := range []bool{false, true} {
        for _, count := range []int{1000, 10000} {
            b.Run(fmt.Sprintf("distinct=%t/count=%d", distinct, count), func(b *testing.B) {
                var source strings.Builder
                for i := 0; i < count; i++ {
                    a, damaged := "a", "b"
                    if distinct { a, damaged = fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i) }
                    source.WriteString(providerRecord(a, `{"ok":1}`) + "\n")
                    source.WriteString(providerRecord(damaged, `{"bad":]}`) + "\n")
                    source.WriteString(providerRecord(damaged, `{"apparent":1}`) + "\n")
                }
                input := source.String()
                checked := InspectProviderJSON(input)
                wantDiagnostics := 1
                if distinct { wantDiagnostics = count }
                if len(checked.Messages) != count || len(checked.Diagnostics) != wantDiagnostics {
                    b.Fatal("invalid benchmark outcome")
                }
                b.ReportAllocs()
                b.ResetTimer()
                for i := 0; i < b.N; i++ { InspectProviderJSON(input) }
            })
        }
    }
}
```

The benchmark file imports `fmt`, `strings`, `testing`; `providerRecord` comes from the existing package tests.

- [ ] **Step 5: Review, cleanup and commit.** Independent review must check unsafe same-stream restart, ordinary continuation ownership, global-stop detection, range overlap and ordering. Run separate test cleanup. Signed commit: `Recover verified responses from independent provider streams`.

### Task 3: Prove strict scrub publication and prepare the viewer handover

**Files:** `internal/scrub/fragments_test.go`, `cmd/tfli/scrub_test.go`, optionally the strict call site in `internal/scrub/fragments.go`, this plan.

**Interfaces:** Consumes `logfmt.InspectProviderJSON`, `logfmt.ReconstructProviderJSON`, `scrub.Scrub(data []byte, extra []string) (Result, error)`, and existing `runScrub(inputPath, outputPath, valuesPath string, stderr io.Writer) error`. No new production API or viewer integration is required.

- [ ] **Step 1: Add strict-policy regressions using real input.** Cover good A/malformed B/good A, pending A/malformed B/completed A, complete A/incomplete A at EOF, invalid UTF-8, invalid JSON syntax, invalid suffix grammar, malformed inline UI, quarantined same-component restart and global ownership failure. Each fixture includes at least one independently verified body and a distinctive private sentinel. In the package test, first require `InspectProviderJSON` to retain a message and report a diagnostic, then require `Scrub` to return an error and `Result{}`. Assert diagnostics do not contain the sentinel, component names, or body fragments.

```go
partial := "2026-09-08T00:00:00.000Z [DEBUG] provider.a: {\"id\":\"private-sentinel\"}\n" +
    "2026-09-08T00:00:00.000Z [DEBUG] provider.b: {\"token\":]}\n" +
    "2026-09-08T00:00:00.000Z [DEBUG] provider.a: {\"ok\":true}\n"
outcome := logfmt.InspectProviderJSON(partial)
if len(outcome.Messages) != 2 || len(outcome.Diagnostics) == 0 {
    t.Fatal("fixture does not exercise partial recovery")
}
result, err := Scrub([]byte(partial), nil)
if err == nil || !reflect.DeepEqual(result, Result{}) {
    t.Fatalf("partial reconstruction produced scrub output: %#v, %v", result, err)
}
if strings.Contains(err.Error(), "private-sentinel") || strings.Contains(err.Error(), "provider.b") {
    t.Fatal("diagnostic disclosed source content")
}
```

In CLI tests, write those inputs into `t.TempDir`, call the actual `runScrub` and assert: returned error is safe, stderr is empty, absent output remains absent, and pre-existing output sentinel bytes remain unchanged. Do not replace `openScrubOutput`, reconstruction functions or the scrubber with mocks. Retain existing successful fragment round-trip, provider metadata, UTF-8, token-collision and privacy tests.

- [ ] **Step 2: Run the policy tests.** `go test ./internal/scrub ./cmd/tfli -run 'Test.*(Recovery|Partial|Fragment)' -count=1`. These may pass immediately because strict policy was retained in task 1; record them as regression coverage, not false RED evidence. If they fail, preserve that behavioural RED and make only the strict-gate correction before rerunning.
- [ ] **Step 3: Confirm the gate remains before transformation/publication.** The required call order is already explicit and must stay so:

```go
messages, err := logfmt.ReconstructProviderJSON(input)
if err != nil {
    return nil, nil, nil, "", err
}
// Build scrub views only from a diagnostic-free message set.
```

`runScrub` must continue to call `scrub.Scrub` before `openScrubOutput`. Neither valid recovered bodies nor prior inspection permits output. No permissive flag, diagnostic filtering, best-effort scrub result or API that switches parser grammar by caller.

- [ ] **Step 4: Complete final validation and review.** Run the commands below once on the final application tree and inspect all results. Obtain independent combined I1 review and separate cleanup, resolving actionable findings. In the execution record, enumerate each retention/ownership/publication requirement with evidence. Record actual signatures/types for I2. No TUI goldens or profile JSON schema changes belong here.
- [ ] **Step 5: Commit verified handover.** Signed commit: `Verify strict scrubbing of partially recovered captures`. Mark I1 complete only after evidence; leave Boundary I as a whole unimplemented until I2 is approved, implemented and reviewed. Integration remains Dan's decision.

## Final validation

```text
go test -race -count=1 ./...
go build ./...
gofmt -d .
go mod tidy -diff
go mod verify
golangci-lint run --timeout=5m
```

Mirror CI's four build targets locally. Run each command separately; these local builds do not constitute remote CI.

```text
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=amd64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/tfli-i1-linux-amd64 ./cmd/tfli
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=arm64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=linux GOARCH=arm64 go build -trimpath -o /tmp/tfli-i1-linux-arm64 ./cmd/tfli
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=amd64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=amd64 go build -trimpath -o /tmp/tfli-i1-darwin-amd64 ./cmd/tfli
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=arm64 go build ./...
env CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOOS=darwin GOARCH=arm64 go build -trimpath -o /tmp/tfli-i1-darwin-arm64 ./cmd/tfli
```

## Spec coverage and I2 handover

| Requirement | I1 evidence / downstream owner |
| --- | --- |
| Verified messages plus safe diagnostics | Task 1 typed outcomes and legacy strict diagnostic tests |
| One parser, exact component ownership | Tasks 1–2 shared grammar and ordinary continuation tests |
| Independent recovery, no same-stream restart | Task 2 local quarantine matrix |
| Global ownership failure / incomplete EOF | Task 2 explicit malformed-header and pending-state cases |
| Source mapping and deterministic ordering | Task 2 original-slice and repeated-outcome assertions |
| Invalid UTF-8 and inline UI isolation | Existing grammar suite plus task 2 mixed-failure tests |
| No scrub output from partial reconstruction | Task 3 package and actual filesystem publication tests |
| Position-specific viewer, raw fallback, navigation | I2, not implemented by this plan |
| Lazy complete/partial/failed quality status | I2, including any necessary profile JSON contract decision |

I2 must use the typed outcome without triggering reconstruction merely to draw a quality panel. It must distinguish verified fragment positions, unsuccessful-message ranges and later unavailable stream ranges, handle multiple messages within one logical entry, and preserve physical selection on return. No I1 API serialises this outcome as profile JSON or exposes message bodies through diagnostics.

## Draft self-review and baseline

Source inspection at `f614493` covered the parser's full state/grammar helpers,
strict error and inline-UI tests, scrub view construction/publication, model
consumer and CI workflow. Baseline `go test ./...` and `go build ./...` pass on
unchanged application code. These checks do not validate the proposed recovery.

The draft separates parser core from viewer policy, lists exact shared types,
and maps each item 8 requirement to I1 or I2. The proposed global-stop trigger
and diagnostic-range semantics require particular attention during plan review.
Self-review checked task dependencies against the declared interfaces, strict
rejection throughout intermediate commits, deterministic EOF ordering, all
diagnostic families in the publication matrix, and source-range ownership.
Relative links and code fences pass; no unfinished-value markers remain.
Implementation, independent code review and test cleanup remain future work.

## Plan review follow-up

Dan authorised both peer-review corrections on 11 September 2026. PAR-I1-1 is
resolved by explicit trigger-only and aborted-pending diagnostic shapes,
trigger-line inclusion and safe sorting for empty range lists. PAR-I1-2 is
resolved by literal structural-tail and global LF/CRLF boundary examples,
including the distinction between consumed-byte counts and diagnostic spans.

Scoped verification checked both findings against the revised contract and the
existing parser helpers, finding no collateral contradictions. Markdown links,
code fences, syntax of the three new Go examples and literal fixture offsets
were checked successfully. These are plan checks; no recovery implementation or
application test changes have been made. Implementation approval remains open.
