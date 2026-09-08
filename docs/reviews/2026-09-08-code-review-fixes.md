# Code Review Fixes

The 8 September 2026 review examined the codebase at `6b44a72`. All ten
findings are addressed on `fix/code-review-findings`.

| Finding | Resolution | Regression coverage |
| --- | --- | --- |
| Terminal controls in decoded log fields | Escape controls at display boundaries, preserving underlying identifiers. Apply the same protection to raw lines, filenames, diagnostic reports, search prompts and CLI errors. | Real structured-log loading through TUI/profile rendering; actual CLI subprocesses with hostile arguments; table, facet and raw-log output. |
| Report output overwrites its source | Open output without truncation, compare file identities, and reject an alias before writing. | Both modes preserve the input for identical paths, hard links and symlinks. |
| Mutation targets cannot be restored | Validate every target as a tracked regular file within the checkout before running any mutation. | Untracked files, outside paths and symlinks are rejected without damage. |
| Valid action hooks rejected | Decode envelopes independently of resource-hook payloads, retaining action timestamps and type counts without adding action spans. | All four action lifecycle types extend open contexts; action timestamps establish the UI baseline. |
| Raw filters hide earlier matches | Reconcile the raw cursor against admitted entries, searching backwards when no later match remains. Reconcile again when widening a call scope. | Earlier and later matches, scoped filtering, genuine empty results and scope widening. |
| Resizing resurrects facet overlay | Clear overlay state when facets become inline, then repair focus. | Narrow/open/wide/focus/narrow sequence retains the list. |
| Interrupted mutations remain applied | Register exit and signal cleanup that restores and verifies the active target. | Interrupt a process group after a real mutated Go test starts; original source is restored. |
| Infrastructure errors count as caught mutations | Require a passing baseline before mutation and recheck restored source after unsuccessful mutants. | Invalid package selection and loss of the real Go executable fail the run rather than reporting a caught mutation. |
| Timestamp offsets wrap | Reject offsets greater than `MaxUint32` milliseconds before conversion. | Exact maximum accepted; one millisecond beyond rejected without emitting the wrapped entry. |
| Saturation evidence is lost | Retain `UISaturatedDurations` in the model and warn in the profile and TUI. | Load oversized durations and check visible warnings; ordinary durations remain unqualified. |

## Verification

Each fix began with an observed failing regression test. Independent reviewers
checked fixes they did not author. The separate test-cleanup passes retained
the tests because they protect distinct behaviours, boundaries and security
contracts; no coverage was removed. Existing terminal golden files remain
unchanged.

Final checks:

- `go build ./...`
- `go test -race ./...`
- `go vet ./...`
- `bash -n scripts/mutate.sh`
- Git whitespace checks and commit-signature verification.

Mutation restoration covers catchable signals, not `SIGKILL` or power loss.
Baseline controls detect persistent infrastructure failures; they cannot
conclusively attribute transient failures that disappear on the control run.
Terminal-security tests inspect emitted bytes without executing clipboard
commands in a live terminal.
