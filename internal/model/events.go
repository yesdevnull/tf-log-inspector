package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/resourceaddr"
)

type EventKind string

const (
	EventStart         EventKind = "start"
	EventProgress      EventKind = "progress"
	EventComplete      EventKind = "complete"
	EventError         EventKind = "error"
	EventDiagnostic    EventKind = "diagnostic"
	EventDrift         EventKind = "drift"
	EventPlannedChange EventKind = "planned_change"
	EventChangeSummary EventKind = "change_summary"
)

// ResourceEvent retains observed evidence in file order. Timestamp is zero
// when no valid clock was supplied; Location names one original physical line.
// Message is untrusted log text and must be escaped by display consumers.
type ResourceEvent struct {
	Kind                                       EventKind
	Address, Action, Message, Severity, Source string
	DeposedKey                                 string
	Timestamp                                  time.Time
	Location                                   SourceLocation
	Summary                                    *ChangeSummary
}

// ChangeSummary preserves field absence separately from observed zero counts.
type ChangeSummary struct {
	Operation        string  `json:"operation"`
	Add              *uint64 `json:"add"`
	Change           *uint64 `json:"change"`
	Remove           *uint64 `json:"remove"`
	Import           *uint64 `json:"import"`
	ActionInvocation *uint64 `json:"action_invocation"`
}

// OutcomeEvidence distinguishes observed changes, drift and diagnostics.
// Empty collections mean unavailable evidence, not a successful or empty plan.
type OutcomeEvidence struct {
	PlannedChanges, Drift, Diagnostics, Summaries []ResourceEvent
}

// IncompleteOperation is a start without an unambiguous observed ending.
// Ambiguous starts cannot safely own subsequent progress or completion events.
type IncompleteOperation struct {
	Start        ResourceEvent
	LastProgress *ResourceEvent
	Ambiguous    bool
}

func (l *Log) indexEvents() {
	structuredLifecycle := false
	var candidates []ResourceEvent
	starts := l.indexSourceLines()
	for i, start := range starts {
		end := uint64(len(l.Data))
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		line := strings.TrimRight(string(l.Data[start:end]), "\r\n")
		line, _ = logfmt.StripANSI(line, nil)
		var e ResourceEvent
		var ok bool
		if strings.HasPrefix(line, "{") {
			e, ok = structuredEvent(line)
		} else {
			e, ok = cliEvent(line)
		}
		if !ok {
			continue
		}
		position, found := l.SourcePosition(uint64(i + 1))
		if !found {
			continue
		}
		// Provider payloads can resemble lifecycle and plan output. Runner
		// headers may own genuine CLI summaries, so preserve those outcomes.
		if owner := l.Entries[position.Entry]; e.Source == "cli" && owner.Timestamped && owner.Level != logfmt.LevelUnknown {
			if lifecycleEvent(e.Kind) || strings.HasPrefix(l.Comps.Lookup(owner.Comp), "provider.") {
				continue
			}
		}
		e.Location = SourceLocation{Entry: position.Entry, StartByte: start, EndByte: end, StartLine: uint64(i + 1), EndLine: uint64(i + 1)}
		candidates = append(candidates, e)
		if e.Source == "ui" && lifecycleEvent(e.Kind) {
			structuredLifecycle = true
		}
	}
	for _, e := range candidates {
		// Mixed captures can contain a second rendering of the same lifecycle.
		// The structured stream is authoritative, matching timing admission.
		if e.Source == "cli" && lifecycleEvent(e.Kind) && structuredLifecycle {
			continue
		}
		l.Events = append(l.Events, e)
		switch e.Kind {
		case EventPlannedChange:
			l.Outcomes.PlannedChanges = append(l.Outcomes.PlannedChanges, e)
		case EventDrift:
			l.Outcomes.Drift = append(l.Outcomes.Drift, e)
		case EventDiagnostic:
			l.Outcomes.Diagnostics = append(l.Outcomes.Diagnostics, e)
		case EventChangeSummary:
			l.Outcomes.Summaries = append(l.Outcomes.Summaries, e)
		}
	}
	l.Incomplete = incompleteOperations(l.Events)
}

func lifecycleEvent(kind EventKind) bool {
	return kind == EventStart || kind == EventProgress || kind == EventComplete || kind == EventError
}

