package model

import (
	"regexp"
	"strings"
)

// EventFilter selects observed event evidence. Set fields combine with AND.
type EventFilter struct {
	Query, Address, Severity string
	Kind                     EventKind
}

// ProgressGroup retains progress observations that can be assigned to one
// operation without guessing. Indices refer to the supplied event slice.
type ProgressGroup struct {
	Address, Action, Source, DeposedKey string
	Members                             []ResourceEvent
	Indices                             []int
	First, Last                         ResourceEvent
	LastElapsed                         string
}

// DiagnosticGroup combines exact repeats while retaining every observation.
type DiagnosticGroup struct {
	Severity, Address, Message, Source string
	Members                            []ResourceEvent
	Indices                            []int
}

// Milestone names a representative observation without manufacturing a clock.
type Milestone struct {
	Label string
	Event ResourceEvent
}

// FilterEvents returns events matching every set filter field in file order.
func FilterEvents(events []ResourceEvent, filter EventFilter) []ResourceEvent {
	var result []ResourceEvent
	for _, event := range events {
		if eventMatches(event, filter) {
			result = append(result, event)
		}
	}
	return result
}

func eventMatches(event ResourceEvent, filter EventFilter) bool {
	if filter.Address != "" && !strings.EqualFold(event.Address, filter.Address) {
		return false
	}
	if filter.Kind != "" && event.Kind != filter.Kind {
		return false
	}
	if filter.Severity != "" && !strings.EqualFold(event.Severity, filter.Severity) {
		return false
	}
	if filter.Query == "" {
		return true
	}
	query := strings.ToLower(filter.Query)
	for _, value := range []string{event.Address, event.Message, event.Action, event.Source, string(event.Kind), event.Severity} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

type progressOperation struct {
	group     int
	ambiguous bool
}

// GroupProgress groups only progress that has one unambiguous open operation.
func GroupProgress(events []ResourceEvent) []ProgressGroup {
	var groups []ProgressGroup
	open := make(map[string][]*progressOperation)
	for index, event := range events {
		if !lifecycleEvent(event.Kind) {
			continue
		}
		key := operationKey(event.Source, event.Address, event.Action, event.DeposedKey)
		switch event.Kind {
		case EventStart:
			operation := &progressOperation{group: -1}
			open[key] = append(open[key], operation)
			if len(open[key]) > 1 {
				for _, pending := range open[key] {
					pending.ambiguous = true
				}
			}
		case EventProgress:
			pending := open[key]
			if event.DeposedKey == "" && hasOpenDeposedOperation(open, event) {
				pending = nil
			}
			if len(pending) != 1 || pending[0].ambiguous {
				groups = append(groups, newProgressGroup(event, index))
				continue
			}
			operation := pending[0]
			if operation.group < 0 {
				groups = append(groups, newProgressGroup(event, index))
				operation.group = len(groups) - 1
			} else {
				group := &groups[operation.group]
				group.Members = append(group.Members, event)
				group.Indices = append(group.Indices, index)
				group.Last = event
				if elapsed := reportedElapsed(event.Message); elapsed != "" {
					group.LastElapsed = elapsed
				}
			}
		case EventComplete, EventError:
			if len(open[key]) == 1 && !open[key][0].ambiguous {
				delete(open, key)
			}
		}
	}
	return groups
}

func operationKey(source, address, action, deposedKey string) string {
	return source + "\x00" + address + "\x00" + action + "\x00" + deposedKey
}

func hasOpenDeposedOperation(open map[string][]*progressOperation, event ResourceEvent) bool {
	prefix := operationKey(event.Source, event.Address, event.Action, "")
	for key, pending := range open {
		if key != prefix && strings.HasPrefix(key, prefix) && len(pending) != 0 {
			return true
		}
	}
	return false
}

var elapsedPattern = regexp.MustCompile(`\[([^]]+ elapsed)\]`)

func newProgressGroup(event ResourceEvent, index int) ProgressGroup {
	return ProgressGroup{
		Address: event.Address, Action: event.Action, Source: event.Source, DeposedKey: event.DeposedKey,
		Members: []ResourceEvent{event}, Indices: []int{index}, First: event, Last: event,
		LastElapsed: reportedElapsed(event.Message),
	}
}

func reportedElapsed(message string) string {
	match := elapsedPattern.FindStringSubmatch(message)
	if match == nil {
		return ""
	}
	return match[1]
}

// FilterProgressGroups returns whole groups when any member matches.
func FilterProgressGroups(groups []ProgressGroup, filter EventFilter) []ProgressGroup {
	var result []ProgressGroup
	for _, group := range groups {
		for _, member := range group.Members {
			if eventMatches(member, filter) {
				result = append(result, group)
				break
			}
		}
	}
	return result
}

// GroupDiagnostics combines exact diagnostic repeats in first-observed order.
func GroupDiagnostics(events []ResourceEvent) []DiagnosticGroup {
	var groups []DiagnosticGroup
	indices := make(map[string]int)
	for index, event := range events {
		if event.Kind != EventDiagnostic {
			continue
		}
		key := event.Severity + "\x00" + event.Address + "\x00" + event.Message + "\x00" + event.Source
		groupIndex, found := indices[key]
		if !found {
			groups = append(groups, DiagnosticGroup{Severity: event.Severity, Address: event.Address, Message: event.Message, Source: event.Source})
			groupIndex = len(groups) - 1
			indices[key] = groupIndex
		}
		groups[groupIndex].Members = append(groups[groupIndex].Members, event)
		groups[groupIndex].Indices = append(groups[groupIndex].Indices, index)
	}
	return groups
}

// InvestigationMilestones selects representative observations in file order.
func InvestigationMilestones(events []ResourceEvent) []Milestone {
	var milestones []Milestone
	activitySeen, plannedSeen, driftSeen, diagnosticSeen := false, false, false, false
	for _, event := range events {
		label := ""
		switch {
		case !activitySeen && lifecycleEvent(event.Kind) && (event.Action == "refresh" || event.Action == "read"):
			label, activitySeen = "First refresh/read activity", true
		case !plannedSeen && event.Kind == EventPlannedChange:
			label, plannedSeen = "Planned changes", true
		case !driftSeen && event.Kind == EventDrift:
			label, driftSeen = "Drift", true
		case !diagnosticSeen && event.Kind == EventDiagnostic:
			label, diagnosticSeen = "Diagnostic", true
		case event.Kind == EventChangeSummary:
			label = "Summary"
		}
		if label != "" {
			milestones = append(milestones, Milestone{Label: label, Event: event})
		}
	}
	return milestones
}
