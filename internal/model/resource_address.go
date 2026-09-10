package model

import (
	"encoding/json"
	"strings"
	"unicode"
)

// ResourceModule identifies a structurally recognised Terraform module path.
// The empty path is the known root module; Known false means the available
// evidence could not establish membership safely.
type ResourceModule struct {
	Path  string
	Known bool
}

// ResolveResourceModule prefers valid observed metadata and otherwise derives
// module identity from a complete, structurally recognised resource address.
func ResolveResourceModule(address, observed string, observedKnown bool) ResourceModule {
	addressModule := resourceAddressModule(address)
	if !observedKnown {
		return addressModule
	}
	if _, ok := moduleSegments(observed); !ok {
		return ResourceModule{}
	}
	if addressModule.Known && addressModule.Path != observed {
		return ResourceModule{}
	}
	return ResourceModule{Path: observed, Known: true}
}

// ModuleContains reports whether child is parent or one of its descendants.
// Both inputs must be complete, structurally recognised module paths.
func ModuleContains(parent, child string) bool {
	parentSegments, parentOK := moduleSegments(parent)
	childSegments, childOK := moduleSegments(child)
	if !parentOK || !childOK || len(parentSegments) > len(childSegments) {
		return false
	}
	for i := range parentSegments {
		if parentSegments[i] != childSegments[i] {
			return false
		}
	}
	return true
}

func resourceAddressModule(address string) ResourceModule {
	segments, ok := splitAddress(address)
	if !ok {
		return ResourceModule{}
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
		return ResourceModule{}
	}
	return ResourceModule{Path: strings.Join(segments[:moduleEnd], "."), Known: true}
}

func moduleSegments(path string) ([]string, bool) {
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
