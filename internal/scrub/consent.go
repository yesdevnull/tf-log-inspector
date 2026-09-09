package scrub

import "strings"

// Consent text can be the only occurrence of an application's name. Only a
// complete recognised sentence preserves prose; other wording is opaque.
func (s *session) discoverConsent(value string, numeric bool) {
	const prefix = "Allow the application to access "
	const suffix = " on behalf of the signed in user"
	sentence := strings.TrimSuffix(value, ".")
	if strings.HasPrefix(sentence, prefix) && strings.HasSuffix(sentence, suffix) && len(sentence) > len(prefix)+len(suffix) {
		end := len(sentence) - len(suffix)
		name := value[len(prefix):end]
		if strings.TrimSpace(name) == name && !strings.ContainsAny(name, "\r\n\t") {
			part := s.part(name, "name", len(prefix), end, "")
			s.composite(value, "name", []compositePart{part}, "")
			return
		}
	}
	// Use secret precedence so URL/address detection cannot reconstruct an
	// unrecognised description or retain any of its original wording.
	s.discover(value, "secret", numeric)
}
