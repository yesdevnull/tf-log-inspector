package model

import "sort"

// SourceLocation identifies an entry's exact half-open byte range and its
// inclusive one-based physical line range in the original log.
type SourceLocation struct {
	Entry              uint32
	StartByte, EndByte uint64
	StartLine, EndLine uint64
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
	starts := l.sourceLineStarts
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