func structuredEvent(line string) (ResourceEvent, bool) {
	var wire struct {
		Level     string `json:"@level"`
		Message   string `json:"@message"`
		Timestamp string `json:"@timestamp"`
		Type      string `json:"type"`
		Hook      *struct {
			Resource *struct {
				Address string `json:"addr"`
			} `json:"resource"`
			Action string `json:"action"`
		} `json:"hook"`
		Change *struct {
			Resource *struct {
				Address string `json:"addr"`
			} `json:"resource"`
			Action string `json:"action"`
		} `json:"change"`
		Diagnostic *struct{ Address, Severity, Summary, Detail string } `json:"diagnostic"`
		Changes    *ChangeSummary                                       `json:"changes"`
	}
	if json.Unmarshal([]byte(line), &wire) != nil || wire.Level == "" {
		return ResourceEvent{}, false
	}
	e := ResourceEvent{Source: "ui", Message: wire.Message, Severity: wire.Level}
	e.Timestamp, _ = time.Parse(time.RFC3339Nano, wire.Timestamp)
	switch wire.Type {
	case "apply_start", "refresh_start", "provision_start":
		e.Kind = EventStart
	case "apply_progress", "refresh_progress", "provision_progress":
		e.Kind = EventProgress
	case "apply_complete", "refresh_complete", "provision_complete":
		e.Kind = EventComplete
	case "apply_errored", "refresh_errored", "provision_errored":
		e.Kind = EventError
	case "planned_change":
		e.Kind = EventPlannedChange
	case "resource_drift":
		e.Kind = EventDrift
	case "diagnostic":
		e.Kind = EventDiagnostic
	case "change_summary":
		e.Kind = EventChangeSummary
	default:
		return ResourceEvent{}, false
	}
	if lifecycleEvent(e.Kind) {
		if wire.Hook == nil || wire.Hook.Resource == nil || wire.Hook.Resource.Address == "" {
			return ResourceEvent{}, false
		}
		e.Address, e.Action = wire.Hook.Resource.Address, wire.Hook.Action
		if strings.HasPrefix(wire.Type, "refresh_") {
			e.Action = "refresh"
		}
		if strings.HasPrefix(wire.Type, "provision_") {
			e.Action = "provision"
		}
	}
	switch e.Kind {
	case EventPlannedChange, EventDrift:
		if wire.Change == nil || wire.Change.Resource == nil || wire.Change.Resource.Address == "" {
			return ResourceEvent{}, false
		}
		e.Address, e.Action = wire.Change.Resource.Address, wire.Change.Action
	case EventDiagnostic:
		if wire.Diagnostic == nil {
			return ResourceEvent{}, false
		}
		e.Address, e.Severity = wire.Diagnostic.Address, wire.Diagnostic.Severity
		e.Message = strings.TrimSpace(wire.Diagnostic.Summary + "\n" + wire.Diagnostic.Detail)
	case EventChangeSummary:
		if wire.Changes == nil || (wire.Changes.Add == nil && wire.Changes.Change == nil && wire.Changes.Remove == nil && wire.Changes.Import == nil && wire.Changes.ActionInvocation == nil) {
			return ResourceEvent{}, false
		}
		e.Summary = wire.Changes
		if e.Message == "" {
			raw, _ := json.Marshal(wire.Changes)
			e.Message = string(raw)
		}
	}
	if e.Message == "" {
		e.Message = wire.Type
	}
	return e, true
}

// Scrubbed IDs can lose their closing bracket. The address and lifecycle verb
// establish the event; opaque bracket payloads do not contribute evidence.
var cliLifecyclePattern = regexp.MustCompile(`^((?:[^"[:space:]]|"(?:[^"\\]|\\.)*")+): (Reading|Creating|Modifying|Destroying|Refreshing state|Still reading|Still creating|Still modifying|Still destroying)\.\.\.(?: \[.*)?$`)

var cliPlanSummaryPattern = regexp.MustCompile(`^Plan: (?:([0-9]+) to import, )?([0-9]+) to add, ([0-9]+) to change, ([0-9]+) to destroy\.(?: Actions: ([0-9]+) to invoke\.)?$`)

var cliDeposedPattern = regexp.MustCompile(`^((?:[^"[:space:]]|"(?:[^"\\]|\\.)*")+) \(deposed object ([0-9a-f]{8})\): `)

var cliPlannedChangePattern = regexp.MustCompile(`^[ \t]*# (.+) (will be created|will be updated in-place|will be destroyed|must be replaced|will be read during apply)$`)

