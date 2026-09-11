package logfmt

import (
	"encoding/json"
	"sort"
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
	last            byte
	lastConsumed    int
	ranges          []JSONFragment
}

// InspectProviderJSON joins timestamped fragments by exact provider component.
// Physical line endings are transport bytes, not JSON payload. A failed exact
// component is quarantined while independent components remain recoverable.
func InspectProviderJSON(text string) ProviderJSONResult {
	var messages []ProviderJSON
	var diagnostics []ProviderJSONDiagnostic
	pending := make(map[string]*providerJSONPending)
	quarantined := make(map[string]int)
	active := ""
	lastLine := 0
	globalDiagnostic := -1
	for start, line := 0, 1; start < len(text); line++ {
		lastLine = line
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
		if globalDiagnostic >= 0 {
			diagnostics[globalDiagnostic].Unavailable = append(diagnostics[globalDiagnostic].Unavailable, JSONFragment{Start: start, End: end, Line: line})
			start = next
			continue
		}
		if providerJSONOwnershipUnknown(raw) {
			for _, p := range pending {
				message := messages[p.index]
				diagnostics = append(diagnostics, ProviderJSONDiagnostic{
					Code: "ambiguous_ownership", Line: line, StartLine: message.Fragments[0].Line,
					FragmentCount: len(message.Fragments), JoinedBytes: p.body.Len(),
					FirstFragmentBytes: message.Fragments[0].End - message.Fragments[0].Start,
					LastFragmentBytes:  p.lastConsumed, Ranges: append([]JSONFragment(nil), message.Fragments...),
				})
				messages[p.index] = ProviderJSON{}
			}
			clear(pending)
			diagnostics = append(diagnostics, ProviderJSONDiagnostic{Code: "ambiguous_ownership", Line: line, StartLine: line, Unavailable: []JSONFragment{{Start: start, End: end, Line: line}}})
			globalDiagnostic = len(diagnostics) - 1
			start = next
			continue
		}
		comp, offset, header := providerJSONOuter(raw)
		var p *providerJSONPending
		quarantineIndex := -1
		if header {
			active = comp
			quarantineIndex = quarantined[comp]
			if _, ok := quarantined[comp]; !ok {
				quarantineIndex = -1
			}
			p = pending[comp]
			if p == nil && quarantineIndex < 0 {
				offset = providerJSONInitial(raw)
				if offset >= 0 {
					p = &providerJSONPending{index: len(messages)}
					pending[comp] = p
					messages = append(messages, ProviderJSON{})
				}
			}
		} else if len(pending) != 0 {
			// Physical continuations belong to the most recent entry, even
			// when only a different component has unfinished JSON.
			comp = active
			p = pending[comp]
		} else {
			comp = active
		}
		if !header {
			if index, ok := quarantined[comp]; ok {
				quarantineIndex = index
			}
		}
		if quarantineIndex >= 0 {
			diagnostics[quarantineIndex].Unavailable = append(diagnostics[quarantineIndex].Unavailable, JSONFragment{Start: start + offset, End: end, Line: line})
			start = next
			continue
		}
		if p != nil {
			message := &messages[p.index]
			startLine := line
			if len(message.Fragments) > 0 {
				startLine = message.Fragments[0].Line
			}
			consumed, complete, reason := p.consume(raw[offset:])
			p.lastConsumed = consumed
			firstBytes := consumed
			if len(message.Fragments) > 0 {
				firstBytes = message.Fragments[0].End - message.Fragments[0].Start
			}
			if reason != "" {
				ranges := append([]JSONFragment(nil), message.Fragments...)
				ranges = append(ranges, JSONFragment{Start: start + offset, End: start + len(raw), Line: line})
				diagnostic := newProviderJSONDiagnostic(reason, line, startLine, len(message.Fragments)+1, p.body.Len(), firstBytes, consumed, providerJSONSyntaxOffset(p.body.String()), ranges)
				diagnostics = append(diagnostics, diagnostic)
				quarantined[comp] = len(diagnostics) - 1
				messages[p.index] = ProviderJSON{}
				delete(pending, comp)
				start = next
				continue
			}
			for _, fragment := range p.ranges {
				message.Fragments = append(message.Fragments, JSONFragment{Start: start + offset + fragment.Start, End: start + offset + fragment.End, Line: line})
			}
			if complete {
				message.Text = p.body.String()
				if !utf8.ValidString(message.Text) {
					diagnostic := newProviderJSONDiagnostic("UTF-8", line, startLine, len(message.Fragments), len(message.Text), firstBytes, consumed, 0, message.Fragments)
					diagnostics = append(diagnostics, diagnostic)
					quarantined[comp] = len(diagnostics) - 1
					messages[p.index] = ProviderJSON{}
					delete(pending, comp)
					start = next
					continue
				}
				if !json.Valid([]byte(message.Text)) {
					syntaxOffset := providerJSONSyntaxOffset(message.Text)
					diagnostic := newProviderJSONDiagnostic("JSON syntax", line, startLine, len(message.Fragments), len(message.Text), firstBytes, consumed, syntaxOffset, message.Fragments)
					diagnostics = append(diagnostics, diagnostic)
					quarantined[comp] = len(diagnostics) - 1
					messages[p.index] = ProviderJSON{}
					delete(pending, comp)
					start = next
					continue
				}
				delete(pending, comp)
			}
		}
		start = next
	}
	for _, p := range pending {
		message := messages[p.index]
		startLine := message.Fragments[0].Line
		firstBytes := message.Fragments[0].End - message.Fragments[0].Start
		diagnostic := ProviderJSONDiagnostic{
			Code:               "incomplete",
			Line:               lastLine,
			StartLine:          startLine,
			FragmentCount:      len(message.Fragments),
			JoinedBytes:        totalProviderJSONBytes(message.Fragments),
			FirstFragmentBytes: firstBytes,
			LastFragmentBytes:  p.lastConsumed,
			Ranges:             append([]JSONFragment(nil), message.Fragments...),
		}
		diagnostics = append(diagnostics, diagnostic)
		messages[p.index] = ProviderJSON{}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i], diagnostics[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		if providerJSONDiagnosticStart(a) != providerJSONDiagnosticStart(b) {
			return providerJSONDiagnosticStart(a) < providerJSONDiagnosticStart(b)
		}
		return a.Code < b.Code
	})
	return ProviderJSONResult{Messages: completedProviderJSON(messages), Diagnostics: diagnostics}
}

