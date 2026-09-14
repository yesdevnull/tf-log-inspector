package model

import (
	"sort"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// DurationTotal summarises a set of admitted duration observations.
type DurationTotal struct {
	Count      uint64
	TotalMs    uint64
	MaxMs      uint32
	LowerBound bool
}

// MeanMs is the arithmetic mean of observed durations, zero for no observations.
// A saturated observation makes this a lower bound, as recorded by LowerBound.
func (d DurationTotal) MeanMs() float64 {
	if d.Count == 0 {
		return 0
	}
	return float64(d.TotalMs) / float64(d.Count)
}

// ModuleRow groups operations by their exact module instance, including root
// (Known with an empty path) and unavailable module evidence (not Known).
type ModuleRow struct {
	Module  ResourceModule
	UI      DurationTotal
	Sources map[span.DurationSource]DurationTotal
}

// GroupResourcesByModule ranks the selected observations without counting
// parent modules again or adding overlapping RPC durations.
func GroupResourcesByModule(l *Log, index ResourceIndex, projection ResourceProjection) []ModuleRow {
	groups := make(map[ResourceModule]*ModuleRow)
	for _, i := range projection.UIIndices {
		s := l.UISpans[i]
		module := index.Operations[i].Module
		group := groups[module]
		if group == nil {
			group = &ModuleRow{Module: module, Sources: make(map[span.DurationSource]DurationTotal)}
			groups[module] = group
		}
		group.UI.add(s)
		source := group.Sources[s.DurationSource]
		source.add(s)
		group.Sources[s.DurationSource] = source
	}
	rows := make([]ModuleRow, 0, len(groups))
	for _, group := range groups {
		rows = append(rows, *group)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UI.TotalMs != rows[j].UI.TotalMs {
			return rows[i].UI.TotalMs > rows[j].UI.TotalMs
		}
		if rows[i].Module.Known != rows[j].Module.Known {
			return rows[i].Module.Known
		}
		return rows[i].Module.Path < rows[j].Module.Path
	})
	return rows
}

func (d *DurationTotal) add(s span.Span) {
	d.Count++
	d.TotalMs += uint64(s.DurationMs)
	if s.DurationMs > d.MaxMs {
		d.MaxMs = s.DurationMs
	}
	d.LowerBound = d.LowerBound || s.DurationSaturated
}

// ResourceOperation identifies one UI-hook operation by its original index.
type ResourceOperation struct {
	UIIndex int
	Module  ResourceModule
}

// ResourceRow summarises all observed duration evidence for one exact address.
type ResourceRow struct {
	Address        string
	Operations     []ResourceOperation
	UI             DurationTotal
	NamedRPC       DurationTotal
	OverlappingRPC DurationTotal
}

// ResourceChoice is one selectable exact resource address and its module.
type ResourceChoice struct {
	Address string
	Module  ResourceModule
}

// ResourceIndex retains source indices and reusable resource identities for one Log.
type ResourceIndex struct {
	Operations []ResourceOperation
	RPCModules []ResourceModule
	Choices    []ResourceChoice
	Modules    []string
}

// BuildResourceIndex builds the resource identities observed in one loaded log.
func BuildResourceIndex(l *Log) ResourceIndex {
	if l == nil {
		return ResourceIndex{}
	}

	index := ResourceIndex{
		Operations: make([]ResourceOperation, 0, len(l.UISpans)),
		RPCModules: make([]ResourceModule, len(l.RPCSpans)),
	}
	choices := make(map[string]choiceModule)
	modules := make(map[string]struct{})

	for i, s := range l.UISpans {
		module, unavailable := moduleFromEvidence(s.Address, s.Module, s.ModuleKnown, s.ModuleInvalid)
		index.Operations = append(index.Operations, ResourceOperation{UIIndex: i, Module: module})
		addKnownModule(modules, module)
		addChoice(choices, s.Address, module, unavailable)
	}

	for _, c := range l.Contexts {
		module, unavailable := moduleFromEvidence(c.Address, c.Module, c.ModuleKnown, c.ModuleInvalid)
		addKnownModule(modules, module)
		addChoice(choices, c.Address, module, unavailable)
	}

	for i := range l.RPCSpans {
		if i >= len(l.Attribs) || !namedConfidence(l.Attribs[i].Confidence) || l.Attribs[i].Address == "" {
			continue
		}
		a := l.Attribs[i]
		module, unavailable := moduleFromEvidence(a.Address, a.Module, a.ModuleKnown, a.ModuleInvalid)
		index.RPCModules[i] = module
		addKnownModule(modules, module)
		addChoice(choices, a.Address, module, unavailable)
	}

	addresses := make([]string, 0, len(choices))
	for address := range choices {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	index.Choices = make([]ResourceChoice, 0, len(addresses))
	for _, address := range addresses {
		fact := choices[address]
		module := fact.module
		if fact.unavailable || fact.conflict {
			module = ResourceModule{}
		}
		index.Choices = append(index.Choices, ResourceChoice{Address: address, Module: module})
	}

	index.Modules = make([]string, 0, len(modules))
	for module := range modules {
		index.Modules = append(index.Modules, module)
	}
	sort.Strings(index.Modules)
	return index
}

type choiceModule struct {
	module ResourceModule
	// unavailable records supplied evidence that could not be reconciled.
	// It stays sticky so later evidence cannot make a contradiction disappear.
	unavailable bool
	conflict    bool
}

func moduleFromEvidence(address, observed string, observedKnown, invalid bool) (ResourceModule, bool) {
	if invalid {
		return ResourceModule{}, true
	}
	module := ResolveResourceModule(address, observed, observedKnown)
	return module, observedKnown && !module.Known
}

func namedConfidence(confidence attrib.Confidence) bool {
	switch confidence {
	case attrib.Contained, attrib.Likely, attrib.Overlapping:
		return true
	default:
		return false
	}
}

func addChoice(choices map[string]choiceModule, address string, module ResourceModule, unavailable bool) {
	if address == "" {
		return
	}
	fact := choices[address]
	fact.unavailable = fact.unavailable || unavailable
	if module.Known {
		if fact.module.Known && fact.module.Path != module.Path {
			fact.conflict = true
		} else if !fact.module.Known {
			fact.module = module
		}
	}
	choices[address] = fact
}

func addKnownModule(modules map[string]struct{}, module ResourceModule) {
	if !module.Known {
		return
	}
	modules[""] = struct{}{}
	segments, ok := moduleSegments(module.Path)
	if !ok {
		return
	}
	for i := 2; i <= len(segments); i += 2 {
		modules[strings.Join(segments[:i], ".")] = struct{}{}
	}
}
