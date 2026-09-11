package model

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileReadsOpenedCaptureAndRetainsOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.log")
	original := []byte("2026-09-11T00:00:00.000Z [INFO] original capture\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := LoadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l.Data, original) || len(l.Entries) != 1 {
		t.Fatalf("loaded replacement instead of opened capture: %q", l.Data)
	}
	if _, err := f.Stat(); err != nil {
		t.Fatalf("loader closed caller's file: %v", err)
	}
}

func TestLoadFileReadFailureRetainsContextAndOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	l, err := LoadFile(f)
	var pathErr *os.PathError
	if l != nil || err == nil || !strings.HasPrefix(err.Error(), "reading "+path+": ") || !errors.As(err, &pathErr) || pathErr.Op != "read" || pathErr.Path != path {
		t.Fatalf("load read failure: log=%v error=%v", l, err)
	}
	if _, err := f.Stat(); err != nil {
		t.Fatalf("loader closed caller's file after error: %v", err)
	}
}
