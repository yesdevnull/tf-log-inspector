package model

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestProviderResponsePreservesSourceEntries(t *testing.T) {
	const head = "2026-01-01T00:00:00.000Z [DEBUG] provider.example: "
	const source = head + `{"value":"fir` + "\n" + "ordinary text\n" + head + `st"}` + "\n"
	path := filepath.Join(t.TempDir(), "synthetic.log")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := append(l.Entries[:0:0], l.Entries...)
	var wg sync.WaitGroup
	for _, e := range l.Entries {
		wg.Go(func() {
			response, err := l.ProviderResponse(e)
			if err != nil || response.Text != `{"value":"firordinary textst"}` || len(response.Fragments) != 3 {
				t.Errorf("response = %+v, error = %v", response, err)
			}
			if !strings.Contains(source, string(l.Bytes(e))) {
				t.Error("source bytes changed")
			}
		})
	}
	wg.Wait()
	if string(l.Data) != source || !reflect.DeepEqual(entries, l.Entries) {
		t.Fatal("source changed")
	}
}

func TestProviderResponseMalformedDoesNotBlockLoad(t *testing.T) {
	const source = "2026-01-01T00:00:00.000Z [DEBUG] provider.example: {\"secret\":\n"
	path := filepath.Join(t.TempDir(), "synthetic.log")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	response, err := l.ProviderResponse(l.Entries[0])
	if err == nil || strings.Contains(err.Error(), "secret") || response.Text != "" {
		t.Fatalf("response = %+v, error = %v", response, err)
	}
	if string(l.Bytes(l.Entries[0])) != source {
		t.Fatal("raw entry unavailable")
	}
}
