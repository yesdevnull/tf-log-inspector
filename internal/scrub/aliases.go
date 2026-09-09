package scrub

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *session) allocate() error {
	reserved := make(map[string]bool)
	for token := range s.sources {
		reserved[token] = true
	}
	for alias := range s.blocked {
		reserved[alias] = true
	}
	caseSensitive := make(map[string]bool)
	for _, c := range s.ordered {
		reserved[c.value] = true
		if core, ok := guidCore(c.value); ok {
			reserved[strings.ToLower(core)] = true
			if c.caseSensitive {
				caseSensitive[strings.ToLower(core)] = true
			}
		}
	}
	groups := make(map[string]string)
	for n, c := range s.ordered {
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
			}
			if c.numeric {
				alias = fmt.Sprintf("%d", 900000000+serial)
			} else if c.category == "name" && !c.hostname {
				for _, suffix := range []string{"dev", "test", "stage", "staging", "prod"} {
					if strings.HasSuffix(c.value, "_"+suffix) || strings.HasSuffix(c.value, "-"+suffix) {
						alias += "_" + suffix
						break
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
	// Longest original spans win. Stable ordering supplies a deterministic tie break.
	sort.SliceStable(s.ordered, func(i, j int) bool { return len(s.ordered[i].value) > len(s.ordered[j].value) })
	return nil
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
	if c := s.candidates[v.text]; v.whole && c != nil && !v.mandatory && (c.category == "secret" || !exactRegion(v.addresses, 0, len(v.text))) {
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
	for i, childIndex := 0, 0; i < len(v.text); {
		if _, ok := positions[i]; ok {
			positions[i] = out.Len()
		}
		var winner *candidate
		for _, c := range s.ordered {
			end := i + len(c.value)
			if end > len(v.text) || !strings.HasPrefix(v.text[i:], c.value) || (!boundaries(v.text, i, end) && !exactRegion(v.allowed, i, end)) {
				continue
			}
			if isNumber(c.value) && c.category != "secret" && c.category != "explicit" && !containsRegion(v.numeric, i, end) && !(i == 0 && end == len(v.text)) {
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
