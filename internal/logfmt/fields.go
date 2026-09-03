package logfmt

// Field is one structured key=value pair from a log line.
type Field struct {
	Key, Val string
}

// Fields is the set of structured pairs on a line. Order is not meaningful:
// Terraform emits the same fields in different orders on different lines.
type Fields []Field

// Get returns the value for key.
func (f Fields) Get(key string) (string, bool) {
	for _, fl := range f {
		if fl.Key == key {
			return fl.Val, true
		}
	}
	return "", false
}

// ValidKey reports whether s has the shape of an hclog field key:
// an optional "@", then an identifier of letters, digits, "_", "." and "-".
//
// This is the disclosure guarantee for field keys. Log content that merely
// happens to contain "=" -- a JSON body, a quoted CLI argument, a base64 blob
// -- must never be recorded as a key, because keys are printed verbatim.
func ValidKey(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '@' {
		s = s[1:]
		if s == "" {
			return false
		}
	}
	if c := s[0]; !isAlpha(c) && c != '_' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isAlpha(c) && !isDigit(c) && c != '_' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// ParseFields extracts key=value pairs from s, appending to dst and returning
// the result. Pass dst[:0] to reuse a buffer across lines. Tokens whose key
// fails ValidKey are discarded entirely.
func ParseFields(s string, dst Fields) Fields {
	for i := 0; i < len(s); {
		if isSpace(s[i]) {
			i++
			continue
		}
		key, val, next, ok := parsePair(s, i)
		if ok && ValidKey(key) {
			dst = append(dst, Field{Key: key, Val: val})
		}
		i = next
	}
	return dst
}

// parsePair reads one whitespace-delimited token starting at i, returning ok
// only when the token has the k=v shape. Validity of the key is the caller's
// concern.
func parsePair(s string, i int) (key, val string, next int, ok bool) {
	start := i
	eq := -1
	for ; i < len(s); i++ {
		if s[i] == '=' && eq < 0 {
			eq = i
			if i+1 < len(s) && s[i+1] == '"' {
				v, end := readQuoted(s, i+1)
				return s[start:eq], v, end, true
			}
			continue
		}
		if isSpace(s[i]) {
			break
		}
	}
	if eq < 0 || eq == start {
		return "", "", i, false
	}
	return s[start:eq], s[eq+1 : i], i, true
}

// readQuoted consumes a quoted value beginning at the opening quote, honouring
// backslash escapes as hclog writes them. It returns the value without the
// surrounding quotes and the index just past the closing quote. An
// unterminated quote consumes the remainder of the string.
func readQuoted(s string, open int) (val string, next int) {
	for i := open + 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return s[open+1 : i], i + 1
		}
	}
	return s[open+1:], len(s)
}
