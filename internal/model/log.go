// Package model holds a loaded log and pure functions over it. Nothing here
// renders or reads flags; internal/profile and cmd/tfli do that.
package model

import (
	"bytes"
	"fmt"
	"os"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
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

	// Contexts and Attribs are the address-attribution layer. Attribs is
	// parallel to RPCSpans specifically -- never to a concatenation of
	// RPCSpans and UISpans, which are kept apart above. UISpans need no
	// attribution: their Address is observed, not inferred.
	//
	// Attribs is nil when the log carries no address context at all, which
	// is a different fact from a span that context failed to resolve. See
	// HasAddressContext.
	Contexts []attrib.Context
	Attribs  []attrib.Attribution
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
	// Enable the bare-tf_provider_addr fallback. This is set only here, on
	// the --profile path: --diagnose's report is masked for sharing and does
	// not render provider addresses, so giving it a component-derived value
	// would change a disclosure surface for no benefit.
	rb.Comps = comps
	var ub span.UIHookBuilder
	var cc attrib.ContextCollector

	stats, err := logfmt.Scan(bytes.NewReader(data), comps, idx, sniffer, &rb, &ub, &cc)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}

	rpcSpans := rb.Spans()
	// Attribution runs only where there is something to correlate against.
	// Below one completed context pair the answer for every span would be
	// the same, and recording it per span would make a property of the LOG
	// look like a property of each call.
	var (
		ctxs    []attrib.Context
		attribs []attrib.Attribution
	)
	if cc.CompletedPairs() > 0 {
		ctxs = cc.Contexts()
		attribs = attrib.Correlate(rpcSpans, stats.FirstTS, ctxs)
	}

	return &Log{
		Data:     data,
		Entries:  idx.entries,
		Comps:    comps,
		Stats:    stats,
		RPCSpans: rpcSpans,
		UISpans:  ub.Spans(),
		Caps:     sniffer.Report(),
		Contexts: ctxs,
		Attribs:  attribs,
	}, nil
}

// Bytes returns every line of an entry, including its continuations.
func (l *Log) Bytes(e logfmt.Entry) []byte {
	return l.Data[e.Off : e.Off+uint64(e.Len)]
}

// HasAddressContext reports whether this log carries any address context at
// all. It is the difference between "this log cannot answer which resource a
// call belongs to" and "this log can, and did not for this call" -- two facts
// the interface must not present in the same words.
//
// This tests Contexts, not Attribs. attrib.Correlate always allocates
// make([]Attribution, len(spans)), so a log with completed context but zero
// RPC spans (e.g. a plan with no provider calls at all) gets a non-nil,
// zero-length Attribs -- len(Attribs) > 0 would wrongly report no context for
// a log that plainly has some. Contexts is only ever populated under the same
// CompletedPairs() > 0 gate in Load, and a completed pair guarantees at least
// one entry in ContextCollector.Contexts(), so this reflects the log-level
// property regardless of how many RPC spans exist to attribute.
func (l *Log) HasAddressContext() bool { return len(l.Contexts) > 0 }
