package scrub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

type region struct{ start, end int }
type quoted struct {
	region
	view     *view
	verbatim bool
}
type view struct {
	text             string
	line             int
	whole, mandatory bool
	addressContext   bool
	resourceKey      bool
	responseJSON     bool
	jsonBody         bool
	textField        bool
	chunk            *chunkFrame
	addresses        []region
	composites       []region
	protected        []region
	keys             []region
	wholeValues      []region
	credentials      []region
	emails           []region
	nulls            []region
	numeric          []region
	allowed          []region
	children         []quoted
	cuts             []sourceCut
	unsupported      []unsupportedInput
}

func (v *view) protect(start, end int) { v.protected = append(v.protected, region{start, end}) }

var fieldAssignment = regexp.MustCompile(`(?:^|[ \t])([^ \t=]+)[ \t]*=[ \t]*`)

func httpBodyKind(key string) string {
	switch keyWords(key) {
	case "http/request/body":
		return "request"
	case "http/response/body":
		return "response"
	}
	return ""
}

func httpBodyStart(text string) (int, string) {
	// Normalising an HTTP body key preserves the contiguous letters in
	// "body". Most metadata has no such key and needs no assignment parsing.
	for start := 0; ; {
		at := strings.IndexAny(text[start:], "bB")
		if at < 0 || start+at+4 > len(text) {
			return -1, ""
		}
		at += start
		if strings.EqualFold(text[at:at+4], "body") {
			break
		}
		start = at + 1
	}
	for _, m := range fieldAssignment.FindAllStringSubmatchIndex(text, -1) {
		key := text[m[2]:m[3]]
		if kind := httpBodyKind(key); logfmt.ValidKey(key) && kind != "" {
			return m[1], kind
		}
	}
	return -1, ""
}

// Providers can log a JSON body directly as their message, without an HTTP
// status line or body field. Keep its source offset after peeling log prefixes.
func providerJSONStart(text string) int {
	h := logfmt.ParseHeader(strings.TrimRight(text, "\r\n"))
	msg := strings.TrimLeft(h.Msg, " \t\r\n")
	// An unquoted identifier in brackets is a diagnostic tag, not a JSON
	// array. JSON's literal identifiers remain valid array elements.
	if strings.HasPrefix(msg, "[") {
		if end := strings.IndexByte(msg, ']'); end > 0 {
			label := strings.TrimSpace(msg[1:end])
			if logfmt.ValidKey(label) && label != "true" && label != "false" && label != "null" {
				return -1
			}
		}
	}
	if h.HasTS && strings.HasPrefix(h.Comp, "provider.") && (strings.HasPrefix(msg, "{") || strings.HasPrefix(msg, "[")) {
		return strings.Index(text, msg)
	}
	return -1
}

func (s *session) parseLines(text string) []*view {
	return s.parseBodyLines(text, false, false)
}

