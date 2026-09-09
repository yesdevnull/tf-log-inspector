package scrub

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *session) allocate() error {
	s.compositeOrigins = make(map[string]string)
	reserved := make(map[string]bool)
	for token := range s.sources {
		reserved[token] = true
	}
	for alias := range s.blocked {
		reserved[alias] = true
	}
	caseSensitive := make(map[string]bool)
	// A composite resource key gives its GUID components case-sensitive identity.
	for _, c := range s.ordered {
		if c.caseSensitive && c.format == "guid-sequence" {
			for _, part := range c.parts {
				part.candidate.caseSensitive = true
			}
		}
	}
	for _, c := range s.ordered {
		reserved[c.value] = true
		c.syntaxConflict = false
		c.identity = ""
		if c.ip.IsValid() {
			reserved[c.ip.String()] = true
		}
		if core, ok := guidCore(c.value); ok {
			reserved[strings.ToLower(core)] = true
			if c.caseSensitive {
				caseSensitive[strings.ToLower(core)] = true
			}
		}
	}
	groups := make(map[string]string)
	ipv4, ipv6 := netip.MustParseAddr("10.0.0.0"), netip.MustParseAddr("fd00::")
	for n, c := range s.ordered {
		if len(c.parts) > 0 && c.category != "secret" && c.category != "explicit" {
			c.alias = ""
			continue
		}
		if c.ip.IsValid() && c.category != "secret" && c.category != "explicit" {
			prefix, cursor := netip.MustParsePrefix("10.0.0.0/8"), &ipv4
			if c.ip.Is6() {
				prefix, cursor = netip.MustParsePrefix("fd00::/8"), &ipv6
			}
			alias, err := freshIP(prefix, cursor, reserved)
			if err != nil {
				return err
			}
			c.alias, c.category = alias, "network"
			continue
		}
		if c.value == "true" || c.value == "false" {
			alias := "true"
			if c.value == "true" {
				alias = "false"
			}
			if reserved[alias] {
				return fmt.Errorf("%s at allocation: scalar syntax conflict", c.category)
			}
			c.alias = alias
			reserved[alias] = true
			continue
		}
		if core, ok := guidCore(c.value); c.category != "secret" && c.category != "explicit" && ok {
			key := strings.ToLower(core)
			if caseSensitive[key] {
				key = core
			}
			alias := groups[key]
			if alias == "" {
				var err error
				alias, err = freshGUID(rand.Reader, reserved)
				if err != nil {
					return err
				}
				groups[key] = alias
			}
			c.alias = guidCase(alias, core)
			if core != c.value {
				c.alias = "{" + c.alias + "}"
			}
			c.category = "guid"
			continue
		}
		for serial := n + 1; ; serial++ {
			alias := fmt.Sprintf("%s_%04d", c.category, serial)
			if c.hostname {
				alias = fmt.Sprintf("name%04d", serial)
				if c.category == "secret" || c.category == "explicit" {
					alias = fmt.Sprintf("%s%04d", c.category, serial)
				} else if !c.caseSensitive {
					alias += c.suffix
				}
			}
			if c.email && c.category != "secret" && c.category != "explicit" {
				alias = fmt.Sprintf("email%04d@example.invalid", serial)
				c.category = "email"
			}
			if c.numeric {
				alias = fmt.Sprintf("%d", 900000000+serial)
				if c.digits == 12 {
					alias = fmt.Sprintf("%d", 900000000000+serial)
				}
			} else if c.category == "name" && !c.hostname {
				for _, suffix := range []string{"dev", "test", "stage", "staging", "prod"} {
					if strings.HasSuffix(c.value, "_"+suffix) || strings.HasSuffix(c.value, "-"+suffix) {
						alias += "_" + suffix
						break
					}
				}
			}
			if c.pem {
				for i := range len(c.value) {
					if c.value[i] == '\r' || c.value[i] == '\n' {
						alias += c.value[i : i+1]
					}
				}
			}
			if !reserved[alias] {
				c.alias = alias
				reserved[alias] = true
				break
			}
		}
	}
	s.index = candidateIndex{}
	for _, c := range s.ordered {
		s.index.add(c)
	}
	for _, c := range s.ordered {
		if err := s.buildComposite(c, reserved, make(map[*candidate]bool)); err != nil {
			return err
		}
	}
	// Longest original spans win. Stable ordering supplies a deterministic tie break.
	sort.SliceStable(s.ordered, func(i, j int) bool { return len(s.ordered[i].value) > len(s.ordered[j].value) })
	return nil
}

