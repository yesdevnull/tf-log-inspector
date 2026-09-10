# Task 1 implementation report

Implemented module evidence retention for UI spans, attribution contexts and named attributions. Explicit root, absent and invalid module fields remain distinguishable. Invalid module metadata contributes one schema error while otherwise valid duration and context observations remain admitted.

Added conservative resource-address decomposition and module subtree membership in `internal/model/resource_address.go`. It recognises complete managed, data and ephemeral resource shapes; nested module instances; numeric and quoted keys; escaped quoted-key content; and Unicode names. Malformed or unsupported syntax remains unknown, and observed metadata wins only when valid and consistent with a recognised address.

Files changed:

- `internal/span/span.go`, `internal/span/uihook.go`, `internal/span/uihook_test.go`
- `internal/attrib/context.go`, `internal/attrib/context_test.go`, `internal/attrib/correlate.go`, `internal/attrib/correlate_test.go`
- `internal/model/resource_address.go`, `internal/model/resource_address_test.go`
- Task 1 execution evidence in the D1 plan, plus the controller's approved implementation-authorisation wording in the shared spec and D2 plan

RED command:

`go test ./internal/span ./internal/model -run 'Test(UI.*Module|ResourceModule)' -count=1`

Result: failed after declarations compiled. UI tests reported discarded module value/presence/invalidity and missing schema counts; model tests reported false membership and unknown decomposition. These were the intended missing behaviours.

GREEN commands:

- `go test ./internal/span ./internal/model -run 'Test(UI.*Module|ResourceModule)' -count=1` — passed
- `go test ./internal/span ./internal/model ./internal/attrib -count=1` — passed
- `go test ./...` — passed across all packages
- `/Users/dan/.codex/bin/codex-git diff --check` — passed

Self-review checked that source ordinals, reported duration, timestamp status, saturation, context pairing, candidate selection and confidence remain independent of module metadata. It also checked malformed supplied modules, observed/address conflicts, root and descendant boundaries, indexed module distinctions, partial addresses, trailing separators, raw controls and multiple index suffixes. No Task 1 concern remains. Independent review is intentionally pending for the controller.

## Round-one review fixes

Review found that plain address segments accepted spaces, leading digits and expression punctuation; quoted keys containing `[` were rejected; and the `Context` and `Attribution` module comments did not explain the evidence flags. Added behavioural tests before changing production code.

RED: `go test ./internal/model -run 'TestResourceModule(Rejects|Accepts)' -count=1` failed for `module.bad name`, `module.9name`, `module.bad+name`, a punctuated resource name, and `module.m["a[b"]`.

GREEN: the same command passed after applying conservative Unicode identifier validation with ASCII hyphen support and relying on numeric/JSON key validation to reject multiple index suffixes. Comments now state that `ModuleKnown` and `ModuleInvalid` distinguish root from unavailable metadata. The original 43 model cases remain intact.