// Embedded response text cannot confer logger metadata or lifecycle exemptions.
func (s *session) parseBodyLines(text string, responseContext, textField bool) []*view {
	var views []*view
	metadata, body, httpHeaders := false, false, false
	httpComponent := ""
	responseJSON := responseContext
	chunked := false
	chunkEnd := 0
	for line, start := 1, 0; start < len(text); line++ {
		if chunked && start < chunkEnd {
			views = append(views, s.parseChunks(text[start:chunkEnd], line, responseJSON)...)
			line += strings.Count(text[start:chunkEnd], "\n") - 1
			start = chunkEnd
			continue
		}
		end := strings.IndexByte(text[start:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += start + 1
		}
		v := &view{text: text[start:end], line: line}
		if strings.Contains(v.text, "-----BEGIN ") {
			if m := privatePEMPattern.FindStringIndex(text[start:]); m != nil && m[0] < len(v.text) {
				pemEnd := start + m[1]
				if newline := strings.IndexByte(text[pemEnd:], '\n'); newline >= 0 {
					end = pemEnd + newline + 1
				} else {
					end = len(text)
				}
				v.text = text[start:end]
			}
		}
		trim := strings.TrimLeft(v.text, " \t\r\n")
		jsonStart := start
		bodyStart := providerJSONStart(v.text)
		bodyKind := "response"
		if bodyStart < 0 {
			bodyStart, bodyKind = httpBodyStart(v.text)
		}
		if bodyStart >= 0 {
			jsonStart += bodyStart
			trim = strings.TrimLeft(text[jsonStart:], " \t\r\n")
		}
		if (strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[")) && !json.Valid([]byte(strings.TrimSpace(v.text))) {
			decoder := json.NewDecoder(strings.NewReader(text[jsonStart:]))
			var raw json.RawMessage
			if decoder.Decode(&raw) == nil {
				jsonEnd := jsonStart + int(decoder.InputOffset())
				for jsonEnd < len(text) && (text[jsonEnd] == ' ' || text[jsonEnd] == '\t' || text[jsonEnd] == '\r') {
					jsonEnd++
				}
				if bodyStart >= 0 {
					if newline := strings.IndexByte(text[jsonEnd:], '\n'); newline >= 0 {
						end = jsonEnd + newline + 1
					} else {
						end = len(text)
					}
					v.text = text[start:end]
				} else if jsonEnd == len(text) || text[jsonEnd] == '\n' {
					if jsonEnd < len(text) {
						jsonEnd++
					}
					end = jsonEnd
					v.text = text[start:end]
				}
			}
		}
		h := logfmt.ParseHeader(strings.TrimRight(v.text, "\r\n"))
		trimmed := strings.TrimSpace(v.text)
		framing := trimmed
		if h.HasTS {
			framing = strings.TrimSpace(h.Msg)
		}
		if h.HasTS && !responseContext {
			key, _, header := strings.Cut(framing, ":")
			continuesHTTP := httpHeaders && !body && h.Comp == httpComponent && (framing == "" || header && logfmt.ValidKey(key))
			metadata = true
			if !continuesHTTP {
				body, httpHeaders = false, false
				responseJSON = strings.HasPrefix(h.Msg, "HTTP Response")
				chunked = false
			}
			httpComponent = h.Comp
			prefix := strings.Index(v.text, h.Msg)
			if prefix >= 0 {
				compStart := strings.Index(v.text, h.Comp)
				if h.Comp != "" && compStart >= 0 {
					v.protect(0, compStart)
					s.provider(v, compStart, compStart+len(h.Comp), true)
					v.protect(compStart+len(h.Comp), prefix)
				} else {
					v.protect(0, prefix)
				}
			}
		}
		responseJSON = responseJSON || bodyKind == "response"
		lifecycle := !responseJSON && lifecycleEnvelope(trimmed)
		if strings.HasPrefix(framing, "HTTP/") {
			responseJSON = true
		} else if httpRequestLine(framing) && !responseContext {
			responseJSON = false
		}
		if httpRequestLine(framing) || strings.HasPrefix(framing, "HTTP/") || strings.HasPrefix(strings.ToLower(framing), "content-type:") || strings.HasPrefix(strings.ToLower(framing), "content-length:") {
			httpHeaders = true
		}
		if httpHeaders && !body {
			key, value, ok := strings.Cut(framing, ":")
			if ok && strings.EqualFold(key, "Transfer-Encoding") {
				for _, coding := range strings.Split(value, ",") {
					chunked = chunked || strings.EqualFold(strings.TrimSpace(coding), "chunked")
				}
			}
		}
		if httpHeaders && framing == "" && !body {
			body = true
			if chunked {
				chunkEnd = end + chunkedBodyLength(text[end:])
			}
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			body = !lifecycle
		}
		v.responseJSON = responseJSON && !lifecycle
		v.textField = textField && !httpHeaders
		s.parseView(v, metadata && !body, lifecycle)
		views = append(views, v)
		line += strings.Count(v.text, "\n") - 1
		start = end
	}
	return views
}

type chunkFrame struct {
	sizeEnd, headerLength, dataLength, endingLength int
}

// parseChunks receives only framing already validated by chunkedBodyLength.
func (s *session) parseChunks(text string, line int, responseJSON bool) []*view {
	var views []*view
	for pos := 0; pos < len(text); {
		start := pos
		at := strings.IndexByte(text[pos:], '\n')
		header := strings.TrimSuffix(text[pos:pos+at], "\r")
		size, _, _ := strings.Cut(header, ";")
		n, _ := strconv.ParseUint(size, 16, 64)
		pos += at + 1
		head := s.parseChunkHeader(text[start:pos], len(size), line)
		if n == 0 {
			views = append(views, head)
			v := &view{text: text[pos:], line: line + 1}
			s.parseView(v, false, false)
			views = append(views, v)
			break
		}
		dataStart := pos
		pos += int(n)
		endingLength := 1
		if strings.HasPrefix(text[pos:], "\r\n") {
			endingLength = 2
		}
		child := &view{text: text[dataStart:pos], line: line + 1, responseJSON: responseJSON}
		trimmed := strings.TrimSpace(child.text)
		if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && !json.Valid([]byte(trimmed)) {
			s.parseErr = fmt.Errorf("HTTP at line %d: fragmented JSON body unsupported", line+1)
			return nil
		}
		s.parseView(child, false, false)
		v := &view{text: text[start : pos+endingLength], line: line, chunk: &chunkFrame{len(size), dataStart - start, int(n), endingLength}}
		v.children = append(v.children, quoted{region{0, dataStart - start}, head, true})
		v.children = append(v.children, quoted{region{dataStart - start, pos - start}, child, true})
		views = append(views, v)
		pos += endingLength
		line += strings.Count(text[start:pos], "\n")
	}
	return views
}

func (s *session) parseChunkHeader(text string, sizeEnd, line int) *view {
	// Normalise only field separators for discovery, retaining original offsets.
	fields := []byte(text)
	for i := sizeEnd; i < len(fields); i++ {
		if fields[i] == '"' {
			end, _, ok := readString(text, i)
			if ok {
				i = end - 1
				continue
			}
		}
		if fields[i] == ';' {
			fields[i] = ' '
		}
	}
	head := &view{text: string(fields), line: line}
	s.parseView(head, false, false)
	for _, key := range head.keys {
		head.protect(key.start, key.end)
		pos := skipSpace(head.text, key.end)
		if pos >= len(head.text) || head.text[pos] != '=' {
			continue
		}
		pos = skipSpace(head.text, pos+1)
		if pos >= len(head.text) || head.text[pos] == '"' {
			continue
		}
		end := pos
		for end < len(head.text) && !space(head.text[end]) {
			end++
		}
		s.discoverPatterns(&view{text: head.text[pos:end], line: line})
	}
	head.text = text
	head.protect(0, sizeEnd)
	return head
}

// Provider loggers can pretty-print bodies after producing their HTTP headers.
// Enforce wire lengths only when the original dump has verifiable chunk framing.
func chunkedBodyLength(text string) int {
	pos := 0
	for pos < len(text) {
		at := strings.IndexByte(text[pos:], '\n')
		if at < 0 {
			return 0
		}
		header := strings.TrimSuffix(text[pos:pos+at], "\r")
		size, _, _ := strings.Cut(header, ";")
		n, err := strconv.ParseUint(size, 16, 64)
		pos += at + 1
		if err != nil || n > uint64(len(text)-pos) {
			return 0
		}
		if n == 0 {
			for pos < len(text) {
				at = strings.IndexByte(text[pos:], '\n')
				if at < 0 {
					return 0
				}
				trailer := strings.TrimSuffix(text[pos:pos+at], "\r")
				pos += at + 1
				if trailer == "" {
					return pos
				}
				if !strings.Contains(trailer, ":") {
					return 0
				}
			}
			return 0
		}
		pos += int(n)
		if strings.HasPrefix(text[pos:], "\r\n") {
			pos += 2
		} else if strings.HasPrefix(text[pos:], "\n") {
			pos++
		} else {
			return 0
		}
	}
	return 0
}

func httpRequestLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) != 3 || !logfmt.ValidKey(fields[0]) {
		return false
	}
	_, _, ok := http.ParseHTTPVersion(fields[2])
	return ok
}