func (s *session) buildComposite(c *candidate, reserved map[string]bool, visiting map[*candidate]bool) error {
	if c.alias != "" {
		return nil
	}
	if visiting[c] {
		return fmt.Errorf("identifier at allocation: composite syntax conflict")
	}
	visiting[c] = true
	defer delete(visiting, c)
	c.syntaxConflict = s.compositeStructureConflict(c)
	var out strings.Builder
	var identity strings.Builder
	pos := 0
	for _, part := range c.parts {
		alias := ""
		original := part.literal
		if part.candidate != nil {
			if err := s.buildComposite(part.candidate, reserved, visiting); err != nil {
				return err
			}
			alias = part.candidate.alias
			original = part.candidate.value
			if part.candidate.identity != "" {
				original = part.candidate.identity
			}
			c.syntaxConflict = c.syntaxConflict || part.candidate.syntaxConflict
		} else {
			var err error
			alias, err = s.compositeText(part.literal, reserved, visiting, c)
			if err != nil {
				return err
			}
		}
		out.WriteString(c.value[pos:part.start])
		identity.WriteString(c.value[pos:part.start])
		out.WriteString(escapeCompositePart(alias, part.encoding))
		identity.WriteString(escapeCompositePart(original, part.encoding))
		pos = part.end
	}
	out.WriteString(c.value[pos:])
	identity.WriteString(c.value[pos:])
	out.WriteString(c.suffix)
	if c.format == "url" {
		u, err := url.Parse(out.String())
		if err != nil {
			return fmt.Errorf("network at allocation: URL syntax conflict")
		}
		if strings.HasPrefix(u.Host, "[") {
			if _, err := netip.ParseAddr(u.Hostname()); err != nil {
				return fmt.Errorf("network at allocation: URL address conflict")
			}
		}
	}
	if reserved[out.String()] && s.compositeOrigins[out.String()] != identity.String() {
		return fmt.Errorf("identifier at allocation: composite collision")
	}
	c.alias = out.String()
	c.identity = identity.String()
	s.compositeOrigins[c.alias] = c.identity
	reserved[c.alias] = true
	return nil
}

func escapeCompositePart(value, encoding string) string {
	switch encoding {
	case "path":
		return url.PathEscape(value)
	case "query":
		return url.QueryEscape(value)
	case "user":
		return url.User(value).String()
	case "fragment":
		fragment := url.URL{Fragment: value}
		return fragment.EscapedFragment()
	}
	return value
}

// Unclassified query values and fragments propagate identifiers discovered elsewhere.
// Work on their decoded original text, before escaping the reconstructed URL.
func (s *session) compositeText(value string, reserved map[string]bool, visiting map[*candidate]bool, owner *candidate) (string, error) {
	var out strings.Builder
	var matches []*candidate
	for i := 0; i < len(value); {
		matches = s.index.matches(value[i:], matches[:0])
		var winner *candidate
		for n := len(matches) - 1; n >= 0; n-- {
			c := matches[n]
			if !boundaries(value, i, i+len(c.value)) || isNumber(c.value) && c.category != "secret" && c.category != "explicit" {
				continue
			}
			if winner == nil || priority(c.category) > priority(winner.category) && c.category == "secret" {
				winner = c
			}
		}
		if winner == nil {
			out.WriteByte(value[i])
			i++
			continue
		}
		if err := s.buildComposite(winner, reserved, visiting); err != nil {
			return "", err
		}
		owner.syntaxConflict = owner.syntaxConflict || winner.syntaxConflict
		out.WriteString(winner.alias)
		i += len(winner.value)
	}
	return out.String(), nil
}

// Copied separators and type labels cannot retain a whole secret. Record the
// conflict until rendering, because an enclosing secret may suppress this value.
func (s *session) compositeStructureConflict(c *candidate) bool {
	var copied []region
	pos := 0
	for _, part := range c.parts {
		if pos < part.start {
			copied = append(copied, region{pos, part.start})
		}
		pos = part.end
	}
	if pos < len(c.value) {
		copied = append(copied, region{pos, len(c.value)})
	}
	if s.protectedIdentifier(c.value, copied) {
		return true
	}
	for _, decoded := range c.decodedStructure {
		if s.protectedIdentifier(decoded, []region{{0, len(decoded)}}) {
			return true
		}
	}
	return false
}

