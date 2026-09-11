package model

import "sort"

// SourceLocation identifies an entry's exact half-open byte range and its
// inclusive one-based physical line range in the original log.
type SourceLocation struct {
	Entry              uint32
	StartByte, EndByte uint64
	StartLine, EndLine uint64
}

// SourcePosition identifies an entry and a zero-based physical-line offset
// within that entry.
type SourcePosition struct {
	Entry     uint32
	EntryLine uint64
}

func (l *Log) indexSourceLines() []uint64 {
	l.sourceLinesOnce.Do(func() {
		if len(l.Data) == 0 {
			return
		}
		l.sourceLineStarts = append(l.sourceLineStarts, 0)
		for i, b := range l.Data {
			if b == '\n' && i+1 < len(l.Data) {
				l.sourceLineStarts = append(l.sourceLineStarts, uint64(i+1))
			}
		}
	})
	return l.sourceLineStarts
}

// PhysicalLineCount returns the number of physical lines in the original log.
func (l *Log) PhysicalLineCount() uint64 {
	return uint64(len(l.indexSourceLines()))
}

// SourcePosition resolves a one-based physical line to its original entry.
func (l *Log) SourcePosition(line uint64) (SourcePosition, bool) {
	starts := l.indexSourceLines()
	if line == 0 || line > uint64(len(starts)) {
		return SourcePosition{}, false
	}
	offset := starts[line-1]
	i := sort.Search(len(l.Entries), func(i int) bool {
		return l.Entries[i].Off > offset
	}) - 1
	if i < 0 || uint64(i) > uint64(^uint32(0)) {
		return SourcePosition{}, false
	}
	e := l.Entries[i]
	end := e.Off + uint64(e.Len)
	if e.Len == 0 || end < e.Off || offset < e.Off || offset >= end || end > uint64(len(l.Data)) {
		return SourcePosition{}, false
	}
	location, ok := l.SourceLocation(uint32(i))
	if !ok || line < location.StartLine {
		return SourcePosition{}, false
	}
	return SourcePosition{Entry: uint32(i), EntryLine: line - location.StartLine}, true
}

// SourceLocation returns the original source range for an entry ordinal.
func (l *Log) SourceLocation(entry uint32) (SourceLocation, bool) {
	if uint64(entry) >= uint64(len(l.Entries)) {
		return SourceLocation{}, false
	}
	e := l.Entries[entry]
	end := e.Off + uint64(e.Len)
	if e.Len == 0 || end < e.Off || end > uint64(len(l.Data)) {
		return SourceLocation{}, false
	}

	starts := l.indexSourceLines()
	startLine := sort.Search(len(starts), func(i int) bool { return starts[i] > e.Off })
	endLine := sort.Search(len(starts), func(i int) bool { return starts[i] > end-1 })
	return SourceLocation{
		Entry:     entry,
		StartByte: e.Off,
		EndByte:   end,
		StartLine: uint64(startLine),
		EndLine:   uint64(endLine),
	}, true
}
