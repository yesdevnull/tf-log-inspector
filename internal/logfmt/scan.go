package logfmt

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// Sink receives each entry as it is parsed. ord is the entry's zero-based
// ordinal, supplied by the scanner so every sink agrees on numbering. msg is
// the header line's message only -- never continuation text -- and f are the
// fields parsed from it. Both are valid only for the duration of the call.
type Sink interface {
	Entry(ord uint32, e Entry, msg string, f Fields)
}

// StructuredSink is implemented by sinks that need the raw text of a
// structured-output line. A sink that does not implement it never sees that
// text, which is what keeps the diagnostic report's disclosure guarantee
// true by construction: the report's collector deliberately does not
// implement this interface. line is valid only for the duration of the
// call, the same contract as msg in Sink.Entry.
type StructuredSink interface {
	Structured(ord uint32, e Entry, line string)
}

// Scan reads r in a single pass, assembling logical entries and pushing each
// to every sink. Memory use beyond the two interners is independent of
// input size: only the header line's message is retained, and only until
// the entry is flushed.
//
// The interners are bounded rather than free: comps and reqIDs each retain
// one clone of every DISTINCT string they intern, up to Interner's own
// 65534-id cap, so their cost tracks cardinality rather than input size --
// but that cost is paid whether or not a caller reads what it interned.
// cmd/tfli's --diagnose passes a reqIDs Interner solely because this
// signature requires one and never reads an id back out of it, so it still
// pays one map operation per id-bearing entry and, in the worst case, up to
// 65534 cloned UUID strings, on the order of 10MB.
//
// comps and reqIDs are separate Interners because ids compare only within
// one interner: a component id and a request id are different vocabularies
// of very different cardinality (see Entry.ReqID), and sharing an interner
// between them would make an id ambiguous as to which vocabulary it named.
func Scan(r io.Reader, comps, reqIDs *Interner, sinks ...Sink) (Stats, error) {
	var st Stats
	br := bufio.NewReaderSize(r, 256*1024)

	var (
		fieldBuf Fields
		scratch  []byte
		off      uint64

		open   bool
		cur    Entry
		curMsg string
		// contFields is the parse buffer for a continuation line, kept
		// beside fieldBuf rather than sharing it: flush reads fieldBuf
		// after the continuations of its entry have been scanned, so one
		// buffer would have the header's fields overwritten by the last
		// continuation's.
		contFields Fields
		// curContReqID records that a continuation of the entry now open
		// carried a tf_req_id. Compared at flush against the header's own
		// fields, it is what tells an id that was merely unparsed from one
		// that was never there.
		curContReqID bool
		runLen       uint64
		baseTS       time.Time
		haveBase     bool
		ord          uint32
	)

	flush := func() {
		if !open {
			return
		}
		if runLen > longRun {
			st.LongContinuationRuns++
		}
		fieldBuf = ParseFields(curMsg, fieldBuf[:0])
		if curContReqID {
			if _, ok := fieldBuf.Get("tf_req_id"); !ok {
				st.ContinuationOnlyReqIDEntries++
			}
		}
		if id, ok := fieldBuf.Get(reqIDKey); ok {
			cur.ReqID = reqIDs.Intern(id)
		}
		st.Entries++
		st.ByLevel[cur.Level]++
		for _, s := range sinks {
			s.Entry(ord, cur, curMsg, fieldBuf)
		}
		ord++
		open = false
		curMsg = ""
		curContReqID = false
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
			case IsStructuredLine(text):
				// A structured-output line is its own logical entry, closed
				// immediately rather than left open for the next line to
				// continue. Its content is a disclosure risk -- the message
				// carries full resource and module addresses -- so it is
				// counted without ever reaching an ordinary Sink, exactly
				// like the non-hclog content handled below. A sink that
				// opts in via StructuredSink still receives the raw line,
				// for span extraction.
				flush()
				st.StructuredLines++
				st.UntimestampedLines++
				entryOrd := ord
				// The severity is read off the line, so a structured
				// capture's levels count and filter the way an hclog
				// capture's do. Without it every entry of such a log is
				// LevelUnknown, which reads as "this log says nothing about
				// severity" when in fact it says it on every line.
				cur = Entry{Off: off, Len: raw, Lines: 1, Level: StructuredLevel(text)}
				curMsg = ""
				open = true
				flush()
				for _, s := range sinks {
					if ss, ok := s.(StructuredSink); ok {
						ss.Structured(entryOrd, cur, text)
					}
				}

			case h.HasTS:
				flush()
				if !haveBase {
					baseTS, haveBase = h.TS, true
					st.FirstTS = h.TS
				}
				st.LastTS = h.TS

				delta := h.TS.Sub(baseTS).Milliseconds()
				if delta > math.MaxUint32 {
					return st, fmt.Errorf("line %d: timestamp offset %dms exceeds supported maximum %dms", st.PhysicalLines, delta, uint64(math.MaxUint32))
				}
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
					m := h.Msg[:maxHeaderMsg]
					// A byte-index cut can land inside a multi-byte rune.
					// Back off to the last whole rune so a truncated message
					// is never invalid UTF-8. DecodeLastRuneInString reports
					// (RuneError, 1) for a bad encoding; a genuine U+FFFD
					// decodes with size 3, so the size test distinguishes
					// them.
					for len(m) > 0 {
						r, size := utf8.DecodeLastRuneInString(m)
						if r != utf8.RuneError || size > 1 {
							break
						}
						m = m[:len(m)-1]
					}
					curMsg = m
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
				// A substring test first, and the parser only on the lines
				// that pass it. The parser is what decides -- the key
				// spelled inside a JSON body is not a field, and counting
				// it would overstate the blind spot, biasing a decision
				// towards paying for parsing that would buy nothing -- but
				// running it on every continuation line of a 1GB log to
				// reject nearly all of them is a cost this scan does not
				// need to pay.
				if strings.Contains(text, reqIDKey+"=") {
					contFields = ParseFields(text, contFields[:0])
					if _, ok := contFields.Get(reqIDKey); ok {
						st.ContinuationReqIDLines++
						curContReqID = true
					}
				}
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
