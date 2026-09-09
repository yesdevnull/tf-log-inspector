package scrub

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

type region struct{ start, end int }
type quoted struct {
	region
	view *view
}
type view struct {
	text             string
	line             int
	whole, mandatory bool
	addressContext   bool
	resourceKey      bool
	addresses        []region
	composites       []region
	protected        []region
	keys             []region
	wholeValues      []region
	nulls            []region
	numeric          []region
	allowed          []region
	children         []quoted
}

func (v *view) protect(start, end int) { v.protected = append(v.protected, region{start, end}) }

func (s *session) parseLines(text string) []*view {
	var views []*view
	metadata, body, httpHeaders := false, false, false
	for line, start := 1, 0; start < len(text); line++ {
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
		if (strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[")) && !json.Valid([]byte(strings.TrimSpace(v.text))) {
			decoder := json.NewDecoder(strings.NewReader(text[start:]))
			var raw json.RawMessage
			if decoder.Decode(&raw) == nil {
				jsonEnd := start + int(decoder.InputOffset())
				for jsonEnd < len(text) && (text[jsonEnd] == ' ' || text[jsonEnd] == '\t' || text[jsonEnd] == '\r') {
					jsonEnd++
				}
				if jsonEnd == len(text) || text[jsonEnd] == '\n' {
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
		lifecycle := lifecycleEnvelope(trimmed)
		if strings.HasPrefix(trimmed, "HTTP/") || strings.HasPrefix(strings.ToLower(trimmed), "content-type:") || strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			httpHeaders = true
		}
		if httpHeaders && trimmed == "" {
			body = true
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			body = !lifecycle
		}
		s.parseView(v, metadata && !body, lifecycle)
		views = append(views, v)
		line += strings.Count(v.text, "\n") - 1
		start = end
	}
	return views
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
	s.discoverPatterns(v)
	if len(v.wholeValues) > 0 {
		return
	}
	trimmed := strings.TrimSpace(v.text)
	if _, ok := guidCore(trimmed); ok {
		return
	}
	if !v.whole || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if json.Valid([]byte(trimmed)) {
			if !v.whole {
				v.protectJSONSyntax()
			}
			i := skipSpace(v.text, 0)
			s.jsonValue(v, &i, "", nil, lifecycle)
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
	for i := 0; i < len(v.text); {
		if v.text[i] == '"' {
			end, decoded, ok := readString(v.text, i)
			if !ok {
				s.unsupported++
				break
			}
			child := &view{text: decoded, line: v.line, whole: true}
			s.parseView(child, false, false)
			v.children = append(v.children, quoted{region{i, end}, child})
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
		protected := metadata && preservedField(key)
		if v.text[valueStart] == '"' {
			end, decoded, ok := readString(v.text, valueStart)
			if !ok {
				s.unsupported++
				break
			}
			child := &view{text: decoded, line: v.line, whole: true}
			if metadata && key == "tf_provider_addr" {
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
			v.children = append(v.children, quoted{region{valueStart, end}, child})
			i = end
		} else {
			end := valueStart
			for end < len(v.text) && !space(v.text[end]) {
				end++
			}
			if metadata && key == "tf_provider_addr" {
				s.provider(v, valueStart, end, false)
			} else if protected {
				v.protect(valueStart, end)
			} else {
				value := v.text[valueStart:end]
				if category == "secret" {
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
			i = end
		}
	}
}

func (s *session) jsonValue(v *view, i *int, key string, path []string, lifecycle bool) {
	*i = skipSpace(v.text, *i)
	start := *i
	category := fieldCategory(key)
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
			s.jsonValue(v, i, k, childPath, lifecycle)
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
			s.jsonValue(v, i, key, path, lifecycle)
			*i = skipSpace(v.text, *i)
			if v.text[*i] == ',' {
				*i++
				*i = skipSpace(v.text, *i)
			}
		}
		*i++
	case '"':
		end, decoded, _ := readString(v.text, start)
		child := &view{text: decoded, line: v.line, whole: true}
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
		v.children = append(v.children, quoted{region{start, end}, child})
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
