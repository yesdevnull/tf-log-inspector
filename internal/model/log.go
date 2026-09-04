// Package model holds a loaded log and pure functions over it. Nothing here
// renders or reads flags; internal/profile and cmd/tfli do that.
package model

import (
	"bytes"
	"fmt"
	"os"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// Log is one loaded log file: its bytes, an index of its logical entries, and
// the spans both builders extracted from it.
//
// The whole file is held in Data. Four real HCP captures measure 17 to 37MB,
// so this costs less than the entry index would have under the 102 bytes/line
// estimate the design was originally sized against. Bytes() is the only
// accessor, so swapping in ReadAt or mmap later is contained to this file if a
// log ever turns up large enough to need it.
type Log struct {
	Data    []byte
	Entries []logfmt.Entry
	Comps   *logfmt.Interner
	Stats   logfmt.Stats

	// RPCSpans and UISpans are kept apart rather than concatenated. Their
	// StartMs/EndMs sit on different zero points -- see the doc comment on
	// span.Span -- so a single merged slice would invite exactly the
	// cross-timeline comparison that produces silently wrong orderings.
	RPCSpans []span.Span
	UISpans  []span.Span
	Caps     span.Capabilities
}

// entryIndex retains every entry Scan emits. It deliberately ignores msg and
// fields: the model indexes structure, and any consumer wanting an entry's
// text reads it back out of Data via Bytes, which keeps this slice free of
// pointers for the garbage collector to trace.
type entryIndex struct{ entries []logfmt.Entry }

func (x *entryIndex) Entry(_ uint32, e logfmt.Entry, _ string, _ logfmt.Fields) {
	x.entries = append(x.entries, e)
}

// Load reads a log file whole and extracts everything phase 2 needs in a
// single pass.
func Load(path string) (*Log, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	comps := &logfmt.Interner{}
	idx := &entryIndex{}
	sniffer := span.NewSniffer(comps)
	var rb span.ReportedBuilder
	var ub span.UIHookBuilder

	stats, err := logfmt.Scan(bytes.NewReader(data), comps, idx, sniffer, &rb, &ub)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}

	return &Log{
		Data:     data,
		Entries:  idx.entries,
		Comps:    comps,
		Stats:    stats,
		RPCSpans: rb.Spans(),
		UISpans:  ub.Spans(),
		Caps:     sniffer.Report(),
	}, nil
}

// Bytes returns every line of an entry, including its continuations.
func (l *Log) Bytes(e logfmt.Entry) []byte {
	return l.Data[e.Off : e.Off+uint64(e.Len)]
}
