package profile

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

var errInvalidComparisonUTF8 = errors.New("comparison JSON contains invalid UTF-8")

type jsonComparison struct {
	SchemaVersion  uint8                   `json:"schema_version"`
	Kind           string                  `json:"kind"`
	ToolVersion    string                  `json:"tool_version"`
	DurationUnit   string                  `json:"duration_unit"`
	Before         jsonCapture             `json:"before"`
	After          jsonCapture             `json:"after"`
	Comparability  jsonComparability       `json:"comparability"`
	Sections       []jsonComparisonSection `json:"sections"`
	Qualifications []string                `json:"qualifications"`
}

type jsonComparability struct {
	LoggingConfiguration   string   `json:"logging_configuration"`
	ProviderIdentityStatus string   `json:"provider_identity_status"`
	BeforeProviders        []string `json:"before_providers"`
	AfterProviders         []string `json:"after_providers"`
}

type jsonComparisonSection struct {
	Kind            string              `json:"kind"`
	Tier            string              `json:"tier"`
	BeforeAvailable bool                `json:"before_available"`
	AfterAvailable  bool                `json:"after_available"`
	Rows            []jsonComparisonRow `json:"rows"`
}

type jsonComparisonRow struct {
	Key     any                   `json:"key"`
	State   string                `json:"state"`
	Before  *jsonComparisonTotal  `json:"before"`
	After   *jsonComparisonTotal  `json:"after"`
	Changes jsonComparisonChanges `json:"changes"`
}

type jsonProviderKey struct {
	Provider string `json:"provider"`
}
type jsonResourceTypeKey struct {
	ResourceType string `json:"resource_type"`
}
type jsonRPCMethodKey struct {
	Provider     string `json:"provider"`
	ResourceType string `json:"resource_type"`
	Method       string `json:"method"`
}
type jsonUIOperationKey struct {
	Address string `json:"address"`
	Action  string `json:"action"`
}

type jsonComparisonTotal struct {
	Count      uint64   `json:"count"`
	TotalMs    uint64   `json:"total_ms"`
	MeanMs     *float64 `json:"mean_ms"`
	MaxMs      *uint32  `json:"max_ms"`
	LowerBound bool     `json:"lower_bound"`
}

type jsonComparisonChanges struct {
	Count        *json.Number `json:"count"`
	TotalMs      *json.Number `json:"total_ms"`
	MeanMs       *float64     `json:"mean_ms"`
	MaxMs        *json.Number `json:"max_ms"`
	CountPercent *float64     `json:"count_percent"`
	TotalPercent *float64     `json:"total_percent"`
	MeanPercent  *float64     `json:"mean_percent"`
	MaxPercent   *float64     `json:"max_percent"`
}

func RenderComparisonJSON(w io.Writer, report ComparisonReport, metadata ComparisonMetadata) error {
	doc, err := buildComparisonJSON(report, metadata)
	if err != nil {
		return err
	}
	if !finiteComparisonJSON(doc) {
		return errors.New("encoding comparison JSON failed")
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errors.New("encoding comparison JSON failed")
	}
	return writeText(w, string(data)+"\n")
}

func buildComparisonJSON(report ComparisonReport, metadata ComparisonMetadata) (jsonComparison, error) {
	if err := validStrings(metadata.ToolVersion, metadata.BeforeBasename, metadata.AfterBasename); err != nil {
		return jsonComparison{}, errInvalidComparisonUTF8
	}
	before, err := captureJSON(report.Before, metadata.BeforeBasename, report.Data.BeforeUnnamedUI)
	if err != nil {
		return jsonComparison{}, comparisonJSONError(err)
	}
	after, err := captureJSON(report.After, metadata.AfterBasename, report.Data.AfterUnnamedUI)
	if err != nil {
		return jsonComparison{}, comparisonJSONError(err)
	}
	if err := validStrings(report.Data.ProviderIdentityStatus); err != nil {
		return jsonComparison{}, errInvalidComparisonUTF8
	}
	beforeProviders := append([]string{}, report.Data.BeforeProviders...)
	afterProviders := append([]string{}, report.Data.AfterProviders...)
	if err := validStrings(append(beforeProviders, afterProviders...)...); err != nil {
		return jsonComparison{}, errInvalidComparisonUTF8
	}
	doc := jsonComparison{SchemaVersion: 1, Kind: "comparison", ToolVersion: metadata.ToolVersion, DurationUnit: "ms", Before: before, After: after,
		Comparability: jsonComparability{LoggingConfiguration: "unknown", ProviderIdentityStatus: report.Data.ProviderIdentityStatus, BeforeProviders: beforeProviders, AfterProviders: afterProviders},
		Sections:      make([]jsonComparisonSection, 0, len(report.Data.Sections)), Qualifications: comparisonQualifications()}
	for _, section := range report.Data.Sections {
		wire, err := comparisonSectionJSON(section)
		if err != nil {
			return jsonComparison{}, err
		}
		doc.Sections = append(doc.Sections, wire)
	}
	return doc, nil
}

