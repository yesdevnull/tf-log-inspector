package scrub

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const unsupportedLimit = 10

type unsupportedInput struct {
	offset int
	reason string
}

func (s *session) unsupportedAt(v *view, offset int, reason string) {
	s.unsupported++
	if len(v.unsupported) < unsupportedLimit {
		v.unsupported = append(v.unsupported, unsupportedInput{offset, reason})
	}
}

// Keep only the earliest samples at each level. Source mapping is performed
// for these samples, never by allocating an offset for every decoded byte.
func firstUnsupported(samples []unsupportedInput) []unsupportedInput {
	if len(samples) < 2 {
		return samples
	}
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].offset < samples[j].offset })
	return samples[:min(len(samples), unsupportedLimit)]
}

func viewUnsupported(v *view) []unsupportedInput {
	samples := append([]unsupportedInput(nil), v.unsupported...)
	for _, child := range v.children {
		for _, sample := range viewUnsupported(child.view) {
			if child.verbatim {
				sample.offset += child.start
			} else {
				sample.offset = child.start + quotedSourceOffset(v.text[child.start:child.end], sample.offset)
			}
			samples = append(samples, sample)
		}
		samples = firstUnsupported(samples)
	}
	return samples
}

// A decoded position inside an escape points at its opening backslash.
// readString accepts JSON and Go string escapes, so mapping accepts both too.
func quotedSourceOffset(raw string, offset int) int {
	for i, decoded := 1, 0; i < len(raw)-1; {
		end := i + 1
		_, width := utf8.DecodeRuneInString(raw[i:])
		if raw[i] != '\\' {
			end = i + width
		} else {
			end = i + 2
			switch raw[i+1] {
			case 'u':
				end = i + 6
				code, _ := strconv.ParseUint(raw[i+2:end], 16, 16)
				if code >= 0xd800 && code <= 0xdbff && end+6 <= len(raw)-1 && raw[end:end+2] == `\u` {
					low, _ := strconv.ParseUint(raw[end+2:end+6], 16, 16)
					if low >= 0xdc00 && low <= 0xdfff {
						end += 6
					}
				}
			case 'U':
				end = i + 10
			case 'x', '0', '1', '2', '3', '4', '5', '6', '7':
				end = i + 4
			}
			var value string
			encoded := `"` + raw[i:end] + `"`
			if json.Unmarshal([]byte(encoded), &value) != nil {
				value, _ = strconv.Unquote(encoded)
			}
			width = len(value)
		}
		if offset < decoded+width {
			return i
		}
		decoded += width
		i = end
	}
	return len(raw) - 1
}

func unsupportedLocations(input string, physical, logical []*view, fragments []providerFragment) []UnsupportedInput {
	var samples []unsupportedInput
	pos := 0
	for _, v := range physical {
		for _, sample := range viewUnsupported(v) {
			sample.offset += pos
			samples = append(samples, sample)
		}
		pos += len(v.text)
		samples = firstUnsupported(samples)
	}
	origins := make([][]providerFragment, len(logical))
	for _, fragment := range fragments {
		origins[fragment.message] = append(origins[fragment.message], fragment)
	}
	for i, v := range logical {
		for _, sample := range viewUnsupported(v) {
			piece := sort.Search(len(v.cuts)-1, func(j int) bool { return v.cuts[j+1].source > sample.offset })
			sample.offset = origins[i][piece].start + sample.offset - v.cuts[piece].source
			samples = append(samples, sample)
		}
		samples = firstUnsupported(samples)
	}
	var locations []UnsupportedInput
	line, start, scanned := 1, 0, 0
	for _, sample := range samples {
		for {
			newline := strings.IndexByte(input[scanned:sample.offset], '\n')
			if newline < 0 {
				break
			}
			start = scanned + newline + 1
			scanned = start
			line++
		}
		scanned = sample.offset
		locations = append(locations, UnsupportedInput{line, utf8.RuneCountInString(input[start:sample.offset]) + 1, sample.reason})
	}
	return locations
}
