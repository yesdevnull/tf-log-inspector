package profile

import (
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

type ComparisonReport struct {
	Before, After Report
	Data          model.Comparison
}

type ComparisonMetadata struct {
	ToolVersion, BeforeBasename, AfterBasename string
}

func BuildComparison(before, after Report) (ComparisonReport, error) {
	data, err := model.Compare(comparisonInput(before), comparisonInput(after))
	if err != nil {
		return ComparisonReport{}, err
	}
	return ComparisonReport{Before: before, After: after, Data: data}, nil
}

func comparisonInput(r Report) model.ComparisonInput {
	return model.ComparisonInput{RPC: observationSpans(r.RPC), UI: observationSpans(r.UI)}
}

func observationSpans(observations []Observation) []span.Span {
	spans := make([]span.Span, len(observations))
	for i, observation := range observations {
		spans[i] = observation.Span
	}
	return spans
}
