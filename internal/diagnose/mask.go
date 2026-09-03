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
