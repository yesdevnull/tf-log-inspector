package logfmt

import (
	"strconv"
	"strings"
	"unicode"
)

// DisplayText makes untrusted text safe for a single display line. Controls
// are shown as visible escapes so decoded JSON and filenames cannot issue
// terminal commands or inject rows. Apply styling only after this step.
func DisplayText(s string) string {
	// Raw files and filenames may contain single-byte C1 controls that are
	// invalid UTF-8 and would otherwise bypass rune-based control checks.
	s = strings.ToValidUTF8(s, "\uFFFD")
	if strings.IndexFunc(s, unicode.IsControl) < 0 {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			quoted := strconv.QuoteRune(r)
			b.WriteString(quoted[1 : len(quoted)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// HasANSI reports whether s contains an ANSI escape sequence.
func HasANSI(s string) bool { return strings.IndexByte(s, 0x1b) >= 0 }

// StripANSI removes ANSI escape sequences from s. scratch is reused as the
// working buffer and the grown buffer is returned so the caller can pass it
// back on the next call. Strings with no escapes are returned unchanged.
func StripANSI(s string, scratch []byte) (string, []byte) {
	if !HasANSI(s) {
		return s, scratch
	}
	scratch = scratch[:0]
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			scratch = append(scratch, s[i])
			i++
			continue
		}
		// Skip ESC, an optional '[', parameter bytes, then one final byte.
		// An unterminated sequence consumes the rest of the string.
		i++
		if i < len(s) && s[i] == '[' {
			i++
		}
		for i < len(s) && (s[i] < '@' || s[i] > '~') {
			i++
		}
		if i < len(s) {
			i++
		}
	}
	return string(scratch), scratch
}
