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

func TestStripANSIReusesScratch(t *testing.T) {
	_, scratch := StripANSI("\x1b[31mred\x1b[0m", nil)
	if cap(scratch) == 0 {
		t.Fatal("StripANSI returned an empty scratch buffer")
	}
	got, scratch2 := StripANSI("\x1b[32mgreen\x1b[0m", scratch)
	if got != "green" {
		t.Errorf("StripANSI = %q, want green", got)
	}
	if cap(scratch2) < cap(scratch) {
		t.Error("scratch buffer shrank between calls")
	}
}