func lifecycleEnvelope(text string) bool {
	if !strings.HasPrefix(text, "{") {
		return false
	}
	var envelope struct {
		Level     *string `json:"@level"`
		Timestamp *string `json:"@timestamp"`
	}
	return json.Unmarshal([]byte(text), &envelope) == nil && envelope.Level != nil && envelope.Timestamp != nil
}

func (s *session) parseView(v *view, metadata, lifecycle bool) {
	trimmed := strings.TrimSpace(v.text)
	first, _, _ := strings.Cut(trimmed, "\n")
	if v.whole && (v.responseJSON || strings.HasPrefix(first, "HTTP/") || httpRequestLine(first)) && strings.Contains(trimmed, "\n") && !json.Valid([]byte(trimmed)) {
		pos := 0
		for _, child := range s.parseBodyLines(v.text, v.responseJSON, !v.jsonBody) {
			v.children = append(v.children, quoted{region{pos, pos + len(child.text)}, child, true})
			pos += len(child.text)
		}
		return
	}
	s.discoverPatterns(v)
	if _, ok := guidCore(trimmed); ok {
		return
	}
	if !v.whole || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if json.Valid([]byte(trimmed)) {
			if !v.whole {
				v.protectJSONSyntax()
			}
			i := skipSpace(v.text, 0)
			s.jsonValue(v, &i, "", nil, lifecycle, v.responseJSON)
			return
		}
		// Ordinary string values may contain brackets and braces as prose.
		// Only standalone records and explicitly identified bodies confer
		// JSON expectations; response discovery context alone does not.
		if ((!v.whole && !v.textField) || v.jsonBody) && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
			s.unsupportedAt(v, len(v.text)-len(strings.TrimLeftFunc(v.text, unicode.IsSpace)), "invalid JSON")
		}
	}
	// Preserve diagnostic classification only where it is a real header message.
	if metadata {
		for _, prefix := range []string{"Received downstream response", "Sending request downstream"} {
			if at := strings.Index(v.text, prefix); at >= 0 {
				v.protect(at, at+len(prefix))
			}
		}
	}
	bodyStart := providerJSONStart(v.text)
	for i, bracketEnd := 0, -1; i < len(v.text); {
		if bracketEnd >= 0 && i >= bracketEnd {
			bracketEnd = -1
		}
		if i == bodyStart {
			decoder := json.NewDecoder(strings.NewReader(v.text[i:]))
			var raw json.RawMessage
			if decoder.Decode(&raw) == nil {
				end := i + int(decoder.InputOffset())
				child := &view{text: v.text[i:end], line: v.line, responseJSON: true}
				s.parseView(child, false, false)
				v.children = append(v.children, quoted{region{i, end}, child, true})
				i = end
				continue
			}
			s.parseErr = fmt.Errorf("provider JSON at line %d: invalid body", v.line)
			return
		}
		if end := wholeValueEnd(v.wholeValues, i); end > i {
			i = end
			continue
		}
		// Masked provider bodies can contain long whitespace runs. Consume
		// each run once instead of searching its remaining suffix per byte.
		if space(v.text[i]) {
			i = skipSpace(v.text, i)
			continue
		}
		if v.text[i] == '"' {
			end, decoded, ok := readString(v.text, i)
			if !ok {
				s.unsupportedAt(v, i, "invalid quoted string")
				break
			}
			child := &view{text: decoded, line: v.line, whole: true, responseJSON: v.responseJSON}
			s.parseView(child, false, false)
			v.children = append(v.children, quoted{region{i, end}, child, false})
			i = end
			continue
		}
		if i > 0 && !space(v.text[i-1]) && !(v.text[i] == '[' && v.text[i-1] == ']') {
			i++
			continue
		}
		// Terraform progress encloses its identifier assignment in brackets.
		bracketed := v.text[i] == '['
		if bracketed {
			i++
		}
		start := i
		for i < len(v.text) && !space(v.text[i]) && v.text[i] != '=' && v.text[i] != ':' && v.text[i] != '"' {
			i++
		}
		key := v.text[start:i]
		sep := skipSpace(v.text, i)
		if !logfmt.ValidKey(key) || sep >= len(v.text) || v.text[sep] != '=' {
			if i == start {
				i++
			}
			continue
		}
		if bracketed {
			bracketEnd = bracketedFieldEnd(v.text, sep+1)
		}
		v.keys = append(v.keys, region{start, start + len(key)})
		valueStart := skipSpace(v.text, sep+1)
		if valueStart >= len(v.text) {
			break
		}
		category := fieldCategory(key)
		bodyKind := httpBodyKind(key)
		responseJSON := v.responseJSON
		if bodyKind != "" {
			responseJSON = bodyKind == "response"
		}
		planAssignment := sep > i || valueStart > sep+1
		// Hclog metadata requires adjacent key=value bytes; spaced assignments
		// remain useful for discovery but are not parser-recognised fields.
		metadataField := metadata && !bracketed && sep == i && valueStart == sep+1
		protected := metadataField && preservedField(key)
		for valueStart < len(v.text) {
			if v.text[valueStart] == '"' {
				end, decoded, ok := readString(v.text, valueStart)
				if !ok {
					s.unsupportedAt(v, valueStart, "invalid quoted string")
					i = len(v.text)
					break
				}
				child := &view{text: decoded, line: v.line, whole: true, responseJSON: responseJSON, jsonBody: bodyKind != ""}
				if metadataField && key == "tf_provider_addr" {
					child.mandatory = true
					s.provider(child, 0, len(decoded), false)
				} else if protected {
					child.mandatory = true
					child.protect(0, len(decoded))
				} else {
					child.addressContext = key == "addr" || key == "address"
					child.mandatory = child.addressContext
					s.discover(decoded, category, false)
					if category != "" {
						child.numeric = append(child.numeric, region{0, len(decoded)})
					}
					s.parseView(child, false, false)
				}
				v.children = append(v.children, quoted{region{valueStart, end}, child, false})
				i = end
			} else {
				if bodyKind != "" && strings.ContainsAny(v.text[valueStart:valueStart+1], "{[") {
					decoder := json.NewDecoder(strings.NewReader(v.text[valueStart:]))
					var raw json.RawMessage
					if decoder.Decode(&raw) == nil {
						end := valueStart + int(decoder.InputOffset())
						child := &view{text: v.text[valueStart:end], line: v.line, whole: true, responseJSON: responseJSON, jsonBody: true}
						s.parseView(child, false, false)
						v.children = append(v.children, quoted{region{valueStart, end}, child, true})
						i = end
						break
					}
				}
				end := valueStart
				for end < len(v.text) && end != bracketEnd && (!space(v.text[end]) || key == "id" && end < bracketEnd) {
					if space(v.text[end]) && (end == valueStart || !space(v.text[end-1])) && assignmentAhead(v.text, end) {
						break
					}
					if v.text[end] == '"' {
						quoteEnd, decoded, ok := readString(v.text, end)
						if ok {
							child := &view{text: decoded, line: v.line, whole: true, responseJSON: v.responseJSON}
							s.parseView(child, false, false)
							v.children = append(v.children, quoted{region{end, quoteEnd}, child, false})
							end = quoteEnd
							continue
						}
					}
					end++
				}
				if metadataField && key == "tf_provider_addr" {
					s.provider(v, valueStart, end, false)
				} else if protected {
					v.protect(valueStart, end)
				} else {
					value := v.text[valueStart:end]
					if value == "null" && planAssignment {
						v.nulls = append(v.nulls, region{valueStart, end})
					} else {
						if category == "secret" || category == "consent" {
							v.wholeValues = append(v.wholeValues, region{valueStart, end})
						}
						if key == "addr" || key == "address" {
							s.discoverAddresses(v, valueStart, end, true)
						}
						numeric := isNumber(value)
						s.discover(value, category, numeric)
						if numeric && category != "" {
							v.numeric = append(v.numeric, region{valueStart, end})
						}
					}
				}
				i = end
			}
			next := skipSpace(v.text, i)
			if !strings.HasPrefix(v.text[next:], "->") {
				break
			}
			valueStart = skipSpace(v.text, next+2)
			metadataField, protected = false, false
		}
	}
}

