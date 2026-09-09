package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestRunScrubWritesReloadableCopyAndPreservesSource(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	source := []byte("2026-09-08T00:00:00.000Z [TRACE] provider.aws: Sending request downstream: name=customer_prod tf_req_id=12345678-1234-4234-8234-123456789abc\n")
	if err := os.WriteFile(inputPath, source, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if err := run([]string{"--scrub", "-o", outputPath, inputPath}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	gotSource, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotSource) != string(source) {
		t.Errorf("source changed: %q", gotSource)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "customer_prod") {
		t.Errorf("output retained identifying value: %q", output)
	}
	if _, err := model.Load(outputPath); err != nil {
		t.Fatalf("reload scrubbed log: %v", err)
	}
	for _, want := range []string{"name: 1", "Review the scrubbed log before sharing it."} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q: %q", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "customer_prod") {
		t.Errorf("stderr disclosed identifying value: %q", stderr.String())
	}
}

func TestRunScrubReportsHostnameNetworkCounts(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	if err := os.WriteFile(inputPath, []byte("hostname=private-host\nhostname=private.internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if err := run([]string{"--scrub", "-o", outputPath, inputPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "private") || !strings.Contains(stderr.String(), "network: 2") || strings.Contains(stderr.String(), "private") || stdout.Len() != 0 {
		t.Fatalf("hostname output/counts: output=%q stdout=%q stderr=%q", output, stdout.String(), stderr.String())
	}
}

func TestRunScrubValidatesModeAndOutputFlags(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "source.log")
	if err := os.WriteFile(inputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args []string
		want []string
	}{
		"diagnose combination": {
			args: []string{"--scrub", "--diagnose", "-o", filepath.Join(t.TempDir(), "out.log"), inputPath},
			want: []string{"--scrub", "--diagnose"},
		},
		"profile combination": {
			args: []string{"--scrub", "--profile", "-o", filepath.Join(t.TempDir(), "out.log"), inputPath},
			want: []string{"--scrub", "--profile"},
		},
		"missing output": {
			args: []string{"--scrub", inputPath},
			want: []string{"-o"},
		},
		"values outside scrub": {
			args: []string{"--diagnose", "--scrub-values", filepath.Join(t.TempDir(), "values.txt"), inputPath},
			want: []string{"--scrub-values", "--scrub"},
		},
		"explicitly empty values outside scrub": {
			args: []string{"--diagnose", "--scrub-values=", inputPath},
			want: []string{"--scrub-values", "--scrub"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := run(tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("invalid flags succeeded")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("invalid invocation wrote output: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunScrubUsesLiteralValuesFile(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	valuesPath := filepath.Join(dir, "values.txt")
	input := []byte("prefix  private phrase  suffix\n")
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(valuesPath, []byte("\r\n private phrase \r\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr strings.Builder
	if err := run([]string{"--scrub", "--scrub-values", valuesPath, "-o", outputPath, inputPath}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), " private phrase ") {
		t.Errorf("explicit value remains: %q", output)
	}
	if !strings.Contains(stderr.String(), "explicit: 1") {
		t.Errorf("stderr missing explicit count: %q", stderr.String())
	}
}

func TestRunScrubRejectsInvalidLiteralValuesWithoutDisclosure(t *testing.T) {
	for name, value := range map[string][]byte{
		"control":       []byte("private\tvalue\n"),
		"invalid UTF-8": {0xff, '\n'},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			inputPath := filepath.Join(dir, "source.log")
			outputPath := filepath.Join(dir, "sanitised.log")
			valuesPath := filepath.Join(dir, "values.txt")
			if err := os.WriteFile(inputPath, []byte("private value\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(valuesPath, value, 0o600); err != nil {
				t.Fatal(err)
			}

			err := run([]string{"--scrub", "--scrub-values", valuesPath, "-o", outputPath, inputPath}, io.Discard, io.Discard)
			if err == nil {
				t.Fatal("invalid values file succeeded")
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), string(value)) {
				t.Errorf("error disclosed values-file content: %q", err)
			}
			if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
				t.Errorf("output exists after values-file failure: %v", statErr)
			}
		})
	}
}

func TestRunScrubReportsInvalidUTF8ValuesLine(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	valuesPath := filepath.Join(dir, "values.txt")
	if err := os.WriteFile(inputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(valuesPath, []byte("valid\n\xff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"--scrub", "--scrub-values", valuesPath, "-o", outputPath, inputPath}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("run error = %v, want invalid value at line 2", err)
	}
}

func TestRunScrubRefusesExistingOutputAliasesWithoutChangingThem(t *testing.T) {
	for _, alias := range []string{"same path", "existing file", "hard link", "symbolic link"} {
		t.Run(alias, func(t *testing.T) {
			dir := t.TempDir()
			inputPath := filepath.Join(dir, "source.log")
			outputPath := filepath.Join(dir, "sanitised.log")
			source := []byte("name=private-name\n")
			if err := os.WriteFile(inputPath, source, 0o600); err != nil {
				t.Fatal(err)
			}
			existing := []byte("keep this output\n")
			switch alias {
			case "same path":
				outputPath = inputPath
				existing = source
			case "existing file":
				if err := os.WriteFile(outputPath, existing, 0o600); err != nil {
					t.Fatal(err)
				}
			case "hard link":
				if err := os.Link(inputPath, outputPath); err != nil {
					t.Fatal(err)
				}
				existing = source
			case "symbolic link":
				if err := os.Symlink(inputPath, outputPath); err != nil {
					t.Fatal(err)
				}
				existing = source
			}

			var stdout, stderr strings.Builder
			err := run([]string{"--scrub", "-o", outputPath, inputPath}, &stdout, &stderr)
			if err == nil {
				t.Fatal("existing output was overwritten")
			}
			got, readErr := os.ReadFile(outputPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != string(existing) {
				t.Errorf("existing output changed: %q", got)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("failure wrote diagnostics through run: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunScrubCreatesPrivateOutput(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	if err := os.WriteFile(inputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--scrub", "-o", outputPath, inputPath}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("output mode = %04o, want 0600", got)
	}
}

func TestRunScrubDoesNotCreateOutputUntilInputsAndTransformationAreValid(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, dir string) (string, string){
		"missing input": func(t *testing.T, dir string) (string, string) {
			return filepath.Join(dir, "missing.log"), ""
		},
		"missing values": func(t *testing.T, dir string) (string, string) {
			input := filepath.Join(dir, "source.log")
			if err := os.WriteFile(input, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return input, filepath.Join(dir, "missing-values.txt")
		},
		"invalid input": func(t *testing.T, dir string) (string, string) {
			input := filepath.Join(dir, "source.log")
			if err := os.WriteFile(input, []byte{0xff}, 0o600); err != nil {
				t.Fatal(err)
			}
			return input, ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			inputPath, valuesPath := setup(t, dir)
			outputPath := filepath.Join(dir, "sanitised.log")
			args := []string{"--scrub", "-o", outputPath}
			if valuesPath != "" {
				args = append(args, "--scrub-values", valuesPath)
			}
			args = append(args, inputPath)
			if err := run(args, io.Discard, io.Discard); err == nil {
				t.Fatal("invalid input succeeded")
			}
			if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
				t.Errorf("output exists after failure: %v", err)
			}
		})
	}
}

func TestRunScrubReportsInvalidOutputDirectory(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "missing", "sanitised.log")
	if err := os.WriteFile(inputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	err := run([]string{"--scrub", "-o", outputPath, inputPath}, io.Discard, &stderr)
	if err == nil || !strings.Contains(err.Error(), outputPath) {
		t.Fatalf("run error = %v, want output path", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("failure printed success diagnostics: %q", stderr.String())
	}
	if _, statErr := os.Lstat(outputPath); !os.IsNotExist(statErr) {
		t.Errorf("output exists after create failure: %v", statErr)
	}
}

func TestRunScrubRejectsFragmentedHTTPRequestBeforeCreatingOutput(t *testing.T) {
	dir := t.TempDir()
	inputPath, outputPath := filepath.Join(dir, "source.log"), filepath.Join(dir, "out.log")
	const input = "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n6\r\n{\"pass\r\n1b\r\nword\":\"private-credential\"}\r\n0\r\n\r\n"
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	err := run([]string{"--scrub", "-o", outputPath, inputPath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "fragmented JSON") || strings.Contains(err.Error(), "private-credential") {
		t.Fatalf("fragmented request accepted or disclosed content: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("rejected request printed success output: %q %q", stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("output exists after rejecting fragmented request: %v", err)
	}
}

func TestRunScrubRejectsParserWindowChangesBeforeCreatingOutput(t *testing.T) {
	prefix := "Received downstream response: name=a padding="
	for name, message := range map[string]string{
		"shrinking header": "Received downstream response: password=\"" + strings.Repeat("private", 10000) + "\" tf_req_id=abc tf_req_duration_ms=5\n",
		"expanding header": prefix + strings.Repeat("x", 65536-len(prefix)-len(" tf_req_id=abc")) + " tf_req_id=abc tf_req_duration_ms=5\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			inputPath := filepath.Join(dir, "source.log")
			outputPath := filepath.Join(dir, "sanitised.log")
			source := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: " + message
			if err := os.WriteFile(inputPath, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr strings.Builder
			err := run([]string{"--scrub", "-o", outputPath, inputPath}, &stdout, &stderr)
			if err == nil {
				t.Fatal("accepted changed parser-visible metadata")
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "xxx") || strings.Contains(err.Error(), "abc") {
				t.Errorf("error disclosed log content: %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("failure printed success output: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if _, statErr := os.Lstat(outputPath); !os.IsNotExist(statErr) {
				t.Errorf("output exists after metadata rejection: %v", statErr)
			}
		})
	}
}

func TestRunScrubReadsInputBeforeRefusingExistingOutput(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "missing.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	if err := os.WriteFile(outputPath, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"--scrub", "-o", outputPath, inputPath}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), inputPath) {
		t.Fatalf("run error = %v, want missing input path", err)
	}
	output, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(output) != "keep\n" {
		t.Errorf("existing output changed: %q", output)
	}
}

func TestRunScrubReportsCountsInStableOrder(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	if err := os.WriteFile(inputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if err := run([]string{"--scrub", "-o", outputPath, inputPath}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	want := "Replacement counts:\n" +
		"  name: 0\n" +
		"  id: 0\n" +
		"  guid: 0\n" +
		"  email: 0\n" +
		"  network: 0\n" +
		"  cloud: 0\n" +
		"  path: 0\n" +
		"  secret: 0\n" +
		"  explicit: 0\n" +
		"Unsupported structured or quoted inputs: 0\n" +
		"Review the scrubbed log before sharing it.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRunScrubReportsUnsupportedLocationsWithoutContent(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	source := []byte("token=\"private-unterminated\n")
	if err := os.WriteFile(inputPath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if err := run([]string{"--scrub", "-o", outputPath, inputPath}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Unsupported structured or quoted inputs: 1") {
		t.Errorf("stderr missing unsupported count: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "private-unterminated") {
		t.Errorf("stderr disclosed input: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Locations in the original input (showing first 1; 1-based character columns):\n  line 1, column 7: invalid quoted string\n") {
		t.Errorf("stderr missing source location: %q", stderr.String())
	}
}

func TestRunScrubCapsUnsupportedLocations(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	if err := os.WriteFile(inputPath, []byte(strings.Repeat("{private-broken}\n", 12)), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if err := run([]string{"--scrub", "-o", filepath.Join(dir, "out.log"), inputPath}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Unsupported structured or quoted inputs: 12", "showing first 10", "line 10, column 1: invalid JSON", "2 additional instances omitted."} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("missing %q in %q", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "private-broken") || strings.Contains(stderr.String(), "line 11,") {
		t.Fatalf("unexpected diagnostic content: %q", stderr.String())
	}
}

func TestScrubUsageShowsOutputAndLiteralValues(t *testing.T) {
	var stderr strings.Builder
	if err := run([]string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	want := "tfli --scrub [--scrub-values values.txt] -o sanitised.log <logfile>"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("usage missing %q:\n%s", want, stderr.String())
	}
}

func TestRunScrubRemovesPartialOutputAfterWriteOrCloseFailure(t *testing.T) {
	original := openScrubOutput
	t.Cleanup(func() { openScrubOutput = original })

	for name, failure := range map[string]struct {
		shortWrite bool
		writeErr   error
		closeErr   error
		want       string
	}{
		"short write": {shortWrite: true, want: "short write"},
		"write error": {writeErr: errors.New("synthetic write failure"), want: "synthetic write failure"},
		"close error": {closeErr: errors.New("synthetic close failure"), want: "synthetic close failure"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			inputPath := filepath.Join(dir, "source.log")
			outputPath := filepath.Join(dir, "sanitised.log")
			source := []byte("name=private-name\n")
			if err := os.WriteFile(inputPath, source, 0o600); err != nil {
				t.Fatal(err)
			}
			openScrubOutput = func(path string) (io.WriteCloser, error) {
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					return nil, err
				}
				return &failingScrubFile{File: file, shortWrite: failure.shortWrite, writeErr: failure.writeErr, closeErr: failure.closeErr}, nil
			}

			var stderr strings.Builder
			err := run([]string{"--scrub", "-o", outputPath, inputPath}, io.Discard, &stderr)
			if err == nil || !strings.Contains(err.Error(), failure.want) {
				t.Fatalf("run error = %v, want %q", err, failure.want)
			}
			if _, statErr := os.Lstat(outputPath); !os.IsNotExist(statErr) {
				t.Errorf("partial output remains: %v", statErr)
			}
			gotSource, readErr := os.ReadFile(inputPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(gotSource) != string(source) {
				t.Errorf("source changed after output failure: %q", gotSource)
			}
			if stderr.Len() != 0 {
				t.Errorf("failure printed success diagnostics: %q", stderr.String())
			}
		})
	}
}

func TestRunScrubReportsPartialOutputCleanupFailure(t *testing.T) {
	original := openScrubOutput
	t.Cleanup(func() { openScrubOutput = original })

	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.log")
	outputPath := filepath.Join(dir, "sanitised.log")
	if err := os.WriteFile(inputPath, []byte("name=private-name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	openScrubOutput = func(path string) (io.WriteCloser, error) {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		return &failingScrubFile{
			File:       file,
			shortWrite: true,
			afterWrite: func() error { return os.Chmod(dir, 0o500) },
		}, nil
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore directory mode: %v", err)
		}
	})

	err := run([]string{"--scrub", "-o", outputPath, inputPath}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "removing partial output") {
		t.Fatalf("run error = %v, want cleanup failure", err)
	}
	if strings.Contains(err.Error(), "private-name") {
		t.Errorf("cleanup error disclosed log content: %q", err)
	}
	if _, statErr := os.Lstat(outputPath); statErr != nil {
		t.Errorf("partial output missing despite removal failure: %v", statErr)
	}
}

type failingScrubFile struct {
	*os.File
	shortWrite bool
	writeErr   error
	closeErr   error
	afterWrite func() error
}

func (f *failingScrubFile) Write(p []byte) (int, error) {
	limit := len(p)
	if (f.shortWrite || f.writeErr != nil) && limit > 0 {
		limit /= 2
	}
	n, err := f.File.Write(p[:limit])
	if err == nil && f.afterWrite != nil {
		err = f.afterWrite()
	}
	if err != nil {
		return n, err
	}
	if f.writeErr != nil {
		return n, f.writeErr
	}
	return n, nil
}

func (f *failingScrubFile) Close() error {
	err := f.File.Close()
	if err != nil {
		return err
	}
	return f.closeErr
}
