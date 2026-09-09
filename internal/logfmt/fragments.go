package logfmt

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// JSONFragment identifies JSON payload bytes on a physical source line.
// Start and End are byte offsets into the original text; Line is one-based.
type JSONFragment struct {
	Start, End, Line int
}

// ProviderJSON is a complete provider message, with its original source ranges.
type ProviderJSON struct {
	Text      string
	Fragments []JSONFragment
}

type providerJSONPending struct {
	index           int
	body            strings.Builder
	stack           []byte
	quoted, escaped bool
}

// ReconstructProviderJSON joins timestamped fragments by exact provider
// component. Physical line endings are transport bytes, not JSON payload.
// Results follow the order in which messages start. Errors never quote input.
func ReconstructProviderJSON(text string) ([]ProviderJSON, error) {
	var messages []ProviderJSON
	pending := make(map[string]*providerJSONPending)
	active := ""
	for start, line := 0, 1; start < len(text); line++ {
		end := strings.IndexByte(text[start:], '\n')
		next := len(text)
		if end < 0 {
			end = len(text)
		} else {
			end += start
			next = end + 1
		}
		if end > start && text[end-1] == '\r' {
			end--
		}
		raw := text[start:end]
		comp, offset, header := providerJSONOuter(raw)
		var p *providerJSONPending
		if header {
			active = comp
			p = pending[comp]
			if p == nil {
				offset = providerJSONInitial(raw)
				if offset >= 0 {
					p = &providerJSONPending{index: len(messages)}
					pending[comp] = p
					messages = append(messages, ProviderJSON{})
				}
			}
		} else if len(pending) != 0 {
			comp = active
			p = pending[comp]
			if p == nil {
				return nil, fmt.Errorf("provider JSON at line %d: ambiguous continuation", line)
			}
		}
		if p != nil {
			message := &messages[p.index]
			startLine := line
			if len(message.Fragments) > 0 {
				startLine = message.Fragments[0].Line
			}
			consumed, complete, reason := p.consume(raw[offset:])
			firstBytes := consumed
			if len(message.Fragments) > 0 {
				firstBytes = message.Fragments[0].End - message.Fragments[0].Start
			}
			if reason != "" {
				// The failed fragment has not been appended by consume. Inspect
				// the candidate through the failure, never its parser error text.
				p.body.WriteString(raw[offset : offset+consumed])
				return nil, providerJSONFailure(reason, line, startLine, len(message.Fragments)+1, p.body.Len(), firstBytes, consumed, providerJSONSyntaxOffset(p.body.String()))
			}
			message.Fragments = append(message.Fragments, JSONFragment{Start: start + offset, End: start + offset + consumed, Line: line})
			if complete {
				message.Text = p.body.String()
				if !utf8.ValidString(message.Text) {
					return nil, providerJSONFailure("UTF-8", line, startLine, len(message.Fragments), len(message.Text), firstBytes, consumed, 0)
				}
				if !json.Valid([]byte(message.Text)) {
					return nil, providerJSONFailure("JSON syntax", line, startLine, len(message.Fragments), len(message.Text), firstBytes, consumed, providerJSONSyntaxOffset(message.Text))
				}
				delete(pending, comp)
			}
		}
		start = next
	}
	if len(pending) != 0 {
		for _, message := range messages {
			if message.Text == "" {
				return nil, fmt.Errorf("provider JSON at line %d: incomplete or invalid body", message.Fragments[0].Line)
			}
		}
	}
	return messages, nil
}

// Diagnostics describe structure only. JSON parser error text can contain
// source bytes and must never be included in a scrubbing failure.
func providerJSONFailure(reason string, line, startLine, fragments, bytes, firstBytes, lastBytes int, syntaxOffset int64) error {
	detail := fmt.Sprintf("%s; start line %d; fragments %d; joined bytes %d; first fragment bytes %d; last fragment bytes %d", reason, startLine, fragments, bytes, firstBytes, lastBytes)
	if syntaxOffset > 0 {
		detail += fmt.Sprintf("; syntax offset %d", syntaxOffset)
	}
	return fmt.Errorf("provider JSON at line %d: invalid body (%s)", line, detail)
}

func providerJSONSyntaxOffset(text string) int64 {
	var body json.RawMessage
	if syntax, ok := json.Unmarshal([]byte(text), &body).(*json.SyntaxError); ok {
		return syntax.Offset
	}
	return 0
}

