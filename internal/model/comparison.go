package model

import (
	"slices"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

type ComparisonInput struct{ RPC, UI []span.Span }
type SignedChange struct {
	Negative  bool
	Magnitude uint64
}
type ComparisonTotal struct {
	Count, TotalMs uint64
	MeanMs         *float64
	MaxMs          *uint32
	LowerBound     bool
}
type ComparisonChanges struct {
	Count, TotalMs, MaxMs                               *SignedChange
	MeanMs                                              *float64
	CountPercent, TotalPercent, MeanPercent, MaxPercent *float64
}
type ComparisonKey struct {
	Provider, ResourceType, Method, Address, Action string
	DurationSource                                  span.DurationSource
}
type ComparisonRow struct {
	Key           ComparisonKey
	State         string
	Before, After *ComparisonTotal
	Changes       ComparisonChanges
}
type ComparisonSection struct {
	Kind, Tier                      string
	BeforeAvailable, AfterAvailable bool
	Rows                            []ComparisonRow
}
type Comparison struct {
	Sections                        []ComparisonSection
	BeforeUnnamedUI, AfterUnnamedUI *ComparisonTotal
	BeforeProviders, AfterProviders []string
	ProviderIdentityStatus          string
}

type comparisonKeyFields uint8

const (
	keyProvider comparisonKeyFields = iota
	keyResourceType
	keyRPCMethod
	keyUIResourceType
	keyUIOperation
)

type comparisonSectionSpec struct {
	kind, tier string
	fields     comparisonKeyFields
}

var comparisonSectionSpecs = [...]comparisonSectionSpec{
	{"rpc_providers", "rpc", keyProvider},
	{"rpc_resource_types", "rpc", keyResourceType},
	{"rpc_methods", "rpc", keyRPCMethod},
	{"ui_resource_types", "ui", keyUIResourceType},
	{"ui_operations", "ui", keyUIOperation},
}

func Compare(before, after ComparisonInput) (Comparison, error) {
	beforeAggregates, err := aggregateComparisonInput(before)
	if err != nil {
		return Comparison{}, err
	}
	afterAggregates, err := aggregateComparisonInput(after)
	if err != nil {
		return Comparison{}, err
	}
	result := Comparison{
		Sections:        make([]ComparisonSection, 0, len(comparisonSectionSpecs)),
		BeforeProviders: providers(before.RPC), AfterProviders: providers(after.RPC),
	}
	result.ProviderIdentityStatus = providerIdentityStatus(before.RPC, after.RPC, result.BeforeProviders, result.AfterProviders)
	result.BeforeUnnamedUI = beforeAggregates.unnamedUI
	result.AfterUnnamedUI = afterAggregates.unnamedUI
	for index, spec := range comparisonSectionSpecs {
		beforeSpans, afterSpans := tierSpans(spec.tier, before), tierSpans(spec.tier, after)
		section := compareSection(spec, beforeSpans, afterSpans, beforeAggregates.sections[index], afterAggregates.sections[index])
		result.Sections = append(result.Sections, section)
	}
	return result, nil
}

func tierSpans(tier string, input ComparisonInput) []span.Span {
	if tier == "rpc" {
		return input.RPC
	}
	return input.UI
}

func compareSection(spec comparisonSectionSpec, before, after []span.Span, beforeGroups, afterGroups map[ComparisonKey]ComparisonTotal) ComparisonSection {
	section := ComparisonSection{
		Kind: spec.kind, Tier: spec.tier, BeforeAvailable: len(before) > 0,
		AfterAvailable: len(after) > 0, Rows: []ComparisonRow{},
	}
	keys := make(map[ComparisonKey]struct{}, len(beforeGroups)+len(afterGroups))
	for key := range beforeGroups {
		keys[key] = struct{}{}
	}
	for key := range afterGroups {
		keys[key] = struct{}{}
	}
	beforeSources, afterSources := durationSourceSet(before), durationSourceSet(after)
	for key := range keys {
		beforeTotal, beforeFound := beforeGroups[key]
		afterTotal, afterFound := afterGroups[key]
		row := ComparisonRow{Key: key}
		beforeAvailable, afterAvailable := section.BeforeAvailable, section.AfterAvailable
		if spec.tier == "ui" {
			beforeAvailable = beforeSources[key.DurationSource]
			afterAvailable = afterSources[key.DurationSource]
		}
		if !beforeAvailable || !afterAvailable {
			row.State = "unavailable"
			if beforeAvailable {
				row.Before = totalForPresence(beforeTotal, beforeFound)
			}
			if afterAvailable {
				row.After = totalForPresence(afterTotal, afterFound)
			}
		} else {
			row.Before = totalForPresence(beforeTotal, beforeFound)
			row.After = totalForPresence(afterTotal, afterFound)
			switch {
			case beforeFound && afterFound:
				row.State = "matched"
			case afterFound:
				row.State = "added"
			default:
				row.State = "removed"
			}
			row.Changes = comparisonChanges(*row.Before, *row.After)
		}
		section.Rows = append(section.Rows, row)
	}
	sortComparisonRows(section.Rows)
	return section
}

func durationSourceSet(observations []span.Span) map[span.DurationSource]bool {
	sources := make(map[span.DurationSource]bool)
	for _, observation := range observations {
		sources[observation.DurationSource] = true
	}
	return sources
}

type comparisonAggregates struct {
	sections  [5]map[ComparisonKey]ComparisonTotal
	unnamedUI *ComparisonTotal
}

func aggregateComparisonInput(input ComparisonInput) (comparisonAggregates, error) {
	var result comparisonAggregates
	for index := range result.sections {
		result.sections[index] = make(map[ComparisonKey]ComparisonTotal)
	}
	for _, observation := range input.RPC {
		for index := 0; index < 3; index++ {
			key, _ := comparisonKeyForSpan(observation, comparisonSectionSpecs[index].fields)
			if err := addComparisonObservation(result.sections[index], key, observation); err != nil {
				return comparisonAggregates{}, err
			}
		}
	}
	var unnamed ComparisonTotal
	for _, observation := range input.UI {
		key, _ := comparisonKeyForSpan(observation, keyUIResourceType)
		if err := addComparisonObservation(result.sections[3], key, observation); err != nil {
			return comparisonAggregates{}, err
		}
		if key, admitted := comparisonKeyForSpan(observation, keyUIOperation); admitted {
			if err := addComparisonObservation(result.sections[4], key, observation); err != nil {
				return comparisonAggregates{}, err
			}
		} else if err := addComparisonTotalObservation(&unnamed, observation); err != nil {
			return comparisonAggregates{}, err
		}
	}
	for _, groups := range result.sections {
		for key, total := range groups {
			total.MeanMs = float64Pointer(float64(total.TotalMs) / float64(total.Count))
			groups[key] = total
		}
	}
	if len(input.UI) > 0 {
		if unnamed.Count > 0 {
			unnamed.MeanMs = float64Pointer(float64(unnamed.TotalMs) / float64(unnamed.Count))
		}
		result.unnamedUI = cloneComparisonTotal(unnamed)
	}
	return result, nil
}

func addComparisonObservation(groups map[ComparisonKey]ComparisonTotal, key ComparisonKey, observation span.Span) error {
	total := groups[key]
	if err := addComparisonTotalObservation(&total, observation); err != nil {
		return err
	}
	groups[key] = total
	return nil
}

func addComparisonTotalObservation(total *ComparisonTotal, observation span.Span) error {
	var err error
	total.TotalMs, err = addDuration(total.TotalMs, observation.DurationMs)
	if err != nil {
		return err
	}
	total.Count++
	total.LowerBound = total.LowerBound || observation.DurationSaturated
	if total.MaxMs == nil {
		total.MaxMs = comparisonUint32Pointer(observation.DurationMs)
	} else if observation.DurationMs > *total.MaxMs {
		*total.MaxMs = observation.DurationMs
	}
	return nil
}

func comparisonKeyForSpan(observation span.Span, fields comparisonKeyFields) (ComparisonKey, bool) {
	switch fields {
	case keyProvider:
		return ComparisonKey{Provider: observation.Provider}, true
	case keyResourceType:
		return ComparisonKey{ResourceType: observation.ResourceType}, true
	case keyUIResourceType:
		return ComparisonKey{ResourceType: observation.ResourceType, DurationSource: observation.DurationSource}, true
	case keyRPCMethod:
		return ComparisonKey{Provider: observation.Provider, ResourceType: observation.ResourceType, Method: observation.RPC}, true
	case keyUIOperation:
		if observation.Address == "" {
			return ComparisonKey{}, false
		}
		return ComparisonKey{Address: observation.Address, Action: observation.RPC, DurationSource: observation.DurationSource}, true
	default:
		panic("unknown comparison key fields")
	}
}

func totalForPresence(total ComparisonTotal, found bool) *ComparisonTotal {
	if !found {
		return &ComparisonTotal{}
	}
	return cloneComparisonTotal(total)
}
func cloneComparisonTotal(total ComparisonTotal) *ComparisonTotal {
	copied := total
	if total.MeanMs != nil {
		copied.MeanMs = float64Pointer(*total.MeanMs)
	}
	if total.MaxMs != nil {
		copied.MaxMs = comparisonUint32Pointer(*total.MaxMs)
	}
	return &copied
}

func providers(observations []span.Span) []string {
	set := make(map[string]struct{})
	for _, observation := range observations {
		if observation.Provider != "" {
			set[observation.Provider] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for provider := range set {
		result = append(result, provider)
	}
	slices.Sort(result)
	return result
}
func providerIdentityStatus(before, after []span.Span, beforeProviders, afterProviders []string) string {
	if len(before) == 0 || len(after) == 0 {
		return "unknown"
	}
	for _, observations := range [][]span.Span{before, after} {
		for _, observation := range observations {
			if observation.Provider == "" {
				return "unknown"
			}
		}
	}
	if slices.Equal(beforeProviders, afterProviders) {
		return "same"
	}
	return "different"
}
func float64Pointer(value float64) *float64        { return &value }
func comparisonUint32Pointer(value uint32) *uint32 { return &value }
