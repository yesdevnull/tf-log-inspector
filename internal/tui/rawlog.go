package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// rawLogPageSize is how many entries PgUp/PgDown move the raw log's top
// entry by. The pane's own height is not known here -- Model's width and
// height are the whole terminal's, and the pane heights are worked out in
// layout.go as part of composing a frame -- so this is a fixed, reasonable
// screenful rather than something derived from state a key handler cannot
// see.
const rawLogPageSize = 20

// rawLogState holds the raw log view's own state: which entry sits at the
// top of the pane, and any free-text search in progress or most recently
// run. It is kept as its own struct, rather than loose fields on Model, so
// the raw log's concerns stay grouped with the file that owns them.
type rawLogState struct {
	top int

	// searching is true while '/' is capturing a query one key at a time;
	// query accumulates what has been typed so far. lastQuery is what n/N
	// repeat once a search has been submitted with enter.
	searching bool
	query     string
	lastQuery string

	// notFound records that the last search ran off the end of the log
	// without a match. A miss leaves the position unchanged, which on its
	// own looks exactly like a match on the entry already shown, so the
	// footer says which it was; see Model.footer.
	notFound bool
}

// TopEntry reports the index into m.log.Entries currently at the top of the
// raw log pane.
func (m Model) TopEntry() int {
	return m.raw.top
}

// jumpToSpan switches to the raw log view positioned at the entry that
// closed the RPC span at idx. idx is a rollup row's spanIdx, which is -1 for
// a row that represents many spans rather than one (every ViewProviders and
// ViewTypes row); jumpToSpan leaves m unchanged for those, since there is no
// single span to jump to.
func (m *Model) jumpToSpan(idx int) {
	if idx < 0 || idx >= len(m.log.RPCSpans) {
		return
	}
	// Span.Entry indexes the same log's Entries, but nothing revalidates it
	// when a Log is assembled, so every reader of the field checks it --
	// componentProviders does the same -- rather than each deciding for
	// itself whether it can be trusted. An index outside the log leaves the
	// view where it is: there is no entry to jump to.
	entry := int(m.log.RPCSpans[idx].Entry)
	if entry >= len(m.log.Entries) {
		return
	}
	m.view = ViewRawLog
	m.raw.top = entry
	m.selected = 0
	m.invalidateRows()
}

// pageRawLog moves the raw log's top entry by delta screenfuls.
func (m *Model) pageRawLog(delta int) {
	m.scrollRawLog(delta * rawLogPageSize)
}

// scrollRawLog moves the raw log's top entry by delta entries, clamped to
// [0, len(Entries)-1] (or 0 for a log with no entries at all) so neither
// paging nor an arrow key can walk off either end of the index.
//
// It also drops any "pattern not found": the miss was reported about the
// position the search started from, and scrolling has moved it.
func (m *Model) scrollRawLog(delta int) {
	m.raw.notFound = false
	m.raw.top += delta
	if m.raw.top < 0 {
		m.raw.top = 0
	}
	if max := len(m.log.Entries) - 1; max < 0 {
		m.raw.top = 0
	} else if m.raw.top > max {
		m.raw.top = max
	}
}

// componentProviders maps a log line's component (Entry.Comp) to the
// provider whose span closed on an entry carrying that component. It is
// built once per render/search from the (few thousand, at most) spans
// rather than walked per entry (tens of thousands on a real capture): every
// entry sharing a component with a provider's own closing entry is that
// provider's traffic -- its request/response lines, its DEBUG chatter, its
// HTTP body dumps -- not just the one line that happened to close a span.
// A component of 0 means "none" (see logfmt.Entry), so it is never
// recorded: an entry with no component never resolves to a provider.
func componentProviders(spans []span.Span, entries []logfmt.Entry) map[uint16]string {
	out := map[uint16]string{}
	for _, s := range spans {
		if int(s.Entry) >= len(entries) {
			continue
		}
		if c := entries[s.Entry].Comp; c != 0 {
			out[c] = s.Provider
		}
	}
	return out
}

// entryVisible reports whether entry e passes the raw log's active facet
// filter. Level is a per-entry attribute and applies directly via
// Filter.MatchEntry.
//
// Of the three span dimensions, only provider applies here, via
// compProviders: an entry's own component says whose traffic it is,
// regardless of whether that particular entry happens to close a span. RPC
// and resource type are deliberately NOT applied to raw entries at all --
// they are properties of one call, not of a log line, and most lines
// (request/response chatter, DEBUG output, HTTP body dumps) surrounding a
// call never carry a tf_rpc or tf_resource_type field of their own. Hiding
// every line outside the exact RPC boundary because, say, ReadDataSource is
// selected would hide precisely the context -- the provider's own
// surrounding output -- that jumping to a slow call exists to show.
//
// With no provider facet selected every entry passes regardless of
// component. Once one is selected, an entry whose component maps to no
// provider (Terraform's own core lines, plan output) is hidden: the filter
// asked for one provider's traffic, and a core line is not that. Such an
// entry resolves to the empty provider, so it is normalised through
// model.FacetKey and matched against "(none)" -- the same key the facet
// pane offers for a span with no provider address -- rather than against a
// raw "" no checkbox can ever select.
func entryVisible(f model.Filter, compProviders map[uint16]string, e logfmt.Entry) bool {
	if !f.MatchEntry(e) {
		return false
	}
	if len(f.Providers) == 0 {
		return true
	}
	return f.Providers[model.FacetKey(compProviders[e.Comp])]
}

