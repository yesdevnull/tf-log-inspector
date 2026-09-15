# Task 1 report: evidence projections and CLI diagnostics

## Result

Implemented conservative CLI diagnostic extraction and pure investigation projections in `internal/model`.

CLI diagnostics require a `Warning: ` or `Error: ` heading, a recognised `with <address>,` line whose address passes `resourceaddr.Parse`, and CLI rather than provider ownership. Boxed and unboxed diagnostics retain their original physical byte and line ranges. Diagnostic content remains untrusted display text in `ResourceEvent.Message`; consumers must escape it as documented on `ResourceEvent`.

Progress attribution runs over the complete event stream before any filtering. Groups carry every member plus its original input index and source location. Repeated starts and CLI deposed ambiguity leave progress observations as singleton groups.

## Public helper APIs

```go
type EventFilter struct {
    Query, Address, Severity string
    Kind                     EventKind
}

type ProgressGroup struct {
    Address, Action, Source, DeposedKey string
    Members                            []ResourceEvent
    Indices                            []int
    First, Last                        ResourceEvent
    LastElapsed                        string
}

type DiagnosticGroup struct {
    Severity, Address, Message, Source string
    Members                            []ResourceEvent
    Indices                            []int
}

type Milestone struct {
    Label string
    Event ResourceEvent
}

func FilterEvents(events []ResourceEvent, filter EventFilter) []ResourceEvent
func GroupProgress(events []ResourceEvent) []ProgressGroup
func FilterProgressGroups(groups []ProgressGroup, filter EventFilter) []ProgressGroup
func GroupDiagnostics(events []ResourceEvent) []DiagnosticGroup
func InvestigationMilestones(events []ResourceEvent) []Milestone
```

`EventFilter.Query` is a case-insensitive literal search across address, message, action, source, kind and severity. Set filter fields combine with AND. `FilterProgressGroups` returns a whole already-attributed group when any member matches, so a hidden earlier message remains searchable without changing operation boundaries.

`InvestigationMilestones` emits, in observed file order, the first refresh/read lifecycle observation, the first planned change, the first drift event, the first diagnostic, and every change summary. Each milestone retains its representative `ResourceEvent`; it does not infer a phase, timestamp or completion.

## TDD evidence

Initial focused red run:

```text
$ go test ./internal/model -run 'Test.*(Diagnostic|Investigation|ProgressGroup|Milestone)'
# github.com/yesdevnull/tf-log-inspector/internal/model [github.com/yesdevnull/tf-log-inspector/internal/model.test]
internal/model/investigation_test.go:15:9: undefined: FilterEvents
internal/model/investigation_test.go:15:30: undefined: EventFilter
internal/model/investigation_test.go:19:12: undefined: FilterEvents
internal/model/investigation_test.go:35:9: undefined: GroupProgress
internal/model/investigation_test.go:45:14: undefined: FilterProgressGroups
internal/model/investigation_test.go:78:9: undefined: GroupDiagnostics
FAIL github.com/yesdevnull/tf-log-inspector/internal/model [build failed]
```

The first implementation made the focused suite green:

```text
ok github.com/yesdevnull/tf-log-inspector/internal/model 0.323s
```

A milestone mutation test then added repeated drift, planned-change and diagnostic evidence. It failed because nine milestones were emitted instead of the required six representative observations. Adding first-observation gates made the focused suite green.

A runner-owned boxed diagnostic test then failed with no diagnostic because the opening rule included the runner prefix. Recognising an owned line ending in `: ╷`, while retaining the provider-owner rejection, made the focused suite green.

Final verification before commit:

```text
$ go test ./internal/model -run 'Test.*(Diagnostic|Investigation|ProgressGroup|Milestone)'
ok github.com/yesdevnull/tf-log-inspector/internal/model 0.241s

$ go test ./internal/model
ok github.com/yesdevnull/tf-log-inspector/internal/model 0.236s

$ go vet ./internal/model
(no output)
```

## Concerns

The milestone labels are model-owned display strings. Later consumers should use them directly or introduce typed milestone kinds before requiring alternate wording; matching these labels with ad hoc string comparisons would make presentation changes brittle.

CLI lifecycle lines owned by a timestamped runner entry remain excluded by the pre-existing ownership policy. This task does not change that policy; diagnostic extraction only ensures a diagnostic block itself does not consume independently admissible lifecycle or summary evidence.

## Commit

- `f3124721afe00367b396e8bec1e4cc7e9adb2c6b` — `Add investigation evidence projections`
- The task report is committed separately so it can record the implementation commit exactly.
