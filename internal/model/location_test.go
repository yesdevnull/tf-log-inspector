package model

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

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
