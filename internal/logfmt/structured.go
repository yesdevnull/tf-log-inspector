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

// levelKey is the field a structured line carries its severity in.
const levelKey = `"@level":`

// StructuredLevel reads the severity off one line of Terraform's structured
// output, or LevelUnknown if the line carries none this package has a name
// for.
//
// It reads ONE field rather than decoding the line, for the two reasons
// IsStructuredLine does not decode either: a real capture is tens of
// thousands of lines and a JSON decode per line is a cost the scan cannot
// carry, and the rest of the object is a disclosure risk -- the message
// holds full resource and module addresses, which is why the scanner never
// hands structured content to an ordinary sink. A severity name is none of
// that.
//
// The FIRST marker wins. A message quoting `+"`"+`"@level":`+"`"+` would otherwise
// hand the line whatever severity it was quoting; taking the first takes the
// real key on every line Terraform writes, and the cost of being wrong is
// one line labelled by what it quotes rather than by what it is.
//
// Structured output spells its levels in lower case where hclog spells them
// in upper, so the name is upper-cased before ParseLevel sees it: the two
// formats then agree on what a level IS, which is what lets a level facet
// built over one mean the same as one built over the other.
func StructuredLevel(text string) Level {
	at := strings.Index(text, levelKey)
	if at < 0 {
		return LevelUnknown
	}
	rest := strings.TrimLeft(text[at+len(levelKey):], " \t")
	if !strings.HasPrefix(rest, `"`) {
		return LevelUnknown
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return LevelUnknown
	}
	return ParseLevel(strings.ToUpper(rest[:end]))
}
