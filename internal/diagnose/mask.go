// Package diagnose summarises a log's structure without disclosing its
// contents.
package diagnose

import (
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// MaskProse replaces the parts of a message that carry content -- quoted
// strings, filesystem paths, resource addresses and long identifiers -- with
// placeholders, leaving the message's shape legible.
//
// This is heuristic by design. Field keys are guarded by a strict charset and
// are safe by construction; prose cannot be, because the point of reporting it
// is to reveal message shapes nobody anticipated. The report therefore carries
// a review notice, and Dan reviews it before sharing.
func MaskProse(s string) string {
	s = maskQuotedSpans(s)
	fields := strings.Fields(s)
	for i, tok := range fields {
		fields[i] = maskToken(tok)
	}
	return strings.Join(fields, " ")
}

// MaskComponent masks a component name that looks like a resource address, is
// too long to plausibly be a genuine component name, or contains a character
// no real Terraform component uses. splitComponent (internal/logfmt/header.go)
// accepts any whitespace-free byte sequence before the first colon -- unlike a
// field key, a component name carries no charset guarantee of its own -- so
// this is the component equivalent of logfmt.ValidKey's length cap, applied
// after the fact rather than during parsing so a withheld shape can still be
// counted. "/" is deliberately allowed through: "backend/local" and
// "dag/walk" are genuine Terraform component names.
func MaskComponent(s string) string {
	if s == "" {
		return s
	}
	if len(s) > logfmt.MaxKeyLen || strings.ContainsAny(s, `"[]~`) {
		return "<other>"
	}
	if strings.HasPrefix(s, "provider.") {
		// Plugin binary names carry no customer content.
		return s
	}
	if looksLikeAddress(s) {
		return "<addr>"
	}
	return s
}

// maskQuotedSpans replaces every double-quoted span -- quotes included --
// with a single "<q>" placeholder before the message is split into words. A
// per-token quote check alone only replaces the words touching the quote
// marks, leaving the words between them -- the actual content -- untouched,
// which defeats the rule's intent of keeping quoted strings out of the
// report. An unterminated quote masks to the end of the string, the
// conservative direction, matching readQuoted in internal/logfmt/fields.go,
// which already consumes the remainder on an unterminated quote -- and, like
// readQuoted, a backslash inside a span escapes the following byte, so an
// escaped quote never terminates the span. Without that, each escaped quote
// is treated as an independent delimiter and the text between two of them --
// exactly what a nested-JSON provider response body is full of -- is wrongly
// classified as outside any span and written straight through.
//
// Deliberately scoped to double quotes: a single quote is an English
// contraction far more often than the start of a quoted phrase, and treating
// it as a span opener would erase the rest of the line from the first
// apostrophe onward. maskToken's existing per-token check still catches a
// lone single quote.
func maskQuotedSpans(s string) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(s, '"')
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		b.WriteString("<q>")
		closed := false
		i := start + 1
		for ; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				closed = true
				break
			}
		}
		if !closed {
			return b.String()
		}
		s = s[i+1:]
	}
}

func maskToken(t string) string {
	switch {
	case t == "":
		return t
	case strings.ContainsAny(t, `"'`):
		return "<q>"
	case strings.ContainsAny(t, "/\\") || strings.HasPrefix(t, "~"):
		return "<path>"
	case strings.ContainsAny(t, "[]"):
		return "<addr>"
	case looksLikeAddress(t):
		return "<addr>"
	case len(t) > 12 && hasDigit(t):
		return "<id>"
	}
	return t
}

// looksLikeAddress reports whether t has the shape of a Terraform resource
// address: a dotted identifier whose segment after the dot begins lower-case.
// Core's own Go identifiers -- terraform.NewContext, statemgr.Filesystem --
// are CamelCase after the dot and are kept, because they are what make a
// masked template legible.
func looksLikeAddress(t string) bool {
	dot := strings.IndexByte(t, '.')
	if dot <= 0 || dot == len(t)-1 {
		return false
	}
	head, tail := t[:dot], t[dot+1:]
	if !isLowerIdent(head) {
		return false
	}
	c := tail[0]
	return c >= 'a' && c <= 'z'
}

func isLowerIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '_' {
			return false
		}
	}
	return s != ""
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

// MaskAddress masks a Terraform resource address's identifying segments
// while preserving its structure. Terraform's address grammar (simplified,
// per the "hook.resource.addr" field span.UIHookBuilder reads) is:
//
//	("module" "." NAME ["[" INDEX "]"] ".")*  ["data" "."]  TYPE "." NAME ["[" INDEX "]"]
//
// The structural keywords "module" and "data" are kept verbatim, as is the
// resource type -- a public provider schema name, never customer data, and
// already shown unmasked in its own column wherever this is used. Every
// module name masks to "<m>", the final instance name masks to "<name>",
// and any for_each/count index inside "[...]" masks to "<k>" while keeping
// its brackets (and quotes, for a string key) so indexing stays visible.
// This is deliberately more conservative than masking only the leaf name:
// module names and index keys are exactly where free-form customer content
// (environment names, account ids, per-item keys) lives.
//
// A shape that does not parse as at least TYPE.NAME -- shorter than any
// real Terraform address can be -- masks wholesale rather than risk
// disclosing an unanticipated shape.
func MaskAddress(addr string) string {
	segs := splitAddressSegments(addr)

	var out []string
	i := 0
	for i+1 < len(segs) && segs[i] == "module" {
		_, index := splitSegmentIndex(segs[i+1])
		out = append(out, "module", "<m>"+maskIndex(index))
		i += 2
	}
	if i < len(segs) && segs[i] == "data" {
		out = append(out, "data")
		i++
	}

	if len(segs)-i < 2 {
		// Not enough segments left for TYPE.NAME: an unanticipated shape,
		// masked wholesale rather than disclosed.
		for ; i < len(segs); i++ {
			out = append(out, "<addr>")
		}
		return strings.Join(out, ".")
	}

	typeName, typeIndex := splitSegmentIndex(segs[i])
	out = append(out, typeName+maskIndex(typeIndex))
	i++

	_, nameIndex := splitSegmentIndex(segs[i])
	out = append(out, "<name>"+maskIndex(nameIndex))
	i++

	for ; i < len(segs); i++ {
		out = append(out, "<addr>")
	}
	return strings.Join(out, ".")
}

// splitAddressSegments splits addr on "." at bracket depth zero. A dot
// cannot be trusted as a segment separator inside "[...]": a for_each key
// can itself contain a literal dot.
func splitAddressSegments(addr string) []string {
	var segs []string
	depth := 0
	start := 0
	for i := 0; i < len(addr); i++ {
		switch addr[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '.':
			if depth == 0 {
				segs = append(segs, addr[start:i])
				start = i + 1
			}
		}
	}
	segs = append(segs, addr[start:])
	return segs
}

// splitSegmentIndex splits a segment like `module_name["key"]` into its bare
// name and its bracketed index text (including the brackets), or "" if the
// segment carries no index.
func splitSegmentIndex(seg string) (name, index string) {
	if i := strings.IndexByte(seg, '['); i >= 0 && strings.HasSuffix(seg, "]") {
		return seg[:i], seg[i:]
	}
	return seg, ""
}

// maskIndex masks the content of a bracketed index, keeping the brackets --
// and the quotes, for a string key -- so whether a resource is indexed
// stays visible without disclosing the key or count value itself.
func maskIndex(index string) string {
	if index == "" {
		return ""
	}
	inner := index[1 : len(index)-1]
	if strings.HasPrefix(inner, `"`) && strings.HasSuffix(inner, `"`) {
		return `["<k>"]`
	}
	return "[<k>]"
}
