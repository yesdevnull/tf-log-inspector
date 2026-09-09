// Package scrub creates consistently pseudonymised Terraform log candidates.
package scrub

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Result contains transformed input and aggregate counts without source values.
type Result struct {
	Data         []byte
	Replacements map[string]int
	Unsupported  int
}

// Scrub replaces identifying values using one mapping for the entire input.
func Scrub(data []byte, extra []string) (Result, error) {
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return Result{}, fmt.Errorf("input at byte 0: unsupported encoding")
	}
	s := &session{candidates: make(map[string]*candidate), counts: make(map[string]int)}
	for i, value := range extra {
		if value == "" || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return Result{}, fmt.Errorf("explicit at value %d: invalid input", i+1)
		}
		s.discover(value, "explicit", false)
	}
	views := s.parseLines(string(data))
	s.sources = make(map[string]bool)
	var reserve func(*view)
	reserve = func(v *view) {
		for _, token := range sourceToken.FindAllString(v.text, -1) {
			s.sources[token] = true
		}
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
	if err := s.ensureDistinctResources(views); err != nil {
		return Result{}, err
	}
	var out strings.Builder
	for _, v := range views {
		text, err := s.render(v)
		if err != nil {
			return Result{}, err
		}
		out.WriteString(text)
	}
	if err := s.validate(string(data), out.String()); err != nil {
		return Result{}, err
	}
	return Result{Data: []byte(out.String()), Replacements: s.counts, Unsupported: s.unsupported}, nil
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
	candidates       map[string]*candidate
	compositeOrigins map[string]string
	ordered          []*candidate
	index            candidateIndex
	counts           map[string]int
	unsupported      int
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