// Field delimiters precede whitespace, another group, or the end of the
// record. Brackets inside opaque IDs and IPv6 authorities need not balance.
func bracketedFieldEnd(text string, start int) int {
	for end := start; end < len(text) && text[end] != '\r' && text[end] != '\n'; end++ {
		if text[end] == '"' {
			if quoteEnd, _, ok := readString(text, end); ok {
				end = quoteEnd - 1
				continue
			}
		}
		if text[end] == ']' && (end+1 == len(text) || space(text[end+1]) || text[end+1] == '[') {
			return end
		}
	}
	return -1
}

// Whitespace can belong to an opaque Terraform ID, but another assignment
// starts a separate field whose credentials and identifiers need discovery.
func assignmentAhead(text string, start int) bool {
	start = skipSpace(text, start)
	if start < len(text) && text[start] == '[' {
		start++
	}
	end := start
	for end < len(text) && !space(text[end]) && text[end] != '=' && text[end] != ':' && text[end] != '"' {
		end++
	}
	sep := skipSpace(text, end)
	return logfmt.ValidKey(text[start:end]) && sep < len(text) && text[sep] == '='
}

func wholeValueEnd(values []region, pos int) int {
	for _, value := range values {
		if value.start <= pos && pos < value.end {
			return value.end
		}
	}
	return pos
}

