package logfmt

import (
	"bufio"
	"io"
	"math"
	"strings"
	"time"
)

// Sink receives each entry as it is parsed. ord is the entry's zero-based
// ordinal, supplied by the scanner so every sink agrees on numbering. msg is
// the header line's message only -- never continuation text -- and f are the
// fields parsed from it. Both are valid only for the duration of the call.
type Sink interface {
	Entry(ord uint32, e Entry, msg string, f Fields)
}

// Scan reads r in a single pass, assembling logical entries and pushing each
// to every sink. Memory use is independent of input size: only the header
// line's message is retained, and only until the entry is flushed.
func Scan(r io.Reader, comps *Interner, sinks ...Sink) (Stats, error) {
	var st Stats
	br := bufio.NewReaderSize(r, 256*1024)

	var (
		fieldBuf Fields
		scratch  []byte
		off      uint64

		open     bool
		cur      Entry
		curMsg   string
		runLen   uint64
		baseTS   time.Time
		haveBase bool
		ord      uint32
	)

	flush := func() {
		if !open {
			return
		}
		if runLen > longRun {
			st.LongContinuationRuns++
		}
		fieldBuf = ParseFields(curMsg, fieldBuf[:0])
		st.Entries++
		st.ByLevel[cur.Level]++
		for _, s := range sinks {
			s.Entry(ord, cur, curMsg, fieldBuf)
		}
		ord++
		open = false
		curMsg = ""
		runLen = 0
	}

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			raw := uint32(len(line))
			text := strings.TrimRight(line, "\r\n")
			if HasANSI(text) {
				st.SawANSI = true
				text, scratch = StripANSI(text, scratch)
			}

			st.PhysicalLines++
			st.Bytes += uint64(raw)

			h := ParseHeader(text)
			switch {
			case h.HasTS:
				flush()
				if !haveBase {
					baseTS, haveBase = h.TS, true
					st.FirstTS = h.TS
				}
				st.LastTS = h.TS

				delta := h.TS.Sub(baseTS).Milliseconds()
				if delta < 0 {
					// Concurrent goroutines can emit out of order. Clamp
					// rather than wrapping the unsigned field.
					st.BackwardsTimestamps++
					delta = 0
				}
				cur = Entry{
					Off:         off,
					Len:         raw,
					TSms:        uint32(delta),
					Level:       h.Level,
					Comp:        comps.Intern(h.Comp),
					Lines:       1,
					Timestamped: true,
				}
				if len(h.Msg) > maxHeaderMsg {
					curMsg = h.Msg[:maxHeaderMsg]
				} else {
					curMsg = h.Msg
				}
				open = true

			case open:
				// Continuation: counted and covered by Off/Len, but its text
				// never reaches a sink.
				st.ContinuationLines++
				st.ContinuationBytes += uint64(raw)
				st.UntimestampedLines++
				runLen++
				cur.Len += raw
				if cur.Lines == math.MaxUint16 {
					st.LinesSaturated++
				} else {
					cur.Lines++
				}

			default:
				// Non-hclog content before any entry.
				st.UntimestampedLines++
				cur = Entry{Off: off, Len: raw, Lines: 1}
				curMsg = ""
				open = true
			}
			off += uint64(raw)
		}

		if err != nil {
			flush()
			if err == io.EOF {
				return st, nil
			}
			return st, err
		}
	}
}