// renderRawLog renders entries from TopEntry() downward, honouring the
// active facet filter, at most w columns wide and h lines tall.
//
// A log line's own bytes are untrusted terminal input -- Log.Bytes returns
// them verbatim, and Terraform's plan output in a captured log is
// colourised -- so every line is stripped of its escape sequences before it
// is clipped. Measuring around them would not be enough: a sequence that
// moves the cursor or clears the screen corrupts the whole frame rather
// than its own line, and a colour the line never resets bleeds into the
// panes beside it. One scratch buffer is reused across the loop, as
// StripANSI's API intends. Each visible
// entry renders every line Off/Len cover -- including continuations -- so a
// multi-line entry such as an HTTP body dump appears whole rather than as a
// fragment; an entry that would not fit inside the remaining height is left
// for the next page rather than cut apart, unless it is the very first
// entry considered, in which case it is shown in full regardless (better to
// overflow the pane than show nothing for the one entry the user jumped to).
func (m Model) renderRawLog(w, h int) string {
	f := m.filter()
	compProviders := componentProviders(m.log.RPCSpans, m.log.Entries)

	var lines []string
	var scratch []byte
	for i := m.TopEntry(); i < len(m.log.Entries); i++ {
		e := m.log.Entries[i]
		if !entryVisible(f, compProviders, e) {
			continue
		}
		entryLines := strings.Split(strings.TrimRight(string(m.log.Bytes(e)), "\n"), "\n")
		if len(lines) > 0 && len(lines)+len(entryLines) > h {
			break
		}
		for _, ln := range entryLines {
			var plain string
			plain, scratch = logfmt.StripANSI(ln, scratch)
			lines = append(lines, clipWidth(plain, w))
		}
		if len(lines) >= h {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// handleSearchKey routes one keystroke while a search query is being typed:
// runes and space extend the query, backspace removes its last rune, enter
// submits it and jumps to the first match, esc cancels without searching,
// and ctrl+c quits.
func (m *Model) handleSearchKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyCtrlC:
		// In the alt screen Ctrl+C arrives as a key rather than a signal, so
		// the quit binding has to be honoured here too: a prompt that
		// swallows every key would otherwise trap a user who opened it by
		// accident until they guessed Esc.
		m.raw.searching = false
		m.quitting = true
	case tea.KeyEnter:
		m.raw.searching = false
		if m.raw.query != "" {
			m.raw.lastQuery = m.raw.query
			m.raw.notFound = !m.searchFrom(m.raw.top, true, true)
		}
	case tea.KeyEsc:
		m.raw.searching = false
		m.raw.query = ""
		m.raw.notFound = false
	case tea.KeyBackspace:
		if r := []rune(m.raw.query); len(r) > 0 {
			m.raw.query = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		m.raw.query += " "
	case tea.KeyRunes:
		m.raw.query += string(msg.Runes)
	}
}

// searchAgain repeats the last submitted search, forward for n or backward
// for N. It is a no-op until a search has been run at least once.
//
// It searches from the entry AFTER the one at the top of the pane, since the
// top entry is where the previous match landed: including it would make n
// return that same match for ever.
func (m *Model) searchAgain(direction int) {
	if m.raw.lastQuery == "" {
		return
	}
	m.raw.notFound = !m.searchFrom(m.raw.top, direction > 0, false)
}

// searchFrom is the synchronous free-text search over m.log.Data described
// in the interface's design notes: the spec sizes /pattern with n/N as a
// cancellable goroutine because it was designed against a 1GB log, but real
// captures measured for this tool are 17-37MB, where a linear scan costs a
// few tens of milliseconds -- well under a frame. The concurrent,
// cancellable version is deferred until a log turns up large enough to need
// it, the same reasoning phase 2 used to replace mmap with a whole-file
// read.
//
// It scans from start in the given direction, honouring the same filter
// renderRawLog does, and moves the raw log's top entry to the first match.
// It does not wrap around either end of the log; reaching an end without a
// match leaves the position unchanged.
//
// includeStart says whether the entry AT start counts as a candidate. A
// newly submitted search includes it: the user can see that entry on the
// pane's first line -- Enter on a slow call puts it there -- and a query for
// text sitting on that very line must find it rather than report "pattern
// not found". n and N exclude it, or they would return the match already
// shown for ever instead of advancing.
//
// It matches against the same ANSI-stripped text renderRawLog puts on
// screen, not the entry's original bytes: an escape sequence sitting inside
// a colourised plan line is invisible to the user, so it must not be able to
// split a phrase the screen shows whole, or let a query match bytes the
// screen never displays. searchFrom scans the whole log, so the scratch
// buffer is reused across entries the way renderRawLog reuses its own,
// rather than allocating one stripped copy per entry.
func (m *Model) searchFrom(start int, forward, includeStart bool) bool {
	if m.raw.lastQuery == "" {
		return false
	}
	f := m.filter()
	compProviders := componentProviders(m.log.RPCSpans, m.log.Entries)

	step := 1
	if !forward {
		step = -1
	}
	first := start
	if !includeStart {
		first += step
	}
	var scratch []byte
	for i := first; i >= 0 && i < len(m.log.Entries); i += step {
		e := m.log.Entries[i]
		if !entryVisible(f, compProviders, e) {
			continue
		}
		var plain string
		plain, scratch = logfmt.StripANSI(string(m.log.Bytes(e)), scratch)
		if strings.Contains(plain, m.raw.lastQuery) {
			m.raw.top = i
			return true
		}
	}
	return false
}