func (s *session) protectedIdentifier(value string, protected []region) bool {
	var matches []*candidate
	for i := range len(value) {
		matches = s.index.matches(value[i:], matches[:0])
		for _, match := range matches {
			end := i + len(match.value)
			if (match.category == "secret" || match.category == "explicit") && boundaries(value, i, end) && overlaps(protected, i, end) {
				return true
			}
		}
	}
	return false
}

func freshIP(prefix netip.Prefix, cursor *netip.Addr, reserved map[string]bool) (string, error) {
	for prefix.Contains(*cursor) {
		alias := cursor.String()
		*cursor = cursor.Next()
		if !reserved[alias] {
			reserved[alias] = true
			return alias, nil
		}
	}
	return "", fmt.Errorf("network at allocation: address space exhausted")
}

// candidateIndex only visits prefixes present at the current input position.
// Terminals retain pointers because resource collision retries update aliases.
type candidateIndex struct {
	next     map[byte]*candidateIndex
	terminal *candidate
}

func (index *candidateIndex) add(c *candidate) {
	for i := range len(c.value) {
		if index.next == nil {
			index.next = make(map[byte]*candidateIndex)
		}
		if index.next[c.value[i]] == nil {
			index.next[c.value[i]] = &candidateIndex{}
		}
		index = index.next[c.value[i]]
	}
	index.terminal = c
}

func (index *candidateIndex) matches(text string, matches []*candidate) []*candidate {
	for i := range len(text) {
		index = index.next[text[i]]
		if index == nil {
			break
		}
		if index.terminal != nil {
			matches = append(matches, index.terminal)
		}
	}
	return matches
}

func guidCore(value string) (string, bool) {
	core := value
	if strings.HasPrefix(core, "{") && strings.HasSuffix(core, "}") {
		core = core[1 : len(core)-1]
	}
	return core, len(core) == 36 && guidPattern.MatchString(core)
}

func freshGUID(reader io.Reader, reserved map[string]bool) (string, error) {
	for {
		var b [16]byte
		if _, err := io.ReadFull(reader, b[:]); err != nil {
			return "", fmt.Errorf("guid at allocation: randomness unavailable")
		}
		b[6] = b[6]&0x0f | 0x40
		b[8] = b[8]&0x3f | 0x80
		alias := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
		if !reserved[alias] {
			reserved[alias] = true
			return alias, nil
		}
	}
}

func guidCase(alias, original string) string {
	if original == strings.ToUpper(original) {
		return strings.ToUpper(alias)
	}
	return alias
}

