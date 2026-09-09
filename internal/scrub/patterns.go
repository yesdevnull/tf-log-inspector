package scrub

import (
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var guidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
var guidSequencePattern = regexp.MustCompile(guidPattern.String() + `(?:-` + guidPattern.String() + `){2}`)
var addressPart = regexp.MustCompile(`([\pL\p{Nl}_][\pL\p{Nl}\pN\pM_-]*)\.([\pL\p{Nl}_][\pL\p{Nl}\pN\pM_-]*)(\[(?:"(?:\\.|[^"\\])*"|[0-9]+)\])?`)
var declaration = regexp.MustCompile(`\b(resource|data|module)\s+"([^"]+)"(?:\s+"([^"]+)")?`)
var sourceToken = regexp.MustCompile(`[\pL\pN_-]+`)
var emailPattern = regexp.MustCompile(`[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?\.[a-zA-Z]{2,}`)
var ipPattern = regexp.MustCompile(`[0-9A-Fa-f:.]+(?:%[a-zA-Z0-9_-]+)?`)
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"<>\\]+`)
var arnPattern = regexp.MustCompile(`arn:[a-z0-9-]+:[a-z0-9-]+:[a-z0-9-]*:[0-9]*:[^\s"<>\\]+`)
var cloudPathPattern = regexp.MustCompile(`(?i)(?:/subscriptions/|(?://[a-z0-9.-]+\.googleapis\.com/|/)?projects/)[^\s"<>\\?#]+`)
var localPathPattern = regexp.MustCompile(`(?:[A-Za-z]:[\\/]|\\\\|/)[^\s"<>]+`)
var privatePEMPattern = regexp.MustCompile(`(?s)-----BEGIN ([A-Z ]*PRIVATE KEY)-----\r?\n(.*?)-----END ([A-Z ]*PRIVATE KEY)-----`)

func (s *session) discoverPatterns(v *view) {
	s.discoverAzureEndpoints(v)
	for _, m := range guidSequencePattern.FindAllStringIndex(v.text, -1) {
		if boundaries(v.text, m[0], m[1]) {
			s.discoverGUIDSequence(v.text[m[0]:m[1]])
		}
	}
	for _, m := range privatePEMPattern.FindAllStringSubmatchIndex(v.text, -1) {
		if v.text[m[2]:m[3]] != v.text[m[6]:m[7]] {
			continue
		}
		payload := v.text[m[4]:m[5]]
		if strings.TrimSpace(payload) == "" {
			continue
		}
		s.discover(payload, "secret", false)
		s.candidates[payload].pem = true
		v.wholeValues = append(v.wholeValues, region{m[4], m[5]})
		v.protect(m[0], m[4])
		v.protect(m[5], m[1])
	}
	for _, m := range localPathPattern.FindAllStringIndex(v.text, -1) {
		if m[0] > 0 && !strings.ContainsRune(" \t\r\n\"='(", rune(v.text[m[0]-1])) {
			continue
		}
		value := strings.TrimRight(v.text[m[0]:m[1]], ",;)]}")
		if v.whole && m[0] == 0 {
			value = v.text
		}
		if s.discoverCloudPath(value) == nil {
			s.discoverPath(value)
		}
		s.markComposite(v, value, m[0])
	}
	for _, m := range cloudPathPattern.FindAllStringIndex(v.text, -1) {
		value := v.text[m[0]:m[1]]
		value = strings.TrimRight(value, ",;)]}")
		s.discoverCloudPath(strings.TrimRight(value, ",;)]}"))
		s.markComposite(v, value, m[0])
	}
	for _, m := range arnPattern.FindAllStringIndex(v.text, -1) {
		value := strings.TrimRight(v.text[m[0]:m[1]], ",;)]}")
		s.discoverARN(value)
		s.markComposite(v, value, m[0])
	}
	for _, m := range urlPattern.FindAllStringIndex(v.text, -1) {
		value := strings.TrimRight(v.text[m[0]:m[1]], ",;)}")
		s.discoverURL(value)
		s.markComposite(v, value, m[0])
	}
	for _, m := range ipPattern.FindAllStringIndex(v.text, -1) {
		value := strings.TrimRight(v.text[m[0]:m[1]], ".")
		addr, err := netip.ParseAddr(value)
		if err != nil {
			if endpoint, parseErr := netip.ParseAddrPort(value); parseErr == nil {
				addr, err = endpoint.Addr(), nil
				value = value[:strings.LastIndexByte(value, ':')]
			}
		}
		if err == nil && boundaries(v.text, m[0], m[0]+len(value)) {
			s.discover(value, "network", false)
			s.candidates[value].ip = addr
		}
	}
	for _, value := range emailPattern.FindAllString(v.text, -1) {
		if parsed, err := mail.ParseAddress(value); err == nil && parsed.Address == value {
			s.discover(value, "email", false)
			s.candidates[value].email = true
		}
	}
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
	s.discoverHTTPHeaders(v)
}

func (s *session) discoverHTTPHeaders(v *view) {
	offset := 0
	for line := range strings.SplitAfterSeq(v.text, "\n") {
		trimmed := strings.TrimSpace(line)
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
				start := offset + strings.Index(line, trimmed)
				v.protect(start, start+colon)
				valueStart := start + colon + 1
				for valueStart < len(v.text) && space(v.text[valueStart]) {
					valueStart++
				}
				v.wholeValues = append(v.wholeValues, region{valueStart, valueStart + len(value)})
			}
		}
		offset += len(line)
	}
}

