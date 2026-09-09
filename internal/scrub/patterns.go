package scrub

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var guidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
var addressPart = regexp.MustCompile(`([\pL\p{Nl}_][\pL\p{Nl}\pN\pM_-]*)\.([\pL\p{Nl}_][\pL\p{Nl}\pN\pM_-]*)(\[(?:"(?:\\.|[^"\\])*"|[0-9]+)\])?`)
var declaration = regexp.MustCompile(`\b(resource|data|module)\s+"([^"]+)"(?:\s+"([^"]+)")?`)
var sourceToken = regexp.MustCompile(`[\pL\pN_-]+`)

func (s *session) discoverPatterns(v *view) {
	for _, m := range guidPattern.FindAllStringIndex(v.text, -1) {
		if boundaries(v.text, m[0], m[1]) {
			s.discover(v.text[m[0]:m[1]], "guid", false)
		}
	}
	for _, m := range declaration.FindAllStringSubmatchIndex(v.text, -1) {
		v.protect(m[2], m[3])
		if v.text[m[2]:m[3]] == "module" {
			s.discover(v.text[m[4]:m[5]], "name", false)
		} else {
			if s.resourceTypes == nil {
				s.resourceTypes = make(map[string]bool)
			}
			s.resourceTypes[v.text[m[4]:m[5]]] = true
			v.protect(m[4]-1, m[5]+1)
			if m[6] >= 0 {
				s.discover(v.text[m[6]:m[7]], "name", false)
			}
		}
	}
	s.discoverAddresses(v, 0, len(v.text), v.addressContext)
	trimmed := strings.TrimSpace(v.text)
	if colon := strings.IndexByte(trimmed, ':'); colon > 0 {
		key := strings.ToLower(trimmed[:colon])
		category := ""
		switch key {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key":
			category = "secret"
		case "x-request-id", "x-correlation-id", "x-amzn-requestid", "x-ms-request-id":
			category = "id"
		}
		if category != "" {
			value := strings.TrimSpace(trimmed[colon+1:])
			s.discover(value, category, false)
			start := strings.Index(v.text, trimmed)
			v.protect(start, start+colon)
			valueStart := start + colon + 1
			for valueStart < len(v.text) && space(v.text[valueStart]) {
				valueStart++
			}
			v.wholeValues = append(v.wholeValues, region{valueStart, valueStart + len(value)})
		}
	}
}

func (s *session) discoverAddresses(v *view, start, end int, known bool) {
	var complete region
	flush := func() {
		if complete.end > complete.start && !exactRegion(v.addresses, complete.start, complete.end) {
			v.addresses = append(v.addresses, complete)
		}
	}
	for pos := start; pos < end; {
		m := addressPart.FindStringSubmatchIndex(v.text[pos:end])
		if m == nil {
			break
		}
		for i := range m {
			if m[i] >= 0 {
				m[i] += pos
			}
		}
		pos = m[1]
		if m[0] > start {
			previous, _ := utf8.DecodeLastRuneInString(v.text[:m[0]])
			if word(previous) {
				continue
			}
		}
		typeName := v.text[m[2]:m[3]]
		if typeName == "data" {
			pos = m[4]
			continue
		}
		if !known && typeName != "module" && !strings.Contains(typeName, "_") && !s.resourceTypes[typeName] && m[6] < 0 {
			pos = m[4]
			continue
		}
		v.protect(m[0], m[4])
		addressStart := m[0]
		if m[0] >= len("data.") && v.text[m[0]-len("data."):m[0]] == "data." {
			addressStart -= len("data.")
			v.protect(addressStart, m[0])
		}
		if complete.end > complete.start && addressStart == complete.end+1 && v.text[complete.end] == '.' {
			complete.end = m[1]
		} else {
			flush()
			complete = region{addressStart, m[1]}
		}
		s.discover(v.text[m[4]:m[5]], "name", false)
		if m[6] >= 0 {
			keyStart, keyEnd := m[6]+1, m[7]-1
			if v.text[keyStart] == '"' {
				_, value, ok := readString(v.text, keyStart)
				if ok && value != "" {
					s.discover(value, "name", false)
				}
			} else {
				v.protect(keyStart, keyEnd)
			}
		}
	}
	flush()
}