// Keep all bytes after the outer logger's single separator space. In
// particular, a nested-looking prefix in a continuation belongs to the body.
func providerJSONOuter(line string) (comp string, offset int, header bool) {
	_, rest, ok := splitTimestamp(line)
	if !ok {
		return "", 0, false
	}
	_, rest = splitLevel(rest)
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 || strings.ContainsAny(rest[:colon], " \t") {
		return "", 0, true
	}
	comp = rest[:colon]
	offset = len(line) - len(rest) + colon + 1
	if offset < len(line) && line[offset] == ' ' {
		offset++
	}
	return comp, offset, true
}

func providerJSONInitial(line string) int {
	comp, offset, header := providerJSONOuter(line)
	if !header || !strings.HasPrefix(comp, "provider.") {
		return -1
	}
	msg := strings.TrimLeft(line[offset:], " \t\r\n")
	if rest, ok := splitNestedTimestamp(msg); ok {
		msg = rest
	}
	// Unknown bracketed text can be an array, so only recognised levels
	// are transport prefixes at this boundary.
	if level, rest := splitLevel(msg); level != LevelUnknown {
		msg = rest
	}
	msg = strings.TrimLeft(msg, " \t\r\n")
	if len(msg) == 0 || (msg[0] != '{' && msg[0] != '[') {
		return -1
	}
	if msg[0] == '[' {
		if end := strings.IndexByte(msg, ']'); end > 0 {
			label := strings.TrimSpace(msg[1:end])
			if providerDiagnosticLabel(label) {
				return -1
			}
		}
	}
	return len(line) - len(msg)
}

// Diagnostic labels are identifier words, while JSON literals must still be
// parsed as array contents even when the surrounding array is malformed.
func providerDiagnosticLabel(label string) bool {
	words := strings.Fields(label)
	if len(words) == 0 {
		return false
	}
	for _, literal := range []string{"true", "false", "null"} {
		if strings.HasPrefix(words[0], literal) {
			return false
		}
	}
	for _, word := range words {
		if !isAlpha(word[0]) && word[0] != '_' {
			return false
		}
		for i := range len(word) {
			c := word[i]
			if !isAlpha(c) && !isDigit(c) && c != '_' && c != '.' && c != '-' {
				return false
			}
		}
	}
	return true
}

// Scan each byte once; structural validation runs only on a completed body.
func (p *providerJSONPending) consume(payload string) (int, bool, string) {
	for i := 0; i < len(payload); i++ {
		c := payload[i]
		if p.quoted {
			if p.escaped {
				p.escaped = false
			} else if c == '\\' {
				p.escaped = true
			} else if c == '"' {
				p.quoted = false
			}
			continue
		}
		switch c {
		case '"':
			p.quoted = true
		case '{', '[':
			p.stack = append(p.stack, c)
		case '}', ']':
			if len(p.stack) == 0 || (c == '}' && p.stack[len(p.stack)-1] != '{') || (c == ']' && p.stack[len(p.stack)-1] != '[') {
				return i + 1, false, "delimiter mismatch"
			}
			p.stack = p.stack[:len(p.stack)-1]
			if len(p.stack) == 0 {
				if !providerJSONSuffix(payload[i+1:]) {
					return i + 1, false, "suffix grammar"
				}
				p.body.WriteString(payload[:i+1])
				return i + 1, true, ""
			}
		}
	}
	p.body.WriteString(payload)
	return len(payload), false, ""
}

// A completed body may be followed by whitespace-delimited logger fields,
// optionally introduced by hclog's colon separator.
// Require every suffix byte to belong to that grammar so a valid JSON prefix
// cannot conceal a malformed body.
func providerJSONSuffix(suffix string) bool {
	if strings.HasPrefix(suffix, ":") {
		suffix = suffix[1:]
		if strings.TrimSpace(suffix) == "" {
			return false
		}
	}
	for i := 0; i < len(suffix); {
		if !isSpace(suffix[i]) {
			return false
		}
		for i < len(suffix) && isSpace(suffix[i]) {
			i++
		}
		if i == len(suffix) {
			return true
		}
		key, value, next, ok := parsePair(suffix, i)
		if !ok || !ValidKey(key) {
			return false
		}
		valueStart := i + len(key) + 1
		// readQuoted also returns unterminated values. A closed value has
		// exactly two quote bytes outside the returned raw value.
		if valueStart < len(suffix) && suffix[valueStart] == '"' && next != valueStart+len(value)+2 {
			return false
		}
		i = next
	}
	return true
}
