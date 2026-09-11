package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareRequiresExactlyTwoInputs(t *testing.T) {
	for _, paths := range [][]string{nil, {"before.log"}, {"a.log", "b.log", "c.log"}} {
		args := append([]string{"--compare"}, paths...)
		var stdout, stderr bytes.Buffer
		err := run(args, &stdout, &stderr)
		if err == nil || err.Error() != "expected exactly two log file arguments for --compare" {
			t.Fatalf("run(%v) error = %v", args, err)
		}
		if stdout.Len() != 0 || strings.Count(stderr.String(), "Usage: tfli") != 1 {
			t.Fatalf("run(%v): stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestCompareJSONRejectsExplicitLimitBeforeTouchingOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(output, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"--compare", "--format=json", "--limit=0", "-o", output, "missing-before.log", "missing-after.log"}, &stdout, &stderr)
	if err == nil || err.Error() != "--limit is not supported with --format json" {
		t.Fatalf("error: %v", err)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "keep\n" || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("validation touched output: data=%q readErr=%v stdout=%q stderr=%q", data, readErr, stdout.String(), stderr.String())
	}
}

func TestComparisonDefaultTextEqualsExplicitText(t *testing.T) {
	before := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	var implicit, explicit, repeated bytes.Buffer
	if err := run([]string{"--compare", before, before}, &implicit, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--compare", "--format=text", before, before}, &explicit, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--compare", "--format=json", "--format=text", "--limit=1", "--limit=20", before, before}, &repeated, io.Discard); err != nil {
		t.Fatal(err)
	}
	if implicit.String() != explicit.String() || implicit.String() != repeated.String() || !strings.Contains(implicit.String(), "tfli comparison report") {
		t.Fatalf("default, explicit and repeated text differ\ndefault:\n%s\nexplicit:\n%s\nrepeated:\n%s", implicit.String(), explicit.String(), repeated.String())
	}
}

func TestCompareValidationPrecedesInputAndOutputAccess(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"diagnose conflict", []string{"--compare", "--diagnose"}, "pass only one of --diagnose, --profile, --scrub, or --compare"},
		{"profile conflict", []string{"--compare", "--profile"}, "pass only one of --diagnose, --profile, --scrub, or --compare"},
		{"scrub conflict", []string{"--compare", "--scrub"}, "pass only one of --diagnose, --profile, --scrub, or --compare"},
		{"scrub values", []string{"--compare", "--scrub-values=values.txt"}, "--scrub-values applies only to --scrub"},
		{"negative limit", []string{"--compare", "--limit=-1"}, "--limit must be non-negative"},
		{"empty format", []string{"--compare", "--format="}, "--format must be text or json"},
		{"uppercase format", []string{"--compare", "--format=JSON"}, "--format must be text or json"},
		{"control format", []string{"--compare", "--format=json\x1b[31m"}, "--format must be text or json"},
		{"explicit default JSON limit", []string{"--compare", "--format=json", "--limit=20"}, "--limit is not supported with --format json"},
		{"repeated limit ending default", []string{"--compare", "--format=json", "--limit=1", "--limit=20"}, "--limit is not supported with --format json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "report")
			if err := os.WriteFile(output, []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string{}, tc.args...), "-o", output, "missing-before.log", "missing-after.log")
			var stdout, stderr bytes.Buffer
			err := run(args, &stdout, &stderr)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			got, readErr := os.ReadFile(output)
			if readErr != nil || string(got) != "keep\n" || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("validation touched output: %q, %v, %q, %q", got, readErr, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCompareFalseRetainsOneInputArity(t *testing.T) {
	var stderr bytes.Buffer
	err := run([]string{"--compare=false", "a.log", "b.log"}, io.Discard, &stderr)
	if err == nil || err.Error() != "expected exactly one log file argument" || strings.Count(stderr.String(), "Usage: tfli") != 1 {
		t.Fatalf("error=%v stderr=%q", err, stderr.String())
	}
}

func TestComparisonProtectsBothInputsFromOutputAliases(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, role := range []int{0, 1} {
			for _, alias := range []string{"same path", "hard link", "symbolic link"} {
				t.Run(format+"/role"+string(rune('0'+role))+"/"+alias, func(t *testing.T) {
					dir := t.TempDir()
					inputs := []string{filepath.Join(dir, "before.log"), filepath.Join(dir, "after.log")}
					original := [][]byte{[]byte("2026-09-08T00:00:00.000Z [INFO] before\n"), []byte("2026-09-08T00:00:00.000Z [INFO] after\n")}
					for i := range inputs {
						if err := os.WriteFile(inputs[i], original[i], 0o600); err != nil {
							t.Fatal(err)
						}
					}
					output := inputs[role]
					if alias != "same path" {
						output = filepath.Join(dir, "report")
						link := os.Link
						if alias == "symbolic link" {
							link = os.Symlink
						}
						if err := link(inputs[role], output); err != nil {
							t.Skipf("creating %s: %v", alias, err)
						}
					}
					beforeBytes := make([][]byte, 2)
					for i := range inputs {
						beforeBytes[i], _ = os.ReadFile(inputs[i])
					}
					err := run([]string{"--compare", "--format=" + format, "-o", output, inputs[0], inputs[1]}, io.Discard, io.Discard)
					if err == nil || !strings.Contains(err.Error(), "same file") {
						t.Fatalf("error = %v", err)
					}
					for i := range inputs {
						got, readErr := os.ReadFile(inputs[i])
						if readErr != nil || !bytes.Equal(got, beforeBytes[i]) {
							t.Fatalf("input %d changed: %q, %v", i, got, readErr)
						}
					}
				})
			}
		}
	}
}

func TestComparisonJSONIsOneDocumentAndOutputOnly(t *testing.T) {
	input := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	output := filepath.Join(t.TempDir(), "comparison.json")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--compare", "--format=json", "-o", output, input, input}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var document any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("decode after document = %v", err)
	}
	root := document.(map[string]any)
	for _, sectionValue := range root["sections"].([]any) {
		for _, rowValue := range sectionValue.(map[string]any)["rows"].([]any) {
			for metric, value := range rowValue.(map[string]any)["changes"].(map[string]any) {
				if value != nil && value.(float64) != 0 {
					t.Errorf("defined %s change = %v, want zero", metric, value)
				}
			}
		}
	}
	if bytes.Count(data, []byte("\"schema_version\"")) != 1 || !bytes.HasSuffix(data, []byte("\n")) || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("data=%q stdout=%q stderr=%q", data, stdout.String(), stderr.String())
	}
}