func (s *session) markComposite(v *view, value string, start int) {
	if c := s.candidates[value]; c != nil && len(c.parts) > 0 {
		v.composites = append(v.composites, region{start, start + len(value)})
	}
}

func (s *session) discoverPath(value string) {
	start := 1
	if len(value) >= 3 && value[1] == ':' {
		start = 3
	} else if strings.HasPrefix(value, `\\`) {
		start = 2
	}
	if start >= len(value) {
		return
	}
	var parts []compositePart
	for pos := start; pos < len(value); {
		end := pos
		for end < len(value) && value[end] != '/' && value[end] != '\\' {
			end++
		}
		segment := value[pos:end]
		stemEnd := end
		if dot := strings.LastIndexByte(segment, '.'); dot > 0 && end == len(value) {
			stemEnd = pos + dot
		}
		if segment != "" && segment != "." && segment != ".." {
			parts = append(parts, s.part(value[pos:stemEnd], "name", pos, stemEnd, ""))
		}
		pos = end + 1
	}
	if len(parts) > 0 {
		s.composite(value, "path", parts, "")
	}
}

func (s *session) discoverCloudPath(value string) *candidate {
	start := 0
	if strings.HasPrefix(value, "//") {
		at := strings.IndexByte(value[2:], '/')
		if at < 0 || !strings.HasSuffix(value[2:at+2], ".googleapis.com") {
			return nil
		}
		start = at + 3
	} else if strings.HasPrefix(value, "/") {
		start = 1
	}
	segments := strings.Split(value[start:], "/")
	if len(segments) < 2 {
		return nil
	}
	azure := strings.EqualFold(segments[0], "subscriptions")
	if !azure && segments[0] != "projects" {
		return nil
	}
	var parts []compositePart
	var structure []string
	pos := start
	for i := 0; i+1 < len(segments); i += 2 {
		key, identifier := segments[i], segments[i+1]
		pos += len(key) + 1
		if identifier == "" {
			return nil
		}
		decodedKey, err := url.PathUnescape(key)
		if err != nil {
			return nil
		}
		structure = append(structure, decodedKey)
		decoded, err := url.PathUnescape(identifier)
		if err != nil {
			return nil
		}
		if !(azure && strings.EqualFold(key, "providers")) {
			parts = append(parts, s.part(decoded, "name", pos, pos+len(identifier), "path"))
		} else {
			structure = append(structure, decoded)
		}
		pos += len(identifier) + 1
	}
	c := s.composite(value, "cloud", parts, "")
	c.decodedStructure = structure
	return c
}

