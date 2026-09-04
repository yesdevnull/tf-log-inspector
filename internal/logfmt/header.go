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
	ts, rest, ok := splitTimestamp(line)
	if !ok {
		return Header{}
	}

	h := Header{TS: ts, HasTS: true}
	h.Level, rest = splitLevel(rest)
	h.Comp, h.Msg = splitComponent(rest)

	// Terraform captures a provider's stderr and re-logs each line through
	// its own logger, so the provider's own hclog header survives inside
	// Terraform's message: two headers on one line. Peel the inner one.
	// Its level is the one that means something -- Terraform's wrapper can
	// say INFO over a provider's DEBUG, which a level filter would then get
	// wrong. Its timestamp is discarded: it comes from a separate process
	// whose clock may be skewed, while the outer timestamp is the one every
	// other entry in the log is ordered by. The outer component is kept,
	// since that is what names the provider.
	if _, inner, ok := splitTimestamp(h.Msg); ok {
		level, msg := splitLevel(inner)
		if level != LevelUnknown {
			h.Level = level
		}
		h.Msg = msg
	} else if level, msg := splitLevel(h.Msg); level != LevelUnknown {
		// Some providers write a bare "[DEBUG] " prefix rather than a full
		// hclog header -- the Azure SDK's convention. Terraform usually
		// parses that level and re-logs at it, so the outer level already
		// agrees and nothing is lost by stripping the duplicate; where it
		// disagrees the provider's own level is again the meaningful one.
		// Only a recognised level name is peeled, so a message opening with
		// some other bracketed token keeps it.
		h.Level = level
		h.Msg = msg
	}
	return h
}

// splitTimestamp peels a leading hclog timestamp off a line, reporting
// whether one was there at all.
func splitTimestamp(line string) (ts time.Time, rest string, ok bool) {
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return time.Time{}, line, false
	}
	ts, err := time.Parse(tsLayout, line[:sp])
	if err != nil {
		return time.Time{}, line, false
	}
	return ts, strings.TrimLeft(line[sp+1:], " "), true
}

// splitLevel peels a leading "[LEVEL]" off a line. A line with no bracketed
// level is returned unchanged alongside LevelUnknown.
func splitLevel(s string) (Level, string) {
	if !strings.HasPrefix(s, "[") {
		return LevelUnknown, s
	}
	end := strings.IndexByte(s, ']')
	if end <= 0 {
		return LevelUnknown, s
	}
	return ParseLevel(s[1:end]), strings.TrimLeft(s[end+1:], " ")
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
