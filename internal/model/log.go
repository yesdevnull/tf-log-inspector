// Package model holds a loaded log and pure functions over it. Nothing here
// renders or reads flags; internal/profile and cmd/tfli do that.
package model

import (
	"bytes"
	"fmt"
	"os"
	"sync"

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
	responseOnce sync.Once
	responses    []logfmt.ProviderJSON
	responseErr  error
	Data         []byte
	Entries      []logfmt.Entry
	Comps        *logfmt.Interner
	Stats        logfmt.Stats

	// RPCSpans and UISpans are kept apart rather than concatenated. Their
	// StartMs/EndMs sit on different zero points -- see the doc comment on
	// span.Span -- so a single merged slice would invite exactly the
	// cross-timeline comparison that produces silently wrong orderings.
	RPCSpans []span.Span
	UISpans  []span.Span
	Caps     span.Capabilities

	// UISaturatedDurations counts UI timings capped at the storage limit.
	// Consumers must qualify these values and their totals as lower bounds.
	UISaturatedDurations uint64

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
	reqIDs := &logfmt.Interner{}
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

	stats, err := logfmt.Scan(bytes.NewReader(data), comps, reqIDs, idx, sniffer, &rb, &ub, &cc)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}

	rpcSpans := rb.Spans()
	// Attribution runs only where there is something to correlate against.
	// The gate is whether ANY context was collected, not whether one has
	// CLOSED: a context is appended the moment a _start hook is seen, so a
	// capture killed mid-run -- every resource started, none finished -- has
	// real context windows despite zero completed pairs. CompletedPairs()
	// alone would miss exactly that case, reporting no context at all for a
	// log that plainly carries some (see Log.HasAddressContext).
	var (
		ctxs    []attrib.Context
		attribs []attrib.Attribution
	)
	if len(cc.Contexts()) > 0 {
		ctxs = cc.Contexts()
		attribs = attrib.Correlate(rpcSpans, stats.FirstTS, ctxs)
	}

	return &Log{
		Data:                 data,
		Entries:              idx.entries,
		Comps:                comps,
		Stats:                stats,
		RPCSpans:             rpcSpans,
		UISpans:              ub.Spans(),
		UISaturatedDurations: ub.Saturated(),
		Caps:                 sniffer.Report(),
		Contexts:             ctxs,
		Attribs:              attribs,
	}, nil
}

// Bytes returns every line of an entry, including its continuations.
func (l *Log) Bytes(e logfmt.Entry) []byte {
	return l.Data[e.Off : e.Off+uint64(e.Len)]
}

// ProviderResponse lazily reconstructs provider JSON without changing source
// entries. Errors contain structural diagnostics only; raw inspection remains
// available even when reconstruction fails. Empty Text means no matching body.
func (l *Log) ProviderResponse(e logfmt.Entry) (logfmt.ProviderJSON, error) {
	l.responseOnce.Do(func() {
		l.responses, l.responseErr = logfmt.ReconstructProviderJSON(string(l.Data))
	})
	if l.responseErr != nil {
		return logfmt.ProviderJSON{}, l.responseErr
	}
	for _, response := range l.responses {
		for _, fragment := range response.Fragments {
			if uint64(fragment.Start) < e.Off+uint64(e.Len) && uint64(fragment.End) > e.Off {
				response.Fragments = append([]logfmt.JSONFragment(nil), response.Fragments...)
				return response, nil
			}
		}
	}
	return logfmt.ProviderJSON{}, nil
}

// HasAddressContext reports whether this log carries any address context at
// all. It is the difference between "this log cannot answer which resource a
// call belongs to" and "this log can, and did not for this call" -- two facts
// the interface must not present in the same words.
//
// This tests Contexts, not Attribs. attrib.Correlate always allocates
// make([]Attribution, len(spans)), so a log with context but zero RPC spans
// (e.g. a plan with no provider calls at all) gets a non-nil, zero-length
// Attribs -- len(Attribs) > 0 would wrongly report no context for a log that
// plainly has some. Contexts is only ever populated under the same
// len(cc.Contexts()) > 0 gate in Load, which requires no context to have
// CLOSED: a context still open at end-of-log is real context too (see
// Load's own comment), so this reflects the log-level property regardless
// of how many RPC spans exist to attribute, or how many contexts finished.
func (l *Log) HasAddressContext() bool { return len(l.Contexts) > 0 }

// ScopeFor returns the indices of every entry carrying request id reqID, in
// ascending order -- one call's own log lines, which is what the raw log
// shows when a call is opened.
//
// It MATERIALISES rather than returning a predicate. A scope is small (a
// measured min of 4, median 7 and max 1934 entries on the standardised
// capture) and the pane iterating it must not walk the log to find its
// members: renderRawLog's only early exit is a full pane, so a scope that
// never fills one would scan every remaining entry on every keystroke --
// 38,379 of them on that capture, and about 8 million at the 1GB target.
//
// Id 0 means "no request id" rather than a call whose id is zero, so it
// scopes to nothing. Returning every entry that carries no id instead would
// answer a jump with a pane full of unrelated traffic.
func (l *Log) ScopeFor(reqID uint16) []int {
	if reqID == 0 {
		return nil
	}
	var scope []int
	for i, e := range l.Entries {
		if e.ReqID == reqID {
			scope = append(scope, i)
		}
	}
	return scope
}

// AttributionForEntry finds the attribution recorded for the RPC span that
// closed log entry `entry`, the same identifier jumpToSpan already trusts to
// name a span uniquely. It is the one supported way to look up a span's
// Attribution: a lookup BY POSITION into Attribs is only safe against the
// unfiltered RPCSpans it is parallel to, and a caller holding a filtered
// copy (e.g. the TUI timeline's filtered slice) has no position that still
// lines up -- so this is entry-keyed instead, which is safe against either.
//
// logfmt.Scan feeds every sink from ONE shared ordinal counter, so Entry
// values are unique log-wide, not per builder: each ordinal closes at most
// one entry, and so at most one span, whichever tier built it. A UI-hook
// span's Entry is therefore never equal to any RPCSpans[i].Entry -- its
// closing entry was a structured-output line, not an RPC-tier one -- so
// passing one here simply finds nothing and returns the zero Attribution.
func (l *Log) AttributionForEntry(entry uint32) attrib.Attribution {
	for i, s := range l.RPCSpans {
		if s.Entry == entry && i < len(l.Attribs) {
			return l.Attribs[i]
		}
	}
	return attrib.Attribution{}
}
