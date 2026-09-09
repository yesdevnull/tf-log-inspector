package scrub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// Source cuts move past atomic replacements and quoted children during rendering.
type sourceCut struct{ source, output int }

type providerFragment struct {
	region
	message, piece int
}

func (s *session) parseProviderFragments(input string) (physical, logical []*view, fragments []providerFragment, masked string, err error) {
	messages, err := logfmt.ReconstructProviderJSON(input)
	if err != nil {
		return nil, nil, nil, "", err
	}
	mask := []byte(input)
	validation := []byte(input)
	for index, message := range messages {
		v := &view{text: message.Text, line: message.Fragments[0].Line, responseJSON: true}
		v.cuts = append(v.cuts, sourceCut{})
		pos := 0
		for piece, fragment := range message.Fragments {
			fragments = append(fragments, providerFragment{region{fragment.Start, fragment.End}, index, piece})
			copy(validation[fragment.Start:fragment.End], providerMetadataMask(input[fragment.Start:fragment.End]))
			for at := fragment.Start; at < fragment.End; at++ {
				if mask[at] != '\n' && mask[at] != '\r' {
					mask[at] = ' '
				}
			}
			pos += fragment.End - fragment.Start
			v.cuts = append(v.cuts, sourceCut{source: pos})
		}
		s.parseView(v, false, false)
		logical = append(logical, v)
	}
	masked = string(mask)
	if !utf8.ValidString(masked) {
		return nil, nil, nil, "", fmt.Errorf("input at byte 0: unsupported encoding")
	}
	physical = s.parseLines(masked)
	sort.Slice(fragments, func(i, j int) bool { return fragments[i].start < fragments[j].start })
	// Physical views may span several lines. Keep body cuts in their own source
	// coordinates so metadata replacements cannot shift the insertion locations.
	pos, fragmentIndex := 0, 0
	for _, v := range physical {
		end := pos + len(v.text)
		for fragmentIndex < len(fragments) && fragments[fragmentIndex].end <= end {
			fragment := fragments[fragmentIndex]
			v.cuts = append(v.cuts, sourceCut{source: fragment.start - pos}, sourceCut{source: fragment.end - pos})
			fragmentIndex++
		}
		pos = end
	}
	return physical, logical, fragments, string(validation), nil
}

// Validation excludes body tokens without changing byte positions or the
// leading spaces trimmed by the header parser. This mask must never enter
// discovery: its filler is not a source identifier.
func providerMetadataMask(body string) string {
	mask := []byte(body)
	for i, b := range mask {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			mask[i] = 'x'
		}
	}
	return string(mask)
}

func (s *session) renderProviderFragments(physical, logical []*view, fragments []providerFragment) (string, string, error) {
	rendered := make([]string, len(logical))
	for i, v := range logical {
		text, err := s.render(v)
		if err != nil {
			return "", "", err
		}
		if !json.Valid([]byte(text)) {
			return "", "", fmt.Errorf("provider JSON at line %d: invalid rendered body", v.line)
		}
		rendered[i] = text
	}
	var output, masked strings.Builder
	fragmentIndex := 0
	for _, v := range physical {
		text, err := s.render(v)
		if err != nil {
			return "", "", err
		}
		pos := 0
		for i := 0; i < len(v.cuts); i += 2 {
			output.WriteString(text[pos:v.cuts[i].output])
			masked.WriteString(text[pos:v.cuts[i].output])
			fragment := fragments[fragmentIndex]
			cuts := logical[fragment.message].cuts
			piece := rendered[fragment.message][cuts[fragment.piece].output:cuts[fragment.piece+1].output]
			output.WriteString(piece)
			masked.WriteString(providerMetadataMask(piece))
			pos = v.cuts[i+1].output
			fragmentIndex++
		}
		output.WriteString(text[pos:])
		masked.WriteString(text[pos:])
	}
	return output.String(), masked.String(), nil
}
