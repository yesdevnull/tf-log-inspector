package logfmt

import (
	"testing"
	"unsafe"
)

// Entry must stay 24 bytes. model.Log holds one per logical entry for the
// whole session, so its width is the index's resident cost: the spec's
// Sizing paragraph budgets ~190MB for a 1GB log at 24 bytes, and 32 would
// make that ~254MB. The field order is what keeps it there -- Timestamped
// sits in the byte after Level, which leaves ReqID the tail padding -- and
// Go does not reorder fields, so the order is load-bearing rather than
// stylistic.
func TestEntryStaysTwentyFourBytes(t *testing.T) {
	if got := unsafe.Sizeof(Entry{}); got != 24 {
		t.Errorf("unsafe.Sizeof(Entry{}) = %d, want 24 -- check the field order before widening a field", got)
	}
}