func (s *session) jsonValue(v *view, i *int, key string, path []string, lifecycle, responseJSON bool) {
	*i = skipSpace(v.text, *i)
	start := *i
	category := fieldCategory(key)
	bodyKind := httpBodyKind(key)
	if bodyKind != "" {
		responseJSON = bodyKind == "response"
	}
	if responseJSON && key == "value" {
		category = "secret"
	}
	protect := lifecycle && (len(path) == 0 && (key == "@level" || key == "@timestamp" || key == "@module" || key == "type") || len(path) == 1 && path[0] == "hook" && (key == "id_key" || key == "action" || key == "elapsed_seconds"))
	if lifecycle && len(path) == 1 && path[0] == "hook" && key == "id_value" {
		category = "id"
	}
	resourceField := lifecycle && len(path) == 2 && path[0] == "hook" && path[1] == "resource"
	if resourceField && (key == "resource_type" || key == "implied_provider") {
		protect = true
	}
	switch v.text[*i] {
	case '{':
		*i++
		*i = skipSpace(v.text, *i)
		childPath := path
		if key != "" {
			childPath = append(append([]string(nil), path...), key)
		}
		for v.text[*i] != '}' {
			ks := *i
			end, k, _ := readString(v.text, ks)
			v.keys = append(v.keys, region{ks, end})
			*i = skipSpace(v.text, end) + 1
			s.jsonValue(v, i, k, childPath, lifecycle, responseJSON)
			*i = skipSpace(v.text, *i)
			if v.text[*i] == ',' {
				*i++
				*i = skipSpace(v.text, *i)
			}
		}
		*i++
	case '[':
		*i++
		*i = skipSpace(v.text, *i)
		for v.text[*i] != ']' {
			s.jsonValue(v, i, key, path, lifecycle, responseJSON)
			*i = skipSpace(v.text, *i)
			if v.text[*i] == ',' {
				*i++
				*i = skipSpace(v.text, *i)
			}
		}
		*i++
	case '"':
		end, decoded, _ := readString(v.text, start)
		child := &view{text: decoded, line: v.line, whole: true, responseJSON: responseJSON, jsonBody: bodyKind != ""}
		if resourceField && (key == "addr" || key == "module" || key == "resource") {
			child.mandatory = true
			child.addressContext = true
		}
		if key == "addr" || key == "address" {
			child.addressContext = true
			child.mandatory = true
		}
		if resourceField && key == "resource_type" {
			if s.resourceTypes == nil {
				s.resourceTypes = make(map[string]bool)
			}
			s.resourceTypes[decoded] = true
		}
		if resourceField && key == "resource_key" {
			category = "name"
			child.resourceKey = true
		}
		if protect {
			child.mandatory = true
			child.protect(0, len(decoded))
		} else {
			s.discover(decoded, category, false)
			if category != "" {
				child.numeric = append(child.numeric, region{0, len(decoded)})
			}
			s.parseView(child, false, false)
		}
		v.children = append(v.children, quoted{region{start, end}, child, false})
		*i = end
	default:
		for *i < len(v.text) && !strings.ContainsRune(",]} \t\r\n", rune(v.text[*i])) {
			*i++
		}
		value := v.text[start:*i]
		if value == "null" {
			v.nulls = append(v.nulls, region{start, *i})
		} else if protect || category == "" {
			v.protect(start, *i)
		} else {
			s.discover(value, category, isNumber(value))
			v.numeric = append(v.numeric, region{start, *i})
		}
	}
}