func (s *session) render(v *view) (string, error) {
	if c := s.candidates[v.text]; v.whole && c != nil && !v.mandatory && v.numericContext(c, 0, len(v.text)) && (c.category == "secret" || !exactRegion(v.addresses, 0, len(v.text))) {
		if c.syntaxConflict {
			return "", fmt.Errorf("identifier at line %d: preserved syntax conflict", v.line)
		}
		s.record(c)
		if s.collectResources && v.resourceKey {
			s.emitted = append(s.emitted, resourceEmission{source: v.text, rendered: c.alias, key: true, line: v.line, used: map[*candidate]bool{c: true}})
		}
		return c.alias, nil
	}
	if c := s.candidates[v.text]; v.whole && c != nil && v.mandatory && (c.category == "secret" || c.category == "explicit") {
		return "", fmt.Errorf("%s at line %d: preserved syntax conflict", c.category, v.line)
	}
	var out strings.Builder
	positions := make(map[int]int)
	var edits []resourceEdit
	if s.collectResources {
		for _, address := range v.addresses {
			positions[address.start] = -1
			positions[address.end] = -1
		}
	}
	var matches []*candidate
	for i, childIndex := 0, 0; i < len(v.text); {
		if _, ok := positions[i]; ok {
			positions[i] = out.Len()
		}
		var winner *candidate
		matches = s.index.matches(v.text[i:], matches[:0])
		for match := len(matches) - 1; match >= 0; match-- {
			c := matches[match]
			end := i + len(c.value)
			if end > len(v.text) || !strings.HasPrefix(v.text[i:], c.value) || (!boundaries(v.text, i, end) && !exactRegion(v.allowed, i, end)) {
				continue
			}
			if !v.numericContext(c, i, end) {
				continue
			}
			if overlaps(v.nulls, i, end) {
				continue
			}
			if overlaps(v.protected, i, end) && !exactRegion(v.wholeValues, i, end) {
				if c.category == "secret" || c.category == "explicit" {
					return "", fmt.Errorf("%s at line %d: preserved syntax conflict", c.category, v.line)
				}
				continue
			}
			if protectedKey(v.keys, i, end) {
				continue
			}
			if winner == nil || priority(c.category) > priority(winner.category) && c.category == "secret" {
				winner = c
			}
		}
		if winner != nil {
			end := i + len(winner.value)
			// Quoted spans are encoded through their decoded view.
			if childIndex >= len(v.children) || end <= v.children[childIndex].start || coversChildren(v.children, childIndex, i, end) {
				if winner.syntaxConflict {
					return "", fmt.Errorf("identifier at line %d: preserved syntax conflict", v.line)
				}
				out.WriteString(winner.alias)
				s.record(winner)
				if s.collectResources {
					edits = append(edits, resourceEdit{region: region{i, end}, used: map[*candidate]bool{winner: true}, secret: winner.category == "secret"})
				}
				for childIndex < len(v.children) && v.children[childIndex].end <= end {
					childIndex++
				}
				i = end
				continue
			}
		}
		if childIndex < len(v.children) && i == v.children[childIndex].start {
			child := v.children[childIndex]
			if containsRegion(v.numeric, child.start, child.end) && !exactRegion(child.view.numeric, 0, len(child.view.text)) {
				child.view.numeric = append(child.view.numeric, region{0, len(child.view.text)})
			}
			if containsRegion(v.protected, child.start, child.end) {
				child.view.protect(0, len(child.view.text))
				child.view.mandatory = true
			}
			previousUsed := s.used
			if s.collectResources {
				s.used = make(map[*candidate]bool)
			}
			rendered, err := s.render(child.view)
			if s.collectResources {
				childUsed := s.used
				s.used = previousUsed
				for c := range childUsed {
					s.used[c] = true
				}
				edits = append(edits, resourceEdit{region: child.region, used: childUsed})
			}
			if err != nil {
				return "", err
			}
			if rendered == child.view.text {
				out.WriteString(v.text[child.start:child.end])
			} else {
				encoded, _ := json.Marshal(rendered)
				out.Write(encoded)
			}
			i = child.end
			childIndex++
			continue
		}
		out.WriteByte(v.text[i])
		i++
	}
	if s.collectResources {
		if _, ok := positions[len(v.text)]; ok {
			positions[len(v.text)] = out.Len()
		}
		s.emitResources(v, out.String(), positions, edits)
	}
	return out.String(), nil
}

func (v *view) numericContext(c *candidate, start, end int) bool {
	return !isNumber(c.value) || c.category == "secret" || c.category == "explicit" || containsRegion(v.numeric, start, end)
}

func (s *session) record(c *candidate) {
	s.counts[c.category]++
	if s.used != nil {
		s.used[c] = true
	}
}

func protectedKey(keys []region, start, end int) bool {
	for _, key := range keys {
		if start < key.end && end > key.start && !(start <= key.start && end > key.end) {
			return true
		}
	}
	return false
}

func coversChildren(children []quoted, index, start, end int) bool {
	for ; index < len(children) && children[index].start < end; index++ {
		if children[index].start < start || children[index].end > end {
			return false
		}
	}
	return true
}

func exactRegion(regions []region, start, end int) bool {
	for _, r := range regions {
		if start == r.start && end == r.end {
			return true
		}
	}
	return false
}

func overlaps(regions []region, start, end int) bool {
	for _, r := range regions {
		if start < r.end && end > r.start {
			return true
		}
	}
	return false
}
func containsRegion(regions []region, start, end int) bool {
	for _, r := range regions {
		if start >= r.start && end <= r.end {
			return true
		}
	}
	return false
}
func word(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' }
func boundaries(text string, start, end int) bool {
	first, _ := utf8.DecodeRuneInString(text[start:end])
	last, _ := utf8.DecodeLastRuneInString(text[start:end])
	if start > 0 && word(first) {
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		if word(before) {
			return false
		}
	}
	if end < len(text) && word(last) {
		after, _ := utf8.DecodeRuneInString(text[end:])
		if word(after) {
			return false
		}
	}
	return true
}
