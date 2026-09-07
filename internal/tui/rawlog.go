package tui

import (
	"slices"
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

// jumpContextLines is how many lines of what came BEFORE a call are left
// above it when Enter opens one.
//
// A few rather than a paneful: the call's own line is what the reader asked
// for and it has to be somewhere obvious, which the top of the pane is and
// the middle is not. The pane's height is not known here -- Model's height
// is the terminal's, and pane heights are worked out while composing a frame
// -- so this is a small fixed number rather than a fraction of a height a
// key handler cannot see.
const jumpContextLines = 3

// rawLogState holds the raw log view's own state: which entry sits at the
// top of the pane, and any free-text search in progress or most recently
// run. It is kept as its own struct, rather than loose fields on Model, so
// the raw log's concerns stay grouped with the file that owns them.
type rawLogState struct {
	// top is the entry at the top of the pane and topLine how many of that
	// entry's own lines are scrolled off above it. Together they are a LINE
	// position, not an entry one.
	//
	// The pair is what makes a tall entry readable. Scrolling an entry at a
	// time, a provider's body dump showed its first paneful and no more --
	// the next press moved to the next ENTRY, taking the rest with it -- and
	// on a capture whose largest entry is half the log, that is half the log
	// unreachable. It is also what lets the pane FILL: an entry too tall to
	// fit whole is begun rather than dropped, where before a one-line entry
	// above a forty-line one drew one line into a twelve-line pane.
	//
	// topLine is an offset into the lines of the entry at top, so it is
	// meaningful only alongside it; every move sets the two together.
	top     int
	topLine int

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

	// scope, when non-nil, is the ascending entry indices of ONE call's
	// lines (model.Log.ScopeFor). Render, scroll and search walk it instead
	// of the whole log, so the pane shows the call rather than the log
	// around it.
	//
	// nil is "no scope", which is not the same as an empty one: a scope is
	// built from a span's own id and always holds at least that span's
	// entry, so an empty scoped pane is the facet filter's doing and says so
	// (see renderRawLog).
	scope []int
}

// TopEntry reports the index into m.log.Entries currently at the top of the
// raw log pane, and TopLine how many of that entry's lines are scrolled off
// above it.
func (m Model) TopEntry() int {
	return m.raw.top
}

func (m Model) TopLine() int {
	return m.raw.topLine
}

// entryLines splits one entry into the physical lines the pane draws for it.
//
// It is measured off the entry's BYTES rather than read from Entry.Lines,
// which saturates at a uint16 and so understates the tallest entries there
// are -- exactly the ones scrolling has to walk a line at a time. A count
// that disagreed with what renderRawLog draws would put the scroll position
// and the pane out of step.
func (m Model) entryLines(e logfmt.Entry) []string {
	return strings.Split(strings.TrimRight(string(m.log.Bytes(e)), "\n"), "\n")
}

// rawLogVisible reports which entries the active filter admits, built once
// for a walk rather than per entry: componentProviders is O(spans), and a
// scroll can step over many entries.
func (m Model) rawLogVisible() func(int) bool {
	f := m.filter()
	compProviders := componentProviders(m.log.RPCSpans, m.log.Entries)
	return func(i int) bool {
		return entryVisible(f, compProviders, m.log.Entries[i])
	}
}

// nextRawEntry returns the entry index at or after i that the pane may draw,
// and whether there is one: the next member of the scope when one is live,
// and i itself otherwise. prevRawEntry is its mirror.
//
// The pair is what lets render, scroll and search share one notion of "the
// entries this pane is showing", so a scope cannot apply to one of them and
// not the others -- which would leave the position pointing at an entry the
// pane skips.
func (m Model) nextRawEntry(i int) (int, bool) {
	if m.raw.scope == nil {
		if i < 0 {
			i = 0
		}
		return i, i < len(m.log.Entries)
	}
	p, _ := slices.BinarySearch(m.raw.scope, i)
	if p >= len(m.raw.scope) {
		return 0, false
	}
	return m.raw.scope[p], true
}

func (m Model) prevRawEntry(i int) (int, bool) {
	if m.raw.scope == nil {
		return i, i >= 0
	}
	p, found := slices.BinarySearch(m.raw.scope, i)
	if !found {
		p--
	}
	if p < 0 || p >= len(m.raw.scope) {
		return 0, false
	}
	return m.raw.scope[p], true
}

// jumpToSpan switches to the raw log view positioned at the entry that
// closed the span at idx in spans. idx is a rollup row's spanIdx, which is
// -1 for a row that represents many spans rather than one (every
// ViewProviders and ViewTypes row); jumpToSpan leaves m unchanged for those,
// since there is no single span to jump to.
//
// spans is the slice idx indexes into: the table views always jump from
// m.log.RPCSpans, but the timeline may be drawing the UI tier instead (see
// timelineSpans), so the caller hands over whichever slice its own index
// names rather than this function assuming one.
func (m *Model) jumpToSpan(spans []span.Span, idx int) {
	if idx < 0 || idx >= len(spans) {
		return
	}
	// Span.Entry indexes the same log's Entries, but nothing revalidates it
	// when a Log is assembled, so every reader of the field checks it --
	// componentProviders does the same -- rather than each deciding for
	// itself whether it can be trusted. An index outside the log leaves the
	// view where it is: there is no entry to jump to.
	entry := int(spans[idx].Entry)
	if entry >= len(m.log.Entries) {
		return
	}
	// The raw log renders from m.raw.top DOWNWARD through the active filter
	// (see renderRawLog), so jumping to an entry the filter hides shows
	// either nothing or whatever visible entry happens to come next -- a
	// different call's lines entirely -- and the pane looks exactly as it
	// would have had the jump worked. The filter is the user's own, so it is
	// left standing and the refusal is reported in the footer instead: Esc
	// clears it, and Enter then lands where it was asked to.
	if !entryVisible(m.filter(), componentProviders(m.log.RPCSpans, m.log.Entries), m.log.Entries[entry]) {
		m.blockedJump = true
		return
	}
	// Recorded AFTER setView, which spends any mark already standing: this
	// jump is the one Esc should undo, not whatever earlier jump the reader
	// has since navigated away from.
	from := m.view
	m.setView(ViewRawLog)
	m.returnTo, m.hasReturn = from, true
	// The scope is what makes this "open the call" rather than "park the
	// whole log on one of its lines". Its first member is the earliest entry
	// in the FILE carrying the id, which is where the call's own traffic
	// begins -- so jumpContextLines is NOT applied here: the scope already
	// supplies what led to the call, and backing up three lines would open
	// the pane on lines outside it.
	if scope := m.log.ScopeFor(spans[idx].ReqID); len(scope) > 0 {
		m.raw.scope = scope
		m.raw.top, m.raw.topLine = scope[0], 0
		return
	}
	m.raw.scope = nil
	m.raw.top, m.raw.topLine = entry, 0
	// Back up a few lines, so the call's own line arrives with what led to
	// it above rather than pinned to the top of the pane with none of it in
	// sight. Span.Entry is the entry that CLOSED the call, so the
	// provider's traffic for it is behind, not ahead.
	m.scrollRawLog(-jumpContextLines)
}

// pageRawLog moves the raw log by delta screenfuls of LINES.
func (m *Model) pageRawLog(delta int) {
	m.scrollRawLog(delta * rawLogPageSize)
}

// scrollRawLog moves the raw log by delta LINES -- forward for positive,
// back for negative -- clamped so neither paging nor an arrow key can walk
// off either end.
//
// Lines rather than entries, because entries are not a unit the reader can
// see: they run from one line to thousands, so an entry-sized step moves the
// pane by an amount that depends on what happens to be under it, and the
// inside of a tall entry cannot be reached at all.
//
// It steps through the FILTER, skipping entries the pane would not draw, so
// a press moves the pane by a line rather than by however many hidden
// entries happen to sit between two visible ones.
//
// It also drops any "pattern not found": the miss was reported about the
// position the search started from, and scrolling has moved it.
func (m *Model) scrollRawLog(delta int) {
	m.raw.notFound = false
	if len(m.log.Entries) == 0 {
		m.raw.top, m.raw.topLine = 0, 0
		return
	}
	visible := m.rawLogVisible()
	for ; delta > 0; delta-- {
		if !m.stepRawLogDown(visible) {
			break
		}
	}
	for ; delta < 0; delta++ {
		if !m.stepRawLogUp(visible) {
			break
		}
	}
}

// stepRawLogDown moves one line further into the log, reporting whether
// there was anywhere to go. The last line of the last visible entry is the
// end: stopping there keeps a line of content on screen, where running past
// it would leave the pane blank with the log still under it.
func (m *Model) stepRawLogDown(visible func(int) bool) bool {
	if visible(m.raw.top) && m.raw.topLine+1 < len(m.entryLines(m.log.Entries[m.raw.top])) {
		m.raw.topLine++
		return true
	}
	for i, ok := m.nextRawEntry(m.raw.top + 1); ok; i, ok = m.nextRawEntry(i + 1) {
		if visible(i) {
			m.raw.top, m.raw.topLine = i, 0
			return true
		}
	}
	return false
}

// stepRawLogUp moves one line back, onto the LAST line of the entry before
// it when it leaves the current one -- which is what makes a press up undo a
// press down rather than landing on that entry's head.
func (m *Model) stepRawLogUp(visible func(int) bool) bool {
	if m.raw.topLine > 0 {
		m.raw.topLine--
		return true
	}
	for i, ok := m.prevRawEntry(m.raw.top - 1); ok; i, ok = m.prevRawEntry(i - 1) {
		if visible(i) {
			m.raw.top, m.raw.topLine = i, len(m.entryLines(m.log.Entries[i]))-1
			return true
		}
	}
	return false
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
//
// The mapping assumes a component belongs to ONE provider, which is what
// Terraform's own naming gives: a component names the plugin process, so
// every span closing under it is that plugin's. Should two providers' spans
// ever close on entries sharing a component, the last span walked wins and
// the earlier provider's lines are attributed to the later one -- a filtered
// raw log showing some of the wrong provider's entries, not a wrong number
// anywhere.
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
// every line outside the exact RPC boundary because, say, every RPC but
// ReadDataSource has been unticked would hide precisely the context -- the
// provider's own surrounding output -- that jumping to a slow call exists
// to show.
//
// With every provider still ticked the dimension is nil -- "no opinion" --
// and every entry passes regardless of component. Once one is unticked, an
// entry whose component maps to no provider (Terraform's own core lines,
// plan output) is hidden: the filter asked for particular providers'
// traffic, and a core line is not that. Such an entry resolves to the empty
// provider, so it is normalised through model.FacetKey and matched against
// "(none)" -- the same key the facet pane offers for a span with no provider
// address -- rather than against a raw "" no checkbox can ever tick.
//
// The nil test is not a length test, for the reason model.Filter's own
// doc gives: an allow-list that is present but EMPTY is every provider
// unticked, and admits nothing. Reading that as "no opinion" would answer
// the reader's last untick by putting the whole log back on screen.
func entryVisible(f model.Filter, compProviders map[uint16]string, e logfmt.Entry) bool {
	if !f.MatchEntry(e) {
		return false
	}
	if f.Providers == nil {
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
// fragment; an entry that would not fit inside the remaining height ends
// the pane instead of being cut apart, and scrolling on brings it to the top
// of the pane, where it is begun from its own first line. The exception is
// the first entry to produce any lines -- the first VISIBLE one, since the
// filter skips the others without ever measuring them -- which is always
// begun. That is what makes an entry taller than the whole pane render its
// head rather than leaving the pane blank: it is the entry at m.raw.top, the
// one a jump or a search put there, and its lines are cut to h by the pane
// row that composes them (framePanes, at every width) rather than being
// allowed to push the caveat off the frame.
func (m Model) renderRawLog(w, h int) string {
	f := m.filter()
	compProviders := componentProviders(m.log.RPCSpans, m.log.Entries)

	var lines []string
	var scratch []byte
	for i, ok := m.nextRawEntry(m.TopEntry()); ok && len(lines) < h; i, ok = m.nextRawEntry(i + 1) {
		e := m.log.Entries[i]
		if !entryVisible(f, compProviders, e) {
			continue
		}
		entryLines := m.entryLines(e)
		// The top entry starts partway down where the reader has scrolled
		// into it. Only that one: every entry after it is drawn from its
		// own first line.
		if i == m.TopEntry() && m.TopLine() > 0 {
			if m.TopLine() >= len(entryLines) {
				continue
			}
			entryLines = entryLines[m.TopLine():]
		}
		// The level is the ENTRY's, so every line of a multi-line entry is
		// marked alike: a stack trace or a body dump under an ERROR header
		// belongs to that error, and marking only the header would leave the
		// rest reading as unrelated traffic.
		style, marked := semantic.forLevel(e.Level)
		for _, ln := range entryLines {
			// The budget is checked per LINE, so an entry taller than the
			// pane is BEGUN rather than dropped and the pane always fills.
			// Dropped whole, a forty-line entry below a one-line one left
			// eleven rows blank with the content that would have filled
			// them immediately beneath, and nothing saying why.
			if len(lines) >= h {
				break
			}
			var plain string
			plain, scratch = logfmt.StripANSI(ln, scratch)
			line := clipWidth(plain, w)
			if marked {
				line = style.Render(line)
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		// An empty pane is the same pane a parse failure or the wrong file
		// would produce, so it says which it is. The raw log's top entry is
		// clamped inside the log (scrollRawLog), so with no filter active
		// the only way to render nothing is a log with no entries at all.
		if m.raw.scope != nil {
			return styles.note.Render(clipWidth(scopedEmptyNote, w))
		}
		if m.filterActive() {
			return styles.note.Render(clipWidth(m.noMatchTail(), w))
		}
		return styles.note.Render(clipWidth(noEntriesNote, w))
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
// it -- the same trade this project makes wherever a simpler synchronous
// read is fast enough for the log sizes that actually exist.
//
// It scans from start in the given direction, honouring the same filter
// renderRawLog does, and moves the raw log's top entry to the first match.
// Whether start itself counts as a candidate is includeStart's to say; see
// below.
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
//
// A search and a scope are both "narrow what I am reading" gestures, and
// the scope is the narrower: it walks nextRawEntry/prevRawEntry rather than
// the whole log, so a live scope confines the search to it. Searching
// outside it would report a match the pane cannot show, leaving top on an
// entry renderRawLog skips.
func (m *Model) searchFrom(start int, forward, includeStart bool) bool {
	if m.raw.lastQuery == "" {
		return false
	}
	f := m.filter()
	compProviders := componentProviders(m.log.RPCSpans, m.log.Entries)

	if !includeStart {
		if forward {
			start++
		} else {
			start--
		}
	}
	var scratch []byte
	if forward {
		for i, ok := m.nextRawEntry(start); ok; i, ok = m.nextRawEntry(i + 1) {
			e := m.log.Entries[i]
			if !entryVisible(f, compProviders, e) {
				continue
			}
			var plain string
			plain, scratch = logfmt.StripANSI(string(m.log.Bytes(e)), scratch)
			if strings.Contains(plain, m.raw.lastQuery) {
				// The whole pair, since a position is a LINE: left at the
				// offset the reader had scrolled to, a match on a shorter
				// entry is skipped by renderRawLog altogether and one on a
				// taller entry opens above the matched text -- a search
				// reported as found over a pane that does not hold the
				// pattern.
				m.raw.top, m.raw.topLine = i, 0
				return true
			}
		}
		return false
	}
	for i, ok := m.prevRawEntry(start); ok; i, ok = m.prevRawEntry(i - 1) {
		e := m.log.Entries[i]
		if !entryVisible(f, compProviders, e) {
			continue
		}
		var plain string
		plain, scratch = logfmt.StripANSI(string(m.log.Bytes(e)), scratch)
		if strings.Contains(plain, m.raw.lastQuery) {
			// The whole pair, since a position is a LINE: left at the offset
			// the reader had scrolled to, a match on a shorter entry is
			// skipped by renderRawLog altogether and one on a taller entry
			// opens above the matched text -- a search reported as found
			// over a pane that does not hold the pattern.
			m.raw.top, m.raw.topLine = i, 0
			return true
		}
	}
	return false
}
