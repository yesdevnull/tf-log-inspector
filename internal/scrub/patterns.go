package scrub

import (
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var guidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
var guidSequencePattern = regexp.MustCompile(guidPattern.String() + `(?:-` + guidPattern.String() + `){2}`)
var terraformSubjectPattern = regexp.MustCompile(`organization:([^:\r\n"\\]+):project:([^:\r\n"\\]+):workspace:([^:\r\n"\\]+):run_phase:(plan|apply|\*)`)
var addressPart = regexp.MustCompile(`([\pL\p{Nl}_][\pL\p{Nl}\pN\pM_-]*)\.([\pL\p{Nl}_][\pL\p{Nl}\pN\pM_-]*)(\[(?:"(?:\\.|[^"\\])*"|[0-9]+)\])?`)
var addressAtStart = regexp.MustCompile(`^` + addressPart.String())
var declaration = regexp.MustCompile(`\b(resource|data|module)\s+"([^"]+)"(?:\s+"([^"]+)")?`)
var emailPattern = regexp.MustCompile(`[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?\.[a-zA-Z]{2,}`)
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"<>\\]+`)
var arnPattern = regexp.MustCompile(`arn:[a-z0-9-]+:[a-z0-9-]+:[a-z0-9-]*:[0-9]*:[^\s"<>\\]+`)
var cloudPathPattern = regexp.MustCompile(`(?i)(?:/subscriptions/|(?://[a-z0-9.-]+\.googleapis\.com/|/)?projects/)[^\s"<>\\?#]+`)
var localPathPattern = regexp.MustCompile(`(?:[A-Za-z]:[\\/]|\\\\|/)[^\s"<>]+`)
var privatePEMPattern = regexp.MustCompile(`(?s)-----BEGIN ([A-Z ]*PRIVATE KEY)-----\r?\n(.*?)-----END ([A-Z ]*PRIVATE KEY)-----`)
var serviceTokenPattern = regexp.MustCompile(`(?:ghs_[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|(?:gh[pousr]_|github_pat_)[A-Za-z0-9_]+|[A-Za-z0-9]+\.atlasv1\.[A-Za-z0-9_-]+)`)

type patternKey struct {
	pattern       *regexp.Regexp
	text, markers string
}

// Short values recur across responses and decoded views. Cache only their
// immutable match coordinates, with bounded entries and input lengths; field
// context and candidate discovery remain specific to each occurrence.
func (s *session) cachedPatternMatches(pattern *regexp.Regexp, text, markers string) [][]int {
	if !strings.ContainsAny(text, markers) {
		return nil
	}
	if len(text) > 256 {
		return patternMatches(pattern, text, markers)
	}
	key := patternKey{pattern, text, markers}
	if matches, ok := s.patterns[key]; ok {
		return matches
	}
	matches := patternMatches(pattern, text, markers)
	if len(s.patterns) < 4096 {
		if s.patterns == nil {
			s.patterns = make(map[patternKey][][]int)
		}
		// A short substring must not retain a much larger decoded input.
		key.text = strings.Clone(key.text)
		s.patterns[key] = matches
	}
	return matches
}

// Each marker set contains a character required by every alternative in its
// pattern. These patterns cannot contain a quote, so quoted boundaries also
// divide the search without changing matches or their original coordinates.
func patternMatches(pattern *regexp.Regexp, text, markers string) [][]int {
	if !strings.ContainsAny(text, markers) {
		return nil
	}
	var matches [][]int
	for start := 0; start < len(text); {
		end := strings.IndexByte(text[start:], '"')
		if end < 0 {
			end = len(text)
		} else {
			end += start
		}
		if strings.ContainsAny(text[start:end], markers) {
			for _, match := range pattern.FindAllStringIndex(text[start:end], -1) {
				match[0] += start
				match[1] += start
				matches = append(matches, match)
			}
		}
		start = end + 1
	}
	return matches
}

func (s *session) discoverServiceTokens(value string) []region {
	var spans []region
	for _, m := range s.cachedPatternMatches(serviceTokenPattern, value, "_.") {
		if boundaries(value, m[0], m[1]) {
			s.discover(value[m[0]:m[1]], "secret", false)
			spans = append(spans, region{m[0], m[1]})
		}
	}
	return spans
}

func (s *session) discoverPatterns(v *view) {
	// Trailing transport whitespace cannot finish any detection pattern.
	// Keep original coordinates and values while excluding provider masks
	// from repeated searches.
	text := strings.TrimRight(v.text, " \t\r\n")
	v.credentials = append(v.credentials, s.discoverServiceTokens(text)...)
	if line := strings.TrimSpace(v.text); httpRequestLine(line) {
		target := strings.Fields(line)[1]
		s.discoverURL(target)
		s.markComposite(v, target, strings.Index(v.text, target))
	}
	s.discoverAzureEndpoints(v)
	for _, m := range terraformSubjectPattern.FindAllStringSubmatchIndex(text, -1) {
		if !boundaries(v.text, m[0], m[1]) {
			continue
		}
		var parts []compositePart
		for group := 2; group <= 6; group += 2 {
			start, end := m[group], m[group+1]
			if v.text[start:end] != "*" {
				parts = append(parts, s.part(v.text[start:end], "name", start-m[0], end-m[0], ""))
			}
		}
		if len(parts) > 0 {
			s.composite(v.text[m[0]:m[1]], "cloud", parts, "")
		}
	}
	for _, m := range s.cachedPatternMatches(guidSequencePattern, text, "-") {
		if boundaries(v.text, m[0], m[1]) {
			s.discoverGUIDSequence(v.text[m[0]:m[1]])
		}
	}
	for _, m := range privatePEMPattern.FindAllStringSubmatchIndex(text, -1) {
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
	for _, m := range s.cachedPatternMatches(localPathPattern, text, "/\\") {
		if overlaps(v.composites, m[0], m[1]) {
			continue
		}
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
	for _, m := range s.cachedPatternMatches(cloudPathPattern, text, "/") {
		value := v.text[m[0]:m[1]]
		value = strings.TrimRight(value, ",;)]}")
		s.discoverCloudPath(strings.TrimRight(value, ",;)]}"))
		s.markComposite(v, value, m[0])
	}
	for _, m := range arnPattern.FindAllStringIndex(text, -1) {
		value := strings.TrimRight(v.text[m[0]:m[1]], ",;)]}")
		s.discoverARN(value)
		s.markComposite(v, value, m[0])
	}
	for _, m := range s.cachedPatternMatches(urlPattern, text, ":") {
		value := strings.TrimRight(v.text[m[0]:m[1]], ",;)}")
		s.discoverURL(value)
		s.markComposite(v, value, m[0])
	}
	for _, m := range ipMatches(text) {
		value := strings.TrimRight(v.text[m.start:m.end], ".")
		if !strings.ContainsAny(value, ".:") {
			continue
		}
		addr, err := netip.ParseAddr(value)
		if err != nil {
			if endpoint, parseErr := netip.ParseAddrPort(value); parseErr == nil {
				addr, err = endpoint.Addr(), nil
				value = value[:strings.LastIndexByte(value, ':')]
			}
		}
		if err == nil && boundaries(v.text, m.start, m.start+len(value)) {
			s.discover(value, "network", false)
			s.candidates[value].ip = addr
		}
	}
	for _, m := range s.cachedPatternMatches(emailPattern, text, "@") {
		value := v.text[m[0]:m[1]]
		if parsed, err := mail.ParseAddress(value); err == nil && parsed.Address == value {
			s.discover(value, "email", false)
			s.candidates[value].email = true
			v.emails = append(v.emails, region{m[0], m[1]})
		}
	}
	for _, m := range s.cachedPatternMatches(guidPattern, text, "-") {
		if boundaries(v.text, m[0], m[1]) {
			s.discover(v.text[m[0]:m[1]], "guid", false)
		}
	}
	for _, m := range declaration.FindAllStringSubmatchIndex(text, -1) {
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

// Scan maximal IP-shaped runs, including optional zone identifiers. Runs without
// a dot or colon cannot be addresses; avoid allocating matches for those words
// and numbers. netip still validates the remaining candidates.
func ipMatches(text string) []region {
	if !strings.ContainsAny(text, ".:") {
		return nil
	}
	var matches []region
	for i := 0; i < len(text); {
		start := i
		separator := false
		for i < len(text) {
			c := text[i]
			if c == '.' || c == ':' {
				separator = true
			} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				break
			}
			i++
		}
		if i == start {
			i++
			continue
		}
		if i < len(text) && text[i] == '%' {
			end := i + 1
			for end < len(text) {
				c := text[end]
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
					break
				}
				end++
			}
			if end > i+1 {
				i = end
			}
		}
		if separator {
			matches = append(matches, region{start, i})
		}
	}
	return matches
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
	s.discoverServiceTokens(value)
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
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
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
	if err != nil || (u.Scheme == "" || u.Host == "") && !(strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//")) {
		return
	}
	var parts []compositePart
	var structure []string
	pathStart := 0
	if u.Host != "" {
		host := s.discoverHost(u.Hostname())
		if host == nil {
			return
		}
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
		pathStart = authorityEnd
	}
	// Source spans use the logged bytes, which may contain raw UTF-8 rather
	// than the longer percent-encoded spelling returned by EscapedPath.
	pathEnd := len(value)
	if at := strings.IndexAny(value[pathStart:], "?#"); at >= 0 {
		pathEnd = pathStart + at
	}
	rawPath := value[pathStart:pathEnd]
	pos := pathStart
	service := azureService(u.Hostname())
	if cloud := s.discoverCloudPath(rawPath); cloud != nil {
		parts = append(parts, compositePart{region: region{pathStart, pathEnd}, candidate: cloud})
	} else {
		for index, segment := range strings.Split(rawPath, "/") {
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
				s.discoverServiceTokens(decodedKey)
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
					s.discoverServiceTokens(decoded)
					parts = append(parts, compositePart{region: region{pos + len(key) + 1, pos + len(pair)}, encoding: "query", literal: decoded})
				}
			}
			pos += len(pair) + 1
		}
	}
	if u.Fragment != "" {
		s.discoverServiceTokens(u.Fragment)
		start := strings.IndexByte(value, '#') + 1
		parts = append(parts, compositePart{region: region{start, len(value)}, encoding: "fragment", literal: u.Fragment})
	}
	if len(parts) == 0 {
		return
	}
	c := s.composite(value, "network", parts, "")
	c.format, c.decodedStructure = "url", structure
}

// A resource address needs a dot immediately after its first identifier.
// Locate those identifiers cheaply, then let the grammar handle the label and
// optional instance key, including escapes and Unicode identifiers.
func addressMatch(text string) []int {
	for pos := 0; pos < len(text); {
		dot := strings.IndexByte(text[pos:], '.')
		if dot < 0 {
			return nil
		}
		dot += pos
		start := dot
		for start > 0 {
			r, size := utf8.DecodeLastRuneInString(text[:start])
			if !addressLetter(r) && !(r >= '0' && r <= '9' || r == '-' || r >= utf8.RuneSelf && (unicode.IsNumber(r) || unicode.IsMark(r))) {
				break
			}
			start -= size
		}
		for start < dot {
			r, size := utf8.DecodeRuneInString(text[start:])
			if addressLetter(r) {
				break
			}
			start += size
		}
		if start < dot {
			if match := addressAtStart.FindStringSubmatchIndex(text[start:]); match != nil {
				for i := range match {
					if match[i] >= 0 {
						match[i] += start
					}
				}
				return match
			}
		}
		pos = dot + 1
	}
	return nil
}

func addressLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r >= utf8.RuneSelf && (unicode.IsLetter(r) || unicode.Is(unicode.Nl, r))
}

func (s *session) discoverAddresses(v *view, start, end int, known bool) {
	end = start + len(strings.TrimRight(v.text[start:end], " \t\r\n"))
	if !strings.Contains(v.text[start:end], ".") {
		return
	}
	var complete region
	flush := func() {
		if complete.end > complete.start && !exactRegion(v.addresses, complete.start, complete.end) {
			v.addresses = append(v.addresses, complete)
		}
	}
	for pos := start; pos < end; {
		m := addressMatch(v.text[pos:end])
		if m == nil {
			break
		}
		for i := range m {
			if m[i] >= 0 {
				m[i] += pos
			}
		}
		pos = m[1]
		// Dotted credentials are opaque; enclosing resource addresses still
		// need their labels and instance keys processed normally.
		if overlaps(v.composites, m[0], m[1]) || containsRegion(v.credentials, m[0], m[1]) || containsRegion(v.emails, m[0], m[1]) {
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
