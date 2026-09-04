package model

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", name)
}

// Load must retain one Entry per logical entry Scan counted -- the index is
// what phase 3's raw-log view pages through, and a count that disagrees with
// Stats means entries were dropped or double-counted.
func TestLoadRetainsOneEntryPerLogicalEntry(t *testing.T) {
	l, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if uint64(len(l.Entries)) != l.Stats.Entries {
		t.Errorf("len(Entries) = %d, Stats.Entries = %d", len(l.Entries), l.Stats.Entries)
	}
	if len(l.Entries) == 0 {
		t.Fatal("no entries retained")
	}
}

// Bytes must return every line of a multi-line entry, not just its header.
// Entry.Off/Len cover all of an entry's physical lines, which is what makes
// "jump from a span to its log lines" a slice expression.
func TestBytesCoversAllLinesOfAnEntry(t *testing.T) {
	l, err := Load(fixture(t, "multiline-body.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var multi logfmt.Entry
	for _, e := range l.Entries {
		if e.Lines > 1 {
			multi = e
			break
		}
	}
	if multi.Lines <= 1 {
		t.Fatal("fixture has no multi-line entry")
	}
	got := string(l.Bytes(multi))
	if n := strings.Count(got, "\n"); n < int(multi.Lines)-1 {
		t.Errorf("Bytes returned %d newlines for a %d-line entry:\n%s", n, multi.Lines, got)
	}
}

func TestLoadBuildsBothSpanKinds(t *testing.T) {
	rpc, err := Load(fixture(t, "provider-rpc.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rpc.RPCSpans) == 0 {
		t.Error("no RPC spans built from provider-rpc.log")
	}
	ui, err := Load(fixture(t, "structured-ui.log"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ui.UISpans) == 0 {
		t.Error("no UI-hook spans built from structured-ui.log")
	}
}

func TestLoadNamesTheFileOnError(t *testing.T) {
	_, err := Load("no-such-file.log")
	if err == nil {
		t.Fatal("Load returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
}