func cliEvent(line string) (ResourceEvent, bool) {
	e := ResourceEvent{Source: "cli", Message: line}
	if parts := cliPlannedChangePattern.FindStringSubmatch(line); parts != nil {
		e.Kind, e.Address = EventPlannedChange, parts[1]
		e.Action = map[string]string{"will be created": "create", "will be updated in-place": "update", "will be destroyed": "delete", "must be replaced": "replace", "will be read during apply": "read"}[parts[2]]
		_, valid := resourceaddr.Parse(e.Address)
		return e, valid
	}
	if line == "No changes. Your infrastructure matches the configuration." {
		add, change, remove := uint64(0), uint64(0), uint64(0)
		e.Kind, e.Summary = EventChangeSummary, &ChangeSummary{Operation: "plan", Add: &add, Change: &change, Remove: &remove}
		return e, true
	}
	if parts := cliPlanSummaryPattern.FindStringSubmatch(line); parts != nil {
		counts := [5]*uint64{}
		for i := range counts {
			if parts[i+1] == "" {
				continue
			}
			n, err := strconv.ParseUint(parts[i+1], 10, 64)
			if err != nil {
				return ResourceEvent{}, false
			}
			counts[i] = &n
		}
		e.Kind = EventChangeSummary
		e.Summary = &ChangeSummary{Operation: "plan", Import: counts[0], Add: counts[1], Change: counts[2], Remove: counts[3], ActionInvocation: counts[4]}
		return e, true
	}
	if parts := cliDeposedPattern.FindStringSubmatch(line); parts != nil {
		e.DeposedKey = parts[2]
		line = parts[1] + ": " + line[len(parts[0]):]
	}
	address, action, _, complete := logfmt.CLICompletionParts(line)
	if complete {
		e.Kind = EventComplete
		e.Address = address
		e.Action = map[string]string{"Read": "read", "Creation": "create", "Modifications": "update", "Destruction": "delete", "Refresh": "refresh"}[action]
	} else {
		parts := cliLifecyclePattern.FindStringSubmatch(line)
		if parts == nil {
			return ResourceEvent{}, false
		}
		e.Address, action, e.Kind = parts[1], parts[2], EventStart
		if strings.HasPrefix(action, "Still ") {
			action = strings.TrimPrefix(action, "Still ")
			e.Kind = EventProgress
		}
		e.Action = map[string]string{"reading": "read", "creating": "create", "modifying": "update", "destroying": "delete", "refreshing state": "refresh"}[strings.ToLower(action)]
	}
	_, valid := resourceaddr.Parse(e.Address)
	return e, valid
}

func incompleteOperations(events []ResourceEvent) []IncompleteOperation {
	var operations []IncompleteOperation
	open := make(map[string][]int)
	closed := make(map[int]bool)
	for _, e := range events {
		if !lifecycleEvent(e.Kind) || (e.Source == "cli" && e.Action == "refresh") {
			continue
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", e.Source, e.Address, e.Action, e.DeposedKey)
		pending := open[key]
		switch e.Kind {
		case EventStart:
			operations = append(operations, IncompleteOperation{Start: e})
			pending = append(pending, len(operations)-1)
			if len(pending) > 1 {
				for _, i := range pending {
					operations[i].Ambiguous = true
					operations[i].LastProgress = nil
				}
			}
			open[key] = pending
		case EventProgress:
			// CLI progress omits deposed identity, so it cannot name an
			// owner while any deposed object for this operation is open.
			deposedOpen := false
			if e.Source == "cli" && e.DeposedKey == "" {
				for _, indices := range open {
					for _, i := range indices {
						start := operations[i].Start
						if start.Source == e.Source && start.Address == e.Address && start.Action == e.Action && start.DeposedKey != "" {
							deposedOpen = true
						}
					}
				}
			}
			if len(pending) == 1 && !operations[pending[0]].Ambiguous && !deposedOpen {
				progress := e
				operations[pending[0]].LastProgress = &progress
			}
		case EventComplete, EventError:
			if len(pending) == 1 && !operations[pending[0]].Ambiguous {
				closed[pending[0]] = true
				delete(open, key)
			}
		}
	}
	var result []IncompleteOperation
	for i, op := range operations {
		if !closed[i] {
			result = append(result, op)
		}
	}
	return result
}
