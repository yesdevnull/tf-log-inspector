package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// The structured-output fixture must report structured lines and, now that
// span.Sniffer counts completion-bearing UI-hook lines, select the
// ui-reported tier as usable rather than falling through to the
// no-tier-usable structured-output guidance -- and it must never disclose
// the fixture's resource addresses. Wiring span.UIHookBuilder itself into
// this CLI so spans are actually built is task 2's job.
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

// A bare invocation (no --diagnose or --profile) now opens the TUI rather
// than erroring. tea.NewProgram needs a terminal, so this cannot launch the
// program in a test; instead it checks that the default case reaches
// model.Load, by way of the load error a missing file surfaces. That error
// is model.Load's, not the old "--diagnose or --profile" usage message this
// test replaces.
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
