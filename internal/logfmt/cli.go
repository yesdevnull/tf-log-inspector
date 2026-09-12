package logfmt

import "regexp"

// CLICompletionSink opts into unmasked CLI lifecycle text. Ordinary sinks
// receive an empty message so diagnostic collectors cannot disclose addresses.
type CLICompletionSink interface {
	CLICompletion(ord uint32, e Entry, line string)
}

// Candidate detection is anchored to a physical line. Full address and duration
// validation belongs to the resource builder, which records rejected candidates.
var cliCompletionPattern = regexp.MustCompile(`^(?:[^"[:space:]]|"(?:[^"\\]|\\.)*")+: (Read|Creation|Modifications|Destruction|Refresh) complete after (.*)$`)

// CLICompletionParts identifies a candidate without interpreting duration text.
func CLICompletionParts(line string) (address, action, duration string, ok bool) {
	match := cliCompletionPattern.FindStringSubmatchIndex(line)
	if match == nil {
		return "", "", "", false
	}
	return line[:match[2]-2], line[match[2]:match[3]], line[match[4]:match[5]], true
}
