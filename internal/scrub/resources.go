package scrub

import (
	"encoding/json"
	"fmt"
	"strings"
)

type resourceEmission struct {
	source, rendered string
	key              bool
	line             int
	used             map[*candidate]bool
}

type resourceEdit struct {
	region
	used   map[*candidate]bool
	secret bool
}

type renderedLog struct {
	text   map[*view]string
	counts map[string]int
}

// ensureDistinctResources validates identities recorded by full rendering, so a
// credential that replaces an enclosing value suppresses its incidental addresses.
func (s *session) ensureDistinctResources(views []*view) (renderedLog, error) {
	type identityKey struct {
		value string
		key   bool
	}
	type collisionKey struct {
		source, rendered string
		key              bool
	}
	retried := make(map[collisionKey]bool)
	for {
		rendered, emitted, err := s.renderResources(views)
		if err != nil {
			return renderedLog{}, err
		}
		sources := make(map[identityKey]bool)
		for _, identity := range emitted {
			sources[identityKey{identity.source, identity.key}] = true
		}
		seen := make(map[identityKey]string)
		collision := false
		for _, identity := range emitted {
			key := identityKey{identity.rendered, identity.key}
			previous, exists := seen[key]
			if sources[key] || exists && previous != identity.source {
				failed := collisionKey{identity.source, identity.rendered, identity.key}
				if len(identity.used) == 0 || retried[failed] {
					return renderedLog{}, fmt.Errorf("name at line %d: resource syntax collision", identity.line)
				}
				retried[failed] = true
				if s.blocked == nil {
					s.blocked = make(map[string]bool)
				}
				for c := range identity.used {
					s.blocked[c.alias] = true
					if core, ok := guidCore(c.alias); ok {
						s.blocked[strings.ToLower(core)] = true
					}
				}
				if err := s.allocate(); err != nil {
					return renderedLog{}, err
				}
				collision = true
				break
			}
			seen[key] = identity.source
		}
		if !collision {
			return rendered, nil
		}
	}
}

func (s *session) renderResources(views []*view) (renderedLog, []resourceEmission, error) {
	counts, used, collect, emitted := s.counts, s.used, s.collectResources, s.emitted
	s.counts = make(map[string]int)
	s.used = make(map[*candidate]bool)
	s.collectResources = true
	s.emitted = nil
	defer func() { s.counts, s.used, s.collectResources, s.emitted = counts, used, collect, emitted }()
	rendered := renderedLog{text: make(map[*view]string, len(views)), counts: s.counts}
	for _, v := range views {
		text, err := s.render(v)
		if err != nil {
			return renderedLog{}, nil, err
		}
		rendered.text[v] = text
	}
	return rendered, s.emitted, nil
}

func (s *session) emitResources(v *view, output string, positions map[int]int, edits []resourceEdit) {
	for _, address := range v.addresses {
		start, end := positions[address.start], positions[address.end]
		if start < 0 || end < 0 {
			continue
		}
		used := make(map[*candidate]bool)
		suppressed := false
		for _, edit := range edits {
			if edit.start >= address.end || edit.end <= address.start {
				continue
			}
			if edit.secret && edit.start <= address.start && edit.end >= address.end {
				suppressed = true
				break
			}
			for c := range edit.used {
				used[c] = true
			}
		}
		if suppressed {
			continue
		}
		source, rendered := v.text[address.start:address.end], output[start:end]
		s.emitted = append(s.emitted, resourceEmission{source: canonicalAddress(source), rendered: canonicalAddress(rendered), line: v.line, used: used})
		sourceKeys, renderedKeys := addressKeys(source), addressKeys(rendered)
		for i, key := range sourceKeys {
			if i < len(renderedKeys) {
				s.emitted = append(s.emitted, resourceEmission{source: key, rendered: renderedKeys[i], key: true, line: v.line, used: used})
			}
		}
	}
}

func addressKeys(text string) []string {
	var keys []string
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			end, value, ok := readString(text, i)
			if ok {
				if value != "" {
					keys = append(keys, value)
				}
				i = end - 1
			}
		}
	}
	return keys
}

func canonicalAddress(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '"' {
			end, value, ok := readString(text, i)
			if ok {
				encoded, _ := json.Marshal(value)
				out.Write(encoded)
				i = end
				continue
			}
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}
