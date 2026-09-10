package profile

import (
	"errors"
	"fmt"
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Observation retains one admitted duration and its identity in the original
// tier slice and source log.
type Observation struct {
	Index       int
	Span        span.Span
	Source      *model.SourceLocation
	Attribution attrib.Attribution
}

// TypeSummary presents both timing tiers for one resource type. UILowerBound
// records whether any UI duration contributing to the row was saturated.
type TypeSummary struct {
	model.TypeRow
	UILowerBound bool
}

// Timeline contains analysis for the one admitted tier selected by the model.
// PositionedIndices maps analysis positions back to original tier indices.
type Timeline struct {
	Tier              *span.Fidelity
	Analysis          model.TimingAnalysis
	PositionedIndices []int
}

// Report is the complete presentation-independent profile of one loaded log.
type Report struct {
	Bytes          uint64
	Quality        model.CaptureQuality
	HasContext     bool
	RPC, UI        []Observation
	RPCRanking     []int
	UIRanking      []int
	Providers      []model.Bucket
	Types          []TypeSummary
	Resources      model.ResourceProjection
	Timeline       Timeline
	Reconstruction model.ReconstructionQuality
}

// Build assembles complete report data without reading files, formatting
// values, or triggering lazy raw-response reconstruction.
func Build(l *model.Log) (Report, error) {
	if l == nil {
		return Report{}, errors.New("building profile data: nil log")
	}

	report := Report{
		Bytes:          l.Stats.Bytes,
		Quality:        l.CaptureQuality(),
		HasContext:     l.HasAddressContext(),
		Reconstruction: l.ReconstructionQuality(),
		Providers: model.RollupBy(l.RPCSpans, func(s span.Span) string {
			return s.Provider
		}),
		Resources: model.SelectResources(l, model.BuildResourceIndex(l), model.Filter{}, model.ResourceSelection{}),
	}
	report.RPC, report.RPCRanking = buildObservations(l, l.RPCSpans, l.Attribs, rankRPC)
	report.UI, report.UIRanking = buildObservations(l, l.UISpans, nil, rankUI)
	report.Types = buildTypeSummaries(l.RPCSpans, l.UISpans)

	tier, ok := model.PreferredTiming(l)
	if !ok {
		return report, nil
	}
	report.Timeline.Tier = &tier
	chosen := l.RPCSpans
	if tier == span.FidelityUIReported {
		chosen = l.UISpans
	}
	timing := model.SelectTiming(chosen)
	for i, s := range chosen {
		if s.HasPosition() {
			report.Timeline.PositionedIndices = append(report.Timeline.PositionedIndices, i)
		}
	}
	analysis, err := model.AnalyseTiming(timing)
	if err != nil {
		return report, fmt.Errorf("analysing %s timing: %w", tier, err)
	}
	report.Timeline.Analysis = analysis
	return report, nil
}

type observationRank func(a, b Observation) int

func buildObservations(l *model.Log, spans []span.Span, attributions []attrib.Attribution, rank observationRank) ([]Observation, []int) {
	if len(spans) == 0 {
		return nil, nil
	}
	observations := make([]Observation, len(spans))
	ranking := make([]int, len(spans))
	for i, s := range spans {
		observations[i] = Observation{Index: i, Span: s}
		if i < len(attributions) {
			observations[i].Attribution = attributions[i]
		}
		if source, ok := l.SourceLocation(s.Entry); ok {
			sourceCopy := source
			observations[i].Source = &sourceCopy
		}
		ranking[i] = i
	}
	sort.Slice(ranking, func(i, j int) bool {
		return rank(observations[ranking[i]], observations[ranking[j]]) < 0
	})
	return observations, ranking
}

func rankRPC(a, b Observation) int {
	if comparison := compareDuration(a.Span.DurationMs, b.Span.DurationMs); comparison != 0 {
		return comparison
	}
	for _, values := range [][2]string{
		{a.Span.Provider, b.Span.Provider},
		{a.Span.ResourceType, b.Span.ResourceType},
		{a.Span.RPC, b.Span.RPC},
		{a.Attribution.Address, b.Attribution.Address},
	} {
		if comparison := compareString(values[0], values[1]); comparison != 0 {
			return comparison
		}
	}
	return compareSourceAndIndex(a, b)
}

func rankUI(a, b Observation) int {
	if comparison := compareDuration(a.Span.DurationMs, b.Span.DurationMs); comparison != 0 {
		return comparison
	}
	for _, values := range [][2]string{
		{a.Span.Address, b.Span.Address},
		{a.Span.RPC, b.Span.RPC},
		{a.Span.ResourceType, b.Span.ResourceType},
	} {
		if comparison := compareString(values[0], values[1]); comparison != 0 {
			return comparison
		}
	}
	return compareSourceAndIndex(a, b)
}

func compareDuration(a, b uint32) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	default:
		return 0
	}
}

func compareString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareSourceAndIndex(a, b Observation) int {
	if a.Span.Entry < b.Span.Entry {
		return -1
	}
	if a.Span.Entry > b.Span.Entry {
		return 1
	}
	if a.Index < b.Index {
		return -1
	}
	if a.Index > b.Index {
		return 1
	}
	return 0
}

func buildTypeSummaries(rpcSpans, uiSpans []span.Span) []TypeSummary {
	lowerBounds := make(map[string]bool)
	for _, s := range uiSpans {
		key := model.FacetKey(s.ResourceType)
		lowerBounds[key] = lowerBounds[key] || s.DurationSaturated
	}
	rows := model.JoinByResourceType(rpcSpans, uiSpans)
	out := make([]TypeSummary, len(rows))
	for i, row := range rows {
		out[i] = TypeSummary{TypeRow: row, UILowerBound: lowerBounds[row.ResourceType]}
	}
	return out
}
