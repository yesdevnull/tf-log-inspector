package logfmt

import "testing"

func TestHasANSI(t *testing.T) {
	if HasANSI("plain text") {
		t.Error("HasANSI reported true for plain text")
	}
	if !HasANSI("\x1b[31mred\x1b[0m") {
		t.Error("HasANSI reported false for coloured text")
	}
}

func TestStripANSI(t *testing.T) {
	got, _ := StripANSI("\x1b[1m\x1b[32m+ create\x1b[0m", nil)
	if got != "+ create" {
		t.Errorf("StripANSI = %q, want %q", got, "+ create")
	}
}

func TestStripANSILeavesPlainTextUntouched(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: b"
	if got, _ := StripANSI(in, nil); got != in {
		t.Errorf("StripANSI altered plain text: %q", got)
	}
}

func TestStripANSIUnterminatedSequence(t *testing.T) {
	if got, _ := StripANSI("text\x1b[", nil); got != "text" {
		t.Errorf("StripANSI = %q, want %q", got, "text")
	}
}

func TestStripANSIWritesIntoCallersBuffer(t *testing.T) {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = '#' // poison, so we can tell whether StripANSI wrote here
	}
	got, out := StripANSI("\x1b[31mred\x1b[0m", buf[:0])
	if got != "red" {
		t.Fatalf("StripANSI = %q, want %q", got, "red")
	}
	if string(buf[:3]) != "red" {
		t.Errorf("caller's buffer holds %q, want the stripped bytes: StripANSI allocated its own buffer instead of reusing the one it was given", buf[:3])
	}
	if cap(out) != cap(buf) {
		t.Errorf("returned buffer cap = %d, want %d: a fresh buffer was returned", cap(out), cap(buf))
	}
}

func TestStripANSIDoesNotAllocatePerCall(t *testing.T) {
	scratch := make([]byte, 0, 256)
	in := "\x1b[31mred\x1b[0m text \x1b[1mbold\x1b[0m"
	var got string
	// One allocation is expected and unavoidable: the string(scratch) copy of
	// the result. A second would mean the scratch buffer is being reallocated
	// on every line.
	n := testing.AllocsPerRun(100, func() {
		got, scratch = StripANSI(in, scratch[:0])
	})
	if got == "" {
		t.Fatal("StripANSI returned an empty string")
	}
	if n > 1 {
		t.Errorf("StripANSI allocated %.0f times per call, want at most 1", n)
	}
}
