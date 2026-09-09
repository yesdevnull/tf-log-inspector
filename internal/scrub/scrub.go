// Package scrub creates consistently pseudonymised Terraform log candidates.
package scrub

import (
	"bytes"
	"fmt"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Result contains transformed input and aggregate counts without source values.
type Result struct {
	Data              []byte
	Replacements      map[string]int
	Unsupported       int
	UnsupportedInputs []UnsupportedInput
}

// UnsupportedInput identifies a parsing limitation in the original input.
// Line and Column are one-based; columns count Unicode characters, not bytes.
type UnsupportedInput struct {
	Line, Column int
	Reason       string
}

// Scrub replaces identifying values using one mapping for the entire input.
func Scrub(data []byte, extra []string) (Result, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return Result{}, fmt.Errorf("input at byte 0: unsupported encoding")
	}
	s := &session{candidates: make(map[string]*candidate), counts: make(map[string]int)}
	for i, value := range extra {
		if value == "" || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return Result{}, fmt.Errorf("explicit at value %d: invalid input", i+1)
		}
		s.discover(value, "explicit", false)
	}
	input := string(data)
	physical, logical, fragments, masked, err := s.parseProviderFragments(input)
	if err != nil {
		return Result{}, err
	}
	views := append(append([]*view(nil), physical...), logical...)
	if s.parseErr != nil {
		return Result{}, s.parseErr
	}
	s.sources = make(map[string]bool)
	s.reserveTokens(input)
	var reserve func(*view)
	reserve = func(v *view) {
		s.reserveTokens(v.text)
		for _, child := range v.children {
			reserve(child.view)
		}
	}
	for _, v := range views {
		reserve(v)
	}
	var addresses func(*view)
	addresses = func(v *view) {
		s.discoverAddresses(v, 0, len(v.text), v.addressContext)
		for _, child := range v.children {
			addresses(child.view)
		}
	}
	for _, v := range views {
		addresses(v)
	}
	if err := s.allocate(); err != nil {
		return Result{}, err
	}
	rendered, err := s.ensureDistinctResources(views)
	if err != nil {
		return Result{}, err
	}
	output, renderedMasked, err := renderProviderFragments(physical, logical, fragments, rendered.text)
	if err != nil {
		return Result{}, err
	}
	if err := s.validate(masked, renderedMasked); err != nil {
		return Result{}, err
	}
	return Result{Data: []byte(output), Replacements: rendered.counts, Unsupported: s.unsupported, UnsupportedInputs: unsupportedLocations(input, physical, logical, fragments)}, nil
}

// Reserve maximal letter/number/underscore/hyphen runs directly, avoiding
// temporary match slices for every occurrence in the input and decoded views.
func (s *session) reserveTokens(text string) {
	start := -1
	for i := 0; i < len(text); {
		c, size := rune(text[i]), 1
		match := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if c >= utf8.RuneSelf {
			c, size = utf8.DecodeRuneInString(text[i:])
			match = unicode.IsLetter(c) || unicode.IsNumber(c)
		}
		if match {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			s.sources[text[start:i]] = true
			start = -1
		}
		i += size
	}
	if start >= 0 {
		s.sources[text[start:]] = true
	}
}

type candidate struct {
	value, category, alias           string
	caseSensitive, numeric, hostname bool
	email                            bool
	ip                               netip.Addr
	parts                            []compositePart
	suffix                           string
	digits                           int
	pem                              bool
	format                           string
	syntaxConflict                   bool
	identity                         string
	decodedStructure                 []string
}

type compositePart struct {
	region
	candidate *candidate
	encoding  string
	literal   string
}

type session struct {
	patterns         map[patternKey][][]int
	candidates       map[string]*candidate
	compositeOrigins map[string]string
	ordered          []*candidate
	index            candidateIndex
	counts           map[string]int
	unsupported      int
	parseErr         error
	sources          map[string]bool
	resourceTypes    map[string]bool
	blocked          map[string]bool
	used             map[*candidate]bool
	collectResources bool
	emitted          []resourceEmission
}

func (s *session) discover(value, category string, numeric bool) {
	if value == "" || category == "" {
		return
	}
	if category == "consent" {
		s.discoverConsent(value, numeric)
		return
	}
	hostname := category == "hostname"
	if hostname {
		category = "network"
	}
	c := s.candidates[value]
	if c == nil {
		c = &candidate{value: value, category: category}
		s.candidates[value] = c
		s.ordered = append(s.ordered, c)
	}
	c.caseSensitive = c.caseSensitive || category == "name"
	c.numeric = c.numeric || numeric
	if len(value) == 12 && strings.Trim(value, "0123456789") == "" && (category == "id" || category == "name" || category == "cloud") {
		c.numeric, c.digits = true, 12
	}
	if priority(category) > priority(c.category) {
		c.category = category
	}
	if hostname {
		s.discoverHost(value)
	}
}

func priority(category string) int {
	switch category {
	case "secret":
		return 100
	case "explicit":
		return 90
	case "guid":
		return 60
	case "name":
		return 50
	default:
		return 40
	}
}
