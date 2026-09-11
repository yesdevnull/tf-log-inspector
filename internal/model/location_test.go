package model

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func TestPhysicalLineCountAndSourcePositionUseLoadedBytes(t *testing.T) {
	const crlfSource = "2026-09-10T00:00:00.000Z [INFO] first\r\n\r\ncontinuation\r\n2026-09-10T00:00:01.000Z [INFO] last"
	tests := []struct {
		name   string
		source string
	}{
		{name: "CRLF", source: crlfSource},
		{name: "LF", source: strings.ReplaceAll(crlfSource, "\r\n", "\n")},
	}
	wants := []struct {
		line      uint64
		wantEntry uint32
		wantLine  uint64
	}{
		{line: 1, wantEntry: 0, wantLine: 0},
		{line: 2, wantEntry: 0, wantLine: 1},
		{line: 3, wantEntry: 0, wantLine: 2},
		{line: 4, wantEntry: 1, wantLine: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.log")
			if err := os.WriteFile(path, []byte(tc.source), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			l, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if got := l.PhysicalLineCount(); got != 4 {
				t.Fatalf("PhysicalLineCount() = %d, want 4", got)
			}
			for _, want := range wants {
				got, ok := l.SourcePosition(want.line)
				wantPosition := SourcePosition{Entry: want.wantEntry, EntryLine: want.wantLine}
				if !ok || got != wantPosition {
					t.Errorf("SourcePosition(%d) = %+v, %v; want %+v, true", want.line, got, ok, wantPosition)
					continue
				}
				location, ok := l.SourceLocation(got.Entry)
				if !ok || location.StartLine+got.EntryLine != want.line {
					t.Errorf("SourceLocation(%d) = %+v, %v; position resolves to line %d", got.Entry, location, ok, want.line)
				}
			}
			for _, line := range []uint64{0, 5, math.MaxUint64} {
				if got, ok := l.SourcePosition(line); ok || got != (SourcePosition{}) {
					t.Errorf("SourcePosition(%d) = %+v, %v; want zero, false", line, got, ok)
				}
			}
		})
	}
}

func TestSourcePositionRejectsMalformedEntryRanges(t *testing.T) {
	tests := []struct {
		name    string
		log     Log
		line    uint64
		wantLen uint64
	}{
		{name: "empty log", log: Log{}, line: 1, wantLen: 0},
		{name: "gap", log: Log{Data: []byte("one\ntwo\n"), Entries: []logfmt.Entry{{Off: 0, Len: 4}, {Off: 5, Len: 3}}}, line: 2, wantLen: 2},
		{name: "zero length", log: Log{Data: []byte("one\n"), Entries: []logfmt.Entry{{Off: 0, Len: 0}}}, line: 1, wantLen: 1},
		{name: "overflow", log: Log{Data: []byte("one\n"), Entries: []logfmt.Entry{{Off: math.MaxUint64, Len: 2}}}, line: 1, wantLen: 1},
		{name: "past data", log: Log{Data: []byte("one\n"), Entries: []logfmt.Entry{{Off: 0, Len: 5}}}, line: 1, wantLen: 1},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.log.PhysicalLineCount(); got != tc.wantLen {
				t.Fatalf("PhysicalLineCount() = %d, want %d", got, tc.wantLen)
			}
			if got, ok := tc.log.SourcePosition(tc.line); ok || got != (SourcePosition{}) {
				t.Errorf("SourcePosition(%d) = %+v, %v; want zero, false", tc.line, got, ok)
			}
		})
	}
}

func TestSourceLocationUsesOriginalByteOffsetsBeyondEntryLineSaturation(t *testing.T) {
	var source strings.Builder
	source.WriteString("2026-09-10T00:00:00.000Z [INFO] first\n")
	for range 65_536 {
		source.WriteString("continuation\n")
	}
	source.WriteString("2026-09-10T00:00:01.000Z [INFO] second\n")

	path := filepath.Join(t.TempDir(), "long.log")
	if err := os.WriteFile(path, []byte(source.String()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(l.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(l.Entries))
	}

	first, ok := l.SourceLocation(0)
	if !ok || first.StartLine != 1 || first.EndLine != 65_537 {
		t.Fatalf("first location=%+v, ok=%v", first, ok)
	}
	second, ok := l.SourceLocation(1)
	if !ok || second.StartLine != 65_538 || second.EndLine != 65_538 {
		t.Fatalf("second location=%+v, ok=%v", second, ok)
	}

	for _, tc := range []struct {
		line uint64
		want SourcePosition
	}{
		{line: 65_537, want: SourcePosition{Entry: 0, EntryLine: 65_536}},
		{line: 65_538, want: SourcePosition{Entry: 1, EntryLine: 0}},
	} {
		if got, ok := l.SourcePosition(tc.line); !ok || got != tc.want {
			t.Errorf("SourcePosition(%d) = %+v, %v; want %+v, true", tc.line, got, ok, tc.want)
		}
	}
}

func TestSourceLocationReportsPhysicalLinesAndHalfOpenBytes(t *testing.T) {
	const source = "é\r\nnext"
	l := &Log{
		Data: []byte(source),
		Entries: []logfmt.Entry{
			{Off: 0, Len: 4},
			{Off: 4, Len: 4},
		},
	}

	first, ok := l.SourceLocation(0)
	if !ok || first != (SourceLocation{Entry: 0, StartByte: 0, EndByte: 4, StartLine: 1, EndLine: 1}) {
		t.Errorf("first location=%+v, ok=%v", first, ok)
	}
	second, ok := l.SourceLocation(1)
	if !ok || second != (SourceLocation{Entry: 1, StartByte: 4, EndByte: 8, StartLine: 2, EndLine: 2}) {
		t.Errorf("second location=%+v, ok=%v", second, ok)
	}
}

func TestSourceLocationRejectsInvalidEntries(t *testing.T) {
	l := &Log{
		Data: []byte("line\n"),
		Entries: []logfmt.Entry{
			{Off: 0, Len: 0},
			{Off: 4, Len: 2},
		},
	}
	for _, entry := range []uint32{0, 1, 2} {
		if got, ok := l.SourceLocation(entry); ok {
			t.Errorf("SourceLocation(%d) = %+v, true; want false", entry, got)
		}
	}
}

func TestSourceLocationInitialisesSafelyAcrossConcurrentReads(t *testing.T) {
	l := &Log{Data: []byte("one\ntwo\n"), Entries: []logfmt.Entry{{Off: 0, Len: 4}, {Off: 4, Len: 4}}}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(entry uint32) {
			defer wg.Done()
			got, ok := l.SourceLocation(entry)
			if !ok || got.StartLine != uint64(entry+1) {
				t.Errorf("SourceLocation(%d) = %+v, %v", entry, got, ok)
			}
		}(uint32(i % 2))
	}
	wg.Wait()
}