func (s *session) discoverARN(value string) {
	fields := strings.SplitN(value, ":", 6)
	if len(fields) != 6 || fields[5] == "" {
		return
	}
	var parts []compositePart
	pos := len(fields[0]) + len(fields[1]) + len(fields[2]) + len(fields[3]) + 4
	if fields[4] != "" {
		if len(fields[4]) != 12 {
			return
		}
		part := s.part(fields[4], "cloud", pos, pos+len(fields[4]), "")
		part.candidate.numeric, part.candidate.digits = true, 12
		parts = append(parts, part)
	}
	pos += len(fields[4]) + 1
	resource := fields[5]
	if fields[2] != "s3" {
		if at := strings.IndexAny(resource, "/:"); at >= 0 {
			pos += at + 1
			resource = resource[at+1:]
		}
	}
	for _, segment := range strings.FieldsFunc(resource, func(r rune) bool { return r == '/' || r == ':' }) {
		at := strings.Index(value[pos:], segment) + pos
		parts = append(parts, s.part(segment, "name", at, at+len(segment), ""))
		pos = at + len(segment)
	}
	if len(parts) > 0 {
		s.composite(value, "cloud", parts, "")
	}
}

func (s *session) composite(value, category string, parts []compositePart, suffix string) *candidate {
	s.discover(value, category, false)
	c := s.candidates[value]
	if len(c.parts) == 0 {
		c.parts, c.suffix = parts, suffix
	}
	return c
}

func (s *session) part(value, category string, start, end int, encoding string) compositePart {
	s.discover(value, category, false)
	s.discoverGUIDSequence(value)
	return compositePart{region: region{start, end}, candidate: s.candidates[value], encoding: encoding}
}

func (s *session) discoverGUIDSequence(value string) {
	if len(value) != 110 || !guidSequencePattern.MatchString(value) {
		return
	}
	var parts []compositePart
	for start := 0; start < len(value); start += 37 {
		parts = append(parts, s.part(value[start:start+36], "guid", start, start+36, ""))
	}
	s.composite(value, "guid", parts, "").format = "guid-sequence"
}

func (s *session) discoverHost(value string) *candidate {
	if addr, err := netip.ParseAddr(value); err == nil {
		s.discover(value, "network", false)
		s.candidates[value].ip = addr
		return s.candidates[value]
	}
	if value == "" || strings.ContainsAny(value, " /:@[]\\") {
		return nil
	}
	labels := strings.Split(strings.TrimSuffix(value, "."), ".")
	for _, label := range labels {
		if label == "" {
			return nil
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return nil
			}
		}
	}
	if azureService(value) != "" {
		part := s.part(labels[0], "name", 0, len(labels[0]), "")
		part.candidate.hostname = true
		return s.composite(value, "network", []compositePart{part}, "")
	}
	if len(labels) == 1 && !strings.HasSuffix(value, ".") {
		s.discover(value, "network", false)
		c := s.candidates[value]
		c.hostname, c.suffix = true, ".example.invalid"
		return c
	}
	var parts []compositePart
	pos := 0
	for _, label := range labels {
		part := s.part(label, "name", pos, pos+len(label), "")
		part.candidate.hostname = true
		parts = append(parts, part)
		pos += len(label) + 1
	}
	suffix := ".example.invalid"
	if strings.HasSuffix(value, ".") {
		suffix = "example.invalid."
	}
	return s.composite(value, "network", parts, suffix)
}

