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
	in := model.ComparisonInput{RPC: make([]span.Span, len(r.RPC)), UI: make([]span.Span, len(r.UI))}
	for i, observation := range r.RPC {
		in.RPC[i] = observation.Span
	}
	for i, observation := range r.UI {
		in.UI[i] = observation.Span
	}
	return in
}
