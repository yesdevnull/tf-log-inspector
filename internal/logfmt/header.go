package logfmt

import (
	"strings"
	"time"
)

// tsLayout matches both offset forms Terraform emits: "Z" for UTC and a
// numeric "+0200" with no colon. It is hclog's TimeFormat.
const tsLayout = "2006-01-02T15:04:05.000Z0700"

// Header is the parsed prefix of a single physical log line.
type Header struct {
	TS    time.Time
	HasTS bool
	Level Level
	Comp  string // component prefix, or "" if the line has none
	Msg   string // message with the component prefix removed
}

// ParseHeader parses one physical line. A line without a leading timestamp is
// not an entry header: HasTS is false and the caller treats it as a
// continuation or as interleaved non-hclog content.
func ParseHeader(line string) Header {
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return Header{}
	}
	ts, err := time.Parse(tsLayout, line[:sp])
	if err != nil {
		return Header{}
	}

	h := Header{TS: ts, HasTS: true}
	rest := strings.TrimLeft(line[sp+1:], " ")
	if strings.HasPrefix(rest, "[") {
		if end := strings.IndexByte(rest, ']'); end > 0 {
			h.Level = ParseLevel(rest[1:end])
			rest = strings.TrimLeft(rest[end+1:], " ")
		}
	}
	h.Comp, h.Msg = splitComponent(rest)
	return h
}

// splitComponent peels a leading "component: " prefix off a message. Terraform
// core writes messages beginning with a component name, and hclog renders a
// named logger identically, so the two are indistinguishable and are treated
// alike. A candidate containing whitespace is prose, not a component.
func splitComponent(s string) (comp, msg string) {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", s
	}
	candidate := s[:colon]
	if strings.ContainsAny(candidate, " \t") {
		return "", s
	}
	return candidate, strings.TrimLeft(s[colon+1:], " ")
}