func (s *session) discoverURL(value string) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return
	}
	host := s.discoverHost(u.Hostname())
	if host == nil {
		return
	}
	var parts []compositePart
	var structure []string
	start := strings.Index(value, "://") + 3
	// URL parsing decodes host escapes, so source spans must use raw bytes.
	authorityEnd := len(value)
	if at := strings.IndexAny(value[start:], "/?#"); at >= 0 {
		authorityEnd = start + at
	}
	if u.User != nil {
		end := strings.LastIndexByte(value[start:authorityEnd], '@') + start
		userEnd := end
		if colon := strings.IndexByte(value[start:end], ':'); colon >= 0 {
			userEnd = start + colon
		}
		if u.User.Username() != "" {
			parts = append(parts, s.part(u.User.Username(), "name", start, userEnd, "user"))
		}
		if password, ok := u.User.Password(); ok && password != "" {
			parts = append(parts, s.part(password, "secret", userEnd+1, end, "user"))
		}
		start = end + 1
	}
	hostStart := start
	hostEnd := authorityEnd
	if value[hostStart] == '[' {
		hostStart++
		hostEnd = hostStart + strings.LastIndexByte(value[hostStart:authorityEnd], ']')
	} else if colon := strings.LastIndexByte(value[hostStart:authorityEnd], ':'); colon >= 0 {
		hostEnd = hostStart + colon
	}
	parts = append(parts, compositePart{region: region{hostStart, hostEnd}, candidate: host})
	pathStart := authorityEnd
	pathEnd := pathStart + len(u.EscapedPath())
	pos := pathStart
	service := azureService(u.Hostname())
	if cloud := s.discoverCloudPath(u.EscapedPath()); cloud != nil {
		parts = append(parts, compositePart{region: region{pathStart, pathEnd}, candidate: cloud})
	} else {
		for index, segment := range strings.Split(u.EscapedPath(), "/") {
			if decoded, err := url.PathUnescape(segment); err == nil && decoded != "" {
				if service == "vault" && index == 1 && vaultCollection(decoded) {
					structure = append(structure, decoded)
				} else {
					part := s.part(decoded, "name", pos, pos+len(segment), "path")
					if service == "vault" || service == "storage" && index == 1 {
						part.candidate.hostname = true
					}
					parts = append(parts, part)
				}
			}
			pos += len(segment) + 1
		}
	}
	if u.RawQuery != "" {
		pos = pathEnd + 1
		for _, pair := range strings.Split(u.RawQuery, "&") {
			key, raw, ok := strings.Cut(pair, "=")
			decodedKey, e1 := url.QueryUnescape(key)
			if e1 == nil {
				structure = append(structure, decodedKey)
			}
			decoded, e2 := url.QueryUnescape(raw)
			if ok && e1 == nil && e2 == nil && decoded != "" {
				category := fieldCategory(decodedKey)
				if service == "storage" {
					switch decodedKey {
					case "sig":
						category = "secret"
					case "si", "skoid", "sktid", "saoid", "suoid", "sduoid", "skdutid", "scid":
						category = "id"
					case "spk", "srk", "epk", "erk":
						category = "name"
					case "sip":
						category = "network"
						s.discoverAzureIPRestriction(decoded)
					}
				}
				if category != "" {
					parts = append(parts, s.part(decoded, category, pos+len(key)+1, pos+len(pair), "query"))
				} else {
					parts = append(parts, compositePart{region: region{pos + len(key) + 1, pos + len(pair)}, encoding: "query", literal: decoded})
				}
			}
			pos += len(pair) + 1
		}
	}
	if u.Fragment != "" {
		start := strings.IndexByte(value, '#') + 1
		parts = append(parts, compositePart{region: region{start, len(value)}, encoding: "fragment", literal: u.Fragment})
	}
	c := s.composite(value, "network", parts, "")
	c.format, c.decodedStructure = "url", structure
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
		if overlaps(v.composites, m[0], m[1]) {
			continue
		}
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
					v.numeric = append(v.numeric, region{keyStart, keyEnd})
				}
			} else {
				v.protect(keyStart, keyEnd)
			}
		}
	}
	flush()
}
