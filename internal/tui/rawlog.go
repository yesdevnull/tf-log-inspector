package tui

import (
	"bytes"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// rawLogPageSize is how many entries PgUp/PgDown move the raw log's top
// entry by. A real pane height isn't known here -- Model's width/height are
// the whole terminal, and splitting that into per-pane heights is Task 6's
// layout work -- so this is a fixed, reasonable screenful rather than
// something derived from state this task doesn't own.
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
func (m Model) jumpToSpan(idx int) Model {
	if idx < 0 || idx >= len(m.log.RPCSpans) {
		return m
	}
	m.view = ViewRawLog
	m.raw.top = int(m.log.RPCSpans[idx].Entry)
	m.selected = 0
	m.invalidateRows()
	return m
}

// pageRawLog moves the raw log's top entry by delta screenfuls, clamped to
// [0, len(Entries)-1] (or 0 for a log with no entries at all) so paging can
// never walk off either end of the index.
func (m *Model) pageRawLog(delta int) {
	m.raw.top += delta * rawLogPageSize
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
// asked for one provider's traffic, and a core line is not that.
func entryVisible(f model.Filter, compProviders map[uint16]string, e logfmt.Entry) bool {
	if !f.MatchEntry(e) {
		return false
	}
	if len(f.Providers) == 0 {
		return true
	}
	return f.Providers[compProviders[e.Comp]]
}

// renderRawLog renders entries from TopEntry() downward, honouring the
// active facet filter, at most w runes wide and h lines tall. Each visible
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
			lines = append(lines, clipWidth(ln, w))
		}
		if len(lines) >= h {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// handleSearchKey routes one keystroke while a search query is being typed:
// runes and space extend the query, backspace removes its last rune, enter
// submits it and jumps to the first match, esc cancels without searching.
func (m Model) handleSearchKey(msg tea.KeyMsg) Model {
	switch msg.Type {
	case tea.KeyEnter:
		m.raw.searching = false
		if m.raw.query != "" {
			m.raw.lastQuery = m.raw.query
			m.searchFrom(m.raw.top, true)
		}
	case tea.KeyEsc:
		m.raw.searching = false
		m.raw.query = ""
	case tea.KeyBackspace:
		if r := []rune(m.raw.query); len(r) > 0 {
			m.raw.query = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		m.raw.query += " "
	case tea.KeyRunes:
		m.raw.query += string(msg.Runes)
	}
	return m
}

// searchAgain repeats the last submitted search, forward for n or backward
// for N. It is a no-op until a search has been run at least once.
func (m *Model) searchAgain(direction int) {
	if m.raw.lastQuery == "" {
		return
	}
	m.searchFrom(m.raw.top, direction > 0)
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
func (m *Model) searchFrom(start int, forward bool) bool {
	if m.raw.lastQuery == "" {
		return false
	}
	f := m.filter()
	compProviders := componentProviders(m.log.RPCSpans, m.log.Entries)
	needle := []byte(m.raw.lastQuery)

	step := 1
	if !forward {
		step = -1
	}
	for i := start + step; i >= 0 && i < len(m.log.Entries); i += step {
		e := m.log.Entries[i]
		if !entryVisible(f, compProviders, e) {
			continue
		}
		if bytes.Contains(m.log.Bytes(e), needle) {
			m.raw.top = i
			return true
		}
	}
	return false
}