func (v *view) protectJSONSyntax() {
	// Large collections contain many delimiters. Count them before reserving
	// storage to avoid repeated copying of the growing protection slice.
	count := 0
	jsonSyntaxRegions(v.text, func(_, _ int) { count++ })
	v.protected = slices.Grow(v.protected, count)
	jsonSyntaxRegions(v.text, v.protect)
}

func jsonSyntaxRegions(text string, visit func(int, int)) {
	// The caller has validated this JSON. Only quote boundaries are needed;
	// decoding escaped contents would allocate values that are never used.
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			end, _ := quotedStringEnd(text, i)
			if end > 0 {
				visit(i, i+1)
				visit(end-1, end)
				i = end - 1
				continue
			}
		}
		if strings.ContainsRune("{}[]:,", rune(text[i])) {
			visit(i, i+1)
		}
	}
}

func readString(text string, start int) (int, string, bool) {
	end, plain := quotedStringEnd(text, start)
	if end == 0 {
		return len(text), "", false
	}
	// Ordinary UTF-8 needs no decoding or copy. Escapes, controls and invalid
	// encoding retain the complete decoder semantics.
	if value := text[start+1 : end-1]; plain && utf8.ValidString(value) {
		return end, value, true
	}
	var value string
	raw := text[start:end]
	if json.Unmarshal([]byte(raw), &value) == nil {
		return end, value, true
	}
	if value, err := strconv.Unquote(raw); err == nil {
		return end, value, true
	}
	return end, "", false
}

func quotedStringEnd(text string, start int) (int, bool) {
	plain := true
	for i := start + 1; i < len(text); i++ {
		if text[i] == '\\' {
			plain = false
			i++
			continue
		}
		if text[i] < ' ' {
			plain = false
		}
		if text[i] == '"' {
			return i + 1, plain
		}
	}
	return 0, false
}

func space(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func skipSpace(text string, i int) int {
	for i < len(text) && space(text[i]) {
		i++
	}
	return i
}
func isNumber(s string) bool {
	return s != "" && (s[0] == '-' || s[0] >= '0' && s[0] <= '9') && json.Valid([]byte(s))
}
