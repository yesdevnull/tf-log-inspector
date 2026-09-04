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
	if inner, ok := splitNestedTimestamp(h.Msg); ok {
		level, msg := splitLevel(inner)
		if level != LevelUnknown {
			h.Level = level
		}
		h.Msg = msg
	} else if level, msg := splitLevel(h.Msg); level != LevelUnknown {
		// Some providers write a bare "[DEBUG] " prefix rather than a full
		// hclog header -- the Azure SDK's convention. Only a recognised
		// level name is peeled, so a message opening with some other
		// bracketed token keeps it.
		//
		// The prefix is stripped but the level is deliberately NOT taken
		// from it. A bare prefix is indistinguishable from a message that
		// genuinely opens by quoting a level, and the two want opposite
		// treatment: taking the level would relabel a TRACE line as ERROR
		// on the strength of its own text. Terraform reaches this line
		// having already parsed the provider's level and re-logged at it --
		// which is why azurerm's outer level and bare prefix agree in the
		// sample log -- so taking it buys nothing and risks a new error
		// where keeping it merely preserves the status quo.
		h.Msg = msg
	}
	return h
}

// goLogLayout is the Go standard library log package's default timestamp.
// Providers that log through "log" rather than hclog -- azuread among them --
// nest this format inside Terraform's message. It spans two space-separated
// tokens, so splitTimestamp's single-token scan cannot see it.
const goLogLayout = "2006/01/02 15:04:05"

// splitNestedTimestamp peels a leading timestamp in either supported format
// off a nested header, reporting whether one was there.
//
// The extra leniency is deliberately confined to nested headers. Accepting a
// Go-log timestamp at the start of a line would promote continuation text --
// a provider's multi-line body, or interleaved plan output -- into an entry
// of its own, splitting entries that belong together.
func splitNestedTimestamp(s string) (rest string, ok bool) {
	// Every supported timestamp opens with a digit and most messages open
	// with a letter, so this rejects the common case before any parsing.
	if s == "" || !isDigit(s[0]) {
		return s, false
	}
	if _, r, ok := splitTimestamp(s); ok {
		return r, true
	}
	first := strings.IndexByte(s, ' ')
	if first < 0 {
		return s, false
	}
	second := strings.IndexByte(s[first+1:], ' ')
	if second < 0 {
		return s, false
	}
	// time.Parse accepts a fractional second after the seconds field even
	// though the layout does not name one, which covers log.Lmicroseconds.
	end := first + 1 + second
	if _, err := time.Parse(goLogLayout, s[:end]); err != nil {
		return s, false
	}
	return strings.TrimLeft(s[end+1:], " "), true
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
