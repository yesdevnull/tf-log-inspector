package logfmt

import "testing"

// A structured line's severity is the only thing read out of it. The rest of
// the object is a disclosure risk -- the message carries full resource and
// module addresses -- which is why the scanner never hands structured
// content to an ordinary sink, and why this reads one field rather than
// decoding the line.
func TestStructuredLevelReadsTheSeverityOffTheLine(t *testing.T) {
	for _, tc := range []struct {
		what string
		line string
		want Level
	}{
		{"an error", `{"@level":"error","@message":"failed","@timestamp":"2023-04-02T09:14:01.500000Z"}`, LevelError},
		{"a warning", `{"@level":"warn","@message":"deprecated","@timestamp":"2023-04-02T09:14:01.400000Z"}`, LevelWarn},
		{"the common case", `{"@level":"info","@message":"applying","@timestamp":"2023-04-02T09:14:01.300000Z"}`, LevelInfo},
		{"debug", `{"@level":"debug","@message":"x","@timestamp":"t"}`, LevelDebug},
		{"trace", `{"@level":"trace","@message":"x","@timestamp":"t"}`, LevelTrace},
		// Terraform writes the level first, but the field order of a JSON
		// object is not a guarantee, and IsStructuredLine deliberately does
		// not depend on one either.
		{"the level after other fields", `{"@message":"x","@level":"error","@timestamp":"t"}`, LevelError},
		// Whitespace after the colon is not what encoding/json emits, but
		// costs nothing to accept and would otherwise read as no level.
		{"a space after the colon", `{"@level": "warn","@timestamp":"t"}`, LevelWarn},
		// A severity this package has no name for is UNKNOWN, the same
		// answer a line carrying no level at all gets: both mean "the log
		// did not tell us", which is the honest reading.
		{"a severity we do not know", `{"@level":"catastrophe","@timestamp":"t"}`, LevelUnknown},
		{"no level at all", `{"@message":"x","@timestamp":"t"}`, LevelUnknown},
		{"a truncated line", `{"@level":"err`, LevelUnknown},
		{"not structured output", `2023-04-02T09:14:01.500Z [ERROR] provider: x`, LevelUnknown},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if got := StructuredLevel(tc.line); got != tc.want {
				t.Errorf("StructuredLevel(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// The marker can appear inside a message as well as as a key, and a line
// quoting it would otherwise take a severity from whatever it was quoting.
// The FIRST occurrence wins, which is the real key on every line Terraform
// writes: the cost of being wrong is one line labelled by what it quotes,
// and the alternative is decoding every line of a 37MB log.
func TestStructuredLevelTakesTheFirstMarkerRatherThanTheLast(t *testing.T) {
	line := `{"@level":"info","@message":"saw \"@level\":\"error\" in the output","@timestamp":"t"}`
	if got := StructuredLevel(line); got != LevelInfo {
		t.Errorf("StructuredLevel = %v, want the line's own level rather than the one it quotes", got)
	}
}
