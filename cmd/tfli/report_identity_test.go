package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportProtectsLoadedInputAfterPathReplacement(t *testing.T) {
	for _, inputCount := range []int{1, 2} {
		for replaced := 0; replaced < inputCount; replaced++ {
			t.Run(string(rune('0'+inputCount))+" inputs/replaced "+string(rune('0'+replaced)), func(t *testing.T) {
				dir := t.TempDir()
				paths := []string{filepath.Join(dir, "before.log"), filepath.Join(dir, "after.log")}[:inputCount]
				original := []byte("2026-09-11T00:00:00.000Z [INFO] original capture\n")
				var inputs []*os.File
				for _, path := range paths {
					if err := os.WriteFile(path, original, 0o600); err != nil {
						t.Fatal(err)
					}
					f, log, err := loadReportInput(path)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = f.Close() }()
					inputs = append(inputs, f)
					if !bytes.Equal(log.Data, original) {
						t.Fatal("loaded wrong capture")
					}
				}
				output := filepath.Join(dir, "report.txt")
				if err := os.Rename(paths[replaced], output); err != nil {
					t.Fatal(err)
				}
				replacement := []byte("replacement capture\n")
				if err := os.WriteFile(paths[replaced], replacement, 0o600); err != nil {
					t.Fatal(err)
				}
				err := writeReport(io.Discard, inputs, output, func(w io.Writer) error {
					_, err := io.WriteString(w, "report overwrote capture\n")
					return err
				})
				got, readErr := os.ReadFile(output)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(got, original) {
					t.Errorf("loaded input changed: %q", got)
				}
				if err == nil || !strings.Contains(err.Error(), "same file") {
					t.Errorf("alias error = %v", err)
				}
				got, readErr = os.ReadFile(paths[replaced])
				if readErr != nil || !bytes.Equal(got, replacement) {
					t.Fatalf("replacement changed: %q, %v", got, readErr)
				}
			})
		}
	}
}
