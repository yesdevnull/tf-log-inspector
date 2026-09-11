// Package resourceaddr recognises Terraform resource addresses without changing their identity.
package resourceaddr

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Address contains the module and resource type of a complete resource address.
type Address struct{ Module, Type string }

// Parse validates a complete address and extracts its module and resource type.
func Parse(address string) (Address, bool) {
	segments, ok := splitAddress(address)
	if !ok {
		return Address{}, false
	}
	moduleEnd := 0
	for moduleEnd+1 < len(segments) && segments[moduleEnd] == "module" && validNameSegment(segments[moduleEnd+1]) {
		moduleEnd += 2
	}
	remainder := segments[moduleEnd:]
	validResource := len(remainder) == 2 && remainder[0] != "data" && remainder[0] != "ephemeral" && validPlainSegment(remainder[0]) && validNameSegment(remainder[1])
	if len(remainder) == 3 && (remainder[0] == "data" || remainder[0] == "ephemeral") {
		validResource = validPlainSegment(remainder[1]) && validNameSegment(remainder[2])
	}
	if !validResource {
		return Address{}, false
	}
	resourceType := remainder[0]
	if len(remainder) == 3 {
		resourceType = remainder[1]
	}
	return Address{Module: strings.Join(segments[:moduleEnd], "."), Type: resourceType}, true
}

// ModuleSegments validates a complete module path.
func ModuleSegments(path string) ([]string, bool) {
	if path == "" {
		return nil, true
	}
	segments, ok := splitAddress(path)
	if !ok || len(segments)%2 != 0 {
		return nil, false
	}
	for i := 0; i < len(segments); i += 2 {
		if segments[i] != "module" || !validNameSegment(segments[i+1]) {
			return nil, false
		}
	}
	return segments, true
}

func splitAddress(address string) ([]string, bool) {
	if address == "" {
		return nil, false
	}
	var segments []string
	start, bracket := 0, false
	quoted, escaped := false, false
	for i, r := range address {
		if unicode.IsControl(r) {
			return nil, false
		}
		if escaped {
			escaped = false
			continue
		}
		if quoted && r == '\\' {
			escaped = true
			continue
		}
		if bracket && r == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch r {
		case '[':
			if bracket {
				return nil, false
			}
			bracket = true
		case ']':
			if !bracket {
				return nil, false
			}
			bracket = false
		case '.':
			if !bracket {
				if i == start {
					return nil, false
				}
				segments = append(segments, address[start:i])
				start = i + 1
			}
		}
	}
	if bracket || quoted || escaped || start == len(address) {
		return nil, false
	}
	segments = append(segments, address[start:])
	return segments, true
}

func validPlainSegment(segment string) bool {
	for i, r := range segment {
		if i == 0 {
			if !identifierStart(r) {
				return false
			}
			continue
		}
		if !identifierContinue(r) {
			return false
		}
	}
	return segment != ""
}

func identifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r)
}

func identifierContinue(r rune) bool {
	return r == '-' || identifierStart(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_ID_Continue, r)
}

func validNameSegment(segment string) bool {
	open := strings.IndexByte(segment, '[')
	if open < 0 {
		return validPlainSegment(segment)
	}
	if !validPlainSegment(segment[:open]) || segment[len(segment)-1] != ']' {
		return false
	}
	key := segment[open+1 : len(segment)-1]
	if key == "" {
		return false
	}
	if key[0] == '"' {
		var decoded string
		return json.Unmarshal([]byte(key), &decoded) == nil
	}
	for _, r := range key {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
