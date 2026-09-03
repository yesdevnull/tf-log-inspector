// Package diagnose summarises a log's structure without disclosing its
// contents.
package diagnose

import "strings"

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

// MaskComponent masks a component name that looks like a resource address.
// Terraform core writes messages prefixed with the address of the resource
// being worked on, so component names are content too.
func MaskComponent(s string) string {
	if s == "" {
		return s
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
