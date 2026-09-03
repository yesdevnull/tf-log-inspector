package logfmt

import "strings"

// IsStructuredLine reports whether text is one line of Terraform's
// machine-readable UI JSON stream ("structured output"), which HCP Terraform
// can produce instead of hclog text. Every line of that stream is a single
// JSON object carrying both "@level" and "@timestamp" keys, so checking for
// both -- rather than decoding the line -- identifies it regardless of field
// ordering. The check never parses the JSON, and it fails toward
// under-counting: a line missing either marker is left for the caller to
// treat exactly as it is today.
func IsStructuredLine(text string) bool {
	return strings.HasPrefix(text, "{") &&
		strings.Contains(text, `"@level":`) &&
		strings.Contains(text, `"@timestamp":`)
}
