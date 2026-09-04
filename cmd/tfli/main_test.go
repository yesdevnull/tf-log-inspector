package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestRunDiagnoseOnFixture(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	for _, want := range []string{"tfli diagnostic report", "selected tier             reported", "spans built          2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// The mixed fixture's plan output must register as real non-hclog content,
// driven by the plan block rather than by the fixture's comment header.
func TestRunReportsNonHclogContent(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "mixed-hcp.log")}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	if strings.Contains(out, "untimestamped lines  0 ") || strings.Contains(out, "untimestamped lines  1 ") {
		t.Errorf("non-hclog content not measured:\n%s", out)
	}
	if !strings.Contains(out, "long output blocks   1") {
		t.Errorf("plan output block not detected:\n%s", out)
	}
}

// The structured-output fixture must report structured lines and select the
// ui-reported tier as usable -- span.Sniffer counts completion-bearing
// UI-hook lines, so the report has a usable tier rather than falling
// through to the no-tier-usable structured-output guidance -- and it must
// never disclose the fixture's resource addresses.
func TestRunReportsStructuredOutputLog(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "structured-ui.log")}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "structured lines     8") {
		t.Errorf("report missing structured line count:\n%s", out)
	}
	if !strings.Contains(out, "selected tier             ui-reported") {
		t.Errorf("report did not select the ui-reported tier:\n%s", out)
	}
	for _, leak := range []string{`module.module_name["key"].data.local_file.thing`, "aws_instance.example"} {
		if strings.Contains(out, leak) {
			t.Fatalf("report leaked the fixture's resource address %q:\n%s", leak, out)
		}
	}
}

func TestRunReportsMissingFileClearly(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", "no-such-file.log"}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
}

// A bare invocation (no --diagnose or --profile) opens the TUI rather than
// erroring. runProfile and runTUI are structurally identical up to a
// missing-file error (both call model.Load and return its error verbatim),
// so a test that only checks the error text cannot tell the two apart --
// it would pass unchanged if the default case dispatched to runProfile
// instead. Substituting runTUIFunc, the seam runTUI calls in place of
// tui.Run, is what actually distinguishes them: tui.Run needs a terminal
// and cannot run under go test, so this stub stands in for it.
func TestRunWithNoModeReachesTheTUIPath(t *testing.T) {
	original := runTUIFunc
	t.Cleanup(func() { runTUIFunc = original })

	var gotPath string
	runTUIFunc = func(l *model.Log, path string) error {
		gotPath = path
		return nil
	}

	var sb strings.Builder
	logPath := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	if err := run([]string{logPath}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotPath != logPath {
		t.Errorf("runTUIFunc called with path %q, want %q", gotPath, logPath)
	}
}

func TestRunWithNoModeOpensTheTUIPath(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"no-such-file.log"}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
	if strings.Contains(err.Error(), "--diagnose") {
		t.Errorf("bare invocation still routed to the old usage error: %v", err)
	}
}

func TestRunProfileOnFixture(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--profile", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(sb.String(), "BY RESOURCE TYPE") {
		t.Errorf("output missing BY RESOURCE TYPE:\n%s", sb.String())
	}
}

func TestRunRejectsDiagnoseAndProfileTogether(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", "--profile", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for --diagnose and --profile together")
	}
	for _, want := range []string{"--diagnose", "--profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

func TestRunWritesToOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.txt")
	var sb strings.Builder
	err := run([]string{"--diagnose", "-o", out, filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(data), "tfli diagnostic report") {
		t.Error("report file does not contain the report")
	}
}

// The -o file is the one artefact meant to leave the machine, so a failure
// to create it must be reported rather than silently falling back to
// standard output -- which would look like success while writing the report
// somewhere the user was not watching.
func TestRunReportsUnwritableOutputPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "no-such-dir", "report.txt")
	var sb strings.Builder
	err := run([]string{"--diagnose", "-o", out, filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for an uncreatable output file")
	}
	if !strings.Contains(err.Error(), out) {
		t.Errorf("error does not name the output path: %v", err)
	}
	if sb.Len() != 0 {
		t.Errorf("report was written to stdout after -o failed:\n%s", sb.String())
	}
}

func TestRunRejectsWrongArgumentCount(t *testing.T) {
	for name, args := range map[string][]string{
		"none": {"--diagnose"},
		"two":  {"--diagnose", "a.log", "b.log"},
	} {
		t.Run(name, func(t *testing.T) {
			var sb strings.Builder
			err := run(args, &sb, io.Discard)
			if err == nil {
				t.Fatal("run returned nil error for the wrong argument count")
			}
			if !strings.Contains(err.Error(), "exactly one log file") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunPrintsVersion(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--version"}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(sb.String(), "tfli ") {
		t.Errorf("version output = %q, want it to start with \"tfli \"", sb.String())
	}
}

func TestRunReportsUnknownFlag(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--no-such-flag", "a.log"}, &sb, io.Discard); err == nil {
		t.Fatal("run returned nil error for an unknown flag")
	}
}