func providerJSONOwnershipUnknown(line string) bool {
	_, rest, ok := splitTimestamp(line)
	if !ok {
		return false
	}
	_, rest = splitLevel(rest)
	if !strings.HasPrefix(strings.TrimLeft(rest, " \t"), "provider.") {
		return false
	}
	comp, _, header := providerJSONOuter(line)
	return !header || !strings.HasPrefix(comp, "provider.") || len(comp) == len("provider.")
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
	// Empty provider records omit both the colon and message, but still
	// identify the owner of any following physical continuations.
	if colon < 0 && strings.HasPrefix(rest, "provider.") && !strings.ContainsAny(rest, " \t") {
		return rest, len(line), true
	}
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
	p.ranges = nil
	start := 0
	failure := func(end int, reason string) (int, bool, string) {
		p.body.WriteString(payload[start:end])
		return end, false, reason
	}
	for i := 0; i < len(payload); i++ {
		c := payload[i]
		// An object cannot start within a string, including after an escape,
		// where an object key is expected, or after a completed value/key.
		// Objects at valid value positions stay in the body even when their
		// fields resemble a UI event.
		objectKey := len(p.stack) > 0 && p.stack[len(p.stack)-1] == '{' && (p.last == '{' || p.last == ',')
		if c == '{' && (p.quoted || objectKey || p.last == '"' || p.last == '}' || p.last == ']') {
			if n := terraformUIBytes(payload[i:]); n > 0 {
				p.appendRange(payload, start, i)
				i += n - 1
				start = i + 1
				continue
			} else if n < 0 {
				return failure(i+1, "invalid inline UI envelope")
			}
		}
		if !isSpace(c) {
			p.last = c
		}
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
				return failure(i+1, "delimiter mismatch")
			}
			p.stack = p.stack[:len(p.stack)-1]
			if len(p.stack) == 0 {
				suffix := payload[i+1:]
				for {
					n := terraformUIBytes(strings.TrimLeft(suffix, " \t\r\n"))
					if n <= 0 {
						break
					}
					suffix = strings.TrimLeft(suffix, " \t\r\n")[n:]
				}
				if !providerJSONSuffix(suffix) {
					return failure(i+1, "suffix grammar")
				}
				p.appendRange(payload, start, i+1)
				return i + 1, true, ""
			}
		}
	}
	p.appendRange(payload, start, len(payload))
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

func (p *providerJSONPending) appendRange(payload string, start, end int) {
	if start == end && len(p.ranges) != 0 {
		return
	}
	p.body.WriteString(payload[start:end])
	p.ranges = append(p.ranges, JSONFragment{Start: start, End: end})
}

// Terraform UI envelopes start with a root annotation. A raw annotation quote
// inside a provider string cannot be escaped JSON data. Decode each candidate
// once, and reject malformed candidates rather than scanning their nested bytes.
func terraformUIBytes(text string) int {
	if len(text) == 0 || text[0] != '{' {
		return 0
	}
	rest := strings.TrimLeft(text[1:], " \t\r\n")
	if !strings.HasPrefix(rest, `"@`) {
		return 0
	}
	dec := json.NewDecoder(strings.NewReader(text))
	var fields map[string]json.RawMessage
	if dec.Decode(&fields) != nil {
		return -1
	}
	var module string
	if json.Unmarshal(fields["@module"], &module) != nil || module != "terraform.ui" {
		return -1
	}
	for _, key := range []string{"@level", "@message", "@timestamp", "type"} {
		var value string
		if json.Unmarshal(fields[key], &value) != nil || value == "" {
			return -1
		}
	}
	return int(dec.InputOffset())
}
