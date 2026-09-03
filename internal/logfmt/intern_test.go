package logfmt

import (
	"strconv"
	"testing"
)

func TestInternerReturnsSameIDForSameString(t *testing.T) {
	var in Interner
	if a, b := in.Intern("provider.aws"), in.Intern("provider.aws"); a != b {
		t.Fatalf("Intern returned %d then %d for the same string", a, b)
	}
}

func TestInternerRoundTrips(t *testing.T) {
	var in Interner
	id := in.Intern("statemgr.Filesystem")
	if got := in.Lookup(id); got != "statemgr.Filesystem" {
		t.Errorf("Lookup(%d) = %q, want %q", id, got, "statemgr.Filesystem")
	}
}

func TestInternerEmptyStringIsZero(t *testing.T) {
	var in Interner
	if id := in.Intern(""); id != 0 {
		t.Errorf("Intern(\"\") = %d, want 0", id)
	}
	if got := in.Lookup(0); got != "" {
		t.Errorf("Lookup(0) = %q, want empty", got)
	}
}

// The id space is uint16. Overflow must saturate to a sentinel rather than
// wrap, which would silently alias one component onto another.
func TestInternerSaturatesOnOverflow(t *testing.T) {
	var in Interner
	first := in.Intern("first")
	for i := 0; i < 70000; i++ {
		in.Intern("s" + strconv.Itoa(i))
	}
	if got := in.Lookup(first); got != "first" {
		t.Errorf("Lookup(%d) = %q after overflow, want %q", first, got, "first")
	}
	if in.Overflowed() == 0 {
		t.Error("Overflowed() = 0, want a non-zero count")
	}
	if got := in.Lookup(OverflowID); got != "(overflow)" {
		t.Errorf("Lookup(OverflowID) = %q, want %q", got, "(overflow)")
	}
}
