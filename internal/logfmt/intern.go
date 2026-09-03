package logfmt

// OverflowID is returned once the id space is exhausted. Component
// cardinality is driven by log content -- Terraform core uses resource
// addresses as message prefixes -- so exhaustion is reachable on a large plan
// and must not silently alias one string onto another.
const OverflowID uint16 = 65535

// Interner maps component names to small integer ids so entries stay
// pointer-free. ID 0 is always the empty string, meaning "no component".
type Interner struct {
	ids      map[string]uint16
	strs     []string
	overflow uint64
}

func (i *Interner) init() {
	if i.strs == nil {
		i.ids = map[string]uint16{"": 0}
		i.strs = []string{""}
	}
}

// Intern returns a stable id for s, allocating one if needed. Once the id
// space is full it returns OverflowID and counts the event.
func (i *Interner) Intern(s string) uint16 {
	i.init()
	if id, ok := i.ids[s]; ok {
		return id
	}
	if len(i.strs) >= int(OverflowID) {
		i.overflow++
		return OverflowID
	}
	id := uint16(len(i.strs))
	i.strs = append(i.strs, s)
	i.ids[s] = id
	return id
}

// Lookup returns the string for an id, "(overflow)" for OverflowID, or "" if
// the id is unknown.
func (i *Interner) Lookup(id uint16) string {
	if id == OverflowID {
		return "(overflow)"
	}
	if int(id) >= len(i.strs) {
		return ""
	}
	return i.strs[id]
}

// Len reports how many distinct strings have been interned.
func (i *Interner) Len() int { return len(i.strs) }

// Overflowed reports how many strings could not be interned.
func (i *Interner) Overflowed() uint64 { return i.overflow }