func comparisonSectionJSON(section model.ComparisonSection) (jsonComparisonSection, error) {
	if err := validStrings(section.Kind, section.Tier); err != nil {
		return jsonComparisonSection{}, errInvalidComparisonUTF8
	}
	if section.Kind != "rpc_providers" && section.Kind != "rpc_resource_types" && section.Kind != "rpc_methods" && section.Kind != "ui_resource_types" && section.Kind != "ui_operations" {
		return jsonComparisonSection{}, errors.New("comparison JSON has invalid section kind")
	}
	w := jsonComparisonSection{Kind: section.Kind, Tier: section.Tier, BeforeAvailable: section.BeforeAvailable, AfterAvailable: section.AfterAvailable, Rows: make([]jsonComparisonRow, 0, len(section.Rows))}
	for _, row := range section.Rows {
		if err := validStrings(row.State); err != nil {
			return jsonComparisonSection{}, errInvalidComparisonUTF8
		}
		if row.State != "matched" && row.State != "added" && row.State != "removed" && row.State != "unavailable" {
			return jsonComparisonSection{}, errors.New("comparison JSON has invalid row state")
		}
		key, err := comparisonKeyJSON(section.Kind, row.Key)
		if err != nil {
			return jsonComparisonSection{}, err
		}
		w.Rows = append(w.Rows, jsonComparisonRow{Key: key, State: row.State, Before: comparisonTotalJSON(row.Before), After: comparisonTotalJSON(row.After), Changes: comparisonChangesJSON(row.Changes)})
	}
	return w, nil
}

func comparisonKeyJSON(kind string, key model.ComparisonKey) (any, error) {
	var values []string
	var result any
	switch kind {
	case "rpc_providers":
		values = []string{key.Provider}
		result = jsonProviderKey{key.Provider}
	case "rpc_resource_types", "ui_resource_types":
		values = []string{key.ResourceType}
		result = jsonResourceTypeKey{key.ResourceType}
	case "rpc_methods":
		values = []string{key.Provider, key.ResourceType, key.Method}
		result = jsonRPCMethodKey{key.Provider, key.ResourceType, key.Method}
	case "ui_operations":
		values = []string{key.Address, key.Action}
		result = jsonUIOperationKey{key.Address, key.Action}
	default:
		return nil, errors.New("comparison JSON has invalid section kind")
	}
	if err := validStrings(values...); err != nil {
		return nil, errInvalidComparisonUTF8
	}
	return result, nil
}

func comparisonTotalJSON(total *model.ComparisonTotal) *jsonComparisonTotal {
	if total == nil {
		return nil
	}
	return &jsonComparisonTotal{total.Count, total.TotalMs, copyFloat(total.MeanMs), copyUint32(total.MaxMs), total.LowerBound}
}
func comparisonChangesJSON(c model.ComparisonChanges) jsonComparisonChanges {
	return jsonComparisonChanges{comparisonIntegerPointer(c.Count), comparisonIntegerPointer(c.TotalMs), copyFloat(c.MeanMs), comparisonIntegerPointer(c.MaxMs), copyFloat(c.CountPercent), copyFloat(c.TotalPercent), copyFloat(c.MeanPercent), copyFloat(c.MaxPercent)}
}
func comparisonIntegerPointer(v *model.SignedChange) *json.Number {
	if v == nil {
		return nil
	}
	n := comparisonInteger(*v)
	return &n
}
func comparisonInteger(v model.SignedChange) json.Number {
	value := strconv.FormatUint(v.Magnitude, 10)
	if v.Negative && v.Magnitude != 0 {
		value = "-" + value
	}
	return json.Number(value)
}
func comparisonJSONError(err error) error {
	if errors.Is(err, errInvalidProfileUTF8) {
		return errInvalidComparisonUTF8
	}
	return err
}
func comparisonQualifications() []string {
	return []string{"unmasked_identifiers", "logging_affects_durations", "rpc_and_ui_measure_different_work", "ui_duration_rounding", "observed_changes_are_not_causal", "added_removed_are_observation_presence", "independent_scrub_aliases_may_differ", "logging_configuration_unknown", "lower_bounds_do_not_define_timing_deltas"}
}

func finiteComparisonJSON(doc jsonComparison) bool {
	for _, s := range doc.Sections {
		for _, r := range s.Rows {
			for _, v := range []*float64{meanOf(r.Before), meanOf(r.After), r.Changes.MeanMs, r.Changes.CountPercent, r.Changes.TotalPercent, r.Changes.MeanPercent, r.Changes.MaxPercent} {
				if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
					return false
				}
			}
		}
	}
	return true
}
func meanOf(t *jsonComparisonTotal) *float64 {
	if t == nil {
		return nil
	}
	return t.MeanMs
}
