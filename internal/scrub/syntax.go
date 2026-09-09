package scrub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

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
}

func (v *view) protect(start, end int) { v.protected = append(v.protected, region{start, end}) }

var fieldAssignment = regexp.MustCompile(`(?:^|[ \t])([^ \t=]+)[ \t]*=[ \t]*`)

func responseBodyStart(text string) int {
	for _, m := range fieldAssignment.FindAllStringSubmatchIndex(text, -1) {
		key := text[m[2]:m[3]]
		if logfmt.ValidKey(key) && keyWords(key) == "http/response/body" {
			return m[1]
		}
	}
	return -1
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
	var views []*view
	metadata, body, httpHeaders := false, false, false
	responseJSON := false
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
		if bodyStart < 0 {
			bodyStart = responseBodyStart(v.text)
		}
		if bodyStart >= 0 {
			trim = strings.TrimSpace(v.text[bodyStart:])
			jsonStart += bodyStart
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
		if h.HasTS {
			metadata, body, httpHeaders = true, false, false
			responseJSON = strings.HasPrefix(h.Msg, "HTTP Response")
			chunked = false
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
		trimmed := strings.TrimSpace(v.text)
		responseJSON = responseJSON || bodyStart >= 0
		lifecycle := !responseJSON && lifecycleEnvelope(trimmed)
		if strings.HasPrefix(trimmed, "HTTP/") {
			responseJSON = true
		} else if httpRequestLine(trimmed) {
			responseJSON = false
		}
		if httpRequestLine(trimmed) || strings.HasPrefix(trimmed, "HTTP/") || strings.HasPrefix(strings.ToLower(trimmed), "content-type:") || strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			httpHeaders = true
		}
		if httpHeaders && !body {
			key, value, ok := strings.Cut(trimmed, ":")
			if ok && strings.EqualFold(key, "Transfer-Encoding") {
				for _, coding := range strings.Split(value, ",") {
					chunked = chunked || strings.EqualFold(strings.TrimSpace(coding), "chunked")
				}
			}
		}
		if httpHeaders && trimmed == "" && !body {
			body = true
			if chunked {
				chunkEnd = end + chunkedBodyLength(text[end:])
			}
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			body = !lifecycle
		}
		v.responseJSON = responseJSON && !lifecycle
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
		if responseJSON && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && !json.Valid([]byte(trimmed)) {
			s.parseErr = fmt.Errorf("HTTP at line %d: fragmented JSON response unsupported", line+1)
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
	if v.whole && (strings.HasPrefix(first, "HTTP/") || httpRequestLine(first)) && strings.Contains(trimmed, "\n") {
		pos := 0
		for _, child := range s.parseLines(v.text) {
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
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			s.unsupported++
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
	for i := 0; i < len(v.text); {
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
		if v.text[i] == '"' {
			end, decoded, ok := readString(v.text, i)
			if !ok {
				s.unsupported++
				break
			}
			child := &view{text: decoded, line: v.line, whole: true, responseJSON: v.responseJSON}
			s.parseView(child, false, false)
			v.children = append(v.children, quoted{region{i, end}, child, false})
			i = end
			continue
		}
		if i > 0 && !space(v.text[i-1]) {
			i++
			continue
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
		v.keys = append(v.keys, region{start, start + len(key)})
		valueStart := skipSpace(v.text, sep+1)
		if valueStart >= len(v.text) {
			break
		}
		category := fieldCategory(key)
		planAssignment := sep > i || valueStart > sep+1
		// Hclog metadata requires adjacent key=value bytes; spaced assignments
		// remain useful for discovery but are not parser-recognised fields.
		metadataField := metadata && sep == i && valueStart == sep+1
		protected := metadataField && preservedField(key)
		for valueStart < len(v.text) {
			if v.text[valueStart] == '"' {
				end, decoded, ok := readString(v.text, valueStart)
				if !ok {
					s.unsupported++
					i = len(v.text)
					break
				}
				child := &view{text: decoded, line: v.line, whole: true, responseJSON: v.responseJSON || keyWords(key) == "http/response/body"}
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
				if keyWords(key) == "http/response/body" && strings.ContainsAny(v.text[valueStart:valueStart+1], "{[") {
					decoder := json.NewDecoder(strings.NewReader(v.text[valueStart:]))
					var raw json.RawMessage
					if decoder.Decode(&raw) == nil {
						end := valueStart + int(decoder.InputOffset())
						child := &view{text: v.text[valueStart:end], line: v.line, whole: true, responseJSON: true}
						s.parseView(child, false, false)
						v.children = append(v.children, quoted{region{valueStart, end}, child, true})
						i = end
						break
					}
				}
				end := valueStart
				for end < len(v.text) && !space(v.text[end]) {
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
	responseJSON = responseJSON || keyWords(key) == "http/response/body"
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
		child := &view{text: decoded, line: v.line, whole: true, responseJSON: responseJSON}
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
	for i := 0; i < len(v.text); i++ {
		if v.text[i] == '"' {
			end, _, ok := readString(v.text, i)
			if ok {
				v.protect(i, i+1)
				v.protect(end-1, end)
				i = end - 1
				continue
			}
		}
		if strings.ContainsRune("{}[]:,", rune(v.text[i])) {
			v.protect(i, i+1)
		}
	}
}

func readString(text string, start int) (int, string, bool) {
	for i := start + 1; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '"' {
			var value string
			raw := text[start : i+1]
			if json.Unmarshal([]byte(raw), &value) == nil {
				return i + 1, value, true
			}
			if value, err := strconv.Unquote(raw); err == nil {
				return i + 1, value, true
			}
			return i + 1, "", false
		}
	}
	return len(text), "", false
}

func space(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func skipSpace(text string, i int) int {
	for i < len(text) && space(text[i]) {
		i++
	}
	return i
}
func isNumber(s string) bool {
	return s != "" && json.Valid([]byte(s)) && (s[0] == '-' || s[0] >= '0' && s[0] <= '9')
}
