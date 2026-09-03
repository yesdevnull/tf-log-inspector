package logfmt

import (
	"strconv"
	"strings"
	"testing"
	"unsafe"
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

// withinBackingArray reports whether sub's data pointer falls inside whole's
// backing array, i.e. whether retaining sub would keep the whole of whole
// reachable. Mirrors the check in internal/diagnose/diagnose_test.go used to
// find the equivalent field-key hazard.
func withinBackingArray(sub, whole string) bool {
	if len(whole) == 0 {
		return false
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(whole)))
	end := start + uintptr(len(whole))
	p := uintptr(unsafe.Pointer(unsafe.StringData(sub)))
	return p >= start && p < end
}

// TestInternDoesNotPinItsSourceLine proves a component id entering the
// Interner does not retain any part of the line it was parsed from. Every
// other retention site in this codebase clones for this exact reason
// (ReportedBuilder.retain, Sniffer.trackRequestID, Collector.bumpFieldKey);
// Intern is the one that did not, and an uncloned component -- a small
// substring of a per-line buffer -- would otherwise pin the whole line (up to
// maxHeaderMsg, 64KB) alive for the life of the run.
func TestInternDoesNotPinItsSourceLine(t *testing.T) {
	line := "backend/local " + strings.Repeat("x", 4000)
	comp := line[:len("backend/local")]

	var in Interner
	id := in.Intern(comp)
	got := in.Lookup(id)

	if withinBackingArray(got, line) {
		t.Errorf("interned component %q shares backing memory with its source line -- retaining it pins the whole line alive", got)
	}
}