func TestComparisonOutputFailuresAreReturned(t *testing.T) {
	input := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	writeFailure := errors.New("comparison output failed")
	for _, tc := range []struct {
		name   string
		args   []string
		stdout io.Writer
		want   error
	}{
		{"text directory", []string{"--compare", "-o", t.TempDir(), input, input}, io.Discard, nil},
		{"JSON directory", []string{"--compare", "--format=json", "-o", t.TempDir(), input, input}, io.Discard, nil},
		{"text short stdout", []string{"--compare", input, input}, profileJSONShortWriter{}, io.ErrShortWrite},
		{"JSON short stdout", []string{"--compare", "--format=json", input, input}, profileJSONShortWriter{}, io.ErrShortWrite},
		{"text stdout error", []string{"--compare", input, input}, comparisonErrorWriter{err: writeFailure}, writeFailure},
		{"JSON stdout error", []string{"--compare", "--format=json", input, input}, comparisonErrorWriter{err: writeFailure}, writeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := run(tc.args, tc.stdout, &stderr)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if tc.want == nil && (err == nil || !strings.Contains(err.Error(), "creating ")) {
				t.Fatalf("directory error=%v", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr=%q, want empty", stderr.String())
			}
		})
	}
}

type comparisonErrorWriter struct{ err error }

func (w comparisonErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCompareLimitParseDiagnosticsAndShortCircuits(t *testing.T) {
	for _, value := range []string{"many", "999999999999999999999999999999999999999999999999999999"} {
		var stderr bytes.Buffer
		err := run([]string{"--compare", "--limit=" + value, "before.log", "after.log"}, io.Discard, &stderr)
		if err == nil || !strings.Contains(err.Error(), "invalid value") || strings.Count(stderr.String(), "invalid value") != 1 {
			t.Fatalf("value=%q error=%v stderr=%q", value, err, stderr.String())
		}
	}
	var help bytes.Buffer
	if err := run([]string{"--help", "--compare"}, io.Discard, &help); err != nil || !strings.Contains(help.String(), "tfli --compare") {
		t.Fatalf("help error=%v output=%q", err, help.String())
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--version", "--compare"}, &stdout, &stderr); err != nil || stdout.String() != "tfli "+version+"\n" || stderr.Len() != 0 {
		t.Fatalf("version error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestComparisonMissingInputNamesRoleAndPreservesOutput(t *testing.T) {
	existing := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	for _, tc := range []struct{ name, before, after, want string }{
		{"before", "missing-before.log", existing, "loading before:"},
		{"after", existing, "missing-after.log", "loading after:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "report")
			if err := os.WriteFile(output, []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := run([]string{"--compare", "-o", output, tc.before, tc.after}, io.Discard, io.Discard)
			got, readErr := os.ReadFile(output)
			if err == nil || !strings.Contains(err.Error(), tc.want) || readErr != nil || string(got) != "keep\n" {
				t.Fatalf("error=%v output=%q readErr=%v", err, got, readErr)
			}
		})
	}
}
