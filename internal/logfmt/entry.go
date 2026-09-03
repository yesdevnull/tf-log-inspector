package logfmt

import "time"

// maxHeaderMsg caps the retained header message, bounding memory against a
// pathologically long single line.
const maxHeaderMsg = 64 << 10

// longRun is the continuation-run length above which a run is counted as a
// block of interleaved non-hclog output rather than a wrapped message.
const longRun = 5

// Entry is one logical log entry: a timestamped line plus any continuation
// lines that follow it. Off and Len cover every line of the entry, so a
// consumer can seek to Off and read Len bytes to render it whole.
//
// The struct holds no pointers and no strings, so a large slice of them costs
// the garbage collector nothing to scan.
type Entry struct {
	Off         uint64 // byte offset of the entry's first line
	Len         uint32 // bytes covering all lines of the entry
	TSms        uint32 // milliseconds since the first timestamped entry
	Level       Level
	Comp        uint16 // interned component; 0 means none
	Lines       uint16 // physical line count, saturating
	Timestamped bool   // false for interleaved non-hclog content
}

// Stats summarises a scan. It is the raw material of the diagnostic report.
type Stats struct {
	Entries              uint64
	PhysicalLines        uint64
	ContinuationLines    uint64
	ContinuationBytes    uint64
	UntimestampedLines   uint64 // every physical line with no timestamp
	LongContinuationRuns uint64 // runs longer than longRun lines
	BackwardsTimestamps  uint64
	LinesSaturated       uint64
	ByLevel              [6]uint64
	Bytes                uint64
	SawANSI              bool
	FirstTS, LastTS      time.Time
}
