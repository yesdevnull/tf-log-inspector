package model

import (
	"sort"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// ResourceSelection allows exact addresses and module subtrees independently.
type ResourceSelection struct {
	Addresses map[string]bool
	Modules   map[string]bool
}

// Membership distinguishes a known exclusion from unavailable identity.
type Membership uint8

const (
	MembershipUnknown Membership = iota
	MembershipSelected
	MembershipOther
)

// Match intersects independent dimensions; a definite exclusion wins over
// unavailable metadata. Nil dimensions pass even when identity is unavailable.
func (s ResourceSelection) Match(address string, module ResourceModule) Membership {
	addressMatch := MembershipSelected
	if s.Addresses != nil {
		addressMatch = MembershipOther
		if address == "" {
			for _, selected := range s.Addresses {
				if selected {
					addressMatch = MembershipUnknown
					break
				}
			}
		} else if s.Addresses[address] {
			addressMatch = MembershipSelected
		}
	}
	if addressMatch == MembershipOther {
		return MembershipOther
	}
	moduleMatch := MembershipSelected
	if s.Modules != nil {
		moduleMatch = s.matchModule(module)
	}
	return combineMembership(addressMatch, moduleMatch)
}

func (s ResourceSelection) matchModule(module ResourceModule) Membership {
	if !module.Known {
		for _, selected := range s.Modules {
			if selected {
				return MembershipUnknown
			}
		}
		return MembershipOther
	}
	segments, ok := moduleSegments(module.Path)
	if !ok {
		return MembershipOther
	}
	if s.Modules[""] {
		return MembershipSelected
	}
	for end := 2; end <= len(segments); end += 2 {
		if s.Modules[strings.Join(segments[:end], ".")] {
			return MembershipSelected
		}
	}
	return MembershipOther
}

func combineMembership(a, b Membership) Membership {
	if a == MembershipOther || b == MembershipOther {
		return MembershipOther
	}
	if a == MembershipUnknown || b == MembershipUnknown {
		return MembershipUnknown
	}
	return MembershipSelected
}

// NamedEvidence retains the confidence of each assigned RPC duration.
type NamedEvidence struct {
	Contained, Likely, Overlapping DurationTotal
}

// SelectionEvidence partitions the RPC baseline before named filtering.
type SelectionEvidence struct {
	Active          bool
	Baseline        DurationTotal
	Selected, Other NamedEvidence
	Unresolved      DurationTotal
}

// ResourceEvidence partitions admitted RPCs independently of named selection.
type ResourceEvidence struct {
	Baseline                                                DurationTotal
	MissingType, NoContext                                  DurationTotal
	Contained, Likely, Overlapping, Ambiguous, Unattributed DurationTotal
}

// ResourceProjection retains source indices and observed UI resource rows.
type ResourceProjection struct {
	RPCIndices, UIIndices []int
	Rows                  []ResourceRow
	UI, UnnamedUI         DurationTotal
	Evidence              ResourceEvidence
	Selection             SelectionEvidence
}

// SelectResources projects one log using its index. RPC evidence covers the
// base-filtered input before named selection, while UI has only type and named
// filters. Rows rank observed UI durations, independently of RPC measurements.
func SelectResources(l *Log, index ResourceIndex, base Filter, named ResourceSelection) ResourceProjection {
	result := ResourceProjection{Selection: SelectionEvidence{Active: named.Addresses != nil || named.Modules != nil}}
	if l == nil {
		return result
	}

	rows := make(map[string]int)
	uiBase := Filter{Types: base.Types}
	for _, op := range index.Operations {
		s := l.UISpans[op.UIIndex]
		if !uiBase.MatchSpan(s) || named.Match(s.Address, op.Module) != MembershipSelected {
			continue
		}
		result.UIIndices = append(result.UIIndices, op.UIIndex)
		result.UI.add(s)
		if s.Address == "" {
			result.UnnamedUI.add(s)
			continue
		}
		i, exists := rows[s.Address]
		if !exists {
			i = len(result.Rows)
			rows[s.Address] = i
			result.Rows = append(result.Rows, ResourceRow{Address: s.Address})
		}
		row := &result.Rows[i]
		row.Operations = append(row.Operations, op)
		row.UI.add(s)
	}

	hasContext := l.HasAddressContext()
	for i, s := range l.RPCSpans {
		if !base.MatchSpan(s) {
			continue
		}
		a := attrib.Attribution{}
		if i < len(l.Attribs) {
			a = l.Attribs[i]
		}
		result.Evidence.add(s, a.Confidence, hasContext)
		membership := MembershipUnknown
		if hasContext && s.HasPosition() && namedConfidence(a.Confidence) && a.Address != "" {
			module := ResourceModule{}
			if i < len(index.RPCModules) {
				module = index.RPCModules[i]
			}
			membership = named.Match(a.Address, module)
		}
		switch membership {
		case MembershipSelected:
			result.Selection.Selected.add(s, a.Confidence)
		case MembershipOther:
			result.Selection.Other.add(s, a.Confidence)
		default:
			result.Selection.Unresolved.add(s)
		}
		if !result.Selection.Active || membership == MembershipSelected {
			result.RPCIndices = append(result.RPCIndices, i)
		}
		// Only usable named RPC evidence supplements an existing UI row.
		if membership == MembershipSelected {
			if rowIndex, exists := rows[a.Address]; exists {
				row := &result.Rows[rowIndex]
				if a.Confidence == attrib.Overlapping {
					row.OverlappingRPC.add(s)
				} else {
					row.NamedRPC.add(s)
				}
			}
		}
	}
	result.Selection.Baseline = result.Evidence.Baseline
	sort.Slice(result.Rows, func(i, j int) bool {
		if result.Rows[i].UI.TotalMs != result.Rows[j].UI.TotalMs {
			return result.Rows[i].UI.TotalMs > result.Rows[j].UI.TotalMs
		}
		return result.Rows[i].Address < result.Rows[j].Address
	})
	return result
}

func (e *NamedEvidence) add(s span.Span, confidence attrib.Confidence) {
	switch confidence {
	case attrib.Contained:
		e.Contained.add(s)
	case attrib.Likely:
		e.Likely.add(s)
	case attrib.Overlapping:
		e.Overlapping.add(s)
	}
}

func (e *ResourceEvidence) add(s span.Span, confidence attrib.Confidence, hasContext bool) {
	e.Baseline.add(s)
	if s.ResourceType == "" {
		e.MissingType.add(s)
		return
	}
	if !hasContext {
		e.NoContext.add(s)
		return
	}
	switch confidence {
	case attrib.Contained:
		e.Contained.add(s)
	case attrib.Likely:
		e.Likely.add(s)
	case attrib.Overlapping:
		e.Overlapping.add(s)
	case attrib.Ambiguous:
		e.Ambiguous.add(s)
	default:
		e.Unattributed.add(s)
	}
}
